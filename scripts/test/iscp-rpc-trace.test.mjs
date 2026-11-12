import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

test('correlates both encrypted directions and reports a late reply without reading payloads', t => {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), 'rpc-trace-query-'));
  t.after(() => fs.rmSync(directory, { recursive: true, force: true }));
  const script = fileURLToPath(new URL('../iscp-rpc-trace.mjs', import.meta.url));
  const rpc = '11111111-1111-4111-8111-111111111111';
  const request = '22222222-2222-4222-8222-222222222222';
  const outgoing = '33333333-3333-4333-8333-333333333333';
  const incoming = '44444444-4444-4444-8444-444444444444';
  const other = '55555555-5555-4555-8555-555555555555';
  const paths = ['helper.ndjson', 'relay.log', 'gateway.ndjson'].map(name => path.join(directory, name));
  const log = (filename, rows) => fs.writeFileSync(filename, rows.map(row => typeof row === 'string' ? row : JSON.stringify(row)).join('\n') + '\n');
  log(paths[0], [
    {rpc_id: rpc, envelope_id: outgoing, stage: 'rpc.request_received'},
    {rpc_id: rpc, envelope_id: incoming, stage: 'rpc.envelope_created', body: 'PRIVATE_CONTENT'},
  ]);
  log(paths[1], [
    `level=INFO msg="workbench envelope trace" event=workbench_envelope_trace role=relay stage=relay.delivery_commit envelope_id=${outgoing} status=ok`,
    {MESSAGE: `level=INFO event=workbench_envelope_trace role=relay stage=relay.delivery_write envelope_id=${incoming} status=ok`},
    {event: 'workbench_envelope_trace', stage: 'relay.delivery_write', envelope_id: other},
    {event: 'workbench_envelope_trace_health', status: 'dropped', count: 4},
  ]);
  log(paths[2], [
    {rpc_id: rpc, request_id: request, envelope_id: outgoing, stage: 'rpc.envelope_created'},
    {rpc_id: rpc, request_id: request, stage: 'rpc.call', status: 'deadline'},
    {rpc_id: rpc, envelope_id: incoming, stage: 'rpc.response_dropped', reason: 'no_pending_call'},
  ]);
  for (const [flag, id] of [['--request-id', request], ['--rpc-id', rpc]]) {
    const raw = execFileSync(process.execPath, [script, flag, id, ...paths], { encoding: 'utf8' });
    assert.doesNotMatch(raw, /PRIVATE_CONTENT/u);
    const trace = JSON.parse(raw);
    assert.deepEqual(trace.rpc_ids, [rpc]);
    assert.deepEqual(new Set(trace.envelope_ids), new Set([outgoing, incoming]));
    assert.equal(trace.events.filter(row => row.role === 'relay').length, 2);
    assert.ok(trace.events.some(row => row.stage === 'rpc.response_dropped' && row.reason === 'no_pending_call'));
    assert.ok(trace.events.some(row => row.stage === 'rpc.call' && row.status === 'deadline'));
    assert.equal(trace.trace_health[0].count, 4);
  }
});
