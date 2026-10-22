import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import {appendFileSync} from 'node:fs';
import path from 'node:path';
import os from 'node:os';
import crypto from 'node:crypto';
import {spawn} from 'node:child_process';
import {once} from 'node:events';
import {fileURLToPath} from 'node:url';
import test from 'node:test';
import {HostPage} from '../src/host-page.mjs';
import {PageTransport, PageTransportUnavailable} from '../src/page-transport.mjs';
import {pageTransportEnabled} from '../src/host-driver.mjs';

const fixture = fileURLToPath(new URL('./fixtures/fake-cli-page-session.mjs', import.meta.url));
const protocolFixture = fileURLToPath(new URL('./fixtures/fake-page-transport-helper.mjs', import.meta.url));
const cli = fileURLToPath(new URL('../node_modules/@playwright/cli/playwright-cli.js', import.meta.url));
const token = 'private-fixture-extension-token';
const secrets = {MAIL_TO: 'sink@example.test', MAIL_SUBJECT: 'Fixture subject', MAIL_BODY: 'Fixture body'};
const owner = {title: 'Owner', url: 'https://owner.test/', current: false, crashed: false};
// Daemon ownership is proved through /proc, as for guarded read batches; other
// platforms keep per-command processes (asserted below without a daemon).
const linuxOnly = {skip: process.platform !== 'linux'};

// One fixture daemon (pinned Session protocol) per page; `failure` is
// "<1-based command>:<kind>[:text]" or "none".
async function page(t, {transport, failure = 'none', metadataPID, diagnostics = [], actionTimeoutMS = 1500}) {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), 'page-transport-'));
  const output = path.join(root, 'output'), cache = path.join(root, 'cache'), log = path.join(root, 'calls.jsonl');
  await fs.mkdir(output); await fs.mkdir(cache);
  const sessionID = `session_${crypto.randomBytes(16).toString('hex')}`;
  const name = `sc-cli-${crypto.createHash('sha256').update(sessionID).digest('hex').slice(0, 20)}`;
  const daemon = spawn(process.execPath, [fixture, name, failure, log],
    {cwd: output, env: {...process.env, XDG_CACHE_HOME: cache, PLAYWRIGHT_MCP_EXTENSION_TOKEN: token}, stdio: ['ignore', 'pipe', 'pipe']});
  let stderr = ''; daemon.stderr.on('data', chunk => stderr += chunk);
  await Promise.race([once(daemon.stdout, 'data'), once(daemon, 'exit').then(() => {throw new Error('fixture startup failed: ' + stderr);})]);
  await fs.writeFile(path.join(root, 'metadata.json'), JSON.stringify({pid: metadataPID ?? daemon.pid, session_name: name}));
  const spawns = [];
  const counted = (executable, args, options) => {
    spawns.push(path.basename(args[0]));
    assert.equal(args.some(arg => Object.values(secrets).includes(arg) || arg.includes(token)), false);
    return spawn(executable, args, options);
  };
  const task = new HostPage({entryPoint: cli, pageTransport: transport, pageTransportIdleMS: 60_000,
    state: {sessionID, directory: root, outputDir: output, environment: {XDG_CACHE_HOME: cache}, secretValues: Object.values(secrets),
      secretName: value => Object.entries(secrets).find(([, candidate]) => candidate === value)?.[0] ?? ''},
    registration: {codeEnabled: true, readOnlyCode: false, awaitedRead: true, origins: ['https://mail.google.test'],
      effectSelectors: ['#send'], timeoutMS: 60_000},
    actionTimeoutMS, navigationTimeoutMS: actionTimeoutMS, spawn: counted, extraEnv: {}, token,
    diagnostic: event => diagnostics.push(event),
    beforeEffect: () => appendFileSync(log, JSON.stringify({marker: 'beforeEffect'}) + '\n')});
  task.attached = true; task.taskIndex = 0; task.ownerTabs = [owner];
  t.after(async () => {
    task.attached = false; await task.stop().catch(() => {});
    if (daemon.exitCode === null && daemon.signalCode === null) {daemon.kill('SIGKILL'); await once(daemon, 'close').catch(() => {});}
    await fs.rm(root, {recursive: true, force: true});
  });
  return {task, spawns, diagnostics, calls: async () => {
    try {return (await fs.readFile(log, 'utf8')).trim().split('\n').filter(Boolean).map(line => JSON.parse(line));}
    catch {return [];}
  }};
}

// The page-call shape of a compose send (App-CLI sendManagedMail issues 23-27
// such calls): prepare, compose, fill, verify, one effect click, Sent proof.
const SEND_SHAPED_CALLS = [
  ['reloadSecrets'], ['currentURL'], ['waitFor', '#compose'], ['click', '#compose'], ['waitFor', '#to'],
  ['fill', '#to', secrets.MAIL_TO], ['press', 'Tab'], ['fill', '#subject', secrets.MAIL_SUBJECT], ['fill', '#body', secrets.MAIL_BODY],
  ['inspect', '() => document.title'], ['visible', '#send'], ['enabled', '#send'], ['runReadCode', 'async page => ({ok:true})'],
  ['inspect', '() => true'], ['click', '#send'], ['waitFor', '#sent-toast'], ['currentURL'], ['runReadCode', 'async page => ({sent:true})'],
  ['inspect', '() => 1'], ['evaluate', '() => true'], ['visible', '#sent-toast'], ['waitMilliseconds', 1], ['currentURL'], ['closeTaskPage'],
];

async function runSendShaped(task) {
  const results = [];
  for (const [method, ...args] of SEND_SHAPED_CALLS) {
    try {results.push(await task[method](...args) ?? null);}
    catch (error) {
      return {results, failedCall: results.length, method, code: error.code, reason: error.diagnosticReason,
        command: error.diagnosticCommand, effectAttempted: task.effectAttempted};
    }
  }
  return {results, failedCall: null, effectAttempted: task.effectAttempted};
}

test('send-shaped page: one resident transport replaces one CLI process per command', linuxOnly, async t => {
  const legacy = await page(t, {transport: false}), batched = await page(t, {transport: true});
  const [before, after] = await Promise.all([runSendShaped(legacy.task), runSendShaped(batched.task)]);
  assert.equal(before.failedCall, null, JSON.stringify(before));
  assert.deepEqual(after, before);
  const commands = await legacy.calls();
  assert.deepEqual(await batched.calls(), commands, 'identical daemon commands, order and effect marker');
  const daemonCommands = commands.filter(entry => entry.command).length;
  assert.ok(daemonCommands >= 60, `send-shaped page issues ${daemonCommands} commands`);
  assert.equal(legacy.spawns.length, daemonCommands);
  assert.deepEqual(batched.spawns, ['cli-page-transport.mjs']);
  // The effect marker precedes exactly one click on the effect selector.
  const marker = commands.findIndex(entry => entry.marker === 'beforeEffect');
  assert.deepEqual(commands.slice(marker + 1).find(entry => entry.command === 'click'), {command: 'click', target: '#send'});
  assert.equal(commands.filter(entry => entry.command === 'click' && entry.target === '#send').length, 1);
  batched.task.attached = false; await batched.task.stop();
  assert.deepEqual(batched.diagnostics.at(-1), {event: 'application_page_processes', cli_processes: 0, transport_processes: 1,
    transport_commands: daemonCommands, transport_fallbacks: 0, lifetime_ms: batched.diagnostics.at(-1).lifetime_ms});
});

// Positions are taken from the successful run's command log so the matrix
// follows the scenario: just before the effect, the effect, just after it.
test('a failing command stops both transports at the same call with the same classification', linuxOnly, async t => {
  // The first test proves both transports issue the same command log.
  const probe = await page(t, {transport: true});
  await runSendShaped(probe.task);
  const commands = (await probe.calls()).filter(entry => entry.command);
  const effect = commands.findIndex(entry => entry.command === 'click' && entry.target === '#send') + 1;
  const firstFill = commands.findIndex(entry => entry.command === 'fill') + 1;
  const firstRunCode = commands.findIndex((entry, index) => entry.command === 'run-code' && index > 2) + 1;
  // [position, kind, stops]: a destroyed context is retried by the existing
  // rule and the final tab-close tolerates an already-closed page.
  const cases = [
    [effect - 2, 'stale', true], [effect - 1, 'stale', true], [effect, 'hang', true], [effect, 'exit', true],
    [effect + 3, 'error:Error: Execution context was destroyed, most likely because of a navigation', false],
    [firstFill, 'token', true], [firstRunCode, 'error:SyntaxError: Unexpected token', true], [commands.length, 'close', false],
  ];
  // Two cases at a time: per-command processes are slow enough under load
  // that more parallelism would trip the action timeout on healthy commands.
  const queue = [...cases];
  await Promise.all([0, 1].map(async () => {for (let next; (next = queue.shift());) await check(...next);}));
  async function check(position, kind, stops) {
    const failure = `${position}:${kind}`;
    const legacy = await page(t, {transport: false, failure, actionTimeoutMS: 4000}), batched = await page(t, {transport: true, failure, actionTimeoutMS: 4000});
    const [before, after] = await Promise.all([runSendShaped(legacy.task), runSendShaped(batched.task)]);
    assert.equal(before.failedCall !== null, stops, failure);
    assert.deepEqual(after, before, failure);
    const log = await legacy.calls();
    assert.deepEqual(await batched.calls(), log, failure);
    // "May have been sent" is reported exactly when the effect marker was
    // reached, and never for a failure before it.
    assert.equal(before.effectAttempted, log.some(entry => entry.marker === 'beforeEffect'), failure);
    if (position < effect - 1) assert.equal(before.effectAttempted, false, failure);
    if (position >= effect - 1 && stops) assert.equal(before.effectAttempted, true, failure);
    assert.deepEqual(batched.spawns, ['cli-page-transport.mjs'], failure);
  }
});

test('a timed-out or aborted command is never retried; the next call uses a new helper', linuxOnly, async t => {
  const probe = await page(t, {transport: true});
  await runSendShaped(probe.task);
  const effect = (await probe.calls()).filter(entry => entry.command).findIndex(entry => entry.command === 'click' && entry.target === '#send') + 1;
  const hung = await page(t, {transport: true, failure: `${effect}:hang`});
  const outcome = await runSendShaped(hung.task);
  assert.equal(outcome.code, 'browser_script_timeout');
  assert.equal(outcome.effectAttempted, true);
  const before = (await hung.calls()).filter(entry => entry.command === 'click' && entry.target === '#send').length;
  assert.equal(await hung.task.currentURL(), 'https://mail.google.test/compose');
  assert.equal((await hung.calls()).filter(entry => entry.command === 'click' && entry.target === '#send').length, before);
  assert.deepEqual(hung.spawns, ['cli-page-transport.mjs', 'cli-page-transport.mjs']);

  const controller = new AbortController();
  const aborted = await page(t, {transport: true, failure: '2:hang'});
  aborted.task.signal = controller.signal;
  const pending = aborted.task.currentURL();
  for (let attempt = 0; attempt < 100 && (await aborted.calls()).length < 2; attempt++) await new Promise(resolve => setTimeout(resolve, 10));
  controller.abort();
  await assert.rejects(pending, error => error.code === 'browser_extension_unavailable' && error.diagnosticReason === undefined);
  assert.deepEqual((await aborted.calls()).map(entry => entry.command), ['tab-list', 'eval']);
});

test('a helper that cannot prove daemon ownership falls back before sending anything', linuxOnly, async t => {
  const diagnostics = [];
  const resident = await page(t, {transport: true}), refused = await page(t, {transport: true, metadataPID: process.pid, diagnostics});
  const [before, after] = await Promise.all([runSendShaped(resident.task), runSendShaped(refused.task)]);
  assert.deepEqual(after, before);
  const log = await resident.calls();
  assert.deepEqual(await refused.calls(), log, 'each command executed exactly once');
  assert.equal(refused.spawns[0], 'cli-page-transport.mjs');
  assert.equal(refused.spawns.filter(name => name === 'cli-page-transport.mjs').length, 1, 'not retried for this page');
  assert.equal(refused.spawns.length, 1 + log.filter(entry => entry.command).length);
  assert.deepEqual(diagnostics[0], {event: 'application_page_transport_unavailable', reason: 'transport_startup'});
});

test('guarded read batches run inside the same resident helper', linuxOnly, async t => {
  const reader = await page(t, {transport: true});
  reader.task.registration = {...reader.task.registration, readOnlyCode: true, effectSelectors: []};
  reader.task.batchReadCommands = true;
  for (let index = 0; index < 3; index++) assert.deepEqual(await reader.task.runReadCode('async page => ({ok:true})'), {ok: true});
  assert.deepEqual((await reader.calls()).map(entry => entry.command), Array(3).fill(['tab-list', 'run-code', 'tab-list']).flat());
  assert.deepEqual(reader.spawns, ['cli-page-transport.mjs']);
  assert.equal((await fs.readdir(path.dirname(reader.task.state.outputDir))).filter(name => name.startsWith('read-batch-')).length, 0);
  const foreign = await page(t, {transport: true, failure: '2:stale'});
  foreign.task.registration = {...foreign.task.registration, readOnlyCode: true};
  foreign.task.batchReadCommands = true;
  await assert.rejects(foreign.task.runReadCode('async page => ({ok:true})'),
    error => error.code === 'browser_page_stale' && error.diagnosticReason === 'page_unregistered_other_origin' && error.diagnosticCommand === 'run-code');
});

test('transport is used only for the pinned CLI on Linux and never for attach or close', async t => {
  const spawns = [];
  const task = new HostPage({entryPoint: cli, pageTransport: true, state: {sessionID: `session_${'b'.repeat(32)}`, directory: '/nonexistent',
    outputDir: '/nonexistent', environment: {}, secretValues: []}, registration: {origins: ['https://mail.google.test'], timeoutMS: 1000},
  actionTimeoutMS: 100, navigationTimeoutMS: 100, extraEnv: {}, token,
  spawn: (executable, args) => {spawns.push(path.basename(args[0])); throw new Error('no spawn in this test');}});
  task.attached = true; task.taskIndex = 0; task.ownerTabs = [];
  await assert.rejects(task.currentURL(), error => error.diagnosticReason === 'spawn_error');
  assert.equal(spawns[0], process.platform === 'linux' ? 'cli-page-transport.mjs' : 'playwright-cli.js');
  spawns.length = 0; task.transportDisabled = true;
  await assert.rejects(task.stop(), error => error.diagnosticReason === 'spawn_error');
  assert.deepEqual(spawns, ['playwright-cli.js'], 'close keeps its own process');
});

test('page transport policy: mutation pages by default, explicit all/off', () => {
  assert.equal(pageTransportEnabled('mutation', undefined), true);
  assert.equal(pageTransportEnabled('read', undefined), false);
  assert.equal(pageTransportEnabled('watch', ''), false);
  assert.equal(pageTransportEnabled('exclusive', 'mutation'), false);
  assert.equal(pageTransportEnabled('read', 'all'), true);
  assert.equal(pageTransportEnabled('mutation', 'off'), false);
});

// Protocol-level behavior of the parent with a stand-in helper (any platform).
function protocol(t, startup = 'ok', options = {}) {
  const log = path.join(os.tmpdir(), `transport-protocol-${crypto.randomUUID()}.log`);
  const transport = new PageTransport({spawn, cwd: os.tmpdir(), env: {...process.env, FAKE_TRANSPORT_STARTUP: startup, FAKE_TRANSPORT_LOG: log},
    sessionName: 'sc-cli-00000000000000000000', helper: protocolFixture, ...options});
  t.after(async () => {await transport.close(); await fs.rm(log, {force: true});});
  return {transport, log: async () => (await fs.readFile(log, 'utf8').catch(() => '')).trim().split('\n').filter(Boolean)};
}

test('protocol: sequential requests share one helper process', async t => {
  const {transport, log} = protocol(t);
  for (let index = 0; index < 5; index++) {
    assert.deepEqual(await transport.request({args: ['echo', String(index)]}, {timeoutMS: 5000}),
      {id: index + 1, code: 0, stdout: `echo:${index}\n`, stderr: ''});
  }
  assert.equal(transport.spawns, 1);
  assert.equal(new Set((await log()).map(line => line.split(' ')[0])).size, 1);
});

test('protocol: startup failures are reported as unavailable before any request is written', async t => {
  for (const startup of ['fail', 'crash', 'silent']) {
    const {transport, log} = protocol(t, startup);
    await assert.rejects(transport.request({args: ['echo']}, {timeoutMS: 300}),
      error => error instanceof PageTransportUnavailable && error.reason === {fail: 'fixture_refused', crash: 'startup_exit', silent: 'startup_timeout'}[startup]);
    assert.deepEqual(await log(), []);
  }
});

test('protocol: failures after a request is written are final and discard the helper', async t => {
  const cases = [
    ['hang', error => error.code === 'browser_script_timeout' && error.diagnosticReason === 'timeout'],
    ['exit', error => error.code === 'browser_extension_unavailable' && error.diagnosticReason === 'process_exit'],
    ['garbage', error => error.diagnosticReason === 'transport_protocol'],
    ['stale-id', error => error.diagnosticReason === 'transport_protocol'],
    ['big', error => error.diagnosticReason === 'output_overflow'],
  ];
  for (const [command, expected] of cases) {
    const {transport, log} = protocol(t);
    await assert.rejects(transport.request({args: [command]}, {timeoutMS: 2000}), expected, command);
    assert.equal(transport.child, null, command);
    assert.equal((await transport.request({args: ['echo', 'next']}, {timeoutMS: 5000})).stdout, 'echo:next\n');
    assert.equal(transport.spawns, 2, command);
    const pids = (await log()).map(line => line.split(' ')[0]);
    assert.equal(pids.length, 2, `${command}: the failed request is not replayed`);
    assert.notEqual(pids[0], pids[1]);
  }
});

test('protocol: abort kills the helper; an idle helper exits on its own', async t => {
  const {transport} = protocol(t, 'ok', {idleMS: 50});
  const controller = new AbortController();
  const pending = transport.request({args: ['hang']}, {timeoutMS: 5000, signal: controller.signal});
  setTimeout(() => controller.abort(), 50);
  await assert.rejects(pending, error => error.code === 'browser_extension_unavailable' && error.diagnosticReason === undefined);
  await transport.request({args: ['echo']}, {timeoutMS: 5000});
  const child = transport.child;
  await once(child, 'close');
  assert.equal(transport.child, null);
  assert.equal(transport.spawns, 2);
});
