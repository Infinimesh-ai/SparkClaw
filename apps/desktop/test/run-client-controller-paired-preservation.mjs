// Opt-in same-profile candidate upgrade/rollback proof. The operator switches
// only the labelled disposable Gateway while both application writers are off.
import assert from "node:assert/strict";
import fs from "node:fs/promises";
import path from "node:path";
import crypto from "node:crypto";
import { DatabaseSync } from "node:sqlite";
import { execFileSync } from "node:child_process";
import { pathToFileURL } from "node:url";
import { qualificationLaunchEnvironment, verifyQualificationAuthorization } from "./client-controller-qualification-launch.mjs";

const directory = process.env.SPARKCLAW_CONTROLLER_ISCP_LAB;
const executablePath = process.env.SPARKCLAW_CONTROLLER_CANDIDATE;
const phase = process.env.SPARKCLAW_CONTROLLER_PRESERVATION_PHASE;
assert.ok(path.isAbsolute(directory || "") && path.basename(directory).startsWith("sx-controller-iscp-"));
assert.ok(path.isAbsolute(executablePath || "") && !executablePath.startsWith("/Applications/"));
assert.ok(["prepare", "upgrade", "rollback"].includes(phase));
const resources = path.resolve(path.dirname(executablePath), process.platform === "darwin" ? "../Resources" : "resources");
const { _electron } = await import(pathToFileURL(path.join(resources, "browser-controller/node_modules/playwright-core/index.mjs")));
const profile = path.join(directory, "controller-native-profile");
const root = path.join(profile, "workbench");
const checkpointPath = path.join(directory, "evidence/controller-paired-checkpoint.json");
const upgradedCheckpointPath = path.join(directory, "evidence/controller-paired-upgraded-checkpoint.json");
const output = path.join(directory, `evidence/controller-paired-${phase}-${Date.now()}.json`);
const origin = "https://controller.fixture.test";
const attachmentText = "Synthetic attachment retained through paired candidate upgrade and rollback.\n";
const hash = bytes => crypto.createHash("sha256").update(bytes).digest("hex");
const evidence = { schema_version: 1, phase, scope: "actual_candidates_same_profile_and_encrypted_ISCP", passed: false, checks: {} };
let application, window, checkpoint;
const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
async function until(predicate, label, timeout = 60000) {
  const deadline = performance.now() + timeout;
  while (performance.now() < deadline) { if (await predicate()) return; await pause(100); }
  throw Error(`Paired preservation timeout: ${label}`);
}
async function retained() {
  const db = new DatabaseSync(path.join(root, "client.sqlite"), { readOnly: true });
  let data;
  try {
    data = {
      version: db.prepare("PRAGMA user_version").get().user_version,
      installation: db.prepare("SELECT value FROM metadata WHERE key='installation_id'").get().value,
      conversations: db.prepare("SELECT id,scope,created_at FROM conversations ORDER BY id").all(),
      messages: db.prepare("SELECT id,conversation_id,role,content,created_at FROM messages ORDER BY id").all(),
      tasks: db.prepare("SELECT id,conversation_id,request_id,input_digest,context_json,created_at FROM tasks ORDER BY id").all(),
      drafts: db.prepare("SELECT * FROM drafts ORDER BY scope,conversation_id").all(),
      cancellation: db.prepare("SELECT key,value FROM metadata WHERE key LIKE 'execution_cancel:%' ORDER BY key").all(),
    };
  } finally { db.close(); }
  data.journal = [];
  for (const name of (await fs.readdir(path.join(root, "browser-journal"))).filter(name => name.endsWith(".json")).sort())
    data.journal.push(JSON.parse(await fs.readFile(path.join(root, "browser-journal", name), "utf8")));
  data.files = [];
  async function walk(relative = "") {
    for (const entry of await fs.readdir(path.join(root, "files", relative), { withFileTypes: true })) {
      const name = path.join(relative, entry.name);
      assert.equal(entry.isSymbolicLink(), false);
      if (entry.isDirectory()) await walk(name);
      else data.files.push({ path: name, sha256: hash(await fs.readFile(path.join(root, "files", name))) });
    }
  }
  await walk(); data.files.sort((a, b) => a.path.localeCompare(b.path));
  return JSON.parse(JSON.stringify(data));
}
function requireRetained(before, after) {
  assert.equal(after.installation, before.installation); assert.equal(after.version, before.version);
  for (const [key, id] of [["conversations", "id"], ["messages", "id"], ["tasks", "id"], ["cancellation", "key"], ["journal", "command_id"], ["files", "path"]]) {
    const rows = new Map(after[key].map(row => [row[id], row]));
    for (const row of before[key]) assert.deepEqual(rows.get(row[id]), row, `retained ${key} ${row[id]}`);
  }
  assert.deepEqual(after.drafts.filter(row => row.conversation_id === checkpoint.draft_id), before.drafts.filter(row => row.conversation_id === checkpoint.draft_id));
}
try {
  const config = JSON.parse(await fs.readFile(path.join(directory, "gateway-config.json"), "utf8"));
  const catalog = JSON.parse(await fs.readFile(path.join(directory, "model.profiles.json"), "utf8"));
  assert.equal(typeof catalog.profiles[config.model.capacity_profile].mock, "boolean");
  evidence.provider = { capacity_profile: config.model.capacity_profile, mock: catalog.profiles[config.model.capacity_profile].mock };
  evidence.gateway_sha256 = hash(await fs.readFile(path.join(directory, "bin/gateway-linux")));
  evidence.app_asar_sha256 = hash(await fs.readFile(path.join(resources, "app.asar")));
  evidence.helper_sha256 = hash(await fs.readFile(path.join(resources, "iscp-workbench")));
  evidence.controller_manifest = JSON.parse(await fs.readFile(path.join(resources, "browser-controller/manifest.json"), "utf8"));
  if (phase === "prepare") await assert.rejects(fs.stat(checkpointPath), { code: "ENOENT" });
  else {
    checkpoint = JSON.parse(await fs.readFile(phase === "rollback" ? upgradedCheckpointPath : checkpointPath, "utf8"));
    requireRetained(checkpoint.retained, await retained());
    evidence.checks.cold_identity_history_draft_cancel_and_fences_preserved = true;
  }
  application = await _electron.launch({ executablePath, timeout: 45000, chromiumSandbox: true,
    env: qualificationLaunchEnvironment(directory) });
  application.process().stderr.on("data", bytes => process.stderr.write(bytes));
  evidence.runtime = await application.evaluate(({ app, safeStorage }) => ({ packaged: app.isPackaged, executable: process.execPath, profile: app.getPath("userData"), versions: process.versions, secure_storage: safeStorage.isEncryptionAvailable() }));
  assert.equal(evidence.runtime.packaged, true); assert.equal(evidence.runtime.executable, executablePath);
  assert.equal(evidence.runtime.profile, profile); assert.equal(evidence.runtime.secure_storage, true);
  await until(() => (window = application.windows().find(page => page.url() === "sparkclaw-app://workbench/index.html")), "workbench");
  await window.waitForSelector(".localWorkbench");
  await until(async () => (await window.evaluate(() => window.sparkclawDesktop.localConnection())).state === "connected", "connection");
  evidence.connection = await window.evaluate(() => window.sparkclawDesktop.localConnection());
  verifyQualificationAuthorization(evidence.connection);
  await application.evaluate(async ({ app, session, net, webContents }, { origin, inject }) => {
    const require = process.getBuiltinModule("node:module").createRequire(app.getAppPath() + "/package.json");
    const { ClientControllerRuntime } = require(app.getAppPath() + "/src/browser/controller-runtime.mjs");
    const { BrowserControllerForwarder } = require(app.getAppPath() + "/src/browser/controller-forwarder.mjs");
    const reverse = BrowserControllerForwarder.prototype.handleReverse;
    BrowserControllerForwarder.prototype.handleReverse = function(request, authority) {
      if (request.operation === "browser.controller.acquire") globalThis.__pairedForwarder = { agent: this, request: structuredClone(request), authority: structuredClone(authority) };
      return reverse.call(this, request, authority);
    };
    const request = ClientControllerRuntime.prototype.request;
    globalThis.__paired = { native_calls: [], business_http: 0, injected: false };
    ClientControllerRuntime.prototype.request = async function(action, body, options) {
      globalThis.__paired.native_calls.push({ action, operation: body.operation });
      const result = await request.call(this, action, body, options);
      if (inject && action === "execute" && body.operation === "page.fill" && !globalThis.__paired.injected) {
        const page = webContents.getAllWebContents().find(item => item.getURL() === origin + "/" && item.debugger.isAttached());
        globalThis.__paired.observed_value = await page.executeJavaScript('document.querySelector("input").value');
        globalThis.__paired.injected = true;
        throw Error("Qualification: lose local fill reply after observed native effect");
      }
      return result;
    };
    const original = net.fetch.bind(net);
    net.fetch = (...args) => { if (/^https?:/u.test(String(args[0]?.url || args[0]))) { globalThis.__paired.business_http++; throw Error("Business HTTP forbidden"); } return original(...args); };
    globalThis.fetch = () => { globalThis.__paired.business_http++; throw Error("Business HTTP forbidden"); };
    session.fromPartition("persist:sparkclaw-workbench-default").webRequest.onBeforeRequest({ urls: ["http://*/*", "https://*/*", "ws://*/*", "wss://*/*"] }, (_request, callback) => { globalThis.__paired.business_http++; callback({ cancel: true }); });
    await session.fromPartition("persist:sparkclaw-browser-default").protocol.handle("https", request => {
      if (new URL(request.url).origin !== origin) throw Error("Unexpected fixture destination");
      return new Response('<!doctype html><title>Paired candidate retention</title><label>Display name<input aria-label="Display name"></label><p id="draft">Empty draft</p><script>document.querySelector("input").oninput=e=>document.querySelector("#draft").textContent=e.target.value</script>', { headers: { "content-type": "text/html" } });
    });
  }, { origin, inject: phase === "prepare" });
  if (phase === "prepare") {
    const draft = await window.evaluate(async text => {
      const store = window.sparkclawClientStore, conversation = await store.create("Paired upgrade retained draft"), draft = await store.draft(conversation.id);
      const file = await store.saveFile(conversation.id, "paired-retained.txt", new TextEncoder().encode(text));
      await store.saveDraft(conversation.id, "Retain this unsent draft through upgrade and rollback.", [file.id], draft.revision, draft.scope_key);
      return { id: conversation.id, file_id: file.id };
    }, attachmentText);
    await application.evaluate(async ({ session }, origin) => { const browser = session.fromPartition("persist:sparkclaw-browser-default"); await browser.cookies.set({ url: origin, name: "controller-paired-marker", value: "preserved", expirationDate: Date.now() / 1000 + 86400 }); await browser.cookies.flushStore(); }, origin);
    checkpoint = { draft_id: draft.id, file_id: draft.file_id, file_sha256: hash(Buffer.from(attachmentText)) };
  } else {
    const cookies = await application.evaluate(({ session }) => session.fromPartition("persist:sparkclaw-browser-default").cookies.get({ name: "controller-paired-marker" }));
    assert.ok(cookies.some(row => row.value === "preserved"));
    await until(async () => {
      const host = (await window.evaluate(() => window.sparkclawDesktop.state())).browser_host;
      return host.state === "connected" && host.unknown_writes.some(row => row.command_id === checkpoint.unknown.command_id && row.digest === checkpoint.unknown.digest);
    }, "Controller readiness and persisted unknown fence");
    assert.deepEqual((await application.evaluate(() => globalThis.__paired)).native_calls, []);
    evidence.checks.reopen_exposes_unknown_without_automatic_native_replay = true;
    evidence.checks.personal_cookie_preserved = true;
  }
  const file = await window.evaluate(id => window.sparkclawClientStore.readFile(id), checkpoint.file_id);
  assert.equal(hash(Buffer.from(Object.values(file.bytes))), checkpoint.file_sha256);
  const draft = await window.evaluate(id => window.sparkclawClientStore.draft(id), checkpoint.draft_id);
  assert.equal(draft.content, "Retain this unsent draft through upgrade and rollback.");
  assert.ok(draft.local_file_ids.includes(checkpoint.file_id));
  evidence.checks.original_file_and_draft_IPC_preserve_attachment_bytes = true;
  // A separate explicit task stays available; the unresolved original is never
  // retried or reconciled merely to allow the candidate comparison to pass.
  const conversation = await window.evaluate(title => window.sparkclawClientStore.create(title), `Paired ${phase} ${Date.now()}`);
  await window.evaluate(id => window.sparkclawDesktop.selectConversation(id), conversation.id);
  const task = await window.evaluate(async ({ id, prompt }) => { const store = window.sparkclawClientStore, task = await store.enqueue(id, prompt, []); await store.submit(task.request_id); return task; }, { id: conversation.id, prompt: phase === "prepare" ? `在 ${origin}/ 页面 Display name 字段填写 Alice。` : `打开 ${origin}/ 网页` });
  let state;
  await until(async () => {
    await window.evaluate(id => window.sparkclawClientStore.reconcile(id), task.request_id);
    state = await window.evaluate(id => window.sparkclawClientStore.read(id), conversation.id);
    const current = state.tasks.find(row => row.request_id === task.request_id), approval = current?.approvals?.find(row => row.state === "pending" && row.actionable);
    if (approval) { assert.equal(phase, "prepare"); assert.equal(approval.tool, "browser.type"); await window.evaluate(({ id, approval }) => window.sparkclawClientStore.decideApproval(id, approval.approval_id, approval.digest, "approve"), { id: task.request_id, approval }); }
    return ["delivered", "failed", "canceled", "unknown"].includes(current?.status);
  }, "task terminal", 90000);
  evidence.task = state;
  evidence.observer = await application.evaluate(() => globalThis.__paired);
  assert.equal(evidence.observer.business_http, 0);
  if (phase === "prepare") {
    assert.equal(evidence.observer.injected, true); assert.equal(evidence.observer.observed_value, "Alice");
    assert.equal(state.tasks.find(row => row.request_id === task.request_id).status, "unknown");
    await until(async () => (await retained()).journal.some(row => row.state === "unknown" && row.local_task_id === task.id), "durable unknown");
    checkpoint.unknown = (await retained()).journal.find(row => row.state === "unknown" && row.local_task_id === task.id);
    evidence.checks.applied_fill_lost_reply_retained_as_unknown = true;
  } else {
    assert.equal(evidence.observer.native_calls.filter(row => row.operation === "page.fill").length, 0);
    assert.equal(state.tasks.find(row => row.request_id === task.request_id).status, "delivered");
    assert.equal(state.messages.some(row => row.role === "assistant" && /workflow (?:stopped|is blocked)/iu.test(row.content)), false);
    const pages = await application.evaluate(({ webContents }, origin) => webContents.getAllWebContents().filter(page => page.getURL() === origin + "/").map(page => ({ sandbox: page.getLastWebPreferences().sandbox, debugger: page.debugger.isAttached() })), origin);
    assert.equal(pages.length, 1); assert.ok(pages.every(page => page.sandbox && !page.debugger));
    requireRetained(checkpoint.retained, await retained());
    evidence.checks.explicit_new_task_completes_with_original_unknown_retained = true;
  }
  const markers = (await retained()).cancellation; assert.ok(markers.length > 0);
  evidence.cancel_marker_count = markers.length;
  evidence.fresh_command_probes = await application.evaluate(async ({ app }, unknown) => {
    const require = process.getBuiltinModule("node:module").createRequire(app.getAppPath() + "/package.json");
    const { controllerDigest } = require(app.getAppPath() + "/src/browser/controller-wire.mjs");
    const { DatabaseSync } = process.getBuiltinModule("node:sqlite"), { join } = process.getBuiltinModule("node:path");
    const db = new DatabaseSync(join(app.getPath("userData"), "workbench/client.sqlite"), { readOnly: true });
    let canceled;
    try { canceled = db.prepare("SELECT t.id,t.conversation_id FROM tasks t JOIN metadata m ON m.key='execution_cancel:' || t.id ORDER BY t.created_at DESC LIMIT 1").get(); }
    finally { db.close(); }
    const captured = globalThis.__pairedForwarder, agent = captured.agent;
    const results = [];
    for (const [reason, task, conversation] of [["unknown", unknown.local_task_id, unknown.local_conversation_id], ["canceled", canceled.id, canceled.conversation_id]]) {
      const request = structuredClone(captured.request);
      request.body.command_id = process.getBuiltinModule("node:crypto").randomUUID();
      request.body.binding.local_task_id = task; request.body.binding.local_conversation_id = conversation;
      request.body.digest = controllerDigest(request.operation, request.body);
      const calls = globalThis.__paired.native_calls.length;
      const result = await agent.handleReverse(request, captured.authority);
      results.push({ reason, response: result, native_calls_added: globalThis.__paired.native_calls.length - calls });
    }
    return results;
  }, checkpoint.unknown);
  for (const probe of evidence.fresh_command_probes) {
    assert.equal(probe.response.status, 200); assert.equal(probe.response.body.outcome, "not_dispatched");
    assert.equal(probe.response.body.error_code, probe.reason === "unknown" ? "browser_write_outcome_unknown" : "command_fenced");
    assert.equal(probe.native_calls_added, 0);
  }
  evidence.checks.packaged_forwarder_refuses_fresh_ids_for_unknown_and_canceled_tasks = true;
  await application.close(); application = undefined;
  const after = await retained();
  if (phase === "prepare") {
    checkpoint.retained = after;
    await fs.writeFile(checkpointPath, JSON.stringify(checkpoint, null, 2) + "\n", { mode: 0o600, flag: "wx" });
  } else {
    requireRetained(checkpoint.retained, after);
    if (phase === "upgrade") await fs.writeFile(upgradedCheckpointPath, JSON.stringify({ ...checkpoint, retained: after }, null, 2) + "\n", { mode: 0o600, flag: "wx" });
  }
  evidence.checks.clean_quit_preserves_cold_data = true;
  evidence.passed = true;
} catch (error) {
  evidence.error = String(error?.stack || error);
  if (window) evidence.desktop_on_failure = await window.evaluate(() => window.sparkclawDesktop.state()).catch(() => null);
}
finally {
  if (application) await application.close().catch(error => { evidence.quit_error = String(error); evidence.passed = false; });
  const children = execFileSync("ps", ["-axo", "command="], { encoding: "utf8" }).split("\n").filter(line => line.includes(resources + "/browser-controller/"));
  evidence.checks.no_owned_children_after_quit = children.length === 0;
  if (children.length) evidence.passed = false;
  await fs.writeFile(output, JSON.stringify(evidence, null, 2) + "\n", { mode: 0o600, flag: "wx" });
  console.log(JSON.stringify({ passed: evidence.passed, phase, output, checks: evidence.checks, error: evidence.error }));
  if (!evidence.passed) process.exitCode = 1;
}
