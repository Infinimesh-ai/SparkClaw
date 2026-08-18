import { useEffect, useRef } from "react";
import type { NotificationBinding } from "../api/types";
import { isBindingSetupPending } from "../lib/connectors";

type BindingPollingOptions = {
  pendingBindingKey: string;
  refreshBinding: (id: string, signal?: AbortSignal) => Promise<NotificationBinding>;
  fallbackError: string;
  onError: (message: string) => void;
};

export function useBindingPolling({
  pendingBindingKey,
  refreshBinding,
  fallbackError,
  onError
}: BindingPollingOptions) {
  const refreshBindingRef = useRef(refreshBinding);
  const onErrorRef = useRef(onError);

  useEffect(() => {
    refreshBindingRef.current = refreshBinding;
  }, [refreshBinding]);

  useEffect(() => {
    onErrorRef.current = onError;
  }, [onError]);

  useEffect(() => {
    const pendingIDs = JSON.parse(pendingBindingKey) as string[];
    if (pendingIDs.length === 0) return;
    let cancelled = false;
    let timer = 0;
    let controller: AbortController | null = null;
    const poll = () => {
      controller = new AbortController();
      void Promise.allSettled(pendingIDs.map((id) => refreshBindingRef.current(id, controller?.signal)))
        .then((results) => {
          controller = null;
          if (cancelled) return;
          const rejected = results.find((result) => result.status === "rejected");
          if (rejected?.status === "rejected") {
            onErrorRef.current(rejected.reason instanceof Error ? rejected.reason.message : fallbackError);
            timer = window.setTimeout(poll, 4000);
            return;
          }
          const hasStillPending = results.some((result) => result.status === "fulfilled" && isBindingSetupPending(result.value));
          if (!hasStillPending) return;
          timer = window.setTimeout(poll, 2000);
        })
        .catch((error: unknown) => {
          if (!cancelled) {
            onErrorRef.current(error instanceof Error ? error.message : fallbackError);
            timer = window.setTimeout(poll, 4000);
          }
        });
    };
    timer = window.setTimeout(poll, 1000);
    return () => {
      cancelled = true;
      window.clearTimeout(timer);
      controller?.abort();
    };
  }, [pendingBindingKey, fallbackError]);
}
