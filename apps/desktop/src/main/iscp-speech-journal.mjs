import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
const hash = value => crypto.createHash('sha256').update(value).digest('hex');
const MAX_BYTES = 8 << 20;

// Identities, sequence fences and private results are retained, never PCM.
export class ISCPSpeechJournal {
  constructor(root, scope, protector) {
    Object.assign(this, { root, scope: hash(scope), protector });
    fs.mkdirSync(root, { recursive: true, mode: 0o700 });
    const st = fs.lstatSync(root);
    if (!st.isDirectory() || st.isSymbolicLink() || st.mode & 0o077 || process.getuid && st.uid !== process.getuid()) throw new Error('Speech storage is not private');
  }
  key(request) { return hash(JSON.stringify([request.session_id, request.request_id])); }
  read(key) {
    if (!/^[a-f0-9]{64}$/u.test(key)) throw new Error('Invalid speech identity');
    let fd;
    try {
      fd = fs.openSync(this.#path(key), fs.constants.O_RDONLY | fs.constants.O_NOFOLLOW | fs.constants.O_NONBLOCK);
      const st = fs.fstatSync(fd);
      if (!st.isFile() || st.mode & 0o077 || st.size > MAX_BYTES || process.getuid && st.uid !== process.getuid()) throw new Error('Speech storage is not private');
      const row = this.protector?.openMutationRecord(fs.readFileSync(fd));
      if (row?.schema_version !== 2 || row.kind !== 'speech-session' || row.scope !== this.scope || row.key !== key) throw new Error('Saved speech identity differs');
      return row;
    } catch (error) { if (error.code === 'ENOENT') return; throw error; }
    finally { if (fd !== undefined) fs.closeSync(fd); }
  }
  find(sessionID) {
    for (const name of fs.readdirSync(this.root)) {
      if (!name.startsWith(this.scope + '.') || !name.endsWith('.sealed')) continue;
      const row = this.read(name.slice(this.scope.length + 1, -7));
      if (row?.session_id === sessionID) return row;
    }
  }
  update(key, fields) {
    const row = { ...this.read(key), ...fields, schema_version: 2, kind: 'speech-session', key, scope: this.scope };
    if (!this.protector?.sealMutationRecord) throw new Error('Secure speech storage is unavailable');
    const bytes = this.protector.sealMutationRecord(row);
    if (bytes.length > MAX_BYTES) throw new Error('Speech result exceeds storage bounds');
    const filename = this.#path(key), temporary = filename + '.' + crypto.randomUUID() + '.tmp';
    try {
      const fd = fs.openSync(temporary, 'wx', 0o600);
      try { fs.writeFileSync(fd, bytes); fs.fsyncSync(fd); } finally { fs.closeSync(fd); }
      fs.renameSync(temporary, filename);
      const directory = fs.openSync(this.root, 'r');
      try { fs.fsyncSync(directory); } finally { fs.closeSync(directory); }
    } finally { fs.rmSync(temporary, { force: true }); }
    return row;
  }
  #path(key) { return path.join(this.root, this.scope + '.' + key + '.sealed'); }
}
