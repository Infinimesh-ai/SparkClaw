// SPARKCLAW_MAIL_BROWSER_TEST=1 enables an isolated, end-to-end local-mail gate.
// Uses a disposable Electron profile and only a loopback fixture. Never opens a
// mail provider, an installed SparkX profile, or a production controller socket.
import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import fs from 'node:fs/promises';
import http from 'node:http';
import os from 'node:os';
import path from 'node:path';
import {spawn} from 'node:child_process';
import {fileURLToPath} from 'node:url';
import {setTimeout as delay} from 'node:timers/promises';

const here = fileURLToPath(import.meta.url);
const repository = path.resolve(path.dirname(here), '../../..');
if (process.versions.electron) void browserHost().catch(error => {console.error(error); process.exit(1);});
else if (process.env.SPARKCLAW_MAIL_BROWSER_TEST !== '1') console.log('Skipped: set SPARKCLAW_MAIL_BROWSER_TEST=1');
else await qualify();

async function browserHost() {
  const {app, BrowserWindow, WebContentsView, session, protocol} = await import('electron');
  const {BrowserPresentation} = await import('../../../apps/desktop/src/main/presentation.mjs');
  const {PageRegistry} = await import('../../../apps/desktop/src/browser/page-registry.mjs');
  const {ElectronAdapterServer} = await import('../../../apps/desktop/src/browser/adapter-server.mjs');
  const directory = process.env.SPARKCLAW_LOCAL_MAIL_FIXTURE_DIRECTORY;
  app.setPath('userData', path.join(directory, 'profile'));
  protocol.registerSchemesAsPrivileged([{scheme: 'sparkclaw-internal', privileges: {standard: true, secure: true}}]);
  await app.whenReady();
  const browserSession = session.fromPartition('persist:local-mail-fixture');
  browserSession.protocol.handle('sparkclaw-internal', () => new Response('<title>Extension connect</title>'));
  browserSession.setPermissionRequestHandler((_contents, _permission, done) => done(false));
  browserSession.webRequest.onBeforeRequest((request, done) => done({cancel:
    !request.url.startsWith(process.env.SPARKCLAW_LOCAL_MAIL_FIXTURE_ORIGIN + '/') &&
    !request.url.startsWith('sparkclaw-internal:') && !request.url.startsWith('about:')}));
  const preferences = {session: browserSession, sandbox: true, contextIsolation: true, nodeIntegration: false};
  const window = new BrowserWindow({width: 1200, height: 800, show: false, webPreferences: preferences});
  const presentation = new BrowserPresentation({window, shieldView: new WebContentsView({webPreferences: preferences})});
  const runtimeGeneration = crypto.randomBytes(16).toString('hex');
  const registry = new PageRegistry({runtimeGeneration, browserSession, presentation, qualification: true,
    createView: () => new WebContentsView({webPreferences: preferences})});
  const adapter = await new ElectronAdapterServer({socketPath: path.join(directory, 'electron-adapter.sock'),
    secretPath: path.join(directory, 'adapter-secret'), downloadRoot: path.join(directory, 'downloads'),
    runtimeGeneration, registry, browserSession, qualification: true}).start();
  adapter.server.on('connection', socket => socket.once('data', bytes => {try {console.log(JSON.stringify({event: 'adapter_operation', operation: JSON.parse(bytes).operation}));} catch {}}));
  process.once('SIGTERM', () => {void (async () => {await adapter.close(); window.destroy(); app.exit(0);})();});
  console.log(JSON.stringify({event: 'local_mail_browser_ready', electron: process.versions.electron, chromium: process.versions.chrome}));
}

async function qualify() {
  const {LocalMailRuntimeFactory} = await import('../src/local-mail-runtime.mjs');
  const {BrowserController} = await import('../src/controller.mjs');
  const {digest} = await import('../mail-runtime/src/protocol.mjs');
  const {inspectFixture} = await import('./fixtures/local-mail-browser-handler.mjs');
  const root = await fs.realpath(await fs.mkdtemp(path.join(process.platform === 'darwin' ? '/tmp' : os.tmpdir(), 'lm-br-')));
  const evidence = {node: process.version, passed: false, repository_mail_runtime: true,
    actual_controller: true, signed_host: true, actual_cli: true, actual_browser: true, no_email_sent: true,
    linux_process_ownership_qualified: process.platform === 'linux', commands: {}, diagnostics: []};
  const fixture = http.createServer((request, response) => {
    if (request.url !== '/fixture') {response.writeHead(404).end(); return;}
    response.setHeader('content-type', 'text/html; charset=utf-8');
    response.end('<!doctype html><title>Electron adapter fixture</title><h1 id="native-target">Owned fixture</h1>');
  });
  let electron, factory, controller, failure;
  const daemonPIDs = new Set();
  try {
    await new Promise(resolve => fixture.listen(0, '127.0.0.1', resolve));
    const origin = `http://127.0.0.1:${fixture.address().port}`;
    const executable = path.join(repository, 'node_modules/electron/dist',
      process.platform === 'darwin' ? 'Electron.app/Contents/MacOS/Electron' : 'electron');
    const env = {...process.env, SPARKCLAW_LOCAL_MAIL_FIXTURE_DIRECTORY: root, SPARKCLAW_LOCAL_MAIL_FIXTURE_ORIGIN: origin};
    delete env.ELECTRON_RUN_AS_NODE;
    electron = spawn(executable, [here], {env, stdio: ['ignore', 'pipe', 'pipe']});
    evidence.browser = await browserReady(electron, evidence);
    factory = new LocalMailRuntimeFactory({fixtureAssembly: true, runtimeRoot: path.join(root, 'cli-runtime'),
      stateDirectory: path.join(root, 'state'), emailWorkspaceRoot: path.join(root, 'workspace'),
      executablePath: path.join(repository, 'apps/desktop/bin/sparkclaw-electron-browser.mjs'),
      userDataDir: path.join(root, 'profile'), browserChannel: 'chromium',
      electronAdapter: {socketPath: path.join(root, 'electron-adapter.sock'), secretPath: path.join(root, 'adapter-secret')},
      extraEnv: {TMPDIR: root, PWTEST_DAEMON_SESSION_DIR: path.join(root, 'daemon')}, connectTimeoutMS: 15000, actionTimeoutMS: 10000, navigationTimeoutMS: 15000,
      diagnostic: value => evidence.diagnostics.push(value)});
    await fs.mkdir(factory.emailWorkspaceRoot, {mode: 0o700});
    await factory.prepare();
    factory.driver.spawn = (command, args, options) => {
      const operation = args.slice(1).find(value => !value.startsWith('-'));
      evidence.commands[operation] = (evidence.commands[operation] ?? 0) + 1;
      const child = spawn(command, args, options);
      if (operation === 'attach') child.stderr.on('data', bytes => {
        let text = bytes.toString();
        for (const [key, value] of Object.entries(options.env)) if (/TOKEN|CREDENTIAL|SECRET/.test(key) && value) text = text.split(value).join('[redacted]');
        evidence.attach_diagnostics = ((evidence.attach_diagnostics ?? '') + text).slice(-2000);
      });
      return child;
    };
    // Darwin has no /proc ownership recovery. After the real CLI close, require
    // its exact recorded PID to be gone before dropping this disposable fixture's
    // metadata. Production reap/remove then runs normally. Never kill by PID,
    // bypass live-daemon checks, or claim Linux crash-recovery qualification.
    const close = factory.driver.close.bind(factory.driver);
    factory.driver.close = async handle => {
      if (!handle?.page?.attached && handle?.state?.directory) {
        const collect = async directory => {
          for (const entry of await fs.readdir(directory, {withFileTypes: true}).catch(() => [])) {
            const file = path.join(directory, entry.name);
            if (entry.isDirectory()) await collect(file);
            else if (entry.name.endsWith('.err')) evidence.daemon_error = (await fs.readFile(file, 'utf8')).slice(-2000);
          }
        };
        await collect(handle.state.directory); await collect(path.join(root, 'daemon'));
      }
      return close(handle);
    };
    const create = factory.driver.create.bind(factory.driver);
    factory.driver.create = async options => {
      const handle = await create(options), metadata = path.join(handle.state.directory, 'metadata.json');
      const {pid} = JSON.parse(await fs.readFile(metadata, 'utf8')); daemonPIDs.add(pid);
      if (process.platform !== 'linux') {
        const reap = handle.state.reapDaemon.bind(handle.state);
        handle.state.reapDaemon = async () => {await requireExited(pid); await fs.unlink(metadata); await reap();};
      }
      return handle;
    };
    controller = new BrowserController({clientFactory: {async open() {throw new Error('unexpected alternate browser path');}}, scriptFactory: factory});
    factory.bindController(controller);
    const binding = structuredClone(factory.bindings.get('gmail')), spec = binding.commands.send;
    spec.host.page = {...spec.host.page, loginURL: origin + '/fixture', origins: [origin], beforeNavigationAssets: [],
      effectSelectors: ['[data-sc-fixture-action="prepare"]']};
    factory.bindings.set('gmail', binding); factory.host.bindings.set(digest(binding), binding);
    const key = `${binding.manifest.id}\0send`, entry = factory.commands.get(key);
    let executions = 0;
    factory.commands.set(key, {...entry, spec, output: () => true, handler: {async run(input, context) {
      executions++; return inspectFixture.run({...input, fixture_origin: origin}, context);
    }}});
    const request = {profile_id: 'default', provider: 'gmail', operation: 'send', token: crypto.randomBytes(32).toString('base64url'),
      task_id: 'owned-browser-fixture', credential_generation: 1, script_id: spec.script_id, revision: spec.revision,
      input: {schema_version: 1, operation: 'send', provider: 'gmail', account: 'default', invocation_id: 'owned-browser-fixture',
        message: {recipient: 'synthetic@example.test', subject: 'Fixture only', body: {format: 'text', content: 'Fixture only'}}}};
    const firstRequest = structuredClone(request), joinedRequest = structuredClone(request);
    const [result, joined] = await Promise.all([controller.runScript(firstRequest), controller.runScript(joinedRequest)]);
    assert.equal(result.state, 'completed', JSON.stringify({result, diagnostics: evidence.diagnostics}));
    assert.deepEqual(result.result, {title: 'Electron adapter fixture', heading: 'Owned fixture', private_fields: true});
    assert.deepEqual(joined.result, result.result); assert.equal(joined.source_checksum, result.source_checksum);
    assert.equal(firstRequest.token, ''); assert.equal(joinedRequest.token, ''); assert.equal(executions, 1);
    assert.deepEqual((await controller.runScript(structuredClone(request))).result, result.result, 'completed retry must preserve its original receipt');
    assert.equal(executions, 1);
    assert.equal(factory.driver.handles.size, 0); assert.equal(factory.host.resources.size, 0);
    assert.equal(factory.authorization.records.size, 0); assert.equal(controller.providerReservations.size, 0);
    assert.equal(controller.active, null);
    assert.ok(evidence.commands.attach > 0); assert.ok(evidence.commands['run-code'] > 0); assert.ok(evidence.commands.close > 0);
    evidence.private_fields = true; evidence.concurrent_retry_joined = true; evidence.completed_retry_reused = true;
    await controller.shutdown();
    assert.equal(factory.driver.events.listening, false);
    await assert.rejects(fs.stat(factory.driver.eventSocket), {code: 'ENOENT'});
    assert.deepEqual(await fs.readdir(factory.runtimeRoot), []);
    for (const pid of daemonPIDs) await requireExited(pid);
    evidence.cleanup = true; evidence.passed = true;
  } catch (error) {failure = error; evidence.error = {message: error.message, code: error.code};}
  finally {
    try {await controller?.shutdown(); await factory?.close();} catch (error) {failure ??= error; evidence.cleanup_error = error.message;}
    if (electron && electron.exitCode === null && electron.signalCode === null) {
      const exited = new Promise(resolve => electron.once('exit', resolve)); electron.kill('SIGTERM');
      await Promise.race([exited, delay(5000)]);
      if (electron.exitCode === null && electron.signalCode === null) electron.kill('SIGKILL');
      await exited;
    }
    await new Promise(resolve => fixture.close(resolve));
    if (failure) {evidence.passed = false; evidence.retained_fixture = root; process.exitCode = 1;}
    else await fs.rm(root, {recursive: true, force: true});
    console.log(JSON.stringify(evidence));
  }
}

async function requireExited(pid) {
  for (let attempt = 0; attempt < 100; attempt++) {
    try {process.kill(pid, 0);} catch (error) {if (error.code === 'ESRCH') return; throw error;}
    await delay(50);
  }
  throw new Error('owned fixture CLI daemon did not exit');
}

async function browserReady(child, evidence) {
  return new Promise((resolve, reject) => {
    let stdout = '', stderr = '';
    const timer = setTimeout(() => reject(new Error('isolated Electron startup timed out: ' + stderr.slice(-2000))), 15000);
    child.stderr.on('data', bytes => {stderr = (stderr + bytes).slice(-4000);});
    child.stdout.on('data', bytes => {
      stdout += bytes;
      for (;;) {
        const at = stdout.indexOf('\n'); if (at < 0) break;
        const line = stdout.slice(0, at); stdout = stdout.slice(at + 1);
        try {const value = JSON.parse(line); if (value.event === 'local_mail_browser_ready') {clearTimeout(timer); resolve(value);} else (evidence.browser_events ??= []).push(value);} catch {}
      }
    });
    child.once('error', error => {clearTimeout(timer); reject(error);});
    child.once('exit', code => {clearTimeout(timer); reject(new Error(`isolated Electron exited ${code}: ${stderr.slice(-2000)}`));});
  });
}
