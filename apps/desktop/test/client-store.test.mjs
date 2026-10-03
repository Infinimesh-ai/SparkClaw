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
  store.db.exec("PRAGMA user_version=5");
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

test("schema 1 upgrade preserves installation and saved requests without enabling replay", (t) => {
  const root = fixture(t);
  const databasePath = path.join(root, "client.sqlite");
  fs.closeSync(fs.openSync(databasePath, "wx", 0o600));
  const db = new DatabaseSync(databasePath);
  const installation = crypto.randomUUID();
  const conversation = crypto.randomUUID();
  const task = crypto.randomUUID();
  const request = crypto.randomUUID();
  const envelope = JSON.stringify({ schema_version: 1, ...scope, installation_id: installation,
    local_conversation_id: conversation, local_task_id: task, request_id: request, messages: [{ role: "user", content: "saved before upgrade" }] });
  db.exec(`CREATE TABLE metadata(key TEXT PRIMARY KEY,value TEXT NOT NULL);
    CREATE TABLE conversations(id TEXT PRIMARY KEY,scope TEXT NOT NULL,title TEXT NOT NULL,created_at TEXT NOT NULL,updated_at TEXT NOT NULL);
    CREATE TABLE messages(id TEXT PRIMARY KEY,conversation_id TEXT NOT NULL REFERENCES conversations(id),role TEXT NOT NULL,content TEXT NOT NULL,created_at TEXT NOT NULL);
    CREATE TABLE tasks(id TEXT PRIMARY KEY,conversation_id TEXT NOT NULL REFERENCES conversations(id),request_id TEXT UNIQUE NOT NULL,input_digest TEXT NOT NULL,context_json TEXT NOT NULL,status TEXT NOT NULL,created_at TEXT NOT NULL);
    CREATE TABLE files(id TEXT PRIMARY KEY,conversation_id TEXT NOT NULL REFERENCES conversations(id),name TEXT NOT NULL,sha256 TEXT NOT NULL,size INTEGER NOT NULL,created_at TEXT NOT NULL);
    CREATE TABLE deliveries(request_id TEXT NOT NULL REFERENCES tasks(request_id),sequence INTEGER NOT NULL,digest TEXT NOT NULL,PRIMARY KEY(request_id,sequence));
    PRAGMA user_version=1;`);
  db.prepare("INSERT INTO metadata VALUES('installation_id',?)").run(installation);
  db.prepare("INSERT INTO conversations VALUES(?,?,?,?,?)").run(conversation, JSON.stringify(Object.values(scope)), "prior conversation", "", "");
  db.prepare("INSERT INTO tasks VALUES(?,?,?,?,?,?,?)").run(task, conversation, request, digest(envelope), envelope, "awaiting_runtime", "");
  db.close();
  const store = new ClientStore(root);
  assert.equal(store.installationID, installation);
  assert.equal(store.request(scope, request).context_json, envelope);
  assert.equal(store.request(scope, request).explicitly_submitted, 0);
  assert.deepEqual(store.pending(scope), []);
  assert.equal(store.db.prepare("PRAGMA user_version").get().user_version, 4);
  store.close();
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
