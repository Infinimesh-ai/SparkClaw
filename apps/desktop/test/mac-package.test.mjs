import assert from "node:assert/strict";
import test from "node:test";
import fs from "node:fs/promises";
import path from "node:path";
import os from "node:os";
import { spawnSync } from "node:child_process";
import { createPackage } from "@electron/asar";
import auditPackage, { auditApplicationEntries, auditRuntimeEntries, requiredRuntimeEntries } from "../scripts/audit-package.mjs";

test("package audit rejects missing converter, parser and guardian runtime files", () => {
  for (const platform of ['darwin', 'linux']) {
    const entries = requiredRuntimeEntries(platform, 'arm64');
    auditRuntimeEntries(entries, platform, 'arm64');
    assert.throws(() => auditRuntimeEntries(['/package.json', '/src/main/main.mjs'], platform, 'arm64'), /missing a required runtime file/);
    for (const omitted of entries) assert.throws(() => auditRuntimeEntries(entries.filter(entry => entry !== omitted), platform, 'arm64'), /missing a required runtime file/);
  }
});

test("client package audit permits public sources and only locked desktop runtime dependencies", () => {
  auditApplicationEntries(["/package.json", "/src/main/main.mjs", "/src/main/secure-credential-store.mjs", "/src/assets/icon.png", "/bin/sparkclaw-electron-browser.mjs", "/node_modules/@sparkclaw/browser-bridge/src/relay-connection.mjs"]);
  auditApplicationEntries(["/node_modules/exceljs/lib/doc/workbook.js", "/node_modules/@deepseek-ai/libreoffice-kit-darwin-arm64/program/resource/data/file"]);
  for (const entry of ["/data/runtime/desktop-client.json", "/services/gateway/gateway", "/.env", "/src/private/key.pem", "/src/credential.sqlite", "/src/iscp-launch-profile.json", "/src/desktop-profile.json", "/node_modules/unknown-dependency/index.js", "/src/../data/secrets.json"]) {
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
  await fs.mkdir(path.join(resources, "local-preview", "assets"), { recursive: true });
  await fs.mkdir(path.join(resources, "local-preview", "wasm"), { recursive: true });
  await fs.writeFile(path.join(resources, "local-preview", "wasm", "LICENSE_QCMS"), "synthetic upstream notice");
  await fs.writeFile(path.join(resources, "local-preview", "index.html"), "<!doctype html>");
  await fs.writeFile(path.join(resources, "local-preview", "assets", "pdf.worker.mjs"), "// synthetic bundled PDF worker");
  await fs.writeFile(path.join(source, "package.json"), "{}");
  await fs.writeFile(path.join(source, "src", "main", "main.mjs"), "// synthetic public client");
  for (const entry of requiredRuntimeEntries('darwin', 'arm64')) {
    const filename = path.join(source, entry);
    await fs.mkdir(path.dirname(filename), { recursive: true });
    await fs.writeFile(filename, 'synthetic runtime fixture');
  }
  await fs.writeFile(path.join(resources, "webchat", "index.html"), "<!doctype html>");
  await fs.writeFile(path.join(resources, "webchat", "assets", "main.js"), "// synthetic built UI");
  await createPackage(source, path.join(resources, "app.asar"));
  const context = { electronPlatformName: "darwin", arch: 3, appOutDir: output, packager: { appInfo: { productFilename: "SparkX" } } };
  const plistPath = path.join(resources, "..", "Info.plist");
  await fs.writeFile(plistPath, "<plist><dict><key>CFBundleIconFile</key><string>electron.icns</string></dict></plist>");
  await assert.rejects(auditPackage(context), /SparkX application icon/);
  await fs.writeFile(plistPath, "<plist><dict><key>CFBundleIconFile</key><string>icon.icns</string></dict></plist>");
  await fs.writeFile(path.join(resources, "icon.icns"), "synthetic icon");
  await assert.rejects(auditPackage(context), /ENOENT/);
  const helperPath = path.join(resources, "iscp-workbench");
  await fs.writeFile(helperPath, "synthetic helper", { mode: 0o600 });
  await assert.rejects(auditPackage(context), /executable ISCP helper/);
  await fs.chmod(helperPath, 0o755);
  await auditPackage(context);
  const result = JSON.parse(await fs.readFile(path.join(resources, "client-package-audit.json")));
  assert.equal(result.platform, "darwin"); assert.equal(result.ui_files, 2);
  assert.match(result.iscp_helper_sha256, /^[a-f0-9]{64}$/u);
  await fs.writeFile(path.join(resources, "webchat", "backend.json"), "{}");
  await assert.rejects(auditPackage(context), /allowlist/);
});

test("Mac packaging entry points exist and Linux refuses to invoke any Mac build", async () => {
  const pkg = JSON.parse(await fs.readFile(new URL("../package.json", import.meta.url)));
  assert.equal(pkg.build.productName, "SparkX");
  assert.equal(pkg.build.appId, "ai.infinimesh.sparkclaw");
  assert.equal(pkg.name, "@sparkclaw/desktop");
  assert.equal(pkg.build.linux.artifactName, "SparkX-${version}-linux-${arch}.${ext}");
  assert.equal(pkg.build.deb.packageName, "sparkclaw");
  assert.ok(pkg.build.deb.depends.includes("bubblewrap"), "the Linux package must install its preview isolation runtime");
  assert.ok(pkg.build.deb.depends.includes("procps"), "the Linux package must install process-tree monitoring");
  assert.equal(pkg.build.mac.artifactName, "SparkX-${version}-mac-${arch}.${ext}");
  assert.equal(pkg.scripts["dist:mac-arm64"], "node scripts/package-macos.mjs --arch arm64");
  assert.equal(pkg.scripts["dist:mac-x64"], "node scripts/package-macos.mjs --arch x64");
  assert.equal(pkg.build.afterPack, "./scripts/audit-package.mjs");
  assert.equal(pkg.build.mac.hardenedRuntime, true);
  assert.deepEqual(pkg.build.mac.files.filter(value => !value.includes("libreoffice-kit-wasm")), pkg.build.files, "platform files must retain the public-source allowlist");
  assert.ok(pkg.build.mac.files.includes("!node_modules/@deepseek-ai/libreoffice-kit-wasm/**/*"));
  assert.ok(!pkg.build.files.some(value => value.includes("libreoffice-kit-wasm")), "Linux requires its bundled WASM engine");
  if (process.platform === "linux") {
    const result = spawnSync(process.execPath, [new URL("../scripts/package-macos.mjs", import.meta.url).pathname, "--arch", "arm64"], { encoding: "utf8" });
    assert.notEqual(result.status, 0);
    assert.match(result.stderr, /must run locally on macOS/);
    assert.equal(result.stdout, "");
  }
});
