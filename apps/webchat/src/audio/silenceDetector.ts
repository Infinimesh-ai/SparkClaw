import { CANONICAL_SAMPLE_RATE } from "./pcm16";

export type SilenceMode = "off" | "standard" | "patient";
export type SilenceDecision = "auto_stop" | "no_speech_cancel";
export type SilenceState = "disabled" | "waiting_for_speech" | "speech_active" | "trailing_silence" | "decided";

const STORAGE_KEY = "sparkclaw.voice.silence_mode";
const WINDOW_SAMPLES = 320;
const CALIBRATION_SAMPLES = 3_840;
const START_CONFIRM_SAMPLES = 2_560;
const NO_SPEECH_SAMPLES = CANONICAL_SAMPLE_RATE * 10;
const TRAILING_SAMPLES: Record<Exclude<SilenceMode, "off">, number> = {
  standard: Math.round(CANONICAL_SAMPLE_RATE * 1.2),
  patient: Math.round(CANONICAL_SAMPLE_RATE * 2)
};

export class SilenceDetector {
  private totalSamples = 0;
  private windowSamples = 0;
  private windowSquareSum = 0;
  private activeSamples = 0;
  private silentSamples = 0;
  private readonly noiseWindows: number[] = [];
  private decided = false;
  state: SilenceState;

  constructor(readonly mode: SilenceMode) {
    this.state = mode === "off" ? "disabled" : "waiting_for_speech";
  }

  process(samples: Int16Array): SilenceDecision | null {
    if (this.mode === "off" || this.decided) return null;
    for (const sample of samples) {
      const normalized = sample / (sample < 0 ? 0x8000 : 0x7fff);
      this.windowSquareSum += normalized * normalized;
      this.windowSamples += 1;
      this.totalSamples += 1;
      if (this.windowSamples === WINDOW_SAMPLES) {
        const decision = this.evaluateWindow(Math.sqrt(this.windowSquareSum / WINDOW_SAMPLES));
        this.windowSamples = 0;
        this.windowSquareSum = 0;
        if (decision) return decision;
      }
    }
    return null;
  }

  private evaluateWindow(level: number): SilenceDecision | null {
    if (this.state === "waiting_for_speech") {
      const noiseFloor = this.noiseFloor();
      const startThreshold = Math.max(0.012, Math.min(0.05, noiseFloor * 2.5 + 0.004));
      if (this.totalSamples <= CALIBRATION_SAMPLES) {
        this.recordNoise(level);
      } else if (level >= startThreshold) {
        this.activeSamples += WINDOW_SAMPLES;
      } else {
        this.activeSamples = 0;
        this.recordNoise(level);
      }
      if (this.activeSamples >= START_CONFIRM_SAMPLES) {
        this.state = "speech_active";
        return null;
      }
      if (this.totalSamples >= NO_SPEECH_SAMPLES) return this.decide("no_speech_cancel");
      return null;
    }

    const endThreshold = Math.max(0.008, Math.min(0.035, this.noiseFloor() * 1.6 + 0.002));
    if (level < endThreshold) {
      this.silentSamples += WINDOW_SAMPLES;
      this.state = "trailing_silence";
    } else {
      this.silentSamples = 0;
      this.state = "speech_active";
    }
    if (this.silentSamples >= TRAILING_SAMPLES[this.mode as Exclude<SilenceMode, "off">]) {
      return this.decide("auto_stop");
    }
    return null;
  }

  private recordNoise(level: number) {
    this.noiseWindows.push(Math.min(level, 0.02));
    if (this.noiseWindows.length > 250) this.noiseWindows.shift();
  }

  private noiseFloor() {
    if (this.noiseWindows.length === 0) return 0.003;
    const sorted = [...this.noiseWindows].sort((left, right) => left - right);
    return sorted[Math.floor((sorted.length - 1) * 0.2)];
  }

  private decide(decision: SilenceDecision) {
    this.decided = true;
    this.state = "decided";
    return decision;
  }
}

export function loadSilenceMode(): SilenceMode {
  try {
    const value = window.localStorage.getItem(STORAGE_KEY);
    return value === "standard" || value === "patient" ? value : "off";
  } catch {
    return "off";
  }
}

export function saveSilenceMode(mode: SilenceMode) {
  try {
    window.localStorage.setItem(STORAGE_KEY, mode);
  } catch {
    // Browser privacy settings may disable local storage; Off remains the next-load default.
  }
}
