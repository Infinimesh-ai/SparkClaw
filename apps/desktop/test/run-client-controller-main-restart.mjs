// Opt-in supervisor for the actual main-process crash/restart qualification.
import assert from "node:assert/strict";
import fs from "node:fs/promises";
import path from "node:path";
import { spawn, execFileSync } from "node:child_process";
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);
const electron = require("electron");
const lab = process.env.SPARKCLAW_CONTROLLER_ISCP_LAB;
assert.ok(lab?.startsWith("/private/tmp/sx-controller-iscp-"));
const root = path.join(lab, "evidence", `controller-main-restart-${Date.now()}`);
await fs.mkdir(root, { mode: 0o700 });
const fixture = new URL("./client-controller-iscp-native-fixture.mjs", import.meta.url);
const summary = { passed: false, checks: {}, evidence_directory: root };
async function run(phase) {
  const log = await fs.open(path.join(root, `${phase}.log`), "wx", 0o600);
  const child = spawn(electron, [fixture.pathname], { env: { ...process.env,
    SPARKCLAW_CONTROLLER_RESTART_EVIDENCE: root, SPARKCLAW_CONTROLLER_RESTART_PHASE: phase,
    SPARKCLAW_CONTROLLER_ISCP_FAULT: phase === "crash" ? "main-crash-after-fill" : "",
    SPARKCLAW_CONTROLLER_ISCP_FORM_ONLY: "1", SPARKCLAW_CONTROLLER_ISCP_CONCURRENCY: "0" },
  stdio: ["ignore", log.fd, log.fd] });
  const timer = setTimeout(() => child.kill("SIGTERM"), 190000);
  try { return await new Promise((resolve, reject) => {
    child.once("error", reject); child.once("exit", (code, signal) => resolve({ code, signal, pid: child.pid }));
  }); } finally { clearTimeout(timer); await log.close(); }
}
try {
  summary.crash_exit = await run("crash");
  const before = JSON.parse(await fs.readFile(path.join(root, "before-crash.json"), "utf8"));
  assert.equal(summary.crash_exit.signal, "SIGKILL");
  const started = performance.now();
  const groupExists = () => execFileSync("ps", ["-axo", "pgid="], { encoding: "utf8" })
    .split("\n").some(line => Number(line.trim()) === before.controller_pid);
  while (groupExists() && performance.now() - started < 8000) await new Promise(resolve => setTimeout(resolve, 50));
  summary.checks.owned_process_group_gone = !groupExists();
  summary.group_cleanup_observed_ms = Math.round(performance.now() - started);
  summary.runtime_entries_after_crash = await fs.readdir(before.runtime_directory).catch(error => {
    if (error.code === "ENOENT") return []; throw error;
  });
  summary.checks.owned_runtime_files_removed = summary.runtime_entries_after_crash.length === 0;
  const durable = JSON.parse(await fs.readFile(before.journal_path, "utf8"));
  assert.equal(durable.state, "dispatched"); assert.equal(durable.digest, before.digest);
  summary.checks.terminal_receipt_absent_before_restart = true;
  summary.recovery_exit = await run("recover");
  const result = JSON.parse(await fs.readFile(path.join(lab, "evidence/controller-native.json"), "utf8"));
  await fs.writeFile(path.join(root, "after-restart.json"), JSON.stringify(result, null, 2), { mode: 0o600 });
  assert.equal(result.passed, true, result.error);
  assert.equal(summary.recovery_exit.code, 0);
  summary.checks.same_profile_recovery_passed = true;
  assert.ok(Object.values(summary.checks).every(Boolean), JSON.stringify(summary.checks));
  summary.passed = true;
} catch (error) { summary.error = error.stack || String(error); }
await fs.writeFile(path.join(root, "summary.json"), JSON.stringify(summary, null, 2), { mode: 0o600 });
console.log(JSON.stringify(summary));
process.exitCode = summary.passed ? 0 : 1;
