import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { traceLatency } from '../src/main/latency-trace.mjs';
import { prepareDefaultLatencyTrace } from '../src/main/latency-trace-directory.mjs';

test('private timings are opt-in, allowlisted and cannot expose payloads or change business behavior', t => {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), 'sparkclaw-timing-'));
  t.after(() => fs.rmSync(directory, { recursive: true, force: true }));
  const previous = process.env.SPARKCLAW_PRIVATE_LATENCY_TRACE_DIR;
  t.after(() => { if (previous === undefined) delete process.env.SPARKCLAW_PRIVATE_LATENCY_TRACE_DIR; else process.env.SPARKCLAW_PRIVATE_LATENCY_TRACE_DIR = previous; });
  process.env.SPARKCLAW_PRIVATE_LATENCY_TRACE_DIR = '';
  traceLatency('execution.lookup', { body: 'SECRET' });
  assert.deepEqual(fs.readdirSync(directory), []);
  process.env.SPARKCLAW_PRIVATE_LATENCY_TRACE_DIR = directory;
  traceLatency('execution.lookup', { request_id: 'request-1', duration_ms: 12.5, body: 'SECRET', error: new Error('SECRET'), url: 'https://SECRET', state: 'running', ok: true });
  const filename = path.join(directory, fs.readdirSync(directory)[0]);
  const raw = fs.readFileSync(filename, 'utf8'), row = JSON.parse(raw);
  assert.equal(raw.includes('SECRET'), false);
  assert.equal(row.request_id, 'request-1'); assert.equal(row.duration_ms, 12.5);
  assert.equal(row.state, 'running'); assert.equal(row.ok, true);
  assert.ok(row.monotonic_ms >= 0); assert.equal(fs.statSync(filename).mode & 0o077, 0);
  process.env.SPARKCLAW_PRIVATE_LATENCY_TRACE_DIR = path.join(directory, 'missing');
  assert.doesNotThrow(() => traceLatency('failed.sink'));
  const publicDir = path.join(directory, 'public'); fs.mkdirSync(publicDir, { mode: 0o755 });
  process.env.SPARKCLAW_PRIVATE_LATENCY_TRACE_DIR = publicDir;
  traceLatency('unsafe.directory'); assert.deepEqual(fs.readdirSync(publicDir), []);
});

test('the owning profile retains recent reverse diagnostics and does not prune unrelated files', async t => {
  const profile = fs.mkdtempSync(path.join(os.tmpdir(), 'sparkclaw-profile-trace-'));
  const previous = process.env.SPARKCLAW_PRIVATE_LATENCY_TRACE_DIR;
  t.after(() => {
    if (previous === undefined) delete process.env.SPARKCLAW_PRIVATE_LATENCY_TRACE_DIR;
    else process.env.SPARKCLAW_PRIVATE_LATENCY_TRACE_DIR = previous;
    fs.rmSync(profile, {recursive: true, force: true});
  });
  delete process.env.SPARKCLAW_PRIVATE_LATENCY_TRACE_DIR;
  const directory = path.join(profile, 'iscp-diagnostics');
  fs.mkdirSync(directory, {mode: 0o700});
  const expired = 'go-123-20261101T000000.000000000-11.ndjson.1.ndjson';
  const keep = 'go-124-20261101T000000.000000000-22.ndjson';
  const unrelated = 'keep.txt';
  for (const name of [expired, keep, unrelated]) fs.writeFileSync(path.join(directory, name), '');
  const old = new Date(Date.now() - 8 * 86400000);
  for (const name of [expired, unrelated]) fs.utimesSync(path.join(directory, name), old, old);
  await prepareDefaultLatencyTrace(profile);
  assert.equal(process.env.SPARKCLAW_PRIVATE_LATENCY_TRACE_DIR, directory);
  assert.deepEqual(fs.readdirSync(directory).sort(), [keep, unrelated].sort());
  traceLatency('execution.lookup');
  assert.equal(fs.readdirSync(directory).length, 2);
  traceLatency('ipc.reverse_request_received', {rpc_id: '11111111-1111-4111-8111-111111111111', operation: 'browser.controller.status'});
  const traceName = fs.readdirSync(directory).find(name => name.startsWith('desktop-'));
  assert.ok(traceName);
  assert.equal(JSON.parse(fs.readFileSync(path.join(directory, traceName), 'utf8')).stage, 'ipc.reverse_request_received');
});
