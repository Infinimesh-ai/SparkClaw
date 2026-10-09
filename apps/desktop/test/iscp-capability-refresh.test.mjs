import assert from 'node:assert/strict';
import test from 'node:test';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { EventEmitter } from 'node:events';
import { PassThrough, Writable } from 'node:stream';
import { DesktopAuth } from '../src/main/desktop-auth.mjs';
import { ISCPTransport, ISCP_OPERATIONS, ISCPRequestNotSentError } from '../src/main/iscp-transport.mjs';
import { ISCP_SURFACES } from '../src/main/iscp-capabilities.mjs';

const scope = { deployment_id: 'deployment', owner_id: 'owner', client_id: 'client' };
const profile = 'sparkclaw.workbench.transport.v2';
const installationID = '11111111-1111-4111-8111-111111111111';
const operations = (JSON.parse(await fs.readFile(new URL('../src/shared/iscp-operations.json', import.meta.url)))).map(row => row.name);
const settle = () => new Promise(resolve => setImmediate(resolve));
const advance = async (t, milliseconds) => { t.mock.timers.tick(milliseconds); await settle(); };

async function fixture(t) {
  t.mock.timers.enable({ apis: ['setTimeout', 'Date'], now: Date.UTC(2026, 9, 9) });
  const directory = await fs.mkdtemp(path.join(await fs.realpath(os.tmpdir()), 'iscp-capability-refresh-'));
  const descriptor = { schema_version: 3, transport: 'iscp', origin: 'https://iscp.invalid', ...scope,
    domain_id: 'domain', initiator_device_id: 'desktop', responder_device_id: 'gateway', responder_key_thumbprint: 'a'.repeat(64), relay_url: 'https://relay.invalid', test_mode: true };
  const helper = path.join(directory, 'helper.json'), config = path.join(directory, 'profile.json');
  await fs.writeFile(helper, JSON.stringify({ schema_version: 1, mode: 'local-test', role: 'initiator', binding: scope }), { mode: 0o600 });
  await fs.writeFile(config, JSON.stringify({ schema_version: 1, transport: 'iscp', test_mode: true, backend: descriptor, helper_config: helper }), { mode: 0o600 });
  const f = { calls: [], states: [], reportTTL: 120000, session: 'session-1', revision: 1 };
  f.manifest = () => ({ schema_version: 2, profile, session_id: f.session, authorization_revision: f.revision,
    expires_at: new Date(Date.now() + 300000).toISOString(), binding: scope, operations });
  f.report = () => ({ ...f.manifest(), expires_at: new Date(Date.now() + f.reportTTL).toISOString(),
    capabilities: ISCP_SURFACES.map(id => ({ id, qualified: true, supported: true, permitted: true, dependencies_ready: true, enabled: true })) });
  const spawnProcess = () => {
    const child = new EventEmitter();
    child.stdout = new PassThrough(); child.stderr = new PassThrough(); child.exitCode = null;
    child.send = frame => child.stdout.write(JSON.stringify({ ipc_version: 1, ...frame }) + '\n');
    child.kill = () => { child.exitCode = 0; child.emit('exit', 0); };
    child.stdin = new Writable({ write(bytes, _encoding, done) {
      const frame = JSON.parse(bytes), request = frame.request;
      f.calls.push(request.operation);
      const respond = (body, status = 200) => child.send({ type: 'response', id: frame.id,
        response: { type: 'task.result', profile: request.profile, id: request.id, status, body } });
      if (request.operation === 'workbench.identity') respond({ schema_version: 1, ...scope });
      else if (request.operation === 'installation.bind') respond({ schema_version: 1, installation_id: installationID, ...scope });
      else if (request.operation === 'capabilities.get') {
        if (f.holdReport) f.releaseReport = respond;
        else respond(f.reportError ? { error: 'report rejected', error_code: f.reportError, retryable: true } : f.report(), f.reportError ? (f.reportStatus || 503) : 200);
      } else if (request.operation === 'events.ack') f.releaseACK = () => respond({ acknowledged: true });
      else throw new Error(`Unexpected operation ${request.operation}`);
      done();
    } });
    f.child = child;
    queueMicrotask(() => {
      child.send({ type: 'hello', operations: ISCP_OPERATIONS, max_request_bytes: 65536, max_response_bytes: 65536,
        identity: { domain_id: descriptor.domain_id, initiator_device_id: descriptor.initiator_device_id,
          responder_device_id: descriptor.responder_device_id, responder_key_thumbprint: descriptor.responder_key_thumbprint,
          relay_url: descriptor.relay_url, relay_profile: 'production' } });
      child.send({ type: 'capabilities', capabilities: f.manifest() });
      child.send({ type: 'state', state: 'transport_ready' });
    });
    return child;
  };
  let saved;
  f.auth = new DesktopAuth({ descriptorPath: path.join(directory, 'backend.json'), installationID,
    vault: { available: () => true, load: async () => saved, save: async value => { saved = value; }, clear: async () => { saved = undefined; } },
    iscpProfilePath: config, allowLocalISCPTest: true, onChange: state => f.states.push(state),
    transportFactory: options => {
      f.transportOptions = options;
      const transport = new ISCPTransport({ ...options, spawnProcess });
      const invoke = transport.invoke.bind(transport);
      transport.invoke = (...args) => {
        if (args[0] === 'capabilities.get') { f.reportAttempts = (f.reportAttempts || 0) + 1; if (f.localReportError) return Promise.reject(f.localReportError); }
        return invoke(...args);
      };
      return transport;
    } });
  t.after(async () => { f.auth.close(); t.mock.timers.reset(); await fs.rm(directory, { recursive: true, force: true }); });
  await f.auth.initialize();
  assert.equal(f.auth.status.capabilities.files, true);
  return f;
}

test('an occupied event ACK delays an unsent refresh briefly without closing current capabilities', async t => {
  const f = await fixture(t);
  await advance(t, 29999);
  const ack = f.auth.invokeISCP('events.ack', { cursor: 'opaque-test-cursor' });
  const statesBeforeRefresh = f.states.length;
  await advance(t, 1);
  assert.equal(f.auth.status.state, 'connected');
  for (const capability of ['files', 'mail', 'browser']) assert.equal(f.auth.status.capabilities[capability], true);
  assert.equal(f.calls.filter(op => op === 'capabilities.get').length, 1, 'blocked refresh never reached helper');
  f.releaseACK(); await ack;
  await advance(t, 249);
  assert.equal(f.calls.filter(op => op === 'capabilities.get').length, 1);
  await advance(t, 1);
  assert.equal(f.calls.filter(op => op === 'capabilities.get').length, 2);
  assert.ok(f.states.slice(statesBeforeRefresh).every(state => state.capabilities.files && state.capabilities.mail && state.capabilities.browser));
});

test('persistent control congestion has a bounded retry burst, fails closed, and eventually recovers', async t => {
  const f = await fixture(t);
  await advance(t, 29999);
  const ack = f.auth.invokeISCP('events.ack', { cursor: 'opaque-test-cursor' });
  for (const delay of [1, 250, 500, 1000]) {
    await advance(t, delay);
    assert.equal(f.auth.status.capabilities.files, true);
  }
  await advance(t, 2000);
  assert.equal(f.auth.status.capabilities.files, false);
  assert.equal(f.reportAttempts, 6, 'initial read plus five bounded refresh attempts');
  f.releaseACK(); await ack;
  await advance(t, 29999);
  assert.equal(f.reportAttempts, 6);
  assert.equal(f.auth.status.capabilities.files, false);
  await advance(t, 1);
  assert.equal(f.auth.status.capabilities.files, true);
  assert.equal(f.reportAttempts, 7);
});

for (const expires of ['report', 'manifest']) test(`${expires} expiry disables cached capabilities during a capacity retry`, async t => {
  const f = await fixture(t);
  if (expires === 'report') f.auth.capabilityReport.expires_at = new Date(Date.now() + 30100).toISOString();
  else f.auth.transport.capabilities = { ...f.auth.transport.capabilities, expires_at: new Date(Date.now() + 30100).toISOString() };
  // Receiving a fresh manifest publishes the projection and its new expiry.
  f.transportOptions.onCapabilities(f.auth.transport.capabilities);
  await advance(t, 29999);
  const ack = f.auth.invokeISCP('events.ack', { cursor: 'opaque-test-cursor' });
  await advance(t, 1);
  assert.equal(f.auth.status.capabilities.files, true);
  await advance(t, 100);
  assert.equal(f.auth.status.capabilities.files, false, 'TTL is enforced before the scheduled 250ms retry');
  f.releaseACK(); await ack;
  if (expires === 'manifest') {
    f.child.send({ type: 'capabilities', capabilities: f.manifest() });
  }
  await advance(t, 150);
  assert.equal(f.auth.status.capabilities.files, true);
});

test('report expiry also closes capability state while a refresh response is in flight', async t => {
  const f = await fixture(t);
  f.auth.capabilityReport.expires_at = new Date(Date.now() + 30100).toISOString();
  f.transportOptions.onCapabilities(f.auth.transport.capabilities);
  f.holdReport = true;
  await advance(t, 30000);
  assert.equal(f.auth.status.capabilities.files, true);
  await advance(t, 100);
  assert.equal(f.auth.status.capabilities.files, false);
  f.releaseReport(f.report()); await settle();
  assert.equal(f.auth.status.capabilities.files, true);
});

for (const error of [new Error('unknown outcome'), new ISCPRequestNotSentError('not ready', 'unavailable'), Object.assign(new Error('not a typed local error'), { reason: 'capacity' })]) {
  test(`non-capacity refresh error stays fail-closed: ${error.message}`, async t => {
    const f = await fixture(t);
    f.localReportError = error;
    await advance(t, 30000);
    assert.equal(f.auth.status.capabilities.files, false);
    await advance(t, 3750);
    assert.equal(f.reportAttempts, 2, 'no short retry for unproven local admission');
  });
}

test('a remote retryable failure is not mistaken for local unsent capacity', async t => {
  const f = await fixture(t);
  f.reportError = 'throttled';
  f.reportStatus = 429;
  await advance(t, 30000);
  assert.equal(f.auth.status.capabilities.files, false);
  await advance(t, 3750);
  assert.equal(f.reportAttempts, 2);
});

test('verified revocation during the short retry cancels it and never revives capabilities', async t => {
  const f = await fixture(t);
  await advance(t, 29999);
  const ack = f.auth.invokeISCP('events.ack', { cursor: 'opaque-test-cursor' }).catch(() => {});
  await advance(t, 1);
  f.child.send({ type: 'state', state: 'authorization_revoked' });
  await ack;
  assert.equal(f.auth.status.state, 'invalid_authentication');
  assert.equal(f.auth.status.capabilities.files, false);
  const attempts = f.reportAttempts;
  await advance(t, 120000);
  assert.equal(f.reportAttempts, attempts);
  assert.equal(f.auth.status.capabilities.files, false);
});

for (const failure of [false, true]) test(`a stale retry ${failure ? 'failure' : 'success'} cannot overwrite the new session report`, async t => {
  const f = await fixture(t);
  const originalInvoke = f.auth.transport.invoke.bind(f.auth.transport);
  let releaseOld;
  f.auth.transport.invoke = (operation, ...args) => {
    if (operation === 'capabilities.get') return new Promise((resolve, reject) => { releaseOld = failure ? () => reject(new Error('old session lost')) : () => resolve(oldReport); });
    return originalInvoke(operation, ...args);
  };
  const oldReport = f.report();
  const oldRetry = f.auth.retry();
  await settle();
  assert.ok(releaseOld);
  f.auth.generation++;
  f.session = 'session-2'; f.revision = 2;
  f.auth.transport.capabilities = f.manifest();
  f.auth.transport.invoke = originalInvoke;
  await f.auth.retry();
  const acceptedReport = f.auth.capabilityReport;
  releaseOld(); await oldRetry;
  assert.equal(f.auth.capabilityReport, acceptedReport);
  assert.equal(f.auth.capabilityReport.session_id, 'session-2');
  assert.equal(f.auth.capabilityReport.authorization_revision, 2);
  assert.equal(f.auth.status.capabilities.files, true);
});
