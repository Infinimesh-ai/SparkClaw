export type LocalConversation = { id: string; title: string; created_at: string; updated_at: string };
export type LocalMessage = { id: string; role: "user" | "assistant"; content: string; created_at: string };
export type LocalTask = { id: string; request_id: string; status: string; explicitly_submitted?: number; created_at: string };
export type LocalFile = { id: string; name: string; size: number; sha256: string; created_at: string };
export type LocalSchedule = { request_id: string; due_at: string; state: string; lease_expires_at?: string | null; recovery_request_id?: string | null };
export type LocalConversationContent = { messages: LocalMessage[]; tasks: LocalTask[]; files: LocalFile[]; schedules?: LocalSchedule[] };
export type ClientStoreAPI = {
  schemaVersion: 1;
  list: () => Promise<LocalConversation[]>;
  create: (title: string) => Promise<LocalConversation>;
  read: (id: string) => Promise<LocalConversationContent>;
  enqueue: (id: string, content: string, localFileIDs?: string[]) => Promise<LocalTask>;
  submit: (requestID: string) => Promise<LocalTask>;
  reconcile: (requestID: string) => Promise<LocalTask>;
  cancel: (requestID: string) => Promise<LocalTask>;
  scheduleCreate: (id: string, content: string, dueAt: string) => Promise<LocalSchedule>;
  scheduleCheck: (requestID: string) => Promise<LocalSchedule>;
  scheduleCancel: (requestID: string) => Promise<LocalSchedule>;
  scheduleRunNow: (requestID: string) => Promise<LocalSchedule>;
  saveFile: (id: string, name: string, bytes: Uint8Array) => Promise<LocalFile>;
  exportFile: (id: string) => Promise<{ saved: boolean }>;
  onChange?: (listener: () => void) => () => void;
};

declare global {
  interface Window { sparkclawClientStore?: ClientStoreAPI }
}

export function clientStore() { return window.sparkclawClientStore; }
