import { describe, expect, it } from "vitest";
import { initialVoiceOperationState, reduceVoiceOperation, voicePhaseOwnsWork } from "./voiceState";
import type { VoiceOperationState } from "./voiceState";

describe("voice operation state", () => {
  it("follows the successful capture and transcription lifecycle", () => {
    let state = initialVoiceOperationState;
    for (const event of [
      { type: "start" } as const,
      { type: "microphone_ready_realtime" } as const,
      { type: "realtime_ready" } as const,
      { type: "capture_realtime_ready" } as const,
      { type: "stop_realtime" } as const,
      { type: "realtime_failure" } as const,
      { type: "recovery_captured" } as const,
      { type: "encoded" } as const,
      { type: "reset" } as const
    ]) state = reduceVoiceOperation(state, event);
    expect(state).toEqual(initialVoiceOperationState);
  });

  it("keeps a retryable failure distinct from a terminal failure", () => {
    let state: VoiceOperationState = { ...initialVoiceOperationState, phase: "transcribing" };
    state = reduceVoiceOperation(state, { type: "retryable_failure", code: "speech_busy", detail: "busy" });
    expect(state).toEqual({ phase: "retryable_error", errorCode: "speech_busy", errorDetail: "busy" });
    state = reduceVoiceOperation(state, { type: "retry" });
    expect(state.phase).toBe("transcribing");
  });

  it("rejects invalid transitions and identifies resource-owning phases", () => {
    expect(reduceVoiceOperation(initialVoiceOperationState, { type: "encoded" })).toBe(initialVoiceOperationState);
    expect(voicePhaseOwnsWork("connecting_realtime")).toBe(true);
    expect(voicePhaseOwnsWork("pending_insert")).toBe(false);
    expect(voicePhaseOwnsWork("retryable_error")).toBe(false);
  });
});
