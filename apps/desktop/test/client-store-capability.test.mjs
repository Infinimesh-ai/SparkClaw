import assert from "node:assert/strict";
import test from "node:test";
import { ClientStoreCapability } from "../src/main/client-store-capability.mjs";

test("conversation creation carries an explicit automatic-title flag while preserving older custom-title callers", async () => {
  const frame = { url: "sparkclaw-app://workbench/index.html" }, webContents = { mainFrame: frame };
  const identity = { deployment_id: "deployment", owner_id: "owner", client_id: "client" };
  const calls = [];
  const capability = new ClientStoreCapability({ window: { webContents }, getIdentity: () => identity,
    store: { create: (...args) => { calls.push(args); return { title: args[1] }; } } });
  const event = { sender: webContents, senderFrame: frame };
  const request = { schema_version: 1, operation: "create", title: "新对话" };
  await capability.dispatch(event, request);
  await capability.dispatch(event, { ...request, auto_title: true });
  assert.deepEqual(calls, [[identity, "新对话", false, false], [identity, "新对话", false, true]]);
  for (const value of ["true", 1, null]) await assert.rejects(capability.dispatch(event, { ...request, auto_title: value }), /flag/);
  await assert.rejects(capability.dispatch(event, { ...request, auto_title: true, owner_id: "other" }), /fields/);
  assert.equal(calls.length, 2);
});

test("conversation deletion IPC uses the authenticated local scope and rejects injected fields", async () => {
  const frame = { url: "sparkclaw-app://workbench/index.html" }, webContents = { mainFrame: frame };
  const identity = { deployment_id: "deployment", owner_id: "owner", client_id: "client" };
  let current = identity;
  const calls = [];
  const capability = new ClientStoreCapability({ window: { webContents }, getIdentity: () => current,
    store: { remove(scope, id) { calls.push([scope, id]); return { deleted: true }; } } });
  const event = { sender: webContents, senderFrame: frame };
  const request = { schema_version: 1, operation: "remove", conversation_id: "conversation" };
  assert.deepEqual(await capability.dispatch(event, request), { deleted: true });
  assert.deepEqual(calls, [[identity, "conversation"]]);
  for (const injection of [{ owner_id: "another" }, { path: "/tmp" }, { force: true }]) {
    await assert.rejects(capability.dispatch(event, { ...request, ...injection }), /fields/);
  }
  await assert.rejects(capability.dispatch({ ...event, senderFrame: { ...frame } }, request), /trusted/);
  current = null;
  await assert.rejects(capability.dispatch(event, request), /locked/);
  assert.equal(calls.length, 1);
});

test("ClientStore IPC rejects web/subframe/scope injection and locks on signout", async () => {
  const frame = { url: "sparkclaw-app://workbench/index.html" };
  const webContents = { mainFrame: frame };
  const identity = { deployment_id: "deployment", owner_id: "owner", client_id: "client" };
  let current = identity;
  const capability = new ClientStoreCapability({ window: { webContents }, getIdentity: () => current,
    store: { list: (scope) => { assert.deepEqual(scope, identity); return []; } } });
  const event = { sender: webContents, senderFrame: frame };
  assert.deepEqual(await capability.dispatch(event, { schema_version: 1, operation: "list" }), []);
  await assert.rejects(capability.dispatch(event, { schema_version: 1, operation: "list", owner_id: "other" }), /fields/);
  await assert.rejects(capability.dispatch(event, { schema_version: 1, operation: "commitResult" }), /unavailable/);
  await assert.rejects(capability.dispatch({ ...event, senderFrame: { ...frame } }, { schema_version: 1, operation: "list" }), /trusted/);
  frame.url = "https://evil.example";
  await assert.rejects(capability.dispatch(event, { schema_version: 1, operation: "list" }), /trusted/);
  frame.url = "sparkclaw-app://workbench/index.html"; current = null;
  await assert.rejects(capability.dispatch(event, { schema_version: 1, operation: "list" }), /locked/);
});

test("schedule IPC carries recurrence to main and cannot inject a submission claim or lease", async () => {
  const frame = { url: "sparkclaw-app://workbench/index.html" };
  const webContents = { mainFrame: frame };
  const identity = { deployment_id: "deployment", owner_id: "owner", client_id: "client" };
  const calls = [];
  const capability = new ClientStoreCapability({ window: { webContents }, getIdentity: () => identity,
    schedules: { create: (...args) => { calls.push(args); return { state: "saved" }; } } });
  const event = { sender: webContents, senderFrame: frame };
  const request = { schema_version: 1, operation: "scheduleCreate", conversation_id: "conversation",
    content: "repeat", due_at: "2026-10-06T18:00:00Z", interval_ms: 3600000 };
  assert.deepEqual(await capability.dispatch(event, request), { state: "saved" });
  assert.deepEqual(calls, [[identity, "conversation", "repeat", request.due_at, 3600000]]);
  await assert.rejects(capability.dispatch(event, { ...request, submission_claim: "forged" }), /fields/);
  await assert.rejects(capability.dispatch(event, { ...request, lease_expires_at: request.due_at }), /fields/);
});

test("draft IPC fences delayed welcome autosave after identity changes", async () => {
  const frame = { url: "sparkclaw-app://workbench/index.html" };
  const webContents = { mainFrame: frame };
  let identity = { deployment_id: "deployment", owner_id: "owner", client_id: "client" };
  let writes = 0;
  const capability = new ClientStoreCapability({ window: { webContents }, getIdentity: () => identity,
    store: { draft: () => ({ content: "", local_file_ids: [], revision: 0 }),
      saveDraft: () => { writes++; return { content: "old scope text", local_file_ids: [], revision: 1 }; } } });
  const event = { sender: webContents, senderFrame: frame };
  const snapshot = await capability.dispatch(event, { schema_version: 1, operation: "draft", conversation_id: "" });
  const save = { schema_version: 1, operation: "saveDraft", conversation_id: "", content: "old scope text", local_file_ids: [], revision: 0, expected_scope: snapshot.scope_key };
  await capability.dispatch(event, save); assert.equal(writes, 1);
  identity = { ...identity, owner_id: "another-owner" };
  await assert.rejects(capability.dispatch(event, save), /authentication changed/);
  assert.equal(writes, 1);
});

test("recording draft evidence uses the current scope and admits only the opaque request identity", async () => {
  const frame = { url: "sparkclaw-app://workbench/index.html" }, webContents = { mainFrame: frame };
  let identity = { deployment_id: "deployment", owner_id: "owner", client_id: "client" };
  const calls = [];
  const capability = new ClientStoreCapability({ window: { webContents }, getIdentity: () => identity,
    store: { draft: () => ({ content: "", local_file_ids: [], revision: 0 }),
      recordingApplied: (...args) => { calls.push(args); return true; },
      saveDraft: (...args) => { calls.push(args); return { content: "text", local_file_ids: [], revision: 1 }; } } });
  const event = { sender: webContents, senderFrame: frame };
  const { scope_key } = await capability.dispatch(event, { schema_version: 1, operation: "draft", conversation_id: "" });
  const requestID = "00000000-0000-4000-8000-000000000001";
  const query = { schema_version: 1, operation: "recordingApplied", conversation_id: "", request_id: requestID, expected_scope: scope_key };
  assert.equal(await capability.dispatch(event, query), true);
  await capability.dispatch(event, { schema_version: 1, operation: "saveDraft", conversation_id: "", content: "text", local_file_ids: [], revision: 0, expected_scope: scope_key, recording_request_id: requestID });
  assert.deepEqual(calls[0], [identity, "", requestID]); assert.equal(calls[1].at(-1), requestID);
  await assert.rejects(capability.dispatch(event, { ...query, owner_id: "foreign" }), /fields/);
  identity = { ...identity, client_id: "other" };
  await assert.rejects(capability.dispatch(event, query), /authentication changed/);
  assert.equal(calls.length, 2);
});

test("local file inventory accepts no renderer path or scope and requires the qualified files surface", async () => {
  const frame = { url: "sparkclaw-app://workbench/index.html" }, webContents = { mainFrame: null }; webContents.mainFrame = frame;
  const identity = { deployment_id: "d", owner_id: "o", client_id: "c" };
  let enabled = true, calls = 0;
  const capability = new ClientStoreCapability({ window: { webContents }, getIdentity: () => identity,
    getCapabilities: () => ({ files: enabled }), store: { listFiles(scope) { calls++; assert.deepEqual(scope, identity); return []; } } });
  const event = { sender: webContents, senderFrame: frame }, request = { schema_version: 1, operation: "listFiles" };
  assert.deepEqual(await capability.dispatch(event, request), []);
  await assert.rejects(capability.dispatch(event, { ...request, path: "/tmp" }), /fields/);
  await assert.rejects(capability.dispatch(event, { ...request, owner_id: "another" }), /fields/);
  enabled = false;
  await assert.rejects(capability.dispatch(event, request), /unavailable/);
  assert.equal(calls, 1);
});
