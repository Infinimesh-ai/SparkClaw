import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import * as trace from '../src/main/latency-trace.mjs';

function fixture(t) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'trace-health-'));
  const prior = process.env.SPARKCLAW_PRIVATE_LATENCY_TRACE_DIR;
  const events = [];
  const write = fs.writeSync;
  t.mock.method(fs, 'writeSync', (fd, data, ...rest) => {
    if (fd === 2) { events.push(JSON.parse(String(data))); return Buffer.byteLength(data); }
    return write(fd, data, ...rest);
  });
  t.after(() => {
    if (prior === undefined) delete process.env.SPARKCLAW_PRIVATE_LATENCY_TRACE_DIR;
    else process.env.SPARKCLAW_PRIVATE_LATENCY_TRACE_DIR = prior;
    fs.rmSync(root, {recursive: true, force: true});
  });
  return {root, events};
}

test('Desktop reports disabled, enabled and failed initialization without leaking paths', t => {
  const f = fixture(t);
  process.env.SPARKCLAW_PRIVATE_LATENCY_TRACE_DIR = '';
  trace.traceLatency('rpc.call');
  assert.equal(f.events.at(-1)?.state, 'disabled');
  assert.equal(f.events.at(-1)?.evidence, 'incomplete');
  process.env.SPARKCLAW_PRIVATE_LATENCY_TRACE_DIR = f.root;
  trace.traceLatency('rpc.call');
  assert.equal(f.events.at(-1)?.state, 'enabled');
  process.env.SPARKCLAW_PRIVATE_LATENCY_TRACE_DIR = path.join(f.root, 'private-secret-missing');
  for (let i = 0; i < 3; i++) trace.traceLatency('rpc.call');
  assert.equal(f.events.at(-1)?.reason, 'initialization_failed');
  assert.equal(f.events.at(-1)?.state, 'degraded');
  assert.equal(f.events.filter(x => x.reason === 'initialization_failed').length, 1);
  assert.doesNotMatch(JSON.stringify(f.events), /private-secret|trace-health|\//u);
});

test('Desktop sink write errors are bounded observable evidence loss and never throw', t => {
  const f = fixture(t);
  process.env.SPARKCLAW_PRIVATE_LATENCY_TRACE_DIR = f.root;
  trace.traceLatency('rpc.call');
  const write = fs.writeSync;
  t.mock.method(fs, 'writeSync', (fd, ...args) => {if (fd === 2) return write(fd, ...args); throw new Error('private@example.test /secret/file payload');});
  for (let i = 0; i < 3; i++) assert.doesNotThrow(() => trace.traceLatency('rpc.call'));
  assert.equal(f.events.at(-1)?.reason, 'write_failed');
  assert.equal(f.events.at(-1)?.state, 'degraded');
  assert.equal(f.events.filter(x => x.reason === 'write_failed').length, 1);
  assert.doesNotMatch(JSON.stringify(f.events), /private|secret|payload/u);
  assert.deepEqual({...trace.latencyTraceStatus(), last_written_at: ''}, {
    schema_version: 1, component: 'desktop_latencytrace', enabled: true, state: 'degraded', reason: 'write_failed',
    coverage: 'instrumented_boundaries_only', evidence: 'incomplete', attempted: 4, written: 1, dropped: 0, lost: 3,
    metadata_rejected: 0, first_loss_reason: 'write_failed', last_written_at: '',
  });
});

test('Desktop record limit reports incomplete coverage and preserves cumulative loss', t => {
  const f = fixture(t);
  process.env.SPARKCLAW_PRIVATE_LATENCY_TRACE_DIR = f.root;
  const write = fs.writeSync;
  t.mock.method(fs, 'writeSync', (fd, data, ...rest) => fd === 2 ? write(fd, data, ...rest) : Buffer.byteLength(data));
  for (let i = 0; i < 100003; i++) trace.traceLatency('rpc.call');
  const status = trace.latencyTraceStatus();
  assert.equal(status.reason, 'record_limit'); assert.equal(status.state, 'degraded');
  assert.equal(status.attempted, 100003); assert.equal(status.written, 100000); assert.equal(status.lost, 3);
  assert.equal(f.events.filter(x => x.reason === 'record_limit').length, 1);
});

test('Desktop rejects unsafe metadata and health observer failure cannot escape', t => {
  const f = fixture(t);
  process.env.SPARKCLAW_PRIVATE_LATENCY_TRACE_DIR = f.root;
  trace.traceLatency('rpc.call', {request_id: 'private@example.test', operation: 'https://private.example/secret', body: 'secret-payload'});
  trace.traceLatency('private@example.test');
  const status = trace.latencyTraceStatus();
  assert.equal(status.reason, 'invalid_metadata'); assert.equal(status.metadata_rejected, 2);
  assert.equal(status.written, 1); assert.equal(status.lost, 1);
  const raw = fs.readFileSync(path.join(f.root, fs.readdirSync(f.root)[0]), 'utf8');
  assert.doesNotMatch(raw + JSON.stringify(f.events), /private|secret|payload/u);
  const next = path.join(f.root, 'next'); fs.mkdirSync(next, {mode: 0o700});
  process.env.SPARKCLAW_PRIVATE_LATENCY_TRACE_DIR = next;
  const write = fs.writeSync;
  t.mock.method(fs, 'writeSync', (fd, ...args) => {if (fd === 2) throw Object.assign(new Error('private pipe'), {code: 'EPIPE'}); return write(fd, ...args);});
  assert.doesNotThrow(() => trace.traceLatency('rpc.call'));
  assert.equal(trace.latencyTraceStatus().reason, 'health_observer_failed');
  assert.equal(trace.latencyTraceStatus().written, 1);
});
