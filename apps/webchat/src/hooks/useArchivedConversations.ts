// Archiving hides a conversation from the recent list. In the host WebChat
// the list is kept on this device; in the desktop workbench the backend keeps
// it (session.archive), so every client shows the same archive.
import { useCallback, useEffect, useState } from "react";

const archiveStorageKey = (ownerID?: string) => `sparkx.archived-conversations.v1.${ownerID ?? "local"}`;
// The sidebar and the settings page share one list. A storage event only
// reaches other documents, so changes are also announced in this window.
const archiveChangeEvent = "sparkx:archived-conversations";

function readArchived(key: string) {
  try {
    const value: unknown = JSON.parse(window.localStorage.getItem(key) ?? "[]");
    return Array.isArray(value) ? value.filter((id): id is string => typeof id === "string") : [];
  } catch {
    return [];
  }
}

function backendArchive() {
  const store = typeof window === "undefined" ? undefined : window.sparkclawClientStore;
  return store?.archive && store.archivedIDs ? store : undefined;
}

function mirror(key: string, ids: string[]) {
  try { window.localStorage.setItem(key, JSON.stringify(ids)); return true; } catch { return false; }
}

export function useArchivedConversations(ownerID?: string) {
  const key = archiveStorageKey(ownerID);
  const [archived, setArchived] = useState(() => readArchived(key));
  useEffect(() => {
    setArchived(readArchived(key));
    const storageChanged = (event: StorageEvent) => { if (event.key === key) setArchived(readArchived(key)); };
    const windowChanged = (event: Event) => { if ((event as CustomEvent<string>).detail === key) setArchived(readArchived(key)); };
    window.addEventListener("storage", storageChanged);
    window.addEventListener(archiveChangeEvent, windowChanged);
    // The backend's archive replaces the device list whenever it changes.
    const store = backendArchive();
    let active = true;
    const refresh = () => {
      void store?.archivedIDs!().then((ids) => {
        if (!active || !Array.isArray(ids)) return;
        mirror(key, ids); setArchived(ids);
        window.dispatchEvent(new CustomEvent(archiveChangeEvent, { detail: key }));
      }).catch(() => undefined);
    };
    refresh();
    const unsubscribe = store?.onChange?.(refresh);
    return () => {
      active = false;
      unsubscribe?.();
      window.removeEventListener("storage", storageChanged);
      window.removeEventListener(archiveChangeEvent, windowChanged);
    };
  }, [key]);

  // Returns false, leaving the list unchanged, when the change cannot be saved.
  const save = useCallback((next: string[]) => {
    const store = backendArchive();
    if (store) {
      const previous = readArchived(key);
      const changes = [...next.filter(id => !previous.includes(id)).map(id => [id, true] as const),
        ...previous.filter(id => !next.includes(id)).map(id => [id, false] as const)];
      mirror(key, next); setArchived(next);
      window.dispatchEvent(new CustomEvent(archiveChangeEvent, { detail: key }));
      // A refused change is corrected by the next backend list.
      for (const [id, value] of changes) void store.archive!(id, value).catch(() => store.archivedIDs!().then((ids) => {
        if (!Array.isArray(ids)) return;
        mirror(key, ids); setArchived(ids);
        window.dispatchEvent(new CustomEvent(archiveChangeEvent, { detail: key }));
      }).catch(() => undefined));
      return true;
    }
    if (!mirror(key, next)) return false;
    setArchived(next);
    window.dispatchEvent(new CustomEvent(archiveChangeEvent, { detail: key }));
    return true;
  }, [key]);

  return { archived, save };
}
