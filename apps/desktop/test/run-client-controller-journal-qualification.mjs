// Opt-in packaged persistence-failure qualification. Inject only terminal
// journal-method failures; never exhaust shared storage or alter saved rows.
import assert from "node:assert/strict";
import fs from "node:fs/promises";
import path from "node:path";
import crypto from "node:crypto";
import { execFileSync } from "node:child_process";
import { pathToFileURL } from "node:url";

const directory = process.env.SPARKCLAW_CONTROLLER_ISCP_LAB;
const executablePath = process.env.SPARKCLAW_CONTROLLER_CANDIDATE;
assert.ok(path.isAbsolute(directory || "") && path.basename(directory).startsWith("sx-controller-iscp-"));
assert.ok(path.isAbsolute(executablePath || "") && !executablePath.startsWith("/Applications/"));
const resources = path.resolve(path.dirname(executablePath), process.platform === "darwin" ? "../Resources" : "resources");
const { _electron } = await import(pathToFileURL(path.join(resources, "browser-controller/node_modules/playwright-core/index.mjs")));
const output = path.join(directory, `evidence/controller-journal-failure-${Date.now()}.json`);
const evidence = { schema_version: 1, passed: false, checks: {},
  scope: "packaged_terminal_journal_method_fault_over_encrypted_ISCP", duplicate_probes_are_local: true };
const hash = bytes => crypto.createHash("sha256").update(bytes).digest("hex");
const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
const sort = rows => [...rows].sort((a, b) => a.command_id.localeCompare(b.command_id));
let application, window, originalAcquire;
async function until(check, label, timeout = 90000) {
  const deadline = performance.now() + timeout;
  while (performance.now() < deadline) { if (await check()) return; await pause(100); }
  throw Error(`Journal qualification timeout: ${label}`);
}
async function launch(inject) {
  application = await _electron.launch({ executablePath, timeout: 45000, chromiumSandbox: true, env: { ...process.env,
    SPARKCLAW_DESKTOP_ISCP_LOCAL_TEST: "1", SPARKCLAW_DESKTOP_ISCP_CONFIG: path.join(directory, "desktop-profile.json"),
    SPARKCLAW_DESKTOP_USER_DATA_DIR: path.join(directory, "controller-native-profile") } });
  application.process().stderr.on("data", bytes => process.stderr.write(bytes));
  await application.evaluate(async ({ app, net, session, webContents }, inject) => {
    const require = process.getBuiltinModule("node:module").createRequire(app.getAppPath() + "/package.json");
    const assert = require("node:assert/strict"), fs = require("node:fs/promises"), path = require("node:path");
    const { BrowserControllerForwarder } = require(app.getAppPath() + "/src/browser/controller-forwarder.mjs");
    const { ClientControllerRuntime } = require(app.getAppPath() + "/src/browser/controller-runtime.mjs");
    const { HostJournal } = require(app.getAppPath() + "/src/browser/host-journal.mjs");
    const { controllerDigest } = require(app.getAppPath() + "/src/browser/controller-wire.mjs");
    const trace = globalThis.__journalQualification = { native_calls: [], finish_attempts: [], business_http: 0 };
    const snapshot = BrowserControllerForwarder.prototype.snapshot;
    BrowserControllerForwarder.prototype.snapshot = function() { globalThis.__journalAgent = this; return snapshot.call(this); };
    const reverse = BrowserControllerForwarder.prototype.handleReverse;
    BrowserControllerForwarder.prototype.handleReverse = async function(request, authority) {
      globalThis.__journalAgent = this;
      if (request.operation === "browser.controller.acquire") globalThis.__journalAcquire = structuredClone(request);
      if (inject && request.body?.operation === "page.fill" && !trace.command) {
        trace.command = { command_id: request.body.command_id, digest: request.body.digest, binding: request.body.binding };
        const first = reverse.call(this, request, authority);
        const duplicate = reverse.call(this, structuredClone(request), authority);
        const changed = structuredClone(request); changed.body.arguments.text = "Must not be applied";
        changed.body.digest = controllerDigest(changed.operation, changed.body);
        const [reply, repeated, conflict] = await Promise.all([first, duplicate, reverse.call(this, changed, authority)]);
        assert.deepEqual(repeated, reply); assert.equal(reply.body.outcome, "unknown");
        assert.equal(conflict.body.outcome, "not_dispatched"); assert.equal(conflict.body.error_code, "command_fenced");
        trace.concurrent_duplicate_shared_unknown = true; trace.changed_digest_fenced = true;
        trace.reply = { outcome: reply.body.outcome, error_code: reply.body.error_code,
          no_result: !reply.body.payload && !reply.body.result };
        return reply;
      }
      return reverse.call(this, request, authority);
    };
    const finish = HostJournal.prototype.finish;
    HostJournal.prototype.finish = async function(id, state) {
      if (inject && id === trace.command?.command_id) {
        trace.finish_attempts.push(state);
        throw Object.assign(new Error("Qualification: terminal journal persistence unavailable"), { code: "EIO" });
      }
      return finish.call(this, id, state);
    };
    const request = ClientControllerRuntime.prototype.request;
    ClientControllerRuntime.prototype.request = async function(action, body, options) {
      trace.native_calls.push({ action, operation: body.operation });
      const reply = await request.call(this, action, body, options);
      if (action === "execute" && body.operation === "page.fill") {
        const page = webContents.getAllWebContents().find(page => page.getURL() === "https://controller.fixture.test/" && page.debugger.isAttached());
        trace.effect = await page.executeJavaScript('({value:document.querySelector("input").value,inputs:window.fixtureInputs})');
        assert.deepEqual(trace.effect, { value: "Alice", inputs: 1 });
        trace.dispatched_before_terminal = JSON.parse(await fs.readFile(path.join(globalThis.__journalAgent.journal.root, trace.command.command_id + ".json"), "utf8"));
        assert.equal(trace.dispatched_before_terminal.state, "dispatched");
      }
      return reply;
    };
    const fetch = net.fetch.bind(net);
    net.fetch = (...args) => { if (/^https?:/u.test(String(args[0]?.url || args[0]))) { trace.business_http++; throw Error("Business HTTP forbidden"); } return fetch(...args); };
    globalThis.fetch = () => { trace.business_http++; throw Error("Business HTTP forbidden"); };
    session.fromPartition("persist:sparkclaw-workbench-default").webRequest.onBeforeRequest({ urls: ["http://*/*", "https://*/*", "ws://*/*", "wss://*/*"] }, (_request, callback) => { trace.business_http++; callback({ cancel: true }); });
    await session.fromPartition("persist:sparkclaw-browser-default").protocol.handle("https", request => {
      assert.equal(new URL(request.url).origin, "https://controller.fixture.test");
      return new Response('<!doctype html><title>Journal failure fixture</title><label>Display name<input aria-label="Display name"></label><script>window.fixtureInputs=0;document.querySelector("input").oninput=()=>window.fixtureInputs++</script>', { headers: { "content-type": "text/html" } });
    });
  }, inject);
  await until(() => (window = application.windows().find(page => page.url() === "sparkclaw-app://workbench/index.html")), "workbench");
  await window.waitForSelector(".localWorkbench");
  await until(async () => (await window.evaluate(() => window.sparkclawDesktop.localConnection())).state === "connected", "connection");
  await until(async () => (await window.evaluate(() => window.sparkclawDesktop.state())).browser_host.state === "connected", "Controller readiness");
  const runtime = await application.evaluate(({ app, safeStorage }) => ({ packaged: app.isPackaged, executable: process.execPath,
    profile: app.getPath("userData"), versions: process.versions, secure_storage: safeStorage.isEncryptionAvailable() }));
  assert.ok(runtime.packaged && runtime.secure_storage); assert.equal(runtime.executable, executablePath);
  evidence.runtime ||= runtime; assert.equal(runtime.profile, evidence.runtime.profile);
}
async function freshAcquireDenied() {
  return application.evaluate(async ({ app }, original) => {
    const require = process.getBuiltinModule("node:module").createRequire(app.getAppPath() + "/package.json");
    const { controllerDigest, iscpHostID, iscpAuthorizationDigest } = require(app.getAppPath() + "/src/browser/controller-wire.mjs");
    const agent = globalThis.__journalAgent, session = agent.session, trace = globalThis.__journalQualification;
    const request = structuredClone(original), before = trace.native_calls.length;
    request.body.command_id = require("node:crypto").randomUUID();
    Object.assign(request.body.binding, { host_id: iscpHostID(agent.scope, session.session_id), runtime_generation: "iscp_" + session.session_id,
      connection_epoch: session.session_id, authorization_digest: iscpAuthorizationDigest(session.session_id, session.authorization_revision) });
    request.body.digest = controllerDigest(request.operation, request.body);
    const reply = await agent.handleReverse(request, session);
    return { outcome: reply.body?.outcome, error_code: reply.body?.error_code, calls_added: trace.native_calls.length - before };
  }, originalAcquire);
}
try {
  const configBytes = await fs.readFile(path.join(directory, "gateway-config.json")), catalogBytes = await fs.readFile(path.join(directory, "model.profiles.json"));
  const config = JSON.parse(configBytes), catalog = JSON.parse(catalogBytes);
  evidence.provider = { capacity_profile: config.model.capacity_profile, mock: catalog.profiles[config.model.capacity_profile].mock,
    config_sha256: hash(configBytes), catalog_sha256: hash(catalogBytes) };
  evidence.gateway_sha256 = hash(await fs.readFile(path.join(directory, "bin/gateway-linux")));
  evidence.asar_sha256 = hash(await fs.readFile(path.join(resources, "app.asar")));
  evidence.controller_manifest_sha256 = hash(await fs.readFile(path.join(resources, "browser-controller/manifest.json")));
  await launch(true);
  evidence.unknown_before = (await window.evaluate(() => window.sparkclawDesktop.state())).browser_host.unknown_writes;
  const conversation = await window.evaluate(title => window.sparkclawClientStore.create(title), `Journal failure ${Date.now()}`);
  await window.evaluate(id => window.sparkclawDesktop.selectConversation(id), conversation.id);
  const task = await window.evaluate(async id => { const s = window.sparkclawClientStore, t = await s.enqueue(id, "在 https://controller.fixture.test/ 页面 Display name 字段填写 Alice。", []); await s.submit(t.request_id); return t; }, conversation.id);
  evidence.task = { request_id: task.request_id, local_task_id: task.id, conversation_id: conversation.id };
  let state, approvals = 0;
  await until(async () => {
    await window.evaluate(id => window.sparkclawClientStore.reconcile(id), task.request_id);
    state = await window.evaluate(id => window.sparkclawClientStore.read(id), conversation.id);
    const current = state.tasks.find(row => row.request_id === task.request_id), approval = current?.approvals?.find(row => row.state === "pending" && row.actionable);
    if (approval) { assert.equal(approvals++, 0); assert.equal(approval.tool, "browser.type"); await window.evaluate(({ id, a }) => window.sparkclawClientStore.decideApproval(id, a.approval_id, a.digest, "approve"), { id: task.request_id, a: approval }); }
    return ["delivered", "failed", "canceled", "unknown"].includes(current?.status);
  }, "unknown execution");
  assert.equal(approvals, 1); assert.equal(state.tasks.find(row => row.request_id === task.request_id).status, "unknown");
  assert.equal(state.messages.filter(row => row.role === "assistant").length, 0);
  evidence.trace = await application.evaluate(() => globalThis.__journalQualification);
  assert.deepEqual(evidence.trace.finish_attempts, ["completed", "unknown"]);
  assert.equal(evidence.trace.native_calls.filter(row => row.operation === "page.fill").length, 1);
  assert.ok(evidence.trace.concurrent_duplicate_shared_unknown && evidence.trace.changed_digest_fenced && evidence.trace.reply.no_result);
  evidence.checks.actual_fill_once_and_concurrent_local_duplicate_cannot_repeat_it = true;
  evidence.checks.failed_terminal_persistence_cannot_report_success = true;
  originalAcquire = await application.evaluate(() => globalThis.__journalAcquire);
  evidence.before_restart_probe = await freshAcquireDenied();
  assert.deepEqual(evidence.before_restart_probe, { outcome: "not_dispatched", error_code: "browser_write_outcome_unknown", calls_added: 0 });
  const stopped = await application.evaluate(({ webContents }) => ({ child_absent: globalThis.__journalAgent.runtime.child === null,
    lease_absent: globalThis.__journalAgent.lease === null, task_pages: webContents.getAllWebContents().filter(page => page.getURL() === "https://controller.fixture.test/").length }));
  assert.deepEqual(stopped, { child_absent: true, lease_absent: true, task_pages: 0 });
  evidence.checks.failure_stops_owned_runtime_and_blocks_fresh_command = true;
  assert.equal(evidence.trace.business_http, 0);
  await application.close(); application = undefined;
  const journalPath = path.join(directory, "controller-native-profile/workbench/browser-journal", evidence.trace.command.command_id + ".json");
  evidence.cold_dispatched = JSON.parse(await fs.readFile(journalPath, "utf8"));
  assert.equal(evidence.cold_dispatched.state, "dispatched");
  await launch(false);
  const unknown = (await window.evaluate(() => window.sparkclawDesktop.state())).browser_host.unknown_writes;
  assert.deepEqual(sort(unknown.filter(row => row.command_id !== evidence.trace.command.command_id)), sort(evidence.unknown_before));
  assert.ok(unknown.some(row => row.command_id === evidence.trace.command.command_id && row.digest === evidence.trace.command.digest));
  evidence.after_restart_probe = await freshAcquireDenied();
  assert.deepEqual(evidence.after_restart_probe, evidence.before_restart_probe);
  evidence.reopened = await application.evaluate(() => globalThis.__journalQualification);
  assert.deepEqual(evidence.reopened.native_calls, []); assert.equal(evidence.reopened.business_http, 0);
  assert.equal(await window.evaluate(() => fetch(window.sparkclawDesktop.gatewayBase + "/api/owner").then(response => response.status)), 200);
  evidence.checks.same_profile_restart_preserves_unknown_and_never_replays = true;
  evidence.checks.prior_fences_original_workbench_and_unrelated_ISCP_survive = true;
  await application.close(); application = undefined;
  evidence.final_unknown = JSON.parse(await fs.readFile(journalPath, "utf8"));
  assert.equal(evidence.final_unknown.state, "unknown");
  for (const key of ["command_id", "digest", "installation_id", "local_conversation_id", "local_task_id", "owner_id", "client_id"]) assert.equal(evidence.final_unknown[key], evidence.cold_dispatched[key]);
  evidence.checks.restart_promotes_dispatched_to_durable_unknown_with_same_identity = true;
  evidence.passed = true;
} catch (error) { evidence.error = String(error?.stack || error); }
finally {
  if (application) await application.close().catch(error => { evidence.quit_error = String(error); evidence.passed = false; });
  const children = execFileSync("ps", ["-axo", "command="], { encoding: "utf8" }).split("\n").filter(line => line.includes(resources + "/browser-controller/"));
  evidence.checks.no_owned_controller_children = children.length === 0;
  if (children.length) evidence.passed = false;
  await fs.writeFile(output, JSON.stringify(evidence, null, 2) + "\n", { mode: 0o600, flag: "wx" });
  console.log(JSON.stringify({ passed: evidence.passed, output, checks: evidence.checks, error: evidence.error }));
  if (!evidence.passed) process.exitCode = 1;
}
