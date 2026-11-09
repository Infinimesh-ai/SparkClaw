// Opt-in native package corruption/recovery proof. Only an explicitly prepared
// private candidate copy may be missing a manifest or have a changed module.
import assert from "node:assert/strict";
import fs from "node:fs/promises";
import path from "node:path";
import crypto from "node:crypto";
import { DatabaseSync } from "node:sqlite";
import { execFileSync } from "node:child_process";
import { pathToFileURL } from "node:url";

const directory = process.env.SPARKCLAW_CONTROLLER_ISCP_LAB;
const executablePath = process.env.SPARKCLAW_CONTROLLER_CANDIDATE;
const phase = process.env.SPARKCLAW_CONTROLLER_UNAVAILABILITY_PHASE;
const reuseCheckpoint = process.env.SPARKCLAW_CONTROLLER_UNAVAILABILITY_REUSE_CHECKPOINT === "1";
assert.ok(path.isAbsolute(directory || "") && path.basename(directory).startsWith("sx-controller-iscp-"));
assert.ok(path.isAbsolute(executablePath || "") && !executablePath.startsWith("/Applications/"));
assert.ok(["missing", "corrupt", "recovered", "rollback", "legacy_peer", "legacy_client"].includes(phase));
const unavailable = ["missing", "corrupt", "legacy_peer", "legacy_client"].includes(phase);
if (["missing", "corrupt"].includes(phase)) assert.ok(executablePath.includes("controller-unavailable-"));
const resources = path.resolve(path.dirname(executablePath), process.platform === "darwin" ? "../Resources" : "resources");
// Pre-Controller packages have no bundled Playwright observer. Keep their
// original resources intact and load the observer from an explicit new package.
const observerResources = process.env.SPARKCLAW_CONTROLLER_OBSERVER_RESOURCES || resources;
assert.ok(path.isAbsolute(observerResources));
const { _electron } = await import(pathToFileURL(path.join(observerResources, "browser-controller/node_modules/playwright-core/index.mjs")));
const profile = path.join(directory, "controller-native-profile");
const checkpointFile = path.join(directory, "evidence/controller-unavailability-checkpoint.json");
const output = path.join(directory, `evidence/controller-unavailability-${phase}-${Date.now()}.json`);
const text = "Synthetic retained attachment: new candidate to recovered and rollback packages.\n";
const hash = bytes => crypto.createHash("sha256").update(bytes).digest("hex");
const evidence = { schema_version: 1, phase, reuse_checkpoint: reuseCheckpoint, observer_resources: observerResources, platform: process.platform, scope: "actual_candidate_encrypted_ISCP", passed: false, checks: {} };
let application, window, checkpoint;
const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
async function until(predicate, label) {
  const deadline = performance.now() + 60000;
  while (performance.now() < deadline) { if (await predicate()) return; await pause(100); }
  throw Error(`Unavailable candidate timeout: ${label}`);
}
async function retained() {
  const db = new DatabaseSync(path.join(profile, "workbench/client.sqlite"), { readOnly: true });
  let rows;
  try {
    rows = { installation: db.prepare("SELECT value FROM metadata WHERE key='installation_id'").get().value,
      cancellation: db.prepare("SELECT key,value FROM metadata WHERE key LIKE 'execution_cancel:%' ORDER BY key").all() };
  } finally { db.close(); }
  rows.unknown = [];
  const root = path.join(profile, "workbench/browser-journal");
  for (const name of (await fs.readdir(root)).filter(name => name.endsWith(".json")).sort()) {
    const row = JSON.parse(await fs.readFile(path.join(root, name), "utf8"));
    if (row.state === "unknown") rows.unknown.push(row);
  }
  return JSON.parse(JSON.stringify(rows));
}
try {
  evidence.gateway_sha256 = hash(await fs.readFile(path.join(directory, "bin/gateway-linux")));
  evidence.asar_sha256 = hash(await fs.readFile(path.join(resources, "app.asar")));
  const manifest = await fs.readFile(path.join(resources, "browser-controller/manifest.json")).catch(error => { if (error.code === "ENOENT") return null; throw error; });
  evidence.controller_manifest_sha256 = manifest && hash(manifest);
  evidence.before = await retained();
  if (phase === "missing" && !reuseCheckpoint) await assert.rejects(fs.stat(checkpointFile), { code: "ENOENT" });
  else checkpoint = JSON.parse(await fs.readFile(checkpointFile, "utf8"));
  application = await _electron.launch({ executablePath, timeout: 45000, chromiumSandbox: true, env: { ...process.env,
    SPARKCLAW_DESKTOP_ISCP_LOCAL_TEST: "1", SPARKCLAW_DESKTOP_ISCP_CONFIG: path.join(directory, "desktop-profile.json"), SPARKCLAW_DESKTOP_USER_DATA_DIR: profile } });
  application.process().stderr.on("data", bytes => process.stderr.write(bytes));
  evidence.runtime = await application.evaluate(({ app, safeStorage }) => ({ packaged: app.isPackaged, executable: process.execPath, profile: app.getPath("userData"), versions: process.versions, secure_storage: safeStorage.isEncryptionAvailable() }));
  assert.equal(evidence.runtime.packaged, true); assert.equal(evidence.runtime.profile, profile); assert.equal(evidence.runtime.secure_storage, true);
  await until(() => (window = application.windows().find(page => page.url() === "sparkclaw-app://workbench/index.html")), "workbench");
  await window.waitForSelector(".localWorkbench");
  await until(async () => (await window.evaluate(() => window.sparkclawDesktop.localConnection())).state === "connected", "ISCP connection");
  await until(async () => (await window.evaluate(() => window.sparkclawDesktop.localConnection())).capabilities?.browser === !unavailable, "expected browser capability");
  evidence.connection = await window.evaluate(() => window.sparkclawDesktop.localConnection());
  assert.equal(evidence.connection.capabilities.files, true); assert.equal(evidence.connection.capabilities.settings, true);
  const button = window.getByRole("button", { name: /^(打开浏览器|Open browser)$/u });
  assert.equal(await button.count(), 1); assert.equal(await button.isDisabled(), unavailable);
  assert.ok(await window.locator(".sessionSelect").count() > 0);
  evidence.checks.original_entry_and_history_retained_with_correct_disabled_state = true;
  await application.evaluate(({ net, session }) => {
    globalThis.__unavailableBusinessHTTP = 0;
    const original = net.fetch.bind(net);
    net.fetch = (...args) => { if (/^https?:/u.test(String(args[0]?.url || args[0]))) { globalThis.__unavailableBusinessHTTP++; throw Error("Business HTTP forbidden"); } return original(...args); };
    globalThis.fetch = () => { globalThis.__unavailableBusinessHTTP++; throw Error("Business HTTP forbidden"); };
    session.fromPartition("persist:sparkclaw-workbench-default").webRequest.onBeforeRequest({ urls: ["http://*/*", "https://*/*", "ws://*/*", "wss://*/*"] }, (_request, callback) => { globalThis.__unavailableBusinessHTTP++; callback({ cancel: true }); });
  });
  if (unavailable) {
    assert.equal(evidence.connection.capabilities.surfaces.browser.reason,
      phase === "legacy_peer" ? "permission_or_implementation_missing" : phase === "legacy_client" ? "capability_unavailable" : "local_runtime_unavailable");
    assert.equal(await window.evaluate(() => window.sparkclawDesktop.createPersonal().then(() => false, () => true)), true);
    evidence.checks.original_browser_IPC_refuses_unavailable_operation = true;
  }
  if (phase === "missing" && !reuseCheckpoint) {
    checkpoint = await window.evaluate(async text => {
      const store = window.sparkclawClientStore, conversation = await store.create("Controller unavailable retained attachment");
      const file = await store.saveFile(conversation.id, "paired-retained.txt", new TextEncoder().encode(text));
      const draft = await store.draft(conversation.id);
      await store.saveDraft(conversation.id, "Keep this attachment and unsent draft.", [file.id], draft.revision, draft.scope_key);
      return { conversation_id: conversation.id, file_id: file.id };
    }, text);
    checkpoint.sha256 = hash(Buffer.from(text)); checkpoint.retained = evidence.before;
    await fs.writeFile(checkpointFile, JSON.stringify(checkpoint, null, 2) + "\n", { mode: 0o600, flag: "wx" });
  }
  const file = await window.evaluate(id => window.sparkclawClientStore.readFile(id), checkpoint.file_id);
  assert.equal(hash(Buffer.from(Object.values(file.bytes))), checkpoint.sha256);
  const draft = await window.evaluate(id => window.sparkclawClientStore.draft(id), checkpoint.conversation_id);
  assert.equal(draft.content, "Keep this attachment and unsent draft.");
  assert.ok(draft.local_file_ids.includes(checkpoint.file_id));
  assert.deepEqual(await retained(), checkpoint.retained);
  evidence.checks.original_file_and_draft_IPC_preserve_actual_attachment_and_prior_fences = true;
  evidence.direct_business_http = await application.evaluate(() => globalThis.__unavailableBusinessHTTP);
  assert.equal(evidence.direct_business_http, 0);
  evidence.checks.no_direct_business_HTTP = true;
  evidence.passed = true;
} catch (error) {
  evidence.error = String(error?.stack || error);
  if (window) evidence.connection_on_failure = await window.evaluate(() => window.sparkclawDesktop.localConnection()).catch(() => null);
} finally {
  if (application) await application.close().catch(error => { evidence.quit_error = String(error); evidence.passed = false; });
  const remaining = execFileSync("ps", ["-axo", "command="], { encoding: "utf8" }).split("\n").filter(line => line.includes(resources + "/browser-controller/"));
  evidence.checks.no_owned_controller_children = remaining.length === 0;
  if (remaining.length) evidence.passed = false;
  await fs.writeFile(output, JSON.stringify(evidence, null, 2) + "\n", { mode: 0o600, flag: "wx" });
  console.log(JSON.stringify({ passed: evidence.passed, phase, output, checks: evidence.checks, error: evidence.error }));
  if (!evidence.passed) process.exitCode = 1;
}
