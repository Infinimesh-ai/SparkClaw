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
const epoch = crypto.randomUUID(), generation = crypto.randomUUID();
async function waitFor(check) {
  const deadline = Date.now() + 3000;
  while (!check()) { assert.ok(Date.now() < deadline, "progress did not arrive"); await new Promise(resolve => setTimeout(resolve, 5)); }
}
function fixture(t, { supported = true } = {}) {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), "sparkclaw-progress-"));
  const store = new ClientStore(directory), conversation = store.create(scope, "original conversation");
  const task = store.enqueue(scope, conversation.id, "original explicit input");
  const requests = [], changes = [], fetches = [];
  let identity = scope, state = "running", client, clock = 0;
  const auth = { generation: 1, status: { state: "connected" }, descriptor: { transport: "iscp", origin: "https://iscp.invalid" },
    transport: { streamSchemaVersion: 1, capabilities: { schema_version: 2, stream_schema_version: 1, profile: "sparkclaw.workbench.transport.v2", session_id: "session",
      authorization_revision: 1, expires_at: new Date(Date.now() + 60000).toISOString(), operations: supported ? ["execution.progress.stream"] : [] },
      streamCall: (operation, body, options) => new Promise((resolve, reject) => {
        assert.equal(operation, "execution.progress.stream");
        const request = { id: crypto.randomUUID(), body, options, settled: false,
          resolve: value => { request.settled = true; resolve(value); }, reject: error => { request.settled = true; reject(error); } };
        requests.push(request); options.onOpen(request.id);
        options.signal.addEventListener("abort", () => request.reject(new Error("closed")), { once: true });
      }) },
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
  client.progressClient.now = () => clock;
  const frame = { url: "sparkclaw-app://workbench/index.html" }, webContents = { mainFrame: frame };
  const capability = new ClientStoreCapability({ window: { webContents }, store, execution: client, getIdentity: () => identity });
  t.after(() => { client.close(); store.close(); fs.rmSync(directory, { recursive: true, force: true }); });
  const body = extra => ({ request_id: task.request_id, input_digest: store.request(scope, task.request_id).input_digest, epoch, generation, revision: 1, ...extra });
  const send = (request, extra, stream_seq = 1, authorityMS = 300000) => { request.options.onAuthority(authorityMS); request.options.onEvent({ type: "iscp.application.stream.v1", schema_version: 1, subscription_id: request.id, stream_seq, body: body(extra) }); };
  return { auth, store, task, conversation, requests, changes, fetches, client, directory, send, body,
    advance: ms => { clock += ms; }, setIdentity: value => { identity = value; }, setState: value => { state = value; },
    delta: (text, revision = 1, prefix = "") => ({ kind: "delta", revision, text, from_offset: Buffer.byteLength(prefix), to_offset: Buffer.byteLength(prefix + text), content_digest: hash(prefix + text) }),
    read: () => capability.dispatch({ sender: webContents, senderFrame: frame }, { schema_version: 1, operation: "read", conversation_id: conversation.id }),
  };
}

test("multiple verified deltas use original task IPC without persisting or acknowledging partial answers; final commit replaces in one notification", async t => {
  const f = fixture(t); await f.client.submit(scope, f.task.request_id);
  assert.equal(f.requests.length, 1); const first = f.requests[0];
  assert.equal(first.body.schema_version, 1); assert.equal(first.body.cursor, undefined); assert.equal(first.options.requestID, f.task.request_id);
  f.send(first, f.delta("First part")); assert.equal(f.client.progressContent(scope, f.task.request_id), "First part");
  assert.equal((await f.read()).tasks[0].progress_content, "First part");
  f.send(first, f.delta(" and 中文🙂", 2, "First part"), 2);
  assert.equal(f.client.progressContent(scope, f.task.request_id), "First part and 中文🙂");
  assert.equal(f.store.receipt(scope, f.task.request_id), null); assert.equal(f.store.read(scope, f.conversation.id).messages.length, 1);
  assert.equal(f.store.read(scope, f.conversation.id).tasks[0].progress_content, undefined);
  const reopened = new ClientStore(f.directory); try { assert.equal(reopened.read(scope, f.conversation.id).messages.length, 1); } finally { reopened.close(); }
  await f.client.reconcilePending(); await f.client.reconcilePending(); assert.equal(f.requests.length, 1);
  f.setState("completed"); f.send(first, { kind: "state", state: "completed", revision: 2 }, 3);
  await waitFor(() => f.store.receipt(scope, f.task.request_id)?.acknowledged === 1);
  assert.equal(f.client.progressContent(scope, f.task.request_id), undefined);
  assert.deepEqual(f.store.read(scope, f.conversation.id).messages.map(m => m.content), ["original explicit input", "Complete verified answer"]);
  const finalIndex = f.changes.findIndex(change => change.messages === 2);
  assert.ok(finalIndex >= 0); assert.equal(f.changes[finalIndex].progress, undefined);
  assert.equal(f.changes.slice(1, finalIndex).some(change => change.progress === undefined), false, "no clear-before-commit notification");
  f.send(first, f.delta("late", 3, "First part and 中文🙂"), 4);
  assert.equal(f.store.read(scope, f.conversation.id).messages.length, 2);
  assert.equal(f.fetches.filter(call => call.method === "POST" && !call.url.endsWith("/ack")).length, 1);
});
test("unmatched helper/remote versions, old progress-only peers, and unaccepted tasks never open or fall back", async t => {
  for (const variant of ["operation", "helper", "remote"]) {
    const f = fixture(t, { supported: variant !== "operation" });
    if (variant === "helper") f.auth.transport.streamSchemaVersion = 2;
    if (variant === "remote") f.auth.transport.capabilities.stream_schema_version = 2;
    await f.client.submit(scope, f.task.request_id); await f.client.reconcilePending(); assert.equal(f.requests.length, 0);
  }
  const f = fixture(t); f.store.markSubmitted(scope, f.task.request_id); f.client.progressClient.ensure(scope, f.store.request(scope, f.task.request_id)); assert.equal(f.requests.length, 0);
});
for (const [label, change] of [["identity", { request_id: crypto.randomUUID() }], ["digest", { content_digest: "0".repeat(64) }], ["field", { secret: true }], ["offset", { from_offset: 1 }], ["UTF-8", { text: "\ud800" }]]) {
  test(`malformed stream ${label} never reaches IPC or creates a receipt`, async t => {
    const f = fixture(t); await f.client.submit(scope, f.task.request_id); f.send(f.requests[0], { ...f.delta("untrusted"), ...change });
    assert.equal(f.client.progressContent(scope, f.task.request_id), undefined); assert.equal((await f.read()).tasks[0].progress_content, undefined);
    assert.equal(f.store.receipt(scope, f.task.request_id), null);
  });
}
for (const boundary of ["identity", "generation", "session", "revision", "expiry", "revocation", "lifetime"]) {
  test(`late frames are fenced by ${boundary} before publication`, async t => {
    const f = fixture(t); await f.client.submit(scope, f.task.request_id); const prior = f.requests[0];
    if (boundary === "identity") f.setIdentity({ ...scope, owner_id: "other" });
    if (boundary === "generation") f.auth.generation++;
    if (boundary === "session") f.auth.transport.capabilities.session_id = "new-session";
    if (boundary === "revision") f.auth.transport.capabilities.authorization_revision++;
    if (boundary === "expiry") f.auth.transport.capabilities.expires_at = new Date(0).toISOString();
    if (boundary === "revocation") f.auth.authorizationRevoked = true;
    if (boundary === "lifetime") f.client.close();
    f.send(prior, f.delta("Stale")); assert.equal(f.client.progressContent(scope, f.task.request_id), undefined);
    assert.equal(f.store.read(scope, f.conversation.id).messages.length, 1);
  });
}
test("warm recovery retains verified text/cursor, discards candidate, resets stream sequence and never resubmits", async t => {
  const f = fixture(t); await f.client.submit(scope, f.task.request_id); const first = f.requests[0];
  f.send(first, f.delta("保留"));
  const snapshot_id = crypto.randomUUID(); f.send(first, { kind: "snapshot_begin", snapshot_id, revision: 2, total_bytes: 3, content_digest: hash("new") }, 2);
  first.reject(new Error("network lost")); await waitFor(() => f.requests.length === 2);
  assert.equal(f.client.progressContent(scope, f.task.request_id), "保留");
  assert.deepEqual(f.requests[1].body.cursor, { epoch, generation, revision: 1, offset: 6, content_digest: hash("保留") });
  f.send(f.requests[1], f.delta("完成", 2, "保留")); assert.equal(f.client.progressContent(scope, f.task.request_id), "保留完成");
  assert.equal(f.fetches.filter(call => call.method === "POST").length, 1);
});
test("cold client restart uses durable original task with no isolated cursor or resubmission", async t => {
  const f = fixture(t); await f.client.submit(scope, f.task.request_id); f.send(f.requests[0], f.delta("temporary"));
  f.client.close(); f.auth.generation++; f.client.start(); await waitFor(() => f.requests.length === 2);
  assert.equal(f.requests[1].body.cursor, undefined); assert.equal(f.requests[1].options.requestID, f.task.request_id);
  assert.equal(f.fetches.filter(call => call.method === "POST").length, 1);
});
test("early terminal completion waits for its tail while verified final result can overtake all stream gaps", async t => {
  const f = fixture(t); await f.client.submit(scope, f.task.request_id); const first = f.requests[0];
  first.resolve({ schema_version: 1, last_stream_seq: 2, reason: "terminal" });
  await new Promise(resolve => setTimeout(resolve, 5)); f.send(first, f.delta("a"));
  f.setState("completed"); await f.client.reconcile(scope, f.task.request_id);
  assert.equal(f.store.read(scope, f.conversation.id).messages.at(-1).content, "Complete verified answer");
  assert.equal(first.options.signal.aborted, true);
  f.send(first, f.delta("b", 2, "a"), 2); assert.equal(f.client.progressContent(scope, f.task.request_id), undefined);
});
test("same authority renewal preserves a partial candidate and does not reopen", async t => {
  const f = fixture(t); await f.client.submit(scope, f.task.request_id); const first = f.requests[0], snapshot_id = crypto.randomUUID();
  const base = { snapshot_id, total_bytes: 6, content_digest: hash("中文") };
  f.send(first, { kind: "snapshot_begin", ...base }); f.send(first, { kind: "snapshot_chunk", snapshot_id, offset: 0, text: "中" }, 2);
  f.auth.transport.capabilities.expires_at = new Date(Date.now() + 120000).toISOString();
  f.send(first, { kind: "snapshot_chunk", snapshot_id, offset: 3, text: "文" }, 3); f.send(first, { kind: "snapshot_end", ...base }, 4);
  assert.equal(f.client.progressContent(scope, f.task.request_id), "中文"); assert.equal(f.requests.length, 1);
});
test("one 30-second incident spans failed opens and partial snapshots; exhausted recovery keeps original lookup alive", async t => {
  const f = fixture(t); await f.client.submit(scope, f.task.request_id); f.advance(9000);
  f.requests[0].reject(new Error("lost opening")); await waitFor(() => f.requests.length === 2);
  f.advance(20000); const second = f.requests[1];
  f.send(second, { kind: "snapshot_begin", snapshot_id: crypto.randomUUID(), total_bytes: 6, content_digest: hash("中文") });
  f.advance(1001); await waitFor(() => second.options.signal.aborted);
  await f.client.reconcilePending(); await new Promise(resolve => setTimeout(resolve, 300)); assert.equal(f.requests.length, 2);
  f.setState("completed"); await f.client.reconcilePending(); assert.equal(f.store.read(scope, f.conversation.id).messages.length, 2);
  assert.equal(f.fetches.filter(call => call.method === "POST" && !call.url.endsWith("/ack")).length, 1);
});
test("corrupt authoritative result never commits preview or ACKs", async t => {
  const f = fixture(t); await f.client.submit(scope, f.task.request_id); f.send(f.requests[0], f.delta("valid preview"));
  const fetch = f.auth.authorizedExecutionFetch; f.auth.authorizedExecutionFetch = async (...args) => {
    const value = await (await fetch(...args)).json(); if (value.result) value.result.digest = "0".repeat(64); return new Response(JSON.stringify(value));
  };
  f.setState("completed"); await assert.rejects(f.client.reconcile(scope, f.task.request_id), /digest/);
  assert.equal(f.store.receipt(scope, f.task.request_id), null); assert.equal(f.store.read(scope, f.conversation.id).messages.length, 1);
  assert.equal(f.fetches.filter(call => call.url.endsWith("/ack")).length, 0);
});

test("valid-snapshot transient outage retains preview and cursor across a replacement transport session", async t => {
  const f = fixture(t); await f.client.submit(scope, f.task.request_id); f.send(f.requests[0], f.delta("verified 中文"));
  const manifest = f.auth.transport.capabilities;
  f.auth.status.state = "service_unavailable"; f.auth.generation++; f.auth.transport.capabilities = undefined;
  f.client.pauseProgress(); assert.equal(f.client.progressContent(scope, f.task.request_id), "verified 中文");
  await new Promise(resolve => setTimeout(resolve, 50));
  f.auth.transport.capabilities = { ...manifest, session_id: "replacement" }; f.auth.status.state = "connected";
  await waitFor(() => f.requests.length === 2);
  assert.equal(f.requests[1].body.cursor.content_digest, hash("verified 中文"));
  f.send(f.requests[1], f.delta("!", 2, "verified 中文")); assert.equal(f.client.progressContent(scope, f.task.request_id), "verified 中文!");
});

test("known authority expiry clears valid text immediately while reconnect retains the original task", async t => {
  const f = fixture(t); await f.client.submit(scope, f.task.request_id); f.send(f.requests[0], f.delta("valid before expiry"));
  f.auth.status.state = "service_unavailable"; f.client.pauseProgress({ authorityExpired: true });
  assert.equal(f.client.progressContent(scope, f.task.request_id), undefined);
  f.auth.generation++; f.auth.status.state = "connected"; await waitFor(() => f.requests.length === 2);
  assert.equal(f.requests[1].body.cursor, undefined); assert.equal(f.requests[1].options.requestID, f.task.request_id);
});

test("real-stream projection grows beyond the former 8 KiB preview limit with multibyte text", async t => {
  const f = fixture(t); await f.client.submit(scope, f.task.request_id); const text = "中文🙂".repeat(1500);
  f.send(f.requests[0], f.delta(text)); f.send(f.requests[0], f.delta("尾部", 2, text), 2);
  assert.equal(f.client.progressContent(scope, f.task.request_id), text + "尾部");
  assert.ok(f.client.progressProjection(scope, f.task.request_id).offset > 8192);
});

test("a reused helper snapshot with ten milliseconds left cannot inherit long capability expiry", async t => {
  const f = fixture(t); await f.client.submit(scope, f.task.request_id); const request = f.requests[0];
  f.send(request, f.delta("briefly authorized"), 1, 10);
  assert.equal(f.client.progressContent(scope, f.task.request_id), "briefly authorized");
  // Reads enforce the monotonic bound even before a delayed timer can notify.
  const elapsed = performance.now() + 12; while (performance.now() < elapsed) { /* Emulate a busy main event loop. */ }
  assert.equal(f.client.progressContent(scope, f.task.request_id), undefined);
  await waitFor(() => request.options.signal.aborted && f.changes.at(-1)?.progress === undefined);
  await waitFor(() => f.fetches.some(call => call.method === "GET"));
  assert.equal(f.store.receipt(scope, f.task.request_id), null);
  assert.equal(f.store.read(scope, f.conversation.id).messages.length, 1);
  assert.ok(Date.parse(f.auth.transport.capabilities.expires_at) > Date.now() + 30000);
  await waitFor(() => f.requests.length === 2); assert.equal(f.requests[1].body.cursor, undefined);
});

test("fresh same-version authority metadata extends its candidate deadline without reopening", async t => {
  const f = fixture(t); await f.client.submit(scope, f.task.request_id); const request = f.requests[0], snapshot_id = crypto.randomUUID();
  const base = { snapshot_id, total_bytes: 6, content_digest: hash("中文") };
  f.send(request, { kind: "snapshot_begin", ...base }, 1, 150);
  f.send(request, { kind: "snapshot_chunk", snapshot_id, offset: 0, text: "中" }, 2, 150);
  f.send(request, { kind: "snapshot_chunk", snapshot_id, offset: 3, text: "文" }, 3, 1000);
  f.send(request, { kind: "snapshot_end", ...base }, 4, 1000);
  await new Promise(resolve => setTimeout(resolve, 170));
  assert.equal(f.client.progressContent(scope, f.task.request_id), "中文"); assert.equal(f.requests.length, 1);
});

test("explicit helper grant expiry immediately invalidates text despite long caps and starts original lookup", async t => {
  const f = fixture(t); await f.client.submit(scope, f.task.request_id); const request = f.requests[0];
  f.send(request, f.delta("prior authority"));
  request.reject(Object.assign(new Error("expired snapshot"), { status: 403, code: "grant_expired" }));
  await Promise.resolve(); await Promise.resolve();
  assert.equal(f.client.progressContent(scope, f.task.request_id), undefined);
  assert.equal(request.options.signal.aborted, true);
  await waitFor(() => f.fetches.some(call => call.method === "GET"));
  assert.equal(f.fetches.filter(call => call.method === "POST").length, 1);
});

test("a frame without helper authority cannot establish a display lease or publish text", async t => {
  const f = fixture(t); await f.client.submit(scope, f.task.request_id); const request = f.requests[0];
  request.options.onEvent({ type: "iscp.application.stream.v1", schema_version: 1, subscription_id: request.id, stream_seq: 1,
    body: f.body(f.delta("unqualified")) });
  assert.equal(f.client.progressContent(scope, f.task.request_id), undefined); assert.equal(request.options.signal.aborted, true);
});

test("a late authority refresh from an obsolete attempt cannot revive an expired projection", async t => {
  const f = fixture(t); await f.client.submit(scope, f.task.request_id); const first = f.requests[0];
  f.send(first, f.delta("old"), 1, 10); await waitFor(() => first.options.signal.aborted);
  first.options.onAuthority(300000); f.send(first, f.delta("late", 2, "old"));
  assert.equal(f.client.progressContent(scope, f.task.request_id), undefined);
  await waitFor(() => f.requests.length === 2); assert.equal(f.requests[1].body.cursor, undefined);
});
