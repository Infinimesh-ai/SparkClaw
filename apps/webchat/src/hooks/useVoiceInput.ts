import { useCallback, useEffect, useRef, useState } from "react";
import { APIError, api } from "../api/client";
import type { SpeechStatus, SpeechTranscriptionResult } from "../api/types";
import { PCMInputCapture } from "../audio/pcmCapture";
import { encodeSpeechWAV, VoiceAudioError } from "../audio/wav";

export type VoiceInputState =
  | "disabled"
  | "idle"
  | "requesting_permission"
  | "recording"
  | "encoding"
  | "transcribing"
  | "error";

export type VoiceDraftAnchor = {
  sessionId: string;
  draft: string;
  selectionStart: number;
  selectionEnd: number;
};

type VoiceOperation = {
  id: number;
  anchor: VoiceDraftAnchor;
  capture?: PCMInputCapture;
  controller?: AbortController;
  interval?: number;
  timeout?: number;
  startedAt: number;
};

type Options = {
  speech: SpeechStatus | null;
  sessionId: string;
  language: string;
  externallyDisabled: boolean;
  onTranscript: (result: SpeechTranscriptionResult, anchor: VoiceDraftAnchor) => void;
};

export function useVoiceInput({ speech, sessionId, language, externallyDisabled, onTranscript }: Options) {
  const [phase, setPhase] = useState<Exclude<VoiceInputState, "disabled">>("idle");
  const [level, setLevel] = useState(0);
  const [elapsedMs, setElapsedMs] = useState(0);
  const [errorCode, setErrorCode] = useState("");
  const [errorDetail, setErrorDetail] = useState("");
  const operation = useRef<VoiceOperation | null>(null);
  const generation = useRef(0);
  const supported = PCMInputCapture.supported();
  const capabilityReady = Boolean(speech?.enabled && speech.ready);
  const state: VoiceInputState = supported && capabilityReady ? phase : "disabled";
  const active = phase === "requesting_permission" || phase === "recording" || phase === "encoding" || phase === "transcribing";

  const clearTimers = useCallback((current: VoiceOperation | null) => {
    if (current?.interval) window.clearInterval(current.interval);
    if (current?.timeout) window.clearTimeout(current.timeout);
  }, []);

  const cancel = useCallback(async () => {
    generation.current += 1;
    const current = operation.current;
    operation.current = null;
    clearTimers(current);
    current?.controller?.abort();
    await current?.capture?.cancel().catch(() => undefined);
    setLevel(0);
    setElapsedMs(0);
    setErrorCode("");
    setErrorDetail("");
    setPhase("idle");
  }, [clearTimers]);

  const stop = useCallback(async () => {
    const current = operation.current;
    if (!current?.capture) return;
    const currentGeneration = generation.current;
    clearTimers(current);
    setPhase("encoding");
    try {
      const captured = await current.capture.stop();
      current.capture = undefined;
      if (generation.current !== currentGeneration || operation.current !== current) return;
      const wav = encodeSpeechWAV(
        captured,
        speech?.max_audio_seconds ?? 60,
        speech?.max_upload_bytes ?? 3 * 1024 * 1024
      );
      setPhase("transcribing");
      const controller = new AbortController();
      current.controller = controller;
      const result = await api.transcribeSpeech(
        current.anchor.sessionId,
        createRequestID(),
        language || "auto",
        wav,
        controller.signal
      );
      if (generation.current !== currentGeneration || operation.current !== current) return;
      if (!result.text.trim()) {
        operation.current = null;
        setLevel(0);
        setElapsedMs(0);
        setErrorCode("speech_no_speech");
        setErrorDetail("");
        setPhase("error");
        return;
      }
      onTranscript(result, current.anchor);
      operation.current = null;
      setLevel(0);
      setElapsedMs(0);
      setPhase("idle");
    } catch (error) {
      if (generation.current !== currentGeneration || operation.current !== current) return;
      operation.current = null;
      setLevel(0);
      setElapsedMs(0);
      if (error instanceof DOMException && error.name === "AbortError") {
        setPhase("idle");
        return;
      }
      const failure = voiceFailure(error);
      setErrorCode(failure.code);
      setErrorDetail(failure.detail);
      setPhase("error");
    }
  }, [clearTimers, language, onTranscript, speech?.max_audio_seconds, speech?.max_upload_bytes]);

  const start = useCallback(async (anchor: VoiceDraftAnchor) => {
    if (!supported || !capabilityReady || externallyDisabled || operation.current) return;
    const id = ++generation.current;
    const current: VoiceOperation = { id, anchor, startedAt: Date.now() };
    operation.current = current;
    setErrorCode("");
    setErrorDetail("");
    setElapsedMs(0);
    setLevel(0);
    setPhase("requesting_permission");
    try {
      const capture = await PCMInputCapture.start(setLevel);
      if (generation.current !== id || operation.current !== current) {
        await capture.cancel();
        return;
      }
      current.capture = capture;
      current.startedAt = Date.now();
      current.interval = window.setInterval(() => setElapsedMs(Date.now() - current.startedAt), 250);
      current.timeout = window.setTimeout(() => void stop(), (speech?.max_audio_seconds ?? 60) * 1000);
      setPhase("recording");
    } catch (error) {
      if (generation.current !== id || operation.current !== current) return;
      operation.current = null;
      const failure = voiceFailure(error);
      setErrorCode(failure.code);
      setErrorDetail(failure.detail);
      setPhase("error");
    }
  }, [capabilityReady, externallyDisabled, speech?.max_audio_seconds, stop, supported]);

  const toggle = useCallback((anchor: VoiceDraftAnchor) => {
    if (phase === "recording") {
      void stop();
    } else if (phase === "encoding" || phase === "transcribing") {
      void cancel();
    } else if (phase === "idle" || phase === "error") {
      void start(anchor);
    }
  }, [cancel, phase, start, stop]);

  useEffect(() => {
    if (operation.current && operation.current.anchor.sessionId !== sessionId) void cancel();
  }, [cancel, sessionId]);

  useEffect(() => {
    if ((!supported || !capabilityReady) && operation.current) void cancel();
  }, [cancel, capabilityReady, supported]);

  useEffect(() => {
    const onKeyDown = (event: globalThis.KeyboardEvent) => {
      if (event.key === "Escape" && operation.current) void cancel();
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [cancel]);

  useEffect(() => () => {
    generation.current += 1;
    const current = operation.current;
    operation.current = null;
    clearTimers(current);
    current?.controller?.abort();
    void current?.capture?.cancel();
  }, [clearTimers]);

  return {
    state,
    active,
    level,
    elapsedMs,
    errorCode: !supported ? "voice_capture_unsupported" : !capabilityReady ? "speech_model_unavailable" : errorCode,
    errorDetail: !supported ? "" : !capabilityReady ? speech?.reason ?? "" : errorDetail,
    disabled: externallyDisabled || state === "disabled" || state === "requesting_permission",
    toggle,
    cancel
  };
}

function createRequestID() {
  const suffix = globalThis.crypto?.randomUUID?.() ?? `${Date.now()}-${Math.random().toString(16).slice(2)}`;
  return `voice-${suffix}`;
}

function voiceFailure(error: unknown) {
  if (error instanceof VoiceAudioError) return { code: error.code, detail: error.message };
  if (error instanceof APIError) return { code: error.code || "speech_inference_failed", detail: error.message };
  if (error instanceof DOMException) {
    if (error.name === "NotAllowedError" || error.name === "SecurityError") return { code: "voice_permission_denied", detail: error.message };
    if (error.name === "NotFoundError") return { code: "voice_no_device", detail: error.message };
    if (error.name === "NotReadableError" || error.name === "AbortError") return { code: "voice_capture_failed", detail: error.message };
  }
  const detail = error instanceof Error ? error.message : String(error ?? "");
  return { code: detail === "voice_capture_unsupported" ? detail : "speech_inference_failed", detail };
}
