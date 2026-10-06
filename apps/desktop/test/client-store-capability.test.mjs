import assert from "node:assert/strict";
import test from "node:test";
import { ClientStoreCapability } from "../src/main/client-store-capability.mjs";

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
