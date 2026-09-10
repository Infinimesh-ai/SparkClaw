import assert from 'node:assert/strict';
import test from 'node:test';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { createRequire } from 'node:module';
import { patchedBundle, installDownloads } from '../src/install-playwright-downloads.mjs';

const { ExtensionDownloads } = createRequire(import.meta.url)('../src/extension-downloads.cjs');
const guid = '12345678-1234-4123-8123-123456789abc';

async function fixture(t) {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), 'sparkclaw-download-test-'));
  t.after(() => fs.rm(root, { recursive: true, force: true }));
  const artifacts = path.join(root, 'artifacts');
  await fs.mkdir(artifacts, { mode: 0o700 });
  const native = path.join(root, 'mail.eml');
  const bytes = Buffer.from(Array.from({ length: (1 << 20) + 17 }, (_, i) => i % 251));
  await fs.writeFile(native, bytes);
  const sent = [], commands = [];
  const adapter = new ExtensionDownloads(async (method, args) => { commands.push({ method, args }); return {}; }, message => sent.push(message));
  await adapter.configure({ behavior: 'allowAndName', downloadPath: artifacts });
  const start = () => adapter.handleEvent('sparkclaw.downloadWillBegin', [{ guid, url: 'https://provider.test/export', frameId: 'frame', suggestedFilename: 'mail.eml' }]);
  const complete = async (changes = {}) => {
    adapter.handleEvent('sparkclaw.downloadProgress', [{ guid, state: 'completed', filename: native, bytes: bytes.length, receivedBytes: bytes.length, ...changes }]);
    await adapter.states.get(guid).completion;
  };
  return { root, artifacts, native, bytes, sent, commands, adapter, start, complete };
}

test('native artifact reaches Playwright only after exact private copy and release', async t => {
  const f = await fixture(t);
  f.start(); await f.complete();
  assert.equal(f.sent[0].method, 'Browser.downloadWillBegin');
  assert.equal(f.sent[0].params.frameId, 'frame');
  assert.equal(f.sent.at(-1).params.state, 'completed');
  const artifact = path.join(f.artifacts, guid);
  assert.deepEqual(await fs.readFile(artifact), f.bytes);
  assert.equal((await fs.stat(artifact)).mode & 0o777, 0o600);
  assert.equal(f.commands.at(-1).method, 'sparkclaw.downloads.release');
});

test('native handoff rejects symlinks, size mismatch, and existing artifacts', async t => {
  for (const mode of ['symlink', 'mismatch', 'collision', 'release-failed']) {
    const f = await fixture(t), artifact = path.join(f.artifacts, guid);
    const changes = {};
    if (mode === 'symlink') { changes.filename = f.native + '.link'; await fs.symlink(f.native, changes.filename); }
    if (mode === 'mismatch') changes.bytes = 2;
    if (mode === 'collision') await fs.writeFile(artifact, 'existing');
    if (mode === 'release-failed') f.adapter.sendCommand = async () => { throw new Error('disconnected'); };
    f.start(); await f.complete(changes);
    assert.equal(f.sent.at(-1).params.state, 'canceled');
    if (mode === 'collision') assert.equal(await fs.readFile(artifact, 'utf8'), 'existing');
    else await assert.rejects(fs.stat(artifact), { code: 'ENOENT' });
    assert.deepEqual(await fs.readFile(f.native), f.bytes);
  }
});

test('download configuration rejects shared directories and foreign contexts', async t => {
  const f = await fixture(t);
  await fs.chmod(f.artifacts, 0o755);
  await assert.rejects(f.adapter.configure({ behavior: 'allowAndName', downloadPath: f.artifacts }), /not private/);
  await assert.rejects(f.adapter.configure({ behavior: 'allowAndName', browserContextId: 'foreign', downloadPath: f.artifacts }), /Unsupported/);
  await assert.rejects(f.adapter.cancel({ guid }), /outside the task/);
});

test('pinned Playwright hooks are installed, idempotent, and reject drift', async () => {
  await installDownloads({ check: true });
  const source = await fs.readFile(new URL('../node_modules/playwright-core/lib/coreBundle.js', import.meta.url), 'utf8');
  assert.equal(patchedBundle(source), source);
  assert.throws(() => patchedBundle('upstream changed'), /hook changed/);
});

test('disconnect while releasing a native download removes the unclaimed artifact', async t => {
  const f=await fixture(t);
  f.adapter.sendCommand=async method => { if(method==='sparkclaw.downloads.release') f.adapter.close(); };
  f.start(); await f.complete();
  await assert.rejects(fs.stat(path.join(f.artifacts,guid)),{code:'ENOENT'});
  assert.equal(f.sent.some(value=>value.params.state==='completed'),false);
});
