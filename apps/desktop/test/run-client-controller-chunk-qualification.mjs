// Opt-in fault qualification against an unchanged packaged app and encrypted
// ISCP lab. The synthetic page enlarges the *real fill result*. Instrumentation
// delays/corrupts selected reverse replies; it never performs a browser action.
import assert from "node:assert/strict";
import fs from "node:fs/promises";
import path from "node:path";
import crypto from "node:crypto";
import { execFileSync } from "node:child_process";
import { pathToFileURL } from "node:url";
import { installResultFaults, verifyCacheRecovery, verifyExpiredWriteReplay } from "./client-controller-result-faults.mjs";
import { probePackagedResponseBoundary } from "./client-controller-response-boundaries.mjs";
import { installLimitTransfer, verifyLimitTransfer } from "./client-controller-limit-transfer.mjs";

const directory = process.env.SPARKCLAW_CONTROLLER_ISCP_LAB;
const executablePath = process.env.SPARKCLAW_CONTROLLER_CANDIDATE;
const mode = process.env.SPARKCLAW_CONTROLLER_CHUNK_MODE;
assert.ok(path.isAbsolute(directory || "") && path.basename(directory).startsWith("sx-controller-iscp-"));
assert.ok(path.isAbsolute(executablePath || "") && !executablePath.startsWith("/Applications/"));
assert.ok(["out_of_order", "lost_final", "wrong_offset", "bad_hash", "cache_full", "expired_result", "exact_limit"].includes(mode));
const resources = path.resolve(path.dirname(executablePath), process.platform === "darwin" ? "../Resources" : "resources");
const { _electron } = await import(pathToFileURL(path.join(resources, "browser-controller/node_modules/playwright-core/index.mjs")));
const output = path.join(directory, `evidence/controller-chunks-${mode}-${Date.now()}.json`);
const evidence = { schema_version: 1, mode, passed: false, checks: {},
  scope: "instrumented_actual_package_reverse_replies_over_encrypted_ISCP", local_probes_are_not_wire_calls: true };
const hash = bytes => crypto.createHash("sha256").update(bytes).digest("hex");
const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
const byCommand = (a, b) => a.command_id.localeCompare(b.command_id);
let application, window;
async function until(check, label, timeout = 90000) {
  const deadline = performance.now() + timeout;
  while (performance.now() < deadline) { if (await check()) return; await pause(100); }
  throw Error(`Chunk qualification timeout: ${label}`);
}
try {
  const configBytes = await fs.readFile(path.join(directory, "gateway-config.json"));
  const catalogBytes = await fs.readFile(path.join(directory, "model.profiles.json"));
  const config = JSON.parse(configBytes), catalog = JSON.parse(catalogBytes);
  evidence.provider = { capacity_profile: config.model.capacity_profile, mock: catalog.profiles[config.model.capacity_profile].mock,
    config_sha256: hash(configBytes), catalog_sha256: hash(catalogBytes) };
  evidence.gateway_sha256 = hash(await fs.readFile(path.join(directory, "bin/gateway-linux")));
  evidence.asar_sha256 = hash(await fs.readFile(path.join(resources, "app.asar")));
  evidence.controller_manifest_sha256 = hash(await fs.readFile(path.join(resources, "browser-controller/manifest.json")));
  application = await _electron.launch({ executablePath, timeout: 45000, chromiumSandbox: true, env: { ...process.env,
    SPARKCLAW_DESKTOP_ISCP_LOCAL_TEST: "1", SPARKCLAW_DESKTOP_ISCP_CONFIG: path.join(directory, "desktop-profile.json"),
    SPARKCLAW_DESKTOP_USER_DATA_DIR: path.join(directory, "controller-native-profile") } });
  application.process().stderr.on("data", bytes => process.stderr.write(bytes));
  evidence.runtime = await application.evaluate(({ app, safeStorage }) => ({ packaged: app.isPackaged, executable: process.execPath,
    profile: app.getPath("userData"), versions: process.versions, secure_storage: safeStorage.isEncryptionAvailable() }));
  assert.equal(evidence.runtime.packaged, true); assert.equal(evidence.runtime.secure_storage, true);
  await until(() => (window = application.windows().find(page => page.url() === "sparkclaw-app://workbench/index.html")), "workbench");
  await window.waitForSelector(".localWorkbench");
  await until(async () => (await window.evaluate(() => window.sparkclawDesktop.localConnection())).state === "connected", "ISCP connection");
  await until(async () => (await window.evaluate(() => window.sparkclawDesktop.state())).browser_host.state === "connected", "Controller readiness");
  evidence.connection = await window.evaluate(() => window.sparkclawDesktop.localConnection());
  assert.equal(evidence.connection.backend.transport, "iscp"); assert.equal(evidence.connection.capabilities.browser, true);
  evidence.unknown_before = (await window.evaluate(() => window.sparkclawDesktop.state())).browser_host.unknown_writes;
  await application.evaluate(async ({ app, net, session }, mode) => {
    const require = process.getBuiltinModule("node:module").createRequire(app.getAppPath() + "/package.json");
    const assert = require("node:assert/strict"), crypto = require("node:crypto");
    const { BrowserControllerForwarder } = require(app.getAppPath() + "/src/browser/controller-forwarder.mjs");
    const { ClientControllerRuntime } = require(app.getAppPath() + "/src/browser/controller-runtime.mjs");
    const { controllerDigest, wireBytes } = require(app.getAppPath() + "/src/browser/controller-wire.mjs");
    const trace = globalThis.__chunkQualification = { events: [], fill_calls: 0, http_attempts: 0, probes: {} };
    const request = ClientControllerRuntime.prototype.request;
    ClientControllerRuntime.prototype.request = function(action, body, options) {
      if (action === "execute" && body.operation === "page.fill") trace.fill_calls++;
      return request.call(this, action, body, options);
    };
    const original = BrowserControllerForwarder.prototype.handleReverse;
    globalThis.__chunkRawDispatch = original;
    BrowserControllerForwarder.prototype.handleReverse = async function(request, authority) {
      const reply = await original.call(this, request, authority);
      const row = { operation: request.operation, inner_operation: request.body?.operation, command_id: request.body?.command_id,
        status: reply.status, outcome: reply.body?.outcome, error_code: reply.body?.error_code };
      if (request.body?.operation === "page.fill" && reply.body?.outcome === "completed") {
        assert.ok(reply.body.result?.total_bytes > 65536, "the real fill result must require multiple chunks");
        assert.equal(reply.body.payload, undefined);
        trace.fill = { command_id: request.body.command_id, digest: request.body.digest, descriptor: reply.body.result,
          journal_before_chunk: this.journal.rows.get(request.body.command_id)?.state };
        assert.equal(trace.fill.journal_before_chunk, "completed");
        assert.deepEqual(await original.call(this, { ...request, id: crypto.randomUUID() }, authority), reply);
        const changed = structuredClone(request);
        changed.body.arguments.text = "must not be written";
        changed.body.digest = controllerDigest(changed.operation, changed.body);
        assert.equal((await original.call(this, changed, authority)).body.error_code, "command_fenced");
        trace.probes.same_command_reuses_descriptor_and_changed_digest_is_fenced = true;
      }
      if (request.operation.endsWith(".chunk") && request.body.command_id === trace.fill?.command_id) {
        trace.fill.first_chunk_at ||= new Date().toISOString();
        const { offset, length } = request.body;
        Object.assign(row, { offset, length, returned_offset: reply.body?.offset, wire_bytes: wireBytes(reply) });
        if (mode === "expired_result") {
          assert.equal(reply.status, 404); assert.equal(reply.error_code, "not_found");
          trace.events.push(row); return reply;
        }
        assert.equal(reply.status, 200);
        const final = offset + length === trace.fill.descriptor.total_bytes;
        if (final) {
          assert.deepEqual(await original.call(this, request, authority), reply);
          assert.deepEqual(await original.call(this, request, authority), reply);
          for (const patch of [{ command_id: "other-command" }, { result_id: "other-result" },
            { offset: trace.fill.descriptor.total_bytes }, { binding: { ...request.body.binding, local_task_id: "other-task" } }]) {
            assert.equal((await original.call(this, { ...request, body: { ...request.body, ...patch } }, authority)).status, 404);
          }
          trace.probes.final_chunk_is_repeatable_and_foreign_scope_or_range_is_denied = true;
        }
        if (mode === "out_of_order" && offset === 0) await new Promise(resolve => setTimeout(resolve, 350));
        if (mode === "lost_final" && final && !trace.fault) {
          trace.fault = { kind: mode, offset, boundary: "completed mutation; final reverse reply held past Gateway deadline" };
          await new Promise(resolve => setTimeout(resolve, 31000));
        }
        if (mode === "wrong_offset" && offset === 0 && !trace.fault) {
          trace.fault = { kind: mode, offset }; reply.body.offset = 1;
        }
        if (mode === "bad_hash" && final && !trace.fault) {
          trace.fault = { kind: mode, offset };
          const bytes = Buffer.from(reply.body.data_base64, "base64"); bytes[0] ^= 1;
          reply.body.data_base64 = bytes.toString("base64");
        }
        row.returned_offset = reply.body.offset;
      }
      trace.events.push(row);
      return reply;
    };
    const fetch = net.fetch.bind(net);
    net.fetch = (...args) => { if (/^https?:/u.test(String(args[0]?.url || args[0]))) { trace.http_attempts++; throw Error("Business HTTP forbidden"); } return fetch(...args); };
    globalThis.fetch = () => { trace.http_attempts++; throw Error("Business HTTP forbidden"); };
    session.fromPartition("persist:sparkclaw-workbench-default").webRequest.onBeforeRequest({ urls: ["http://*/*", "https://*/*", "ws://*/*", "wss://*/*"] }, (_request, callback) => { trace.http_attempts++; callback({ cancel: true }); });
    await session.fromPartition("persist:sparkclaw-browser-default").protocol.handle("https", request => {
      assert.equal(new URL(request.url).origin, "https://controller.fixture.test");
      return new Response('<!doctype html><meta charset="utf-8"><title>Packaged chunk fixture</title><h1>Chunk result qualification</h1><label>Display name<input aria-label="Display name"></label><p id="draft">Empty draft</p><script>window.fixtureInputs=0;document.querySelector("input").addEventListener("input",e=>{window.fixtureInputs++;document.querySelector("#draft").textContent=e.target.value;document.title="Packaged chunk fixture "+"A".repeat(70000)+"<>&".repeat(3000)})</script>', { headers: { "content-type": "text/html" } });
    });
  }, mode);
  if (mode === "cache_full") evidence.packaged_UDS_boundary = await probePackagedResponseBoundary(application);
  await installResultFaults(application, mode);
  await installLimitTransfer(application, mode);
  const title = `Packaged chunks ${mode} ${Date.now()}`;
  const conversation = await window.evaluate(title => window.sparkclawClientStore.create(title), title);
  await window.evaluate(id => window.sparkclawDesktop.selectConversation(id), conversation.id);
  const task = await window.evaluate(async id => {
    const store = window.sparkclawClientStore, task = await store.enqueue(id, "在 https://controller.fixture.test/ 页面 Display name 字段填写 Alice。", []);
    await store.submit(task.request_id); return task;
  }, conversation.id);
  evidence.task = { conversation_id: conversation.id, request_id: task.request_id, local_task_id: task.id };
  let state, approvals = 0;
  await until(async () => {
    await window.evaluate(id => window.sparkclawClientStore.reconcile(id), task.request_id);
    state = await window.evaluate(id => window.sparkclawClientStore.read(id), conversation.id);
    const current = state.tasks.find(row => row.request_id === task.request_id);
    const approval = current?.approvals?.find(row => row.state === "pending" && row.actionable);
    if (approval) {
      assert.equal(approvals++, 0); assert.equal(approval.tool, "browser.type");
      assert.equal(await application.evaluate(() => globalThis.__chunkQualification.fill_calls), 0);
      await window.evaluate(({ request, approval }) => window.sparkclawClientStore.decideApproval(request, approval.approval_id, approval.digest, "approve"), { request: task.request_id, approval });
    }
    return ["delivered", "failed", "canceled", "unknown"].includes(current?.status);
  }, "workflow terminal result");
  evidence.terminal_task = state.tasks.find(row => row.request_id === task.request_id);
  evidence.assistant_messages = state.messages.filter(row => row.role === "assistant").map(row => row.content);
  evidence.trace = await application.evaluate(() => globalThis.__chunkQualification);
  assert.equal(approvals, 1);
  if (mode === "cache_full") await verifyCacheRecovery({ application, window, evidence, until, conversation });
  assert.equal(evidence.trace.fill_calls, 1);
  assert.ok(evidence.trace.fill); assert.equal(evidence.trace.fill.journal_before_chunk, "completed");
  assert.equal(evidence.trace.http_attempts, 0);
  const pages = await application.evaluate(async ({ webContents }) => Promise.all(webContents.getAllWebContents()
    .filter(page => page.getURL() === "https://controller.fixture.test/").map(async page => ({ sandbox: page.getLastWebPreferences().sandbox,
      debugger: page.debugger.isAttached(), dom: await page.executeJavaScript('({value:document.querySelector("input").value,inputs:window.fixtureInputs})') }))));
  evidence.pages = pages;
  assert.ok(pages.some(page => page.dom.value === "Alice" && page.dom.inputs === 1 && page.sandbox && !page.debugger));
  assert.deepEqual((await window.evaluate(() => window.sparkclawDesktop.state())).browser_host.unknown_writes.sort(byCommand), evidence.unknown_before.sort(byCommand));
  const fillChunks = evidence.trace.events.filter(row => row.operation.endsWith(".chunk") && row.command_id === evidence.trace.fill.command_id);
  if (["out_of_order", "cache_full", "exact_limit"].includes(mode)) {
    if (mode === "exact_limit") verifyLimitTransfer(evidence);
    if (mode === "out_of_order") {
      assert.ok(fillChunks.length >= 3 && fillChunks[0].offset > 0 && fillChunks.some(row => row.offset === 0));
      assert.equal(evidence.terminal_task.status, "delivered");
      assert.ok(!evidence.assistant_messages.some(message => /workflow (?:stopped|is blocked)/iu.test(message)));
      evidence.checks.out_of_order_large_fill_result_completes_workflow = true;
    }
  } else {
    assert.ok(evidence.trace.fault);
    // A failed approved workflow intentionally becomes a durable unknown task;
    // the independently completed browser write must remain completed. Do not
    // require an assistant answer when the existing execution path has none.
    assert.equal(evidence.terminal_task.status, "unknown");
    assert.equal(evidence.terminal_task.event_state, "unknown");
    assert.equal(evidence.assistant_messages.length, 0);
    assert.ok(evidence.trace.events.some(row => row.operation === "browser.controller.release" && row.outcome === "completed"));
    await window.evaluate(id => window.sparkclawClientStore.reconcile(id), task.request_id);
    const reconciled = await window.evaluate(id => window.sparkclawClientStore.read(id), conversation.id);
    assert.equal(reconciled.tasks.filter(row => row.request_id === task.request_id).length, 1);
    assert.equal(reconciled.tasks.find(row => row.request_id === task.request_id).status, "unknown");
    assert.equal(await application.evaluate(() => globalThis.__chunkQualification.fill_calls), 1);
    evidence.checks.failed_result_stops_workflow_without_repeating_completed_write = true;
  }
  assert.equal(evidence.trace.probes.same_command_reuses_descriptor_and_changed_digest_is_fenced, true);
  if (mode === "expired_result") await verifyExpiredWriteReplay({ application, evidence, until });
  if (!["wrong_offset", "expired_result"].includes(mode)) assert.equal(evidence.trace.probes.final_chunk_is_repeatable_and_foreign_scope_or_range_is_denied, true);
  assert.equal(await window.evaluate(() => fetch(window.sparkclawDesktop.gatewayBase + "/api/owner").then(response => response.status)), 200);
  evidence.checks.approved_mutation_runs_once_and_completed_journal_precedes_chunk_reads = true;
  evidence.checks.original_fences_and_retained_sandboxed_view_survive = true;
  evidence.checks.unrelated_ISCP_read_survives_and_no_business_HTTP = true;
  evidence.passed = true;
} catch (error) { evidence.error = String(error?.stack || error); }
finally {
  if (application) {
    evidence.trace = await application.evaluate(() => globalThis.__chunkQualification).catch(() => evidence.trace);
    await application.close().catch(error => { evidence.quit_error = String(error); evidence.passed = false; });
  }
  const children = execFileSync("ps", ["-axo", "command="], { encoding: "utf8" }).split("\n").filter(line => line.includes(resources + "/browser-controller/"));
  evidence.checks.no_owned_controller_children = children.length === 0;
  if (children.length) evidence.passed = false;
  await fs.writeFile(output, JSON.stringify(evidence, null, 2) + "\n", { mode: 0o600, flag: "wx" });
  console.log(JSON.stringify({ passed: evidence.passed, mode, output, checks: evidence.checks, error: evidence.error }));
  if (!evidence.passed) process.exitCode = 1;
}
