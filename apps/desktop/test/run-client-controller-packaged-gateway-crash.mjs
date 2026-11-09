// Opt-in actual-package proof: restart only the labelled disposable Gateway
// after an applied fill, then release its real old reply during a new task.
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
const metadataPath = process.env.SPARKCLAW_CONTROLLER_GATEWAY_METADATA;
assert.ok(path.isAbsolute(metadataPath || ""));
const metadata = JSON.parse(await fs.readFile(metadataPath, "utf8"));
assert.match(metadata.id, /^[a-f0-9]{64}$/u); assert.match(metadata.lab_id, /^[a-f0-9-]{36}$/u);
const resources = path.resolve(path.dirname(executablePath), process.platform === "darwin" ? "../Resources" : "resources");
const { _electron } = await import(pathToFileURL(path.join(resources, "browser-controller/node_modules/playwright-core/index.mjs")));
const output = path.join(directory, `evidence/controller-packaged-gateway-crash-${Date.now()}.json`);
const evidence = { schema_version: 1, passed: false, checks: {},
  scope: "actual_package_owned_node_Gateway_SIGKILL_after_fill_before_local_terminal_with_late_reply", local_probes_are_not_wire_calls: true };
const hash = bytes => crypto.createHash("sha256").update(bytes).digest("hex");
const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
let application, window, originalAcquire, originalFill, originalAuthority;
async function until(check, label, timeout = 90000) {
  const end = performance.now() + timeout;
  while (performance.now() < end) { if (await check()) return; await pause(100); }
  throw Error(`Packaged Gateway crash timeout: ${label}`);
}
async function launch(inject) {
  application = await _electron.launch({ executablePath, timeout: 45000, chromiumSandbox: true,
    env: qualificationLaunchEnvironment(directory) });
  application.process().stderr.on("data", bytes => process.stderr.write(bytes));
  await application.evaluate(async ({ app, net, session, webContents }, inject) => {
    const require = process.getBuiltinModule("node:module").createRequire(app.getAppPath() + "/package.json");
    const assert = require("node:assert/strict"), fs = require("node:fs/promises"), path = require("node:path");
    const { BrowserControllerForwarder } = require(app.getAppPath() + "/src/browser/controller-forwarder.mjs");
    const { ClientControllerRuntime } = require(app.getAppPath() + "/src/browser/controller-runtime.mjs");
    const trace = globalThis.__gatewayCrash = { native_calls: [], acquisitions: [], business_http: 0, held: false };
    const snapshot = BrowserControllerForwarder.prototype.snapshot;
    BrowserControllerForwarder.prototype.snapshot = function(...args) { globalThis.__crashAgent = this; return snapshot.apply(this, args); };
    const request = ClientControllerRuntime.prototype.request;
    ClientControllerRuntime.prototype.request = async function(action, body, options) {
      trace.native_calls.push({ action, operation: body.operation });
      const response = await request.call(this, action, body, options);
      if (action === "acquire") trace.acquisitions.push({ incarnation: this.incarnation, status: response.status });
      if (inject && action === "execute" && body.operation === "page.fill" && !trace.held) {
        const page = webContents.getAllWebContents().find(page => page.getURL() === "https://controller.fixture.test/" && page.debugger.isAttached());
        assert.ok(page);
        trace.effect = await page.executeJavaScript('({value:document.querySelector("input").value,inputs:window.fixtureInputs})');
        assert.deepEqual(trace.effect, { value: "Alice", inputs: 1 });
        trace.journal_path = path.join(globalThis.__crashAgent.journal.root, trace.command.command_id + ".json");
        trace.local_dispatched = JSON.parse(await fs.readFile(trace.journal_path, "utf8"));
        assert.equal(trace.local_dispatched.state, "dispatched");
        trace.controller_pid = this.child.pid; trace.runtime_directory = this.directory;
        trace.incarnation = this.incarnation; trace.old_session = globalThis.__crashAgent.session.session_id;
        trace.held = true;
        await new Promise(resolve => { globalThis.__releaseCrashReply = resolve; });
        trace.old_native_reply_released = true;
      }
      return response;
    };
    const reverse = BrowserControllerForwarder.prototype.handleReverse;
    BrowserControllerForwarder.prototype.handleReverse = async function(request, authority) {
      globalThis.__crashAgent = this;
      if (request.operation === "browser.controller.acquire" && !globalThis.__crashAcquire) globalThis.__crashAcquire = structuredClone(request);
      const originalFill = request.body?.operation === "page.fill" && !trace.command;
      if (originalFill) {
        trace.command = { command_id: request.body.command_id, digest: request.body.digest, binding: request.body.binding };
        globalThis.__crashFill = structuredClone(request); globalThis.__crashAuthority = structuredClone(authority);
      }
      const reply = await reverse.call(this, request, authority);
      if (originalFill) trace.old_reverse_reply = { outcome: reply.body?.outcome, error_code: reply.body?.error_code };
      return reply;
    };
    const fetch = net.fetch.bind(net);
    net.fetch = (...args) => { if (/^https?:/u.test(String(args[0]?.url || args[0]))) { trace.business_http++; throw Error("Business HTTP forbidden"); } return fetch(...args); };
    globalThis.fetch = () => { trace.business_http++; throw Error("Business HTTP forbidden"); };
    session.fromPartition("persist:sparkclaw-workbench-default").webRequest.onBeforeRequest({ urls: ["http://*/*", "https://*/*", "ws://*/*", "wss://*/*"] }, (_request, callback) => { trace.business_http++; callback({ cancel: true }); });
    await session.fromPartition("persist:sparkclaw-browser-default").protocol.handle("https", request => {
      assert.equal(new URL(request.url).origin, "https://controller.fixture.test");
      return new Response('<!doctype html><title>Gateway crash fixture</title><label>Display name<input aria-label="Display name"></label><script>window.fixtureInputs=0;document.querySelector("input").oninput=()=>window.fixtureInputs++</script>', { headers: { "content-type": "text/html" } });
    });
  }, inject);
  await until(() => (window = application.windows().find(page => page.url() === "sparkclaw-app://workbench/index.html")), "workbench");
  await window.waitForSelector(".localWorkbench");
  await until(async () => (await window.evaluate(() => window.sparkclawDesktop.localConnection())).state === "connected", "connection");
  verifyQualificationAuthorization(await window.evaluate(() => window.sparkclawDesktop.localConnection()));
  await until(async () => (await window.evaluate(() => window.sparkclawDesktop.state())).browser_host.state === "connected", "Controller readiness");
  await synchronizeQualificationFences(application);
  const runtime = await application.evaluate(({ app, safeStorage }) => ({ packaged: app.isPackaged, executable: process.execPath,
    profile: app.getPath("userData"), versions: process.versions, secure_storage: safeStorage.isEncryptionAvailable() }));
  assert.ok(runtime.packaged && runtime.secure_storage); assert.equal(runtime.executable, executablePath);
  assert.equal(runtime.profile, path.join(directory, "controller-native-profile"));
  evidence.runtime ||= runtime; assert.equal(runtime.profile, evidence.runtime.profile);
}
async function freshAcquireDenied() {
  return application.evaluate(async ({ app }, original) => {
    const require = process.getBuiltinModule("node:module").createRequire(app.getAppPath() + "/package.json");
    const { controllerDigest, iscpHostID, iscpAuthorizationDigest } = require(app.getAppPath() + "/src/browser/controller-wire.mjs");
    const agent = globalThis.__crashAgent, session = agent.session, trace = globalThis.__gatewayCrash;
    const request = structuredClone(original), before = trace.native_calls.length;
    request.body.command_id = require("node:crypto").randomUUID();
    Object.assign(request.body.binding, { host_id: iscpHostID(agent.scope, session.session_id), runtime_generation: "iscp_" + session.session_id,
      connection_epoch: session.session_id, authorization_digest: iscpAuthorizationDigest(session.session_id, session.authorization_revision) });
    request.body.digest = controllerDigest(request.operation, request.body);
    const reply = await agent.handleReverse(request, session);
    return { outcome: reply.body?.outcome, error_code: reply.body?.error_code, calls_added: trace.native_calls.length - before };
  }, originalAcquire);
}
async function verifyUnknown() {
  await synchronizeQualificationFences(application);
  const rows = (await window.evaluate(() => window.sparkclawDesktop.state())).browser_host.unknown_writes;
  for (const prior of evidence.unknown_before) assert.deepEqual(rows.find(row => row.command_id === prior.command_id), prior);
  assert.ok(rows.some(row => row.command_id === evidence.trace.command.command_id && row.digest === evidence.trace.command.digest));
  return rows;
}
function docker(...args) { return execFileSync("docker", args, { encoding: "utf8", timeout: 25000 }); }
function gatewayState() {
  const rows = JSON.parse(docker("inspect", metadata.id)); assert.equal(rows.length, 1);
  const value = rows[0]; assert.equal(value.Id, metadata.id); assert.equal(value.Config.Labels?.["io.sparkclaw.node-lab"], metadata.lab_id);
  assert.equal(value.State.Paused, false); return value;
}
function gatewayFingerprint(value) { return hash(JSON.stringify({ Id: value.Id, Image: value.Image, Config: value.Config, HostConfig: value.HostConfig, Mounts: [...value.Mounts].sort((a, b) => a.Destination.localeCompare(b.Destination)) })); }
try {
  const configBytes = await fs.readFile(path.join(directory, "gateway-config.json")), catalogBytes = await fs.readFile(path.join(directory, "model.profiles.json"));
  const config = JSON.parse(configBytes), catalog = JSON.parse(catalogBytes);
  evidence.provider = { mock: catalog.profiles[config.model.capacity_profile].mock, config_sha256: hash(configBytes), catalog_sha256: hash(catalogBytes) };
  assert.equal(evidence.provider.mock, false);
  evidence.asar_sha256 = hash(await fs.readFile(path.join(resources, "app.asar")));
  await launch(true);
  evidence.unknown_before = await synchronizeQualificationFences(application);
  const marker = crypto.randomUUID();
  await application.evaluate(async ({ session }, marker) => {
    const browser = session.fromPartition("persist:sparkclaw-browser-default");
    await browser.cookies.set({ url: "https://controller.fixture.test/", name: "controller-gateway-crash-marker", value: marker, expirationDate: Date.now() / 1000 + 86400 });
    await browser.cookies.flushStore();
  }, marker);
  const retained = await window.evaluate(async () => {
    const store = window.sparkclawClientStore, conversation = await store.create("Gateway crash retained draft");
    const draft = await store.draft(conversation.id);
    const file = await store.saveFile(conversation.id, "crash-retained.txt", new TextEncoder().encode("Retain this attachment through Gateway SIGKILL.\n"));
    await store.saveDraft(conversation.id, "Retain this unsent draft.", [file.id], draft.revision, draft.scope_key);
    return { conversation: conversation.id, file: file.id };
  });
  const conversation = await window.evaluate(title => window.sparkclawClientStore.create(title), `Gateway crash ${Date.now()}`);
  await window.evaluate(id => window.sparkclawDesktop.selectConversation(id), conversation.id);
  const task = await window.evaluate(async id => { const s = window.sparkclawClientStore, t = await s.enqueue(id, "在 https://controller.fixture.test/ 页面 Display name 字段填写 Alice。", []); await s.submit(t.request_id); return t; }, conversation.id);
  evidence.task = { request_id: task.request_id, local_task_id: task.id, conversation_id: conversation.id };
  let approval;
  await until(async () => {
    await window.evaluate(id => window.sparkclawClientStore.reconcile(id), task.request_id);
    const state = await window.evaluate(id => window.sparkclawClientStore.read(id), conversation.id);
    approval = state.tasks.find(row => row.request_id === task.request_id)?.approvals?.find(row => row.state === "pending" && row.actionable);
    return Boolean(approval);
  }, "original approval");
  assert.equal(approval.tool, "browser.type");
  await window.evaluate(({ id, a }) => window.sparkclawClientStore.decideApproval(id, a.approval_id, a.digest, "approve"), { id: task.request_id, a: approval });
  await until(() => application.evaluate(() => globalThis.__gatewayCrash.held), "applied fill with dispatched journal");
  evidence.trace = await application.evaluate(() => globalThis.__gatewayCrash);
  originalAcquire = await application.evaluate(() => globalThis.__crashAcquire);
  originalFill = await application.evaluate(() => globalThis.__crashFill);
  originalAuthority = await application.evaluate(() => globalThis.__crashAuthority);
  assert.equal(evidence.trace.native_calls.filter(row => row.operation === "page.fill").length, 1);
  assert.equal(evidence.trace.business_http, 0);
  evidence.checks.fill_applied_once_with_real_native_reply_held_and_journal_dispatched = true;
  const immutable = async () => application.evaluate(({ app }, requestID) => {
    const db = new (process.getBuiltinModule("node:sqlite").DatabaseSync)(process.getBuiltinModule("node:path").join(app.getPath("userData"), "workbench/client.sqlite"), { readOnly: true });
    try { return { installation: db.prepare("SELECT value FROM metadata WHERE key='installation_id'").get().value,
      tasks: db.prepare("SELECT id,conversation_id,request_id,input_digest,context_json,created_at FROM tasks WHERE request_id=?").all(requestID) }; }
    finally { db.close(); }
  }, task.request_id);
  evidence.before_crash_identity = await immutable();
  assert.equal(evidence.before_crash_identity.tasks.length, 1);
  const controller = evidence.trace.controller_pid, runtimeDirectory = evidence.trace.runtime_directory;
  const rows = execFileSync("ps", ["-axo", "pid,ppid,pgid,command"], { encoding: "utf8" }).split("\n")
    .map(line => line.match(/^\s*(\d+)\s+(\d+)\s+(\d+)\s+(.*)$/u)).filter(Boolean);
  const ownedController = rows.find(row => Number(row[1]) === controller);
  assert.ok(ownedController && Number(ownedController[2]) === application.process().pid && Number(ownedController[3]) === controller &&
    ownedController[4].includes(resources + "/browser-controller/src/client-main.mjs"));
  assert.equal(await fs.realpath(path.dirname(runtimeDirectory)), path.join(await fs.realpath("/tmp"), `sparkx-${process.getuid()}`));
  assert.match(path.basename(runtimeDirectory), /^c-[A-Za-z0-9]+$/u);
  const owned = await fs.lstat(runtimeDirectory); assert.equal(owned.uid, process.getuid()); assert.equal(owned.mode & 0o077, 0); assert.equal(owned.isSymbolicLink(), false);
  const personal = (await window.evaluate(() => window.sparkclawDesktop.createPersonal())).page_ref;
  const before = gatewayState(); assert.equal(before.State.Running, true);
  evidence.gateway = { id: before.Id, started_before: before.State.StartedAt, immutable_sha256: gatewayFingerprint(before) };
  docker("kill", "--signal=KILL", metadata.id);
  const dead = gatewayState(); assert.equal(dead.State.Running, false); assert.equal(dead.State.ExitCode, 137);
  const started = performance.now();
  await until(async () => (await window.evaluate(() => window.sparkclawDesktop.localConnection())).state !== "connected", "Gateway loss projection", 45000);
  await until(() => application.evaluate(() => globalThis.__crashAgent.runtime.child === null && globalThis.__crashAgent.runtime.state === "stopped" && globalThis.__crashAgent.lease === null), "authority loss cleanup", 15000);
  await until(() => !execFileSync("ps", ["-axo", "pgid="], { encoding: "utf8" }).split("\n").some(line => Number(line.trim()) === controller), "owned Controller group exit", 8000);
  await assert.rejects(fs.lstat(runtimeDirectory), { code: "ENOENT" });
  evidence.gateway.loss_and_cleanup_observed_ms = Math.round(performance.now() - started);
  assert.equal(await application.evaluate(({ webContents }) => webContents.getAllWebContents().filter(page => page.getURL() === "https://controller.fixture.test/").length), 0);
  evidence.gateway.protected_state_refused = await window.evaluate(() => window.sparkclawDesktop.state()
    .then(() => false, error => String(error.message).includes("Desktop session is locked")));
  assert.equal(evidence.gateway.protected_state_refused, true);
  assert.equal(await application.evaluate((_electron, ref) => globalThis.__crashAgent.registry.snapshot()
    .some(page => page.page_ref === ref && page.role === "personal"), personal), true);
  assert.deepEqual(JSON.parse(await fs.readFile(evidence.trace.journal_path, "utf8")), evidence.trace.local_dispatched);
  evidence.checks.Gateway_death_retires_owned_execution_and_keeps_dispatched_fact = true;
  docker("start", metadata.id);
  const after = gatewayState(); assert.equal(after.State.Running, true); assert.equal(gatewayFingerprint(after), evidence.gateway.immutable_sha256);
  assert.notEqual(after.State.StartedAt, before.State.StartedAt); evidence.gateway.started_after = after.State.StartedAt;
  await until(() => application.evaluate(old => globalThis.__crashAgent.auth.status.state === "connected" && globalThis.__crashAgent.state === "connected" && globalThis.__crashAgent.session?.session_id !== old, evidence.trace.old_session), "new session after Gateway restart", 90000);
  verifyQualificationAuthorization(await window.evaluate(() => window.sparkclawDesktop.localConnection()));
  evidence.gateway.new_session = await application.evaluate(() => globalThis.__crashAgent.session.session_id);
  evidence.before_restart_unknown = await verifyUnknown();
  evidence.before_restart_probe = await freshAcquireDenied();
  assert.deepEqual(evidence.before_restart_probe, { outcome: "not_dispatched", error_code: "browser_write_outcome_unknown", calls_added: 0 });
  await until(async () => {
    await window.evaluate(id => window.sparkclawClientStore.reconcile(id), task.request_id);
    const state = await window.evaluate(id => window.sparkclawClientStore.read(id), conversation.id);
    return state.tasks.find(row => row.request_id === task.request_id)?.status === "unknown";
  }, "original execution unknown after Gateway restart");
  evidence.checks.same_Gateway_identity_recovers_new_authority_without_replaying_old_task = true;
  const recoveryConversation = await window.evaluate(() => window.sparkclawClientStore.create("Gateway crash fresh recovery task"));
  const recovery = await window.evaluate(async id => {
    await window.sparkclawDesktop.selectConversation(id);
    const s = window.sparkclawClientStore, t = await s.enqueue(id, "在 https://controller.fixture.test/ 页面 Display name 字段填写 Bob。", []);
    await s.submit(t.request_id); return t;
  }, recoveryConversation.id);
  evidence.recovery_task = { request_id: recovery.request_id, local_task_id: recovery.id, conversation_id: recoveryConversation.id };
  let recoveryApproval;
  await until(async () => {
    await window.evaluate(id => window.sparkclawClientStore.reconcile(id), recovery.request_id);
    const state = await window.evaluate(id => window.sparkclawClientStore.read(id), recoveryConversation.id);
    recoveryApproval = state.tasks.find(row => row.request_id === recovery.request_id)?.approvals?.find(row => row.state === "pending" && row.actionable);
    return Boolean(recoveryApproval);
  }, "separately submitted recovery approval");
  assert.equal(recoveryApproval.tool, "browser.type");
  evidence.old_lease_probe = await application.evaluate(async ({ app }, original) => {
    const require = process.getBuiltinModule("node:module").createRequire(app.getAppPath() + "/package.json");
    const { controllerDigest, iscpHostID, iscpAuthorizationDigest } = require(app.getAppPath() + "/src/browser/controller-wire.mjs");
    const agent = globalThis.__crashAgent, session = agent.session, trace = globalThis.__gatewayCrash;
    const request = structuredClone(original), before = trace.native_calls.length;
    request.body.command_id = require("node:crypto").randomUUID(); request.body.operation = "page.snapshot"; request.body.arguments = {};
    Object.assign(request.body.binding, { host_id: iscpHostID(agent.scope, session.session_id), runtime_generation: "iscp_" + session.session_id,
      connection_epoch: session.session_id, authorization_digest: iscpAuthorizationDigest(session.session_id, session.authorization_revision) });
    request.body.digest = controllerDigest(request.operation, request.body);
    const reply = await agent.handleReverse(request, session);
    return { outcome: reply.body?.outcome, error_code: reply.body?.error_code, calls_added: trace.native_calls.length - before,
      live_incarnation: agent.runtime.incarnation, live_task: agent.lease?.binding.local_task_id };
  }, originalFill);
  assert.equal(evidence.old_lease_probe.outcome, "not_dispatched"); assert.equal(evidence.old_lease_probe.error_code, "browser_session_stale");
  assert.equal(evidence.old_lease_probe.calls_added, 0); assert.equal(evidence.old_lease_probe.live_task, recovery.id);
  assert.notEqual(evidence.old_lease_probe.live_incarnation, evidence.trace.incarnation);
  evidence.old_authority_probes = await application.evaluate(async (_electron, { request, authority }) => {
    const agent = globalThis.__crashAgent, trace = globalThis.__gatewayCrash, before = trace.native_calls.length;
    const oldAuthority = await agent.handleReverse(request, authority), oldBinding = await agent.handleReverse(request, agent.session);
    return { old_authority_status: oldAuthority.status, old_binding_status: oldBinding.status, calls_added: trace.native_calls.length - before };
  }, { request: originalFill, authority: originalAuthority });
  assert.deepEqual(evidence.old_authority_probes, { old_authority_status: 503, old_binding_status: 503, calls_added: 0 });
  evidence.replacement_before_late_reply = await application.evaluate(() => ({ pid: globalThis.__crashAgent.runtime.child.pid,
    incarnation: globalThis.__crashAgent.runtime.incarnation, task: globalThis.__crashAgent.lease.binding.local_task_id }));
  assert.equal(evidence.replacement_before_late_reply.task, recovery.id);
  await application.evaluate(() => globalThis.__releaseCrashReply?.());
  await until(() => application.evaluate(() => Boolean(globalThis.__gatewayCrash.old_reverse_reply)), "late old native reply settles");
  evidence.late_reply = await application.evaluate(() => globalThis.__gatewayCrash.old_reverse_reply);
  assert.equal(evidence.late_reply.outcome, "unknown");
  assert.equal(JSON.parse(await fs.readFile(evidence.trace.journal_path, "utf8")).state, "unknown");
  assert.deepEqual(await application.evaluate(() => ({ pid: globalThis.__crashAgent.runtime.child.pid,
    incarnation: globalThis.__crashAgent.runtime.incarnation, task: globalThis.__crashAgent.lease.binding.local_task_id })), evidence.replacement_before_late_reply);
  evidence.checks.late_real_old_reply_cannot_retire_new_incarnation_or_revive_old_authority = true;
  await window.evaluate(({ id, a }) => window.sparkclawClientStore.decideApproval(id, a.approval_id, a.digest, "approve"), { id: recovery.request_id, a: recoveryApproval });
  await until(async () => {
    await window.evaluate(id => window.sparkclawClientStore.reconcile(id), recovery.request_id);
    const state = await window.evaluate(id => window.sparkclawClientStore.read(id), recoveryConversation.id);
    evidence.recovery_state = state;
    return ["delivered", "failed", "unknown", "canceled"].includes(state.tasks.find(row => row.request_id === recovery.request_id)?.status);
  }, "separately approved recovery task delivery");
  assert.equal(evidence.recovery_state.tasks.find(row => row.request_id === recovery.request_id).status, "delivered");
  evidence.recovery_effects = await application.evaluate(async ({ webContents }) => Promise.all(webContents.getAllWebContents()
    .filter(page => page.getURL() === "https://controller.fixture.test/")
    .map(async page => ({ ...await page.executeJavaScript('({value:document.querySelector("input").value,inputs:window.fixtureInputs})'), attached: page.debugger.isAttached(), sandbox: page.getLastWebPreferences().sandbox }))));
  assert.deepEqual(evidence.recovery_effects, [{ value: "Bob", inputs: 1, attached: false, sandbox: true }]);
  evidence.recovery_trace = await application.evaluate(() => globalThis.__gatewayCrash);
  assert.equal(evidence.recovery_trace.native_calls.filter(row => row.operation === "page.fill").length, 2);
  assert.equal(evidence.recovery_trace.business_http, 0);
  assert.equal(evidence.recovery_trace.acquisitions.length, 2);
  assert.notEqual(evidence.recovery_trace.acquisitions[1].incarnation, evidence.trace.incarnation);
  evidence.final_unknown_writes = await verifyUnknown();
  evidence.checks.old_lease_denied_while_new_incarnation_completes_separately_approved_task = true;
  await application.close(); application = undefined;
  evidence.cold_unknown = JSON.parse(await fs.readFile(evidence.trace.journal_path, "utf8"));
  assert.equal(evidence.cold_unknown.state, "unknown");
  await launch(false);
  evidence.after_restart_identity = await immutable(); assert.deepEqual(evidence.after_restart_identity, evidence.before_crash_identity);
  evidence.restarted_unknown = await verifyUnknown(); evidence.after_restart_probe = await freshAcquireDenied();
  assert.deepEqual(evidence.after_restart_probe, evidence.before_restart_probe);
  await window.evaluate(id => window.sparkclawClientStore.reconcile(id), task.request_id);
  evidence.recovered_task = await window.evaluate(id => window.sparkclawClientStore.read(id), conversation.id);
  assert.equal(evidence.recovered_task.tasks.find(row => row.request_id === task.request_id)?.status, "unknown");
  assert.equal(evidence.recovered_task.tasks.filter(row => row.request_id === task.request_id).length, 1);
  assert.equal(evidence.recovered_task.messages.filter(row => row.role === "assistant").length, 0);
  evidence.restart_trace = await application.evaluate(() => globalThis.__gatewayCrash);
  assert.deepEqual(evidence.restart_trace.native_calls, []); assert.equal(evidence.restart_trace.business_http, 0);
  evidence.checks.original_task_identity_and_unknown_survive_without_native_replay = true;
  const cookies = await application.evaluate(({ session }) => session.fromPartition("persist:sparkclaw-browser-default").cookies.get({ name: "controller-gateway-crash-marker" }));
  assert.ok(cookies.some(cookie => cookie.value === marker));
  const file = await window.evaluate(id => window.sparkclawClientStore.readFile(id), retained.file);
  assert.equal(hash(Buffer.from(Object.values(file.bytes))), hash(Buffer.from("Retain this attachment through Gateway SIGKILL.\n")));
  const draft = await window.evaluate(id => window.sparkclawClientStore.draft(id), retained.conversation);
  assert.equal(draft.content, "Retain this unsent draft."); assert.ok(draft.local_file_ids.includes(retained.file));
  assert.equal(await window.evaluate(() => fetch(window.sparkclawDesktop.gatewayBase + "/api/owner").then(response => response.status)), 200);
  evidence.checks.personal_cookie_attachment_draft_original_IPC_and_safe_ISCP_read_survive = true;
  await application.close(); application = undefined;
  evidence.final_local = JSON.parse(await fs.readFile(evidence.trace.journal_path, "utf8"));
  assert.equal(evidence.final_local.state, "unknown");
  for (const key of ["command_id", "digest", "installation_id", "local_conversation_id", "local_task_id", "owner_id", "client_id"]) assert.equal(evidence.final_local[key], evidence.cold_unknown[key]);
  assert.deepEqual(evidence.final_local, evidence.cold_unknown);
  evidence.checks.restart_preserves_durable_unknown_with_same_identity = true;
  evidence.recovery_journal = [];
  const journalRoot = path.dirname(evidence.trace.journal_path);
  for (const name of (await fs.readdir(journalRoot)).filter(name => name.endsWith(".json"))) {
    const row = JSON.parse(await fs.readFile(path.join(journalRoot, name), "utf8"));
    if (row.local_task_id === recovery.id) { assert.equal(row.state, "completed"); evidence.recovery_journal.push(row); }
  }
  assert.ok(evidence.recovery_journal.length >= 3);
  evidence.passed = true;
} catch (error) { evidence.error = String(error?.stack || error); }
finally {
  try { if (!gatewayState().State.Running) docker("start", metadata.id); }
  catch (error) { evidence.gateway_cleanup_error = String(error); evidence.passed = false; }
  if (application) {
    evidence.failure_trace ||= await application.evaluate(() => globalThis.__gatewayCrash).catch(() => null);
    await application.evaluate(() => globalThis.__releaseCrashReply?.()).catch(() => {});
    await application.close().catch(error => { evidence.quit_error = String(error); evidence.passed = false; });
  }
  const children = execFileSync("ps", ["-axo", "command="], { encoding: "utf8" }).split("\n").filter(line => line.includes(resources + "/browser-controller/"));
  evidence.checks.no_owned_controller_children = children.length === 0;
  if (children.length) evidence.passed = false;
  await fs.writeFile(output, JSON.stringify(evidence, null, 2) + "\n", { mode: 0o600, flag: "wx" });
  console.log(JSON.stringify({ passed: evidence.passed, output, checks: evidence.checks, error: evidence.error }));
  if (!evidence.passed) process.exitCode = 1;
}
