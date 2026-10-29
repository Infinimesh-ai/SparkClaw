import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import vm from 'node:vm';
const source = await fs.readFile(new URL('../src/preload/workbench.cjs', import.meta.url), 'utf8');
function preload({ mainFrame = true, origin = 'sparkclaw-app:', configured = true } = {}) {
  const exposed = {}, calls = [];
  vm.runInNewContext(source, {
    process: { isMainFrame: mainFrame, argv: configured ? ['--sparkclaw-client-store=1'] : [] },
    location: { protocol: origin, hostname: origin === 'sparkclaw-app:' ? 'workbench' : 'viewer' },
    document: { addEventListener() {}, documentElement: { dataset: {} } },
    require: () => ({ contextBridge: { exposeInMainWorld: (name, value) => { exposed[name] = value; } },
      ipcRenderer: { on() {}, removeListener() {}, invoke: async (channel, request) => { calls.push({ channel, request }); return { ok: false, error: { code: 'locked', message: 'Sign in' } }; } } }),
  });
  return { exposed, calls };
}
test('workbench preload exposes no bridges or subscriptions to a document frame', () => {
  assert.deepEqual(Object.keys(preload({ mainFrame: false }).exposed), []);
  assert.deepEqual(Object.keys(preload({ origin: 'sparkclaw-preview:' }).exposed), []);
  assert.equal(preload({ configured: false }).exposed.sparkclawLocalFiles, undefined);
});
test('local files preload carries typed failures and only named capability operations', async () => {
  const { exposed, calls } = preload();
  assert.equal(exposed.sparkclawLocalFiles.envelopeVersion, 1);
  const result = await exposed.sparkclawLocalFiles.roots();
  assert.equal(result.error.code, 'locked');
  assert.deepEqual(JSON.parse(JSON.stringify(calls)), [{ channel: 'sparkclaw-local-files:invoke', request: { schema_version: 1, operation: 'roots' } }]);
  for (const forbidden of ['fs', 'send', 'invoke', 'writeFile', 'deleteFile', 'shell', 'exec']) assert.equal(exposed.sparkclawLocalFiles[forbidden], undefined);
});
