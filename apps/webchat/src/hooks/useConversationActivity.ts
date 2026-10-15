import { useEffect, useState } from "react";
import { conversationActivity, type ConversationActivity } from "../lib/conversationActivity";

const SEEN_STORAGE_KEY = "sparkclaw.seenCompletions";
const SEEN_LIMIT = 500;

// A completion is identified by the conversation's execution count, so a later
// execution finishing shows the indicator again after an earlier one was viewed.
function readSeen(): Record<string, number> {
  try {
    const value = JSON.parse(window.localStorage.getItem(SEEN_STORAGE_KEY) ?? "{}");
    return value && typeof value === "object" && !Array.isArray(value) ? value : {};
  } catch { return {}; }
}

function writeSeen(seen: Record<string, number>) {
  const entries = Object.entries(seen);
  try { window.localStorage.setItem(SEEN_STORAGE_KEY, JSON.stringify(Object.fromEntries(entries.slice(-SEEN_LIMIT)))); } catch { /* Seen state is a per-viewer convenience. */ }
}

type Snapshot = { scope: string; activity: Record<string, ConversationActivity | undefined>; completions: Record<string, number | undefined> };

// Read the existing task projections for background conversations too. Reads
// are bounded and coalesced; no submission or recovery operation is invoked.
export function useConversationActivity(
  ids: string[],
  readStates: (id: string) => Promise<string[]>,
  scope: string,
  subscribe?: (listener: () => void) => () => void,
  viewing = ""
) {
  const idsKey = JSON.stringify([...ids].sort());
  const [snapshot, setSnapshot] = useState<Snapshot>({ scope, activity: {}, completions: {} });
  const [seen, setSeen] = useState(readSeen);
  useEffect(() => {
    let active = true, reading = false, dirty = false;
    let timer = 0;
    const conversationIDs: string[] = JSON.parse(idsKey);
    const refresh = async () => {
      if (!active) return;
      if (reading) { dirty = true; return; }
      reading = true;
      const updates: Record<string, ConversationActivity | undefined> = {};
      const completions: Record<string, number | undefined> = {};
      for (let offset = 0; active && offset < conversationIDs.length; offset += 4) {
        const batch = conversationIDs.slice(offset, offset + 4);
        const results = await Promise.allSettled(batch.map(readStates));
        results.forEach((result, index) => {
          if (result.status !== "fulfilled") return;
          updates[batch[index]] = conversationActivity(result.value);
          completions[batch[index]] = updates[batch[index]] === "completed" ? result.value.length : undefined;
        });
      }
      if (active) setSnapshot(current => {
        const keep = <T,>(fresh: Record<string, T | undefined>, previous: Record<string, T | undefined>) => Object.fromEntries(conversationIDs.map(id => [id,
          Object.hasOwn(fresh, id) ? fresh[id] : current.scope === scope ? previous[id] : undefined
        ]));
        return { scope, activity: keep(updates, current.activity), completions: keep(completions, current.completions) };
      });
      reading = false;
      if (active && dirty) { dirty = false; queue(); }
    };
    const queue = () => {
      if (!active || timer) return;
      timer = window.setTimeout(() => { timer = 0; void refresh(); }, 80);
    };
    const foreground = () => { if (document.visibilityState !== "hidden") queue(); };
    void refresh();
    const unsubscribe = subscribe?.(queue);
    const poll = window.setInterval(foreground, 3000);
    window.addEventListener("focus", foreground);
    document.addEventListener("visibilitychange", foreground);
    return () => {
      active = false;
      unsubscribe?.(); window.clearTimeout(timer); window.clearInterval(poll);
      window.removeEventListener("focus", foreground);
      document.removeEventListener("visibilitychange", foreground);
    };
  }, [idsKey, readStates, scope, subscribe]);
  const current = snapshot.scope === scope ? snapshot : undefined;
  const viewedCompletion = viewing ? current?.completions[viewing] : undefined;
  useEffect(() => {
    if (!viewing || viewedCompletion === undefined) return;
    setSeen(previous => {
      if (previous[viewing] === viewedCompletion) return previous;
      const { [viewing]: _, ...rest } = previous;
      const next = { ...rest, [viewing]: viewedCompletion };
      writeSeen(next);
      return next;
    });
  }, [viewing, viewedCompletion]);
  if (!current) return {};
  // Completed indicators mean "not yet viewed": the open conversation and any
  // completion already viewed show nothing; running progress is always shown.
  return Object.fromEntries(Object.entries(current.activity).map(([id, activity]) => [id,
    activity === "completed" && (id === viewing || seen[id] === current.completions[id]) ? undefined : activity
  ]));
}
