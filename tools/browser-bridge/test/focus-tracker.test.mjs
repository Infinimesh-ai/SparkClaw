import assert from "node:assert/strict";
import test from "node:test";

import { cleanupStaleTaskTabs, FocusTracker } from "../src/background.mjs";

test("task activation restores the owner tab unless handoff was granted", async () => {
  const fixture = createFixture();
  const tracker = new FocusTracker(fixture.chromeAPI);
  await tick();
  tracker.allowTaskTab(2);

  fixture.events.activated.emit({ tabId: 2, windowId: 7 });
  await tick();
  assert.deepEqual(fixture.calls.tabsUpdate, [[1, { active: true }]]);

  tracker.grantHandoff(2);
  fixture.events.activated.emit({ tabId: 2, windowId: 7 });
  await tick();
  assert.deepEqual(fixture.calls.tabsUpdate, [[1, { active: true }]]);
});

test("completed handoff moves the task tab into a focused window", async () => {
  const fixture = createFixture();
  fixture.tabs.get(2).active = true;
  const tracker = new FocusTracker(fixture.chromeAPI);
  await tracker.ready;
  tracker.allowTaskTab(2);
  tracker.grantHandoff(2);

  await tracker.focusHandoffWindow(2);
  assert.deepEqual(fixture.calls.tabsUngroup, [[2]]);
  assert.deepEqual(fixture.calls.windowsCreate, [{ tabId: 2, focused: true, type: "normal" }]);
  assert.deepEqual(fixture.calls.windowsUpdate, []);
  assert.deepEqual(fixture.calls.tabsUpdate, []);
});

test("a repeated handoff reuses the focused task window", async () => {
  const fixture = createFixture();
  const tracker = new FocusTracker(fixture.chromeAPI);
  await tracker.ready;
  tracker.allowTaskTab(2);
  tracker.grantHandoff(2);

  const firstWindowID = await tracker.focusHandoffWindow(2);
  tracker.grantHandoff(2);
  const secondWindowID = await tracker.focusHandoffWindow(2);

  assert.equal(firstWindowID, 8);
  assert.equal(secondWindowID, 8);
  assert.deepEqual(fixture.calls.tabsUngroup, [[2]]);
  assert.deepEqual(fixture.calls.windowsCreate, [{ tabId: 2, focused: true, type: "normal" }]);
  assert.deepEqual(fixture.calls.windowsUpdate, [[8, { focused: true }]]);
});

test("connection page restores the owner tab and prior unfocused state", async () => {
  const fixture = createFixture();
  const tracker = new FocusTracker(fixture.chromeAPI);
  await tick();
  fixture.events.focused.emit(7);

  await tracker.restoreAfterConnectionPage({ id: 3, windowId: 7 });

  assert.deepEqual(fixture.calls.tabsUpdate, [[1, { active: true }]]);
  assert.deepEqual(fixture.calls.windowsUpdate, [[7, { focused: false }]]);
});

test("connection page first observed as about:blank restores the prior owner tab", async () => {
  const fixture = createFixture();
  const tracker = new FocusTracker(fixture.chromeAPI);
  await tick();
  fixture.tabs.set(3, { id: 3, windowId: 7, url: "about:blank" });

  fixture.events.activated.emit({ tabId: 3, windowId: 7 });
  await tick();
  fixture.tabs.set(3, {
    id: 3,
    windowId: 7,
    url: "chrome-extension://bridge/connect.html?mcpRelayUrl=redacted",
  });
  await tracker.restoreAfterConnectionPage({ id: 3, windowId: 7 });

  assert.deepEqual(fixture.calls.tabsUpdate, [[1, { active: true }]]);
});

test("connection page activated during task cleanup restores the owner tab", async () => {
  const fixture = createFixture({ queryAllTabs: true, queryDelay: true });
  fixture.tabs.get(1).lastAccessed = 200;
  fixture.tabs.get(2).lastAccessed = 100;
  fixture.tabs.set(3, {
    id: 3,
    windowId: 7,
    url: "chrome-extension://bridge/connect.html?mcpRelayUrl=redacted",
  });
  new FocusTracker(fixture.chromeAPI);

  fixture.events.activated.emit({ tabId: 3, windowId: 7 });
  await tick();

  assert.deepEqual(fixture.calls.tabsUpdate, [[1, { active: true }]]);
});

test("cold service worker waits for owner history before restoring a connection page", async () => {
  const fixture = createFixture({ queryAllTabs: true, queryDelay: true });
  fixture.tabs.get(1).active = false;
  fixture.tabs.get(1).lastAccessed = 200;
  fixture.tabs.get(2).active = false;
  fixture.tabs.get(2).lastAccessed = 100;
  fixture.tabs.set(3, {
    id: 3,
    windowId: 7,
    url: "chrome-extension://bridge/connect.html?mcpRelayUrl=redacted",
    active: true,
    lastAccessed: 300,
  });
  const tracker = new FocusTracker(fixture.chromeAPI);

  await tracker.restoreAfterConnectionPage(fixture.tabs.get(3));

  assert.deepEqual(fixture.calls.tabsUpdate, [[1, { active: true }]]);
});

test("a tab that becomes a connection page cannot pollute owner history", async () => {
  const fixture = createFixture({ queryAllTabs: true });
  fixture.tabs.set(3, { id: 3, windowId: 7, url: "about:blank", active: true, lastAccessed: 300 });
  const tracker = new FocusTracker(fixture.chromeAPI);
  await tracker.ready;

  fixture.events.activated.emit({ tabId: 3, windowId: 7 });
  await tick();
  const connectionTab = {
    id: 3,
    windowId: 7,
    url: "chrome-extension://bridge/connect.html?mcpRelayUrl=redacted",
    active: true,
  };
  fixture.tabs.set(3, connectionTab);
  fixture.events.updated.emit(3, { url: connectionTab.url }, connectionTab);
  await tick();
  fixture.calls.tabsUpdate.length = 0;

  tracker.allowTaskTab(2);
  fixture.events.activated.emit({ tabId: 2, windowId: 7 });
  await tick();

  assert.deepEqual(fixture.calls.tabsUpdate, [[1, { active: true }]]);
});

test("an active tab is remembered when its final owner URL arrives after activation", async () => {
  const fixture = createFixture({ queryAllTabs: true });
  const tracker = new FocusTracker(fixture.chromeAPI);
  await tracker.ready;
  fixture.tabs.set(3, { id: 3, windowId: 7, url: "about:blank", active: true, lastAccessed: 300 });
  fixture.tabs.set(3, { id: 3, windowId: 7, url: "https://owner-latest.example/", active: true, lastAccessed: 300 });
  fixture.events.updated.emit(3, { url: "https://owner-latest.example/" }, fixture.tabs.get(3));

  tracker.allowTaskTab(2);
  fixture.events.activated.emit({ tabId: 2, windowId: 7 });
  await tick();

  assert.deepEqual(fixture.calls.tabsUpdate, [[3, { active: true }]]);
});

test("cold service worker closes grouped and ungrouped stale task tabs after restoring the owner tab", async () => {
  const fixture = createFixture({ queryAllTabs: true });
  fixture.tabs.delete(2);
  fixture.tabs.get(1).active = false;
  fixture.tabs.set(3, {
    id: 3,
    windowId: 7,
    groupId: 9,
    url: "chrome-extension://bridge/connect.html?mcpRelayUrl=redacted",
    active: true,
    lastAccessed: 300,
  });
  fixture.tabs.set(4, {
    id: 4,
    windowId: 7,
    url: "chrome-extension://bridge/connect.html?mcpRelayUrl=orphaned",
    active: false,
    lastAccessed: 250,
  });
  fixture.tabs.set(5, {
    id: 5,
    windowId: 7,
    url: "chrome-extension://bridge/connect.html?mcpRelayUrl=active",
    active: false,
    lastAccessed: 275,
  });
  fixture.groups.push({ id: 9, title: "SparkClaw task · old-client" });
  const tracker = new FocusTracker(fixture.chromeAPI);

  await cleanupStaleTaskTabs(fixture.chromeAPI, tracker, new Set([5]));

  assert.deepEqual(fixture.calls.tabsUpdate, [[1, { active: true }], [1, { active: true }]]);
  assert.deepEqual(fixture.calls.tabsUngroup, [[3, 4]]);
  assert.deepEqual(fixture.calls.tabsRemove, [[3, 4]]);
});

function createFixture({ queryAllTabs = false, queryDelay = false } = {}) {
  const events = { activated: event(), focused: event(), removed: event(), updated: event() };
  const calls = { tabsRemove: [], tabsUngroup: [], tabsUpdate: [], windowsCreate: [], windowsUpdate: [] };
  const groups = [];
  const tabs = new Map([
    [1, { id: 1, windowId: 7, url: "https://owner.example/" }],
    [2, { id: 2, windowId: 7, url: "https://task.example/" }],
  ]);
  const chromeAPI = {
    runtime: { id: "bridge" },
    tabs: {
      onActivated: events.activated,
      onRemoved: events.removed,
      onUpdated: events.updated,
      get: async (tabId) => tabs.get(tabId),
      query: async (query = {}) => {
        if (queryDelay) await tick();
        if (Number.isInteger(query.groupId)) {
          return [...tabs.values()].filter((tab) => tab.groupId === query.groupId);
        }
        return queryAllTabs ? [...tabs.values()] : [{ id: 1, windowId: 7, url: "https://owner.example/" }];
      },
      remove: async (tabIds) => { calls.tabsRemove.push(tabIds); },
      ungroup: async (tabIds) => { calls.tabsUngroup.push(tabIds); },
      update: async (...args) => { calls.tabsUpdate.push(args); },
    },
    tabGroups: {
      query: async () => groups,
    },
    windows: {
      WINDOW_ID_NONE: -1,
      onFocusChanged: events.focused,
      getLastFocused: async () => ({ id: 7, focused: false }),
      create: async (options) => {
        calls.windowsCreate.push(options);
        const tab = tabs.get(options.tabId);
        if (tab) {
          tab.windowId = 8;
          tab.active = true;
        }
        return { id: 8, focused: true };
      },
      update: async (...args) => { calls.windowsUpdate.push(args); },
    },
  };
  return { chromeAPI, events, calls, groups, tabs };
}

function event() {
  const listeners = new Set();
  return {
    addListener(listener) { listeners.add(listener); },
    emit(...args) { for (const listener of listeners) listener(...args); },
  };
}

function tick() {
  return new Promise((resolve) => setImmediate(resolve));
}
