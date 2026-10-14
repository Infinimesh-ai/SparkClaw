import assert from "node:assert/strict";
import crypto from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { ClientStore } from "../src/main/client-store.mjs";
import { ExecutionClient } from "../src/main/execution-client.mjs";
import { ClientStoreCapability } from "../src/main/client-store-capability.mjs";

const scope = { deployment_id: "deployment", owner_id: "owner", client_id: "client" };
const hash = value => crypto.createHash("sha256").update(value).digest("hex");
const epoch = "11111111-1111-4111-8111-111111111111";
async function waitFor(check) {
  const deadline = Date.now() + 3000;
  while (!check()) { assert.ok(Date.now() < deadline, "progress did not arrive"); await new Promise(resolve => setTimeout(resolve, 5)); }
}
function fixture(t, { supported = true, ignoreAbort = false } = {}) {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), "sparkclaw-progress-"));
  const store = new ClientStore(directory), conversation = store.create(scope, "original conversation");
  const task = store.enqueue(scope, conversation.id, "original explicit input");
  const requests = [], changes = [], fetches = [];
  let identity = scope, state = "running", client;
  const auth = { generation: 1, status: { state: "connected" }, descriptor: { transport: "iscp", origin: "https://iscp.invalid" },
    transport: { capabilities: { schema_version: 2, profile: "sparkclaw.workbench.transport.v2", session_id: "session",
      authorization_revision: 1, expires_at: new Date(Date.now() + 60000).toISOString(), operations: supported ? ["execution.progress"] : [] } },
    invokeISCP: (operation, body, options) => new Promise((resolve, reject) => {
      assert.equal(operation, "execution.progress");
      const request = { body, options, settled: false, resolve: value => { request.settled = true; resolve(value); }, reject: error => { request.settled = true; reject(error); } };
      requests.push(request);
      if (!ignoreAbort) options.signal.addEventListener("abort", () => request.reject(new Error("closed")), { once: true });
    }),
    authorizedExecutionFetch: async (url, init = {}) => {
      fetches.push({ url, method: init.method || "GET" });
      if (url.endsWith("/ack")) return new Response("{}");
      const payload = JSON.stringify({ content: "Complete verified answer", files: [] });
      return new Response(JSON.stringify({ schema_version: 1, request_id: task.request_id,
        input_digest: store.request(scope, task.request_id).input_digest, state: init.method === "POST" ? "accepted" : state,
        ...(state === "completed" && init.method !== "POST" ? { result: { sequence: 1, digest: hash(payload), payload } } : {}) }));
    } };
  client = new ExecutionClient({ auth, store, getIdentity: () => identity,
    onChange: () => changes.push({ progress: client.progressContent(scope, task.request_id), messages: store.read(scope, conversation.id).messages.length }) });
  const frame = { url: "sparkclaw-app://workbench/index.html" }, webContents = { mainFrame: frame };
  const capability = new ClientStoreCapability({ window: { webContents }, store, execution: client, getIdentity: () => identity });
  t.after(() => { client.close(); store.close(); fs.rmSync(directory, { recursive: true, force: true }); });
  return { auth, store, task, conversation, requests, changes, fetches, client, directory,
    setIdentity: value => { identity = value; }, setState: value => { state = value; },
    packet: (content, revision = 1, extra = {}) => ({ schema_version: 1, request_id: task.request_id,
      input_digest: store.request(scope, task.request_id).input_digest, epoch, revision, content, digest: hash(content), state: "running", truncated: false, ...extra }),
    pending: () => requests.filter(request => !request.settled),
    read: () => capability.dispatch({ sender: webContents, senderFrame: frame }, { schema_version: 1, operation: "read", conversation_id: conversation.id }),
  };
}

test("multiple authenticated previews use the original task IPC without persisting or acknowledging partial answers", async t => {
  const f = fixture(t); await f.client.submit(scope, f.task.request_id);
  assert.equal(f.requests.length, 1);
  assert.deepEqual(f.requests[0].body, { input_digest: f.store.request(scope, f.task.request_id).input_digest, after_revision: 0, wait_ms: 10000 });
  assert.equal(f.requests[0].options.requestID, f.task.request_id);
  f.requests[0].resolve(f.packet("First part"));
  await waitFor(() => f.client.progressContent(scope, f.task.request_id) === "First part");
  assert.equal((await f.read()).tasks[0].progress_content, "First part");
  assert.equal(f.store.read(scope, f.conversation.id).tasks[0].progress_content, undefined);
  await waitFor(() => f.requests.length === 2);
  assert.equal(f.requests[1].body.after_revision, 1);
  f.requests[1].resolve(f.packet("First part and second part", 2));
  await waitFor(() => f.client.progressContent(scope, f.task.request_id) === "First part and second part");
  assert.equal(f.store.receipt(scope, f.task.request_id), null);
  assert.equal(f.store.read(scope, f.conversation.id).messages.length, 1);
  assert.equal(f.fetches.filter(call => call.url.endsWith("/ack")).length, 0);
  const reopened = new ClientStore(f.directory);
  try { assert.equal(reopened.read(scope, f.conversation.id).messages.length, 1); } finally { reopened.close(); }
  await f.client.reconcilePending(); await f.client.reconcilePending();
  assert.ok(f.pending().length <= 1, "only one long poll exists for the original task");
  await waitFor(() => f.requests.length === 3);
  f.setState("completed"); f.requests[2].resolve(f.packet("First part and second part", 3, { state: "completed" }));
  await waitFor(() => f.store.receipt(scope, f.task.request_id)?.acknowledged === 1);
  assert.equal(f.client.progressContent(scope, f.task.request_id), undefined);
  assert.equal((await f.read()).tasks[0].progress_content, undefined);
  assert.deepEqual(f.store.read(scope, f.conversation.id).messages.map(message => message.content), ["original explicit input", "Complete verified answer"]);
  assert.equal(f.fetches.filter(call => call.method === "POST" && !call.url.endsWith("/ack")).length, 1);
  assert.equal(f.fetches.filter(call => call.url.endsWith("/ack")).length, 1);
});

test("unnegotiated peers and locally saved unsent tasks never request progress", async t => {
  const old = fixture(t, { supported: false }); await old.client.submit(scope, old.task.request_id); await old.client.reconcilePending();
  assert.equal(old.requests.length, 0);
  const fresh = fixture(t); await fresh.client.reconcilePending(); assert.equal(fresh.requests.length, 0);
});

for (const [label, change] of [
  ["request identity", { request_id: "other" }], ["input digest", { input_digest: "0".repeat(64) }],
  ["epoch", { epoch: "not-an-epoch" }], ["revision", { revision: 0 }], ["digest", { digest: "0".repeat(64) }],
  ["state", { state: "arbitrary" }], ["truncated flag", { truncated: "true" }], ["extra field", { secret: "unexpected" }],
]) {
  test(`malformed progress ${label} is never exposed and failures do not spin or replay work`, async t => {
    const f = fixture(t); await f.client.submit(scope, f.task.request_id);
    f.requests[0].resolve(f.packet("untrusted text", 1, change));
    await new Promise(resolve => setTimeout(resolve, 10));
    assert.equal(f.client.progressContent(scope, f.task.request_id), undefined);
    await f.client.reconcilePending(); assert.equal(f.requests.length, 1);
    assert.equal(f.store.receipt(scope, f.task.request_id), null);
    assert.equal(f.fetches.filter(call => call.url.endsWith("/ack")).length, 0);
  });
}

for (const content of ["x".repeat(8193), "\ud800", "hidden\0text"]) {
  test("oversized or invalid UTF-8 preview is rejected before IPC display", async t => {
    const f = fixture(t); await f.client.submit(scope, f.task.request_id);
    f.requests[0].resolve(f.packet(content)); await new Promise(resolve => setTimeout(resolve, 10));
    assert.equal((await f.read()).tasks[0].progress_content, undefined); assert.equal(f.store.receipt(scope, f.task.request_id), null);
  });
}

for (const variant of ["rollback", "same revision drift", "noncumulative"]) {
  test(`a ${variant} in the same progress epoch clears preview without committing partial data`, async t => {
    const f = fixture(t); await f.client.submit(scope, f.task.request_id); f.requests[0].resolve(f.packet("First", 3));
    await waitFor(() => f.requests.length === 2);
    const packet = variant === "rollback" ? f.packet("First", 2) : variant === "same revision drift" ? f.packet("First changed", 3) : f.packet("Different", 4);
    f.requests[1].resolve(packet); await waitFor(() => f.client.progressContent(scope, f.task.request_id) === undefined);
    assert.equal(f.store.read(scope, f.conversation.id).messages.length, 1); assert.equal(f.store.receipt(scope, f.task.request_id), null);
  });
}

test("a new Gateway epoch replaces temporary preview and truncated UTF-8 stays a preview until terminal lookup", async t => {
  const f = fixture(t); await f.client.submit(scope, f.task.request_id); f.requests[0].resolve(f.packet("Prior gateway", 8));
  await waitFor(() => f.requests.length === 2);
  const nextEpoch = "22222222-2222-4222-8222-222222222222", content = "你".repeat(2730);
  f.requests[1].resolve(f.packet(content, 1, { epoch: nextEpoch, truncated: true }));
  await waitFor(() => f.client.progressContent(scope, f.task.request_id) === content);
  await waitFor(() => f.requests.length === 3);
  assert.equal(f.requests[2].body.after_revision, 1);
  f.setState("completed"); f.requests[2].resolve(f.packet(content, 2, { epoch: nextEpoch, truncated: true, state: "completed" }));
  await waitFor(() => f.store.receipt(scope, f.task.request_id)?.acknowledged === 1);
  assert.equal(f.store.read(scope, f.conversation.id).messages.at(-1).content, "Complete verified answer");
});

for (const boundary of ["identity", "generation", "session", "authorization revision", "lifetime"]) {
  test(`late progress is fenced by ${boundary} and cannot leak into another conversation scope`, async t => {
    const f = fixture(t, { ignoreAbort: true }); await f.client.submit(scope, f.task.request_id);
    const prior = f.requests[0];
    if (boundary === "identity") f.setIdentity({ ...scope, owner_id: "other" });
    else if (boundary === "generation") f.auth.generation++;
    else if (boundary === "session") f.auth.transport.capabilities.session_id = "new-session";
    else if (boundary === "authorization revision") f.auth.transport.capabilities.authorization_revision++;
    else f.client.close();
    prior.resolve(f.packet("Stale text")); await new Promise(resolve => setTimeout(resolve, 10));
    assert.equal(f.client.progressContent(scope, f.task.request_id), undefined);
    assert.equal(f.store.read(scope, f.conversation.id).messages.length, 1);
  });
}

test("disconnect clears previews and reconnect starts at revision zero without resubmitting the task", async t => {
  const f = fixture(t); await f.client.submit(scope, f.task.request_id); f.requests[0].resolve(f.packet("Temporary", 5));
  await waitFor(() => f.client.progressContent(scope, f.task.request_id) === "Temporary");
  f.client.close(); assert.equal(f.client.progressContent(scope, f.task.request_id), undefined);
  f.auth.generation++; f.client.start();
  await waitFor(() => f.requests.length === 2);
  assert.equal(f.requests[1].body.after_revision, 0);
  f.requests[1].resolve(f.packet("Restored snapshot", 6));
  await waitFor(() => f.client.progressContent(scope, f.task.request_id) === "Restored snapshot");
  assert.equal(f.fetches.filter(call => call.method === "POST").length, 1);
});

test("a failed long poll clears preview and retries from zero only after a later ordinary reconciliation", async t => {
  const f = fixture(t); await f.client.submit(scope, f.task.request_id); f.requests[0].resolve(f.packet("Temporary", 3));
  await waitFor(() => f.requests.length === 2);
  f.requests[1].reject(new Error("network lost"));
  await waitFor(() => f.client.progressContent(scope, f.task.request_id) === undefined);
  await f.client.reconcilePending(); assert.equal(f.requests.length, 2);
  await new Promise(resolve => setTimeout(resolve, 2050)); assert.equal(f.requests.length, 2, "no independent retry timer dispatches work");
  await f.client.reconcilePending(); assert.equal(f.requests.length, 3); assert.equal(f.requests[2].body.after_revision, 0);
  assert.equal(f.fetches.filter(call => call.method === "POST").length, 1); assert.equal(f.store.receipt(scope, f.task.request_id), null);
});

test("terminal expiry may release preview text while original lookup remains authoritative", async t => {
  const f = fixture(t); await f.client.submit(scope, f.task.request_id); f.requests[0].resolve(f.packet("Temporary", 1));
  await waitFor(() => f.requests.length === 2);
  f.setState("delivery_expired"); f.requests[1].resolve(f.packet("", 2, { state: "delivery_expired" }));
  await waitFor(() => f.store.request(scope, f.task.request_id).status === "delivery_expired");
  assert.equal(f.client.progressContent(scope, f.task.request_id), undefined);
  assert.equal(f.store.read(scope, f.conversation.id).messages.length, 1); assert.equal(f.store.receipt(scope, f.task.request_id), null);
});

test("a valid terminal preview cannot replace a final result whose durable digest fails verification", async t => {
  const f = fixture(t); await f.client.submit(scope, f.task.request_id);
  const originalFetch = f.auth.authorizedExecutionFetch;
  f.auth.authorizedExecutionFetch = async (...args) => {
    const response = await originalFetch(...args), value = await response.json();
    if (value.result) value.result.digest = "0".repeat(64);
    return new Response(JSON.stringify(value));
  };
  f.setState("completed"); f.requests[0].resolve(f.packet("Never commit this preview", 1, { state: "completed" }));
  await waitFor(() => f.fetches.some(call => call.method === "GET"));
  await waitFor(() => f.client.progressContent(scope, f.task.request_id) === undefined);
  assert.equal(f.store.read(scope, f.conversation.id).messages.length, 1); assert.equal(f.store.receipt(scope, f.task.request_id), null);
  assert.equal(f.fetches.filter(call => call.url.endsWith("/ack")).length, 0);
});
