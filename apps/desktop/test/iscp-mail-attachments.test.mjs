import assert from "node:assert/strict";
import crypto from "node:crypto";
import test from "node:test";
import { prepareMailAttachments, localAttachmentErrorResponse, LocalMailAttachmentError } from "../src/main/iscp-mail-attachments.mjs";
const id = crypto.randomUUID();
test("pre-send local rejection preserves the renderer API error code", async () => {
  const response = localAttachmentErrorResponse(new LocalMailAttachmentError("Changed before send", "email_attachment_changed"));
  assert.equal(response.status, 409);
  // The shared renderer API client reads `code`; error_code alone silently
  // turned a proven pre-send rejection into an unknown-send UI fence.
  assert.equal((await response.json()).code, "email_attachment_changed");
});
const bytes = Buffer.from("local desktop data");
const hash = crypto.createHash("sha256").update(bytes).digest("hex");
const object = { object_id: "object-local", version: 1, size: bytes.length, sha256: hash, purpose: "mail_send_attachment", name: "local.txt" };
const attachment = { local_file_id: id, name: object.name, size_bytes: bytes.length, sha256: `sha256:${hash}`, object };
const save = attachments => ({ operation: "mail.drafts.save", body: { id: "draft", attachments } });
const send = { operation: "mail.drafts.send", params: { draft: "draft" }, body: { expected_version: 3 } };
function fixture(t) {
  const reads = [], uploads = [];
  const transport = {
    generation: 1, objectScopeKey: "same-scope", state: "transport_ready", canSendMailAttachments: () => true,
    capabilities: { binding: { deployment_id: "d", owner_id: "o", client_id: "c", grant: "never-to-store" } },
    readLocalFile(scope, fileID) { reads.push({ scope, fileID }); return { name: "local.txt", size: bytes.length, sha256: hash, content: Buffer.from(bytes) }; },
    objects: { async upload(value, manifest) { uploads.push({ value: Buffer.from(value), manifest }); return object; } },
    async invoke(operation) { assert.equal(operation, "mail.drafts.list"); return { id: "draft", version: 3, state: "draft", attachments: [attachment] }; },
  };
  return { transport, reads, uploads };
}

test("main converts only owned local UUIDs into verified uploaded object references", async t => {
  const f = fixture(t), request = save([{ local_file_id: id }]);
  const value = await prepareMailAttachments(f.transport, request);
  assert.deepEqual(value.body.attachments, [{ local_file_id: id, object }]);
  assert.deepEqual(request.body.attachments, [{ local_file_id: id }]);
  assert.deepEqual(f.reads, [{ scope: { deployment_id: "d", owner_id: "o", client_id: "c" }, fileID: id }]);
  assert.deepEqual(f.uploads, [{ value: bytes, manifest: { purpose: "mail_send_attachment", name: "local.txt" } }]);
});

test("renderer paths, object references, metadata and duplicate IDs fail without reading or uploading", async t => {
  for (const inputs of [[{ path: "reports/local.txt" }], [{ local_file_id: id, object }], [{ local_file_id: "../outside" }], [{ local_file_id: id, name: "forged" }]]) {
    const f = fixture(t);
    await assert.rejects(prepareMailAttachments(f.transport, save(inputs)), { code: "email_attachment_invalid" });
    assert.equal(f.reads.length, 0); assert.equal(f.uploads.length, 0);
  }
  const f = fixture(t);
  await assert.rejects(prepareMailAttachments(f.transport, save([{ local_file_id: id }, { local_file_id: id }])), { code: "email_attachment_invalid" });
  assert.equal(f.uploads.length, 0);
});

test("denied or stale local-attachment capability leaks no local bytes", async t => {
  const f = fixture(t); f.transport.canSendMailAttachments = () => false;
  await assert.rejects(prepareMailAttachments(f.transport, save([{ local_file_id: id }])));
  await assert.rejects(prepareMailAttachments(f.transport, send));
  assert.equal(f.reads.length, 0); assert.equal(f.uploads.length, 0);
});

test("send rereads the reviewed local source without uploading and rejects deleted/changed sources before intent", async t => {
  const f = fixture(t);
  assert.equal(await prepareMailAttachments(f.transport, send), send);
  assert.equal(f.reads.length, 1); assert.equal(f.uploads.length, 0);
  for (const patch of [{ name: "changed.txt" }, { size: bytes.length + 1 }, { sha256: "f".repeat(64) }]) {
    f.transport.readLocalFile = () => ({ ...patch, content: Buffer.from(bytes) });
    await assert.rejects(prepareMailAttachments(f.transport, send), { code: "email_attachment_changed" });
  }
  f.transport.readLocalFile = () => { throw new Error("deleted"); };
  await assert.rejects(prepareMailAttachments(f.transport, send), { code: "email_attachment_changed" });
});

test("authentication changes during upload fence the save before dispatch", async t => {
  const f = fixture(t);
  f.transport.objects.upload = async () => { f.transport.objectScopeKey = "another-owner"; return object; };
  await assert.rejects(prepareMailAttachments(f.transport, save([{ local_file_id: id }])), /authentication changed/);
});

test("unknown draft reconciliation does not read deleted attachments or upload again", async t => {
  const f = fixture(t);
  f.transport.invoke = async () => ({ id: "draft", version: 3, state: "unknown", attachments: [attachment] });
  f.transport.readLocalFile = () => { throw new Error("must not read"); };
  assert.equal(await prepareMailAttachments(f.transport, send), send);
  assert.equal(f.uploads.length, 0);
});

test("count/aggregate-byte limits are checked before the first upload", async t => {
  const f = fixture(t);
  await assert.rejects(prepareMailAttachments(f.transport, save(Array.from({ length: 6 }, () => ({ local_file_id: crypto.randomUUID() })))));
  assert.equal(f.reads.length, 0);
  f.transport.readLocalFile = () => ({ name: "big.bin", size: 6 * 1024 * 1024, sha256: hash, content: Buffer.alloc(1) });
  await assert.rejects(prepareMailAttachments(f.transport, save([{ local_file_id: id }, { local_file_id: crypto.randomUUID() }])));
  assert.equal(f.uploads.length, 0);
});
