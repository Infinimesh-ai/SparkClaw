import assert from "node:assert/strict";
import crypto from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { INLINE_BODY_BYTES, LinkDomainClient } from "../src/main/link-domain-client.mjs";
import { LinkError } from "../src/main/link-transport.mjs";
import { mutationProtector } from "./fixtures/mutation-protector.mjs";

const ORIGIN = "https://backend.sparkclaw-link.invalid";
const binding = { deployment_id: "d", owner_id: "owner", client_id: "link-desk" };
const installationID = "11111111-1111-4111-8111-111111111111";
const DOMAINS = ["mail", "settings", "medicine", "speech", "notifications", "browser", "mcd", "presentation"];
const sha = (bytes) => crypto.createHash("sha256").update(bytes).digest("hex");

// A link that answers domain.call like the Gateway: a mutation runs once per
// idempotency key, replay_only never runs it, and large objects travel with
// the object operations.
function fakeLink(handler) {
  const calls = [], receipts = new Map(), uploads = new Map(), objects = new Map();
  const link = {
    online: true, calls, receipts, objects, runs: 0,
    async request(op, params) {
      calls.push({ op, params: structuredClone(params) });
      if (!link.online) throw new LinkError("backend_unavailable", "The backend is unreachable", true, true);
      if (op === "object.upload.begin") { const id = crypto.randomUUID(); uploads.set(id, { ...params, data: Buffer.alloc(0) }); return { upload_id: id, received: 0, chunk_size: 262144 }; }
      if (op === "object.upload.chunk") { const u = uploads.get(params.upload_id); u.data = Buffer.concat([u.data, Buffer.from(params.data, "base64")]); return { upload_id: params.upload_id, received: u.data.length, chunk_size: 262144 }; }
      if (op === "object.upload.commit") { const u = uploads.get(params.upload_id); assert.equal(sha(u.data), u.sha256); const id = crypto.randomUUID(); objects.set(id, u); return { upload_id: params.upload_id, received: u.data.length, object_id: id }; }
      if (op === "object.read") { const o = objects.get(params.object_id); const part = o.data.subarray(params.offset, params.offset + params.length); return { offset: params.offset, data: part.toString("base64"), eof: params.offset + part.length >= o.data.length }; }
      assert.equal(op, "domain.call");
      let args = params.args;
      if (params.args_object) { const o = objects.get(params.args_object); assert.equal(o.purpose, "domain_args"); args = JSON.parse(o.data.toString()); }
      if (args.body_object) { const o = objects.get(args.body_object.object_id); assert.equal(o.purpose, "request_body"); args = { ...args, body: JSON.parse(o.data.toString()) }; delete args.body_object; }
      const digest = JSON.stringify([params.method, args]);
      if (!params.idempotency_key) return { result: await handler(params.method, args, link) };
      const prior = receipts.get(params.idempotency_key);
      if (prior) {
        if (prior.digest !== digest) throw new LinkError("conflict", "idempotency key reused with different content");
        if (prior.pending) throw new LinkError("outcome_unknown", "the original attempt did not finish");
        return { result: prior.result };
      }
      if (params.replay_only) throw new LinkError("not_found", "no attempt with this idempotency key ran", false, true);
      receipts.set(params.idempotency_key, { digest, pending: true });
      link.runs++;
      const result = await handler(params.method, args, link);
      receipts.set(params.idempotency_key, { digest, result });
      if (link.loseNextReply) { link.loseNextReply = false; throw new LinkError("timeout", "no reply in time", true); }
      return { result };
    },
  };
  return link;
}

function client(t, link, { root = fs.mkdtempSync(path.join(os.tmpdir(), "link-domain-client-")), files = {} } = {}) {
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const make = () => new LinkDomainClient({ link, origin: ORIGIN, binding, installationID, domains: DOMAINS, journalRoot: root, mutationProtector,
    canSendMailAttachments: () => true,
    readLocalFile: (scope, id) => { const file = files[id]; if (!file) throw new Error("missing"); return { ...file, content: Buffer.from(file.content), sha256: sha(file.content), size: Buffer.byteLength(file.content) }; } });
  const c = make();
  t.after(() => c.close());
  return { c, root, make };
}

const fetchJSON = async (c, route, init = {}) => {
  const response = await c.fetch(`${ORIGIN}${route}`, { ...init, ...(init.body !== undefined ? { body: JSON.stringify(init.body), headers: { "content-type": "application/json" } } : {}) });
  return { status: response.status, body: await response.json().catch(() => undefined), headers: response.headers };
};

test("routes become typed domain.call requests; reads carry no key, changes carry one and keep the settings revision", async t => {
  const link = fakeLink((method, args) => {
    if (method === "settings.owner.get") return { status: 200, body: { revision: "r1", value: { display_name: "Owner" } } };
    if (method === "settings.owner.patch") return { status: 200, body: { revision: "r2", value: { display_name: args.display_name } } };
    return { status: 200, body: { ok: true } };
  });
  const { c } = client(t, link);
  await c.start();
  assert.deepEqual((await fetchJSON(c, "/api/owner")).body, { display_name: "Owner" });
  const patched = await fetchJSON(c, "/api/owner", { method: "POST", body: { display_name: "New" } });
  assert.deepEqual(patched.body, { display_name: "New" });
  const read = link.calls.find(call => call.params.method === "settings.owner.get");
  const write = link.calls.find(call => call.params.method === "settings.owner.patch");
  assert.equal(read.params.idempotency_key, undefined);
  assert.match(write.params.idempotency_key, /^[a-f0-9-]{36}$/u);
  assert.equal(write.params.args.expected_revision, "r1");
  assert.equal(write.params.domain, "settings");
  assert.equal((await fetchJSON(c, "/api/clients")).status, 410, "devices are managed on the console");
});

test("a change whose reply was lost is answered by replay_only and never runs twice, also after a restart", async t => {
  const link = fakeLink((method, args) => ({ status: 200, body: { revision: `r${link.runs}`, value: { display_name: args.display_name } } }));
  const { c, make } = client(t, link);
  await c.start();
  link.loseNextReply = true;
  const lost = await fetchJSON(c, "/api/owner", { method: "POST", body: { display_name: "Once" } });
  assert.equal(lost.status, 504);
  assert.equal(lost.body.error_code, "operation_outcome_unknown");
  assert.equal(link.runs, 1);
  // After a restart the recovery scan learns the original outcome.
  c.close();
  const again = make(); t.after(() => again.close());
  await again.start();
  await new Promise(resolve => setTimeout(resolve, 20));
  const replays = link.calls.filter(call => call.params.replay_only);
  assert.equal(replays.length, 1, "the recovery scan replays the original key");
  assert.equal(link.runs, 1, "the change ran once");
  // The same input again returns the original result without a new effect.
  const replayed = await fetchJSON(again, "/api/owner", { method: "POST", body: { display_name: "Once" } });
  assert.deepEqual(replayed.body, { display_name: "Once" });
  assert.equal(link.runs, 1);
});

test("a change that never reached the backend is closed as not sent and a later action may send it", async t => {
  const link = fakeLink((method, args) => ({ status: 200, body: { revision: "r", value: args.body } }));
  const { c } = client(t, link);
  await c.start();
  link.online = false;
  const offline = await fetchJSON(c, "/api/owner", { method: "POST", body: { display_name: "Later" } });
  assert.equal(offline.status, 409);
  assert.equal(offline.body.code, "operation_not_sent");
  link.online = true;
  const later = await fetchJSON(c, "/api/owner", { method: "POST", body: { display_name: "Later" } });
  assert.equal(later.status, 200);
  assert.equal(link.runs, 1);
});

test("an attempt the backend reports as unfinished is never repeated", async t => {
  const link = fakeLink(() => ({ status: 200, body: { revision: "r", value: {} } }));
  const { c } = client(t, link);
  await c.start();
  link.loseNextReply = true;
  await fetchJSON(c, "/api/owner", { method: "POST", body: { display_name: "A" } });
  // The backend stopped midway: its receipt is still pending.
  for (const receipt of link.receipts.values()) receipt.pending = true;
  await assert.rejects(c.fetch(`${ORIGIN}/api/owner`, { method: "POST", body: JSON.stringify({ display_name: "A" }) }), /unknown outcome/u);
  assert.equal(link.runs, 1);
});

test("an attempt replay_only cannot find ran nowhere: it is reported not sent", async t => {
  const link = fakeLink(() => ({ status: 200, body: { revision: "r", value: {} } }));
  const { c } = client(t, link);
  await c.start();
  link.loseNextReply = true;
  await fetchJSON(c, "/api/owner", { method: "POST", body: { display_name: "B" } });
  link.receipts.clear();
  const answer = await fetchJSON(c, "/api/owner", { method: "POST", body: { display_name: "B" } });
  assert.equal(answer.status, 409);
  assert.equal(answer.body.code, "operation_not_sent");
});

test("large bodies and results travel as domain objects", async t => {
  const big = "x".repeat(INLINE_BODY_BYTES + 1024);
  const link = fakeLink((method, args) => method === "settings.owner.patch"
    ? { status: 200, body: { revision: "r", value: { length: args.note.length } } }
    : { status: 200, body_object: undefined, body: { ok: true } });
  const { c } = client(t, link);
  await c.start();
  const patched = await fetchJSON(c, "/api/owner", { method: "POST", body: { note: big } });
  assert.deepEqual(patched.body, { length: big.length });
  const upload = link.calls.find(call => call.op === "object.upload.begin");
  assert.equal(upload.params.purpose, "domain_args");
  const call = link.calls.find(call => call.params?.method === "settings.owner.patch").params;
  assert.ok(call.args_object && call.args === undefined, "the arguments are passed by reference");
  // A large result comes back as a domain object.
  const payload = Buffer.from(JSON.stringify({ providers: Array.from({ length: 50 }, (_, i) => ({ id: i })) }));
  const id = crypto.randomUUID();
  link.objects.set(id, { data: payload, purpose: "domain_result" });
  const reader = fakeLink(() => ({ status: 200, body_object: { object_id: id, size: payload.length, sha256: sha(payload), media_type: "application/json" } }));
  reader.objects.set(id, { data: payload });
  const second = client(t, reader).c;
  await second.start();
  const listed = await fetchJSON(second, "/api/email/providers");
  assert.equal(listed.body.providers.length, 50);
});

test("mail attachments are uploaded from owned local files as mail_send_attachment objects", async t => {
  const fileID = "22222222-2222-4222-8222-222222222222";
  const link = fakeLink((method, args) => ({ status: 200, body: { id: "draft1", version: 1, attachments: args.attachments } }));
  const { c } = client(t, link, { files: { [fileID]: { name: "a.txt", content: "attachment" } } });
  await c.start();
  const saved = await fetchJSON(c, "/api/email/drafts/draft1", { method: "PUT", body: { subject: "s", attachments: [{ local_file_id: fileID }] } });
  assert.equal(saved.status, 200);
  const upload = link.calls.find(call => call.op === "object.upload.begin");
  assert.equal(upload.params.purpose, "mail_send_attachment");
  const save = link.calls.find(call => call.params?.method === "mail.drafts.save");
  const object = save.params.args.attachments[0].object;
  assert.equal(object.purpose, "mail_send_attachment");
  assert.equal(object.size, Buffer.byteLength("attachment"));
  assert.equal(object.sha256, sha("attachment"));
  const rejected = await fetchJSON(c, "/api/email/drafts/draft1", { method: "PUT", body: { subject: "s", attachments: [{ local_file_id: fileID, path: "/etc/passwd" }] } });
  assert.equal(rejected.status, 400, "renderer paths are refused before any read or upload");
});

test("replay returns the original outcome of a named operation", async t => {
  const link = fakeLink(() => ({ text: "hello" }));
  const { c } = client(t, link);
  await c.start();
  const operationID = crypto.randomUUID();
  const body = { session_id: "s", request_id: "voice-1", language: "zh" };
  await assert.rejects(c.replay("speech.transcribe", body, { operationID }), (error) => error.code === "operation_not_found");
  await c.invoke("speech.transcribe", body, { operationID });
  const replayed = await c.replay("speech.transcribe", body, { operationID });
  assert.deepEqual(replayed, { state: "completed", operation_id: operationID, response: { status: 200, body: { text: "hello" } } });
  assert.equal(link.runs, 1);
});

test("an unfinished receiving-mode change is settled from its original request, never applied twice", async t => {
  const originalBody = { intake_enabled: true, expected_mailbox_version: 0 };
  const notApplied = { status: 409, body: { code: "email_intake_not_applied", provider: "gmail", provider_version: 1, retryable: false } };
  let effects = 0, reconciles = 0;
  const link = fakeLink((method, args) => {
    if (method === "mail.intake.reconcile") {
      reconciles++;
      assert.deepEqual(args.original, { ...originalBody, provider: "gmail" }, "the original change's own arguments");
      return { idempotency_key: args.idempotency_key, state: "completed", response: notApplied };
    }
    effects++;
    return { status: 200, body: { mailbox: { id: "box", version: 2 } } };
  });
  const { c, make } = client(t, link);
  await c.start();
  // The original attempt left only a pending receipt behind.
  const resource = "mail-provider:gmail:update";
  const original = c.mutations.prepare(resource, { operation: "mail.intake.update", params: { provider: "gmail" }, body: originalBody, installation_id: installationID },
    { deadline: Date.now() + 60000, authorizationRevision: 1, actionID: "00000000-0000-4000-8000-000000000001" });
  c.mutations.markAttempted(resource, original.operation_id);
  link.receipts.set(original.operation_id, { digest: JSON.stringify(["mail.intake.update", { ...originalBody, provider: "gmail" }]), pending: true });
  c.close();
  const restarted = make(); t.after(() => restarted.close());
  await restarted.start();
  for (let i = 0; i < 50 && restarted.mutations.read(resource)?.phase !== "terminal"; i++) await new Promise(resolve => setTimeout(resolve, 5));
  assert.equal(restarted.mutations.read(resource).phase, "terminal");
  assert.equal(effects, 0); assert.equal(reconciles, 1);
  // The original explanation is delivered before a later action, which is
  // then a new change.
  const body = JSON.stringify({ ...originalBody, expected_mailbox_version: 1 });
  const first = await restarted.fetch(`${ORIGIN}/api/email/providers/gmail`, { method: "PATCH", body, headers: { "x-sparkclaw-action-id": "00000000-0000-4000-8000-000000000002" } });
  assert.equal(first.status, 409); assert.equal((await first.json()).code, "email_intake_not_applied"); assert.equal(effects, 0);
  const next = await restarted.fetch(`${ORIGIN}/api/email/providers/gmail`, { method: "PATCH", body, headers: { "x-sparkclaw-action-id": "00000000-0000-4000-8000-000000000003" } });
  assert.equal(next.status, 200); assert.equal(effects, 1);
});

test("medicine routes become typed methods and keep their original answers", async t => {
  const calls = [];
  const link = { online: true, async request(op, params) {
    calls.push({ op, params: structuredClone(params) });
    assert.equal(op, "domain.call");
    if (params.method === "medicine.sync") return { result: { cursor: "c1", events: [] } };
    if (params.method === "medicine.items.update") return { result: { id: params.args.item_id, version: 2 } };
    if (params.method === "medicine.operations.get") throw new LinkError("not_found", "medicine_not_found", false, false, "medicine_not_found");
    if (params.method === "medicine.items.batch") throw new LinkError("conflict", "medicine_sync_reset_required", false, false, "medicine_sync_reset_required");
    if (params.method === "medicine.recognize") throw new LinkError("backend_unavailable", "模型暂时不可用", true, false, "medicine_recognition_unavailable");
    throw new Error(`unexpected ${params.method}`);
  } };
  const { c } = client(t, link);
  await c.start();
  const sync = await fetchJSON(c, "/api/medicine/sync", { method: "POST", body: { cursor: "", limit: 100 } });
  assert.deepEqual(sync.body, { cursor: "c1", events: [] });
  assert.deepEqual(calls[0].params, { domain: "medicine", method: "medicine.sync", args: { cursor: "", limit: 100 } }, "typed arguments, no HTTP envelope");
  const updated = await fetchJSON(c, "/api/medicine/items/med_1", { method: "PUT", body: { operation_id: "op1", base_version: 1, item: { name: "x" } } });
  assert.equal(updated.status, 200);
  const update = calls.find(call => call.params.method === "medicine.items.update").params;
  assert.deepEqual(update.args, { operation_id: "op1", base_version: 1, item: { name: "x" }, item_id: "med_1" });
  assert.match(update.idempotency_key, /^[a-f0-9-]{36}$/u, "changes carry an idempotency key");
  const missing = await fetchJSON(c, "/api/medicine/operations/op9");
  assert.equal(missing.status, 404); assert.equal(missing.body.code, "medicine_not_found");
  assert.deepEqual(calls.find(call => call.params.method === "medicine.operations.get").params.args, { operation_id: "op9" });
  const reset = await fetchJSON(c, "/api/medicine/items/batch", { method: "POST", body: { operation_id: "b", operation: "create", items: [] } });
  assert.equal(reset.status, 409); assert.equal(reset.body.code, "medicine_sync_reset_required");
  const recognized = await fetchJSON(c, "/api/medicine/recognize", { method: "POST", body: { image: "x", content_type: "image/jpeg" } });
  assert.equal(recognized.status, 503); assert.equal(recognized.body.code, "medicine_recognition_unavailable"); assert.equal(recognized.body.retryable, true);
  assert.ok(c.capabilities.operations.includes("medicine.items.batch") && c.capabilities.operations.includes("medicine.recognize"), "typed methods are reachable");
});

test("notification routes keep the revision envelope and the watermark-bound read-all", async t => {
  const calls = [];
  const link = { online: true, async request(op, params) {
    calls.push(structuredClone(params));
    if (params.method === "notifications.list") return { result: { revision: "7", value: { notifications: [{ id: "pn_1" }], unread_count: 1, watermark: "w1", next_cursor: "", watermark_limit: 500 } } };
    if (params.method === "notifications.read_all") return { result: { revision: "8", value: { updated: 1, unread_count: 0 } } };
    if (params.method === "notifications.read") throw new LinkError("not_found", "notification_not_found", false, false, "notification_not_found");
    throw new Error(`unexpected ${params.method}`);
  } };
  const { c } = client(t, link);
  await c.start();
  const listed = await fetchJSON(c, "/api/notifications?limit=20&after=pn_0");
  assert.deepEqual(listed.body, { notifications: [{ id: "pn_1" }], unread_count: 1, watermark: "w1", next_cursor: "", watermark_limit: 500 }, "the value is unwrapped as before");
  assert.deepEqual(calls[0].args, { limit: 20, after: "pn_0" });
  const all = await fetchJSON(c, "/api/notifications/read-all", { method: "POST", body: { watermark: "w1" } });
  assert.deepEqual(all.body, { updated: 1, unread_count: 0 });
  assert.match(calls[1].idempotency_key, /^[a-f0-9-]{36}$/u);
  const missing = await fetchJSON(c, "/api/notifications/pn_9/read", { method: "POST" });
  assert.equal(missing.status, 404); assert.equal(missing.body.code, "notification_not_found");
  assert.deepEqual(calls[2].args, { id: "pn_9" });
  await assert.rejects(c.fetch(`${ORIGIN}/api/notifications?owner_id=other`), /Query is invalid/u);
});

test("speech methods are typed and called by name; failures keep their reason", async t => {
  const calls = [];
  const link = { online: true, async request(op, params) {
    calls.push(structuredClone(params));
    if (params.method === "speech.session.open") return { result: { session_id: "speech_1", ready: { event: "ready" } } };
    if (params.method === "speech.session.frame") return { result: { accepted_sequence: params.args.sequence } };
    if (params.method === "speech.transcribe" && params.replay_only) return { result: { text: "original", request_id: "voice-1", session_id: "s" } };
    if (params.method === "speech.session.events") throw new LinkError("not_found", "speech_session_not_found", false, false, "speech_session_not_found");
    throw new Error(`unexpected ${params.method}`);
  } };
  const { c } = client(t, link);
  await c.start();
  assert.deepEqual(await c.invoke("speech.session.open", { session_id: "s", request_id: "r", language: "auto" }), { session_id: "speech_1", ready: { event: "ready" } });
  assert.match(calls[0].idempotency_key, /^[a-f0-9-]{36}$/u, "opening a session is a change");
  assert.deepEqual(calls[0].args, { session_id: "s", request_id: "r", language: "auto" });
  await c.invoke("speech.session.frame", { session_id: "speech_1", sequence: 1, pcm16: "AAAA" });
  assert.equal(calls[1].idempotency_key, undefined, "frames are idempotent by sequence");
  await assert.rejects(c.invoke("speech.session.events", { session_id: "gone", after: 0 }), (error) => error.code === "speech_session_not_found" && error.status === 404);
  const operationID = crypto.randomUUID();
  const replayed = await c.replay("speech.transcribe", { session_id: "s", request_id: "voice-1" }, { operationID });
  assert.equal(replayed.response.body.text, "original");
  assert.equal(calls.at(-1).idempotency_key, operationID);
  assert.ok(["speech.status", "speech.transcribe", "speech.session.events"].every(op => c.capabilities.operations.includes(op)));
});

test("browser fences and McDonald's sessions are typed; a browser reconciliation is a recorded change", async t => {
  const calls = [];
  const link = { online: true, async request(op, params) {
    calls.push(structuredClone(params));
    if (params.method === "browser.receipt") return { result: { fences: [] } };
    if (params.method === "browser.reconcile") return { result: { reconciled: true } };
    if (params.method === "mcd.session.status") throw new LinkError("backend_unavailable", "McDonald's ordering is not enabled", false, false, "mcd_selection_unavailable");
    throw new Error(`unexpected ${params.method}`);
  } };
  const { c } = client(t, link);
  await c.start();
  assert.deepEqual(await c.invoke("browser.receipt", {}), { fences: [] });
  assert.deepEqual(calls[0], { domain: "browser", method: "browser.receipt", args: {} });
  assert.deepEqual(await c.invoke("browser.reconcile", { command_id: "c1", digest: "d", outcome: "observed_completed" }), { reconciled: true });
  assert.match(calls[1].idempotency_key, /^[a-f0-9-]{36}$/u);
  assert.ok(c.mutations.read(`browser-reconcile:${installationID}:c1`), "the reconciliation is recorded before it is sent");
  await assert.rejects(c.invoke("mcd.session.status"), (error) => error.code === "mcd_selection_unavailable" && error.status === 503);
});
