const { app, BrowserWindow, dialog, globalShortcut, ipcMain, net: electronNet, session } = require("electron");
const { spawn } = require("child_process");
const fs = require("fs");
const http = require("http");
const net = require("net");
const path = require("path");
const { RuntimeManager } = require("./runtime-manager.cjs");
const {
  managedAppRoot,
  managedUserDataRoot,
  migrateLegacyStorage,
  removeLegacyStorage
} = require("./storage-layout.cjs");
const { deployUninstaller, removeInstalledUninstaller } = require("./uninstaller.cjs");
const { UpdateManager } = require("./update-manager.cjs");

const squirrelCommand = process.argv[1] || "";
const legacyElectronUserDataRoot = app.getPath("userData");
const applicationRoot = managedAppRoot({
  isPackaged: app.isPackaged,
  execPath: process.execPath,
  localAppData: process.env.LOCALAPPDATA,
  userDataRoot: legacyElectronUserDataRoot
});

if (process.platform === "win32" && app.isPackaged) {
  try {
    if (squirrelCommand === "--squirrel-uninstall") {
      const cleanup = removeLegacyStorage({
        appRoot: applicationRoot,
        localAppData: process.env.LOCALAPPDATA,
        legacyUserDataRoot: legacyElectronUserDataRoot
      });
      for (const failure of cleanup.errors) {
        console.error(`Could not remove legacy application data ${failure.path}: ${failure.error}`);
      }
      removeInstalledUninstaller({ execPath: process.execPath });
    } else {
      if (squirrelCommand !== "--squirrel-obsolete") {
        const migrationErrors = migrateLegacyStorage({
          appRoot: applicationRoot,
          localAppData: process.env.LOCALAPPDATA,
          legacyUserDataRoot: legacyElectronUserDataRoot
        });
        for (const failure of migrationErrors) {
          console.error(`Could not migrate application data ${failure.path}: ${failure.error}`);
        }
      }
      const userDataRoot = managedUserDataRoot(applicationRoot);
      fs.mkdirSync(userDataRoot, { recursive: true });
      app.setPath("userData", userDataRoot);
      const sessionDataRoot = path.join(userDataRoot, "session-data");
      const crashDumpsRoot = path.join(userDataRoot, "crash-dumps");
      const logsRoot = path.join(userDataRoot, "logs");
      fs.mkdirSync(sessionDataRoot, { recursive: true });
      fs.mkdirSync(crashDumpsRoot, { recursive: true });
      fs.mkdirSync(logsRoot, { recursive: true });
      app.setPath("sessionData", sessionDataRoot);
      app.setPath("crashDumps", crashDumpsRoot);
      app.setAppLogsPath(logsRoot);
      deployUninstaller({ resourceRoot: process.resourcesPath, execPath: process.execPath });
    }
  } catch (error) {
    console.error("Could not prepare managed application storage:", error);
  }
}
const squirrelStartup = require("electron-squirrel-startup");
if (squirrelStartup) {
  app.quit();
}

let backendProcess = null;
let mainWindow = null;
let shuttingDown = false;
let appURL = "";
let runtimeManager = null;
let updateManager = null;

function resourceRoot() {
  return app.isPackaged ? process.resourcesPath : path.resolve(__dirname, "..");
}

function serverExecutable(root) {
  const suffix = process.platform === "win32" ? ".exe" : "";
  return path.join(root, "bin", `modeltraining-server${suffix}`);
}

function isNonEmptyFile(filePath) {
  try {
    const info = fs.statSync(filePath);
    return info.isFile() && info.size > 0;
  } catch {
    return false;
  }
}

function deleteEnvironmentKeys(env, names) {
  const normalized = new Set(names.map((name) => name.toLowerCase()));
  for (const key of Object.keys(env)) {
    if (normalized.has(key.toLowerCase())) {
      delete env[key];
    }
  }
}

function firstBundledModel(root) {
  const modelDirs = [
    path.join(root, "models"),
    path.join(root, "runtime", "models"),
    path.join(root, "third_party"),
    path.join(root, "third_party", "ultralytics-8.4.10"),
    root
  ];
  const preferredNames = ["yolo26n.pt", "default.pt"];

  for (const dir of modelDirs) {
    for (const name of preferredNames) {
      const candidate = path.join(dir, name);
      if (isNonEmptyFile(candidate)) {
        return candidate;
      }
    }
  }

  for (const dir of modelDirs) {
    try {
      const candidate = fs.readdirSync(dir, { withFileTypes: true })
        .filter((entry) => entry.isFile() && path.extname(entry.name).toLowerCase() === ".pt")
        .map((entry) => path.join(dir, entry.name))
        .sort((left, right) => left.localeCompare(right))[0];
      if (candidate && isNonEmptyFile(candidate)) {
        return candidate;
      }
    } catch {
      // Optional resource directory is absent in some development layouts.
    }
  }
  return "";
}

function ensureLogStream(name) {
  const dir = path.join(app.getPath("userData"), "logs");
  fs.mkdirSync(dir, { recursive: true });
  return fs.createWriteStream(path.join(dir, name), { flags: "a" });
}

function findFreePort() {
  return new Promise((resolve, reject) => {
    const server = net.createServer();
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => {
      const address = server.address();
      const port = typeof address === "object" && address ? address.port : 8080;
      server.close(() => resolve(port));
    });
  });
}

function waitForHealth(url, timeoutMs = 30000) {
  const deadline = Date.now() + timeoutMs;
  return new Promise((resolve, reject) => {
    const poll = () => {
      const req = http.get(`${url}/api/health`, (res) => {
        res.resume();
        if (res.statusCode === 200) {
          resolve();
          return;
        }
        retry();
      });
      req.on("error", retry);
      req.setTimeout(1000, () => {
        req.destroy();
        retry();
      });
    };
    const retry = () => {
      if (Date.now() > deadline) {
        reject(new Error("Go backend did not become ready in time."));
        return;
      }
      setTimeout(poll, 400);
    };
    poll();
  });
}

function isLocalAppURL(value) {
  try {
    const parsed = new URL(value);
    return parsed.protocol === "http:" && parsed.hostname === "127.0.0.1";
  } catch {
    return false;
  }
}

function configurePermissions() {
  session.defaultSession.setPermissionRequestHandler((webContents, permission, callback, details) => {
    const isAppPage = webContents === mainWindow?.webContents && isLocalAppURL(details?.requestingUrl || webContents.getURL() || appURL);
    if (!isAppPage) {
      callback(false);
      return;
    }
    if (permission === "media" && details.mediaTypes?.includes("video") && !details.mediaTypes?.includes("audio")) {
      callback(true);
      return;
    }
    callback(false);
  });

  session.defaultSession.setPermissionCheckHandler((webContents, permission, requestingOrigin, details) => {
    const isAppPage = webContents === mainWindow?.webContents && isLocalAppURL(requestingOrigin || details?.requestingUrl || webContents.getURL() || appURL);
    if (!isAppPage) {
      return false;
    }
    return permission === "media" || permission === "camera";
  });
}

async function startBackend(runtimeRoot = "") {
  const root = resourceRoot();
  const executable = serverExecutable(root);
  if (!fs.existsSync(executable)) {
    throw new Error(`Backend executable not found: ${executable}`);
  }

  const port = await findFreePort();
  const address = `127.0.0.1:${port}`;
  const url = `http://${address}`;
  const userDataDir = app.getPath("userData");
  const stateDir = path.join(userDataDir, "state");
  const runtimeCacheDir = path.join(userDataDir, "runtime-cache");
  const ultralyticsConfigDir = path.join(stateDir, "ultralytics");
  const matplotlibConfigDir = path.join(runtimeCacheDir, "matplotlib");
  const torchCacheDir = path.join(runtimeCacheDir, "torch");
  fs.mkdirSync(stateDir, { recursive: true });
  fs.mkdirSync(runtimeCacheDir, { recursive: true });
  fs.mkdirSync(ultralyticsConfigDir, { recursive: true });
  fs.mkdirSync(matplotlibConfigDir, { recursive: true });
  fs.mkdirSync(torchCacheDir, { recursive: true });
  const env = {
    ...process.env,
    MT_ADDR: address,
    MT_BASE_DIR: root,
    MT_STATE_DIR: stateDir,
    MT_RUNTIME_ROOT: runtimeRoot,
    MT_RUNTIME_READY: runtimeRoot ? "1" : "0",
    MT_MANAGED_RUNTIME_REQUIRED: app.isPackaged ? "1" : "0",
    PYTHONUTF8: "1",
    PYTHONIOENCODING: "utf-8",
    PYTHONNOUSERSITE: "1",
    PYTHONDONTWRITEBYTECODE: "1",
    CUDA_MODULE_LOADING: "LAZY",
    YOLO_OFFLINE: "true",
    YOLO_AUTOINSTALL: "false",
    HF_HUB_OFFLINE: "1",
    HF_DATASETS_OFFLINE: "1",
    TRANSFORMERS_OFFLINE: "1",
    YOLO_CONFIG_DIR: ultralyticsConfigDir,
    MPLCONFIGDIR: matplotlibConfigDir,
    TORCH_HOME: torchCacheDir
  };

  // Packaged core builds deliberately start without Python. The renderer can
  // then download and atomically deploy the separately versioned runtime.
  const bundledPython = runtimeRoot ? path.join(runtimeRoot, "python", "python.exe") : "";
  if (runtimeRoot && !isNonEmptyFile(bundledPython)) {
    throw new Error(`已安装的运行环境缺少 Python：${bundledPython}`);
  }
  if (runtimeRoot && bundledPython) {
    const pythonDir = path.dirname(bundledPython);
    const requiredRuntimeFiles = [
      path.join(runtimeRoot, "runtime-manifest.json"),
      path.join(pythonDir, "MSVCP140.dll"),
      path.join(pythonDir, "CONCRT140.dll")
    ];
    const missingRuntimeFile = requiredRuntimeFiles.find((filePath) => !isNonEmptyFile(filePath));
    if (missingRuntimeFile) {
      throw new Error(`内置训练运行时不完整：${missingRuntimeFile}`);
    }
  }
  if (app.isPackaged || bundledPython) {
    // A release build must not be redirected into a machine-wide Python,
    // Conda environment, or Ultralytics source tree inherited from the host.
    deleteEnvironmentKeys(env, [
      "PYTHONHOME",
      "PYTHONPATH",
      "PYTHONSTARTUP",
      "PYTHONUSERBASE",
      "MT_PYTHON_CMD",
      "PYTHON_CMD",
      "YOLO_CMD",
      "YOLO_MODEL_PATH",
      "YOLO_MODEL_DIR",
      "ULTRALYTICS_DIR",
      "LABELME_PATH",
      "CONDA_PREFIX",
      "CONDA_DEFAULT_ENV",
      "VIRTUAL_ENV",
      "CUDA_VISIBLE_DEVICES"
    ]);
  }
  if (bundledPython) {
    const pythonDir = path.dirname(bundledPython);
    const pathVariable = Object.keys(env).find((name) => name.toLowerCase() === "path") || "PATH";
    env.MT_PYTHON_CMD = bundledPython;
    env[pathVariable] = [pythonDir, path.join(pythonDir, "Scripts"), env[pathVariable]]
      .filter(Boolean)
      .join(path.delimiter);
  }

  const bundledModel = runtimeRoot ? firstBundledModel(runtimeRoot) : "";
  if (bundledModel) {
    env.YOLO_MODEL_DIR = path.dirname(bundledModel);
    env.YOLO_MODEL_PATH = bundledModel;
  }

  const bundledUltralytics = runtimeRoot ? path.join(runtimeRoot, "third_party", "ultralytics-8.4.10") : "";
  if (fs.existsSync(bundledUltralytics)) {
    env.ULTRALYTICS_DIR = bundledUltralytics;
    if (!env.YOLO_MODEL_DIR) {
      env.YOLO_MODEL_DIR = bundledUltralytics;
    }
  }

  const bundledLabelMe = runtimeRoot ? path.join(runtimeRoot, "tools", "labelme", "labelme.exe") : "";
  if (fs.existsSync(bundledLabelMe)) {
    env.LABELME_PATH = bundledLabelMe;
  }

  const out = ensureLogStream("backend.out.log");
  const err = ensureLogStream("backend.err.log");
  backendProcess = spawn(executable, [], {
    cwd: root,
    env,
    windowsHide: true,
    stdio: ["ignore", "pipe", "pipe"]
  });
  backendProcess.stdout.pipe(out);
  backendProcess.stderr.pipe(err);
  backendProcess.once("exit", (code, signal) => {
    if (!shuttingDown && mainWindow) {
      dialog.showErrorBox("服务已退出", `Go 后端服务已退出。\ncode=${code ?? ""} signal=${signal ?? ""}`);
    }
  });

  await waitForHealth(url);
  return url;
}

function createWindow(url) {
  appURL = url;
  mainWindow = new BrowserWindow({
    width: 1300,
    height: 850,
    useContentSize: true,
    frame: false,
    minWidth: 1300,
    minHeight: 850,
    maxWidth: 1300,
    maxHeight: 850,
    resizable: false,
    maximizable: false,
    fullscreenable: false,
    title: "基于YOLO26的铝型方管缺陷智能分类检测系统",
    backgroundColor: "#efefef",
    webPreferences: {
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
      preload: path.join(__dirname, "preload.cjs")
    }
  });
  mainWindow.removeMenu();
  mainWindow.loadURL(url);
  mainWindow.once("closed", () => {
    mainWindow = null;
  });
}

function registerShortcuts() {
  globalShortcut.unregisterAll();
  globalShortcut.register("CommandOrControl+Space", () => {
    mainWindow?.webContents.send("layout-inspector:toggle");
  });
}

function stopBackend() {
  shuttingDown = true;
  if (backendProcess && !backendProcess.killed) {
    backendProcess.kill();
  }
  backendProcess = null;
}

function sendDesktopStatus(channel, payload) {
  if (mainWindow && !mainWindow.isDestroyed()) {
    mainWindow.webContents.send(channel, payload);
  }
}

if (!squirrelStartup) app.whenReady().then(async () => {
  try {
    if (app.isPackaged && process.platform === "win32") {
      try {
        const deployed = deployUninstaller({ resourceRoot: process.resourcesPath, execPath: process.execPath });
        if (!deployed) console.error("Could not deploy Uninstall.exe because the bundled uninstaller is missing.");
      } catch (error) {
        console.error("Could not deploy Uninstall.exe:", error);
      }
    }
    const fetcher = (url, init) => electronNet.fetch(url, init);
    runtimeManager = new RuntimeManager({
      resourceRoot: resourceRoot(),
      appRoot: applicationRoot,
      fetch: fetcher
    });
    runtimeManager.on("status", (status) => sendDesktopStatus("runtime:status", status));
    await runtimeManager.refresh({ fetchRemote: false });

    updateManager = new UpdateManager({
      currentVersion: app.getVersion(),
      appRoot: applicationRoot,
      fetch: fetcher,
      quitAndInstall: () => setTimeout(() => app.quit(), 500)
    });
    updateManager.on("status", (status) => sendDesktopStatus("update:status", status));

    const url = await startBackend(runtimeManager.readyRuntimeRoot());
    configurePermissions();
    createWindow(url);
    registerShortcuts();
    mainWindow.webContents.once("did-finish-load", () => {
      sendDesktopStatus("runtime:status", runtimeManager.snapshot());
      sendDesktopStatus("update:status", updateManager.snapshot());
      setTimeout(() => void runtimeManager.refresh({ fetchRemote: true }), 800);
      if (app.isPackaged) {
        setTimeout(() => void updateManager.check(), 1500);
      }
    });
  } catch (error) {
    dialog.showErrorBox("启动失败", error instanceof Error ? error.message : String(error));
    app.quit();
  }
});

app.on("before-quit", stopBackend);

app.on("will-quit", () => {
  globalShortcut.unregisterAll();
});

app.on("window-all-closed", () => {
  app.quit();
});

ipcMain.handle("window:minimize", (event) => {
  BrowserWindow.fromWebContents(event.sender)?.minimize();
});

ipcMain.handle("window:close", (event) => {
  BrowserWindow.fromWebContents(event.sender)?.close();
});

ipcMain.handle("app:get-version", () => app.getVersion());

ipcMain.handle("runtime:get-status", () => runtimeManager?.snapshot() ?? null);

ipcMain.handle("runtime:refresh", () => runtimeManager?.refresh({ fetchRemote: true }) ?? null);

ipcMain.handle("runtime:install", async () => {
  if (!runtimeManager) throw new Error("运行环境管理器尚未启动。");
  const status = await runtimeManager.install();
  setTimeout(() => {
    app.relaunch();
    app.exit(0);
  }, 1000);
  return status;
});

ipcMain.handle("runtime:cancel", () => runtimeManager?.cancel() ?? false);

ipcMain.handle("update:get-status", () => updateManager?.snapshot() ?? null);

ipcMain.handle("update:check", () => updateManager?.check() ?? null);

ipcMain.handle("update:download", () => {
  if (!updateManager) throw new Error("软件更新管理器尚未启动。");
  return updateManager.download();
});

ipcMain.handle("update:cancel", () => updateManager?.cancel() ?? false);

ipcMain.handle("update:install", () => {
  if (!updateManager) throw new Error("软件更新管理器尚未启动。");
  return updateManager.install();
});

ipcMain.handle("dialog:choose-directory", async (event) => {
  const owner = BrowserWindow.fromWebContents(event.sender);
  const result = await dialog.showOpenDialog(owner ?? mainWindow, {
    title: "选择项目根目录",
    properties: ["openDirectory", "createDirectory"]
  });
  if (result.canceled || !result.filePaths.length) {
    return "";
  }
  return result.filePaths[0];
});

ipcMain.handle("dialog:choose-model-file", async (event) => {
  const owner = BrowserWindow.fromWebContents(event.sender);
  const result = await dialog.showOpenDialog(owner ?? mainWindow, {
    title: "选择测试模型",
    properties: ["openFile"],
    filters: [
      { name: "PyTorch 权重", extensions: ["pt"] },
      { name: "所有文件", extensions: ["*"] }
    ]
  });
  if (result.canceled || !result.filePaths.length) {
    return "";
  }
  return result.filePaths[0];
});

ipcMain.handle("dialog:choose-image-files", async (event) => {
  const owner = BrowserWindow.fromWebContents(event.sender);
  const result = await dialog.showOpenDialog(owner ?? mainWindow, {
    title: "选择训练图片",
    properties: ["openFile", "multiSelections"],
    filters: [
      { name: "训练图片", extensions: ["jpg", "jpeg", "png", "bmp"] }
    ]
  });
  return result.canceled ? [] : result.filePaths;
});
