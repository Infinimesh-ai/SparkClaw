import assert from "node:assert/strict";
import test from "node:test";

import { HANDOFF_EVALUATE_FUNCTION } from "../src/protocol.mjs";
import { RelayConnection } from "../src/relay-connection.mjs";

test("relay exposes only its initial task tab and rejects owner-tab control", async () => {
  const fixture = createFixture();
  const relay = new RelayConnection(fixture.options);
  relay.attachInitialTab(fixture.taskTab);
  assert.deepEqual(JSON.parse(fixture.webSocket.sent[0]), {
    method: "chrome.tabs.onCreated",
    params: [{ id: 2, windowId: 7, active: true, pinned: false, url: "chrome-extension://bridge/connect.html", title: "" }],
  });
  await assert.rejects(
    relay.handleCommand({ id: 1, method: "chrome.debugger.attach", params: [{ tabId: 1 }, "1.3"] }),
    /outside the task allowlist/,
  );
  await assert.rejects(
    relay.handleCommand({ id: 2, method: "chrome.tabs.remove", params: [1] }),
    /outside the task allowlist/,
  );
  assert.deepEqual(relay.connectedTabIds(), [2]);
});

test("relay creates inactive same-window tabs and bounds debugger commands to them", async () => {
  const fixture = createFixture();
  const relay = new RelayConnection(fixture.options);
  const tab = await relay.handleCommand({
    id: 1,
    method: "chrome.tabs.create",
    params: [{ url: "https://example.com/", active: true, windowId: 99, pinned: true }],
  });
  assert.equal(tab.id, 3);
  assert.deepEqual(fixture.calls.create[0], {
    url: "https://example.com/", active: false, pinned: false, windowId: 7,
  });
  await relay.handleCommand({ id: 2, method: "chrome.debugger.attach", params: [{ tabId: 3 }, "1.3"] });
  await relay.handleCommand({ id: 3, method: "chrome.debugger.sendCommand", params: [{ tabId: 3 }, "Runtime.evaluate", { expression: "1 + 1" }] });
  await assert.rejects(
    relay.handleCommand({ id: 4, method: "chrome.debugger.sendCommand", params: [{ tabId: 3 }, "Storage.getCookies", {}] }),
    /Browser-wide command is unavailable/,
  );
  assert.equal(fixture.calls.sendCommand.length, 1);
});

test("relay moves subsequent task creation to the explicit handoff window", async () => {
  const fixture = createFixture();
  const relay = new RelayConnection(fixture.options);

  relay.moveTaskWindow(2, 8);
  const tab = await relay.handleCommand({
    id: 1,
    method: "chrome.tabs.create",
    params: [{ url: "https://example.com/next" }],
  });

  assert.equal(tab.windowId, 8);
  assert.equal(fixture.calls.create[0].windowId, 8);
});

test("foreground activation requires and consumes the exact handoff marker", async () => {
  const fixture = createFixture();
  const relay = new RelayConnection(fixture.options);
  const handoffs = [];
  const completed = [];
  relay.onhandoff = (tabId) => handoffs.push(tabId);
  relay.onhandoffcomplete = (tabId) => completed.push(tabId);
  await relay.handleCommand({ id: 1, method: "chrome.debugger.attach", params: [{ tabId: 2 }, "1.3"] });
  await relay.handleCommand({ id: 2, method: "chrome.debugger.sendCommand", params: [{ tabId: 2 }, "Page.bringToFront", {}] });
  assert.equal(fixture.calls.sendCommand.length, 0);
  await relay.handleCommand({
    id: 3,
    method: "chrome.debugger.sendCommand",
    params: [{ tabId: 2 }, "Runtime.callFunctionOn", {
      functionDeclaration: "(utilityScript, ...args) => utilityScript.evaluate(...args)",
      objectId: "utility",
      arguments: [
        { objectId: "utility" },
        { value: true },
        { value: true },
        { value: `async (expr) => {
              const value = eval(\`(\${expr})\`);
              const isFunction = typeof value === "function";
              const result = await (isFunction ? value() : value);
              return { result, isFunction };
            }` },
        { value: 1 },
        { value: JSON.stringify({ s: HANDOFF_EVALUATE_FUNCTION }) },
      ],
      returnByValue: true,
      awaitPromise: true,
      userGesture: true,
    }],
  });
  await relay.handleCommand({ id: 4, method: "chrome.debugger.sendCommand", params: [{ tabId: 2 }, "Page.bringToFront", {}] });
  await relay.handleCommand({ id: 5, method: "chrome.debugger.sendCommand", params: [{ tabId: 2 }, "Page.bringToFront", {}] });
  assert.deepEqual(fixture.calls.sendCommand.map((call) => call[1]), ["Runtime.callFunctionOn", "Page.bringToFront"]);
  assert.deepEqual(handoffs, [2, 2]);
  assert.deepEqual(completed, [2, 2]);
});

test("marker text in an unrelated debugger command cannot authorize focus", async () => {
  const fixture = createFixture();
  const relay = new RelayConnection(fixture.options);
  await relay.handleCommand({ id: 1, method: "chrome.debugger.attach", params: [{ tabId: 2 }, "1.3"] });
  await relay.handleCommand({
    id: 2,
    method: "chrome.debugger.sendCommand",
    params: [{ tabId: 2 }, "Runtime.callFunctionOn", {
      functionDeclaration: "(utilityScript, ...args) => utilityScript.evaluate(...args)",
      objectId: "utility",
      arguments: [{ objectId: "utility" }, { value: HANDOFF_EVALUATE_FUNCTION }],
      returnByValue: true,
      awaitPromise: true,
      userGesture: true,
    }],
  });
  await relay.handleCommand({ id: 3, method: "chrome.debugger.sendCommand", params: [{ tabId: 2 }, "Page.bringToFront", {}] });
  assert.deepEqual(fixture.calls.sendCommand.map((call) => call[1]), ["Runtime.callFunctionOn"]);
});

test("only same-window children of allowed tabs enter the task allowlist", async () => {
  const fixture = createFixture();
  const relay = new RelayConnection(fixture.options);
  fixture.events.tabsCreated.emit({ id: 4, windowId: 7, openerTabId: 2, active: true, url: "https://example.com/popup" });
  fixture.events.tabsCreated.emit({ id: 5, windowId: 8, openerTabId: 2, active: true, url: "https://example.com/window" });
  fixture.events.tabsCreated.emit({ id: 6, windowId: 7, openerTabId: 1, active: false, url: "https://owner.example/" });
  await new Promise((resolve) => setImmediate(resolve));
  assert.deepEqual(relay.connectedTabIds().sort((a, b) => a - b), [2, 4]);
  assert.deepEqual(fixture.calls.update, [[4, { active: false }]]);
  assert.deepEqual(fixture.calls.remove, [5]);
});

function createFixture() {
  const events = {
    debuggerEvent: event(), debuggerDetach: event(), tabsCreated: event(), tabsRemoved: event(),
  };
  const calls = { attach: [], detach: [], sendCommand: [], create: [], remove: [], update: [] };
  const chromeAPI = {
    debugger: {
      onEvent: events.debuggerEvent,
      onDetach: events.debuggerDetach,
      attach: async (...args) => { calls.attach.push(args); },
      detach: async (...args) => { calls.detach.push(args); },
      sendCommand: async (...args) => { calls.sendCommand.push(args); return {}; },
    },
    tabs: {
      onCreated: events.tabsCreated,
      onRemoved: events.tabsRemoved,
      create: async (options) => { calls.create.push(options); return { id: 3, windowId: options.windowId, active: false, url: options.url }; },
      remove: async (ids) => { calls.remove.push(ids); },
      update: async (...args) => { calls.update.push(args); },
    },
  };
  const webSocket = {
    readyState: 1,
    sent: [],
    send(value) { this.sent.push(value); },
    close() { this.readyState = 3; this.onclose?.(); },
  };
  const taskTab = { id: 2, windowId: 7, active: true, pinned: false, url: "chrome-extension://bridge/connect.html" };
  return { options: { webSocket, chromeAPI, initialTab: taskTab }, webSocket, chromeAPI, taskTab, calls, events };
}

function event() {
  const listeners = new Set();
  return {
    addListener(listener) { listeners.add(listener); },
    removeListener(listener) { listeners.delete(listener); },
    emit(...args) { for (const listener of listeners) listener(...args); },
  };
}
