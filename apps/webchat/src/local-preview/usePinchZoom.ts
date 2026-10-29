import { useEffect, useRef, type RefObject } from 'react';

export type ZoomPoint = { x: number; y: number };

// Chromium delivers trackpad pinch as a Ctrl-wheel event. A non-passive
// listener consumes only that gesture; ordinary two-finger scrolling keeps
// the native horizontal/vertical pan and momentum behavior.
export function usePinchZoom(viewport: RefObject<HTMLElement | null>, zoom: number,
  onZoom: (zoom: number, point: ZoomPoint) => void, min = .25, max = 3) {
  const current = useRef({ zoom, onZoom, min, max });
  current.current = { zoom, onZoom, min, max };
  useEffect(() => {
    const node = viewport.current; if (!node) return;
    let frame = 0;
    let pending: { zoom: number; point: ZoomPoint } | undefined;
    const wheel = (event: WheelEvent) => {
      if (!event.ctrlKey || !Number.isFinite(event.deltaY)) return;
      event.preventDefault();
      const { min, max } = current.current;
      const delta = event.deltaY * (event.deltaMode === 1 ? 16 : event.deltaMode === 2 ? node.clientHeight : 1);
      const next = Math.max(min, Math.min(max, (pending?.zoom ?? current.current.zoom) * Math.exp(-delta / 100)));
      const bounds = node.getBoundingClientRect();
      pending = { zoom: next, point: { x: event.clientX - bounds.left, y: event.clientY - bounds.top } };
      if (!frame) frame = requestAnimationFrame(() => {
        frame = 0;
        const value = pending; pending = undefined;
        if (value && value.zoom !== current.current.zoom) current.current.onZoom(value.zoom, value.point);
      });
    };
    node.addEventListener('wheel', wheel, { passive: false });
    return () => { node.removeEventListener('wheel', wheel); cancelAnimationFrame(frame); };
  }, [viewport]);
}
