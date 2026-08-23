// Maps every failure shape the voice-input stack can produce (capture,
// encoding, API, realtime protocol) onto one { code, detail, retryable }
// record. Moved out of useVoiceInput so the microphone-device hook and the
// state-machine driver share a single mapping.
import { APIError } from "../api/client";
import { VoiceCaptureError } from "../audio/pcmCapture";
import { VoiceAudioError } from "../audio/wav";

export type VoiceFailure = {
  code: string;
  detail: string;
  retryable: boolean;
};

export function voiceFailure(error: unknown): VoiceFailure {
  if (error instanceof VoiceAudioError || error instanceof VoiceCaptureError) {
    return { code: error.code, detail: error.message, retryable: false };
  }
  if (error instanceof APIError) {
    return { code: error.code || "speech_inference_failed", detail: error.message, retryable: error.retryable };
  }
  if (error && typeof error === "object" && "code" in error && typeof error.code === "string") {
    return {
      code: error.code || "speech_inference_failed",
      detail: "detail" in error && typeof error.detail === "string" ? error.detail : "",
      retryable: "retryable" in error && error.retryable === true
    };
  }
  if (error instanceof DOMException) {
    if (error.name === "NotAllowedError" || error.name === "SecurityError") {
      return { code: "voice_permission_denied", detail: error.message, retryable: false };
    }
    if (error.name === "NotFoundError" || error.name === "OverconstrainedError") {
      return { code: "voice_no_device", detail: error.message, retryable: false };
    }
    if (error.name === "NotReadableError" || error.name === "AbortError") {
      return { code: "voice_capture_failed", detail: error.message, retryable: false };
    }
  }
  if (error instanceof TypeError) {
    return { code: "speech_model_unavailable", detail: error.message, retryable: true };
  }
  const detail = error instanceof Error ? error.message : String(error ?? "");
  return {
    code: detail === "voice_capture_unsupported" ? detail : "speech_inference_failed",
    detail,
    retryable: false
  };
}
