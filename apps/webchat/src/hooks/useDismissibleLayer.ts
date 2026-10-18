import { useLayoutEffect, useRef, type RefObject } from "react";

type DismissibleLayerOptions = {
  open: boolean;
  onDismiss: () => void;
  modal?: boolean;
  dismissible?: boolean;
  initialFocus?: string;
  restoreFocusRef?: RefObject<HTMLElement | null>;
};

// Keep Escape ownership independent of document-listener registration order.
const layers: HTMLElement[] = [];
const focusableSelector = 'a[href], button, input:not([type="hidden"]), select, textarea, [tabindex], [contenteditable="true"]';

function focusableElements(layer: HTMLElement) {
  return Array.from(layer.querySelectorAll<HTMLElement>(focusableSelector)).filter(element => {
    if (element.tabIndex < 0 || element.matches(":disabled") || element.closest('[hidden], [inert], [aria-hidden="true"]')) return false;
    for (let current: HTMLElement | null = element; current; current = current.parentElement) {
      const style = getComputedStyle(current);
      if (style.display === "none" || style.visibility === "hidden") return false;
      if (current === layer) break;
    }
    return true;
  });
}

/** Focus containment for DOM modals; native dialogs retain their own Tab behavior. */
export function useDismissibleLayer<T extends HTMLElement>({
  open, onDismiss, modal = false, dismissible = true, initialFocus, restoreFocusRef
}: DismissibleLayerOptions) {
  const layerRef = useRef<T>(null);
  const activationRef = useRef(0);
  const optionsRef = useRef({ onDismiss, dismissible });
  useLayoutEffect(() => { optionsRef.current = { onDismiss, dismissible }; });

  useLayoutEffect(() => {
    const layer = layerRef.current;
    if (!open || !layer) return;
    const activation = ++activationRef.current;
    const previousFocus = restoreFocusRef?.current ?? (document.activeElement instanceof HTMLElement ? document.activeElement : null);
    let restoreOnClose = false;
    const firstNested = layers.findIndex(candidate => layer.contains(candidate));
    if (firstNested >= 0) layers.splice(firstNested, 0, layer);
    else layers.push(layer);
    const isTopLayer = () => layers.at(-1) === layer;
    const focusFirst = () => {
      const preferred = initialFocus ? layer.querySelector<HTMLElement>(initialFocus) : null;
      (preferred && !preferred.matches(":disabled") ? preferred : focusableElements(layer)[0] ?? layer).focus({ preventScroll: true });
    };
    if ((modal || initialFocus) && isTopLayer()) focusFirst();

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.defaultPrevented || !isTopLayer() || event.isComposing) return;
      // Unregistered native dialogs or editors can own a nested Escape first.
      const targetLayer = event.target instanceof Element ? event.target.closest('dialog[open], [role="dialog"], [role="menu"]') : null;
      if (targetLayer && targetLayer !== layer) return;
      if (event.key === "Escape") {
        event.preventDefault();
        event.stopPropagation();
        if (optionsRef.current.dismissible) {
          restoreOnClose = true;
          optionsRef.current.onDismiss();
        }
      } else if (modal && event.key === "Tab") {
        const controls = focusableElements(layer);
        const first = controls[0], last = controls.at(-1);
        const active = document.activeElement;
        if (!first || !layer.contains(active) || (event.shiftKey ? active === first || active === layer : active === last || active === layer)) {
          event.preventDefault();
          (event.shiftKey ? last ?? layer : first ?? layer).focus({ preventScroll: true });
        }
      }
    };
    const containFocus = (event: FocusEvent) => {
      if (modal && isTopLayer() && event.target instanceof Node && !layer.contains(event.target)) focusFirst();
    };
    // Bubble after focused React editors have had an opportunity to consume Escape.
    // Application shortcuts must yield while a dialog/menu/popover is present.
    document.addEventListener("keydown", handleKeyDown);
    if (modal) document.addEventListener("focusin", containFocus);
    return () => {
      document.removeEventListener("keydown", handleKeyDown);
      document.removeEventListener("focusin", containFocus);
      const index = layers.lastIndexOf(layer);
      if (index >= 0) layers.splice(index, 1);
      const nextLayer = layers.at(-1);
      // React restores its saved selection after mutation-effect cleanup. Wait
      // until that commit completes, without stealing focus from a new layer.
      queueMicrotask(() => {
        if (activationRef.current !== activation || layers.at(-1) !== nextLayer) return;
        const active = document.activeElement;
        if (previousFocus?.isConnected && (restoreOnClose || active === document.body || layer.contains(active))) {
          previousFocus.focus({ preventScroll: true });
        }
      });
    };
  }, [open, modal, initialFocus, restoreFocusRef]);
  return layerRef;
}
