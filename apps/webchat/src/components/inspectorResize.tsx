import { useCallback, useRef, useState, type CSSProperties, type KeyboardEvent, type PointerEvent } from "react";

const STORAGE_KEY = "sparkx.inspector-width";
// Desktop split mode follows the embedded browser design: 480-760 px.
const LIMITS = { desktop: { min: 480, max: 760 }, web: { min: 280, max: 760 } };

function storedWidth(): number | null {
  try {
    const value = Number(window.localStorage.getItem(STORAGE_KEY));
    return Number.isFinite(value) && value > 0 ? value : null;
  } catch {
    return null;
  }
}

export function useInspectorWidth(desktop: boolean) {
  const limits = desktop ? LIMITS.desktop : LIMITS.web;
  const [width, setWidth] = useState<number | null>(storedWidth);
  const [resizing, setResizing] = useState(false);
  const clamp = useCallback((value: number) => Math.round(Math.min(limits.max, Math.max(limits.min, value))), [limits]);
  const commit = useCallback((value: number) => {
    const next = clamp(value);
    setWidth(next);
    try { window.localStorage.setItem(STORAGE_KEY, String(next)); } catch { /* Width is a per-viewer convenience. */ }
  }, [clamp]);
  const effective = width === null ? null : clamp(width);
  const style = effective === null ? undefined : { "--inspector-width": `${effective}px` } as CSSProperties;
  return { width: effective, limits, style, resizing, setResizing, commit };
}

type ResizeState = ReturnType<typeof useInspectorWidth>;

export function InspectorResizeHandle({ resize, label }: { resize: ResizeState; label: string }) {
  const handleRef = useRef<HTMLDivElement | null>(null);
  const panel = () => handleRef.current?.parentElement?.querySelector<HTMLElement>(".taskInspector") ?? null;
  // The workbench may apply CSS zoom; pointer and layout rectangles are in zoomed pixels.
  const zoom = () => {
    const workbench = handleRef.current?.closest<HTMLElement>(".workbench");
    return workbench ? parseFloat(getComputedStyle(workbench).getPropertyValue("--workbench-zoom")) || 1 : 1;
  };
  const currentWidth = () => {
    const element = panel();
    return element ? element.getBoundingClientRect().width / zoom() : resize.width ?? resize.limits.min;
  };

  const onPointerDown = (event: PointerEvent<HTMLDivElement>) => {
    const element = panel();
    if (!element || event.button !== 0) return;
    event.preventDefault();
    const right = element.getBoundingClientRect().right;
    const scale = zoom();
    const target = event.currentTarget;
    target.setPointerCapture(event.pointerId);
    resize.setResizing(true);
    const move = (moveEvent: globalThis.PointerEvent) => resize.commit((right - moveEvent.clientX) / scale);
    const stop = () => {
      resize.setResizing(false);
      target.removeEventListener("pointermove", move);
      target.removeEventListener("pointerup", stop);
      target.removeEventListener("pointercancel", stop);
    };
    target.addEventListener("pointermove", move);
    target.addEventListener("pointerup", stop);
    target.addEventListener("pointercancel", stop);
  };

  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    const step = event.shiftKey ? 64 : 16;
    if (event.key === "ArrowLeft") resize.commit(currentWidth() + step);
    else if (event.key === "ArrowRight") resize.commit(currentWidth() - step);
    else if (event.key === "Home") resize.commit(resize.limits.min);
    else if (event.key === "End") resize.commit(resize.limits.max);
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
    aria-valuemin={resize.limits.min}
    aria-valuemax={resize.limits.max}
    aria-valuenow={resize.width ?? undefined}
    tabIndex={0}
    onPointerDown={onPointerDown}
    onKeyDown={onKeyDown}
  />;
}
