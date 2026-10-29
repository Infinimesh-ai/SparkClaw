import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { Page } from 'react-pdf';
import type { PDFDocumentProxy, PDFPageProxy } from 'pdfjs-dist';
import type { DocumentChannel } from './channel';
import { usePinchZoom, type ZoomPoint } from './usePinchZoom';

const GAP = 8;
const MAX_CANVASES = 16;
const PIXEL_BUDGET = 8_000_000;
type Anchor = { page: number; offset: number };
type PageBox = { top: number; width: number; height: number };

function RenderedPage({ number, width, pixelRatio, onError }: { number: number; width: number; pixelRatio: number; onError: (cause: Error) => void }) {
  const loaded = useRef<PDFPageProxy | null>(null);
  useEffect(() => () => {
    // React-PDF cancels rendering and zeros its retired canvas; also release
    // PDF.js operator/image resources instead of retaining every visited page.
    loaded.current?.cleanup();
  }, []);
  return <Page pageNumber={number} width={width} devicePixelRatio={pixelRatio} suspense={false}
    renderAnnotationLayer={false} renderTextLayer loading={null} onLoadSuccess={value => { loaded.current = value; }}
    onLoadError={onError} onRenderError={onError} />;
}

function pageAt(boxes: PageBox[], top: number) {
  let low = 0, high = boxes.length - 1;
  while (low < high) {
    const mid = Math.ceil((low + high) / 2);
    if (boxes[mid].top <= top) low = mid; else high = mid - 1;
  }
  return low;
}

// Keep only a viewport window of canvases/text layers. The remaining pages are
// represented by one scroll surface and lightweight page dimensions.
export function PdfReader({ pdf, channel, onError }: { pdf: PDFDocumentProxy; channel: DocumentChannel; onError: (cause: Error) => void }) {
  const zh = channel.document.language === 'zh';
  const saved = channel.document.position;
  const anchor = useRef<Anchor>({ page: Math.min(pdf.numPages, Math.max(1, Math.floor(saved?.page || 1))), offset: Math.min(1, Math.max(0, saved?.offset || 0)) });
  const [page, setPage] = useState(anchor.current.page);
  const [zoom, setZoom] = useState(Math.max(.25, Math.min(3, saved?.zoom || 1)));
  const [view, setView] = useState({ width: 600, height: 800, top: 0, left: saved?.scrollLeft || 0 });
  const [ratios, setRatios] = useState<Map<number, number>>(() => new Map());
  const viewport = useRef<HTMLDivElement>(null);
  const frame = useRef(0);
  const pinch = useRef<(Anchor & ZoomPoint & { horizontal: number }) | null>(null);
  const errorHandler = useRef(onError); errorHandler.current = onError;
  const boxes = useMemo(() => {
    let top = 0;
    return Array.from({ length: pdf.numPages }, (_, index) => {
      const ratio = ratios.get(index + 1) || Math.SQRT2;
      const width = Math.min(view.width * zoom, 4096, 4096 / ratio);
      const height = width * ratio;
      const box = { top, width, height };
      top += height + GAP;
      return box;
    });
  }, [pdf.numPages, view.width, zoom, ratios]);
  const totalHeight = boxes.at(-1)!.top + boxes.at(-1)!.height;
  const first = pageAt(boxes, view.top);
  const last = pageAt(boxes, view.top + view.height);
  const start = Math.max(0, first - 1);
  const end = Math.min(pdf.numPages - 1, last + 1, start + MAX_CANVASES - 1);
  const visiblePages = Array.from({ length: end - start + 1 }, (_, index) => start + index + 1);
  const pixels = visiblePages.reduce((sum, number) => sum + boxes[number - 1].width * boxes[number - 1].height, 0);
  const pixelRatio = Math.min(window.devicePixelRatio || 1, 2, Math.sqrt(PIXEL_BUDGET / pixels),
    ...visiblePages.map(number => 4096 / Math.max(boxes[number - 1].width, boxes[number - 1].height)));

  useLayoutEffect(() => {
    const node = viewport.current; if (!node) return;
    const update = () => setView(previous => ({ ...previous, width: Math.max(1, node.clientWidth), height: node.clientHeight }));
    node.scrollLeft = saved?.scrollLeft || 0;
    const observer = new ResizeObserver(update); observer.observe(node); update();
    return () => { observer.disconnect(); cancelAnimationFrame(frame.current); };
  }, []);

  const updateAnchor = (node: HTMLDivElement) => {
    const index = pageAt(boxes, node.scrollTop + 1), box = boxes[index];
    anchor.current = { page: index + 1, offset: Math.min(1, Math.max(0, (node.scrollTop - box.top) / box.height)) };
    setPage(index + 1);
    setView(previous => ({ ...previous, top: node.scrollTop, left: node.scrollLeft }));
  };
  usePinchZoom(viewport, zoom, (next, point) => {
    const node = viewport.current; if (!node) return;
    cancelAnimationFrame(frame.current);
    const index = pageAt(boxes, node.scrollTop + point.y), box = boxes[index];
    const left = (Math.max(view.width, view.width * zoom) - box.width) / 2;
    pinch.current = { ...point, page: index + 1, offset: (node.scrollTop + point.y - box.top) / box.height,
      horizontal: (node.scrollLeft + point.x - left) / box.width };
    setZoom(next);
  });

  // Pinch keeps the document point beneath the fingers stationary. Ordinary
  // panel resizing and lazy page measurements preserve the top reading anchor.
  useLayoutEffect(() => {
    const node = viewport.current; if (!node) return;
    const gesture = pinch.current;
    if (gesture) {
      const box = boxes[gesture.page - 1];
      node.scrollTop = box.top + gesture.offset * box.height - gesture.y;
      node.scrollLeft = (Math.max(view.width, view.width * zoom) - box.width) / 2 + gesture.horizontal * box.width - gesture.x;
      pinch.current = null;
      updateAnchor(node);
    } else {
      const box = boxes[anchor.current.page - 1];
      node.scrollTop = box.top + anchor.current.offset * box.height;
      node.scrollLeft = view.left;
      setView(previous => previous.top === node.scrollTop && previous.left === node.scrollLeft ? previous : { ...previous, top: node.scrollTop, left: node.scrollLeft });
    }
  }, [boxes]);

  useEffect(() => {
    let active = true;
    void (async () => {
      const loaded = new Map<number, number>();
      // Sequential metadata reads also respect the channel's concurrent budget.
      for (let number = start + 1; number <= end + 1 && active; number++) {
        if (ratios.has(number)) continue;
        const item = await pdf.getPage(number);
        if (!active) return;
        const size = item.getViewport({ scale: 1 });
        const ratio = size.height / size.width;
        if (!Number.isFinite(ratio) || ratio <= 0) throw new Error(zh ? 'PDF 页面尺寸无效。' : 'Invalid PDF page size.');
        loaded.set(number, ratio);
      }
      if (active && loaded.size) setRatios(previous => new Map([...previous, ...loaded]));
    })().catch((cause: Error) => { if (active) errorHandler.current(cause); });
    return () => { active = false; };
  }, [pdf, start, end, ratios, zh]);

  useEffect(() => {
    channel.position({ page, zoom, offset: anchor.current.offset, scrollTop: view.top, scrollLeft: view.left });
  }, [channel, page, zoom, view.top, view.left]);

  const captureScroll = () => {
    cancelAnimationFrame(frame.current);
    frame.current = requestAnimationFrame(() => {
      const node = viewport.current; if (!node) return;
      updateAnchor(node);
    });
  };
  return <div className="pdfPreview">
    <div className="pdfPageViewport" ref={viewport} onScroll={captureScroll} tabIndex={0} aria-label={zh ? '文档内容' : 'Document content'}>
      <div className="pdfPageStack" style={{ height: totalHeight, minWidth: view.width * zoom }}>
        {visiblePages.map(number => {
          const box = boxes[number - 1];
          return <div className="pdfPageSlot" key={number} data-page={number} style={{ top: box.top, width: box.width, height: box.height }}>
            {ratios.has(number) && <RenderedPage number={number} width={box.width} pixelRatio={pixelRatio} onError={onError} />}
          </div>;
        })}
      </div>
    </div>
  </div>;
}
