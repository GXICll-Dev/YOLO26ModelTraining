const fs = require("fs");
const path = require("path");

const BUNDLED_UNINSTALLER_NAME = "YOLO26ModelTraining-Uninstall.exe";
const INSTALLED_UNINSTALLER_NAME = "Uninstall.exe";

function installedRoot(execPath = process.execPath) {
  return path.resolve(path.dirname(execPath), "..");
}

function bundledUninstallerPath(resourceRoot) {
  return path.join(resourceRoot, "bin", BUNDLED_UNINSTALLER_NAME);
}

function installedUninstallerPath(execPath = process.execPath) {
  return path.join(installedRoot(execPath), INSTALLED_UNINSTALLER_NAME);
}

function deployUninstaller(options = {}) {
  const fsModule = options.fsModule || fs;
  const source = bundledUninstallerPath(options.resourceRoot);
  const destination = installedUninstallerPath(options.execPath);
  if (!fsModule.existsSync(source)) return false;
  fsModule.copyFileSync(source, destination);
  return true;
}

function removeInstalledUninstaller(options = {}) {
  const fsModule = options.fsModule || fs;
  const destination = installedUninstallerPath(options.execPath);
  fsModule.rmSync(destination, { force: true });
  return destination;
}

module.exports = {
  BUNDLED_UNINSTALLER_NAME,
  INSTALLED_UNINSTALLER_NAME,
  bundledUninstallerPath,
  deployUninstaller,
  installedRoot,
  installedUninstallerPath,
  removeInstalledUninstaller
};
