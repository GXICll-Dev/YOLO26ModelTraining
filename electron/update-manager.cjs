const { EventEmitter } = require("events");
const { spawn } = require("child_process");
const fs = require("fs");
const path = require("path");
const { Readable } = require("stream");
const { sha256File } = require("./runtime-manager.cjs");

const DEFAULT_RELEASE_API = "https://api.github.com/repos/GXICll-Dev/YOLO26ModelTraining/releases/latest";

function versionParts(value) {
  const match = String(value || "").trim().replace(/^v/i, "").match(/^(\d+)\.(\d+)\.(\d+)(?:[-+]([0-9A-Za-z.-]+))?$/);
  if (!match) return null;
  return {
    major: Number(match[1]),
    minor: Number(match[2]),
    patch: Number(match[3]),
    suffix: match[4] || ""
  };
}

function compareVersions(left, right) {
  const a = versionParts(left);
  const b = versionParts(right);
  if (!a || !b) return String(left).localeCompare(String(right), undefined, { numeric: true });
  for (const key of ["major", "minor", "patch"]) {
    if (a[key] !== b[key]) return a[key] > b[key] ? 1 : -1;
  }
  if (a.suffix === b.suffix) return 0;
  if (!a.suffix) return 1;
  if (!b.suffix) return -1;
  return a.suffix.localeCompare(b.suffix, undefined, { numeric: true });
}

function installerAsset(release) {
  const assets = Array.isArray(release?.assets) ? release.assets : [];
  return assets.find((item) => /YOLO26ModelTraining-Setup-.*\.exe$/i.test(item.name))
    || assets.find((item) => /Setup\.exe$/i.test(item.name));
}

function checksumAsset(release, installer) {
  const assets = Array.isArray(release?.assets) ? release.assets : [];
  return assets.find((item) => item.name === `${installer.name}.sha256`)
    || assets.find((item) => /checksums.*\.txt$/i.test(item.name));
}

async function responseText(fetcher, url, signal) {
  const response = await fetcher(url, {
    headers: { "Cache-Control": "no-cache", "User-Agent": "YOLO26ModelTraining-Updater" },
    signal
  });
  if (!response.ok) throw new Error(`更新服务器返回 HTTP ${response.status}。`);
  return response.text();
}

class UpdateManager extends EventEmitter {
  constructor(options) {
    super();
    this.currentVersion = options.currentVersion;
    this.fetch = options.fetch;
    this.spawn = options.spawn || spawn;
    this.releaseAPI = options.releaseAPI || process.env.MT_UPDATE_RELEASE_API || DEFAULT_RELEASE_API;
    const localAppData = options.localAppDataRoot || process.env.LOCALAPPDATA || options.userDataRoot;
    const appRoot = options.appRoot || path.join(localAppData, "YOLO26ModelTraining");
    this.storageRoot = path.join(appRoot, "updates");
    this.release = null;
    this.installer = null;
    this.checksum = "";
    this.abortController = null;
    this.activeDownload = null;
    this.quitAndInstall = options.quitAndInstall;
    this.state = {
      phase: "idle",
      currentVersion: this.currentVersion,
      latestVersion: this.currentVersion,
      updateAvailable: false,
      releaseName: "",
      releaseNotes: "",
      publishedAt: "",
      installerSize: 0,
      downloadedBytes: 0,
      percent: 0,
      message: "",
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

  async check() {
    this.emitState({ phase: "checking", message: "正在检查软件更新...", error: "" });
    try {
      const response = await this.fetch(this.releaseAPI, {
        headers: { "Accept": "application/vnd.github+json", "Cache-Control": "no-cache", "User-Agent": "YOLO26ModelTraining-Updater" }
      });
      if (response.status === 404) {
        return this.emitState({ phase: "idle", message: "尚未发布可用更新。", error: "" });
      }
      if (!response.ok) throw new Error(`GitHub 更新检查返回 HTTP ${response.status}。`);
      const release = await response.json();
      const latestVersion = String(release.tag_name || "").replace(/^v/i, "");
      const available = compareVersions(latestVersion, this.currentVersion) > 0;
      const installer = available ? installerAsset(release) : null;
      if (available && !installer) throw new Error(`版本 ${latestVersion} 缺少 Windows 安装包。`);

      this.release = release;
      this.installer = installer;
      this.checksum = installer?.digest?.startsWith("sha256:") ? installer.digest.slice(7).toLowerCase() : "";
      return this.emitState({
        phase: available ? "available" : "idle",
        latestVersion: latestVersion || this.currentVersion,
        updateAvailable: available,
        releaseName: String(release.name || release.tag_name || ""),
        releaseNotes: String(release.body || "本次版本未填写更新说明。"),
        publishedAt: String(release.published_at || ""),
        installerSize: Number(installer?.size) || 0,
        downloadedBytes: 0,
        percent: 0,
        message: available ? `发现新版本 v${latestVersion}。` : "当前已经是最新版本。",
        error: ""
      });
    } catch (error) {
      return this.emitState({
        phase: "failed",
        message: "软件更新检查失败。",
        error: error instanceof Error ? error.message : String(error)
      });
    }
  }

  cancel() {
    if (!this.abortController) return false;
    this.abortController.abort();
    return true;
  }

  async download() {
    if (this.activeDownload) return this.activeDownload;
    this.activeDownload = this.downloadInternal().finally(() => {
      this.abortController = null;
      this.activeDownload = null;
    });
    return this.activeDownload;
  }

  async downloadInternal() {
    if (!this.installer || !this.release) {
      await this.check();
      if (!this.installer || !this.state.updateAvailable) throw new Error("当前没有可下载的软件更新。 ");
    }
    await fs.promises.mkdir(this.storageRoot, { recursive: true });
    this.abortController = new AbortController();
    const signal = this.abortController.signal;
    const destination = path.join(this.storageRoot, path.basename(this.installer.name));
    const partPath = `${destination}.part`;
    let existing = 0;
    try {
      existing = fs.statSync(partPath).size;
    } catch {
      existing = 0;
    }
    if (existing > this.installer.size) {
      await fs.promises.rm(partPath, { force: true });
      existing = 0;
    }
    const headers = { "Accept": "application/octet-stream", "User-Agent": "YOLO26ModelTraining-Updater" };
    if (existing > 0) headers.Range = `bytes=${existing}-`;
    this.emitState({ phase: "downloading", downloadedBytes: existing, percent: Math.floor((existing / this.installer.size) * 100), message: "正在下载安装包...", error: "" });
    try {
      const response = await this.fetch(this.installer.browser_download_url, { headers, signal });
      if (!response.ok && response.status !== 206) throw new Error(`安装包下载返回 HTTP ${response.status}。`);
      if (existing > 0 && response.status !== 206) {
        await fs.promises.rm(partPath, { force: true });
        existing = 0;
      }
      const writer = fs.createWriteStream(partPath, { flags: existing > 0 ? "a" : "w" });
      let downloaded = existing;
      try {
        for await (const chunk of Readable.fromWeb(response.body)) {
          if (signal.aborted) throw new Error("软件更新下载已取消。");
          if (!writer.write(chunk)) await new Promise((resolve) => writer.once("drain", resolve));
          downloaded += chunk.length;
          this.emitState({ downloadedBytes: downloaded, percent: Math.min(99, Math.floor((downloaded / this.installer.size) * 100)) });
        }
        await new Promise((resolve, reject) => writer.end((error) => error ? reject(error) : resolve()));
      } catch (error) {
        writer.destroy();
        throw error;
      }
      if (fs.statSync(partPath).size !== this.installer.size) throw new Error("安装包下载不完整，可重新点击继续下载。");
      await fs.promises.rm(destination, { force: true });
      await fs.promises.rename(partPath, destination);

      this.emitState({ phase: "verifying", percent: 99, message: "正在校验安装包..." });
      let expected = this.checksum;
      if (!expected) {
        const asset = checksumAsset(this.release, this.installer);
        if (!asset) throw new Error("更新缺少 SHA-256 校验文件，已拒绝安装。 ");
        const text = await responseText(this.fetch, asset.browser_download_url, signal);
        const match = text.match(/[a-f0-9]{64}/i);
        if (!match) throw new Error("无法读取安装包 SHA-256。 ");
        expected = match[0].toLowerCase();
      }
      const actual = await sha256File(destination);
      if (actual !== expected) {
        await fs.promises.rm(destination, { force: true });
        throw new Error("安装包 SHA-256 校验失败，文件已删除。 ");
      }
      this.installerPath = destination;
      return this.emitState({ phase: "downloaded", downloadedBytes: this.installer.size, percent: 100, message: "更新已下载，可以重启安装。", error: "" });
    } catch (error) {
      const canceled = signal.aborted;
      this.emitState({
        phase: canceled ? "available" : "failed",
        message: canceled ? "软件更新下载已取消，可稍后继续。" : "软件下载或校验失败。",
        error: error instanceof Error ? error.message : String(error)
      });
      throw error;
    }
  }

  install() {
    if (!this.installerPath || !fs.existsSync(this.installerPath)) throw new Error("尚未下载可安装的更新。 ");
    return new Promise((resolve, reject) => {
      let child;
      let settled = false;
      const fail = (error) => {
        if (settled) return;
        settled = true;
        const message = error instanceof Error ? error.message : String(error);
        this.emitState({ phase: "failed", message: "无法启动更新安装程序。", error: message });
        reject(error instanceof Error ? error : new Error(message));
      };

      try {
        // Squirrel's --silent mode installs successfully but deliberately does
        // not launch the new application. The normal bootstrapper flow closes
        // the old version, installs the package, and starts the new version.
        child = this.spawn(this.installerPath, [], {
          detached: true,
          stdio: "ignore",
          windowsHide: false
        });
      } catch (error) {
        fail(error);
        return;
      }

      child.once("error", fail);
      child.once("spawn", () => {
        if (settled) return;
        settled = true;
        child.unref();
        const state = this.emitState({
          phase: "installing",
          message: "正在退出并安装更新，完成后将自动重新打开...",
          error: ""
        });
        this.quitAndInstall?.();
        resolve(state);
      });
    });
  }
}

module.exports = {
  DEFAULT_RELEASE_API,
  UpdateManager,
  compareVersions,
  versionParts
};
