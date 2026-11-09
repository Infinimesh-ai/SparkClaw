import assert from "node:assert/strict";
import fs from "node:fs/promises";
import path from "node:path";
import { spawn } from "node:child_process";
import { fileURLToPath } from "node:url";
import test from "node:test";

async function fixture(t) {
  const root = await fs.mkdtemp(path.join(await fs.realpath("/tmp"), "sx-eof-"));
  await fs.chmod(root, 0o700);
  t.after(() => fs.rm(root, { recursive: true, force: true }));
  const directory = path.join(root, "runtime");
  await fs.mkdir(directory, { mode: 0o700 });
  await fs.writeFile(path.join(directory, "adapter-secret"), "fixture-only", { mode: 0o600 });
  return { root, directory };
}

function launch(t, directory, overrides = {}) {
  const child = spawn(process.execPath, [fileURLToPath(new URL("../src/client-main.mjs", import.meta.url))], {
    detached: true, stdio: ["pipe", "pipe", "pipe"], env: { ...process.env,
      SPARKCLAW_BROWSER_CONTROLLER_SOCKET: path.join(directory, "controller.sock"),
      SPARKCLAW_CLIENT_BROWSER_LAUNCHER: path.join(directory, "launcher"),
      SPARKCLAW_ELECTRON_ADAPTER_SOCKET: path.join(directory, "electron-adapter.sock"),
      SPARKCLAW_ELECTRON_ADAPTER_SECRET_FILE: path.join(directory, "adapter-secret"), ...overrides },
  });
  let stdout = "", stderr = "";
  child.stdout.on("data", value => { stdout += value; });
  child.stderr.on("data", value => { stderr += value; });
  const exited = new Promise((resolve, reject) => {
    child.once("error", reject); child.once("exit", (code, signal) => resolve({ code, signal }));
  });
  const timer = setTimeout(() => { try { process.kill(-child.pid, "SIGKILL"); } catch {} }, 8000);
  t.after(async () => {
    clearTimeout(timer);
    if (child.exitCode === null && child.signalCode === null) { try { process.kill(-child.pid, "SIGKILL"); } catch {} }
    await exited;
  });
  return { child, exited, stderr: () => stderr, async ready() {
    const deadline = performance.now() + 5000;
    while (!stdout.includes('"event":"client_controller_ready"')) {
      assert.ok(child.exitCode === null && child.signalCode === null, stderr);
      assert.ok(performance.now() < deadline, "Controller did not become ready");
      await new Promise(resolve => setTimeout(resolve, 10));
    }
  } };
}

test("parent pipe loss removes the exact private runtime, while SIGTERM leaves main-owned files", { timeout: 10000 }, async t => {
  for (const signal of ["parent-eof", "SIGTERM"]) {
    const { directory } = await fixture(t);
    const process = launch(t, directory);
    await process.ready();
    if (signal === "parent-eof") process.child.stdin.end();
    else process.child.kill(signal);
    assert.deepEqual(await process.exited, { code: 0, signal: null });
    if (signal === "parent-eof") await assert.rejects(fs.stat(directory), { code: "ENOENT" });
    else assert.equal(await fs.readFile(path.join(directory, "adapter-secret"), "utf8"), "fixture-only");
  }
});

test("parent-loss cleanup refuses a replaced runtime directory", { timeout: 10000 }, async t => {
  const { root, directory } = await fixture(t);
  const process = launch(t, directory);
  await process.ready();
  await fs.rename(directory, path.join(root, "original"));
  await fs.mkdir(directory, { mode: 0o700 });
  await fs.writeFile(path.join(directory, "unrelated"), "keep");
  process.child.stdin.end();
  assert.equal((await process.exited).code, 1);
  assert.equal(await fs.readFile(path.join(directory, "unrelated"), "utf8"), "keep");
});

test("client entry rejects shared, symlinked or split runtime directories without deleting them", { timeout: 10000 }, async t => {
  for (const kind of ["shared", "symlink", "split"]) {
    const { root, directory } = await fixture(t);
    let candidate = directory;
    if (kind === "shared") await fs.chmod(directory, 0o755);
    if (kind === "symlink") { candidate = path.join(root, "link"); await fs.symlink(directory, candidate); }
    const process = launch(t, candidate, kind === "split" ? { SPARKCLAW_ELECTRON_ADAPTER_SECRET_FILE: path.join(root, "adapter-secret") } : {});
    assert.equal((await process.exited).code, 1);
    assert.match(process.stderr(), /runtime directory is invalid/u);
    assert.equal(await fs.readFile(path.join(directory, "adapter-secret"), "utf8"), "fixture-only");
  }
});
