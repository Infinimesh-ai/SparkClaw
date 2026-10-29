import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import crypto from 'node:crypto';
import { pathToFileURL, fileURLToPath } from 'node:url';

if (process.argv.length !== 4) throw new Error('Usage: node run-local-preview-capacity-matrix.mjs FIXTURES OUTPUT');
const fixtureDirectory = path.resolve(process.argv[2]), outputDirectory = path.resolve(process.argv[3]);
assert.notEqual(fixtureDirectory, outputDirectory);
const moduleDirectory = process.env.SPARKCLAW_FORMAT_MODULES || path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../src/main');
const { LocalPreviewBroker } = await import(pathToFileURL(path.join(moduleDirectory, 'local-preview-broker.mjs')).href);
const hash = bytes => crypto.createHash('sha256').update(bytes).digest('hex');
await fs.mkdir(outputDirectory, { recursive: true });
const directory = await fs.realpath(await fs.mkdtemp(path.join(os.tmpdir(), 'sparkx-capacity-matrix-')));
let current;
const observations = [], rows = [];
const broker = new LocalPreviewBroker({ directory, workerRuntime: process.env.SPARKCLAW_PREVIEW_RUNTIME,
  getScope: () => 'synthetic-capacity-qualification', stat: async () => current.metadata,
  snapshot: async () => ({ bytes: Buffer.from(current.bytes) }), onObservation: value => observations.push(value) });
try {
  for (const [name, pages] of [['capacity-40-pages.docx', 40], ['capacity-20-slides.pptx', 20]]) {
    const filename = path.join(fixtureDirectory, name), bytes = await fs.readFile(filename), sha256 = hash(bytes), started = Date.now();
    current = { bytes, metadata: { ref: name, version: sha256, locator: filename, name, kind: 'file', size: bytes.length } };
    const readerId = `capacity-${rows.length}`;
    try {
      const preview = await broker.prepare({ ref: name, version: sha256, kind: 'office', readerId });
      assert.equal(preview.kind, 'pdf'); assert.equal(preview.pageCount, pages, name);
      const output = Buffer.alloc(preview.size);
      for (let offset = 0; offset < preview.size; offset += 1024 * 1024) {
        const value = await broker.read({ previewId: preview.previewId, readerId, offset, length: Math.min(1024 * 1024, preview.size - offset) });
        Buffer.from(value.bytes).copy(output, offset);
      }
      assert.equal(hash(await fs.readFile(filename)), sha256, name + ' original changed');
      await fs.writeFile(path.join(outputDirectory, name + '.pdf'), output);
      const row = { name, pages, inputBytes: bytes.length, inputSHA256: sha256, outputBytes: output.length,
        outputSHA256: hash(output), warnings: preview.warnings, originalPreserved: true, elapsedMs: Date.now() - started };
      rows.push(row); console.log(JSON.stringify(row));
    } finally { broker.release({ readerId }); }
  }
} finally {
  await broker.close(); await fs.rm(directory, { recursive: true, force: true });
  await fs.writeFile(path.join(outputDirectory, 'matrix.json'), JSON.stringify({ schema_version: 1, platform: process.platform,
    architecture: process.arch, syntheticOnly: true, rows, observations, passed: rows.length === 2 }, null, 2) + '\n');
}
