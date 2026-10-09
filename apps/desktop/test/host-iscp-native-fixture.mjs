import { app, BrowserWindow, session, net, netLog } from "electron";
import assert from "node:assert/strict";
import crypto from "node:crypto";
import fs from "node:fs/promises";
import path from "node:path";
import { BrowserHostAgent } from "../src/browser/host-agent.mjs";
import { pinnedHTTPSFetch } from "../src/main/pinned-https.mjs";

const root = process.env.SPARKCLAW_HOST_ISCP_FIXTURE_ROOT;
process.env.SPARKCLAW_DESKTOP_ISCP_LOCAL_TEST = "1";
process.env.SPARKCLAW_DESKTOP_ISCP_CONFIG = path.join(root, "desktop-profile.json");
process.env.SPARKCLAW_DESKTOP_USER_DATA_DIR = path.join(root, "profile");
let agent, window;
const startHost = BrowserHostAgent.prototype.start;
BrowserHostAgent.prototype.start = function (...args) { agent = this; return startHost.apply(this, args); };
const evidence = { event: "sparkclaw_host_iscp_native_qualification", platform: process.platform, architecture: process.arch, electron: process.versions.electron, chromium: process.versions.chrome, production_entry: true, compositor_switches: [], direct_business_attempts: 0, host_websocket_attempts: 0, native_clicks: 0 };
const deadline = setTimeout(() => { console.error("Native ISCP host deadline exceeded"); app.exit(1); }, 90000);
const blocked = () => { evidence.direct_business_attempts++; throw new Error("Direct Gateway HTTP is forbidden in this ISCP fixture"); };
globalThis.fetch = blocked;
const originalNetFetch = net.fetch.bind(net);
net.fetch = (...args) => /^https?:/u.test(String(args[0]?.url || args[0])) ? blocked() : originalNetFetch(...args);
const logStarted = app.whenReady().then(()=>netLog.startLogging(path.join(root,"native-network.json")));
await import("../src/main/main.mjs");
const delay = milliseconds => new Promise(resolve => setTimeout(resolve, milliseconds));
async function until(predicate, label) { const deadline = Date.now()+30000; while(Date.now()<deadline) { if(await predicate())return; await delay(100); } throw new Error(`Timed out: ${label}`); }
void app.whenReady().then(async () => {
  try {
    await logStarted;
    for (const flag of ["disable-backgrounding-occluded-windows","disable-renderer-backgrounding"]) assert.equal(app.commandLine.hasSwitch(flag),false);
    const config = JSON.parse(await fs.readFile(path.join(root, "desktop-helper.json"), "utf8"));
    const installationID = process.env.SPARKCLAW_HOST_ISCP_INSTALLATION;
    const cert = await fs.readFile(path.join(root, "cert.pem"));
    const page = { schemaVersion: 2, origin: process.env.SPARKCLAW_HOST_ISCP_FIXTURE_ORIGIN, ca: cert.toString(), certificateSHA256: crypto.createHash("sha256").update(new crypto.X509Certificate(cert).raw).digest("hex") };
    const debugFetch = pinnedHTTPSFetch(page);
    const control = async (route, init = {}) => {
      assert.ok(["/qualify/step", "/qualify/evidence"].includes(route));
      return debugFetch(`${page.origin}${route}`, { ...init, headers: { ...init.headers, Authorization: `Bearer ${process.env.SPARKCLAW_HOST_ISCP_FIXTURE_TOKEN}` } });
    };
    await until(async () => { window = BrowserWindow.getAllWindows()[0]; return window && !window.webContents.isLoading() && await window.webContents.executeJavaScript("Boolean(window.sparkclawDesktop && document.querySelector('.localWorkbench'))"); }, "real workbench");
    const evaluate = source => window.webContents.executeJavaScript(source, true);
    await until(async () => (await evaluate("window.sparkclawDesktop.localConnection()")).state === "connected" && agent, "native ISCP connection");
    assert.equal((await evaluate("window.sparkclawDesktop.localConnection()")).capabilities.browser, true);
    const registry = agent.registry;
    assert.equal(registry.qualification,false);

    window.webContents.session.webRequest.onBeforeRequest({urls:["http://*/*","https://*/*","ws://*/*","wss://*/*"]},(_details,callback)=>{ evidence.direct_business_attempts++;callback({cancel:true}); });
    const browserSession = session.fromPartition("persist:sparkclaw-browser-default");
    browserSession.setCertificateVerifyProc((request, callback) => { const valid = request.hostname === "127.0.0.1" && crypto.createHash("sha256").update(new crypto.X509Certificate(request.certificate.data).raw).digest("hex") === page.certificateSHA256; callback(valid ? 0 : -2); });
    browserSession.setPermissionRequestHandler((_webContents, _permission, callback) => callback(false));
    browserSession.webRequest.onBeforeRequest({ urls: ["http://*/*", "https://*/*", "ws://*/*", "wss://*/*"] }, (details, callback) => {
      const url = new URL(details.url);
      const allowed = url.origin === page.origin && ["/fixture", "/favicon.ico"].includes(url.pathname);
      if (!allowed) evidence.direct_business_attempts++;
      callback({ cancel: !allowed });
    });
    let loseWriteReply = false;
    agent.connect = () => { evidence.host_websocket_attempts++; throw new Error("Host WebSocket transport is forbidden"); };
    const productionExecute = agent.execute;
    agent.execute = async (...args) => {
      let result;
      try { result = await productionExecute(...args); } catch (error) { evidence.native_error = { operation: args[1], message: error.message };
        throw error; }
      if (args[1] === "screenshot") evidence.native_screenshot_bytes = Buffer.from(result.data, "base64").length;
      if (args[1] === "click") {
        evidence.native_clicks++;
        evidence.observed_counter = await args[0].webContents.executeJavaScript("document.querySelector('#counter').textContent");
      }
      if (loseWriteReply && args[1] === "click") {
        // Inject loss only after the actual Chromium effect, before the host
        // can send its result. The real local journal and Broker must fence it.
        await agent.suspend();
        args[4]();
      }
      return result;
    };
    await evaluate("window.sparkclawDesktop.grantBrowserHost()");
    assert.equal(agent.state, "connected");
    const step = async (operation, args = {}, commandID = crypto.randomUUID()) => {
      const response = await control("/qualify/step", { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify({ operation, arguments: args, command_id: commandID }) });
      const result = await response.json();
      if (!response.ok) throw new Error(result.error);
      return result;
    };
    await evaluate('window.sparkclawDesktop.selectConversation("other")');
    const contentBounds = window.getContentBounds();
    evidence.window_content_bounds = contentBounds;
    await evaluate(`window.sparkclawDesktop.setBounds(${JSON.stringify({x:contentBounds.width-640,y:0,width:640,height:720})},100000)`);
    await step("acquire");
    await step("navigate", { url: `${page.origin}/fixture` });
    assert.match((await step("read")).text, /Controlled native ISCP page/);
    let snapshot = await step("snapshot");
    const name = snapshot.snapshot.controls.find(item => item.name === "Name");
    assert.ok(name);
    await step("fill", { ref: name.ref, snapshot_id: snapshot.snapshot_id, value: "Reversible ISCP draft" });
    assert.match((await step("read")).text, /Draft: Reversible ISCP draft/);
    window.show(); window.focus();
    await new Promise(resolve => setTimeout(resolve, 200));
    evidence.screenshot_window_visible = window.isVisible();
    evidence.screenshot_window_focused = window.isFocused();
    const record = registry.conversationPages.get("native");
    evidence.selected_conversation = registry.selectedConversationID;
    evidence.presented_host_page = registry.presentation.presented === record;
    evidence.child_views = window.contentView.children.map(view => ({id:view.webContents?.id, visible:view.getVisible()}));
    evidence.surface = { visible: record.view.getVisible(), bounds: record.view.getBounds(), attached: window.contentView.children.includes(record.view), loading: record.webContents.isLoading() };
    evidence.surface.two_frames = await Promise.race([record.webContents.executeJavaScript("new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(()=>resolve(true))))"), new Promise(resolve=>setTimeout(()=>resolve(false),2000))]);
    evidence.read_and_reversible_fill = true;
    let screenshotError, png;
    evidence.capture_modes = [];
    const capture = async mode => {
      try {
        const presentation = { visible:window.isVisible(), focused:window.isFocused(), viewVisible:record.view.getVisible(), selected:registry.selectedConversationID };
        const state = await step("snapshot");
        const field = state.snapshot.controls.find(item => item.name === "Name");
        await step("fill", { ref: field.ref, snapshot_id: state.snapshot_id, value: `Capture mode: ${mode}` });
        assert.match((await step("read")).text, new RegExp(`Capture mode: ${mode}`));
        const screenshot = await step("screenshot");
        assert.equal(screenshot.mimeType, "image/png");
        png = Buffer.from(screenshot.data, "base64");
        assert.deepEqual(png.subarray(0, 8), Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]));
        assert.ok(png.length > 500 && png.length <= (64 << 10));
        assert.equal(png.readUInt32BE(16),480);
        assert.equal(png.readUInt32BE(20),540);
        assert.deepEqual({ visible:window.isVisible(), focused:window.isFocused(), viewVisible:record.view.getVisible(), selected:registry.selectedConversationID },presentation,"Capture must preserve presentation and focus");
        assert.equal(record.webContents.debugger.isAttached(),false,"Capture must release its debugger");
        const filename = `native-iscp-host-${mode}.png`;
        await fs.writeFile(path.join(root, filename), png, {mode:0o600});
        evidence.capture_modes.push({mode,passed:true,window_visible:window.isVisible(),window_focused:window.isFocused(),view_visible:record.view.getVisible(),bytes:png.length,sha256:crypto.createHash("sha256").update(png).digest("hex"),filename});
      } catch(error) { screenshotError ||= error; evidence.capture_modes.push({mode,passed:false,error:error.message}); }
      await fs.writeFile(path.join(root,"progress.json"),JSON.stringify(evidence,null,2),{mode:0o600});
    };
    await capture("initial_unselected_conversation");
    await evaluate('window.sparkclawDesktop.selectConversation("native")');
    await capture("foreground");
    const occluder = new BrowserWindow({ ...window.getBounds(), show: true, webPreferences:{sandbox:true,contextIsolation:true,nodeIntegration:false} });
    await occluder.loadURL("data:text/html,<h1>Browser capture occlusion fixture</h1>");
    occluder.focus();
    await delay(500);
    await capture("occluded_background");
    occluder.destroy();
    window.hide();
    await delay(250);
    await capture("hidden_window");
    window.show(); window.focus();
    await delay(250);
    await capture("restored_window");
    await evaluate('window.sparkclawDesktop.selectConversation("other")');
    await capture("unselected_conversation");
    await evaluate('window.sparkclawDesktop.selectConversation("native")');
    await capture("restored_conversation");
    assert.equal(new Set(evidence.capture_modes.filter(row=>row.passed).map(row=>row.sha256)).size,evidence.capture_modes.filter(row=>row.passed).length,"Every capture must reflect its new draft");
    if (png) await fs.writeFile(path.join(root,"native-iscp-host.png"),png,{mode:0o600});
    snapshot = await step("snapshot");
    const button = snapshot.snapshot.controls.find(item => item.name === "Increment");
    assert.ok(button);
    loseWriteReply = true;
    const writeID = crypto.randomUUID();
    const writeArgs = { ref: button.ref, snapshot_id: snapshot.snapshot_id };
    await assert.rejects(step("click", writeArgs, writeID), /unknown|reconciliation/u);
    assert.equal(evidence.native_clicks, 1);
    assert.equal(evidence.observed_counter, "Counter: 1");
    assert.equal(registry.records.size, 0);
    assert.equal(agent.journal.unknown({ owner_id: config.binding.owner_id, client_id: config.binding.client_id, installation_id: installationID }).length, 1);
    await assert.rejects(step("click", writeArgs, writeID), /unknown|reconciliation|fenced/u);
    const backend = await (await control("/qualify/evidence")).json();
    assert.equal(backend.fences.length, 1);
    assert.equal(backend.fences[0].state, "unknown");
    assert.equal(backend.fences[0].command_id, writeID);
    for (const operation of ["browser.host.grant", "browser.host.register", "browser.host.poll", "browser.host.reply", "browser.receipt"]) assert.ok(backend.iscp_operations[operation] > 0, `${operation} did not traverse ISCP`);
    assert.equal(backend.direct_business_attempts, 0);
    assert.equal(evidence.direct_business_attempts, 0);
    assert.equal(evidence.host_websocket_attempts, 0);
    assert.equal(evidence.native_clicks, 1);
    Object.assign(evidence, { unknown_write_not_repeated: true, production_gateway_adapters: true, pinned_issuer_and_compatible_local_relay: true, host_channel: "iscp_poll_reply", real_electron_webcontents: true, iscp_operations: backend.iscp_operations, screenshot_passed: !screenshotError, local_unknown_receipts: 1, backend_unknown_receipts: backend.fences.length, production_data: false });
    if (screenshotError) throw new Error(`Native screenshot unavailable: ${screenshotError.message}`);
    Object.assign(evidence, { passed: true, real_electron_webcontents: true, production_gateway_adapters: true, pinned_issuer_and_compatible_local_relay: true, host_channel: "iscp_poll_reply", read_and_reversible_fill: true, screenshot_bytes: png.length, unknown_write_not_repeated: true, iscp_operations: backend.iscp_operations, production_data: false });
  } catch (error) { evidence.error = error.message; console.error(error.stack); }
  finally {
    clearTimeout(deadline);
    await agent?.stop().catch(() => {});
    await netLog.stopLogging();
    await fs.chmod(path.join(root,"native-network.json"),0o600);
    try {
      const network = JSON.parse(await fs.readFile(path.join(root,"native-network.json"),"utf8"));
      const urls = [...new Set(network.events.map(event=>event.params?.url).filter(url=>typeof url === "string" && /^(https?|wss?):/u.test(url)))];
      assert.ok(urls.length > 0,"NetLog must observe the controlled page request");
      const business = urls.filter(raw=>{const url=new URL(raw);return url.origin !== process.env.SPARKCLAW_HOST_ISCP_FIXTURE_ORIGIN || !["/fixture","/favicon.ico"].includes(url.pathname);});
      evidence.chromium_network = {controlled_page_urls:urls.length,direct_business_urls:business.length};
      assert.deepEqual(business,[]);
    } catch(error) { evidence.passed=false;evidence.error=error.message; }

    agent?.auth.transport?.close();
    await fs.writeFile(path.join(root, "evidence.json"), JSON.stringify(evidence, null, 2), { mode: 0o600 });
    console.log(JSON.stringify(evidence));
    window?.destroy();
    app.exit(evidence.passed ? 0 : 1);
  }
});
