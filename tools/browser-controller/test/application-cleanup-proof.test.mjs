import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import net from 'node:net';
import os from 'node:os';
import path from 'node:path';
import {ApplicationHostDriver} from '../src/host-driver.mjs';
import {queryBridgeTaskStatus} from '../src/bridge-native-protocol.mjs';

const SESSION = 'session-0123456789abcdef01234567';
const clean = [{window_id: 1, focused: false, task_tabs: 0, stale_task_tabs: 0, other_tabs: 3}];

async function fixture(t, windows = clean) {
  const parent = await fs.realpath(await fs.mkdtemp(path.join(os.tmpdir(), 'cp-')));
  await fs.chmod(parent, 0o700); t.after(() => fs.rm(parent, {recursive: true, force: true}));
  const root = path.join(parent, 'cli-runtime'); await fs.mkdir(root, {mode: 0o700});
  const status = {windows, calls: 0};
  const events = [];
  const driver = new ApplicationHostDriver({runtimeRoot: root, diagnostic: event => events.push(event),
    bridgeTaskStatus: async () => {status.calls++; return status.windows;}});
  return {root, driver, status, events};
}

// The 2026-10-16 work2 condition: the task page was already closed under the
// CLI, so tab-list failed after every owned process had been reaped.
async function failPageCleanup(driver, root, name = SESSION) {
  const directory = path.join(root, name);
  await fs.mkdir(directory, {mode: 0o700});
  const failure = Object.assign(new Error('page already closed'), {code: 'browser_extension_unavailable'});
  const handle = {activities: new Map(), page: {async closeTaskPage() {throw failure;}, async stop() {}},
    state: {directory, async reapDaemon() {}, async remove() {}}};
  driver.handles.add(handle);
  await assert.rejects(driver.close(handle), failure);
  return directory;
}

async function fence(root) {
  return JSON.parse(await fs.readFile(path.join(root, 'cleanup-fence.json'), 'utf8'));
}

test('a reaped page-cleanup fence clears itself once the Bridge proves no task tab remains', async t => {
  const f = await fixture(t, [{...clean[0], task_tabs: 1}]);
  const directory = await failPageCleanup(f.driver, f.root);
  assert.deepEqual({...await fence(f.root), fenced_ms: 0},
    {schema_version: 1, reason: 'owned_resource_cleanup_unproven', scope: 'page', sessions: [SESSION], fenced_ms: 0});

  await assert.rejects(f.driver.create({}), {code: 'browser_cleanup_pending'});
  assert.equal(f.status.calls, 1);
  assert.deepEqual(f.events.at(-1), {event: 'application_cleanup_fenced', scope: 'page', reason: 'task_tabs_present'});

  f.status.windows = clean; f.driver.lastCleanupProofMS = 0;
  await f.driver.assertCleanupClear();
  await assert.rejects(fs.stat(path.join(f.root, 'cleanup-fence.json')), {code: 'ENOENT'});
  await assert.rejects(fs.stat(directory), {code: 'ENOENT'});
  assert.deepEqual(f.events.at(-1), {event: 'application_cleanup_fence_cleared', scope: 'page', sessions: 1, windows: 1});
  await f.driver.assertCleanupClear();
  assert.equal(f.status.calls, 2);
});

test('a page fence is not proved while this Host still owns a resource', async t => {
  const f = await fixture(t);
  await failPageCleanup(f.driver, f.root);
  f.driver.handles.add({activities: new Map(), state: {}});
  await assert.rejects(f.driver.assertCleanupClear(), {code: 'browser_cleanup_pending'});
  assert.equal(f.status.calls, 0);
  assert.equal(f.events.at(-1).reason, 'resources_open');
  await fence(f.root);
});

test('a newer fence written while the Bridge answers is not cleared', async t => {
  const f = await fixture(t);
  await failPageCleanup(f.driver, f.root);
  f.driver.bridgeTaskStatus = async () => {
    await failPageCleanup(f.driver, f.root, 'session-89abcdef0123456789abcdef');
    return clean;
  };
  await assert.rejects(f.driver.assertCleanupClear(), {code: 'browser_cleanup_pending'});
  assert.deepEqual((await fence(f.root)).sessions, [SESSION, 'session-89abcdef0123456789abcdef']);
});

test('an unavailable Bridge or a stale task tab keeps the fence', async t => {
  for (const windows of [null, [{...clean[0], stale_task_tabs: 1}]]) {
    const f = await fixture(t, windows);
    await failPageCleanup(f.driver, f.root);
    await assert.rejects(f.driver.assertCleanupClear(), {code: 'browser_cleanup_pending'});
    assert.equal(f.status.calls, 1);
    await fence(f.root);
  }
});

test('proof attempts are bounded and the fenced rejection is logged once per interval', async t => {
  const f = await fixture(t, null);
  await failPageCleanup(f.driver, f.root);
  for (let attempt = 0; attempt < 5; attempt++) await assert.rejects(f.driver.assertCleanupClear(), {code: 'browser_cleanup_pending'});
  assert.equal(f.status.calls, 1);
  assert.equal(f.events.filter(event => event.event === 'application_cleanup_fenced').length, 1);
});

test('an unreaped process fence and a legacy fence are never cleared automatically', async t => {
  const f = await fixture(t);
  const failure = Object.assign(new Error('reaping unproven'), {code: 'browser_extension_unavailable'});
  const handle = {activities: new Map(), page: {async closeTaskPage() {}, async stop() {}},
    state: {async reapDaemon() {throw failure;}, async remove() {}}};
  f.driver.handles.add(handle);
  await assert.rejects(f.driver.close(handle), failure);
  f.driver.handles.clear();
  assert.equal((await fence(f.root)).scope, 'process');
  // A later page failure must not narrow an unproven process fence.
  await failPageCleanup(f.driver, f.root);
  assert.equal((await fence(f.root)).scope, 'process');
  await assert.rejects(f.driver.assertCleanupClear(), {code: 'browser_cleanup_pending'});
  await assert.rejects(f.driver.prepare(), {code: 'browser_cleanup_pending'});
  assert.equal(f.status.calls, 0);
  assert.equal(f.events.at(-1).reason, 'operator_required');

  const legacy = await fixture(t);
  await fs.writeFile(path.join(legacy.root, 'cleanup-fence.json'), JSON.stringify({schema_version: 1, reason: 'owned_resource_cleanup_unproven'}), {mode: 0o600});
  await assert.rejects(legacy.driver.assertCleanupClear(), {code: 'browser_cleanup_pending'});
  assert.equal(legacy.status.calls, 0);
});

test('a page fence does not disable Host startup; admission still proves it first', async t => {
  const f = await fixture(t, null);
  await failPageCleanup(f.driver, f.root);
  const restarted = new ApplicationHostDriver({runtimeRoot: f.root, bridgeTaskStatus: async () => f.status.windows});
  t.after(() => new Promise(resolve => restarted.events?.listening ? restarted.events.close(resolve) : resolve()));
  await restarted.prepare();
  await assert.rejects(restarted.create({}), {code: 'browser_cleanup_pending'});
  f.status.windows = clean; restarted.lastCleanupProofMS = 0;
  await restarted.assertCleanupClear();
  await assert.rejects(fs.stat(path.join(f.root, 'cleanup-fence.json')), {code: 'ENOENT'});
});

test('Bridge task status is read from the native socket and rejects malformed replies', async t => {
  const root = await fs.realpath(await fs.mkdtemp(path.join(os.tmpdir(), 'bs-')));
  t.after(() => fs.rm(root, {recursive: true, force: true}));
  const socketPath = path.join(root, 'bridge-native.sock');
  let reply;
  const server = net.createServer(socket => socket.once('data', chunk => {
    assert.deepEqual(JSON.parse(chunk.toString()), {schema_version: 1, operation: 'taskStatus'});
    socket.end(reply);
  }));
  await new Promise(resolve => server.listen(socketPath, resolve));
  t.after(() => new Promise(resolve => server.close(resolve)));

  reply = `${JSON.stringify({schema_version: 1, state: 'ready', windows: clean})}\n`;
  assert.deepEqual(await queryBridgeTaskStatus(socketPath), clean);
  for (const invalid of [{schema_version: 1, state: 'rejected'}, {schema_version: 1, state: 'ready', windows: [{task_tabs: -1, stale_task_tabs: 0}]}]) {
    reply = `${JSON.stringify(invalid)}\n`;
    assert.equal(await queryBridgeTaskStatus(socketPath), null);
  }
  reply = 'not json\n';
  assert.equal(await queryBridgeTaskStatus(socketPath), null);
  assert.equal(await queryBridgeTaskStatus(path.join(root, 'missing.sock')), null);
});
