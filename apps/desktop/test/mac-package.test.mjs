import assert from "node:assert/strict";
import test from "node:test";
import fs from "node:fs/promises";
import { spawnSync } from "node:child_process";
import { auditApplicationEntries } from "../scripts/audit-package.mjs";

test("client package audit permits only public sources and the owned bridge dependency", () => {
  auditApplicationEntries(["/package.json", "/src/main/main.mjs", "/src/main/secure-credential-store.mjs", "/src/assets/icon.png", "/bin/sparkclaw-electron-browser.mjs", "/node_modules/@sparkclaw/browser-bridge/src/relay-connection.mjs"]);
  for (const entry of ["/data/runtime/desktop-client.json", "/services/gateway/gateway", "/.env", "/src/private/key.pem", "/src/credential.sqlite", "/node_modules/unknown-dependency/index.js", "/src/../data/secrets.json"]) {
    assert.throws(() => auditApplicationEntries([entry]), /allowlist|unapproved/);
  }
});

test("Mac packaging entry points exist and Linux refuses to invoke any Mac build", async () => {
  const pkg = JSON.parse(await fs.readFile(new URL("../package.json", import.meta.url)));
  assert.equal(pkg.scripts["dist:mac-arm64"], "node scripts/package-macos.mjs --arch arm64");
  assert.equal(pkg.scripts["dist:mac-x64"], "node scripts/package-macos.mjs --arch x64");
  assert.equal(pkg.build.afterPack, "./scripts/audit-package.mjs");
  assert.equal(pkg.build.mac.hardenedRuntime, true);
  if (process.platform === "linux") {
    const result = spawnSync(process.execPath, [new URL("../scripts/package-macos.mjs", import.meta.url).pathname, "--arch", "arm64"], { encoding: "utf8" });
    assert.notEqual(result.status, 0);
    assert.match(result.stderr, /must run locally on macOS/);
    assert.equal(result.stdout, "");
  }
});
