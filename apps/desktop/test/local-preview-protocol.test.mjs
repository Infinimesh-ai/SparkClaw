import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { createLocalPreviewProtocolHandler, previewRequestAllowed, PREVIEW_CSP, guardLocalPreviewNavigation } from '../src/main/local-preview-protocol.mjs';

test('preview protocol serves only bundled viewer files with restrictive policy', async (t) => {
  const directory = await fs.mkdtemp(path.join(os.tmpdir(), 'sparkx-preview-'));
  t.after(() => fs.rm(directory, { recursive: true, force: true }));
  await fs.mkdir(path.join(directory, 'assets')); await fs.writeFile(path.join(directory, 'index.html'), 'viewer');
  await fs.writeFile(path.join(directory, 'secret.json'), 'secret');
  await fs.symlink(path.join(directory, 'secret.json'), path.join(directory, 'assets', 'leak.js'));
  const handler = createLocalPreviewProtocolHandler(directory);
  const response = await handler(new Request('sparkclaw-preview://viewer/index.html'));
  assert.equal(response.status, 200); assert.equal(await response.text(), 'viewer');
  assert.equal(response.headers.get('content-security-policy'), PREVIEW_CSP);
  for (const url of ['sparkclaw-preview://other/index.html', 'sparkclaw-preview://viewer/secret.json', 'sparkclaw-preview://viewer/index.html?path=/etc/passwd', 'sparkclaw-preview://viewer/assets/%2e%2e/secret.json', 'file:///etc/passwd']) assert.notEqual((await handler(new Request(url))).status, 200);
  assert.equal((await handler(new Request('sparkclaw-preview://viewer/index.html', { method: 'POST' }))).status, 403);
  const outside = await fs.mkdtemp(path.join(os.tmpdir(), 'sparkx-outside-')); t.after(() => fs.rm(outside, { recursive: true, force: true }));
  await fs.writeFile(path.join(outside, 'outside.js'), 'outside'); await fs.symlink(path.join(outside, 'outside.js'), path.join(directory, 'assets', 'outside.js'));
  assert.equal((await handler(new Request('sparkclaw-preview://viewer/assets/outside.js'))).status, 403);
});
test('preview-origin requests cannot reach network, workbench or nested documents', () => {
  const origin = 'sparkclaw-preview://viewer';
  for (const url of ['https://example.com', 'file:///etc/passwd', 'sparkclaw-app://workbench/index.html', 'http://127.0.0.1/api']) assert.equal(previewRequestAllowed({ url, initiator: origin, resourceType: 'xhr' }), false);
  assert.equal(previewRequestAllowed({ url: origin+'/assets/a.js', initiator: origin, resourceType: 'script' }), true);
  assert.equal(previewRequestAllowed({ url: origin+'/index.html', initiator: origin, resourceType: 'subFrame' }), false);
  assert.equal(previewRequestAllowed({ url: 'https://example.com', frame: { url: origin+'/index.html' } }), false);
  assert.equal(previewRequestAllowed({ url: 'https://example.com', initiator: 'sparkclaw-app://workbench' }), true);
});
test('preview frame navigation cannot enter the workbench or top frame', () => {
  const listeners = new Map(); const wc = { on: (n, fn) => listeners.set(n, fn), isDestroyed: () => false, removeListener: (n) => listeners.delete(n) };
  const dispose = guardLocalPreviewNavigation(wc); let blocked = 0;
  const event = { preventDefault: () => blocked++ };
  const listener = listeners.get('will-frame-navigate');
  listener(event, { frame: { url: 'sparkclaw-preview://viewer/index.html' }, url: 'sparkclaw-app://workbench/index.html', isMainFrame: false });
  listener(event, { frame: { url: 'sparkclaw-app://workbench/index.html' }, url: 'sparkclaw-preview://viewer/index.html', isMainFrame: true });
  listener(event, { frame: { url: 'sparkclaw-app://workbench/index.html' }, url: 'sparkclaw-preview://viewer/index.html', isMainFrame: false });
  assert.equal(blocked, 2); dispose(); assert.equal(listeners.size, 0);
});
