import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import net from 'node:net';
import http from 'node:http';
import {once} from 'node:events';
import {MailboxClient} from '@infinimesh/app-cli-runtime/mail-client';
import {ProductClient} from '@infinimesh/app-cli-runtime/product-client';
import {BrowserHostPort} from '@infinimesh/app-cli-runtime/host-port';
import {AppCLIClientFactory} from '../src/app-cli-client.mjs';
import {ApplicationHostDriver} from '../src/host-driver.mjs';
import {BrowserController} from '../src/controller.mjs';
import {startUnixServer} from '../src/http-server.mjs';

// Real lifecycle classes and both Unix listeners, with only the external
// Executor/browser actions replaced. Never connect to a running deployment.
async function fixture(t, {cancelFailure, pageFailure, reapFailure} = {}) {
  // Darwin's per-user temporary prefix leaves little Unix-socket path space.
  const root = await fs.realpath(await fs.mkdtemp(path.join(os.tmpdir(), 'sd-')));
  const runtimeRoot = path.join(root, 'cli-runtime');
  const driver = new ApplicationHostDriver({runtimeRoot}); await driver.prepare();
  const peer = net.createConnection(driver.eventSocket); await once(peer, 'connect');
  if (!driver.connections.size) await once(driver.events, 'connection');
  const peerClosed = once(peer, 'close');
  const mail = Object.create(MailboxClient.prototype);
  mail.watches = new Map([['outlook', {admission: {}, task: {id: 'fixture-watch'}}]]);
  mail.describe = () => ({spec: {source_checksum: 'fixture'}});
  mail.client = {wait: ProductClient.prototype.wait, async control() {
    if (cancelFailure) throw cancelFailure;
    return {kind: 'task', task: {id: 'fixture-watch', status: 'cancelled'}};
  }};
  const factory = new AppCLIClientFactory(); factory.client = mail; factory.driver = driver;
  factory.host = new BrowserHostPort({authorization: {}, bindings: [], driver, stateDirectory: root});
  const controller = new BrowserController({clientFactory: {async open() {throw new Error('unused');}}, scriptFactory: factory});
  factory.bindController(controller); controller.mailObserverFeed.owned.add('outlook');
  const reservation = await controller.reserveApplication({taskID: 'fixture-read', resource: 'mail', exclusive: false});
  const counts = {closed: 0, stopped: 0, reaped: 0, removed: 0, factoryClosed: 0};
  const handle = {activities: new Map([['read', {reservation}]]), page: {
    async closeTaskPage() {counts.closed++; if (pageFailure) throw pageFailure;},
    async stop() {counts.stopped++;},
  }, state: {
    async reapDaemon() {counts.reaped++; if (reapFailure) throw reapFailure;},
    async remove() {counts.removed++;},
  }};
  driver.handles.add(handle);
  const slot = {key: 'fixture-page', handle, leases: new Set(['read'])};
  factory.host.resources.set(slot.key, slot);
  const lease = {id: 'read', slot, abort: new AbortController()}; factory.host.leases.set(lease.id, lease);
  const close = factory.close.bind(factory);
  factory.close = async () => {counts.factoryClosed++; await close();};
  const runtime = await startUnixServer({socketPath: path.join(root, 'controller.sock'), controller});
  const arrived = once(runtime.server, 'request');
  const pending = http.request({socketPath: runtime.socketPath, method: 'POST', path: '/v1/run-script',
    headers: {'content-type': 'application/json'}});
  const requestClosed = new Promise(resolve => pending.once('error', resolve));
  // An actual request waiting on its remaining body must not pin server.close
  // after shutdown/failed cleanup. No provider call has been admitted.
  pending.write('{"profile_id":'); await arrived;
  t.after(async () => {
    clearInterval(factory.host.timer); clearInterval(controller.mailObserverFeed.timer);
    pending.destroy(); peer.destroy(); for (const socket of driver.connections) socket.destroy();
    if (runtime.server.listening) await new Promise(resolve => {runtime.server.close(resolve); runtime.server.closeAllConnections();});
    if (driver.events.listening) await new Promise(resolve => driver.events.close(resolve));
    await fs.rm(root, {recursive: true, force: true});
  });
  return {root, runtimeRoot, driver, peerClosed, requestClosed, controller, factory, runtime, counts, handle, lease};
}

async function assertClosed(f) {
  assert.equal(f.counts.factoryClosed, 1);
  assert.equal(f.factory.host.closed, true); assert.equal(f.lease.abort.signal.aborted, true);
  assert.equal(f.runtime.server.listening, false); assert.equal(f.driver.events.listening, false);
  await f.peerClosed;
  assert.equal((await f.requestClosed).code, 'ECONNRESET');
  await assert.rejects(fs.stat(f.runtime.socketPath), {code: 'ENOENT'});
  await assert.rejects(fs.stat(f.driver.eventSocket), {code: 'ENOENT'});
}

test('stopped Executor cancellation failure still closes Host and both listeners, preserving the failure', async t => {
  const failure = Object.assign(new Error('stopped Executor'), {code: 'BACKEND_EXECUTION_FAILED'});
  const f = await fixture(t, {cancelFailure: failure});
  await assert.rejects(f.runtime.close(), error => error === failure);
  await assertClosed(f);
  assert.equal(f.counts.reaped, 1); assert.equal(f.counts.removed, 1);
  await assert.rejects(fs.stat(path.join(f.runtimeRoot, 'cleanup-fence.json')), {code: 'ENOENT'});
});

test('normal shutdown waits for owned cleanup and closes the public and private listeners', async t => {
  const f = await fixture(t);
  await f.runtime.close(); await assertClosed(f);
  assert.equal(f.controller.providerReservations.size, 0);
  assert.deepEqual(f.counts, {closed: 1, stopped: 1, reaped: 1, removed: 1, factoryClosed: 1});
});

test('feed cancellation failure still drains a separate ordinary MCP reservation', async t => {
  const failure = Object.assign(new Error('stopped Executor'), {code: 'BACKEND_EXECUTION_FAILED'});
  const f = await fixture(t, {cancelFailure: failure});
  // An initialized watch can retain its page without holding a scheduler lane.
  f.controller.finishApplication(f.handle.activities.get('read').reservation);
  f.handle.activities.clear();
  const events = [];
  let closed;
  const client = {closed: new Promise(resolve => {closed = resolve;}),
    async createTaskPage() {events.push('create');}, async closeTaskPage() {events.push('page-close');},
    async close() {events.push('client-close'); closed();}};
  f.controller.clientFactory = {async open() {return client;}};
  await f.controller.acquire({profile_id: 'default', lane: 'mcp', task_id: 'ordinary-task',
    credential_generation: 1, token: 'fixture-controller-token'});
  await assert.rejects(f.runtime.close(), error => error === failure);
  await assertClosed(f);
  assert.deepEqual(events, ['create', 'page-close', 'client-close']);
  assert.equal(f.controller.active, null);
});

test('failed owned-process reaping remains fenced without an unresolved reservation blocking shutdown', async t => {
  const failure = Object.assign(new Error('reaping unproven'), {code: 'browser_extension_unavailable'});
  const f = await fixture(t, {reapFailure: failure});
  await assert.rejects(f.runtime.close(), error => error === failure);
  await assertClosed(f);
  assert.equal(f.handle.cleanupFailure, failure); assert.equal(f.counts.removed, 0);
  assert.equal((await fs.stat(path.join(f.runtimeRoot, 'cleanup-fence.json'))).mode & 0o777, 0o600);
  await assert.rejects(new ApplicationHostDriver({runtimeRoot: f.runtimeRoot}).assertCleanupClear(), {code: 'browser_extension_unavailable'});
});

test('feed failure remains observable when later page cleanup also fails and retains its fence', async t => {
  const first = Object.assign(new Error('stopped Executor'), {code: 'BACKEND_EXECUTION_FAILED'});
  const cleanup = Object.assign(new Error('unknown page cleanup'), {code: 'browser_page_stale'});
  const f = await fixture(t, {cancelFailure: first, pageFailure: cleanup});
  await assert.rejects(f.runtime.close(), error => error === first);
  await assertClosed(f);
  assert.equal(f.handle.cleanupFailure, cleanup); assert.equal(f.counts.reaped, 1);
  assert.equal(f.counts.removed, 0);
  assert.equal((await fs.stat(path.join(f.runtimeRoot, 'cleanup-fence.json'))).mode & 0o777, 0o600);
});

test('driver shutdown finishes every handle and closes event connections after a cleanup failure', async t => {
  const f = await fixture(t);
  const failure = Object.assign(new Error('unknown page closure'), {code: 'browser_page_stale'});
  f.handle.page.closeTaskPage = async () => {throw failure;};
  let finish, entered;
  const started = new Promise(resolve => {entered = resolve;});
  const completed = new Promise(resolve => {finish = resolve;});
  const second = {activities: new Map(), state: {async reapDaemon() {}, async remove() {}},
    page: {async closeTaskPage() {entered(); await completed;}, async stop() {}}};
  f.driver.handles.add(second);
  const stopped = f.driver.shutdown();
  const rejected = assert.rejects(stopped, error => error === failure);
  await started;
  assert.equal(f.driver.events.listening, true, 'shutdown must await the other owned cleanup');
  finish(); await rejected;
  assert.equal(second.closed, true); assert.equal(f.driver.events.listening, false);
  await f.peerClosed;
  await assert.rejects(fs.stat(f.driver.eventSocket), {code: 'ENOENT'});
  assert.equal(f.handle.cleanupFailure, failure);
});
