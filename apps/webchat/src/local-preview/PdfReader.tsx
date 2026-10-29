import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import type { PDFDocumentProxy } from 'pdfjs-dist';
import type { DocumentChannel } from './channel';
import { usePinchZoom, type ZoomPoint } from './usePinchZoom';
import { PdfRasterBudget, StablePdfPage } from './StablePdfPage';
import { ZoomControl } from './ZoomControl';
import { scrollPadding } from './zoomGeometry';

const GAP = 8;
const MAX_CANVASES = 16;
const FRAME_PIXEL_BUDGET = 3_900_000; // Leave room for the completed frame and rounding.
type Anchor = { page: number; offset: number };
type PageBox = { top: number; width: number; height: number };

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
  const [padding, setPadding] = useState({ left: 0, right: 0, top: 0, bottom: 0 });
  const [rasterBudget] = useState(() => new PdfRasterBudget());
  const viewport = useRef<HTMLDivElement>(null);
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
  const stackWidth = Math.max(view.width, view.width * zoom);
  const first = pageAt(boxes, view.top);
  const last = pageAt(boxes, view.top + view.height);
  const start = Math.max(0, first - 1);
  const end = Math.min(pdf.numPages - 1, last + 1, start + MAX_CANVASES - 1);
  const visiblePages = Array.from({ length: end - start + 1 }, (_, index) => start + index + 1);
  const pixels = visiblePages.reduce((sum, number) => sum + boxes[number - 1].width * boxes[number - 1].height, 0);
  const pixelRatio = Math.min(window.devicePixelRatio || 1, 2, Math.sqrt(FRAME_PIXEL_BUDGET / pixels),
    ...visiblePages.map(number => 4096 / Math.max(boxes[number - 1].width, boxes[number - 1].height)));

  useLayoutEffect(() => {
    const node = viewport.current; if (!node) return;
    const update = () => { pinch.current = null; setView(previous => ({ ...previous, width: Math.max(1, node.clientWidth), height: node.clientHeight })); };
    node.scrollLeft = saved?.scrollLeft || 0;
    const observer = new ResizeObserver(update); observer.observe(node); update();
    return () => observer.disconnect();
  }, []);

  const updateAnchor = (node: HTMLDivElement) => {
    const top = node.scrollTop - padding.top, left = node.scrollLeft - padding.left;
    const index = pageAt(boxes, top + 1), box = boxes[index];
    anchor.current = { page: index + 1, offset: (top - box.top) / box.height };
    setPage(index + 1);
    setView(previous => previous.top === top && previous.left === left ? previous : { ...previous, top, left });
  };
  const changeZoom = (next: number, point: ZoomPoint, origin?: ZoomPoint) => {
    const node = viewport.current; if (!node) return;
    if (origin || !pinch.current) {
      const from = origin || point;
      const top = node.scrollTop - padding.top, left = node.scrollLeft - padding.left;
      const index = pageAt(boxes, top + from.y), box = boxes[index];
      pinch.current = { ...point, page: index + 1, offset: (top + from.y - box.top) / box.height,
        horizontal: (left + from.x - (stackWidth - box.width) / 2) / box.width };
    } else Object.assign(pinch.current, point);
    setZoom(next);
  };
  const busy = usePinchZoom(viewport, zoom, changeZoom, .25, 3, () => { pinch.current = null; });

  // Pinch keeps the document point beneath the fingers stationary. Ordinary
  // panel resizing and lazy page measurements preserve the top reading anchor.
  useLayoutEffect(() => {
    const node = viewport.current; if (!node) return;
    const gesture = pinch.current;
    if (gesture) {
      const box = boxes[gesture.page - 1];
      const top = box.top + gesture.offset * box.height - gesture.y;
      const left = (stackWidth - box.width) / 2 + gesture.horizontal * box.width - gesture.x;
      const next = scrollPadding(left, top, stackWidth, totalHeight, view.width, view.height);
      // Native scroll offsets cannot be negative. Add only the edge space
      // needed to keep the same document point under the fingers at any scale.
      if (Object.keys(next).some(key => next[key as keyof typeof next] !== padding[key as keyof typeof next])) { setPadding(next); return; }
      node.scrollTop = top + padding.top;
      node.scrollLeft = left + padding.left;
      updateAnchor(node);
    } else {
      const box = boxes[anchor.current.page - 1];
      node.scrollTop = box.top + anchor.current.offset * box.height + padding.top;
      node.scrollLeft = view.left + padding.left;
      updateAnchor(node);
    }
  }, [boxes, padding]);

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
    channel.position({ page, zoom, offset: Math.max(0, Math.min(1, anchor.current.offset)), scrollTop: Math.max(0, view.top), scrollLeft: Math.max(0, view.left) });
  }, [channel, page, zoom, view.top, view.left]);

  return <div className="pdfPreview">
    <div className="pdfPageViewport" ref={viewport} onScroll={event => updateAnchor(event.currentTarget)} tabIndex={0} aria-label={zh ? '文档内容' : 'Document content'}>
      <div className="pdfPageStack" style={{ height: totalHeight + padding.top + padding.bottom, width: stackWidth + padding.left + padding.right }}>
        {visiblePages.map(number => {
          const box = boxes[number - 1];
          return <div className="pdfPageSlot" key={number} data-page={number} style={{ top: padding.top + box.top, left: padding.left + (stackWidth - box.width) / 2, width: box.width, height: box.height }}>
            {ratios.has(number) && <StablePdfPage number={number} width={box.width} height={box.height} pixelRatio={pixelRatio} busy={busy} budget={rasterBudget} onError={onError} />}
          </div>;
        })}
      </div>
    </div>
    <ZoomControl zoom={zoom} min={.25} max={3} active={busy} viewport={viewport} zh={zh} onZoom={next => {
      const point = { x: view.width / 2, y: view.height / 2 }; changeZoom(next, point, point);
    }} />
  </div>;
}
