import { useCallback, useEffect, useReducer, useRef, useState } from "react";
import { api } from "../api/client";
import type { SpeechStatus, SpeechTranscriptionResult } from "../api/types";
import { PCMInputCapture, VoiceCaptureError } from "../audio/pcmCapture";
import type { CapturedPCM } from "../audio/pcmCapture";
import { SpeechRealtimeClient } from "../audio/realtimeSpeech";
import type { SpeechRealtimeFailure } from "../audio/realtimeSpeech";
import {
  loadSilenceMode,
  saveSilenceMode,
  SilenceDetector
} from "../audio/silenceDetector";
import type { SilenceMode } from "../audio/silenceDetector";
import { encodeSpeechWAV } from "../audio/wav";
import { voiceFailure } from "../lib/voiceErrors";
import { useMicrophoneDevices } from "./useMicrophoneDevices";
import {
  initialVoiceOperationState,
  reduceVoiceOperation,
  voicePhaseIsRecording,
  voicePhaseOwnsWork
} from "../lib/voiceState";
import type { VoicePhase } from "../lib/voiceState";

export type VoiceInputState = VoicePhase | "disabled";

export type VoiceDraftAnchor = {
  sessionId: string;
  draft: string;
  selectionStart: number;
  selectionEnd: number;
};

type VoicePartial = {
  revision: number;
  text: string;
  language: string;
  frozen: boolean;
};

type VoiceOperation = {
  id: number;
  anchor: VoiceDraftAnchor;
  requestId: string;
  capture?: PCMInputCapture;
  realtime?: SpeechRealtimeClient;
  realtimeCaptureStarted: boolean;
  realtimeSetupFailure?: SpeechRealtimeFailure;
  ticketId?: string;
  setupController?: AbortController;
  controller?: AbortController;
  captured?: CapturedPCM;
  wav?: Blob;
  pending?: SpeechTranscriptionResult;
  silence: SilenceDetector;
  recovery?: Promise<void>;
  interval?: number;
  timeout?: number;
  retryTimeout?: number;
  startedAt: number;
};

type Options = {
  speech: SpeechStatus | null;
  sessionId: string;
  language: string;
  externallyDisabled: boolean;
  onTranscript: (result: SpeechTranscriptionResult, anchor: VoiceDraftAnchor) => boolean;
};

const RETRY_AUDIO_TTL_MS = 5 * 60 * 1000;

export function useVoiceInput({ speech, sessionId, language, externallyDisabled, onTranscript }: Options) {
  const [machine, dispatch] = useReducer(reduceVoiceOperation, initialVoiceOperationState);
  const [level, setLevel] = useState(0);
  const [elapsedMs, setElapsedMs] = useState(0);
  const [partial, setPartial] = useState<VoicePartial | null>(null);
  const [silenceMode, setSilenceModeState] = useState<SilenceMode>(() => loadSilenceMode());
  const operation = useRef<VoiceOperation | null>(null);
  const generation = useRef(0);
  const recoverRef = useRef<((current: VoiceOperation, failure: unknown, captured?: CapturedPCM) => Promise<void>) | undefined>(undefined);
  const stopRef = useRef<((reason?: "manual_stop" | "silence_stop" | "max_duration") => Promise<void>) | undefined>(undefined);
  const noSpeechRef = useRef<((current: VoiceOperation) => Promise<void>) | undefined>(undefined);
  const supported = PCMInputCapture.supported();
  const capabilityReady = Boolean(speech?.enabled && speech.ready);
  const maxAudioSeconds = speech?.max_audio_seconds ?? 60;
  const previewBlocked = useCallback(() => operation.current !== null, []);
  const microphone = useMicrophoneDevices({ supported, previewBlocked });
  const {
    refreshDevices,
    stopPreview,
    noteDefaultFallback,
    clearDeviceFallback,
    selectedDeviceId
  } = microphone;
  const realtimeReady = Boolean(
    capabilityReady && speech?.supports_streaming && speech.realtime?.protocol === "sparkclaw.speech.realtime.v1" &&
    speech.realtime.sample_rate === 16_000 && speech.realtime.channels === 1 && speech.realtime.bits_per_sample === 16
  );
  const state: VoiceInputState = supported && capabilityReady ? machine.phase : "disabled";
  const active = voicePhaseOwnsWork(machine.phase);

  const clearTimers = useCallback((current: VoiceOperation | null) => {
    if (current?.interval) window.clearInterval(current.interval);
    if (current?.timeout) window.clearTimeout(current.timeout);
    if (current?.retryTimeout) window.clearTimeout(current.retryTimeout);
    if (current) {
      current.interval = undefined;
      current.timeout = undefined;
      current.retryTimeout = undefined;
    }
  }, []);

  const setSilenceMode = useCallback((mode: SilenceMode) => {
    setSilenceModeState(mode);
    saveSilenceMode(mode);
  }, []);

  const releaseUnusedTicket = useCallback(async (current: VoiceOperation) => {
    const ticketId = current.ticketId;
    current.ticketId = undefined;
    if (!ticketId) return;
    await api.cancelSpeechRealtimeSession(ticketId).catch(() => undefined);
  }, []);

  const cancel = useCallback(async () => {
    generation.current += 1;
    const current = operation.current;
    operation.current = null;
    clearTimers(current);
    current?.setupController?.abort();
    current?.controller?.abort();
    await Promise.all([
      current?.capture?.cancel().catch(() => undefined),
      current?.realtime?.cancel().catch(() => undefined),
      current ? releaseUnusedTicket(current) : Promise.resolve()
    ]);
    setLevel(0);
    setElapsedMs(0);
    setPartial(null);
    clearDeviceFallback();
    dispatch({ type: "reset" });
  }, [clearDeviceFallback, clearTimers, releaseUnusedTicket]);

  const failOperation = useCallback((current: VoiceOperation, error: unknown) => {
    if (operation.current !== current) return;
    operation.current = null;
    clearTimers(current);
    setLevel(0);
    setElapsedMs(0);
    setPartial(null);
    const failure = voiceFailure(error);
    dispatch({ type: "failure", code: failure.code, detail: failure.detail });
  }, [clearTimers]);

  const applyResult = useCallback((current: VoiceOperation, result: SpeechTranscriptionResult) => {
    if (operation.current !== current) return;
    setPartial(null);
    if (!result.text.trim()) {
      failOperation(current, { code: "speech_no_speech" });
      return;
    }
    if (!onTranscript(result, current.anchor)) {
      current.pending = result;
      current.wav = undefined;
      dispatch({ type: "pending_insert" });
      return;
    }
    operation.current = null;
    clearTimers(current);
    setLevel(0);
    setElapsedMs(0);
    dispatch({ type: "reset" });
  }, [clearTimers, failOperation, onTranscript]);

  const transcribe = useCallback(async (current: VoiceOperation) => {
    if (!current.wav) return;
    const id = current.id;
    const controller = new AbortController();
    current.controller = controller;
    try {
      const result = await api.transcribeSpeech(
        current.anchor.sessionId,
        current.requestId,
        language || "auto",
        current.wav,
        controller.signal
      );
      current.controller = undefined;
      if (generation.current !== id || operation.current !== current) return;
      applyResult(current, result);
    } catch (error) {
      current.controller = undefined;
      if (generation.current !== id || operation.current !== current) return;
      if (error instanceof DOMException && error.name === "AbortError") return;
      const failure = voiceFailure(error);
      if (failure.retryable && current.wav) {
        if (current.retryTimeout) window.clearTimeout(current.retryTimeout);
        current.retryTimeout = window.setTimeout(() => {
          if (generation.current !== id || operation.current !== current) return;
          operation.current = null;
          setPartial(null);
          dispatch({ type: "failure", code: "speech_retry_expired", detail: "" });
        }, RETRY_AUDIO_TTL_MS);
        dispatch({ type: "retryable_failure", code: failure.code, detail: failure.detail });
        return;
      }
      failOperation(current, error);
    }
  }, [applyResult, failOperation, language]);

  const encodeAndTranscribe = useCallback(async (current: VoiceOperation, captured: CapturedPCM, recovering: boolean) => {
    if (operation.current !== current) return;
    current.captured = captured;
    if (recovering) dispatch({ type: "recovery_captured" });
    try {
      current.wav = encodeSpeechWAV(
        captured,
        maxAudioSeconds,
        speech?.max_upload_bytes ?? 3 * 1024 * 1024
      );
      dispatch({ type: "encoded" });
      await transcribe(current);
    } catch (error) {
      failOperation(current, error);
    }
  }, [failOperation, maxAudioSeconds, speech?.max_upload_bytes, transcribe]);

  const recoverBatch = useCallback((current: VoiceOperation, _failure: unknown, captured?: CapturedPCM) => {
    if (current.recovery) return current.recovery;
    current.recovery = (async () => {
      if (operation.current !== current) return;
      clearTimers(current);
      dispatch({ type: "realtime_failure" });
      setPartial((value) => value ? { ...value, frozen: true } : null);
      const realtime = current.realtime;
      current.realtime = undefined;
      const release = realtime?.closeForFallback().catch(() => undefined);
      let fallbackCapture = captured;
      if (!fallbackCapture && current.capture) {
        fallbackCapture = await current.capture.stop();
      }
      current.capture = undefined;
      await release;
      if (operation.current !== current || generation.current !== current.id) return;
      if (!fallbackCapture) throw new VoiceCaptureError("voice_capture_interrupted", "microphone capture ended before audio could be retained");
      await encodeAndTranscribe(current, fallbackCapture, true);
    })().catch((error) => failOperation(current, error));
    return current.recovery;
  }, [clearTimers, encodeAndTranscribe, failOperation]);

  useEffect(() => {
    recoverRef.current = recoverBatch;
  }, [recoverBatch]);

  const stop = useCallback(async (reason: "manual_stop" | "silence_stop" | "max_duration" = "manual_stop") => {
    const current = operation.current;
    if (!current?.capture || current.recovery) return;
    clearTimers(current);
    if (current.realtime) {
      dispatch({ type: "stop_realtime" });
      try {
        const captured = await current.capture.stop();
        current.capture = undefined;
        current.captured = captured;
        if (generation.current !== current.id || operation.current !== current || current.recovery) return;
        const final = await current.realtime.finish(reason);
        if (generation.current !== current.id || operation.current !== current || current.recovery) return;
        current.realtime = undefined;
        applyResult(current, {
          id: `stt-${current.requestId}`,
          request_id: current.requestId,
          session_id: current.anchor.sessionId,
          text: final.text,
          language: final.language,
          duration_ms: final.durationMs,
          inference_ms: final.inferenceMs,
          model: final.model,
          audio_retained: false
        });
      } catch (error) {
        await recoverBatch(current, error, current.captured);
      }
      return;
    }
    dispatch({ type: "stop_batch" });
    try {
      const captured = await current.capture.stop();
      current.capture = undefined;
      if (generation.current !== current.id || operation.current !== current) return;
      await encodeAndTranscribe(current, captured, false);
    } catch (error) {
      failOperation(current, error);
    }
  }, [applyResult, clearTimers, encodeAndTranscribe, failOperation, recoverBatch]);

  useEffect(() => {
    stopRef.current = stop;
  }, [stop]);

  const noSpeechCancel = useCallback(async (current: VoiceOperation) => {
    if (operation.current !== current) return;
    generation.current += 1;
    operation.current = null;
    clearTimers(current);
    await Promise.all([
      current.capture?.cancel().catch(() => undefined),
      current.realtime?.cancel().catch(() => undefined),
      releaseUnusedTicket(current)
    ]);
    setLevel(0);
    setElapsedMs(0);
    setPartial(null);
    dispatch({ type: "failure", code: "speech_no_speech", detail: "" });
  }, [clearTimers, releaseUnusedTicket]);

  useEffect(() => {
    noSpeechRef.current = noSpeechCancel;
  }, [noSpeechCancel]);

  const startCaptureTimers = useCallback((current: VoiceOperation) => {
    current.startedAt = Date.now();
    current.interval = window.setInterval(() => setElapsedMs(Date.now() - current.startedAt), 250);
    current.timeout = window.setTimeout(() => void stopRef.current?.("max_duration"), maxAudioSeconds * 1000);
  }, [maxAudioSeconds]);

  const handleCapturedSamples = useCallback((current: VoiceOperation, samples: Int16Array) => {
    if (operation.current !== current || generation.current !== current.id || current.recovery) return;
    current.realtime?.push(samples);
    if (current.recovery) return;
    const decision = current.silence.process(samples);
    if (decision === "auto_stop") void stopRef.current?.("silence_stop");
    if (decision === "no_speech_cancel") void noSpeechRef.current?.(current);
  }, []);

  const startBatchCapture = useCallback(async (current: VoiceOperation) => {
    if (!current.capture || operation.current !== current) return;
    try {
      await current.capture.start();
      if (operation.current !== current || generation.current !== current.id) return;
      startCaptureTimers(current);
      dispatch({ type: "capture_batch_ready" });
      void refreshDevices();
    } catch (error) {
      await current.capture?.cancel().catch(() => undefined);
      current.capture = undefined;
      failOperation(current, error);
    }
  }, [failOperation, refreshDevices, startCaptureTimers]);

  const start = useCallback(async (anchor: VoiceDraftAnchor) => {
    if (!supported || !capabilityReady || externallyDisabled || operation.current) return;
    const id = ++generation.current;
    const current: VoiceOperation = {
      id,
      anchor,
      requestId: createRequestID(),
      realtimeCaptureStarted: false,
      silence: new SilenceDetector(silenceMode),
      startedAt: Date.now()
    };
    operation.current = current;
    clearDeviceFallback();
    setElapsedMs(0);
    setLevel(0);
    setPartial(null);
    dispatch({ type: "start" });
    await stopPreview();
    if (generation.current !== id || operation.current !== current) return;
    try {
      current.capture = await PCMInputCapture.prepare({
        deviceId: selectedDeviceId,
        onLevel: setLevel,
        onSamples: (samples) => handleCapturedSamples(current, samples),
        onFailure: (error, captured) => {
          if (generation.current !== id || operation.current !== current) return;
          current.capture = undefined;
          if (current.realtimeCaptureStarted) {
            void recoverRef.current?.(current, error, captured);
          } else if (captured) {
            dispatch({ type: "stop_batch" });
            void encodeAndTranscribe(current, captured, false);
          } else {
            failOperation(current, error);
          }
        }
      });
      if (current.capture.usedDefaultFallback) noteDefaultFallback();
    } catch (error) {
      failOperation(current, error);
      return;
    }
    if (generation.current !== id || operation.current !== current) {
      await current.capture?.cancel().catch(() => undefined);
      return;
    }
    if (!realtimeReady) {
      dispatch({ type: "microphone_ready_batch" });
      await startBatchCapture(current);
      return;
    }

    dispatch({ type: "microphone_ready_realtime" });
    try {
      const controller = new AbortController();
      current.setupController = controller;
      const ticket = await api.createSpeechRealtimeSession(
        current.anchor.sessionId,
        current.requestId,
        language || "auto",
        controller.signal
      );
      current.ticketId = ticket.id;
      const realtime = await SpeechRealtimeClient.connect(ticket, {
        onPartial: (event) => {
          if (generation.current !== id || operation.current !== current || current.recovery) return;
          setPartial((previous) => !previous || event.revision > previous.revision
            ? { revision: event.revision, text: event.text, language: event.language, frozen: false }
            : previous);
        },
        onFailure: (failure) => {
          if (generation.current !== id || operation.current !== current) return;
          if (!current.realtimeCaptureStarted) {
            current.realtimeSetupFailure = failure;
            return;
          }
          void recoverRef.current?.(current, failure);
        }
      });
      current.ticketId = undefined;
      current.setupController = undefined;
      if (current.realtimeSetupFailure) {
        await realtime.cancel();
        throw current.realtimeSetupFailure;
      }
      if (generation.current !== id || operation.current !== current) {
        await realtime.cancel();
        return;
      }
      current.realtime = realtime;
      dispatch({ type: "realtime_ready" });
      current.realtimeCaptureStarted = true;
      await current.capture.start();
      if (current.recovery) {
        await current.recovery;
        return;
      }
      if (generation.current !== id || operation.current !== current) return;
      startCaptureTimers(current);
      dispatch({ type: "capture_realtime_ready" });
      void refreshDevices();
    } catch (error) {
      current.setupController = undefined;
      await releaseUnusedTicket(current);
      if (current.recovery) {
        await current.recovery;
        return;
      }
      if (current.realtimeCaptureStarted) {
        await recoverBatch(current, error);
        return;
      }
      current.realtime = undefined;
      setPartial(null);
      if (generation.current !== id || operation.current !== current) return;
      dispatch({ type: "realtime_unavailable" });
      await startBatchCapture(current);
    }
  }, [capabilityReady, clearDeviceFallback, encodeAndTranscribe, externallyDisabled, failOperation, handleCapturedSamples, language, noteDefaultFallback, realtimeReady, recoverBatch, refreshDevices, releaseUnusedTicket, selectedDeviceId, silenceMode, startBatchCapture, startCaptureTimers, stopPreview, supported]);

  const retry = useCallback(() => {
    const current = operation.current;
    if (!current?.wav || machine.phase !== "retryable_error") return;
    if (current.retryTimeout) window.clearTimeout(current.retryTimeout);
    current.retryTimeout = undefined;
    dispatch({ type: "retry" });
    void transcribe(current);
  }, [machine.phase, transcribe]);

  const insertPending = useCallback((anchor: VoiceDraftAnchor) => {
    const current = operation.current;
    if (!current?.pending || machine.phase !== "pending_insert") return;
    if (!onTranscript(current.pending, anchor)) return;
    operation.current = null;
    clearTimers(current);
    dispatch({ type: "reset" });
  }, [clearTimers, machine.phase, onTranscript]);

  const toggle = useCallback((anchor: VoiceDraftAnchor) => {
    if (voicePhaseIsRecording(machine.phase)) {
      void stop();
    } else if (voicePhaseOwnsWork(machine.phase)) {
      void cancel();
    } else {
      void (async () => {
        if (operation.current) await cancel();
        await start(anchor);
      })();
    }
  }, [cancel, machine.phase, start, stop]);

  useEffect(() => {
    if (operation.current && operation.current.anchor.sessionId !== sessionId) void cancel();
    void stopPreview();
  }, [cancel, sessionId, stopPreview]);

  useEffect(() => {
    if ((!supported || !capabilityReady) && operation.current) void cancel();
    if (!supported || !capabilityReady) void stopPreview();
  }, [cancel, capabilityReady, stopPreview, supported]);

  useEffect(() => {
    const onKeyDown = (event: globalThis.KeyboardEvent) => {
      if (event.key === "Escape" && operation.current) void cancel();
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [cancel]);

  useEffect(() => {
    return () => {
      generation.current += 1;
      const current = operation.current;
      operation.current = null;
      clearTimers(current);
      current?.setupController?.abort();
      current?.controller?.abort();
      void current?.capture?.cancel();
      void current?.realtime?.cancel();
      if (current) void releaseUnusedTicket(current);
    };
  }, [clearTimers, releaseUnusedTicket]);

  return {
    state,
    active,
    level,
    elapsedMs,
    partialText: partial?.text ?? "",
    partialFrozen: partial?.frozen ?? false,
    silenceMode,
    errorCode: !supported ? "voice_capture_unsupported" : !capabilityReady ? "speech_model_unavailable" : machine.errorCode,
    errorDetail: !supported ? "" : !capabilityReady ? speech?.reason ?? "" : machine.errorDetail,
    disabled: externallyDisabled || state === "disabled",
    retryable: machine.phase === "retryable_error",
    hasPendingTranscript: machine.phase === "pending_insert",
    devices: microphone.devices,
    selectedDeviceId,
    deviceFallback: microphone.deviceFallback,
    previewState: microphone.previewState,
    previewLevel: microphone.previewLevel,
    previewErrorCode: microphone.previewErrorCode,
    refreshDevices,
    selectDevice: microphone.selectDevice,
    setSilenceMode,
    togglePreview: microphone.togglePreview,
    stopPreview,
    toggle,
    retry,
    insertPending,
    cancel
  };
}

export type VoiceInputModel = ReturnType<typeof useVoiceInput>;

function createRequestID() {
  const suffix = globalThis.crypto?.randomUUID?.() ?? `${Date.now()}-${Math.random().toString(16).slice(2)}`;
  return `voice-${suffix}`;
}
