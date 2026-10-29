import { useEffect, useRef, useState } from 'react';
import { Document, Page, pdfjs } from 'react-pdf';
import type { PDFDocumentProxy } from 'pdfjs-dist';
import { DocumentChannel } from './channel';
import 'react-pdf/dist/Page/TextLayer.css';
pdfjs.GlobalWorkerOptions.workerSrc = new URL('pdfjs-dist/build/pdf.worker.min.mjs', import.meta.url).toString();
const options = { disableAutoFetch: true, disableStream: true, rangeChunkSize: 65536, isEvalSupported: false, enableXfa: false, maxImageSize: 8_000_000, canvasMaxAreaInBytes: 32_000_000, cMapUrl: 'sparkclaw-preview://viewer/cmaps/', cMapPacked: true, standardFontDataUrl: 'sparkclaw-preview://viewer/standard_fonts/', wasmUrl: 'sparkclaw-preview://viewer/wasm/' };
export default function PdfPreview({ channel }: { channel: DocumentChannel }) {
  const zh = channel.document.language === 'zh';
  const [file, setFile] = useState<{ range: InstanceType<typeof pdfjs.PDFDataRangeTransport> }>();
  const [pdf, setPDF] = useState<PDFDocumentProxy>();
  const [error, setError] = useState('');
  const [page, setPage] = useState(Math.max(1, Math.floor(channel.document.position?.page || 1)));
  const [zoom, setZoom] = useState(Math.max(.25, Math.min(3, channel.document.position?.zoom || 1)));
  const [width, setWidth] = useState(600); const [pageWidth, setPageWidth] = useState<number>();
  const viewport = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const node = viewport.current; if (!node) return;
    const observer = new ResizeObserver(() => setWidth(Math.max(120, Math.min(1800, node.clientWidth-32)))); observer.observe(node);
    return () => observer.disconnect();
  }, []);
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
  useEffect(() => {
    let active = true; setPageWidth(undefined);
    if (error) return;
    if (pdf) void Promise.resolve().then(() => pdf.getPage(page)).then((item) => {
      const size = item.getViewport({ scale: 1 });
      // One page canvas, fixed DPR and bounded dimensions/pixels.
      const desired = width * zoom;
      const bounded = Math.min(desired, 4096, 4096 * size.width / size.height, Math.sqrt(8_000_000 * size.width / size.height));
      if (active) setPageWidth(Math.max(1, Math.floor(bounded)));
    }).catch((cause: Error) => { if (active) setError(previous => previous || cause.message); });
    channel.position({ page, zoom });
    return () => { active = false; };
  }, [pdf, page, width, zoom, channel, error]);
  if (error) return <p className="previewMessage" role="alert">{error}</p>;
  return <div className="pdfPreview"><div className="previewToolbar">
    <button type="button" disabled={page <= 1} onClick={() => setPage(page-1)} aria-label={zh ? '上一页' : 'Previous page'}>‹</button>
    <label>{zh ? '页' : 'Page'} <input aria-label={zh ? '页码' : 'Page number'} type="number" min={1} max={pdf?.numPages || 1} value={page} onChange={e => setPage(Math.max(1, Math.min(pdf?.numPages || 1, Number(e.target.value) || 1)))} /></label><span>/ {pdf?.numPages || '…'}</span>
    <button type="button" disabled={!pdf || page >= pdf.numPages} onClick={() => setPage(page+1)} aria-label={zh ? '下一页' : 'Next page'}>›</button>
    <button type="button" onClick={() => setZoom(Math.max(.25, zoom-.25))} aria-label={zh ? '缩小' : 'Zoom out'}>−</button><span>{Math.round(zoom*100)}%</span>
    <button type="button" onClick={() => setZoom(Math.min(3, zoom+.25))} aria-label={zh ? '放大' : 'Zoom in'}>＋</button>
    <button type="button" onClick={() => setZoom(1)}>{zh ? '适合宽度' : 'Fit width'}</button>
  </div><div className="pdfPageViewport" ref={viewport}>{file && <Document file={file} options={options} onLoadSuccess={value => { setPDF(value); setPage(current => Math.min(current, value.numPages)); }} onLoadError={cause => setError(cause.message)} loading={<p className="previewMessage">{zh ? '正在读取 PDF…' : 'Reading PDF…'}</p>}>
    {pageWidth && <Page key={page} pageNumber={page} width={pageWidth} devicePixelRatio={1} renderAnnotationLayer={false} renderTextLayer onLoadError={cause => setError(cause.message)} onRenderError={cause => setError(cause.message)} />}
  </Document>}</div></div>;
}
