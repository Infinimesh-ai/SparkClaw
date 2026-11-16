#!/usr/bin/env node
// Read only local trace files or a Relay journal export. No network or credentials.
import fs from 'node:fs';
import readline from 'node:readline';

const uuid = /^[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}$/iu;
const token = /^[a-z][a-z0-9_.]{0,95}$/iu;
const [mode, suppliedID, ...files] = process.argv.slice(2);
if (!['--rpc-id', '--request-id'].includes(mode) || !uuid.test(suppliedID ?? '') || !files.length) {
  console.error('Usage: node scripts/iscp-rpc-trace.mjs (--rpc-id UUID | --request-id UUID) TRACE_FILE ...');
  process.exit(2);
}
const id = suppliedID.toLowerCase();
const fields = [
  'time', 'observed_at', 'event', 'stage', 'role', 'rpc_id', 'request_id', 'envelope_id',
  'operation', 'reason', 'status', 'http_status', 'duration_ms', 'ok', 'state', 'count', 'component', 'payload_type',
];
const relayKeys = new Set(['event', 'observed_at', 'time', 'stage', 'role', 'envelope_id', 'status', 'reason', 'payload_type', 'count']);

function parse(line) {
  try {
    const value = JSON.parse(line);
    if (typeof value?.MESSAGE === 'string') return parseMessage(value.MESSAGE);
    return value && typeof value === 'object' ? value : null;
  } catch { return parseMessage(line); }
}
function parseMessage(line) {
  if (!line.includes('workbench_envelope_trace')) return null;
  if (line.startsWith('{')) {
    try { return JSON.parse(line); } catch { return null; }
  }
  const row = {};
  for (const [, key, value] of line.matchAll(/(?:^|\s)([a-z_]+)=([A-Za-z0-9_.:+-]+)/gu)) {
    if (relayKeys.has(key)) row[key] = key === 'count' ? Number(value) : value;
  }
  return /^workbench_envelope_trace(?:_health)?$/u.test(row.event ?? '') ? row : null;
}
function safe(row) {
  const value = {};
  for (const key of fields) {
    const v = row[key];
    if (v === undefined) continue;
    if (key.endsWith('_id')) {
      if (typeof v === 'string' && uuid.test(v)) value[key] = v.toLowerCase();
    } else if (key === 'time' || key === 'observed_at') {
      if (typeof v === 'string' && /^\d{4}-\d{2}-\d{2}T/u.test(v) && Number.isFinite(Date.parse(v))) value[key] = v;
    } else if (typeof v === 'string' ? token.test(v) : typeof v === 'boolean' || Number.isFinite(v)) {
      value[key] = v;
    }
  }
  return value;
}
async function scan(visit) {
  for (const file of files) {
    for await (const line of readline.createInterface({ input: fs.createReadStream(file), crlfDelay: Infinity })) {
      const row = parse(line);
      if (row) visit(row);
    }
  }
}

const rpcIDs = new Set(mode === '--rpc-id' ? [id] : []);
const envelopeIDs = new Set();
const events = [];
const matches = row => rpcIDs.has(row.rpc_id) || mode === '--request-id' && row.request_id === id;
const health = [];
if (mode === '--request-id') {
  await scan(raw => {
    const row = safe(raw);
    if (row.request_id === id && row.rpc_id) rpcIDs.add(row.rpc_id);
  });
}
await scan(raw => {
  const row = safe(raw);
  if (raw.event === 'workbench_envelope_trace_health' || raw.event === 'latency_trace_health' ||
      row.stage === 'trace.dropped') health.push(row);
  if (!matches(row)) return;
  if (row.envelope_id) envelopeIDs.add(row.envelope_id);
  if (row.stage) events.push(row);
});
await scan(raw => {
  const row = safe(raw);
  if (!row.stage || matches(row) || !envelopeIDs.has(row.envelope_id)) return;
  events.push(row);
});
events.sort((a, b) => (a.observed_at ?? a.time ?? '').localeCompare(b.observed_at ?? b.time ?? ''));
console.log(JSON.stringify({ rpc_ids: [...rpcIDs], envelope_ids: [...envelopeIDs], events, trace_health: health }, null, 2));
if (events.length === 0) process.exitCode = 1;
