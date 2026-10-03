import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import test from "node:test";
import { PageRegistry } from "../src/browser/page-registry.mjs";

test("released Host pages retain conversation ownership and a crashed page cannot be selected by its old ref", async () => {
  const presentation = { presented: null, add() {}, hidePresented() { this.presented = null; }, showTask(record) { this.presented = record; }, remove(record) { if (this.presented === record) this.presented = null; } };
  const registry = new PageRegistry({ runtimeGeneration: "runtime", presentation, createView: () => {
    const webContents = new EventEmitter();
    Object.assign(webContents, { loadURL: async () => {}, setWindowOpenHandler(handler) { this.openHandler = handler; }, isDestroyed: () => false, close() {} });
    return { webContents };
  } });
  const binding = (lease) => ({ owner_id: "owner", client_id: "client", installation_id: "installation", local_conversation_id: "A", local_task_id: lease, host_id: "host", runtime_generation: "runtime", connection_epoch: "epoch", lease_id: lease, page_id: lease, page_generation: 1, authorization_digest: "digest" });
  registry.selectConversation("A");
  const first = binding("lease_first");
  const page = registry.acquireHostPage(first);
  await page.hostInitialLoad;
  registry.releaseHostPage(first);
  registry.selectConversation("B");
  assert.throws(() => registry.showByRef(page.pageRef, "task"), /another conversation/);
  assert.equal(presentation.presented, null);
  assert.deepEqual(page.webContents.openHandler({ url: "https://example.test" }), { action: "deny" });
  registry.selectConversation("A");
  assert.equal(registry.showByRef(page.pageRef, "task"), page);
  const second = binding("lease_second");
  assert.equal(registry.acquireHostPage(second), page);
  assert.throws(() => registry.requireHostPage(first), /stale/);
  page.webContents.emit("render-process-gone");
  assert.throws(() => registry.getByRef(page.pageRef), /unavailable/);
  const replacement = registry.acquireHostPage(binding("lease_replacement"));
  assert.notEqual(replacement.pageRef, page.pageRef);
  assert.notEqual(replacement.webContents, page.webContents);
});
