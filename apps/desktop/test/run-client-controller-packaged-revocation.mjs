// Opt-in actual-package permanent revocation on a newly enrolled disposable device.
// Hold a real applied-fill reply; never revoke an existing/shared qualification identity.
import assert from "node:assert/strict";
import fs from "node:fs/promises";
import path from "node:path";
import crypto from "node:crypto";
import { execFileSync } from "node:child_process";
import { pathToFileURL } from "node:url";
import { qualificationLaunchEnvironment, verifyQualificationAuthorization, synchronizeQualificationFences } from "./client-controller-qualification-launch.mjs";

const directory = process.env.SPARKCLAW_CONTROLLER_ISCP_LAB;
const executablePath = process.env.SPARKCLAW_CONTROLLER_CANDIDATE;
assert.ok(path.isAbsolute(directory || "") && path.basename(directory).startsWith("sx-controller-iscp-revoke-"));
assert.ok(path.isAbsolute(executablePath || "") && !executablePath.startsWith("/Applications/"));
assert.equal(process.env.SPARKCLAW_CONTROLLER_NODE_MANAGED, "1");
const metadataPath = path.join(directory, "revocation.json");
const metadata = JSON.parse(await fs.readFile(metadataPath, "utf8"));
assert.equal(metadata.purpose, "permanent_revocation_disposable_device");
assert.equal(metadata.profile_parent, directory);
assert.match(metadata.expected_client_id, /^[A-Za-z0-9_-]+$/u);
const credentialPath = process.env.SPARKCLAW_CONTROLLER_REVOCATION_CREDENTIAL;
assert.ok(path.isAbsolute(credentialPath || ""));
const credentialInfo = await fs.lstat(credentialPath);
assert.ok(credentialInfo.isFile() && !credentialInfo.isSymbolicLink());
assert.equal(credentialInfo.uid, process.getuid()); assert.equal(credentialInfo.mode & 0o077, 0);
const credential = (await fs.readFile(credentialPath, "utf8")).trim();
assert.equal(crypto.createHash("sha256").update(credential).digest("hex"), metadata.credential_sha256);
assert.ok(credential.startsWith("sparkclaw-iscp-connect-v1."));
// A new root is required. A failed run retains its profile and must be diagnosed
// explicitly; this fixture cannot silently reset or reuse another identity.
await assert.rejects(fs.lstat(path.join(directory, "controller-native-profile")), { code: "ENOENT" });
const resources = path.resolve(path.dirname(executablePath), process.platform === "darwin" ? "../Resources" : "resources");
const { _electron } = await import(pathToFileURL(path.join(resources, "browser-controller/node_modules/playwright-core/index.mjs")));
const output = path.join(directory, `evidence/controller-packaged-revocation-${Date.now()}.json`);
const evidence = { schema_version: 1, passed: false, checks: {},
  scope: "actual_package_disposable_node_authorization_revocation_after_fill_before_local_terminal", local_probes_are_not_wire_calls: true };
const hash = bytes => crypto.createHash("sha256").update(bytes).digest("hex");
const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
let application, window, originalFill, originalAuthority;
async function until(check, label, timeout = 90000) {
  const end = performance.now() + timeout;
  while (performance.now() < end) { if (await check()) return; await pause(100); }
  throw Error(`Packaged revocation timeout: ${label}`);
}
async function launch(initial) {
  const inject = initial;
  application = await _electron.launch({ executablePath, timeout: 45000, chromiumSandbox: true,
    env: qualificationLaunchEnvironment(directory) });
  application.process().stderr.on("data", bytes => process.stderr.write(bytes));
  await application.evaluate(async ({ app, net, session, webContents }, inject) => {
    const require = process.getBuiltinModule("node:module").createRequire(app.getAppPath() + "/package.json");
    const assert = require("node:assert/strict"), fs = require("node:fs/promises"), path = require("node:path");
    const { BrowserControllerForwarder } = require(app.getAppPath() + "/src/browser/controller-forwarder.mjs");
    const { ClientControllerRuntime } = require(app.getAppPath() + "/src/browser/controller-runtime.mjs");
    const trace = globalThis.__revocationTrace = { native_calls: [], acquisitions: [], business_http: 0, held: false };
    const snapshot = BrowserControllerForwarder.prototype.snapshot;
    BrowserControllerForwarder.prototype.snapshot = function(...args) { globalThis.__revocationAgent = this; return snapshot.apply(this, args); };
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
        trace.journal_path = path.join(globalThis.__revocationAgent.journal.root, trace.command.command_id + ".json");
        trace.local_dispatched = JSON.parse(await fs.readFile(trace.journal_path, "utf8"));
        assert.equal(trace.local_dispatched.state, "dispatched");
        trace.controller_pid = this.child.pid; trace.runtime_directory = this.directory;
        trace.incarnation = this.incarnation; trace.old_session = globalThis.__revocationAgent.session.session_id;
        trace.held = true;
        await new Promise(resolve => { globalThis.__releaseRevocationReply = resolve; });
        trace.old_native_reply_released = true;
      }
      return response;
    };
    const reverse = BrowserControllerForwarder.prototype.handleReverse;
    BrowserControllerForwarder.prototype.handleReverse = async function(request, authority) {
      globalThis.__revocationAgent = this;
      if (request.operation === "browser.controller.acquire" && !globalThis.__revocationAcquire) globalThis.__revocationAcquire = structuredClone(request);
      const originalFill = request.body?.operation === "page.fill" && !trace.command;
      if (originalFill) {
        trace.command = { command_id: request.body.command_id, digest: request.body.digest, binding: request.body.binding };
        globalThis.__revocationFill = structuredClone(request); globalThis.__revocationAuthority = structuredClone(authority);
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
      return new Response('<!doctype html><title>Revocation fixture</title><label>Display name<input aria-label="Display name"></label><script>window.fixtureInputs=0;document.querySelector("input").oninput=()=>window.fixtureInputs++</script>', { headers: { "content-type": "text/html" } });
    });
  }, inject);
  await until(() => (window = application.windows().find(page => page.url() === "sparkclaw-app://workbench/index.html")), "workbench");
  if (initial) {
    await window.waitForSelector(".desktopLogin");
    await window.evaluate(value => {
      const input = document.querySelector("#desktopCredential");
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value").set.call(input, value);
      input.dispatchEvent(new Event("input", { bubbles: true }));
    }, credential);
    await window.locator(".desktopLogin form button[type=submit]").click();
    await until(async () => (await window.evaluate(() => window.sparkclawDesktop.localConnection())).state === "connected", "native first enrollment", 150000);
    const connection = await window.evaluate(() => window.sparkclawDesktop.localConnection());
    verifyQualificationAuthorization(connection);
    assert.equal(connection.client_id, metadata.expected_client_id);
    evidence.connection_before = connection;
    await window.waitForSelector(".localWorkbench");
    await until(async () => (await window.evaluate(() => window.sparkclawDesktop.state())).browser_host.state === "connected", "Controller readiness");
    await synchronizeQualificationFences(application);
  } else {
    await window.waitForSelector(".desktopLogin");
    await until(async () => (await window.evaluate(() => window.sparkclawDesktop.localConnection())).authorization_deletion?.state === "revoked", "persisted revocation");
  }
  const runtime = await application.evaluate(({ app, safeStorage }) => ({ packaged: app.isPackaged, executable: process.execPath,
    profile: app.getPath("userData"), versions: process.versions, secure_storage: safeStorage.isEncryptionAvailable() }));
  assert.ok(runtime.packaged && runtime.secure_storage); assert.equal(runtime.executable, executablePath);
  assert.equal(runtime.profile, path.join(directory, "controller-native-profile"));
  evidence.runtime ||= runtime; assert.equal(runtime.profile, evidence.runtime.profile);
}
async function retainedData() {
  return application.evaluate(async ({ app }) => {
    const fs = process.getBuiltinModule("node:fs/promises"), path = process.getBuiltinModule("node:path"), crypto = process.getBuiltinModule("node:crypto");
    const root = path.join(app.getPath("userData"), "workbench");
    const db = new (process.getBuiltinModule("node:sqlite").DatabaseSync)(path.join(root, "client.sqlite"), { readOnly: true });
    try {
      const files = db.prepare("SELECT * FROM files ORDER BY id").all();
      return {
        installation: db.prepare("SELECT value FROM metadata WHERE key='installation_id'").get().value,
        conversations: db.prepare("SELECT id,scope,created_at FROM conversations ORDER BY id").all(),
        messages: db.prepare("SELECT id,conversation_id,role,content,created_at FROM messages ORDER BY id").all(),
        tasks: db.prepare("SELECT id,conversation_id,request_id,input_digest,context_json,created_at FROM tasks ORDER BY id").all(),
        drafts: db.prepare("SELECT * FROM drafts ORDER BY scope,conversation_id").all(),
        files,
        bytes: await Promise.all(files.map(async row => ({ id: row.id, sha256: crypto.createHash("sha256").update(await fs.readFile(path.join(root, "files", row.id))).digest("hex") }))),
      };
    } finally { db.close(); }
  });
}
async function cookieRetained(marker) {
  const cookies = await application.evaluate(({ session }) => session.fromPartition("persist:sparkclaw-browser-default").cookies.get({ name: "controller-revocation-marker" }));
  assert.ok(cookies.some(cookie => cookie.value === marker));
}
async function readDeletion() {
  const root = path.join(directory, "controller-native-profile");
  const files = (await fs.readdir(root)).filter(name => /^authorization-deletion\.[a-f0-9]+\.json$/u.test(name));
  assert.equal(files.length, 1);
  const intent = JSON.parse(await fs.readFile(path.join(root, files[0]), "utf8"));
  assert.equal(intent.state, "revoked"); assert.equal(intent.receipt.authorization_revision, intent.expected_revision + 1);
  assert.equal(intent.receipt.operation_id, intent.operation_id);
  return intent;
}
async function protectedCallsRefused() {
  assert.equal(await window.evaluate(() => window.sparkclawDesktop.state().then(() => false, () => true)), true);
  assert.equal(await window.evaluate(() => fetch(window.sparkclawDesktop.gatewayBase + "/api/owner").then(response => !response.ok, () => true)), true);
  assert.equal((await window.evaluate(() => window.sparkclawDesktop.retryLocalConnection())).state, "invalid_authentication");
}
try {
  const configBytes = await fs.readFile(path.join(directory, "gateway-config.json")), catalogBytes = await fs.readFile(path.join(directory, "model.profiles.json"));
  const config = JSON.parse(configBytes), catalog = JSON.parse(catalogBytes);
  evidence.provider = { mock: catalog.profiles[config.model.capacity_profile].mock, config_sha256: hash(configBytes), catalog_sha256: hash(catalogBytes) };
  assert.equal(evidence.provider.mock, false);
  evidence.asar_sha256 = hash(await fs.readFile(path.join(resources, "app.asar")));
  assert.equal(evidence.asar_sha256, metadata.candidate_asar_sha256);
  await launch(true);
  evidence.unknown_before = await synchronizeQualificationFences(application);
  assert.deepEqual(evidence.unknown_before, []);
  const selected = JSON.parse(await fs.readFile(path.join(directory, "controller-native-profile/iscp-launch-profile.json"), "utf8"));
  const profile = JSON.parse(await fs.readFile(selected.profile_path, "utf8"));
  const helper = JSON.parse(await fs.readFile(profile.helper_config, "utf8"));
  assert.equal(helper.identity_key_backend, "keyring");
  evidence.native_identity = { client_id: evidence.connection_before.client_id, installation_id: helper.expected_installation_id,
    device_id: evidence.connection_before.backend.initiator_device_id, key_backend: helper.identity_key_backend };
  evidence.checks.fresh_disposable_native_keyring_identity_and_real_node_connection = true;
  const marker = crypto.randomUUID();
  await application.evaluate(async ({ session }, marker) => {
    const browser = session.fromPartition("persist:sparkclaw-browser-default");
    await browser.cookies.set({ url: "https://controller.fixture.test/", name: "controller-revocation-marker", value: marker, expirationDate: Date.now() / 1000 + 86400 });
    await browser.cookies.flushStore();
  }, marker);
  await window.evaluate(async () => {
    const store = window.sparkclawClientStore, conversation = await store.create("Revocation retained draft"), draft = await store.draft(conversation.id);
    const file = await store.saveFile(conversation.id, "retained.txt", new TextEncoder().encode("Preserve this attachment after device revocation.\n"));
    await store.saveDraft(conversation.id, "Keep this unsent draft after revocation.", [file.id], draft.revision, draft.scope_key);
  });
  const personal = (await window.evaluate(() => window.sparkclawDesktop.createPersonal())).page_ref;
  evidence.task = await window.evaluate(async () => {
    const store = window.sparkclawClientStore, conversation = await store.create("Disposable device revocation");
    await window.sparkclawDesktop.selectConversation(conversation.id);
    const task = await store.enqueue(conversation.id, "在 https://controller.fixture.test/ 页面 Display name 字段填写 Alice。", []);
    await store.submit(task.request_id);
    return { request_id: task.request_id, local_task_id: task.id, conversation_id: conversation.id };
  });
  let approval;
  await until(async () => {
    await window.evaluate(id => window.sparkclawClientStore.reconcile(id), evidence.task.request_id);
    const state = await window.evaluate(id => window.sparkclawClientStore.read(id), evidence.task.conversation_id);
    evidence.last_task_state = state;
    approval = state.tasks.find(row => row.request_id === evidence.task.request_id)?.approvals?.find(row => row.state === "pending" && row.actionable);
    return Boolean(approval);
  }, "original fill approval");
  assert.equal(approval.tool, "browser.type");
  await window.evaluate(({ task, approval }) => window.sparkclawClientStore.decideApproval(task.request_id, approval.approval_id, approval.digest, "approve"), { task: evidence.task, approval });
  await until(() => application.evaluate(() => globalThis.__revocationTrace.held), "applied fill and real reply held");
  evidence.trace = await application.evaluate(() => globalThis.__revocationTrace);
  originalFill = await application.evaluate(() => globalThis.__revocationFill);
  originalAuthority = await application.evaluate(() => globalThis.__revocationAuthority);
  assert.equal(evidence.trace.native_calls.filter(row => row.operation === "page.fill").length, 1);
  evidence.retained_before = await retainedData();
  assert.equal(evidence.retained_before.installation, evidence.native_identity.installation_id);
  const controller = evidence.trace.controller_pid, runtimeDirectory = evidence.trace.runtime_directory;
  const rows = execFileSync("ps", ["-axo", "pid,ppid,pgid,command"], { encoding: "utf8" }).split("\n")
    .map(line => line.match(/^\s*(\d+)\s+(\d+)\s+(\d+)\s+(.*)$/u)).filter(Boolean);
  const owner = rows.find(row => Number(row[1]) === controller);
  assert.ok(owner && Number(owner[2]) === application.process().pid && Number(owner[3]) === controller && owner[4].includes(resources + "/browser-controller/src/client-main.mjs"));
  assert.equal(await fs.realpath(path.dirname(runtimeDirectory)), path.join(await fs.realpath("/tmp"), "sparkx-" + process.getuid()));
  assert.match(path.basename(runtimeDirectory), /^c-[A-Za-z0-9]+$/u);
  const info = await fs.lstat(runtimeDirectory); assert.equal(info.uid, process.getuid()); assert.equal(info.mode & 0o077, 0); assert.equal(info.isSymbolicLink(), false);
  evidence.checks.approved_fill_once_before_local_terminal_with_real_success_reply_held = true;
  await window.locator(".sidebarAccountTrigger").click();
  await window.locator(".sidebarAccountMenuItem").filter({ hasText: /settings|设置/i }).click();
  await window.waitForSelector(".settingsPageNavigation");
  await window.locator(".settingsPageNavigation button").filter({ hasText: /devices|设备/i }).click();
  await window.getByRole("button", { name: /^(Delete this device authorization|删除此设备授权)$/u }).click();
  await window.getByRole("button", { name: /^(Confirm deletion|确认删除授权)$/u }).click();
  await until(async () => (await window.evaluate(() => window.sparkclawDesktop.localConnection())).authorization_deletion?.state === "revoked", "signed authorization deletion", 90000);
  evidence.connection_after = await window.evaluate(() => window.sparkclawDesktop.localConnection());
  assert.equal(evidence.connection_after.state, "invalid_authentication");
  evidence.deletion = await readDeletion();
  assert.equal(evidence.connection_after.authorization_deletion.operation_id, evidence.deletion.operation_id);
  await until(() => application.evaluate(() => globalThis.__revocationAgent.runtime.child === null && globalThis.__revocationAgent.runtime.state === "stopped" && globalThis.__revocationAgent.lease === null), "owned execution stopped");
  await until(() => !execFileSync("ps", ["-axo", "pgid="], { encoding: "utf8" }).split("\n").some(row => Number(row.trim()) === controller), "owned process group exit", 8000);
  await assert.rejects(fs.lstat(runtimeDirectory), { code: "ENOENT" });
  assert.equal(await application.evaluate(({ webContents }) => webContents.getAllWebContents().filter(page => page.getURL() === "https://controller.fixture.test/").length), 0);
  assert.equal(await application.evaluate((_electron, ref) => globalThis.__revocationAgent.registry.snapshot().some(page => page.page_ref === ref && page.role === "personal"), personal), true);
  await protectedCallsRefused();
  await window.waitForFunction(() => /This device authorization was deleted|此设备授权已删除/u.test(document.body.textContent));
  evidence.checks.original_settings_delete_receipt_retires_owned_execution_and_preserves_personal_page = true;
  await application.evaluate(() => globalThis.__releaseRevocationReply());
  await until(() => application.evaluate(() => Boolean(globalThis.__revocationTrace.old_reverse_reply)), "late real reply settles");
  evidence.final_trace = await application.evaluate(() => globalThis.__revocationTrace);
  assert.equal(evidence.final_trace.old_reverse_reply.outcome, "unknown");
  evidence.unknown = JSON.parse(await fs.readFile(evidence.trace.journal_path, "utf8"));
  assert.equal(evidence.unknown.state, "unknown"); assert.equal(evidence.unknown.digest, evidence.trace.command.digest);
  assert.equal(evidence.unknown.local_task_id, evidence.task.local_task_id);
  evidence.old_authority = await application.evaluate(async (_electron, { request, authority }) => {
    const before = globalThis.__revocationTrace.native_calls.length;
    const response = await globalThis.__revocationAgent.handleReverse(request, authority);
    return { status: response.status, calls_added: globalThis.__revocationTrace.native_calls.length - before };
  }, { request: originalFill, authority: originalAuthority });
  assert.deepEqual(evidence.old_authority, { status: 503, calls_added: 0 });
  assert.equal(evidence.final_trace.native_calls.filter(row => row.operation === "page.fill").length, 1);
  assert.equal(evidence.final_trace.business_http, 0);
  assert.deepEqual(await retainedData(), evidence.retained_before); await cookieRetained(marker);
  evidence.checks.late_success_is_unknown_and_old_authority_cannot_replay_native_write = true;
  await fs.writeFile(path.join(directory, "evidence/revoked-workbench.png"), await window.screenshot(), { mode: 0o600, flag: "wx" });
  await application.close(); application = undefined;
  await launch(false);
  assert.equal((await window.evaluate(() => window.sparkclawDesktop.localConnection())).state, "invalid_authentication");
  assert.deepEqual(await readDeletion(), evidence.deletion);
  assert.deepEqual(JSON.parse(await fs.readFile(evidence.trace.journal_path, "utf8")), evidence.unknown);
  assert.deepEqual(await retainedData(), evidence.retained_before); await cookieRetained(marker);
  await protectedCallsRefused();
  assert.equal(await window.evaluate(() => window.sparkclawDesktop.checkNewAuthorization().then(() => false, () => true)), true);
  assert.equal((await window.evaluate(() => window.sparkclawDesktop.localConnection())).state, "invalid_authentication");
  evidence.restart_trace = await application.evaluate(() => globalThis.__revocationTrace);
  assert.deepEqual(evidence.restart_trace.native_calls, []); assert.equal(evidence.restart_trace.business_http, 0);
  evidence.checks.revoked_restart_retains_receipt_identity_history_draft_file_cookie_and_unknown_without_replay = true;
  evidence.passed = true;
} catch (error) { evidence.error = String(error?.stack || error).replaceAll(credential, "[redacted]"); }
finally {
  if (application) {
    evidence.failure_connection = await window?.evaluate(() => window.sparkclawDesktop.localConnection()).catch(() => null);
    evidence.last_trace = await application.evaluate(() => globalThis.__revocationTrace).catch(() => null);
    await application.evaluate(() => globalThis.__releaseRevocationReply?.()).catch(() => {});
    await application.close().catch(error => { evidence.quit_error = String(error); evidence.passed = false; });
  }
  const children = execFileSync("ps", ["-axo", "command="], { encoding: "utf8" }).split("\n").filter(line => line.includes(resources + "/browser-controller/"));
  evidence.checks.no_owned_controller_children = children.length === 0;
  if (children.length) evidence.passed = false;
  await fs.writeFile(output, JSON.stringify(evidence, null, 2) + "\n", { mode: 0o600, flag: "wx" });
  console.log(JSON.stringify({ passed: evidence.passed, output, checks: evidence.checks, error: evidence.error }));
  if (!evidence.passed) process.exitCode = 1;
}
