import assert from "node:assert/strict";
import test from "node:test";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { BrowserHostAgent, commandDigest, iscpAuthorizationDigest, iscpHostID, RESULT_BUDGET_BYTES } from "../src/browser/host-agent.mjs";
import { nativePageCommand } from "../src/browser/native-page-commands.mjs";

const scope = { owner_id: "owner", client_id: "client", installation_id: "installation" };
const SESSION = "11111111-1111-4111-8111-111111111111";
const session = (id = SESSION, revision = 7) => ({ session_id: id, authorization_revision: revision });
// The Gateway builds the same binding from the same session (Go test vector).
const binding = (conversation = "A", s = session()) => ({ ...scope, local_conversation_id: conversation, local_task_id: `task_${conversation}`,
  host_id: iscpHostID(scope, s.session_id), runtime_generation: `iscp_${s.session_id}`, connection_epoch: s.session_id, lease_id: `lease_${conversation}`,
  page_id: `page_${conversation}`, page_generation: 1, authorization_digest: iscpAuthorizationDigest(s.session_id, s.authorization_revision), lease_expires_at: "0001-01-01T00:00:00Z" });
function command(operation, b, args = {}, id = `${operation}_${b.local_conversation_id}`) { const value = { schema_version: 1, type: "command", command_id: id, binding: b, operation, arguments: args, digest: "" }; value.digest = commandDigest(value); return value; }
const reverse = (body) => ({ type: "task.invoke", profile: "sparkclaw.workbench.transport.v2", id: "22222222-2222-4222-8222-222222222222", operation: "browser.command", body });

async function fixture(t, { execute, fences = [] } = {}) {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), "sparkclaw-host-iscp-")); t.after(() => fs.rm(root, { recursive: true, force: true }));
  const pages = new Map(); let closed = 0; const calls = [];
  const registry = { runtimeGeneration: "runtime_test",
    acquireHostPage(b) { if (pages.has(b.local_conversation_id)) throw new Error("second controller"); const record = { binding: b, pageRef: `ref_${b.local_conversation_id}` }; pages.set(b.local_conversation_id, record); return record; },
    requireHostPage(b) { const record = pages.get(b.local_conversation_id); if (!record || record.binding.lease_id !== b.lease_id) throw new Error("stale"); return record; },
    releaseHostPage(b) { pages.delete(b.local_conversation_id); }, closeHostPages() { closed++; pages.clear(); }, closeConnection() {} };
  const auth = { generation: 1, status: { state: "connected", capabilities: { browser: true } }, connection: { ownerID: "owner", clientID: "client" }, descriptor: { transport: "iscp" },
    transport: { capabilities: session() },
    async invokeISCP(operation, body) { calls.push({ operation, body }); if (operation === "browser.receipt") return { fences: typeof fences === "function" ? fences() : fences }; if (operation === "browser.reconcile") return { reconciled: true }; throw new Error(`unexpected ${operation}`); } };
  const agent = new BrowserHostAgent({ auth, registry, userDataDir: root, commandDeadlineMS: 200,
    execute: execute || (async (_record, operation, _args, b, fence) => { fence(); return { text: b.local_conversation_id, operation }; }) });
  t.after(() => agent.stop());
  await agent.start({ installation_id: "installation" });
  return { agent, auth, pages, calls, closed: () => closed };
}
async function send(agent, value, s = session()) { return agent.handleReverse(reverse(value), s); }

test("ISCP host connects without grant, poll or timers and answers reverse commands", async (t) => {
  const f = await fixture(t);
  f.agent.autoConnect();
  await new Promise((resolve) => setTimeout(resolve, 10));
  assert.equal(f.agent.state, "connected");
  assert.deepEqual(f.calls.map((call) => call.operation), ["browser.receipt"], "only the fences are read, once");
  for (const timer of ["heartbeat", "liveness", "grantTimer", "autoGrantTimer"]) assert.equal(f.agent[timer], undefined, timer);
  f.agent.autoConnect(); await new Promise((resolve) => setTimeout(resolve, 10));
  assert.equal(f.calls.length, 1, "an unchanged session is not refreshed again");
  const a = binding();
  const acquired = await send(f.agent, command("acquire", a));
  assert.equal(acquired.status, 200); assert.equal(acquired.body.status, "completed"); assert.deepEqual(acquired.body.binding, a);
  assert.equal(f.agent.leases.get(a.lease_id).timer, undefined, "an ISCP lease has no expiry timer");
  const read = await send(f.agent, command("read", a));
  assert.equal(read.body.status, "completed"); assert.equal(read.body.output.text, "A");
  const released = await send(f.agent, command("release", a));
  assert.equal(released.body.status, "completed"); assert.equal(f.pages.size, 0);
});

test("ISCP refuses other reverse operations and unavailable browsers before executing", async (t) => {
  const f = await fixture(t); f.agent.autoConnect();
  const denied = await f.agent.handleReverse({ ...reverse(command("acquire", binding())), operation: "settings.owner.get" }, session());
  assert.equal(denied.status, 403); assert.equal(denied.error_code, "permission_denied");
  f.auth.status = { state: "connected", capabilities: { browser: false } };
  const unavailable = await send(f.agent, command("acquire", binding()));
  assert.equal(unavailable.status, 503); assert.equal(unavailable.error_code, "capability_unavailable"); assert.equal(f.pages.size, 0);
});

test("ISCP binding must be the one derived from this device session", async (t) => {
  const f = await fixture(t); f.agent.autoConnect();
  for (const forged of [{ connection_epoch: "33333333-3333-4333-8333-333333333333" }, { host_id: "iscp_forged" }, { authorization_digest: "0".repeat(64) },
    { runtime_generation: "runtime_test" }, { client_id: "other" }, { lease_expires_at: new Date(Date.now() + 29000).toISOString() }]) {
    const result = await send(f.agent, command("acquire", { ...binding(), ...forged }, {}, `forged_${Object.keys(forged)[0]}`));
    assert.equal(result.body.status, "failed", JSON.stringify(forged)); assert.equal(result.body.error_code, "command_fenced");
  }
  assert.equal(f.pages.size, 0);
  const missing = await send(f.agent, command("read", binding()));
  assert.equal(missing.body.status, "failed", "a command without an acquired lease is fenced");
});

test("a new session closes the old leases and reads its fences once", async (t) => {
  const f = await fixture(t); f.agent.autoConnect();
  const a = binding(); await send(f.agent, command("acquire", a)); assert.equal(f.pages.size, 1);
  const next = session("44444444-4444-4444-8444-444444444444"), before = f.closed();
  const stale = await send(f.agent, command("read", a), next);
  assert.equal(stale.body.status, "failed", "the old session's binding cannot reach the new session");
  assert.equal(f.pages.size, 0); assert.equal(f.closed(), before + 1);
  await new Promise((resolve) => setTimeout(resolve, 10));
  assert.equal(f.calls.filter((call) => call.operation === "browser.receipt").length, 2);
  const fresh = await send(f.agent, command("acquire", binding("A", next), {}, "acquire_next"), next);
  assert.equal(fresh.body.status, "completed");
});

test("the command deadline answers before the ISCP timeout: unknown for writes, failed for reads", async (t) => {
  const f = await fixture(t, { execute: () => new Promise(() => {}) }); f.agent.autoConnect();
  const a = binding(); await send(f.agent, command("acquire", a));
  const write = command("fill", a, { ref: "snapshot:e1", snapshot_id: "snapshot", value: "draft" });
  const started = performance.now();
  const writeResult = await send(f.agent, write);
  assert.ok(performance.now() - started < 1000);
  assert.equal(writeResult.body.status, "unknown"); assert.equal(writeResult.body.error_code, "write_outcome_unknown");
  assert.equal(f.agent.journal.unknown(scope).length, 1, "the uncertain write stays in the local journal");
  const replay = await send(f.agent, write);
  assert.equal(replay.body.status, "unknown"); assert.equal(replay.body.error_code, "write_replay_requires_reconciliation");
  const readResult = await send(f.agent, command("read", a));
  assert.equal(readResult.body.status, "failed");
  assert.equal(readResult.body.error_code, "command_timeout");
});

test("native navigation failures retain bounded codes without retrying or copying error URLs", async (t) => {
  for (const nativeCode of ["ERR_NAME_NOT_RESOLVED", "ERR_CONNECTION_REFUSED", "ERR_CERT_AUTHORITY_INVALID", "https://private.test/?token=secret"]) {
    let loads = 0;
    const record = { webContents: { loadURL: async () => { loads++; throw Object.assign(new Error("load failed at https://private.test/?token=secret"), { code: nativeCode }); } } };
    const f = await fixture(t, { execute: (_record, operation, args, b, fence) => nativePageCommand(record, operation, args, b, fence) });
    f.agent.autoConnect();
    const a = binding(); await send(f.agent, command("acquire", a));
    const result = await send(f.agent, command("navigate", a, { url: "https://example.test/" }));
    assert.equal(result.body.status, "failed"); assert.equal(result.body.error_code, "navigation_failed");
    assert.equal(result.body.output.native_error_code, nativeCode.startsWith("ERR_") ? nativeCode : "ERR_FAILED");
    assert.equal(loads, 1); assert.equal(f.agent.journal.unknown(scope).length, 0);
    assert.doesNotMatch(JSON.stringify(result), /private\.test|secret/u);
  }
});

test("navigation completion after deadline cannot turn a failed command into success", async (t) => {
  let finish; let checks = 0;
  const f = await fixture(t, { execute: async (_record, _operation, _args, _b, fence) => { await new Promise(resolve => { finish = resolve; }); checks++; fence(); return { url: "https://example.test/" }; } });
  f.agent.autoConnect(); const a = binding(); await send(f.agent, command("acquire", a));
  const result = await send(f.agent, command("navigate", a, { url: "https://example.test/" }));
  assert.equal(result.body.status, "failed"); assert.equal(result.body.error_code, "command_timeout");
  finish(); await new Promise(resolve => setTimeout(resolve, 10));
  assert.equal(checks, 1); assert.deepEqual(result.body.output, {}); assert.equal(f.agent.journal.unknown(scope).length, 0);
});

test("results stay inside the reverse response budget", async (t) => {
  const f = await fixture(t, { execute: async (_record, operation, _args, _b, fence) => { fence(); return operation === "read" ? { text: "<".repeat(RESULT_BUDGET_BYTES / 4) } : { clicked: "x".repeat(RESULT_BUDGET_BYTES) }; } });
  f.agent.autoConnect();
  const a = binding(); await send(f.agent, command("acquire", a));
  const read = await send(f.agent, command("read", a));
  assert.equal(read.body.status, "failed"); assert.equal(read.body.error_code, "result_budget_exceeded"); assert.deepEqual(read.body.output, {});
  const click = await send(f.agent, command("click", a, { ref: "snapshot:e1", snapshot_id: "snapshot" }));
  assert.equal(click.body.status, "completed", "a completed write keeps its outcome"); assert.deepEqual(click.body.output, {});
});

test("a known resized snapshot is refused before dispatch while old uncertain writes remain fenced", async (t) => {
  let effects = 0;
  const f = await fixture(t, { execute: async () => { effects++; return { clicked: "e1" }; } }); f.agent.autoConnect();
  const b = binding(); await send(f.agent, command("acquire", b));
  const record = f.pages.get("A");
  record.lastHostSnapshot = { id: "snapshot", layoutRevision: 0 };
  record.layoutRevision = 1;
  const stale = command("click", b, { snapshot_id: "snapshot", ref: "snapshot:e1" });
  const denied = await send(f.agent, stale);
  assert.equal(denied.body.status, "failed");
  assert.equal(effects, 0); assert.equal(f.agent.journal.rows.size, 0);
  await f.agent.journal.begin(stale); await f.agent.journal.finish(stale.command_id, "unknown");
  const replay = await send(f.agent, stale);
  assert.equal(replay.body.status, "unknown"); assert.equal(replay.body.error_code, "write_replay_requires_reconciliation");
  assert.equal(effects, 0); assert.equal(f.agent.journal.unknown(scope).length, 1);
  record.lastHostSnapshot = { id: "fresh", layoutRevision: 1 };
  const fresh = await send(f.agent, command("click", b, { snapshot_id: "fresh", ref: "fresh:e1" }, "fresh_click"));
  assert.equal(fresh.body.status, "completed"); assert.equal(effects, 1);
});

test("a conflicting fence on connect stops automation until the owner reconciles", async (t) => {
  const write = command("click", binding(), { ref: "snapshot:e1", snapshot_id: "snapshot" });
  const b = binding();
  const fence = { command_id: write.command_id, scope: { ...scope, local_conversation_id: "A", local_task_id: "task_A" }, host_id: b.host_id, runtime_generation: b.runtime_generation,
    connection_epoch: b.connection_epoch, lease_id: b.lease_id, page_id: b.page_id, page_generation: 1, authorization_digest: b.authorization_digest,
    digest: "0".repeat(64), write: true, state: "unknown", updated_at: new Date().toISOString() };
  const f = await fixture(t, { fences: [fence] });
  await f.agent.journal.begin(write); await f.agent.journal.finish(write.command_id, "unknown");
  f.agent.autoConnect(); await new Promise((resolve) => setTimeout(resolve, 10));
  assert.equal(f.agent.state, "reconciliation_conflict");
  const refused = await send(f.agent, command("acquire", binding("B"), {}, "acquire_b"));
  assert.equal(refused.status, 503); assert.equal(f.pages.size, 0);
  f.agent.autoConnect(); await new Promise((resolve) => setTimeout(resolve, 10));
  assert.equal(f.calls.length, 1, "a conflict is not retried automatically");
});

test("session-derived binding values match the Gateway's vector", () => {
  // services/gateway/internal/browserhost/reverse_test.go asserts the same values.
  assert.equal(iscpHostID(scope, SESSION), "iscp_7b0b83b55eedc9a5146da1bce2231528");
  assert.equal(iscpAuthorizationDigest(SESSION, 7), "ceb65fe5cc84a5429d48508b563c2793e22fe712b6e1a5501c6221041d81e61d");
});

test("a fence left by a reverse write reaches the original notice and reconciles over ISCP", async (t) => {
  const b = binding();
  const fence = { command_id: "cmd_click", scope: { ...scope, local_conversation_id: "A", local_task_id: "task_A" }, host_id: b.host_id, runtime_generation: b.runtime_generation,
    connection_epoch: b.connection_epoch, lease_id: b.lease_id, page_id: b.page_id, page_generation: 1, authorization_digest: b.authorization_digest,
    digest: "d".repeat(64), write: true, state: "unknown", updated_at: new Date().toISOString() };
  let fences = [fence];
  const f = await fixture(t, { fences: () => fences });
  f.agent.autoConnect(); await new Promise((resolve) => setTimeout(resolve, 10));
  assert.deepEqual(f.agent.snapshot().unknown_writes, [{ command_id: "cmd_click", digest: fence.digest, local_conversation_id: "A", local_task_id: "task_A" }]);
  const original = f.auth.invokeISCP;
  f.auth.invokeISCP = async (operation, body) => { if (operation === "browser.reconcile") fences = []; return original(operation, body); };
  await f.agent.reconcile("cmd_click", fence.digest, "observed_completed");
  assert.deepEqual(f.calls.at(-1), { operation: "browser.reconcile", body: { command_id: "cmd_click", digest: fence.digest, outcome: "observed_completed" } });
  assert.deepEqual(f.agent.snapshot().unknown_writes, []);
});
