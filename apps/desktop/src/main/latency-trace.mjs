import fs from 'node:fs';
import path from 'node:path';

// Private, opt-in timings only. Never serialize request/response bodies, paths,
// credentials or errors. These files are diagnostics, not delivery receipts.
let sink;
let attempted;
let initialized = false;
let stopped = false;
let observedReasons = new Set();
const token = value => typeof value === 'string' && /^[a-zA-Z0-9_.:-]{1,160}$/u.test(value) ? value : undefined;
const emptyHealth = () => ({schema_version: 1, component: 'desktop_latencytrace', enabled: false,
  state: 'disabled', reason: 'not_enabled', coverage: 'instrumented_boundaries_only', evidence: 'incomplete',
  attempted: 0, written: 0, dropped: 0, lost: 0, metadata_rejected: 0, first_loss_reason: '', last_written_at: ''});
let health = emptyHealth();
export const latencyNow = () => performance.now();

// Local state only: no reads, writes, retries or business reconciliation.
export const latencyTraceStatus = () => ({...health});

function publishHealth() {
  // At most one event for each fixed reason per configured sink. Repeated
  // failed writes or ignored events cannot turn into an unbounded log stream.
  if (observedReasons.has(health.reason)) return;
  observedReasons.add(health.reason);
  try {
    const raw = JSON.stringify({event: 'latency_trace_health', time: new Date().toISOString(), ...latencyTraceStatus()}) + '\n';
    // A synchronous bounded diagnostic write keeps EPIPE inside this catch;
    // no unhandled stream error can escape later into the business process.
    if (fs.writeSync(2, raw) !== Buffer.byteLength(raw)) throw new Error('incomplete health write');
  }
  catch {
    health.state = 'degraded'; health.reason = 'health_observer_failed';
    health.first_loss_reason ||= health.reason;
  }
}

function degrade(reason) {
  health.state = 'degraded'; health.reason = reason;
  health.first_loss_reason ||= reason;
  publishHealth();
}

function prepare(directory) {
  if (initialized && attempted === directory) return;
  if (sink !== undefined) {
    try { fs.closeSync(sink); }
    catch { degrade('close_failed'); }
  }
  sink = undefined; attempted = directory; initialized = true; stopped = false;
  health = emptyHealth(); observedReasons = new Set();
  if (!directory) { publishHealth(); return; }
  try {
    if (!path.isAbsolute(directory)) throw new Error('invalid diagnostic destination');
    const stat = fs.lstatSync(directory);
    if (!stat.isDirectory() || stat.isSymbolicLink() || (stat.mode & 0o077) !== 0 || (process.getuid && stat.uid !== process.getuid())) throw new Error('invalid diagnostic destination');
    sink = fs.openSync(path.join(directory, `desktop-${process.pid}.jsonl`), fs.constants.O_CREAT | fs.constants.O_APPEND | fs.constants.O_WRONLY | fs.constants.O_NOFOLLOW, 0o600);
    const file = fs.fstatSync(sink);
    if (!file.isFile() || (file.mode & 0o077) !== 0 || (process.getuid && file.uid !== process.getuid())) throw new Error('invalid diagnostic sink');
    health.enabled = true; health.state = 'enabled'; health.reason = 'recording';
    publishHealth();
  } catch {
    if (sink !== undefined) { try { fs.closeSync(sink); } catch {} }
    sink = undefined; stopped = true;
    degrade('initialization_failed');
  }
}

export function traceLatency(stage, fields = {}) {
  // All diagnostic operations, including health publication, remain best effort.
  try {
    prepare(process.env.SPARKCLAW_PRIVATE_LATENCY_TRACE_DIR);
    if (!latencyTraceStatus().enabled) return;
    health.attempted++;
    if (stopped) { health.lost++; return; }
    if (health.written >= 100000) { health.lost++; stopped = true; degrade('record_limit'); return; }
    if (!token(stage)) { health.lost++; health.metadata_rejected++; degrade('invalid_metadata'); return; }
    const row = {schema_version: 1, role: 'desktop', pid: process.pid, time: new Date().toISOString(), monotonic_ms: latencyNow(), stage};
    let rejected = false;
    for (const key of ['rpc_id', 'request_id', 'operation', 'state']) {
      if (fields[key] === undefined) continue;
      if (token(fields[key])) row[key] = fields[key]; else rejected = true;
    }
    for (const key of ['duration_ms', 'count', 'status']) {
      if (fields[key] === undefined) continue;
      if (Number.isFinite(fields[key]) && fields[key] >= 0) row[key] = fields[key]; else rejected = true;
    }
    if (fields.ok !== undefined) { if (typeof fields.ok === 'boolean') row.ok = fields.ok; else rejected = true; }
    if (rejected) { health.metadata_rejected++; degrade('invalid_metadata'); }
    const raw = JSON.stringify(row) + '\n';
    if (fs.writeSync(sink, raw) !== Buffer.byteLength(raw)) throw new Error('incomplete diagnostic write');
    health.written++; health.last_written_at = row.time;
  } catch {
    health.lost++; stopped = true;
    degrade('write_failed');
  }
}
