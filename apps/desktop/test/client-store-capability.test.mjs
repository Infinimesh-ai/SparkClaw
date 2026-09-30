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
