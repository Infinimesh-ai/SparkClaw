const fs = require('node:fs/promises');
const { constants } = require('node:fs');
const path = require('node:path');

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const MAX_BYTES = 110 << 20;

class ExtensionDownloads {
  constructor(sendCommand, emit) {
    this.sendCommand = sendCommand;
    this.emit = emit;
    this.states = new Map();
    this.downloadPath = null;
    this.closed = false;
  }

  async configure(params) {
    if (this.closed || params?.behavior !== 'allowAndName' || params.browserContextId !== undefined ||
        !path.isAbsolute(params.downloadPath ?? '')) throw new Error('Unsupported extension download configuration');
    const info = await fs.lstat(params.downloadPath);
    if (!info.isDirectory() || info.isSymbolicLink() || info.uid !== process.getuid() ||
        (info.mode & 0o077) !== 0 || await fs.realpath(params.downloadPath) !== params.downloadPath) {
      throw new Error('Download directory is not private');
    }
    this.downloadPath = params.downloadPath;
    return this.sendCommand('sparkclaw.downloads.configure', [{ behavior: params.behavior }]);
  }

  async cancel(params) {
    if (!this.states.has(params?.guid)) throw new Error('Download is outside the task');
    return this.sendCommand('sparkclaw.downloads.cancel', [params.guid]);
  }

  handleEvent(method, params) {
    if (!['sparkclaw.downloadWillBegin', 'sparkclaw.downloadProgress'].includes(method)) return false;
    const value = params?.[0];
    if (this.closed || !this.downloadPath || !UUID.test(value?.guid ?? '')) return true;
    if (method === 'sparkclaw.downloadWillBegin') {
      if (this.states.has(value.guid)) return true;
      this.states.set(value.guid, { finished: false });
      this.emit({ method: 'Browser.downloadWillBegin', params: value });
    } else {
      const state = this.states.get(value.guid);
      if (!state || state.finished) return true;
      if (value.state === 'completed') {
        state.finished = true;
        state.completion = this.#complete(value);
      } else {
        if (value.state === 'canceled') state.finished = true;
        this.emit({ method: 'Browser.downloadProgress', params: value });
      }
    }
    return true;
  }

  async #complete(value) {
    const destination = path.join(this.downloadPath, value.guid);
    let created = false;
    try {
      if (!path.isAbsolute(value.filename ?? '') || !Number.isSafeInteger(value.bytes) ||
          value.bytes < 0 || value.bytes > MAX_BYTES || value.bytes !== value.receivedBytes ||
          await fs.realpath(value.filename) !== value.filename) throw new Error('Invalid native download');
      const source = await fs.open(value.filename, constants.O_RDONLY | constants.O_NOFOLLOW);
      try {
        const before = await source.stat();
        if (!before.isFile() || before.uid !== process.getuid() || before.size !== value.bytes) throw new Error('Native download changed');
        const output = await fs.open(destination, 'wx', 0o600);
        created = true;
        try {
          const buffer = Buffer.alloc(256 << 10);
          let offset = 0;
          for (;;) {
            const { bytesRead } = await source.read(buffer, 0, buffer.length, offset);
            if (bytesRead === 0) break;
            offset += bytesRead;
            if (offset > value.bytes) throw new Error('Native download grew');
            await output.writeFile(buffer.subarray(0, bytesRead));
          }
          const after = await source.stat();
          if (offset !== value.bytes || before.size !== after.size || before.mtimeMs !== after.mtimeMs) throw new Error('Native download changed');
          await output.sync();
        } finally { await output.close(); }
      } finally { await source.close(); }
      await this.sendCommand('sparkclaw.downloads.release', [value.guid]);
      if (this.closed) throw new Error('Download session closed');
      this.emit({ method: 'Browser.downloadProgress', params: { guid: value.guid, state: 'completed', totalBytes: value.bytes, receivedBytes: value.bytes } });
    } catch {
      if (created) await fs.rm(destination, { force: true }).catch(() => {});
      if (!this.closed) {
        await this.sendCommand('sparkclaw.downloads.cancel', [value.guid]).catch(() => {});
        this.emit({ method: 'Browser.downloadProgress', params: { guid: value.guid, state: 'canceled', totalBytes: value.totalBytes, receivedBytes: value.receivedBytes } });
      }
    }
  }

  close() { this.closed = true; }
}

module.exports = { ExtensionDownloads };
