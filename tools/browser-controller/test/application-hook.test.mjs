import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import vm from 'node:vm';
import {EventEmitter} from 'node:events';
import {createRequire} from 'node:module';

async function fixture(t, activities) {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), 'application-lease-'));
  t.after(() => fs.rm(root, {recursive: true, force: true}));
  const file = path.join(root, 'lease.json');
  let now = 1000, monotonic = 1000, tick;
  const write = value => fs.writeFile(file, JSON.stringify({epoch: 1, generation: 'fixture', activities, ...value}), {mode: 0o600});
  await write({});
  const page = new EventEmitter(); let closes = 0;
  page.close = async () => {closes++; page.emit('close');};
  const context = {module: {exports: {}}, require: createRequire(import.meta.url), Buffer,
    process: {env: {APP_CLI_LEASE_FILE: file, APP_CLI_AWAITED_CODE: '1'}},
    Date: {now: () => now}, performance: {now: () => monotonic},
    setInterval: fn => {tick = fn; return {unref() {}};}, clearInterval() {}};
  vm.runInNewContext(await fs.readFile(new URL('../src/application-hook.cjs', import.meta.url), 'utf8'), context);
  const hook = context.module.exports;
  await hook.guard(page);
  return {hook, page, write, env: context.process.env, advance: (wall, mono = wall) => {now += wall; monotonic += mono;},
    tick: () => tick(), closes: () => closes};
}
test('daemon watchdog enforces expiry even if the controller dies or wall clock moves backwards', async t => {
  const f = await fixture(t, [{id: 'read', kind: 'read', expires_ms: 1200}]);
  await f.tick(); f.advance(-5000, 201); await f.tick(); assert.equal(f.closes(), 1);
});

test('late private fields load into the running daemon without a caller-selected path', async t => {
  const f = await fixture(t, [{id: 'send', kind: 'exclusive', expires_ms: 5000}]);
  const file = path.join(path.dirname(f.env.APP_CLI_LEASE_FILE), 'secrets.json');
  const secrets = {RECIPIENT: 'synthetic@example.test', BODY: 'quote " and literal \\n\nsecond line'};
  await fs.writeFile(file, JSON.stringify({secrets}), {mode: 0o600});
  const config = {};
  const tab = {page: f.page, context: {config}, waitForCompletion: callback => callback()};
  await f.hook.waitForCompletion(tab, {code: '/* app-cli:secrets:reload:v1 */ async page => true'}, async () => true);
  assert.deepEqual(JSON.parse(JSON.stringify(config.secrets)), secrets);
  await fs.chmod(file, 0o644);
  await assert.rejects(f.hook.waitForCompletion(tab, {code: '/* app-cli:secrets:reload:v1 */ async page => true'}, async () => true), /host_secrets_invalid/);
});
test('watch expiry preserves a separately leased read; expiry or epoch change closes only the owned page', async t => {
  const f = await fixture(t, [{id: 'watch', kind: 'watch', expires_ms: 1100}, {id: 'read', kind: 'read', expires_ms: 1500}]);
  f.advance(101); await f.tick(); assert.equal(f.closes(), 0);
  await f.write({epoch: 2}); await f.tick(); assert.equal(f.closes(), 1);
});
test('only the explicitly enabled, marked in-memory application read skips ambient completion', async t => {
  const f = await fixture(t, [{id: 'read', kind: 'read', expires_ms: 5000}]);
  for (const enabled of ['0', '1']) for (const marked of [false, true]) for (const filename of [false, true]) {
    f.env.APP_CLI_AWAITED_CODE = enabled;
    let ambient = 0, finished = false;
    const tab = {page: f.page, waitForCompletion: callback => {ambient++; return callback();}};
    const value = await f.hook.waitForCompletion(tab, {code: (marked ? '/* app-cli:awaited-code:v1 */' : '') + 'async page => true',
      ...(filename ? {filename: 'fixture'} : {})}, async () => {await Promise.resolve(); finished = true; return 42;});
    assert.equal(value, 42); assert.equal(finished, true);
    assert.equal(ambient, enabled === '1' && marked && !filename ? 0 : 1);
  }
});
