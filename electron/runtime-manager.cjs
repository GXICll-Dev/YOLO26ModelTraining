const { EventEmitter } = require("events");
const { spawn } = require("child_process");
const crypto = require("crypto");
const fs = require("fs");
const path = require("path");
const { Readable } = require("stream");
const extract = require("extract-zip");

const DEFAULT_RUNTIME_ID = "windows-x64-cuda126-py311";
const DEFAULT_MANIFEST_URL = "https://raw.githubusercontent.com/GXICll-Dev/YOLO26ModelTraining-Runtime/main/runtime/latest.json";
const INSTALL_MARKER = "installed-runtime.json";
const MINIMUM_FREE_BYTES = 10 * 1024 * 1024 * 1024;

function safeRuntimeId(value) {
  const normalized = String(value || "").trim();
  if (!/^[a-z0-9][a-z0-9._-]{2,80}$/i.test(normalized)) {
    throw new Error(`运行环境 ID 不合法: ${normalized || "<empty>"}`);
  }
  return normalized;
}

function safeRelativePath(value) {
  const normalized = String(value || "").replaceAll("\\", "/").replace(/^\/+/, "");
  if (!normalized || normalized.split("/").some((part) => !part || part === "." || part === "..")) {
    throw new Error(`运行环境文件路径不合法: ${value || "<empty>"}`);
  }
  return normalized;
}

function isNonEmptyFile(filePath, expectedSize = 0) {
  try {
    const info = fs.statSync(filePath);
    if (!info.isFile() || info.size <= 0) return false;
    return !expectedSize || info.size === expectedSize;
  } catch {
    return false;
  }
}

function readJSON(filePath) {
  try {
    return JSON.parse(fs.readFileSync(filePath, "utf8"));
  } catch {
    return null;
  }
}

function normalizeManifest(payload, manifestURL = DEFAULT_MANIFEST_URL) {
  if (!payload || payload.schemaVersion !== 1) {
    throw new Error("服务器返回的运行环境清单版本不受支持。");
  }
  const runtimeId = safeRuntimeId(payload.runtimeId);
  const runtimeVersion = String(payload.runtimeVersion || "").trim();
  if (!runtimeVersion) throw new Error("运行环境清单缺少 runtimeVersion。");
  if (!Array.isArray(payload.packages) || payload.packages.length === 0) {
    throw new Error("运行环境清单没有可下载的分包。");
  }
  const baseURL = new URL(manifestURL);
  const packages = payload.packages.map((item, index) => {
    const fileName = path.basename(String(item.fileName || "").trim());
    if (!fileName || fileName !== String(item.fileName || "").trim()) {
      throw new Error(`运行环境分包 ${index + 1} 的文件名不合法。`);
    }
    const size = Number(item.size);
    const sha256 = String(item.sha256 || "").trim().toLowerCase();
    if (!Number.isSafeInteger(size) || size <= 0) {
      throw new Error(`运行环境分包 ${fileName} 的大小不合法。`);
    }
    if (!/^[a-f0-9]{64}$/.test(sha256)) {
      throw new Error(`运行环境分包 ${fileName} 的 SHA-256 不合法。`);
    }
    return {
      name: String(item.name || fileName),
      fileName,
      url: new URL(String(item.url || fileName), baseURL).toString(),
      size,
      sha256
    };
  });
  const requiredFiles = Array.isArray(payload.requiredFiles)
    ? payload.requiredFiles.map((item) => ({
        path: safeRelativePath(item.path),
        size: Number(item.size) || 0,
        sha256: /^[a-f0-9]{64}$/i.test(String(item.sha256 || "")) ? String(item.sha256).toLowerCase() : ""
      }))
    : [];
  return {
    schemaVersion: 1,
    runtimeId,
    runtimeVersion,
    displayName: String(payload.displayName || "Python / PyTorch / CUDA 运行环境"),
    publishedAt: String(payload.publishedAt || ""),
    downloadSize: packages.reduce((total, item) => total + item.size, 0),
    installedSize: Number(payload.installedSize) || 0,
    packages,
    requiredFiles
  };
}

function runtimeFilesReady(root, requiredFiles) {
  if (!root) return false;
  const defaults = [
    { path: "python/python.exe", size: 0 },
    { path: "runtime-manifest.json", size: 0 },
    { path: "models/yolo26n.pt", size: 0 }
  ];
  const checks = requiredFiles?.length ? requiredFiles : defaults;
  return checks.every((item) => isNonEmptyFile(path.join(root, ...safeRelativePath(item.path).split("/")), item.size));
}

async function sha256File(filePath, onProgress) {
  const total = fs.statSync(filePath).size;
  let processed = 0;
  const hash = crypto.createHash("sha256");
  for await (const chunk of fs.createReadStream(filePath)) {
    hash.update(chunk);
    processed += chunk.length;
    onProgress?.(processed, total);
  }
  return hash.digest("hex");
}

async function removeDirectory(directory) {
  await fs.promises.rm(directory, { recursive: true, force: true, maxRetries: 3, retryDelay: 300 });
}

function waitForProcess(command, args, options = {}) {
  return new Promise((resolve, reject) => {
    const child = spawn(command, args, {
      cwd: options.cwd,
      env: options.env,
      windowsHide: true,
      stdio: ["ignore", "pipe", "pipe"]
    });
    const stdout = [];
    const stderr = [];
    child.stdout.on("data", (chunk) => stdout.push(chunk));
    child.stderr.on("data", (chunk) => stderr.push(chunk));
    let timedOut = false;
    const timer = setTimeout(() => {
      timedOut = true;
      child.kill();
    }, options.timeoutMs || 120000);
    child.once("error", (error) => {
      clearTimeout(timer);
      reject(error);
    });
    child.once("exit", (code, signal) => {
      clearTimeout(timer);
      const output = Buffer.concat(stdout).toString("utf8").trim();
      const errorOutput = Buffer.concat(stderr).toString("utf8").trim();
      if (timedOut) {
        reject(new Error("运行环境验证超时。"));
      } else if (code !== 0) {
        reject(new Error(errorOutput || output || `运行环境验证失败，code=${code ?? ""} signal=${signal ?? ""}`));
      } else {
        resolve({ stdout: output, stderr: errorOutput });
      }
    });
  });
}

async function probeRuntime(root) {
  const python = path.join(root, "python", "python.exe");
  const model = path.join(root, "models", "yolo26n.pt");
  const script = [
    "import json, os",
    "import cv2, torch, ultralytics",
    "from ultralytics import YOLO",
    "model = YOLO(os.environ['YOLO_MODEL_PATH'])",
    "x = torch.ones((1,), dtype=torch.float32)",
    "cuda_reported = bool(torch.cuda.is_available())",
    "cuda_ok = False",
    "cuda_error = ''",
    "if cuda_reported:",
    "    try:",
    "        y = x.to('cuda:0') * 2",
    "        cuda_ok = float(y.cpu()[0]) == 2.0",
    "    except Exception as exc:",
    "        cuda_error = str(exc)",
    "print(json.dumps({'python': os.sys.version.split()[0], 'torch': torch.__version__, 'ultralytics': ultralytics.__version__, 'cudaReported': cuda_reported, 'cudaOK': cuda_ok, 'cudaError': cuda_error}, ensure_ascii=False))"
  ].join("\n");
  const env = {
    ...process.env,
    PYTHONUTF8: "1",
    PYTHONIOENCODING: "utf-8",
    PYTHONNOUSERSITE: "1",
    PYTHONDONTWRITEBYTECODE: "1",
    YOLO_OFFLINE: "true",
    YOLO_AUTOINSTALL: "false",
    HF_HUB_OFFLINE: "1",
    TRANSFORMERS_OFFLINE: "1",
    YOLO_MODEL_PATH: model
  };
  const result = await waitForProcess(python, ["-I", "-c", script], { cwd: root, env, timeoutMs: 180000 });
  const lastLine = result.stdout.split(/\r?\n/).filter(Boolean).at(-1) || "";
  let details;
  try {
    details = JSON.parse(lastLine);
  } catch {
    throw new Error(`运行环境验证返回了无法识别的结果：${lastLine || "<empty>"}`);
  }
  return details;
}

class RuntimeManager extends EventEmitter {
  constructor(options) {
    super();
    this.resourceRoot = options.resourceRoot;
    this.fetch = options.fetch;
    this.manifestURL = options.manifestURL || process.env.MT_RUNTIME_MANIFEST_URL || DEFAULT_MANIFEST_URL;
    const localAppData = options.localAppDataRoot || process.env.LOCALAPPDATA || options.userDataRoot;
    this.storageRoot = options.appRoot || path.join(localAppData, "YOLO26ModelTraining");
    this.runtimeBase = path.join(this.storageRoot, "runtime");
    this.downloadBase = path.join(this.storageRoot, "downloads");
    this.downloadRoot = path.join(this.downloadBase, "runtime");
    this.manifest = null;
    this.abortController = null;
    this.activeInstall = null;
    this.state = {
      phase: "checking",
      ready: false,
      source: "",
      runtimeId: DEFAULT_RUNTIME_ID,
      localVersion: "",
      availableVersion: "",
      runtimeRoot: "",
      manifestURL: this.manifestURL,
      downloadedBytes: 0,
      totalBytes: 0,
      percent: 0,
      currentPackage: "",
      message: "正在检查运行环境...",
      error: ""
    };
  }

  snapshot() {
    return { ...this.state };
  }

  emitState(patch = {}) {
    this.state = { ...this.state, ...patch };
    this.emit("status", this.snapshot());
    return this.snapshot();
  }

  managedRoot(runtimeId = DEFAULT_RUNTIME_ID) {
    return path.join(this.runtimeBase, safeRuntimeId(runtimeId));
  }

  bundledCandidates() {
    return [
      this.resourceRoot,
      path.join(this.resourceRoot, "runtime")
    ];
  }

  findReadyRoot() {
    const managedRoot = this.managedRoot();
    const marker = readJSON(path.join(managedRoot, INSTALL_MARKER));
    if (marker?.runtimeId === DEFAULT_RUNTIME_ID && runtimeFilesReady(managedRoot, marker.requiredFiles)) {
      return { root: managedRoot, source: "managed", version: String(marker.runtimeVersion || "") };
    }
    for (const candidate of this.bundledCandidates()) {
      if (runtimeFilesReady(candidate)) {
        const manifest = readJSON(path.join(candidate, "runtime-manifest.json"));
        return {
          root: candidate,
          source: this.resourceRoot === candidate ? "bundled" : "development",
          version: String(manifest?.runtimeVersion || manifest?.runtimeId || "bundled")
        };
      }
    }
    return null;
  }

  readyRuntimeRoot() {
    return this.state.ready ? this.state.runtimeRoot : "";
  }

  async clearDownloadCache() {
    await removeDirectory(this.downloadRoot);
    try {
      await fs.promises.rmdir(this.downloadBase);
    } catch (error) {
      if (error.code !== "ENOENT" && error.code !== "ENOTEMPTY") throw error;
    }
  }

  async refresh(options = {}) {
    const local = this.findReadyRoot();
    if (local) {
      if (local.source === "managed") await this.clearDownloadCache();
      this.emitState({
        phase: "ready",
        ready: true,
        source: local.source,
        localVersion: local.version,
        runtimeRoot: local.root,
        percent: 100,
        message: "运行环境已就绪。",
        error: ""
      });
      if (!options.fetchRemote) return this.snapshot();
    } else {
      this.emitState({
        phase: "checking",
        ready: false,
        source: "",
        localVersion: "",
        runtimeRoot: "",
        percent: 0,
        message: "正在获取运行环境信息...",
        error: ""
      });
    }

    if (options.fetchRemote === false) {
      if (!local) this.emitState({ phase: "missing", message: "尚未安装运行环境。" });
      return this.snapshot();
    }

    try {
      this.manifest = await this.fetchManifest();
      const updateAvailable = Boolean(local && local.version && local.version !== this.manifest.runtimeVersion && local.source === "managed");
      return this.emitState({
        phase: local ? "ready" : "missing",
        ready: Boolean(local),
        runtimeId: this.manifest.runtimeId,
        availableVersion: this.manifest.runtimeVersion,
        totalBytes: this.manifest.downloadSize,
        updateAvailable,
        message: local
          ? updateAvailable ? `运行环境 ${this.manifest.runtimeVersion} 可更新。` : "运行环境已就绪。"
          : "尚未安装运行环境，请下载并自动部署。",
        error: ""
      });
    } catch (error) {
      return this.emitState({
        phase: local ? "ready" : "missing",
        ready: Boolean(local),
        message: local ? "运行环境已就绪，但无法检查服务器版本。" : "无法获取运行环境下载信息。",
        error: error instanceof Error ? error.message : String(error)
      });
    }
  }

  async fetchManifest() {
    if (typeof this.fetch !== "function") throw new Error("当前环境不支持网络下载。 ");
    const response = await this.fetch(this.manifestURL, {
      headers: { "Cache-Control": "no-cache", "User-Agent": "YOLO26ModelTraining-RuntimeManager" }
    });
    if (!response.ok) throw new Error(`运行环境服务器返回 HTTP ${response.status}。`);
    return normalizeManifest(await response.json(), this.manifestURL);
  }

  cancel() {
    if (!this.abortController) return false;
    this.abortController.abort();
    return true;
  }

  async install() {
    if (this.activeInstall) return this.activeInstall;
    this.activeInstall = this.installInternal().finally(() => {
      this.activeInstall = null;
      this.abortController = null;
    });
    return this.activeInstall;
  }

  async installInternal() {
    if (!this.manifest) this.manifest = await this.fetchManifest();
    const manifest = this.manifest;
    await fs.promises.mkdir(this.storageRoot, { recursive: true });
    const free = fs.statfsSync(this.storageRoot, { bigint: true });
    const availableBytes = Number(free.bavail * free.bsize);
    const requiredBytes = Math.max(MINIMUM_FREE_BYTES, manifest.downloadSize + manifest.installedSize + 1024 * 1024 * 1024);
    if (availableBytes < requiredBytes) {
      throw new Error(`磁盘空间不足。至少需要 ${Math.ceil(requiredBytes / 1024 / 1024 / 1024)} GiB 可用空间。`);
    }

    await fs.promises.mkdir(this.downloadRoot, { recursive: true });
    await fs.promises.mkdir(this.runtimeBase, { recursive: true });
    this.abortController = new AbortController();
    const signal = this.abortController.signal;
    let completedBytes = 0;
    this.emitState({
      phase: "downloading",
      ready: false,
      downloadedBytes: 0,
      totalBytes: manifest.downloadSize,
      percent: 0,
      currentPackage: "",
      message: "正在下载运行环境...",
      error: ""
    });

    const archives = [];
    try {
      for (const item of manifest.packages) {
        if (signal.aborted) throw new Error("运行环境下载已取消。");
        const destination = path.join(this.downloadRoot, item.fileName);
        await this.downloadPackage(item, destination, completedBytes, manifest.downloadSize, signal);
        archives.push(destination);
        completedBytes += item.size;
      }

      const staging = path.join(this.runtimeBase, `.staging-${manifest.runtimeId}-${Date.now()}`);
      await removeDirectory(staging);
      await fs.promises.mkdir(staging, { recursive: true });
      try {
        for (let index = 0; index < archives.length; index += 1) {
          if (signal.aborted) throw new Error("运行环境部署已取消。");
          const archive = archives[index];
          this.emitState({
            phase: "installing",
            currentPackage: path.basename(archive),
            percent: Math.round((index / archives.length) * 100),
            message: `正在解压部署 ${index + 1}/${archives.length}...`
          });
          await extract(archive, { dir: staging });
        }

        this.emitState({ phase: "validating", percent: 96, currentPackage: "", message: "正在验证 Python、PyTorch 和 YOLO..." });
        if (!runtimeFilesReady(staging, manifest.requiredFiles)) {
          throw new Error("部署后的运行环境缺少必要文件。");
        }
        const probe = await probeRuntime(staging);
        const marker = {
          schemaVersion: 1,
          runtimeId: manifest.runtimeId,
          runtimeVersion: manifest.runtimeVersion,
          installedAt: new Date().toISOString(),
          requiredFiles: manifest.requiredFiles,
          probe
        };
        await fs.promises.writeFile(path.join(staging, INSTALL_MARKER), JSON.stringify(marker, null, 2), "utf8");
        this.emitState({ phase: "installing", percent: 99, currentPackage: "", message: "正在清理运行环境安装分包..." });
        await this.clearDownloadCache();
        await this.activate(staging, this.managedRoot(manifest.runtimeId));
        return this.emitState({
          phase: "ready",
          ready: true,
          source: "managed",
          localVersion: manifest.runtimeVersion,
          runtimeRoot: this.managedRoot(manifest.runtimeId),
          downloadedBytes: manifest.downloadSize,
          totalBytes: manifest.downloadSize,
          percent: 100,
          currentPackage: "",
          updateAvailable: false,
          message: "运行环境部署完成，正在重启应用...",
          error: ""
        });
      } catch (error) {
        await removeDirectory(staging);
        throw error;
      }
    } catch (error) {
      const canceled = signal.aborted;
      const fallback = this.findReadyRoot();
      this.emitState({
        phase: fallback ? "ready" : canceled ? "missing" : "failed",
        ready: Boolean(fallback),
        source: fallback?.source || "",
        localVersion: fallback?.version || "",
        runtimeRoot: fallback?.root || "",
        currentPackage: "",
        message: fallback
          ? "新运行环境部署未完成，现有运行环境仍可继续使用。"
          : canceled ? "运行环境下载已取消，可稍后继续。" : "运行环境下载或部署失败。",
        error: error instanceof Error ? error.message : String(error)
      });
      throw error;
    }
  }

  async downloadPackage(item, destination, completedBytes, totalBytes, signal) {
    const partPath = `${destination}.part`;
    let existing = 0;
    try {
      existing = fs.statSync(partPath).size;
    } catch {
      existing = 0;
    }
    if (existing > item.size) {
      await fs.promises.rm(partPath, { force: true });
      existing = 0;
    }
    if (isNonEmptyFile(destination, item.size)) {
      const digest = await sha256File(destination);
      if (digest === item.sha256) return;
      await fs.promises.rm(destination, { force: true });
    }

    const headers = {
      "User-Agent": "YOLO26ModelTraining-RuntimeManager",
      "Accept": "application/octet-stream"
    };
    if (existing > 0) headers.Range = `bytes=${existing}-`;
    const response = await this.fetch(item.url, { headers, signal });
    if (response.status === 416 && existing === item.size) {
      await fs.promises.rename(partPath, destination);
    } else {
      if (!response.ok && response.status !== 206) {
        throw new Error(`下载 ${item.name} 失败，HTTP ${response.status}。`);
      }
      if (existing > 0 && response.status !== 206) {
        await fs.promises.rm(partPath, { force: true });
        existing = 0;
      }
      const writer = fs.createWriteStream(partPath, { flags: existing > 0 ? "a" : "w" });
      let current = existing;
      try {
        for await (const chunk of Readable.fromWeb(response.body)) {
          if (signal.aborted) throw new Error("运行环境下载已取消。");
          if (!writer.write(chunk)) await new Promise((resolve) => writer.once("drain", resolve));
          current += chunk.length;
          const downloadedBytes = completedBytes + current;
          this.emitState({
            phase: "downloading",
            currentPackage: item.name,
            downloadedBytes,
            totalBytes,
            percent: Math.min(95, Math.floor((downloadedBytes / totalBytes) * 95)),
            message: `正在下载 ${item.name}...`
          });
        }
        await new Promise((resolve, reject) => writer.end((error) => error ? reject(error) : resolve()));
      } catch (error) {
        writer.destroy();
        throw error;
      }
      if (!isNonEmptyFile(partPath, item.size)) {
        throw new Error(`${item.name} 下载大小不正确，可重新点击继续下载。`);
      }
      await fs.promises.rename(partPath, destination);
    }

    this.emitState({ phase: "verifying", currentPackage: item.name, message: `正在校验 ${item.name}...` });
    const digest = await sha256File(destination, (processed, size) => {
      const verifiedBytes = completedBytes + Math.round((processed / size) * item.size);
      this.emitState({
        downloadedBytes: completedBytes + item.size,
        percent: Math.min(95, Math.floor((verifiedBytes / totalBytes) * 95))
      });
    });
    if (digest !== item.sha256) {
      await fs.promises.rm(destination, { force: true });
      throw new Error(`${item.name} 校验失败，文件已删除，请重新下载。`);
    }
  }

  async activate(staging, finalRoot) {
    const backup = `${finalRoot}.backup-${Date.now()}`;
    let hadPrevious = false;
    try {
      await fs.promises.rename(finalRoot, backup);
      hadPrevious = true;
    } catch (error) {
      if (error.code !== "ENOENT") throw error;
    }
    try {
      await fs.promises.rename(staging, finalRoot);
      if (hadPrevious) await removeDirectory(backup);
    } catch (error) {
      if (hadPrevious && !fs.existsSync(finalRoot)) await fs.promises.rename(backup, finalRoot);
      throw error;
    }
  }
}

module.exports = {
  DEFAULT_MANIFEST_URL,
  DEFAULT_RUNTIME_ID,
  RuntimeManager,
  normalizeManifest,
  probeRuntime,
  runtimeFilesReady,
  safeRelativePath,
  safeRuntimeId,
  sha256File
};
