import assert from "node:assert/strict";
import fs from "node:fs/promises";
import { spawn } from "node:child_process";
import electron from "electron";

// The disposable fixture deliberately dies with a live MCP connection. Its
// Controller must notice the parent pipe EOF and terminate every child itself.
const child = spawn(electron, [new URL("client-controller-native-fixture.mjs", import.meta.url).pathname], {
  env: { ...process.env, SPARKCLAW_TEST_CONTROLLER_PARENT_CRASH: "1" }, stdio: ["ignore", "pipe", "pipe"],
});
let output = "", error = "", ready;
child.stdout.on("data", bytes => {
  output += bytes;
  for (const line of output.split("\n")) {
    try { const value = JSON.parse(line); if (value.event === "controller_parent_crash_ready") ready = value; } catch {}
  }
});
child.stderr.on("data", bytes => { error = (error + bytes).slice(-10000); });
const timer = setTimeout(() => child.kill("SIGTERM"), 45000);
const exited = await new Promise((resolve, reject) => { child.once("exit", (code, signal) => resolve({ code, signal })); child.once("error", reject); });
clearTimeout(timer);
assert.ok(ready, output + error);
assert.equal(exited.signal, "SIGKILL");
const deadline = performance.now()+8000;
let gone = false;
while (performance.now()<deadline) {
  try { process.kill(-ready.controller_pid, 0); }
  catch (error) { if (error.code === "ESRCH") { gone = true; break; } throw error; }
  await new Promise(resolve => setTimeout(resolve, 50));
}
const evidence = { schema_version: 1, passed: gone, ...ready, parent_exit: exited,
  cleanup_ms: 8000-Math.max(0,deadline-performance.now()), runtime: "packaged Electron", system_node_required: false };
const evidenceFile = `${ready.directory}/parent-crash-evidence.json`;
await fs.writeFile(evidenceFile, JSON.stringify(evidence,null,2)+"\n");
console.log(JSON.stringify({ passed: gone, evidence_file: evidenceFile }));
assert.equal(gone, true, "Controller process group survived main crash");
