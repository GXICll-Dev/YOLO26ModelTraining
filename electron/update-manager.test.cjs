const assert = require("node:assert/strict");
const test = require("node:test");

const { compareVersions, versionParts } = require("./update-manager.cjs");

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
