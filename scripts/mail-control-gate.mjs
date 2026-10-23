#!/usr/bin/env node
// Measure the mail control-plane gate ("control message <= 20 ms on work2",
// docs/email-network-native-architecture.md section 8): one Lifecycle 3.0
// `lookup` of an already admitted send, sent to the Browser Controller socket
// exactly as the Gateway outbox drain sends it. The path crosses the
// Controller, MailboxClient, the resident `app_cli --machine --session` pool
// and the direct executor socket; `lookup` only reads the ledger.
//
// Self-contained (Node built-ins only) so it can be piped to `node -` on a
// host without installing anything:
//   node --input-type=module - --socket <controller.sock> --provider qq_mail --task-id <op3...> < scripts/mail-control-gate.mjs
import http from 'node:http';
import fs from 'node:fs';
import { performance } from 'node:perf_hooks';
import { parseArgs } from 'node:util';

const TASK_ID = /^op3\.[0-9]+\.[0-9]+\.[0-9a-f]{32}$/u;
const PROVIDERS = new Set(['qq_mail', 'gmail', 'outlook']);

// Nearest-rank percentile over a numeric sample; shared with the object gate.
export function percentile(values, p) {
  if (!values.length) return null;
  const sorted = [...values].sort((a, b) => a - b);
  return sorted[Math.min(sorted.length, Math.max(1, Math.ceil(p * sorted.length))) - 1];
}
export function summarize(values) {
  if (!values.length) return { n: 0 };
  const round = value => Math.round(value * 1000) / 1000;
  return { n: values.length, min: round(Math.min(...values)), p50: round(percentile(values, 0.5)), p95: round(percentile(values, 0.95)),
    max: round(Math.max(...values)), mean: round(values.reduce((sum, value) => sum + value, 0) / values.length) };
}

export function parseOptions(argv) {
  const { values } = parseArgs({ args: argv, options: {
    socket: { type: 'string' }, provider: { type: 'string' }, 'task-id': { type: 'string' },
    samples: { type: 'string', default: '100' }, warmup: { type: 'string', default: '3' },
    'interval-ms': { type: 'string', default: '100' }, 'gate-ms': { type: 'string', default: '20' },
  } });
  const options = { socket: values.socket, provider: values.provider, taskID: values['task-id'], samples: Number(values.samples),
    warmup: Number(values.warmup), intervalMS: Number(values['interval-ms']), gateMS: Number(values['gate-ms']) };
  if (!options.socket?.startsWith('/') || !PROVIDERS.has(options.provider) || !TASK_ID.test(options.taskID ?? '') ||
      !Number.isInteger(options.samples) || options.samples < 1 || options.samples > 2000 ||
      !Number.isInteger(options.warmup) || options.warmup < 0 || options.warmup > 20 ||
      // Below 30 s so the resident session pool's idle eviction never applies.
      !Number.isInteger(options.intervalMS) || options.intervalMS < 0 || options.intervalMS > 25000 || !(options.gateMS > 0)) {
    throw new Error('Usage: --socket <absolute controller.sock> --provider qq_mail|gmail|outlook --task-id <admitted op3 send ID> [--samples 100] [--warmup 3] [--interval-ms 100] [--gate-ms 20]');
  }
  return options;
}

// One keep-alive connection, as the Gateway's pooled HTTP client keeps one.
export function controllerClient(socketPath) {
  const agent = new http.Agent({ keepAlive: true, maxSockets: 1 });
  const request = (method, path, body) => new Promise((resolve, reject) => {
    const raw = body === undefined ? undefined : Buffer.from(JSON.stringify(body));
    const req = http.request({ socketPath, agent, method, path, timeout: 30000,
      headers: { accept: 'application/json', ...(raw ? { 'content-type': 'application/json', 'content-length': raw.length } : {}) } }, response => {
      const chunks = [];
      let size = 0;
      response.on('data', chunk => { size += chunk.length; if (size > 1 << 20) req.destroy(new Error('Controller response exceeds bound')); else chunks.push(chunk); });
      response.on('end', () => {
        try { resolve({ status: response.statusCode, body: JSON.parse(Buffer.concat(chunks).toString('utf8')) }); }
        catch { reject(new Error('Controller returned invalid JSON')); }
      });
    });
    req.on('timeout', () => req.destroy(new Error('Controller request timed out')));
    req.on('error', reject);
    req.end(raw);
  });
  return { request, close: () => agent.destroy() };
}

// Resident session processes visible to this user (Linux /proc only).
export function sessionProcesses(proc = '/proc') {
  let names;
  try { names = fs.readdirSync(proc); } catch { return null; }
  const pids = [];
  for (const name of names) {
    if (!/^[0-9]+$/u.test(name)) continue;
    try {
      const args = fs.readFileSync(`${proc}/${name}/cmdline`, 'utf8').split('\0');
      if (args.includes('app_cli') && args.includes('--machine') && args.includes('--session')) pids.push(Number(name));
    } catch { /* exited while listing */ }
  }
  return pids.sort((a, b) => a - b);
}

export async function measure(options, { client = controllerClient(options.socket), now = () => performance.now(), sleep = ms => new Promise(resolve => setTimeout(resolve, ms)), processes = sessionProcesses } = {}) {
  const report = { schema_version: 1, gate: 'mail control message <= 20 ms on work2', boundary: 'client-side POST /v1/application-lifecycle lookup over the Controller unix socket until the full JSON response',
    measured_at: new Date().toISOString(), provider: options.provider, samples_requested: options.samples, warmup: options.warmup, interval_ms: options.intervalMS, gate_ms: options.gateMS,
    node: process.version, samples_ms: [], failures: [] };
  try {
    const health = await client.request('GET', '/v1/health');
    if (health.status !== 200 || typeof health.body?.profile_id !== 'string') throw new Error('Controller health did not return a profile');
    const body = { profile_id: health.body.profile_id, action: 'lookup', provider: options.provider, operation: 'send', task_id: options.taskID };
    report.sessions_before = processes();
    for (let index = 0; index < options.warmup + options.samples; index++) {
      const started = now();
      const response = await client.request('POST', '/v1/application-lifecycle', body);
      const elapsed = now() - started;
      // Only an executor answer for the admitted row proves the full path ran.
      if (response.status !== 200 || response.body?.outcome !== 'known' || typeof response.body?.task !== 'object') {
        report.failures.push({ index, status: response.status, outcome: response.body?.outcome ?? null, code: response.body?.error?.code ?? response.body?.code ?? null });
      } else if (index >= options.warmup) report.samples_ms.push(Math.round(elapsed * 1000) / 1000);
      if (options.intervalMS) await sleep(options.intervalMS);
    }
    report.sessions_after = processes();
  } finally { client.close(); }
  report.summary_ms = summarize(report.samples_ms);
  report.passed = report.failures.length === 0 && report.samples_ms.length === options.samples && report.summary_ms.p95 <= options.gateMS;
  return report;
}

const invokedDirectly = !process.argv[1] || process.argv[1] === '-' || import.meta.url === `file://${fs.realpathSync(process.argv[1])}`;
if (invokedDirectly) {
  const args = process.argv.slice(2);
  Promise.resolve().then(() => measure(parseOptions(args[0] === '-' ? args.slice(1) : args)))
    .then(report => { process.stdout.write(`${JSON.stringify(report)}\n`); if (!report.passed) process.exitCode = 2; })
    .catch(error => { process.stderr.write(`${error.message}\n`); process.exitCode = 1; });
}
