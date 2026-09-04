import assert from "node:assert/strict";
import fs from "node:fs/promises";
import http from "node:http";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import { BrowserController } from "../src/controller.mjs";
import { createRequestHandler, startUnixServer } from "../src/http-server.mjs";

const token = "http-test-extension-token";

test("HTTP endpoints return redacted status and strict JSON errors", async (t) => {
  const controller = new BrowserController({
    profileID: "default",
    clientFactory: new FakeFactory(),
    controllerGeneration: 404,
  });
  const server = http.createServer(createRequestHandler(controller));
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  t.after(() => new Promise((resolve) => server.close(resolve)));
  const address = server.address();
  const base = `http://127.0.0.1:${address.port}`;

  const health = await fetch(`${base}/v1/health`);
  assert.equal(health.status, 200);
  assert.equal(health.headers.get("cache-control"), "no-store");
  assert.deepEqual(await health.json(), {
    schema_version: 1,
    state: "ready",
    profile_id: "default",
    controller_generation: 404,
    active_session: false,
    versions: { client: "fake" },
  });

  const checked = await fetch(`${base}/v1/validate-token`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ profile_id: "default", token }),
  });
  assert.equal(checked.status, 200);
  const checkedText = await checked.text();
  assert.equal(checkedText.includes(token), false);
  assert.equal(JSON.parse(checkedText).state, "ready");

  const invalid = await fetch(`${base}/v1/validate-token`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ profile_id: "default", token, unknown: true }),
  });
  assert.equal(invalid.status, 400);
  assert.deepEqual(await invalid.json(), {
    error: "browser controller request is invalid",
    code: "invalid_request",
    retryable: false,
  });

  const query = await fetch(`${base}/v1/health?token=${encodeURIComponent(token)}`);
  assert.equal(query.status, 400);
  assert.equal((await query.text()).includes(token), false);
});

test("Unix socket is owner-only and removed on shutdown", async () => {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), "sparkclaw-browser-socket-"));
  const socketPath = path.join(dir, "runtime", "controller.sock");
  const controller = new BrowserController({
    profileID: "default",
    clientFactory: new FakeFactory(),
  });
  const runtime = await startUnixServer({ socketPath, controller });

  assert.equal((await fs.stat(path.dirname(socketPath))).mode & 0o777, 0o700);
  assert.equal((await fs.stat(socketPath)).mode & 0o777, 0o600);
  await runtime.close();
  await assert.rejects(fs.stat(socketPath), (error) => error.code === "ENOENT");
});

class FakeFactory {
  info() {
    return { client: "fake" };
  }

  async open() {
    const closed = deferred();
    return {
      closed: closed.promise,
      async createTaskPage() {},
      async closeTaskPage() {},
      async close() { closed.resolve({ code: 0, signal: null }); },
    };
  }
}

function deferred() {
  let resolve;
  const promise = new Promise((done) => { resolve = done; });
  return { promise, resolve };
}
