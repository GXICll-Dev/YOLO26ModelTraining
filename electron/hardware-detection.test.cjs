const assert = require("node:assert/strict");
const test = require("node:test");

const { detectNvidiaHardware } = require("./hardware-detection.cjs");

test("recommends CUDA when nvidia-smi reports a GPU", async () => {
  const result = await detectNvidiaHardware({
    execFile: (_command, _args, _options, callback) => callback(null, "NVIDIA GeForce RTX 4090\r\n")
  });
  assert.equal(result.hasNvidiaGPU, true);
  assert.equal(result.recommendedRuntime, "cuda");
  assert.deepEqual(result.gpuNames, ["NVIDIA GeForce RTX 4090"]);
});

test("recommends CPU when nvidia-smi is unavailable", async () => {
  const result = await detectNvidiaHardware({
    execFile: (_command, _args, _options, callback) => callback(new Error("not found"), "")
  });
  assert.equal(result.hasNvidiaGPU, false);
  assert.equal(result.recommendedRuntime, "cpu");
  assert.equal(result.recommendedDevice, "cpu");
});
