const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const test = require("node:test");

const { CPU_RUNTIME_ID, DEFAULT_RUNTIME_ID, RuntimeManager, extractRuntimeArchive, normalizeManifest, runtimeFilesReady, safeRelativePath, safeRuntimeId } = require("./runtime-manager.cjs");

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
    assert.equal(manager.downloadRoot, path.join(root, "downloads", "runtime", DEFAULT_RUNTIME_ID));

    fs.mkdirSync(manager.downloadRoot, { recursive: true });
    fs.writeFileSync(path.join(manager.downloadRoot, "runtime-base.zip"), "archive");
    await manager.clearDownloadCache();

    assert.equal(fs.existsSync(path.join(root, "downloads")), false);
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
});

test("selects the CPU manifest on a computer without NVIDIA hardware", async () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "runtime-cpu-profile-test-"));
  const requests = [];
  try {
    const manager = new RuntimeManager({
      appRoot: root,
      resourceRoot: root,
      hardwareDetector: async () => ({ checked: true, hasNvidiaGPU: false, gpuNames: [], recommendedRuntime: "cpu", recommendedDevice: "cpu" }),
      fetch: async (url) => {
        requests.push(String(url));
        return new Response(JSON.stringify({
          schemaVersion: 1,
          runtimeId: CPU_RUNTIME_ID,
          runtimeVersion: "1.0.0",
          packages: [{ fileName: "cpu.zip", size: 1, sha256: "a".repeat(64), url: "https://github.com/example/cpu.zip" }]
        }), { status: 200, headers: { "Content-Type": "application/json" } });
      }
    });
    const status = await manager.refresh({ fetchRemote: true });
    assert.equal(status.runtimeFlavor, "cpu");
    assert.equal(status.runtimeId, CPU_RUNTIME_ID);
    assert.equal(status.recommendedDevice, "cpu");
    assert.match(requests[0], /latest-cpu\.json$/);
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
});

test("uses the configured resolver for the runtime manifest", async () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "runtime-manifest-source-test-"));
  const requests = [];
  try {
    const manager = new RuntimeManager({
      appRoot: root,
      resourceRoot: root,
      resolveDownloadURL: (value) => `https://gh-proxy.org/${value}`,
      hardwareDetector: async () => ({ checked: true, hasNvidiaGPU: true, gpuNames: ["NVIDIA GPU"], recommendedRuntime: "cuda", recommendedDevice: "auto" }),
      fetch: async (url) => {
        requests.push(String(url));
        return new Response(JSON.stringify({
          schemaVersion: 1,
          runtimeId: DEFAULT_RUNTIME_ID,
          runtimeVersion: "1.0.0",
          packages: [{ fileName: "cuda.zip", size: 1, sha256: "a".repeat(64), url: "https://github.com/example/cuda.zip" }]
        }), { status: 200, headers: { "Content-Type": "application/json" } });
      }
    });
    await manager.refresh({ fetchRemote: true });
    assert.equal(requests[0], "https://gh-proxy.org/https://raw.githubusercontent.com/GXICll-Dev/YOLO26ModelTraining-Runtime/main/runtime/latest.json");
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
});

test("keeps an installed CUDA runtime while reporting an unavailable CPU feed", async () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "runtime-cpu-feed-missing-test-"));
  try {
    const cudaRoot = path.join(root, "runtime", DEFAULT_RUNTIME_ID);
    fs.mkdirSync(path.join(cudaRoot, "python"), { recursive: true });
    fs.mkdirSync(path.join(cudaRoot, "models"), { recursive: true });
    fs.writeFileSync(path.join(cudaRoot, "python", "python.exe"), "python");
    fs.writeFileSync(path.join(cudaRoot, "models", "yolo26n.pt"), "model");
    fs.writeFileSync(path.join(cudaRoot, "runtime-manifest.json"), "{}");
    fs.writeFileSync(path.join(cudaRoot, "installed-runtime.json"), JSON.stringify({
      runtimeId: DEFAULT_RUNTIME_ID,
      runtimeVersion: "1.0.0",
      requiredFiles: [
        { path: "python/python.exe", size: 6 },
        { path: "models/yolo26n.pt", size: 5 },
        { path: "runtime-manifest.json", size: 2 }
      ]
    }));
    const manager = new RuntimeManager({
      appRoot: root,
      resourceRoot: path.join(root, "bundled-missing"),
      hardwareDetector: async () => ({ checked: true, hasNvidiaGPU: false, gpuNames: [], recommendedRuntime: "cpu", recommendedDevice: "cpu" }),
      fetch: async () => new Response("not found", { status: 404 })
    });
    const status = await manager.refresh({ fetchRemote: true, flavor: "cpu" });
    assert.equal(status.ready, true);
    assert.equal(status.runtimeRoot, cudaRoot);
    assert.equal(status.runtimeFlavor, "cpu");
    assert.equal(status.runtimeId, CPU_RUNTIME_ID);
    assert.equal(status.switchAvailable, true);
    assert.equal(status.availableVersion, "");
    assert.match(status.error, /HTTP 404/);
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
});

test("persists an explicit runtime flavor selection", async () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "runtime-flavor-store-test-"));
  const stateRoot = path.join(root, "state");
  try {
    const responseFor = (url) => new Response(JSON.stringify({
      schemaVersion: 1,
      runtimeId: String(url).includes("latest-cpu") ? CPU_RUNTIME_ID : DEFAULT_RUNTIME_ID,
      runtimeVersion: "1.0.0",
      packages: [{ fileName: "runtime.zip", size: 1, sha256: "a".repeat(64), url: "https://github.com/example/runtime.zip" }]
    }), { status: 200, headers: { "Content-Type": "application/json" } });
    const options = {
      appRoot: root,
      stateRoot,
      resourceRoot: path.join(root, "bundled-missing"),
      hardwareDetector: async () => ({ checked: true, hasNvidiaGPU: true, gpuNames: ["NVIDIA GPU"], recommendedRuntime: "cuda", recommendedDevice: "auto" }),
      fetch: async (url) => responseFor(url)
    };
    const first = new RuntimeManager(options);
    await first.refresh({ fetchRemote: true, flavor: "cpu" });
    const restored = new RuntimeManager(options);
    const status = await restored.refresh({ fetchRemote: true });
    assert.equal(status.runtimeFlavor, "cpu");
    assert.equal(status.runtimeId, CPU_RUNTIME_ID);
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
});

test("removes incomplete staging directories without touching other runtime folders", async () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "runtime-staging-test-"));
  try {
    const manager = new RuntimeManager({ appRoot: root, resourceRoot: root, fetch: global.fetch });
    fs.mkdirSync(path.join(manager.runtimeBase, ".staging-windows-x64-cuda126-py311-old"), { recursive: true });
    fs.mkdirSync(path.join(manager.runtimeBase, "windows-x64-cuda126-py311"), { recursive: true });

    await manager.clearStagingDirectories("windows-x64-cuda126-py311");

    assert.equal(fs.existsSync(path.join(manager.runtimeBase, ".staging-windows-x64-cuda126-py311-old")), false);
    assert.equal(fs.existsSync(path.join(manager.runtimeBase, "windows-x64-cuda126-py311")), true);
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
});

test("extractRuntimeArchive rejects a missing archive", async () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "runtime-extract-test-"));
  try {
    await assert.rejects(
      extractRuntimeArchive(path.join(root, "missing.zip"), root, { timeoutMs: 5000 }),
      /missing|cannot|Could not|No such file/i
    );
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
});
