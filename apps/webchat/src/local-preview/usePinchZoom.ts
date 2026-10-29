import { useEffect, useRef, useState, type RefObject } from 'react';

export type ZoomPoint = { x: number; y: number };

// Chromium delivers trackpad pinch as a Ctrl-wheel event. A non-passive
// listener consumes only that gesture; ordinary two-finger scrolling keeps
// the native horizontal/vertical pan and momentum behavior.
export function usePinchZoom(viewport: RefObject<HTMLElement | null>, zoom: number,
  onZoom: (zoom: number, point: ZoomPoint, origin?: ZoomPoint) => void, min = .25, max = 3, onPan?: () => void) {
  const [active, setActive] = useState(false);
  const current = useRef({ zoom, onZoom, min, max, onPan });
  current.current = { zoom, onZoom, min, max, onPan };
  useEffect(() => {
    const node = viewport.current; if (!node) return;
    let frame = 0, idle = 0, sequenceZoom: number | undefined;
    let origin: ZoomPoint | undefined;
    let pending: { zoom: number; point: ZoomPoint } | undefined;
    const wheel = (event: WheelEvent) => {
      if (!event.ctrlKey) { current.current.onPan?.(); return; }
      if (!Number.isFinite(event.deltaY)) return;
      event.preventDefault();
      const bounds = node.getBoundingClientRect();
      const point = { x: event.clientX - bounds.left, y: event.clientY - bounds.top };
      if (sequenceZoom === undefined) { sequenceZoom = current.current.zoom; origin = point; setActive(true); }
      clearTimeout(idle);
      idle = window.setTimeout(() => { sequenceZoom = undefined; setActive(false); }, 160);
      const { min, max } = current.current;
      const delta = event.deltaY * (event.deltaMode === 1 ? 16 : event.deltaMode === 2 ? node.clientHeight : 1);
      sequenceZoom = Math.max(min, Math.min(max, sequenceZoom * Math.exp(-delta / 100)));
      pending = { zoom: sequenceZoom, point };
      if (!frame) frame = requestAnimationFrame(() => {
        frame = 0;
        const value = pending; pending = undefined;
        if (value && value.zoom !== current.current.zoom) { current.current.onZoom(value.zoom, value.point, origin); origin = undefined; }
      });
    };
    node.addEventListener('wheel', wheel, { passive: false });
    return () => { node.removeEventListener('wheel', wheel); cancelAnimationFrame(frame); clearTimeout(idle); };
  }, [viewport]);
  return active;
}
