#!/usr/bin/env node
// Measure the presentation-plane gates of docs/email-network-native-architecture.md
// section 8 over a real ISCP transport (desktop adapter -> Go helper -> Relay ->
// Gateway `iscpobjects`): a 10 MiB object upload and download (target <= 30 s)
// and the round trip of one control-class RPC (`capabilities.get`).
//
// The objects use purpose `file`, which has no Gateway consumer, are random
// test bytes, and are released after each sample (an aborted upload is
// aborted). No mail, settings or execution operation is sent.
//
//   node scripts/iscp-object-gate.mjs --lab <prepared iscp-local-lab directory> --output <new private directory>
//   node scripts/iscp-object-gate.mjs --helper-config <helper json> --allow-production --output <dir>
// A production helper config shares its device credentials with any running
// SparkX; quit SparkX first and only run it with the owner's approval.
import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { spawn } from 'node:child_process';
import { performance } from 'node:perf_hooks';
import { parseArgs } from 'node:util';
import { fileURLToPath } from 'node:url';
import { summarize } from './mail-control-gate.mjs';

const MiB = 1024 * 1024;

export function parseOptions(argv) {
  const { values } = parseArgs({ args: argv, options: {
    lab: { type: 'string' }, 'helper-config': { type: 'string' }, 'allow-production': { type: 'boolean', default: false },
    output: { type: 'string' }, samples: { type: 'string', default: '5' }, 'size-mib': { type: 'string', default: '10' },
    'control-samples': { type: 'string', default: '50' }, 'control-interval-ms': { type: 'string', default: '50' },
    'gate-seconds': { type: 'string', default: '30' },
  } });
  const options = { lab: values.lab, helperConfig: values['helper-config'], allowProduction: values['allow-production'], output: values.output,
    samples: Number(values.samples), sizeMiB: Number(values['size-mib']), controlSamples: Number(values['control-samples']),
    controlIntervalMS: Number(values['control-interval-ms']), gateSeconds: Number(values['gate-seconds']) };
  const usage = 'Usage: --lab <prepared lab> | --helper-config <json> [--allow-production]; --output <new absolute directory> [--samples 5] [--size-mib 10] [--control-samples 50]';
  if (Boolean(options.lab) === Boolean(options.helperConfig) || !path.isAbsolute(options.lab ?? options.helperConfig) || !path.isAbsolute(options.output ?? '') ||
      !Number.isInteger(options.samples) || options.samples < 1 || options.samples > 50 || !(options.sizeMiB > 0 && options.sizeMiB <= 64) ||
      !Number.isInteger(options.controlSamples) || options.controlSamples < 0 || options.controlSamples > 1000 ||
      !Number.isInteger(options.controlIntervalMS) || options.controlIntervalMS < 0 || !(options.gateSeconds > 0)) throw new Error(usage);
  return options;
}

// Wraps the transport call: every RPC is timed, and each in-flight count is
// sampled so the report shows the window the transfer actually achieved.
export function timedCall(call, now = () => performance.now()) {
  const state = { events: [], inflight: 0, maxInflight: 0, openTransfers: new Set() };
  const wrapped = async (operation, body, options) => {
    const started = now();
    state.inflight++; state.maxInflight = Math.max(state.maxInflight, state.inflight);
    if (operation === 'transfer.open') state.openTransfers.add(body.transfer_id);
    let ok = false;
    try { const value = await call(operation, body, options); ok = true; return value; }
    finally {
      state.inflight--;
      if (operation === 'transfer.commit' && ok) state.openTransfers.delete(body.transfer_id);
      state.events.push({ operation, started, ended: now(), ok });
    }
  };
  return { call: wrapped, state };
}

// Per-operation latency and the wall-clock span each operation occupied.
export function phases(events, from = 0) {
  const byOperation = {};
  for (const event of events.slice(from)) (byOperation[event.operation] ??= []).push(event);
  return Object.fromEntries(Object.entries(byOperation).map(([operation, rows]) => [operation, {
    count: rows.length, failed: rows.filter(row => !row.ok).length,
    span_ms: Math.round((Math.max(...rows.map(row => row.ended)) - Math.min(...rows.map(row => row.started))) * 1000) / 1000,
    latency_ms: summarize(rows.map(row => row.ended - row.started)) }]));
}

// Joins desktop/helper/Gateway latency trace rows (when present) by stage and
// operation; durations come from the rows' own `duration_ms`.
export function summarizeTraces(directory) {
  const groups = {};
  let names;
  try { names = fs.readdirSync(directory); } catch { return {}; }
  for (const name of names.filter(name => /\.(jsonl|ndjson)$/u.test(name))) {
    for (const line of fs.readFileSync(path.join(directory, name), 'utf8').split('\n')) {
      if (!line) continue;
      let row; try { row = JSON.parse(line); } catch { continue; }
      if (!Number.isFinite(row.duration_ms) || !row.stage) continue;
      (groups[`${row.role ?? 'unknown'} ${row.stage} ${row.operation ?? '-'}`] ??= []).push(row.duration_ms);
    }
  }
  return Object.fromEntries(Object.entries(groups).sort().map(([key, values]) => [key, summarize(values)]));
}

function privateDirectory(directory) {
  fs.mkdirSync(directory, { mode: 0o700 });
  const stat = fs.lstatSync(directory);
  if (!stat.isDirectory() || stat.isSymbolicLink() || (stat.mode & 0o077) !== 0) throw new Error(`${directory} must be a private directory`);
  return directory;
}

export async function resolveTarget(options, readJSON = file => JSON.parse(fs.readFileSync(file, 'utf8'))) {
  if (options.lab) {
    const run = readJSON(path.join(options.lab, 'run.json'));
    if (run.schema_version !== 1 || run.relay_profile !== 'local-lab' || !Array.isArray(run.qualified_operations)) throw new Error('A lab prepared with prepare-expansion is required');
    // A Gateway binds one installation per client: reuse this lab's measurement ID.
    const file = path.join(options.lab, 'evidence/object-gate-installation.json');
    let installationID;
    try { installationID = readJSON(file).installation_id; }
    catch (error) {
      if (error.code !== 'ENOENT') throw error;
      installationID = crypto.randomUUID();
      fs.writeFileSync(file, JSON.stringify({ installation_id: installationID }), { mode: 0o600, flag: 'wx' });
    }
    return { helperConfig: path.join(options.lab, 'desktop-helper.json'), installationID, link: 'local Docker lab: desktop helper -> reference Relay container -> Gateway container', relay_id: run.relay_id, lab_id: run.lab_id };
  }
  const helper = readJSON(options.helperConfig);
  if (helper.relay_profile !== 'local-lab' && !options.allowProduction) throw new Error('This helper config is not a local lab; pass --allow-production only with SparkX quit and owner approval');
  if (!/^[0-9a-f-]{36}$/u.test(helper.expected_installation_id ?? '')) throw new Error('The helper config names no installation to measure with');
  return { helperConfig: options.helperConfig, installationID: helper.expected_installation_id, link: helper.relay_profile === 'local-lab' ? 'local lab helper config' : 'production helper config', relay_profile: helper.relay_profile };
}

export async function run(options, { Transport, ObjectClient, now = () => performance.now(), sleep = ms => new Promise(resolve => setTimeout(resolve, ms)), random = size => crypto.randomBytes(size) } = {}) {
  const output = privateDirectory(options.output);
  const traceDirectory = privateDirectory(path.join(output, 'trace'));
  const target = await resolveTarget(options);
  const size = Math.round(options.sizeMiB * MiB);
  const report = { schema_version: 1, measured_at: new Date().toISOString(), link: target.link, target, size_bytes: size, gate_seconds: options.gateSeconds,
    node: process.version, samples: [], control_idle_ms: [], control_under_transfer_ms: [], failures: [] };
  const save = () => fs.writeFileSync(path.join(output, 'object-gate.json'), JSON.stringify(report, null, 2), { mode: 0o600 });
  // The helper and adapter inherit this; both only write when it is private.
  const previousTrace = process.env.SPARKCLAW_PRIVATE_LATENCY_TRACE_DIR;
  process.env.SPARKCLAW_PRIVATE_LATENCY_TRACE_DIR = traceDirectory;
  const { installationID } = target;
  report.states = []; report.helper_states = [];
  const onState = state => report.states.push({ at_ms: Math.round(now()), state });
  // Local test credentials only: keep the helper's diagnostics privately for a
  // failed lab run. A production helper's stderr is never kept.
  const spawnProcess = (command, args, spawnOptions) => {
    const child = spawn(command, args, spawnOptions);
    if (options.lab) child.stderr.pipe(fs.createWriteStream(path.join(output, 'helper-stderr.private.log'), { flags: 'a', mode: 0o600 }));
    // Helper state frames carry no secrets; they explain a lost transport.
    let partial = '';
    child.stdout.on('data', chunk => {
      const lines = (partial + chunk.toString('utf8')).split('\n');
      partial = lines.pop().slice(-4096);
      for (const line of lines) if (line.length < 256 && line.includes('"type":"state"')) { try { report.helper_states.push({ at_ms: Math.round(now()), state: JSON.parse(line).state }); } catch { /* not a frame */ } }
    });
    return child;
  };
  const transport = new Transport({ configPath: target.helperConfig, installationID, origin: 'https://iscp.invalid', timeoutMS: 30000, onState, spawnProcess });
  const timed = timedCall((operation, body, callOptions = {}) => transport.invoke(operation, body, { ...callOptions, installationID }), now);
  const released = [];
  try {
    let started = now();
    await transport.start();
    report.transport_start_ms = now() - started;
    report.capabilities = { profile: transport.capabilities?.profile, limits: transport.capabilities?.limits, schema_version: transport.capabilities?.schema_version };
    await timed.call('installation.bind', { schema_version: 1, installation_id: installationID });
    for (let index = 0; index < options.controlSamples; index++) {
      started = now();
      await timed.call('capabilities.get');
      report.control_idle_ms.push(now() - started);
      if (options.controlIntervalMS) await sleep(options.controlIntervalMS);
    }
    const objects = new ObjectClient({ root: path.join(output, 'objects'), scope: { installationID, authorization_revision: transport.capabilities.authorization_revision }, call: timed.call });
    for (let index = 0; index < options.samples; index++) {
      const bytes = random(size), digest = crypto.createHash('sha256').update(bytes).digest('hex');
      let active = true;
      // Control probe while the bulk window is busy (400 ms spacing).
      const probes = (async () => { while (active) { const begin = now(); try { await transport.invoke('capabilities.get', undefined, { installationID }); report.control_under_transfer_ms.push(now() - begin); } catch (error) { report.failures.push({ sample: index, operation: 'capabilities.get', error: error.message }); } await sleep(400); } })();
      try {
        const mark = timed.state.events.length;
        timed.state.maxInflight = 0;
        started = now();
        const object = await objects.upload(bytes, { purpose: 'file', name: `gate-${index + 1}.bin` });
        const upload = now() - started, uploadPeak = timed.state.maxInflight;
        const uploadPhases = phases(timed.state.events, mark);
        const readMark = timed.state.events.length;
        timed.state.maxInflight = 0;
        started = now();
        const content = await objects.download(object);
        const download = now() - started;
        if (content.length !== size || crypto.createHash('sha256').update(content).digest('hex') !== digest) throw new Error('Downloaded object differs from the uploaded test bytes');
        report.samples.push({ index: index + 1, upload_seconds: upload / 1000, download_seconds: download / 1000, upload_max_inflight: uploadPeak, download_max_inflight: timed.state.maxInflight,
          upload: uploadPhases, download: phases(timed.state.events, readMark) });
        await timed.call('object.release', { object_id: object.object_id, version: object.version });
        released.push(object.object_id);
      } catch (error) {
        report.failures.push({ sample: index + 1, error: error.message });
        break;
      } finally { active = false; await probes; save(); }
    }
  } finally {
    // An interrupted upload never stays open; committed objects were released.
    for (const transferID of timed.state.openTransfers) {
      try { await transport.invoke('transfer.abort', { transfer_id: transferID }, { installationID }); report.aborted = [...(report.aborted ?? []), transferID]; }
      catch (error) { report.failures.push({ operation: 'transfer.abort', error: error.message }); }
    }
    transport.close();
    if (previousTrace === undefined) delete process.env.SPARKCLAW_PRIVATE_LATENCY_TRACE_DIR;
    else process.env.SPARKCLAW_PRIVATE_LATENCY_TRACE_DIR = previousTrace;
    // Local download/upload journals hold only this run's random test bytes.
    fs.rmSync(path.join(output, 'objects'), { recursive: true, force: true });
    report.released_objects = released.length;
    report.upload_seconds = summarize(report.samples.map(sample => sample.upload_seconds));
    report.download_seconds = summarize(report.samples.map(sample => sample.download_seconds));
    report.control_idle = summarize(report.control_idle_ms);
    report.control_under_transfer = summarize(report.control_under_transfer_ms);
    report.traces = summarizeTraces(traceDirectory);
    report.passed = report.failures.length === 0 && report.samples.length === options.samples &&
      report.upload_seconds.p95 <= options.gateSeconds && report.download_seconds.p95 <= options.gateSeconds;
    save();
  }
  return report;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const { ISCPTransport } = await import('../apps/desktop/src/main/iscp-transport.mjs');
  const { ISCPObjectClient } = await import('../apps/desktop/src/main/iscp-object-client.mjs');
  try {
    const report = await run(parseOptions(process.argv.slice(2)), { Transport: ISCPTransport, ObjectClient: ISCPObjectClient });
    const brief = ({ n, p50, p95 }) => ({ n, p50, p95 });
    process.stdout.write(`${JSON.stringify({ passed: report.passed, upload_seconds: brief(report.upload_seconds), download_seconds: brief(report.download_seconds),
      control_idle_ms: brief(report.control_idle), control_under_transfer_ms: brief(report.control_under_transfer), failures: report.failures })}\n`);
    if (!report.passed) process.exitCode = 2;
  } catch (error) { process.stderr.write(`${error.stack}\n`); process.exitCode = 1; }
}
