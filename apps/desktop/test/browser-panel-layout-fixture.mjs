import { app, BrowserWindow, WebContentsView, ipcMain, protocol, session } from "electron";
import assert from "node:assert/strict";
import fs from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { DesktopCapability } from "../src/main/desktop-capability.mjs";
import { BrowserPresentation } from "../src/main/presentation.mjs";
import { PageRegistry } from "../src/browser/page-registry.mjs";
import { bindWorkbenchActivation } from "../src/main/workbench-activation.mjs";
import { nativePageCommand } from "../src/browser/native-page-commands.mjs";

const temporary = process.env.SPARKCLAW_LAYOUT_ROOT;
const directory = path.dirname(fileURLToPath(import.meta.url));
app.setName("SparkX isolated layout regression");
app.setPath("userData", path.join(temporary, "profile"));
protocol.registerSchemesAsPrivileged([{ scheme: "sparkclaw-app", privileges: { standard: true, secure: true, supportFetchAPI: true } }]);
const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
app.whenReady().then(async () => {
  app.dock?.hide();
  let window, capability, registry, authorized = true;
  try {
    const [javascript, css] = await Promise.all(["panel.js", "panel.css"].map(file => fs.readFile(path.join(temporary, "ui", file), "utf8")));
    const html = `<html><head><meta charset="utf-8"><style>${css}</style></head><body><div id="root"></div><script>${javascript}</script></body></html>`;
    session.defaultSession.webRequest.onBeforeRequest(({ url }, callback) => callback({ cancel: !url.startsWith("sparkclaw-app://workbench/") }));
    session.defaultSession.protocol.handle("sparkclaw-app", () => new Response(html, { headers: { "content-type": "text/html; charset=utf-8" } }));
    window = new BrowserWindow({ show: false, width: 1440, height: 900, webPreferences: {
      preload: path.join(directory, "../src/preload/workbench.cjs"), sandbox: true, contextIsolation: true, nodeIntegration: false, backgroundThrottling: false } });
    window.on("close", event => { event.preventDefault(); window.hide(); });
    bindWorkbenchActivation(app, () => window);
    const browserSession = session.fromPartition("isolated-panel-native");
    browserSession.protocol.handle("https", async ({ url }) => {
      if (url.endsWith("/slow")) await pause(500);
      if (url.endsWith("/failure")) return Response.error();
      return new Response(url.endsWith("/redirect")
        ? '<html><img src="/slow"><script>setTimeout(() => location.replace("https://fixture.test/page"), 50)</script></html>'
        : "<html><title>Isolated page</title><body>Fixture <input value='retained native input'></body></html>", { headers: { "content-type": "text/html" } });
    });
    browserSession.webRequest.onBeforeRequest(({ url }, callback) => callback({ cancel: !url.startsWith("https://fixture.test/") && url !== "about:blank" }));
    const shieldView = new WebContentsView({ webPreferences: { session: browserSession, sandbox: true, nodeIntegration: false, backgroundThrottling: false } });
    const presentation = new BrowserPresentation({ window, shieldView, explicitSelection: true });
    registry = new PageRegistry({ runtimeGeneration: "a".repeat(32), browserSession, presentation,
      createView: () => new WebContentsView({ webPreferences: { session: browserSession, sandbox: true, nodeIntegration: false, backgroundThrottling: false } }) });
    capability = new DesktopCapability({ ipcMain, window, registry, presentation, authorizeSession: () => authorized,
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
    window.show();
    await pause(100);
    await check("initial visible window");
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
    const pageBounds = record.view.getBounds();
    window.close();
    await pause(100);
    app.emit("activate");
    await pause(100);
    assert.deepEqual(record.view.getBounds(), pageBounds, "reopening must not expand the page over the conversation");
    assert.equal(record.view.getVisible(), true);
    let abortedLoads = 0;
    record.webContents.on("did-fail-load", (_event, code, _description, _url, mainFrame) => {
      if (mainFrame && code === -3) abortedLoads++;
    });
    await evaluate(`(() => { const input=document.querySelector('.desktopAddressBar input'); Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(input,'https://fixture.test/redirect'); input.dispatchEvent(new Event('input',{bubbles:true})); })()`);
    await evaluate("document.querySelector('.desktopAddressBar').requestSubmit()");
    await until(() => abortedLoads > 0, "fixture must exercise Electron ERR_ABORTED");
    await until(() => record.webContents.getURL() === "https://fixture.test/page" && !record.webContents.isLoading(), "redirect did not finish");
    assert.ok(abortedLoads > 0, "fixture must exercise Electron ERR_ABORTED");
    await check("redirect after aborted initial load");
    const created = await evaluate("window.sparkclawDesktop.createPersonal('https://fixture.test/redirect')");
    const createdPage = registry.getPersonal(created.page_ref);
    assert.equal(createdPage.webContents.getURL(), "https://fixture.test/page");
    registry.closePersonal(created.page_ref);
    const beforePopup = new Set(registry.records.keys());
    await record.webContents.executeJavaScript("window.open('https://fixture.test/redirect'); void 0");
    await until(() => [...registry.records.values()].some(page => !beforePopup.has(page.tabID) && page.webContents.getURL() === "https://fixture.test/page" && !page.webContents.isLoading()), "redirected personal popup was lost");
    const popup = [...registry.records.values()].find(page => !beforePopup.has(page.tabID));
    assert.equal(popup.destroyed, false);
    registry.closePersonal(popup.pageRef);
    const failed = await evaluate(`window.sparkclawDesktop.navigatePersonal(${JSON.stringify(record.pageRef)}, 'https://fixture.test/failure').then(() => '', error => error.message)`);
    assert.match(failed, /ERR_FAILED/);
    await evaluate(`window.sparkclawDesktop.navigatePersonal(${JSON.stringify(record.pageRef)}, 'https://fixture.test/page')`);
    registry.showByRef(record.pageRef, "personal");
    await check("normal navigation after genuine failure");
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
    window.close();
    await pause(100);
    app.emit("activate");
    await pause(100);
    assert.equal(record.view.getVisible(), false, "reopening the window must keep collapsed pages hidden");
    assert.equal(await evaluate("document.querySelector('#chat-input').value"), "Preserved draft");
    // A restored workbench starts with its browser closed; native pages outlive
    // the renderer and cannot rely on React's unmount cleanup during reload.
    await evaluate("document.querySelector('#toggle-panel').click()");
    await evaluate("document.querySelector('.desktopTabSelect').click()");
    await until(() => record.view.getVisible(), "retained tab must be selectable");
    await window.loadURL("sparkclaw-app://workbench/index.html?closed=1");
    await until(() => evaluate("Boolean(document.querySelector('#chat-input'))"), "workbench did not reload");
    assert.equal(record.view.getVisible(), false, "reloaded workbench must not be covered by its retained page");
    const reselect = async () => {
      await evaluate("document.querySelector('#toggle-panel').click()");
      await until(() => evaluate("Boolean(document.querySelector('.desktopTabSelect'))"), "retained tabs did not return");
      await evaluate("document.querySelector('.desktopTabSelect').click()");
      await until(() => presentation.presented === record && record.view.getVisible(), "same page not restored");
      await check("retained page after renderer recovery");
      assert.deepEqual(record.view.getBounds(), presentation.bounds());
      assert.equal(await record.webContents.executeJavaScript("document.querySelector('input').value"), "retained native input");
    };
    await reselect();
    let crashed = false;
    window.webContents.once("render-process-gone", () => { crashed = true; });
    window.webContents.forcefullyCrashRenderer();
    await until(() => crashed, "workbench renderer did not crash");
    assert.equal(record.view.getVisible(), false, "crashed workbench must release native presentation immediately");
    assert.equal(shieldView.getVisible(), false);
    presentation.showPersonal(record);
    assert.equal(record.view.getVisible(), false, "background page cannot cover the recovering workbench");
    await window.loadURL("sparkclaw-app://workbench/index.html?closed=1");
    await until(() => evaluate("Boolean(document.querySelector('#chat-input'))"), "workbench did not recover");
    assert.equal(record.view.getVisible(), false);
    await reselect();
    // Connection loss can collapse the browser while the window is closed.
    // Reopening must not reveal the native page left behind by that cleanup.
    await evaluate("document.querySelector('[aria-label=\"Widen browser\"]').click()");
    await check("wide page before close");
    window.close();
    assert.equal(window.isVisible(), false);
    authorized = false;
    window.webContents.send("sparkclaw-local-backend:state", { state: "reconnecting", backend: { transport: "iscp" }, capabilities: { surfaces: { browser: { enabled: false } } } });
    await until(() => evaluate("document.querySelector('.desktopBrowserPanel').hidden"), "unavailable browser must collapse");
    await pause(100);
    authorized = true;
    window.webContents.send("sparkclaw-local-backend:state", { state: "connected", backend: { transport: "iscp" }, capabilities: { surfaces: { browser: { enabled: true } } } });
    app.emit("activate");
    await pause(100);
    assert.equal(record.view.getVisible(), false, "close/reopen after reconnect must not cover the conversation");
    assert.equal(await evaluate("document.querySelector('#chat-input').value"), "Preserved draft");
    await reselect();
    await evaluate("document.querySelector('[aria-label=\"Restore browser width\"]').click()");
    await check("split restored after close/reconnect");
    // A leased and a released task page use the same native panel geometry as
    // personal pages, including sizes below the former 640 x 720 threshold.
    const binding = { local_conversation_id: "layout", local_task_id: "task_layout", lease_id: "lease_layout", page_id: "page_layout", page_generation: 1 };
    registry.selectConversation("layout");
    const task = registry.acquireHostPage(binding);
    await task.hostInitialLoad;
    await task.webContents.loadURL("https://fixture.test/task");
    await until(() => evaluate("Boolean(document.querySelector('.desktopTabSelect[title=\"Isolated page\"]'))"), "task tab missing");
    const selectTask = async () => {
      await until(() => evaluate("[...document.querySelectorAll('.desktopTabSelect')].some(button => button.querySelector('.lucide-eye'))"), "task tab missing");
      await evaluate("[...document.querySelectorAll('.desktopTabSelect')].find(button => button.querySelector('.lucide-eye')).click()");
      await until(() => presentation.presented === task && task.view.getVisible(), "task page not shown");
    };
    await selectTask();
    const taskID = task.webContents.id;
    const taskCases = [];
    for (const released of [false, true]) {
      if (released) registry.releaseHostPage(binding);
      for (const [width, height] of [[1440,900], [1180,720], [1920,1080]]) {
        window.setContentSize(width, height);
        for (const zoom of [1, 1.5]) {
          window.webContents.setZoomFactor(zoom); await pause(220);
          if (await evaluate("document.querySelector('.desktopBrowserPanel').hidden")) await evaluate("document.querySelector('#toggle-panel').click()");
          await selectTask(); await check(`task ${width}x${height} zoom ${zoom} released ${released}`);
          assert.deepEqual(task.view.getBounds(), presentation.bounds());
          assert.deepEqual(shieldView.getBounds(), presentation.bounds());
          assert.equal(task.view.getVisible(), true); assert.equal(shieldView.getVisible(), true);
          assert.equal(task.webContents.id, taskID);
          assert.equal(presentation.status().insufficient_space, false);
          assert.equal(await task.webContents.executeJavaScript("document.querySelector('input').value"), "retained native input");
          taskCases.push({ width, height, zoom, released, viewport: task.view.getBounds() });
        }
      }
    }
    assert.ok(taskCases.some(item => item.viewport.width < 640));
    assert.ok(taskCases.some(item => item.viewport.height < 720));
    window.webContents.setZoomFactor(1); window.setContentSize(1440,900); await pause(220); await check("task action layout");
    await task.webContents.executeJavaScript("window.fixtureClicks=0;const button=document.createElement('button');button.textContent='Layout action';button.onclick=()=>window.fixtureClicks++;document.body.prepend(button)");
    const oldSnapshot = await nativePageCommand(task, "snapshot", {}, binding, () => {});
    const oldAction = oldSnapshot.snapshot.controls.find(item => item.name === "Layout action");
    window.setContentSize(1440,580); await pause(220); await check("task action resized");
    await assert.rejects(nativePageCommand(task, "click", { snapshot_id: oldSnapshot.snapshot_id, ref: oldAction.ref }, binding, () => {}), /viewport changed/);
    assert.equal(await task.webContents.executeJavaScript("window.fixtureClicks"), 0);
    const freshSnapshot = await nativePageCommand(task, "snapshot", {}, binding, () => {});
    const freshAction = freshSnapshot.snapshot.controls.find(item => item.name === "Layout action");
    await nativePageCommand(task, "click", { snapshot_id: freshSnapshot.snapshot_id, ref: freshAction.ref }, binding, () => {});
    assert.equal(await task.webContents.executeJavaScript("window.fixtureClicks"), 1);
    await evaluate("document.querySelector('#show-modal').click()");
    await until(() => !task.view.getVisible(), "task obscures modal");
    assert.equal(shieldView.getVisible(), false);
    await evaluate("document.querySelector('#close-modal').click()");
    await until(() => task.view.getVisible(), "task not restored after modal");
    console.log(JSON.stringify({ event:"browser_panel_layout",passed:true,cases:cases.length,task_cases:taskCases,task_resize_stale_action_refused:true,real_component_css_preload_ipc:true,real_native_view:true,zoom:true,modal_occlusion:true,focus_layout:true,retained_page:true,divider_keyboard:true,divider_hide:true,window_reopen:true,closed_window_reconnect:true,renderer_reload:true,renderer_crash_recovery:true,aborted_redirect:true,redirected_new_page:true,redirected_popup:true,real_failure_reported:true,network:false,production_data:false }));
  } finally { capability?.close(); for (const record of registry?.records.values() ?? []) record.webContents.close(); window?.destroy(); }
}).then(() => app.exit(0)).catch(error => { console.error(error); app.exit(1); });
