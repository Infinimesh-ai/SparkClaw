import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { DatabaseSync } from "node:sqlite";
import { ClientStore, CLIENT_SCHEMA_VERSION } from "../src/main/client-store.mjs";

const scope = { deployment_id: "deployment", owner_id: "owner", client_id: "client" };
function fixture(t) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "sparkclaw-conversation-titles-"));
  let store = new ClientStore(root);
  t.after(() => { store.close(); fs.rmSync(root, { recursive: true, force: true }); });
  return { root, get store() { return store; }, restart() { store.close(); store = new ClientStore(root); } };
}
const title = (store, id, identity = scope) => store.list(identity).find(row => row.id === id)?.title;

test("the first saved question titles Chinese and English conversations durably before network submission", t => {
  const f = fixture(t);
  for (const [placeholder, question, expected] of [
    ["新对话", "  如何修复\n 对话标题？  ", "如何修复 对话标题？"],
    ["New conversation", "  How do I rename\n\ta conversation?  ", "How do I rename a conversation?"],
    ["New conversation", "🚀".repeat(43), "🚀".repeat(42) + "..."],
  ]) {
    const conversation = f.store.create(scope, placeholder, false, true);
    f.store.saveDraft(scope, conversation.id, question, [], 0);
    assert.equal(title(f.store, conversation.id), placeholder);
    const queued = f.store.enqueueDraft(scope, conversation.id, conversation.id, 1);
    assert.equal(title(f.store, conversation.id), expected);
    assert.equal(queued.task.status, "awaiting_runtime");
    assert.equal(f.store.request(scope, queued.task.request_id).explicitly_submitted, 0);
    assert.equal(f.store.context(scope, queued.task.request_id).messages.at(-1).content, question.trim());
    f.restart();
    assert.equal(title(f.store, conversation.id), expected);
    f.store.enqueue(scope, conversation.id, "Follow-up question");
    assert.equal(title(f.store, conversation.id), expected);
  }
});

test("explicit titles and manual renames remain authoritative even when they equal a default label", t => {
  const f = fixture(t);
  for (const label of ["My project", "新对话", "New conversation"]) {
    const manual = f.store.create(scope, label);
    f.store.enqueue(scope, manual.id, "First question");
    assert.equal(title(f.store, manual.id), label);
    const pending = f.store.create(scope, "New conversation", false, true);
    f.store.rename(scope, pending.id, label);
    f.restart();
    f.store.enqueue(scope, pending.id, "First question after manual rename");
    assert.equal(title(f.store, pending.id), label);
  }
  const generated = f.store.create(scope, "新对话", false, true);
  f.store.enqueue(scope, generated.id, "Question-based title");
  f.store.rename(scope, generated.id, "My chosen title");
  f.store.enqueue(scope, generated.id, "Later question");
  assert.equal(title(f.store, generated.id), "My chosen title");
});

test("title generation rolls back with a failed draft submission and stays within its authenticated scope", t => {
  const f = fixture(t), conversation = f.store.create(scope, "新对话", false, true);
  f.store.saveDraft(scope, conversation.id, "A durable question", [], 0);
  f.store.db.exec("CREATE TRIGGER fail_clear BEFORE UPDATE ON drafts BEGIN SELECT RAISE(ABORT,'disk full'); END;");
  assert.throws(() => f.store.enqueueDraft(scope, conversation.id, conversation.id, 1), /disk full/);
  assert.equal(title(f.store, conversation.id), "新对话");
  assert.equal(f.store.read(scope, conversation.id).messages.length, 0);
  assert.equal(f.store.read(scope, conversation.id).tasks.length, 0);
  assert.equal(f.store.draft(scope, conversation.id).content, "A durable question");
  f.store.db.exec("DROP TRIGGER fail_clear");
  assert.throws(() => f.store.enqueue({ ...scope, owner_id: "other" }, conversation.id, "Wrong owner"), /not found/);
  f.store.enqueueDraft(scope, conversation.id, conversation.id, 1);
  assert.equal(title(f.store, conversation.id), "A durable question");
});

test("schema 9 default titles are backfilled once without changing existing history, files, drafts or timestamps", t => {
  const f = fixture(t), installation = f.store.installationID;
  const otherScope = { ...scope, client_id: "other" };
  const old = [f.store.create(scope, "新对话"), f.store.create(otherScope, "New conversation")];
  for (const [index, row] of old.entries()) {
    const identity = index ? otherScope : scope;
    f.store.enqueue(identity, row.id, `Original question ${index}`);
    f.store.enqueue(identity, row.id, "Later question must not become the title");
  }
  const manual = f.store.create(scope, "My chosen title");
  f.store.enqueue(scope, manual.id, "Keep manual title");
  const empty = f.store.create(scope, "新对话");
  const hidden = f.store.create(scope, "New conversation", true);
  f.store.enqueue(scope, hidden.id, "Hidden schedule question");
  const file = f.store.saveFile(scope, old[0].id, "note.txt", Buffer.from("original bytes"));
  f.store.saveDraft(scope, old[0].id, "Unsent draft", [file.id], 0);
  const content = f.store.read(scope, old[0].id), draft = f.store.draft(scope, old[0].id);
  const timestamps = f.store.db.prepare("SELECT id,created_at,updated_at FROM conversations ORDER BY id").all();
  const contexts = f.store.db.prepare("SELECT request_id,input_digest,context_json FROM tasks ORDER BY rowid").all();
  f.store.db.exec("ALTER TABLE conversations DROP COLUMN title_pending; PRAGMA user_version=9;");
  f.restart();
  assert.equal(f.store.db.prepare("PRAGMA user_version").get().user_version, CLIENT_SCHEMA_VERSION);
  assert.equal(f.store.installationID, installation);
  assert.equal(title(f.store, old[0].id), "Original question 0");
  assert.equal(title(f.store, old[1].id, otherScope), "Original question 1");
  assert.equal(title(f.store, manual.id), "My chosen title");
  assert.equal(title(f.store, empty.id), "新对话");
  assert.equal(f.store.db.prepare("SELECT title FROM conversations WHERE id=?").get(hidden.id).title, "New conversation");
  assert.deepEqual(f.store.read(scope, old[0].id), content);
  assert.deepEqual(f.store.draft(scope, old[0].id), draft);
  assert.deepEqual(f.store.db.prepare("SELECT id,created_at,updated_at FROM conversations ORDER BY id").all(), timestamps);
  assert.deepEqual(f.store.db.prepare("SELECT request_id,input_digest,context_json FROM tasks ORDER BY rowid").all(), contexts);
  assert.equal(f.store.file(scope, file.id).content.toString(), "original bytes");
  f.store.enqueue(scope, empty.id, "First question after upgrade");
  f.store.rename(scope, old[0].id, "新对话");
  f.restart();
  assert.equal(title(f.store, empty.id), "First question after upgrade");
  assert.equal(title(f.store, old[0].id), "新对话");
});

test("failed title migration preserves schema 9 and permits a clean retry", t => {
  const f = fixture(t), old = f.store.create(scope, "新对话");
  f.store.enqueue(scope, old.id, "Preserved first question");
  f.store.db.exec("ALTER TABLE conversations DROP COLUMN title_pending; PRAGMA user_version=9;");
  f.store.db.exec("CREATE TRIGGER fail_title BEFORE UPDATE ON conversations BEGIN SELECT RAISE(ABORT,'disk full'); END;");
  assert.throws(() => new ClientStore(f.root), /disk full/);
  const db = new DatabaseSync(path.join(f.root, "client.sqlite"));
  try {
    assert.equal(db.prepare("PRAGMA user_version").get().user_version, 9);
    assert.equal(db.prepare("SELECT title FROM conversations WHERE id=?").get(old.id).title, "新对话");
    assert.equal(db.prepare("PRAGMA table_info(conversations)").all().some(row => row.name === "title_pending"), false);
    db.exec("DROP TRIGGER fail_title");
  } finally { db.close(); }
  f.restart();
  assert.equal(title(f.store, old.id), "Preserved first question");
});
