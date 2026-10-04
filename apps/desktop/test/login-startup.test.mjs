import assert from "node:assert/strict";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { linuxLoginStartup } from "../src/main/login-startup.mjs";

test("Linux login startup writes a persistent launcher and can turn it off", async () => {
  const home = await fs.mkdtemp(path.join(os.tmpdir(), "sparkclaw-login-startup-"));
  try {
    const launcher = path.join(home, ".config", "autostart", "sparkclaw.desktop");
    assert.deepEqual(await linuxLoginStartup({ home, executable: "/opt/SparkX/SparkX.AppImage", enabled: undefined }), { supported: true, enabled: false });
    assert.deepEqual(await linuxLoginStartup({ home, executable: "/opt/SparkX/SparkX.AppImage", enabled: true }), { supported: true, enabled: true });
    const content = await fs.readFile(launcher, "utf8");
    assert.match(content, /^Name=SparkX$/m);
    assert.match(content, /^Exec="\/opt\/SparkX\/SparkX.AppImage"$/m);
    assert.equal((await fs.stat(launcher)).mode & 0o777, 0o600);
    assert.deepEqual(await linuxLoginStartup({ home, executable: "/opt/SparkX/SparkX.AppImage", enabled: false }), { supported: true, enabled: false });
  } finally {
    await fs.rm(home, { recursive: true, force: true });
  }
});
