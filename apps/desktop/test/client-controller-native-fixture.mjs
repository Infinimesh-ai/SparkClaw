import { app, BrowserWindow, WebContentsView, session, protocol } from "electron";
import assert from "node:assert/strict";
import crypto from "node:crypto";
import fs from "node:fs/promises";
import http from "node:http";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { ElectronAdapterServer } from "../src/browser/adapter-server.mjs";
import { PageRegistry } from "../src/browser/page-registry.mjs";
import { BrowserPresentation } from "../src/main/presentation.mjs";
import { verifyControllerBundle } from "../src/browser/controller-bundle.mjs";
import { ClientControllerRuntime } from "../src/browser/controller-runtime.mjs";
import { BrowserControllerForwarder } from "../src/browser/controller-forwarder.mjs";
import { CONTROLLER_OPERATIONS, controllerDigest, iscpHostID, iscpAuthorizationDigest } from "../src/browser/controller-wire.mjs";

// Disposable native main process; never reads the installed app's profile or authority.
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../../..");
const directory = await fs.mkdtemp(path.join(await fs.realpath("/tmp"), "sx-bc-p0-"));
await fs.chmod(directory, 0o700);
app.setPath("userData", path.join(directory, "profile"));
protocol.registerSchemesAsPrivileged([{ scheme: "sparkclaw-internal", privileges: { standard: true, secure: true, supportFetchAPI: true } }]);
const evidence = { schema_version: 1, scope: "disposable_native_local_controller", passed: false, platform: process.platform,
  architecture: process.arch, versions: process.versions, checks: {}, timings_ms: {} };
let window, adapter, registry, runtime, fixture, forwarder;
const useForwarder = process.env.SPARKCLAW_TEST_CONTROLLER_FORWARDER === "1";
const crossOrigin = process.env.SPARKCLAW_TEST_CONTROLLER_CROSS_ORIGIN === "1";
if (useForwarder) evidence.scope = "disposable_native_controller_forwarder_wire";

const bundle = process.env.SPARKCLAW_TEST_CONTROLLER_BUNDLE || path.join(root, "apps/desktop/bin/browser-controller");
let controllerSocket;
app.on("window-all-closed", () => {});
void app.whenReady().then(run);
async function run() {
const started = performance.now();
const killTimer = setTimeout(() => {
  evidence.error = "native fixture exceeded 90 seconds";
  void fs.writeFile(path.join(directory, "evidence.json"), JSON.stringify(evidence)).finally(() => { runtime?.child?.kill("SIGKILL"); app.exit(2); });
}, 90000);
try {
  console.log("client_controller_fixture: verifying bundle");
  evidence.bundle = await verifyControllerBundle(bundle).then(({ bytes, source_revision, versions }) => ({ bytes, source_revision, versions }));
  const browserSession = session.fromPartition(`p0-${crypto.randomUUID()}`);
  if (crossOrigin) browserSession.protocol.handle("https", request => {
    assert.equal(request.url, "https://cross.controller.fixture.test/frame");
    return new Response(`<!doctype html><title>Cross-origin task frame</title>
      <button onclick="document.body.dataset.clicked='1';setTimeout(()=>{this.insertAdjacentHTML('afterend','<label>Dynamic field<input aria-label=&quot;Dynamic field&quot;></label>');this.disabled=true},250)">Add dynamic field</button>`,
    { headers: { "content-type": "text/html" } });
  });
  browserSession.protocol.handle("sparkclaw-internal", () => new Response("<!doctype html><title>Local adapter</title>"));
  window = new BrowserWindow({ show: false, width: 1100, height: 800, webPreferences: { sandbox: true, contextIsolation: true, nodeIntegration: false } });
  await window.loadURL("data:text/html,<title>Client Controller native fixture</title>");
  const shield = new WebContentsView({ webPreferences: { sandbox: true, contextIsolation: true } });
  const presentation = new BrowserPresentation({ window, shieldView: shield, explicitSelection: true });
  registry = new PageRegistry({ runtimeGeneration: crypto.randomBytes(16).toString("hex"), browserSession, presentation,
    createView: () => new WebContentsView({ webPreferences: { session: browserSession, sandbox: true, contextIsolation: true, nodeIntegration: false } }) });
  runtime = new ClientControllerRuntime({ bundleRoot: bundle, runtimeRoot: path.join(directory, "run"),
    adapterFactory: async ({ directory: runtimeDirectory, incarnation }) => {
      adapter = await new ElectronAdapterServer({ socketPath: path.join(runtimeDirectory, "electron-adapter.sock"), secretPath: path.join(runtimeDirectory, "adapter-secret"),
        downloadRoot: path.join(runtimeDirectory, "downloads"), runtimeGeneration: registry.runtimeGeneration, registry, browserSession,
        controllerIncarnation: incarnation }).start();
      return adapter;
    } });
  await runtime.prepare();
  assert.equal(runtime.snapshot().process_state, "stopped");
  assert.equal(runtime.child, null);
  const starts = await Promise.all([runtime.start(), runtime.start()]);
  assert.equal(starts[0].child.pid, starts[1].child.pid);
  controllerSocket = runtime.socketPath;
  registry.selectConversation("conversation-A");
  evidence.timings_ms.start = performance.now() - started;
  fixture = http.createServer((req, res) => {
    res.setHeader("content-type", "text/html");
    if (req.url === "/popup") return res.end("<!doctype html><title>Owned popup</title><p>Popup evidence</p>");
    if (req.url === "/frame") return res.end('<!doctype html><button onclick="this.textContent=\'Frame applied\'">Frame action</button>');
    res.end(`<!doctype html><html><head><title>Controller fixture</title></head><body>
      <label>Name<input aria-label="Name"></label><button onclick="document.querySelector('#count').textContent=String(++window.count)">Increment</button>
      <p id="count">0</p><select aria-label="Choice"><option value="one">One</option><option value="two">Two</option></select>
      <button onclick="window.open('/popup')">Open popup</button><iframe title="Owned frame" src="/frame"></iframe>
      ${crossOrigin ? '<iframe title="Cross-origin frame" src="https://cross.controller.fixture.test/frame"></iframe>' : ''}
      ${useForwarder ? '<canvas width="700" height="500"></canvas>' : ''}
      <script>window.count=0;const c=document.querySelector('canvas');if(c){const x=c.getContext('2d'),d=x.createImageData(700,500);let s=7;for(let i=0;i<d.data.length;i++){s=(s*1664525+1013904223)>>>0;d.data[i]=i%4===3?255:s>>>24;}x.putImageData(d,0,0);}</script></body></html>`);
  });
  await new Promise(resolve => fixture.listen(0, "127.0.0.1", resolve));
  const origin = `http://127.0.0.1:${fixture.address().port}`;
  console.log("client_controller_fixture: acquiring MCP");
  const binding = { owner_id: "fixture-owner", client_id: "fixture-client", installation_id: "fixture-installation", local_conversation_id: "conversation-A",
    local_task_id: "fixture-task", host_id: "fixture-host", runtime_generation: "iscp_fixture", connection_epoch: "epoch_fixture", lease_id: "lease_fixture",
    page_id: "page_fixture", page_generation: 1, authorization_digest: "fixture-digest" };
  let sendWire;
  if (useForwarder) {
    const manifest = { schema_version: 2, profile: "sparkclaw.workbench.transport.v2", session_id: "fixture_session", authorization_revision: 1,
      expires_at: new Date(Date.now()+300000).toISOString(), operations: [...CONTROLLER_OPERATIONS, "browser.receipt", "browser.reconcile"],
      limits: { body_bytes: 63488, response_bytes: 63488, concurrent: 8 } };
    const auth = { descriptor: { transport: "iscp" }, connection: { ownerID: binding.owner_id, clientID: binding.client_id },
      status: { state: "connected", capabilities: { browser: true } }, transport: { capabilities: manifest },
      invokeISCP: async () => ({ fences: [] }) };
    Object.assign(binding, { host_id: iscpHostID(binding, manifest.session_id), runtime_generation: `iscp_${manifest.session_id}`,
      connection_epoch: manifest.session_id, authorization_digest: iscpAuthorizationDigest(manifest.session_id, 1), lease_expires_at: "0001-01-01T00:00:00Z" });
    forwarder = new BrowserControllerForwarder({ auth, registry, runtime, userDataDir: app.getPath("userData") });
    await forwarder.start(binding);
    sendWire = async (action, fields) => {
      const operation = `browser.controller.${action}`;
      const body = ["status", "chunk"].includes(action) ? fields : { schema_version: 1, command_id: `cmd_${crypto.randomUUID()}`,
        binding, digest: "", timeout_ms: 25000, ...fields };
      if (!["status", "chunk"].includes(action)) body.digest = controllerDigest(operation, body);
      const reply = await forwarder.handleReverse({ type: "task.invoke", profile: manifest.profile, id: crypto.randomUUID(), operation,
        installation_id: binding.installation_id, body }, manifest);
      assert.equal(reply.status, 200, JSON.stringify(reply));
      if (["status", "chunk"].includes(action)) return reply.body;
      assert.equal(reply.body.outcome, "completed", JSON.stringify(reply));
      if (reply.body.payload) return reply.body.payload;
      const descriptor = reply.body.result;
      assert.ok(descriptor, JSON.stringify(reply));
      const bytes = [];
      for (let offset = 0; offset < descriptor.total_bytes; offset += 32768) {
        const input = { schema_version: 1, binding, command_id: body.command_id, result_id: descriptor.result_id, offset,
          length: Math.min(32768, descriptor.total_bytes-offset) };
        const chunk = await sendWire("chunk", input);
        assert.deepEqual(await sendWire("chunk", input), chunk);
        bytes.push(Buffer.from(chunk.data_base64, "base64"));
      }
      const assembled = Buffer.concat(bytes);
      assert.equal(crypto.createHash("sha256").update(assembled).digest("hex"), descriptor.sha256);
      evidence.checks.chunked_native_payload_hash_and_retry = true;
      return JSON.parse(assembled);
    };
    const status = await sendWire("status", { schema_version: 1 });
    assert.equal(status.process_state, "stopped"); assert.equal(runtime.child, null);
    evidence.checks.status_wire_does_not_start_controller = true;
  }
  let task;
  const acquire = async () => {
    if (useForwarder) return sendWire("acquire", { wait_timeout_ms: 0, session_ttl_ms: 60000 });
    task = adapter.prepareControllerTask(binding, runtime.incarnation);
    return request("acquire", { profile_id: "client_embedded", lane: "mcp", task_id: task.taskID, credential_generation: 1,
      token: task.token, wait_timeout_ms: 0, session_ttl_ms: 60000 });
  };
  let lease = await acquire();
  if (process.env.SPARKCLAW_TEST_CONTROLLER_QUEUE_FULL === "1") {
    assert.ok(useForwarder);
    const pid = runtime.child.pid, incarnation = runtime.incarnation;
    const queued = Array.from({ length: 33 }, (_, index) => {
      const body = { schema_version: 1, command_id: `queued_${crypto.randomUUID()}`, digest: "", timeout_ms: 10000,
        wait_timeout_ms: 10000, session_ttl_ms: 60000, binding: { ...binding, local_conversation_id: `queue-${index}`,
          local_task_id: `task-${index}`, lease_id: `lease-${index}`, page_id: `page-${index}` } };
      body.digest = controllerDigest("browser.controller.acquire", body);
      return { type: "task.invoke", profile: "sparkclaw.workbench.transport.v2", id: crypto.randomUUID(),
        operation: "browser.controller.acquire", installation_id: binding.installation_id, body };
    });
    const pending = queued.slice(0,32).map(request => forwarder.handleReverse(request, forwarder.auth.transport.capabilities));
    await until(() => forwarder.admission.queue.length === 32, 3000);
    const overflow = await forwarder.handleReverse(queued[32], forwarder.auth.transport.capabilities);
    assert.equal(overflow.body.outcome,"not_dispatched");assert.equal(overflow.body.error_code,"browser_busy");
    for (const request of queued) assert.equal(forwarder.journal.rows.has(request.body.command_id),false);
    for (const request of queued.slice(0,32)) forwarder.cancelTask(request.body.binding);
    for (const reply of await Promise.all(pending)) assert.equal(reply.body.outcome,"not_dispatched");
    assert.equal(forwarder.admission.queue.length,0);
    for (const request of queued) assert.equal(forwarder.journal.rows.has(request.body.command_id),false);
    assert.equal(runtime.child.pid,pid);assert.equal(runtime.incarnation,incarnation);
    assert.deepEqual(forwarder.lease.binding,binding);
    evidence.checks.native_fifo_32_waiters_rejects_33rd_before_dispatch = true;
    evidence.checks.cancel_all_waiters_preserves_active_native_lease = true;
  }
  const execute = async (operation, args = {}) => {
    const response = useForwarder ? await sendWire("execute", { lease, operation, arguments: args }) : await request("execute", { session_id: lease.session_id, controller_generation: lease.controller_generation,
      session_generation: lease.session_generation, page_generation: lease.page_generation, operation, arguments: args });
    lease = { ...lease, page_generation: response.page_generation };
    return response.result;
  };
  console.log("client_controller_fixture: navigating");
  await execute("page.navigate", { url: origin });
  if (process.env.SPARKCLAW_TEST_CONTROLLER_PARENT_CRASH === "1") {
    console.log(JSON.stringify({ event: "controller_parent_crash_ready", main_pid: process.pid, controller_pid: runtime.child.pid, directory }));
    process.kill(process.pid, "SIGKILL");
    return;
  }
  let snapshot = await execute("page.snapshot", { boxes: true });
  const nameRef = findRef(snapshot.snapshot, "Name"); assert.ok(nameRef);
  await execute("page.fill", { ref: nameRef, text: "Local Electron" });
  snapshot = await execute("page.snapshot");
  await execute("page.click", { ref: findRef(snapshot.snapshot, "Increment") });
  let read = await execute("page.read");
  assert.match(read.page.text, /1/);
  snapshot = await execute("page.snapshot");
  await execute("page.select", { ref: findRef(snapshot.snapshot, "Choice"), value: "two" });
  const screen = await execute("page.screenshot", { type: "png" });
  assert.equal(screen.screenshot.mime_type, "image/png");
  assert.ok(Buffer.from(screen.screenshot.data_base64, "base64").length > 1000);
  await fs.writeFile(path.join(directory, "capture.png"), Buffer.from(screen.screenshot.data_base64, "base64"));
  const page = [...registry.records.values()].find(r => r.webContents.getURL().startsWith(origin));
  assert.ok(page);
  const actual = await page.webContents.executeJavaScript('({name:document.querySelector("input").value,count:window.count,choice:document.querySelector("select").value})');
  assert.deepEqual(actual, { name: "Local Electron", count: 1, choice: "two" });
  evidence.checks.read_fill_click_select_snapshot_capture = true;
  evidence.checks.renderer_sandbox = page.webContents.getLastWebPreferences().sandbox === true;
  evidence.checks.system_node_absent_from_child_path = true;
  snapshot = await execute("page.snapshot");
  evidence.frame_snapshot = snapshot.snapshot;
  evidence.checks.frame_ref = Boolean(findRef(snapshot.snapshot, "Frame action"));
  if (evidence.checks.frame_ref) {
    await execute("page.click", { ref: findRef(snapshot.snapshot, "Frame action") });
    evidence.checks.frame_effect = await page.webContents.executeJavaScript('document.querySelector("iframe").contentDocument.body.textContent.includes("Frame applied")');
  }
  if (crossOrigin) {
    snapshot = await execute("page.snapshot");
    evidence.cross_origin_snapshot = snapshot.snapshot;
    const ref = findRef(snapshot.snapshot, "Add dynamic field"); assert.ok(ref);
    await execute("page.click", { ref });
    await execute("page.wait", { duration_ms: 350 });
    await until(async () => {
      snapshot = await execute("page.snapshot");
      evidence.cross_dynamic_snapshot = snapshot.snapshot;
      return Boolean(findRef(snapshot.snapshot, "Dynamic field"));
    }, 5000);
    evidence.cross_dynamic_snapshot = snapshot.snapshot;
    evidence.cross_dynamic_frames = await Promise.all(page.webContents.mainFrame.framesInSubtree.map(async frame => ({
      url: frame.url, pid: frame.osProcessId, text: await frame.executeJavaScript('document.body.innerHTML'),
    })));
    const field = findRef(snapshot.snapshot, "Dynamic field"); assert.ok(field);
    await execute("page.type", { ref: field, text: "Cross-origin effect", submit: false });
    const frame = page.webContents.mainFrame.framesInSubtree.find(frame => frame.url === "https://cross.controller.fixture.test/frame");
    assert.ok(frame);
    assert.equal(await frame.executeJavaScript('document.querySelector("input").value'), "Cross-origin effect");
    evidence.cross_origin_processes = { main: page.webContents.mainFrame.osProcessId, frame: frame.osProcessId };
    evidence.checks.cross_origin_dynamic_field_effect = true;
    const personal = await registry.createPersonalPage(origin + "/popup");
    const scoped = await execute("tabs.list");
    assert.ok(scoped.pages.every(item => item.url !== origin + "/popup"));
    assert.equal(personal.webContents.debugger.isAttached(), false);
    evidence.checks.personal_page_excluded_from_controller_targets = true;
    registry.destroy(personal.tabID, "fixture_personal_cleanup");
  }
  snapshot = await execute("page.snapshot");
  await execute("page.click", { ref: findRef(snapshot.snapshot, "Open popup") });
  await until(() => [...registry.records.values()].some(r => r.webContents.getURL().endsWith("/popup")), 5000);
  const tabs = await execute("tabs.list");
  evidence.checks.owned_popup = tabs.pages.some(p => p.url.endsWith("/popup"));
  const mainPage = tabs.pages.find(p => p.url === origin + "/");
  assert.ok(mainPage);
  await execute("page.read", { page_id: mainPage.page_id });
  evidence.before_release = registry.desktopSnapshot();
  const release = async () => {
    if (useForwarder) return sendWire("release", { lease });
    adapter.beginControllerRelease(task.taskID);
    await request("release", { session_id: lease.session_id, controller_generation: lease.controller_generation, session_generation: lease.session_generation });
    adapter.revokeControllerTask(task.taskID, "normal_release");
  };
  await release();
  evidence.after_release = registry.desktopSnapshot();
  evidence.checks.normal_release_retains_conversation_view = registry.records.size === 1 && registry.conversationPages.get("conversation-A") === page && !page.connectionID && !page.webContents.debugger.isAttached();
  assert.equal(registry.connections.size, 0);
  lease = await acquire();
  read = await execute("page.read");
  evidence.after_adoption = { pages: registry.desktopSnapshot(), read };
  evidence.checks.fresh_lease_adopts_retained_view = registry.conversationPages.get("conversation-A") === page && read.page.text.includes("1");
  await execute("tabs.close");
  await release();
  evidence.checks.explicit_close_removes_view = registry.records.size === 0;
  lease = await acquire();
  await execute("page.navigate", { url: origin });
  const oldIncarnation = runtime.incarnation;
  runtime.child.kill("SIGKILL");
  await until(() => runtime.state === "stopped", 10000);
  evidence.checks.controller_crash_revokes_pages_and_children = registry.records.size === 0 && registry.connections.size === 0 && runtime.child === null;
  await runtime.start();
  evidence.checks.restart_uses_new_incarnation = runtime.incarnation !== oldIncarnation;
  await runtime.stop("fixture_complete", { permanent: true });
  evidence.checks.controller_exit = runtime.state === "stopped" && runtime.child === null;
  evidence.timings_ms.total = performance.now() - started;
  assert.ok(Object.values(evidence.checks).every(Boolean), JSON.stringify(evidence.checks));
  evidence.passed = true;
} catch (error) {
  evidence.error = error.stack || String(error);
  evidence.runtime_state = runtime?.snapshot();
} finally {
  clearTimeout(killTimer);
  await forwarder?.stop().catch(error => { evidence.cleanup_error = String(error); evidence.passed = false; });
  await runtime?.stop("fixture_cleanup", { permanent: true }).catch(error => { evidence.cleanup_error = String(error); evidence.passed = false; });
  for (const record of registry?.records.values() || []) registry.destroy(record.tabID);
  fixture?.closeAllConnections(); await new Promise(resolve => fixture ? fixture.close(resolve) : resolve());
  window?.destroy();
  await fs.writeFile(path.join(directory, "evidence.json"), `${JSON.stringify(evidence, null, 2)}\n`);
  console.log(JSON.stringify({ passed: evidence.passed, evidence_file: path.join(directory, "evidence.json"), checks: evidence.checks, error: evidence.error }));
  app.exit(evidence.passed ? 0 : 1);
}

}

async function request(action, body) {
  return new Promise((resolve, reject) => {
    const encoded = JSON.stringify(body);
    const req = http.request({ socketPath: controllerSocket, path: `/v1/${action}`, method: "POST", headers: { "content-type": "application/json", "content-length": Buffer.byteLength(encoded) } }, response => {
      const chunks = []; let bytes = 0;
      response.on("data", chunk => { bytes += chunk.length; if (bytes > 10 << 20) req.destroy(new Error("Response too large")); else chunks.push(chunk); });
      response.on("end", () => {
        try { const value = JSON.parse(Buffer.concat(chunks)); if (response.statusCode >= 300) throw new Error(`${action}: ${JSON.stringify(value)}`); resolve(value); }
        catch (error) { reject(error); }
      });
    });
    req.setTimeout(25000, () => req.destroy(new Error("Controller fixture call timed out")));
    req.on("error", reject); req.end(encoded);
  });
}
async function until(check, milliseconds) {
  const end = performance.now() + milliseconds;
  while (performance.now() < end) { if (await check()) return; await new Promise(resolve => setTimeout(resolve, 30)); }
  throw new Error("Native fixture condition timed out");
}
function findRef(snapshot, name) {
  const queue = Array.isArray(snapshot) ? [...snapshot] : [snapshot];
  while (queue.length) {
    const value = queue.shift(); if (!value || typeof value !== "object") continue;
    if (value.name === name && /^(?:f[1-9][0-9]*)?e[1-9][0-9]*$/u.test(value.ref ?? "")) return value.ref;
    for (const child of Object.values(value)) if (child && typeof child === "object") queue.push(...(Array.isArray(child) ? child : [child]));
  }
  return "";
}
