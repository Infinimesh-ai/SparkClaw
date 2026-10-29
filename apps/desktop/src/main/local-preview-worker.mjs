import { readFile, writeFile, stat } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { previewError, previewLimits } from './local-preview-limits.mjs';

export async function runPreviewWorker(request) {
  const { kind, inputPath, outputPath, limits: overrides } = request;
  const limits = previewLimits(overrides);
  if (!['office', 'xlsx', 'csv', 'tsv'].includes(kind) || !path.isAbsolute(inputPath) || !path.isAbsolute(outputPath) ||
      path.dirname(inputPath) !== process.cwd() || path.dirname(outputPath) !== process.cwd()) {
    throw previewError('invalid_request', 'Invalid private preview paths.');
  }
  const metadata = await stat(inputPath);
  if (!metadata.isFile() || metadata.size > limits.inputBytes) throw previewError('limit_exceeded', 'Invalid or oversized preview input.');
  const bytes = await readFile(inputPath);
  if (kind !== 'office') {
    const { parseSpreadsheet } = await import('./local-preview-spreadsheet.mjs');
    const model = await parseSpreadsheet(bytes, kind, limits);
    await writeFile(outputPath, JSON.stringify(model), { flag: 'wx', mode: 0o600 });
    return { kind: 'spreadsheet', warnings: model.warnings };
  }
  const suffix = path.extname(inputPath).toLowerCase();
  if (!['.doc', '.docx', '.ppt', '.pptx'].includes(suffix)) throw previewError('unsupported', 'This Office format is not supported.');
  if (suffix.endsWith('x')) {
    const { inspectPreviewArchive } = await import('./local-preview-zip.mjs');
    const archive = await inspectPreviewArchive(bytes, limits);
    if (!archive.names.has('[Content_Types].xml') || !archive.names.has(suffix === '.docx' ? 'word/document.xml' : 'ppt/presentation.xml')) {
      throw previewError('invalid_document', 'The file does not match its Office format.');
    }
    if ([...archive.names].some(name => /vbaProject|activeX|embeddings\//i.test(name))) throw previewError('unsupported', 'Macros and embedded objects are not supported in this preview.');
  } else if (!bytes.subarray(0, 8).equals(Buffer.from('d0cf11e0a1b11ae1', 'hex'))) {
    throw previewError('invalid_document', 'The file is not a binary Office document.');
  }
  const { createConverter, ENGINE_VERSION } = await import('@deepseek-ai/libreoffice-kit');
  if (ENGINE_VERSION !== '0.2.3') throw previewError('preview_unavailable', 'The installed Office API and engine versions are not qualified.');
  const converter = await createConverter({ timeoutMs: limits.deadlineMs,
    maxInputBytes: limits.inputBytes, maxOutputBytes: limits.outputBytes,
    maxArchiveEntries: limits.archiveEntries, maxUncompressedBytes: limits.archiveBytes,
    fontDirectories: request.fontDirectories, fontMetadataCacheDirectory: false,
    maxFontFiles: 2048, maxFontFileBytes: 32 * 1024 * 1024, maxLoadedFontBytes: 128 * 1024 * 1024,
    maxImageResolution: 120 });
  try {
    const result = await converter.render({ inputPath, outputPath });
    const output = await stat(outputPath);
    if (!output.isFile() || output.size > limits.outputBytes) throw previewError('limit_exceeded', 'The converted PDF exceeds the preview limit.');
    return { kind: 'pdf', pageCount: result.pageCount, warnings: result.missingFonts.length ? [`Missing local fonts: ${result.missingFonts.slice(0, 20).join(', ')}`] : [] };
  } finally { await converter.dispose(); }
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const request = JSON.parse(await readFile(path.join(process.cwd(), 'request.json'), 'utf8'));
    const result = await runPreviewWorker(request);
    await writeFile(path.join(process.cwd(), 'result.json'), JSON.stringify({ ok: true, ...result }), { flag: 'wx', mode: 0o600 });
  } catch (error) {
    // Original paths, parser diagnostics and document fragments never leave the worker.
    const codes = new Set(['invalid_document', 'limit_exceeded', 'unsupported', 'unsupported_encoding', 'preview_unavailable']);
    await writeFile(path.join(process.cwd(), 'result.json'), JSON.stringify({ ok: false, error: {
      code: codes.has(error.code) ? error.code : 'preview_failed',
      message: codes.has(error.code) ? error.message : 'The local document preview could not be created.',
    } }), { flag: 'wx', mode: 0o600 }).catch(() => {});
    process.exitCode = 1;
  }
}
