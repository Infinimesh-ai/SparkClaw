import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { api, streamPassiveNotifications } from "../api/client";
import type { PassiveNotification } from "../api/types";

// Bound on the duplicate-suppression set: newest ids win, older ones age out.
const SEEN_LIMIT = 500;
const RECONNECT_BASE_DELAY_MS = 1000;
const RECONNECT_MAX_DELAY_MS = 30_000;

function boundedSeen(freshIds: Iterable<string>, previous: Set<string>) {
  const next = new Set<string>();
  for (const id of freshIds) {
    if (next.size >= SEEN_LIMIT) return next;
    next.add(id);
  }
  for (const id of previous) {
    if (next.size >= SEEN_LIMIT) return next;
    next.add(id);
  }
  return next;
}

export function usePassiveNotifications() {
  const [notifications, setNotifications] = useState<PassiveNotification[]>([]);
  const [open, setOpen] = useState(false);
  const [toast, setToast] = useState<PassiveNotification | null>(null);
  const [error, setError] = useState("");
  const seen = useRef(new Set<string>());

  // Derived from the notification list instead of double-bookkeeping a
  // counter that could drift from what is actually rendered.
  const unreadCount = useMemo(
    () => notifications.reduce((count, notification) => (notification.read_at ? count : count + 1), 0),
    [notifications]
  );

  const load = useCallback(async () => {
    const result = await api.notifications();
    const list = result.notifications ?? [];
    // Union instead of replace: ids the realtime stream already delivered
    // stay suppressed even when the list window has moved past them.
    seen.current = boundedSeen(list.map((notification) => notification.id), seen.current);
    setNotifications(list);
    setError("");
    return list[0]?.id ?? "";
  }, []);

  const refresh = useCallback(async () => {
    try {
      await load();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }, [load]);

  useEffect(() => {
    const controller = new AbortController();
    let cursor = "";
    let initialized = false;
    let delay = RECONNECT_BASE_DELAY_MS;
    const backoff = async () => {
      await new Promise((resolve) => window.setTimeout(resolve, delay));
      delay = Math.min(delay * 2, RECONNECT_MAX_DELAY_MS);
    };
    async function subscribe() {
      while (!controller.signal.aborted) {
        if (!initialized) {
          try {
            cursor = await load();
            initialized = true;
            delay = RECONNECT_BASE_DELAY_MS;
          } catch {
            await backoff();
            continue;
          }
        }
        try {
          await streamPassiveNotifications(cursor, {
            signal: controller.signal,
            onNotification: (notification) => {
              cursor = notification.id;
              if (seen.current.has(notification.id)) return;
              seen.current = boundedSeen([notification.id], seen.current);
              setNotifications((current) => [notification, ...current].slice(0, 100));
              setToast(notification);
            }
          });
        } catch {
          if (controller.signal.aborted) return;
        }
        initialized = false;
        await backoff();
      }
    }
    void subscribe();
    return () => controller.abort();
  }, [load]);

  useEffect(() => {
    if (!toast) return;
    const timeout = window.setTimeout(() => setToast(null), 8000);
    return () => window.clearTimeout(timeout);
  }, [toast]);

  const markRead = useCallback(async (id: string) => {
    try {
      const result = await api.markNotificationRead(id);
      setNotifications((current) => current.map((item) => (item.id === id ? result.notification : item)));
      setToast((current) => (current?.id === id ? null : current));
      setError("");
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }, []);

  const markAllRead = useCallback(async () => {
    try {
      await api.markAllNotificationsRead();
      const readAt = new Date().toISOString();
      setNotifications((current) => current.map((item) => (item.read_at ? item : { ...item, read_at: readAt, updated_at: readAt })));
      setToast(null);
      setError("");
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }, []);

  return {
    notifications,
    unreadCount,
    open,
    toast,
    error,
    setOpen,
    dismissToast: () => setToast(null),
    markRead,
    markAllRead,
    refresh
  };
}
