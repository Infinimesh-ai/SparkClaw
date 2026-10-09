import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import vm from 'node:vm';
import {EventEmitter} from 'node:events';
import {createRequire} from 'node:module';
import {BrowserHostPort} from '@infinimesh/app-cli-runtime/host-port';
import {ApplicationHostDriver} from '../src/host-driver.mjs';

// The real Host port, atomic lease publisher and daemon watchdog share one
// private fixture. Inspect after every publication, without a timing sleep.
async function fixture(t, {cleanupFails = false} = {}) {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), 'host-retirement-'));
  const directory = path.join(root, 'session'); await fs.mkdir(directory, {mode: 0o700});
  const file = path.join(directory, 'lease.json');
  let tick, now = 1000, watchdogCloses = 0, explicitCloses = 0, reaped = 0;
  const page = new EventEmitter(); let closed = false;
  page.close = async () => {closed = true; watchdogCloses++; page.emit('close');};
  const handle = {closed: false, activities: new Map(), page: {
    async closeTaskPage() {
      explicitCloses++;
      if (closed || cleanupFails) throw Object.assign(new Error('unproven page closure'), {
        code: 'browser_extension_unavailable', diagnosticReason: 'process_exit_page_closed', diagnosticCommand: 'tab-list',
      });
      closed = true; page.emit('close');
    }, async stop() {},
  }, state: {directory, async reapDaemon() {reaped++;}, async remove() {}}};
  const driver = new ApplicationHostDriver({runtimeRoot: root}); driver.handles.add(handle);
  const published = [], publish = driver.updateLease.bind(driver);
  driver.updateLease = async (resource, stamp) => {
    await publish(resource, stamp); published.push(structuredClone(stamp)); await tick();
  };
  const port = new BrowserHostPort({authorization: {}, bindings: [], driver, stateDirectory: root, clock: () => now});
  port.epoch = 1;
  const slot = {key: 'shared-mail', leases: new Set(['watch', 'read']), handle, idleMS: 5000};
  port.resources.set(slot.key, slot);
  for (const kind of ['watch', 'read']) port.leases.set(kind, {
    id: kind, kind, slot, epoch: 1, expires: 6000, monotonicDeadline: 6000, abort: new AbortController(),
  });
  await publish(handle, port.stamp(slot));
  const sandbox = {module: {exports: {}}, require: createRequire(import.meta.url), Buffer,
    process: {env: {APP_CLI_LEASE_FILE: file}}, Date: {now: () => now}, performance: {now: () => now},
    setInterval: callback => {tick = callback; return {unref() {}};}, clearInterval() {}};
  vm.runInNewContext(await fs.readFile(new URL('../src/application-hook.cjs', import.meta.url), 'utf8'), sandbox);
  await sandbox.module.exports.guard(page);
  t.after(async () => {clearInterval(port.timer); await fs.rm(root, {recursive: true, force: true});});
  return {root, port, slot, handle, driver, published, tick: () => tick(), advance: ms => {now += ms;},
    counts: () => ({watchdogCloses, explicitCloses, reaped})};
}

for (const order of [['watch', 'read'], ['read', 'watch']]) {
  test(`concurrent ${order.join('/')} retirement never publishes an empty live-page lease`, async t => {
    const f = await fixture(t), leases = order.map(id => f.port.leases.get(id));
    const results = await Promise.allSettled(leases.map(lease => f.port.drop(lease)));
    assert.deepEqual(f.published, [], 'last retirement must close directly, not instruct the watchdog first');
    assert.ok(results.every(result => result.status === 'fulfilled'), JSON.stringify(results));
    assert.deepEqual(f.counts(), {watchdogCloses: 0, explicitCloses: 1, reaped: 1});
    assert.equal(f.port.resources.size, 0); assert.equal(f.port.leases.size, 0);
    for (const lease of leases) {assert.equal(lease.retired, true); assert.equal(lease.abort.signal.aborted, true); assert.ok(f.port.released.has(lease.id));}
    await assert.rejects(fs.access(path.join(f.root, 'cleanup-fence.json')), {code: 'ENOENT'});
  });
}

test('a retained live watch remains authorized when a concurrent read retires', async t => {
  const f = await fixture(t);
  await f.port.drop(f.port.leases.get('read'));
  assert.deepEqual(f.published[0].activities.map(activity => activity.id), ['watch']);
  assert.equal(f.counts().watchdogCloses, 0); assert.equal(f.counts().explicitCloses, 0);
  f.advance(5001); await f.tick();
  assert.equal(f.counts().watchdogCloses, 1, 'the remaining real lease still expires');
});

test('simultaneous real lease expiration still closes the resource', async t => {
  const f = await fixture(t);
  f.advance(5001); await f.port.expire();
  assert.equal(f.port.resources.size, 0); assert.equal(f.port.leases.size, 0);
  assert.deepEqual(f.counts(), {watchdogCloses: 0, explicitCloses: 1, reaped: 1});
  assert.deepEqual(f.published, []);
});

test('retirement immediately aborts access while waiting for an in-flight page action', async t => {
  const f = await fixture(t), leases = [...f.port.leases.values()];
  let finish;
  f.slot.queue = new Promise(resolve => {finish = resolve;});
  const pending = leases.map(lease => f.port.drop(lease));
  for (const lease of leases) {
    assert.equal(lease.retired, true); assert.equal(lease.abort.signal.aborted, true);
  }
  assert.equal(f.slot.retiring, 2); assert.deepEqual(f.published, []);
  assert.equal(f.counts().explicitCloses, 0);
  finish(); await Promise.all(pending);
  assert.equal(f.slot.retiring, 0); assert.equal(f.counts().explicitCloses, 1);
});

test('epoch revocation during a queued parked release closes without reviving authority', async t => {
  const f = await fixture(t), leases = [...f.port.leases.values()];
  let finish;
  f.slot.queue = new Promise(resolve => {finish = resolve;});
  const release = f.port.drop(f.port.leases.get('read'), {park: true});
  const revoke = f.port.hello({epoch: 2, bindings: []});
  for (const lease of leases) {
    assert.equal(lease.retired, true); assert.equal(lease.abort.signal.aborted, true);
  }
  finish(); await Promise.all([release, revoke]);
  assert.equal(f.port.epoch, 2); assert.equal(f.port.resources.size, 0);
  assert.deepEqual(f.published, []);
  assert.deepEqual(f.counts(), {watchdogCloses: 0, explicitCloses: 1, reaped: 1});
});

test('parked retirement is bounded and cannot revive a resource already disposed by another release', async t => {
  for (const readFirst of [true, false]) {
    const f = await fixture(t), read = f.port.leases.get('read'), watch = f.port.leases.get('watch');
    await Promise.all(readFirst ? [f.port.drop(read, {park: true}), f.port.drop(watch)] : [f.port.drop(watch), f.port.drop(read, {park: true})]);
    assert.ok(f.published.every(stamp => stamp.activities.length || stamp.idle_until_ms > 1000));
    assert.equal(f.counts().watchdogCloses, 0);
    if (readFirst) {
      assert.equal(f.counts().explicitCloses, 0); f.advance(5001); await f.tick();
      assert.equal(f.counts().watchdogCloses, 1, 'idle retention must still expire');
    } else {
      assert.equal(f.counts().explicitCloses, 1); assert.equal(f.port.resources.size, 0);
    }
  }
});

test('unknown cleanup failures remain fenced after concurrent retirements', async t => {
  const f = await fixture(t, {cleanupFails: true});
  const results = await Promise.allSettled([...f.port.leases.values()].map(lease => f.port.drop(lease)));
  assert.ok(results.every(result => result.status === 'rejected'));
  assert.equal(f.slot.fenced, true); assert.equal(f.handle.cleanupFailure?.code, 'browser_extension_unavailable');
  await assert.rejects(f.driver.assertCleanupClear(), {code: 'browser_extension_unavailable'});
  assert.equal((await fs.stat(path.join(f.root, 'cleanup-fence.json'))).mode & 0o777, 0o600);
});
