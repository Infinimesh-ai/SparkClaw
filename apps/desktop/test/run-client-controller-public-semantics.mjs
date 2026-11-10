// Actual packaged Electron/Controller plus the production Go public adapters.
// The JSON message bridge and authority are synthetic. No ISCP, provider,
// installed user profile or real workflow qualification is claimed here.
import assert from "node:assert/strict";
import crypto from "node:crypto";
import fs from "node:fs/promises";
import path from "node:path";
import { spawn } from "node:child_process";
import { createInterface } from "node:readline";
import { pathToFileURL } from "node:url";

const directory = process.env.SPARKCLAW_CONTROLLER_SEMANTIC_ROOT;
const executable = process.env.SPARKCLAW_CONTROLLER_CANDIDATE;
const driver = process.env.SPARKCLAW_CONTROLLER_TOOL_FIXTURE;
const launcher = process.env.SPARKCLAW_CONTROLLER_LAUNCHER;
const routeMode = process.env.SPARKCLAW_CONTROLLER_ROUTE_SEMANTICS === "1";
assert.ok(path.isAbsolute(directory || "") && path.basename(directory).startsWith("sx-controller-semantics-"));
assert.ok(path.isAbsolute(executable || "") && !executable.startsWith("/Applications/"));
assert.ok(path.isAbsolute(driver || ""));
await fs.mkdir(directory, { mode: 0o700 });
const profile = path.join(directory, "profile");
const resources = path.resolve(path.dirname(executable), process.platform === "darwin" ? "../Resources" : "resources");
const hash = data => crypto.createHash("sha256").update(data).digest("hex");
const record = async filename => { const bytes = await fs.readFile(filename); return { path: filename, bytes: bytes.length, sha256: hash(bytes) }; };
const asar = await record(path.join(resources, "app.asar"));
assert.equal(asar.sha256, process.env.SPARKCLAW_CONTROLLER_EXPECTED_ASAR);
const { _electron } = await import(pathToFileURL(path.join(resources, "browser-controller/node_modules/playwright-core/index.mjs")));
const evidence = { schema_version: 1, scope: "actual_package_public_Go_tools_native_effects_local_fixture_transport",
  encrypted_ISCP: false, real_provider: false, production_authority: false, passed: false,
  asar, driver: await record(driver), checks: {}, tools: [], reverse: [] };
evidence.matrix = routeMode ? "frozen_target_routes" : "public_tool_effects";
let application, child, lines, childExit;
const pending = new Map();
let sequence = 0, readyResolve;
const ready = new Promise(resolve => { readyResolve = resolve; });
const env = Object.fromEntries(Object.entries(process.env).filter(([key]) => !key.startsWith("SPARKCLAW_") && key !== "ELECTRON_RUN_AS_NODE"));
Object.assign(env, { SPARKCLAW_DESKTOP_USER_DATA_DIR: profile, SPARKCLAW_DESKTOP_RUNTIME_DIR: path.join(directory, "empty-runtime") });
const request = async input => {
  const id = `tool_${++sequence}`;
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => { pending.delete(id); reject(Error("Public tool fixture deadline")); }, 35000);
    pending.set(id, { resolve: value => { clearTimeout(timer); resolve(value); }, reject });
    child.stdin.write(JSON.stringify({ ...input, id }) + "\n");
  });
};
const tool = async (name, args = {}, expectedError = false) => {
  const result = await request({ kind: "tool", tool: name, conversation_id: "semantic-A", task_id: "semantic-task-A", arguments: args });
  evidence.tools.push(result);
  if (expectedError) assert.equal(typeof result.error, "string", JSON.stringify(result));
  else assert.equal(result.error, undefined, JSON.stringify(result));
  if (expectedError instanceof RegExp) assert.match(result.error, expectedError);
  return result.result;
};
const inspect = () => application.evaluate(async () => {
  const { registry, runtime, forwarder } = globalThis.__publicSemantics;
  const record = registry.conversationPages.get("semantic-A");
  let kernelSandbox;
  if (record && process.platform === "linux") {
    const status = await process.getBuiltinModule("node:fs/promises").readFile(`/proc/${record.webContents.getOSProcessId()}/status`, "utf8");
    kernelSandbox = { no_new_privileges: Number(status.match(/^NoNewPrivs:\s+(\d+)$/mu)?.[1]),
      seccomp: Number(status.match(/^Seccomp:\s+(\d+)$/mu)?.[1]) };
  }
  return { selected: registry.selectedConversationID, presented: registry.presentation.presented?.pageRef ?? null,
    pages: registry.desktopSnapshot(), runtime: runtime.snapshot(), controller_pid: runtime.child?.pid ?? null,
    lease: forwarder.lease?.value ?? null, journal: [...forwarder.journal.rows.values()],
    effect: record && { sandbox: record.webContents.getLastWebPreferences().sandbox, kernel_sandbox: kernelSandbox,
      debugger: record.webContents.debugger.isAttached(), dom: await record.webContents.executeJavaScript(
        '({value:document.querySelector("input").value,inputs:window.inputs,submits:window.submits,text:document.querySelector("#state").textContent})') } };
});
try {
  application = await _electron.launch({ executablePath: launcher || executable, args: [], env, timeout: 60000, chromiumSandbox: true });
  await application.firstWindow();
  evidence.runtime = await application.evaluate(async ({ app, BrowserWindow, WebContentsView, session }) => {
    if (!app.isPackaged) throw Error("Actual packaged main required");
    const require = process.getBuiltinModule("node:module").createRequire(app.getAppPath() + "/package.json");
    const path = process.getBuiltinModule("node:path"), fs = process.getBuiltinModule("node:fs/promises"), crypto = process.getBuiltinModule("node:crypto");
    const { ElectronAdapterServer } = require(app.getAppPath() + "/src/browser/adapter-server.mjs");
    const { PageRegistry } = require(app.getAppPath() + "/src/browser/page-registry.mjs");
    const { BrowserPresentation } = require(app.getAppPath() + "/src/main/presentation.mjs");
    const { ClientControllerRuntime } = require(app.getAppPath() + "/src/browser/controller-runtime.mjs");
    const { BrowserControllerForwarder } = require(app.getAppPath() + "/src/browser/controller-forwarder.mjs");
    const { CONTROLLER_OPERATIONS } = require(app.getAppPath() + "/src/browser/controller-wire.mjs");
    const browser = session.fromPartition(`semantic-${crypto.randomUUID()}`);
    await browser.protocol.handle("sparkclaw-internal", () => new Response("<!doctype html><title>Local adapter</title>"));
    await browser.protocol.handle("https", request => {
      const url = new URL(request.url);
      if (!["https://semantics.fixture.test", "https://child.semantics.fixture.test", "https://unrelated.fixture.test"].includes(url.origin)) throw Error("Unexpected native fixture origin");
      const redirects = {
        "/redirect-same": "https://semantics.fixture.test/alternate?receipt=fixture#/drift",
        "/redirect-child": "https://child.semantics.fixture.test/form",
        "/redirect-foreign": "https://unrelated.fixture.test/form",
        "/bounce": "https://semantics.fixture.test/alternate",
      };
      if (url.origin === "https://semantics.fixture.test" && redirects[url.pathname]) return new Response(null, { status: 302, headers: { location: redirects[url.pathname] } });
      if (url.pathname === "/personal") return new Response("<!doctype html><title>Personal fixture</title><p>Personal retained</p>", { headers: { "content-type": "text/html" } });
      if (!["/form", "/alternate"].includes(url.pathname)) throw Error("Unexpected native fixture path");
      return new Response('<!doctype html><meta charset="utf-8"><title>Public tool semantics</title><form><label>Name<input aria-label="Name"></label><button type="submit">Submit</button></form><p id="state">Draft</p><script>window.inputs=0;window.submits=0;document.querySelector("input").oninput=()=>window.inputs++;document.querySelector("form").onsubmit=e=>{e.preventDefault();window.submits++;document.querySelector("#state").textContent="Submitted"}</script>', { headers: { "content-type": "text/html" } });
    });
    // A hidden fixture window, separate from the unchanged original workbench.
    const window = new BrowserWindow({ show: false, width: 1100, height: 800,
      webPreferences: { sandbox: true, contextIsolation: true, nodeIntegration: false } });
    await window.loadURL("data:text/html,<title>Native public tool qualification</title>");
    const shield = new WebContentsView({ webPreferences: { sandbox: true, contextIsolation: true } });
    const presentation = new BrowserPresentation({ window, shieldView: shield, explicitSelection: true });
    const registry = new PageRegistry({ runtimeGeneration: crypto.randomBytes(16).toString("hex"), browserSession: browser, presentation,
      createView: () => new WebContentsView({ webPreferences: { session: browser, sandbox: true, contextIsolation: true, nodeIntegration: false } }) });
    const runtimeRoot = await fs.mkdtemp(path.join(await fs.realpath("/tmp"), "sx-sem-"));
    await fs.chmod(runtimeRoot, 0o700);
    const runtime = new ClientControllerRuntime({ bundleRoot: path.join(process.resourcesPath, "browser-controller"), runtimeRoot,
      adapterFactory: async ({ directory, incarnation }) => new ElectronAdapterServer({
        socketPath: path.join(directory, "electron-adapter.sock"), secretPath: path.join(directory, "adapter-secret"),
        downloadRoot: path.join(directory, "downloads"), runtimeGeneration: registry.runtimeGeneration, registry,
        browserSession: browser, controllerIncarnation: incarnation,
      }).start() });
    await runtime.prepare();
    const manifest = { schema_version: 2, profile: "sparkclaw.workbench.transport.v2", session_id: "semantic_session", authorization_revision: 1,
      expires_at: new Date(Date.now() + 300000).toISOString(), operations: [...CONTROLLER_OPERATIONS, "browser.receipt", "browser.reconcile"],
      limits: { body_bytes: 63488, response_bytes: 63488, concurrent: 8 } };
    const auth = { descriptor: { transport: "iscp" }, connection: { ownerID: "semantic-owner", clientID: "semantic-client" },
      status: { state: "connected", capabilities: { browser: true } }, transport: { capabilities: manifest }, invokeISCP: async () => ({ fences: [] }) };
    const forwarder = new BrowserControllerForwarder({ auth, registry, runtime, userDataDir: path.join(app.getPath("userData"), "semantic-fixture") });
    await forwarder.start({ owner_id: "semantic-owner", client_id: "semantic-client", installation_id: "semantic-installation" });
    registry.selectConversation("semantic-selected-other");
    const personal = await registry.createPersonalPage("https://semantics.fixture.test/personal");
    globalThis.__publicSemantics = { registry, runtime, forwarder, manifest, window, runtimeRoot, personal: personal.pageRef };
    return { executable: process.execPath, packaged: app.isPackaged, profile: app.getPath("userData"),
      versions: process.versions, architecture: process.arch, runtime_root: runtimeRoot, personal: personal.pageRef,
      sandbox_disabled: app.commandLine.hasSwitch("no-sandbox") };
  });
  assert.equal(evidence.runtime.executable, executable); assert.equal(evidence.runtime.profile, profile);
  assert.equal(evidence.runtime.sandbox_disabled, false);
  evidence.checks.actual_package_and_separate_fresh_profile = true;
  child = spawn(driver, [path.join(directory, "broker")], { stdio: ["pipe", "pipe", "pipe"] });
  childExit = new Promise(resolve => child.once("exit", (code, signal) => resolve({ code, signal })));
  const stderr = []; child.stderr.on("data", chunk => { if (stderr.length < 16) stderr.push(chunk.toString().slice(0, 4096)); });
  lines = createInterface({ input: child.stdout });
  lines.on("line", line => { void (async () => {
    const frame = JSON.parse(line);
    if (frame.kind === "ready") { evidence.driver_ready = frame; readyResolve(); return; }
    if (frame.kind === "result") { const item = pending.get(frame.id); pending.delete(frame.id); item?.resolve(frame); return; }
    assert.equal(frame.kind, "reverse");
    const started = performance.now();
    const reply = await application.evaluate(async (_, frame) => {
      const { forwarder, manifest } = globalThis.__publicSemantics;
      return forwarder.handleReverse({ type: "task.invoke", profile: manifest.profile, id: frame.id,
        operation: frame.operation, installation_id: "semantic-installation", body: frame.body }, manifest);
    }, frame);
    evidence.reverse.push({ id: frame.id, operation: frame.operation, inner_operation: frame.body.operation,
      arguments: frame.body.arguments, status: reply.status, outcome: reply.body?.outcome, error_code: reply.body?.error_code,
      duration_ms: Math.round(performance.now() - started) });
    child.stdin.write(JSON.stringify({ kind: "reverse_result", id: frame.id, status: reply.status, body: reply.body }) + "\n");
  })().catch(error => { evidence.bridge_error = String(error); for (const item of pending.values()) item.reject(error); }); });
  await Promise.race([ready, childExit.then(() => { throw Error("Go fixture exited before ready"); }), new Promise((_, reject) => setTimeout(() => reject(Error("Go fixture startup timeout")), 10000).unref())]);
  const empty = await tool("browser.list_tabs"); assert.deepEqual(empty.pages, []); assert.equal(evidence.reverse.length, 0);
  await tool("browser.status");
  assert.equal((await inspect()).controller_pid, null);
  assert.deepEqual(evidence.reverse.map(row => row.operation), ["browser.controller.status"]);
  evidence.checks.list_without_lease_and_status_do_not_start_native_runtime = true;
  if (routeMode) {
    const origin = "https://semantics.fixture.test";
    const opened = await tool("browser.open", { url: `${origin}/form#/target` });
    const pageID = opened.pages.find(page => page.selected)?.page_id || opened.pages[0]?.page_id;
    assert.ok(pageID);
    const wait = args => tool("browser.wait", { mode: "stable_state", page_id: pageID, allow_no_change: true,
      timeout_ms: 3000, quiet_period_ms: 100, poll_interval_ms: 25, ...args });
    const rejectWait = async (args, pattern) => {
      const before = evidence.reverse.length;
      await tool("browser.wait", { mode: "stable_state", page_id: pageID, allow_no_change: true,
        timeout_ms: 3000, quiet_period_ms: 100, poll_interval_ms: 25, ...args }, pattern);
      return evidence.reverse.slice(before);
    };
    await tool("browser.navigate", { page_id: pageID, url: `${origin}/redirect-same` });
    const rebound = await wait({ expected_url: `${origin}/form#/target`, target_kind: "explicit_url" });
    assert.equal(rebound.output.route_rebinds, 1);
    assert.equal(rebound.output.url, `${origin}/form?receipt=fixture#/target`);
    assert.equal(rebound.output.quiet_period_ms, 100);
    assert.ok(rebound.output.observations >= 5);
    evidence.checks.same_origin_redirect_rebind_preserves_query_and_frozen_path_fragment = true;
    await tool("browser.navigate", { page_id: pageID, url: `${origin}/redirect-child` });
    const denied = await rejectWait({ expected_url: `${origin}/`, target_kind: "explicit_url" }, /browser_route_diverged/);
    assert.deepEqual(denied.map(row => row.inner_operation), ["page.read"]);
    const registered = await wait({ expected_url: `${origin}/`, target_kind: "registered_destination" });
    assert.equal(registered.output.url, "https://child.semantics.fixture.test/form");
    assert.equal(registered.output.route_rebinds, 0);
    evidence.checks.explicit_target_rejects_child_redirect_registered_destination_accepts = true;
    await tool("browser.navigate", { page_id: pageID, url: `${origin}/redirect-foreign` });
    const foreign = await rejectWait({ expected_url: `${origin}/`, target_kind: "registered_destination" }, /browser_route_diverged/);
    assert.deepEqual(foreign.map(row => row.inner_operation), ["page.read"]);
    const beforeRead = evidence.reverse.length;
    await tool("browser.read", { url: `${origin}/form`, page_id: pageID, reuse_active_page: true }, /does not match bound read URL/);
    assert.deepEqual(evidence.reverse.slice(beforeRead).map(row => row.inner_operation), ["page.info"]);
    evidence.checks.foreign_redirect_and_bound_read_mismatch_reject_without_navigation = true;
    const stale = await rejectWait({ session_generation: registered.session_generation + 1 }, /browser_session_stale/);
    assert.deepEqual(stale, []);
    evidence.checks.stale_wait_session_rejects_before_native_dispatch = true;
    await tool("browser.navigate", { page_id: pageID, url: `${origin}/bounce#/target` });
    const bounded = await rejectWait({ expected_url: `${origin}/bounce#/target`, target_kind: "explicit_url" }, /same-origin route rebind limit exceeded/);
    const navigations = bounded.filter(row => row.inner_operation === "page.navigate");
    assert.equal(navigations.length, 2);
    assert.ok(navigations.every(row => row.arguments.url === `${origin}/bounce#/target`));
    evidence.checks.repeated_same_origin_redirect_stops_at_existing_two_rebind_limit = true;
    await tool("browser.close", { page_id: pageID });
    evidence.after_close = await inspect();
    assert.equal(evidence.after_close.lease, null);
    assert.ok(evidence.after_close.pages.every(row => row.role !== "task"));
    assert.ok(evidence.after_close.pages.some(row => row.page_ref === evidence.runtime.personal));
    evidence.checks.route_failures_leave_no_lease_after_explicit_close = true;
  } else {
  const opened = await tool("browser.navigate", { url: "https://semantics.fixture.test/form", presentation: "hidden" });
  const state = await inspect(); assert.ok(state.effect?.sandbox); assert.equal(state.selected, "semantic-selected-other");
  const read = await tool("browser.read", { url: "https://semantics.fixture.test/form", reuse_active_page: true, max_chars: 2000 }); assert.ok(JSON.stringify(read).includes("Name"));
  const snapshot = await tool("browser.snapshot");
  const shot = snapshot.output?.snapshot || snapshot.output;
  const field = shot.controls?.find(row => row.accessible_name === "Name" || row.name === "Name");
  assert.ok(field, JSON.stringify(snapshot));
  const ref = field.ref || field.uid;
  const pageID = shot.page_id;
  assert.ok(ref && pageID && shot.snapshot_id);
  await tool("browser.type", { page_id: pageID, ref, snapshot_id: shot.snapshot_id, text: "Alice", submit: false });
  const filled = await inspect(); assert.equal(filled.effect.dom.value, "Alice"); assert.equal(filled.effect.dom.submits, 0);
  if (process.platform === "linux") assert.deepEqual(filled.effect.kernel_sandbox, { no_new_privileges: 1, seccomp: 2 });
  const beforeFocus = evidence.reverse.length;
  await tool("browser.focus", { page_id: pageID, presentation: "visible" });
  assert.deepEqual(evidence.reverse.slice(beforeFocus).map(row => row.inner_operation), ["page.info"]);
  const focused = await inspect(); assert.equal(focused.selected, "semantic-selected-other"); assert.equal(focused.presented, filled.presented);
  evidence.checks.public_focus_is_logical_without_handoff_or_conversation_selection = true;
  await tool("browser.type", { page_id: pageID, mode: "focused", text: "Bob", submit: true });
  const typed = await inspect(); evidence.effects = { filled: filled.effect, typed: typed.effect };
  assert.equal(typed.effect.dom.value, "AliceBob"); assert.equal(typed.effect.dom.submits, 1);
  assert.equal(evidence.reverse.filter(row => row.inner_operation === "page.fill").length, 1);
  assert.equal(evidence.reverse.filter(row => row.inner_operation === "page.type").length, 1);
  evidence.checks.ref_fill_and_focused_type_submit_have_distinct_actual_effects = true;
  await tool("browser.wait", { page_id: pageID, text: "Submitted" });
  await tool("browser.wait", { page_id: pageID, duration_ms: 25 });
  assert.ok(evidence.reverse.some(row => row.inner_operation === "page.wait" && row.arguments?.text === "Submitted"));
  assert.ok(evidence.reverse.some(row => row.inner_operation === "page.wait" && row.arguments?.duration_ms === 25));
  evidence.checks.public_wait_fields_reach_native_controller = true;
  const beforeStale = evidence.reverse.length;
  await tool("browser.type", { page_id: pageID, ref, snapshot_id: shot.snapshot_id, text: "Rejected" }, true);
  assert.equal(evidence.reverse.length, beforeStale); assert.equal((await inspect()).effect.dom.value, "AliceBob");
  evidence.checks.invalidated_snapshot_ref_is_rejected_before_native_dispatch = true;
  const listed = await tool("browser.list_tabs");
  assert.ok(listed.pages.length > 0 && listed.pages.every(row => row.url !== "https://semantics.fixture.test/personal"));
  const beforeClose = evidence.reverse.length;
  await tool("browser.close", { page_id: pageID });
  const closed = await inspect(); evidence.after_close = closed;
  assert.equal(closed.lease, null); assert.ok(closed.pages.every(row => row.role !== "task"));
  assert.ok(closed.pages.some(row => row.page_ref === evidence.runtime.personal));
  assert.ok(!evidence.reverse.slice(beforeClose).some(row => row.inner_operation === "tabs.new"));
  assert.ok(evidence.reverse.slice(beforeClose).some(row => row.inner_operation === "tabs.close"));
  assert.ok(evidence.reverse.slice(beforeClose).some(row => row.operation === "browser.controller.release"));
  evidence.checks.public_close_releases_lease_without_blank_page_and_preserves_personal = true;
  }
  evidence.shutdown = await request({ kind: "shutdown" });
  assert.deepEqual(evidence.shutdown.fences, []);
  evidence.driver_exit = await childExit; assert.equal(evidence.driver_exit.code, 0);
  evidence.driver_stderr = stderr;
  evidence.passed = true;
} catch (error) {
  evidence.error = String(error);
  if (application) evidence.failure_state = await inspect().catch(() => null);
}
finally {
  if (application) {
    try {
      evidence.cleanup = await application.evaluate(async () => {
        const state = globalThis.__publicSemantics;
        if (!state) return { initialized: false };
        const { forwarder, runtime, registry, window, runtimeRoot } = state;
        await forwarder.stop();
        for (const row of [...registry.records.values()]) registry.destroy(row.tabID, "semantic_fixture_complete");
        window.destroy();
        const fs = process.getBuiltinModule("node:fs/promises");
        const remaining = await fs.readdir(runtimeRoot);
        if (!remaining.length) await fs.rmdir(runtimeRoot);
        return { initialized: true, runtime_state: runtime.state, child: runtime.child?.pid ?? null,
          records: registry.records.size, runtime_files: remaining };
      });
      assert.equal(evidence.cleanup.child, null); assert.equal(evidence.cleanup.runtime_state, "stopped");
      assert.deepEqual(evidence.cleanup.runtime_files, []); assert.equal(evidence.cleanup.records, 0);
      evidence.checks.owned_native_resources_cleaned = true;
    } catch (error) { evidence.cleanup_error = String(error); evidence.passed = false; }
    await application.close().catch(error => { evidence.quit_error = String(error); evidence.passed = false; });
  }
  if (child && child.exitCode === null) { child.stdin.end(); child.kill("SIGTERM"); await childExit; }
  lines?.close();
  await fs.writeFile(path.join(directory, "evidence.json"), JSON.stringify(evidence, null, 2) + "\n", { flag: "wx", mode: 0o600 });
}
console.log(JSON.stringify({ passed: evidence.passed, checks: evidence.checks, error: evidence.error, evidence: path.join(directory, "evidence.json") }));
if (!evidence.passed) process.exitCode = 1;
