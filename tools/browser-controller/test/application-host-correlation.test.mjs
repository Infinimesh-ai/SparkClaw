import test from 'node:test';
import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import {BrowserHostPort} from '@infinimesh/app-cli-runtime/host-port';
import {digest} from '@infinimesh/app-cli-runtime/protocol';
import {ApplicationHostDriver} from '../src/host-driver.mjs';

const firstTask = '11111111-1111-4111-8111-111111111111';
const secondTask = '22222222-2222-4222-8222-222222222222';
const firstKey = `op3.1.4.${'a'.repeat(32)}`;
const secondKey = `op3.1.4.${'b'.repeat(32)}`;
const keyDigest = value => crypto.createHash('sha256').update(value).digest('hex');
const spec = {timeout_ms: 30000, host: {activity: 'read', family: 'fixture', page: {}}};
const grant = (task, request_key) => ({task_id: task, request_key, execution_expires_ms: Date.now() + 30000});
const failure = () => Object.assign(new Error('private provider payload'), {
  code: 'browser_extension_unavailable', diagnosticReason: 'process_exit_target_missing', diagnosticCommand: 'run-code',
});
const correlation = (task, key) => ({task_id: task, operation_key_sha256: keyDigest(key), correlation: 'complete'});
function fixture() {
  const events = [];
  const driver = new ApplicationHostDriver({diagnostic: value => events.push(value), controller: {
    async reserveApplication() {return {abortController: new AbortController()};}, finishApplication() {},
  }});
  const handle = () => ({closed: false, activities: new Map(), page: {registration: {}, async read() {throw failure();}}});
  const begin = (resource, task, key, id = task) => driver.beginActivity(resource,
    {id, task, grant: grant(task, key), kind: 'read', spec, resource: {account_address: 'private@example.test'}, signal: new AbortController().signal});
  const call = (resource, activity) => driver.call(resource, 'read', ['private body'],
    {activity, spec, signal: new AbortController().signal});
  return {driver, events, handle, begin, call};
}

test('failed Host creation correlates the validated task and operation without payloads', async t => {
  const f = fixture();
  f.driver.runtimeRoot = await fs.mkdtemp(path.join(os.tmpdir(), 'host-correlation-'));
  t.after(() => fs.rm(f.driver.runtimeRoot, {force: true, recursive: true}));
  const error = failure();
  f.driver.controller.reserveApplication = async () => {throw error;};
  await assert.rejects(f.driver.create({task: firstTask, grant: grant(firstTask, firstKey), spec,
    resource: {account_address: 'private@example.test', token: 'secret-token'}, epoch: 1, generation: 1}), actual => actual === error);
  assert.deepEqual(f.events, [{event: 'application_host_failed', phase: 'reserve', code: error.code,
    reason: error.diagnosticReason, command: error.diagnosticCommand, ...correlation(firstTask, firstKey)}]);
  assert.equal(f.driver.handles.size, 0);
  assert.doesNotMatch(JSON.stringify(f.events), /private|secret-token|op3\./u);
});

test('concurrent Host failures retain each current activity identity in completion order', async () => {
  const f = fixture(), first = f.handle(), second = f.handle();
  let releaseFirst;
  first.page.read = async () => {await new Promise(resolve => {releaseFirst = resolve;}); throw failure();};
  await f.begin(first, firstTask, firstKey);
  await f.begin(second, secondTask, secondKey);
  const pending = assert.rejects(f.call(first, firstTask), {code: 'browser_extension_unavailable'});
  await assert.rejects(f.call(second, secondTask), {code: 'browser_extension_unavailable'});
  releaseFirst(); await pending;
  assert.deepEqual(f.events.map(({task_id, operation_key_sha256, correlation}) => ({task_id, operation_key_sha256, correlation})),
    [correlation(secondTask, secondKey), correlation(firstTask, firstKey)]);
});

test('a retained page failure belongs to the replacement activity, never its original task', async () => {
  const f = fixture(), resource = f.handle();
  await f.begin(resource, firstTask, firstKey, 'first-lease');
  await assert.rejects(f.call(resource, 'first-lease'));
  await f.driver.revokeActivity(resource, 'first-lease');
  await f.begin(resource, secondTask, secondKey, 'second-lease');
  await assert.rejects(f.call(resource, 'second-lease'));
  assert.equal(f.events[1].task_id, secondTask);
  assert.equal(f.events[1].operation_key_sha256, keyDigest(secondKey));
  assert.equal(f.events[1].correlation, 'complete');
});

test('invalid, absent and mismatched correlation never emits raw identity or fabricates a relation', async () => {
  for (const [task, key, boundTask] of [
    ['private@example.test', 'https://private.example/secret', undefined],
    ['private-subject', 'secret'.repeat(40), undefined],
    [firstTask, undefined, firstTask],
    [firstTask, firstKey, secondTask],
  ]) {
    const f = fixture(), resource = f.handle();
    await f.driver.beginActivity(resource, {id: 'lease', task, grant: {...grant(task, key), task_id: boundTask}, kind: 'read', spec, resource: {}});
    await assert.rejects(f.call(resource, 'lease'));
    assert.equal(f.events[0].correlation, 'incomplete');
    assert.equal(f.events[0].operation_key_sha256, undefined);
    if (boundTask !== firstTask) assert.equal(f.events[0].task_id, undefined);
    assert.doesNotMatch(JSON.stringify(f.events), /private|secret|https:|op3\./u);
  }
});

test('pinned HostPort supplies the operation of each lease when it reuses a page', async t => {
  const f = fixture();
  const root = await fs.mkdtemp(path.join(os.tmpdir(), 'host-port-correlation-'));
  let creations = 0;
  f.driver.runtimeRoot = root;
  f.driver.create = async () => {
    creations++;
    const resource = f.handle();
    Object.assign(resource.page, {async closeTaskPage() {}, async stop() {}});
    resource.state = {directory: root, async reapDaemon() {}, async remove() {}};
    f.driver.handles.add(resource);
    return resource;
  };
  const binding = {manifest: {id: 'fixture'}, commands: {read: {...spec,
    host: {...spec.host, reuse_idle_ms: 30000, methods: ['read']}}}};
  const resource = {profile_id: 'fixture', credential_generation: 1, pool_key: 'shared', binding_digest: digest(binding)};
  const records = new Map(), mac = 'a'.repeat(64);
  const port = new BrowserHostPort({authorization: {record: key => records.get(key), mac: () => mac},
    bindings: [binding], driver: f.driver, stateDirectory: root});
  port.epoch = 1;
  t.after(async () => {await port.close(); await fs.rm(root, {force: true, recursive: true});});
  const request = (task, operation, extra = {}) => port.control({protocol_version: '1.0', mac, operation,
    task_id: task, epoch: 1, generation: port.generation, authorization_ref: task, ...extra});
  for (const [task, key] of [[firstTask, firstKey], [secondTask, secondKey]]) {
    records.set(task, {resource, grant: {...grant(task, key), owner: 'fixture', principal: 'fixture', app: 'fixture',
      command: 'read', intent_digest: keyDigest(key), access_expires_ms: Date.now() + 60000, revision: 1}});
    const lease = await request(task, 'acquire');
    assert.equal(lease.reused, task === secondTask);
    await assert.rejects(request(task, 'call', {lease_id: lease.lease_id, method: 'read', arguments: []}), {code: 'browser_extension_unavailable'});
    await request(task, 'release', {lease_id: lease.lease_id, park: true});
  }
  assert.equal(creations, 1);
  assert.deepEqual(f.events.map(({task_id, operation_key_sha256, correlation}) => ({task_id, operation_key_sha256, correlation})),
    [correlation(firstTask, firstKey), correlation(secondTask, secondKey)]);
});
