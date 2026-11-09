import assert from "node:assert/strict";
import fs from "node:fs/promises";
import path from "node:path";
import { execFileSync } from "node:child_process";

function evidencePath(directory) {
  const root = process.env.SPARKCLAW_CONTROLLER_RESTART_EVIDENCE;
  assert.equal(path.dirname(root), path.join(directory, "evidence"));
  assert.match(path.basename(root), /^controller-main-restart-\d+$/u);
  return path.join(root, "before-crash.json");
}

export async function captureMainCrash({ directory, evidence, runtime, forwarders, command }) {
  assert.deepEqual(evidence.observed_before_crash, { value: "Alice", text: "Alice" });
  const journal = [...forwarders].find(forwarder => forwarder.journal.rows.has(command.command_id)).journal;
  const journalPath = path.join(journal.root, `${command.command_id}.json`);
  const durable = JSON.parse(await fs.readFile(journalPath, "utf8"));
  assert.equal(durable.state, "dispatched");
  assert.equal(durable.digest, command.digest);
  const group = execFileSync("ps", ["-axo", "pid,pgid,command"], { encoding: "utf8" }).split("\n")
    .map(line => line.match(/^\s*(\d+)\s+(\d+)\s+(.*)$/u))
    .filter(row => row && Number(row[2]) === runtime.child.pid)
    .map(row => ({ pid: Number(row[1]), command: row[3] }));
  assert.ok(group.some(row => row.command.includes("@playwright/mcp/cli.js")));
  const captured = { ...evidence, main_pid: process.pid, controller_pid: runtime.child.pid,
    supervisor_incarnation: runtime.incarnation, runtime_directory: runtime.directory, group,
    journal_path: journalPath, command_id: command.command_id, digest: command.digest,
    boundary: "native fill observed; durable dispatch exists; main killed before terminal journal write" };
  const file = await fs.open(evidencePath(directory), "wx", 0o600);
  try { await file.writeFile(JSON.stringify(captured, null, 2)); await file.sync(); }
  finally { await file.close(); }
  process.kill(process.pid, "SIGKILL");
  await new Promise(() => {});
}

export async function recoverMainCrash({ directory, evidence, evaluate, until, forwarders }) {
  const before = JSON.parse(await fs.readFile(evidencePath(directory), "utf8"));
  evidence.restart = { command_id: before.command_id, prior_main_pid: before.main_pid, main_pid: process.pid };
  assert.notEqual(process.pid, before.main_pid);
  const sort = rows => [...rows].sort((a, b) => a.command_id.localeCompare(b.command_id));
  const fences = (await evaluate("window.sparkclawDesktop.state()")).browser_host.unknown_writes;
  const newFence = fences.find(row => row.command_id === before.command_id);
  assert.equal(newFence?.digest, before.digest);
  assert.deepEqual(sort(fences.filter(row => row.command_id !== before.command_id)), sort(before.unknown_fences_before));
  const durable = JSON.parse(await fs.readFile(before.journal_path, "utf8"));
  assert.equal(durable.state, "unknown");
  assert.equal(durable.digest, before.digest);
  evidence.checks.restart_preserves_prior_fences_and_marks_dispatched_unknown = true;
  const { conversation_id: conversationID, request_id: requestID } = before.active_task;
  let state;
  await until(async () => {
    await evaluate(`window.sparkclawClientStore.reconcile(${JSON.stringify(requestID)})`);
    state = await evaluate(`window.sparkclawClientStore.read(${JSON.stringify(conversationID)})`);
    return state.tasks.find(row => row.request_id === requestID)?.status === "unknown";
  }, "original task becomes unknown after desktop restart", 45000);
  evidence.tasks.push(state);
  assert.equal(evidence.wire_events.filter(event => event.inner_operation === "page.fill").length, 0);
  assert.equal(await evaluate('fetch(window.sparkclawDesktop.gatewayBase+"/api/owner").then(response=>response.status)'), 200);
  evidence.checks.restart_keeps_task_history_without_write_replay = true;
  // Only this run's directly observed synthetic effect is resolved. Earlier
  // unresolved writes keep their original installation and command identities.
  await evaluate(`window.sparkclawDesktop.reconcileBrowserHost(${JSON.stringify(before.command_id)},${JSON.stringify(before.digest)},"observed_completed")`);
  const conversation = await evaluate("window.sparkclawClientStore.create('Controller main restart recovery')");
  await evaluate(`window.sparkclawDesktop.selectConversation(${JSON.stringify(conversation.id)})`);
  const task = await evaluate(`(async()=>{const s=window.sparkclawClientStore;const t=await s.enqueue(${JSON.stringify(conversation.id)},"打开 https://controller.fixture.test/ 网页",[]);await s.submit(t.request_id);return t;})()`);
  let recovered;
  await until(async () => {
    await evaluate(`window.sparkclawClientStore.reconcile(${JSON.stringify(task.request_id)})`);
    recovered = await evaluate(`window.sparkclawClientStore.read(${JSON.stringify(conversation.id)})`);
    return ["delivered", "failed", "canceled", "unknown"].includes(recovered.tasks.find(row => row.request_id === task.request_id)?.status);
  }, "new native task after desktop restart", 60000);
  assert.equal(recovered.tasks.find(row => row.request_id === task.request_id).status, "delivered");
  const forwarder = [...forwarders][0];
  assert.notEqual(forwarder.runtime.incarnation, before.supervisor_incarnation);
  const page = forwarder.registry.conversationPages.get(conversation.id)?.webContents;
  assert.equal(page?.getURL(), "https://controller.fixture.test/");
  assert.equal(page.debugger.isAttached(), false);
  assert.equal(page.getLastWebPreferences().sandbox, true);
  assert.equal(evidence.wire_events.filter(event => event.inner_operation === "page.fill").length, 0);
  const after = (await evaluate("window.sparkclawDesktop.state()")).browser_host.unknown_writes;
  assert.deepEqual(sort(after), sort(before.unknown_fences_before));
  evidence.tasks.push(recovered);
  evidence.checks.new_incarnation_recovers_after_explicit_reconciliation = true;
  assert.equal(evidence.direct_business_http_attempts, 0);
  evidence.passed = true;
}
