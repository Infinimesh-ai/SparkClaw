import { useCallback, useLayoutEffect, useRef, useState } from "react";

// The conversation keeps at least this much beside the browser; the browser
// narrows to fit and takes over the workspace only below its own minimum.
export const BROWSER_CONVERSATION_RESERVE = 520 + 6;
export const BROWSER_MIN_WIDTH = 360;
export const BROWSER_DEFAULT_WIDTH = 440;

// The split threshold is measured in CSS pixels, after sidebar and page zoom.
// A fitted browser (the desktop client) narrows to keep the conversation; an
// unfitted one keeps its width and takes over as soon as both no longer fit.
export function browserFocusRequired(width: number, panelWidth: number, wasFocused: boolean, fitted = false) {
  return width < BROWSER_CONVERSATION_RESERVE + (fitted ? Math.min(panelWidth, BROWSER_MIN_WIDTH) : panelWidth) + (wasFocused ? 24 : 0);
}

export function useBrowserLayout(open: boolean, panelWidth: number | null, enabled = true, fitted = false) {
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
      const next = browserFocusRequired(width, panelWidth ?? (fitted ? BROWSER_DEFAULT_WIDTH : 640), previous, fitted);
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
  }, [enabled, panelWidth, fitted]);
  const reveal = useCallback(() => setRegion("browser"), []);
  // While the browser covers the workspace, the conversation can be shown in its place.
  const showConversation = useCallback(() => setRegion("conversation"), []);
  const restoreTrigger = useCallback(() => {
    setWide(false);
    requestAnimationFrame(() => triggerRef.current?.focus());
  }, []);
  return {
    workspaceRef, triggerRef, wide, mounted,
    setWide: (value: boolean) => { setWide(value); if (value) setRegion("browser"); },
    visible: enabled && open && (!focused || region === "browser"),
    className: enabled && open && focused ? region === "browser" ? "browserFocus" : "conversationFocus" : "",
    // The browser covers the conversation, whichever of the two is in front.
    covering: enabled && open && focused,
    region, reveal, showConversation, restoreTrigger,
  };
}
