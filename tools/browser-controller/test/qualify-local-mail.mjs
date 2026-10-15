import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import {digest} from '../mail-runtime/src/protocol.mjs';
import {LocalMailRuntimeFactory} from '../src/local-mail-runtime.mjs';
import {BrowserController} from '../src/controller.mjs';
import {startUnixServer} from '../src/http-server.mjs';
import {inspectFixture} from './fixtures/local-mail-browser-handler.mjs';

// Synthetic local page through the production in-process mail factory and Host.
// The provider handler/origin alone are substituted: no real mailbox is accessed.
export async function qualifyLocalMail({directory, adapter, launcher, userDataDir, token, origin, clientFactory}) {
  const root = path.join(directory, 'local-mail'); await fs.mkdir(root, {mode: 0o700});
  const factory = new LocalMailRuntimeFactory({fixtureAssembly: true, stateDirectory: path.join(root, 'state'),
    emailWorkspaceRoot: path.join(root, 'workspace'), electronAdapter: adapter, executablePath: launcher, userDataDir,
    runtimeRoot: path.join(root, 'cli-runtime'), browserChannel: 'chromium', connectTimeoutMS: 15000,
    actionTimeoutMS: 10000, navigationTimeoutMS: 15000,
    diagnostic: value => process.stderr.write(JSON.stringify(value) + '\n')});
  await fs.mkdir(factory.emailWorkspaceRoot, {recursive: true, mode: 0o700});
  await factory.prepare();
  const binding = structuredClone(factory.bindings.get('gmail'));
  const spec = binding.commands.send;
  spec.host.page = {...spec.host.page, loginURL: origin + '/fixture', origins: [origin], beforeNavigationAssets: [],
    effectSelectors: ['[data-sc-fixture-action="prepare"]']};
  factory.bindings.set('gmail', binding);
  factory.host.bindings.set(digest(binding), binding);
  const key = `${binding.manifest.id}\0send`, entry = factory.commands.get(key);
  let executions = 0;
  factory.commands.set(key, {...entry, spec, output: () => true, handler: {async run(input, context) {
    executions++;
    return inspectFixture.run({...input, fixture_origin: origin}, context);
  }}});
  const controller = new BrowserController({clientFactory, scriptFactory: factory}); factory.bindController(controller);
  const server = await startUnixServer({socketPath: path.join(root, 'controller.sock'), controller});
  try {
    const request = {provider: 'gmail', operation: 'send', token, taskID: 'owned-fixture', credentialGeneration: 1,
      scriptID: spec.script_id, revision: spec.revision,
      input: {schema_version: 1, operation: 'send', provider: 'gmail', account: 'default', invocation_id: 'owned-fixture',
        message: {recipient: 'synthetic@example.test', subject: 'Fixture only', body: {format: 'text', content: 'Fixture only'}}}};
    const [result, joined] = await Promise.all([factory.runScript(request), factory.runScript(request)]);
    assert.equal(result.state, 'completed', JSON.stringify(result));
    assert.equal(result.result.title, 'Electron adapter fixture');
    assert.equal(result.result.private_fields, true, 'late private values were not filled exactly');
    assert.deepEqual(joined, result); assert.equal(executions, 1);
    assert.equal(factory.driver.handles.size, 0, 'completed mail left an owned page/process');
    assert.equal(factory.authorization.records.size, 0);
    return {repository_mail_runtime: true, signed_browser_host: true, actual_owned_page: true,
      concurrent_retry_joined: true, cleanup: true, late_private_fields: true, external_executor: false};
  } finally {await server.close();}
}
