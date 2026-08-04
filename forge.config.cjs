const fs = require("fs");
const path = require("path");
const coreOnly = process.env.MT_CORE_ONLY === "1";

function removePythonBytecode(root) {
  if (!fs.existsSync(root)) {
    return;
  }

  const pending = [root];
  while (pending.length > 0) {
    const current = pending.pop();
    for (const entry of fs.readdirSync(current, { withFileTypes: true })) {
      const target = path.join(current, entry.name);
      if (entry.isDirectory()) {
        if (entry.name.toLowerCase() === "__pycache__") {
          fs.rmSync(target, { recursive: true, force: true });
        } else {
          pending.push(target);
        }
      } else if (entry.isFile() && /\.(?:pyc|pyo)$/i.test(entry.name)) {
        fs.rmSync(target, { force: true });
      }
    }
  }
}

module.exports = {
  // Squirrel's bundled rcedit cannot update a Setup.exe whose path contains
  // non-ASCII characters. The make wrapper can redirect Forge to an ASCII
  // staging directory and copy the verified artifacts back into ./out.
  outDir: process.env.MT_FORGE_OUT_DIR
    ? path.resolve(process.env.MT_FORGE_OUT_DIR)
    : path.resolve(__dirname, "out"),
  packagerConfig: {
    name: "YOLO26ModelTraining",
    executableName: "YOLO26ModelTraining",
    asar: true,
    win32metadata: {
     CompanyName: "ModelTraining",
      FileDescription: "YOLO26 Aluminum Profile Defect Detection System",
      ProductName: "YOLO26 Model Training"
    },
    electronZipDir: path.resolve(__dirname, "electron-cache"),
    extraResource: [
      path.resolve(__dirname, "bin"),
      path.resolve(__dirname, "web", "dist"),
      path.resolve(__dirname, "data"),
      ...(!coreOnly ? [
        path.resolve(__dirname, "runtime", "python"),
        path.resolve(__dirname, "runtime", "models"),
        path.resolve(__dirname, "runtime", "runtime-manifest.json"),
        path.resolve(__dirname, "tools")
      ] : [])
    ],
    ignore: [
      /^\/cmd($|\/)/,
      /^\/internal($|\/)/,
      /^\/scripts($|\/)/,
      /^\/runtime($|\/)/,
      /^\/third_party($|\/)/,
      /^\/tools($|\/)/,
      /^\/bin($|\/)/,
      /^\/web($|\/)/,
      /^\/data($|\/)/,
      /^\/state($|\/)/,
      /^\/output($|\/)/,
      /^\/out($|\/)/,
      /^\/artifacts($|\/)/,
      /^\/\.cache($|\/)/,
      /^\/electron-cache($|\/)/,
      /^\/\.playwright-cli($|\/)/,
      /^\/yolo26n\.pt$/,
      /(^|\/).*\.log$/
    ]
  },
  rebuildConfig: {},
  hooks: {
    postPackage: async (_forgeConfig, packageResult) => {
      const source = path.resolve(__dirname, "runtime", "python", "CONCRT140.dll");
      if (!coreOnly && !fs.existsSync(source)) {
        throw new Error(`Pinned app-local CONCRT140.dll is missing: ${source}`);
      }
      for (const outputPath of packageResult.outputPaths) {
        if (!coreOnly) {
          const target = path.join(outputPath, "resources", "tools", "labelme", "_internal", "CONCRT140.dll");
          fs.mkdirSync(path.dirname(target), { recursive: true });
          fs.copyFileSync(source, target);
        }
        removePythonBytecode(path.join(outputPath, "resources", "python"));
        removePythonBytecode(path.join(outputPath, "resources", "tools"));
      }
    }
  },
  makers: [
    {
      name: "@electron-forge/maker-zip",
      platforms: ["win32"]
    },
    ...(process.env.MT_ENABLE_SQUIRREL === "1" ? [{
      name: "@electron-forge/maker-squirrel",
      platforms: ["win32"],
      config: {
        name: "YOLO26ModelTraining",
        authors: "ModelTraining",
        description: "YOLO26 Aluminum Profile Defect Detection System"
      }
    }] : [])
  ]
};
