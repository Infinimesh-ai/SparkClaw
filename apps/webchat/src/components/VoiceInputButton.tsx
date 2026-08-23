import { useEffect, useRef, useState } from "react";
import { Check, ChevronDown, CornerDownLeft, LoaderCircle, Mic, MicOff, RotateCcw, Square, Volume2, X } from "lucide-react";
import type { Copy as CopyText } from "../i18n";
import type { VoiceInputModel, VoiceInputState } from "../hooks/useVoiceInput";
import { voiceCaptureFailureLabel, voiceInputLabel, voiceInputTitle } from "../lib/voiceLabels";
import { voicePhaseIsRecording } from "../lib/voiceState";

export function VoiceInputControl({
  voice,
  text,
  onToggle
}: {
  voice: VoiceInputModel;
  text: CopyText;
  onToggle: () => void;
}) {
  const [open, setOpen] = useState(false);
  const root = useRef<HTMLDivElement | null>(null);
  const { state, disabled, active, devices, selectedDeviceId, silenceMode, previewState, previewLevel, stopPreview } = voice;
  const label = voiceInputLabel(state, voice.errorCode, voice.errorDetail, voice.deviceFallback, text);
  const title = voiceInputTitle(state, label, text);
  const previewError = voiceCaptureFailureLabel(voice.previewErrorCode, "", text);
  const recording = state !== "disabled" && voicePhaseIsRecording(state);
  const icon = state === "disabled"
    ? <MicOff size={18} />
    : recording
      ? <Square size={16} fill="currentColor" />
      : state !== "idle" && state !== "error" && state !== "retryable_error" && state !== "pending_insert"
        ? <LoaderCircle className="spin" size={18} />
        : <Mic size={18} />;

  useEffect(() => {
    if (!open) return;
    const close = (event: PointerEvent) => {
      if (!root.current?.contains(event.target as Node)) setOpen(false);
    };
    document.addEventListener("pointerdown", close);
    return () => document.removeEventListener("pointerdown", close);
  }, [open]);

  useEffect(() => {
    if (active && open) setOpen(false);
  }, [active, open]);

  useEffect(() => {
    if (!open) return;
    return () => void stopPreview();
  }, [open, stopPreview]);

  return (
    <div className="voiceControl" ref={root}>
      <button
        className={`voiceButton ${state}`}
        type="button"
        disabled={disabled}
        aria-label={title}
        aria-pressed={recording}
        title={title}
        onClick={onToggle}
      >
        {icon}
      </button>
      <button
        className="voiceDeviceButton"
        type="button"
        disabled={disabled || active}
        aria-label={text.chat.voiceChooseMicrophone}
        aria-expanded={open}
        title={text.chat.voiceChooseMicrophone}
        onClick={() => {
          const next = !open;
          setOpen(next);
          if (next) void voice.refreshDevices();
        }}
      >
        <ChevronDown size={13} />
      </button>
      {open && (
        <div className="voiceDeviceMenu" role="menu" aria-label={text.chat.voiceChooseMicrophone}>
          <strong>{text.chat.voiceMicrophone}</strong>
          <button
            type="button"
            className={!selectedDeviceId ? "selected" : ""}
            onClick={() => voice.selectDevice("")}
          >
            <span>{text.chat.voiceDefaultMicrophone}</span>
            {!selectedDeviceId && <Check size={14} />}
          </button>
          {devices.map((device) => (
            <button
              type="button"
              className={selectedDeviceId === device.deviceId ? "selected" : ""}
              key={device.deviceId}
              onClick={() => voice.selectDevice(device.deviceId)}
            >
              <span>{device.label}</span>
              {selectedDeviceId === device.deviceId && <Check size={14} />}
            </button>
          ))}
          <strong>{text.chat.voiceSilenceStop}</strong>
          {(["off", "standard", "patient"] as const).map((mode) => (
            <button
              type="button"
              className={silenceMode === mode ? "selected" : ""}
              key={mode}
              onClick={() => voice.setSilenceMode(mode)}
            >
              <span>{mode === "off" ? text.chat.voiceSilenceOff : mode === "standard" ? text.chat.voiceSilenceStandard : text.chat.voiceSilencePatient}</span>
              {silenceMode === mode && <Check size={14} />}
            </button>
          ))}
          <div className="voicePreviewRow">
            <button type="button" className="voicePreviewButton" onClick={voice.togglePreview}>
              {previewState === "starting" ? <LoaderCircle className="spin" size={14} /> : previewState === "active" ? <Square size={13} /> : <Volume2 size={15} />}
              <span>{previewState === "active" ? text.chat.voiceStopPreview : text.chat.voicePreview}</span>
            </button>
            <span className="voicePreviewLevel" aria-hidden="true">
              <i style={{ width: `${Math.round(previewLevel * 100)}%` }} />
            </span>
          </div>
          {previewError && <small className="voicePreviewError">{previewError}</small>}
        </div>
      )}
    </div>
  );
}

export function VoiceInputStatus({
  state,
  level,
  elapsedMs,
  partialText,
  partialFrozen,
  label,
  retryable,
  pendingInsert,
  text,
  onRetry,
  onInsertPending,
  onDismiss
}: {
  state: VoiceInputState;
  level: number;
  elapsedMs: number;
  partialText: string;
  partialFrozen: boolean;
  label: string;
  retryable: boolean;
  pendingInsert: boolean;
  text: CopyText;
  onRetry: () => void;
  onInsertPending: () => void;
  onDismiss: () => void;
}) {
  if ((state === "idle" || state === "disabled") && !partialText) return null;
  const recording = state !== "disabled" && voicePhaseIsRecording(state);
  return (
    <>
      {partialText && (
        <div className={`voicePartial${partialFrozen ? " frozen" : ""}`} aria-live="polite">
          <small>{partialFrozen ? text.chat.voicePartialFrozen : text.chat.voicePartial}</small>
          <span>{partialText}</span>
        </div>
      )}
      {state !== "idle" && state !== "disabled" && (
        <div className={`voiceStatus ${state}`} aria-live="polite">
          {recording && <span className="voiceRecordDot" aria-hidden="true" />}
          <span className="voiceStatusLabel">{label}</span>
          {recording && (
            <>
              <span className="voiceLevel" aria-hidden="true">
                {Array.from({ length: 7 }, (_, index) => (
                  <i key={index} style={{ height: `${4 + Math.max(0, level - index * 0.08) * 14}px` }} />
                ))}
              </span>
              <time>{formatElapsed(elapsedMs)}</time>
            </>
          )}
          {retryable && (
            <button type="button" className="voiceStatusAction" onClick={onRetry} title={text.chat.voiceRetry}>
              <RotateCcw size={14} />
              <span>{text.chat.voiceRetry}</span>
            </button>
          )}
          {pendingInsert && (
            <button type="button" className="voiceStatusAction" onClick={onInsertPending} title={text.chat.voiceInsertAtCursor}>
              <CornerDownLeft size={14} />
              <span>{text.chat.voiceInsertAtCursor}</span>
            </button>
          )}
          {(retryable || pendingInsert) && (
            <button type="button" className="voiceStatusDismiss" onClick={onDismiss} title={text.chat.voiceDismiss}>
              <X size={14} />
            </button>
          )}
        </div>
      )}
    </>
  );
}

function formatElapsed(elapsedMs: number) {
  const seconds = Math.floor(elapsedMs / 1000);
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")}`;
}
