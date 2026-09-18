import assert from "node:assert/strict";
import test from "node:test";

import { FocusTracker, OwnedGroupRegistry, cleanupStaleTaskTabs } from "../src/background.mjs";

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

test("connection page preserves browser focus after returning from another app", async () => {
  const fixture = createFixture();
  const tracker = new FocusTracker(fixture.chromeAPI);
  await tick();
  fixture.events.focused.emit(7);

  await tracker.restoreAfterConnectionPage({ id: 3, windowId: 7 });

  assert.deepEqual(fixture.calls.tabsUpdate, [[1, { active: true }]]);
  assert.deepEqual(fixture.calls.windowsUpdate, []);
});

test("a cold service worker preserves the already focused owner window", async () => {
  const fixture = createFixture({ browserFocused: true });
  const tracker = new FocusTracker(fixture.chromeAPI);
  await tracker.ready;

  await tracker.restoreAfterConnectionPage({ id: 3, windowId: 7 });

  assert.deepEqual(fixture.calls.tabsUpdate, [[1, { active: true }]]);
  assert.deepEqual(fixture.calls.windowsUpdate, []);
});

test("background connection and cleanup preserve focus on another app", async () => {
  const fixture = createFixture();
  const tracker = new FocusTracker(fixture.chromeAPI);
  await tracker.ready;
  fixture.events.focused.emit(7);
  fixture.events.focused.emit(-1);

  await tracker.restoreAfterConnectionPage({ id: 3, windowId: 7 });
  await tracker.closeTaskTabs([2]);

  assert.deepEqual(fixture.calls.windowsUpdate, []);
  assert.deepEqual(fixture.calls.windowsCreate, []);
});

test("background task pages share one unfocused dedicated window", async () => {
  const fixture = createFixture();
  const tracker = new FocusTracker(fixture.chromeAPI);

  const [firstTabID, secondTabID] = await Promise.all([
    tracker.openBackgroundConnectionPage("chrome-extension://bridge/connect.html?first"),
    tracker.openBackgroundConnectionPage("chrome-extension://bridge/connect.html?second"),
  ]);

  assert.deepEqual([firstTabID, secondTabID], [4, 5]);
  assert.deepEqual(fixture.calls.windowsCreate, [{
    focused: false,
    type: "normal",
  }]);
  assert.deepEqual(fixture.calls.tabsCreate, [
    {
      url: "chrome-extension://bridge/connect.html?first",
      active: true,
      pinned: false,
      windowId: 8,
    },
    {
      url: "chrome-extension://bridge/connect.html?second",
      active: false,
      pinned: false,
      windowId: 8,
    },
  ]);
  assert.deepEqual(fixture.calls.tabsRemove, [[3]]);
  assert.deepEqual(fixture.calls.windowsUpdate, []);
});

test("a released task window is never reused for later background work", async () => {
  const fixture = createFixture();
  const tracker = new FocusTracker(fixture.chromeAPI);
  const firstTabID = await tracker.openBackgroundConnectionPage(
    "chrome-extension://bridge/connect.html?first",
  );

  tracker.releaseTaskTab(firstTabID);
  await tracker.openBackgroundConnectionPage("chrome-extension://bridge/connect.html?second");

  assert.equal(fixture.calls.windowsCreate.length, 2);
  assert.deepEqual(fixture.calls.tabsCreate.map((call) => call.windowId), [8, 9]);
});

test("a task-tab creation failure closes its new dedicated window", async () => {
  const fixture = createFixture();
  const tracker = new FocusTracker(fixture.chromeAPI);
  fixture.chromeAPI.tabs.create = async () => { throw new Error("tab failed"); };

  await assert.rejects(
    tracker.openBackgroundConnectionPage("chrome-extension://bridge/connect.html?failed"),
    /tab failed/,
  );

  assert.equal(fixture.calls.windowsCreate.length, 1);
  assert.deepEqual(fixture.calls.windowsRemove, [8]);
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
  const stored = new Map();
  const ownedGroups = new OwnedGroupRegistry({ storage: { session: {
    get: async (key) => (stored.has(key) ? { [key]: stored.get(key) } : {}),
    set: async (items) => { for (const [key, value] of Object.entries(items)) stored.set(key, value); },
  } } });
  await ownedGroups.add(9);

  await cleanupStaleTaskTabs(fixture.chromeAPI, tracker, ownedGroups, new Set([5]));

  assert.deepEqual(fixture.calls.tabsUpdate, [[1, { active: true }], [1, { active: true }]]);
  assert.deepEqual(fixture.calls.tabsUngroup, [], "keep ownership evidence until native removal succeeds");
  assert.deepEqual(fixture.calls.tabsRemove, [[3, 4]]);
});

function createFixture({ queryAllTabs = false, queryDelay = false, browserFocused = false } = {}) {
  const events = {
    activated: event(), focused: event(), removed: event(), updated: event(), windowRemoved: event(),
  };
  const calls = {
    tabsCreate: [], tabsRemove: [], tabsUngroup: [], tabsUpdate: [], windowsCreate: [], windowsRemove: [],
    windowsUpdate: [],
  };
  const groups = [];
  const tabs = new Map([
    [1, { id: 1, windowId: 7, url: "https://owner.example/" }],
    [2, { id: 2, windowId: 7, url: "https://task.example/" }],
  ]);
  const windows = new Set([7]);
  let nextTabID = 2;
  let nextWindowID = 7;
  const chromeAPI = {
    runtime: { id: "bridge" },
    tabs: {
      onActivated: events.activated,
      onRemoved: events.removed,
      onUpdated: events.updated,
      get: async (tabId) => tabs.get(tabId),
      create: async (options) => {
        calls.tabsCreate.push(options);
        const tab = { id: ++nextTabID, windowId: options.windowId, url: options.url, active: options.active };
        tabs.set(tab.id, tab);
        return tab;
      },
      query: async (query = {}) => {
        if (queryDelay) await tick();
        if (Number.isInteger(query.groupId)) {
          return [...tabs.values()].filter((tab) => tab.groupId === query.groupId);
        }
        if (Number.isInteger(query.windowId)) {
          return [...tabs.values()].filter((tab) => tab.windowId === query.windowId);
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
      onRemoved: events.windowRemoved,
      getLastFocused: async () => ({ id: 7, focused: browserFocused }),
      get: async (windowId) => {
        if (!windows.has(windowId)) throw new Error("window not found");
        return { id: windowId };
      },
      create: async (options) => {
        calls.windowsCreate.push(options);
        const windowId = ++nextWindowID;
        windows.add(windowId);
        if (Number.isInteger(options.tabId)) {
          const tab = tabs.get(options.tabId);
          if (tab) {
            tab.windowId = windowId;
            tab.active = true;
          }
          return { id: windowId, focused: true };
        }
        const tab = { id: ++nextTabID, windowId, url: options.url, active: true };
        tabs.set(tab.id, tab);
        return { id: windowId, focused: options.focused, tabs: [tab] };
      },
      remove: async (windowId) => {
        calls.windowsRemove.push(windowId);
        windows.delete(windowId);
        events.windowRemoved.emit(windowId);
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
