// Opt-in actual-package proof: pause an owned Relay after a native fill and
// durable desktop completion, before the reverse reply reaches the helper.
import assert from "node:assert/strict";
import fs from "node:fs/promises";
import path from "node:path";
import crypto from "node:crypto";
import { execFileSync } from "node:child_process";
import { pathToFileURL } from "node:url";
import { qualificationLaunchEnvironment, verifyQualificationAuthorization, synchronizeQualificationFences } from "./client-controller-qualification-launch.mjs";

const directory = process.env.SPARKCLAW_CONTROLLER_ISCP_LAB;
const executablePath = process.env.SPARKCLAW_CONTROLLER_CANDIDATE;
const metadataPath = process.env.SPARKCLAW_CONTROLLER_RELAY_METADATA;
assert.ok(path.isAbsolute(directory || "") && path.basename(directory).startsWith("sx-controller-iscp-"));
assert.ok(path.isAbsolute(executablePath || "") && !executablePath.startsWith("/Applications/"));
assert.ok(path.isAbsolute(metadataPath || ""));
assert.equal(process.env.SPARKCLAW_CONTROLLER_NODE_MANAGED, "1");
const metadata = JSON.parse(await fs.readFile(metadataPath, "utf8"));
assert.match(metadata.id, /^[a-f0-9]{64}$/u); assert.ok(metadata.lab_id);
function relay() {
  const value = JSON.parse(execFileSync("docker", ["inspect", metadata.id], { encoding: "utf8" }))[0];
  assert.equal(value.Config.Labels?.["io.sparkclaw.node-lab"], metadata.lab_id);
  assert.equal(value.Id, metadata.id); assert.equal(value.State.Running, true); return value;
}
assert.equal(relay().State.Paused, false);
const resources = path.resolve(path.dirname(executablePath), process.platform === "darwin" ? "../Resources" : "resources");
const { _electron } = await import(pathToFileURL(path.join(resources, "browser-controller/node_modules/playwright-core/index.mjs")));
const output = path.join(directory, `evidence/controller-write-disconnect-${Date.now()}.json`);
const evidence = { schema_version: 1, passed: false, checks: {},
  scope: "actual_package_owned_node_Relay_pause_after_durable_fill_before_reverse_reply", local_probes_are_not_wire_calls: true };
const hash = bytes => crypto.createHash("sha256").update(bytes).digest("hex");
const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
let application, window, originalAcquire;
async function until(check, label, timeout = 90000) {
  const end = performance.now() + timeout;
  while (performance.now() < end) { if (await check()) return; await pause(100); }
  throw Error(`Write disconnect timeout: ${label}`);
}
async function launch(inject) {
  application = await _electron.launch({ executablePath, timeout: 45000, chromiumSandbox: true,
    env: qualificationLaunchEnvironment(directory) });
  application.process().stderr.on("data", bytes => process.stderr.write(bytes));
  await application.evaluate(async ({ app, net, session, webContents }, { inject, relayID }) => {
    const require = process.getBuiltinModule("node:module").createRequire(app.getAppPath() + "/package.json");
    const assert = require("node:assert/strict"), fs = require("node:fs/promises"), path = require("node:path");
    const { BrowserControllerForwarder } = require(app.getAppPath() + "/src/browser/controller-forwarder.mjs");
    const { ClientControllerRuntime } = require(app.getAppPath() + "/src/browser/controller-runtime.mjs");
    const trace = globalThis.__writeDisconnect = { native_calls: [], business_http: 0, held: false };
    const snapshot = BrowserControllerForwarder.prototype.snapshot;
    BrowserControllerForwarder.prototype.snapshot = function(...args) { globalThis.__writeAgent = this; return snapshot.apply(this, args); };
    const request = ClientControllerRuntime.prototype.request;
    ClientControllerRuntime.prototype.request = function(action, body, options) {
      trace.native_calls.push({ action, operation: body.operation }); return request.call(this, action, body, options);
    };
    const reverse = BrowserControllerForwarder.prototype.handleReverse;
    BrowserControllerForwarder.prototype.handleReverse = async function(request, authority) {
      globalThis.__writeAgent = this;
      if (request.operation === "browser.controller.acquire") globalThis.__writeAcquire = structuredClone(request);
      const reply = await reverse.call(this, request, authority);
      if (inject && request.body?.operation === "page.fill" && reply.body?.outcome === "completed" && !trace.held) {
        const page = webContents.getAllWebContents().find(page => page.getURL() === "https://controller.fixture.test/" && page.debugger.isAttached());
        trace.effect = await page.executeJavaScript('({value:document.querySelector("input").value,inputs:window.fixtureInputs})');
        assert.deepEqual(trace.effect, { value: "Alice", inputs: 1 });
        trace.command = { command_id: request.body.command_id, digest: request.body.digest, binding: request.body.binding };
        trace.local_completed = JSON.parse(await fs.readFile(path.join(this.journal.root, trace.command.command_id + ".json"), "utf8"));
        assert.equal(trace.local_completed.state, "completed");
        require("node:child_process").execFileSync("docker", ["pause", relayID]);
        trace.paused_at_ms = Date.now(); trace.held = true;
        await new Promise(resolve => { globalThis.__releaseWriteReply = resolve; });
        trace.reply_released = true;
      }
      return reply;
    };
    const fetch = net.fetch.bind(net);
    net.fetch = (...args) => { if (/^https?:/u.test(String(args[0]?.url || args[0]))) { trace.business_http++; throw Error("Business HTTP forbidden"); } return fetch(...args); };
    globalThis.fetch = () => { trace.business_http++; throw Error("Business HTTP forbidden"); };
    session.fromPartition("persist:sparkclaw-workbench-default").webRequest.onBeforeRequest({ urls: ["http://*/*", "https://*/*", "ws://*/*", "wss://*/*"] }, (_request, callback) => { trace.business_http++; callback({ cancel: true }); });
    await session.fromPartition("persist:sparkclaw-browser-default").protocol.handle("https", request => {
      assert.equal(new URL(request.url).origin, "https://controller.fixture.test");
      return new Response('<!doctype html><title>Write disconnect fixture</title><label>Display name<input aria-label="Display name"></label><script>window.fixtureInputs=0;document.querySelector("input").oninput=()=>window.fixtureInputs++</script>', { headers: { "content-type": "text/html" } });
    });
  }, { inject, relayID: metadata.id });
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
    const agent = globalThis.__writeAgent, session = agent.session, trace = globalThis.__writeDisconnect;
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
  evidence.relay = { id: metadata.id, lab_id: metadata.lab_id };
  await launch(true);
  evidence.unknown_before = await synchronizeQualificationFences(application);
  const personal = (await window.evaluate(() => window.sparkclawDesktop.createPersonal())).page_ref;
  const conversation = await window.evaluate(title => window.sparkclawClientStore.create(title), `Write disconnect ${Date.now()}`);
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
  await until(() => application.evaluate(() => globalThis.__writeDisconnect.held), "durable fill before reply");
  evidence.trace = await application.evaluate(() => globalThis.__writeDisconnect);
  originalAcquire = await application.evaluate(() => globalThis.__writeAcquire);
  assert.equal(relay().State.Paused, true);
  evidence.checks.fill_applied_once_and_completed_locally_before_owned_Relay_pause = true;
  await until(async () => (await window.evaluate(() => window.sparkclawDesktop.localConnection())).state !== "connected", "authority loss under paused Relay", 45000);
  evidence.unavailable_state = (await window.evaluate(() => window.sparkclawDesktop.localConnection())).state;
  evidence.disconnect_observed_ms = Date.now() - evidence.trace.paused_at_ms;
  await until(() => application.evaluate(() => !globalThis.__writeAgent.runtime.child && !globalThis.__writeAgent.lease), "owned execution retired after disconnect", 15000);
  execFileSync("docker", ["unpause", metadata.id]);
  const recovered = await window.evaluate(() => window.sparkclawDesktop.retryLocalConnection());
  assert.equal(recovered.state, "connected");
  await application.evaluate(() => globalThis.__releaseWriteReply());
  await until(() => application.evaluate(() => globalThis.__writeDisconnect.reply_released), "late old-generation reply released");
  await until(async () => {
    await window.evaluate(id => window.sparkclawClientStore.reconcile(id), task.request_id);
    const state = await window.evaluate(id => window.sparkclawClientStore.read(id), conversation.id);
    evidence.recovered_task = state; return state.tasks.find(row => row.request_id === task.request_id)?.status === "unknown";
  }, "original execution unknown after reconnect");
  assert.equal(evidence.recovered_task.messages.filter(row => row.role === "assistant").length, 0);
  evidence.remote_unknown = await verifyUnknown();
  evidence.before_restart_probe = await freshAcquireDenied();
  assert.deepEqual(evidence.before_restart_probe, { outcome: "not_dispatched", error_code: "browser_write_outcome_unknown", calls_added: 0 });
  evidence.trace = await application.evaluate(() => globalThis.__writeDisconnect);
  assert.equal(evidence.trace.native_calls.filter(row => row.operation === "page.fill").length, 1);
  assert.equal(evidence.trace.business_http, 0);
  assert.ok((await window.evaluate(() => window.sparkclawDesktop.state())).pages.some(page => page.page_ref === personal && page.role === "personal"));
  evidence.checks.reconnect_preserves_original_unknown_without_success_or_native_repeat = true;
  evidence.checks.fresh_command_ID_cannot_bypass_remote_unknown_and_personal_page_survives = true;
  const journalPath = path.join(directory, "controller-native-profile/workbench/browser-journal", evidence.trace.command.command_id + ".json");
  await application.close(); application = undefined;
  assert.deepEqual(JSON.parse(await fs.readFile(journalPath, "utf8")), evidence.trace.local_completed);
  await launch(false);
  evidence.restarted_unknown = await verifyUnknown();
  evidence.after_restart_probe = await freshAcquireDenied();
  assert.deepEqual(evidence.after_restart_probe, evidence.before_restart_probe);
  evidence.restart_trace = await application.evaluate(() => globalThis.__writeDisconnect);
  assert.deepEqual(evidence.restart_trace.native_calls, []); assert.equal(evidence.restart_trace.business_http, 0);
  assert.equal(await window.evaluate(() => fetch(window.sparkclawDesktop.gatewayBase + "/api/owner").then(response => response.status)), 200);
  evidence.checks.same_profile_restart_keeps_remote_unknown_and_makes_no_native_calls = true;
  await application.close(); application = undefined;
  evidence.final_local = JSON.parse(await fs.readFile(journalPath, "utf8"));
  assert.deepEqual(evidence.final_local, evidence.trace.local_completed);
  evidence.checks.local_completed_receipt_remains_exact_through_reconnect_and_restart = true;
  evidence.passed = true;
} catch (error) { evidence.error = String(error?.stack || error); }
finally {
  try { if (relay().State.Paused) execFileSync("docker", ["unpause", metadata.id]); }
  catch (error) { evidence.relay_cleanup_error = String(error); evidence.passed = false; }
  if (application) {
    evidence.failure_trace ||= await application.evaluate(() => globalThis.__writeDisconnect).catch(() => null);
    await application.evaluate(() => globalThis.__releaseWriteReply?.()).catch(() => {});
    await application.close().catch(error => { evidence.quit_error = String(error); evidence.passed = false; });
  }
  const children = execFileSync("ps", ["-axo", "command="], { encoding: "utf8" }).split("\n").filter(line => line.includes(resources + "/browser-controller/"));
  evidence.checks.no_owned_controller_children = children.length === 0;
  if (children.length) evidence.passed = false;
  await fs.writeFile(output, JSON.stringify(evidence, null, 2) + "\n", { mode: 0o600, flag: "wx" });
  console.log(JSON.stringify({ passed: evidence.passed, output, checks: evidence.checks, error: evidence.error }));
  if (!evidence.passed) process.exitCode = 1;
}
