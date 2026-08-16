// SSE event emitted by the gateway once a message stream is accepted.
// Must stay in sync with the emit site in
// services/gateway/internal/gateway/server.go (postMessageStream).
export const MESSAGE_STREAM_STARTED_EVENT = "message.stream.started";

// SSE event emitted by the gateway when the run finished but handing its
// result to the selected external endpoint failed. Must stay in sync with the
// emit site in services/gateway/internal/gateway/server.go (postMessageStream).
export const MESSAGE_STREAM_DELIVERY_FAILED_EVENT = "message.stream.delivery_failed";

// Raised by the API client when the gateway reports a post-run delivery
// failure; carries the gateway's delivery error message. Kept here so the
// failure disposition below stays the single source of truth for how stream
// failures are classified.
export class MessageStreamDeliveryError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "MessageStreamDeliveryError";
  }
}

// "restore_draft": the gateway never accepted the request, so the send failed;
// restore the draft and surface the failure as an error.
// "refresh_session": the gateway accepted the run and keeps executing it
// server-side after a dropped stream; refresh from the server and surface a
// non-error notice, never an error banner.
// "delivery_failed": the run finished and its result is persisted, but
// delivering it to the selected external endpoint failed; restore the draft
// and surface the delivery error as a real error, never the benign
// stream-detached notice.
export type MessageStreamFailureDisposition = "refresh_session" | "restore_draft" | "delivery_failed";

export function messageStreamFailureDisposition(accepted: boolean, error?: unknown): MessageStreamFailureDisposition {
  if (error instanceof MessageStreamDeliveryError) return "delivery_failed";
  return accepted ? "refresh_session" : "restore_draft";
}

export function hasPersistedResultMessage(message: { id?: string } | null | undefined): boolean {
  return Boolean(message?.id?.trim());
}
