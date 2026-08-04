const fs = require("fs");
const path = require("path");
const extract = require("extract-zip");
const { normalizeManifest, probeRuntime, runtimeFilesReady, sha256File } = require("./runtime-manager.cjs");

async function main() {
  const manifestPath = path.resolve(process.argv[2] || "artifacts/runtime-release/runtime-v1.0.0/runtime-release.json");
  const releaseDir = path.dirname(manifestPath);
  const manifest = normalizeManifest(JSON.parse(fs.readFileSync(manifestPath, "utf8")), `file:///${manifestPath.replaceAll("\\", "/")}`);
  const staging = path.join(releaseDir, `.verify-runtime-${process.pid}-${Date.now()}`);
  await fs.promises.mkdir(staging, { recursive: true });
  try {
    for (const item of manifest.packages) {
      const archive = path.join(releaseDir, item.fileName);
      const info = fs.statSync(archive);
      if (info.size !== item.size) throw new Error(`${item.fileName} size mismatch`);
      const digest = await sha256File(archive);
      if (digest !== item.sha256) throw new Error(`${item.fileName} SHA-256 mismatch`);
      process.stdout.write(`Extracting ${item.fileName}...\n`);
      await extract(archive, { dir: staging });
    }
    if (!runtimeFilesReady(staging, manifest.requiredFiles)) {
      throw new Error("Extracted runtime is missing a required file or has an unexpected size.");
    }
    for (const item of manifest.requiredFiles) {
      if (!item.sha256) continue;
      const filePath = path.join(staging, ...item.path.split("/"));
      const digest = await sha256File(filePath);
      if (digest !== item.sha256) throw new Error(`${item.path} SHA-256 mismatch after extraction`);
    }
    const probe = await probeRuntime(staging);
    process.stdout.write(`${JSON.stringify({ ok: true, runtimeVersion: manifest.runtimeVersion, probe }, null, 2)}\n`);
  } finally {
    await fs.promises.rm(staging, { recursive: true, force: true, maxRetries: 3, retryDelay: 300 });
  }
}

main().catch((error) => {
  process.stderr.write(`${error?.stack || error}\n`);
  process.exitCode = 1;
});
