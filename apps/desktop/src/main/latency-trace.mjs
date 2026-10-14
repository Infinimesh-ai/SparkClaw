import fs from 'node:fs';
import path from 'node:path';

// Private, opt-in timings only. Never serialize request/response bodies, paths,
// credentials or errors. These files are diagnostics, not delivery receipts.
let sink;
let attempted;
let records = 0;
const token = value => typeof value === 'string' && /^[a-zA-Z0-9_.:-]{1,160}$/u.test(value) ? value : undefined;
export const latencyNow = () => performance.now();

export function traceLatency(stage, fields = {}) {
  const directory = process.env.SPARKCLAW_PRIVATE_LATENCY_TRACE_DIR;
  if (!directory || !path.isAbsolute(directory)) return;
  try {
    if (attempted !== directory) {
      if (sink !== undefined) fs.closeSync(sink);
      sink = undefined; attempted = directory; records = 0;
      const stat = fs.lstatSync(directory);
      if (!stat.isDirectory() || stat.isSymbolicLink() || (stat.mode & 0o077) !== 0 || (process.getuid && stat.uid !== process.getuid())) return;
      sink = fs.openSync(path.join(directory, `desktop-${process.pid}.jsonl`), fs.constants.O_CREAT | fs.constants.O_APPEND | fs.constants.O_WRONLY | fs.constants.O_NOFOLLOW, 0o600);
      const file = fs.fstatSync(sink);
      if (!file.isFile() || (file.mode & 0o077) !== 0 || (process.getuid && file.uid !== process.getuid())) { fs.closeSync(sink); sink = undefined; }
    }
    if (sink === undefined || records >= 100000 || !token(stage)) return;
    const row = { schema_version: 1, role: 'desktop', pid: process.pid, time: new Date().toISOString(), monotonic_ms: latencyNow(), stage };
    for (const key of ['rpc_id', 'request_id', 'operation', 'state']) if (token(fields[key])) row[key] = fields[key];
    for (const key of ['duration_ms', 'count', 'status']) if (Number.isFinite(fields[key]) && fields[key] >= 0) row[key] = fields[key];
    if (typeof fields.ok === 'boolean') row.ok = fields.ok;
    fs.writeSync(sink, JSON.stringify(row) + '\n'); records++;
  } catch { /* A diagnostic failure cannot alter execution or recovery. */ }
}
