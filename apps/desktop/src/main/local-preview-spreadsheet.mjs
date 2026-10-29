import ExcelJS from 'exceljs';
import Papa from 'papaparse';
import { inspectPreviewArchive } from './local-preview-zip.mjs';
import { previewError, previewLimits } from './local-preview-limits.mjs';

function text(value, limits) {
  const result = String(value ?? '');
  if (result.length > limits.cellText) throw previewError('limit_exceeded', 'A cell exceeds the text preview limit.');
  return result;
}
function scalar(value, limits) {
  if (value == null || typeof value === 'boolean' || typeof value === 'number' && Number.isFinite(value)) return value ?? null;
  if (value instanceof Date) return value.toISOString();
  if (typeof value === 'string') return text(value, limits);
  if (value.richText) return text(value.richText.map(part => part.text).join(''), limits);
  if (value.error) return text(value.error, limits);
  if (value.hyperlink) return text(value.text ?? value.hyperlink, limits);
  return text(value, limits);
}
function color(value) { return /^[0-9a-f]{8}$/i.test(value?.argb ?? '') ? `#${value.argb.slice(2)}` : undefined; }
function style(value) {
  const result = {};
  if (value.font?.bold) result.bold = true;
  if (value.font?.italic) result.italic = true;
  if (color(value.font?.color)) result.color = color(value.font.color);
  if (value.fill?.type === 'pattern' && value.fill.pattern === 'solid' && color(value.fill.fgColor)) result.background = color(value.fill.fgColor);
  if (Number.isFinite(value.font?.size)) result.fontSize = Math.min(48, Math.max(8, value.font.size));
  if (['left', 'center', 'right'].includes(value.alignment?.horizontal)) result.align = value.alignment.horizontal;
  if (typeof value.numFmt === 'string') result.numberFormat = value.numFmt.slice(0, 128);
  return Object.keys(result).length ? result : undefined;
}
function checkedModel(model, limits) {
  if (Buffer.byteLength(JSON.stringify(model)) > limits.modelBytes) throw previewError('limit_exceeded', 'The table preview exceeds its memory budget.');
  return model;
}

export async function parseSpreadsheet(bytes, kind, overrides = {}) {
  const limits = previewLimits(overrides);
  if (bytes.length > limits.inputBytes) throw previewError('limit_exceeded', 'The file exceeds the table preview limit.');
  if (kind === 'csv' || kind === 'tsv') return parseDelimited(bytes, kind, limits);
  if (kind !== 'xlsx') throw previewError('unsupported', 'This spreadsheet format is not supported.');
  const archive = await inspectPreviewArchive(bytes, limits);
  if (!archive.names.has('[Content_Types].xml') || !archive.names.has('xl/workbook.xml')) {
    throw previewError('invalid_document', 'The archive is not an XLSX workbook.');
  }
  if ([...archive.names].some(name => /vbaProject|activeX|embeddings\//i.test(name))) {
    throw previewError('unsupported', 'Macros, ActiveX and embedded objects are not supported in table previews.');
  }
  const warnings = [];
  if ([...archive.names].some(name => /(?:charts|drawings|pivotTables|pivotCache|externalLinks)\//.test(name))) {
    warnings.push('Charts, drawings, pivot features and external links are not displayed.');
  }
  const workbook = new ExcelJS.Workbook();
  await workbook.xlsx.load(bytes);
  if (workbook.worksheets.length > limits.sheets) throw previewError('limit_exceeded', 'The workbook has too many sheets.');
  let area = 0, count = 0, estimatedBytes = 0;
  const sheets = workbook.worksheets.map((sheet, index) => {
    const rowCount = Math.max(1, sheet.rowCount), columnCount = Math.max(1, sheet.columnCount);
    area += rowCount * columnCount;
    if (!Number.isSafeInteger(area) || area > limits.rectangle) throw previewError('limit_exceeded', 'The worksheet area exceeds the preview limit.');
    const cells = [];
    if (sheet.conditionalFormattings?.length) warnings.push('Conditional formatting is not displayed.');
    sheet.eachRow(row => row.eachCell(cell => {
      if (cell.isMerged && cell.master !== cell) return;
      if (++count > limits.cells) throw previewError('limit_exceeded', 'The workbook contains too many cells.');
      const raw = cell.value;
      const record = { row: cell.row - 1, column: cell.col - 1, value: scalar(raw, limits) };
      if (raw && typeof raw === 'object' && ('formula' in raw || 'sharedFormula' in raw)) {
        record.formula = text(cell.formula ?? raw.formula ?? raw.sharedFormula, limits);
        // ExcelJS Cell.value omits falsy cached results; its parsed model keeps them.
        record.missingCachedValue = !Object.hasOwn(cell.model, 'result');
        record.value = record.missingCachedValue ? null : scalar(cell.model.result, limits);
      }
      const formatting = style(cell.style);
      if (formatting) record.style = formatting;
      estimatedBytes += Buffer.byteLength(JSON.stringify(record));
      if (estimatedBytes > limits.modelBytes) throw previewError('limit_exceeded', 'The table preview exceeds its memory budget.');
      cells.push(record);
    }));
    const merges = (sheet.model.merges ?? []).map(range => {
      const match = /^([A-Z]+)(\d+):([A-Z]+)(\d+)$/.exec(range);
      if (!match) throw previewError('invalid_document', 'Invalid merged cell range.');
      const col = letters => [...letters].reduce((n, c) => n * 26 + c.charCodeAt(0) - 64, 0) - 1;
      const row = Number(match[2]) - 1, column = col(match[1]);
      const rowSpan = Number(match[4]) - row, columnSpan = col(match[3]) - column + 1;
      if (rowSpan < 1 || columnSpan < 1 || row + rowSpan > rowCount || column + columnSpan > columnCount) throw previewError('invalid_document', 'Invalid merged cell bounds.');
      return { row, column, rowSpan, columnSpan };
    });
    return { id: String(index), name: text(sheet.name, limits), rowCount, columnCount, cells, merges };
  });
  return checkedModel({ kind: 'spreadsheet', sheets, warnings: [...new Set(warnings)] }, limits);
}

function parseDelimited(bytes, kind, limits) {
  let input;
  try { input = new TextDecoder('utf-8', { fatal: true }).decode(bytes); }
  catch { throw previewError('unsupported_encoding', 'Save this table as UTF-8 before previewing it.'); }
  if (input.includes('\0')) throw previewError('invalid_document', 'The table contains binary data.');
  const cells = [];
  let rowCount = 0, columnCount = 0, estimatedBytes = 0, failure;
  Papa.parse(input, { delimiter: kind === 'tsv' ? '\t' : ',', dynamicTyping: false, skipEmptyLines: false,
    step(result, parser) {
      try {
        if (result.errors.length) throw previewError('invalid_document', 'The table has malformed quoted fields.');
        columnCount = Math.max(columnCount, result.data.length);
        if (++rowCount * columnCount > limits.rectangle || cells.length + result.data.length > limits.cells) throw previewError('limit_exceeded', 'The table exceeds the preview area limit.');
        result.data.forEach((value, column) => {
          const cell = { row: rowCount - 1, column, value: text(value, limits) };
          estimatedBytes += Buffer.byteLength(JSON.stringify(cell));
          if (estimatedBytes > limits.modelBytes) throw previewError('limit_exceeded', 'The table exceeds its preview memory budget.');
          cells.push(cell);
        });
      } catch (error) { failure = error; parser.abort(); }
    },
  });
  if (failure) throw failure;
  return checkedModel({ kind: 'spreadsheet', sheets: [{ id: '0', name: 'Table', rowCount: Math.max(1, rowCount), columnCount: Math.max(1, columnCount), cells, merges: [] }], warnings: [] }, limits);
}
