import assert from "node:assert/strict";
import fs from "node:fs/promises";
import test from "node:test";
import { controllerDigest, validateControllerRequest, validateControllerBinding, iscpHostID, iscpAuthorizationDigest, CONTROLLER_OPERATIONS } from "../src/browser/controller-wire.mjs";
import { ControllerResults, RESULT_LIMIT, CHUNK_LIMIT } from "../src/browser/controller-results.mjs";
import { ControllerAdmission } from "../src/browser/controller-admission.mjs";

const scope = { owner_id: "owner", client_id: "client", installation_id: "installation" };
const session = { session_id: "session-a", authorization_revision: 7 };
const binding = { ...scope, local_conversation_id: "conversation", local_task_id: "task", host_id: iscpHostID(scope, session.session_id),
  runtime_generation: "iscp_session-a", connection_epoch: session.session_id, lease_id: "lease-a", page_id: "page-a", page_generation: 1,
  authorization_digest: iscpAuthorizationDigest(session.session_id, 7), lease_expires_at: "0001-01-01T00:00:00Z" };
const lease = { schema_version: 1, state: "acquired", profile_id: "client_embedded", lane: "mcp", session_id: "session_" + "a".repeat(32),
  credential_generation: 7, controller_generation: 1, session_generation: 1, page_generation: 1, expires_at: "2030-01-01T00:00:00Z" };
const execute = (operation, args) => {
  const body = { schema_version: 1, command_id: "command-a", digest: "", binding, timeout_ms: 25000, lease, operation, arguments: args };
  body.digest = controllerDigest("browser.controller.execute", body); return body;
};

test("Controller wire schema admits only scoped generic operations with digest-bound deadlines", async () => {
  validateControllerBinding(binding, scope, session);
  assert.throws(() => validateControllerBinding(binding, scope, { ...session, authorization_revision: 8 }), /stale/);
  assert.equal(validateControllerRequest("browser.controller.execute", execute("page.navigate", { url: "https://example.test" })), true);
  assert.equal(validateControllerRequest("browser.controller.execute", execute("page.read", { max_chars: 12000 })), false);
  for (const [operation, args] of [["tabs.handoff", { page_id: "page_1" }], ["ai_chat.export", { provider: "x", url: "https://example.test" }],
    ["page.read", { script: "x" }], ["page.wait", { duration_ms: 15001 }]]) {
    assert.throws(() => validateControllerRequest("browser.controller.execute", execute(operation, args)));
  }
  const stale = execute("page.fill", { ref: "f1e2", text: "value" }); stale.timeout_ms--;
  assert.throws(() => validateControllerRequest("browser.controller.execute", stale), /digest/);
  const registry = JSON.parse(await fs.readFile(new URL("../src/shared/iscp-operations.json", import.meta.url)));
  for (const operation of CONTROLLER_OPERATIONS) {
    const spec = registry.find(row => row.name === operation);
    assert.equal(spec.direction, "reverse"); assert.equal(spec.scope, "browser.control"); assert.equal(spec.http, undefined);
    assert.equal(spec.mutation, ["acquire", "execute", "release"].some(name => operation.endsWith("." + name)));
  }
});

test("large results reserve before dispatch and keep final-chunk retries immutable until the fixed TTL", () => {
  let now = 0;
  const results = new ControllerResults({ now: () => now });
  const first = results.reserve(), second = results.reserve();
  assert.ok(first); assert.ok(second); assert.equal(results.reserve(), null);
  const command = { command_id: "command-a", binding, digest: "a".repeat(64) };
  const descriptor = results.put(first, command, { text: "x".repeat(RESULT_LIMIT - 100) });
  results.release(second);
  const input = { binding, command_id: command.command_id, result_id: descriptor.result_id,
    offset: descriptor.total_bytes - 12, length: 12 };
  const chunk = results.read(input);
  assert.deepEqual(results.read(input), chunk);
  assert.equal(Buffer.from(chunk.data_base64, "base64").length, 12);
  assert.throws(() => results.read({ ...input, binding: { ...binding, connection_epoch: "foreign" } }), /unavailable/);
  assert.throws(() => results.read({ ...input, length: CHUNK_LIMIT + 1 }), /range/);
  assert.throws(() => results.read({ ...input, offset: descriptor.total_bytes }), /range/);
  now = 59999; assert.deepEqual(results.read(input), chunk);
  now = 60000; assert.throws(() => results.read(input), /unavailable/);
  assert.equal(results.bytes, 0);
  const pending = results.reserve(); results.clear();
  assert.throws(() => results.put(pending, command, {}), /reservation/);
});

test("one active acquire uses bounded FIFO and expires canceled entries without dispatch", async () => {
  const gate = new ControllerAdmission({ maximum: 2 });
  const options = () => ({ deadline: performance.now() + 1000, waitMS: 1000 });
  const active = await gate.acquire(options());
  const abort = new AbortController();
  const canceled = gate.acquire({ ...options(), signal: abort.signal });
  const next = gate.acquire(options());
  await assert.rejects(gate.acquire(options()), /busy/);
  abort.abort(); await assert.rejects(canceled, /busy/);
  gate.release(active); const admitted = await next;
  assert.equal(gate.active, admitted);
  const expired = gate.acquire({ deadline: performance.now() + 10, waitMS: 1000 });
  await assert.rejects(expired, /busy/);
  const shutdown = gate.acquire(options()); gate.close();
  await assert.rejects(shutdown, /busy/);
  assert.equal(gate.active, admitted, "shutdown must await proof of active cleanup");
  gate.release(admitted); assert.equal(gate.active, null);
  await assert.rejects(gate.acquire(options()), /busy/);
});

test("digest vectors include Go-compatible escaping and UTF-8 key ordering", async () => {
  const vectors = JSON.parse(await fs.readFile(new URL("../../../tools/browser-controller/test/fixtures/client-wire-vectors.json", import.meta.url)));
  for (const vector of vectors) assert.equal(controllerDigest(vector.operation, vector.body), vector.digest);
});
