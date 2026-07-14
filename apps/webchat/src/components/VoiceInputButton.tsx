import { LoaderCircle, Mic, MicOff, Square } from "lucide-react";
import type { VoiceInputState } from "../hooks/useVoiceInput";

export function VoiceInputButton({
  state,
  disabled,
  title,
  onClick
}: {
  state: VoiceInputState;
  disabled: boolean;
  title: string;
  onClick: () => void;
}) {
  const icon = state === "disabled"
    ? <MicOff size={18} />
    : state === "recording"
      ? <Square size={16} fill="currentColor" />
      : state === "requesting_permission" || state === "encoding" || state === "transcribing"
        ? <LoaderCircle className="spin" size={18} />
        : <Mic size={18} />;
  return (
    <button
      className={`voiceButton ${state}`}
      type="button"
      disabled={disabled}
      aria-label={title}
      aria-pressed={state === "recording"}
      title={title}
      onClick={onClick}
    >
      {icon}
    </button>
  );
}

export function VoiceInputStatus({
  state,
  level,
  elapsedMs,
  label
}: {
  state: VoiceInputState;
  level: number;
  elapsedMs: number;
  label: string;
}) {
  if (state === "idle" || state === "disabled") return null;
  return (
    <div className={`voiceStatus ${state}`} aria-live="polite">
      {state === "recording" && <span className="voiceRecordDot" aria-hidden="true" />}
      <span className="voiceStatusLabel">{label}</span>
      {state === "recording" && (
        <>
          <span className="voiceLevel" aria-hidden="true">
            {Array.from({ length: 7 }, (_, index) => (
              <i key={index} style={{ height: `${4 + Math.max(0, level - index * 0.08) * 14}px` }} />
            ))}
          </span>
          <time>{formatElapsed(elapsedMs)}</time>
        </>
      )}
    </div>
  );
}

function formatElapsed(elapsedMs: number) {
  const seconds = Math.floor(elapsedMs / 1000);
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")}`;
}
