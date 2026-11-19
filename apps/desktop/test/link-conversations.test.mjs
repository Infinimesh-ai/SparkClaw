import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { ClientStore } from "../src/main/client-store.mjs";
import { LinkConversations } from "../src/main/link-conversations.mjs";
import { LinkError } from "../src/main/link-transport.mjs";
import { fakeBackend, until } from "./link-fakes.mjs";

const scope = { deployment_id: "sparkclaw-host", owner_id: "owner", client_id: "link-desktop-1" };

async function fixture() {
  const directory = await fs.mkdtemp(path.join(os.tmpdir(), "link-conversations-"));
  const store = new ClientStore(path.join(directory, "workbench"));
  const backend = fakeBackend();
  const link = new EventEmitter();
  link.online = true;
  link.request = async (op, params) => {
    if (!link.online) throw new LinkError("backend_unavailable", "The backend is unreachable", true);
    return structuredClone(await backend.request(op, structuredClone(params)));
  };
  const auth = { link, status: { state: "connected" } };
  let changes = 0;
  const terminal = [];
  const conversations = new LinkConversations({ auth, store, getIdentity: () => scope, onChange: () => changes++, onTaskTerminal: (binding, reason) => terminal.push(reason) });
  const run = (sessionID, runID, kind, extra = {}) => link.emit("event", { type: "event", topic: "run", subscription: `run:${sessionID}`, data: { session_id: sessionID, run_id: runID, kind, ...extra } });
  return { store, backend, link, auth, conversations, run, terminal, changes: () => changes };
}

test("sync mirrors headers by cursor: new, renamed, archived and deleted elsewhere", async () => {
  const f = await fixture();
  const a = f.backend.createSession("Trip plan"), b = f.backend.createSession("Mail triage");
  for (let i = 0; i < 3; i++) f.backend.createSession(`Extra ${i}`);
  await f.conversations.sync();
  assert.equal(f.store.list(scope).length, 5);
  assert.ok(f.store.linkCursor(scope) > 0, "the cursor is persisted");
  const before = f.backend.calls.filter(c => c.op === "sync.changes").length;
  f.backend.sessions.get(a.id).title = "Trip plan (final)"; f.backend.touchSession(a.id);
  f.backend.sessions.get(b.id).archived = true; f.backend.touchSession(b.id);
  await f.conversations.sync();
  const cursorCall = f.backend.calls.filter(c => c.op === "sync.changes")[before];
  assert.ok(cursorCall.params.cursor > 0, "the next sync starts at the saved cursor");
  const rows = f.store.list(scope);
  assert.ok(rows.some(r => r.title === "Trip plan (final)"));
  const bLocal = f.store.linkConversationByRemote(scope, b.id).conversation_id;
  assert.deepEqual(f.conversations.archivedIDs(scope), [bLocal]);
  f.backend.deleteSession(a.id);
  f.link.emit("event", { type: "event", topic: "sync", data: { cursor: 200 } });
  await until(() => f.store.list(scope).length === 4);
  assert.ok(!f.store.list(scope).some(r => r.title.startsWith("Trip plan")), "a conversation deleted elsewhere disappears");
  f.store.close();
});

test("opening a conversation shows the cache, then fetches the latest page and older pages on demand", async () => {
  const f = await fixture();
  const s = f.backend.createSession("Long");
  for (let i = 0; i < 45; i++) f.backend.addMessage(s.id, i % 2 ? "assistant" : "user", `message ${i}`);
  await f.conversations.sync();
  const id = f.store.linkConversationByRemote(scope, s.id).conversation_id;
  assert.equal(f.store.read(scope, id).messages.length, 0, "headers only until the conversation is opened");
  f.conversations.touch(scope, id);
  await until(() => f.store.read(scope, id).messages.length === 30);
  const page = f.store.read(scope, id);
  assert.equal(page.messages[0].content, "message 15");
  assert.equal(page.messages.at(-1).content, "message 44");
  assert.equal(page.has_older, true);
  assert.deepEqual(await f.conversations.loadOlder(scope, id), { has_older: false });
  const all = f.store.read(scope, id).messages;
  assert.equal(all.length, 45);
  assert.deepEqual(all.slice(0, 2).map(m => m.content), ["message 0", "message 1"], "older pages keep sequence order");
  f.backend.addMessage(s.id, "assistant", "from the phone");
  await f.conversations.sync();
  f.conversations.touch(scope, id);
  await until(() => f.store.read(scope, id).messages.at(-1).content === "from the phone");
  assert.ok(f.backend.calls.some(c => c.op === "session.messages" && c.params.after_seq === 45), "only messages after the cache are fetched");
  f.store.close();
});

test("a task is sent with message.send, streams its preview from run events and completes with the stored reply", async () => {
  const f = await fixture();
  const created = await f.conversations.create(scope, "New conversation", true);
  assert.ok(f.backend.calls.find(c => c.op === "session.create").params.client_request_id);
  const file = f.store.saveFile(scope, created.id, "note.txt", new Uint8Array(Buffer.from("hello attachment")));
  const uploads = [];
  f.backend.handlers["object.upload.begin"] = (p) => { uploads.push(p); return { upload_id: "up_1", received: 0, chunk_size: 262144 }; };
  f.backend.handlers["object.upload.chunk"] = (p) => { assert.equal(Buffer.from(p.data, "base64").toString(), "hello attachment"); return { upload_id: "up_1", received: 16, chunk_size: 262144 }; };
  f.backend.handlers["object.upload.commit"] = () => ({ upload_id: "up_1", received: 16, object_id: "ws_obj1" });
  const task = f.store.enqueue(scope, created.id, "Summarize the note", [file.id], undefined, undefined, undefined, true);
  f.store.markLinkRename(scope, created.id, true);
  const sent = await f.conversations.submit(scope, task.request_id);
  assert.equal(sent.status, "running");
  const send = f.backend.calls.find(c => c.op === "message.send");
  assert.equal(send.params.client_message_id, task.request_id, "the task's request id is the client message id");
  assert.deepEqual(send.params.attachments, [{ object_id: "ws_obj1", name: "note.txt", size: 16, content_type: "text/plain", sha256: uploads[0].sha256 }]);
  assert.ok(f.backend.calls.findIndex(c => c.op === "run.subscribe") < f.backend.calls.findIndex(c => c.op === "message.send"), "subscribed before sending");
  assert.equal(f.backend.calls.find(c => c.op === "session.rename").params.title, "Summarize the note", "the automatic title reaches the backend");
  const remote = f.store.linkConversation(scope, created.id).remote_id;
  const runID = f.store.linkTask(scope, task.request_id).run_id;
  f.run(remote, runID, "delta", { text: "The note " });
  f.run(remote, runID, "delta", { text: "says hello." });
  assert.equal(f.conversations.progressContent(scope, task.request_id), "The note says hello.");
  const reply = f.backend.addMessage(remote, "assistant", "The note says hello.", { run_id: runID });
  f.run(remote, runID, "final", { status: { session_id: remote, run_id: runID, state: "completed", final_message: reply } });
  await until(() => f.store.linkTask(scope, task.request_id).status === "delivered");
  const content = f.store.read(scope, created.id);
  assert.deepEqual(content.messages.map(m => [m.role, m.content]), [["user", "Summarize the note"], ["assistant", "The note says hello."]]);
  assert.equal(content.messages[1].request_id, task.request_id, "the reply replaces the in-progress row of its task");
  assert.equal(content.messages[0].attachments.length, 1, "the local attachment stays with the confirmed user message");
  assert.equal(f.conversations.progressContent(scope, task.request_id), undefined);
  await f.conversations.refreshMessages(scope, created.id);
  assert.equal(f.store.read(scope, created.id).messages.length, 2, "the backend copies of both messages are not duplicated");
  assert.deepEqual(f.terminal, ["task_ended"]);
  f.store.close();
});

test("an unsent message fails at once and is not queued", async () => {
  const f = await fixture();
  const created = await f.conversations.create(scope, "Busy", false);
  f.backend.handlers["message.send"] = () => { throw new LinkError("busy", "another message is running"); };
  const task = f.store.enqueue(scope, created.id, "Second message");
  await assert.rejects(f.conversations.submit(scope, task.request_id), /still running/u);
  const content = f.store.read(scope, created.id);
  assert.equal(content.tasks[0].status, "failed");
  assert.match(content.messages.at(-1).content, /Not sent/u);
  f.link.online = false;
  const offline = f.store.enqueue(scope, created.id, "Offline message");
  await assert.rejects(f.conversations.submit(scope, offline.request_id), /offline/u);
  assert.equal(f.backend.calls.filter(c => c.op === "message.send").length, 1, "nothing was sent while offline");
  await assert.rejects(f.conversations.create(scope, "x"), /offline/u);
  f.store.close();
});

test("approvals map onto the original decision contract and resolve on the backend", async () => {
  const f = await fixture();
  const created = await f.conversations.create(scope, "Approval", false);
  const task = f.store.enqueue(scope, created.id, "Send the mail");
  await f.conversations.submit(scope, task.request_id);
  const remote = f.store.linkConversation(scope, created.id).remote_id;
  const runID = f.store.linkTask(scope, task.request_id).run_id;
  const approval = { id: "ap_1", session_id: remote, run_id: runID, title: "Send mail", status: "pending", tool_name: "mail.send", created_at: new Date().toISOString(),
    detail: { arguments: { to: "a@example.com" }, input_form: undefined } };
  f.run(remote, runID, "final", { status: { session_id: remote, run_id: runID, state: "approval_pending", pending_approvals: [approval] } });
  await until(() => f.store.linkTask(scope, task.request_id).status === "approval_pending");
  const [local] = f.conversations.approvals(scope, task.request_id);
  assert.equal(local.approval_id, "ap_1"); assert.equal(local.tool, "mail.send"); assert.deepEqual(local.arguments, { to: "a@example.com" });
  assert.equal(local.state, "pending"); assert.equal(local.actionable, true);
  await assert.rejects(f.conversations.decideApproval(scope, task.request_id, "ap_1", "0".repeat(64), "approve"), /digest/u);
  const resolves = [];
  f.backend.handlers["approval.resolve"] = (p) => { resolves.push(p); return { approval: { ...approval, status: "approved" } }; };
  f.backend.runStatus = () => ({ session_id: remote, run_id: runID, state: "running" });
  assert.deepEqual(await f.conversations.decideApproval(scope, task.request_id, "ap_1", local.digest, "approve"), { resolved: true });
  assert.equal(resolves[0].decision, "approve");
  assert.match(resolves[0].idempotency_key, /^[a-f0-9-]{36}$/u);
  assert.equal(f.store.linkTask(scope, task.request_id).status, "running");
  f.store.close();
});

test("reconnecting reconciles runs in progress through run.status and the stored reply", async () => {
  const f = await fixture();
  const created = await f.conversations.create(scope, "Reconnect", false);
  const task = f.store.enqueue(scope, created.id, "Long job");
  await f.conversations.submit(scope, task.request_id);
  const remote = f.store.linkConversation(scope, created.id).remote_id;
  const runID = f.store.linkTask(scope, task.request_id).run_id;
  f.link.emit("state", { path: "" });
  const reply = f.backend.addMessage(remote, "assistant", "Done while offline", { run_id: runID });
  f.backend.runStatus = () => ({ session_id: remote, run_id: runID, state: "completed", final_message: reply });
  await f.conversations.reconcilePending();
  assert.equal(f.store.linkTask(scope, task.request_id).status, "delivered");
  assert.equal(f.store.read(scope, created.id).messages.at(-1).content, "Done while offline");
  f.store.close();
});

test("rename, archive and delete go to the backend first; generated files download on demand", async () => {
  const f = await fixture();
  const created = await f.conversations.create(scope, "Files", false);
  const remote = f.store.linkConversation(scope, created.id).remote_id;
  f.backend.addMessage(remote, "assistant", "Here is your report", { attachments: [{ object_id: "ws_report", name: "report.txt", size: 6, content_type: "text/plain" }] });
  await f.conversations.refreshMessages(scope, created.id);
  const attachment = f.store.read(scope, created.id).messages[0].attachments[0];
  assert.equal(attachment.name, "report.txt");
  f.backend.handlers["object.read"] = (p) => ({ object: { object_id: p.object_id, name: "report.txt", size: 6 }, offset: p.offset, data: Buffer.from("report").toString("base64"), eof: true });
  await f.conversations.ensureFile(scope, attachment.artifact_id);
  assert.equal(Buffer.from(f.store.file(scope, attachment.artifact_id).content).toString(), "report");
  assert.equal(f.store.read(scope, created.id).messages[0].attachments.length, 1, "a downloaded file is listed once");
  await f.conversations.rename(scope, created.id, "Quarterly files");
  assert.equal(f.backend.sessions.get(remote).title, "Quarterly files");
  await f.conversations.archive(scope, created.id, true);
  assert.equal(f.backend.sessions.get(remote).archived, true);
  assert.deepEqual(f.conversations.archivedIDs(scope), [created.id]);
  assert.deepEqual(await f.conversations.remove(scope, created.id), { deleted: true });
  assert.equal(f.backend.sessions.get(remote).deleted, true);
  f.link.online = false;
  const other = f.store.list(scope);
  assert.equal(other.length, 0);
  f.store.close();
});

test("cutover clears old local conversations once and keeps the welcome draft", async () => {
  const directory = await fs.mkdtemp(path.join(os.tmpdir(), "link-cutover-"));
  const store = new ClientStore(path.join(directory, "workbench"));
  const old = { deployment_id: "old-deployment", owner_id: "owner", client_id: "old-client" };
  const conversation = store.create(old, "Old local conversation");
  store.saveFile(old, conversation.id, "old.txt", new Uint8Array([1, 2, 3]));
  store.enqueue(old, conversation.id, "old question");
  store.saveDraft(old, "", "unsent welcome text", [], 0);
  assert.equal(store.linkCutover(scope), true);
  assert.equal(store.list(old).length, 0);
  assert.equal(store.listFiles(old).length, 0);
  assert.equal(store.draft(scope, "").content, "unsent welcome text");
  assert.equal(store.linkCutover(scope), false, "only once");
  store.close();
});
