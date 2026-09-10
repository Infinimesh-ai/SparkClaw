import { useCallback, useEffect, useRef, useState } from "react";
import { api } from "../api/client";

// Presence in a fetched page is not a viewing receipt. Only intersection of
// an individual rendered mail card with the active detail viewport admits it.
export function useEmailViewing(
  root: HTMLElement | null,
  contentKey: string,
  onViewed: () => void,
  onError: (reason: unknown) => void
) {
  const [viewed, setViewed] = useState<ReadonlySet<string>>(new Set());
  const admitted = useRef(new Set<string>());
  const confirmed = useRef(new Set<string>());
  const callbacks = useRef({ onViewed, onError });
  callbacks.current = { onViewed, onError };

  useEffect(() => {
    if (!root || !globalThis.IntersectionObserver) return;
    const controller = new AbortController();
    let active = true;
    let busy = false;
    let retryAt = 0;
    const visible = new Set<string>();
    const observer = new IntersectionObserver((entries) => {
      for (const entry of entries) {
        const id = (entry.target as HTMLElement).dataset.emailMailId;
        if (!id) continue;
        if (entry.isIntersecting && entry.intersectionRatio > 0) visible.add(id);
        else visible.delete(id);
      }
      admitVisible();
    }, { root, threshold: 0 });
    root.querySelectorAll<HTMLElement>("[data-email-mail-id]").forEach((card) => observer.observe(card));

    function admitVisible() {
      if (document.visibilityState === "hidden") return;
      for (const id of visible) {
        if (!confirmed.current.has(id)) admitted.current.add(id);
      }
    }
    async function flush() {
      if (busy || document.visibilityState === "hidden" || Date.now() < retryAt || admitted.current.size === 0) return;
      const ids = [...admitted.current].slice(0, 100);
      busy = true;
      try {
        const result = await api.markEmailViewed(ids, controller.signal);
        if (!active) return;
        // Trust only explicit IDs from this request, even if a response is
        // malformed. Successful receipts survive stale polls and reassignment.
        const accepted = new Set(result.mail_ids);
        for (const id of ids) {
          if (accepted.has(id)) { confirmed.current.add(id); admitted.current.delete(id); }
        }
        setViewed(new Set(confirmed.current));
        callbacks.current.onViewed();
      } catch (reason) {
        if (active) {
          retryAt = Date.now() + 5000;
          callbacks.current.onError(reason);
        }
      } finally {
        busy = false;
      }
    }
    document.addEventListener("visibilitychange", admitVisible);
    const timer = setInterval(() => void flush(), 300);
    return () => {
      active = false;
      controller.abort();
      observer.disconnect();
      clearInterval(timer);
      document.removeEventListener("visibilitychange", admitVisible);
    };
  }, [root, contentKey]);

  const isViewed = useCallback((id: string) => viewed.has(id), [viewed]);
  return isViewed;
}
