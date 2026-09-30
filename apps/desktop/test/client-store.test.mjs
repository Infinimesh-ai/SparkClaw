import assert from "node:assert/strict";
import crypto from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { DatabaseSync } from "node:sqlite";
import { ClientStore, CLIENT_LIMITS, boundedContext } from "../src/main/client-store.mjs";

const scope = { deployment_id: "deployment_test", owner_id: "owner_test", client_id: "client_test" };
function fixture(t) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "sparkclaw-client-store-"));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  return root;
}
const digest = (text) => crypto.createHash("sha256").update(text).digest("hex");

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
  const history = Array.from({ length: 80 }, (_, i) => ({ role: "user", content: `${i}: ${"中".repeat(5000)}` }));
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
  const result = { request_id: task.request_id, sequence: 1, digest: digest("answer"), content: "answer" };
  store.db.exec("CREATE TRIGGER fail_delivery BEFORE INSERT ON deliveries BEGIN SELECT RAISE(ABORT,'disk full'); END;");
  assert.throws(() => store.commitResult(scope, result), /disk full/);
  assert.equal(store.read(scope, conversation.id).messages.length, 1);
  store.db.exec("DROP TRIGGER fail_delivery");
  const ack = store.commitResult(scope, result);
  assert.equal(ack.durable, true);
  assert.deepEqual(store.commitResult(scope, result), ack);
  assert.equal(store.read(scope, conversation.id).messages.length, 2);
  assert.throws(() => store.commitResult(scope, { ...result, sequence: 3 }), /gap/);
  assert.throws(() => store.commitResult(scope, { ...result, content: "changed", digest: digest("changed") }), /replay/);
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
  assert.throws(() => store.file(scope, file.id), /verification/);
  store.db.exec("CREATE TRIGGER fail_manifest BEFORE INSERT ON files BEGIN SELECT RAISE(ABORT,'disk full'); END;");
  assert.throws(() => store.saveFile(scope, conversation.id, "failure.txt", bytes), /disk full/);
  assert.equal(fs.readdirSync(path.join(root, "files")).length, 1);
  store.close();
});

test("newer schemas and insecure/symlink storage fail closed without deleting user data", (t) => {
  const root = fixture(t);
  let store = new ClientStore(root);
  store.create(scope, "preserve");
  store.db.exec("PRAGMA user_version=2");
  store.close();
  assert.throws(() => new ClientStore(root), /newer/);
  const db = new DatabaseSync(path.join(root, "client.sqlite"));
  assert.equal(db.prepare("SELECT COUNT(*) AS n FROM conversations").get().n, 1);
  db.close();
  fs.chmodSync(root, 0o755);
  assert.throws(() => new ClientStore(root), /private/);
  const symlink = path.join(fixture(t), "link");
  fs.symlinkSync(root, symlink);
  assert.throws(() => new ClientStore(symlink), /private/);
});
