import { app, BrowserWindow, WebContentsView, session } from "electron";
import assert from "node:assert/strict";
import crypto from "node:crypto";
import fs from "node:fs/promises";
import path from "node:path";
import { BrowserPresentation } from "../src/main/presentation.mjs";
import { PageRegistry } from "../src/browser/page-registry.mjs";
import { BrowserHostAgent } from "../src/browser/host-agent.mjs";
import { nativePageCommand } from "../src/browser/native-page-commands.mjs";
import { pinnedHTTPSFetch } from "../src/main/pinned-https.mjs";
import { ISCPTransport } from "../src/main/iscp-transport.mjs";

const root = process.env.SPARKCLAW_HOST_ISCP_FIXTURE_ROOT;
app.setPath("userData", path.join(root, "profile"));
// The opt-in fixture needs a live compositor even when launched from a test
// terminal. These presentation switches do not alter the production app.
if (process.platform === "darwin") app.setActivationPolicy("regular");
app.commandLine.appendSwitch("disable-backgrounding-occluded-windows");
app.commandLine.appendSwitch("disable-renderer-backgrounding");
app.on("window-all-closed", () => {});
let agent, window, transport;
let collectErrors = true;
const evidence = { event: "sparkclaw_host_iscp_native_qualification", platform: process.platform, architecture: process.arch, electron: process.versions.electron, chromium: process.versions.chrome, fixture_compositor: { regular_activation: process.platform === "darwin", dock_shown: process.platform === "darwin", disable_occluded_backgrounding: true, disable_renderer_backgrounding: true }, direct_business_attempts: 0, host_websocket_attempts: 0, native_clicks: 0 };
const deadline = setTimeout(() => { console.error("Native ISCP host deadline exceeded"); app.exit(1); }, 90000);
void app.whenReady().then(async () => {
  try {
    if (process.platform === "darwin") await app.dock.show();
    const config = JSON.parse(await fs.readFile(path.join(root, "desktop-helper.json"), "utf8"));
    const installationID = process.env.SPARKCLAW_HOST_ISCP_INSTALLATION;
    const cert = await fs.readFile(path.join(root, "cert.pem"));
    const page = { schemaVersion: 2, origin: process.env.SPARKCLAW_HOST_ISCP_FIXTURE_ORIGIN, ca: cert.toString(), certificateSHA256: crypto.createHash("sha256").update(new crypto.X509Certificate(cert).raw).digest("hex") };
    const debugFetch = pinnedHTTPSFetch(page);
    const control = async (route, init = {}) => {
      assert.ok(["/qualify/step", "/qualify/evidence"].includes(route));
      return debugFetch(`${page.origin}${route}`, { ...init, headers: { ...init.headers, Authorization: `Bearer ${process.env.SPARKCLAW_HOST_ISCP_FIXTURE_TOKEN}` } });
    };
    const blocked = () => { evidence.direct_business_attempts++; throw new Error("Direct Gateway HTTP is forbidden in this ISCP fixture"); };
    globalThis.fetch = blocked;
    transport = new ISCPTransport({ configPath: path.join(root, "desktop-helper.json"), origin: "https://iscp.invalid", installationID, expectedBinding: config.binding, packaged: true, resourcesPath: path.join(root, "bin"), journalRoot: path.join(root, "profile", "iscp"), timeoutMS: 20000 });
    await transport.start();
    assert.equal(transport.capabilities.profile, "sparkclaw.workbench.transport.v2");
    await transport.invoke("installation.bind", { schema_version: 1, installation_id: installationID });
    const projection = await transport.invoke("capabilities.get");
    assert.equal(projection.capabilities.find(row => row.id === "browser")?.enabled, true);
    const auth = { descriptor: { transport: "iscp", origin: "https://iscp.invalid" }, generation: 1, status: { state: "connected", capabilities: { browser: true } }, connection: { ownerID: config.binding.owner_id, clientID: config.binding.client_id }, authorizedFetch: blocked, invokeISCP: async (...args) => { try { return await transport.invoke(...args); } catch (error) { if (collectErrors) evidence.iscp_error = { operation: args[0], status: error.status, code: error.code, message: error.message }; throw error; } } };
    const browserSession = session.fromPartition("persist:iscp-native-host-qualification");
    browserSession.setCertificateVerifyProc((request, callback) => { const valid = request.hostname === "127.0.0.1" && crypto.createHash("sha256").update(new crypto.X509Certificate(request.certificate.data).raw).digest("hex") === page.certificateSHA256; callback(valid ? 0 : -2); });
    browserSession.setPermissionRequestHandler((_webContents, _permission, callback) => callback(false));
    browserSession.webRequest.onBeforeRequest({ urls: ["http://*/*", "https://*/*", "ws://*/*", "wss://*/*"] }, (details, callback) => {
      const url = new URL(details.url);
      const allowed = url.origin === page.origin && ["/fixture", "/favicon.ico"].includes(url.pathname);
      if (!allowed) evidence.direct_business_attempts++;
      callback({ cancel: !allowed });
    });
    window = new BrowserWindow({ width: 1300, height: 900, show: false, webPreferences: { nodeIntegration: false, contextIsolation: true, sandbox: true } });
    await window.loadURL("data:text/html,<title>Native ISCP Host qualification</title>");
    window.show();
    window.focus();
    const shieldView = new WebContentsView({ webPreferences: { nodeIntegration: false, contextIsolation: true, sandbox: true, backgroundThrottling: false } });
    shieldView.setBackgroundColor("#00000000");
    await shieldView.webContents.loadURL("data:text/html,<title>Task input shield</title><style>html,body{margin:0;width:100%;height:100%;background:transparent;overflow:hidden}body{cursor:not-allowed}</style>");
    const presentation = new BrowserPresentation({ window, shieldView });
    presentation.setPanelBounds({ x: 520, y: 40, width: 700, height: 780 });
    const registry = new PageRegistry({ runtimeGeneration: crypto.randomBytes(16).toString("hex"), browserSession, presentation, createView: () => new WebContentsView({ webPreferences: { session: browserSession, nodeIntegration: false, contextIsolation: true, sandbox: true, webSecurity: true, backgroundThrottling: false } }) });
    let loseWriteReply = false;
    agent = new BrowserHostAgent({ auth, registry, userDataDir: path.join(root, "profile"), connect: () => { evidence.host_websocket_attempts++; throw new Error("Host WebSocket transport is forbidden"); }, execute: async (...args) => {
      let result;
      try { result = await nativePageCommand(...args); } catch (error) { evidence.native_error = { operation: args[1], message: error.message };
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
    } });
    await agent.start({ installation_id: installationID });
    await agent.grant();
    assert.equal(agent.state, "connected");
    const step = async (operation, args = {}, commandID = crypto.randomUUID()) => {
      const response = await control("/qualify/step", { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify({ operation, arguments: args, command_id: commandID }) });
      const result = await response.json();
      if (!response.ok) throw new Error(result.error);
      return result;
    };
    registry.selectConversation("native");
    await step("acquire");
    await step("navigate", { url: `${page.origin}/fixture` });
    assert.match((await step("read")).text, /Controlled native ISCP page/);
    let snapshot = await step("snapshot");
    const name = snapshot.snapshot.controls.find(item => item.name === "Name");
    assert.ok(name);
    await step("fill", { ref: name.ref, snapshot_id: snapshot.snapshot_id, value: "Reversible ISCP draft" });
    assert.match((await step("read")).text, /Draft: Reversible ISCP draft/);
    window.show(); app.focus({ steal: true }); window.focus();
    await new Promise(resolve => setTimeout(resolve, 200));
    evidence.screenshot_window_visible = window.isVisible();
    evidence.screenshot_window_focused = window.isFocused();
    const record = registry.conversationPages.get("native");
    evidence.selected_conversation = registry.selectedConversationID;
    evidence.presented_host_page = presentation.presented === record;
    evidence.surface = { visible: record.view.getVisible(), bounds: record.view.getBounds(), attached: window.contentView.children.includes(record.view), loading: record.webContents.isLoading() };
    evidence.surface.two_frames = await Promise.race([record.webContents.executeJavaScript("new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(()=>resolve(true))))"), new Promise(resolve=>setTimeout(()=>resolve(false),2000))]);
    evidence.read_and_reversible_fill = true;
    let screenshot, screenshotError;
    try { screenshot = await step("screenshot"); } catch (error) { screenshotError = error; }
    let png;
    if (screenshot) {
      assert.equal(screenshot.mimeType, "image/png");
      png = Buffer.from(screenshot.data, "base64");
      assert.deepEqual(png.subarray(0, 8), Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]));
      assert.ok(png.length > 500 && png.length <= (64 << 10));
      await fs.writeFile(path.join(root, "native-iscp-host.png"), png, { mode: 0o600 });
    }
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
    Object.assign(evidence, { unknown_write_not_repeated: true, production_gateway_adapters: true, pinned_issuer_and_reference_relay: true, host_channel: "iscp_poll_reply", real_electron_webcontents: true, iscp_operations: backend.iscp_operations, screenshot_passed: !screenshotError, local_unknown_receipts: 1, backend_unknown_receipts: backend.fences.length, production_data: false });
    if (screenshotError) throw new Error(`Native screenshot unavailable: ${screenshotError.message}`);
    Object.assign(evidence, { passed: true, real_electron_webcontents: true, production_gateway_adapters: true, pinned_issuer_and_reference_relay: true, host_channel: "iscp_poll_reply", read_and_reversible_fill: true, screenshot_bytes: png.length, unknown_write_not_repeated: true, iscp_operations: backend.iscp_operations, production_data: false });
  } catch (error) { evidence.error = error.message; console.error(error.stack); }
  finally {
    clearTimeout(deadline);
    collectErrors = false;
    await agent?.stop().catch(() => {});
    transport?.close();
    window?.destroy();
    await fs.writeFile(path.join(root, "evidence.json"), JSON.stringify(evidence, null, 2), { mode: 0o600 });
    console.log(JSON.stringify(evidence));
    app.exit(evidence.passed ? 0 : 1);
  }
});
