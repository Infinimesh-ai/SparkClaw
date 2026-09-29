import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import {BrowserHostClient} from '@infinimesh/app-cli-runtime/host-port';
import {ApplicationHostDriver} from '../src/host-driver.mjs';

test('large host results use verified owner-private references and cannot read a symlink or altered file', async t => {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), 'application-artifacts-'));
  await fs.chmod(root, 0o700); t.after(() => fs.rm(root, {recursive: true, force: true}));
  const driver = new ApplicationHostDriver({});
  const handle = {state: {outputDir: root}};
  const result = {text: 'fixture-byte\n'.repeat(50000)};
  let response = await driver.serializeResult(handle, result);
  assert.ok(response.artifact); assert.ok(JSON.stringify(response).length < 1024);
  const client = new BrowserHostClient({authorization: {mac: () => 'test-mac'}, bindings: [],
    transport: async request => request.operation === 'acquire' ? {lease_id: 'fixture'} : response});
  const session = await client.acquire({authorization: () => 'test-grant', task_id: 'test-task', check() {}, resource: {host_runtime_root: root}});
  t.after(() => session.release());
  assert.deepEqual(await session.call('read'), result);
  await assert.rejects(fs.stat(response.artifact.path), {code: 'ENOENT'});
  response = await driver.serializeResult(handle, result);
  await fs.appendFile(response.artifact.path, 'corruption');
  await assert.rejects(session.call('read'), {code: 'HOST_ARTIFACT_INVALID'});
  response = await driver.serializeResult(handle, result);
  const original = response.artifact.path;
  response.artifact.path = path.join(root, 'symlink'); await fs.symlink(original, response.artifact.path);
  await assert.rejects(session.call('read'), {code: 'HOST_ARTIFACT_INVALID'});
});

test('production consumers have no imports of application handlers or old private registries', async () => {
  const directory = new URL('../src/', import.meta.url);
  for (const name of await fs.readdir(directory)) {
    if (!/\.(mjs|cjs)$/u.test(name)) continue;
    const source = await fs.readFile(new URL(name, directory), 'utf8');
    assert.doesNotMatch(source, /(?:from|import\()\s*['"][^'"]*(?:scripts\/email|applications\/mail|provider-scripts|mail-observers)/u, name);
  }
});

test('an invalid application release disables only application admission, leaving generic Host startup available', async () => {
  const {AppCLIClientFactory} = await import('../src/app-cli-client.mjs');
  const factory = new AppCLIClientFactory({configFile: '/nonexistent/application-config.json', python: '/usr/bin/python3'});
  await factory.prepare();
  assert.equal(factory.info().app_cli, 'application_release_unavailable');
  await factory.drainIdleMailReads();
  await assert.rejects(factory.runScript({}), {code: 'browser_script_unavailable'});
  await factory.close();
});
