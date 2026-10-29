import crypto from 'node:crypto';
import path from 'node:path';
import { mkdir, mkdtemp, readFile, writeFile, rename, rm, lstat, open, opendir, readdir, statfs, realpath } from 'node:fs/promises';
import { previewError, previewLimits } from './local-preview-limits.mjs';
import { sandboxCommand, systemFontDirectories, verifyPreviewSandbox, PREVIEW_ENGINE_VERSION } from './local-preview-sandbox.mjs';
import { runPreviewProcess, treeDiskBytes } from './local-preview-process.mjs';

const digest = value => crypto.createHash('sha256').update(value).digest('hex');
const id = () => crypto.randomUUID();
const validID = value => typeof value === 'string' && /^[a-zA-Z0-9_-]{1,128}$/.test(value);
const error = (code, message) => previewError(code, message);

async function beforeDeadline(promise, deadline, signal) {
  let timer, abort;
  try {
    return await Promise.race([promise, new Promise((_, reject) => {
      timer = setTimeout(() => reject(error('timeout', 'The local preview timed out.')), Math.max(1, deadline - Date.now()));
      abort = () => reject(error('cancelled', 'Preview cancelled.'));
      if (signal?.aborted) abort(); else signal?.addEventListener('abort', abort, { once: true });
    })]);
  } finally { clearTimeout(timer); signal?.removeEventListener('abort', abort); }
}

async function fontEnvironment(roots) {
  const records = [], pending = [...roots];
  while (pending.length) {
    const directory = pending.pop();
    let stream;
    try { stream = await opendir(directory); } catch (failure) { if (['ENOENT', 'EACCES', 'EPERM'].includes(failure.code)) continue; throw failure; }
    for await (const entry of stream) {
      if (records.length >= 8192) throw error('limit_exceeded', 'The installed font directory exceeds the local preview budget.');
      const fullPath = path.join(directory, entry.name), value = await lstat(fullPath);
      records.push([fullPath, value.dev, value.ino, value.size, value.mtimeMs, value.ctimeMs]);
      if (value.isDirectory()) pending.push(fullPath);
    }
  }
  records.sort((a, b) => a[0].localeCompare(b[0]));
  return digest(JSON.stringify(records));
}

export class LocalPreviewBroker {
  constructor({ directory, snapshot, stat, getScope, workerRuntime, limits, onObservation = () => {},
    // Only test/programmatic construction can inject a runner. No renderer field
    // can choose an executable, sandbox profile, directory, or resource limits.
    launch = sandboxCommand, runProcess = runPreviewProcess, verifyIsolation = verifyPreviewSandbox, remove = rm }) {
    if (!path.isAbsolute(directory) || typeof snapshot !== 'function' || typeof stat !== 'function' || typeof getScope !== 'function') throw new TypeError('Invalid preview broker dependencies');
    Object.assign(this, { directory, snapshot, stat, getScope, workerRuntime, onObservation, launch, runProcess, verifyIsolation, remove });
    this.limits = previewLimits(limits);
    this.tasks = new Map(); this.readers = new Map(); this.cache = new Map(); this.pendingCleanup = new Set();
    this.reservedInput = 0; this.retainedBytes = 0; this.cacheBytes = 0; this.active = null; this.closed = false;
    this.scope = null; this.generation = 0; this.ready = this.initialize();
  }

  async initialize() {
    await mkdir(this.directory, { recursive: true, mode: 0o700 });
    this.directory = await realpath(this.directory);
    // Only remove directories carrying this feature's ownership proof and a dead
    // owner. PID reuse conservatively retains data until an explicit clear.
    for (const name of await readdir(this.directory)) {
      if (!/^owner-\d+-[a-f0-9-]+$/.test(name)) continue;
      const full = path.join(this.directory, name);
      let marker;
      try { marker = JSON.parse(await readFile(path.join(full, 'owner.json'), 'utf8')); } catch { continue; }
      if (marker.feature !== 'sparkx-local-preview-v1' || !Number.isSafeInteger(marker.pid) || marker.pid < 1) continue;
      let alive = true;
      try { process.kill(marker.pid, 0); } catch (e) { if (e.code === 'ESRCH') alive = false; }
      if (!alive) { try { await this.remove(full, { recursive: true, force: true }); } catch { this.pendingCleanup.add(full); } }
    }
    this.sessionDirectory = path.join(this.directory, `owner-${process.pid}-${id()}`);
    await mkdir(this.sessionDirectory, { mode: 0o700 });
    await writeFile(path.join(this.sessionDirectory, 'owner.json'), JSON.stringify({ feature: 'sparkx-local-preview-v1', pid: process.pid }), { mode: 0o600, flag: 'wx' });
    this.fontDirectories = systemFontDirectories();
  }

  refreshIdentity() {
    const scope = this.getScope();
    if (typeof scope !== 'string' || !scope) {
      if (this.scope !== null) this.invalidate(error('locked', 'Sign in to read local files.'));
      this.scope = null; return null;
    }
    if (this.scope !== scope) { this.invalidate(error('locked', 'The desktop identity changed.')); this.scope = scope; }
    return scope;
  }

  invalidate(reason) {
    ++this.generation;
    for (const reader of [...this.readers.values()]) this.dropReader(reader, reason);
    for (const task of this.tasks.values()) task.controller.abort(reason);
    for (const entry of this.cache.values()) void this.removeCache(entry);
  }

  assertScope(scope, generation) {
    if (this.closed || !this.refreshIdentity() || this.scope !== scope || this.generation !== generation) throw error('locked', 'The local reading session is no longer available.');
  }

  async prepare({ ref, version, kind, readerId, signal }, deadline = Date.now() + this.limits.deadlineMs) {
    await beforeDeadline(this.ready, deadline, signal);
    const scope = this.refreshIdentity(), generation = this.generation;
    if (this.closed || !scope) throw error('locked', 'Sign in to read local files.');
    if (!validID(readerId) || typeof ref !== 'string' || typeof version !== 'string' || !['office', 'xlsx', 'csv', 'tsv'].includes(kind)) throw error('invalid_request', 'Invalid document preview request.');
    if (signal?.aborted) throw error('cancelled', 'Preview cancelled.');
    if (this.pendingCleanup.size) throw error('cleanup_pending', 'Clear local preview caches to retry unfinished cleanup.');
    if (this.readers.has(readerId)) throw error('invalid_request', 'The preview reader already exists.');
    if (this.readers.size >= this.limits.readers) throw error('limit_exceeded', 'Close a document preview before opening another.');
    const metadata = await beforeDeadline(this.stat({ ref }), deadline, signal);
    this.assertScope(scope, generation);
    if (metadata.version !== version) throw error('source_changed', 'The source file changed. Reopen it to preview the new version.');
    if (metadata.kind !== 'file' || !Number.isSafeInteger(metadata.size) || metadata.size < 0 || metadata.size > this.limits.inputBytes) throw error('limit_exceeded', 'This file cannot fit in the local preview budget.');
    // Do not coalesce different locators merely because a basename matches.
    const taskKey = digest(JSON.stringify([scope, generation, metadata.locator ?? ref, version, kind]));
    let task = this.tasks.get(taskKey);
    if (task?.finished) { await beforeDeadline(task.completed, deadline, signal); return this.prepare({ ref, version, kind, readerId, signal }, deadline); }
    if (!task) {
      if (this.tasks.size >= this.limits.queuedJobs || this.reservedInput + metadata.size > this.limits.aggregateInputBytes) throw error('limit_exceeded', 'The local preview queue is full. Close a preview and retry.');
      this.reservedInput += metadata.size;
      task = { key: taskKey, scope, generation, ref, version, kind, metadata, readers: new Set(), controller: new AbortController(),
        reserved: metadata.size, deadline };
      task.completed = new Promise(resolve => { task.complete = resolve; });
      this.tasks.set(taskKey, task);
    }
    const promise = new Promise((resolve, reject) => {
      const reader = { id: readerId, scope, generation, ref, version, task, resolve, reject, signal, bytes: 0 };
      reader.abort = () => this.dropReader(reader, error('cancelled', 'Preview cancelled.'));
      reader.timer = setTimeout(() => this.dropReader(reader, error('timeout', 'The local preview timed out.')), Math.max(1, deadline - Date.now()));
      signal?.addEventListener('abort', reader.abort, { once: true });
      task.readers.add(readerId); this.readers.set(readerId, reader);
    });
    void this.pump();
    return promise;
  }

  async pump() {
    if (this.active || this.closed || this.pendingCleanup.size) return;
    const task = [...this.tasks.values()].find(value => !value.started);
    if (!task) return;
    task.started = true; this.active = task;
    const timer = setTimeout(() => task.controller.abort(error('timeout', 'The local preview timed out.')), Math.max(1, task.deadline - Date.now()));
    try {
      if (!task.readers.size || task.controller.signal.aborted) throw task.controller.signal.reason ?? error('cancelled', 'Preview cancelled.');
      const entry = await this.execute(task);
      const current = await this.stat({ ref: task.ref });
      if (current.version !== task.version) throw error('source_changed', 'The source file changed during preview.');
      this.assertScope(task.scope, task.generation);
      while (task.readers.size) {
        const readerId = task.readers.values().next().value;
        const reader = this.readers.get(readerId);
        if (!reader) { task.readers.delete(readerId); continue; }
        try {
          const model = entry.kind === 'spreadsheet' ? JSON.parse(await readFile(entry.file, 'utf8')) : undefined;
          if (!this.readers.has(readerId)) continue;
          this.assertScope(task.scope, task.generation);
          if (model && this.retainedBytes + entry.size > this.limits.cacheBytes) throw error('limit_exceeded', 'Retained table views exceed the preview memory budget.');
          clearTimeout(reader.timer);
          reader.entry = entry; reader.task = null; reader.bytes = model ? entry.size : 0;
          this.retainedBytes += reader.bytes; entry.readers.add(readerId); task.readers.delete(readerId);
          reader.resolve({ previewId: entry.id, kind: entry.kind, size: entry.size, warnings: entry.warnings, ...(model ? { model } : {}), ...(entry.pageCount ? { pageCount: entry.pageCount } : {}) });
        } catch (failure) { this.dropReader(reader, failure); }
      }
    } catch (failure) {
      for (const readerId of [...task.readers]) { const reader = this.readers.get(readerId); if (reader) this.dropReader(reader, failure); }
    } finally {
      task.finished = true;
      clearTimeout(timer);
      if (task.directory && !task.processCleanupPending) {
        try {
          const cleanupStartedAt = Date.now();
          await this.remove(task.directory, { recursive: true, force: true });
          this.onObservation({ phase: 'cleanup', directoryCleanupMs: Date.now() - cleanupStartedAt, released: true });
        }
        catch { this.pendingCleanup.add(task.directory); }
      }
      // A failed process join/cleanup retains the slot and input reservation.
      if (task.processCleanupPending || this.pendingCleanup.has(task.directory)) {
        task.cleanupPending = true; this.pendingCleanup.add(task.directory ?? task.key);
      } else {
        this.reservedInput -= task.reserved; this.tasks.delete(task.key); this.active = null;
        void this.pump();
      }
      task.complete();
    }
  }

  async execute(task) {
    this.assertScope(task.scope, task.generation);
    const capacity = await statfs(this.directory);
    if (capacity.bavail * capacity.bsize < this.limits.diskHeadroomBytes + task.reserved * 2) throw error('limit_exceeded', 'Free disk space is too low for a local preview.');
    await treeDiskBytes(this.directory, this.limits.diskBytes);
    let snapshot = await this.snapshot({ ref: task.ref, version: task.version, maxBytes: this.limits.inputBytes, signal: task.controller.signal });
    this.assertScope(task.scope, task.generation);
    if (task.controller.signal.aborted) throw task.controller.signal.reason;
    if (snapshot.bytes.length !== task.reserved) throw error('source_changed', 'The source file changed while opening its preview.');
    // Hash original snapshot bytes ourselves; metadata cannot prove cache reuse.
    const hash = digest(snapshot.bytes);
    const fontGeneration = task.kind === 'office' ? await fontEnvironment(this.fontDirectories) : '';
    const cacheKey = digest(JSON.stringify([task.scope, hash, task.kind, PREVIEW_ENGINE_VERSION, fontGeneration, 120]));
    const cached = this.cache.get(cacheKey);
    if (cached) { cached.used = Date.now(); return cached; }
    task.directory = await mkdtemp(path.join(this.sessionDirectory, 'job-'));
    if (!this.isolationVerified) {
      const outside = path.join(this.sessionDirectory, 'isolation-canary');
      await writeFile(outside, 'local-preview-isolation-check', { mode: 0o600 });
      await this.verifyIsolation({ jobDirectory: task.directory, outsidePath: outside, runtime: this.workerRuntime });
      await this.remove(outside, { force: true });
      this.isolationVerified = true;
    }
    const suffix = task.kind === 'office' ? path.extname(task.metadata.name).toLowerCase() : `.${task.kind}`;
    if (!['.doc', '.docx', '.ppt', '.pptx', '.xlsx', '.csv', '.tsv'].includes(suffix)) throw error('unsupported', 'This document type is not supported.');
    const inputPath = path.join(task.directory, `input${suffix}`), outputPath = path.join(task.directory, task.kind === 'office' ? 'output.pdf' : 'output.json');
    await writeFile(inputPath, snapshot.bytes, { flag: 'wx', mode: 0o400 });
    // Snapshot bytes are not kept in the cache or worker IPC.
    snapshot = null;
    await writeFile(path.join(task.directory, 'request.json'), JSON.stringify({ kind: task.kind, inputPath, outputPath, limits: this.limits, fontDirectories: this.fontDirectories }), { flag: 'wx', mode: 0o600 });
    const launch = await this.launch({ jobDirectory: task.directory, runtime: this.workerRuntime, readOnlyFiles: [inputPath, path.join(task.directory, 'request.json')] });
    let processResult;
    try {
      processResult = await this.runProcess({ launch, jobDirectory: task.directory, rootDirectory: this.directory, limits: { ...this.limits, deadlineMs: Math.max(1, task.deadline - Date.now()) }, signal: task.controller.signal, onObservation: this.onObservation });
    } catch (failure) { if (failure.code === 'cleanup_pending') task.processCleanupPending = true; throw failure; }
    this.assertScope(task.scope, task.generation);
    if (task.controller.signal.aborted) throw task.controller.signal.reason;
    const resultPath = path.join(task.directory, 'result.json');
    const resultMetadata = await lstat(resultPath);
    if (!resultMetadata.isFile() || resultMetadata.size > 8192) throw error('preview_failed', 'Invalid preview worker result.');
    const result = JSON.parse(await readFile(resultPath, 'utf8'));
    if (processResult.code !== 0 || !result.ok) throw error(result.error?.code ?? 'preview_failed', result.error?.message ?? 'The local document preview failed.');
    if (!['pdf', 'spreadsheet'].includes(result.kind) || !Array.isArray(result.warnings)) throw error('preview_failed', 'Invalid preview worker output.');
    const output = await lstat(outputPath);
    if (!output.isFile() || output.size < 1 || output.size > (result.kind === 'pdf' ? this.limits.outputBytes : this.limits.modelBytes)) throw error('limit_exceeded', 'The generated preview exceeds its output limit.');
    if (result.kind === 'pdf') {
      const file = await open(outputPath, 'r');
      try { const header = Buffer.alloc(8); await file.read(header, 0, 8, 0); if (!header.toString().startsWith('%PDF-')) throw error('invalid_document', 'The Office converter did not produce a PDF.'); }
      finally { await file.close(); }
    }
    await this.evict(output.size);
    const file = path.join(this.sessionDirectory, `cache-${cacheKey}`);
    await rename(outputPath, file);
    const entry = { id: id(), key: cacheKey, file, scope: task.scope, generation: task.generation, kind: result.kind,
      size: output.size, warnings: result.warnings, pageCount: result.pageCount, readers: new Set(), used: Date.now() };
    this.cache.set(cacheKey, entry); this.cacheBytes += entry.size;
    return entry;
  }

  async read({ previewId, readerId, offset, length }) {
    const reader = this.readers.get(readerId);
    if (!reader?.entry || reader.entry.id !== previewId || reader.entry.kind !== 'pdf') throw error('invalid_reference', 'The preview is no longer available.');
    this.assertScope(reader.scope, reader.generation);
    if (!Number.isSafeInteger(offset) || offset < 0 || !Number.isSafeInteger(length) || length < 1 || length > this.limits.readBytes || offset + length > reader.entry.size) throw error('invalid_range', 'Invalid PDF read range.');
    const metadata = await this.stat({ ref: reader.ref });
    if (metadata.version !== reader.version) { this.dropReader(reader, error('source_changed', 'The source file changed.')); throw error('source_changed', 'The source file changed.'); }
    const file = await open(reader.entry.file, 'r');
    try {
      const bytes = Buffer.alloc(length), { bytesRead } = await file.read(bytes, 0, length, offset);
      this.assertScope(reader.scope, reader.generation);
      if (!this.readers.has(readerId)) throw error('cancelled', 'The preview was closed.');
      if ((await this.stat({ ref: reader.ref })).version !== reader.version) throw error('source_changed', 'The source file changed during reading.');
      this.assertScope(reader.scope, reader.generation);
      if (bytesRead !== length) throw error('preview_failed', 'The preview cache is incomplete.');
      return { bytes: new Uint8Array(bytes), offset };
    } finally { await file.close(); }
  }

  release({ readerId }) { const reader = this.readers.get(readerId); if (reader) this.dropReader(reader, error('cancelled', 'Preview closed.')); return { released: true }; }
  cancel(request) { return this.release(request); }
  dropReader(reader, reason) {
    if (!this.readers.delete(reader.id)) return;
    clearTimeout(reader.timer); reader.signal?.removeEventListener('abort', reader.abort);
    this.retainedBytes -= reader.bytes;
    if (reader.entry) reader.entry.readers.delete(reader.id);
    if (reader.task) {
      reader.task.readers.delete(reader.id);
      if (!reader.task.readers.size) {
        reader.task.controller.abort(reason);
        if (!reader.task.started && this.tasks.delete(reader.task.key)) this.reservedInput -= reader.task.reserved;
      }
    }
    reader.reject(reason);
  }

  async evict(incoming = 0) {
    for (const entry of [...this.cache.values()].sort((a, b) => a.used - b.used)) {
      if (this.cacheBytes + incoming <= this.limits.cacheBytes && this.cache.size < this.limits.cacheEntries) break;
      if (!entry.readers.size) await this.removeCache(entry);
    }
    if (this.cacheBytes + incoming > this.limits.cacheBytes || this.cache.size >= this.limits.cacheEntries) throw error('limit_exceeded', 'Close a document to release preview cache capacity.');
  }
  async removeCache(entry) {
    if (entry.removing) return entry.removing;
    entry.removing = (async () => {
      try {
        await this.remove(entry.file, { force: true });
        if (this.cache.delete(entry.key)) this.cacheBytes -= entry.size;
        this.pendingCleanup.delete(entry.file);
      } catch { this.pendingCleanup.add(entry.file); }
      finally { entry.removing = null; }
    })();
    return entry.removing;
  }

  async clear() {
    await this.ready;
    this.invalidate(error('cancelled', 'Local previews were cleared.'));
    // pump keeps active capacity until worker exit. Wait only within the fixed
    // cleanup bound; unresolved process joins remain blocked for a later retry.
    const until = Date.now() + this.limits.cleanupMs + this.limits.killGraceMs + 1000;
    while (this.active && !this.active.cleanupPending && Date.now() < until) await new Promise(resolve => setTimeout(resolve, 20));
    if (this.active?.processCleanupPending) throw error('cleanup_pending', 'Preview process cleanup is still pending. Restart SparkX before clearing these files.');
    if (this.active?.cleanupPending) {
      const task = this.active;
      try {
        await this.remove(task.directory, { recursive: true, force: true });
        this.pendingCleanup.delete(task.directory); this.reservedInput -= task.reserved;
        this.tasks.delete(task.key); this.active = null;
      } catch {}
    }
    for (const entry of [...this.cache.values()]) await this.removeCache(entry);
    for (const directory of [...this.pendingCleanup]) {
      if (!path.isAbsolute(directory) || !directory.startsWith(this.directory + path.sep)) continue;
      try { await this.remove(directory, { recursive: true, force: true }); this.pendingCleanup.delete(directory); } catch {}
    }
    if (this.pendingCleanup.size || this.active) throw error('cleanup_pending', 'Some local preview files could not be cleared. Retry after the files are released.');
    return { cleared: true };
  }

  async close() {
    this.closed = true;
    try { await this.clear(); } finally {
      if (!this.active && !this.pendingCleanup.size && this.sessionDirectory) await this.remove(this.sessionDirectory, { recursive: true, force: true });
    }
  }
}
