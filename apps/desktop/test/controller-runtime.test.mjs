import assert from "node:assert/strict";
import fs from "node:fs/promises";
import http from "node:http";
import path from "node:path";
import test from "node:test";
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
