import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
const hash = value => crypto.createHash('sha256').update(value).digest('hex');
const phases = new Set(['prepared', 'attempted', 'terminal', 'applied']);
const MAX_RECORD_BYTES = 24 * 1024 * 1024;

// New intents/results use native credential protection. Fingerprint-only
// records remain lookup fences; they are never imported as replayable intents.
export class ISCPMutationJournal {
  constructor(root, scope, protector) {
    Object.assign(this, { root, scope: hash(JSON.stringify(scope)), protector });
    fs.mkdirSync(root, { recursive: true, mode: 0o700 });
    const st = fs.lstatSync(root);
    if (!st.isDirectory() || st.isSymbolicLink() || st.mode & 0o077 || process.getuid && st.uid !== process.getuid()) throw new Error('Mutation journal must be private');
  }
  read(resource) {
    const sealed = this.#readSealed(this.#path(resource, true));
    if (sealed) { this.#validate(sealed, resource); return sealed; }
    try {
      const row = JSON.parse(this.#readPrivate(this.#path(resource)));
      this.#validate(row, resource);
      if (row.schema_version !== 1) throw new Error('Mutation journal conflict');
      return row;
    } catch (error) { if (error.code === 'ENOENT') return; throw error; }
  }
  // Recordings retain their opaque identity. Old rows without an attempt phase
  // cannot prove never-attempted, even when the remote lookup returns missing.
  begin(resource, request, revision) {
    const prior = this.read(resource), fingerprint = hash(JSON.stringify(request));
    if (prior) {
      if (prior.fingerprint !== fingerprint) throw new Error('The previous change requires reconciliation');
      return prior;
    }
    const row = { schema_version: 1, scope: this.scope, resource: hash(resource), operation: request.operation,
      operation_id: crypto.randomUUID(), fingerprint, expected_revision: revision || '', phase: 'prepared' };
    this.#write(resource, row); return row;
  }
  prepare(resource, request, { revision = '', rawBody, deadline, authorizationRevision } = {}) {
    if (!Number.isFinite(deadline)) throw new Error('Mutation requires its original deadline');
    const previous = this.read(resource);
    if (previous) {
      if (this.matches(previous, request)) return previous;
      if (previous.schema_version !== 2 || previous.phase !== 'applied') throw new Error('The previous change requires reconciliation');
      // Preserve immutable receipt/tombstone before installing another intent.
      this.#atomicWrite(this.#historyPath(previous), this.#seal(previous));
      fs.unlinkSync(this.#path(resource, true)); this.#sync();
    }
    const row = { schema_version: 2, scope: this.scope, resource: hash(resource), resource_key: resource,
      operation: request.operation, operation_id: crypto.randomUUID(), fingerprint: hash(JSON.stringify(request)),
      expected_revision: revision, authorization_revision: authorizationRevision, phase: 'prepared', created_at: Date.now(), not_after: deadline,
      request: structuredClone(request), ...(rawBody !== undefined ? { raw_body: rawBody } : {}) };
    this.#write(resource, row); return row;
  }
  pending() {
    const pattern = new RegExp(`^${this.scope}\\.[a-f0-9]{64}\\.sealed$`);
    return fs.readdirSync(this.root).filter(name => pattern.test(name)).map(name => {
      const row = this.#readSealed(path.join(this.root, name));
      this.#validate(row, row?.resource_key);
      if (this.#path(row.resource_key, true) !== path.join(this.root, name)) throw new Error('Mutation journal conflict');
      return row;
    }).filter(row => ['prepared', 'attempted'].includes(row.phase));
  }
  markAttempted(resource, id) {
    const row = this.#record(resource, id);
    if (row.phase !== 'prepared') throw new Error('Mutation was already attempted');
    if (row.not_after !== undefined && row.not_after <= Date.now()) throw new Error('Original mutation deadline expired');
    this.#write(resource, { ...row, phase: 'attempted' });
  }
  saveResult(resource, id, response) {
    const row = this.#record(resource, id);
    if (row.schema_version !== 2 || !Number.isInteger(response?.status) || response.status < 200 || response.status > 599) throw new Error('Mutation receipt mismatch');
    if (row.response && JSON.stringify(row.response) !== JSON.stringify(response)) throw new Error('Mutation receipt changed');
    this.#write(resource, { ...row, phase: row.phase === 'applied' ? 'applied' : 'terminal', response });
  }
  cancelPrepared(resource, id, reason = 'Original change was not sent') {
    const row = this.#record(resource, id);
    if (row.schema_version === 2 && row.phase === 'prepared') this.saveResult(resource, id, { status: 409, body: { error: reason, code: 'operation_not_sent' } });
  }
  markReleased(resource, id) { const row = this.#record(resource, id); this.#write(resource, { ...row, audio_released: true }); }
  complete(resource, id) {
    const row = this.#record(resource, id);
    if (row.schema_version === 2) {
      if (!row.response || !['terminal', 'applied'].includes(row.phase)) throw new Error('Mutation outcome is unresolved');
      // Renderer delivery can be lost. Keep the last result so the same UI
      // intent still returns it after restart, without another external effect.
      this.#write(resource, { ...row, phase: 'applied' });
    } else { fs.unlinkSync(this.#path(resource)); this.#sync(); }
  }
  matches(row, request) { return row.fingerprint === hash(JSON.stringify(request)); }
  #record(resource, id) { const row = this.read(resource); if (row?.operation_id !== id) throw new Error('Mutation receipt mismatch'); return row; }
  #validate(row, resource) {
    if (!row || typeof resource !== 'string' || row.scope !== this.scope || row.resource !== hash(resource) || !row.operation_id || !row.fingerprint) throw new Error('Mutation journal conflict');
    if (row.schema_version === 2 && (!phases.has(row.phase) || row.resource_key !== resource || !Number.isFinite(row.not_after) || !this.matches(row, row.request) || row.operation !== row.request.operation)) throw new Error('Mutation journal conflict');
  }
  #path(resource, sealed = false) { return path.join(this.root, sealed ? `${this.scope}.${hash(resource)}.sealed` : `${hash(this.scope + ':' + resource)}.json`); }
  #historyPath(row) { return path.join(this.root, `${this.scope}.${hash(row.operation_id)}.done`); }
  #seal(row) {
    if (!this.protector?.sealMutationRecord) throw new Error('Secure mutation storage is unavailable');
    return this.protector.sealMutationRecord(row);
  }
  #readSealed(filename) {
    let bytes;
    try { bytes = this.#readPrivate(filename); } catch (error) { if (error.code === 'ENOENT') return; throw error; }
    if (!this.protector?.openMutationRecord) throw new Error('Secure mutation storage is unavailable');
    return this.protector.openMutationRecord(bytes);
  }
  #readPrivate(filename) {
    const fd = fs.openSync(filename, fs.constants.O_RDONLY | fs.constants.O_NOFOLLOW | fs.constants.O_NONBLOCK);
    try {
      const st = fs.fstatSync(fd);
      if (!st.isFile() || st.mode & 0o077 || st.size > MAX_RECORD_BYTES || process.getuid && st.uid !== process.getuid()) throw new Error('Mutation journal is unsafe');
      return fs.readFileSync(fd);
    } finally { fs.closeSync(fd); }
  }
  #write(resource, row) { this.#atomicWrite(this.#path(resource, row.schema_version === 2), row.schema_version === 2 ? this.#seal(row) : Buffer.from(JSON.stringify(row))); }
  #atomicWrite(filename, bytes) {
    if (bytes.length > MAX_RECORD_BYTES) throw new Error('Mutation record exceeds storage bounds');
    const tmp = filename + '.' + crypto.randomUUID() + '.tmp';
    try {
      const fd = fs.openSync(tmp, 'wx', 0o600);
      try { fs.writeFileSync(fd, bytes); fs.fsyncSync(fd); } finally { fs.closeSync(fd); }
      fs.renameSync(tmp, filename); this.#sync();
    } finally { fs.rmSync(tmp, { force: true }); }
  }
  #sync() { const fd = fs.openSync(this.root, 'r'); try { fs.fsyncSync(fd); } finally { fs.closeSync(fd); } }
}
