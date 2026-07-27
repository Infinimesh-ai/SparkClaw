export type MessageStreamFailureDisposition = "refresh_session" | "restore_draft";

export function messageStreamFailureDisposition(accepted: boolean): MessageStreamFailureDisposition {
  return accepted ? "refresh_session" : "restore_draft";
}
