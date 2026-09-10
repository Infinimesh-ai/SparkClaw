import { useCallback, useEffect, useRef, useState } from "react";
import { api } from "../api/client";
import type { EmailConversation, EmailSyncStatus } from "../api/email";
import type { EmailProviderStatus } from "../api/types";

export function useEmailStatus(selectedId: string) {
  const [status, setStatus] = useState<EmailSyncStatus | null>(null);
  const [providers, setProviders] = useState<EmailProviderStatus[]>([]);
  const [detail, setDetail] = useState<{ version: number; conversation: EmailConversation } | null>(null);
  const [error, setError] = useState<unknown>(null);
  const refreshRef = useRef<() => Promise<void>>(async () => {});

  useEffect(() => {
    const controller = new AbortController();
    let active = true;
    let busy = false;
    let timer: ReturnType<typeof setTimeout>;
    setDetail(null);
    setError(null);
    void api.emailProviders().then((result) => { if (active) setProviders(result.providers ?? []); }).catch((reason) => { if (active) setError(reason); });
    async function refresh() {
      if (busy || !active) return;
      busy = true;
      const results = await Promise.allSettled([
        api.emailSyncStatus(controller.signal).then((result) => {
          if (active) setStatus((old) => !old || result.version >= old.version ? result : old);
        }),
        ...(selectedId ? [api.emailConversation(selectedId, controller.signal).then((result) => {
          if (active) setDetail((old) => !old || result.version >= old.version ? result : old);
        })] : [])
      ]);
      if (active) {
        const failure = results.find((result) => result.status === "rejected");
        setError(failure?.status === "rejected" ? failure.reason : null);
      }
      busy = false;
    }
    refreshRef.current = refresh;
    async function poll() {
      if (document.visibilityState !== "hidden") await refresh();
      if (active) timer = setTimeout(() => void poll(), 5000);
    }
    void poll();
    return () => { active = false; clearTimeout(timer); controller.abort(); refreshRef.current = async () => {}; };
  }, [selectedId]);

  const refresh = useCallback(() => refreshRef.current(), []);
  return { status, providers, detail: detail?.conversation.id === selectedId ? detail.conversation : null, error, refresh };
}
