import assert from "node:assert/strict";
import test from "node:test";
import fs from "node:fs/promises";
import path from "node:path";
import os from "node:os";
import { spawnSync } from "node:child_process";
import { createPackage } from "@electron/asar";
import auditPackage, { auditApplicationEntries } from "../scripts/audit-package.mjs";

test("client package audit permits only public sources and the owned bridge dependency", () => {
  auditApplicationEntries(["/package.json", "/src/main/main.mjs", "/src/main/secure-credential-store.mjs", "/src/assets/icon.png", "/bin/sparkclaw-electron-browser.mjs", "/node_modules/@sparkclaw/browser-bridge/src/relay-connection.mjs"]);
  for (const entry of ["/data/runtime/desktop-client.json", "/services/gateway/gateway", "/.env", "/src/private/key.pem", "/src/credential.sqlite", "/node_modules/unknown-dependency/index.js", "/src/../data/secrets.json"]) {
    assert.throws(() => auditApplicationEntries([entry]), /allowlist|unapproved/);
  }
});

test("afterPack audits a synthetic ASAR/resources tree without compiling any Mac binary", async (t) => {
  const directory = await fs.mkdtemp(path.join(os.tmpdir(), "sparkclaw-package-audit-"));
  t.after(() => fs.rm(directory, { recursive: true, force: true }));
  const source = path.join(directory, "source");
  const output = path.join(directory, "output");
  const resources = path.join(output, "SparkX.app", "Contents", "Resources");
  await fs.mkdir(path.join(source, "src", "main"), { recursive: true });
  await fs.mkdir(path.join(resources, "webchat", "assets"), { recursive: true });
  await fs.writeFile(path.join(source, "package.json"), "{}");
  await fs.writeFile(path.join(source, "src", "main", "main.mjs"), "// synthetic public client");
  await fs.writeFile(path.join(resources, "webchat", "index.html"), "<!doctype html>");
  await fs.writeFile(path.join(resources, "webchat", "assets", "main.js"), "// synthetic built UI");
  await createPackage(source, path.join(resources, "app.asar"));
  const context = { electronPlatformName: "darwin", appOutDir: output, packager: { appInfo: { productFilename: "SparkX" } } };
  const plistPath = path.join(resources, "..", "Info.plist");
  await fs.writeFile(plistPath, "<plist><dict><key>CFBundleIconFile</key><string>electron.icns</string></dict></plist>");
  await assert.rejects(auditPackage(context), /SparkX application icon/);
  await fs.writeFile(plistPath, "<plist><dict><key>CFBundleIconFile</key><string>icon.icns</string></dict></plist>");
  await fs.writeFile(path.join(resources, "icon.icns"), "synthetic icon");
  await auditPackage(context);
  const result = JSON.parse(await fs.readFile(path.join(resources, "client-package-audit.json")));
  assert.equal(result.platform, "darwin"); assert.equal(result.ui_files, 2);
  await fs.writeFile(path.join(resources, "webchat", "backend.json"), "{}");
  await assert.rejects(auditPackage(context), /allowlist/);
});

test("Mac packaging entry points exist and Linux refuses to invoke any Mac build", async () => {
  const pkg = JSON.parse(await fs.readFile(new URL("../package.json", import.meta.url)));
  assert.equal(pkg.build.productName, "SparkX");
  assert.equal(pkg.build.appId, "ai.infinimesh.sparkclaw");
  assert.equal(pkg.name, "@sparkclaw/desktop");
  assert.equal(pkg.build.linux.artifactName, "SparkX-${version}-linux-${arch}.${ext}");
  assert.equal(pkg.build.mac.artifactName, "SparkX-${version}-mac-${arch}.${ext}");
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
