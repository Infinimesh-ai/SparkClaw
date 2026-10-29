import { memo, useCallback, useEffect, useRef, useState } from 'react';
import { Page } from 'react-pdf';
import type { PDFPageProxy } from 'pdfjs-dist';

type Raster = { id: number; width: number; ratio: number; pixels: number; release: () => void };

// Both the visible raster and its replacement count toward the same limit.
export class PdfRasterBudget {
  private pixels = 0;
  private count = 0;
  private next = 0;
  private listeners = new Set<() => void>();
  reserve(width: number, height: number, ratio: number): Raster | undefined {
    const pixels = Math.ceil(width * ratio) * Math.ceil(height * ratio);
    if (this.count >= 32 || this.pixels + pixels > 8_000_000) return;
    this.pixels += pixels; this.count++;
    let released = false;
    return { id: ++this.next, width, ratio, pixels, release: () => {
      if (released) return; released = true;
      this.pixels -= pixels; this.count--;
      this.listeners.forEach(listener => listener());
    } };
  }
  subscribe(listener: () => void) { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; }
}

const RasterPage = memo(function RasterPage({ number, raster, ready, loaded, onError }: {
  number: number; raster: Raster; ready: (id: number) => void; loaded: (page: PDFPageProxy) => void; onError: (cause: Error) => void;
}) {
  const complete = useRef({ canvas: false, text: false });
  const finish = (part: 'canvas' | 'text') => {
    complete.current[part] = true;
    if (complete.current.canvas && complete.current.text) ready(raster.id);
  };
  return <Page pageNumber={number} width={raster.width} devicePixelRatio={raster.ratio} suspense={false}
    _enableRegisterUnregisterPage={false} renderAnnotationLayer={false} renderTextLayer loading={null}
    onLoadSuccess={loaded} onRenderSuccess={() => finish('canvas')} onRenderTextLayerSuccess={() => finish('text')}
    onGetTextError={() => finish('text')} onRenderTextLayerError={() => finish('text')}
    onLoadError={onError} onRenderError={onError} />;
});

export function StablePdfPage({ number, width, height, pixelRatio, busy, budget, onError }: {
  number: number; width: number; height: number; pixelRatio: number; busy: boolean; budget: PdfRasterBudget; onError: (cause: Error) => void;
}) {
  const [faces, setFaces] = useState<{ front?: Raster; pending?: Raster }>({});
  const [revision, wake] = useState(0);
  const leases = useRef(new Map<number, Raster>());
  const page = useRef<PDFPageProxy | null>(null);
  const onLoaded = useCallback((value: PDFPageProxy) => { page.current = value; }, []);
  const ready = useCallback((id: number) => setFaces(previous => previous.pending?.id === id ? { front: previous.pending } : previous), []);
  useEffect(() => budget.subscribe(() => wake(value => value + 1)), [budget]);
  useEffect(() => () => { leases.current.forEach(value => value.release()); leases.current.clear(); page.current?.cleanup(); }, []);
  useEffect(() => {
    // Release only after the old DOM canvas has actually been retired.
    for (const [id, value] of leases.current) if (id !== faces.front?.id && id !== faces.pending?.id) { value.release(); leases.current.delete(id); }
  }, [faces]);
  useEffect(() => {
    if (faces.pending || (faces.front && (busy || Math.abs(faces.front.width - width) < .5 && Math.abs(faces.front.ratio - pixelRatio) < .01))) return;
    // Retain a completed front during the whole gesture. Existing back renders
    // finish normally; they can be promoted at any live CSS scale without a gap.
    const raster = budget.reserve(width, height, pixelRatio);
    if (!raster) return;
    leases.current.set(raster.id, raster);
    setFaces(previous => ({ ...previous, pending: raster }));
  }, [width, height, pixelRatio, busy, faces, budget, revision]);
  return <>{[faces.front, faces.pending].filter((value): value is Raster => Boolean(value)).map(raster =>
    <div key={raster.id} className="pdfRaster" data-pdf-front={raster.id === faces.front?.id ? '' : undefined}
      aria-hidden={raster.id !== faces.front?.id} style={{ width: raster.width, transform: `scale(${width / raster.width})`, visibility: raster.id === faces.front?.id ? 'visible' : 'hidden' }}>
      <RasterPage number={number} raster={raster} ready={ready} loaded={onLoaded} onError={onError} />
    </div>)}</>;
}
