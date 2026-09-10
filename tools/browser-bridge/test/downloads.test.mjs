import assert from 'node:assert/strict';
import test from 'node:test';
import { TaskDownloads } from '../src/downloads.mjs';

const guid = '12345678-1234-4123-8123-123456789abc';
const url = 'https://provider.test/export';
const startedAt = Date.now();
const tick = () => new Promise(resolve => setImmediate(resolve));

async function fixture() {
  const listeners = new Set(), items = new Map(), sent = [], removed = [], canceled = [];
  const downloads = {
    onCreated: { addListener: value => listeners.add(value), removeListener: value => listeners.delete(value) },
    search: async query => [...items.values()].filter(item => query.id === undefined || item.id === query.id),
    removeFile: async id => { removed.push(id); },
    erase: async ({ id }) => { items.delete(id); },
    cancel: async id => { canceled.push(id); items.get(id).state = 'interrupted'; },
  };
  const task = new TaskDownloads({ chromeAPI: { downloads }, send: value => sent.push(value),
    ownsTab: id => id === 7, now: () => startedAt, delay: tick });
  await task.command('sparkclaw.downloads.configure', [{ behavior: 'allowAndName' }]);
  const create = (id, changes = {}) => {
    const item = { id, url, finalUrl: url, startTime: new Date(startedAt + 1).toISOString(), filename: '/downloads/mail.eml', state: 'complete', fileSize: 16, exists: true, ...changes };
    items.set(id, item); for (const listener of listeners) listener(item); return item;
  };
  const begin = (id = guid, tabId = 7) => task.onPageEvent({ tabId }, 'Page.downloadWillBegin', { guid: id, url, frameId: 'frame', suggestedFilename: 'mail.eml' });
  const progress = (state = 'completed', id = guid) => task.onPageEvent({ tabId: 7 }, 'Page.downloadProgress', { guid: id, state, receivedBytes: 16, totalBytes: 16 });
  return { task, downloads, items, sent, removed, canceled, listeners, create, begin, progress };
}

test('native downloads retain GUID/frame ownership and wait for the API completion', async () => {
  const f = await fixture();
  assert.equal(f.begin(guid, 99), false);
  assert.equal(f.sent.length, 0);
  const item = f.create(1, { state: 'in_progress' });
  f.begin(); f.progress();
  await tick();
  assert.equal(f.sent.length, 1);
  item.state = 'complete';
  await tick(); await tick();
  assert.equal(f.sent[0].params[0].frameId, 'frame');
  assert.equal(f.sent.at(-1).params[0].filename, item.filename);
  await f.task.command('sparkclaw.downloads.release', [guid]);
  assert.deepEqual(f.removed, [1]);
  const next = '22345678-1234-4123-8123-123456789abc';
  f.create(2); f.begin(next); f.progress('completed', next);
  await tick();
  assert.equal(f.sent.at(-1).params[0].state, 'completed');
  await f.task.command('sparkclaw.downloads.release', [next]);
  f.task.close();
  assert.equal(f.listeners.size, 0);
});

test('same-URL ambiguity never selects or removes an owner download', async () => {
  const f = await fixture();
  f.create(1); f.create(2); f.begin(); f.progress();
  await tick();
  assert.equal(f.sent.at(-1).params[0].state, 'canceled');
  assert.deepEqual(f.removed, []);
  assert.deepEqual(f.canceled, []);
  f.task.close();
});

test('parallel provider downloads retain independent files and cancellation', async () => {
  const first = await fixture();
  const sent = [];
  const second = new TaskDownloads({ chromeAPI: { downloads: first.downloads },
    send: value => sent.push(value), ownsTab: id => id === 20, now: () => startedAt, delay: tick });
  await second.command('sparkclaw.downloads.configure', [{ behavior: 'allowAndName' }]);
  const otherGuid = '22345678-1234-4123-8123-123456789abc';
  const otherURL = 'https://other-provider.test/export';
  first.create(1);
  first.create(2, { url: otherURL, finalUrl: otherURL, filename: '/downloads/other.eml' });
  first.begin(); first.progress();
  assert.equal(second.onPageEvent({ tabId: 7 }, 'Page.downloadWillBegin', { guid, url }), false);
  second.onPageEvent({ tabId: 20 }, 'Page.downloadWillBegin', { guid: otherGuid, url: otherURL, frameId: 'other-frame', suggestedFilename: 'mail.eml' });
  second.onPageEvent({ tabId: 20 }, 'Page.downloadProgress', { guid: otherGuid, state: 'completed', receivedBytes: 16, totalBytes: 16 });
  await tick();
  assert.equal(sent.at(-1).params[0].filename, '/downloads/other.eml');
  assert.equal(first.sent.at(-1).params[0].filename, '/downloads/mail.eml');
  first.task.close();
  await tick();
  assert.deepEqual(first.removed, [1]);
  assert.equal(first.items.has(2), true);
  await second.command('sparkclaw.downloads.release', [otherGuid]);
  assert.deepEqual(first.removed, [1, 2]);
  assert.deepEqual(first.canceled, []);
  second.close();
});

test('task cancellation and disconnect cancel only a proven native download', async () => {
  for (const disconnect of [false, true]) {
    const f = await fixture();
    f.create(1, { state: 'in_progress' }); f.begin();
    if (disconnect) f.task.close();
    else await f.task.command('sparkclaw.downloads.cancel', [guid]);
    await tick();
    assert.deepEqual(f.canceled, [1]);
    assert.equal(f.items.size, 0);
    f.task.close();
  }
});

test('download commands reject foreign GUIDs and unsupported context behavior', async () => {
  const f = await fixture();
  await assert.rejects(f.task.command('sparkclaw.downloads.cancel', [guid]), /outside the task/);
  await assert.rejects(f.task.command('sparkclaw.downloads.configure', [{ behavior: 'allow' }]), /Unsupported/);
  f.task.close();
});

test('disconnect after native completion releases a file still waiting for the host', async () => {
  const f = await fixture();
  f.create(1); f.begin(); f.progress();
  await tick();
  assert.equal(f.sent.at(-1).params[0].state, 'completed');
  f.task.close(); f.task.close();
  await tick();
  assert.deepEqual(f.removed, [1]);
  assert.equal(f.items.size, 0);
});

test('native size limits cancel acquisition and interrupted records can be erased', async () => {
  const f = await fixture();
  f.create(1, {state:'in_progress',exists:false}); f.begin();
  f.task.onPageEvent({tabId:7}, 'Page.downloadProgress', {guid,state:'inProgress',receivedBytes:0,totalBytes:(110<<20)+1});
  await tick();
  assert.equal(f.sent.at(-1).params[0].state,'canceled');
  assert.deepEqual(f.canceled,[1]);
  assert.equal(f.items.size,0);
  f.task.close();
});
