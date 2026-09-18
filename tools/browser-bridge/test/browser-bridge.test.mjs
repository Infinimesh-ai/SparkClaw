import assert from "node:assert/strict";
import test from "node:test";

import { OwnedGroupRegistry, SparkClawBrowserBridge, TaskTabGroup, cleanupStaleTaskTabs } from "../src/background.mjs";
import { BRIDGE_VERSION } from "../src/protocol.mjs";

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

test("relay connect timeout and error close the pending socket", async () => {
  for (const mode of ["timeout", "error"]) {
    const fixture = createBridgeFixture();
    const clock = createClock();
    const sockets = [];
    class StalledWebSocket {
      constructor() {
        this.readyState = 0;
        this.closed = [];
        sockets.push(this);
        if (mode === "error") queueMicrotask(() => this.onerror?.(new Event("error")));
      }
      close(code, reason) { this.readyState = 3; this.closed.push([code, reason]); }
      send() {}
    }
    new SparkClawBrowserBridge({
      chromeAPI: fixture.chromeAPI,
      WebSocketClass: StalledWebSocket,
      setTimeoutFn: clock.setTimeout,
      clearTimeoutFn: clock.clearTimeout,
      staleCleanupDelays: [],
    });
    assert.deepEqual(await fixture.send({ type: "connectionRequested", mcpRelayUrl: RELAY_URL }), { success: true });

    const connecting = fixture.send({ type: "connectToTask", clientName: "test" });
    await tick();
    if (mode === "timeout") {
      const connectTimer = clock.findByDelay(5000);
      assert.ok(connectTimer, "relay connect timeout must use the injected timer");
      await clock.run(connectTimer);
    }
    assert.deepEqual(await connecting, { success: false, error: "Bridge request failed" }, mode);
    assert.equal(sockets.length, 1, mode);
    assert.deepEqual(sockets[0].closed, [[1000, mode === "timeout" ? "Relay connection timeout" : "Relay connection failed"]], mode);
    assert.equal(clock.findByDelay(5000), undefined, `${mode} left the connect timer armed`);
  }
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

test("native host requests create an unfocused dedicated task window", async () => {
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
    version: BRIDGE_VERSION,
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

  assert.deepEqual(fixture.calls.windowsCreate, [{ focused: false, type: "normal" }]);
  assert.deepEqual(fixture.calls.tabsCreate, [{
    url,
    active: true,
    pinned: false,
    windowId: 8,
  }]);
  assert.deepEqual(fixture.calls.tabsRemove, [[3], [4]]);
  assert.deepEqual(port.sent, [
    {
      type: "bridgeReady",
      extension_id: "mmlmfjhmonkocbjadbfplnigmagldckm",
      version: BRIDGE_VERSION,
      protocol_version: 2,
    },
    { type: "openConnectionResult", id: 7, success: true },
  ]);
});

test("native host cleanup closes a restored owned group before opening another task page", async () => {
  const fixture = createBridgeFixture();
  const port = nativePort();
  const token = "0123456789abcdef0123456789abcdef";
  const order = [];
  fixture.tabs.set(5, { id: 5, windowId: 7, url: "https://task.example/restored", active: false });
  fixture.chromeAPI.tabGroups.query = async () => [{
    id: 81,
    title: `SparkClaw task · playwright-mcp · sc:${token}`,
  }];
  fixture.chromeAPI.tabs.query = async (query = {}) => query.groupId === 81
    ? [fixture.tabs.get(5)] : [...fixture.tabs.values()];
  await fixture.chromeAPI.storage.local.set({ sparkclawTaskGroupTokens: [token] });
  const create = fixture.chromeAPI.windows.create;
  const remove = fixture.chromeAPI.tabs.remove;
  fixture.chromeAPI.windows.create = async options => { order.push("create"); return create(options); };
  fixture.chromeAPI.tabs.remove = async ids => { order.push("remove"); return remove(ids); };
  fixture.chromeAPI.runtime.connectNative = () => port;
  new SparkClawBrowserBridge({ chromeAPI: fixture.chromeAPI, staleCleanupDelays: [] });
  const url = "chrome-extension://mmlmfjhmonkocbjadbfplnigmagldckm/connect.html?" + new URLSearchParams({
    mcpRelayUrl: RELAY_URL,
    client: JSON.stringify({ name: "test" }),
    protocolVersion: "2",
    token: "secret-value",
  });

  port.messages.emit({ type: "openConnection", id: 8, url });
  await tick();
  await tick();

  assert.deepEqual(order.slice(0, 2), ["remove", "create"]);
  assert.deepEqual(fixture.calls.tabsRemove[0], [5, 3]);
  assert.deepEqual(port.sent.at(-1), { type: "openConnectionResult", id: 8, success: true });
});

test("stale cleanup closes only groups recorded as Bridge-owned, never an owner group by title", async () => {
  const fixture = createBridgeFixture();
  const groups = [
    { id: 21, title: "SparkClaw task" },
    { id: 22, title: "SparkClaw task · playwright-mcp" },
    { id: 23, title: "Research" },
  ];
  const groupTabs = new Map([
    [21, [{ id: 31, windowId: 7, url: "https://owner.example/named-group" }]],
    [22, [{ id: 32, windowId: 7, url: "https://task.example/left-behind" }]],
    [23, [{ id: 33, windowId: 7, url: "https://task.example/after-restart" }]],
  ]);
  fixture.chromeAPI.tabGroups.query = async () => groups;
  fixture.chromeAPI.tabs.query = async (query = {}) =>
    (Number.isInteger(query.groupId) ? groupTabs.get(query.groupId) ?? [] : [...fixture.tabs.values()]);
  await fixture.chromeAPI.storage.session.set({ sparkclawTaskGroupIDs: [23, 99] });
  const registry = new OwnedGroupRegistry(fixture.chromeAPI);
  const focus = new SparkClawBrowserBridge({ chromeAPI: fixture.chromeAPI, staleCleanupDelays: [] }).focus;

  await cleanupStaleTaskTabs(fixture.chromeAPI, focus, registry, new Set([3]));

  assert.deepEqual(fixture.calls.tabsRemove, [[33]]);
  assert.deepEqual([...await registry.list()], [], "closed and vanished group ids are forgotten");
});

test("stale cleanup recognizes a restored group by durable token after its numeric id changes", async () => {
  const fixture = createBridgeFixture();
  const token = "abcdef0123456789abcdef0123456789";
  const groups = [
    { id: 72, title: "SparkClaw task · playwright-mcp" },
    { id: 73, title: `SparkClaw task · playwright-mcp · sc:${token}` },
    { id: 74, title: "SparkClaw task · playwright-mcp · sc:00000000000000000000000000000000" },
  ];
  const groupTabs = new Map([
    [72, [{ id: 31, windowId: 7, url: "https://owner.example/same-title" }]],
    [73, [{ id: 32, windowId: 7, url: "https://task.example/restored" }]],
    [74, [{ id: 33, windowId: 7, url: "https://owner.example/foreign-token" }]],
  ]);
  fixture.chromeAPI.tabGroups.query = async () => groups;
  fixture.chromeAPI.tabs.query = async (query = {}) => Number.isInteger(query.groupId)
    ? groupTabs.get(query.groupId) ?? [] : [...fixture.tabs.values()];
  await fixture.chromeAPI.storage.local.set({ sparkclawTaskGroupTokens: [token] });
  const registry = new OwnedGroupRegistry(fixture.chromeAPI);
  const focus = new SparkClawBrowserBridge({ chromeAPI: fixture.chromeAPI, staleCleanupDelays: [] }).focus;

  await cleanupStaleTaskTabs(fixture.chromeAPI, focus, registry, new Set([3]));

  assert.deepEqual(fixture.calls.tabsRemove, [[32]]);
  assert.deepEqual([...await registry.listTokens()], []);
});

test("an early startup sweep retains durable ownership until restored groups appear", async () => {
  const fixture = createBridgeFixture();
  const token = "fedcba9876543210fedcba9876543210";
  await fixture.chromeAPI.storage.local.set({ sparkclawTaskGroupTokens: [token] });
  const registry = new OwnedGroupRegistry(fixture.chromeAPI);
  const focus = new SparkClawBrowserBridge({ chromeAPI: fixture.chromeAPI, staleCleanupDelays: [] }).focus;

  await cleanupStaleTaskTabs(fixture.chromeAPI, focus, registry, new Set([3]));

  assert.deepEqual([...await registry.listTokens()], [token]);
});

test("stale cleanup keeps an owned group whose tabs belong to a live connection", async () => {
  const fixture = createBridgeFixture();
  fixture.chromeAPI.tabGroups.query = async () => [{ id: 40, title: "SparkClaw task · live" }];
  fixture.chromeAPI.tabs.query = async (query = {}) =>
    (query.groupId === 40 ? [{ id: 3, windowId: 7, url: CONNECT_URL }, { id: 5, windowId: 7, url: "https://task.example/" }] : [...fixture.tabs.values()]);
  const registry = new OwnedGroupRegistry(fixture.chromeAPI);
  await registry.add(40);
  const focus = new SparkClawBrowserBridge({ chromeAPI: fixture.chromeAPI, staleCleanupDelays: [] }).focus;

  await cleanupStaleTaskTabs(fixture.chromeAPI, focus, registry, new Set([3, 5]));

  assert.deepEqual(fixture.calls.tabsRemove, []);
  assert.deepEqual([...await registry.list()], [40]);
});

test("task tab groups record session ids and durable title tokens until they close", async () => {
  const fixture = createBridgeFixture();
  const registry = new OwnedGroupRegistry(fixture.chromeAPI);
  let nextGroupID = 60;
  fixture.chromeAPI.tabs.group = async () => ++nextGroupID;
  const relay = {
    ontaballowed: null,
    ontabattached: null,
    onclose: null,
    connectedTabIds: () => [3],
    releaseTab() {},
  };
  const group = new TaskTabGroup({
    chromeAPI: fixture.chromeAPI,
    relay,
    initialTab: { id: 3 },
    clientName: "test",
    style: { title: "SparkClaw task · test", color: "green", collapsed: false },
    focus: { releaseTaskTab() {}, closeTaskTabs: async () => {} },
    ownedGroups: registry,
  });

  relay.ontabattached(3);
  await tick();
  assert.deepEqual([...await registry.list()], [61]);
  assert.equal((await registry.listTokens()).size, 1);
  group.beginHandoff(3);
  await group.completeHandoff(3);
  assert.deepEqual([...await registry.list()], [61, 62]);

  relay.onclose();
  await tick();
  assert.deepEqual([...await registry.list()], []);
  assert.deepEqual([...await registry.listTokens()], []);
  assert.deepEqual(await fixture.chromeAPI.storage.session.get("sparkclawTaskGroupIDs"), { sparkclawTaskGroupIDs: [] });
  assert.deepEqual(await fixture.chromeAPI.storage.local.get("sparkclawTaskGroupTokens"), { sparkclawTaskGroupTokens: [] });
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

test("failed native removal preserves group ownership and retries without ungrouping", async () => {
  const fixture = createBridgeFixture();
  fixture.chromeAPI.tabGroups.query = async () => [{ id: 40 }];
  fixture.chromeAPI.tabs.query = async query => Number.isInteger(query?.groupId)
    ? [fixture.tabs.get(3)] : [...fixture.tabs.values()];
  let failed = false;
  fixture.chromeAPI.tabs.remove = async ids => {
    fixture.calls.tabsRemove.push(ids);
    if (!failed) { failed = true; throw new Error("native removal failed"); }
  };
  const registry = new OwnedGroupRegistry(fixture.chromeAPI);
  await registry.add(40);
  const focus = new SparkClawBrowserBridge({ chromeAPI: fixture.chromeAPI, staleCleanupDelays: [] }).focus;
  await assert.rejects(cleanupStaleTaskTabs(fixture.chromeAPI, focus, registry), /native removal failed/);
  assert.deepEqual([...await registry.list()], [40]);
  assert.deepEqual(fixture.calls.tabsUngroup, []);
  await cleanupStaleTaskTabs(fixture.chromeAPI, focus, registry);
  assert.deepEqual(fixture.calls.tabsRemove, [[3], [3]]);
  assert.deepEqual([...await registry.list()], []);
});

test("unavailable group inventory does not erase persisted ownership", async () => {
  const fixture = createBridgeFixture();
  fixture.chromeAPI.tabGroups.query = async () => { throw new Error("inventory unavailable"); };
  const registry = new OwnedGroupRegistry(fixture.chromeAPI);
  await registry.add(40);
  const focus = new SparkClawBrowserBridge({ chromeAPI: fixture.chromeAPI, staleCleanupDelays: [] }).focus;
  await assert.rejects(cleanupStaleTaskTabs(fixture.chromeAPI, focus, registry), /inventory unavailable/);
  assert.deepEqual([...await registry.list()], [40]);
  assert.deepEqual(fixture.calls.tabsRemove, []);
});

test("a connection admitted during focus restoration is protected at native removal", async () => {
  const fixture = createBridgeFixture();
  const protectedTabs = new Set();
  fixture.chromeAPI.tabGroups.query = async () => [{ id: 40 }];
  fixture.chromeAPI.tabs.query = async query => Number.isInteger(query?.groupId)
    ? [fixture.tabs.get(3)] : [...fixture.tabs.values()];
  const get = fixture.chromeAPI.tabs.get;
  fixture.chromeAPI.tabs.get = async id => {
    if (id === 3) protectedTabs.add(3);
    return get(id);
  };
  const registry = new OwnedGroupRegistry(fixture.chromeAPI);
  await registry.add(40);
  const focus = new SparkClawBrowserBridge({ chromeAPI: fixture.chromeAPI, staleCleanupDelays: [] }).focus;
  await cleanupStaleTaskTabs(fixture.chromeAPI, focus, registry, () => protectedTabs);
  assert.deepEqual(fixture.calls.tabsRemove, []);
  assert.deepEqual([...await registry.list()], [40]);
});

test("background cleanup retries native failure without waiting for a worker restart", async () => {
  const fixture = createBridgeFixture();
  const clock = createClock();
  fixture.chromeAPI.tabGroups.query = async () => [{ id: 40 }];
  fixture.chromeAPI.tabs.query = async query => Number.isInteger(query?.groupId)
    ? [fixture.tabs.get(3)] : [...fixture.tabs.values()];
  const registry = new OwnedGroupRegistry(fixture.chromeAPI);
  await registry.add(40);
  let failed = false;
  fixture.chromeAPI.tabs.remove = async ids => {
    fixture.calls.tabsRemove.push(ids);
    if (!failed) { failed = true; throw new Error("native removal failed"); }
  };
  new SparkClawBrowserBridge({ chromeAPI: fixture.chromeAPI, staleCleanupDelays: [1],
    setTimeoutFn: clock.setTimeout, clearTimeoutFn: clock.clearTimeout });
  await clock.run(clock.findByDelay(1));
  assert.deepEqual([...await registry.list()], [40]);
  const retry = clock.findByDelay(10_000);
  assert.ok(retry);
  await clock.run(retry);
  assert.deepEqual([...await registry.list()], []);
  assert.deepEqual(fixture.calls.tabsRemove, [[3], [3]]);
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
    windowRemoved: event(),
  };
  const calls = { tabsCreate: [], tabsRemove: [], tabsUngroup: [], tabsUpdate: [], windowsCreate: [] };
  const sessionStorage = new Map();
  const localStorage = new Map();
  const tabs = new Map([
    [1, { id: 1, windowId: 7, url: "https://owner.example/", active: false, lastAccessed: 100 }],
    [3, { id: 3, windowId: 7, url: CONNECT_URL, active: true, lastAccessed: 200 }],
  ]);
  let nextTabID = 3;
  let nextWindowID = 7;
  const chromeAPI = {
    runtime: {
      id: "bridge",
      getURL: (path) => `chrome-extension://bridge/${path}`,
      onMessage: events.message,
      onStartup: events.startup,
    },
    action: { onClicked: events.clicked },
    downloads: { onCreated: event() },
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
        const tab = { id: ++nextTabID, windowId: options.windowId, url: options.url, active: options.active };
        tabs.set(tab.id, tab);
        return tab;
      },
      get: async (tabId) => tabs.get(tabId),
      group: async () => 9,
      query: async (query = {}) => Number.isInteger(query.groupId) ? [] : Number.isInteger(query.windowId)
        ? [...tabs.values()].filter((tab) => tab.windowId === query.windowId) : [...tabs.values()],
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
    storage: {
      session: {
        get: async (key) => (sessionStorage.has(key) ? { [key]: sessionStorage.get(key) } : {}),
        set: async (items) => { for (const [key, value] of Object.entries(items)) sessionStorage.set(key, value); },
      },
      local: {
        get: async (key) => (localStorage.has(key) ? { [key]: localStorage.get(key) } : {}),
        set: async (items) => { for (const [key, value] of Object.entries(items)) localStorage.set(key, value); },
      },
    },
    windows: {
      WINDOW_ID_NONE: -1,
      create: async (options) => {
        calls.windowsCreate.push(options);
        const windowId = ++nextWindowID;
        const tab = { id: ++nextTabID, windowId, url: options.url, active: true };
        tabs.set(tab.id, tab);
        return { id: windowId, focused: options.focused, tabs: [tab] };
      },
      get: async (windowId) => ({ id: windowId }),
      getLastFocused: async () => ({ id: 7, focused: false }),
      onFocusChanged: events.focused,
      onRemoved: events.windowRemoved,
      remove: async () => {},
      update: async () => {},
    },
  };
  return {
    chromeAPI,
    calls,
    events,
    tabs,
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

test("concurrent task attachments create one group and retain both tab leases", async () => {
  const f = groupingFixture();
  f.relay.ontabattached(2);
  f.relay.ontabattached(3);
  await tick();
  assert.equal(f.pending.length, 1);
  f.pending[0].resolve();
  await tick();
  assert.equal(f.pending.length, 2);
  assert.equal(f.pending[1].options.groupId, 91);
  f.pending[1].resolve();
  await tick();
  assert.deepEqual(f.released, []);
  assert.deepEqual([...await f.registry.list()], [91]);
  await f.relay.onclose();
  assert.deepEqual(f.closed, [[2, 3]]);
});

test("close waits for in-flight grouping, skips queued attachments and removes late group ownership", async () => {
  const f = groupingFixture();
  let closed = false;
  f.group.onclose = () => { closed = true; };
  f.relay.ontabattached(2);
  f.relay.ontabattached(3);
  await tick();
  const closing = f.relay.onclose();
  f.relay.ontabattached(4);
  assert.equal(closed, false);
  assert.deepEqual(f.closed, []);
  f.pending[0].resolve();
  await closing;
  assert.equal(f.pending.length, 1);
  assert.equal(closed, true);
  assert.deepEqual(f.closed, [[2, 3]]);
  assert.deepEqual([...await f.registry.list()], []);
});

test("failed task close retains persisted group ownership for retry", async () => {
  const f = groupingFixture({ failClose: true });
  f.relay.ontabattached(2);
  await tick();
  f.pending[0].resolve();
  await tick();
  await f.relay.onclose();
  assert.deepEqual([...await f.registry.list()], [91]);
});

test("owner ungroup during pending group creation releases the tab and never closes it", async () => {
  const f = groupingFixture();
  f.relay.ontabattached(2);
  await tick();
  f.tabs.get(2).groupId = -1;
  f.updated.emit(2, { groupId: -1 });
  f.pending[0].resolve();
  await tick();
  assert.deepEqual(f.released, [2]);
  await f.relay.onclose();
  assert.deepEqual(f.closed, []);
});

test("explicit release while grouping is pending undoes only that late task grouping", async () => {
  const f = groupingFixture();
  f.relay.ontabattached(2);
  await tick();
  f.group.releaseTab(2);
  f.pending[0].resolve();
  await tick();
  assert.deepEqual(f.ungrouped, [[2]]);
  await f.relay.onclose();
  assert.deepEqual(f.closed, []);
});

function groupingFixture({ failClose = false } = {}) {
  const base = createBridgeFixture();
  const registry = new OwnedGroupRegistry(base.chromeAPI);
  const pending = [], released = [], closed = [], ungrouped = [];
  const tabs = new Map([[2, { id: 2, groupId: -1 }], [3, { id: 3, groupId: -1 }]]);
  let nextGroupID = 90;
  base.chromeAPI.tabs.get = async id => tabs.get(id);
  base.chromeAPI.tabs.group = options => {
    const id = options.groupId ?? ++nextGroupID;
    for (const tabId of options.tabIds) {
      tabs.get(tabId).groupId = id;
      base.events.updated.emit(tabId, { groupId: id });
    }
    return new Promise(resolve => pending.push({ options, resolve: () => resolve(id) }));
  };
  base.chromeAPI.tabs.ungroup = async ids => { ungrouped.push(ids); };
  const relay = { connectedTabIds: () => [...tabs.keys()], releaseTab: id => released.push(id) };
  const group = new TaskTabGroup({ chromeAPI: base.chromeAPI, relay, initialTab: { id: 2 },
    clientName: "test", style: { title: "SparkClaw task" }, ownedGroups: registry,
    focus: { releaseTaskTab() {}, closeTaskTabs: async ids => {
      closed.push(ids);
      if (failClose) throw new Error("temporary Chrome failure");
    } } });
  return { group, relay, pending, released, closed, ungrouped, registry, tabs, updated: base.events.updated };
}

test("owner ungroup before concurrent disconnect survives pending group cleanup", async () => {
  const f = groupingFixture();
  f.relay.ontabattached(2);
  await tick();
  f.tabs.get(2).groupId = -1;
  f.updated.emit(2, { groupId: -1 });
  const closing = f.relay.onclose();
  f.pending[0].resolve();
  await closing;
  assert.deepEqual(f.released, [2]);
  assert.deepEqual(f.closed, []);
});

test("closing groups protect owned tabs until late grouping and native close finish", async () => {
  const f = groupingFixture();
  f.relay.ontabattached(2);
  f.relay.ontabattached(3);
  await tick();
  f.relay.connectedTabIds = () => [];
  const closing = f.relay.onclose();
  assert.deepEqual(f.group.connectedTabIds(), [2, 3]);
  f.group.releaseTab(3);
  assert.deepEqual(f.group.connectedTabIds(), [2]);
  f.pending[0].resolve();
  await closing;
  assert.deepEqual(f.closed, [[2]]);
  assert.deepEqual(f.group.connectedTabIds(), []);
});

test("owner ungroup while close restores focus is rechecked before native removal", async () => {
  const f = groupingFixture();
  f.relay.ontabattached(2);
  await tick();
  f.pending[0].resolve();
  await tick();
  let finish;
  const gate = new Promise(resolve => { finish = resolve; });
  let closeEntered = false;
  const actuallyClosed = [];
  f.group.focus.closeTaskTabs = async (ids, canClose) => {
    closeEntered = true;
    await gate;
    actuallyClosed.push(...ids.filter(canClose));
  };
  const closing = f.relay.onclose();
  await tick();
  assert.equal(closeEntered, true);
  f.updated.emit(2, { groupId: -1 });
  finish();
  await closing;
  assert.deepEqual(f.released, [2]);
  assert.deepEqual(actuallyClosed, []);
});
