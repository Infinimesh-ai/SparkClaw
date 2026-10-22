import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import {spawn} from 'node:child_process';
import {once} from 'node:events';
import {fileURLToPath} from 'node:url';
import {BrowserHostPort} from '@infinimesh/app-cli-runtime/host-port';
import {digest} from '@infinimesh/app-cli-runtime/protocol';
import {BrowserController} from '../src/controller.mjs';
import {ApplicationHostDriver} from '../src/host-driver.mjs';
import {HostPage} from '../src/host-page.mjs';

// Actual Host leases, scheduler, driver and guarded HostPage commands; only the
// browser daemon is replaced by the pinned CLI Session protocol fixture.
test('a parked mutation renews signal and deadline before its first secret reload', async t => {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), 'mutation-reuse-'));
  const output = path.join(root, 'output'), cache = path.join(root, 'cache');
  await fs.mkdir(output); await fs.mkdir(cache);
  const state = {sessionID: 'mutation-fixture', directory: root, outputDir: output,
    environment: {XDG_CACHE_HOME: cache}, secretValues: [], async reapDaemon() {}, async remove() {}};
  const page = new HostPage({state, spawn, token: 'fixture-extension-token', extraEnv: {},
    entryPoint: fileURLToPath(new URL('../node_modules/@playwright/cli/playwright-cli.js', import.meta.url)),
    actionTimeoutMS: 5000, navigationTimeoutMS: 5000,
    registration: {timeoutMS: 30000, codeEnabled: true, readOnlyCode: true, origins: ['https://mail.google.test']}});
  page.taskIndex = 0;
  page.ownerTabs = [{title: 'Owner', url: 'https://owner.test/', current: false, crashed: false}];
  page.closeTaskPage = async () => {}; page.stop = async () => {};
  const daemon = spawn(process.execPath, [fileURLToPath(new URL('./fixtures/fake-cli-session.mjs', import.meta.url)),
    page.sessionName, 'normal', path.join(root, 'calls.jsonl')],
  {cwd: output, env: {...process.env, XDG_CACHE_HOME: cache}, stdio: ['ignore', 'pipe', 'pipe']});
  const controller = new BrowserController({clientFactory: {async open() {throw new Error('unexpected startup');}}});
  const driver = new ApplicationHostDriver({controller, runtimeRoot: root});
  let creations = 0, handle;
  driver.create = async ({task, spec}) => {
    creations++;
    const bootstrap = await controller.reserveApplication({taskID: task, resource: spec.host.family, exclusive: false});
    handle = {page, state, bootstrap, activities: new Map(), closed: false};
    driver.handles.add(handle); return handle;
  };
  const spec = {timeout_ms: 30000, host: {family: 'qq_mail.mutation', activity: 'mutation',
    reuse_idle_ms: 120000, methods: ['setSecrets', 'runReadCode'], page: {origins: ['https://mail.google.test']}}};
  const binding = {manifest: {id: 'mail'}, commands: {send: spec}};
  const records = new Map(), mac = 'a'.repeat(64);
  const port = new BrowserHostPort({authorization: {record: key => records.get(key), mac: () => mac},
    bindings: [binding], driver, stateDirectory: root});
  port.epoch = 1;
  t.after(async () => {
    await port.close(); await controller.shutdown();
    if (daemon.exitCode === null) {daemon.kill('SIGKILL'); await once(daemon, 'close');}
    await fs.rm(root, {recursive: true, force: true});
  });
  await Promise.race([once(daemon.stdout, 'data'), once(daemon, 'exit').then(() => {throw new Error('fixture exited');})]);
  const resource = {profile_id: 'fixture', credential_generation: 1, pool_key: 'qq-send-account', binding_digest: digest(binding)};
  const request = (task, operation, extra = {}) => port.control({protocol_version: '1.0', mac, operation,
    task_id: task, epoch: 1, generation: port.generation, authorization_ref: task, ...extra});
  const acquire = task => {
    records.set(task, {resource, grant: {owner: 'owner', principal: 'principal', app: 'mail', command: 'send',
      intent_digest: task, revision: 1, access_expires_ms: Date.now() + 300000, execution_expires_ms: Date.now() + 300000}});
    return request(task, 'acquire');
  };
  const first = await acquire('send-first');
  const call = (task, lease, method, args) => request(task, 'call', {lease_id: lease.lease_id, method, arguments: args});
  await call('send-first', first, 'setSecrets', [{BODY: 'first private message'}]);
  // An ordinary first-send call publishes the first lease's abort signal.
  await call('send-first', first, 'runReadCode', ['async page => ({ok:true})']);
  await call('send-first', first, 'setSecrets', [{}]);
  const retiredSignal = page.signal;
  await request('send-first', 'release', {lease_id: first.lease_id, park: true});
  assert.equal(retiredSignal.aborted, true);
  assert.deepEqual(JSON.parse(await fs.readFile(state.secretsPath, 'utf8')), {secrets: {}});
  page.deadline = Date.now() - 1;
  const second = await acquire('send-second');
  assert.equal(second.reused, true); assert.equal(creations, 1);
  await call('send-second', second, 'setSecrets', [{BODY: 'second private message'}]);
  assert.notEqual(page.signal, retiredSignal); assert.equal(page.signal.aborted, false);
  assert.ok(page.deadline > Date.now());
  assert.deepEqual(state.secretValues, ['second private message']);
  assert.deepEqual(JSON.parse(await fs.readFile(state.secretsPath, 'utf8')), {secrets: {BODY: 'second private message'}});
  await request('send-second', 'release', {lease_id: second.lease_id});
  assert.equal(port.resources.size, 0); assert.equal(driver.handles.size, 0);
});
