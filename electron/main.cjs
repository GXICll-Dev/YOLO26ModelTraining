const { app, BrowserWindow, dialog, globalShortcut, ipcMain, session } = require("electron");
const { spawn } = require("child_process");
const fs = require("fs");
const http = require("http");
const net = require("net");
const path = require("path");

let backendProcess = null;
let mainWindow = null;
let shuttingDown = false;
let appURL = "";

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

async function startBackend() {
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

  const bundledPython = [
    path.join(root, "python", "python.exe"),
    path.join(root, "runtime", "python", "python.exe")
  ].find(isNonEmptyFile);
  if (app.isPackaged && !bundledPython) {
    throw new Error("内置 Python 运行时不完整，请重新解压完整程序包。");
  }
  if (app.isPackaged && bundledPython) {
    const pythonDir = path.dirname(bundledPython);
    const requiredRuntimeFiles = [
      path.join(root, "runtime-manifest.json"),
      path.join(pythonDir, "MSVCP140.dll"),
      path.join(pythonDir, "CONCRT140.dll")
    ];
    const missingRuntimeFile = requiredRuntimeFiles.find((filePath) => !isNonEmptyFile(filePath));
    if (missingRuntimeFile) {
      throw new Error(`内置训练运行时不完整：${missingRuntimeFile}`);
    }
  }
  if (bundledPython) {
    // A portable build must not be redirected into a machine-wide Python,
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
    const pythonDir = path.dirname(bundledPython);
    const pathVariable = Object.keys(env).find((name) => name.toLowerCase() === "path") || "PATH";
    env.MT_PYTHON_CMD = bundledPython;
    env[pathVariable] = [pythonDir, path.join(pythonDir, "Scripts"), env[pathVariable]]
      .filter(Boolean)
      .join(path.delimiter);
  }

  const bundledModel = firstBundledModel(root);
  if (app.isPackaged && !bundledModel) {
    throw new Error("内置 YOLO26 模型不完整，请重新解压完整程序包。");
  }
  if (bundledModel) {
    env.YOLO_MODEL_DIR = path.dirname(bundledModel);
    env.YOLO_MODEL_PATH = bundledModel;
  }

  const bundledUltralytics = path.join(root, "third_party", "ultralytics-8.4.10");
  if (fs.existsSync(bundledUltralytics)) {
    env.ULTRALYTICS_DIR = bundledUltralytics;
    if (!env.YOLO_MODEL_DIR) {
      env.YOLO_MODEL_DIR = bundledUltralytics;
    }
  }

  const bundledLabelMe = path.join(root, "tools", "labelme", "labelme.exe");
  if (app.isPackaged && !isNonEmptyFile(bundledLabelMe)) {
    throw new Error("内置 LabelMe 工具不完整，请重新解压完整程序包。");
  }
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

app.whenReady().then(async () => {
  try {
    const url = await startBackend();
    configurePermissions();
    createWindow(url);
    registerShortcuts();
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
