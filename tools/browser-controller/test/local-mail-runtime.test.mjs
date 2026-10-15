import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import {setTimeout as delay} from 'node:timers/promises';
import {LocalMailRuntimeFactory} from '../src/local-mail-runtime.mjs';

// Real bindings, Host protocol and lane scheduling; only the browser driver
// and provider handlers are replaced. Never touches a running deployment.
async function fixture(t, handlers = {}, reuseRoot) {
  // Darwin's per-user temporary prefix leaves little Unix-socket path space.
  const root = reuseRoot ?? await fs.realpath(await fs.mkdtemp(path.join(os.tmpdir(), 'lm-')));
  t.after(() => fs.rm(root, {recursive: true, force: true}));
  const factory = new LocalMailRuntimeFactory({runtimeRoot: path.join(root, 'cli-runtime'), stateDirectory: path.join(root, 'state'),
    emailWorkspaceRoot: path.join(root, 'ws'), fixtureAssembly: true});
  await factory.prepare();
  const counts = {created: 0, closed: 0, calls: []};
  Object.assign(factory.driver, {
    async create() {counts.created++; return {activities: new Map()};},
    async beginActivity() {}, async updateLease() {}, async revokeActivity() {},
    async call(handle, method, args) {counts.calls.push(method); return method === 'currentURL' ? 'https://wx.mail.qq.com/' : null;},
    async close() {counts.closed++;},
  });
  for (const [key, entry] of factory.commands) {
    const name = `${entry.spec.handler}`;
    if (handlers[name]) factory.commands.set(key, {...entry, handler: handlers[name], output: () => true});
  }
  const diagnostics = [];
  factory.diagnostic = event => diagnostics.push(event);
  t.after(() => factory.close());
  return {factory, counts, diagnostics, root};
}

function request(factory, provider, operation, input, overrides = {}) {
  const spec = factory.describe(provider, operation);
  return {token: 't'.repeat(43), taskID: overrides.taskID ?? `task-${Math.random().toString(16).slice(2)}`, credentialGeneration: 1,
    provider, operation, scriptID: spec.script_id, revision: spec.revision, input, ...overrides};
}
const scope = 'a'.repeat(64);
const discover = {schema_version: 1, operation: 'discover', provider: 'qq_mail', invocation_id: 'inv-1', account: 'default', owner_scope: scope,
  discovery: {account_address: 'owner@qq.com', continuation: '', lane: 'recent_inbound', limit: 10,
    interval_start: '2026-10-13T05:04:52Z', interval_end: '2026-10-15T04:00:00Z'}};

test('runs a provider command through the in-process Host and returns the controller envelope', async t => {
  const {factory, counts} = await fixture(t, {'qq_mail.discover': {async run(input, context) {
    return {data: {url: await context.browser.call('currentURL')}};
  }}});
  const spec = factory.describe('qq_mail', 'discover');
  const result = await factory.runScript(request(factory, 'qq_mail', 'discover', discover));
  assert.deepEqual(result, {state: 'completed', result: {url: 'https://wx.mail.qq.com/'}, sourceChecksum: spec.source_checksum});
  assert.equal(counts.created, 1); assert.equal(counts.closed, 1);
  assert.equal(factory.info().app_cli, 'local-mail-runtime');
});

test('keeps no per-task state after completion, unlike the capped App-CLI ledger', async t => {
  const {factory} = await fixture(t, {'qq_mail.discover': {async run() {return {data: {}};}}});
  for (let index = 0; index < 10001; index++) {
    assert.equal((await factory.runScript(request(factory, 'qq_mail', 'discover', discover))).state, 'completed');
  }
  assert.equal(factory.authorization.records.size, 0);
  assert.equal(factory.inflight.size, 0);
  assert.equal(factory.host.leases.size, 0);
  assert.equal(factory.host.resources.size, 0);
});

test('maps blocked and effect-started failures to the former stable codes', async t => {
  const {factory, diagnostics} = await fixture(t, {
    'qq_mail.discover': {async run() {throw Object.assign(new Error('x'), {code: 'BROWSER_EXTENSION_UNAVAILABLE'});}},
    'qq_mail.send': {async run(input, context) {context.beforeEffect(); throw Object.assign(new Error('x'), {code: 'EMAIL_SEND_FAILED'});}},
  });
  const blocked = await factory.runScript(request(factory, 'qq_mail', 'discover', discover));
  assert.equal(blocked.state, 'failed');
  assert.equal(blocked.result.code, 'browser_extension_unavailable');
  assert.equal(blocked.task.status, 'blocked');
  const send = {schema_version: 1, operation: 'send', provider: 'qq_mail', invocation_id: 'inv-2', account: 'default',
    message: {recipient: 'a@example.test', body: {format: 'text', content: 'x'}}};
  const unknown = await factory.runScript(request(factory, 'qq_mail', 'send', send));
  assert.equal(unknown.result.code, 'send_outcome_unknown');
  assert.equal(unknown.task.status, 'uncertain');
  assert.deepEqual(diagnostics, []);
});

test('rejects a stale script revision without running the handler', async t => {
  let ran = false;
  const {factory, diagnostics} = await fixture(t, {'qq_mail.discover': {async run() {ran = true; return {data: {}};}}});
  await assert.rejects(factory.runScript(request(factory, 'qq_mail', 'discover', discover, {revision: 99})),
    error => error.code === 'browser_script_unavailable');
  assert.equal(ran, false);
  assert.deepEqual(diagnostics.map(value => value.code), ['RELEASE_MISMATCH']);
});

test('joins a retried Gateway call for the same task instead of running it twice', async t => {
  let runs = 0, release;
  const gate = new Promise(resolve => {release = resolve;});
  const {factory} = await fixture(t, {'qq_mail.discover': {async run() {runs++; await gate; return {data: {runs}};}}});
  const first = factory.runScript(request(factory, 'qq_mail', 'discover', discover, {taskID: 'same-task'}));
  const second = factory.runScript(request(factory, 'qq_mail', 'discover', discover, {taskID: 'same-task'}));
  release();
  assert.deepEqual((await first).result, {runs: 1});
  assert.deepEqual((await second).result, {runs: 1});
  assert.equal(runs, 1);
});

test('starts, reports, renews and stops a provider watch', async t => {
  const {factory} = await fixture(t, {'qq_mail.watch': {async run(input, context) {
    context.emit('watch_ready', {provider: 'qq_mail', resync_required: true});
    context.emit('mailbox_changed', {provider: 'qq_mail', reason: 'qq_inbound_envelope'});
    while (!context.signal.aborted) await delay(10);
    context.check();
  }}});
  const observed = [];
  factory.mailObserverSink = (provider, slot, kind, reason) => observed.push([provider, kind, reason ?? '', slot.state]);
  const start = await factory.runScript(request(factory, 'qq_mail', 'observe',
    {schema_version: 1, action: 'start', account_address: 'owner@qq.com', owner_scope: scope}, {taskID: 'mail-observer-qq_mail'}));
  assert.equal(start.result.status, 'starting');
  await delay(50);
  const watch = await factory.pollWatch('qq_mail');
  assert.equal(watch.state, 'watching'); assert.equal(watch.task.status, 'running');
  assert.deepEqual(observed, [['qq_mail', 'resync_required', '', 'watching'], ['qq_mail', 'mailbox_changed', 'qq_inbound_envelope', 'watching'],
    ['qq_mail', 'state', '', 'watching']]);
  const expires = watch.admission.grant.execution_expires_ms;
  watch.admission.grant.execution_expires_ms = Date.now() + 1000;
  await factory.pollWatch('qq_mail', {renew: true});
  assert.ok(watch.admission.grant.execution_expires_ms >= expires);
  assert.ok(watch.admission.references.length <= 2);
  const stopped = await factory.stopWatch('qq_mail');
  assert.equal(stopped.result.status, 'stopped');
  assert.equal(watch.task.status, 'cancelled');
  assert.equal(factory.authorization.records.size, 0);
});

test('already-cancelled work never reaches its provider or retains authority', async t => {
  let calls = 0;
  const {factory} = await fixture(t, {'qq_mail.discover': {async run() {calls++; return {data: {}};}}});
  const result = await factory.runScript(request(factory, 'qq_mail', 'discover', discover, {signal: AbortSignal.abort()}));
  assert.equal(result.task.status, 'cancelled');
  assert.equal(calls, 0);
  assert.equal(factory.authorization.records.size, 0);
});

test('rejected inputs leave no authorization records', async t => {
  const {factory} = await fixture(t);
  for (let i = 0; i < 20; i++) await assert.rejects(factory.runScript(request(factory, 'qq_mail', 'discover', {})));
  assert.equal(factory.authorization.records.size, 0);
  assert.equal(factory.inflight.size, 0);
});

const legacySend = {schema_version: 1, operation: 'send', provider: 'qq_mail', invocation_id: 'legacy-send', account: 'default',
  message: {recipient: 'sink@example.test', subject: 'Synthetic', body: {format: 'text', content: 'Fixture only'}}};

test('completed legacy send survives restart without dispatching twice or retaining message secrets', async t => {
  let calls = 0;
  const handlers = {'qq_mail.send': {async run(input, context) {
    context.beforeEffect(); calls++;
    return {data: {schema_version: 1, provider: 'qq_mail', status: 'sent', recipient_digest: 'sha256:' + 'b'.repeat(64)}};
  }}};
  const first = await fixture(t, handlers);
  const invoke = factory => request(factory, 'qq_mail', 'send', legacySend, {taskID: 'durable-send'});
  const result = await first.factory.runScript(invoke(first.factory));
  assert.deepEqual(await first.factory.runScript(invoke(first.factory)), result);
  await first.factory.close();
  const next = await fixture(t, handlers, first.root);
  assert.deepEqual(await next.factory.runScript(invoke(next.factory)), result);
  assert.equal(calls, 1);
  const directory = path.join(first.root, 'state', 'send-operations');
  const files = await fs.readdir(directory);
  assert.equal(files.length, 1);
  const saved = await fs.readFile(path.join(directory, files[0]), 'utf8');
  assert.doesNotMatch(saved, /Fixture only|sink@example|tttttttt/);
  await assert.rejects(next.factory.runScript({...invoke(next.factory), token: 'different-token'}));
  await assert.rejects(next.factory.runScript({...invoke(next.factory), input: {...legacySend, message: {...legacySend.message, subject: 'Changed'}}}));
  assert.equal(calls, 1);
});

test('interrupted remote effects remain unknown on retry and after restart', async t => {
  let calls = 0;
  const handlers = {'qq_mail.send': {async run(input, context) {
    calls++; context.beforeEffect(); throw Object.assign(new Error('lost receipt'), {code: 'EMAIL_SEND_FAILED'});
  }}};
  const first = await fixture(t, handlers);
  const invoke = factory => request(factory, 'qq_mail', 'send', legacySend, {taskID: 'unknown-send'});
  assert.equal((await first.factory.runScript(invoke(first.factory))).result.code, 'send_outcome_unknown');
  await first.factory.close();
  const next = await fixture(t, handlers, first.root);
  assert.equal((await next.factory.runScript(invoke(next.factory))).result.code, 'send_outcome_unknown');
  assert.equal(calls, 1);
});

test('watch overflow retains readiness, reports a gap, and renews process-local access', async t => {
  let ready;
  const started = new Promise(resolve => {ready = resolve;});
  const {factory} = await fixture(t, {'qq_mail.watch': {async run(input, context) {
    context.emit('watch_ready', {provider: 'qq_mail'});
    for (let i = 0; i < 1001; i++) context.emit('mailbox_changed', {reason: 'qq_inbound_envelope'});
    ready();
    while (!context.signal.aborted) await delay(5);
    context.check();
  }}});
  const events = [];
  factory.mailObserverSink = (provider, slot, kind) => events.push(kind);
  await factory.runScript(request(factory, 'qq_mail', 'observe', {schema_version: 1, action: 'start', account_address: 'owner@qq.com', owner_scope: scope}));
  await started;
  const watch = await factory.pollWatch('qq_mail');
  assert.equal(watch.state, 'watching');
  assert.equal(watch.ready, true);
  assert.ok(events.includes('resync_required'));
  watch.admission.grant.access_expires_ms = Date.now() - 1;
  watch.admission.grant.execution_expires_ms = Date.now() + 1000;
  await factory.pollWatch('qq_mail', {renew: true});
  assert.ok(watch.admission.grant.access_expires_ms > watch.admission.grant.execution_expires_ms);
  await factory.stopWatch('qq_mail');
});

test('cancellation by a joined caller cancels the original task', async t => {
  let entered;
  const started = new Promise(resolve => {entered = resolve;});
  const {factory} = await fixture(t, {'qq_mail.discover': {async run(input, context) {
    entered();
    await delay(10000, undefined, {signal: context.signal});
    return {data: {}};
  }}});
  const original = request(factory, 'qq_mail', 'discover', discover, {taskID: 'joined-cancellation'});
  const first = factory.runScript(original);
  await started;
  const abort = new AbortController();
  const joined = factory.runScript({...original, signal: abort.signal});
  abort.abort();
  const results = await Promise.all([first, joined]);
  assert.equal(results[0].task.status, 'cancelled');
  assert.deepEqual(results[1], results[0]);
  assert.equal(factory.authorization.records.size, 0);
});

test('deep attachment validation releases its grant', async t => {
  const {factory} = await fixture(t);
  const input = {...legacySend, mode: 'compose', account_address: 'owner@qq.com', message: {
    to: ['sink@example.test'], subject: 'Synthetic', body: legacySend.message.body,
    attachments: [{name: 'x.txt', path: 'bad', size_bytes: 1, sha256: 'sha256:' + 'a'.repeat(64)}],
  }};
  delete input.message.recipient;
  await assert.rejects(factory.runScript(request(factory, 'qq_mail', 'send', input)));
  assert.equal(factory.authorization.records.size, 0);
});

test('a proved pre-effect busy send may resume only within its original deadline', async t => {
  let calls = 0;
  const {factory} = await fixture(t, {'qq_mail.send': {async run(input, context) {
    if (++calls === 1) throw Object.assign(new Error('busy'), {code: 'BROWSER_BUSY'});
    context.beforeEffect(); return {data: {sent: true}};
  }}});
  const input = request(factory, 'qq_mail', 'send', legacySend, {taskID: 'pre-effect-retry'});
  assert.equal((await factory.runScript(input)).task.status, 'blocked');
  assert.equal((await factory.runScript(input)).state, 'completed');
  assert.equal((await factory.runScript(input)).state, 'completed');
  assert.equal(calls, 2);
});
