import { useCallback, useRef, useState, type CSSProperties, type KeyboardEvent, type PointerEvent } from "react";

const STORAGE_KEY = "sparkx.inspector-width";
// Narrowest width kept after a drag or key press; releasing below HIDE_BELOW hides the panel.
const MIN_WIDTH = 240;
const HIDE_BELOW = 120;

function storedWidth(): number | null {
  try {
    const value = Number(window.localStorage.getItem(STORAGE_KEY));
    return Number.isFinite(value) && value > 0 ? value : null;
  } catch {
    return null;
  }
}

export function useInspectorWidth() {
  const [width, setWidth] = useState<number | null>(storedWidth);
  const [resizing, setResizing] = useState(false);
  const commit = useCallback((value: number) => {
    const next = Math.round(Math.max(MIN_WIDTH, value));
    setWidth(next);
    try { window.localStorage.setItem(STORAGE_KEY, String(next)); } catch { /* Width is a per-viewer convenience. */ }
  }, []);
  // A live drag may shrink the panel toward zero before it is hidden or committed.
  const preview = useCallback((value: number) => setWidth(Math.max(0, Math.round(value))), []);
  const style = width === null ? undefined : { "--inspector-width": `${width}px` } as CSSProperties;
  return { width, style, resizing, setResizing, commit, preview, restore: setWidth };
}

type ResizeState = ReturnType<typeof useInspectorWidth>;

export function InspectorResizeHandle({ resize, label, onHide }: { resize: ResizeState; label: string; onHide: () => void }) {
  const handleRef = useRef<HTMLDivElement | null>(null);
  const workspace = () => handleRef.current?.parentElement ?? null;
  const panel = () => workspace()?.querySelector<HTMLElement>(".taskInspector") ?? null;
  // The workbench may apply CSS zoom; pointer and layout rectangles are in zoomed pixels.
  const zoom = () => {
    const workbench = handleRef.current?.closest<HTMLElement>(".workbench");
    return workbench ? parseFloat(getComputedStyle(workbench).getPropertyValue("--workbench-zoom")) || 1 : 1;
  };
  // The panel may grow until it covers the whole conversation area.
  const maxWidth = () => (workspace()?.getBoundingClientRect().width ?? 0) / zoom();
  const currentWidth = () => (panel()?.getBoundingClientRect().width ?? 0) / zoom();

  const onPointerDown = (event: PointerEvent<HTMLDivElement>) => {
    const element = panel();
    if (!element || event.button !== 0) return;
    event.preventDefault();
    const right = element.getBoundingClientRect().right;
    const scale = zoom();
    const limit = maxWidth();
    const before = resize.width;
    const target = event.currentTarget;
    target.setPointerCapture(event.pointerId);
    resize.setResizing(true);
    let latest = currentWidth();
    const stop = () => {
      target.removeEventListener("pointermove", move);
      target.removeEventListener("pointerup", stop);
      target.removeEventListener("pointercancel", stop);
      target.removeEventListener("lostpointercapture", stop);
      window.removeEventListener("pointerup", stop);
      window.removeEventListener("blur", stop);
      resize.setResizing(false);
      if (latest < HIDE_BELOW) {
        resize.restore(before);
        onHide();
      } else {
        resize.commit(latest);
      }
    };
    const move = (moveEvent: globalThis.PointerEvent) => {
      // A release outside the renderer (for example over a native view) still ends the drag.
      if (moveEvent.buttons === 0) return stop();
      latest = Math.min(limit, (right - moveEvent.clientX) / scale);
      resize.preview(latest);
    };
    target.addEventListener("pointermove", move);
    target.addEventListener("pointerup", stop);
    target.addEventListener("pointercancel", stop);
    target.addEventListener("lostpointercapture", stop);
    window.addEventListener("pointerup", stop);
    window.addEventListener("blur", stop);
  };

  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    const step = event.shiftKey ? 64 : 16;
    if (event.key === "ArrowLeft") resize.commit(Math.min(maxWidth(), currentWidth() + step));
    else if (event.key === "ArrowRight") resize.commit(currentWidth() - step);
    else if (event.key === "Home") resize.commit(MIN_WIDTH);
    else if (event.key === "End") resize.commit(maxWidth());
    else return;
    event.preventDefault();
  };

  return <div
    ref={handleRef}
    className="inspectorResizeHandle"
    role="separator"
    aria-orientation="vertical"
    aria-label={label}
    title={label}
    aria-valuemin={MIN_WIDTH}
    aria-valuenow={resize.width ?? undefined}
    tabIndex={0}
    onPointerDown={onPointerDown}
    onKeyDown={onKeyDown}
  />;
}
