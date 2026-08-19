export type VoicePhase =
  | "idle"
  | "acquiring_microphone"
  | "connecting_realtime"
  | "starting_capture"
  | "starting_batch_capture"
  | "recording_realtime"
  | "recording_batch_only"
  | "finalizing_realtime"
  | "recovering_batch"
  | "encoding"
  | "transcribing"
  | "retryable_error"
  | "pending_insert"
  | "error";

export type VoiceOperationState = {
  phase: VoicePhase;
  errorCode: string;
  errorDetail: string;
};

export type VoiceStateEvent =
  | { type: "start" }
  | { type: "microphone_ready_realtime" }
  | { type: "microphone_ready_batch" }
  | { type: "realtime_ready" }
  | { type: "realtime_unavailable" }
  | { type: "capture_realtime_ready" }
  | { type: "capture_batch_ready" }
  | { type: "stop_realtime" }
  | { type: "stop_batch" }
  | { type: "realtime_failure" }
  | { type: "recovery_captured" }
  | { type: "encoded" }
  | { type: "retry" }
  | { type: "retryable_failure"; code: string; detail: string }
  | { type: "pending_insert" }
  | { type: "failure"; code: string; detail: string }
  | { type: "reset" };

export const initialVoiceOperationState: VoiceOperationState = {
  phase: "idle",
  errorCode: "",
  errorDetail: ""
};

const transitions: Record<VoicePhase, Partial<Record<VoiceStateEvent["type"], VoicePhase>>> = {
  idle: { start: "acquiring_microphone", reset: "idle" },
  acquiring_microphone: {
    microphone_ready_realtime: "connecting_realtime",
    microphone_ready_batch: "starting_batch_capture",
    failure: "error",
    reset: "idle"
  },
  connecting_realtime: {
    realtime_ready: "starting_capture",
    realtime_unavailable: "starting_batch_capture",
    failure: "error",
    reset: "idle"
  },
  starting_capture: {
    capture_realtime_ready: "recording_realtime",
    realtime_failure: "recovering_batch",
    failure: "error",
    reset: "idle"
  },
  starting_batch_capture: { capture_batch_ready: "recording_batch_only", failure: "error", reset: "idle" },
  recording_realtime: {
    stop_realtime: "finalizing_realtime",
    realtime_failure: "recovering_batch",
    failure: "error",
    reset: "idle"
  },
  recording_batch_only: { stop_batch: "encoding", failure: "error", reset: "idle" },
  finalizing_realtime: {
    realtime_failure: "recovering_batch",
    pending_insert: "pending_insert",
    failure: "error",
    reset: "idle"
  },
  recovering_batch: { recovery_captured: "encoding", failure: "error", reset: "idle" },
  encoding: { encoded: "transcribing", failure: "error", reset: "idle" },
  transcribing: {
    retryable_failure: "retryable_error",
    pending_insert: "pending_insert",
    failure: "error",
    reset: "idle"
  },
  retryable_error: { retry: "transcribing", start: "acquiring_microphone", failure: "error", reset: "idle" },
  pending_insert: { start: "acquiring_microphone", failure: "error", reset: "idle" },
  error: { start: "acquiring_microphone", reset: "idle" }
};

export function reduceVoiceOperation(state: VoiceOperationState, event: VoiceStateEvent): VoiceOperationState {
  const nextPhase = transitions[state.phase][event.type];
  if (!nextPhase) return state;
  if (event.type === "failure" || event.type === "retryable_failure") {
    return { phase: nextPhase, errorCode: event.code, errorDetail: event.detail };
  }
  return { phase: nextPhase, errorCode: "", errorDetail: "" };
}

export function voicePhaseOwnsWork(phase: VoicePhase) {
  return phase === "acquiring_microphone" || phase === "connecting_realtime" ||
    phase === "starting_capture" || phase === "starting_batch_capture" ||
    phase === "recording_realtime" || phase === "recording_batch_only" ||
    phase === "finalizing_realtime" || phase === "recovering_batch" ||
    phase === "encoding" || phase === "transcribing";
}

export function voicePhaseIsRecording(phase: VoicePhase) {
  return phase === "recording_realtime" || phase === "recording_batch_only";
}
