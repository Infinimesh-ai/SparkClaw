import { useCallback, useEffect, useRef, useState } from "react";
import { api } from "../api/client";
import type { EmailPresentation } from "../api/email";
import type { Language } from "../i18n";

// Language is part of the request identity: late results never leak across settings.
export function useEmailPresentations(kind: "mail" | "conversation", ids: string[], language: Language) {
  const idsKey = [...new Set(ids)].sort().join("\n");
  const key = `${kind}:${language}:${idsKey}`;
  const [state, setState] = useState<{ key: string; items: Record<string, EmailPresentation> }>({ key, items: {} });
  const [error, setError] = useState<unknown>(null);
  const refreshRef = useRef<(retry?: boolean) => Promise<void>>(async () => {});
  useEffect(() => {
    const controller = new AbortController();
    let active = true;
    let busy = false;
    let timer: ReturnType<typeof setTimeout>;
    const targets = idsKey.split("\n").filter(Boolean);
    setState({ key, items: {} });
    setError(null);
    function publish(items: EmailPresentation[]) {
      if (!active) return;
      setState((old) => {
        const next = old.key === key ? { ...old.items } : {};
        for (const item of items) {
          if (item.presentation_language !== language || item.target_kind !== kind) continue;
          if (!next[item.target_id] || item.analysis_revision !== next[item.target_id].analysis_revision || item.revision >= next[item.target_id].revision) next[item.target_id] = item;
        }
        return { key, items: next };
      });
    }
    async function refresh(retry = false) {
      if (busy || !active || !targets.length) return;
      busy = true;
      try {
        // Only loaded targets, never the archive; batches and requests are bounded.
        for (let start = 0; start < targets.length && active; start += 100) {
          const batch = targets.slice(start, start + 100);
          const result = await api.emailPresentations(kind, batch, language, controller.signal);
          publish(result.items ?? []);
          const byId = new Map((result.items ?? []).map((item) => [item.target_id, item]));
          const missing = batch.filter((id) => !byId.has(id) || byId.get(id)?.state === "missing" || retry && byId.get(id)?.state === "failed");
          if (missing.length && active) publish((await api.ensureEmailPresentations(kind, missing, language, retry, controller.signal)).items ?? []);
        }
        if (active) setError(null);
      } catch (reason) { if (active) setError(reason); }
      finally { busy = false; }
    }
    refreshRef.current = refresh;
    async function poll() {
      if (document.visibilityState !== "hidden") await refresh();
      if (active) timer = setTimeout(() => void poll(), 5000);
    }
    void poll();
    return () => { active = false; clearTimeout(timer); controller.abort(); refreshRef.current = async () => {}; };
  }, [key, idsKey, kind, language]);
  const retry = useCallback(() => refreshRef.current(true), []);
  return { items: state.key === key ? state.items : {}, error, retry };
}
