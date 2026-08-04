// SSE event emitted by the gateway once a message stream is accepted.
// Must stay in sync with the emit site in
// services/gateway/internal/gateway/server.go (postMessageStream).
export const MESSAGE_STREAM_STARTED_EVENT = "message.stream.started";

// "restore_draft": the gateway never accepted the request, so the send failed;
// restore the draft and surface the failure as an error.
// "refresh_session": the gateway accepted the run and keeps executing it
// server-side after a dropped stream; refresh from the server and surface a
// non-error notice, never an error banner.
export type MessageStreamFailureDisposition = "refresh_session" | "restore_draft";

export function messageStreamFailureDisposition(accepted: boolean): MessageStreamFailureDisposition {
  return accepted ? "refresh_session" : "restore_draft";
}

export function hasPersistedResultMessage(message: { id?: string } | null | undefined): boolean {
  return Boolean(message?.id?.trim());
}
