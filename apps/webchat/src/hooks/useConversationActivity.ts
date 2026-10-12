import { useEffect, useState } from "react";
import { conversationActivity, type ConversationActivity } from "../lib/conversationActivity";

// Read the existing task projections for background conversations too. Reads
// are bounded and coalesced; no submission or recovery operation is invoked.
export function useConversationActivity(
  ids: string[],
  readStates: (id: string) => Promise<string[]>,
  scope: string,
  subscribe?: (listener: () => void) => () => void
) {
  const idsKey = JSON.stringify([...ids].sort());
  const [snapshot, setSnapshot] = useState<{ scope: string; activity: Record<string, ConversationActivity | undefined> }>({ scope, activity: {} });
  useEffect(() => {
    let active = true, reading = false, dirty = false;
    let timer = 0;
    const conversationIDs: string[] = JSON.parse(idsKey);
    const refresh = async () => {
      if (!active) return;
      if (reading) { dirty = true; return; }
      reading = true;
      const updates: Record<string, ConversationActivity | undefined> = {};
      for (let offset = 0; active && offset < conversationIDs.length; offset += 4) {
        const batch = conversationIDs.slice(offset, offset + 4);
        const results = await Promise.allSettled(batch.map(readStates));
        results.forEach((result, index) => {
          if (result.status === "fulfilled") updates[batch[index]] = conversationActivity(result.value);
        });
      }
      if (active) setSnapshot(current => ({ scope, activity: Object.fromEntries(conversationIDs.map(id => [id,
        Object.hasOwn(updates, id) ? updates[id] : current.scope === scope ? current.activity[id] : undefined
      ])) }));
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
  return snapshot.scope === scope ? snapshot.activity : {};
}
