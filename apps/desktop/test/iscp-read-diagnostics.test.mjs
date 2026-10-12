import assert from 'node:assert/strict';
import test from 'node:test';
import { traceISCPRead } from '../src/main/iscp-read-diagnostics.mjs';
import { DesktopAuth } from '../src/main/desktop-auth.mjs';

function capture(t, enabled) {
  const previous = process.env.SPARKCLAW_PRIVATE_ISCP_READ_DIAGNOSTICS;
  if (enabled) process.env.SPARKCLAW_PRIVATE_ISCP_READ_DIAGNOSTICS = '1'; else delete process.env.SPARKCLAW_PRIVATE_ISCP_READ_DIAGNOSTICS;
  t.after(() => { if (previous === undefined) delete process.env.SPARKCLAW_PRIVATE_ISCP_READ_DIAGNOSTICS; else process.env.SPARKCLAW_PRIVATE_ISCP_READ_DIAGNOSTICS = previous; });
  const rows = []; t.mock.method(console, 'error', value => rows.push(JSON.parse(value)));
  return rows;
}

function auth() {
  return { descriptor: { transport: 'iscp' }, status: { state: 'connected' }, generation: 2,
    transport: { state: 'transport_ready', pending: new Map(), mailReadQueue: new Set() } };
}

test('private diagnostics are silent by default and excluded from LAN and arbitrary paths', t => {
  const rows = capture(t, false), a = auth();
  traceISCPRead({ path: '/api/config', phase: 'failure', auth: a, error: new Error('ISCP request deadline exceeded') });
  assert.equal(rows.length, 0);
  process.env.SPARKCLAW_PRIVATE_ISCP_READ_DIAGNOSTICS = '1';
  traceISCPRead({ path: '/api/config?credential=private', phase: 'failure', auth: a });
  traceISCPRead({ path: '/api/files/private-file', phase: 'failure', auth: a });
  traceISCPRead({ path: '/api/config', phase: 'failure', auth: { ...a, descriptor: { transport: 'lan' } } });
  assert.equal(rows.length, 0);
});

test('enabled logs contain only fixed metadata and static errors, never credentials, headers, bodies, stacks or identifiers', t => {
  const rows = capture(t, true), a = auth(), secret = 'synthetic-secret-credential-sentinel';
  a.authorization = secret;
  a.transport.pending.set(secret, { request: { operation: 'presentation.config', body: secret, id: secret }, capacityClass: 'business', diagnosticStartedMS: Date.now() - 1000 });
  a.transport.pending.set('other', { request: { operation: secret }, capacityClass: secret, diagnosticStartedMS: Date.now() });
  const error = new Error(secret); error.name = secret; error.stack = secret; error.reason = secret;
  traceISCPRead({ path: '/api/config', phase: 'failure', auth: a, error, generation: 1, headers: { authorization: secret }, body: secret });
  const text = JSON.stringify(rows); assert.ok(!text.includes(secret));
  assert.equal(rows[0].error_name, 'unclassified'); assert.equal(rows[0].error_message, 'unclassified');
  assert.equal(rows[0].generation, 1); assert.equal(rows[0].current_generation, 2);
  assert.equal(rows[0].pending_operations[0].operation, 'presentation.config');
  assert.equal(rows[0].pending_operations[0].capacity_class, 'business'); assert.ok(rows[0].pending_operations[0].age_ms >= 1000);
  assert.equal(rows[0].pending_operations[1].operation, 'unknown'); assert.equal(rows[0].pending_operations[1].capacity_class, 'unknown');
});

test('auth logs the original static failure before proxy wrapping and distinguishes deadline from caller abort', async t => {
  const rows = capture(t, true);
  const a = new DesktopAuth({ vault: {} }); a.descriptor = { schemaVersion: 4, transport: 'iscp', origin: 'https://iscp.invalid' };
  a.connection = { transport: 'iscp', origin: a.descriptor.origin }; a.status = { state: 'connected' };
  a.transport = { state: 'transport_ready', capabilities: {}, close() {}, fetch: async () => { throw new Error('ISCP request deadline exceeded'); } };
  t.after(() => a.close());
  await assert.rejects(a.authorizedFetch('https://iscp.invalid/api/config'), /Backend request is unavailable/);
  assert.deepEqual(rows.map(row => row.phase), ['request', 'failure']);
  assert.equal(rows[1].error_message, 'ISCP request deadline exceeded'); assert.equal(rows[1].auth_state, 'connected');
  assert.equal(rows[1].signal_aborted, false); assert.equal(rows[1].caller_aborted, false); assert.equal(rows[1].deadline_aborted, false);
  assert.equal(a.status.state, 'service_unavailable');
});
