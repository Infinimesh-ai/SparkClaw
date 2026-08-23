// Microphone device slice of the voice-input feature: enumeration, the
// persisted device preference, the default-device fallback flag, and the
// standalone level-meter preview. Extracted from useVoiceInput so the
// state-machine driver only orchestrates capture/transcription; the only
// coupling back into it is the previewBlocked() probe that keeps a preview
// from starting while a recording operation owns the microphone.
import { useCallback, useEffect, useRef, useState } from "react";
import {
  enumerateMicrophones,
  loadPreferredMicrophone,
  savePreferredMicrophone
} from "../audio/microphones";
import type { MicrophoneDevice } from "../audio/microphones";
import { PCMInputCapture } from "../audio/pcmCapture";
import { voiceFailure } from "../lib/voiceErrors";

type Options = {
  supported: boolean;
  previewBlocked: () => boolean;
};

export function useMicrophoneDevices({ supported, previewBlocked }: Options) {
  const [devices, setDevices] = useState<MicrophoneDevice[]>([]);
  const [selectedDeviceId, setSelectedDeviceId] = useState(() => loadPreferredMicrophone());
  const [deviceFallback, setDeviceFallback] = useState(false);
  const [previewState, setPreviewState] = useState<"idle" | "starting" | "active">("idle");
  const [previewLevel, setPreviewLevel] = useState(0);
  const [previewErrorCode, setPreviewErrorCode] = useState("");
  const previewCapture = useRef<PCMInputCapture | null>(null);
  const previewGeneration = useRef(0);
  const mounted = useRef(true);

  const refreshDevices = useCallback(async () => {
    try {
      const next = await enumerateMicrophones();
      if (mounted.current) setDevices(next);
    } catch {
      if (mounted.current) setDevices([]);
    }
  }, []);

  const stopPreview = useCallback(async () => {
    previewGeneration.current += 1;
    const capture = previewCapture.current;
    previewCapture.current = null;
    await capture?.cancel().catch(() => undefined);
    if (mounted.current) {
      setPreviewState("idle");
      setPreviewLevel(0);
    }
  }, []);

  const selectDevice = useCallback((deviceId: string) => {
    void stopPreview();
    setSelectedDeviceId(deviceId);
    savePreferredMicrophone(deviceId);
    setDeviceFallback(false);
    setPreviewErrorCode("");
  }, [stopPreview]);

  // The active recording fell back to the default input because the
  // preferred device disappeared: forget the stale preference.
  const noteDefaultFallback = useCallback(() => {
    setSelectedDeviceId("");
    savePreferredMicrophone("");
    setDeviceFallback(true);
  }, []);

  const clearDeviceFallback = useCallback(() => setDeviceFallback(false), []);

  const startPreview = useCallback(async () => {
    if (!supported || previewBlocked() || previewState !== "idle") return;
    const id = ++previewGeneration.current;
    setPreviewState("starting");
    setPreviewErrorCode("");
    try {
      const capture = await PCMInputCapture.start({
        deviceId: selectedDeviceId,
        retainSamples: false,
        onLevel: setPreviewLevel,
        onFailure: (error) => {
          if (previewGeneration.current !== id) return;
          previewCapture.current = null;
          setPreviewState("idle");
          setPreviewLevel(0);
          setPreviewErrorCode(error.code);
        }
      });
      if (previewGeneration.current !== id || previewBlocked()) {
        await capture.cancel();
        return;
      }
      previewCapture.current = capture;
      if (capture.usedDefaultFallback) noteDefaultFallback();
      setPreviewState("active");
      void refreshDevices();
    } catch (error) {
      if (previewGeneration.current !== id) return;
      const failure = voiceFailure(error);
      setPreviewState("idle");
      setPreviewLevel(0);
      setPreviewErrorCode(failure.code);
    }
  }, [noteDefaultFallback, previewBlocked, previewState, refreshDevices, selectedDeviceId, supported]);

  const togglePreview = useCallback(() => {
    if (previewState === "idle") void startPreview();
    else void stopPreview();
  }, [previewState, startPreview, stopPreview]);

  useEffect(() => {
    void refreshDevices();
    const mediaDevices = navigator.mediaDevices;
    const onDeviceChange = () => void refreshDevices();
    mediaDevices?.addEventListener?.("devicechange", onDeviceChange);
    return () => mediaDevices?.removeEventListener?.("devicechange", onDeviceChange);
  }, [refreshDevices]);

  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      previewGeneration.current += 1;
      void previewCapture.current?.cancel();
      previewCapture.current = null;
    };
  }, []);

  return {
    devices,
    selectedDeviceId,
    deviceFallback,
    previewState,
    previewLevel,
    previewErrorCode,
    refreshDevices,
    selectDevice,
    stopPreview,
    togglePreview,
    noteDefaultFallback,
    clearDeviceFallback
  };
}
