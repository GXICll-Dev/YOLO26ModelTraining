const fs = require("fs");
const path = require("path");

const APP_ROOT_NAME = "YOLO26ModelTraining";
const LEGACY_DATA_ROOT_NAME = "YOLO26ModelTrainingData";
const USER_DATA_DIR_NAME = "user-data";

function managedAppRoot(options = {}) {
  if (options.isPackaged && options.execPath) {
    return path.resolve(path.dirname(options.execPath), "..");
  }
  const base = options.localAppData || process.env.LOCALAPPDATA || options.userDataRoot;
  if (!base) throw new Error("无法定位应用数据目录。");
  return path.join(path.resolve(base), APP_ROOT_NAME);
}

function managedUserDataRoot(appRoot) {
  return path.join(path.resolve(appRoot), USER_DATA_DIR_NAME);
}

function legacyDataRoot(localAppData = process.env.LOCALAPPDATA) {
  if (!localAppData) return "";
  return path.join(path.resolve(localAppData), LEGACY_DATA_ROOT_NAME);
}

function isSameOrInside(candidate, parent) {
  if (!candidate || !parent) return false;
  const relative = path.relative(path.resolve(parent), path.resolve(candidate));
  return relative === "" || (!relative.startsWith("..") && !path.isAbsolute(relative));
}

function moveDirectoryContentsSync(source, destination, errors = []) {
  if (!source || !fs.existsSync(source) || !fs.statSync(source).isDirectory()) return errors;
  fs.mkdirSync(destination, { recursive: true });
  for (const entry of fs.readdirSync(source, { withFileTypes: true })) {
    const from = path.join(source, entry.name);
    const to = path.join(destination, entry.name);
    try {
      if (!fs.existsSync(to)) {
        try {
          fs.renameSync(from, to);
          continue;
        } catch {
          // Cross-volume moves and temporarily locked parent directories fall
          // back to a recursive merge below.
        }
      }

      if (entry.isDirectory()) {
        if (fs.existsSync(to) && !fs.statSync(to).isDirectory()) {
          fs.rmSync(from, { recursive: true, force: true, maxRetries: 3, retryDelay: 200 });
        } else {
          moveDirectoryContentsSync(from, to, errors);
        }
      } else if (!fs.existsSync(to)) {
        fs.copyFileSync(from, to);
        fs.rmSync(from, { force: true, maxRetries: 3, retryDelay: 200 });
      } else {
        // Prefer the file already stored in the new managed directory.
        fs.rmSync(from, { force: true, maxRetries: 3, retryDelay: 200 });
      }
    } catch (error) {
      errors.push({ path: from, error: error instanceof Error ? error.message : String(error) });
    }
  }
  try {
    fs.rmdirSync(source);
  } catch (error) {
    if (error?.code !== "ENOTEMPTY" && error?.code !== "ENOENT") {
      errors.push({ path: source, error: error instanceof Error ? error.message : String(error) });
    }
  }
  return errors;
}

function migrateLegacyStorage(options = {}) {
  const appRoot = path.resolve(options.appRoot);
  const targets = [
    { source: legacyDataRoot(options.localAppData), destination: appRoot },
    { source: options.legacyUserDataRoot ? path.resolve(options.legacyUserDataRoot) : "", destination: managedUserDataRoot(appRoot) }
  ];
  const errors = [];
  for (const target of targets) {
    if (!target.source || isSameOrInside(target.source, appRoot)) continue;
    moveDirectoryContentsSync(target.source, target.destination, errors);
  }
  return errors;
}

function removeLegacyStorage(options = {}) {
  const appRoot = path.resolve(options.appRoot);
  const candidates = [
    legacyDataRoot(options.localAppData),
    options.legacyUserDataRoot ? path.resolve(options.legacyUserDataRoot) : ""
  ];
  const removed = [];
  const errors = [];
  for (const candidate of new Set(candidates.filter(Boolean))) {
    if (isSameOrInside(candidate, appRoot)) continue;
    try {
      fs.rmSync(candidate, { recursive: true, force: true, maxRetries: 5, retryDelay: 300 });
      removed.push(candidate);
    } catch (error) {
      errors.push({ path: candidate, error: error instanceof Error ? error.message : String(error) });
    }
  }
  return { removed, errors };
}

module.exports = {
  APP_ROOT_NAME,
  LEGACY_DATA_ROOT_NAME,
  USER_DATA_DIR_NAME,
  isSameOrInside,
  legacyDataRoot,
  managedAppRoot,
  managedUserDataRoot,
  migrateLegacyStorage,
  removeLegacyStorage
};
