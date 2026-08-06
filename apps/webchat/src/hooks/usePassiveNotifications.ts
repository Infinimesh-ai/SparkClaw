import { useCallback, useEffect, useRef, useState } from "react";
import { api, streamPassiveNotifications } from "../api/client";
import type { PassiveNotification } from "../api/types";

export function usePassiveNotifications() {
  const [notifications, setNotifications] = useState<PassiveNotification[]>([]);
  const [unreadCount, setUnreadCount] = useState(0);
  const [open, setOpen] = useState(false);
  const [toast, setToast] = useState<PassiveNotification | null>(null);
  const seen = useRef(new Set<string>());

  const refresh = useCallback(async () => {
    const result = await api.notifications();
    seen.current = new Set(result.notifications.map((notification) => notification.id));
    setNotifications(result.notifications);
    setUnreadCount(result.unread_count);
    return result.notifications[0]?.id ?? "";
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    let cursor = "";
    let initialized = false;
    async function subscribe() {
      while (!controller.signal.aborted) {
        if (!initialized) {
          try {
            cursor = await refresh();
            initialized = true;
          } catch {
            await new Promise((resolve) => window.setTimeout(resolve, 1000));
            continue;
          }
        }
        try {
          await streamPassiveNotifications(cursor, {
            signal: controller.signal,
            onNotification: (notification) => {
              cursor = notification.id;
              if (seen.current.has(notification.id)) return;
              seen.current.add(notification.id);
              setNotifications((current) => [notification, ...current].slice(0, 100));
              if (!notification.read_at) {
                setUnreadCount((current) => current + 1);
              }
              setToast(notification);
            }
          });
        } catch {
          if (controller.signal.aborted) return;
        }
        initialized = false;
        await new Promise((resolve) => window.setTimeout(resolve, 1000));
      }
    }
    void subscribe();
    return () => controller.abort();
  }, [refresh]);

  useEffect(() => {
    if (!toast) return;
    const timeout = window.setTimeout(() => setToast(null), 8000);
    return () => window.clearTimeout(timeout);
  }, [toast]);

  const markRead = useCallback(async (id: string) => {
    const result = await api.markNotificationRead(id);
    setNotifications((current) => current.map((item) => (item.id === id ? result.notification : item)));
    setUnreadCount(result.unread_count);
    setToast((current) => (current?.id === id ? null : current));
  }, []);

  const markAllRead = useCallback(async () => {
    const result = await api.markAllNotificationsRead();
    const readAt = new Date().toISOString();
    setNotifications((current) => current.map((item) => (item.read_at ? item : { ...item, read_at: readAt, updated_at: readAt })));
    setUnreadCount(result.unread_count);
    setToast(null);
  }, []);

  return {
    notifications,
    unreadCount,
    open,
    toast,
    setOpen,
    dismissToast: () => setToast(null),
    markRead,
    markAllRead,
    refresh
  };
}
