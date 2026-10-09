import assert from "node:assert/strict";
import crypto from "node:crypto";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { LANMailClient } from "../src/main/lan-mail-client.mjs";

const origin = "https://local-gateway.test";
const localID = crypto.randomUUID();
const content = Buffer.from("desktop workspace bytes");
const digest = crypto.createHash("sha256").update(content).digest("hex");
const object = { object_id: crypto.randomUUID(), version: 1, purpose: "mail_send_attachment", name: "local.txt", size: content.length, sha256: digest, expires_at: new Date(Date.now() + 86400000).toISOString() };
const attachment = { local_file_id: localID, object, name: object.name, size_bytes: object.size, sha256: `sha256:${digest}` };
const save = { method: "POST", body: JSON.stringify({ id: "draft", expected_version: 0, attachments: [{ local_file_id: localID }] }) };
const send = { method: "POST", body: JSON.stringify({ expected_version: 1, idempotency_key: "send-once" }) };
const json = value => new Response(JSON.stringify(value), { headers: { "content-type": "application/json" } });
async function fixture(t) {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), "lan-mail-test-"));
  t.after(() => fs.rm(root, { recursive: true, force: true }));
  const state = { generation: 1, reads: 0, uploads: 0, sends: 0, saves: 0, reconcile: 0, allowed: true, missing: false, lost: false, draft: { id: "draft", version: 1, state: "draft", attachments: [attachment] } };
  const options = { root, scope: { deployment_id: "d", owner_id: "o", client_id: "c", installation_id: crypto.randomUUID() }, generation: () => state.generation, ready: () => true,
    readLocalFile(scope, id) { state.reads++; assert.deepEqual(scope, { deployment_id: "d", owner_id: "o", client_id: "c" }); assert.equal(id, localID); if (state.missing) throw new Error("deleted"); return { name: object.name, size: content.length, sha256: digest, content: Buffer.from(content) }; } };
  const next = async (raw, init) => {
    const url = new URL(raw);
    if (url.pathname.endsWith("compose-capabilities")) return json({ compose: true, workspace_attachments: state.allowed });
    if (url.pathname === "/api/v1/mail/attachments") {
      state.uploads++; assert.deepEqual(init.body, content); assert.equal(new Headers(init.headers).get("x-sparkclaw-digest"), digest); return json(object);
    }
    if (url.pathname.endsWith("/send")) { state.sends++; state.draft = { ...state.draft, state: "sent" }; if (state.lost) throw new Error("connection lost after provider effect"); return json(state.draft); }
    if (url.pathname.endsWith("/reconcile")) { state.reconcile++; return json(state.draft); }
    if (init.method === "POST") { state.saves++; const body = JSON.parse(init.body); assert.deepEqual(body.attachments, [{ local_file_id: localID, object }]); if (state.lost) throw new Error("save response lost"); return json(state.draft); }
    return json(state.draft);
  };
  return { state, options, next, client: new LANMailClient(options) };
}

test("LAN saves only main-verified local IDs and sends the reviewed bytes without reupload", async t => {
  const f = await fixture(t);
  assert.equal((await f.client.fetch(origin + "/api/email/drafts", save, f.next)).status, 200);
  assert.equal(f.state.reads, 1); assert.equal(f.state.uploads, 1); assert.equal(f.state.saves, 1);
  assert.equal((await f.client.fetch(origin + "/api/email/drafts/draft/send", send, f.next)).status, 200);
  assert.equal(f.state.reads, 2); assert.equal(f.state.uploads, 1); assert.equal(f.state.sends, 1);
});

test("LAN rejects forged renderer objects/bytes/paths and unqualified access before reading files", async t => {
  const f = await fixture(t);
  for (const attachments of [[{ local_file_id: localID, object }], [{ path: "/outside" }], [{ bytes: content.toString("base64") }]]) {
    const response = await f.client.fetch(origin + "/api/email/drafts", { method: "POST", body: JSON.stringify({ id: "draft", attachments }) }, f.next);
    assert.equal(response.status, 400);
  }
  for (const suffix of ["?ignored=1", "?draft=other"]) assert.equal((await f.client.fetch(origin + "/api/email/drafts" + suffix, save, f.next)).status, 400);
  assert.equal((await f.client.fetch(origin + "/api/email/%64rafts", save, f.next)).status, 400);
  f.state.allowed = false;
  assert.equal((await f.client.fetch(origin + "/api/email/drafts", save, f.next)).status, 409);
  assert.equal(f.state.reads, 0); assert.equal(f.state.uploads, 0); assert.equal(f.state.saves, 0);
});

test("LAN lost send survives process recreation and receipt reconciliation never reads deleted sources or resends", async t => {
  const f = await fixture(t); f.state.lost = true;
  await assert.rejects(f.client.fetch(origin + "/api/email/drafts/draft/send", send, f.next), /lost/);
  f.state.missing = true;
  const restarted = new LANMailClient(f.options), before = f.state.reads;
  const blocked = await restarted.fetch(origin + "/api/email/drafts/draft/send", send, f.next);
  assert.equal((await blocked.json()).code, "email_send_unknown");
  const reconciled = await restarted.fetch(origin + "/api/email/drafts/draft/reconcile", { method: "POST", body: "{}" }, f.next);
  assert.equal((await reconciled.json()).state, "sent");
  assert.equal(f.state.reads, before); assert.equal(f.state.uploads, 0); assert.equal(f.state.sends, 1); assert.equal(f.state.reconcile, 1);
});

test("LAN unknown save requires explicit draft reload before a new save can upload again", async t => {
  const f = await fixture(t); f.state.lost = true;
  await assert.rejects(f.client.fetch(origin + "/api/email/drafts", save, f.next), /lost/);
  const restarted = new LANMailClient(f.options);
  assert.equal((await restarted.fetch(origin + "/api/email/drafts", save, f.next)).status, 409);
  assert.equal(f.state.uploads, 1);
  assert.equal((await restarted.fetch(origin + "/api/email/drafts?draft=draft", { method: "GET" }, f.next)).status, 200);
  f.state.lost = false;
  assert.equal((await restarted.fetch(origin + "/api/email/drafts", save, f.next)).status, 200);
});

test("LAN explicit unknown responses preserve the send journal without another read or effect", async t => {
  for (const variant of ["code", "error_code", "header", "proxy"]) {
    const f = await fixture(t);
    const next = async (raw, init) => {
      const response = await f.next(raw, init);
      if (!raw.endsWith("/send")) return response;
      return new Response(JSON.stringify(variant === "header" ? {} : { [variant]: "operation_outcome_unknown" }), {
        status: variant === "proxy" ? 502 : 409,
        headers: variant === "header" ? { "x-sparkclaw-error-code": "operation_outcome_unknown" } : {},
      });
    };
    await f.client.fetch(origin + "/api/email/drafts/draft/send", send, next);
    const before = f.state.reads;
    const restarted = new LANMailClient(f.options);
    const blocked = await restarted.fetch(origin + "/api/email/drafts/draft/send", send, next);
    assert.equal((await blocked.json()).code, "email_send_unknown");
    assert.equal(f.state.reads, before); assert.equal(f.state.uploads, 0); assert.equal(f.state.sends, 1);
  }
});

test("LAN changed local source or authentication fences send/save before provider effects", async t => {
  const f = await fixture(t); f.state.missing = true;
  assert.equal((await f.client.fetch(origin + "/api/email/drafts/draft/send", send, f.next)).status, 409);
  assert.equal(f.state.sends, 0);
  f.state.missing = false;
  const next = async (url, options) => { const response = await f.next(url, options); if (url.endsWith("/attachments")) f.state.generation++; return response; };
  await assert.rejects(f.client.fetch(origin + "/api/email/drafts", save, next), /authentication changed/);
  assert.equal(f.state.saves, 0);
});
