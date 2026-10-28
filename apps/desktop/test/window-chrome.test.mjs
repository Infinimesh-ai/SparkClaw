import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import fs from "node:fs/promises";
import vm from "node:vm";
import test from "node:test";
import { bindWindowChrome } from "../src/main/window-chrome.mjs";

test("native fullscreen hides the inset, survives renderer reload, and restores on exit", () => {
  const window = new EventEmitter(), contents = new EventEmitter();
  const visibility = [], states = [];
  let fullscreen = false;
  Object.assign(contents, { isDestroyed: () => false, send: (channel, value) => states.push([channel, value]) });
  Object.assign(window, { webContents: contents, isDestroyed: () => false,
    isFullScreen: () => fullscreen, setWindowButtonVisibility: value => visibility.push(value) });
  bindWindowChrome(window, { inset: true });
  contents.emit("did-finish-load");
  fullscreen = true;
  window.emit("enter-full-screen");
  contents.emit("did-finish-load");
  fullscreen = false;
  window.emit("leave-full-screen");
  assert.deepEqual(visibility, [true, false, false, true]);
  assert.deepEqual(states, [false, true, true, false].map(value => ["sparkclaw-window:fullscreen", value]));
  window.emit("closed");
  assert.equal(contents.listenerCount("did-finish-load"), 0);
  assert.equal(window.listenerCount("enter-full-screen"), 0);
  assert.equal(window.listenerCount("leave-full-screen"), 0);
});

test("native frames publish fullscreen without using macOS window controls", () => {
  const window = new EventEmitter(), contents = new EventEmitter();
  let published;
  Object.assign(contents, { isDestroyed: () => false, send: (_channel, value) => { published = value; } });
  Object.assign(window, { webContents: contents, isDestroyed: () => false, isFullScreen: () => true });
  bindWindowChrome(window);
  window.emit("enter-full-screen");
  assert.equal(published, true);
});

test("closing a destroyed native window does not access its webContents getter", () => {
  const window = new EventEmitter(), contents = new EventEmitter();
  let destroyed = false;
  Object.assign(contents, { isDestroyed: () => destroyed, send: () => assert.fail("sent after destruction") });
  Object.assign(window, { isDestroyed: () => destroyed });
  Object.defineProperty(window, "webContents", { get() {
    if (destroyed) throw new TypeError("Object has been destroyed");
    return contents;
  } });
  bindWindowChrome(window, { inset: true });
  destroyed = true;
  assert.doesNotThrow(() => contents.emit("did-finish-load"));
  assert.doesNotThrow(() => window.emit("closed"));
  assert.equal(contents.listenerCount("did-finish-load"), 0);
  assert.equal(window.listenerCount("enter-full-screen"), 0);
  assert.equal(window.listenerCount("leave-full-screen"), 0);
});

test("preload projects only boolean fullscreen state, including before DOM readiness", async () => {
  const ipcRenderer = new EventEmitter();
  let ready;
  const document = { documentElement: null, addEventListener: (event, listener) => {
    assert.equal(event, "DOMContentLoaded"); ready = listener;
  } };
  const source = await fs.readFile(new URL("../src/preload/workbench.cjs", import.meta.url), "utf8");
  vm.runInNewContext(source, { document, process: { argv: [] }, require: () => ({
    ipcRenderer, contextBridge: { exposeInMainWorld() {} },
  }) });
  ipcRenderer.emit("sparkclaw-window:fullscreen", {}, true);
  document.documentElement = { dataset: {} };
  ready();
  assert.equal(document.documentElement.dataset.nativeFullscreen, "true");
  ipcRenderer.emit("sparkclaw-window:fullscreen", {}, { fullscreen: false });
  assert.equal(document.documentElement.dataset.nativeFullscreen, "true");
  ipcRenderer.emit("sparkclaw-window:fullscreen", {}, false);
  assert.equal(document.documentElement.dataset.nativeFullscreen, "false");
});
