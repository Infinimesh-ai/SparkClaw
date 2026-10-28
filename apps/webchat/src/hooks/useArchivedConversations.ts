// Archiving only hides a conversation from the recent list on this device.
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

export function useArchivedConversations(ownerID?: string) {
  const key = archiveStorageKey(ownerID);
  const [archived, setArchived] = useState(() => readArchived(key));
  useEffect(() => {
    setArchived(readArchived(key));
    const storageChanged = (event: StorageEvent) => { if (event.key === key) setArchived(readArchived(key)); };
    const windowChanged = (event: Event) => { if ((event as CustomEvent<string>).detail === key) setArchived(readArchived(key)); };
    window.addEventListener("storage", storageChanged);
    window.addEventListener(archiveChangeEvent, windowChanged);
    return () => {
      window.removeEventListener("storage", storageChanged);
      window.removeEventListener(archiveChangeEvent, windowChanged);
    };
  }, [key]);

  // Returns false, leaving the list unchanged, when browser storage rejects it.
  const save = useCallback((next: string[]) => {
    try {
      window.localStorage.setItem(key, JSON.stringify(next));
    } catch {
      return false;
    }
    setArchived(next);
    window.dispatchEvent(new CustomEvent(archiveChangeEvent, { detail: key }));
    return true;
  }, [key]);

  return { archived, save };
}
