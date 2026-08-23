// Pure voice state / error-code -> copy-key mapping, moved out of
// composer.tsx so components render labels without owning the table.
import type { Copy as CopyText } from "../i18n";
import type { VoicePhase } from "./voiceState";

export type VoiceLabelState = VoicePhase | "disabled";

export function voiceInputTitle(state: VoiceLabelState, label: string, text: CopyText) {
  if (state === "recording_realtime" || state === "recording_batch_only") return text.chat.voiceStop;
  if (state !== "idle" && state !== "disabled" && state !== "error" && state !== "retryable_error" && state !== "pending_insert") {
    return text.chat.voiceCancel;
  }
  if (state === "disabled") return label;
  return text.chat.voiceStart;
}

export function voiceInputLabel(state: VoiceLabelState, errorCode: string, errorDetail: string, deviceFallback: boolean, text: CopyText) {
  if (state === "acquiring_microphone") return text.chat.voiceRequesting;
  if (state === "connecting_realtime") return text.chat.voiceConnectingRealtime;
  if (state === "starting_capture") return text.chat.voiceStarting;
  if (state === "starting_batch_capture") return text.chat.voiceStartingBatch;
  if (state === "recording_realtime") return deviceFallback ? `${text.chat.voiceRecordingRealtime} · ${text.chat.voiceFallback}` : text.chat.voiceRecordingRealtime;
  if (state === "recording_batch_only") return deviceFallback ? `${text.chat.voiceRecordingBatch} · ${text.chat.voiceFallback}` : text.chat.voiceRecordingBatch;
  if (state === "finalizing_realtime") return text.chat.voiceFinalizingRealtime;
  if (state === "recovering_batch") return text.chat.voiceRecoveringBatch;
  if (state === "encoding") return text.chat.voicePreparing;
  if (state === "transcribing") return text.chat.voiceTranscribing;
  if (state === "pending_insert") return text.chat.voicePendingInsert;
  if (state === "retryable_error") return voiceCaptureFailureLabel(errorCode, errorDetail, text);
  return voiceCaptureFailureLabel(errorCode, errorDetail, text, state);
}

export function voiceCaptureFailureLabel(errorCode: string, errorDetail: string, text: CopyText, state?: VoiceLabelState) {
  switch (errorCode) {
    case "voice_capture_unsupported":
      return text.chat.voiceUnsupported;
    case "voice_permission_denied":
      return text.chat.voicePermissionDenied;
    case "voice_no_device":
      return text.chat.voiceNoDevice;
    case "voice_capture_failed":
      return text.chat.voiceCaptureFailed;
    case "voice_capture_start_timeout":
      return text.chat.voiceCaptureStartTimeout;
    case "voice_device_disconnected":
      return text.chat.voiceDeviceDisconnected;
    case "voice_capture_interrupted":
      return text.chat.voiceCaptureInterrupted;
    case "speech_too_short":
      return text.chat.voiceTooShort;
    case "speech_no_speech":
      return text.chat.voiceNoSpeech;
    case "speech_too_large":
      return text.chat.voiceTooLarge;
    case "speech_busy":
      return text.chat.voiceBusy;
    case "speech_disabled":
    case "speech_model_unavailable":
      return state === "disabled" ? text.chat.voiceUnavailable : errorDetail || text.chat.voiceUnavailable;
    case "speech_timeout":
      return text.chat.voiceTimeout;
    case "speech_stream_overrun":
      return text.chat.voiceStreamOverrun;
    case "speech_stream_protocol_error":
      return text.chat.voiceStreamFailed;
    case "speech_retry_expired":
      return text.chat.voiceRetryExpired;
    case "speech_inference_failed":
      return errorDetail || text.chat.voiceFailed;
    default:
      return state === "error" ? errorDetail || text.chat.voiceFailed : errorDetail;
  }
}
