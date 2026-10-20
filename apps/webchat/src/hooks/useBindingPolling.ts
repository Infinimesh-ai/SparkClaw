import { useEffect, useRef } from "react";
import type { NotificationBinding } from "../api/types";
import { isBindingSetupPending } from "../lib/connectors";

type BindingPollingOptions = {
  pendingBindingKey: string;
  refreshBinding: (id: string, signal?: AbortSignal) => Promise<NotificationBinding>;
  fallbackError: string;
  onError: (message: string) => void;
  // Called with the previously reported poll error once polling succeeds again.
  onRecovered?: (message: string) => void;
};

export function useBindingPolling({
  pendingBindingKey,
  refreshBinding,
  fallbackError,
  onError,
  onRecovered
}: BindingPollingOptions) {
  const refreshBindingRef = useRef(refreshBinding);
  const onErrorRef = useRef(onError);
  const onRecoveredRef = useRef(onRecovered);
  onRecoveredRef.current = onRecovered;

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
    let reportedError = "";
    const report = (message: string) => {
      reportedError = message;
      onErrorRef.current(message);
    };
    const poll = () => {
      controller = new AbortController();
      void Promise.allSettled(pendingIDs.map((id) => refreshBindingRef.current(id, controller?.signal)))
        .then((results) => {
          controller = null;
          if (cancelled) return;
          const rejected = results.find((result) => result.status === "rejected");
          if (rejected?.status === "rejected") {
            report(rejected.reason instanceof Error ? rejected.reason.message : fallbackError);
            timer = window.setTimeout(poll, 4000);
            return;
          }
          if (reportedError) {
            onRecoveredRef.current?.(reportedError);
            reportedError = "";
          }
          const hasStillPending = results.some((result) => result.status === "fulfilled" && isBindingSetupPending(result.value));
          if (!hasStillPending) return;
          timer = window.setTimeout(poll, 2000);
        })
        .catch((error: unknown) => {
          if (!cancelled) {
            report(error instanceof Error ? error.message : fallbackError);
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
