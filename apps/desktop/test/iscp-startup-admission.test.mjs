import assert from 'node:assert/strict';
import test from 'node:test';
import { EventEmitter } from 'node:events';
import { PassThrough, Writable } from 'node:stream';
import { DesktopAuth } from '../src/main/desktop-auth.mjs';
import { ISCPTransport, ISCP_OPERATIONS, ISCPRequestNotSentError } from '../src/main/iscp-transport.mjs';
import { parseBackendDescriptor } from '../src/main/local-backend.mjs';

const origin = 'https://iscp.invalid';
const scope = { deployment_id: 'deployment', owner_id: 'owner', client_id: 'client' };
const paths = ['/api/config', '/readyz', '/api/owner', '/api/notifications', '/api/clients'];
const settle = () => new Promise(resolve => setImmediate(resolve));

function authenticated(t, fetcher, transport) {
  const auth = new DesktopAuth({ vault: {}, fetcher });
  auth.descriptor = parseBackendDescriptor({ schema_version: 4, transport: 'iscp', mode: 'node-managed', test_mode: false,
    origin, ...scope, domain_id: 'domain', initiator_device_id: 'desktop', responder_device_id: 'gateway',
    responder_key_thumbprint: 'a'.repeat(43), relay_url: 'https://127.0.0.1', relay_profile: 'production' });
  auth.connection = Object.freeze({ transport: 'iscp', origin, deploymentID: scope.deployment_id, ownerID: scope.owner_id, clientID: scope.client_id, identityVerified: true });
  auth.transport = transport || { capabilities: {}, fetch: fetcher, close() {} };
  auth.status = Object.freeze({ state: 'connected' });
  t.after(() => auth.close());
  return auth;
}

async function realTransport(t) {
  const calls = [], children = [], held = []; let transport, maximum = 0;
  const send = (call, child, status = 200) => child.stdout.write(`${JSON.stringify({ ipc_version: 1, type: 'response', id: call.id,
    response: { type: 'task.result', profile: call.request.profile, id: call.id, status, body: call.request.operation === 'clients.list' ? { clients: [] } : { ok: true } } })}\n`);
  const spawnProcess = () => {
    const child = new EventEmitter(); child.stdout = new PassThrough(); child.stderr = new PassThrough(); child.exitCode = null;
    child.kill = () => { child.exitCode = 0; child.emit('exit', 0); };
    child.stdin = new Writable({ write(bytes, _encoding, done) {
      const call = JSON.parse(bytes); calls.push(call); maximum = Math.max(maximum, transport.pending.size);
      if (calls.length <= 4) held.push(() => send(call, child)); else send(call, child);
      done();
    } });
    children.push(child);
    queueMicrotask(() => {
      child.stdout.write(`${JSON.stringify({ ipc_version: 1, type: 'hello', operations: ISCP_OPERATIONS, max_request_bytes: 65536, max_response_bytes: 65536 })}\n`);
      child.stdout.write(`${JSON.stringify({ ipc_version: 1, type: 'state', state: 'transport_ready' })}\n`);
    });
    return child;
  };
  transport = new ISCPTransport({ origin, configPath: '/private/synthetic.json', spawnProcess, timeoutMS: 30000 });
  t.after(() => transport.close()); await transport.start();
  children[0].stdout.write(`${JSON.stringify({ ipc_version: 1, type: 'capabilities', capabilities: { schema_version: 2,
    profile: 'sparkclaw.workbench.transport.v2', session_id: 'session', authorization_revision: 1,
    expires_at: new Date(Date.now() + 60000).toISOString(), binding: scope,
    operations: [...ISCP_OPERATIONS, 'settings.owner.get', 'notifications.list', 'clients.list'] } })}\n`);
  return { transport, calls, held, get maximum() { return maximum; } };
}

test('fixed startup GETs recover real four-business-slot contention without sending duplicates or changing quota', async t => {
  const f = await realTransport(t), auth = authenticated(t, undefined, f.transport);
  const blockers = paths.slice(0, 4).map(path => f.transport.fetch(`${origin}${path}`));
  await settle(); assert.equal(f.transport.pending.size, 4);
  const reads = paths.map(path => auth.authorizedFetch(`${origin}${path}`));
  await settle(); assert.equal(f.calls.length, 4, 'capacity waits cannot write an RPC');
  f.held.forEach(release => release());
  await Promise.all(blockers);
  const responses = await Promise.all(reads);
  for (const response of responses) { assert.equal(response.status, 200); await response.json(); }
  assert.equal(f.calls.length, 9, 'each startup read is dispatched only once after admission');
  assert.ok(f.maximum <= 4); assert.equal(auth.status.state, 'connected'); assert.equal(auth.generation, 0);
});

test('capacity waits share one 30-second total deadline and never renew it on retries', async t => {
  t.mock.timers.enable({ apis: ['Date', 'setTimeout'], now: 0 });
  const budgets = [], signals = [];
  t.mock.method(AbortSignal, 'timeout', milliseconds => { budgets.push(milliseconds); const controller = new AbortController(); setTimeout(() => controller.abort(), milliseconds); return controller.signal; });
  let attempts = 0;
  const auth = authenticated(t, async (_url, init) => { attempts++; signals.push(init.signal); throw new ISCPRequestNotSentError('full', 'capacity'); });
  const pending = assert.rejects(auth.authorizedFetch(`${origin}/api/config`), error => error instanceof ISCPRequestNotSentError && error.reason === 'canceled');
  await settle();
  for (let elapsed = 0; elapsed < 30000; elapsed += 250) { t.mock.timers.tick(250); await settle(); }
  await pending;
  assert.deepEqual(budgets, [30000]); assert.ok(attempts > 1 && attempts <= 121);
  assert.ok(signals.every(signal => signal === signals[0])); assert.equal(auth.requests.size, 0); assert.equal(auth.status.state, 'connected');
  const before = attempts; t.mock.timers.tick(1000); await settle(); assert.equal(attempts, before);
});

test('waiting startup reads cannot send after caller abort, generation change, transport replacement or authentication loss', async t => {
  for (const transition of ['abort', 'generation', 'transport', 'authentication']) {
    let attempts = 0; const controller = new AbortController();
    const auth = authenticated(t, async () => { attempts++; throw new ISCPRequestNotSentError('full', 'capacity'); });
    const pending = assert.rejects(auth.authorizedFetch(`${origin}/api/owner`, { signal: controller.signal }), error => error instanceof ISCPRequestNotSentError && error.reason === 'canceled');
    await settle(); assert.equal(attempts, 1);
    if (transition === 'abort') controller.abort();
    else if (transition === 'generation') ++auth.generation;
    else if (transition === 'transport') auth.transport = { ...auth.transport, fetch() { throw new Error('must not use replacement'); } };
    else auth.status = Object.freeze({ state: 'locked' });
    await pending; assert.equal(attempts, 1); assert.equal(auth.requests.size, 0); assert.equal(auth.status.state, transition === 'authentication' ? 'locked' : 'connected');
  }
});

test('sent errors, remote failures, authentication and every mutation retain their original single-attempt behavior', async t => {
  for (const outcome of ['sent error', 429, 503, 401, 403]) {
    let attempts = 0;
    const auth = authenticated(t, async () => { attempts++; if (outcome === 'sent error') throw new Error('sent reply lost'); return new Response(null, { status: outcome }); });
    if (outcome === 'sent error') await assert.rejects(auth.authorizedFetch(`${origin}/api/config`), /unavailable/);
    else assert.equal((await auth.authorizedFetch(`${origin}/api/config`)).status, outcome);
    assert.equal(attempts, 1);
  }
  for (const [path, init] of [['/api/owner', { method: 'PATCH', body: '{}' }], ['/api/clients', { method: 'POST', body: '{}' }], ['/api/config', { method: 'PUT', body: '{}' }], ['/api/clients/client/revoke', { method: 'POST', body: '{}' }], ['/api/workbench/identity', {}], ['/api/config?different=1', {}], ['/api/config', { body: '{}' }]]) {
    let attempts = 0;
    const auth = authenticated(t, async () => { attempts++; throw new ISCPRequestNotSentError('full', 'capacity'); });
    await assert.rejects(auth.authorizedFetch(`${origin}${path}`, init), error => error instanceof ISCPRequestNotSentError && error.reason === 'capacity');
    assert.equal(attempts, 1); assert.equal(auth.status.state, 'connected');
  }
});

test('LAN startup reads keep their original behavior and authorization header', async t => {
  let attempts = 0;
  const auth = authenticated(t, async (_url, init) => { attempts++; assert.equal(init.headers.get('authorization'), 'Bearer synthetic'); throw new ISCPRequestNotSentError('full', 'capacity'); });
  auth.descriptor = parseBackendDescriptor({ schema_version: 1, origin: 'http://127.0.0.1:18790', deployment_id: scope.deployment_id });
  auth.connection = { ...auth.connection, origin: auth.descriptor.origin, transport: undefined, authorization: 'Bearer synthetic' };
  await assert.rejects(auth.authorizedFetch(`${auth.descriptor.origin}/api/config`), error => error instanceof ISCPRequestNotSentError && error.reason === 'capacity');
  assert.equal(attempts, 1);
});
