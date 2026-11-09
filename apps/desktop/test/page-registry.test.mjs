import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import test from "node:test";
import { PageRegistry } from "../src/browser/page-registry.mjs";
import { ElectronChromeAPI } from "../src/browser/electron-chrome-api.mjs";

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

test("Controller release keeps exactly the selected conversation view and drops the old connection authority", async () => {
  const presentation = { presented: null, add() {}, hidePresented() { this.presented = null; },
    showTask(record) { this.presented = record; }, isPresented(record) { return this.presented === record; },
    remove(record) { if (this.presented === record) this.presented = null; } };
  let detached = 0;
  const registry = new PageRegistry({ runtimeGeneration: "runtime", presentation, createView: () => {
    const webContents = new EventEmitter(), debuggerTransport = new EventEmitter();
    debuggerTransport.detach = () => { detached++; };
    Object.assign(webContents, { debugger: debuggerTransport, getURL: () => "https://example.test", getTitle: () => "Retained", loadURL: async () => {},
      setWindowOpenHandler() {}, isDestroyed: () => false, close() {} });
    return { webContents };
  } });
  const register = (id, conversation) => {
    registry.registerConnection({ id, binding: { session_id: id, session_generation: 1 },
      association: { binding: { local_conversation_id: conversation, local_task_id: id } } });
    const facade = new ElectronChromeAPI({ registry, connectionID: id, downloadRegistry: { eventFor() {}, releaseConnection() {} } });
    registry.bindConnectionFacade(id, facade);
    return facade;
  };
  registry.selectConversation("B");
  const old = register("old", "A");
  const page = registry.createTaskPage("old"), extra = registry.createTaskPage("old");
  assert.equal(presentation.presented, null, "background task cannot select another conversation");
  assert.throws(() => registry.showByRef(page.pageRef, "task"), /another conversation/);
  registry.selectTaskPage("old", page.tabID);
  page.attached = true;
  registry.beginControllerRelease("old");
  registry.removeTaskPage("old", extra.tabID);
  registry.removeTaskPage("old", page.tabID);
  registry.closeConnection("old");
  assert.equal(registry.records.size, 1);
  assert.equal(detached, 1);
  await assert.rejects(old.debugger.sendCommand({ tabId: page.tabID }, "Runtime.evaluate", {}), /outside/);
  register("new", "A");
  assert.equal(registry.createTaskPage("new"), page);
  assert.throws(() => registry.get("old", page.tabID), /outside/);
  assert.equal(registry.get("new", page.tabID), page);
  registry.removeTaskPage("new", page.tabID);
  assert.equal(registry.records.size, 0, "explicit close never uses the retention hook");
  registry.closeConnection("new");
  register("crashed", "A");
  const crashed = registry.createTaskPage("crashed");
  crashed.webContents.emit("render-process-gone");
  assert.equal(registry.records.size, 0);
  assert.throws(() => registry.getByRef(crashed.pageRef), /unavailable/);
});
