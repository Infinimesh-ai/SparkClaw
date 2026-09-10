import type { EmailProviderStatus } from "./types";

export type EmailConcern = {
  id: string;
  kind: "suspected_duplicate" | "pending_correction";
  evidence: string;
  related_conversation_ids: string[];
  version: number;
};

export type EmailConversation = {
  historical_mixed?: boolean;
  id: string;
  version: number;
  title: string;
  participants: string[];
  summary?: string;
  summary_state?: string;
  unseen_count: number;
  last_activity_at?: string;
  concerns: EmailConcern[];
  processing_state?: string;
};

export type EmailMessage = {
  confirmation_source?: string;
  local_send_id?: string;
  reply_mail_id?: string;
  source_state?: string;
  current_sender_address?: string;
  current_sender_rule_revision?: number;
  verification?: EmailVerification;
  classification?: EmailClassification;
  conversation_id?: string;
  id: string;
  version: number;
  mailbox_id: string;
  receiving_address: string;
  direction: string;
  from: string;
  to: string[];
  cc: string[];
  subject: string;
  sent_at?: string;
  arrived_at: string;
  summary?: string;
  summary_state?: string;
  body_text?: string;
  viewed: boolean;
  original_path?: string;
  original_available?: boolean;
  attachments: { id: string; name: string; path?: string; size?: number; available?: boolean }[];
  processing_state?: string;
};

export type EmailMailbox = {
  id: string;
  version: number;
  provider: EmailProviderStatus["provider"];
  address: string;
  intake_enabled: boolean;
  active_binding: boolean;
  state: string;
  last_sync_at?: string;
  coverage_start?: string;
  coverage_end?: string;
  gap?: string;
  backlog?: number;
  error?: string;
};

export type EmailSyncStatus = {
  version: number;
  mailboxes: EmailMailbox[];
  backlog: number;
  pending_count: number;
};

export type EmailFilters = { mailbox_id?: string; q?: string; cursor?: string; limit?: number; subtype?: string; validity?: string };
export type EmailConversationPage = { counts?: { total: number; unseen: number }; version: number; conversations: EmailConversation[]; next_cursor?: string };
export type EmailMessagePage = { version: number; messages: EmailMessage[]; counts?: { total: number; unseen: number }; server_now?: string; next_cursor?: string };

export function emailQuery(filters: EmailFilters) {
  const query = new URLSearchParams();
  for (const [key, value] of Object.entries(filters)) {
    if (value !== undefined && value !== "") query.set(key, String(value));
  }
  return query.size ? `?${query}` : "";
}

export type EmailEntry = "notification" | "interaction";
export type EmailClassification = {
  category: "notification" | "interaction" | "unknown";
  effective_entry: EmailEntry;
  source: "manual" | "rule" | "model" | "fallback";
  notification_subtype?: string;
  state: string;
  revision: number;
  reason_code?: string;
  evidence_refs?: string[];
  uncertainty?: boolean;
  sender_address?: string;
  rule_revision?: number;
};
export type EmailSenderRule = { id: string; address: string; entry: EmailEntry; enabled: boolean; revision: number };
export type EmailPresentation = {
  target_kind: "mail" | "conversation";
  target_id: string;
  presentation_language: "en" | "zh";
  state: "missing" | "queued" | "running" | "ready" | "failed";
  analysis_revision: string;
  revision: number;
  title?: string;
  summary?: string;
  explanation?: string;
  requested_response?: string;
  purpose?: string;
  service_label?: string;
  evidence?: { ref: string; text: string }[];
  concern_explanations?: Record<string, string>;
  error_code?: string;
};

export type EmailDraft = {
  id: string; version: number; mailbox_id: string; mode: "compose" | "reply" | "reply_all";
  reply_mail_id?: string; conversation_id?: string; to: string[]; cc: string[]; subject: string; body: string;
  state: "draft" | "sending" | "sent" | "failed" | "unknown"; error_code?: string; send_key?: string; sent_mail_id?: string; timeline_mail_id?: string; confirmation_source?: string; reconciled_at?: string;
};
export type EmailDraftInput = Pick<EmailDraft, "mailbox_id" | "mode" | "reply_mail_id" | "to" | "cc" | "subject" | "body"> & { id?: string; expected_version: number };
export type EmailComposeCapabilities = { compose: boolean; reply: boolean; reply_all: boolean; cc: boolean; max_to: number; reply_reason?: string };

export type EmailVerification = { purpose: string; state: "validity_unknown" | "not_expired" | "expired"; expires_at?: string; server_now: string; can_reveal: boolean; code?: string; timing_evidence?: string; source_time?: string; received_at?: string };
