import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { LocalFilesService } from '../src/main/local-files-service.mjs';
import { LocalFilesPolicy, parseMounts } from '../src/main/local-files-policy.mjs';
import { LocalFilesCapability } from '../src/main/local-files-capability.mjs';

async function fixture(t, options = {}) {
  const root = await fs.realpath(await fs.mkdtemp(path.join(os.tmpdir(), 'sparkx-files-')));
  const source = path.join(root, 'source'); await fs.mkdir(source);
  let identity = { deployment_id: 'deployment', owner_id: 'owner', client_id: 'client' };
  const changes = [];
  const policy = new LocalFilesPolicy({ platform: 'linux', mountProvider: async () => [{ path: '/', type: 'ext4' }] });
  const service = new LocalFilesService({ directory: path.join(root, 'profile'), getIdentity: () => identity, onChange: value => changes.push(value), policy, roots: [source], ...options });
  t.after(async () => { await service.close(); await fs.rm(root, { recursive: true, force: true }); });
  return { root, source, service, changes, setIdentity(value) { identity = value; service.refreshIdentity(); }, async file(name, contents = 'hello world') { const value = path.join(source, name); await fs.writeFile(value, contents); return service.dispatch({ operation: 'resolve', locator: value }); } };
}
const rejects = (fn, code) => assert.rejects(fn, error => error.code === code);

test('default roots contain only Desktop, Documents and Downloads, including unavailable folders', async t => {
  const f = await fixture(t, { roots: undefined });
  t.mock.method(os, 'homedir', () => f.root);
  for (const name of ['Desktop', 'Downloads', 'Other']) await fs.mkdir(path.join(f.root, name));
  f.service.policy.mountProvider = async () => [{ path: '/', type: 'ext4' }, { path: '/external', type: 'ext4' }];
  const { roots } = await f.service.dispatch({ operation: 'roots' });
  assert.deepEqual(roots.map(root => root.name), ['Desktop', 'Documents', 'Downloads']);
  assert.deepEqual(roots.map(root => root.locator), ['Desktop', 'Documents', 'Downloads'].map(name => path.join(f.root, name)));
  assert.equal(roots[1].unavailable, 'unavailable');
  assert.ok(roots[0].ref && roots[2].ref);
});

test('verified bounded windows preserve originals and reject replacement/in-place changes', async t => {
  const f = await fixture(t); const file = await f.file('notes.txt', '原始文件 hello');
  const read = await f.service.dispatch({ operation: 'readBytes', ref: file.ref, version: file.version, offset: 0, length: 100 });
  assert.equal(Buffer.from(read.bytes).toString(), '原始文件 hello');
  assert.equal(await fs.readFile(file.locator, 'utf8'), '原始文件 hello');
  await rejects(() => f.service.dispatch({ operation: 'readBytes', ref: file.ref, version: file.version, offset: -1, length: 5 }), 'invalid_range');
  await rejects(() => f.service.dispatch({ operation: 'readBytes', ref: file.ref, version: file.version, offset: 0, length: 2 ** 22 }), 'invalid_range');
  await fs.writeFile(file.locator, 'new source');
  await rejects(() => f.service.dispatch({ operation: 'readBytes', ref: file.ref, version: file.version, offset: 0, length: 10 }), 'source_changed');
  const refreshed = await f.service.dispatch({ operation: 'resolve', locator: file.locator });
  await fs.rename(file.locator, `${file.locator}.old`); await fs.writeFile(file.locator, 'replacement');
  await rejects(() => f.service.dispatch({ operation: 'readBytes', ref: refreshed.ref, version: refreshed.version, offset: 0, length: 10 }), 'source_changed');
});

test('login loss revokes old references and reading scope across same-identity sign-in', async t => {
  const f = await fixture(t), file = await f.file('local.txt'); const initial = f.service.getScope();
  f.setIdentity(null); await rejects(() => f.service.dispatch({ operation: 'roots' }), 'locked');
  f.setIdentity({ deployment_id: 'deployment', owner_id: 'owner', client_id: 'client' });
  assert.notEqual(f.service.getScope(), initial);
  await rejects(() => f.service.dispatch({ operation: 'stat', ref: file.ref }), 'reference_expired');
  assert.ok(f.changes.some(change => change.kind === 'revoked'));
});

test('cancel and auth change reject in-flight bytes and top-level navigation revokes authority', async t => {
  const f = await fixture(t), file = await f.file('cancel.txt');
  let resume, entered;
  const gate = new Promise(resolve => { resume = resolve; }), ready = new Promise(resolve => { entered = resolve; });
  f.service.policy.available = async () => { entered(); await gate; };
  const reading = f.service.dispatch({ operation: 'readBytes', ref: file.ref, version: file.version, offset: 0, length: 8, request_id: 'read-1' });
  await ready; await f.service.dispatch({ operation: 'cancel', request_id: 'read-1' }); resume();
  await rejects(() => reading, 'cancelled');
  const previous = f.service.getScope(); f.service.revoke(); assert.notEqual(f.service.getScope(), previous);
  await rejects(() => f.service.dispatch({ operation: 'stat', ref: file.ref }), 'reference_expired');
});

test('pagination sorts on disk and revisits every entry without full in-memory arrays', async t => {
  const f = await fixture(t, { limits: { pageSize: 2, scannedPerBatch: 4 } });
  await Promise.all(['file10', 'file2', 'file1', '.hidden', 'file3'].map(name => fs.writeFile(path.join(f.source, name), name)));
  await fs.mkdir(path.join(f.source, 'folder'));
  const { roots } = await f.service.dispatch({ operation: 'roots' }); const ref = roots[0].ref;
  let listing = await f.service.dispatch({ operation: 'list', ref });
  const oldCursor = listing.cursor;
  while (listing.cursor) listing = await f.service.dispatch({ operation: 'list', ref, cursor: listing.cursor });
  assert.equal(listing.complete, true); assert.equal(listing.totalLoaded, 5);
  const names = [];
  for (let page = 0; page < 3; page++) names.push(...(await f.service.dispatch({ operation: 'list', ref, page })).entries.map(entry => entry.name));
  assert.deepEqual(names, ['folder', 'file1', 'file2', 'file3', 'file10']);
  await rejects(() => f.service.dispatch({ operation: 'list', ref, cursor: oldCursor }), 'stale_cursor');
  let hidden = await f.service.dispatch({ operation: 'list', ref, showHidden: true, filter: 'hidden', refresh: true });
  // A bounded scan may contain no match; directory order differs by filesystem.
  while (hidden.cursor) hidden = await f.service.dispatch({ operation: 'list', ref, showHidden: true, filter: 'hidden', cursor: hidden.cursor });
  assert.equal(hidden.entries[0].name, '.hidden');
  const stale = hidden.cursor;
  if (stale) await rejects(() => f.service.dispatch({ operation: 'list', ref, cursor: stale, showHidden: true, filter: 'file' }), 'stale_cursor');
  assert.equal(f.service.pages.db.prepare('PRAGMA cache_size').get().cache_size, -512);
});

test('directory entry budget reports incomplete and does not pretend completion', async t => {
  const f = await fixture(t, { limits: { pageSize: 2, entries: 3 } });
  for (let i = 0; i < 6; i++) await fs.writeFile(path.join(f.source, `file${i}`), 'x');
  const { roots } = await f.service.dispatch({ operation: 'roots' }), ref = roots[0].ref;
  let listing = await f.service.dispatch({ operation: 'list', ref });
  listing = await f.service.dispatch({ operation: 'list', ref, cursor: listing.cursor });
  assert.equal(listing.totalLoaded, 3); assert.equal(listing.complete, false); assert.equal(listing.incomplete, 'entry_limit'); assert.equal(listing.cursor, null);
});

test('disk page budget stops enumeration, keeps prior pages readable and releases handles', async t => {
  const f = await fixture(t, { limits: { pageSize: 20, tempBytes: 128 * 1024 } });
  for (let i = 0; i < 140; i++) await fs.writeFile(path.join(f.source, `${String(i).padStart(3, '0')}-${'long-name-'.repeat(15)}`), 'x');
  const { roots } = await f.service.dispatch({ operation: 'roots' }), ref = roots[0].ref;
  let listing = await f.service.dispatch({ operation: 'list', ref });
  while (listing.cursor) listing = await f.service.dispatch({ operation: 'list', ref, cursor: listing.cursor });
  assert.equal(listing.incomplete, 'storage_limit'); assert.equal(listing.complete, false); assert.equal(f.service.listings.get(ref).dir, null);
  assert.ok((await f.service.dispatch({ operation: 'list', ref, page: 0 })).entries.length > 0);
});

test('current-directory mutation invalidates cursor instead of mixing generations', async t => {
  const f = await fixture(t, { limits: { pageSize: 1 } }); await f.file('a'); await f.file('b');
  const { roots } = await f.service.dispatch({ operation: 'roots' }), ref = roots[0].ref;
  const listing = await f.service.dispatch({ operation: 'list', ref }); await f.file('c');
  await rejects(() => f.service.dispatch({ operation: 'list', ref, cursor: listing.cursor }), 'stale_cursor');
  const fresh = await f.service.dispatch({ operation: 'list', ref, refresh: true }); assert.notEqual(fresh.listing, listing.listing);
});

test('a mutation during the first unwatched batch cannot claim a complete listing', async t => {
  const f = await fixture(t); await f.file('first');
  const { roots } = await f.service.dispatch({ operation: 'roots' });
  const guard = f.service.policy.guard.bind(f.service.policy); let changed = false;
  f.service.policy.guard = async value => { const result = await guard(value); if (!changed && value.endsWith('/first')) { changed = true; await fs.writeFile(path.join(f.source, 'arrived-during-list'), 'new'); } return result; };
  const listing = await f.service.dispatch({ operation: 'list', ref: roots[0].ref });
  assert.equal(listing.changed, true); assert.equal(listing.complete, false); assert.equal(listing.cursor, null);
});

test('links require explicit following and ancestor cycles are rejected', async t => {
  const f = await fixture(t), file = await f.file('target');
  await fs.symlink(file.locator, path.join(f.source, 'alias'));
  const link = await f.service.dispatch({ operation: 'resolve', locator: path.join(f.source, 'alias') });
  assert.equal(link.kind, 'link');
  await rejects(() => f.service.dispatch({ operation: 'readBytes', ref: link.ref, version: link.version, offset: 0, length: 8 }), 'unsupported');
  const followed = await f.service.dispatch({ operation: 'followLink', ref: link.ref, ancestors: [f.source] });
  assert.equal(followed.kind, 'file');
  await fs.unlink(path.join(f.source, 'alias')); await fs.symlink(f.source, path.join(f.source, 'alias'));
  await rejects(() => f.service.dispatch({ operation: 'readBytes', ref: followed.ref, version: followed.version, offset: 0, length: 8 }), 'source_changed');
  const cycle = await f.service.dispatch({ operation: 'resolve', locator: path.join(f.source, 'alias') });
  await rejects(() => f.service.dispatch({ operation: 'followLink', ref: cycle.ref, ancestors: [f.source] }), 'link_cycle');
});

test('network mounts are visible metadata but never opened, even through a link', async t => {
  const f = await fixture(t); const remote = path.join(f.root, 'remote'); await fs.mkdir(remote); await fs.writeFile(path.join(remote, 'secret'), 'remote');
  f.service.policy = new LocalFilesPolicy({ platform: 'linux', mountProvider: async () => [{ path: remote, type: 'nfs' }, { path: '/', type: 'ext4' }] });
  f.service.rootLocators = [f.source, remote];
  const { roots } = await f.service.dispatch({ operation: 'roots' }); assert.equal(roots[1].unavailable, 'remote_location'); assert.equal(roots[1].ref, '');
  await rejects(() => f.service.dispatch({ operation: 'resolve', locator: path.join(remote, 'secret') }), 'remote_location');
  await fs.symlink(path.join(remote, 'secret'), path.join(f.source, 'remote-link'));
  const link = await f.service.dispatch({ operation: 'resolve', locator: path.join(f.source, 'remote-link') });
  await rejects(() => f.service.dispatch({ operation: 'followLink', ref: link.ref }), 'remote_location');
});

test('a replacement mount invalidates old authority even if inode metadata is unchanged', async t => {
  const f = await fixture(t); let mountId = 'original';
  f.service.policy = new LocalFilesPolicy({ platform: 'linux', mountProvider: async () => [{ path: '/', type: 'ext4', id: mountId }] });
  const file = await f.file('volume.txt'); mountId = 'replacement';
  await rejects(() => f.service.dispatch({ operation: 'readBytes', ref: file.ref, version: file.version, offset: 0, length: 10 }), 'source_changed');
  const newFile = await f.service.dispatch({ operation: 'resolve', locator: file.locator }); assert.notEqual(newFile.ref, file.ref);
});

test('placeholders and special files cannot be opened as content', async t => {
  const f = await fixture(t), placeholder = await f.file('.report.icloud', 'placeholder');
  await rejects(() => f.service.dispatch({ operation: 'readBytes', ref: placeholder.ref, version: placeholder.version, offset: 0, length: 100 }), 'cloud_placeholder');
  const directory = await f.service.dispatch({ operation: 'resolve', locator: f.source });
  await rejects(() => f.service.dispatch({ operation: 'readBytes', ref: directory.ref, version: directory.version, offset: 0, length: 100 }), 'unsupported');
});

test('relative images enforce resolved containment, signatures and byte budgets', async t => {
  const f = await fixture(t), document = await f.file('readme.md', '![](image.png)');
  const png = Buffer.from([137,80,78,71,13,10,26,10,0]); await f.file('image.png', png);
  const image = await f.service.dispatch({ operation: 'relativeImage', ref: document.ref, version: document.version, path: 'image.png' }); assert.equal(image.mime, 'image/png');
  await fs.writeFile(path.join(f.root, 'outside.png'), png); await fs.symlink(path.join(f.root, 'outside.png'), path.join(f.source, 'escape.png'));
  await rejects(() => f.service.dispatch({ operation: 'relativeImage', ref: document.ref, version: document.version, path: 'escape.png' }), 'outside_document');
  await rejects(() => f.service.dispatch({ operation: 'relativeImage', ref: document.ref, version: document.version, path: '../outside.png' }), 'outside_document');
  await rejects(() => f.service.dispatch({ operation: 'relativeImage', ref: document.ref, version: document.version, path: 'https://invalid/image.png' }), 'invalid_request');
  await f.file('fake.png', '<script>bad</script>');
  await rejects(() => f.service.dispatch({ operation: 'relativeImage', ref: document.ref, version: document.version, path: 'fake.png' }), 'unsupported');
});

test('persistent reading state is bounded, identity scoped, locator only, and clear preserves originals', async t => {
  const f = await fixture(t), file = await f.file('report');
  const state = { tabs: [{ locator: file.locator, position: { page: 2 } }], active: file.locator, expanded: [f.source], selected: file.locator, scrollTop: 10, showHidden: false, filter: '' };
  await f.service.dispatch({ operation: 'saveState', state }); assert.deepEqual((await f.service.dispatch({ operation: 'loadState' })).state, state);
  await rejects(() => f.service.dispatch({ operation: 'saveState', state: { ...state, tabs: [{ locator: file.locator, ref: file.ref }] } }), 'invalid_state');
  f.setIdentity({ deployment_id: 'deployment', owner_id: 'different-owner', client_id: 'client' }); assert.equal((await f.service.dispatch({ operation: 'loadState' })).state, null);
  f.setIdentity({ deployment_id: 'deployment', owner_id: 'owner', client_id: 'client' }); assert.deepEqual((await f.service.dispatch({ operation: 'loadState' })).state, state);
  await f.service.dispatch({ operation: 'clear', request_id: 'clear-request' }); assert.equal((await f.service.dispatch({ operation: 'loadState' })).state, null);
  assert.equal(await fs.readFile(file.locator, 'utf8'), 'hello world');
});

test('watch budgets fall back to manual refresh and release keeps reusable locator reference', async t => {
  const f = await fixture(t, { limits: { watches: 1 } }), a = await f.file('a'), b = await f.file('b');
  assert.equal((await f.service.dispatch({ operation: 'watch', ref: a.ref })).watching, true);
  assert.deepEqual(await f.service.dispatch({ operation: 'watch', ref: b.ref }), { watching: false, reason: 'watch_limit' });
  await f.service.dispatch({ operation: 'release', ref: a.ref }); assert.equal(f.service.watches.size, 0); assert.equal((await f.service.dispatch({ operation: 'stat', ref: a.ref })).name, 'a');
});

test('capability rejects child frames, other webcontents and malformed requests before filesystem access', async t => {
  const f = await fixture(t), mainFrame = { url: 'sparkclaw-app://workbench/index.html' }, webContents = { mainFrame };
  const capability = new LocalFilesCapability({ window: { webContents }, service: f.service });
  const valid = { sender: webContents, senderFrame: mainFrame };
  assert.equal((await capability.dispatch(valid, { schema_version: 1, operation: 'roots' })).ok, true);
  for (const event of [{ ...valid, sender: {} }, { ...valid, senderFrame: { url: mainFrame.url } }, { ...valid, senderFrame: { url: 'sparkclaw-preview://viewer/index.html' } }]) assert.equal((await capability.dispatch(event, { schema_version: 1, operation: 'roots' })).error.code, 'untrusted_sender');
  assert.equal((await capability.dispatch(valid, { schema_version: 1, operation: 'roots', path: '/' })).error.code, 'invalid_request');
  mainFrame.url = 'https://example.com'; assert.equal((await capability.dispatch(valid, { schema_version: 1, operation: 'roots' })).error.code, 'untrusted_sender');
});

test('capability does not deliver a completed response after its top frame navigates', async () => {
  const mainFrame = { url: 'sparkclaw-app://workbench/index.html' }, webContents = { mainFrame };
  let resume;
  const capability = new LocalFilesCapability({ window: { webContents }, service: { dispatch: () => new Promise(resolve => { resume = resolve; }) } });
  const pending = capability.dispatch({ sender: webContents, senderFrame: mainFrame }, { schema_version: 1, operation: 'roots' });
  mainFrame.url = 'sparkclaw-preview://viewer/index.html'; resume({ bytes: 'private' });
  assert.equal((await pending).error.code, 'untrusted_sender');
});

test('mount parsing preserves escaped paths and explicitly classifies remote mounts', () => {
  assert.deepEqual(parseMounts('1 0 1:2 / / rw - ext4 /dev/root rw\n2 1 0:1 / /media/My\\040Disk rw - nfs host:/share rw', 'linux'), [{ path: '/media/My Disk', type: 'nfs', id: '2', source: 'host:/share' }, { path: '/', type: 'ext4', id: '1', source: '/dev/root' }]);
  assert.deepEqual(parseMounts('/dev/disk on / (apfs, local)\nserver:/share on /Volumes/Share (nfs)', 'darwin'), [{ path: '/Volumes/Share', type: 'nfs', source: 'server:/share' }, { path: '/', type: 'apfs', source: '/dev/disk' }]);
});
