import { useCallback, useRef, useState, type CSSProperties, type KeyboardEvent, type PointerEvent } from "react";

const STORAGE_KEY = "sparkx.inspector-width";
// Narrowest width kept after a drag or key press; releasing below HIDE_BELOW hides the panel.
const MIN_WIDTH = 240;
const HIDE_BELOW = 120;

// In the desktop client a drag that carries on this far past a panel's narrowest
// or widest width hides it or lets it take the whole space; the snapped layout
// shows while dragging and is kept on release.
export const SNAP_DISTANCE = 56;
export type DragSnap = "hide" | "fill" | null;
export function snapState(width: number, min: number, max: number): DragSnap {
  return width < min - SNAP_DISTANCE ? "hide" : width > max + SNAP_DISTANCE ? "fill" : null;
}

// The native browser view paints above the page, so a preview that covers or
// hides it suspends the view, as an open dialog does.
export function suspendBrowserView(element: HTMLElement, suspended: boolean) {
  if (suspended) element.setAttribute("data-browser-suspended", "");
  else element.removeAttribute("data-browser-suspended");
}

// A snapped preview stays until React has drawn the committed layout.
export function afterCommit(clear: () => void) {
  requestAnimationFrame(() => requestAnimationFrame(clear));
}

// The desktop browser keeps a dragged width only for this run of the app.
let sessionWidth: number | null = null;

function storedWidth(): number | null {
  try {
    const value = Number(window.localStorage.getItem(STORAGE_KEY));
    return Number.isFinite(value) && value > 0 ? value : null;
  } catch {
    return null;
  }
}

export function useInspectorWidth({ persist = true }: { persist?: boolean } = {}) {
  const [width, setWidth] = useState<number | null>(() => persist ? storedWidth() : sessionWidth);
  const [resizing, setResizing] = useState(false);
  const commit = useCallback((value: number) => {
    const next = Math.round(Math.max(MIN_WIDTH, value));
    setWidth(next);
    if (!persist) { sessionWidth = next; return; }
    try { window.localStorage.setItem(STORAGE_KEY, String(next)); } catch { /* Width is a per-viewer convenience. */ }
  }, [persist]);
  // A live drag may shrink the panel toward zero before it is hidden or committed.
  const preview = useCallback((value: number) => setWidth(Math.max(0, Math.round(value))), []);
  const style = width === null ? undefined : { "--inspector-width": `${width}px` } as CSSProperties;
  return { width, style, resizing, setResizing, commit, preview, restore: setWidth };
}

type ResizeState = ReturnType<typeof useInspectorWidth>;

// reserve keeps that much of the workspace for the conversation; onResizeStart
// lets a widened panel return to its dragged width before the drag continues.
// With onExpand a drag snaps: past the narrowest width it hides the panel and
// past the widest the panel takes the workspace in place of the conversation.
export function InspectorResizeHandle({ resize, label, onHide, reserve = 0, defaultWidth, onResizeStart, onExpand }: {
  resize: ResizeState; label: string; onHide: () => void; reserve?: number; defaultWidth?: number; onResizeStart?: () => void; onExpand?: () => void;
}) {
  const handleRef = useRef<HTMLDivElement | null>(null);
  const workspace = () => handleRef.current?.parentElement ?? null;
  const panel = () => workspace()?.querySelector<HTMLElement>(".taskInspector") ?? null;
  const maxWidth = () => Math.max(MIN_WIDTH, (workspace()?.getBoundingClientRect().width ?? 0) - reserve);
  const currentWidth = () => panel()?.getBoundingClientRect().width ?? 0;

  const onPointerDown = (event: PointerEvent<HTMLDivElement>) => {
    const element = panel();
    if (!element || event.button !== 0) return;
    event.preventDefault();
    onResizeStart?.();
    const right = element.getBoundingClientRect().right;
    const limit = maxWidth();
    const before = resize.width;
    const target = event.currentTarget;
    target.setPointerCapture(event.pointerId);
    resize.setResizing(true);
    let latest = currentWidth();
    let state: DragSnap = null;
    const space = workspace();
    const preview = (next: DragSnap) => {
      if (!space) return;
      if (next) space.dataset.browserSnap = next; else delete space.dataset.browserSnap;
      suspendBrowserView(space, next === "hide");
    };
    const stop = () => {
      target.removeEventListener("pointermove", move);
      target.removeEventListener("pointerup", stop);
      target.removeEventListener("pointercancel", stop);
      target.removeEventListener("lostpointercapture", stop);
      window.removeEventListener("pointerup", stop);
      window.removeEventListener("blur", stop);
      resize.setResizing(false);
      if (onExpand) {
        if (!state) { resize.commit(latest); preview(null); return; }
        resize.restore(before);
        if (state === "hide") onHide(); else onExpand();
        afterCommit(() => preview(null));
        return;
      }
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
      const raw = right - moveEvent.clientX;
      if (onExpand) {
        state = snapState(raw, MIN_WIDTH, limit);
        preview(state);
        latest = Math.min(limit, Math.max(MIN_WIDTH, raw));
        if (!state) resize.preview(latest);
        return;
      }
      latest = Math.min(limit, raw);
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
    aria-valuenow={resize.width ?? defaultWidth ?? (typeof window !== "undefined" && window.sparkclawDesktop ? 640 : 360)}
    aria-valuetext={`${Math.round(resize.width ?? defaultWidth ?? (typeof window !== "undefined" && window.sparkclawDesktop ? 640 : 360))} px`}
    tabIndex={0}
    onPointerDown={onPointerDown}
    onKeyDown={onKeyDown}
  />;
}
