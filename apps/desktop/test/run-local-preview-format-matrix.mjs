import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import crypto from 'node:crypto';
import { pathToFileURL, fileURLToPath } from 'node:url';
import ExcelJS from 'exceljs';

const fixtureDirectory = path.resolve(process.argv[2] || '');
const outputDirectory = path.resolve(process.argv[3] || '');
if (process.argv.length < 4 || fixtureDirectory === outputDirectory) throw new Error('Usage: node run-local-preview-format-matrix.mjs FIXTURES OUTPUT');
const moduleDirectory = process.env.SPARKCLAW_FORMAT_MODULES || path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../src/main');
const { LocalPreviewBroker } = await import(pathToFileURL(path.join(moduleDirectory, 'local-preview-broker.mjs')).href);
const hash = bytes => crypto.createHash('sha256').update(bytes).digest('hex');
await fs.mkdir(outputDirectory, { recursive: true });
const directory = await fs.realpath(await fs.mkdtemp(path.join(os.tmpdir(), 'sparkx-format-matrix-')));
let current;
const observations = [];
const broker = new LocalPreviewBroker({ directory, workerRuntime: process.env.SPARKCLAW_PREVIEW_RUNTIME,
  getScope: () => 'synthetic-format-qualification', stat: async () => current.metadata,
  snapshot: async () => ({ bytes: Buffer.from(current.bytes) }), onObservation: value => observations.push(value) });
const cases = [
  ['complex.docx', 'office', 'pdf', 2], ['complex.doc', 'office', 'pdf', 2],
  ['complex.pptx', 'office', 'pdf', 3], ['complex.ppt', 'office', 'pdf', 3],
  ['missing-font.docx', 'office', 'pdf', 1],
  ...['encrypted.docx', 'encrypted.pptx', 'corrupt.docx', 'corrupt.pptx', 'corrupt.doc', 'corrupt.ppt', 'wrong-signature.doc', 'wrong-signature.ppt'].map(name => [name, 'office', 'rejected']),
  ...['embedded.docx', 'embedded.pptx'].map(name => [name, 'office', 'unsupported']),
  ['valid.csv', 'csv', 'spreadsheet'], ['invalid.csv', 'csv', 'invalid_document'], ['invalid-utf8.csv', 'csv', 'unsupported_encoding'],
];
const book = new ExcelJS.Workbook(), sheet = book.addWorksheet('中文');
sheet.getCell('A1').value = { formula: '1-1', result: 0 };
sheet.getCell('B1').value = { formula: '1=2', result: false };
sheet.getCell('A2').value = { formula: 'A1+7' };
sheet.getCell('B2').value = '00123'; sheet.getCell('B2').font = { bold: true };
sheet.mergeCells('A3:B3'); sheet.getCell('A3').value = 'MERGED';
book.addWorksheet('Second').getCell('A1').value = 'SECOND SHEET';
await fs.writeFile(path.join(outputDirectory, 'complex.xlsx'), await book.xlsx.writeBuffer());
cases.push(['complex.xlsx', 'xlsx', 'spreadsheet']);
const rows = [];
try {
  for (const [name, kind, expected, pages] of cases) {
    const filename = path.join(name === 'complex.xlsx' ? outputDirectory : fixtureDirectory, name);
    const bytes = await fs.readFile(filename), sha256 = hash(bytes), started = Date.now();
    current = { bytes, metadata: { ref: name, version: sha256, locator: filename, name, kind: 'file', size: bytes.length } };
    const readerId = `format-${rows.length}`;
    const row = { name, kind, expected, inputBytes: bytes.length, inputSHA256: sha256 };
    try {
      const preview = await broker.prepare({ ref: name, version: sha256, kind, readerId });
      row.actual = preview.kind; row.pages = preview.pageCount; row.warnings = preview.warnings;
      assert.equal(preview.kind, expected, name);
      if (pages) assert.equal(preview.pageCount, pages, name+' page count');
      if (preview.kind === 'pdf') {
        const output = Buffer.alloc(preview.size);
        for (let offset = 0; offset < preview.size; offset += 1024*1024) {
          const value = await broker.read({ previewId: preview.previewId, readerId, offset, length: Math.min(1024*1024, preview.size-offset) });
          Buffer.from(value.bytes).copy(output, offset);
        }
        row.output = name+'.pdf'; row.outputBytes = output.length; row.outputSHA256 = hash(output);
        await fs.writeFile(path.join(outputDirectory, row.output), output);
        if (name === 'missing-font.docx') assert.ok(preview.warnings.some(value => value.includes('SparkX Missing Font QA 20261029')));
      } else {
        row.output = name+'.json'; await fs.writeFile(path.join(outputDirectory, row.output), JSON.stringify(preview.model));
        if (name === 'complex.xlsx') {
          const cells = preview.model.sheets[0].cells;
          assert.equal(cells.find(cell=>cell.row===0&&cell.column===0).value,0);
          assert.equal(cells.find(cell=>cell.row===0&&cell.column===1).value,false);
          assert.equal(cells.find(cell=>cell.row===1&&cell.column===0).missingCachedValue,true);
          assert.equal(preview.model.sheets.length,2);
        }
      }
    } catch (error) {
      row.actual = 'rejected'; row.errorCode = error.code || 'assertion'; row.errorMessage = error.message;
      if (expected === 'rejected') row.failure = !['invalid_document','unsupported','preview_failed'].includes(error.code);
      else if (['unsupported','invalid_document','unsupported_encoding'].includes(expected)) row.failure = error.code !== expected;
      else row.failure = true;
    } finally { broker.release({ readerId }); }
    row.elapsedMs = Date.now()-started;
    assert.equal(hash(await fs.readFile(filename)),sha256,name+' original changed');
    row.originalPreserved = true; rows.push(row);
    console.log(JSON.stringify(row));
  }
} finally {
  await broker.close(); await fs.rm(directory,{recursive:true,force:true});
  await fs.writeFile(path.join(outputDirectory,'matrix.json'),JSON.stringify({schema_version:1,platform:process.platform,architecture:process.arch,syntheticOnly:true,engine:'0.2.3',rows,observations,passed:rows.length===cases.length&&rows.every(row=>!row.failure)},null,2)+'\n');
}
assert.ok(rows.every(row=>!row.failure),'One or more conversion matrix rows failed; inspect matrix.json');
