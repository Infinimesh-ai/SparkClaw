// Opt-in actual-package proof: hold the real CDP fill reply, kill the owned
// Controller or MCP, and observe the product settling its pending request.
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
const faultTarget = process.env.SPARKCLAW_CONTROLLER_CRASH_TARGET;
assert.ok(["controller", "mcp"].includes(faultTarget));
const resources = path.resolve(path.dirname(executablePath), process.platform === "darwin" ? "../Resources" : "resources");
const { _electron } = await import(pathToFileURL(path.join(resources, "browser-controller/node_modules/playwright-core/index.mjs")));
const output = path.join(directory, `evidence/controller-packaged-child-crash-${faultTarget}-${Date.now()}.json`);
const evidence = { schema_version: 1, passed: false, checks: {},
  scope: "actual_package_child_SIGKILL_while_CDP_fill_reply_pending", fault_target: faultTarget, local_probes_are_not_wire_calls: true };
const hash = bytes => crypto.createHash("sha256").update(bytes).digest("hex");
const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
let application, window, originalAcquire, originalFill;
async function until(check, label, timeout = 90000) {
  const end = performance.now() + timeout;
  while (performance.now() < end) { if (await check()) return; await pause(100); }
  throw Error(`Packaged child crash timeout: ${label}`);
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
    const trace = globalThis.__childCrash = { native_calls: [], acquisitions: [], business_http: 0, held: false };
    const snapshot = BrowserControllerForwarder.prototype.snapshot;
    BrowserControllerForwarder.prototype.snapshot = function(...args) { globalThis.__crashAgent = this; return snapshot.apply(this, args); };
    const request = ClientControllerRuntime.prototype.request;
    ClientControllerRuntime.prototype.request = async function(action, body, options) {
      trace.native_calls.push({ action, operation: body.operation });
      const injecting = inject && action === "execute" && body.operation === "page.fill" && !trace.held;
      let debuggerObject, originalSend;
      if (injecting) {
        const page = webContents.getAllWebContents().find(page => page.getURL() === "https://controller.fixture.test/" && page.debugger.isAttached());
        assert.ok(page); debuggerObject = page.debugger; originalSend = debuggerObject.sendCommand;
        trace.controller_pid = this.child.pid; trace.runtime_directory = this.directory;
        trace.incarnation = this.incarnation; trace.native_fill_pending = true;
        debuggerObject.sendCommand = async function(method, ...args) {
          const response = await originalSend.call(this, method, ...args);
          if (method === "Input.insertText" && !trace.held) {
            trace.effect = await page.executeJavaScript('({value:document.querySelector("input").value,inputs:window.fixtureInputs})');
            assert.deepEqual(trace.effect, { value: "Alice", inputs: 1 });
            trace.journal_path = path.join(globalThis.__crashAgent.journal.root, trace.command.command_id + ".json");
            trace.local_dispatched = JSON.parse(await fs.readFile(trace.journal_path, "utf8"));
            assert.equal(trace.local_dispatched.state, "dispatched");
            trace.held_method = method; trace.held = true;
            await new Promise(resolve => { globalThis.__releaseCrashReply = resolve; });
          }
          return response;
        };
      }
      try {
        const response = await request.call(this, action, body, options);
        if (action === "acquire") trace.acquisitions.push({ incarnation: this.incarnation, status: response.status });
        if (injecting) trace.native_fill_result = { returned: true, status: response.status };
        return response;
      } catch (error) {
        if (injecting) trace.native_fill_result = { rejected: true, code: error.code || "" };
        throw error;
      } finally {
        if (injecting) { trace.native_fill_pending = false; debuggerObject.sendCommand = originalSend; }
      }
    };
    const reverse = BrowserControllerForwarder.prototype.handleReverse;
    BrowserControllerForwarder.prototype.handleReverse = function(request, authority) {
      globalThis.__crashAgent = this;
      if (request.operation === "browser.controller.acquire") globalThis.__crashAcquire = structuredClone(request);
      if (request.body?.operation === "page.fill") {
        trace.command = { command_id: request.body.command_id, digest: request.body.digest, binding: request.body.binding };
        globalThis.__crashFill = structuredClone(request);
      }
      return reverse.call(this, request, authority);
    };
    const fetch = net.fetch.bind(net);
    net.fetch = (...args) => { if (/^https?:/u.test(String(args[0]?.url || args[0]))) { trace.business_http++; throw Error("Business HTTP forbidden"); } return fetch(...args); };
    globalThis.fetch = () => { trace.business_http++; throw Error("Business HTTP forbidden"); };
    session.fromPartition("persist:sparkclaw-workbench-default").webRequest.onBeforeRequest({ urls: ["http://*/*", "https://*/*", "ws://*/*", "wss://*/*"] }, (_request, callback) => { trace.business_http++; callback({ cancel: true }); });
    await session.fromPartition("persist:sparkclaw-browser-default").protocol.handle("https", request => {
      assert.equal(new URL(request.url).origin, "https://controller.fixture.test");
      return new Response('<!doctype html><title>Child crash fixture</title><label>Display name<input aria-label="Display name"></label><script>window.fixtureInputs=0;document.querySelector("input").oninput=()=>window.fixtureInputs++</script>', { headers: { "content-type": "text/html" } });
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
    const agent = globalThis.__crashAgent, session = agent.session, trace = globalThis.__childCrash;
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
    await browser.cookies.set({ url: "https://controller.fixture.test/", name: "controller-child-crash-marker", value: marker, expirationDate: Date.now() / 1000 + 86400 });
    await browser.cookies.flushStore();
  }, marker);
  const retained = await window.evaluate(async () => {
    const store = window.sparkclawClientStore, conversation = await store.create("Child crash retained draft");
    const draft = await store.draft(conversation.id);
    const file = await store.saveFile(conversation.id, "crash-retained.txt", new TextEncoder().encode("Retain this attachment through child-process SIGKILL.\n"));
    await store.saveDraft(conversation.id, "Retain this unsent draft.", [file.id], draft.revision, draft.scope_key);
    return { conversation: conversation.id, file: file.id };
  });
  const conversation = await window.evaluate(title => window.sparkclawClientStore.create(title), `Child crash ${faultTarget} ${Date.now()}`);
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
  await until(() => application.evaluate(() => globalThis.__childCrash.held), "applied fill with dispatched journal");
  evidence.trace = await application.evaluate(() => globalThis.__childCrash);
  originalAcquire = await application.evaluate(() => globalThis.__crashAcquire);
  originalFill = await application.evaluate(() => globalThis.__crashFill);
  assert.equal(evidence.trace.native_calls.filter(row => row.operation === "page.fill").length, 1);
  assert.equal(evidence.trace.business_http, 0);
  evidence.checks.fill_applied_once_with_real_CDP_reply_pending_and_journal_dispatched = true;
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
  const mcp = rows.filter(row => Number(row[2]) === controller && Number(row[3]) === controller &&
    row[4].includes(resources + "/browser-controller/node_modules/@playwright/mcp/cli.js"));
  assert.equal(mcp.length, 1);
  const crashPID = faultTarget === "controller" ? controller : Number(mcp[0][1]);
  assert.equal(await fs.realpath(path.dirname(runtimeDirectory)), path.join(await fs.realpath("/tmp"), `sparkx-${process.getuid()}`));
  assert.match(path.basename(runtimeDirectory), /^c-[A-Za-z0-9]+$/u);
  const owned = await fs.lstat(runtimeDirectory); assert.equal(owned.uid, process.getuid()); assert.equal(owned.mode & 0o077, 0); assert.equal(owned.isSymbolicLink(), false);
  assert.equal(evidence.trace.native_fill_pending, true); assert.equal(evidence.trace.native_fill_result, undefined);
  assert.equal(evidence.trace.held_method, "Input.insertText");
  const personal = (await window.evaluate(() => window.sparkclawDesktop.createPersonal())).page_ref;
  const started = performance.now(); process.kill(crashPID, "SIGKILL");
  await until(async () => application.evaluate(() => !globalThis.__childCrash.native_fill_pending &&
    globalThis.__crashAgent.runtime.child === null && globalThis.__crashAgent.runtime.state === "stopped" && globalThis.__crashAgent.lease === null), "native request failure and runtime cleanup", 15000);
  await until(() => !execFileSync("ps", ["-axo", "pgid="], { encoding: "utf8" }).split("\n").some(line => Number(line.trim()) === controller), "owned Controller/MCP process group exit", 8000);
  await assert.rejects(fs.lstat(runtimeDirectory), { code: "ENOENT" });
  evidence.child_exit = { target: faultTarget, pid: crashPID, controller_pid: controller, cleanup_observed_ms: Math.round(performance.now() - started) };
  evidence.settled_trace = await application.evaluate(() => globalThis.__childCrash);
  assert.ok(evidence.settled_trace.native_fill_result.rejected || evidence.settled_trace.native_fill_result.status >= 400);
  assert.equal(await application.evaluate(({ webContents }) => webContents.getAllWebContents().filter(page => page.getURL() === "https://controller.fixture.test/").length), 0);
  assert.ok((await window.evaluate(() => window.sparkclawDesktop.state())).pages.some(page => page.page_ref === personal && page.role === "personal"));
  evidence.checks.child_SIGKILL_propagates_through_pending_native_call_and_cleans_owned_execution = true;
  await application.evaluate(() => globalThis.__releaseCrashReply?.());
  evidence.checks.abnormal_teardown_closes_task_view_and_preserves_personal_page = true;
  await until(async () => {
    await window.evaluate(id => window.sparkclawClientStore.reconcile(id), task.request_id);
    const state = await window.evaluate(id => window.sparkclawClientStore.read(id), conversation.id);
    return state.tasks.find(row => row.request_id === task.request_id)?.status === "unknown";
  }, "original execution unknown before restart");
  evidence.before_restart_unknown = await verifyUnknown();
  evidence.before_restart_probe = await freshAcquireDenied();
  assert.deepEqual(evidence.before_restart_probe, { outcome: "not_dispatched", error_code: "browser_write_outcome_unknown", calls_added: 0 });
  await application.close(); application = undefined; window = undefined;
  evidence.cold_unknown = JSON.parse(await fs.readFile(evidence.trace.journal_path, "utf8"));
  assert.equal(evidence.cold_unknown.state, "unknown");
  for (const key of ["command_id", "digest", "installation_id", "local_conversation_id", "local_task_id", "owner_id", "client_id"]) assert.equal(evidence.cold_unknown[key], evidence.trace.local_dispatched[key]);
  evidence.checks.unknown_durable_before_restart_and_fresh_ID_cannot_bypass = true;
  await launch(false);
  evidence.after_restart_identity = await immutable();
  assert.deepEqual(evidence.after_restart_identity, evidence.before_crash_identity);
  evidence.restarted_unknown = await verifyUnknown();
  evidence.after_restart_probe = await freshAcquireDenied();
  assert.deepEqual(evidence.after_restart_probe, { outcome: "not_dispatched", error_code: "browser_write_outcome_unknown", calls_added: 0 });
  await until(async () => {
    await window.evaluate(id => window.sparkclawClientStore.reconcile(id), task.request_id);
    const state = await window.evaluate(id => window.sparkclawClientStore.read(id), conversation.id);
    evidence.recovered_task = state; return state.tasks.find(row => row.request_id === task.request_id)?.status === "unknown";
  }, "original execution unknown after restart");
  assert.equal(evidence.recovered_task.tasks.filter(row => row.request_id === task.request_id).length, 1);
  assert.equal(evidence.recovered_task.messages.filter(row => row.role === "assistant").length, 0);
  evidence.restart_trace = await application.evaluate(() => globalThis.__childCrash);
  assert.deepEqual(evidence.restart_trace.native_calls, []); assert.equal(evidence.restart_trace.business_http, 0);
  evidence.checks.original_task_identity_and_unknown_survive_without_native_replay = true;
  const cookies = await application.evaluate(({ session }) => session.fromPartition("persist:sparkclaw-browser-default").cookies.get({ name: "controller-child-crash-marker" }));
  assert.ok(cookies.some(cookie => cookie.value === marker));
  const file = await window.evaluate(id => window.sparkclawClientStore.readFile(id), retained.file);
  assert.equal(hash(Buffer.from(Object.values(file.bytes))), hash(Buffer.from("Retain this attachment through child-process SIGKILL.\n")));
  const draft = await window.evaluate(id => window.sparkclawClientStore.draft(id), retained.conversation);
  assert.equal(draft.content, "Retain this unsent draft."); assert.ok(draft.local_file_ids.includes(retained.file));
  assert.equal(await window.evaluate(() => fetch(window.sparkclawDesktop.gatewayBase + "/api/owner").then(response => response.status)), 200);
  evidence.checks.personal_cookie_attachment_draft_original_IPC_and_safe_ISCP_read_survive = true;
  const recoveryConversation = await window.evaluate(() => window.sparkclawClientStore.create("Child crash fresh recovery task"));
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
    const agent = globalThis.__crashAgent, session = agent.session, trace = globalThis.__childCrash;
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
  evidence.recovery_trace = await application.evaluate(() => globalThis.__childCrash);
  assert.equal(evidence.recovery_trace.native_calls.filter(row => row.operation === "page.fill").length, 1);
  assert.equal(evidence.recovery_trace.business_http, 0);
  assert.equal(evidence.recovery_trace.acquisitions.length, 1);
  assert.notEqual(evidence.recovery_trace.acquisitions[0].incarnation, evidence.trace.incarnation);
  evidence.final_unknown_writes = await verifyUnknown();
  evidence.checks.old_lease_denied_while_new_incarnation_completes_separately_approved_task = true;
  await application.close(); application = undefined;
  evidence.final_local = JSON.parse(await fs.readFile(evidence.trace.journal_path, "utf8"));
  assert.equal(evidence.final_local.state, "unknown");
  for (const key of ["command_id", "digest", "installation_id", "local_conversation_id", "local_task_id", "owner_id", "client_id"]) assert.equal(evidence.final_local[key], evidence.cold_unknown[key]);
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
  if (application) {
    evidence.failure_trace ||= await application.evaluate(() => globalThis.__childCrash).catch(() => null);
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
