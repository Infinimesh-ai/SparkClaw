import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { ClientStore, CLIENT_LIMITS } from "../src/main/client-store.mjs";
const scope = { deployment_id: "deployment", owner_id: "owner", client_id: "client" };
function fixture(t) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "sparkclaw-workbench-drafts-"));
  let store = new ClientStore(root);
  t.after(() => { store.close(); fs.rmSync(root, { recursive: true, force: true }); });
  return { get store() { return store; }, restart() { store.close(); store = new ClientStore(root); } };
}

test("welcome and conversation drafts preserve whitespace and selected owned files across restart without enqueueing", (t) => {
  const f = fixture(t); const conversation = f.store.create(scope, "draft");
  const file = f.store.saveFile(scope, conversation.id, "note.txt", Buffer.from("saved"));
  assert.deepEqual(f.store.draft(scope, ""), { content: "", local_file_ids: [], revision: 0 });
  f.store.saveDraft(scope, "", "  welcome\n", [], 0);
  f.store.saveDraft(scope, conversation.id, "  unfinished\n", [file.id], 0);
  f.restart();
  assert.deepEqual(f.store.draft(scope, conversation.id), { content: "  unfinished\n", local_file_ids: [file.id], revision: 1 });
  assert.equal(f.store.draft(scope, "").content, "  welcome\n");
  assert.equal(f.store.read(scope, conversation.id).tasks.length, 0);
  assert.equal(f.store.read(scope, conversation.id).messages.length, 0);
  assert.throws(() => f.store.draft({ ...scope, owner_id: "other" }, conversation.id), /not found/);
  assert.equal(f.store.draft({ ...scope, owner_id: "other" }, "").content, "");
});

test("CAS, bounds, scope and attachment checks preserve the prior durable draft on failure", (t) => {
  const f = fixture(t); const one = f.store.create(scope, "one"); const two = f.store.create(scope, "two");
  const file = f.store.saveFile(scope, two.id, "private.txt", Buffer.from("private"));
  f.store.saveDraft(scope, one.id, "saved", [], 0);
  assert.throws(() => f.store.saveDraft(scope, one.id, "stale", [], 0), /another editor/);
  assert.throws(() => f.store.saveDraft(scope, one.id, "x".repeat(CLIENT_LIMITS.inputBytes + 1), [], 1), /oversized/);
  assert.throws(() => f.store.saveDraft(scope, one.id, "bad attachment", [file.id], 1), /not in this conversation/);
  assert.throws(() => f.store.saveDraft(scope, "", "bad home file", [file.id], 0), /not in this conversation/);
  f.store.db.exec("CREATE TRIGGER fail_draft BEFORE UPDATE ON drafts BEGIN SELECT RAISE(ABORT,'disk full'); END;");
  assert.throws(() => f.store.saveDraft(scope, one.id, "not durable", [], 1), /disk full/);
  assert.deepEqual(f.store.draft(scope, one.id), { content: "saved", local_file_ids: [], revision: 1 });
});

test("enqueue consumes exactly the durable revision atomically and stale autosave cannot resurrect it", (t) => {
  const f = fixture(t); const conversation = f.store.create(scope, "atomic send");
  f.store.saveDraft(scope, conversation.id, "send once", [], 0);
  f.store.db.exec("CREATE TRIGGER fail_clear BEFORE UPDATE ON drafts BEGIN SELECT RAISE(ABORT,'disk full'); END;");
  assert.throws(() => f.store.enqueueDraft(scope, conversation.id, conversation.id, 1), /disk full/);
  assert.equal(f.store.read(scope, conversation.id).tasks.length, 0);
  assert.equal(f.store.read(scope, conversation.id).messages.length, 0);
  assert.equal(f.store.draft(scope, conversation.id).content, "send once");
  f.store.db.exec("DROP TRIGGER fail_clear");
  const { task, draft } = f.store.enqueueDraft(scope, conversation.id, conversation.id, 1);
  assert.equal(task.status, "awaiting_runtime");
  assert.equal(f.store.request(scope, task.request_id).explicitly_submitted, 0);
  assert.deepEqual(draft, { content: "", local_file_ids: [], revision: 2 });
  assert.throws(() => f.store.saveDraft(scope, conversation.id, "old delayed text", [], 1), /another editor/);
  assert.throws(() => f.store.enqueueDraft(scope, conversation.id, conversation.id, 1), /empty|changed/);
  assert.equal(f.store.read(scope, conversation.id).tasks.length, 1);
});

test("welcome draft moves atomically into its first conversation, preserving revision fences", (t) => {
  const f = fixture(t); const conversation = f.store.create(scope, "first");
  f.store.saveDraft(scope, "", "initial draft", [], 0);
  f.store.db.exec("CREATE TRIGGER fail_move BEFORE UPDATE ON drafts BEGIN SELECT RAISE(ABORT,'disk full'); END;");
  assert.throws(() => f.store.moveWelcomeDraft(scope, conversation.id, 1), /disk full/);
  assert.equal(f.store.draft(scope, conversation.id).revision, 0);
  f.store.db.exec("DROP TRIGGER fail_move");
  const moved = f.store.moveWelcomeDraft(scope, conversation.id, 1);
  assert.deepEqual(moved.source, { content: "", local_file_ids: [], revision: 2 });
  assert.equal(moved.draft.content, "initial draft"); assert.equal(moved.draft.revision, 1);
  assert.throws(() => f.store.saveDraft(scope, "", "delayed welcome", [], 1), /another editor/);
  assert.throws(() => f.store.moveWelcomeDraft(scope, conversation.id, 2), /changed/);
});
