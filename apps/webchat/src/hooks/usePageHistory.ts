import { useEffect, useRef, useState } from "react";

export type PageHistory = {
  canGoBack: boolean;
  canGoForward: boolean;
  goBack: () => void;
  goForward: () => void;
};

type PageHistoryOptions<T> = {
  // Entries recorded under another identity or connection are discarded.
  scope: string;
  disabled?: boolean;
  // Skip entries whose conversation no longer exists.
  valid: (entry: T) => boolean;
  restore: (entry: T) => void | Promise<void>;
};

type HistoryState<T> = { entries: T[]; index: number; scope: string };

// Back and forward for the desktop title bar. Each distinct page, conversation
// or settings tab becomes an entry; restoring one never records a new entry.
export function usePageHistory<T>(current: T, options: PageHistoryOptions<T>, enabled: boolean): PageHistory {
  const [history, setHistory] = useState<HistoryState<T>>({ entries: [current], index: 0, scope: options.scope });
  const historyRef = useRef(history); historyRef.current = history;
  const latest = useRef({ current, options }); latest.current = { current, options };
  const restoringRef = useRef(false);
  const [restoring, setRestoring] = useState(false);
  const key = (entry: T) => JSON.stringify(entry);

  useEffect(() => {
    if (!enabled || restoringRef.current) return;
    const { current, options } = latest.current, state = historyRef.current;
    if (state.scope !== options.scope) setHistory({ entries: [current], index: 0, scope: options.scope });
    else if (key(state.entries[state.index]) !== key(current)) {
      setHistory({ ...state, entries: [...state.entries.slice(0, state.index + 1), current], index: state.index + 1 });
    }
  });

  function target(step: -1 | 1) {
    const state = historyRef.current, options = latest.current.options;
    if (!enabled || restoring || options.disabled || state.scope !== options.scope) return -1;
    for (let index = state.index + step; index >= 0 && index < state.entries.length; index += step) {
      if (options.valid(state.entries[index])) return index;
    }
    return -1;
  }

  async function go(step: -1 | 1) {
    if (restoringRef.current) return;
    const index = target(step);
    if (index < 0) return;
    const state = historyRef.current;
    restoringRef.current = true; setRestoring(true);
    try {
      await latest.current.options.restore(state.entries[index]);
      if (state.scope === latest.current.options.scope) setHistory({ ...state, index });
    } catch {
      // The host surfaces its own restore failure; the position stays put.
    } finally {
      restoringRef.current = false; setRestoring(false);
    }
  }

  return {
    canGoBack: target(-1) >= 0,
    canGoForward: target(1) >= 0,
    goBack: () => void go(-1),
    goForward: () => void go(1),
  };
}
