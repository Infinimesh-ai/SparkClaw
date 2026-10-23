import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import crypto from 'node:crypto';
import {spawn} from 'node:child_process';
import {once} from 'node:events';
import {fileURLToPath} from 'node:url';
import test from 'node:test';
import {HostPage} from '../src/host-page.mjs';
import {joinedCode, MAX_CODE_REQUEST_ARGUMENT_BYTES} from '../src/cli-read-batch.mjs';

const fixture = fileURLToPath(new URL('./fixtures/fake-cli-session.mjs', import.meta.url));
const token = 'private-fixture-extension-token';
const secret = 'Private message body for recipient@example.test';
const linuxOnly = {skip: process.platform !== 'linux'};
const marker = '/* app-cli:awaited-code:v1 */';

async function harness(t, {mode = 'code', transport = false} = {}) {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), 'code-request-'));
  const output = path.join(root, 'output'), cache = path.join(root, 'cache'); await fs.mkdir(output); await fs.mkdir(cache);
  const sessionID = `session_${'b'.repeat(32)}`, name = `sc-cli-${crypto.createHash('sha256').update(sessionID).digest('hex').slice(0, 20)}`, log = path.join(root, 'calls.jsonl');
  const env = {...process.env, XDG_CACHE_HOME: cache, PLAYWRIGHT_MCP_EXTENSION_TOKEN: token};
  const daemon = spawn(process.execPath, [fixture, name, mode, log], {cwd: output, env, stdio: ['ignore', 'pipe', 'pipe']});
  await Promise.race([once(daemon.stdout, 'data'), once(daemon, 'exit').then(() => { throw new Error('fixture startup failed'); })]);
  t.after(async () => { daemon.kill('SIGKILL'); await once(daemon, 'close').catch(() => {}); await fs.rm(root, {recursive: true, force: true}); });
  await fs.writeFile(path.join(root, 'metadata.json'), JSON.stringify({pid: daemon.pid, session_name: name}));
  const spawned = [];
  // Every process this page starts: its argument list must never carry content.
  const countedSpawn = (executable, args, options) => {
    spawned.push(args);
    for (const value of args) { assert.equal(value.includes(secret), false); assert.equal(value.includes(token), false); }
    const child = spawn(executable, args, options);
    if (process.platform === 'linux') child.once('spawn', async () => {
      try { const cmdline = await fs.readFile(`/proc/${child.pid}/cmdline`, 'utf8'); assert.equal(cmdline.includes(secret), false); spawned.cmdlines = (spawned.cmdlines ?? 0) + 1; } catch {}
    });
    return child;
  };
  const page = new HostPage({entryPoint: fileURLToPath(new URL('../node_modules/@playwright/cli/playwright-cli.js', import.meta.url)), batchReadCommands: true,
    pageTransport: transport, attached: true,
    state: {sessionID, directory: root, outputDir: output, environment: {XDG_CACHE_HOME: cache}, secretValues: []},
    registration: {codeEnabled: true, readOnlyCode: false, awaitedRead: true, origins: ['https://mail.google.test'], timeoutMS: 10000},
    actionTimeoutMS: 1000, navigationTimeoutMS: 1000, spawn: countedSpawn, extraEnv: {}, token});
  page.taskIndex = 0; page.ownerTabs = [{title: 'Owner', url: 'https://owner.test/', current: false, crashed: false}];
  const calls = async () => { try { return (await fs.readFile(log, 'utf8')).trim().split('\n').map(line => JSON.parse(line)); } catch { return []; } };
  const leftovers = async () => (await fs.readdir(root)).filter(file => /^(?:code-request|read-batch)-/.test(file));
  return {page, root, spawned, calls, leftovers};
}

test('the argument is joined with the fixed code only inside the guarded process', () => {
  const code = `${marker}\nasync (page, argument) => argument.text`;
  const joined = joinedCode({code, argument: {text: secret}});
  assert.ok(joined.startsWith(marker), 'awaited-code requests keep their marker');
  assert.equal(joined, `${marker}\nasync page => (${code})(page, ${JSON.stringify({text: secret})})`);
  assert.equal(joinedCode({code: 'async page => 1'}), 'async page => 1', 'read batches are unchanged');
  assert.throws(() => joinedCode({code, argument: 'x'.repeat(MAX_CODE_REQUEST_ARGUMENT_BYTES)}));
});

test('code requests are refused on read-only pages, for oversized input and without a private path', async t => {
  const h = await harness(t);
  h.page.registration.readOnlyCode = true;
  await assert.rejects(h.page.runCodeRequest('async (page, argument) => 1', {}), {code: 'browser_extension_unavailable'});
  h.page.registration.readOnlyCode = false;
  await assert.rejects(h.page.runCodeRequest('x'.repeat((72 << 10) + 1), {}), {code: 'browser_extension_unavailable'});
  await assert.rejects(h.page.runCodeRequest('async (page, argument) => 1', {text: 'x'.repeat(512 << 10)}), {code: 'browser_extension_unavailable'});
  if (process.platform !== 'linux') {
    await assert.rejects(h.page.runCodeRequest('async (page, argument) => 1', {text: secret}), error => error.diagnosticReason === 'code_request_unsupported');
  }
  assert.equal(h.spawned.length, 0, 'nothing is started for a refused request');
});

for (const transport of [false, true]) test(`a mutation page sends content only through the guarded ${transport ? 'transport stdin' : 'request file'}`, linuxOnly, async t => {
  const h = await harness(t, {transport});
  const result = await h.page.runCodeRequest('async (page, argument) => argument.text.length', {text: secret});
  assert.deepEqual(result, {ok: true});
  const calls = await h.calls();
  assert.deepEqual(calls.map(call => call.command), ['tab-list', 'run-code', 'tab-list']);
  const code = calls.find(call => call.command === 'run-code').code;
  assert.ok(code.startsWith(marker)); assert.ok(code.includes(JSON.stringify(secret)), 'the daemon receives the joined code over its socket');
  assert.equal(h.spawned.length, 1);
  assert.ok(h.spawned[0].every(value => !value.includes(secret)));
  assert.deepEqual(await h.leftovers(), [], 'the one-use request file is gone');
});

for (const mode of ['post-owner', 'foreign', 'hang']) test(`a failed code request (${mode}) still removes its private file`, linuxOnly, async t => {
  const h = await harness(t, {mode});
  await assert.rejects(h.page.runCodeRequest('async (page, argument) => 1', {text: secret}, mode === 'hang' ? 100 : 30000));
  assert.deepEqual(await h.leftovers(), []);
});

test('the guarded process refuses a code request file without an argument and a batch file with one', linuxOnly, async t => {
  const h = await harness(t);
  const helper = fileURLToPath(new URL('../src/cli-read-batch.mjs', import.meta.url));
  const write = async (name, value) => { const file = path.join(h.root, name); await fs.writeFile(file, JSON.stringify(value), {mode: 0o600}); return file; };
  const base = {sessionName: 'sc-cli-' + '0'.repeat(20), taskIndex: 0, ownerTabs: [], origins: ['https://mail.google.test'], code: 'async page => 1', actionTimeoutMS: 100, readTimeoutMS: 100};
  for (const [flag, name, value] of [['--code-request', `code-request-${crypto.randomUUID()}.json`, base],
    ['--batch-request', `read-batch-${crypto.randomUUID()}.json`, {...base, argument: {text: secret}}]]) {
    const file = await write(name, value);
    const child = spawn(process.execPath, [helper, flag, file], {cwd: path.join(h.root, 'output')});
    let out = ''; child.stdout.on('data', chunk => out += chunk); await once(child, 'close');
    assert.equal(JSON.parse(out).error.code, 'browser_extension_unavailable');
  }
});
