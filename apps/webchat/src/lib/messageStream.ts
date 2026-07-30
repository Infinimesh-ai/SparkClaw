// SSE event emitted by the gateway once a message stream is accepted.
// Must stay in sync with the emit site in
// services/gateway/internal/gateway/server.go (postMessageStream).
export const MESSAGE_STREAM_STARTED_EVENT = "message.stream.started";

export type MessageStreamFailureDisposition = "refresh_session" | "restore_draft";

export function messageStreamFailureDisposition(accepted: boolean): MessageStreamFailureDisposition {
  return accepted ? "refresh_session" : "restore_draft";
}
