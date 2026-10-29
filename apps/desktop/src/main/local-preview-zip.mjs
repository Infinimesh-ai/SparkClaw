import yauzl from 'yauzl';
import { previewError, previewLimits } from './local-preview-limits.mjs';

const crcTable = new Uint32Array(256);
for (let n = 0; n < 256; n++) {
  let c = n;
  for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
  crcTable[n] = c;
}

// Inspect every entry before ExcelJS/LibreOffice receives the same snapshot.
// Decompressed chunks are counted before being consumed; no expanded archive is retained.
export async function inspectPreviewArchive(bytes, overrides = {}) {
  const limits = previewLimits(overrides);
  if (bytes.length > limits.inputBytes || bytes.length < 22 || bytes.readUInt32LE(0) !== 0x04034b50) {
    throw previewError('invalid_document', 'This file is not a supported ZIP document.');
  }
  const zip = await new Promise((resolve, reject) => yauzl.fromBuffer(bytes,
    { lazyEntries: true, autoClose: true, validateEntrySizes: false, strictFileNames: true },
    (error, value) => error ? reject(error) : resolve(value)));
  const names = new Set();
  let entries = 0, declared = 0, actual = 0;
  try {
    if (zip.entryCount > limits.archiveEntries) throw previewError('limit_exceeded', 'Archive has too many entries.');
    await new Promise((resolve, reject) => {
      let stopped = false;
      const fail = error => { if (!stopped) { stopped = true; zip.close(); reject(error); } };
      zip.on('error', fail);
      zip.on('end', () => { if (!stopped) { stopped = true; resolve(); } });
      zip.on('entry', async entry => {
        try {
          if (++entries > limits.archiveEntries || names.has(entry.fileName) ||
              !/^[^\0\\]+$/.test(entry.fileName) || entry.fileName.startsWith('/') ||
              entry.fileName.split('/').includes('..') || entry.fileName.includes(':') ||
              (entry.generalPurposeBitFlag & 1) || ![0, 8].includes(entry.compressionMethod) ||
              ((entry.externalFileAttributes >>> 16) & 0o170000) === 0o120000) {
            throw previewError('invalid_document', 'Unsupported or ambiguous archive entry.');
          }
          names.add(entry.fileName);
          declared += entry.uncompressedSize;
          if (!Number.isSafeInteger(declared) || entry.uncompressedSize > limits.archiveEntryBytes || declared > limits.archiveBytes) {
            throw previewError('limit_exceeded', 'Declared archive expansion exceeds the preview limit.');
          }
          const stream = await new Promise((yes, no) => zip.openReadStream(entry, (error, value) => error ? no(error) : yes(value)));
          let emitted = 0, crc = 0xffffffff;
          for await (const chunk of stream) {
            emitted += chunk.length; actual += chunk.length;
            if (emitted > entry.uncompressedSize || emitted > limits.archiveEntryBytes || actual > limits.archiveBytes) {
              stream.destroy();
              throw previewError('limit_exceeded', 'Actual archive expansion exceeds the preview limit.');
            }
            for (const byte of chunk) crc = crcTable[(crc ^ byte) & 0xff] ^ (crc >>> 8);
          }
          if (emitted !== entry.uncompressedSize || ((crc ^ 0xffffffff) >>> 0) !== entry.crc32) {
            throw previewError('invalid_document', 'Archive entry size or checksum is inconsistent.');
          }
          if (!stopped) zip.readEntry();
        } catch (error) { fail(error); }
      });
      zip.readEntry();
    });
    return { names, entries, declaredBytes: declared, actualBytes: actual };
  } catch (error) {
    zip.close();
    if (error.code) throw error;
    throw previewError('invalid_document', 'The document archive is damaged or unsupported.');
  }
}
