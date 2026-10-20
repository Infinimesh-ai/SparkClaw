import assert from "node:assert/strict";
import test from "node:test";

import { DesktopCapability } from "../src/main/desktop-capability.mjs";

test('queued state notification does not access a destroyed native window',async()=>{
 let destroyed=false;
 const window={isDestroyed:()=>destroyed,get webContents(){throw new Error('Object has been destroyed');}};
 const capability=new DesktopCapability({window});
 capability.changed();destroyed=true;
 await new Promise(resolve=>setImmediate(resolve));
 assert.equal(capability.emitQueued,false);
});

test("desktop capability admits only the trusted workbench main frame and opaque bounded operations", async () => {
  let handler;
  const ipcMain = {
    handle(channel, callback) { assert.equal(channel, "sparkclaw-desktop:invoke"); handler = callback; },
    removeHandler() {},
  };
  const mainFrame = { url: "sparkclaw-app://workbench/index.html" };
  const sent = [];
  const webContents = { mainFrame, isDestroyed: () => false, send: (...args) => sent.push(args) };
  const window = { webContents, getContentBounds: () => ({ width: 1440, height: 900 }) };
  const calls = [];
  const registry = {
    setChangeListener(listener) { this.listener = listener; },
    desktopSnapshot: () => [],
    async createPersonalPage(url) { calls.push(["create", url]); return { pageRef: "page_0123456789abcdef0123456789abcdef" }; },
    async navigatePersonal(ref, url) { calls.push(["navigate", ref, url]); },
    showByRef(ref, role) { calls.push(["show", ref, role]); },
    hidePresented() { calls.push(["hide"]); },
  };
  const presentation = {
    status: () => ({ panel_bounds: { x: 680, y: 80, width: 640, height: 720 }, insufficient_space: false, presented_page_ref: "" }),
    setPanelBounds(bounds) { calls.push(["bounds", bounds]); },
  };
  const browserServices = {
    snapshot: () => ({ downloads: [], permissions: [] }),
    respondPermission() {}, cancelDownload() {}, showDownload() {},
  };
  const capability = new DesktopCapability({
    ipcMain, window, registry, presentation, browserServices, runtimeGeneration: "a".repeat(32),
  }).start();
  const event = { sender: webContents, senderFrame: mainFrame };

  const state = await handler(event, { schema_version: 1, operation: "state" });
  assert.equal(state.runtime_generation, "a".repeat(32));
  await assert.rejects(handler(event, { schema_version: 1, operation: "grantBrowserHost" }), /operation is unavailable/);
  await assert.rejects(handler({ sender: webContents, senderFrame: { url: mainFrame.url } }, {
    schema_version: 1, operation: "state",
  }), /not trusted/);
  await assert.rejects(handler(event, {
    schema_version: 1, operation: "createPersonal", url: "http://example.test",
  }), /HTTPS/);
  await handler(event, { schema_version: 1, operation: "createPersonal", url: "https://example.test/path" });
  await handler(event, {
    schema_version: 1, operation: "observeTask", page_ref: "page_0123456789abcdef0123456789abcdef",
  });
  await handler(event, {
    schema_version: 1,
    operation: "setBounds",
    revision: 1,
    bounds: { x: 680, y: 80, width: 640, height: 720 },
  });
  await handler(event, {
    schema_version: 1,
    operation: "setBounds",
    revision: 2,
    bounds: { x: 1120, y: 80, width: 320, height: 180 },
  });
  await assert.rejects(handler(event, {
    schema_version: 1,
    operation: "setBounds",
    revision: 3,
    bounds: { x: 0, y: 0, width: 1441, height: 900 },
  }), /outside the workbench/);
  const afterLayout = await handler(event, { schema_version: 1, operation: "state" });
  assert.equal(afterLayout.presentation.layout_revision, 2, "a remounted renderer resumes the accepted layout revision");
  await assert.rejects(handler(event, {
    schema_version: 1, operation: "setBounds", revision: 3,
    bounds: { x: 801, y: 225, width: 639, height: 676 },
  }), /outside the workbench/, "even a one-pixel bottom overflow stays forbidden");
  assert.deepEqual(calls, [
    ["create", "https://example.test/path"],
    ["show", "page_0123456789abcdef0123456789abcdef", "task"],
    ["bounds", { x: 680, y: 80, width: 640, height: 720 }],
    ["bounds", { x: 1120, y: 80, width: 320, height: 180 }],
  ]);
  registry.listener();
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(sent.at(-1)[0], "sparkclaw-desktop:state");
  capability.close();
});

test("native bounds convert page zoom once and hidden views stay dismissible after capability loss", async () => {
  let invoke, browser = true, bounds, hidden = false;
  const ipcMain = { handle(_channel, fn) { invoke = fn; }, removeHandler() {} };
  const mainFrame = { url: "sparkclaw-app://workbench/index.html" };
  const webContents = { mainFrame, getZoomFactor: () => 1.5, isDestroyed: () => false, send() {} };
  const capability = new DesktopCapability({ ipcMain, window: { webContents, getContentBounds: () => ({ width:1440,height:900 }) },
    runtimeGeneration: "a".repeat(32), getCapabilities: () => ({ browser }),
    registry: { setChangeListener() {}, desktopSnapshot: () => [], hidePresented() { hidden = true; } },
    browserServices: { snapshot: () => ({ downloads:[],permissions:[] }) },
    presentation: { status: () => ({}), setPanelBounds(value) { bounds = value; }, setPanelVisible(value) { assert.equal(value,false); } },
  }).start();
  const event = { sender:webContents,senderFrame:mainFrame };
  try {
    await invoke(event,{schema_version:1,operation:"setBounds",revision:1,bounds:{x:501,y:101,width:459,height:499}});
    assert.deepEqual(bounds,{x:752,y:152,width:688,height:748});
    await assert.rejects(invoke(event,{schema_version:1,operation:"setBounds",revision:2,bounds:{x:501,y:101,width:460,height:499}}),/outside the workbench/);
    browser = false;
    await invoke(event,{schema_version:1,operation:"hideBrowser"}); assert.equal(hidden,true);
    await assert.rejects(invoke(event,{schema_version:1,operation:"createPersonal"}),/unavailable/);
  } finally { capability.close(); }
});

test("a disconnected session can hide a native page only through the trusted workbench", async () => {
  let invoke, hidden = 0;
  const ipcMain = { handle(_channel, fn) { invoke = fn; }, removeHandler() {} };
  const mainFrame = { url: "sparkclaw-app://workbench/index.html" };
  const webContents = { mainFrame };
  const capability = new DesktopCapability({ ipcMain, window: { webContents }, authorizeSession: () => false,
    registry: { setChangeListener() {}, hidePresented() { hidden++; } },
    presentation: { setPanelVisible(visible) { assert.equal(visible, false); } },
  }).start();
  const event = { sender: webContents, senderFrame: mainFrame };
  const request = { schema_version: 1, operation: "hideBrowser" };
  try {
    await assert.rejects(invoke({ ...event, senderFrame: { url: mainFrame.url } }, request), /not trusted/);
    await assert.rejects(invoke(event, { ...request, page_ref: "extra" }), /fields are invalid/);
    assert.equal(hidden, 0);
    await invoke(event, request);
    assert.equal(hidden, 1);
    for (const operation of ["state", "createPersonal", "presentPersonal", "observeTask", "setBounds", "grantBrowserHost"]) {
      await assert.rejects(invoke(event, { schema_version: 1, operation }), /session is locked/);
    }
    assert.equal(hidden, 1);
  } finally { capability.close(); }
});
