// Opt-in actual-package proof: lock through original Settings, restart locked,
// then unlock with the same device credential and fence the original lease.
import assert from "node:assert/strict";
import fs from "node:fs/promises";
import path from "node:path";
import crypto from "node:crypto";
import { execFileSync } from "node:child_process";
import { pathToFileURL } from "node:url";
import { qualificationLaunchEnvironment, verifyQualificationAuthorization, synchronizeQualificationFences } from "./client-controller-qualification-launch.mjs";

const directory = process.env.SPARKCLAW_CONTROLLER_ISCP_LAB;
const executablePath = process.env.SPARKCLAW_CONTROLLER_CANDIDATE;
assert.ok(path.isAbsolute(directory || "") && path.basename(directory).startsWith("sx-controller-iscp-"));
assert.ok(path.isAbsolute(executablePath || "") && !executablePath.startsWith("/Applications/"));
assert.equal(process.env.SPARKCLAW_CONTROLLER_NODE_MANAGED, "1");
const credentialPath = process.env.SPARKCLAW_CONTROLLER_LOCK_CREDENTIAL;
assert.ok(path.isAbsolute(credentialPath || ""));
const credentialInfo = await fs.lstat(credentialPath);
assert.ok(credentialInfo.isFile() && !credentialInfo.isSymbolicLink());
assert.equal(credentialInfo.uid, process.getuid()); assert.equal(credentialInfo.mode & 0o077, 0);
const credential = (await fs.readFile(credentialPath, "utf8")).trim();
assert.ok(credential.startsWith("sparkclaw-iscp-connect-v1."));
const resources = path.resolve(path.dirname(executablePath), process.platform === "darwin" ? "../Resources" : "resources");
const { _electron } = await import(pathToFileURL(path.join(resources, "browser-controller/node_modules/playwright-core/index.mjs")));
const output = path.join(directory, `evidence/controller-packaged-lock-recovery-${Date.now()}.json`);
const evidence = { schema_version: 1, passed: false, checks: {},
  scope: "actual_package_original_Settings_lock_same_profile_restart_and_same_credential_unlock", local_probes_are_not_wire_calls: true };
const hash = bytes => crypto.createHash("sha256").update(bytes).digest("hex");
const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
let application, window, originalExecute, originalAuthority;
async function until(check, label, timeout = 90000) {
  const end = performance.now() + timeout;
  while (performance.now() < end) { if (await check()) return; await pause(100); }
  throw Error(`Packaged Lock recovery timeout: ${label}`);
}
async function launch(locked = false) {
  application = await _electron.launch({ executablePath, timeout: 45000, chromiumSandbox: true,
    env: qualificationLaunchEnvironment(directory) });
  application.process().stderr.on("data", bytes => process.stderr.write(bytes));
  await application.evaluate(async ({ app, net, session }) => {
    const require = process.getBuiltinModule("node:module").createRequire(app.getAppPath() + "/package.json");
    const assert = require("node:assert/strict");
    const { BrowserControllerForwarder } = require(app.getAppPath() + "/src/browser/controller-forwarder.mjs");
    const { ClientControllerRuntime } = require(app.getAppPath() + "/src/browser/controller-runtime.mjs");
    const { ISCPTransport } = require(app.getAppPath() + "/src/main/iscp-transport.mjs");
    const trace = globalThis.__lockTrace = { native_calls: [], acquisitions: [], business_http: 0, reads: [], read_drops: 0 };
    // Observe failures before DesktopAuth maps them to its public error. Keep
    // fixed labels/counts only: no URLs, credentials, request or response bodies.
    const transportFetch = ISCPTransport.prototype.fetch;
    ISCPTransport.prototype.fetch = async function(raw, init) {
      const started = Date.now(), generation = this.generation;
      const category = /^\/api\/v1\/executions\/[^/]+$/u.test(new URL(raw).pathname) ? "execution" : "other";
      const record = value => {
        if (trace.reads.length < 2048) trace.reads.push({ category, elapsed_ms: Date.now() - started, generation,
          current_generation: this.generation, transport_state: this.state, pending: this.pending.size, ...value });
        else trace.read_drops++;
      };
      try { const response = await transportFetch.call(this, raw, init); record({ status: response.status }); return response; }
      catch (error) {
        const allowed = ["ISCP transport is unavailable", "ISCP request was canceled", "ISCP request deadline exceeded", "ISCP authentication changed", "ISCP request concurrency limit reached", "ISCP read admission deadline exceeded"];
        record({ error: allowed.includes(error.message) ? error.message : "unclassified", aborted: Boolean(init?.signal?.aborted) });
        throw error;
      }
    };
    const snapshot = BrowserControllerForwarder.prototype.snapshot;
    BrowserControllerForwarder.prototype.snapshot = function(...args) { globalThis.__lockAgent = this; return snapshot.apply(this, args); };
    const request = ClientControllerRuntime.prototype.request;
    ClientControllerRuntime.prototype.request = async function(action, body, options) {
      trace.native_calls.push({ action, operation: body.operation });
      const response = await request.call(this, action, body, options);
      if (action === "acquire") trace.acquisitions.push({ incarnation: this.incarnation, status: response.status });
      return response;
    };
    const reverse = BrowserControllerForwarder.prototype.handleReverse;
    BrowserControllerForwarder.prototype.handleReverse = async function(request, authority) {
      globalThis.__lockAgent = this;
      if (request.operation === "browser.controller.execute") {
        globalThis.__approvalExecute = structuredClone(request);
        globalThis.__approvalAuthority = structuredClone(authority);
      }
      const reply = await reverse.call(this, request, authority);
      return reply;
    };
    const fetch = net.fetch.bind(net);
    net.fetch = (...args) => { if (/^https?:/u.test(String(args[0]?.url || args[0]))) { trace.business_http++; throw Error("Business HTTP forbidden"); } return fetch(...args); };
    globalThis.fetch = () => { trace.business_http++; throw Error("Business HTTP forbidden"); };
    session.fromPartition("persist:sparkclaw-workbench-default").webRequest.onBeforeRequest({ urls: ["http://*/*", "https://*/*", "ws://*/*", "wss://*/*"] }, (_request, callback) => { trace.business_http++; callback({ cancel: true }); });
    await session.fromPartition("persist:sparkclaw-browser-default").protocol.handle("https", request => {
      assert.equal(new URL(request.url).origin, "https://controller.fixture.test");
      return new Response('<!doctype html><title>Lock recovery fixture</title><label>Display name<input aria-label="Display name"></label><script>window.fixtureInputs=0;document.querySelector("input").oninput=()=>window.fixtureInputs++</script>', { headers: { "content-type": "text/html" } });
    });
  });
  await until(() => (window = application.windows().find(page => page.url() === "sparkclaw-app://workbench/index.html")), "workbench");
  if (locked) {
    await window.waitForSelector(".desktopLogin");
    assert.equal((await window.evaluate(() => window.sparkclawDesktop.localConnection())).state, "locked");
  } else {
    await window.waitForSelector(".localWorkbench");
    await until(async () => (await window.evaluate(() => window.sparkclawDesktop.localConnection())).state === "connected", "connection");
    verifyQualificationAuthorization(await window.evaluate(() => window.sparkclawDesktop.localConnection()));
    await until(async () => (await window.evaluate(() => window.sparkclawDesktop.state())).browser_host.state === "connected", "Controller readiness");
    await synchronizeQualificationFences(application);
  }
  const runtime = await application.evaluate(({ app, safeStorage }) => ({ packaged: app.isPackaged, executable: process.execPath,
    profile: app.getPath("userData"), versions: process.versions, secure_storage: safeStorage.isEncryptionAvailable() }));
  assert.ok(runtime.packaged && runtime.secure_storage); assert.equal(runtime.executable, executablePath);
  assert.equal(runtime.profile, path.join(directory, "controller-native-profile"));
  evidence.runtime ||= runtime; assert.equal(runtime.profile, evidence.runtime.profile);
}
const sorted = rows => [...rows].sort((a, b) => a.command_id.localeCompare(b.command_id));
async function unchangedUnknowns() {
  const rows = await synchronizeQualificationFences(application);
  for (const prior of evidence.unknown_before) assert.deepEqual(rows.find(row => row.command_id === prior.command_id), prior);
  for (const row of rows) if (!evidence.unknown_before.some(prior => prior.command_id === row.command_id)) assert.equal(row.local_task_id, evidence.task.local_task_id);
  evidence.latest_unknowns = sorted(rows);
  return rows;
}
async function readTask(task) {
  await window.evaluate(id => window.sparkclawClientStore.reconcile(id), task.request_id);
  return window.evaluate(id => window.sparkclawClientStore.read(id), task.conversation_id);
}
async function pendingApproval(task) {
  let approval;
  await until(async () => {
    const state = await readTask(task);
    approval = state.tasks.find(row => row.request_id === task.request_id)?.approvals?.find(row => row.state === "pending" && row.actionable);
    return Boolean(approval);
  }, "fill approval");
  assert.equal(approval.tool, "browser.type");
  return approval;
}
async function submit(name) {
  return window.evaluate(async name => {
    const store = window.sparkclawClientStore, conversation = await store.create(`Lock recovery ${name}`);
    await window.sparkclawDesktop.selectConversation(conversation.id);
    const task = await store.enqueue(conversation.id, `在 https://controller.fixture.test/ 页面 Display name 字段填写 ${name}。`, []);
    await store.submit(task.request_id);
    return { request_id: task.request_id, local_task_id: task.id, conversation_id: conversation.id };
  }, name);
}
async function identity() {
  return application.evaluate(({ app }, requestID) => {
    const db = new (process.getBuiltinModule("node:sqlite").DatabaseSync)(process.getBuiltinModule("node:path").join(app.getPath("userData"), "workbench/client.sqlite"), { readOnly: true });
    try { return { installation: db.prepare("SELECT value FROM metadata WHERE key='installation_id'").get().value,
      tasks: db.prepare("SELECT id,conversation_id,request_id,input_digest,context_json,created_at FROM tasks WHERE request_id=?").all(requestID) }; }
    finally { db.close(); }
  }, evidence.task.request_id);
}
async function rejectOldApproval() {
  const rejected = await window.evaluate(({ task, approval }) => window.sparkclawClientStore
    .decideApproval(task.request_id, approval.approval_id, approval.digest, "approve").then(() => false, () => true),
  { task: evidence.task, approval: evidence.original_approval });
  assert.equal(rejected, true);
}
async function unlockWithOriginalCredential() {
  await window.evaluate(value => {
    const input = document.querySelector("#desktopCredential");
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value").set.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  }, credential);
  await window.locator(".desktopLogin form button[type=submit]").click();
  await until(async () => (await window.evaluate(() => window.sparkclawDesktop.localConnection())).state === "connected", "same credential unlock", 150000);
}
try {
  const configBytes = await fs.readFile(path.join(directory, "gateway-config.json")), catalogBytes = await fs.readFile(path.join(directory, "model.profiles.json"));
  const config = JSON.parse(configBytes), catalog = JSON.parse(catalogBytes);
  evidence.provider = { mock: catalog.profiles[config.model.capacity_profile].mock, config_sha256: hash(configBytes), catalog_sha256: hash(catalogBytes) };
  assert.equal(evidence.provider.mock, false);
  evidence.asar_sha256 = hash(await fs.readFile(path.join(resources, "app.asar")));
  await launch();
  evidence.connection_before = await window.evaluate(() => window.sparkclawDesktop.localConnection());
  evidence.credential_binding_verified = await application.evaluate(async ({ app }, value) => {
    const require = process.getBuiltinModule("node:module").createRequire(app.getAppPath() + "/package.json");
    const fs = require("node:fs/promises"), path = require("node:path"), crypto = require("node:crypto");
    const { parseISCPConnectionCredential } = require(app.getAppPath() + "/src/main/iscp-connection-credential.mjs");
    const { hasPendingNodeEnrollment } = require(app.getAppPath() + "/src/main/iscp-node-enrollment.mjs");
    const payload = parseISCPConnectionCredential(value, { allowExpired: true }).payload;
    const root = app.getPath("userData"), selected = JSON.parse(await fs.readFile(path.join(root, "iscp-launch-profile.json"), "utf8"));
    const directory = path.join(root, "iscp-enrollment", crypto.createHash("sha256").update(value).digest("hex"));
    const profile = JSON.parse(await fs.readFile(selected.profile_path, "utf8"));
    const helper = JSON.parse(await fs.readFile(profile.helper_config, "utf8"));
    return path.dirname(selected.profile_path) === directory && payload.client_id === profile.backend.client_id &&
      payload.domain_id === profile.backend.domain_id && payload.deployment_id === profile.backend.deployment_id &&
      await hasPendingNodeEnrollment(directory, helper.expected_installation_id, payload.domain_id);
  }, credential);
  assert.equal(evidence.credential_binding_verified, true);
  evidence.unknown_before = await synchronizeQualificationFences(application);
  const marker = crypto.randomUUID();
  await application.evaluate(async ({ session }, marker) => {
    const browser = session.fromPartition("persist:sparkclaw-browser-default");
    await browser.cookies.set({ url: "https://controller.fixture.test/", name: "controller-lock-marker", value: marker, expirationDate: Date.now() / 1000 + 86400 });
    await browser.cookies.flushStore();
  }, marker);
  const retained = await window.evaluate(async () => {
    const store = window.sparkclawClientStore, conversation = await store.create("Preapproval retained draft"), draft = await store.draft(conversation.id);
    const file = await store.saveFile(conversation.id, "approval-retained.txt", new TextEncoder().encode("Preserve preapproval attachment.\n"));
    await store.saveDraft(conversation.id, "Keep this unsent draft.", [file.id], draft.revision, draft.scope_key);
    return { conversation: conversation.id, file: file.id };
  });
  evidence.task = await submit("Alice");
  evidence.original_approval = await pendingApproval(evidence.task);
  evidence.before_identity = await identity(); assert.equal(evidence.before_identity.tasks.length, 1);
  evidence.trace_before = await application.evaluate(() => globalThis.__lockTrace);
  assert.equal(evidence.trace_before.native_calls.filter(row => row.operation === "page.fill").length, 0);
  evidence.original_effect = await application.evaluate(async ({ webContents }) => {
    const page = webContents.getAllWebContents().find(page => page.getURL() === "https://controller.fixture.test/" && page.debugger.isAttached());
    return page?.executeJavaScript('({value:document.querySelector("input").value,inputs:window.fixtureInputs})');
  });
  assert.deepEqual(evidence.original_effect, { value: "", inputs: 0 });
  originalExecute = await application.evaluate(() => globalThis.__approvalExecute);
  originalAuthority = await application.evaluate(() => globalThis.__approvalAuthority);
  assert.ok(originalExecute?.body?.lease);
  evidence.execution = await application.evaluate(() => ({ controller_pid: globalThis.__lockAgent.runtime.child.pid,
    runtime_directory: globalThis.__lockAgent.runtime.directory, incarnation: globalThis.__lockAgent.runtime.incarnation,
    old_session: globalThis.__lockAgent.session.session_id, journal_root: globalThis.__lockAgent.journal.root }));
  const controller = evidence.execution.controller_pid, runtimeDirectory = evidence.execution.runtime_directory;
  const rows = execFileSync("ps", ["-axo", "pid,ppid,pgid,command"], { encoding: "utf8" }).split("\n")
    .map(line => line.match(/^\s*(\d+)\s+(\d+)\s+(\d+)\s+(.*)$/u)).filter(Boolean);
  const ownedController = rows.find(row => Number(row[1]) === controller);
  assert.ok(ownedController && Number(ownedController[2]) === application.process().pid && Number(ownedController[3]) === controller &&
    ownedController[4].includes(resources + "/browser-controller/src/client-main.mjs"));
  assert.equal(await fs.realpath(path.dirname(runtimeDirectory)), path.join(await fs.realpath("/tmp"), `sparkx-${process.getuid()}`));
  assert.match(path.basename(runtimeDirectory), /^c-[A-Za-z0-9]+$/u);
  const owned = await fs.lstat(runtimeDirectory); assert.equal(owned.uid, process.getuid()); assert.equal(owned.mode & 0o077, 0); assert.equal(owned.isSymbolicLink(), false);
  const personal = (await window.evaluate(() => window.sparkclawDesktop.createPersonal())).page_ref;
  evidence.checks.pending_original_approval_has_zero_native_fill = true;
  await window.locator(".sidebarAccountTrigger").click();
  await window.locator(".sidebarAccountMenuItem").filter({ hasText: /settings|设置/i }).click();
  await window.waitForSelector(".settingsPageNavigation");
  await window.locator(".settingsPageNavigation button").filter({ hasText: /devices|设备/i }).click();
  await window.locator(".clientLogout").click();
  await until(async () => (await window.evaluate(() => window.sparkclawDesktop.localConnection())).state === "locked", "Settings lock");
  await window.waitForSelector(".desktopLogin");
  await until(() => application.evaluate(() => globalThis.__lockAgent.runtime.child === null && globalThis.__lockAgent.runtime.state === "stopped" && globalThis.__lockAgent.lease === null), "lock cleanup", 15000);
  await until(() => !execFileSync("ps", ["-axo", "pgid="], { encoding: "utf8" }).split("\n").some(line => Number(line.trim()) === controller), "owned Controller group exit", 8000);
  await assert.rejects(fs.lstat(runtimeDirectory), { code: "ENOENT" });
  assert.equal(await application.evaluate(({ webContents }) => webContents.getAllWebContents().filter(page => page.getURL() === "https://controller.fixture.test/").length), 0);
  assert.equal(await application.evaluate((_electron, ref) => globalThis.__lockAgent.registry.snapshot().some(page => page.page_ref === ref && page.role === "personal"), personal), true);
  assert.equal(await window.evaluate(() => window.sparkclawDesktop.state().then(() => false, error => String(error.message).includes("Desktop session is locked"))), true);
  assert.equal((await window.evaluate(() => window.sparkclawDesktop.retryLocalConnection())).state, "locked");
  await rejectOldApproval();
  const denied = await application.evaluate(async (_electron, { request, authority }) => {
    const before = globalThis.__lockTrace.native_calls.length;
    const response = await globalThis.__lockAgent.handleReverse(request, authority);
    return { status: response.status, calls_added: globalThis.__lockTrace.native_calls.length - before };
  }, { request: originalExecute, authority: originalAuthority });
  assert.deepEqual(denied, { status: 503, calls_added: 0 }); evidence.locked_old_authority = denied;
  evidence.locked_trace = await application.evaluate(() => globalThis.__lockTrace);
  assert.equal(evidence.locked_trace.native_calls.filter(row => row.operation === "page.fill").length, 0);
  evidence.checks.original_Settings_lock_retires_execution_and_rejects_retry_and_old_authority = true;
  await application.close(); application = undefined;
  await launch(true);
  assert.deepEqual(await identity(), evidence.before_identity);
  assert.equal((await window.evaluate(() => window.sparkclawDesktop.retryLocalConnection())).state, "locked");
  evidence.locked_restart_trace = await application.evaluate(() => globalThis.__lockTrace);
  assert.deepEqual(evidence.locked_restart_trace.native_calls, []);
  evidence.checks.same_profile_restart_stays_locked_without_automatic_native_calls = true;
  await unlockWithOriginalCredential();
  await window.waitForSelector(".localWorkbench");
  evidence.connection_after = await window.evaluate(() => window.sparkclawDesktop.localConnection());
  verifyQualificationAuthorization(evidence.connection_after);
  assert.deepEqual(evidence.connection_after.backend, evidence.connection_before.backend);
  assert.equal(evidence.connection_after.client_id, evidence.connection_before.client_id);
  await until(async () => (await window.evaluate(() => window.sparkclawDesktop.state())).browser_host.state === "connected", "Controller after unlock");
  await unchangedUnknowns();
  assert.deepEqual(await identity(), evidence.before_identity);
  assert.notEqual(await application.evaluate(() => globalThis.__lockAgent.session.session_id), evidence.execution.old_session);
  // Logout invalidates browser authority; it does not cancel a remote execution.
  // Observe its original approval, then explicitly decide it once through the
  // original API. Even an accepted decision must not enter the retired lease.
  evidence.original_after_unlock = await readTask(evidence.task);
  evidence.old_approval_decision = await window.evaluate(({ task, approval }) => window.sparkclawClientStore
    .decideApproval(task.request_id, approval.approval_id, approval.digest, "approve")
    .then(value => ({ resolved: value.resolved }), error => ({ error: String(error.message) })),
  { task: evidence.task, approval: evidence.original_approval });
  await until(async () => {
    evidence.interrupted_state = await readTask(evidence.task);
    return ["failed", "canceled", "unknown", "delivered"].includes(evidence.interrupted_state.tasks.find(row => row.request_id === evidence.task.request_id)?.status);
  }, "original execution terminates");
  const interrupted = evidence.interrupted_state.tasks.find(row => row.request_id === evidence.task.request_id);
  assert.equal(interrupted.approvals?.some(row => row.state === "pending" && row.actionable) || false, false);
  assert.deepEqual(await application.evaluate(() => globalThis.__lockTrace.native_calls), []);
  evidence.checks.same_credential_unlock_and_explicit_old_decision_cannot_enter_retired_lease = true;
  evidence.recovery_task = await submit("Bob");
  const approval = await pendingApproval(evidence.recovery_task);
  evidence.old_authority_probes = await application.evaluate(async (_electron, { request, authority }) => {
    const agent = globalThis.__lockAgent, trace = globalThis.__lockTrace, before = trace.native_calls.length;
    const oldAuthority = await agent.handleReverse(request, authority), oldBinding = await agent.handleReverse(request, agent.session);
    return { old_authority_status: oldAuthority.status, old_binding_status: oldBinding.status, calls_added: trace.native_calls.length - before };
  }, { request: originalExecute, authority: originalAuthority });
  assert.deepEqual(evidence.old_authority_probes, { old_authority_status: 503, old_binding_status: 503, calls_added: 0 });
  evidence.old_lease_probe = await application.evaluate(async ({ app }, original) => {
    const require = process.getBuiltinModule("node:module").createRequire(app.getAppPath() + "/package.json");
    const { controllerDigest, iscpHostID, iscpAuthorizationDigest } = require(app.getAppPath() + "/src/browser/controller-wire.mjs");
    const agent = globalThis.__lockAgent, session = agent.session, trace = globalThis.__lockTrace, request = structuredClone(original), before = trace.native_calls.length;
    request.body.command_id = require("node:crypto").randomUUID(); request.body.operation = "page.snapshot"; request.body.arguments = {};
    Object.assign(request.body.binding, { host_id: iscpHostID(agent.scope, session.session_id), runtime_generation: "iscp_" + session.session_id,
      connection_epoch: session.session_id, authorization_digest: iscpAuthorizationDigest(session.session_id, session.authorization_revision) });
    request.body.digest = controllerDigest(request.operation, request.body);
    const reply = await agent.handleReverse(request, session);
    return { outcome: reply.body?.outcome, error_code: reply.body?.error_code, calls_added: trace.native_calls.length - before,
      live_incarnation: agent.runtime.incarnation, live_task: agent.lease?.binding.local_task_id };
  }, originalExecute);
  assert.equal(evidence.old_lease_probe.outcome, "not_dispatched"); assert.equal(evidence.old_lease_probe.error_code, "browser_session_stale");
  assert.equal(evidence.old_lease_probe.calls_added, 0); assert.equal(evidence.old_lease_probe.live_task, evidence.recovery_task.local_task_id);
  assert.notEqual(evidence.old_lease_probe.live_incarnation, evidence.execution.incarnation);
  assert.equal(await application.evaluate(() => globalThis.__lockTrace.native_calls.filter(row => row.operation === "page.fill").length), 0);
  evidence.checks.old_authority_binding_and_lease_cannot_enter_new_incarnation = true;
  await window.evaluate(({ task, approval }) => window.sparkclawClientStore.decideApproval(task.request_id, approval.approval_id, approval.digest, "approve"), { task: evidence.recovery_task, approval });
  await until(async () => {
    evidence.recovery_state = await readTask(evidence.recovery_task);
    return ["delivered", "failed", "unknown", "canceled"].includes(evidence.recovery_state.tasks.find(row => row.request_id === evidence.recovery_task.request_id)?.status);
  }, "separately approved recovery delivery");
  assert.equal(evidence.recovery_state.tasks.find(row => row.request_id === evidence.recovery_task.request_id).status, "delivered");
  evidence.recovery_effects = await application.evaluate(async ({ webContents }) => Promise.all(webContents.getAllWebContents()
    .filter(page => page.getURL() === "https://controller.fixture.test/")
    .map(async page => ({ ...await page.executeJavaScript('({value:document.querySelector("input").value,inputs:window.fixtureInputs})'), attached: page.debugger.isAttached(), sandbox: page.getLastWebPreferences().sandbox }))));
  assert.deepEqual(evidence.recovery_effects, [{ value: "Bob", inputs: 1, attached: false, sandbox: true }]);
  evidence.final_trace = await application.evaluate(() => globalThis.__lockTrace);
  assert.equal(evidence.final_trace.native_calls.filter(row => row.operation === "page.fill").length, 1); assert.equal(evidence.final_trace.business_http, 0);
  assert.equal(evidence.final_trace.acquisitions.length, 1);
  evidence.checks.only_separately_approved_recovery_fills_once_and_retains_sandboxed_view = true;
  await unchangedUnknowns();
  await application.close(); application = undefined;
  await launch();
  evidence.after_restart_identity = await identity(); assert.deepEqual(evidence.after_restart_identity, evidence.before_identity);
  evidence.unknown_after_restart = await unchangedUnknowns();
  evidence.restarted_original = await readTask(evidence.task);
  const restarted = evidence.restarted_original.tasks.filter(row => row.request_id === evidence.task.request_id);
  assert.equal(restarted.length, 1); assert.equal(restarted[0].status, interrupted.status);
  assert.equal(restarted[0].approvals?.some(row => row.state === "pending" && row.actionable) || false, false);
  evidence.restart_trace = await application.evaluate(() => globalThis.__lockTrace);
  assert.deepEqual(evidence.restart_trace.native_calls, []); assert.equal(evidence.restart_trace.business_http, 0);
  evidence.checks.same_profile_restart_retains_identity_retired_approval_and_all_unknowns_without_replay = true;
  const cookies = await application.evaluate(({ session }) => session.fromPartition("persist:sparkclaw-browser-default").cookies.get({ name: "controller-lock-marker" }));
  assert.ok(cookies.some(cookie => cookie.value === marker));
  const file = await window.evaluate(id => window.sparkclawClientStore.readFile(id), retained.file);
  assert.equal(hash(Buffer.from(Object.values(file.bytes))), hash(Buffer.from("Preserve preapproval attachment.\n")));
  const draft = await window.evaluate(id => window.sparkclawClientStore.draft(id), retained.conversation);
  assert.equal(draft.content, "Keep this unsent draft."); assert.ok(draft.local_file_ids.includes(retained.file));
  assert.equal(await window.evaluate(() => fetch(window.sparkclawDesktop.gatewayBase + "/api/owner").then(response => response.status)), 200);
  evidence.checks.personal_cookie_attachment_draft_original_IPC_and_safe_ISCP_read_survive = true;
  await application.close(); application = undefined;
  evidence.original_journal = []; evidence.recovery_journal = [];
  for (const name of (await fs.readdir(evidence.execution.journal_root)).filter(name => name.endsWith(".json"))) {
    const row = JSON.parse(await fs.readFile(path.join(evidence.execution.journal_root, name), "utf8"));
    if (row.local_task_id === evidence.task.local_task_id) { assert.equal(row.state, "completed"); evidence.original_journal.push(row); }
    if (row.local_task_id === evidence.recovery_task.local_task_id) { assert.equal(row.state, "completed"); evidence.recovery_journal.push(row); }
  }
  assert.ok(evidence.original_journal.length >= 2); assert.ok(evidence.recovery_journal.length >= 3);
  evidence.passed = true;
} catch (error) { evidence.error = String(error?.stack || error).replaceAll(credential, "[redacted]"); }
finally {
  if (application) {
    evidence.failure_trace = await application.evaluate(() => globalThis.__lockTrace).catch(() => null);
    const connection = await window?.evaluate(() => window.sparkclawDesktop.localConnection()).catch(() => null);
    if (connection?.state === "locked") {
      // Preserve the failed observation while restoring this same test identity
      // through the original form; never patch secure storage or resubmit tasks.
      try {
        await unlockWithOriginalCredential();
        evidence.cleanup_unlock = (await window.evaluate(() => window.sparkclawDesktop.localConnection())).state;
      } catch (error) { evidence.cleanup_unlock_error = String(error?.message || error).replaceAll(credential, "[redacted]"); }
    }
    await application.close().catch(error => { evidence.quit_error = String(error); evidence.passed = false; });
  }
  const children = execFileSync("ps", ["-axo", "command="], { encoding: "utf8" }).split("\n").filter(line => line.includes(resources + "/browser-controller/"));
  evidence.checks.no_owned_controller_children = children.length === 0;
  if (children.length) evidence.passed = false;
  await fs.writeFile(output, JSON.stringify(evidence, null, 2) + "\n", { mode: 0o600, flag: "wx" });
  console.log(JSON.stringify({ passed: evidence.passed, output, checks: evidence.checks, error: evidence.error }));
  if (!evidence.passed) process.exitCode = 1;
}
