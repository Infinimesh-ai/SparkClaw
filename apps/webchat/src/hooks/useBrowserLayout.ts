import { useCallback, useLayoutEffect, useRef, useState } from "react";

// The split threshold is measured in CSS pixels, after sidebar and page zoom.
export function browserFocusRequired(width: number, panelWidth: number, wasFocused: boolean) {
  return width < 520 + 6 + panelWidth + (wasFocused ? 24 : 0);
}

export function useBrowserLayout(open: boolean, panelWidth: number | null, enabled = true) {
  const workspaceRef = useRef<HTMLElement | null>(null);
  const triggerRef = useRef<HTMLButtonElement | null>(null);
  const [mounted, setMounted] = useState(open);
  useLayoutEffect(() => { if (open) setMounted(true); }, [open]);
  const [narrow, setNarrow] = useState(false);
  const narrowRef = useRef(false);
  const [wide, setWide] = useState(false);
  const [region, setRegion] = useState<"conversation" | "browser">("browser");
  const focused = narrow || wide;
  useLayoutEffect(() => {
    const workspace = workspaceRef.current;
    if (!workspace || !enabled) return;
    const update = () => {
      const previous = narrowRef.current;
      const width = workspace.getBoundingClientRect().width;
      if (width <= 0) return;
      const next = browserFocusRequired(width, panelWidth ?? 640, previous);
      if (next && !previous) {
        const active = document.activeElement;
        setRegion(active instanceof Element && active.closest(".chatColumn") ? "conversation" : "browser");
      }
      narrowRef.current = next;
      setNarrow(next);
    };
    const observer = new ResizeObserver(update);
    observer.observe(workspace); update();
    return () => observer.disconnect();
  }, [enabled, panelWidth]);
  const reveal = useCallback(() => setRegion("browser"), []);
  const restoreTrigger = useCallback(() => {
    setWide(false);
    requestAnimationFrame(() => triggerRef.current?.focus());
  }, []);
  return {
    workspaceRef, triggerRef, wide, mounted,
    setWide: (value: boolean) => { setWide(value); if (value) setRegion("browser"); },
    visible: enabled && open && (!focused || region === "browser"),
    className: enabled && open && focused ? region === "browser" ? "browserFocus" : "conversationFocus" : "",
    reveal, restoreTrigger,
  };
}
