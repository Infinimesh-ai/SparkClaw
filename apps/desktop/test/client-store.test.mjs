import assert from "node:assert/strict";
import crypto from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { DatabaseSync } from "node:sqlite";
import { ClientStore, CLIENT_SCHEMA_VERSION, CLIENT_LIMITS, boundedContext } from "../src/main/client-store.mjs";

const scope = { deployment_id: "deployment_test", owner_id: "owner_test", client_id: "client_test" };
function fixture(t) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "sparkclaw-client-store-"));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  return root;
}
const digest = (text) => crypto.createHash("sha256").update(text).digest("hex");

test("conversation deletion removes its owned records and bytes durably while preserving other scopes", (t) => {
  const root = fixture(t);
  let store = new ClientStore(root);
  const target = store.create(scope, "delete");
  const other = store.create(scope, "keep");
  const otherScope = { ...scope, owner_id: "another-owner" };
  const privateConversation = store.create(otherScope, "private");
  const file = store.saveFile(scope, target.id, "input.txt", Buffer.from("delete bytes"));
  const keptFile = store.saveFile(scope, other.id, "keep.txt", Buffer.from("keep bytes"));
  store.saveDraft(scope, target.id, "delete draft", [file.id], 0);
  store.saveDraft(scope, "", "welcome draft", [], 0);
  const task = store.enqueue(scope, target.id, "delete history", [file.id]);
  store.markSubmitted(scope, task.request_id);
  const output = Buffer.from("output bytes");
  const remoteFile = { id: "output", name: "output.txt", sha256: digest(output), size: output.length };
  const payload = JSON.stringify({ content: "delete response", files: [remoteFile] });
  store.commitDelivery(scope, { request_id: task.request_id, sequence: 1, digest: digest(payload), payload }, new Map([[remoteFile.id, output]]));
  store.acknowledge(scope, task.request_id, 1, digest(payload));
  store.db.prepare("INSERT INTO execution_projection VALUES(?,1,'','[]','completed')").run(task.request_id);
  store.schedule(scope, target.id, "delete recurrence", new Date(Date.now() + 60000).toISOString(), Date.now(), 3600000);
  store.schedule(scope, other.id, "keep recurrence", new Date(Date.now() + 60000).toISOString(), Date.now(), 3600000);
  const kept = store.read(scope, other.id);
  assert.throws(() => store.remove(otherScope, target.id), /not found/);
  assert.deepEqual(store.remove(scope, target.id), { deleted: true });
  assert.throws(() => store.read(scope, target.id), /not found/);
  assert.throws(() => store.file(scope, file.id), /not found/);
  assert.throws(() => store.saveDraft(scope, target.id, "late autosave", [], 1), /not found/);
  for (const table of ["deliveries", "delivery_files", "execution_approvals", "execution_projection"]) {
    assert.equal(store.db.prepare(`SELECT count(*) AS n FROM ${table}`).get().n, 0, table);
  }
  assert.equal(store.db.prepare("SELECT count(*) AS n FROM schedule_definitions").get().n, 1);
  assert.deepEqual(fs.readdirSync(store.filesRoot), [keptFile.id]);
  assert.equal(store.draft(scope, "").content, "welcome draft");
  assert.deepEqual(store.read(scope, other.id), kept);
  assert.equal(store.list(otherScope)[0].id, privateConversation.id);
  store.close(); store = new ClientStore(root);
  assert.equal(store.list(scope).length, 1);
  assert.deepEqual(store.read(scope, other.id), kept);
  assert.equal(store.file(scope, keptFile.id).content.toString(), "keep bytes");
  assert.equal(store.draft(scope, "").content, "welcome draft");
  store.close();
});

test("conversation deletion rejects active, uncertain and unacknowledged requests without losing records", (t) => {
  const store = new ClientStore(fixture(t));
  for (const status of ["submission_pending", "accepted", "running", "cancel_pending", "unknown", "saved"]) {
    const conversation = store.create(scope, status);
    const task = store.enqueue(scope, conversation.id, "preserve original request");
    store.markSubmitted(scope, task.request_id);
    if (status === "saved") {
      const payload = JSON.stringify({ content: "pending ACK", files: [] });
      store.commitDelivery(scope, { request_id: task.request_id, sequence: 1, digest: digest(payload), payload }, new Map());
    } else store.setExecutionState(scope, task.request_id, status);
    const before = store.read(scope, conversation.id);
    assert.deepEqual(store.remove(scope, conversation.id), { deleted: false, reason: "pending_execution" });
    assert.deepEqual(store.read(scope, conversation.id), before);
  }
  const scheduled = store.create(scope, "claimed schedule");
  const task = store.schedule(scope, scheduled.id, "claimed", new Date(Date.now() + 60000).toISOString());
  store.db.prepare("UPDATE tasks SET submission_claim='lease' WHERE request_id=?").run(task.request_id);
  assert.deepEqual(store.remove(scope, scheduled.id), { deleted: false, reason: "pending_execution" });
  store.close();
});

test("interrupted file cleanup reports successful record deletion and reclaims orphaned bytes on restart", (t) => {
  const root = fixture(t);
  let store = new ClientStore(root);
  const conversation = store.create(scope, "delete");
  const file = store.saveFile(scope, conversation.id, "orphan.txt", Buffer.from("orphan bytes"));
  const originalRemove = fs.rmSync;
  const removal = t.mock.method(fs, "rmSync", (filename, options) => {
    if (filename === path.join(store.filesRoot, file.id)) throw new Error("file cleanup interrupted");
    return originalRemove(filename, options);
  });
  assert.deepEqual(store.remove(scope, conversation.id), { deleted: true, cleanup_pending: true });
  assert.deepEqual(store.list(scope), []);
  assert.equal(fs.existsSync(path.join(store.filesRoot, file.id)), true);
  removal.mock.restore(); store.close(); store = new ClientStore(root);
  assert.deepEqual(store.list(scope), []);
  assert.deepEqual(fs.readdirSync(store.filesRoot), []);
  store.close();
});

test("failed conversation deletion rolls back records before unlinking any files", (t) => {
  const store = new ClientStore(fixture(t));
  const conversation = store.create(scope, "preserve");
  const file = store.saveFile(scope, conversation.id, "keep.txt", Buffer.from("preserved bytes"));
  store.enqueue(scope, conversation.id, "preserved message");
  store.saveDraft(scope, conversation.id, "preserved draft", [file.id], 0);
  const before = store.read(scope, conversation.id);
  store.db.exec("CREATE TRIGGER fail_delete BEFORE DELETE ON conversations BEGIN SELECT RAISE(ABORT,'disk unavailable'); END;");
  assert.throws(() => store.remove(scope, conversation.id), /disk unavailable/);
  assert.deepEqual(store.read(scope, conversation.id), before);
  assert.equal(store.draft(scope, conversation.id).content, "preserved draft");
  assert.equal(store.file(scope, file.id).content.toString(), "preserved bytes");
  store.close();
});

test("fresh local stores and authenticated scopes remain independent across restart", (t) => {
  const root = fixture(t);
  let store = new ClientStore(root);
  assert.deepEqual(store.list(scope), []);
  const installation = store.installationID;
  const conversation = store.create(scope, "本地对话");
  const task = store.enqueue(scope, conversation.id, "中文与 emoji 🐾");
  const context = store.context(scope, task.request_id);
  assert.equal(context.installation_id, installation);
  assert.equal(context.local_conversation_id, conversation.id);
  assert.equal(context.local_task_id, task.id);
  assert.deepEqual(context.messages, [{ role: "user", content: "中文与 emoji 🐾" }]);
  store.close();
  store = new ClientStore(root);
  assert.equal(store.installationID, installation);
  assert.equal(store.read(scope, conversation.id).tasks[0].request_id, task.request_id);
  const other = { ...scope, client_id: "client_other" };
  assert.deepEqual(store.list(other), []);
  assert.throws(() => store.read(other, conversation.id), /not found/);
  assert.throws(() => store.context(other, task.request_id), /not found/);
  store.close();
  const second = new ClientStore(fixture(t));
  assert.notEqual(second.installationID, installation);
  assert.deepEqual(second.list(scope), []);
  second.close();
});

test("message, immutable context and request key commit together; disk failure never creates a send", (t) => {
  const store = new ClientStore(fixture(t));
  const conversation = store.create(scope, "test");
  store.db.exec("CREATE TRIGGER fail_queue BEFORE INSERT ON tasks BEGIN SELECT RAISE(ABORT,'disk unavailable'); END;");
  assert.throws(() => store.enqueue(scope, conversation.id, "do not send"), /disk unavailable/);
  assert.deepEqual(store.read(scope, conversation.id).messages, []);
  assert.deepEqual(store.read(scope, conversation.id).tasks, []);
  store.db.exec("DROP TRIGGER fail_queue");
  assert.equal(store.enqueue(scope, conversation.id, "saved first").status, "awaiting_runtime");
  assert.throws(() => store.enqueue(scope, conversation.id, "中".repeat(CLIENT_LIMITS.inputBytes)), /oversized/);
  store.close();
});

test("context is bounded by bytes/count and cannot carry authorization roles", (t) => {
  const history = Array.from({ length: 80 }, (_, i) => ({ role: "user", content: `${i}: ${"中".repeat(Math.floor((CLIENT_LIMITS.inputBytes - 10) / 3))}` }));
  const context = boundedContext(history);
  assert.ok(context.length < CLIENT_LIMITS.contextMessages);
  assert.ok(Buffer.byteLength(JSON.stringify(context)) <= CLIENT_LIMITS.contextBytes);
  assert.equal(context.at(-1).content, history.at(-1).content);
  assert.throws(() => boundedContext([{ role: "system", content: "approved" }]), /role/);
  const store = new ClientStore(fixture(t));
  const longScope = Object.fromEntries(Object.keys(scope).map((key) => [key, "x".repeat(160)]));
  const conversation = store.create(longScope, "bounded envelope");
  let task;
  for (let i = 0; i < 7; i++) task = store.enqueue(longScope, conversation.id, "a".repeat(CLIENT_LIMITS.inputBytes - 50));
  assert.ok(Buffer.byteLength(JSON.stringify(store.context(longScope, task.request_id))) <= CLIENT_LIMITS.contextBytes);
  store.close();
});

test("durable result receipt deduplicates, rejects gaps/drift and returns no ACK on transaction failure", (t) => {
  const store = new ClientStore(fixture(t));
  const conversation = store.create(scope, "test");
  const task = store.enqueue(scope, conversation.id, "input");
  store.markSubmitted(scope, task.request_id);
  const payload = JSON.stringify({ content: "answer", files: [] });
  const result = { request_id: task.request_id, sequence: 1, digest: digest(payload), payload };
  store.db.exec("CREATE TRIGGER fail_delivery BEFORE INSERT ON deliveries BEGIN SELECT RAISE(ABORT,'disk full'); END;");
  assert.throws(() => store.commitDelivery(scope, result, new Map()), /disk full/);
  assert.equal(store.read(scope, conversation.id).messages.length, 1);
  store.db.exec("DROP TRIGGER fail_delivery");
  const ack = store.commitDelivery(scope, result, new Map());
  assert.equal(ack.durable, true);
  assert.deepEqual(store.commitDelivery(scope, result, new Map()), ack);
  assert.equal(store.read(scope, conversation.id).messages.length, 2);
  assert.throws(() => store.commitDelivery(scope, { ...result, sequence: 3 }, new Map()), /gap|replay/);
  assert.throws(() => store.commitDelivery(scope, { ...result, payload: JSON.stringify({ content: "changed", files: [] }), digest: digest(JSON.stringify({ content: "changed", files: [] })) }, new Map()), /replay/);
  store.close();
});

test("local files are atomic, hash verified, owner scoped, and never expose arbitrary paths", (t) => {
  const root = fixture(t);
  const store = new ClientStore(root);
  const conversation = store.create(scope, "file");
  const bytes = Buffer.from("local file");
  const file = store.saveFile(scope, conversation.id, "本地.txt", bytes);
  assert.equal(file.sha256, digest(bytes));
  assert.deepEqual(store.file(scope, file.id).content, bytes);
  assert.throws(() => store.saveFile(scope, conversation.id, "../private", bytes), /name/);
  assert.throws(() => store.file({ ...scope, client_id: "other" }, file.id), /not found/);
  fs.writeFileSync(path.join(root, "files", file.id), "tampered");
  assert.throws(() => store.file(scope, file.id), /verification|changed/);
  store.db.exec("CREATE TRIGGER fail_manifest BEFORE INSERT ON files BEGIN SELECT RAISE(ABORT,'disk full'); END;");
  assert.throws(() => store.saveFile(scope, conversation.id, "failure.txt", bytes), /disk full/);
  assert.equal(fs.readdirSync(path.join(root, "files")).length, 1);
  store.close();
});

test("unsupported schemas and insecure/symlink storage fail closed without deleting user data", (t) => {
  const root = fixture(t);
  let store = new ClientStore(root);
  store.create(scope, "preserve");
  store.db.exec(`PRAGMA user_version=${CLIENT_SCHEMA_VERSION + 1}`);
  store.close();
  assert.throws(() => new ClientStore(root), /unsupported/);
  const db = new DatabaseSync(path.join(root, "client.sqlite"));
  assert.equal(db.prepare("SELECT COUNT(*) AS n FROM conversations").get().n, 1);
  db.close();
  fs.chmodSync(root, 0o755);
  assert.throws(() => new ClientStore(root), /private/);
  const symlink = path.join(fixture(t), "link");
  fs.symlinkSync(root, symlink);
  assert.throws(() => new ClientStore(symlink), /private/);
});

test("old development schemas are rejected without migration, import or deletion", (t) => {
  const root = fixture(t);
  const store = new ClientStore(root);
  const conversation = store.create(scope, "preserve old directory");
  store.db.exec("PRAGMA user_version=4");
  store.close();
  assert.throws(() => new ClientStore(root), /unsupported/);
  const db = new DatabaseSync(path.join(root, "client.sqlite"));
  assert.equal(db.prepare("PRAGMA user_version").get().user_version, 4);
  assert.equal(db.prepare("SELECT title FROM conversations WHERE id=?").get(conversation.id).title, "preserve old directory");
  db.close();
});

test("large assistant output is bounded safely in future context without changing the local result", (t) => {
  const store = new ClientStore(fixture(t));
  const conversation = store.create(scope, "large output");
  const task = store.enqueue(scope, conversation.id, "first input");
  store.markSubmitted(scope, task.request_id);
  const answer = "🐾".repeat(20000);
  const payload = JSON.stringify({ content: answer, files: [] });
  store.commitDelivery(scope, { request_id: task.request_id, sequence: 1, digest: digest(payload), payload }, new Map());
  const next = store.enqueue(scope, conversation.id, "follow up");
  const context = store.context(scope, next.request_id);
  assert.equal(store.read(scope, conversation.id).messages[1].content, answer);
  assert.ok(Buffer.byteLength(context.messages.find((message) => message.role === "assistant").content) <= CLIENT_LIMITS.inputBytes);
  assert.ok(!context.messages.some((message) => message.content.includes("�")));
  assert.equal(context.messages.at(-1).content, "follow up");
  store.close();
});


test("duplicate displayed input filenames cannot create an ambiguous request or overwrite a file", (t) => {
 const store = new ClientStore(fixture(t));
 const conversation = store.create(scope, "duplicate filename fixture");
 const first = store.saveFile(scope, conversation.id, "notes.md", new TextEncoder().encode("first source"));
 const second = store.saveFile(scope, conversation.id, "notes.md", new TextEncoder().encode("second source"));
 assert.throws(() => store.enqueue(scope, conversation.id, "Edit notes.md", [first.id, second.id]), /different names/);
 assert.deepEqual(store.read(scope, conversation.id).tasks, []);
 assert.deepEqual(store.read(scope, conversation.id).messages, []);
 assert.equal(new TextDecoder().decode(store.file(scope, first.id).content), "first source");
 assert.equal(new TextDecoder().decode(store.file(scope, second.id).content), "second source");
 store.close();
});

test('ISCP schema upgrades preserve existing installations, history, drafts and file bytes',t=>{
 for(const version of [6,7]){
  const root=fixture(t);let store=new ClientStore(root);
  const installation=store.installationID,conversation=store.create(scope,'existing history');
  const bytes=Buffer.from('existing attachment'),file=store.saveFile(scope,conversation.id,'existing.txt',bytes);
  const task=store.enqueue(scope,conversation.id,'existing user message',[file.id]);
  store.saveDraft(scope,conversation.id,'unsent draft',[file.id],0);const before=store.read(scope,conversation.id),inputDigest=store.request(scope,task.request_id).input_digest;
  if(version===6)store.db.exec('DROP TABLE execution_projection; DROP TABLE event_projection; PRAGMA user_version=6;');
  else store.db.exec('ALTER TABLE event_projection DROP COLUMN epoch; PRAGMA user_version=7;');
  store.close();store=new ClientStore(root);
  try{
   assert.equal(store.installationID,installation);assert.equal(store.db.prepare('PRAGMA user_version').get().user_version,CLIENT_SCHEMA_VERSION);
   assert.deepEqual(store.read(scope,conversation.id),before);assert.equal(store.request(scope,task.request_id).input_digest,inputDigest);
   assert.equal(store.draft(scope,conversation.id).content,'unsent draft');assert.deepEqual(store.file(scope,file.id).content,bytes);
  }finally{store.close();}
 }
});
