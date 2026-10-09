import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import vm from 'node:vm';
import {EventEmitter} from 'node:events';
import {createRequire} from 'node:module';

async function fixture(t, activities, observer = false) {
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
  if (observer) {
    const module = path.join(root, 'observer.cjs'), config = path.join(root, 'hook.json');
    await fs.writeFile(module, 'module.exports = {suspend: page => page.suspend(), dispose: async () => {}}');
    await fs.writeFile(config, JSON.stringify({module}));
    context.process.env.APP_CLI_HOOK_CONFIG = config;
  }
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
test('navigation during observer suspension preserves the valid read and retries suspension', async t => {
  const f = await fixture(t, [{id: 'read', kind: 'read', expires_ms: 5000}], true);
  let calls = 0;
  f.page.suspend = async () => {
    if (++calls === 1) throw new Error('page.evaluate: Execution context was destroyed, most likely because of a navigation.');
  };
  await f.tick(); assert.equal(f.closes(), 0);
  await f.tick(); assert.equal(calls, 2); assert.equal(f.closes(), 0);
  f.advance(4001); await f.tick(); assert.equal(f.closes(), 1);
});
test('unknown suspension failures still close the owned page with a valid lease', async t => {
  const f = await fixture(t, [{id: 'read', kind: 'read', expires_ms: 5000}], true);
  f.page.suspend = async () => {throw new Error('observer suspension failed');};
  await f.tick(); assert.equal(f.closes(), 1);
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

const chooserMarker = '/* app-cli:awaited-code:v1 */\n/* app-cli:owned-file-chooser:v1 */\nasync page => true';
const chooserACK = Symbol.for('sparkclaw.app-cli.owned-file-chooser.v1');
for (const scenario of ['owned', 'preexisting', 'foreign', 'multiple', 'wrong-input', 'failed', 'unmarked', 'typed-rejection']) {
  test(`owned chooser cleanup is exact and closed: ${scenario}`, async t => {
    const f = await fixture(t, [{id: 'send', kind: 'exclusive', expires_ms: 5000}]);
    const input = {}, chooser = {element: () => input};
    const modal = {type: 'fileChooser', fileChooser: chooser};
    const states = scenario === 'preexisting' ? [modal] : [];
    let cleared = 0, calls = 0;
    const tab = {page: f.page, modalStates: () => [...states], clearModalState: value => {
      assert.equal(value, modal); states.splice(states.indexOf(value), 1); cleared++;
    }};
    const callback = async () => {
      calls++;
      if (scenario === 'typed-rejection') return {error: 'email_attachment_control_unavailable'};
      states.push(modal); f.page.emit('filechooser', chooser);
      if (scenario === 'multiple') {const other = {element: () => ({})};states.push({type: 'fileChooser', fileChooser: other});f.page.emit('filechooser', other);}
      if (scenario !== 'foreign') f.page[chooserACK] = {chooser, input: scenario === 'wrong-input' ? {} : input};
      if (scenario === 'failed') throw new Error('transfer_failed');
      return 42;
    };
    const code = scenario === 'unmarked' ? '/* app-cli:awaited-code:v1 */ async page => true' : chooserMarker;
    if (['owned', 'unmarked', 'typed-rejection'].includes(scenario)) await f.hook.waitForCompletion(tab, {code}, callback);
    else await assert.rejects(f.hook.waitForCompletion(tab, {code}, callback), /host_owned_chooser_unverified|transfer_failed/);
    assert.equal(cleared, scenario === 'owned' ? 1 : 0);
    assert.equal(calls, scenario === 'preexisting' ? 0 : 1);
    if (scenario !== 'unmarked') assert.equal(f.page[chooserACK], undefined);
    assert.equal(f.page.listenerCount('filechooser'), 0);
  });
}
