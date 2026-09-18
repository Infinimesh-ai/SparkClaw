import type { EmailProviderStatus } from "./types";

export type EmailConcern = {
  id: string;
  kind: "suspected_duplicate" | "pending_correction";
  evidence: string;
  related_conversation_ids: string[];
  version: number;
};

export type EmailConversation = {
  effective_entry?: EmailEntry;
  title_state?: "ready" | "source_fallback" | "pending";
  member_count?: number;
  historical_mixed?: boolean;
  id: string;
  version: number;
  title: string;
  participants: string[];
  summary?: string;
  summary_state?: string;
  summary_partial?: boolean;
  unseen_count: number;
  last_activity_at?: string;
  concerns: EmailConcern[];
  processing_state?: string;
};

export type EmailMessage = {
  classification_state?: string;
  assignment_state?: string;
  history_state?: "complete" | "pending" | "partial" | "paused" | "failed";
  history_reason?: string;
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
  summary_partial?: boolean;
  body_text?: string;
  body_available?: boolean;
  body_revision?: string;
  viewed: boolean;
  original_path?: string;
  original_available?: boolean;
  original_purged?: boolean;
  attachments: { id: string; name: string; path?: string; size?: number; available?: boolean }[];
  processing_state?: string;
};

// The server extracts sender content into this closed semantic tree. WebChat
// maps only known node kinds to React elements. Sender HTML/CSS never enter the
// application document; validated HTTP(S) URLs exist only on user-clicked links.
export type EmailRenderNode = {
  kind: "text" | "section" | "paragraph" | "heading" | "strong" | "emphasis" | "underline" | "strike" | "small" | "mark" | "code" | "preformatted" | "quote" | "list" | "item" | "description_list" | "term" | "description" | "table" | "table_head" | "table_body" | "table_foot" | "row" | "header_cell" | "cell" | "image" | "link" | "line_break" | "divider";
  text?: string;
  source?: string;
  url?: string;
  alt?: string;
  level?: number;
  ordered?: boolean;
  col_span?: number;
  row_span?: number;
  children?: EmailRenderNode[];
};

export type EmailRenderPreview = {
  version: number;
  id: string;
  state: "ready" | "unavailable" | "failed";
  sanitizer_version: string;
  representation_id: string;
  content: EmailRenderNode[];
  embedded_resource_count: number;
  embedded_resource_bytes: number;
  failure_code?: string;
};

export type EmailCapacity = { state: "ok" | "warning" | "critical" | "unknown"; total_bytes: number; free_bytes: number; used_percent: number };
export type EmailCleanupScope = "mail" | "date" | "mailbox" | "all";
export type EmailCleanupResult = { scope: string; purged: number; freed_bytes: number; partial: boolean };

export type EmailMailbox = {
  id: string;
  version: number;
  provider: EmailProviderStatus["provider"];
  address: string;
  intake_enabled: boolean;
  active_binding: boolean;
  state: string;
  scope_version?: string;
  provider_mode?: "change_cursor" | "time_range" | "anchored_head" | "unqualified";
  last_sync_at?: string;
  coverage_start?: string;
  coverage_end?: string;
  poll_through?: string;
  inflight_until?: string;
  pending_failure_count?: number;
  suppressed_mail_count?: number;
  coverage_gap_count?: number;
  unacknowledged_warning_count?: number;
  refresh_available?: boolean;
  refresh_pending?: boolean;
  refresh_request_id?: string;
  gap?: string;
  backlog?: number;
  error?: string;
};

export type EmailSyncWarning = {
  id: string;
  mailbox_id: string;
  warning_ref: string;
  stage: string;
  scope: string;
  error_code: string;
  state: "suppressed" | "coverage_gap";
  observed_at?: string;
  attempt_count: number;
  first_failed_at: string;
  last_attempt_at: string;
  acknowledged_at?: string;
};

export type EmailSyncWarningPage = { items: EmailSyncWarning[]; next_cursor?: string };

export type EmailSyncStatus = {
  version: number;
  mailboxes: EmailMailbox[];
  backlog: number;
  pending_count: number;
  captured_count?: number;
  capacity?: EmailCapacity;
};

export type EmailSyncSchedule = {
  scheduled: boolean;
  refresh_requests?: { mailbox_id: string; refresh_request_id: string }[];
};

export type EmailFilters = { entry?: EmailEntry; unassigned_only?: boolean; mailbox_id?: string; q?: string; cursor?: string; limit?: number; subtype?: string; validity?: string };
export type EmailConversationPage = { counts?: { total: number; unseen: number }; version: number; conversations: EmailConversation[]; next_cursor?: string };
export type EmailConversationDeleteResult = { conversation_id: string; deleted_mails: number; freed_bytes: number };
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
  source: "manual" | "rule" | "model" | "fallback" | "pattern";
  notification_subtype?: string;
  state: string;
  revision: number;
  reason_code?: string;
  evidence_refs?: string[];
  evidence?: { ref: string; text: string }[];
  uncertainty?: boolean;
  sender_address?: string;
  rule_revision?: number;
};
export type EmailSenderRule = { id: string; address: string; entry: EmailEntry; enabled: boolean; revision: number };
export type EmailPresentation = {
  target_kind: "mail" | "conversation";
  target_id: string;
  presentation_language: "en" | "zh";
  state: "missing" | "queued" | "running" | "ready" | "failed" | "suspended";
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
export type EmailReplyPolishInput = { id: string; mail_id: string; instruction: string; language: "en" | "zh" };
export type EmailComposeCapabilities = { compose: boolean; reply: boolean; reply_all: boolean; cc: boolean; max_to: number; reply_reason?: string };

export type EmailVerification = { purpose: string; state: "validity_unknown" | "not_expired" | "expired"; expires_at?: string; server_now: string; can_reveal: boolean; code?: string; timing_evidence?: string; source_time?: string; received_at?: string };
