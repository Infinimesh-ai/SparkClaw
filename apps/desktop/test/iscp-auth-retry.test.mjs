import assert from 'node:assert/strict';
import test from 'node:test';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import crypto from 'node:crypto';
import { DesktopAuth } from '../src/main/desktop-auth.mjs';
import { parseBackendDescriptor } from '../src/main/local-backend.mjs';

const scope = { deployment_id: 'deployment', owner_id: 'owner', client_id: 'client' };
const installationID = '11111111-1111-4111-8111-111111111111';
const settle = () => new Promise(resolve => setImmediate(resolve));
function deferred() { let resolve; const promise = new Promise(value => { resolve = value; }); return { promise, resolve }; }

async function fixture(t, options = {}) {
  const root = await fs.mkdtemp(path.join(await fs.realpath(os.tmpdir()), 'iscp-auth-retry-'));
  const descriptor = { schema_version: 4, mode: 'node-managed', transport: 'iscp', test_mode: false, origin: 'https://iscp.invalid', ...scope,
    domain_id: 'domain', initiator_device_id: 'desktop', responder_device_id: 'responder', responder_key_thumbprint: 'a'.repeat(43), relay_url: 'https://127.0.0.1', relay_profile: 'production' };
  const helper = path.join(root, 'workbench.json'), profile = path.join(root, 'connection.profile.json');
  await fs.writeFile(helper, JSON.stringify({ schema_version: 2, mode: 'node-managed', role: 'initiator', relay_profile: 'production', binding: scope }), { mode: 0o600 });
  await fs.writeFile(profile, JSON.stringify({ schema_version: 2, mode: 'node-managed', transport: 'iscp', backend: descriptor, helper_config: helper }), { mode: 0o600 });
  const binding = crypto.createHash('sha256').update(JSON.stringify([descriptor.origin, scope.deployment_id, scope.owner_id, '', '', 'iscp', scope.client_id, descriptor.domain_id, descriptor.initiator_device_id, descriptor.responder_device_id, descriptor.responder_key_thumbprint, descriptor.relay_url, descriptor.relay_profile])).digest('hex');
  let saved = { schema_version: 2, transport: 'iscp', mode: 'node-managed', testMode: false, configPath: profile, binding,
    origin: descriptor.origin, deploymentID: scope.deployment_id, ownerID: scope.owner_id, clientID: scope.client_id, identityVerified: false };
  const f = { calls: [], states: [], saves: 0, transports: [], identityStarted: deferred(), identityGate: options.holdIdentity ? deferred() : undefined, responseDelay: options.responseDelay || 0 };
  f.makeTransport = transportOptions => {
    const transport = { options: transportOptions, timeoutMS: 30000, closed: false,
      capabilities: { schema_version: 2, profile: 'sparkclaw.workbench.transport.v2', session_id: 'session', authorization_revision: 1, expires_at: new Date(Date.now() + 300000).toISOString(), binding: scope, operations: [] },
      async start() { transportOptions.onState('connecting'); transportOptions.onState('transport_ready'); },
      close() { this.closed = true; },
      async fetch(url, init) {
        if (this.closed) throw new Error('closed previous transport');
        const identity = url.endsWith('/identity');
        f.calls.push({ type: identity ? 'identity' : 'installation', transport: this });
        if (identity) { f.identityStarted.resolve(); if (this.identityGate || f.identityGate) await (this.identityGate || f.identityGate).promise; }
        if (f.responseDelay) await new Promise((resolve, reject) => {
          const timer = setTimeout(resolve, f.responseDelay);
          init.signal.addEventListener('abort', () => { clearTimeout(timer); reject(new Error('identity deadline')); }, { once: true });
        });
        return new Response(JSON.stringify({ schema_version: 1, ...scope,
          ...(identity ? options.wrongIdentity ? { owner_id: 'other-owner' } : {} : { installation_id: options.wrongInstallation ? 'other-installation' : JSON.parse(init.body).installation_id }) }));
      },
      async invoke(operation) {
        f.calls.push({ type: operation, transport: this });
        return { schema_version: 2, profile: this.capabilities.profile, session_id: options.wrongReport ? 'other-session' : this.capabilities.session_id,
          authorization_revision: 1, expires_at: new Date(Date.now() + 120000).toISOString(), capabilities: [] };
      } };
    f.transports.push(transport); return transport;
  };
  f.auth = new DesktopAuth({ installationID, descriptorPath: path.join(root, 'backend.json'), iscpProfilePath: profile, allowNodeManagedISCP: true,
    vault: { available: () => true, load: async () => saved, save: async value => { saved = value; f.saves++; }, clear: async () => { saved = undefined; } },
    transportFactory: f.makeTransport, onChange: state => f.states.push({ state: state.state, calls: f.calls.map(call => call.type) }) });
  t.after(async () => { f.auth.close(); await fs.rm(root, { recursive: true, force: true }); });
  return f;
}

test('Ready callbacks and concurrent explicit retries share one complete identity/installation/capability verification', async t => {
  const f = await fixture(t, { holdIdentity: true });
  const initialize = f.auth.initialize(); await f.identityStarted.promise;
  const first = f.auth.retry(), second = f.auth.retry(); assert.equal(first, second);
  f.auth.needsIdentity = true; f.transports[0].options.onState('transport_ready');
  await settle();
  assert.equal(f.calls.filter(call => call.type === 'identity').length, 1);
  assert.ok(!f.states.some(row => row.state === 'connected'));
  f.identityGate.resolve();
  assert.equal((await initialize).state, 'connected'); assert.equal((await first).state, 'connected');
  assert.deepEqual(f.calls.map(call => call.type), ['identity', 'installation', 'capabilities.get']);
  assert.equal(f.saves, 1);
  assert.ok(f.states.filter(row => row.state === 'connected').every(row => row.calls.includes('installation') && row.calls.includes('capabilities.get')));
});

test('a stale generation/transport cannot publish over or clear the newer pending verification', async t => {
  for (const changeGeneration of [true, false]) {
    const f = await fixture(t, { holdIdentity: true });
    const original = f.auth.initialize(); await f.identityStarted.promise;
    const first = f.transports[0]; first.identityGate = f.identityGate;
    if (changeGeneration) f.auth.suspend(); else first.close();
    f.identityGate = undefined;
    const current = f.makeTransport(first.options); current.identityGate = deferred(); f.auth.transport = current;
    const pending = f.auth.retry(); await settle();
    first.identityGate.resolve(); await original;
    assert.equal(f.auth.retry(), pending, 'completion of the stale flow cannot clear the new single flight');
    assert.ok(!f.states.some(row => row.state === 'connected'));
    current.identityGate.resolve();
    assert.equal((await pending).state, 'connected'); assert.equal(f.auth.transport, current); assert.equal(f.saves, 1);
  }
});

test('public ISCP identity and installation accept a six-second drain roundtrip using the existing 30-second protocol budget', async t => {
  t.mock.timers.enable({ apis: ['Date', 'setTimeout'], now: Date.UTC(2026, 9, 12) });
  const budgets = [];
  t.mock.method(AbortSignal, 'timeout', milliseconds => {
    budgets.push(milliseconds); const controller = new AbortController(); setTimeout(() => controller.abort(), milliseconds); return controller.signal;
  });
  const f = await fixture(t, { responseDelay: 6000 });
  const pending = f.auth.initialize(); await f.identityStarted.promise;
  t.mock.timers.tick(6000); await settle();
  assert.deepEqual(f.calls.map(call => call.type), ['identity', 'installation']);
  assert.ok(!f.states.some(row => row.state === 'connected'));
  t.mock.timers.tick(6000); await settle();
  assert.equal((await pending).state, 'connected'); assert.deepEqual(budgets, [30000, 30000]);
});

test('LAN retains its five-second identity deadline and changed identity/installation still fail closed', async t => {
  for (const options of [{ wrongIdentity: true }, { wrongInstallation: true }]) {
    const f = await fixture(t, options);
    assert.equal((await f.auth.initialize()).state, 'identity_conflict');
    assert.ok(!f.states.some(row => row.state === 'connected')); assert.equal(f.saves, 0);
  }
  const f = await fixture(t, { wrongReport: true });
  assert.equal((await f.auth.initialize()).state, 'connected');
  assert.ok(f.calls.some(call => call.type === 'capabilities.get'));
  assert.equal(f.auth.capabilityReport, undefined, 'a report for a different session is never retained');
  assert.equal(f.auth.status.capabilities.files, false, 'an invalid report cannot enable any capability');
  const budgets = [];
  t.mock.method(AbortSignal, 'timeout', milliseconds => { budgets.push(milliseconds); return new AbortController().signal; });
  const direct = new DesktopAuth({ installationID, vault: { clear: async () => {} }, fetcher: async (url, init) => new Response(JSON.stringify({ schema_version: 1, ...scope, ...(url.endsWith('/installations') ? { installation_id: JSON.parse(init.body).installation_id } : {}) })) });
  direct.descriptor = parseBackendDescriptor({ schema_version: 1, origin: 'http://127.0.0.1:18790', deployment_id: scope.deployment_id });
  direct.connection = { origin: direct.descriptor.origin, deploymentID: scope.deployment_id, ownerID: scope.owner_id, clientID: scope.client_id, authorization: 'Bearer synthetic' };
  assert.equal((await direct.retry()).state, 'connected'); assert.deepEqual(budgets, [5000, 5000]);
});

test('verified helper revocation stays terminal after close and retry without losing its UI reason', async t => {
  const f = await fixture(t);
  assert.equal((await f.auth.initialize()).state, 'connected');
  const transport = f.transports[0], before = f.calls.length;
  transport.options.onState('authorization_revoked');
  assert.equal(f.auth.status.state, 'invalid_authentication');
  assert.equal(f.auth.status.transport_stage, 'authorization_revoked');
  transport.options.onState('closed');
  const retried = await f.auth.retry();
  assert.equal(retried.state, 'invalid_authentication');
  assert.equal(retried.transport_stage, 'authorization_revoked');
  assert.equal(f.calls.length, before, 'retry must not reconnect or resubmit under revoked authority');
  assert.equal((await f.auth.authorizedFetch('https://iscp.invalid/api/owner')).status, 401);
});

test('ordinary helper disconnect preserves temporary recovery without claiming revoked authority', async t => {
  const f = await fixture(t);
  await f.auth.initialize();
  f.transports[0].options.onState('disconnected');
  assert.equal(f.auth.status.state, 'service_unavailable');
  assert.equal(f.auth.status.transport_stage, 'disconnected');
  assert.equal(f.auth.authorizationRevoked, undefined);
  assert.equal((await f.auth.retry()).state, 'connected');
});
