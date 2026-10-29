import { DatabaseSync } from 'node:sqlite';
import fs from 'node:fs/promises';
import path from 'node:path';
import { fail } from './local-files-policy.mjs';

// A disk-backed index provides sorted, revisitable windows without materializing
// an entire directory. The SQL index also avoids unbounded temporary sort files.
export class LocalFilesPages {
  constructor(directory, limitBytes) { this.directory = directory; this.limitBytes = limitBytes; }
  async open() {
    await fs.mkdir(this.directory, { recursive: true, mode: 0o700 });
    await fs.chmod(this.directory, 0o700);
    const space = await fs.statfs(this.directory);
    if (space.bavail * space.bsize < 64 * 1024) fail('storage_limit', 'Free disk space is too low for the local listing cache');
    this.file = path.join(this.directory, 'directory-pages.sqlite');
    try {
      this.db = new DatabaseSync(this.file);
      await fs.chmod(this.file, 0o600);
      this.db.exec(`PRAGMA page_size=4096; PRAGMA max_page_count=${Math.max(16, Math.floor(this.limitBytes / 8192))}; PRAGMA cache_size=-512; PRAGMA mmap_size=0; PRAGMA journal_mode=DELETE;
        CREATE TABLE IF NOT EXISTS entries(listing TEXT NOT NULL, sequence INTEGER NOT NULL, directory INTEGER NOT NULL, sort_key TEXT NOT NULL, name TEXT NOT NULL, data TEXT NOT NULL, PRIMARY KEY(listing,sequence));
        CREATE INDEX IF NOT EXISTS entry_order ON entries(listing,directory DESC,sort_key,name,sequence);`);
      // Lists are authority-scoped and never survive a process restart.
      this.db.exec('DELETE FROM entries;'); return this;
    } catch (error) {
      this.close();
      if (error.errcode === 13 || error.code === 'ERR_SQLITE_ERROR' && /full/i.test(error.message)) fail('storage_limit', 'The local listing cache is full; free disk space and retry');
      throw error;
    }
  }
  put(listing, entries, start) {
    const insert = this.db.prepare('INSERT INTO entries VALUES(?,?,?,?,?,?)');
    this.db.exec('BEGIN');
    try {
      entries.forEach((entry, index) => insert.run(listing, start + index, ['directory', 'bundle'].includes(entry.kind) ? 1 : 0, naturalKey(entry.name), entry.name, JSON.stringify(entry)));
      this.db.exec('COMMIT');
    } catch (error) {
      // SQLITE_FULL can roll back the transaction itself. Preserve that original
      // failure instead of replacing it with "no transaction is active".
      try { this.db.exec('ROLLBACK'); } catch { /* already rolled back */ }
      if (error.errcode === 13 || error.code === 'ERR_SQLITE_ERROR' && /full/i.test(error.message)) fail('storage_limit', 'The local listing cache is full; narrow this directory filter'); throw error;
    }
  }
  read(listing, page, pageSize) {
    return this.db.prepare('SELECT data FROM entries WHERE listing=? ORDER BY directory DESC,sort_key,name,sequence LIMIT ? OFFSET ?').all(listing, pageSize, page * pageSize).map(row => JSON.parse(row.data));
  }
  remove(listing) { this.db?.prepare('DELETE FROM entries WHERE listing=?').run(listing); }
  close() { this.db?.close(); this.db = null; }
}

function naturalKey(name) {
  return name.toLocaleLowerCase('en').replace(/\d+/g, digits => { const number = digits.replace(/^0+/, '') || '0'; return `\u0001${String(number.length).padStart(3, '0')}${number}\u0002${String(digits.length).padStart(3, '0')}`; });
}
