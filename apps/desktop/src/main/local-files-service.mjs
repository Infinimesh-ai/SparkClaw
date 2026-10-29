import fs from 'node:fs/promises';
import { constants, watch as watchFile } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import crypto from 'node:crypto';
import { LocalFilesPages } from './local-files-pages.mjs';
import { LocalFilesPolicy, fail, locator, sourceVersion, fileIdentity, mountIdentity, kindOf, publicError, within } from './local-files-policy.mjs';

export const LOCAL_FILES_LIMITS = Object.freeze({ pageSize: 128, scannedPerBatch: 1024, entries: 32768, listings: 24, refs: 4096, watches: 64, reads: 8, readBytes: 1024 * 1024, imageBytes: 4 * 1024 * 1024, snapshotBytes: 32 * 1024 * 1024, tempBytes: 32 * 1024 * 1024, stateBytes: 64 * 1024, requestMs: 30000 });
const token = () => crypto.randomUUID();

export class LocalFilesService {
  constructor({ directory, getIdentity, onChange = () => {}, policy = new LocalFilesPolicy(), limits = {}, roots, broker = null }) {
    Object.assign(this, { directory, getIdentity, onChange, policy, broker });
    this.limits = { ...LOCAL_FILES_LIMITS, ...limits }; this.rootLocators = roots;
    this.refs = new Map(); this.listings = new Map(); this.watches = new Map(); this.requests = new Map(); this.dirty = new Set();
    this.generation = token(); this.scope = null; this.visible = true; this.closed = false; this.reads = 0; this.activeRequests = 0;
    this.cleanup = Promise.resolve(); this.pendingSetup = null; this.pendingCleanup = [];
  }

  refreshIdentity() {
    const identity = this.closed ? null : this.getIdentity();
    const next = identity && ['deployment_id', 'owner_id', 'client_id'].every(key => typeof identity[key] === 'string' && identity[key]) ? crypto.createHash('sha256').update(JSON.stringify([identity.deployment_id, identity.owner_id, identity.client_id, identity.authorization_revision ?? null])).digest('hex') : null;
    if (next !== this.scope) {
      this.scope = next; this.generation = token();
      for (const request of this.requests.values()) request.abort();
      for (const watcher of this.watches.values()) watcher.close();
      this.watches.clear(); this.dirty.clear(); this.refs.clear();
      const oldLists = [...this.listings.values()]; this.listings.clear();
      const pages = this.pages, run = this.runDirectory; this.pages = null; this.runDirectory = null;
      this.pendingCleanup.push({ lists: oldLists, pages, run });
      this.cleanup = this.cleanup.then(() => this.#cleanPending()).catch(() => { this.cleanupFailed = true; });
      this.broker?.refreshIdentity?.();
      this.onChange({ kind: 'revoked', generation: this.generation });
    }
    return this.scope;
  }
  async #cleanPending() {
    while (this.pendingCleanup.length) {
      const record = this.pendingCleanup[0];
      await Promise.all(record.lists.map(item => item.dir?.close().catch(error => { if (error.code !== 'ERR_DIR_CLOSED') throw error; })));
      record.pages?.close(); record.pages = null;
      if (record.run) await fs.rm(record.run, { recursive: true, force: true });
      this.pendingCleanup.shift();
    }
    this.cleanupFailed = false;
  }
  #auth(generation) {
    if (!this.refreshIdentity()) fail('locked', 'Sign in to browse local files');
    if (generation && generation !== this.generation) fail('revoked', 'The local reading session changed');
    return this.generation;
  }
  async #storage(generation) {
    await this.cleanup; this.#auth(generation);
    if (this.cleanupFailed) fail('cleanup_pending', 'Local reading cleanup needs a retry');
    if (this.pages) return;
    if (!this.pendingSetup) {
      const scope = this.scope;
      this.pendingSetup = (async () => {
        const root = path.join(this.directory, scope); await fs.mkdir(root, { recursive: true, mode: 0o700 }); await fs.chmod(root, 0o700);
        await cleanAbandonedRuns(root);
        const run = await fs.mkdtemp(path.join(root, 'reading-')); let pages;
        try { await fs.writeFile(path.join(run, 'owner.json'), JSON.stringify({ pid: process.pid }), { mode: 0o600, flag: 'wx' }); pages = await new LocalFilesPages(run, this.limits.tempBytes).open(); this.#auth(generation); this.runDirectory = run; this.pages = pages; }
        catch (error) { pages?.close(); await fs.rm(run, { recursive: true, force: true }); throw error; }
      })().finally(() => { this.pendingSetup = null; });
    }
    await this.pendingSetup; this.#auth(generation);
  }

  async dispatch(request) {
    const generation = this.#auth();
    if (!request || typeof request !== 'object' || Array.isArray(request)) fail('invalid_request');
    const operations = {
      roots: [], resolve: ['locator'], stat: ['ref'], list: ['ref', '?cursor', '?showHidden', '?filter', '?refresh', '?page'],
      readBytes: ['ref', 'version', 'offset', 'length'], readText: ['ref', 'version', 'offset', 'length'],
      relativeImage: ['ref', 'version', 'path'], followLink: ['ref', '?ancestors'], watch: ['ref'], unwatch: ['ref'], release: ['ref'],
      setVisible: ['visible'], cancel: ['request_id'], loadState: [], saveState: ['state'], clear: [],
      preview: ['ref', 'version', 'kind', 'readerId'], readPreview: ['previewId', 'readerId', 'offset', 'length'], releasePreview: ['readerId'], cancelPreview: ['readerId'],
    };
    const fields = Object.hasOwn(operations, request.operation) ? operations[request.operation] : null;
    if (!fields) fail('invalid_request', 'Unknown local files operation');
    const allowed = new Set(['operation', 'schema_version', 'request_id', ...fields.map(key => key.replace(/^\?/, ''))]);
    if (Object.keys(request).some(key => !allowed.has(key)) || fields.some(key => !key.startsWith('?') && !Object.hasOwn(request, key))) fail('invalid_request', 'Invalid local files fields');
    if (Object.hasOwn(request, 'schema_version') && request.schema_version !== 1) fail('invalid_request');
    let controller;
    if (request.operation !== 'cancel') {
      if (this.activeRequests >= 32) fail('busy', 'Too many local reading requests');
      if (request.request_id !== undefined && (typeof request.request_id !== 'string' || request.request_id.length > 100 || !request.request_id)) fail('invalid_request');
      if (request.request_id && this.requests.has(request.request_id)) fail('invalid_request', 'Request ID is already active');
      controller = new AbortController(); this.activeRequests++; if (request.request_id) this.requests.set(request.request_id, controller);
    }
    const timer = controller && setTimeout(() => controller.abort(), this.limits.requestMs); timer?.unref?.();
    try {
      if (['resolve', 'list', 'readBytes', 'readText', 'relativeImage', 'followLink', 'watch'].includes(request.operation)) await this.policy.mounts(true);
      const result = await this.#operate(request, generation, controller?.signal);
      this.#auth(request.operation === 'clear' ? undefined : generation); if (controller?.signal.aborted) fail('cancelled', 'Local read cancelled'); return result;
    } finally { clearTimeout(timer); if (controller) this.activeRequests--; if (request.operation !== 'cancel' && request.request_id) this.requests.delete(request.request_id); }
  }

  async #operate(r, generation, signal) {
    switch (r.operation) {
      case 'roots': return { roots: await this.#roots(generation), generation, limits: this.limits };
      case 'resolve': return this.#reference(locator(r.locator), false, generation);
      case 'stat': return this.#stat(r.ref, generation);
      case 'list': return this.#list(r, generation, signal);
      case 'readBytes': return this.#read(r, generation, signal);
      case 'readText': { const result = await this.#read(r, generation, signal); try { return { ...result, bytes: undefined, text: new TextDecoder('utf-8', { fatal: true }).decode(result.bytes) }; } catch { fail('encoding_unavailable', 'This window is not valid UTF-8; open a different range or format'); } }
      case 'relativeImage': return this.#relativeImage(r, generation, signal);
      case 'followLink': {
        const source = this.#ref(r.ref); if (source.metadata.kind !== 'link') fail('invalid_request');
        if (r.ancestors !== undefined && (!Array.isArray(r.ancestors) || r.ancestors.length > 128)) fail('invalid_request');
        const target = await this.policy.resolve(source.locator, true);
        for (const ancestor of r.ancestors ?? []) if (await this.policy.resolve(locator(ancestor), true) === target) fail('link_cycle', 'This link points to an ancestor directory');
        return this.#reference(source.locator, true, generation);
      }
      case 'watch': return this.#watch(r.ref, generation);
      case 'unwatch': this.#unwatch(r.ref); return { completed: true };
      case 'release': await this.#release(r.ref); return { completed: true };
      case 'setVisible':
        if (typeof r.visible !== 'boolean') fail('invalid_request');
        this.visible = r.visible;
        if (r.visible && this.dirty.size) { this.onChange({ kind: 'changed', refs: [...this.dirty], generation }); this.dirty.clear(); }
        return { completed: true };
      case 'cancel':
        if (typeof r.request_id !== 'string' || r.request_id.length > 100) fail('invalid_request');
        this.requests.get(r.request_id)?.abort(); return { completed: true };
      case 'loadState': return { state: await this.#loadState(generation), generation };
      case 'saveState': return this.#saveState(r.state, generation);
      case 'clear': return this.#clear(generation, signal);
      case 'preview':
        if (!this.broker) fail('preview_unavailable', 'Heavy previews are unavailable in this package');
        await this.#stat(r.ref, generation, r.version);
        return this.broker.prepare({ ...r, signal, generation });
      case 'readPreview':
        if (!this.broker) fail('preview_unavailable');
        return this.broker.read(r);
      case 'releasePreview': case 'cancelPreview':
        await this.broker?.[r.operation === 'cancelPreview' ? 'cancel' : 'release']({ readerId: r.readerId }); return { completed: true };
    }
  }

  #ref(value) { if (typeof value !== 'string' || !this.refs.has(value)) fail('reference_expired', 'Reopen this location'); const record = this.refs.get(value); this.refs.delete(value); this.refs.set(value, record); return record; }
  async #reference(value, follow, generation, initial) {
    this.#auth(generation);
    const real = await this.policy.resolve(value, follow), stat = initial ?? await fs.lstat(real, { bigint: true });
    this.#auth(generation);
    const mount = mountIdentity(await this.policy.guard(real));
    const version = sourceVersion(stat), metadata = { locator: value, name: path.basename(value) || '/', kind: kindOf(stat, value), size: Number(stat.size), version, displayPath: value };
    if (/\.icloud$/i.test(value) || stat.isFile() && stat.size > 0n && stat.blocks === 0n) metadata.unavailable = 'cloud_placeholder';
    for (const [ref, record] of this.refs) if (record.locator === value && record.follow === follow && record.metadata.version === version && record.mount === mount) return { ...record.metadata, ref };
    while (this.refs.size >= this.limits.refs) {
      const evict = [...this.refs.keys()].find(ref => !this.watches.has(ref) && !this.listings.has(ref));
      if (!evict) fail('resource_limit', 'Close some expanded folders or document tabs'); this.refs.delete(evict);
    }
    const ref = token(); this.refs.set(ref, { locator: value, real, follow, metadata, identity: fileIdentity(stat), mount }); return { ...metadata, ref };
  }
  async #stat(ref, generation, expected) {
    await this.policy.mounts(true);
    const record = this.#ref(ref), real = await this.policy.resolve(record.locator, record.follow), stat = await fs.lstat(real, { bigint: true });
    if (!stat.isSymbolicLink()) await fs.access(real, constants.R_OK);
    if (stat.isFile()) await this.policy.available(real, stat);
    this.#auth(generation);
    if (real !== record.real || mountIdentity(await this.policy.guard(real)) !== record.mount || fileIdentity(stat) !== record.identity || (expected && sourceVersion(stat) !== expected)) fail('source_changed', 'This file changed; reopen it to read its new version');
    const current = { ...record.metadata, ref, size: Number(stat.size), version: sourceVersion(stat) };
    return current;
  }

  async #roots(generation) {
    const mounts = await this.policy.mounts(true);
    const locations = this.rootLocators ?? ['/', os.homedir(), ...['Desktop', 'Documents', 'Downloads'].map(name => path.join(os.homedir(), name)), ...mounts.filter(entry => entry.path !== '/').map(entry => entry.path)];
    const result = [], seen = new Set();
    for (const value of locations.slice(0, 128)) {
      if (seen.has(value)) continue; seen.add(value);
      try { result.push(await this.#reference(value, false, generation)); }
      catch (error) {
        const code = publicError(error).code;
        // Network mount names are visible without touching that filesystem.
        if (['remote_location', 'unavailable', 'permission_denied'].includes(code)) result.push({ ref: '', locator: value, name: path.basename(value) || '/', kind: 'directory', size: 0, version: '', displayPath: value, unavailable: code });
        else throw error;
      }
    }
    return result;
  }

  async #read(r, generation, signal) {
    const max = r.maxBytes ?? this.limits.readBytes;
    if (!Number.isSafeInteger(r.offset) || r.offset < 0 || !Number.isSafeInteger(r.length) || r.length < 0 || r.length > max || typeof r.version !== 'string') fail('invalid_range', 'Local read range exceeds its limit');
    if (this.reads >= this.limits.reads) fail('busy', 'Too many file reads');
    this.reads++;
    try {
      const metadata = await this.#stat(r.ref, generation, r.version), record = this.#ref(r.ref);
      if (metadata.kind !== 'file') fail('unsupported', 'Only regular local files can be read');
      if (r.offset > metadata.size) fail('invalid_range');
      const before = await fs.lstat(record.real, { bigint: true }); await this.policy.available(record.real, before);
      if (signal?.aborted) fail('cancelled');
      const file = await fs.open(record.real, constants.O_RDONLY | constants.O_NOFOLLOW | constants.O_NONBLOCK);
      try {
        const opened = await file.stat({ bigint: true });
        if (!opened.isFile() || sourceVersion(opened) !== r.version) fail('source_changed');
        await this.#stat(r.ref, generation, r.version);
        const bytes = Buffer.alloc(Math.min(r.length, metadata.size - r.offset)); let read = 0;
        while (read < bytes.length) {
          this.#auth(generation); if (signal?.aborted) fail('cancelled');
          const result = await file.read(bytes, read, Math.min(256 * 1024, bytes.length - read), r.offset + read);
          if (!result.bytesRead) break; read += result.bytesRead;
        }
        if (sourceVersion(await file.stat({ bigint: true })) !== r.version) fail('source_changed');
        await this.#stat(r.ref, generation, r.version);
        return { bytes: new Uint8Array(bytes.subarray(0, read)), offset: r.offset, total: metadata.size, version: r.version };
      } finally { await file.close(); }
    } finally { this.reads--; }
  }

  async snapshot({ ref, version, maxBytes = this.limits.snapshotBytes, signal }) {
    const generation = this.#auth(), metadata = await this.#stat(ref, generation, version);
    const limit = Math.min(maxBytes, this.limits.snapshotBytes);
    if (metadata.size > limit) fail('input_limit', 'This file is too large for a local preview');
    const { bytes } = await this.#read({ ref, version, offset: 0, length: metadata.size, maxBytes: limit }, generation, signal);
    return { bytes, metadata, hash: crypto.createHash('sha256').update(bytes).digest('hex'), generation };
  }
  getScope() { return this.refreshIdentity() ? `${this.scope}:${this.generation}` : null; }
  revoke() { this.scope = undefined; this.refreshIdentity(); }
  async stat({ ref, version }) { const generation = this.#auth(); return this.#stat(ref, generation, version); }

  async #relativeImage(r, generation, signal) {
    await this.#stat(r.ref, generation, r.version); const source = this.#ref(r.ref);
    if (typeof r.path !== 'string' || r.path.length > 4096 || !r.path || path.isAbsolute(r.path) || /^[a-z][a-z\d+.-]*:/i.test(r.path) || r.path.includes('\0')) fail('invalid_request');
    let decoded; try { decoded = decodeURIComponent(r.path); } catch { fail('invalid_request'); }
    const base = path.dirname(source.real), target = path.resolve(base, decoded);
    if (!within(base, target)) fail('outside_document', 'Open images outside this folder separately');
    const real = await this.policy.resolve(target, true);
    if (!within(base, real)) fail('outside_document', 'Open images outside this folder separately');
    const existingRefs = new Set(this.refs.keys()), metadata = await this.#reference(real, false, generation);
    try {
      if (metadata.kind !== 'file' || metadata.size > this.limits.imageBytes) fail('input_limit');
      const result = await this.#read({ ref: metadata.ref, version: metadata.version, offset: 0, length: metadata.size, maxBytes: this.limits.imageBytes }, generation, signal);
      const b = Buffer.from(result.bytes), mime = b.subarray(0, 8).equals(Buffer.from([137,80,78,71,13,10,26,10])) ? 'image/png' : b[0] === 255 && b[1] === 216 && b[2] === 255 ? 'image/jpeg' : /GIF8[79]a/.test(b.subarray(0, 6).toString('ascii')) ? 'image/gif' : b.subarray(0, 4).toString() === 'RIFF' && b.subarray(8, 12).toString() === 'WEBP' ? 'image/webp' : '';
      if (!mime) fail('unsupported', 'Only local raster images can be embedded');
      await this.#stat(r.ref, generation, r.version); return { bytes: result.bytes, mime, version: metadata.version };
    } finally { if (!existingRefs.has(metadata.ref)) this.refs.delete(metadata.ref); }
  }

  async #list(r, generation, signal) {
    if (r.showHidden !== undefined && typeof r.showHidden !== 'boolean' || r.refresh !== undefined && typeof r.refresh !== 'boolean' || r.filter !== undefined && (typeof r.filter !== 'string' || r.filter.length > 128) || r.page !== undefined && (!Number.isSafeInteger(r.page) || r.page < 0 || r.page > this.limits.entries)) fail('invalid_request');
    await this.#storage(generation); const metadata = await this.#stat(r.ref, generation), record = this.#ref(r.ref);
    if (!['directory', 'bundle'].includes(metadata.kind)) fail('unsupported', 'This location is not a directory');
    const conditions = JSON.stringify([r.showHidden ?? false, r.filter ?? '']); let listing = this.listings.get(r.ref);
    if (listing?.busy) fail('busy', 'This directory is already loading');
    if (r.cursor && (!listing || listing.cursor !== r.cursor || listing.conditions !== conditions || listing.version !== metadata.version || listing.changed)) fail('stale_cursor', 'Refresh this changed directory');
    if (listing && (r.refresh || listing.conditions !== conditions)) { await this.#dropListing(r.ref); listing = null; }
    if (!listing) {
      if (r.cursor) fail('stale_cursor');
      if (this.listings.size >= this.limits.listings) fail('resource_limit', 'Collapse a folder before opening another');
      await this.policy.available(record.real, await fs.lstat(record.real, { bigint: true }));
      listing = { id: token(), conditions, version: metadata.version, dir: await fs.opendir(record.real, { bufferSize: 32 }), count: 0, complete: false, cursor: token(), changed: false, busy: false };
      try { this.#auth(generation); } catch (error) { await listing.dir.close(); throw error; }
      this.listings.set(r.ref, listing);
    }
    listing.busy = true;
    try {
      if (metadata.version !== listing.version) listing.changed = true;
      // An explicit page request revisits the bounded disk index without reads.
      if ((!Object.hasOwn(r, 'page') || r.cursor) && !listing.complete && !listing.changed && !listing.incomplete) {
        const entries = []; let scanned = 0;
        while (entries.length < this.limits.pageSize && scanned++ < this.limits.scannedPerBatch) {
          this.#auth(generation); if (signal?.aborted) fail('cancelled');
          if (listing.count + entries.length >= this.limits.entries) { listing.incomplete = 'entry_limit'; break; }
          const entry = await listing.dir.read();
          if (!entry) { listing.complete = true; await listing.dir.close(); listing.dir = null; break; }
          if ((!r.showHidden && entry.name.startsWith('.')) || (r.filter && !entry.name.toLocaleLowerCase().includes(r.filter.toLocaleLowerCase()))) continue;
          const value = path.join(record.real, entry.name);
          try {
            await this.policy.guard(value); const stat = await fs.lstat(value, { bigint: true });
            entries.push({ locator: value, name: entry.name, kind: kindOf(stat, entry.name), size: Number(stat.size), version: sourceVersion(stat), displayPath: value });
          } catch (error) { entries.push({ locator: value, name: entry.name, kind: entry.isDirectory() ? 'directory' : 'unsupported', size: 0, version: '', displayPath: value, unavailable: publicError(error).code }); }
        }
        this.#auth(generation);
        try { this.pages.put(listing.id, entries, listing.count); listing.count += entries.length; }
        catch (error) { if (publicError(error).code !== 'storage_limit') throw error; listing.incomplete = 'storage_limit'; }
        listing.cursor = token();
      }
      if (listing.incomplete && listing.dir) { await listing.dir.close(); listing.dir = null; }
      const page = r.page ?? Math.max(0, Math.ceil(listing.count / this.limits.pageSize) - 1);
      const entries = [];
      for (const entry of this.pages.read(listing.id, page, this.limits.pageSize)) {
        if (entry.unavailable) { entries.push({ ...entry, ref: '' }); continue; }
        try { entries.push(await this.#reference(entry.locator, false, generation)); }
        catch (error) { entries.push({ ...entry, ref: '', unavailable: publicError(error).code }); }
      }
      if ((await this.#stat(r.ref, generation)).version !== listing.version) listing.changed = true;
      return { entries, cursor: listing.complete || listing.incomplete || listing.changed ? null : listing.cursor, complete: listing.complete && !listing.changed, page, listing: listing.id, totalLoaded: listing.count, pageSize: this.limits.pageSize, changed: listing.changed, ...(listing.incomplete ? { incomplete: listing.incomplete } : {}) };
    } finally { listing.busy = false; }
  }

  async #watch(ref, generation) {
    const metadata = await this.#stat(ref, generation), record = this.#ref(ref);
    if (this.watches.has(ref)) return { watching: true };
    if (this.watches.size >= this.limits.watches) return { watching: false, reason: 'watch_limit' };
    if (!['file', 'directory', 'bundle'].includes(metadata.kind)) return { watching: false, reason: 'unsupported' };
    try {
      const watcher = watchFile(record.real, { persistent: false }, () => {
        if (!this.refreshIdentity() || generation !== this.generation) return;
        const listing = this.listings.get(ref); if (listing) listing.changed = true;
        this.dirty.add(ref); if (this.visible && !this.notifyTimer) this.notifyTimer = setTimeout(() => { this.notifyTimer = null; if (this.visible && this.dirty.size) { this.onChange({ kind: 'changed', refs: [...this.dirty], generation: this.generation }); this.dirty.clear(); } }, 150);
      });
      watcher.on('error', () => { this.#unwatch(ref); this.onChange({ kind: 'changed', refs: [ref], generation }); }); this.watches.set(ref, watcher); return { watching: true };
    } catch { return { watching: false, reason: 'watch_unavailable' }; }
  }
  #unwatch(ref) { this.watches.get(ref)?.close(); this.watches.delete(ref); this.dirty.delete(ref); }
  async #dropListing(ref) { const listing = this.listings.get(ref); if (!listing) return; this.listings.delete(ref); await listing.dir?.close().catch(() => {}); this.pages?.remove(listing.id); }
  async #release(ref) { if (typeof ref !== 'string') fail('invalid_request'); this.#unwatch(ref); await this.#dropListing(ref); }

  async #loadState(generation) {
    await this.cleanup; const file = path.join(this.directory, this.scope, 'reading-state.json');
    try { const handle = await fs.open(file, constants.O_RDONLY | constants.O_NOFOLLOW); try { const buffer = Buffer.alloc(this.limits.stateBytes + 1), { bytesRead } = await handle.read(buffer, 0, buffer.length, 0); if (bytesRead > this.limits.stateBytes) fail('state_limit'); this.#auth(generation); return validateState(JSON.parse(buffer.subarray(0, bytesRead).toString('utf8')), this.limits.stateBytes); } finally { await handle.close(); } }
    catch (error) { if (error.code === 'ENOENT') return null; throw error; }
  }
  async #saveState(state, generation) {
    const value = validateState(state, this.limits.stateBytes); await this.#storage(generation);
    const root = path.join(this.directory, this.scope), temporary = path.join(root, `state-${token()}.tmp`);
    try { await fs.writeFile(temporary, JSON.stringify(value), { mode: 0o600, flag: 'wx' }); this.#auth(generation); await fs.rename(temporary, path.join(root, 'reading-state.json')); return { completed: true }; }
    finally { await fs.rm(temporary, { force: true }); }
  }
  async #clear(generation, signal) {
    await this.cleanup; await this.#cleanPending();
    const state = path.join(this.directory, this.scope, 'reading-state.json');
    for (const request of this.requests.values()) if (request.signal !== signal) request.abort();
    for (const ref of [...this.listings.keys()]) await this.#dropListing(ref);
    for (const ref of [...this.watches.keys()]) this.#unwatch(ref);
    this.refs.clear(); this.pages?.close(); this.pages = null;
    await this.broker?.clear?.();
    try { if (this.runDirectory) await fs.rm(this.runDirectory, { recursive: true, force: true }); this.runDirectory = null; }
    catch (error) { this.cleanupFailed = true; throw error; }
    await fs.rm(state, { force: true }); this.cleanupFailed = false; this.#auth(generation); this.generation = token(); this.broker?.refreshIdentity?.(); this.onChange({ kind: 'revoked', generation: this.generation }); return { completed: true, generation: this.generation };
  }
  async close() { this.closed = true; this.refreshIdentity(); clearTimeout(this.notifyTimer); await this.cleanup; await this.pendingSetup?.catch(() => {}); }
}

async function cleanAbandonedRuns(root) {
  const directory = await fs.opendir(root);
  try {
    let inspected = 0;
    for await (const entry of directory) {
      if (++inspected > 256) fail('cleanup_pending', 'Too many local reading remnants; clear the local reading cache');
      if (!entry.isDirectory() || !/^reading-[A-Za-z0-9]{6}$/.test(entry.name)) continue;
      const candidate = path.join(root, entry.name), owner = path.join(candidate, 'owner.json');
      try {
        const handle = await fs.open(owner, constants.O_RDONLY | constants.O_NOFOLLOW);
        let pid;
        try { const buffer = Buffer.alloc(128), { bytesRead } = await handle.read(buffer, 0, 128, 0); if (bytesRead === 128) continue; pid = JSON.parse(buffer.subarray(0, bytesRead).toString()).pid; } finally { await handle.close(); }
        if (!Number.isSafeInteger(pid) || pid <= 0) continue;
        try { process.kill(pid, 0); } catch (error) { if (error.code === 'ESRCH') await fs.rm(candidate, { recursive: true, force: true }); }
      } catch (error) { if (error.code !== 'ENOENT') throw error; }
    }
  } finally { await directory.close().catch(() => {}); }
}

function validateState(state, maxBytes) {
  if (!state || typeof state !== 'object' || Array.isArray(state) || Buffer.byteLength(JSON.stringify(state)) > maxBytes) fail('invalid_state');
  if (Object.keys(state).some(key => !['tabs', 'active', 'expanded', 'selected', 'scrollTop', 'showHidden', 'filter'].includes(key)) || !Array.isArray(state.tabs) || state.tabs.length > 24 || !Array.isArray(state.expanded) || state.expanded.length > 128) fail('invalid_state');
  for (const item of state.tabs) {
    if (!item || Object.keys(item).some(key => !['locator', 'position'].includes(key))) fail('invalid_state'); locator(item.locator);
    if (item.position !== undefined && (!item.position || typeof item.position !== 'object' || Array.isArray(item.position) || Object.keys(item.position).length > 8 || Object.entries(item.position).some(([key, value]) => !/^[a-zA-Z]{1,24}$/.test(key) || (key === 'sheet' ? typeof value !== 'string' || value.length > 128 : typeof value !== 'number' || !Number.isFinite(value) || value < 0 || value > 1e9)))) fail('invalid_state');
  }
  state.expanded.forEach(locator); if (state.active !== 'files') locator(state.active); if (state.selected) locator(state.selected);
  if (!Number.isFinite(state.scrollTop) || state.scrollTop < 0 || state.scrollTop > 1e9 || typeof state.showHidden !== 'boolean' || typeof state.filter !== 'string' || state.filter.length > 128) fail('invalid_state');
  return structuredClone(state);
}
