import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import crypto from 'node:crypto';
import JSZip from 'jszip';
import { LocalPreviewBroker } from '../src/main/local-preview-broker.mjs';

const wait = ms => new Promise(resolve => setTimeout(resolve, ms));
async function until(check) { for (let n = 0; n < 200; n++) { if (check()) return; await wait(5); } throw new Error('condition timeout'); }
async function harness(t, options = {}) {
  const directory = await fs.mkdtemp(path.join(os.tmpdir(), 'sparkx-broker-'));
  let scope = 'identity', snapshots = 0, calls = 0, source = Buffer.from('a,b\n0012,=1+1');
  const metadata = { ref: 'ref', version: 'version', kind: 'file', name: 'data.csv', size: source.length };
  const runProcess = options.runProcess ?? (async ({ jobDirectory, signal }) => {
    ++calls; if (signal.aborted) throw signal.reason;
    await fs.writeFile(path.join(jobDirectory, 'output.json'), JSON.stringify({ kind: 'spreadsheet', sheets: [], warnings: [] }));
    await fs.writeFile(path.join(jobDirectory, 'result.json'), JSON.stringify({ ok: true, kind: 'spreadsheet', warnings: [] }));
    return { code: 0 };
  });
  const broker = new LocalPreviewBroker({ directory, getScope: () => scope, stat: async () => metadata,
    snapshot: async () => { ++snapshots; return { bytes: Buffer.from(source) }; },
    launch: async () => ({}), verifyIsolation: async () => ({}), runProcess, ...options });
  t.after(async () => { await broker.close().catch(() => {}); await fs.rm(directory, { recursive: true, force: true }); });
  return { broker, metadata, directory, scope: value => { scope = value; }, source: value => { source = value; }, snapshots: () => snapshots, calls: () => calls };
}
const request = readerId => ({ ref: 'ref', version: 'version', kind: 'csv', readerId });

test('admission reserves before snapshot and cache reuse rehashes original bytes', async t => {
  const h = await harness(t);
  const first = await h.broker.prepare(request('first'));
  h.broker.release({ readerId: 'first' });
  await until(() => !h.broker.active);
  const second = await h.broker.prepare(request('second'));
  assert.equal(first.previewId, second.previewId); assert.equal(h.calls(), 1); assert.equal(h.snapshots(), 2);
  h.broker.release({ readerId: 'second' }); await until(() => !h.broker.active);
  h.source(Buffer.from('a,b\n0012,=2+1'));
  const third = await h.broker.prepare(request('third'));
  assert.notEqual(third.previewId, first.previewId); assert.equal(h.calls(), 2);
  h.metadata.size = 100;
  await assert.rejects(h.broker.prepare({ ...request('oversized'), version: 'version' }), { code: 'source_changed' });
});

test('each shared reader cancels independently and last departure aborts process', async t => {
  let started = 0, finish;
  const h = await harness(t, { runProcess: async ({ jobDirectory, signal }) => {
    started++;
    await new Promise((resolve, reject) => { finish = resolve; signal.addEventListener('abort', () => reject(signal.reason), { once: true }); });
    await fs.writeFile(path.join(jobDirectory, 'output.json'), JSON.stringify({ kind: 'spreadsheet', sheets: [], warnings: [] }));
    await fs.writeFile(path.join(jobDirectory, 'result.json'), JSON.stringify({ ok: true, kind: 'spreadsheet', warnings: [] }));
    return { code: 0 };
  } });
  const first = h.broker.prepare(request('first')), firstRejected = assert.rejects(first, { code: 'cancelled' });
  const second = h.broker.prepare(request('second'));
  await until(() => started === 1 && h.broker.readers.size === 2);
  h.broker.cancel({ readerId: 'first' }); await firstRejected;
  assert.equal(h.broker.active.controller.signal.aborted, false);
  finish(); assert.equal((await second).kind, 'spreadsheet');
  assert.equal(started, 1);
});

test('queued deadline includes queue time and cannot start after readers leave', async t => {
  const h = await harness(t, { limits: { deadlineMs: 50 }, runProcess: async ({ signal }) => new Promise((_, reject) => signal.addEventListener('abort', () => reject(signal.reason), { once: true })) });
  const first = h.broker.prepare(request('first')); const rejected1 = assert.rejects(first, { code: 'timeout' });
  const second = h.broker.prepare({ ...request('second'), ref: 'other-ref' }); const rejected2 = assert.rejects(second, { code: 'timeout' });
  await Promise.all([rejected1, rejected2]);
  await until(() => !h.broker.active);
  assert.equal(h.broker.reservedInput, 0); assert.equal(h.broker.tasks.size, 0);
});

test('admission metadata time is included in the total preview deadline', async t => {
  const h = await harness(t, { limits: { deadlineMs: 25 }, stat: () => new Promise(() => {}) });
  await assert.rejects(h.broker.prepare(request('slow-metadata')), { code: 'timeout' });
  assert.equal(h.broker.reservedInput, 0); assert.equal(h.snapshots(), 0);
});

test('logout invalidates retained models/cache IDs and rejects new preparation', async t => {
  const h = await harness(t); await h.broker.prepare(request('first'));
  h.scope(null); h.broker.refreshIdentity();
  assert.equal(h.broker.readers.size, 0); assert.equal(h.broker.retainedBytes, 0);
  await assert.rejects(h.broker.prepare(request('second')), { code: 'locked' });
  h.scope('identity'); await until(() => !h.broker.active);
  assert.equal((await h.broker.prepare(request('second'))).kind, 'spreadsheet');
});

test('cleanup failure retains execution capacity and explicit retry releases it', async t => {
  let fail = true;
  const h = await harness(t, { remove: async (target, options) => {
    if (fail && path.basename(target).startsWith('job-')) throw Object.assign(new Error('busy'), { code: 'EBUSY' });
    return fs.rm(target, options);
  } });
  await h.broker.prepare(request('first')); await until(() => h.broker.active?.cleanupPending);
  assert.ok(h.broker.reservedInput > 0); assert.ok(h.broker.pendingCleanup.size > 0);
  await assert.rejects(h.broker.prepare(request('second')), { code: 'cleanup_pending' });
  fail = false; await h.broker.clear();
  assert.equal(h.broker.reservedInput, 0); assert.equal(h.broker.pendingCleanup.size, 0); assert.equal(h.broker.active, null);
});

test('changed source during worker execution never becomes a ready preview', async t => {
  let h;
  h = await harness(t, { runProcess: async ({ jobDirectory }) => {
    h.metadata.version = 'changed';
    await fs.writeFile(path.join(jobDirectory, 'output.json'), JSON.stringify({ kind: 'spreadsheet', sheets: [], warnings: [] }));
    await fs.writeFile(path.join(jobDirectory, 'result.json'), JSON.stringify({ ok: true, kind: 'spreadsheet', warnings: [] }));
    return { code: 0 };
  } });
  await assert.rejects(h.broker.prepare(request('first')), { code: 'source_changed' });
  assert.equal(h.broker.readers.size, 0);
});

test('a worker-created symlink is not admitted as a readable output', async t => {
  const h = await harness(t, { runProcess: async ({ jobDirectory }) => {
    await fs.symlink(path.join(jobDirectory, 'request.json'), path.join(jobDirectory, 'output.json'));
    await fs.writeFile(path.join(jobDirectory, 'result.json'), JSON.stringify({ ok: true, kind: 'spreadsheet', warnings: [] }));
    return { code: 0 };
  } });
  await assert.rejects(h.broker.prepare(request('first')), { code: 'limit_exceeded' });
});

async function documentFixture() {
  const zip = new JSZip();
  zip.file('[Content_Types].xml', '<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>');
  zip.file('_rels/.rels', '<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>');
  zip.file('word/document.xml', '<?xml version="1.0"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>SparkX local preview 中文</w:t></w:r></w:p><w:sectPr><w:pgSz w:w="12240" w:h="15840"/><w:pgMar w:top="1440" w:right="1440" w:bottom="1440" w:left="1440"/></w:sectPr></w:body></w:document>');
  return zip.generateAsync({ type: 'nodebuffer' });
}

test('real isolated bundled Office conversion preserves source and returns bounded PDF bytes', { skip: process.platform !== 'darwin', timeout: 100_000 }, async t => {
  const bytes = await documentFixture(), hash = crypto.createHash('sha256').update(bytes).digest('hex');
  const directory = await fs.mkdtemp(path.join(os.tmpdir(), 'sparkx-office-qualified-'));
  const metadata = { ref: 'ref', version: 'version', name: 'fixture.docx', size: bytes.length, kind: 'file' };
  const observations = [];
  const broker = new LocalPreviewBroker({ directory, getScope: () => 'fixture-login', stat: async () => metadata, snapshot: async () => ({ bytes: Buffer.from(bytes) }), onObservation: value => observations.push(value) });
  t.after(async () => { await broker.close(); await fs.rm(directory, { recursive: true, force: true }); });
  const preview = await broker.prepare({ ...request('office'), kind: 'office' });
  assert.equal(preview.kind, 'pdf'); assert.equal(preview.pageCount, 1);
  const read = await broker.read({ previewId: preview.previewId, readerId: 'office', offset: 0, length: 8 });
  assert.ok(Buffer.from(read.bytes).toString().startsWith('%PDF-'));
  assert.equal(crypto.createHash('sha256').update(bytes).digest('hex'), hash);
  broker.release({ readerId: 'office' });
  await assert.rejects(broker.read({ previewId: preview.previewId, readerId: 'office', offset: 0, length: 8 }), { code: 'invalid_reference' });
  await until(() => !broker.active);
  t.diagnostic(`Synthetic DOCX observed resources: ${JSON.stringify(observations)}`);
});
