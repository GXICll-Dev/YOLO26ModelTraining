const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const test = require("node:test");

const { RuntimeManager, normalizeManifest, runtimeFilesReady, safeRelativePath, safeRuntimeId } = require("./runtime-manager.cjs");

test("normalizes a versioned multi-package runtime manifest", () => {
  const manifest = normalizeManifest({
    schemaVersion: 1,
    runtimeId: "windows-x64-cuda126-py311",
    runtimeVersion: "1.0.0",
    packages: [
      { name: "base", fileName: "base.zip", url: "base.zip", size: 10, sha256: "a".repeat(64) },
      { name: "torch", fileName: "torch.zip", url: "https://example.test/torch.zip", size: 20, sha256: "b".repeat(64) }
    ],
    requiredFiles: [{ path: "python/python.exe", size: 5 }]
  }, "https://example.test/runtime/latest.json");

  assert.equal(manifest.downloadSize, 30);
  assert.equal(manifest.packages[0].url, "https://example.test/runtime/base.zip");
  assert.equal(manifest.requiredFiles[0].path, "python/python.exe");
});

test("rejects unsafe runtime identifiers and paths", () => {
  assert.throws(() => safeRuntimeId("../runtime"), /不合法/);
  assert.throws(() => safeRelativePath("python/../evil.exe"), /不合法/);
  assert.throws(() => normalizeManifest({
    schemaVersion: 1,
    runtimeId: "runtime-ok",
    runtimeVersion: "1",
    packages: [{ fileName: "../bad.zip", size: 1, sha256: "a".repeat(64) }]
  }), /文件名不合法/);
});

test("checks the installed runtime using exact required file sizes", () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "runtime-manager-test-"));
  try {
    fs.mkdirSync(path.join(root, "python"), { recursive: true });
    fs.writeFileSync(path.join(root, "python", "python.exe"), "12345");
    assert.equal(runtimeFilesReady(root, [{ path: "python/python.exe", size: 5 }]), true);
    assert.equal(runtimeFilesReady(root, [{ path: "python/python.exe", size: 6 }]), false);
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
});

test("stores managed runtime under the application root and removes deployed archives", async () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "runtime-storage-test-"));
  try {
    const manager = new RuntimeManager({ appRoot: root, resourceRoot: root, fetch: global.fetch });
    assert.equal(manager.runtimeBase, path.join(root, "runtime"));
    assert.equal(manager.downloadRoot, path.join(root, "downloads", "runtime"));

    fs.mkdirSync(manager.downloadRoot, { recursive: true });
    fs.writeFileSync(path.join(manager.downloadRoot, "runtime-base.zip"), "archive");
    await manager.clearDownloadCache();

    assert.equal(fs.existsSync(path.join(root, "downloads")), false);
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
});
