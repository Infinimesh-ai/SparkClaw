import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import {BrowserHostPort} from '@infinimesh/app-cli-runtime/host-port';
import {ApplicationHostDriver} from '../src/host-driver.mjs';
import {AppCLIClientFactory} from '../src/app-cli-client.mjs';

const FAILED = 'session-0123456789abcdef01234567';
const clean = [{window_id: 1, task_tabs: 0, stale_task_tabs: 0, other_tabs: 4}];

async function fixture(t) {
  const root = await fs.realpath(await fs.mkdtemp(path.join(os.tmpdir(), 'resident-proof-')));
  const events = [], status = {windows: clean, calls: 0};
  const driver = new ApplicationHostDriver({runtimeRoot: root, diagnostic: event => events.push(event),
    bridgeTaskStatus: async () => {status.calls++; return status.windows;}});
  const host = new BrowserHostPort({authorization: {}, bindings: [], driver, stateDirectory: root});
  host.epoch = 1;
  const factory = new AppCLIClientFactory(); factory.driver = driver; factory.host = host;
  factory.bindController({finishApplication() {}});
  t.after(async () => {clearInterval(host.timer); await fs.rm(root, {recursive: true, force: true});});
  const failure = Object.assign(new Error('tab-close timed out'), {code: 'browser_script_timeout'});
  const failedDirectory = path.join(root, FAILED); await fs.mkdir(failedDirectory);
  const failed = {activities: new Map(), page: {async closeTaskPage() {throw failure;}, async stop() {}},
    state: {directory: failedDirectory, async reapDaemon() {}, async remove() {}}};
  const failedSlot = {key: FAILED, handle: failed, leases: new Set([FAILED])};
  const failedLease = {id: FAILED, kind: 'exclusive', slot: failedSlot, abort: new AbortController(),
    expires: Date.now() + 60000, monotonicDeadline: performance.now() + 60000};
  host.resources.set(FAILED, failedSlot); host.leases.set(FAILED, failedLease);
  driver.handles.add(failed); await assert.rejects(host.dispose(failedSlot), failure);
  let serial = 0;
  async function resident(kind = 'watch', options = {}) {
    const name = `session-${String(++serial).padStart(24, '0')}`, directory = path.join(root, name);
    await fs.mkdir(directory);
    const counts = {close: 0, stop: 0, reap: 0};
    const handle = {activityKind: kind, activities: new Map([[name, {kind}]]), closed: false,
      state: {directory, async reapDaemon() {counts.reap++;}, async remove() {await fs.rm(directory, {recursive: true});}},
      page: {async closeTaskPage() {counts.close++; if (options.fail) throw failure;}, async stop() {counts.stop++;}}};
    driver.handles.add(handle);
    const slot = {key: name, handle, leases: new Set([name]), idleMS: 5000};
    const lease = {id: name, kind, slot, abort: new AbortController(), expires: Date.now() + 60000,
      monotonicDeadline: performance.now() + 60000};
    host.resources.set(name, slot); host.leases.set(name, lease);
    return {handle, slot, lease, counts, name};
  }
  const fence = () => fs.readFile(path.join(root, 'cleanup-fence.json'), 'utf8');
  return {root, driver, host, factory, events, status, resident, fence, failed, failedSlot};
}

test('page-fence recovery retires receive leases and still requires the global Bridge zero-tab proof', async t => {
  const f = await fixture(t), watch = await f.resident(), read = await f.resident('read');
  const before = await f.fence();
  f.driver.bridgeTaskStatus = async () => {
    f.status.calls++;
    assert.equal(f.driver.handles.size, 0); assert.equal(f.host.resources.size, 1);
    assert.equal(await f.fence(), before, 'draining alone must never clear the write fence');
    return clean;
  };
  await f.driver.assertCleanupClear();
  assert.equal(f.status.calls, 1);
  for (const item of [watch, read]) {
    assert.deepEqual(item.counts, {close: 1, stop: 1, reap: 1});
    assert.equal(item.lease.retired, true); assert.equal(item.lease.abort.signal.aborted, true);
  }
  assert.equal(f.host.leases.size, 1, 'the failed write slot remains until Host retirement is acknowledged');
  await assert.rejects(f.fence(), {code: 'ENOENT'});
  assert.equal(f.failed.cleanupFailure.code, 'browser_script_timeout', 'the failed write result stays unchanged');
});

for (const epoch of [1, 2]) test(`Host hello epoch ${epoch} retires the proved failed write slot and admits a new write`, async t => {
  const f = await fixture(t); await f.resident();
  const originalFailure = f.failed.cleanupFailure;
  assert.equal(f.failed.closed, true); assert.equal(f.failedSlot.fenced, true);
  await f.host.hello({epoch, bindings: []});
  await f.host.hello({epoch, bindings: []});
  assert.equal(f.host.resources.size, 0); assert.equal(f.host.leases.size, 0);
  assert.equal(f.failed.cleanupFailure, originalFailure, 'proof must not rewrite the prior failed result');
  await assert.rejects(f.fence(), {code: 'ENOENT'});
  // The native browser construction is the fixture boundary; admission and
  // cleanup use the real Host port and driver throughout.
  let admissions = 0;
  f.driver.create = async () => {await f.driver.assertCleanupClear(); admissions++; return {activities: new Map()};};
  f.driver.beginActivity = async () => {};
  f.driver.updateLease = async () => {};
  const acquired = await f.host.acquire({task_id: 'new-explicit-write', epoch, authorization_ref: 'fresh'}, {
    grant: {owner: 'owner', principal: 'principal', app: 'mail', command: 'send', intent_digest: 'new-intent',
      revision: 1, execution_expires_ms: Date.now() + 60000},
    resource: {profile_id: 'profile', credential_generation: 1},
  }, {host: {activity: 'exclusive'}});
  assert.equal(admissions, 1); assert.equal(acquired.reused, false);
  assert.equal(f.host.leases.get(acquired.lease_id).kind, 'exclusive');
  assert.equal(f.failed.cleanupFailure, originalFailure);
});

test('normal receive pages are not drained without a cleanup fence', async t => {
  const f = await fixture(t), watch = await f.resident();
  await fs.rm(path.join(f.root, 'cleanup-fence.json'));
  await f.driver.assertCleanupClear();
  assert.equal(watch.counts.close, 0); assert.equal(f.status.calls, 0);
});

test('Host hello keeps the failed write slot when Bridge closure cannot be proved', async t => {
  const f = await fixture(t); await f.resident();
  const failure = f.failed.cleanupFailure, before = await f.fence();
  f.status.windows = [{...clean[0], stale_task_tabs: 1}];
  await assert.rejects(f.host.hello({epoch: 1, bindings: []}), {code: 'browser_cleanup_pending'});
  assert.equal(f.host.resources.get(FAILED), f.failedSlot);
  assert.equal(f.failed.cleanupFailure, failure); assert.equal(await f.fence(), before);
});

for (const condition of ['exclusive', 'pending', 'unmapped', 'creating', 'bootstrap', 'unknown-kind']) {
  test(`a ${condition} resource prevents the entire receive drain`, async t => {
    const f = await fixture(t), first = await f.resident(), second = await f.resident();
    const before = await f.fence();
    if (condition === 'exclusive') second.lease.kind = 'exclusive';
    if (condition === 'pending') second.lease.pending = true;
    if (condition === 'unmapped') f.host.resources.delete(second.slot.key);
    if (condition === 'creating') second.slot.creating = true;
    if (condition === 'bootstrap') second.handle.bootstrap = {};
    if (condition === 'unknown-kind') delete second.handle.activityKind;
    await assert.rejects(f.driver.assertCleanupClear(), {code: 'browser_cleanup_pending'});
    assert.equal(first.counts.close, 0); assert.equal(second.counts.close, 0);
    assert.equal(first.lease.retired, undefined); assert.equal(second.lease.abort.signal.aborted, false);
    assert.equal(await f.fence(), before); assert.equal(f.status.calls, 0);
  });
}

test('read work drains before page close, with new admission retired throughout the batch', async t => {
  const f = await fixture(t), read = await f.resident('read'), watch = await f.resident();
  let finish; read.slot.queue = new Promise(resolve => {finish = resolve;});
  let started; const entered = new Promise(resolve => {started = resolve;});
  const dispose = f.host.dispose.bind(f.host);
  f.host.dispose = slot => {started(); return dispose(slot);};
  const proof = f.driver.assertCleanupClear(); await entered;
  assert.equal(read.slot.retiring, 1); assert.equal(watch.slot.retiring, 1);
  assert.equal(read.lease.retired, true); assert.equal(read.counts.close, 0); assert.equal(f.status.calls, 0);
  finish(); await proof;
  assert.equal(read.slot.retiring, 0); assert.equal(watch.slot.retiring, 0);
  assert.equal(read.counts.close, 1); assert.equal(watch.counts.close, 1);
});

test('a receive cleanup failure adds its own session and preserves the prior write fence', async t => {
  const f = await fixture(t), watch = await f.resident('watch', {fail: true});
  await assert.rejects(f.driver.assertCleanupClear(), {code: 'browser_cleanup_pending'});
  assert.deepEqual(JSON.parse(await f.fence()).sessions, [FAILED, watch.name]);
  assert.equal(f.status.calls, 0); assert.equal(watch.slot.fenced, true);
});

test('a fence replaced during successful draining cannot be cleared by the old proof', async t => {
  const f = await fixture(t); await f.resident();
  const drain = f.driver.drainCleanupReads;
  f.driver.drainCleanupReads = async handles => {
    await drain(handles);
    await f.driver.fenceCleanup({state: {directory: path.join(f.root, 'session-abcdef0123456789abcdef01')}}, new Error('new page'), 'page');
  };
  await assert.rejects(f.driver.assertCleanupClear(), {code: 'browser_cleanup_pending'});
  assert.equal(JSON.parse(await f.fence()).sessions.length, 2); assert.equal(f.status.calls, 0);
});

for (const windows of [null, [{...clean[0], task_tabs: 1}], [{...clean[0], stale_task_tabs: 1}]]) {
  test(`draining does not prove cleanup when Bridge reports ${JSON.stringify(windows)}`, async t => {
    const f = await fixture(t), watch = await f.resident(), before = await f.fence();
    f.status.windows = windows;
    await assert.rejects(f.driver.assertCleanupClear(), {code: 'browser_cleanup_pending'});
    assert.equal(watch.counts.close, 1); assert.equal(await f.fence(), before);
  });
}

test('a resource appearing while Bridge answers retains the fence', async t => {
  const f = await fixture(t); await f.resident();
  f.driver.bridgeTaskStatus = async () => {await f.resident('exclusive'); return clean;};
  await assert.rejects(f.driver.assertCleanupClear(), {code: 'browser_cleanup_pending'});
  await f.fence(); assert.equal(f.driver.handles.size, 1);
});

test('process-scope fences never drain even healthy receive pages', async t => {
  const f = await fixture(t), watch = await f.resident();
  await f.driver.fenceCleanup(f.failed, new Error('process ownership unknown'), 'process');
  await assert.rejects(f.driver.assertCleanupClear(), {code: 'browser_cleanup_pending'});
  assert.equal(watch.counts.close, 0); assert.equal(f.status.calls, 0);
});
