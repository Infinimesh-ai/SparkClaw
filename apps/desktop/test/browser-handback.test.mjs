import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import test from "node:test";
import { PageRegistry } from "../src/browser/page-registry.mjs";
import { BrowserPresentation } from "../src/main/presentation.mjs";
import { ElectronChromeAPI } from "../src/browser/electron-chrome-api.mjs";
import { trackPageWork } from "../src/browser/page-interaction.mjs";

const binding = (task = "task-A", epoch = "epoch-A") => ({ owner_id: "owner", client_id: "client", installation_id: "installation",
  local_conversation_id: "conversation-A", local_task_id: task, connection_epoch: epoch });
function fixture() {
  let nextID = 1;
  const createView = () => {
    const wc = new EventEmitter(), debuggerTransport = new EventEmitter();
    let url = "about:blank", closed = false;
    Object.assign(debuggerTransport, { attach() {}, detach() { this.emit("detach", {}, "detached"); }, async sendCommand() { return {}; } });
    Object.assign(wc, { id: nextID++, debugger: debuggerTransport, getURL: () => url, getTitle: () => "Live form",
      isDestroyed: () => closed, isLoading: () => false, isCrashed: () => false,
      focus() { this.focused = true; }, setWindowOpenHandler(fn) { this.popup = fn; },
      close(options) { this.closeOptions = options; if (this.cancelClose && options.waitForBeforeUnload) return; closed = true; this.emit("destroyed"); },
      async loadURL(value) { url = value; this.loads = (this.loads || 0) + 1; },
      reload() { this.reloads = (this.reloads || 0) + 1; },
      navigationHistory: { canGoBack: () => true, canGoForward: () => true, goBack() {}, goForward() {} } });
    return { webContents: wc, setBounds(value) { this.bounds = value; }, getBounds() { return this.bounds; },
      setVisible(value) { this.visible = value; }, getVisible() { return this.visible; } };
  };
  const shield = createView();
  const window = { contentView: { addChildView() {}, removeChildView() {} }, webContents: new EventEmitter() };
  const presentation = new BrowserPresentation({ window, shieldView: shield });
  const registry = new PageRegistry({ runtimeGeneration: "runtime", presentation, createView,
    browserSession: { flushStorageData() {}, cookies: { async flushStore() {} } } });
  registry.selectConversation("conversation-A");
  const connect = (id, scope = binding()) => {
    registry.registerConnection({ id, binding: { session_id: id, session_generation: 1 }, association: { binding: scope } });
    const facade = new ElectronChromeAPI({ registry, connectionID: id, downloadRegistry: { eventFor() {}, releaseConnection() {} } });
    registry.bindConnectionFacade(id, facade);
    return facade;
  };
  return { registry, connect, presentation, shield };
}
const blocked = (page, name) => {
  let prevented = false;
  page.webContents.emit(name, { preventDefault() { prevented = true; } });
  return prevented;
};

test("temporary release and same-task reacquire stay locked; terminal cleanup hands back the same live page", async () => {
  const { registry, connect, shield } = fixture();
  const api = connect("first");
  const page = registry.createTaskPage("first", "https://example.test/form");
  page.webContents.formValue = "kept";
  assert.equal(shield.visible, true);
  for (const event of ["before-input-event", "context-menu"]) assert.equal(blocked(page, event), true);
  await assert.rejects(registry.navigatePersonal(page.pageRef, "https://example.test/new"), /read-only/);
  assert.throws(() => registry.closePersonal(page.pageRef), /read-only/);
  registry.beginControllerRelease("first");
  registry.removeTaskPage("first", page.tabID);
  await registry.closeConnection("first", "normal_release");
  assert.equal(registry.desktopSnapshot()[0].interaction_state, "agent");
  assert.equal(shield.visible, true);
  connect("second");
  assert.equal(registry.createTaskPage("second"), page, "only the same still-running task can reuse its page");
  const groups = registry.beginTaskEnd(binding(), "completed");
  assert.equal(page.interaction.state, "releasing");
  await assert.rejects(api.debugger.sendCommand({ tabId: page.tabID }, "Runtime.evaluate"), /outside/);
  await registry.completeTaskEnd(groups);
  assert.equal(page.interaction.state, "releasing", "terminal fact alone cannot unlock a connected page");
  await registry.closeConnection("second", "task_ended");
  await registry.completeTaskEnd(groups);
  assert.equal(page.role, "task"); assert.equal(page.hostBinding.local_task_id, "task-A");
  assert.equal(page.interaction.state, "human"); assert.equal(shield.visible, false);
  assert.equal(page.webContents.loads, 1); assert.equal(page.webContents.formValue, "kept");
  assert.equal(blocked(page, "before-input-event"), false);
  assert.equal(blocked(page, "context-menu"), false);
  await registry.personalNavigation(page.pageRef, "reload");
  assert.equal(page.webContents.reloads, 1);
  page.webContents.cancelClose = true;
  registry.closePersonal(page.pageRef);
  assert.equal(registry.getByRef(page.pageRef), page);
  assert.deepEqual(page.webContents.closeOptions, { waitForBeforeUnload: true });
  connect("third", binding("task-B"));
  const next = registry.createTaskPage("third", "https://example.test/new-task");
  assert.notEqual(next, page);
  assert.throws(() => registry.get("third", page.tabID), /outside/);
  await registry.completeTaskEnd(groups);
  assert.equal(next.interaction.state, "agent", "late completion cannot change a new execution");
  page.webContents.cancelClose = false; registry.closePersonal(page.pageRef);
  assert.throws(() => registry.getByRef(page.pageRef), /unavailable/);
});

test("native read settlement and debugger detach are required before handback; cleanup failure remains locked", async () => {
  const { registry, connect, shield } = fixture();
  connect("active");
  const page = registry.createTaskPage("active", "https://example.test/form");
  let finish;
  trackPageWork(page, () => new Promise(resolve => { finish = resolve; }));
  const groups = registry.beginTaskEnd(binding(), "canceled");
  const cleanup = registry.closeConnection("active", "task_canceled");
  let handedBack = false;
  const handback = registry.completeTaskEnd(groups).then(() => { handedBack = true; });
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(handedBack, false); assert.equal(shield.visible, true);
  assert.equal(page.interaction.state, "releasing");
  finish(); await cleanup; await handback;
  assert.equal(page.interaction.state, "human");

  connect("failed", binding("task-B"));
  const failed = registry.createTaskPage("failed", "https://example.test/b");
  failed.attached = true;
  failed.webContents.debugger.detach = () => { throw new Error("detach failed"); };
  const failingGroups = registry.beginTaskEnd(binding("task-B"), "failed");
  await registry.closeConnection("failed", "task_ended");
  await registry.completeTaskEnd(failingGroups);
  assert.equal(failed.interaction.state, "releasing");
  assert.equal(registry.desktopSnapshot().find(p => p.page_ref === failed.pageRef).capabilities.interact, false);
  // The locked page is local: later tasks still connect, clean up and hand back.
  connect("after", binding("task-C"));
  const later = registry.createTaskPage("after", "https://example.test/c");
  const laterGroups = registry.beginTaskEnd(binding("task-C"), "completed");
  await registry.closeConnection("after", "task_ended");
  await registry.completeTaskEnd(laterGroups);
  assert.equal(later.interaction.state, "human");
  assert.equal(failed.interaction.state, "releasing", "a failed detach never unlocks");
});

test("a page's beforeunload asks the owner instead of silently keeping the tab open", async () => {
  const { registry, connect } = fixture();
  connect("active");
  const page = registry.createTaskPage("active", "https://example.test/compose");
  assert.equal(blocked(page, "will-prevent-unload"), true, "an execution-owned page never holds unload");
  const groups = registry.beginTaskEnd(binding(), "completed");
  await registry.closeConnection("active", "task_ended"); await registry.completeTaskEnd(groups);
  assert.equal(blocked(page, "will-prevent-unload"), true, "without a prompt the close proceeds");
  const answers = [];
  registry.confirmUnload = record => { answers.push(record); return answers.length > 1; };
  page.userCloseRequested = true;
  assert.equal(blocked(page, "will-prevent-unload"), false, "staying keeps the page");
  assert.equal(page.userCloseRequested, false);
  assert.equal(blocked(page, "will-prevent-unload"), true, "leaving lets the page unload");
  assert.deepEqual(answers, [page, page]);
});

test("native work that outlives the cleanup timeout hands the page back once it settles", async t => {
  t.mock.timers.enable({ apis: ["setTimeout"] });
  const { registry, connect } = fixture();
  connect("slow");
  const page = registry.createTaskPage("slow", "https://example.test/slow-read");
  let finish;
  trackPageWork(page, () => new Promise(resolve => { finish = resolve; }));
  const groups = registry.beginTaskEnd(binding(), "canceled");
  const cleanup = registry.closeConnection("slow", "task_canceled");
  await new Promise(resolve => setImmediate(resolve));
  t.mock.timers.tick(5000);
  await cleanup;
  await registry.completeTaskEnd(groups);
  assert.equal(page.interaction.state, "releasing", "unsettled native work keeps the page read-only");
  finish();
  for (let i = 0; i < 5; i++) await new Promise(resolve => setImmediate(resolve));
  assert.equal(page.interaction.state, "human");
});

test("execution popups stay locked; human popups retain provenance without gaining Controller authority", async () => {
  const { registry, connect } = fixture();
  connect("active");
  const page = registry.createTaskPage("active", "https://example.test/form");
  page.webContents.popup({ url: "https://example.test/agent-popup" });
  await new Promise(resolve => setImmediate(resolve));
  const popup = [...registry.records.values()].find(p => p !== page);
  assert.equal(popup.interaction, page.interaction); assert.equal(blocked(popup, "before-input-event"), true);
  const groups = registry.beginTaskEnd(binding(), "completed");
  await registry.closeConnection("active", "task_ended"); await registry.completeTaskEnd(groups);
  page.webContents.popup({ url: "https://example.test/human-popup" });
  await new Promise(resolve => setImmediate(resolve));
  const human = [...registry.records.values()].at(-1);
  assert.equal(human.role, "task"); assert.equal(human.hostConversationID, page.hostConversationID);
  assert.equal(human.interaction.state, "human"); assert.equal(human.connectionID, null);
  assert.equal(blocked(human, "before-input-event"), false);
  assert.throws(() => registry.get("active", human.tabID), /outside/);
  registry.closeHostPages("authentication_locked");
  assert.equal(registry.records.size, 0);
});

test("stale epoch terminal facts cannot release current pages; safe human bookmarks restore without execution", async () => {
  const { registry, connect } = fixture();
  connect("active", binding("task-A", "epoch-new"));
  const page = registry.createTaskPage("active", "https://example.test/form");
  await registry.completeTaskEnd(registry.beginTaskEnd(binding(), "completed"));
  assert.equal(page.interaction.state, "agent");
  registry.closeHostPages();
  const ref = registry.restoreConversation("conversation-A", { url: "https://example.test/result", title: "Human history",
    schema_version: 1, interaction_state: "human", binding: binding() });
  const restored = registry.getByRef(ref);
  assert.equal(restored.connectionID, null); assert.equal(restored.interaction.state, "human");
  await registry.showByRef(ref, "task");
  assert.equal(restored.webContents.loads, 1);
  connect("new", binding("task-B"));
  assert.notEqual(registry.createTaskPage("new"), restored);
});

test("a late agent navigation rejection cannot destroy a handed-back page", async () => {
  const { registry, connect } = fixture();
  const createView = registry.createView;
  let rejectLoad;
  registry.createView = () => {
    const view = createView(), load = view.webContents.loadURL.bind(view.webContents);
    view.webContents.loadURL = async url => { await load(url); await new Promise((_, reject) => { rejectLoad = reject; }); };
    return view;
  };
  connect("active");
  const page = registry.createTaskPage("active", "https://example.test/slow");
  await new Promise(resolve => setImmediate(resolve));
  const groups = registry.beginTaskEnd(binding(), "completed");
  await registry.closeConnection("active", "task_ended"); await registry.completeTaskEnd(groups);
  rejectLoad(new Error("ERR_ABORTED after user navigation"));
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(registry.getByRef(page.pageRef), page);
  assert.equal(page.interaction.state, "human");
});

test("viewing an older human tab cannot change the active task's retained result", async () => {
  const { registry, connect } = fixture();
  connect("first");
  const human = registry.createTaskPage("first", "https://example.test/human");
  const groups = registry.beginTaskEnd(binding(), "completed");
  await registry.closeConnection("first", "task_ended"); await registry.completeTaskEnd(groups);
  registry.selectConversation("another-conversation");
  connect("second", binding("task-B"));
  const result = registry.createTaskPage("second", "https://example.test/result");
  registry.selectConversation("conversation-A");
  registry.showByRef(human.pageRef, "task");
  assert.equal(registry.conversationPages.get("conversation-A"), human);
  registry.beginControllerRelease("second");
  registry.removeTaskPage("second", result.tabID);
  await registry.closeConnection("second", "normal_release");
  assert.equal(registry.getByRef(result.pageRef), result);
  assert.equal(result.interaction.state, "agent");
  assert.equal(human.interaction.state, "human");
});
