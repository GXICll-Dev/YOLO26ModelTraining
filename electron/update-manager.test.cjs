const assert = require("node:assert/strict");
const crypto = require("node:crypto");
const { EventEmitter } = require("node:events");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const test = require("node:test");

const { UpdateManager, compareVersions, versionParts } = require("./update-manager.cjs");

test("parses semantic application versions", () => {
  assert.deepEqual(versionParts("v0.3.0"), { major: 0, minor: 3, patch: 0, suffix: "" });
  assert.equal(versionParts("broken"), null);
});

test("compares stable and prerelease versions", () => {
  assert.equal(compareVersions("0.3.1", "0.3.0"), 1);
  assert.equal(compareVersions("0.3.0", "0.3.0"), 0);
  assert.equal(compareVersions("0.3.0-beta.1", "0.3.0"), -1);
  assert.equal(compareVersions("1.0.0", "0.99.99"), 1);
});

test("stores downloaded application updates under the Squirrel application root", () => {
  const appRoot = path.join(process.cwd(), "YOLO26ModelTraining");
  const manager = new UpdateManager({ currentVersion: "0.3.5", fetch: global.fetch, appRoot });
  assert.equal(manager.storageRoot, path.join(appRoot, "updates"));
});

test("uses the configured resolver for installer downloads", async () => {
  const calls = [];
  const appRoot = fs.mkdtempSync(path.join(os.tmpdir(), "update-source-test-"));
  try {
    const body = Buffer.from("installer");
    const digest = crypto.createHash("sha256").update(body).digest("hex");
    const manager = new UpdateManager({
      currentVersion: "0.3.7",
      appRoot,
      resolveDownloadURL: (value) => `https://gh-proxy.org/${value}`,
      fetch: async (url) => {
        calls.push(String(url));
        if (String(url).includes("api.github.com")) {
          return new Response(JSON.stringify({
            tag_name: "v0.3.8",
            assets: [{ name: "YOLO26ModelTraining-Setup-0.3.8.exe", size: body.length, digest: `sha256:${digest}`, browser_download_url: "https://github.com/example/app.exe" }]
          }), { status: 200, headers: { "Content-Type": "application/json" } });
        }
        return new Response(body, { status: 200 });
      },
      quitAndInstall() {}
    });
    await manager.check();
    await manager.download();
    assert.equal(calls.at(-1), "https://gh-proxy.org/https://github.com/example/app.exe");
  } finally {
    fs.rmSync(appRoot, { recursive: true, force: true });
  }
});

test("starts the visible Squirrel installer and quits only after it spawns", async () => {
  const child = new EventEmitter();
  let spawnCall = null;
  let unrefCalled = false;
  let quitCalls = 0;
  child.unref = () => { unrefCalled = true; };

  const manager = new UpdateManager({
    currentVersion: "0.3.1",
    fetch: global.fetch,
    userDataRoot: process.cwd(),
    spawn(command, args, options) {
      spawnCall = { command, args, options };
      setImmediate(() => child.emit("spawn"));
      return child;
    },
    quitAndInstall() { quitCalls += 1; }
  });
  manager.installerPath = __filename;

  const state = await manager.install();

  assert.equal(spawnCall.command, __filename);
  assert.deepEqual(spawnCall.args, []);
  assert.deepEqual(spawnCall.options, { detached: true, stdio: "ignore", windowsHide: false });
  assert.equal(unrefCalled, true);
  assert.equal(quitCalls, 1);
  assert.equal(state.phase, "installing");
  assert.match(state.message, /自动重新打开/);
});

test("keeps the application open when the update installer cannot start", async () => {
  const child = new EventEmitter();
  let quitCalls = 0;
  const manager = new UpdateManager({
    currentVersion: "0.3.1",
    fetch: global.fetch,
    userDataRoot: process.cwd(),
    spawn() {
      setImmediate(() => child.emit("error", new Error("installer spawn failed")));
      return child;
    },
    quitAndInstall() { quitCalls += 1; }
  });
  manager.installerPath = __filename;

  await assert.rejects(manager.install(), /installer spawn failed/);
  assert.equal(quitCalls, 0);
  assert.equal(manager.snapshot().phase, "failed");
});
