import { useEffect, useState } from 'react';
import { Document, pdfjs } from 'react-pdf';
import type { PDFDocumentProxy } from 'pdfjs-dist';
import { DocumentChannel } from './channel';
import 'react-pdf/dist/Page/TextLayer.css';
import { PdfReader } from './PdfReader';
pdfjs.GlobalWorkerOptions.workerSrc = new URL('pdfjs-dist/build/pdf.worker.min.mjs', import.meta.url).toString();
const options = { disableAutoFetch: true, disableStream: true, rangeChunkSize: 65536, isEvalSupported: false, enableXfa: false, maxImageSize: 8_000_000, canvasMaxAreaInBytes: 32_000_000, cMapUrl: 'sparkclaw-preview://viewer/cmaps/', cMapPacked: true, standardFontDataUrl: 'sparkclaw-preview://viewer/standard_fonts/', wasmUrl: 'sparkclaw-preview://viewer/wasm/' };
export default function PdfPreview({ channel }: { channel: DocumentChannel }) {
  const zh = channel.document.language === 'zh';
  const [file, setFile] = useState<{ range: InstanceType<typeof pdfjs.PDFDataRangeTransport> }>();
  const [pdf, setPDF] = useState<PDFDocumentProxy>();
  const [error, setError] = useState('');
  useEffect(() => {
    let active = true; let transport: InstanceType<typeof pdfjs.PDFDataRangeTransport> | undefined;
    void (async () => {
      if (channel.document.size > 64 * 1024 * 1024) throw new Error(zh ? 'PDF 超出 64 MiB 预览限制。' : 'PDF exceeds the 64 MiB preview limit.');
      const initial = await channel.read(0, Math.min(channel.document.size, 65536));
      if (new TextDecoder().decode(initial.slice(0, 5)) !== '%PDF-') throw new Error(zh ? '文件不是有效的 PDF。' : 'File does not have a PDF signature.');
      class Range extends pdfjs.PDFDataRangeTransport {
        stopped = false;
        queued = 0;
        pending = Promise.resolve();
        requestDataRange(begin: number, end: number) {
          if (this.stopped) return;
          if (!Number.isSafeInteger(begin) || !Number.isSafeInteger(end) || begin < 0 || end <= begin || end > channel.document.size || this.queued >= 64) {
            this.fail(new Error(zh ? 'PDF 读取范围超出预览限制。' : 'PDF range exceeds the preview limits.'));
            return;
          }
          this.queued++;
          // PDF.js consumes one complete response for each requested range.
          // Serialize ranges so only one bounded assembly buffer is retained and
          // PDF reads cannot exhaust the channel's concurrent-request budget.
          this.pending = this.pending.then(async () => {
            if (this.stopped) return;
            const range = new Uint8Array(end - begin);
            for (let offset = begin; offset < end && !this.stopped; offset += 65536) {
              const bytes = await channel.read(offset, Math.min(65536, end-offset));
              if (!this.stopped) range.set(bytes, offset-begin);
            }
            if (!this.stopped) this.onDataRange(begin, range);
          }).catch((cause: Error) => this.fail(cause)).finally(() => { this.queued--; });
        }
        fail(cause: Error) { this.abort(); if (active) setError(previous => previous || cause.message); }
        abort() { this.stopped = true; }
      }
      transport = new Range(channel.document.size, initial, true);
      if (active) setFile({ range: transport }); else transport.abort();
    })().catch((cause: Error) => { if (active) setError(cause.message); });
    return () => { active = false; transport?.abort(); };
  }, [channel, zh]);
  if (error) return <p className="previewMessage" role="alert">{error}</p>;
  return file ? <Document className="pdfDocument" file={file} options={options}
    onPassword={() => {
      const message = zh ? '此 PDF 需要密码。请先解锁并另存未加密副本后预览。' : 'This PDF requires a password. Unlock it and save an unencrypted copy before previewing.';
      setError(message); throw new Error(message);
    }}
    onLoadSuccess={value => {
      if (value.numPages > 10000) setError(zh ? 'PDF 超出 10,000 页预览限制。' : 'PDF exceeds the 10,000 page preview limit.');
      else setPDF(value);
    }} onLoadError={cause => setError(previous => previous || cause.message)} loading={<p className="previewMessage">{zh ? '正在读取 PDF…' : 'Reading PDF…'}</p>}>
    {pdf && <PdfReader pdf={pdf} channel={channel} onError={cause => setError(previous => previous || cause.message)} />}
  </Document> : null;
}
