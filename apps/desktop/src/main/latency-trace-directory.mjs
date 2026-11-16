import fs from "node:fs/promises";
import path from "node:path";
import { setProfileLatencyTraceDirectory } from "./latency-trace.mjs";

const environment = "SPARKCLAW_PRIVATE_LATENCY_TRACE_DIR";
const traceName = /^(?:go-\d+-\d{8}T\d{6}\.\d{9}-\d+\.ndjson(?:\.[12]\.ndjson)?|desktop-\d+-\d+-[a-f0-9-]{36}\.jsonl(?:\.[12]\.jsonl)?)$/u;
const retentionMS = 7 * 24 * 60 * 60 * 1000;
const retainedBytes = 128 << 20;

// Only the main process that owns the profile may provision/prune its default
// sink. Explicit destinations, including a deliberate empty value, are untouched.
export async function prepareDefaultLatencyTrace(userData) {
  if (process.env[environment] !== undefined) return;
  const directory = path.join(userData, "iscp-diagnostics");
  try {
    await fs.mkdir(directory, { mode: 0o700, recursive: true });
  } catch (error) {
    if (error.code !== "EEXIST") {
      // Let the trace sink report failed initialization, without a path.
      setProfileLatencyTraceDirectory(directory);
      return;
    }
  }
  setProfileLatencyTraceDirectory(directory); // inherited by the private ISCP helper
  try {
    const stat = await fs.lstat(directory);
    if (!stat.isDirectory() || stat.mode & 0o077 || process.getuid && stat.uid !== process.getuid()) return;
    const files = [];
    for (const name of await fs.readdir(directory)) {
      if (!traceName.test(name)) continue;
      const filename = path.join(directory, name);
      try {
        const entry = await fs.lstat(filename);
        if (entry.isFile() && (!process.getuid || entry.uid === process.getuid())) files.push({ filename, size: entry.size, modified: entry.mtimeMs });
      } catch { /* The existing sink can rotate independently. */ }
    }
    const now = Date.now();
    let bytes = 0;
    for (const file of files.sort((a, b) => b.modified - a.modified)) {
      if (now - file.modified > retentionMS || bytes + file.size > retainedBytes) await fs.unlink(file.filename).catch(() => {});
      else bytes += file.size;
    }
  } catch { /* A trace retention failure must not delay or prevent startup. */ }
}
