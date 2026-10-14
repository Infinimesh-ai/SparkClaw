import { app, BrowserWindow, ipcMain, protocol, session } from "electron";
import assert from "node:assert/strict";
import fs from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { DesktopCapability } from "../src/main/desktop-capability.mjs";

const temporary = process.env.SPARKCLAW_LAYOUT_ROOT;
const directory = path.dirname(fileURLToPath(import.meta.url));
app.setName("SparkX isolated layout regression");
app.setPath("userData", path.join(temporary, "profile"));
protocol.registerSchemesAsPrivileged([{ scheme: "sparkclaw-app", privileges: { standard: true, secure: true, supportFetchAPI: true } }]);

app.whenReady().then(async () => {
  app.dock?.hide();
  let window, capability, panelBounds, accepted = 0;
  try {
    const [javascript, css] = await Promise.all(["panel.js", "panel.css"].map(file => fs.readFile(path.join(temporary, "ui", file), "utf8")));
    const html = `<html><head><meta charset="utf-8"><style>${css}</style></head><body><div id="root"></div><script>${javascript}</script></body></html>`;
    session.defaultSession.webRequest.onBeforeRequest(({ url }, callback) => callback({ cancel: !url.startsWith("sparkclaw-app://workbench/") }));
    session.defaultSession.protocol.handle("sparkclaw-app", () => new Response(html, { headers: { "content-type": "text/html; charset=utf-8" } }));
    window = new BrowserWindow({ show: false, width: 1440, height: 900,
      webPreferences: { preload: path.join(directory, "../src/preload/workbench.cjs"), sandbox: true, contextIsolation: true, nodeIntegration: false, backgroundThrottling: false } });
    window.webContents.on("console-message", (details) => { if (details.level === "error") console.error(details.message); });
    const registry = { setChangeListener() {}, desktopSnapshot: () => [], hidePresented() {} };
    const presentation = {
      status: () => ({ panel_bounds: panelBounds, insufficient_space: false, presented_page_ref: "" }),
      setPanelBounds(bounds) { panelBounds = bounds; accepted++; },
    };
    capability = new DesktopCapability({ ipcMain, window, registry, presentation,
      browserServices: { snapshot: () => ({ downloads: [], permissions: [] }) }, runtimeGeneration: "a".repeat(32) }).start();
    window.setContentSize(1440, 836);
    await window.loadURL("sparkclaw-app://workbench/index.html");
    const evaluate = expression => window.webContents.executeJavaScript(expression);
    async function check(label, wide = false) {
      const deadline = Date.now() + 5000;
      while (Date.now() < deadline) {
        const measured = await evaluate(`(() => {
          const host = document.querySelector('.desktopBrowserHost');
          return { host: host?.getBoundingClientRect().toJSON(), error: document.querySelector('.desktopBrowserError')?.textContent };
        })()`);
        assert.ok(!measured.error, `${label}: ${measured.error}`);
        const rect = measured.host;
        if (rect && panelBounds) {
          const expected = { x: Math.ceil(rect.x), y: Math.ceil(rect.y),
            width: Math.floor(rect.right) - Math.ceil(rect.x), height: Math.floor(rect.bottom) - Math.ceil(rect.y) };
          if (JSON.stringify(panelBounds) === JSON.stringify(expected)) {
            const content = window.getContentBounds();
            // Split mode stays within 760 DIPs; expanded focus mode may use the whole workspace.
            assert.ok(panelBounds.width > 0 && (wide || panelBounds.width <= 760), label);
            assert.ok(panelBounds.x + panelBounds.width <= content.width, label);
            assert.ok(panelBounds.y + panelBounds.height <= content.height, label);
            return;
          }
        }
        await new Promise(resolve => setTimeout(resolve, 25));
      }
      throw new Error(`${label}: layout did not reach an accepted native rectangle: ${JSON.stringify({ panelBounds, dom: await evaluate("document.querySelector('.desktopBrowserHost')?.getBoundingClientRect().toJSON()") })}`);
    }
    await check("initial open with authorization toolbar");
    const cases = [];
    for (const [width, height] of [[1440, 836], [1180, 720], [1441, 837]]) {
      window.setContentSize(width, height);
      for (const textSize of ["default", "large"]) {
        await evaluate(`document.documentElement.dataset.textSize = ${JSON.stringify(textSize)}`);
        for (const collapsed of [false, true]) {
          await evaluate(`document.querySelector('.workbench').classList.toggle('sidebarCollapsed', ${collapsed})`);
          for (const wide of [false, true]) {
            await evaluate(`document.querySelector('[aria-label="${wide ? "Widen browser" : "Restore browser width"}"]')?.click()`);
            // Width transition produces intermediate bounds; inspect the settled layout.
            await new Promise(resolve => setTimeout(resolve, 220));
            const label = `${width}x${height}, ${textSize}, collapsed=${collapsed}, wide=${wide}`;
            await check(label, wide);
            cases.push(label);
          }
        }
      }
    }
    // Drag the real divider with native input events, as a user would.
    window.setContentSize(1440, 836);
    await evaluate(`document.documentElement.dataset.textSize = "default"; document.querySelector('.workbench').classList.remove('sidebarCollapsed'); document.querySelector('[aria-label="Restore browser width"]')?.click()`);
    await new Promise(resolve => setTimeout(resolve, 220));
    const handle = await evaluate(`(() => { const r = document.querySelector('.inspectorResizeHandle').getBoundingClientRect(); return { x: Math.round(r.left + r.width / 2), y: Math.round(r.top + r.height / 2) }; })()`);
    const panelWidth = () => evaluate("document.querySelector('.desktopBrowserPanel').getBoundingClientRect().width");
    const before = await panelWidth();
    window.webContents.sendInputEvent({ type: "mouseDown", x: handle.x, y: handle.y, button: "left", clickCount: 1 });
    for (let step = 1; step <= 6; step++) {
      window.webContents.sendInputEvent({ type: "mouseMove", x: handle.x - step * 10, y: handle.y, button: "left", modifiers: ["leftButtonDown"] });
      await new Promise(resolve => setTimeout(resolve, 16));
    }
    window.webContents.sendInputEvent({ type: "mouseUp", x: handle.x - 60, y: handle.y, button: "left", clickCount: 1 });
    await new Promise(resolve => setTimeout(resolve, 50));
    const after = await panelWidth();
    assert.ok(Math.abs(after - before - 60) <= 2, `divider drag widens the panel: ${before} -> ${after}`);
    await check("after divider drag");
    await evaluate("document.querySelector('#toggle-panel').click()");
    await new Promise(resolve => setTimeout(resolve, 50));
    assert.equal(await evaluate("document.querySelector('.desktopBrowserHost') === null"), true);
    await evaluate("document.querySelector('#toggle-panel').click()");
    await check("panel close and reopen");
    // Reload remounts the panel against the same main process layout revision.
    const reloaded = new Promise(resolve => window.webContents.once("did-finish-load", resolve));
    window.reload();
    await reloaded;
    await check("renderer reload");
    const revision = capability.layoutRevision + 1;
    const content = window.getContentBounds();
    const rejection = await evaluate(`window.sparkclawDesktop.setBounds({x:0,y:1,width:640,height:${content.height}},${revision}).then(()=>null,error=>error.message)`);
    assert.match(rejection, /outside the workbench/, "main process must still reject a one-pixel overflow");
    console.log(JSON.stringify({ event: "browser_panel_layout", passed: true, cases: cases.length, accepted_bounds: accepted,
      real_component_css_preload_ipc: true, panel_reopened: true, divider_drag: true, renderer_reload: true, overflow_rejected: true, network: false, production_data: false }));
  } finally {
    capability?.close();
    window?.destroy();
  }
}).then(() => app.exit(0)).catch(error => { console.error(error); app.exit(1); });
