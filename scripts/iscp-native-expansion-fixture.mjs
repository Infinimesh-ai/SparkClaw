// Run with Electron against a fresh, explicitly prepared expansion lab. This
// imports the real application entry point and uses its sandboxed renderer IPC.
import { app, BrowserWindow, net, netLog, safeStorage } from "electron";
import assert from "node:assert/strict";
import fs from "node:fs/promises";
import path from "node:path";
import { execFileSync } from "node:child_process";
import { containerState } from "./lib/iscp-docker-lab.mjs";

const directory = process.env.SPARKCLAW_ISCP_NATIVE_LAB;
if (!path.isAbsolute(directory || "") || !directory.startsWith("/private/tmp/")) throw new Error("A private disposable native expansion lab is required");
const metadata = JSON.parse(await fs.readFile(path.join(directory, "run.json"), "utf8"));
assert.ok(metadata.source.capacity_patch && metadata.qualified_operations);
process.env.SPARKCLAW_DESKTOP_ISCP_LOCAL_TEST = "1";
process.env.SPARKCLAW_DESKTOP_ISCP_CONFIG = path.join(directory, "desktop-profile.json");
process.env.SPARKCLAW_DESKTOP_USER_DATA_DIR = path.join(directory, "userdata");
const evidence = { schema_version: 1, electron: process.versions.electron, chromium: process.versions.chrome,
  source: metadata.source, topology: "isolated Docker Gateway without published business ports", model: "mock", checks: [], direct_gateway_business_attempts: 0 };
const originalFetch = net.fetch.bind(net);
net.fetch = (...args) => {
  if (/^https?:/u.test(String(args[0]?.url || args[0]))) {
    evidence.direct_gateway_business_attempts++;
    throw new Error("Native main direct business HTTP is blocked");
  }
  return originalFetch(...args);
};
globalThis.fetch = () => { evidence.direct_gateway_business_attempts++; throw new Error("Native Node direct HTTP is blocked"); };
const logStarted = app.whenReady().then(() => netLog.startLogging(path.join(directory, "evidence/native-network.json")));
await import("../apps/desktop/src/main/main.mjs");
const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
let window;
let relayPaused = false;
const timer = setTimeout(() => {
  if (relayPaused) { execFileSync("docker", ["unpause", metadata.relay_container]); relayPaused = false; }
  console.error("Native qualification deadline exceeded"); app.exit(1);
}, 120000);
async function evaluate(source) { return window.webContents.executeJavaScript(source, true); }
async function until(predicate, label) {
  const deadline = Date.now() + 30000;
  while (Date.now() < deadline) { if (await predicate()) return; await pause(100); }
  throw new Error(`Native qualification timed out: ${label}`);
}
void app.whenReady().then(async () => {
try {
  await logStarted;
  assert.ok(safeStorage.isEncryptionAvailable(), "Native Keychain encryption is required");
  await until(async () => { window = BrowserWindow.getAllWindows()[0]; return window && !window.webContents.isLoading() && await evaluate("Boolean(window.sparkclawDesktop && document.querySelector('.localWorkbench'))"); }, "real workbench");
  window.webContents.session.webRequest.onBeforeRequest({ urls: ["http://*/*", "https://*/*", "ws://*/*", "wss://*/*"] }, (_details, callback) => {
    evidence.direct_gateway_business_attempts++; callback({ cancel: true });
  });
  await until(async () => (await evaluate("window.sparkclawDesktop.localConnection()")).state === "connected", "reconnected current identity");
  const status = await evaluate("window.sparkclawDesktop.localConnection()");
  assert.equal(status.state, "connected");
  assert.equal(status.backend.transport, "iscp");
  assert.equal(status.capabilities.settings, true);
  assert.equal(status.capabilities.files, true);
  assert.equal(status.capabilities.approvals, true);
  assert.equal(status.capabilities.events, true);
  assert.equal(status.capabilities.speech, false, "An absent provider cannot qualify voice");
  await evaluate("window.__iscpAppliedEvents=0;window.sparkclawDesktop.onBackendEvents(()=>window.__iscpAppliedEvents++);true");
  evidence.checks.push("native_keychain_and_current_capability_projection");
  await evaluate("document.querySelector('.sidebarAccountTrigger').click()");
  await evaluate("[...document.querySelectorAll('.sidebarAccountMenuItem')].find(button=>/settings|设置/i.test(button.textContent)).click()");
  await until(() => evaluate("Boolean(document.querySelector('.settingsBlock button.edit'))"), "owner settings");
  await evaluate("document.querySelector('.settingsBlock button.edit').click()");
  await until(() => evaluate("Boolean(document.querySelector('.ownerEditor input'))"), "owner editor");
  await evaluate(`(()=>{const input=document.querySelector('.ownerEditor input');Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(input,'Native ISCP verification');input.dispatchEvent(new Event('input',{bubbles:true}));})()`);
  await evaluate("document.querySelector('.settingsBlock button.approve').click()");
  await until(() => evaluate("!document.querySelector('.ownerEditor') && document.querySelector('.settingsBlock').textContent.includes('Native ISCP verification')"), "durable settings save");
  const owner = await evaluate("fetch(window.sparkclawDesktop.gatewayBase+'/api/owner').then(r=>r.json())");
  assert.equal(owner.display_name, "Native ISCP verification");
  evidence.checks.push("real_settings_UI_saved_and_read_through_ISCP_with_HTTP_blocked");
  await evaluate("new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)))");
  await fs.writeFile(path.join(directory, "evidence/native-settings.png"), (await window.webContents.capturePage()).toPNG(), { mode: 0o600 });
  await evaluate("document.querySelector('.settingsPageBack').click()");
  const result = await evaluate(`(async()=>{const store=window.sparkclawClientStore;const conversation=await store.create('Native ISCP attachment');const file=await store.saveFile(conversation.id,'native-input.txt',new TextEncoder().encode('Native fixture attachment through encrypted chunks.'));const task=await store.enqueue(conversation.id,'Hello.',[file.id]);await store.submit(task.request_id);return {conversation_id:conversation.id,request_id:task.request_id};})()`);
  await until(async () => {
    await evaluate(`window.sparkclawClientStore.reconcile(${JSON.stringify(result.request_id)})`);
    return evaluate(`window.sparkclawClientStore.read(${JSON.stringify(result.conversation_id)}).then(value=>value.tasks.some(task=>task.request_id===${JSON.stringify(result.request_id)} && task.status==='delivered'))`);
  }, "attachment execution and durable ACK");
  evidence.execution = result;
  evidence.checks.push("native_local_file_upload_execution_delivery_and_ACK");
  const notifications = await evaluate("fetch(window.sparkclawDesktop.gatewayBase+'/api/notifications').then(r=>r.json())");
  assert.ok(Array.isArray(notifications.notifications));
  evidence.checks.push("native_bounded_notification_reader");
  await until(() => evaluate("window.__iscpAppliedEvents>0"), "durable event projection and ACK");
  evidence.checks.push("native_event_projection_committed_before_ACK");
  assert.ok(containerState(metadata.relay_container, metadata.lab_id)?.State.Running);
  execFileSync("docker", ["pause", metadata.relay_container]); relayPaused = true;
  const interrupted = await evaluate("fetch(window.sparkclawDesktop.gatewayBase+'/api/owner',{signal:AbortSignal.timeout(2000)}).then(response=>({ok:response.ok}),()=>({ok:false}))");
  assert.equal(interrupted.ok, false);
  execFileSync("docker", ["unpause", metadata.relay_container]); relayPaused = false;
  const reconnected = await evaluate("window.sparkclawDesktop.retryLocalConnection()");
  assert.equal(reconnected.state, "connected");
  const original = await evaluate(`window.sparkclawClientStore.read(${JSON.stringify(result.conversation_id)})`);
  assert.equal(original.tasks.filter(task => task.request_id === result.request_id).length, 1);
  assert.equal(original.tasks.find(task => task.request_id === result.request_id).status, "delivered");
  evidence.checks.push("Relay_interruption_has_no_HTTP_rescue_and_original_delivery_survives_reconnect");
  assert.equal(evidence.direct_gateway_business_attempts, 0);
  evidence.passed = true;
} catch (error) {
  evidence.error = error.message;
  console.error(error.stack);
} finally {
  clearTimeout(timer);
  if (relayPaused) execFileSync("docker", ["unpause", metadata.relay_container]);
  await netLog.stopLogging();
  await fs.chmod(path.join(directory, "evidence/native-network.json"), 0o600);
  await fs.writeFile(path.join(directory, "evidence/native-expansion.json"), JSON.stringify(evidence, null, 2), { mode: 0o600 });
  console.log(JSON.stringify(evidence));
  app.exit(evidence.passed ? 0 : 1);
}
});
