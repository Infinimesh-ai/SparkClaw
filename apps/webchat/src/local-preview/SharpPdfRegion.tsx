import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import type { PDFDocumentProxy, RenderTask } from 'pdfjs-dist';
import { PdfRasterBudget, type Raster } from './StablePdfPage';

export type Region = { x: number; y: number; width: number; height: number };
type Detail = Raster & { pageWidth: number; region: Region; canvas: HTMLCanvasElement };

function Surface({ detail, width, visible }: { detail: Detail; width: number; visible: boolean }) {
  const host = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => { host.current?.replaceChildren(detail.canvas); }, [detail]);
  const scale = width / detail.pageWidth;
  return <div ref={host} className="pdfSharpRegion" data-pdf-sharp={visible ? '' : undefined} aria-hidden
    style={{ left: detail.region.x * scale, top: detail.region.y * scale, width: detail.region.width,
      height: detail.region.height, transform: `scale(${scale})`, visibility: visible ? 'visible' : 'hidden' }} />;
}

// Rasterize only the visible page region at screen density, with oversampling
// headroom for the next pinch frames. The completed detail remains visible
// while the next one renders, including during an ongoing gesture.
export function SharpPdfRegion({ pdf, number, width, height, region, pixelRatio, budget, onError }: {
  pdf: PDFDocumentProxy; number: number; width: number; height: number; region: Region;
  pixelRatio: number; budget: PdfRasterBudget; onError: (cause: Error) => void;
}) {
  const [faces, setFaces] = useState<{ front?: Detail; pending?: Detail }>({});
  const [revision, wake] = useState(0);
  const leases = useRef(new Map<number, Detail>()), task = useRef<RenderTask | null>(null);
  const alive = useRef(true);
  const error = useRef(onError); error.current = onError;
  useEffect(() => budget.subscribe(() => wake(value => value + 1)), [budget]);
  useEffect(() => () => {
    alive.current = false; task.current?.cancel();
    leases.current.forEach(value => { value.canvas.width = 0; value.canvas.height = 0; value.release(); }); leases.current.clear();
  }, []);
  useEffect(() => {
    for (const [id, value] of leases.current) if (id !== faces.front?.id && id !== faces.pending?.id) {
      value.canvas.width = 0; value.canvas.height = 0; value.release(); leases.current.delete(id);
    }
  }, [faces]);
  useEffect(() => {
    if (faces.pending || region.width <= 0 || region.height <= 0) return;
    const front = faces.front, scale = front ? width / front.pageWidth : 1;
    if (front && front.ratio / scale >= Math.min(pixelRatio / 1.1, (window.devicePixelRatio || 1) * 1.15) &&
      front.region.x * scale <= region.x + .5 && front.region.y * scale <= region.y + .5 &&
      (front.region.x + front.region.width) * scale >= region.x + region.width - .5 &&
      (front.region.y + front.region.height) * scale >= region.y + region.height - .5) return;
    const x = Math.max(0, region.x - 48), y = Math.max(0, region.y - 48);
    const clip = { x, y, width: Math.min(width, region.x + region.width + 48) - x, height: Math.min(height, region.y + region.height + 48) - y };
    const lease = budget.reserve(clip.width, clip.height, pixelRatio); if (!lease) return;
    const canvas = document.createElement('canvas');
    canvas.width = Math.ceil(clip.width * pixelRatio); canvas.height = Math.ceil(clip.height * pixelRatio);
    canvas.style.width = `${clip.width}px`; canvas.style.height = `${clip.height}px`;
    const detail: Detail = { ...lease, canvas, pageWidth: width, region: clip };
    leases.current.set(detail.id, detail); setFaces(previous => ({ ...previous, pending: detail }));
    void pdf.getPage(number).then(page => {
      if (!alive.current) return;
      const viewport = page.getViewport({ scale: width / page.getViewport({ scale: 1 }).width * pixelRatio });
      const render = page.render({ canvas, viewport, transform: [1, 0, 0, 1, -x * pixelRatio, -y * pixelRatio] });
      task.current = render;
      return render.promise.then(() => {
        if (alive.current) setFaces(previous => previous.pending?.id === detail.id ? { front: detail } : previous);
      });
    }).catch((cause: Error) => { if (alive.current) error.current(cause); });
  }, [pdf, number, width, height, region.x, region.y, region.width, region.height, pixelRatio, faces, budget, revision]);
  return <>{[faces.front, faces.pending].filter((value): value is Detail => Boolean(value)).map(detail =>
    <Surface key={detail.id} detail={detail} width={width} visible={detail.id === faces.front?.id} />)}</>;
}
