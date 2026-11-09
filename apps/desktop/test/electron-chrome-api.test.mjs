import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import { ChromeEvent, ElectronChromeAPI, ElectronDownloadRegistry } from "../src/browser/electron-chrome-api.mjs";

test("Electron debugger commands remain bound to the exact task and announced nested sessions", async () => {
  const debuggerTransport = new EventEmitter();
  debuggerTransport.attach = (version) => { debuggerTransport.version = version; };
  debuggerTransport.detach = () => debuggerTransport.emit("detach", {}, "canceled_by_user");
  debuggerTransport.sendCommand = async (...args) => {
    debuggerTransport.commands.push(args);
    return { ok: true };
  };
  debuggerTransport.commands = [];
  const record = {
    tabID: 17,
    attached: false,
    childSessions: new Set(),
    childTargetTypes: new Set(),
    webContents: { debugger: debuggerTransport },
  };
  const registry = {
    get(connectionID, tabID) {
      if (connectionID !== "connection-a" || tabID !== 17) throw new Error("foreign target");
      return record;
    },
  };
  const api = new ElectronChromeAPI({
    registry,
    connectionID: "connection-a",
    downloadRegistry: emptyDownloads(),
  });

  await api.debugger.attach({ tabId: 17 }, "1.3");
  assert.equal(debuggerTransport.version, "1.3");
  await api.debugger.sendCommand({ tabId: 17 }, "Page.enable", null);
  debuggerTransport.emit("message", {}, "Target.attachedToTarget", {
    sessionId: "worker-session",
    targetInfo: { type: "worker" },
  });
  await api.debugger.sendCommand({ tabId: 17, sessionId: "worker-session" }, "Runtime.enable", {});
  await assert.rejects(
    api.debugger.sendCommand({ tabId: 17, sessionId: "foreign-session" }, "Runtime.enable", {}),
    /outside the task/,
  );
  await assert.rejects(
    api.debugger.sendCommand({ tabId: 18 }, "Runtime.enable", {}),
    /foreign target/,
  );
  assert.deepEqual(debuggerTransport.commands, [
    ["Page.enable"],
    ["Page.enable", {}, undefined],
    ["Runtime.enable", {}, "worker-session"],
  ]);
  assert.deepEqual([...record.childTargetTypes], ["worker"]);
});

test("ChromeEvent removes listeners before later authority changes", () => {
  const event = new ChromeEvent();
  const observed = [];
  const listener = (value) => observed.push(value);
  event.addListener(listener);
  event.emit("first");
  event.removeListener(listener);
  event.emit("second");
  assert.deepEqual(observed, ["first"]);
});

test("hidden screenshot primes a tiny native surface and preserves the exact CDP result and scope", async () => {
  let visible = false, active = true, wakeError = false;
  const captures = [], commands = [], image = { data: "authoritative-CDP-image" };
  const record = {
    attached: true, childSessions: new Set(), view: { getVisible: () => visible },
    webContents: {
      async capturePage(...args) { captures.push(args); if (wakeError) throw new Error("surface unavailable"); return "discarded native image"; },
      debugger: { async sendCommand(...args) { commands.push(args); return image; } },
    },
  };
  const api = new ElectronChromeAPI({ connectionID: "task-a", downloadRegistry: emptyDownloads(), registry: {
    get(connection, tab) {
      if (!active || connection !== "task-a" || tab !== 17) throw new Error("task revoked");
      return record;
    },
  } });
  const target = { tabId: 17 }, params = Object.freeze({ format: "png", captureBeyondViewport: false,
    clip: Object.freeze({ x: 7, y: 11, width: 640, height: 720, scale: 1 }) });
  assert.equal(await api.debugger.sendCommand(target, "Page.captureScreenshot", params), image);
  assert.deepEqual(captures, [[{ x: 0, y: 0, width: 1, height: 1 }, { stayHidden: true, stayAwake: true }]]);
  assert.equal(commands[0][1], params);
  assert.equal(visible, false);
  visible = true;
  await api.debugger.sendCommand(target, "Page.captureScreenshot", params);
  visible = false;
  await api.debugger.sendCommand(target, "Page.getLayoutMetrics", {});
  assert.equal(captures.length, 1);
  await assert.rejects(api.debugger.sendCommand({ ...target, sessionId: "foreign" }, "Page.captureScreenshot", params), /outside the task/);
  active = false;
  await assert.rejects(api.debugger.sendCommand(target, "Page.captureScreenshot", params), /revoked/);
  assert.equal(captures.length, 1); assert.equal(commands.length, 3);
  active = true; wakeError = true;
  assert.equal(await api.debugger.sendCommand(target, "Page.captureScreenshot", params), image);
  record.webContents.debugger.sendCommand = async () => { throw new Error("CDP capture failed"); };
  await assert.rejects(api.debugger.sendCommand(target, "Page.captureScreenshot", params), /CDP capture failed/);
});

test("Electron downloads are visible only to the connection that owns the initiating page", async () => {
  const temporary = await fs.mkdtemp(path.join(os.tmpdir(), "sparkclaw-electron-download-test-"));
  const browserSession = new EventEmitter();
  const owners = new Map([[21, "connection-a"], [22, "connection-b"]]);
  const registry = new ElectronDownloadRegistry(browserSession, {
    downloadOwner(webContentsID) { return owners.get(webContentsID) ?? null; },
  }, temporary);
  await registry.prepare();
  await registry.registerConnection("connection-a");
  await registry.registerConnection("connection-b");
  const createdA = [];
  registry.eventFor("connection-a").addListener((item) => createdA.push(item.id));
  const itemA = downloadItem("https://example.test/a");
  const itemB = downloadItem("https://example.test/b");
  const personalItem = downloadItem("https://example.test/personal");
  browserSession.emit("will-download", {}, itemA, { id: 21 });
  browserSession.emit("will-download", {}, itemB, { id: 22 });
  browserSession.emit("will-download", {}, personalItem, { id: 23 });

  assert.deepEqual(createdA, [1]);
  assert.equal(path.dirname(itemA.savePath), path.join(temporary, "connection-a"));
  assert.equal(path.dirname(itemB.savePath), path.join(temporary, "connection-b"));
  assert.equal(personalItem.savePath, "");
  assert.deepEqual((await registry.search("connection-a", {})).map((item) => item.url),
    ["https://example.test/a"]);
  assert.deepEqual((await registry.search("connection-b", {})).map((item) => item.url),
    ["https://example.test/b"]);
  await assert.rejects(registry.cancel("connection-a", 2), /outside the task/);
  await assert.rejects(registry.cancel("connection-a", 3), /outside the task/);
  await registry.cancel("connection-a", 1);
  assert.equal(itemA.canceled, true);
  assert.equal(itemB.canceled, false);
  assert.equal(personalItem.canceled, false);
  registry.releaseConnection("connection-a");
  await registry.close();
  await fs.rm(temporary, { recursive: true, force: true });
});

function emptyDownloads() {
  return {
    eventFor: () => new ChromeEvent(),
    releaseConnection: () => {},
    search: async () => [],
    cancel: async () => {},
    removeFile: async () => {},
    erase: async () => [],
  };
}

function downloadItem(url) {
  const item = new EventEmitter();
  item.canceled = false;
  item.getURL = () => url;
  item.getSavePath = () => "";
  item.getReceivedBytes = () => 0;
  item.cancel = () => { item.canceled = true; };
  item.setSavePath = (value) => { item.savePath = value; };
  item.savePath = "";
  return item;
}
