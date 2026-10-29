import test from 'node:test';
import assert from 'node:assert/strict';
import ExcelJS from 'exceljs';
import JSZip from 'jszip';
import { parseSpreadsheet } from '../src/main/local-preview-spreadsheet.mjs';
import { inspectPreviewArchive } from '../src/main/local-preview-zip.mjs';

test('CSV preserves literal values, quoted newlines and formula-looking strings', async () => {
  const model = await parseSpreadsheet(Buffer.from('code,value,note\r\n0012,=1+1,"two\nlines"\r\n'), 'csv');
  assert.equal(model.sheets[0].cells.find(cell => cell.row === 1 && cell.column === 0).value, '0012');
  assert.equal(model.sheets[0].cells.find(cell => cell.row === 1 && cell.column === 1).value, '=1+1');
  assert.equal(model.sheets[0].cells.find(cell => cell.row === 1 && cell.column === 2).value, 'two\nlines');
  assert.ok(model.sheets[0].cells.every(cell => !Object.hasOwn(cell, 'formula')));
});

test('TSV and invalid encoding/quoting/rectangle fail explicitly', async () => {
  assert.equal((await parseSpreadsheet(Buffer.from('0001\tfalse'), 'tsv')).sheets[0].cells[1].value, 'false');
  await assert.rejects(parseSpreadsheet(Buffer.from([0xff, 0xfe, 0x80]), 'csv'), { code: 'unsupported_encoding' });
  await assert.rejects(parseSpreadsheet(Buffer.from('"unclosed'), 'csv'), { code: 'invalid_document' });
  await assert.rejects(parseSpreadsheet(Buffer.from('a,b\nc,d'), 'csv', { rectangle: 3 }), { code: 'limit_exceeded' });
  await assert.rejects(parseSpreadsheet(Buffer.from('1234'), 'csv', { cellText: 3 }), { code: 'limit_exceeded' });
});

test('XLSX retains saved zero/false, missing results, merges and style without recalculation', async () => {
  const book = new ExcelJS.Workbook(); const sheet = book.addWorksheet('中文');
  sheet.getCell('A1').value = { formula: '1-1', result: 0 };
  sheet.getCell('B1').value = { formula: '1=2', result: false };
  sheet.getCell('A2').value = { formula: 'A1+7' };
  sheet.getCell('B2').value = '0012'; sheet.getCell('B2').font = { bold: true, color: { argb: 'FFFF0000' } };
  sheet.mergeCells('A3:B3'); sheet.getCell('A3').value = 'merged';
  book.addWorksheet('Second').getCell('A1').value = 'sheet 2';
  const bytes = Buffer.from(await book.xlsx.writeBuffer()); const before = Buffer.from(bytes);
  const model = await parseSpreadsheet(bytes, 'xlsx');
  const cells = model.sheets[0].cells;
  assert.deepEqual(cells.find(cell => cell.row === 0 && cell.column === 0), { row: 0, column: 0, value: 0, formula: '1-1', missingCachedValue: false });
  assert.equal(cells.find(cell => cell.row === 0 && cell.column === 1).value, false);
  assert.equal(cells.find(cell => cell.row === 1 && cell.column === 0).missingCachedValue, true);
  assert.equal(cells.find(cell => cell.row === 1 && cell.column === 1).value, '0012');
  assert.equal(cells.find(cell => cell.row === 1 && cell.column === 1).style.color, '#FF0000');
  assert.deepEqual(model.sheets[0].merges, [{ row: 2, column: 0, rowSpan: 1, columnSpan: 2 }]);
  assert.equal(model.sheets.length, 2); assert.deepEqual(bytes, before);
});

test('archive validates every entry and stops dishonest expansion before workbook parsing', async () => {
  const zip = new JSZip(); zip.file('unreferenced.bin', Buffer.alloc(64 * 1024, 65));
  const bytes = await zip.generateAsync({ type: 'nodebuffer', compression: 'DEFLATE' });
  await assert.rejects(inspectPreviewArchive(bytes, { archiveEntryBytes: 1024 }), { code: 'limit_exceeded' });
  const forged = Buffer.from(bytes), central = forged.indexOf(Buffer.from('504b0102', 'hex'));
  forged.writeUInt32LE(1, central + 24);
  await assert.rejects(inspectPreviewArchive(forged), { code: 'limit_exceeded' });
  const corrupt = Buffer.from(bytes); corrupt.writeUInt32LE(0, central + 16);
  await assert.rejects(inspectPreviewArchive(corrupt), { code: 'invalid_document' });
  await assert.rejects(inspectPreviewArchive(bytes, { archiveBytes: 1024 }), { code: 'limit_exceeded' });
});

test('archive limits entries and rejects binary or wrong Office containers', async () => {
  const zip = new JSZip(); zip.file('one', 'a'); zip.file('two', 'b');
  const bytes = await zip.generateAsync({ type: 'nodebuffer' });
  await assert.rejects(inspectPreviewArchive(bytes, { archiveEntries: 1 }), { code: 'limit_exceeded' });
  await assert.rejects(parseSpreadsheet(bytes, 'xlsx'), { code: 'invalid_document' });
  await assert.rejects(inspectPreviewArchive(Buffer.from('not a zip')), { code: 'invalid_document' });
});
