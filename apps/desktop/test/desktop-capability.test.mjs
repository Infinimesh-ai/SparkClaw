import assert from "node:assert/strict";
import test from "node:test";

import { DesktopCapability } from "../src/main/desktop-capability.mjs";

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
    bounds: { x: 0, y: 0, width: 1440, height: 900 },
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
