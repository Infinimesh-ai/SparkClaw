import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import vm from 'node:vm';
import {EventEmitter} from 'node:events';
import {createRequire} from 'node:module';
import {BrowserHostPort} from '@infinimesh/app-cli-runtime/host-port';
import {digest} from '@infinimesh/app-cli-runtime/protocol';
import {BrowserController} from '../src/controller.mjs';
import {ApplicationHostDriver} from '../src/host-driver.mjs';

// Real scheduler, Host admission/retirement, atomic lease files and daemon
// watchdog. Only browser startup/page effects are replaced; no browser or user
// profile is accessed. Mock time advances deadlines, never admission order.
async function fixture(t, {cleanupFails = false} = {}) {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), 'host-admission-'));
  const directory = path.join(root, 'session'); await fs.mkdir(directory, {mode: 0o700});
  const file = path.join(directory, 'lease.json');
  const initial = Date.now(); t.mock.timers.enable({apis: ['Date', 'setTimeout'], now: initial});
  let inspect, handle, watchdogCloses = 0, explicitCloses = 0, reaped = 0, closed = false;
  const page = new EventEmitter();
  page.close = async () => {closed = true; watchdogCloses++; page.emit('close');};
  const controller = new BrowserController({clientFactory: {async open() {throw new Error('unexpected browser startup');}}});
  const driver = new ApplicationHostDriver({controller, runtimeRoot: root});
  driver.create = async ({task, spec}) => {
    const bootstrap = await controller.reserveApplication({taskID: task, resource: spec.host.family, exclusive: false});
    handle = {closed: false, bootstrap, activities: new Map(), page: {
      async closeTaskPage() {
        explicitCloses++;
        if (closed || cleanupFails) throw Object.assign(new Error('owned page closure unproven'), {
          code: 'browser_extension_unavailable', diagnosticReason: 'process_exit_page_closed', diagnosticCommand: 'tab-list',
        });
        closed = true; page.emit('close');
      }, async stop() {},
    }, state: {directory, async reapDaemon() {reaped++;}, async remove() {}}};
    driver.handles.add(handle); return handle;
  };
  const published = [], publish = driver.updateLease.bind(driver);
  driver.updateLease = async (resource, stamp) => {
    await publish(resource, stamp); published.push(structuredClone(stamp));
    if (inspect) await inspect();
  };
  const spec = kind => ({timeout_ms: 30000, host: {family: 'qq.inbound', activity: kind, reuse_idle_ms: 1800000, methods: ['hookEvents']}});
  const binding = {manifest: {id: 'mail'}, commands: {read: spec('read'), watch: spec('watch')}};
  const records = new Map(), mac = 'a'.repeat(64);
  const authorization = {record: key => records.get(key), mac: () => mac};
  const port = new BrowserHostPort({authorization, bindings: [binding], driver, stateDirectory: root,
    clock: Date.now, monotonic: Date.now});
  clearInterval(port.timer); port.epoch = 1;
  const resource = {profile_id: 'fixture', credential_generation: 1, pool_key: 'qq-inbound', binding_digest: digest(binding)};
  const grant = {owner: 'fixture', principal: 'fixture', app: 'mail', intent_digest: 'fixture', revision: 1,
    execution_expires_ms: initial + 300000, access_expires_ms: initial + 3600000};
  const request = (kind, operation, extra = {}) => port.control({protocol_version: '1.0', mac, operation,
    task_id: kind, epoch: 1, generation: port.generation, authorization_ref: kind, ...extra});
  const acquire = (kind, override = {}) => {
    records.set(kind, {grant: {...grant, command: kind, ...override}, resource});
    return request(kind, 'acquire');
  };
  const read = await acquire('read');
  const sandbox = {module: {exports: {}}, require: createRequire(import.meta.url), Buffer,
    process: {env: {APP_CLI_LEASE_FILE: file}}, Date, performance: {now: Date.now},
    setInterval: callback => {inspect = callback; return {unref() {}};}, clearInterval() {}};
  vm.runInNewContext(await fs.readFile(new URL('../src/application-hook.cjs', import.meta.url), 'utf8'), sandbox);
  await sandbox.module.exports.guard(page);
  t.after(async () => {
    for (const reservation of [controller.active, ...controller.providerReservations.values()].filter(Boolean)) controller.finishApplication(reservation);
    await port.close().catch(() => {}); await controller.shutdown();
    t.mock.timers.reset(); await fs.rm(root, {recursive: true, force: true});
  });
  return {root, port, driver, controller, handle, read, acquire, request, published,
    lease: id => port.leases.get(id), pending: () => [...port.leases.values()].find(lease => lease.kind === 'watch'),
    stamp: async () => JSON.parse(await fs.readFile(file, 'utf8')),
    tick: () => inspect(), advance: ms => t.mock.timers.tick(ms),
    counts: () => ({watchdogCloses, explicitCloses, reaped})};
}

const turn = () => new Promise(setImmediate);
const settled = promise => promise.then(value => ({value}), error => ({error}));

test('waiting watch is not published by a Reader heartbeat and cancellation preserves the Reader', async t => {
  const f = await fixture(t), waiting = settled(f.acquire('watch'));
  await turn(); const pending = f.pending(), read = f.lease(f.read.lease_id);
  assert.ok(pending); assert.equal(f.handle.activities.has(pending.id), false);
  await f.request('read', 'heartbeat', {lease_id: read.id});
  const before = await f.stamp();
  assert.deepEqual(before.activities.map(value => value.kind), ['read']);
  await assert.rejects(f.request('watch', 'heartbeat', {lease_id: pending.id}), {code: 'HOST_LEASE_EXPIRED'});
  await assert.rejects(f.request('watch', 'call', {lease_id: pending.id, method: 'hookEvents', arguments: []}), {code: 'HOST_LEASE_EXPIRED'});
  await f.port.drop(pending, {invalidate: true, park: true});
  assert.ok((await waiting).error); assert.deepEqual(await f.stamp(), before);
  assert.equal(read.retired, undefined); assert.equal(read.abort.signal.aborted, false);
  assert.equal(f.handle.activities.size, 1); assert.equal(f.controller.providerReservations.size, 1);
  assert.deepEqual(f.counts(), {watchdogCloses: 0, explicitCloses: 0, reaped: 0});
});

for (const watchdogFirst of [true, false]) test(`Reader park and long Outlook foreground preserve idle through watch timeout (watchdog first=${watchdogFirst})`, async t => {
  const f = await fixture(t);
  const foreground = f.controller.reserveApplication({taskID: 'outlook-send', exclusive: true, waitMS: 30000});
  const waiting = settled(f.acquire('watch')); await turn();
  const pending = f.pending(); assert.ok(pending); assert.equal(f.controller.active, null);
  await f.port.drop(f.lease(f.read.lease_id), {park: true});
  const exclusive = await foreground;
  assert.equal(f.controller.active, exclusive); assert.equal(f.handle.activities.has(pending.id), false);
  const idle = await f.stamp(); assert.deepEqual(idle.activities, []); assert.equal(idle.idle_until_ms, Date.now() + 1800000);
  f.advance(30001);
  if (watchdogFirst) await f.tick();
  await f.port.expire(); assert.ok((await waiting).error);
  if (!watchdogFirst) await f.tick();
  assert.deepEqual(await f.stamp(), idle, 'pending expiry may not renew or replace the parked authority');
  assert.equal(f.controller.active, exclusive); assert.equal(f.port.leases.size, 0);
  assert.deepEqual(f.counts(), {watchdogCloses: 0, explicitCloses: 0, reaped: 0});
  f.controller.finishApplication(exclusive);
  await f.port.drainIdle();
  assert.deepEqual(f.counts(), {watchdogCloses: 0, explicitCloses: 1, reaped: 1});
});

test('foreground drain disposes the parked page and aborts its pending watch before explicit cleanup', async t => {
  const f = await fixture(t);
  const foreground = f.controller.reserveApplication({taskID: 'outlook-send', exclusive: true, waitMS: 30000});
  const waiting = settled(f.acquire('watch')); await turn(); const pending = f.pending();
  await f.port.drop(f.lease(f.read.lease_id), {park: true});
  const exclusive = await foreground;
  await f.port.drainIdle();
  assert.ok((await waiting).error); assert.equal(pending.abort.signal.aborted, true);
  assert.equal(f.port.resources.size, 0); assert.equal(f.handle.activities.size, 0);
  assert.equal(f.controller.active, exclusive);
  assert.deepEqual(f.counts(), {watchdogCloses: 0, explicitCloses: 1, reaped: 1});
  await assert.rejects(fs.access(path.join(f.root, 'cleanup-fence.json')), {code: 'ENOENT'});
});

test('admission starts page authority only after reservation and remains capped by the original grant', async t => {
  const f = await fixture(t), expires = Date.now() + 20000;
  const waiting = f.acquire('watch', {execution_expires_ms: expires}); await turn();
  f.advance(5000);
  await f.port.drop(f.lease(f.read.lease_id), {park: true});
  const watch = await waiting, stamp = await f.stamp();
  assert.equal(watch.expires_ms, expires); assert.equal(stamp.idle_until_ms, 0);
  assert.deepEqual(stamp.activities, [{id: watch.lease_id, kind: 'watch', expires_ms: expires}]);
  f.advance(15001); await f.tick();
  assert.equal(f.counts().watchdogCloses, 1, 'actual admitted grant expiry still revokes the page');
  await assert.rejects(f.port.expire(), {code: 'HOST_CLEANUP_FAILED'});
  await assert.rejects(f.driver.assertCleanupClear(), {code: 'browser_extension_unavailable'});
});

test('an expired pending grant cannot revive a still-authorized Reader or erase its stamp', async t => {
  const f = await fixture(t), waiting = settled(f.acquire('watch', {execution_expires_ms: Date.now() + 1000}));
  await turn(); const before = await f.stamp(); f.advance(1001);
  await f.port.expire(); assert.ok((await waiting).error);
  assert.deepEqual(await f.stamp(), before); assert.equal(f.handle.activities.size, 1);
  assert.equal(f.counts().explicitCloses, 0); await f.tick(); assert.equal(f.counts().watchdogCloses, 0);
});

test('epoch revocation aborts pending admission and closes the owned page without leaked reservations', async t => {
  const f = await fixture(t), waiting = settled(f.acquire('watch')); await turn();
  await f.port.hello({epoch: 2, bindings: []});
  assert.ok((await waiting).error); assert.equal(f.port.epoch, 2); assert.equal(f.port.leases.size, 0);
  assert.equal(f.controller.providerReservations.size, 0); assert.equal(f.handle.activities.size, 0);
  assert.deepEqual(f.counts(), {watchdogCloses: 0, explicitCloses: 1, reaped: 1});
});

for (const cancel of ['revoke', 'close', 'expire']) test(`late beginActivity completion after ${cancel} releases its reservation without publishing`, async t => {
  const f = await fixture(t), begin = f.driver.beginActivity.bind(f.driver);
  let entered, finish;
  const reserved = new Promise(resolve => {entered = resolve;});
  const completion = new Promise(resolve => {finish = resolve;});
  f.driver.beginActivity = async (handle, activity) => {
    await begin(handle, activity);
    if (activity.kind === 'watch') {entered(); await completion;}
  };
  const waiting = settled(f.acquire('watch')); await turn(); const pending = f.pending();
  await f.port.drop(f.lease(f.read.lease_id), {park: true}); await reserved;
  const before = await f.stamp(), publications = f.published.length;
  assert.equal(f.handle.activities.has(pending.id), true, 'driver granted a reservation but has not returned it');
  if (cancel === 'expire') f.advance(30001);
  const stopping = cancel === 'revoke' ? f.port.hello({epoch: 2, bindings: []}) : cancel === 'close' ? f.port.close() : f.port.expire();
  await turn(); assert.equal(pending.abort.signal.aborted, true);
  assert.equal(f.counts().explicitCloses, 0, 'owned driver admission must finish before cleanup');
  finish(); await stopping; assert.ok((await waiting).error);
  assert.equal(f.published.length, publications); assert.deepEqual(await f.stamp(), before);
  assert.equal(f.handle.activities.size, 0); assert.equal(f.controller.providerReservations.size, 0);
  assert.equal(f.counts().watchdogCloses, 0); assert.equal(f.counts().explicitCloses, cancel === 'expire' ? 0 : 1);
  assert.equal(f.port.leases.size, 0);
});

test('failed cleanup with a pending admission retains the original error and fence', async t => {
  const f = await fixture(t, {cleanupFails: true}), waiting = settled(f.acquire('watch')); await turn();
  await assert.rejects(f.port.drop(f.lease(f.read.lease_id)), {code: 'browser_extension_unavailable'});
  assert.equal((await waiting).error?.code, 'browser_extension_unavailable');
  assert.equal(f.handle.cleanupFailure?.diagnosticCommand, 'tab-list');
  await assert.rejects(f.driver.assertCleanupClear(), {code: 'browser_extension_unavailable'});
  assert.equal(f.controller.providerReservations.size, 0);
  assert.deepEqual(f.counts(), {watchdogCloses: 0, explicitCloses: 1, reaped: 1});
});
