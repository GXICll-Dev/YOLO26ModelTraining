const assert = require("node:assert/strict");
const test = require("node:test");

const {
  bundledUninstallerPath,
  deployUninstaller,
  installedUninstallerPath,
  removeInstalledUninstaller
} = require("./uninstaller.cjs");

test("deploys a real Uninstall.exe beside Squirrel Update.exe", () => {
  const resourceRoot = "C:\\Users\\Tester\\AppData\\Local\\YOLO26ModelTraining\\app-0.3.3\\resources";
  const execPath = "C:\\Users\\Tester\\AppData\\Local\\YOLO26ModelTraining\\app-0.3.3\\YOLO26ModelTraining.exe";
  const copies = [];
  const fsModule = {
    existsSync(filePath) {
      return filePath === bundledUninstallerPath(resourceRoot);
    },
    copyFileSync(source, destination) {
      copies.push({ source, destination });
    }
  };

  assert.equal(deployUninstaller({ resourceRoot, execPath, fsModule }), true);
  assert.deepEqual(copies, [{
    source: bundledUninstallerPath(resourceRoot),
    destination: installedUninstallerPath(execPath)
  }]);
  assert.equal(copies[0].destination, "C:\\Users\\Tester\\AppData\\Local\\YOLO26ModelTraining\\Uninstall.exe");
});

test("does not deploy the uninstaller when the bundled executable is absent", () => {
  let copied = false;
  const result = deployUninstaller({
    resourceRoot: "C:\\missing\\resources",
    execPath: "C:\\app-0.3.3\\YOLO26ModelTraining.exe",
    fsModule: {
      existsSync() { return false; },
      copyFileSync() { copied = true; }
    }
  });

  assert.equal(result, false);
  assert.equal(copied, false);
});

test("removes the installed Uninstall.exe during Squirrel uninstall", () => {
  const removed = [];
  const execPath = "C:\\Users\\Tester\\AppData\\Local\\YOLO26ModelTraining\\app-0.3.3\\YOLO26ModelTraining.exe";
  const result = removeInstalledUninstaller({
    execPath,
    fsModule: {
      rmSync(filePath, options) { removed.push({ filePath, options }); }
    }
  });

  assert.equal(result, installedUninstallerPath(execPath));
  assert.deepEqual(removed, [{ filePath: result, options: { force: true } }]);
});
