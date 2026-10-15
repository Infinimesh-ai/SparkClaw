// Production repository assembly qualification. No browser or mailbox is opened.
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import {LocalMailRuntimeFactory} from '../src/local-mail-runtime.mjs';

const root = await fs.realpath(await fs.mkdtemp(path.join(os.tmpdir(), 'lm-q-')));
const factory = new LocalMailRuntimeFactory({fixtureAssembly: true, runtimeRoot: path.join(root, 'cli-runtime'),
  stateDirectory: path.join(root, 'state'), emailWorkspaceRoot: path.join(root, 'workspace')});
try {
  await factory.prepare();
  assert.ok(factory.handlers);
  assert.equal(factory.bindings.size, 3);
  let commands = 0;
  for (const provider of ['gmail', 'outlook', 'qq_mail']) {
    const binding = factory.bindings.get(provider);
    assert.equal(binding.manifest.commands.length, 9);
    for (const command of binding.manifest.commands) {
      const spec = factory.describe(provider, command.name === 'watch' ? 'observe' : command.name);
      assert.equal(typeof spec.handler, 'string'); commands++;
    }
  }
  assert.equal(factory.hostClient.epoch, factory.host.epoch);
  assert.equal(factory.host.resources.size, 0);
  assert.equal(factory.driver.handles.size, 0);
  assert.equal(factory.authorization.records.size, 0);
  assert.equal(factory.active.size, 0);
  console.log(JSON.stringify({repository_assembly: true, signed_host_handshake: true,
    registered_mail_commands: commands, browser_resources_opened: 0, external_executor: false}));
} finally {await factory.close(); await fs.rm(root, {recursive: true, force: true});}
