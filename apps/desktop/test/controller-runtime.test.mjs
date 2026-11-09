import assert from "node:assert/strict";
import fs from "node:fs/promises";
import http from "node:http";
import path from "node:path";
import test from "node:test";
import { spawn } from "node:child_process";
import { ClientControllerRuntime, createControllerDirectory, controllerRequest } from "../src/browser/controller-runtime.mjs";
import { syntheticControllerBundle } from "./controller-bundle-fixture.mjs";

test("lazy Controller status verifies the bundle without starting a child or adapter", async t => {
  const root = await fs.mkdtemp(path.join(await fs.realpath("/tmp"), "sx-runtime-"));
  t.after(() => fs.rm(root, { recursive: true, force: true }));
  await syntheticControllerBundle(root);
  const runtime = new ClientControllerRuntime({ bundleRoot: root, adapterFactory: () => assert.fail("status started adapter") });
  assert.equal(runtime.snapshot().available, false);
  const status = await runtime.prepare();
  assert.equal(status.available, true);
  assert.equal(status.process_state, "stopped");
  assert.equal(runtime.child, null);
  await fs.appendFile(path.join(root, "src/client-main.mjs"), "tampered");
  await assert.rejects(runtime.start(), /integrity/);
  assert.equal(runtime.snapshot().available, false);
  await runtime.stop("qualification_cleanup");
  assert.equal(runtime.snapshot().available, false, "cleanup cannot resurrect a previously verified but now corrupt bundle");
  await assert.rejects(runtime.prepare(), /integrity/);
});

test("runtime directories reject shared permissions, symlinks and overlong Unix paths", async t => {
  const temporary = await fs.mkdtemp(path.join(await fs.realpath("/tmp"), "sx-runtime-"));
  t.after(() => fs.rm(temporary, { recursive: true, force: true }));
  const root = path.join(temporary, "root");
  const directory = await createControllerDirectory(root);
  assert.equal((await fs.stat(directory)).mode & 0o077, 0);
  await fs.chmod(root, 0o755);
  await assert.rejects(createControllerDirectory(root), /not private/);
  const link = path.join(temporary, "link");
  await fs.symlink(root, link);
  await assert.rejects(createControllerDirectory(link), /not private/);
  await assert.rejects(createControllerDirectory(path.join(temporary, "x".repeat(75))), /too long/);
});

test("failed runtime cleanup notifies only after permanent unavailability is visible", async t => {
  const root = await fs.mkdtemp(path.join(await fs.realpath("/tmp"), "sx-runtime-"));
  t.after(() => fs.rm(root, { recursive: true, force: true }));
  await syntheticControllerBundle(root);
  const observed = [];
  const runtime = new ClientControllerRuntime({ bundleRoot: root, adapterFactory: () => assert.fail("cleanup started an adapter"),
    onStopped: reason => observed.push({ reason, state: runtime.state, closed: runtime.closed,
      stopping: runtime.stopping, available: runtime.snapshot().available }) });
  await runtime.prepare();
  runtime.adapter = { revokeControllerTasks() {}, async close() { throw Error("cleanup denied"); } };
  await assert.rejects(runtime.stop("task_canceled"), /cleanup denied/);
  assert.deepEqual(observed, [{ reason: "task_canceled", state: "unavailable", closed: true, stopping: null, available: false }]);
  await assert.rejects(runtime.start(), /stopping/);
});

test("group exit polling waits through EPERM and still requires ESRCH before availability", async t => {
  const child = spawn(process.execPath, ["-e", "setInterval(() => {}, 1000)"], { detached: true, stdio: "ignore" });
  const exited = new Promise(resolve => child.once("exit", resolve));
  const kill = process.kill;
  let denied = false, absent = false;
  const observed = [];
  const runtime = new ClientControllerRuntime({ graceMS: 1000, onStopped: () => observed.push(runtime.snapshot()) });
  runtime.manifest = { versions: {} }; runtime.child = child; runtime.exit = exited;
  process.kill = function(pid, signal) {
    if (pid === -child.pid && signal === 0) {
      if (!denied) { denied = true; throw Object.assign(new Error("kill EPERM"), { code: "EPERM" }); }
      try { return kill.call(process, pid, signal); }
      catch (error) { if (error.code === "ESRCH") absent = true; throw error; }
    }
    return kill.call(process, pid, signal);
  };
  t.after(async () => {
    process.kill = kill;
    if (child.exitCode === null && child.signalCode === null) child.kill("SIGKILL");
    await exited;
  });
  await runtime.stop("authority_changed");
  assert.equal(denied, true); assert.equal(absent, true);
  assert.equal(runtime.child, null); assert.equal(runtime.closed, false);
  assert.equal(observed.length, 1); assert.equal(observed[0].process_state, "stopped"); assert.equal(observed[0].available, true);
});

test("persistent EPERM never proves group exit or restores availability", async t => {
  const child = spawn(process.execPath, ["-e", "setInterval(() => {}, 1000)"], { detached: true, stdio: "ignore" });
  const exited = new Promise(resolve => child.once("exit", resolve));
  const kill = process.kill;
  let probes = 0;
  const runtime = new ClientControllerRuntime({ graceMS: 0 });
  runtime.manifest = { versions: {} }; runtime.child = child; runtime.exit = exited;
  process.kill = function(pid, signal) {
    if (pid === -child.pid) { if (signal === 0) probes++; throw Object.assign(new Error("kill EPERM"), { code: "EPERM" }); }
    return kill.call(process, pid, signal);
  };
  t.after(async () => {
    process.kill = kill;
    if (child.exitCode === null && child.signalCode === null) child.kill("SIGKILL");
    await exited;
  });
  await assert.rejects(runtime.stop("authority_changed"), /Controller process cleanup is unproved/);
  assert.ok(probes > 1); assert.equal(runtime.closed, true); assert.equal(runtime.snapshot().available, false);
  assert.equal(runtime.child, child);
  assert.equal(child.exitCode, null); assert.equal(child.signalCode, null);
});

test("UDS client bounds total response time, bytes and request cancellation", async t => {
  const temporary = await fs.mkdtemp(path.join(await fs.realpath("/tmp"), "sx-runtime-"));
  const socketPath = path.join(temporary, "controller.sock");
  const server = http.createServer((request, response) => {
    if (request.url === "/v1/health") { response.end('{"state":"ready"}'); return; }
    if (request.url === "/v1/execute") { response.end(Buffer.alloc((10 << 20) + 1, 32)); return; }
    // acquire hangs to exercise cancellation before any response.
  });
  await new Promise(resolve => server.listen(socketPath, resolve));
  t.after(async () => { server.closeAllConnections(); await new Promise(resolve => server.close(resolve)); await fs.rm(temporary, { recursive: true, force: true }); });
  assert.deepEqual(await controllerRequest(socketPath, "health"), { status: 200, payload: { state: "ready" } });
  await assert.rejects(controllerRequest(socketPath, "execute", {}), /too large/);
  await assert.rejects(controllerRequest(socketPath, "acquire", {}, { timeoutMS: 20 }), /timed out/);
  const abort = new AbortController();
  const pending = controllerRequest(socketPath, "acquire", {}, { signal: abort.signal });
  abort.abort();
  await assert.rejects(pending, /abort/i);
  assert.throws(() => controllerRequest(socketPath, "execute", { text: "x".repeat(48 << 10) }), /too large/);
});
