import { app, BrowserWindow, WebContentsView, ipcMain, protocol, session } from "electron";
import assert from "node:assert/strict";
import fs from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { DesktopCapability } from "../src/main/desktop-capability.mjs";
import { BrowserPresentation } from "../src/main/presentation.mjs";
import { PageRegistry } from "../src/browser/page-registry.mjs";

const temporary = process.env.SPARKCLAW_LAYOUT_ROOT;
const directory = path.dirname(fileURLToPath(import.meta.url));
app.setName("SparkX isolated layout regression");
app.setPath("userData", path.join(temporary, "profile"));
protocol.registerSchemesAsPrivileged([{ scheme: "sparkclaw-app", privileges: { standard: true, secure: true, supportFetchAPI: true } }]);
const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
app.whenReady().then(async () => {
  app.dock?.hide();
  let window, capability, registry;
  try {
    const [javascript, css] = await Promise.all(["panel.js", "panel.css"].map(file => fs.readFile(path.join(temporary, "ui", file), "utf8")));
    const html = `<html><head><meta charset="utf-8"><style>${css}</style></head><body><div id="root"></div><script>${javascript}</script></body></html>`;
    session.defaultSession.webRequest.onBeforeRequest(({ url }, callback) => callback({ cancel: !url.startsWith("sparkclaw-app://workbench/") }));
    session.defaultSession.protocol.handle("sparkclaw-app", () => new Response(html, { headers: { "content-type": "text/html; charset=utf-8" } }));
    window = new BrowserWindow({ show: false, width: 1440, height: 900, webPreferences: {
      preload: path.join(directory, "../src/preload/workbench.cjs"), sandbox: true, contextIsolation: true, nodeIntegration: false, backgroundThrottling: false } });
    const browserSession = session.fromPartition("isolated-panel-native");
    browserSession.protocol.handle("https", () => new Response("<html><title>Isolated page</title><body>Fixture <input value='retained native input'></body></html>", { headers: { "content-type": "text/html" } }));
    browserSession.webRequest.onBeforeRequest(({ url }, callback) => callback({ cancel: !url.startsWith("https://fixture.test/") && url !== "about:blank" }));
    const shieldView = new WebContentsView({ webPreferences: { session: browserSession, sandbox: true, nodeIntegration: false, backgroundThrottling: false } });
    const presentation = new BrowserPresentation({ window, shieldView, explicitSelection: true });
    registry = new PageRegistry({ runtimeGeneration: "a".repeat(32), browserSession, presentation,
      createView: () => new WebContentsView({ webPreferences: { session: browserSession, sandbox: true, nodeIntegration: false, backgroundThrottling: false } }) });
    capability = new DesktopCapability({ ipcMain, window, registry, presentation,
      browserServices: { snapshot: () => ({ downloads: [], permissions: [] }) }, runtimeGeneration: "a".repeat(32) }).start();
    window.setContentSize(1440, 900);
    await window.loadURL("sparkclaw-app://workbench/index.html");
    const evaluate = expression => window.webContents.executeJavaScript(expression);
    async function until(fn, label) {
      const deadline = Date.now() + 5000;
      while (Date.now() < deadline) { if (await fn()) return; await pause(20); }
      throw new Error(label);
    }
    async function check(label) {
      await until(async () => {
        const measured = await evaluate(`(() => { const host = document.querySelector('.desktopBrowserHost'); return { host: host?.getBoundingClientRect().toJSON(), error: document.querySelector('.desktopBrowserError')?.textContent }; })()`);
        assert.ok(!measured.error, `${label}: ${measured.error}`);
        if (!measured.host?.width) return false;
        const rect = measured.host, zoom = window.webContents.getZoomFactor();
        const x = Math.ceil(rect.x), y = Math.ceil(rect.y), right = Math.floor(rect.right), bottom = Math.floor(rect.bottom);
        const expected = { x: Math.ceil(x * zoom), y: Math.ceil(y * zoom), width: Math.floor(right * zoom) - Math.ceil(x * zoom), height: Math.floor(bottom * zoom) - Math.ceil(y * zoom) };
        if (JSON.stringify(presentation.bounds()) !== JSON.stringify(expected)) return false;
        const content = window.getContentBounds(), bounds = presentation.bounds();
        assert.ok(bounds.width > 0 && bounds.height > 0 && bounds.x + bounds.width <= content.width && bounds.y + bounds.height <= content.height, label);
        return true;
      }, `${label}: bounds did not settle: ${JSON.stringify(presentation.bounds())}`);
    }
    await check("initial direct blank");
    assert.equal(registry.records.size, 1);
    assert.equal(presentation.presented, null, "blank is DOM only");
    await until(() => evaluate("document.activeElement === document.querySelector('.desktopAddressBar input')"), "address must receive focus");
    const cases = [];
    for (const [width, height] of [[1440,900], [1180,720], [1920,1080]]) {
      window.setContentSize(width, height);
      for (const textSize of ["default", "large"]) for (const collapsed of [false, true]) for (const zoom of [1, 1.25, 1.5, 2]) {
        window.webContents.setZoomFactor(zoom);
        await evaluate(`document.documentElement.dataset.textSize=${JSON.stringify(textSize)}; document.querySelector('.workbench').classList.toggle('sidebarCollapsed',${collapsed})`);
        await pause(220);
        await check(`${width}x${height},${textSize},${collapsed},${zoom}`);
        cases.push({ width, height, textSize, collapsed, zoom });
        assert.equal(await evaluate("document.querySelector('#chat-input').value"), "Preserved draft");
      }
    }
    window.webContents.setZoomFactor(1); window.setContentSize(1440,900);
    await evaluate("document.documentElement.dataset.textSize='default'; document.querySelector('.workbench').classList.remove('sidebarCollapsed')"); await pause(220);
    // Native page lifetime, dialog occlusion and collapse are checked with a real view.
    await evaluate(`(() => { const input=document.querySelector('.desktopAddressBar input'); Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(input,'https://fixture.test/page'); input.dispatchEvent(new Event('input',{bubbles:true})); })()`);
    await evaluate("document.querySelector('.desktopAddressBar').requestSubmit()");
    await until(() => presentation.presented?.role === "personal", "page not shown");
    const record = presentation.presented;
    await check("real view");
    assert.equal(record.view.getVisible(), true);
    await evaluate("document.querySelector('#show-modal').click()");
    await until(() => !record.view.getVisible(), "native view obscures modal");
    await evaluate("document.querySelector('#close-modal').click()");
    await until(() => record.view.getVisible(), "native view not restored");
    await evaluate("document.querySelector('[aria-label=\"Collapse browser\"]').click()");
    await until(() => !record.view.getVisible(), "native view survives collapse");
    presentation.showPersonal(record); // background presentation is fenced too.
    assert.equal(record.view.getVisible(), false);
    const before = registry.records.size;
    await evaluate("document.querySelector('#toggle-panel').click()"); await check("reopen creates blank");
    await until(() => registry.records.size === before + 1, "opening needs one new blank");
    await evaluate("document.querySelector('#toggle-panel').click()"); await pause(30);
    await evaluate("document.querySelector('#toggle-panel').click()"); await pause(100);
    assert.equal(registry.records.size, before + 1, "rapid opens reuse the unused blank");
    assert.equal(await record.webContents.executeJavaScript("document.querySelector('input').value"), "retained native input");
    // Narrowing preserves the currently focused conversation instead of covering it.
    await evaluate("document.querySelector('#chat-input').focus()"); window.setContentSize(1180,720); await pause(220);
    assert.equal(await evaluate("document.querySelector('.workspace').classList.contains('conversationFocus')"), true);
    assert.equal(await evaluate("document.querySelector('.desktopBrowserPanel').hidden"), true);
    await evaluate("document.querySelector('#toggle-panel').click()"); await check("explicit browser focus");
    assert.equal(await evaluate("document.querySelector('.chatColumn').inert"), true);
    window.setContentSize(1440,900); await pause(220); await check("automatic split recovery");
    // Divider supports keyboard, pointer resizing, and drag-to-hide.
    await evaluate("document.querySelector('.inspectorResizeHandle').focus()");
    window.webContents.sendInputEvent({ type: "keyDown", keyCode: "Left", modifiers: ["shift"] });
    window.webContents.sendInputEvent({ type: "keyUp", keyCode: "Left", modifiers: ["shift"] }); await pause(220);
    assert.equal(await evaluate("Number(document.querySelector('.inspectorResizeHandle').getAttribute('aria-valuenow'))"), 704);
    await evaluate("document.querySelector('.inspectorResizeHandle').dispatchEvent(new KeyboardEvent('keydown',{key:'Home',bubbles:true}))"); await pause(220);
    const handle = await evaluate("(() => { const r=document.querySelector('.inspectorResizeHandle').getBoundingClientRect(); return {x:Math.round(r.left+r.width/2),y:Math.round(r.top+r.height/2)} })()");
    window.webContents.sendInputEvent({ type:"mouseDown", x:handle.x, y:handle.y, button:"left", clickCount:1 });
    window.webContents.sendInputEvent({ type:"mouseMove", x:1438, y:handle.y, button:"left", modifiers:["leftButtonDown"] });
    window.webContents.sendInputEvent({ type:"mouseUp", x:1438, y:handle.y, button:"left", clickCount:1 });
    await until(() => evaluate("document.querySelector('.desktopBrowserPanel').hidden"), "drag-to-hide failed");
    console.log(JSON.stringify({ event:"browser_panel_layout",passed:true,cases:cases.length,real_component_css_preload_ipc:true,real_native_view:true,zoom:true,modal_occlusion:true,focus_layout:true,retained_page:true,divider_keyboard:true,divider_hide:true,network:false,production_data:false }));
  } finally { capability?.close(); for (const record of registry?.records.values() ?? []) record.webContents.close(); window?.destroy(); }
}).then(() => app.exit(0)).catch(error => { console.error(error); app.exit(1); });
