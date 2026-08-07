const { execFile } = require("child_process");

function runNvidiaSmi(executable = "nvidia-smi", options = {}) {
  const execFileImpl = options.execFile || execFile;
  return new Promise((resolve) => {
    execFileImpl(executable, ["--query-gpu=name", "--format=csv,noheader"], {
      windowsHide: true,
      timeout: options.timeoutMs || 5000,
      encoding: "utf8"
    }, (error, stdout) => {
      if (error) return resolve([]);
      const names = String(stdout || "").split(/\r?\n/).map((item) => item.trim()).filter(Boolean);
      resolve(names);
    });
  });
}

async function detectNvidiaHardware(options = {}) {
  const names = await runNvidiaSmi(options.executable || "nvidia-smi", options);
  const hasNvidiaGPU = names.length > 0;
  return {
    checked: true,
    hasNvidiaGPU,
    gpuNames: names,
    recommendedRuntime: hasNvidiaGPU ? "cuda" : "cpu",
    recommendedDevice: hasNvidiaGPU ? "auto" : "cpu"
  };
}

module.exports = { detectNvidiaHardware, runNvidiaSmi };
