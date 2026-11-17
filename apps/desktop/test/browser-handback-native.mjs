import assert from "node:assert/strict";
import crypto from "node:crypto";
import fs from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { ClientControllerRuntime } from "../src/browser/controller-runtime.mjs";
import { BrowserControllerForwarder } from "../src/browser/controller-forwarder.mjs";
import { ElectronAdapterServer } from "../src/browser/adapter-server.mjs";
import { CONTROLLER_OPERATIONS, controllerDigest, iscpHostID, iscpAuthorizationDigest } from "../src/browser/controller-wire.mjs";

// Uses the original BrowserPanel, preload and IPC from the layout fixture plus
// the actual local Controller/adapter. Authority and website are synthetic.
export async function qualifyHandback({ registry, browserSession, window, shieldView, evaluate, until, temporary }) {
  const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../../..");
  const runtimeRoot = await fs.mkdtemp(path.join(await fs.realpath("/tmp"), "sx-hb-"));
  await fs.chmod(runtimeRoot, 0o700);
  const runtime = new ClientControllerRuntime({ bundleRoot: path.join(root, "apps/desktop/bin/browser-controller"), runtimeRoot,
    adapterFactory: async ({ directory, incarnation }) => new ElectronAdapterServer({
      socketPath: path.join(directory, "electron-adapter.sock"), secretPath: path.join(directory, "adapter-secret"), downloadRoot: path.join(directory, "downloads"),
      runtimeGeneration: registry.runtimeGeneration, registry, browserSession, controllerIncarnation: incarnation }).start() });
  const manifest = { session_id: "handback-session", authorization_revision: 1, expires_at: new Date(Date.now() + 120000).toISOString(),
    operations: [...CONTROLLER_OPERATIONS, "browser.receipt", "browser.reconcile"], limits: { body_bytes: 63488, response_bytes: 63488, concurrent: 4 } };
  const auth = { descriptor: { transport: "iscp" }, connection: { ownerID: "owner", clientID: "client" },
    status: { state: "connected", capabilities: { browser: true } }, transport: { capabilities: manifest }, async invokeISCP() { return { fences: [] }; } };
  const forwarder = new BrowserControllerForwarder({ auth, registry, runtime, userDataDir: temporary });
  let binding = { owner_id: "owner", client_id: "client", installation_id: "installation", local_conversation_id: "handback-conversation",
    local_task_id: "handback-task", connection_epoch: manifest.session_id, runtime_generation: `iscp_${manifest.session_id}`,
    lease_id: "handback-lease", page_id: "handback-page", page_generation: 1, lease_expires_at: "0001-01-01T00:00:00Z" };
  binding.host_id = iscpHostID(binding, manifest.session_id);
  binding.authorization_digest = iscpAuthorizationDigest(manifest.session_id, 1);
  let lease;
  const send = async (action, fields = {}, expectSuccess = true) => {
    const operation = `browser.controller.${action}`;
    const body = { schema_version: 1, command_id: `command_${crypto.randomUUID()}`, binding, digest: "", timeout_ms: 25000, ...fields };
    body.digest = controllerDigest(operation, body);
    const response = await forwarder.handleReverse({ type: "task.invoke", id: crypto.randomUUID(), profile: "sparkclaw.workbench.transport.v2",
      installation_id: binding.installation_id, operation, body }, manifest);
    assert.equal(response.status, 200, JSON.stringify(response));
    if (expectSuccess) assert.equal(response.body.outcome, "completed", JSON.stringify(response));
    return response.body;
  };
  const execute = async (operation, args = {}) => {
    const reply = await send("execute", { lease, operation, arguments: args });
    assert.ok(reply.payload, "fixture result must fit inline");
    lease = { ...lease, page_generation: reply.payload.page_generation };
    return reply.payload.result;
  };
  try {
    await forwarder.start(binding);
    registry.selectConversation(binding.local_conversation_id);
    lease = (await send("acquire", { wait_timeout_ms: 0, session_ttl_ms: 60000 })).payload;
    await execute("page.navigate", { url: "https://fixture.test/handback" });
    const page = registry.conversationPages.get(binding.local_conversation_id);
    assert.ok(page); const nativeID = page.webContents.id;
    await until(() => evaluate("[...document.querySelectorAll('.desktopTabSelect')].some(b=>b.querySelector('.lucide-eye'))"), "native task tab missing");
    await evaluate("[...document.querySelectorAll('.desktopTabSelect')].find(b=>b.querySelector('.lucide-eye')).click()");
    await until(() => page.view.getVisible() && shieldView.getVisible(), "execution shield missing");
    await page.webContents.executeJavaScript("document.querySelector('input').focus()");
    page.webContents.sendInputEvent({ type: "keyDown", keyCode: "X" });
    page.webContents.sendInputEvent({ type: "char", keyCode: "X" });
    page.webContents.sendInputEvent({ type: "keyUp", keyCode: "X" });
    await new Promise(resolve => setTimeout(resolve, 50));
    assert.equal(await page.webContents.executeJavaScript("document.querySelector('input').value"), "retained native input");
    const ipcNavigate = `window.sparkclawDesktop.navigatePersonal(${JSON.stringify(page.pageRef)}, 'https://fixture.test/changed')`;
    assert.match(await evaluate(`${ipcNavigate}.then(()=>'',e=>e.message)`), /read-only/);
    assert.match(await evaluate(`window.sparkclawDesktop.closePersonal(${JSON.stringify(page.pageRef)}).then(()=>'',e=>e.message)`), /read-only/);
    const snapshot = await execute("page.snapshot");
    const queue = Array.isArray(snapshot.snapshot) ? [...snapshot.snapshot] : [snapshot.snapshot];
    let ref;
    while (queue.length) { const node = queue.shift(); if (!node || typeof node !== "object") continue;
      if (node.role === "textbox" && node.ref) { ref = node.ref; break; } if (Array.isArray(node.children)) queue.push(...node.children); }
    assert.ok(ref, JSON.stringify(snapshot));
    await execute("page.fill", { ref, text: "agent retained value" });
    await send("release", { lease });
    assert.equal(page.interaction.state, "agent"); assert.equal(page.webContents.debugger.isAttached(), false);
    assert.equal(shieldView.getVisible(), true, "tool release must not unlock");
    const evidenceRoot = process.env.SPARKCLAW_HANDBACK_EVIDENCE;
    if (evidenceRoot) {
      await fs.mkdir(evidenceRoot, { recursive: true });
      await fs.writeFile(path.join(evidenceRoot, "executing.png"), (await window.webContents.capturePage()).toPNG());
    }
    await forwarder.endTask(binding, "completed");
    await until(() => page.interaction.state === "human" && !shieldView.getVisible(), "completed task did not hand back");
    await until(() => evaluate("!document.querySelector('.desktopTaskNotice')"), "notice did not update");
    assert.equal(page.webContents.id, nativeID); assert.equal(page.role, "task");
    assert.equal(await page.webContents.executeJavaScript("document.querySelector('input').value"), "agent retained value");
    await page.webContents.executeJavaScript("document.querySelector('input').focus();document.querySelector('input').select()");
    page.webContents.focus();
    page.webContents.sendInputEvent({ type: "keyDown", keyCode: "H" });
    page.webContents.sendInputEvent({ type: "char", keyCode: "H" });
    page.webContents.sendInputEvent({ type: "keyUp", keyCode: "H" });
    await until(async () => await page.webContents.executeJavaScript("document.querySelector('input').value") === "H", "human keyboard input did not arrive");
    assert.equal((await send("execute", { lease, operation: "page.read", arguments: {} }, false)).outcome, "not_dispatched");
    // User navigation goes through the existing address bar and same native tab.
    await evaluate(`(() => { const input=document.querySelector('.desktopAddressBar input'); Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(input,'https://fixture.test/handback-next'); input.dispatchEvent(new Event('input',{bubbles:true})); })()`);
    await evaluate("document.querySelector('.desktopAddressBar').requestSubmit()");
    await until(() => page.webContents.getURL() === "https://fixture.test/handback-next" && !page.webContents.isLoading(), "human address navigation failed");
    assert.equal(page.webContents.id, nativeID); assert.equal(page.webContents.navigationHistory.canGoBack(), true);
    await evaluate(`window.sparkclawDesktop.personalNavigation(${JSON.stringify(page.pageRef)}, 'back')`);
    await until(() => page.webContents.getURL() === "https://fixture.test/handback" && !page.webContents.isLoading(), "human back failed");
    // The owner choosing to stay at beforeunload retains the native page; the second close succeeds.
    const confirmUnload = registry.confirmUnload; let asked = 0;
    registry.confirmUnload = () => { asked++; return false; };
    await page.webContents.executeJavaScript("window.onbeforeunload=()=>false;void 0", true);
    let prevented = false; page.webContents.once("will-prevent-unload", () => { prevented = true; });
    await evaluate(`window.sparkclawDesktop.closePersonal(${JSON.stringify(page.pageRef)})`);
    await until(() => prevented, "beforeunload was not observed");
    assert.equal(page.webContents.isDestroyed(), false); assert.equal(asked, 1);
    registry.confirmUnload = confirmUnload;
    if (evidenceRoot) await fs.writeFile(path.join(evidenceRoot, "handed-back.png"), (await window.webContents.capturePage()).toPNG());
    await page.webContents.executeJavaScript("window.onbeforeunload=null");
    // Keep the page alive for a new-task isolation check before closing it.
    binding = { ...binding, local_task_id: "next-task", lease_id: "next-lease", page_id: "next-page" };
    lease = (await send("acquire", { wait_timeout_ms: 0, session_ttl_ms: 60000 })).payload;
    await execute("page.navigate", { url: "https://fixture.test/new-task" });
    const next = registry.conversationPages.get(binding.local_conversation_id);
    assert.notEqual(next.webContents.id, nativeID); assert.equal(page.connectionID, null);
    await forwarder.cancelTask(binding);
    assert.equal(next.interaction.state, "human"); assert.equal(next.webContents.isDestroyed(), false);
    assert.equal(runtime.child, null); assert.equal(registry.connections.size, 0);
    await evaluate(`window.sparkclawDesktop.closePersonal(${JSON.stringify(page.pageRef)})`);
    await until(() => page.destroyed, "human close did not complete");
    return { controller_process: true, real_preload_ipc: true, execution_keyboard_blocked: true, direct_ipc_blocked: true,
      temporary_release_locked: true, same_native_page: true, human_keyboard: true, address_and_history: true,
      beforeunload_canceled: true, new_task_isolated: true, stop_retains_page_after_process_cleanup: true };
  } finally { await forwarder.stop(); await fs.rm(runtimeRoot, { recursive: true, force: true }); }
}
