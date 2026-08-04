const assert = require("node:assert/strict");
const { EventEmitter } = require("node:events");
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
