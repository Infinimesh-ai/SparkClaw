import assert from "node:assert/strict";
import test from "node:test";

import { SparkClawBrowserBridge, TaskTabGroup } from "../src/background.mjs";

const CONNECT_URL = "chrome-extension://bridge/connect.html?mcpRelayUrl=redacted";
const RELAY_URL = "ws://127.0.0.1/extension/12345678-1234-4234-8234-123456789abc";

test("default browser timers retain their required global receiver", () => {
  const fixture = createBridgeFixture();
  const originalSetTimeout = globalThis.setTimeout;
  const originalClearTimeout = globalThis.clearTimeout;
  let timerCalls = 0;
  globalThis.setTimeout = function setTimeoutWithRequiredReceiver() {
    assert.equal(this, globalThis);
    timerCalls += 1;
    return timerCalls;
  };
  globalThis.clearTimeout = function clearTimeoutWithRequiredReceiver() {
    assert.equal(this, globalThis);
  };
  try {
    new SparkClawBrowserBridge({ chromeAPI: fixture.chromeAPI, staleCleanupDelays: [1] });
  } finally {
    globalThis.setTimeout = originalSetTimeout;
    globalThis.clearTimeout = originalClearTimeout;
  }
  assert.equal(timerCalls, 1);
});

test("pending connection expiry restores the owner tab and closes the connection page", async () => {
  const fixture = createBridgeFixture();
  const clock = createClock();
  new SparkClawBrowserBridge({
    chromeAPI: fixture.chromeAPI,
    setTimeoutFn: clock.setTimeout,
    clearTimeoutFn: clock.clearTimeout,
    staleCleanupDelays: [],
  });

  assert.deepEqual(await fixture.send({ type: "connectionRequested", mcpRelayUrl: RELAY_URL }), { success: true });
  const expiry = clock.findByDelay(6500);
  assert.ok(expiry);

  await clock.run(expiry);

  assert.deepEqual(fixture.calls.tabsUpdate, [
    [1, { active: true }],
    [1, { active: true }],
    [1, { active: true }],
  ]);
  assert.deepEqual(fixture.calls.tabsRemove, [[3]]);
});

test("successful connection cancels pending expiry", async () => {
  const fixture = createBridgeFixture();
  const clock = createClock();
  new SparkClawBrowserBridge({
    chromeAPI: fixture.chromeAPI,
    WebSocketClass: OpenWebSocket,
    setTimeoutFn: clock.setTimeout,
    clearTimeoutFn: clock.clearTimeout,
    staleCleanupDelays: [],
  });

  assert.deepEqual(await fixture.send({ type: "connectionRequested", mcpRelayUrl: RELAY_URL }), { success: true });
  const expiry = clock.findByDelay(6500);
  assert.ok(expiry);
  assert.deepEqual(await fixture.send({ type: "connectToTask", clientName: "test" }), { success: true });

  assert.equal(clock.has(expiry), false);
  assert.deepEqual(fixture.calls.tabsRemove, []);
});

test("discard, tab removal, and replacement cancel pending expiry", async () => {
  for (const action of ["discard", "remove", "replace"]) {
    const fixture = createBridgeFixture();
    const clock = createClock();
    new SparkClawBrowserBridge({
      chromeAPI: fixture.chromeAPI,
      setTimeoutFn: clock.setTimeout,
      clearTimeoutFn: clock.clearTimeout,
      staleCleanupDelays: [],
    });

    assert.deepEqual(await fixture.send({ type: "connectionRequested", mcpRelayUrl: RELAY_URL }), { success: true });
    const firstExpiry = clock.findByDelay(6500);
    assert.ok(firstExpiry);
    if (action === "discard") {
      assert.deepEqual(await fixture.send({ type: "discardConnectionPage" }), { success: true });
    } else if (action === "remove") {
      fixture.events.removed.emit(3);
    } else {
      assert.deepEqual(await fixture.send({ type: "connectionRequested", mcpRelayUrl: RELAY_URL }), { success: true });
    }

    assert.equal(clock.has(firstExpiry), false, `${action} did not cancel the first expiry`);
  }
});

test("native host requests create an inactive connection tab in the owner window", async () => {
  const fixture = createBridgeFixture();
  const port = nativePort();
  fixture.chromeAPI.runtime.connectNative = (name) => {
    assert.equal(name, "com.sparkclaw.browser_bridge");
    return port;
  };
  new SparkClawBrowserBridge({ chromeAPI: fixture.chromeAPI, staleCleanupDelays: [] });
  assert.deepEqual(port.sent, [{
    type: "bridgeReady",
    extension_id: "mmlmfjhmonkocbjadbfplnigmagldckm",
    version: "1.0.18",
    protocol_version: 2,
  }]);
  const url = "chrome-extension://mmlmfjhmonkocbjadbfplnigmagldckm/connect.html?" + new URLSearchParams({
    mcpRelayUrl: RELAY_URL,
    client: JSON.stringify({ name: "test" }),
    protocolVersion: "2",
    token: "secret-value",
  });

  port.messages.emit({ type: "openConnection", id: 7, url });
  await tick();

  assert.deepEqual(fixture.calls.tabsCreate, [{ url, active: false, pinned: false, windowId: 7 }]);
  assert.deepEqual(port.sent, [
    {
      type: "bridgeReady",
      extension_id: "mmlmfjhmonkocbjadbfplnigmagldckm",
      version: "1.0.18",
      protocol_version: 2,
    },
    { type: "openConnectionResult", id: 7, success: true },
  ]);
});

test("handoff regrouping cannot release the task tab before Chrome resolves tabs.group", async () => {
  const updated = event();
  const removed = event();
  const released = [];
  let nextGroupID = 8;
  const relay = {
    ontaballowed: null,
    ontabattached: null,
    onclose: null,
    connectedTabIds: () => [2],
    releaseTab: (tabId) => released.push(tabId),
  };
  const chromeAPI = {
    tabs: {
      group: async ({ tabIds }) => {
        const groupID = ++nextGroupID;
        updated.emit(tabIds[0], { groupId: groupID });
        return groupID;
      },
      onRemoved: removed,
      onUpdated: updated,
      ungroup: async () => {},
    },
    tabGroups: { update: async () => {} },
  };
  const group = new TaskTabGroup({
    chromeAPI,
    relay,
    initialTab: { id: 2 },
    clientName: "test",
    style: { title: "SparkClaw task", color: "green", collapsed: false },
    focus: { releaseTaskTab() {} },
  });

  relay.ontabattached(2);
  await tick();
  group.beginHandoff(2);
  await group.completeHandoff(2);
  await tick();

  assert.deepEqual(released, []);
});

function createBridgeFixture() {
  const events = {
    activated: event(),
    clicked: event(),
    created: event(),
    debuggerDetach: event(),
    debuggerEvent: event(),
    focused: event(),
    message: event(),
    removed: event(),
    startup: event(),
    updated: event(),
  };
  const calls = { tabsCreate: [], tabsRemove: [], tabsUngroup: [], tabsUpdate: [] };
  const tabs = new Map([
    [1, { id: 1, windowId: 7, url: "https://owner.example/", active: false, lastAccessed: 100 }],
    [3, { id: 3, windowId: 7, url: CONNECT_URL, active: true, lastAccessed: 200 }],
  ]);
  const chromeAPI = {
    runtime: {
      id: "bridge",
      getURL: (path) => `chrome-extension://bridge/${path}`,
      onMessage: events.message,
      onStartup: events.startup,
    },
    action: { onClicked: events.clicked },
    debugger: {
      attach: async () => {},
      detach: async () => {},
      sendCommand: async () => ({}),
      onDetach: events.debuggerDetach,
      onEvent: events.debuggerEvent,
    },
    tabs: {
      create: async (options) => {
        calls.tabsCreate.push(options);
        const tab = { id: 4, windowId: options.windowId, url: options.url, active: options.active };
        tabs.set(tab.id, tab);
        return tab;
      },
      get: async (tabId) => tabs.get(tabId),
      group: async () => 9,
      query: async (query = {}) => Number.isInteger(query.groupId) ? [] : [...tabs.values()],
      remove: async (tabIds) => { calls.tabsRemove.push(Array.isArray(tabIds) ? tabIds : [tabIds]); },
      ungroup: async (tabIds) => { calls.tabsUngroup.push(tabIds); },
      update: async (...args) => { calls.tabsUpdate.push(args); },
      onActivated: events.activated,
      onCreated: events.created,
      onRemoved: events.removed,
      onUpdated: events.updated,
    },
    tabGroups: {
      query: async () => [],
      update: async () => {},
    },
    windows: {
      WINDOW_ID_NONE: -1,
      getLastFocused: async () => ({ id: 7, focused: false }),
      onFocusChanged: events.focused,
      update: async () => {},
    },
  };
  return {
    chromeAPI,
    calls,
    events,
    send(message) {
      return new Promise((resolve) => {
        events.message.emit(message, { id: "bridge", url: CONNECT_URL, tab: tabs.get(3) }, resolve);
      });
    },
  };
}

function createClock() {
  let nextHandle = 0;
  const timers = new Map();
  return {
    setTimeout(callback, delay) {
      const handle = ++nextHandle;
      timers.set(handle, { callback, delay });
      return handle;
    },
    clearTimeout(handle) { timers.delete(handle); },
    findByDelay(delay) {
      return [...timers].find(([, timer]) => timer.delay === delay)?.[0];
    },
    has(handle) { return timers.has(handle); },
    async run(handle) {
      const timer = timers.get(handle);
      timers.delete(handle);
      timer?.callback();
      await tick();
    },
  };
}

function event() {
  const listeners = new Set();
  return {
    addListener(listener) { listeners.add(listener); },
    removeListener(listener) { listeners.delete(listener); },
    emit(...args) { for (const listener of listeners) listener(...args); },
  };
}

class OpenWebSocket {
  constructor() {
    this.readyState = 1;
    queueMicrotask(() => this.onopen?.());
  }
  close() { this.readyState = 3; }
  send() {}
}

function nativePort() {
  return {
    messages: event(),
    disconnected: event(),
    sent: [],
    get onMessage() { return this.messages; },
    get onDisconnect() { return this.disconnected; },
    postMessage(message) { this.sent.push(message); },
  };
}

function tick() {
  return new Promise((resolve) => setImmediate(resolve));
}
