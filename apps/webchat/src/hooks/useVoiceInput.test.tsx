// @vitest-environment jsdom

import { act, StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { APIError, api } from "../api/client";
import type { SpeechRealtimeTicket, SpeechStatus, SpeechTranscriptionResult } from "../api/types";

const pcmMocks = vi.hoisted(() => ({ prepare: vi.fn(), start: vi.fn() }));
const realtimeMocks = vi.hoisted(() => ({ connect: vi.fn() }));

vi.mock("../audio/pcmCapture", () => {
  class VoiceCaptureError extends Error {
    code: string;
    constructor(code: string, message: string) {
      super(message);
      this.code = code;
    }
  }
  return {
    PCMInputCapture: { supported: () => true, prepare: pcmMocks.prepare, start: pcmMocks.start },
    VoiceCaptureError
  };
});

vi.mock("../audio/realtimeSpeech", () => ({
  SpeechRealtimeClient: { connect: realtimeMocks.connect }
}));

import { useVoiceInput } from "./useVoiceInput";
import type { VoiceDraftAnchor } from "./useVoiceInput";

const batchSpeech: SpeechStatus = {
  enabled: true,
  ready: true,
  state: "ready",
  backend: "openai-http",
  model: "test-asr",
  supports_streaming: false,
  accepted_content_types: ["audio/wav"],
  max_audio_seconds: 60,
  max_upload_bytes: 3 << 20
};

const realtimeSpeech: SpeechStatus = {
  ...batchSpeech,
  supports_streaming: true,
  realtime: {
    protocol: "sparkclaw.speech.realtime.v1",
    sample_rate: 16_000,
    channels: 1,
    bits_per_sample: 16,
    frame_ms: 100
  }
};

const ticket: SpeechRealtimeTicket = {
  id: "speech-rt-a",
  url: "/api/speech/realtime?ticket=secret",
  expires_at: new Date(Date.now() + 30_000).toISOString(),
  protocol: "sparkclaw.speech.realtime.v1",
  format: { sample_rate: 16_000, channels: 1, bits_per_sample: 16, frame_ms: 100 },
  limits: { max_audio_seconds: 60, max_frame_samples: 1_600 }
};

const anchor: VoiceDraftAnchor = {
  sessionId: "session-a",
  draft: "before",
  selectionStart: 6,
  selectionEnd: 6
};

const result: SpeechTranscriptionResult = {
  id: "stt-a",
  request_id: "voice-a",
  session_id: "session-a",
  text: "transcript",
  duration_ms: 1000,
  inference_ms: 25,
  audio_retained: false
};

type HookValue = ReturnType<typeof useVoiceInput>;

describe("useVoiceInput", () => {
  const roots: ReturnType<typeof createRoot>[] = [];

  beforeEach(() => {
    (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    Object.defineProperty(window, "localStorage", {
      configurable: true,
      value: { getItem: () => null, setItem: vi.fn(), removeItem: vi.fn() }
    });
    Object.defineProperty(navigator, "mediaDevices", {
      configurable: true,
      value: {
        enumerateDevices: vi.fn(async () => []),
        addEventListener: vi.fn(),
        removeEventListener: vi.fn()
      }
    });
    pcmMocks.prepare.mockReset();
    pcmMocks.start.mockReset();
    realtimeMocks.connect.mockReset();
    vi.restoreAllMocks();
  });

  afterEach(() => {
    for (const root of roots.splice(0)) act(() => root.unmount());
  });

  function capture() {
    return {
      usedDefaultFallback: false,
      effectiveDeviceId: "device-a",
      start: vi.fn(async () => {}),
      stop: vi.fn(async () => ({ samples: new Int16Array(16000), sampleRate: 16000, durationMs: 1000 })),
      cancel: vi.fn(async () => {})
    };
  }

  function realtime() {
    return {
      push: vi.fn(),
      finish: vi.fn(async () => ({
        revision: 2,
        text: "live final",
        language: "English",
        durationMs: 1000,
        inferenceMs: 20,
        model: "test-asr",
        stopReason: "manual_stop"
      })),
      closeForFallback: vi.fn(async () => {}),
      cancel: vi.fn(async () => {})
    };
  }

  function renderHook(
    speech: SpeechStatus = batchSpeech,
    onTranscript = vi.fn(() => true)
  ) {
    const container = document.createElement("div");
    const root = createRoot(container);
    roots.push(root);
    let value: HookValue | undefined;
    function Harness() {
      value = useVoiceInput({ speech, sessionId: "session-a", language: "auto", externallyDisabled: false, onTranscript });
      return null;
    }
    act(() => root.render(<StrictMode><Harness /></StrictMode>));
    return { value: () => value as HookValue, onTranscript };
  }

  async function settle() {
    await act(async () => {
      for (let index = 0; index < 8; index += 1) await Promise.resolve();
    });
  }

  it("retries the byte-identical WAV with the same operation request ID", async () => {
    const currentCapture = capture();
    pcmMocks.prepare.mockResolvedValue(currentCapture);
    const transcribe = vi.spyOn(api, "transcribeSpeech")
      .mockRejectedValueOnce(new APIError(429, "busy", "speech_busy", true))
      .mockResolvedValueOnce(result);
    const hook = renderHook();

    act(() => hook.value().toggle(anchor));
    await settle();
    expect(hook.value().state).toBe("recording_batch_only");
    act(() => hook.value().toggle(anchor));
    await settle();
    expect(hook.value().state).toBe("retryable_error");
    expect(currentCapture.stop).toHaveBeenCalledOnce();

    act(() => hook.value().retry());
    await settle();
    expect(hook.value().state).toBe("idle");
    expect(transcribe).toHaveBeenCalledTimes(2);
    expect(transcribe.mock.calls[1][1]).toBe(transcribe.mock.calls[0][1]);
    expect(transcribe.mock.calls[1][3]).toBe(transcribe.mock.calls[0][3]);
  });

  it("uses realtime final without making a batch request", async () => {
    const currentCapture = capture();
    const currentRealtime = realtime();
    pcmMocks.prepare.mockResolvedValue(currentCapture);
    vi.spyOn(api, "createSpeechRealtimeSession").mockResolvedValue(ticket);
    const transcribe = vi.spyOn(api, "transcribeSpeech");
    let handlers: { onPartial: (event: { revision: number; text: string; language: string; audioEndMs: number }) => void } | undefined;
    realtimeMocks.connect.mockImplementation(async (_ticket, nextHandlers) => {
      handlers = nextHandlers;
      return currentRealtime;
    });
    const hook = renderHook(realtimeSpeech);

    act(() => hook.value().toggle(anchor));
    await settle();
    expect(hook.value().state).toBe("recording_realtime");
    act(() => handlers?.onPartial({ revision: 1, text: "live partial", language: "English", audioEndMs: 800 }));
    expect(hook.value().partialText).toBe("live partial");
    act(() => hook.value().toggle(anchor));
    await settle();

    expect(hook.value().state).toBe("idle");
    expect(transcribe).not.toHaveBeenCalled();
    expect(hook.onTranscript).toHaveBeenCalledWith(expect.objectContaining({ text: "live final" }), anchor);
  });

  it("falls back to a visibly batch-only recording when realtime setup fails", async () => {
    const currentCapture = capture();
    pcmMocks.prepare.mockResolvedValue(currentCapture);
    vi.spyOn(api, "createSpeechRealtimeSession").mockResolvedValue(ticket);
    vi.spyOn(api, "cancelSpeechRealtimeSession").mockResolvedValue({ cancelled: true });
    realtimeMocks.connect.mockRejectedValue({ code: "speech_busy", retryable: true });
    const hook = renderHook(realtimeSpeech);

    act(() => hook.value().toggle(anchor));
    await settle();

    expect(currentCapture.start).toHaveBeenCalledOnce();
    expect(hook.value().state).toBe("recording_batch_only");
  });

  it("stops once and automatically batch-transcribes retained audio after a mid-stream failure", async () => {
    const currentCapture = capture();
    const currentRealtime = realtime();
    pcmMocks.prepare.mockResolvedValue(currentCapture);
    vi.spyOn(api, "createSpeechRealtimeSession").mockResolvedValue(ticket);
    const transcribe = vi.spyOn(api, "transcribeSpeech").mockResolvedValue(result);
    let handlers: { onFailure: (failure: { code: string; retryable: boolean }) => void } | undefined;
    realtimeMocks.connect.mockImplementation(async (_ticket, nextHandlers) => {
      handlers = nextHandlers;
      return currentRealtime;
    });
    const hook = renderHook(realtimeSpeech);

    act(() => hook.value().toggle(anchor));
    await settle();
    act(() => handlers?.onFailure({ code: "speech_stream_overrun", retryable: true }));
    await settle();

    expect(currentCapture.stop).toHaveBeenCalledOnce();
    expect(currentRealtime.closeForFallback).toHaveBeenCalledOnce();
    expect(transcribe).toHaveBeenCalledOnce();
    expect(hook.value().state).toBe("idle");
    expect(hook.onTranscript).toHaveBeenCalledWith(result, anchor);
  });

  it("keeps a stale transcript pending until the owner inserts it", async () => {
    pcmMocks.prepare.mockResolvedValue(capture());
    vi.spyOn(api, "transcribeSpeech").mockResolvedValue(result);
    const onTranscript = vi.fn().mockReturnValueOnce(false).mockReturnValueOnce(true);
    const hook = renderHook(batchSpeech, onTranscript);

    act(() => hook.value().toggle(anchor));
    await settle();
    act(() => hook.value().toggle(anchor));
    await settle();
    expect(hook.value().state).toBe("pending_insert");

    const currentAnchor = { ...anchor, draft: "owner changed this", selectionStart: 18, selectionEnd: 18 };
    act(() => hook.value().insertPending(currentAnchor));
    expect(hook.value().state).toBe("idle");
    expect(onTranscript).toHaveBeenLastCalledWith(result, currentAnchor);
  });

  it("cancels capture and the realtime client when unmounted mid-recording", async () => {
    const currentCapture = capture();
    const currentRealtime = realtime();
    pcmMocks.prepare.mockResolvedValue(currentCapture);
    vi.spyOn(api, "createSpeechRealtimeSession").mockResolvedValue(ticket);
    const cancelSession = vi.spyOn(api, "cancelSpeechRealtimeSession").mockResolvedValue({ cancelled: true });
    realtimeMocks.connect.mockResolvedValue(currentRealtime);
    const hook = renderHook(realtimeSpeech);

    act(() => hook.value().toggle(anchor));
    await settle();
    expect(hook.value().state).toBe("recording_realtime");

    act(() => { for (const root of roots.splice(0)) root.unmount(); });

    expect(currentCapture.cancel).toHaveBeenCalled();
    expect(currentRealtime.cancel).toHaveBeenCalled();
    // The ticket was consumed by the successful connect: no spurious release.
    expect(cancelSession).not.toHaveBeenCalled();
  });

  it("releases a pending realtime ticket and cancels the late client on unmount", async () => {
    const currentCapture = capture();
    const currentRealtime = realtime();
    pcmMocks.prepare.mockResolvedValue(currentCapture);
    vi.spyOn(api, "createSpeechRealtimeSession").mockResolvedValue(ticket);
    const cancelSession = vi.spyOn(api, "cancelSpeechRealtimeSession").mockResolvedValue({ cancelled: true });
    let resolveConnect: ((client: ReturnType<typeof realtime>) => void) | undefined;
    realtimeMocks.connect.mockReturnValue(new Promise((resolve) => { resolveConnect = resolve; }));
    const hook = renderHook(realtimeSpeech);

    act(() => hook.value().toggle(anchor));
    await settle();
    expect(hook.value().state).toBe("connecting_realtime");

    act(() => { for (const root of roots.splice(0)) root.unmount(); });
    await act(async () => {
      for (let index = 0; index < 8; index += 1) await Promise.resolve();
    });

    expect(currentCapture.cancel).toHaveBeenCalled();
    expect(cancelSession).toHaveBeenCalledWith(ticket.id);

    await act(async () => {
      resolveConnect?.(currentRealtime);
      for (let index = 0; index < 8; index += 1) await Promise.resolve();
    });
    expect(currentRealtime.cancel).toHaveBeenCalled();
  });

  it("ignores a transcription result that arrives after cancellation", async () => {
    pcmMocks.prepare.mockResolvedValue(capture());
    let resolveTranscription: ((value: SpeechTranscriptionResult) => void) | undefined;
    vi.spyOn(api, "transcribeSpeech").mockReturnValue(new Promise((resolve) => { resolveTranscription = resolve; }));
    const hook = renderHook();

    act(() => hook.value().toggle(anchor));
    await settle();
    act(() => hook.value().toggle(anchor));
    await settle();
    expect(hook.value().state).toBe("transcribing");
    await act(async () => { await hook.value().cancel(); });
    await act(async () => { resolveTranscription?.(result); await Promise.resolve(); });
    expect(hook.value().state).toBe("idle");
    expect(hook.onTranscript).not.toHaveBeenCalled();
  });
});
