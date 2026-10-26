import assert from 'node:assert/strict';
import test from 'node:test';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import {randomBytes} from 'node:crypto';
import {setImmediate as settle} from 'node:timers/promises';
import {AppCLIClientFactory} from '../src/app-cli-client.mjs';

async function fixture(t, diagnostic) {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), 'd07-'));
  const config = {runtime_directory: root, grants_directory: path.join(root, 'grants'),
    issuer_key_file: path.join(root, 'issuer'), host_state_directory: path.join(root, 'state'), bindings: []};
  await fs.writeFile(config.issuer_key_file, randomBytes(32), {mode: 0o600});
  const configFile = path.join(root, 'config.json');
  await fs.writeFile(configFile, JSON.stringify(config), {mode: 0o600});
  let tick;
  t.mock.method(globalThis, 'setInterval', (callback, ms) => {
    if (ms === 123456) tick = callback;
    return {unref() {}};
  });
  const factory = new AppCLIClientFactory({fixtureAssembly: true, configFile, python: process.execPath,
    runtimeRoot: path.join(root, 'cli-runtime'), maintenanceIntervalMS: 123456, diagnostic});
  t.after(async () => {await factory.close(); await fs.rm(root, {recursive:true, force:true});});
  await factory.prepare();
  assert.equal(typeof tick, 'function');
  return {factory, async tick() {tick(); await settle(); await settle();}};
}

test('maintenance failure is content-free evidence and the next scheduled round still runs', async t => {
  const events = [], f = await fixture(t, value => events.push(value));
  let calls = 0;
  f.factory.client.maintain = async () => {calls++; throw Object.assign(new Error('owner@example.test /private/file body credentials'), {code:'CONTROL_TIMEOUT'});};
  await f.tick();
  assert.equal(calls, 1);
  assert.equal(events.length, 1, 'maintenance rejection was swallowed');
  assert.deepEqual(events[0], {schema_version:1, event:'application_maintenance_failed', stage:'maintenance', code:'CONTROL_TIMEOUT', evidence:'incomplete'});
  assert.doesNotMatch(JSON.stringify(events), /@|private|body|credentials/);
  const unknown = {state:'uncertain', task:{status:'uncertain', id:'retained-task'}};
  let sends = 0;
  f.factory.client.execute = async () => {sends++; return unknown;};
  assert.equal(await f.factory.runScript({operation:'send'}), unknown);
  assert.equal(sends, 1);
  f.factory.client.maintain = async () => {calls++;};
  await f.tick();
  assert.equal(calls, 2);
  assert.equal(events.length, 1);
});

test('maintenance diagnostics reject arbitrary codes and tolerate failed diagnostic callbacks', async t => {
  for (const kind of ['capture', 'throw', 'reject']) {
    await t.test(kind, async t => {
      const events = [];
      const diagnostic = value => {
        events.push(value);
        if (kind === 'throw') throw new Error('private sink path');
        if (kind === 'reject') return Promise.reject(new Error('private sink credential'));
      };
      const f = await fixture(t, diagnostic);
      let calls = 0;
      f.factory.client.maintain = () => {calls++; throw Object.assign(new Error('private body'), {code:'private-token'});};
      await f.tick();
      await f.tick();
      assert.equal(calls, 2);
      assert.equal(events.length, 2);
      assert.equal(events[0].code, 'unclassified');
      assert.doesNotMatch(JSON.stringify(events), /private|credential|body/);
    });
  }
});
