// Installed production assembly qualification: no resource or mailbox is opened.
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import {spawn, spawnSync} from 'node:child_process';
import {setTimeout as delay} from 'node:timers/promises';
import {AppCLIClientFactory} from '../src/app-cli-client.mjs';
import {startUnixServer} from '../src/http-server.mjs';

const meta = JSON.parse(await fs.readFile(process.argv[2], 'utf8'));
const config = JSON.parse(await fs.readFile(meta.config, 'utf8'));
const factory = new AppCLIClientFactory({configFile: meta.config, python: meta.python, runtimeRoot: config.host_runtime_root});
await factory.prepare();
assert.equal(factory.info().app_cli, 'lifecycle-2.0');
const server = await startUnixServer({socketPath: config.browser_host_socket,
  controller: {scriptFactory: factory, shutdown: () => factory.close()}});
const service = spawn(meta.python, ['-m', 'app_cli.executor_service', meta.config], {stdio: ['ignore', 'ignore', 'pipe']});
let diagnostics = '';
service.stderr.on('data', bytes => {diagnostics += bytes.toString().slice(0, 1024);});
try {
  const until = Date.now() + 10000;
  while (!await fs.stat(config.socket).catch(() => null) && service.exitCode === null && Date.now() < until) await delay(25);
  assert.equal(service.exitCode, null, diagnostics);
  assert.ok(await fs.stat(config.socket).catch(() => null), diagnostics);
  const discovery = spawnSync(meta.python, ['-m', 'app_cli', 'apps'], {
    encoding: 'utf8', env: {...process.env, APP_CLI_CONFIG: meta.config}, timeout: 10000});
  assert.equal(discovery.status, 0, discovery.stderr);
  const apps = JSON.parse(discovery.stdout).data.apps.filter(app => app.id.startsWith('mail-'));
  assert.equal(apps.length, 3);
  for (const app of apps) assert.equal(app.commands.length, 9);
  for (const provider of ['gmail', 'outlook', 'qq_mail']) {
    const {spec} = factory.client.describe(provider, 'probe');
    const admission = factory.client.admission({provider, operation: 'probe', taskID: 'deployment-qualification',
      input: {schema_version: 1, operation: 'probe', invocation_id: 'deployment-qualification', provider, account: 'default'},
      credentialGeneration: 1, token: 'fixture-not-a-live-token', scriptID: spec.script_id, revision: spec.revision});
    const result = await factory.client.client.control(admission, 'lookup');
    assert.equal(result.outcome, 'unresolved');
  }
  assert.equal(factory.driver.handles.size, 0);
  console.log(JSON.stringify({production_assembly: true, paired_python_and_consumer: true,
    signed_host_handshake: true, registered_mail_commands: 27, public_registry_lookup: true, browser_resources_opened: 0}));
} finally {
  const stopped = service.exitCode === null && service.signalCode === null ? new Promise(resolve => service.once('exit', resolve)) : Promise.resolve();
  service.kill('SIGTERM'); await Promise.race([stopped, delay(5000)]);
  if (service.exitCode === null && service.signalCode === null) service.kill('SIGKILL');
  await stopped; await server.close();
}
