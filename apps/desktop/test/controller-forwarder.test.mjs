import assert from "node:assert/strict";
import crypto from "node:crypto";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { BrowserControllerForwarder } from "../src/browser/controller-forwarder.mjs";
import { controllerDigest, iscpHostID, iscpAuthorizationDigest, CONTROLLER_OPERATIONS } from "../src/browser/controller-wire.mjs";

const scope = { owner_id: "owner", client_id: "client", installation_id: "installation" };
const manifest = () => ({ session_id: "session-a", authorization_revision: 7, expires_at: new Date(Date.now() + 60000).toISOString(),
  operations: CONTROLLER_OPERATIONS, limits: { body_bytes: 63488, response_bytes: 65536, concurrent: 4 } });
function binding(conversation = "A", session = manifest()) {
  return { ...scope, local_conversation_id: conversation, local_task_id: "task-" + conversation, host_id: iscpHostID(scope, session.session_id),
    runtime_generation: "iscp_" + session.session_id, connection_epoch: session.session_id, lease_id: "lease-" + conversation, page_id: "page-" + conversation,
    page_generation: 1, authorization_digest: iscpAuthorizationDigest(session.session_id, session.authorization_revision), lease_expires_at: "0001-01-01T00:00:00Z" };
}
function envelope(action, body) { return { type: "task.invoke", profile: "sparkclaw.workbench.transport.v2", id: crypto.randomUUID(), operation: `browser.controller.${action}`, installation_id: scope.installation_id, body }; }
function command(action, b, additional, { id = crypto.randomUUID(), timeout = 25000 } = {}) {
  const body = { schema_version: 1, command_id: id, digest: "", binding: b, timeout_ms: timeout, ...additional };
  body.digest = controllerDigest(`browser.controller.${action}`, body); return envelope(action, body);
}
async function fixture(t, { execute, acquire } = {}) {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), "sx-forwarder-"));
  t.after(() => fs.rm(root, { recursive: true, force: true }));
  const calls = [], lifecycle = [];
  const runtime = {
    state: "stopped", incarnation: "", controllerGeneration: 0,
    async prepare() {}, snapshot() { return { available: true, protocol: "browser.controller.v1", process_state: this.state, supervisor_incarnation: this.incarnation, limits: {} }; },
    async start() { if (!this.incarnation) { this.incarnation = crypto.randomBytes(16).toString("hex"); this.controllerGeneration++; } this.state = "ready"; return this; },
    async stop(reason) { lifecycle.push(reason); this.state = "stopped"; this.incarnation = ""; this.onStopped?.(reason); },
    adapter: { prepareControllerTask() { return { taskID: "local-fixed-task", token: "private-token" }; }, beginControllerRelease() {}, revokeControllerTask() {} },
    async request(action, body, options) {
      calls.push({ action, body: { ...body } });
      if (action === "acquire") {
        if (acquire) await acquire(body, options);
        return { status: 201, payload: { schema_version: 1, state: "acquired", profile_id: "client_embedded", lane: "mcp", session_id: "session_" + crypto.randomBytes(16).toString("hex"),
          credential_generation: 7, controller_generation: this.controllerGeneration, session_generation: 1, page_generation: 1, expires_at: new Date(Date.now() + body.session_ttl_ms).toISOString() } };
      }
      if (action === "release") return { status: 200, payload: { schema_version: 1, state: "released", profile_id: "client_embedded", controller_generation: body.controller_generation, session_generation: body.session_generation } };
      const result = execute ? await execute(body, options) : { text: "fixture" };
      return { status: 200, payload: { schema_version: 1, state: "completed", profile_id: "client_embedded", lane: "mcp", session_id: body.session_id,
        credential_generation: 7, controller_generation: body.controller_generation, session_generation: body.session_generation,
        page_generation: body.page_generation + (["page.navigate", "page.fill", "page.click", "tabs.close"].includes(body.operation) ? 1 : 0), operation: body.operation, result } };
    },
  };
  const auth = { descriptor: { transport: "iscp" }, status: { state: "connected", capabilities: { browser: true } },
    connection: { ownerID: "owner", clientID: "client" }, transport: { capabilities: manifest() },
    async invokeISCP(operation) { if (operation === "browser.receipt") return { fences: [] }; return { reconciled: true }; } };
  const forwarder = new BrowserControllerForwarder({ auth, runtime, registry: { closeHostPages() {} }, userDataDir: root });
  t.after(() => forwarder.stop());
  await forwarder.start(scope);
  const send = request => forwarder.handleReverse(request, auth.transport.capabilities);
  const acquireTask = async (b = binding(), options = {}) => {
    const request = command("acquire", b, { wait_timeout_ms: 0, session_ttl_ms: 600000 }, options);
    const response = await send(request); assert.equal(response.body?.outcome, "completed", JSON.stringify(response)); return response.body.payload;
  };
  return { forwarder, runtime, auth, calls, lifecycle, send, acquireTask };
}

test("status bootstraps without browser-ready UI or process startup, while mixed peers fail closed", async t => {
  const f = await fixture(t); f.auth.status.capabilities.browser = false;
  const status = await f.send(envelope("status", { schema_version: 1 }));
  assert.equal(status.body.process_state, "stopped"); assert.equal(f.calls.length, 0);
  assert.equal((await f.send(command("acquire", binding(), { wait_timeout_ms: 0, session_ttl_ms: 600000 }))).status, 503);
  f.auth.transport.capabilities.operations = CONTROLLER_OPERATIONS.slice(1);
  assert.equal((await f.send(envelope("status", { schema_version: 1 }))).status, 503);
});

test("concurrent duplicate mutations share one durable slot, preserve typed fields and never replay after cache loss", async t => {
  const f = await fixture(t); const b = binding(); let lease = await f.acquireTask(b);
  const begin = f.forwarder.journal.begin.bind(f.forwarder.journal);
  f.forwarder.journal.begin = async command => { await new Promise(resolve => setTimeout(resolve, 15)); return begin(command); };
  const request = command("execute", b, { lease, operation: "page.fill", arguments: { ref: "f1e2", text: "private value", submit: true } });
  const replies = await Promise.all([f.send(request), f.send({ ...request, id: crypto.randomUUID() })]);
  assert.deepEqual(replies[0], replies[1]); assert.equal(replies[0].body.outcome, "completed");
  assert.equal(f.calls.filter(row => row.action === "execute").length, 1);
  assert.deepEqual(f.calls.at(-1).body.arguments, request.body.arguments);
  const row = f.forwarder.journal.rows.get(request.body.command_id);
  assert.equal(row.state, "completed"); assert.ok(!JSON.stringify(row).includes("private value"));
  const changed = structuredClone(request); changed.body.arguments.text = "changed"; changed.body.digest = controllerDigest(changed.operation, changed.body);
  assert.equal((await f.send(changed)).body.error_code, "command_fenced");
  f.forwarder.flights.clear();
  const duplicate = await f.send(request);
  assert.equal(duplicate.body.outcome, "completed"); assert.equal(duplicate.body.error_code, "browser_result_unavailable");
  assert.equal(f.calls.filter(row => row.action === "execute").length, 1);
});

test("a dispatched timeout terminates execution and fences new mutations without ending ISCP", async t => {
  const f = await fixture(t, { execute: (_body, { signal }) => new Promise((_, reject) => signal.addEventListener("abort", () => reject(new Error("aborted")), { once: true })) });
  const b = binding(), lease = await f.acquireTask(b);
  const request = command("execute", b, { lease, operation: "page.click", arguments: { ref: "e2" } }, { timeout: 30 });
  const reply = await f.send(request);
  assert.equal(reply.body.outcome, "unknown"); assert.equal(f.forwarder.journal.rows.get(request.body.command_id).state, "unknown");
  assert.ok(f.lifecycle.includes("controller_call_failed")); assert.equal(f.auth.status.state, "connected");
  assert.equal((await f.send(command("acquire", b, { wait_timeout_ms: 0, session_ttl_ms: 600000 }))).body.outcome, "not_dispatched");
  await f.forwarder.reconcile(request.body.command_id, request.body.digest, "observed_not_applied");
  await f.acquireTask(b);
});

test("large responses use immutable scoped chunks and release remains available when result reservations are full", async t => {
  const f = await fixture(t, { execute: async () => ({ screenshot: "x".repeat(100000) }) });
  const b = binding(), lease = await f.acquireTask(b);
  const request = command("execute", b, { lease, operation: "page.screenshot", arguments: { type: "png", full_page: true } });
  const reply = await f.send(request); const descriptor = reply.body.result;
  assert.equal(reply.body.outcome, "completed"); assert.ok(descriptor.total_bytes > 100000);
  const bytes = [];
  for (let offset = 0; offset < descriptor.total_bytes; offset += 32768) {
    const chunk = envelope("chunk", { schema_version: 1, binding: b, command_id: request.body.command_id,
      result_id: descriptor.result_id, offset, length: Math.min(32768, descriptor.total_bytes - offset) });
    const response = await f.send(chunk); assert.equal(response.status, 200);
    assert.deepEqual(await f.send(chunk), response);
    bytes.push(Buffer.from(response.body.data_base64, "base64"));
  }
  assert.equal(crypto.createHash("sha256").update(Buffer.concat(bytes)).digest("hex"), descriptor.sha256);
  f.forwarder.results.maximum = 0;
  assert.equal((await f.send(command("execute", b, { lease, operation: "page.click", arguments: { ref: "e2" } }))).body.error_code, "browser_result_capacity");
  assert.equal((await f.send(command("release", b, { lease }))).body.outcome, "completed");
});

test("queued acquires expire before journal or local dispatch and cannot inherit a released task", async t => {
  const f = await fixture(t); const b = binding(), lease = await f.acquireTask(b);
  const queued = command("acquire", binding("B"), { wait_timeout_ms: 1000, session_ttl_ms: 600000 }, { timeout: 20 });
  const reply = await f.send(queued);
  assert.equal(reply.body.outcome, "not_dispatched"); assert.equal(f.forwarder.journal.rows.has(queued.body.command_id), false);
  await f.send(command("release", b, { lease }));
  assert.equal(f.calls.filter(row => row.action === "acquire").length, 1);
  await f.acquireTask(binding("B"));
});

test("inline retries use the bounded byte cache and never replay when its data is lost", async t => {
  const f = await fixture(t); const b = binding(), lease = await f.acquireTask(b);
  const request = command("execute", b, { lease, operation: "page.fill", arguments: { ref: "e2", text: "fixture" } });
  const reply = await f.send(request);
  const slot = f.forwarder.flights.get(request.body.command_id);
  assert.equal(slot.promise, null); assert.equal(slot.reply.payload, undefined); assert.ok(slot.inline);
  assert.deepEqual(await f.send(request), reply);
  f.forwarder.results.clear();
  const retry = await f.send(request);
  assert.equal(retry.body.outcome, "completed"); assert.equal(retry.body.error_code, "browser_result_unavailable");
  assert.equal(f.calls.filter(row => row.action === "execute").length, 1);
});

test("failed terminal journal persistence keeps a visible unknown fence and releases owned execution", async t => {
  const f = await fixture(t); const b = binding(), lease = await f.acquireTask(b);
  f.forwarder.journal.finish = async () => { throw new Error("fixture disk unavailable"); };
  const request = command("execute", b, { lease, operation: "page.fill", arguments: { ref: "e2", text: "fixture" } });
  const reply = await f.send(request);
  assert.equal(reply.body.outcome, "unknown");
  assert.equal(f.forwarder.snapshot().unknown_writes[0].command_id, request.body.command_id);
  assert.equal(f.runtime.state, "stopped");
  assert.equal((await f.send(command("acquire", b, { wait_timeout_ms: 0, session_ttl_ms: 600000 }))).body.outcome, "not_dispatched");
});

test("presentation evidence comes from the local adapter rather than the request or page payload", async t => {
  const f = await fixture(t, { execute: async () => ({ surface_visible: true }) });
  const b = binding(), lease = await f.acquireTask(b);
  const reply = await f.send(command("execute", b, { lease, operation: "page.info", arguments: {} }));
  assert.equal(reply.body.payload.result.surface_visible, false);
  f.runtime.adapter.controllerSurfaceVisible = () => true;
  assert.equal((await f.send(command("execute", b, { lease, operation: "page.info", arguments: {} }))).body.payload.result.surface_visible, true);
});
