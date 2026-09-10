import { useCallback, useEffect, useRef, useState } from "react";

export type EmailPage<T> = { version: number; items: T[]; counts?: { total: number; unseen: number }; server_now?: string; next_cursor?: string };
type Row = { id: string; version: number };
type PageState<T> = { key: string; pages: Map<string, EmailPage<T>> };

// A refresh replaces only the requested page. Other loaded cursors and the
// selection remain intact, and an older response cannot regress a page or row.
export function mergeEmailPages<T extends Row>(pages: Map<string, EmailPage<T>>) {
  const rows = new Map<string, T>();
  for (const page of pages.values()) {
    for (const item of page.items) {
      const previous = rows.get(item.id);
      if (!previous || item.version >= previous.version) rows.set(item.id, item);
    }
  }
  return [...rows.values()];
}

export function useEmailPages<T extends Row>(
  key: string,
  load: (cursor: string, signal: AbortSignal) => Promise<EmailPage<T>>,
  enabled = true
) {
  const [state, setState] = useState<PageState<T>>({ key, pages: new Map() });
  const [error, setError] = useState<unknown>(null);
  const [loading, setLoading] = useState(false);
  const stateRef = useRef(state);
  stateRef.current = state;
  const refreshRef = useRef<(cursor?: string) => Promise<void>>(async () => {});

  useEffect(() => {
    const controller = new AbortController();
    const pending = new Set<string>();
    let active = true;
    let rotation = 0;
    let timer: ReturnType<typeof setTimeout>;
    setState({ key, pages: new Map() });
    setError(null);
    setLoading(false);
    if (!enabled) return;

    async function refresh(cursor = "") {
      if (pending.has(cursor) || !active) return;
      pending.add(cursor);
      setLoading(true);
      try {
        const page = await load(cursor, controller.signal);
        if (!active) return;
        setState((previous) => {
          if (previous.key !== key) return previous;
          const old = previous.pages.get(cursor);
          if (old && old.version > page.version) return previous;
          const pages = new Map(previous.pages);
          pages.set(cursor, page);
          return { key, pages };
        });
        setError(null);
      } catch (reason) {
        if (active) setError(reason);
      } finally {
        pending.delete(cursor);
        if (active) setLoading(pending.size > 0);
      }
    }
    refreshRef.current = refresh;
    async function poll() {
      if (document.visibilityState !== "hidden") {
        // Poll the current head and one loaded historical page per tick, so
        // request volume stays bounded even after extensive pagination.
        const cursors = [...stateRef.current.pages.keys()].filter(Boolean);
        await Promise.all([refresh(), ...(cursors.length ? [refresh(cursors[rotation++ % cursors.length])] : [])]);
      }
      if (active) timer = setTimeout(() => void poll(), 5000);
    }
    void poll();
    return () => {
      active = false;
      clearTimeout(timer);
      controller.abort();
      refreshRef.current = async () => {};
    };
  }, [key, load, enabled]);

  const pages = state.key === key ? state.pages : new Map<string, EmailPage<T>>();
  const last = [...pages.values()].at(-1);
  const nextCursor = last?.next_cursor ?? "";
  const loadMore = useCallback(() => refreshRef.current(nextCursor), [nextCursor]);
  const refresh = useCallback(() => refreshRef.current(), []);
  return { items: mergeEmailPages(pages), counts: pages.get("")?.counts, serverNow: pages.get("")?.server_now, loading, error, nextCursor, loadMore, refresh };
}
