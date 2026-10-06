export type LocalConversation = { id: string; title: string; created_at: string; updated_at: string };
export type LocalMessage = { id: string; role: "user" | "assistant"; content: string; created_at: string };
export type LocalApproval = { approval_id: string; digest: string; tool: string; summary: string; arguments: Record<string, unknown>;
  state: "pending" | "decision_pending" | "approved" | "rejected" | "resolved" | "expired" | "decision_unknown";
  decision?: "approve" | "reject" | null; expires_at: string; actionable?: boolean };
export type LocalTask = { id: string; request_id: string; status: string; explicitly_submitted?: number; created_at: string; approvals?: LocalApproval[] };
export type LocalFile = { id: string; name: string; size: number; sha256: string; created_at: string };
export type LocalSchedule = { request_id: string; schedule_id: string; due_at: string; state: string;
  interval_ms: number; definition_state: "active" | "completed" | "canceled"; missed_count: number;
  missed_until?: string | null; claimed_at?: string | null; recovery_request_id?: string | null };
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
  decideApproval: (requestID: string, approvalID: string, digest: string, decision: "approve" | "reject") => Promise<{ resolved: true }>;
  scheduleCreate: (id: string, content: string, dueAt: string, intervalMS?: number) => Promise<LocalSchedule>;
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
