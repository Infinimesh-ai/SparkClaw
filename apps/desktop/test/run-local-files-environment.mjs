// Opt-in OS qualification. Uses only owned temporary files, processes and disk
// images; never fills a host filesystem or alters a user's mount/permissions.
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import crypto from 'node:crypto';
import { spawn, execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { fileURLToPath, pathToFileURL } from 'node:url';

const execute = promisify(execFile);
const modules = process.env.SPARKCLAW_LOCAL_FILES_TEST_MODULES || fileURLToPath(new URL('../src/main/', import.meta.url));
const moduleURL = name => pathToFileURL(path.join(modules, name)).href;
const { LocalFilesService } = await import(moduleURL('local-files-service.mjs'));
const { LocalPreviewBroker } = await import(moduleURL('local-preview-broker.mjs'));
const { LocalFilesPages } = await import(moduleURL('local-files-pages.mjs'));
const { publicError } = await import(moduleURL('local-files-policy.mjs'));
const root = await fs.realpath(await fs.mkdtemp(path.join(os.tmpdir(), 'sparkx-owned-environment-')));
const source = path.join(root, 'source'); await fs.mkdir(source);
const identity = { deployment_id: 'synthetic-environment', owner_id: 'synthetic-owner', client_id: 'synthetic-client' };
const service = new LocalFilesService({ directory: path.join(root, 'reading'), getIdentity: () => identity, roots: [source] });
const results = [], pause = ms => new Promise(resolve => setTimeout(resolve, ms));
const until = async check => { const end = Date.now() + 5000; while (!await check()) { if (Date.now() > end) throw new Error('Cleanup deadline exceeded'); await pause(20); } };
const inspect = async (name, run) => {
  if (process.env.SPARKCLAW_ENVIRONMENT_CASE && !name.includes(process.env.SPARKCLAW_ENVIRONMENT_CASE)) return;
  const started = performance.now();
  try { results.push({ name, status: 'passed', boundary: 'real_os', ...(await run()), duration_ms: Math.round(performance.now() - started) }); }
  catch (error) { results.push({ name, status: 'failed', code: publicError(error).code, message: error.message.replaceAll(root, '<fixture>'), duration_ms: Math.round(performance.now() - started) }); }
};
const resolve = locator => service.dispatch({ operation: 'resolve', locator });
const read = metadata => service.dispatch({ operation: 'readBytes', ref: metadata.ref, version: metadata.version, offset: 0, length: Math.min(256, metadata.size) });
const rejected = (promise, code, label = code) => assert.rejects(promise, error => publicError(error).code === code, label);
let mounted;
try {
  await inspect('POSIX permission removal and restoration', async () => {
    const file = path.join(source, 'permissions.txt'); await fs.writeFile(file, 'original bytes', { mode: 0o600 });
    const initial = await resolve(file); assert.equal(Buffer.from((await read(initial)).bytes).toString(), 'original bytes');
    try { await fs.chmod(file, 0); await rejected(read(initial), 'permission_denied'); }
    finally { await fs.chmod(file, 0o600); }
    await rejected(read(initial), 'source_changed');
    assert.equal(Buffer.from((await read(await resolve(file))).bytes).toString(), 'original bytes');
    return { old_version_invalid_after_permission_restore: true, source_bytes_preserved: true };
  });

  await inspect('Changed symlink, link cycle and associated-image containment', async () => {
    const first = path.join(source, 'first.txt'), second = path.join(source, 'second.txt'), link = path.join(source, 'link');
    await fs.writeFile(first, 'first'); await fs.writeFile(second, 'second'); await fs.symlink(first, link);
    const followed = await service.dispatch({ operation: 'followLink', ref: (await resolve(link)).ref });
    assert.equal(Buffer.from((await read(followed)).bytes).toString(), 'first');
    await fs.unlink(link); await fs.symlink(second, link); await rejected(read(followed), 'source_changed');
    await fs.unlink(link); await fs.symlink(link, link);
    await rejected(service.dispatch({ operation: 'followLink', ref: (await resolve(link)).ref }), 'link_cycle');
    const md = path.join(source, 'notes.md'), image = path.join(root, 'outside.png');
    await fs.writeFile(md, '# Owned document'); await fs.writeFile(image, Buffer.from('89504e470d0a1a0a', 'hex'));
    await fs.symlink(image, path.join(source, 'escape.png')); const document = await resolve(md);
    await rejected(service.dispatch({ operation: 'relativeImage', ref: document.ref, version: document.version, path: 'escape.png' }), 'outside_document');
    return { explicit_link_following: true, old_target_rejected: true, outside_image_not_read: true };
  });

  await inspect('Legacy iCloud placeholder gate without hydration', async () => {
    const file = path.join(source, '.placeholder.txt.icloud'); await fs.writeFile(file, 'owned placeholder metadata');
    const before = await fs.readFile(file), metadata = await resolve(file);
    assert.equal(metadata.unavailable, 'cloud_placeholder'); await rejected(read(metadata), 'cloud_placeholder');
    assert.deepEqual(await fs.readFile(file), before);
    return { mechanism: 'real .icloud path; synthetic metadata file', file_provider_dataless: 'not_exercised', hydration_attempted: false };
  });

  await inspect('Large directory bounded pagination and revisiting', async () => {
    const directory = path.join(source, 'many'); await fs.mkdir(directory);
    const total = 33_024;
    for (let start = 0; start < total; start += 64) await Promise.all(Array.from({ length: Math.min(64, total - start) }, (_, offset) => fs.writeFile(path.join(directory, `entry-${String(start + offset).padStart(5, '0')}`), 'x')));
    const metadata = await resolve(directory); const rssBefore = process.memoryUsage().rss; let peakRSS = rssBefore;
    const timer = setInterval(() => { peakRSS = Math.max(peakRSS, process.memoryUsage().rss); }, 10);
    let listing, calls = 0;
    try {
      do {
        listing = await service.dispatch({ operation: 'list', ref: metadata.ref, ...(listing?.cursor ? { cursor: listing.cursor } : {}) });
        assert.ok(listing.entries.length <= 128); assert.ok(service.refs.size <= 4096); calls++;
      } while (listing.cursor);
      assert.equal(listing.complete, false); assert.ok(['entry_limit', 'storage_limit'].includes(listing.incomplete));
      assert.ok(listing.totalLoaded > 1000 && listing.totalLoaded <= 32768);
      for (const page of [0, Math.floor(listing.totalLoaded / 256), Math.floor((listing.totalLoaded - 1) / 128)]) {
        const result = await service.dispatch({ operation: 'list', ref: metadata.ref, page });
        const names = result.entries.map(entry => entry.name); assert.deepEqual(names, [...names].sort());
        assert.equal(new Set(names).size, names.length);
      }
      assert.equal(service.listings.get(metadata.ref).dir, null);
      const bytes = (await fs.stat(service.pages.file)).size; assert.ok(bytes <= service.limits.tempBytes);
      return { input_entries: total, indexed_entries: listing.totalLoaded, stop_reason: listing.incomplete, calls, sqlite_bytes: bytes, peak_rss_bytes: peakRSS, rss_growth_bytes: peakRSS - rssBefore, bounded_refs: service.refs.size };
    } finally { clearInterval(timer); await service.dispatch({ operation: 'release', ref: metadata.ref }); }
  });

  await inspect('Rapid read cancellation, watcher release and bounded references', async () => {
    const file = path.join(source, 'switch.txt'); await fs.writeFile(file, Buffer.alloc(8192, 65));
    const hash = crypto.createHash('sha256').update(await fs.readFile(file)).digest('hex'); let cancellations = 0;
    for (let iteration = 0; iteration < 60; iteration++) {
      const metadata = await resolve(file); await service.dispatch({ operation: 'watch', ref: metadata.ref });
      const pending = Array.from({ length: 16 }, (_, n) => service.dispatch({ operation: 'readBytes', ref: metadata.ref, version: metadata.version, offset: 0, length: 8192, request_id: `read-${iteration}-${n}` }));
      const settled = Promise.allSettled(pending);
      await Promise.all(pending.map((_, n) => service.dispatch({ operation: 'cancel', request_id: `read-${iteration}-${n}` })));
      for (const result of await settled) if (result.status === 'rejected') { assert.ok(['busy', 'cancelled'].includes(publicError(result.reason).code)); cancellations++; }
      await service.dispatch({ operation: 'release', ref: metadata.ref });
      assert.equal(service.requests.size, 0); assert.equal(service.activeRequests, 0); assert.equal(service.reads, 0); assert.equal(service.watches.size, 0);
    }
    assert.equal(crypto.createHash('sha256').update(await fs.readFile(file)).digest('hex'), hash);
    return { cycles: 60, requested_reads: 960, cancelled_or_bounded: cancellations, outstanding_reads: 0, watchers: 0, source_hash_preserved: true, ui_switching: 'covered separately by native viewer qualification' };
  });

  await inspect('SIGKILL restart cleanup of owned reading indexes and preview cache', async () => {
    const fixture = path.join(root, 'crash'); await fs.mkdir(fixture);
    const csv = path.join(fixture, 'source.csv'); await fs.writeFile(csv, 'a,b\n1,2');
    const ownerFile = path.join(fixture, 'owner.mjs'), ready = path.join(fixture, 'ready.json');
    await fs.writeFile(ownerFile, `import fs from 'node:fs/promises';import {LocalFilesService} from ${JSON.stringify(moduleURL('local-files-service.mjs'))};import {LocalPreviewBroker} from ${JSON.stringify(moduleURL('local-preview-broker.mjs'))};const service=new LocalFilesService({directory:${JSON.stringify(path.join(fixture, 'reading'))},getIdentity:()=>(${JSON.stringify(identity)}),roots:[${JSON.stringify(fixture)}]});const broker=new LocalPreviewBroker({directory:${JSON.stringify(path.join(fixture, 'previews'))},getScope:()=>service.getScope(),stat:args=>service.stat(args),snapshot:args=>service.snapshot(args)});service.broker=broker;const {roots}=await service.dispatch({operation:'roots'});await service.dispatch({operation:'list',ref:roots[0].ref});const file=await service.dispatch({operation:'resolve',locator:${JSON.stringify(csv)}});await broker.prepare({ref:file.ref,version:file.version,kind:'csv',readerId:'crashed-reader'});await fs.writeFile(${JSON.stringify(ready)},JSON.stringify({reading:service.runDirectory,preview:broker.sessionDirectory}));setInterval(()=>{},1000);`);
    const owner = spawn(process.execPath, [ownerFile], { env: { ...process.env, ELECTRON_RUN_AS_NODE: '1' }, stdio: 'ignore' });
    let reading, broker;
    try {
      await until(async () => { try { await fs.access(ready); return true; } catch { return false; } });
      const old = JSON.parse(await fs.readFile(ready)); const exit = new Promise(resolve => owner.once('exit', resolve)); owner.kill('SIGKILL'); await exit;
      const protectedDirectory = path.join(fixture, 'previews', `owner-${process.pid}-aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa`);
      await fs.mkdir(protectedDirectory); await fs.writeFile(path.join(protectedDirectory, 'owner.json'), JSON.stringify({ feature: 'sparkx-local-preview-v1', pid: process.pid }));
      const decoy = path.join(fixture, 'previews', 'unowned'); await fs.mkdir(decoy); await fs.writeFile(path.join(decoy, 'keep'), 'unowned');
      reading = new LocalFilesService({ directory: path.join(fixture, 'reading'), getIdentity: () => identity, roots: [fixture] });
      broker = new LocalPreviewBroker({ directory: path.join(fixture, 'previews'), getScope: () => reading.getScope(), stat: args => reading.stat(args), snapshot: args => reading.snapshot(args) }); reading.broker = broker;
      await broker.ready; const { roots } = await reading.dispatch({ operation: 'roots' }); await reading.dispatch({ operation: 'list', ref: roots[0].ref });
      await assert.rejects(fs.stat(old.reading), { code: 'ENOENT' }); await assert.rejects(fs.stat(old.preview), { code: 'ENOENT' });
      await fs.access(protectedDirectory); assert.equal(await fs.readFile(path.join(decoy, 'keep'), 'utf8'), 'unowned');
      assert.equal(await fs.readFile(csv, 'utf8'), 'a,b\n1,2');
      return { owned_abandoned_index_removed: true, owned_abandoned_cache_removed: true, live_owner_preserved: true, unowned_directory_preserved: true, source_preserved: true };
    } finally { owner.kill('SIGKILL'); await broker?.close(); await reading?.close(); }
  });

  if (process.platform === 'darwin') await inspect('Actual mounted-volume replacement and low-space admission', async () => {
    const mount = path.join(root, 'volume'); await fs.mkdir(mount);
    const images = [path.join(root, 'one.dmg'), path.join(root, 'two.dmg')];
    for (let n = 0; n < images.length; n++) await execute('/usr/bin/hdiutil', ['create', '-size', '32m', '-fs', 'HFS+', '-volname', `SparkXOwnedQA${n}`, images[n]], { timeout: 30_000 });
    const attach = async image => { await execute('/usr/bin/hdiutil', ['attach', '-nobrowse', '-owners', 'on', '-mountpoint', mount, image], { timeout: 30_000 }); mounted = mount; };
    const detach = async () => { await execute('/usr/bin/hdiutil', ['detach', mount], { timeout: 15_000 }); mounted = null; };
    try {
      await attach(images[0]); await fs.writeFile(path.join(mount, 'linked.txt'), 'volume one');
      const link = path.join(source, 'volume-link'); await fs.symlink(path.join(mount, 'linked.txt'), link);
      const metadata = await service.dispatch({ operation: 'followLink', ref: (await resolve(link)).ref });
      assert.equal(Buffer.from((await read(metadata)).bytes).toString(), 'volume one');
      await detach(); await rejected(read(metadata), 'unavailable', 'detached volume must invalidate old reference');
      await attach(images[1]); await fs.writeFile(path.join(mount, 'linked.txt'), 'volume two');
      await rejected(read(metadata), 'source_changed', 'replacement volume must invalidate old reference');
      const reopened = await service.dispatch({ operation: 'followLink', ref: (await resolve(link)).ref });
      assert.equal(Buffer.from((await read(reopened)).bytes).toString(), 'volume two');
      let snapshots = 0;
      const broker = new LocalPreviewBroker({ directory: path.join(mount, 'preview'), getScope: () => 'low-space-fixture', stat: async () => ({ ref: 'file', version: 'version', name: 'fixture.csv', size: 4, kind: 'file' }), snapshot: async () => { snapshots++; return { bytes: Buffer.from('a,b\n') }; } });
      try { await assert.rejects(broker.prepare({ ref: 'file', version: 'version', kind: 'csv', readerId: 'low-space' }), error => error.code === 'limit_exceeded' && error.message.includes('disk space')); await until(() => !broker.active); assert.equal(snapshots, 0); assert.equal(broker.reservedInput, 0); }
      finally { await broker.close(); }
      const free = await fs.statfs(mount);
      // Fill only this owned 32 MiB filesystem, never the host's filesystem.
      assert.ok(free.blocks * free.bsize < 64 * 1024 * 1024);
      assert.notEqual((await fs.stat(mount)).dev, (await fs.stat(root)).dev);
      const pages = new LocalFilesPages(path.join(mount, 'pages'), 1024 * 1024);
      await fs.mkdir(pages.directory); const fillerPath = path.join(mount, 'owned-filler');
      const filler = await fs.open(fillerPath, 'wx'); let filled = false, written = 0;
      try {
        const chunk = Buffer.alloc(64 * 1024, 1);
        while (written < 64 * 1024 * 1024) { try { const result = await filler.write(chunk); written += result.bytesWritten; } catch (error) { if (error.code !== 'ENOSPC') throw error; filled = true; break; } }
        assert.equal(filled, true, 'owned image did not reach ENOSPC');
      } finally { await filler.close(); }
      try { await rejected(pages.open(), 'storage_limit', 'full disk must report listing storage limit'); assert.ok(!pages.db, 'failed listing open retained a SQLite handle'); }
      finally { pages.close(); await fs.rm(fillerPath, { force: true }); }
      await pages.open(); pages.close();
      return { media: 'two owned HFS+ disk images; actual kernel mounts', physical_hotplug: 'not_exercised', old_reference_rejected_after_detach_and_replacement: true, free_bytes_before_fill: free.bavail * free.bsize, default_512MiB_reserve_blocks_before_snapshot: true, real_ENOSPC: true, listing_recovered_after_space_released: true, filler_bytes: written };
    } finally { if (mounted) await detach(); }
  });
  else results.push({ name: 'Mounted-volume replacement and low-space admission', status: 'not_exercised', reason: 'This runner uses owned Darwin disk images; qualify Linux mounts separately.' });
} finally {
  await service.close();
  if (mounted) await execute('/usr/bin/hdiutil', ['detach', mounted], { timeout: 15_000 });
  await fs.rm(root, { recursive: true, force: true });
}
console.log(JSON.stringify({ platform: process.platform, node: process.version, passed: results.every(item => item.status !== 'failed'), cases: results,
  remaining_environment_conditions: ['A real File Provider-owned UF_DATALESS object and its provider hydration lifecycle were not created by this isolated runner.', 'Disk-image detach/replacement exercises real mount identity; physical cable/device failure is a separate hardware condition.'],
  original_user_files_accessed: false, original_user_permissions_modified: false, host_disk_filled: false }));
if (results.some(item => item.status === 'failed')) process.exitCode = 1;
