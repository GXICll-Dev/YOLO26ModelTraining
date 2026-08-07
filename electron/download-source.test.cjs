const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const test = require("node:test");

const { DownloadSourceStore, normalizeDownloadSource, resolveDownloadURL } = require("./download-source.cjs");

test("normalizes download source values", () => {
  assert.equal(normalizeDownloadSource("proxy"), "proxy");
  assert.equal(normalizeDownloadSource("PROXY"), "proxy");
  assert.equal(normalizeDownloadSource("unknown"), "github");
});

test("prefixes only trusted GitHub download URLs", () => {
  const release = "https://github.com/example/project/releases/download/v1/file.zip";
  assert.equal(resolveDownloadURL(release, "proxy"), `https://gh-proxy.org/${release}`);
  assert.equal(resolveDownloadURL(release, "github"), release);
  assert.equal(resolveDownloadURL("https://example.com/file.zip", "proxy"), "https://example.com/file.zip");
});

test("persists the selected source", async () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "download-source-test-"));
  try {
    const store = new DownloadSourceStore({ stateRoot: root });
    assert.equal(store.snapshot().source, "github");
    await store.setSource("proxy");
    const restored = new DownloadSourceStore({ stateRoot: root });
    assert.equal(restored.snapshot().source, "proxy");
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
});
