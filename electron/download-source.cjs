const fs = require("fs");
const path = require("path");

const DOWNLOAD_SOURCE_ORIGINAL = "github";
const DOWNLOAD_SOURCE_PROXY = "proxy";
const DEFAULT_PROXY_BASE = "https://gh-proxy.org/";
const DOWNLOAD_SOURCE_FILE = "download-source.json";

function normalizeDownloadSource(value) {
  return String(value || "").trim().toLowerCase() === DOWNLOAD_SOURCE_PROXY
    ? DOWNLOAD_SOURCE_PROXY
    : DOWNLOAD_SOURCE_ORIGINAL;
}

function normalizeProxyBase(value = DEFAULT_PROXY_BASE) {
  const parsed = new URL(String(value || DEFAULT_PROXY_BASE));
  if (parsed.protocol !== "https:") throw new Error("下载加速地址必须使用 HTTPS。");
  return parsed.toString().replace(/\/+$/, "/");
}

function isGitHubDownloadURL(value) {
  try {
    const url = new URL(value);
    return url.protocol === "https:" && [
      "github.com",
      "www.github.com",
      "raw.githubusercontent.com",
      "objects.githubusercontent.com",
      "github-releases.githubusercontent.com"
    ].includes(url.hostname.toLowerCase());
  } catch {
    return false;
  }
}

function resolveDownloadURL(value, source = DOWNLOAD_SOURCE_ORIGINAL, proxyBase = DEFAULT_PROXY_BASE) {
  const url = String(value || "");
  if (normalizeDownloadSource(source) !== DOWNLOAD_SOURCE_PROXY || !isGitHubDownloadURL(url)) return url;
  const base = normalizeProxyBase(proxyBase);
  if (url.startsWith(base)) return url;
  return `${base}${url}`;
}

class DownloadSourceStore {
  constructor(options = {}) {
    this.stateRoot = options.stateRoot;
    this.filePath = path.join(this.stateRoot, DOWNLOAD_SOURCE_FILE);
    this.proxyBase = normalizeProxyBase(options.proxyBase || process.env.MT_GITHUB_PROXY_URL || DEFAULT_PROXY_BASE);
    this.source = DOWNLOAD_SOURCE_ORIGINAL;
    this.load();
  }

  load() {
    try {
      const payload = JSON.parse(fs.readFileSync(this.filePath, "utf8"));
      this.source = normalizeDownloadSource(payload.source);
    } catch {
      this.source = DOWNLOAD_SOURCE_ORIGINAL;
    }
    return this.snapshot();
  }

  snapshot() {
    return { source: this.source, proxyBase: this.proxyBase };
  }

  async setSource(value) {
    this.source = normalizeDownloadSource(value);
    await fs.promises.mkdir(this.stateRoot, { recursive: true });
    await fs.promises.writeFile(this.filePath, JSON.stringify({ source: this.source }, null, 2), "utf8");
    return this.snapshot();
  }

  resolve(value) {
    return resolveDownloadURL(value, this.source, this.proxyBase);
  }
}

module.exports = {
  DEFAULT_PROXY_BASE,
  DOWNLOAD_SOURCE_ORIGINAL,
  DOWNLOAD_SOURCE_PROXY,
  DownloadSourceStore,
  isGitHubDownloadURL,
  normalizeDownloadSource,
  normalizeProxyBase,
  resolveDownloadURL
};
