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

  const leaseResponse = await fetch(`${base}/v1/acquire`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({
      profile_id: "default",
      lane: "mcp",
      task_id: "http-task",
      credential_generation: 1,
      token,
    }),
  });
  assert.equal(leaseResponse.status, 201);
  const lease = await leaseResponse.json();
  const execute = await fetch(`${base}/v1/execute`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({
      session_id: lease.session_id,
      controller_generation: lease.controller_generation,
      session_generation: lease.session_generation,
      page_generation: lease.page_generation,
      operation: "tabs.list",
      arguments: {},
    }),
  });
  assert.equal(execute.status, 200);
  assert.equal((await execute.json()).operation, "tabs.list");

  const release = await fetch(`${base}/v1/release`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({
      session_id: lease.session_id,
      controller_generation: lease.controller_generation,
      session_generation: lease.session_generation,
    }),
  });
  assert.equal(release.status, 200);

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

test("script routes accept bounded email bodies and return no sensitive values", async (t) => {
  const scriptFactory = new FakeScriptFactory();
  const controller = new BrowserController({
    profileID: "default",
    clientFactory: new FakeFactory(),
    scriptFactory,
    controllerGeneration: 505,
  });
  const server = http.createServer(createRequestHandler(controller));
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  t.after(() => new Promise((resolve) => server.close(resolve)));
  const address = server.address();
  const base = `http://127.0.0.1:${address.port}`;
  const body = "x".repeat(64 << 10);
  const request = {
    profile_id: "default",
    task_id: "email-task",
    credential_generation: 8,
    token,
    provider: "gmail",
    operation: "send",
    script_id: "gmail.send",
    revision: 1,
    input: {
      schema_version: 1,
      operation: "send",
      invocation_id: "send-1",
      provider: "gmail",
      account: "default",
      message: {
        recipient: "person@example.test",
        subject: "subject",
        body: { format: "text", content: body },
      },
    },
  };

  const response = await fetch(`${base}/v1/run-script`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify(request),
  });
  assert.equal(response.status, 200);
  const responseText = await response.text();
  assert.equal(responseText.includes(token), false);
  assert.equal(responseText.includes(body), false);
  const result = JSON.parse(responseText);
  assert.equal(result.state, "completed");
  assert.equal(result.source_checksum, `sha256:${"b".repeat(64)}`);
  assert.equal(scriptFactory.bodyBytes, Buffer.byteLength(body));

  const opened = await fetch(`${base}/v1/open-provider-login`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({
      profile_id: "default",
      task_id: "login-task",
      provider: "gmail",
    }),
  });
  assert.equal(opened.status, 200);
  assert.equal((await opened.json()).state, "opened");
  assert.deepEqual(scriptFactory.loginProviders, ["gmail"]);
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
      async execute(operation, args) { return { operation, arguments: args }; },
      async close() { closed.resolve({ code: 0, signal: null }); },
    };
  }
}

class FakeScriptFactory {
  constructor() {
    this.bodyBytes = 0;
    this.loginProviders = [];
  }

  info() {
    return { cli: "fake" };
  }

  async runScript({ input }) {
    this.bodyBytes = Buffer.byteLength(input.message.body.content, "utf8");
    return {
      state: "completed",
      sourceChecksum: `sha256:${"b".repeat(64)}`,
      result: { schema_version: 1, status: "sent", provider: "gmail" },
    };
  }

  async openProviderLogin(provider) {
    this.loginProviders.push(provider);
  }
}

function deferred() {
  let resolve;
  const promise = new Promise((done) => { resolve = done; });
  return { promise, resolve };
}
