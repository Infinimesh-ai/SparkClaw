import type { EmailConversation } from "../api/email";
import type { Copy } from "../i18n";

export function emailProgressLabel(state: string | undefined, text: Copy) {
  if (!state) return "";
  switch (state) {
    case "ready": case "complete": case "completed": case "active": case "current": case "idle": case "succeeded": return text.email.ready;
    case "running": case "processing": case "analyzing": case "backfilling": case "scanning": return text.email.processing;
    case "retry_wait": case "waiting_source": case "pending": case "queued": case "retry": case "retrying": case "waiting": return text.email.waiting;
    case "stale": case "refresh_pending": return text.email.refreshPending;
    case "suspended": case "paused": case "disabled": case "paused_binding_changed": case "binding_changed": return text.email.paused;
    case "failed": case "exhausted": case "error": case "unsupported": case "blocked": return text.email.failed;
    case "needs_review": return text.email.needsAttention;
    default: return text.email.needsAttention;
  }
}

export function EmailProgress({ state, text }: { state?: string; text: Copy }) {
  return state ? <span className={`emailProgress emailProgress-${state.replace(/[^a-z_]/g, "")}`}>{emailProgressLabel(state, text)}</span> : null;
}

export function emailErrorLabel(reason: unknown, text: Copy): string {
  const error = reason && typeof reason === "object" ? reason as { status?: number; code?: string } : {};
  if (["email_native_reply_unavailable", "email_reply_target_unverified", "email_reply_target_unavailable"].includes(error.code ?? "")) return text.email.replyTargetUnavailable;
  if (["email_compose_unavailable", "email_not_configured", "email_login_required"].includes(error.code ?? "")) return text.email.sendAccountUnavailable;
  if (error.code === "email_reply_polish_unavailable") return text.email.actionFailed;
  if (error.code === "email_multiple_recipients_unavailable") return text.email.recipientLimit;
  if (error.code === "email_reply_unsupported" || error.code === "email_send_unsupported") return text.email.sendUnsupported;
  if (error.status === 409) return text.email.stateChanged;
  if (error.status === 401 || error.status === 403) return text.email.notAuthorized;
  return text.email.actionFailed;
}

export function emailSendFailureLabel(code: string | undefined, text: Copy): string {
  if (code === "email_draft_conflict") return text.email.browserDraftConflict;
  if (code === "email_draft_verification_failed" || code === "email_send_control_unverified") return text.email.draftVerificationFailed;
  return text.email.sendFailed;
}

export function emailEventTitle(conversation: EmailConversation | null, text: Copy) {
  const title = conversation?.title?.trim();
  if (!title) return text.email.eventAwaitingName;
  return conversation?.title_state === "source_fallback" ? `${text.email.originalSubject}: ${title}` : title;
}
