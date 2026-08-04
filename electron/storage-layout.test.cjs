const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const test = require("node:test");

const {
  managedAppRoot,
  managedUserDataRoot,
  migrateLegacyStorage,
  removeLegacyStorage
} = require("./storage-layout.cjs");

test("uses the Squirrel root as the single packaged application directory", () => {
  const execPath = "C:\\Users\\Tester\\AppData\\Local\\YOLO26ModelTraining\\app-0.3.5\\YOLO26ModelTraining.exe";
  assert.equal(
    managedAppRoot({ isPackaged: true, execPath }),
    "C:\\Users\\Tester\\AppData\\Local\\YOLO26ModelTraining"
  );
});

test("migrates legacy runtime and Electron data into the application root", () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "storage-layout-test-"));
  const localAppData = path.join(root, "Local");
  const appRoot = path.join(localAppData, "YOLO26ModelTraining");
  const legacyData = path.join(localAppData, "YOLO26ModelTrainingData");
  const legacyUserData = path.join(root, "Roaming", "YOLO26ModelTraining");
  try {
    fs.mkdirSync(path.join(legacyData, "runtime", "runtime-id"), { recursive: true });
    fs.writeFileSync(path.join(legacyData, "runtime", "runtime-id", "installed-runtime.json"), "{}");
    fs.mkdirSync(path.join(legacyData, "downloads", "runtime"), { recursive: true });
    fs.writeFileSync(path.join(legacyData, "downloads", "runtime", "base.zip"), "zip");
    fs.mkdirSync(path.join(legacyUserData, "logs"), { recursive: true });
    fs.writeFileSync(path.join(legacyUserData, "logs", "backend.log"), "log");

    const errors = migrateLegacyStorage({ appRoot, localAppData, legacyUserDataRoot: legacyUserData });

    assert.deepEqual(errors, []);
    assert.equal(fs.existsSync(path.join(appRoot, "runtime", "runtime-id", "installed-runtime.json")), true);
    assert.equal(fs.existsSync(path.join(appRoot, "downloads", "runtime", "base.zip")), true);
    assert.equal(fs.existsSync(path.join(managedUserDataRoot(appRoot), "logs", "backend.log")), true);
    assert.equal(fs.existsSync(legacyData), false);
    assert.equal(fs.existsSync(legacyUserData), false);
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
});

test("uninstall cleanup removes legacy stores without touching the managed root", () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "storage-layout-cleanup-test-"));
  const localAppData = path.join(root, "Local");
  const appRoot = path.join(localAppData, "YOLO26ModelTraining");
  const legacyData = path.join(localAppData, "YOLO26ModelTrainingData");
  const legacyUserData = path.join(root, "Roaming", "YOLO26ModelTraining");
  try {
    fs.mkdirSync(appRoot, { recursive: true });
    fs.writeFileSync(path.join(appRoot, "Update.exe"), "app");
    fs.mkdirSync(legacyData, { recursive: true });
    fs.writeFileSync(path.join(legacyData, "runtime.zip"), "zip");
    fs.mkdirSync(legacyUserData, { recursive: true });
    fs.writeFileSync(path.join(legacyUserData, "state.json"), "{}");

    const result = removeLegacyStorage({ appRoot, localAppData, legacyUserDataRoot: legacyUserData });

    assert.deepEqual(result.errors, []);
    assert.equal(fs.existsSync(legacyData), false);
    assert.equal(fs.existsSync(legacyUserData), false);
    assert.equal(fs.existsSync(path.join(appRoot, "Update.exe")), true);
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
});
