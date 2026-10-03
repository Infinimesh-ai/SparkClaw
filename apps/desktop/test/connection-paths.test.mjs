import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { promisify } from "node:util";
import test from "node:test";

import { resolveDesktopConnectionPaths } from "../src/main/connection-paths.mjs";

const execFileAsync = promisify(execFile);
const installer = path.resolve(import.meta.dirname, "../../../scripts/configure-desktop-connection.mjs");

test("packaged desktop finds provisioned backend without a launcher or environment overrides", async (t) => {
  const root = await fs.mkdtemp(path.join(await fs.realpath(os.tmpdir()), "sparkclaw-desktop-paths-"));
  t.after(() => fs.rm(root, { recursive: true, force: true }));
  const runtime = path.join(root, "runtime");
  const configHome = path.join(root, "config");
  await fs.mkdir(runtime, { mode: 0o700 });
  await fs.writeFile(path.join(runtime, "local-workbench.json"), "{}", { mode: 0o600 });
  await fs.writeFile(path.join(runtime, "desktop-client.json"), "{}", { mode: 0o600 });
  const env = { ...process.env, XDG_CONFIG_HOME: configHome };
  delete env.SPARKCLAW_DESKTOP_CONNECTION_FILE;
  delete env.SPARKCLAW_DESKTOP_CREDENTIAL_FILE;

  await execFileAsync(process.execPath, [installer, "--runtime-dir", runtime], { env });
  const paths = await resolveDesktopConnectionPaths({ env, home: root, packaged: true });
  assert.deepEqual(paths, {
    descriptorPath: path.join(runtime, "local-workbench.json"),
    credentialPath: path.join(runtime, "desktop-client.json"),
  });
  await execFileAsync(process.execPath, [installer, "--runtime-dir", runtime, "--check"], { env });

  await fs.chmod(path.join(configHome, "sparkclaw", "desktop-connection.json"), 0o644);
  await assert.rejects(resolveDesktopConnectionPaths({ env, home: root, packaged: true }), /owner-only/);
});

test("packaged desktop reads an existing legacy path configuration directly", async (t) => {
  const root = await fs.mkdtemp(path.join(await fs.realpath(os.tmpdir()), "sparkclaw-desktop-legacy-"));
  t.after(() => fs.rm(root, { recursive: true, force: true }));
  const configHome = path.join(root, "config");
  const configDir = path.join(configHome, "sparkclaw");
  await fs.mkdir(configDir, { recursive: true, mode: 0o700 });
  const descriptorPath = path.join(root, "local-workbench.json");
  const credentialPath = path.join(root, "desktop-client.json");
  await fs.writeFile(path.join(configDir, "desktop-launcher.conf"),
    `executable=/old/app\ndescriptor=${descriptorPath}\ncredential=${credentialPath}\n`, { mode: 0o600 });
  const env = { XDG_CONFIG_HOME: configHome };
  assert.deepEqual(await resolveDesktopConnectionPaths({ env, home: root, packaged: true }),
    { descriptorPath, credentialPath });
  await fs.writeFile(path.join(configDir, "desktop-connection.json"), "{}", { mode: 0o600 });
  await assert.rejects(resolveDesktopConnectionPaths({ env, home: root, packaged: true }), /configuration is invalid/);
});

test("explicit connection paths remain available for controlled launches", async () => {
  const paths = { descriptorPath: "/runtime/local-workbench.json", credentialPath: "/runtime/desktop-client.json" };
  assert.deepEqual(await resolveDesktopConnectionPaths({
    env: {
      SPARKCLAW_DESKTOP_CONNECTION_FILE: paths.descriptorPath,
      SPARKCLAW_DESKTOP_CREDENTIAL_FILE: paths.credentialPath,
    }, home: "/unused", packaged: true,
  }), paths);
  await assert.rejects(resolveDesktopConnectionPaths({
    env: { SPARKCLAW_DESKTOP_CONNECTION_FILE: paths.descriptorPath }, home: "/unused", packaged: true,
  }), /both desktop local-backend paths/i);
});
