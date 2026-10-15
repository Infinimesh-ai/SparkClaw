import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import test from 'node:test';

test('Gateway projection exactly matches the repository mail catalog and its commands', async () => {
  const projection = await fs.readFile(new URL('../mail-runtime/bindings/provider-scripts.json', import.meta.url), 'utf8');
  assert.equal(await fs.readFile(new URL('../../../services/gateway/internal/emailautomation/provider_scripts.json', import.meta.url), 'utf8'), projection);
  for (const entry of JSON.parse(projection).scripts) {
    const binding = JSON.parse(await fs.readFile(new URL(`../mail-runtime/bindings/${entry.app}.json`, import.meta.url), 'utf8'));
    const command = binding.commands[entry.command];
    assert.equal(command.script_id, entry.script_id);
    assert.equal(command.revision, entry.revision);
    assert.equal(command.timeout_ms, entry.timeout_ms);
    assert.ok(binding.manifest.commands.some(item => item.name === entry.command));
  }
});
