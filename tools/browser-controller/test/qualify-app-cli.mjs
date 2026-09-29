import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import {randomBytes} from 'node:crypto';
import {spawn} from 'node:child_process';
import {fileURLToPath} from 'node:url';
import {setTimeout as delay} from 'node:timers/promises';
import {ProductClient} from '@infinimesh/app-cli-runtime/product-client';
import {digest} from '@infinimesh/app-cli-runtime/protocol';
import {AppCLIClientFactory} from '../src/app-cli-client.mjs';
import {BrowserController} from '../src/controller.mjs';
import {startUnixServer} from '../src/http-server.mjs';

export async function qualifyAppCLI({directory, python, adapter, launcher, userDataDir, token, origin, clientFactory}) {
  const root = path.join(directory, 'app-cli'); await fs.mkdir(root, {mode: 0o700});
  const state = path.join(root, 'state'); await fs.mkdir(state, {mode: 0o700});
  const runtimeRoot = fileURLToPath(new URL('../node_modules/@infinimesh/app-cli-runtime/', import.meta.url));
  const manifest = {schema_version: '1.0', id: 'browser-fixture', name: 'Owned browser fixture', version: '1.0.0',
    platforms: ['linux'], adapter: {kind: 'runtime', name: 'BrowserHostPort qualification'}, commands: [
      {name: 'inspect', description: 'Read an owned local fixture.', side_effect: 'read_only', input_schema: {type: 'object', additionalProperties: false},
        output_schema: {type: 'object', properties: {title: {type: 'string'}, heading: {type: 'string'}}, required: ['title', 'heading'], additionalProperties: false}},
    ]};
  const binding = {version: '1.0', manifest, manifest_digest: digest(manifest), commands: {inspect: {
    handler: 'inspect', resource: 'fixture', timeout_ms: 30000, host: {family: 'fixture', activity: 'read', methods: ['navigate', 'inspect'],
      page: {loginURL: origin + '/fixture', origins: [origin], timeoutMS: 30000, codeEnabled: true, beforeNavigationAssets: []}},
  }}};
  const bindingFile = path.join(root, 'binding.json'); await fs.writeFile(bindingFile, JSON.stringify(binding), {mode: 0o600});
  const config = {node: process.execPath, runtime_directory: runtimeRoot, state_directory: state, socket: path.join(state, 'executor.sock'),
    grants_directory: path.join(root, 'grants'), issuer_key_file: path.join(root, 'issuer'),
    browser_host_socket: path.join(root, 'host.sock'), host_state_directory: path.join(root, 'host-state'),
    bindings: [{path: bindingFile, digest: digest(binding)}], assembly_module: fileURLToPath(new URL('./fixtures/app-cli-browser-assembly.mjs', import.meta.url)),
    fixture_origin: origin};
  await fs.writeFile(config.issuer_key_file, randomBytes(32), {mode: 0o600});
  const configFile = path.join(root, 'config.json'); await fs.writeFile(configFile, JSON.stringify(config), {mode: 0o600});
  const factory = new AppCLIClientFactory({fixtureAssembly: true, configFile, python, electronAdapter: adapter, executablePath: launcher, userDataDir,
    runtimeRoot: path.join(root, 'cli-runtime'), browserChannel: 'chromium', connectTimeoutMS: 15000, actionTimeoutMS: 10000, navigationTimeoutMS: 15000,
    diagnostic: value => process.stderr.write(JSON.stringify(value) + '\n')});
  await factory.prepare();
  const controller = new BrowserController({clientFactory, scriptFactory: factory}); factory.bindController(controller);
  const server = await startUnixServer({socketPath: config.browser_host_socket, controller});
  const service = spawn(python, ['-m', 'app_cli.executor_service', configFile], {stdio: ['ignore', 'ignore', 'pipe']});
  let serviceError = ''; service.stderr.on('data', value => {serviceError += value.toString().slice(0, 1024);});
  try {
    const deadline = Date.now() + 10000;
    while (!(await fs.stat(config.socket).catch(() => null)) && Date.now() < deadline && service.exitCode === null) await delay(50);
    assert.equal(service.exitCode, null, serviceError);
    const product = new ProductClient({configFile, python});
    const admission = product.authorize({app: manifest.id, command: 'inspect', arguments: {}, request_key: 'browser-fixture-one',
      principal: 'fixture-owner', owner: 'fixture-owner', side_effect: 'read_only', timeout_ms: 60000,
      resource: {binding_digest: digest(binding), profile_id: 'default', credential_generation: 1, token, pool_key: 'fixture'}});
    const result = await product.wait(admission, await product.invoke(admission));
    assert.equal(result.task.status, 'completed', JSON.stringify(result));
    assert.equal(result.data.title, 'Electron adapter fixture');
    const replay = await product.invoke(admission); assert.equal(replay.task.id, result.task.id);
    assert.equal(factory.driver.handles.size, 0, 'completed application left an owned page/process');
    return {public_registry: true, runtime_v2: true, resident_executor: true, browser_host: true,
      actual_owned_page: true, idempotent_replay: true, cleanup: true, non_mail_application: true};
  } finally {
    const stopped = service.exitCode === null ? new Promise(resolve => service.once('exit', resolve)) : Promise.resolve();
    service.kill('SIGTERM'); await Promise.race([stopped, delay(5000)]); if (service.exitCode === null) service.kill('SIGKILL');
    await stopped; await server.close();
  }
}
