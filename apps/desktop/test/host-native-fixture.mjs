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

const root = process.env.SPARKCLAW_HOST_QUALIFICATION_ROOT;
app.setPath("userData", path.join(root, "profile"));
let agent, window;
app.whenReady().then(async () => {
try {
  const cert = await fs.readFile(path.join(root, "cert.pem"));
  const descriptor = { schemaVersion: 2, origin: process.env.SPARKCLAW_HOST_QUALIFICATION_ORIGIN, ca: cert.toString(), certificateSHA256: crypto.createHash("sha256").update(new crypto.X509Certificate(cert).raw).digest("hex") };
  const authorization = "Bearer isolated-r3-native-qualification";
  const fetcher = pinnedHTTPSFetch(descriptor);
  const auth = { descriptor, status: { state: "connected" }, connection: { ownerID: "qualification-owner", clientID: "qualification-client", authorization }, authorizedFetch: (url, options = {}) => fetcher(url, { ...options, headers: { ...options.headers, Authorization: authorization } }) };
  const browserSession = session.fromPartition("persist:r3-native-qualification");
  // The synthetic self-signed fixture is explicitly trusted in this isolated
  // profile. WSS transport independently verifies chain+hostname+leaf pin.
  browserSession.setCertificateVerifyProc((request, callback) => { const valid = request.hostname === "127.0.0.1" && crypto.createHash("sha256").update(new crypto.X509Certificate(request.certificate.data).raw).digest("hex") === descriptor.certificateSHA256; callback(valid ? 0 : -2); });
  browserSession.setPermissionRequestHandler((_webContents, _permission, callback) => callback(false));
  window = new BrowserWindow({ width: 1500, height: 920, show: false, webPreferences: { nodeIntegration: false, contextIsolation: true, sandbox: true } });
  await window.loadURL("data:text/html,<title>workbench isolated qualification</title>");
  window.showInactive();
  const shieldView = new WebContentsView({ webPreferences: { nodeIntegration: false, contextIsolation: true, sandbox: true } });
  await shieldView.webContents.loadURL("data:text/html,<title>Task input shield</title>");
  const presentation = new BrowserPresentation({ window, shieldView });
  presentation.setPanelBounds({ x: 780, y: 80, width: 640, height: 720 });
  const registry = new PageRegistry({ runtimeGeneration: crypto.randomBytes(16).toString("hex"), browserSession, presentation, createView: () => new WebContentsView({ webPreferences: { session: browserSession, nodeIntegration: false, contextIsolation: true, sandbox: true, webSecurity: true } }) });
  let interruptWrite = false;
  agent = new BrowserHostAgent({ auth, registry, userDataDir: path.join(root, "profile"), execute: async (...args) => {
    const result = await nativePageCommand(...args);
    if (interruptWrite && args[1] === "click") {
      // The real native input effect has completed, then the response is lost.
      await new Promise((resolve) => setTimeout(resolve, 50));
      await agent.suspend(); args[4]();
    }
    return result;
  } });
  await agent.start({ installation_id: "qualification-installation" });
  assert.equal(agent.state, "ungranted"); await agent.grant();
  async function step(conversation, operation, args = {}, task = `task_${conversation}`) {
    const response = await auth.authorizedFetch(`${descriptor.origin}/qualify/step`, { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify({ conversation_id: conversation, task_id: task, operation, arguments: args }) });
    const value = await response.json(); if (!response.ok) throw new Error(value.error); return value;
  }
  registry.selectConversation("A"); await step("A", "acquire"); await step("A", "navigate", { url: `${descriptor.origin}/fixture?conversation=A` });
  let snapshot = await step("A", "snapshot"); let name = snapshot.snapshot.controls.find((item) => item.name === "Name"); assert.ok(name);
  await step("A", "fill", { ref: name.ref, snapshot_id: snapshot.snapshot_id, value: "A local draft" });
  assert.match((await step("A", "read")).text, /Draft: A local draft/);
  snapshot = await step("A", "snapshot"); let button = snapshot.snapshot.controls.find((item) => item.name === "Increment"); assert.ok(button);
  await step("A", "click", { ref: button.ref, snapshot_id: snapshot.snapshot_id });
  await new Promise((resolve) => setTimeout(resolve, 100)); assert.match((await step("A", "read")).text, /Counter: 1/);
  const aPage = registry.conversationPages.get("A"); const aContentsID = aPage.webContents.id;
  registry.selectConversation("B"); assert.equal(presentation.presented, null); await step("B", "acquire"); await step("B", "navigate", { url: `${descriptor.origin}/fixture?conversation=B` });
  const bPage = registry.conversationPages.get("B"); assert.notEqual(aContentsID, bPage.webContents.id); assert.equal(presentation.presented, bPage);
  // A finishes in the background; its content never selects or replaces B.
  assert.match((await step("A", "read")).text, /Conversation A/); assert.equal(presentation.presented, bPage);
  assert.throws(() => registry.showByRef(aPage.pageRef, "task"), /another conversation/);
  for (let index = 0; index < 20; index++) registry.selectConversation(index % 2 ? "A" : "B");
  registry.selectConversation("A"); assert.equal(presentation.presented.webContents.id, aContentsID);
  await step("A", "release"); await step("A", "acquire", {}, "task_A_second"); assert.equal(registry.conversationPages.get("A").webContents.id, aContentsID);
  const resumed = await step("A", "read"); assert.match(resumed.text, /Counter: 1/); assert.match(resumed.text, /Draft: A local draft/);
  assert.equal(registry.conversationPages.size, 2); assert.equal(registry.connections.size, 2);
  await assert.rejects(step("A", "open_system_browser", { url: `${descriptor.origin}/fixture` }), /fenced/);
  // A granted Host page cannot escape into an independent popup. This is
  // fixture-owned DOM instrumentation, not an operation exposed on the wire.
  await aPage.webContents.executeJavaScript(`(()=>{const button=document.createElement('button');button.textContent='Popup';button.onclick=()=>window.open('/fixture?conversation=popup');document.body.append(button)})()`);
  snapshot = await step("A", "snapshot");
  const popupButton = snapshot.snapshot.controls.find((item) => item.name === "Popup"); assert.ok(popupButton);
  await step("A", "click", { ref: popupButton.ref, snapshot_id: snapshot.snapshot_id });
  await new Promise((resolve) => setTimeout(resolve, 100));
  assert.equal(BrowserWindow.getAllWindows().length, 1);
  assert.equal(registry.conversationPages.size, 2); assert.equal(registry.connections.size, 2);
  snapshot = await step("A", "snapshot"); button = snapshot.snapshot.controls.find((item) => item.name === "Increment"); interruptWrite = true;
  await assert.rejects(step("A", "click", { ref: button.ref, snapshot_id: snapshot.snapshot_id }), /reconciliation|unknown/);
  await new Promise((resolve) => setTimeout(resolve, 100));
  assert.equal(registry.records.size, 0); assert.equal(agent.journal.unknown({ owner_id: "qualification-owner", client_id: "qualification-client", installation_id: "qualification-installation" }).length, 1);
  const fences = await (await auth.authorizedFetch(`${descriptor.origin}/qualify/fences`)).json(); assert.equal(fences.length, 1); assert.equal(fences[0].state, "unknown");
  console.log(JSON.stringify({ event: "sparkclaw_host_native_qualification", passed: true, platform: process.platform, architecture: process.arch, electron: process.versions.electron, chromium: process.versions.chrome, real_embedded_views: true, actual_embedded_click_and_fill: true, conversations: 2, rapid_switches: 20, same_view_released_reacquired: true, late_reply_cannot_select_other_page: true, system_browser_command_rejected: true, host_popup_blocked: true, response_loss_fenced_unknown_write: true, standalone_browser: false, production_data: false }));
  await agent.stop(); window.destroy(); app.exit(0);
} catch (error) { console.error(error.stack); await agent?.stop().catch(() => {}); window?.destroy(); app.exit(1); }
});
