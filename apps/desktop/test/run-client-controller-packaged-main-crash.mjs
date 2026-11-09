// Opt-in actual-package proof: SIGKILL the main process after one native fill
// while its durable journal still says dispatched, then reopen the same profile.
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
const resources = path.resolve(path.dirname(executablePath), process.platform === "darwin" ? "../Resources" : "resources");
const { _electron } = await import(pathToFileURL(path.join(resources, "browser-controller/node_modules/playwright-core/index.mjs")));
const output = path.join(directory, `evidence/controller-packaged-main-crash-${Date.now()}.json`);
const evidence = { schema_version: 1, passed: false, checks: {},
  scope: "actual_package_main_SIGKILL_after_native_fill_before_local_terminal_persistence", local_probes_are_not_wire_calls: true };
const hash = bytes => crypto.createHash("sha256").update(bytes).digest("hex");
const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
let application, window, originalAcquire;
async function until(check, label, timeout = 90000) {
  const end = performance.now() + timeout;
  while (performance.now() < end) { if (await check()) return; await pause(100); }
  throw Error(`Packaged main crash timeout: ${label}`);
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
    const trace = globalThis.__mainCrash = { native_calls: [], business_http: 0, held: false };
    const snapshot = BrowserControllerForwarder.prototype.snapshot;
    BrowserControllerForwarder.prototype.snapshot = function(...args) { globalThis.__crashAgent = this; return snapshot.apply(this, args); };
    const request = ClientControllerRuntime.prototype.request;
    ClientControllerRuntime.prototype.request = async function(action, body, options) {
      trace.native_calls.push({ action, operation: body.operation });
      const result = await request.call(this, action, body, options);
      if (inject && action === "execute" && body.operation === "page.fill" && !trace.held) {
        const page = webContents.getAllWebContents().find(page => page.getURL() === "https://controller.fixture.test/" && page.debugger.isAttached());
        trace.effect = await page.executeJavaScript('({value:document.querySelector("input").value,inputs:window.fixtureInputs})');
        assert.deepEqual(trace.effect, { value: "Alice", inputs: 1 });
        const agent = globalThis.__crashAgent;
        trace.journal_path = path.join(agent.journal.root, trace.command.command_id + ".json");
        trace.local_dispatched = JSON.parse(await fs.readFile(trace.journal_path, "utf8"));
        assert.equal(trace.local_dispatched.state, "dispatched");
        trace.controller_pid = this.child.pid; trace.runtime_directory = this.directory;
        trace.held = true;
        await new Promise(resolve => { globalThis.__releaseCrashReply = resolve; });
      }
      return result;
    };
    const reverse = BrowserControllerForwarder.prototype.handleReverse;
    BrowserControllerForwarder.prototype.handleReverse = function(request, authority) {
      globalThis.__crashAgent = this;
      if (request.operation === "browser.controller.acquire") globalThis.__crashAcquire = structuredClone(request);
      if (request.body?.operation === "page.fill") trace.command = { command_id: request.body.command_id, digest: request.body.digest, binding: request.body.binding };
      return reverse.call(this, request, authority);
    };
    const fetch = net.fetch.bind(net);
    net.fetch = (...args) => { if (/^https?:/u.test(String(args[0]?.url || args[0]))) { trace.business_http++; throw Error("Business HTTP forbidden"); } return fetch(...args); };
    globalThis.fetch = () => { trace.business_http++; throw Error("Business HTTP forbidden"); };
    session.fromPartition("persist:sparkclaw-workbench-default").webRequest.onBeforeRequest({ urls: ["http://*/*", "https://*/*", "ws://*/*", "wss://*/*"] }, (_request, callback) => { trace.business_http++; callback({ cancel: true }); });
    await session.fromPartition("persist:sparkclaw-browser-default").protocol.handle("https", request => {
      assert.equal(new URL(request.url).origin, "https://controller.fixture.test");
      return new Response('<!doctype html><title>Main crash fixture</title><label>Display name<input aria-label="Display name"></label><script>window.fixtureInputs=0;document.querySelector("input").oninput=()=>window.fixtureInputs++</script>', { headers: { "content-type": "text/html" } });
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
    const agent = globalThis.__crashAgent, session = agent.session, trace = globalThis.__mainCrash;
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
    await browser.cookies.set({ url: "https://controller.fixture.test/", name: "controller-crash-marker", value: marker, expirationDate: Date.now() / 1000 + 86400 });
    await browser.cookies.flushStore();
  }, marker);
  const retained = await window.evaluate(async () => {
    const store = window.sparkclawClientStore, conversation = await store.create("Main crash retained draft");
    const draft = await store.draft(conversation.id);
    const file = await store.saveFile(conversation.id, "crash-retained.txt", new TextEncoder().encode("Retain this attachment through main-process SIGKILL.\n"));
    await store.saveDraft(conversation.id, "Retain this unsent draft.", [file.id], draft.revision, draft.scope_key);
    return { conversation: conversation.id, file: file.id };
  });
  const conversation = await window.evaluate(title => window.sparkclawClientStore.create(title), `Main crash ${Date.now()}`);
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
  await until(() => application.evaluate(() => globalThis.__mainCrash.held), "applied fill with dispatched journal");
  evidence.trace = await application.evaluate(() => globalThis.__mainCrash);
  originalAcquire = await application.evaluate(() => globalThis.__crashAcquire);
  assert.equal(evidence.trace.native_calls.filter(row => row.operation === "page.fill").length, 1);
  assert.equal(evidence.trace.business_http, 0);
  evidence.checks.fill_applied_once_while_durable_journal_still_dispatched = true;
  const immutable = async () => application.evaluate(({ app }, requestID) => {
    const db = new (process.getBuiltinModule("node:sqlite").DatabaseSync)(process.getBuiltinModule("node:path").join(app.getPath("userData"), "workbench/client.sqlite"), { readOnly: true });
    try { return { installation: db.prepare("SELECT value FROM metadata WHERE key='installation_id'").get().value,
      tasks: db.prepare("SELECT id,conversation_id,request_id,input_digest,context_json,created_at FROM tasks WHERE request_id=?").all(requestID) }; }
    finally { db.close(); }
  }, task.request_id);
  evidence.before_crash_identity = await immutable();
  assert.equal(evidence.before_crash_identity.tasks.length, 1);
  const controller = evidence.trace.controller_pid, runtimeDirectory = evidence.trace.runtime_directory;
  const groups = execFileSync("ps", ["-axo", "pid,pgid,command"], { encoding: "utf8" }).split("\n");
  assert.ok(groups.some(line => { const match = line.match(/^\s*(\d+)\s+(\d+)\s+(.*)$/u); return match && Number(match[1]) === controller && Number(match[2]) === controller && match[3].includes(resources + "/browser-controller/src/client-main.mjs"); }));
  assert.equal(await fs.realpath(path.dirname(runtimeDirectory)), path.join(await fs.realpath("/tmp"), `sparkx-${process.getuid()}`));
  assert.match(path.basename(runtimeDirectory), /^c-[A-Za-z0-9]+$/u);
  const owned = await fs.lstat(runtimeDirectory); assert.equal(owned.uid, process.getuid()); assert.equal(owned.mode & 0o077, 0); assert.equal(owned.isSymbolicLink(), false);
  const child = application.process();
  const exited = new Promise(resolve => child.once("exit", (code, signal) => resolve({ code, signal })));
  const started = performance.now(); child.kill("SIGKILL");
  evidence.main_exit = await exited; application = undefined; window = undefined;
  assert.equal(evidence.main_exit.signal, "SIGKILL");
  await until(() => !execFileSync("ps", ["-axo", "pgid="], { encoding: "utf8" }).split("\n").some(line => Number(line.trim()) === controller), "owned Controller/MCP process group exit", 8000);
  await assert.rejects(fs.lstat(runtimeDirectory), { code: "ENOENT" });
  evidence.main_exit.cleanup_observed_ms = Math.round(performance.now() - started);
  evidence.checks.SIGKILL_removes_owned_process_group_and_runtime_directory = true;
  evidence.cold_dispatched = JSON.parse(await fs.readFile(evidence.trace.journal_path, "utf8"));
  assert.deepEqual(evidence.cold_dispatched, evidence.trace.local_dispatched);
  evidence.checks.no_local_terminal_receipt_before_restart = true;
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
  evidence.restart_trace = await application.evaluate(() => globalThis.__mainCrash);
  assert.deepEqual(evidence.restart_trace.native_calls, []); assert.equal(evidence.restart_trace.business_http, 0);
  evidence.checks.original_task_identity_and_unknown_survive_without_native_replay = true;
  const cookies = await application.evaluate(({ session }) => session.fromPartition("persist:sparkclaw-browser-default").cookies.get({ name: "controller-crash-marker" }));
  assert.ok(cookies.some(cookie => cookie.value === marker));
  const file = await window.evaluate(id => window.sparkclawClientStore.readFile(id), retained.file);
  assert.equal(hash(Buffer.from(Object.values(file.bytes))), hash(Buffer.from("Retain this attachment through main-process SIGKILL.\n")));
  const draft = await window.evaluate(id => window.sparkclawClientStore.draft(id), retained.conversation);
  assert.equal(draft.content, "Retain this unsent draft."); assert.ok(draft.local_file_ids.includes(retained.file));
  assert.equal(await window.evaluate(() => fetch(window.sparkclawDesktop.gatewayBase + "/api/owner").then(response => response.status)), 200);
  evidence.checks.personal_cookie_attachment_draft_original_IPC_and_safe_ISCP_read_survive = true;
  await application.close(); application = undefined;
  evidence.final_local = JSON.parse(await fs.readFile(evidence.trace.journal_path, "utf8"));
  assert.equal(evidence.final_local.state, "unknown");
  for (const key of ["command_id", "digest", "installation_id", "local_conversation_id", "local_task_id", "owner_id", "client_id"]) assert.equal(evidence.final_local[key], evidence.cold_dispatched[key]);
  evidence.checks.restart_promotes_dispatched_to_durable_unknown_with_same_identity = true;
  evidence.passed = true;
} catch (error) { evidence.error = String(error?.stack || error); }
finally {
  if (application) {
    evidence.failure_trace ||= await application.evaluate(() => globalThis.__mainCrash).catch(() => null);
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
