import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import test from "node:test";
import { PageRegistry } from "../src/browser/page-registry.mjs";
import { ElectronChromeAPI } from "../src/browser/electron-chrome-api.mjs";

test("a saved conversation restores lazily as a new read-only page without Controller authority", async () => {
  const loads = [], views = [];
  const presentation = { add() {}, hidePresented() {}, showTask() {}, isPresented() { return false; }, remove() {} };
  const registry = new PageRegistry({ runtimeGeneration: "new-runtime", presentation, createView: () => {
    const wc = new EventEmitter(); let url = "about:blank";
    Object.assign(wc, { getURL: () => url, getTitle: () => "", isLoading: () => false, isCrashed: () => false,
      setWindowOpenHandler(handler) { this.popup = handler; }, isDestroyed: () => false, close() {},
      loadURL: async value => { loads.push(value); url = value; } });
    views.push(wc); return { webContents: wc };
  } });
  registry.selectConversation("A");
  const saved = { url: "https://example.test/#/result", title: "Saved result", document_version: "a".repeat(64) };
  const ref = registry.restoreConversation("A", saved);
  assert.equal(registry.restoreConversation("A", saved), ref);
  assert.deepEqual(loads, []);
  assert.equal(registry.desktopSnapshot()[0].url, saved.url);
  const record = registry.getByRef(ref);
  assert.equal(record.connectionID, null);
  assert.equal(record.binding, null);
  assert.equal(views[0].popup({ url: "https://other.test/" }).action, "deny");
  assert.throws(() => registry.get("old-controller", record.tabID), /outside/);
  registry.selectConversation("B");
  assert.throws(() => registry.showByRef(ref, "task"), /another conversation/);
  registry.selectConversation("A");
  await registry.showByRef(ref, "task");
  assert.deepEqual(loads, [saved.url]);
  registry.closeHostPages();
  assert.throws(() => registry.getByRef(ref), /unavailable/);
  const next = registry.restoreConversation("A", saved);
  assert.notEqual(next, ref);
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
