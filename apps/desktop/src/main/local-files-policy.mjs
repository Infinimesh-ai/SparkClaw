import fs from 'node:fs/promises';
import path from 'node:path';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';

const execute = promisify(execFile);
const LOCAL_TYPES = new Set(['apfs', 'hfs', 'devfs', 'ext2', 'ext3', 'ext4', 'xfs', 'btrfs', 'tmpfs', 'overlay', 'ramfs', 'vfat', 'exfat', 'ntfs', 'ntfs3', 'iso9660', 'udf', 'squashfs', 'zfs']);
export class LocalFilesError extends Error {
  constructor(code, message = code) { super(message); this.code = code; }
}
export const fail = (code, message) => { throw new LocalFilesError(code, message); };
export function within(parent, child) { const part = path.relative(parent, child); return part === '' || (part !== '..' && !part.startsWith(`..${path.sep}`) && !path.isAbsolute(part)); }
export function locator(value) {
  if (typeof value !== 'string' || value.length > 8192 || !path.isAbsolute(value) || value.includes('\0')) fail('invalid_request', 'Invalid local locator');
  return path.normalize(value);
}
export function sourceVersion(stat) { return [stat.dev, stat.ino, stat.mode, stat.size, stat.mtimeNs, stat.ctimeNs, stat.birthtimeNs].join(':'); }
export function fileIdentity(stat) { return [stat.dev, stat.ino, stat.birthtimeNs].join(':'); }
export function mountIdentity(mount) { return JSON.stringify([mount.path, mount.type, mount.id ?? '', mount.source ?? '']); }
export function kindOf(stat, name) {
  return stat.isSymbolicLink() ? 'link' : stat.isDirectory() ? (/\.app$/i.test(name) ? 'bundle' : 'directory') : stat.isFile() ? 'file' : 'unsupported';
}
export function publicError(error) {
  const previewCodes = new Set(['locked', 'invalid_request', 'invalid_reference', 'invalid_range', 'source_changed', 'limit_exceeded', 'cancelled', 'timeout', 'cleanup_pending', 'preview_unavailable', 'preview_failed', 'invalid_document', 'unsupported', 'unsupported_encoding']);
  const code = error instanceof LocalFilesError || previewCodes.has(error?.code) ? error.code : ({ EACCES: 'permission_denied', EPERM: 'permission_denied', ENOENT: 'unavailable', ENOTDIR: 'source_changed', ELOOP: 'source_changed', ENOSPC: 'storage_limit', EDQUOT: 'storage_limit' }[error?.code] || 'unavailable');
  // OS errors carry paths; keep this local API's error payload deliberately small.
  return { code, message: error instanceof LocalFilesError ? error.message : code };
}
export function parseMounts(text, platform) {
  const result = [];
  for (const line of text.split('\n')) {
    if (platform === 'darwin') {
      const match = / on (.+) \(([^,)]+)/.exec(line);
      if (match) result.push({ path: match[1], type: match[2], source: line.slice(0, match.index) });
    } else {
      const halves = line.split(' - '), words = halves[0].split(' ');
      if (halves.length === 2 && words.length >= 6) result.push({ path: words[4].replace(/\\([0-7]{3})/g, (_, n) => String.fromCharCode(parseInt(n, 8))), type: halves[1].split(' ')[0], id: words[0], source: halves[1].split(' ')[1] });
    }
  }
  return result.sort((a, b) => b.path.length - a.path.length);
}

// Mount inventory reads only local kernel metadata. Unknown/fuse/autofs mounts
// are not probed: a stat/readdir there could connect to a server or hydrate data.
export class LocalFilesPolicy {
  constructor({ platform = process.platform, mountProvider, clock = Date.now } = {}) {
    Object.assign(this, { platform, mountProvider, clock }); this.cached = null; this.checkedAt = 0;
  }
  async mounts(force = false) {
    if (!force && this.cached && this.clock() - this.checkedAt < 2000) return this.cached;
    let mounts;
    if (this.mountProvider) mounts = await this.mountProvider();
    else if (this.platform === 'darwin') mounts = parseMounts((await execute('/sbin/mount', [], { timeout: 2000, maxBuffer: 256 * 1024, env: { PATH: '/usr/bin:/bin:/usr/sbin:/sbin' } })).stdout, this.platform);
    else if (this.platform === 'linux') {
      const handle = await fs.open('/proc/self/mountinfo', 'r');
      try { const data = Buffer.alloc(256 * 1024 + 1); const { bytesRead } = await handle.read(data, 0, data.length, 0); if (bytesRead > 256 * 1024) fail('unavailable', 'Mount inventory exceeds its limit'); mounts = parseMounts(data.subarray(0, bytesRead).toString(), this.platform); }
      finally { await handle.close(); }
    } else fail('unavailable', 'Local file browsing is unavailable on this platform');
    if (!mounts.some(entry => entry.path === '/')) fail('unavailable', 'Local mount inventory is unavailable');
    this.cached = mounts.map(entry => ({ ...entry, local: entry.local ?? LOCAL_TYPES.has(entry.type) })).sort((a, b) => b.path.length - a.path.length);
    this.checkedAt = this.clock(); return this.cached;
  }
  async guard(value) {
    const mount = (await this.mounts()).find(entry => within(entry.path, value));
    if (!mount?.local) fail('remote_location', 'This location must be opened through the operating system');
    return mount;
  }
  async available(value, stat) {
    if (/\.icloud$/i.test(value)) fail('cloud_placeholder', 'Download this file through the operating system first');
    // UF_DATALESS is not exposed by Node stat. Query only metadata, before open.
    if (this.platform === 'darwin') {
      const { stdout } = await execute('/usr/bin/stat', ['-f', '%f', value], { timeout: 2000, maxBuffer: 1024, env: { PATH: '/usr/bin:/bin' } });
      const flags = Number(stdout.trim());
      if (!Number.isSafeInteger(flags)) fail('unavailable', 'File availability could not be verified');
      if ((flags & 0x40000000) !== 0) fail('cloud_placeholder', 'Download this file through the operating system first');
    } else if (stat.isFile() && stat.size > 0n && stat.blocks === 0n) {
      // Conservatively deny sparse placeholders on unsupported providers.
      fail('cloud_placeholder', 'This file has no locally allocated data');
    }
  }
  async resolve(value, followFinal = false) {
    await this.guard(locator(value));
    let pending = locator(value).split(path.sep).filter(Boolean), current = path.parse(value).root, links = 0;
    while (pending.length) {
      const part = pending.shift(); current = path.resolve(current, part);
      await this.guard(current);
      const stat = await fs.lstat(current, { bigint: true });
      if (stat.isSymbolicLink() && (pending.length || followFinal)) {
        if (++links > 32) fail('link_cycle', 'This link contains a cycle');
        const target = path.resolve(path.dirname(current), await fs.readlink(current));
        await this.guard(target);
        pending = [...target.split(path.sep).filter(Boolean), ...pending]; current = path.parse(target).root;
      }
    }
    await this.guard(current);
    return current;
  }
}
