import { isUnavailableMicrophoneError } from "./microphones";
import { CANONICAL_SAMPLE_RATE, mergePCM16Chunks, StatefulPCM16Resampler } from "./pcm16";

export type CapturedPCM = {
  samples: Int16Array;
  sampleRate: number;
  durationMs: number;
};

type WorkletMessage =
  | { type: "samples"; samples: Float32Array; level: number }
  | { type: "flushed"; id: number };

export type PCMInputCaptureOptions = {
  deviceId?: string;
  retainSamples?: boolean;
  onLevel: (level: number) => void;
  onSamples?: (samples: Int16Array) => void;
  onFailure?: (error: VoiceCaptureError, captured?: CapturedPCM) => void;
};

export class VoiceCaptureError extends Error {
  readonly code: "voice_capture_start_timeout" | "voice_device_disconnected" | "voice_capture_interrupted";

  constructor(code: VoiceCaptureError["code"], message: string) {
    super(message);
    this.code = code;
  }
}

const FIRST_SAMPLE_TIMEOUT_MS = 2500;
const SAMPLE_STALL_TIMEOUT_MS = 4000;

export class PCMInputCapture {
  private readonly stream: MediaStream;
  private readonly chunks: Int16Array[] = [];
  private readonly options: PCMInputCaptureOptions;
  private context: AudioContext | null = null;
  private source: MediaStreamAudioSourceNode | null = null;
  private node: AudioWorkletNode | null = null;
  private mute: GainNode | null = null;
  private resampler: StatefulPCM16Resampler | null = null;
  readonly effectiveDeviceId: string;
  readonly usedDefaultFallback: boolean;
  private flushID = 0;
  private stopped = false;
  private started = false;
  private accepting = false;
  private monitoring = false;
  private failureReported = false;
  private firstSampleResolve: (() => void) | null = null;
  private lastSampleAt = Date.now();
  private watchdog: number | undefined;
  private stopPromise: Promise<CapturedPCM> | null = null;

  private constructor(stream: MediaStream, options: PCMInputCaptureOptions, usedDefaultFallback: boolean) {
    this.stream = stream;
    this.options = options;
    this.effectiveDeviceId = stream.getAudioTracks()[0]?.getSettings().deviceId ?? "";
    this.usedDefaultFallback = usedDefaultFallback;
  }

  static supported() {
    return Boolean(
      navigator.mediaDevices &&
      window.AudioContext &&
      "audioWorklet" in AudioContext.prototype
    );
  }

  static async prepare(options: PCMInputCaptureOptions) {
    if (!PCMInputCapture.supported()) throw new Error("voice_capture_unsupported");
    const acquired = await acquireMicrophone(options.deviceId);
    return new PCMInputCapture(acquired.stream, options, acquired.usedDefaultFallback);
  }

  static async start(options: PCMInputCaptureOptions) {
    const capture = await PCMInputCapture.prepare(options);
    try {
      await capture.start();
      return capture;
    } catch (error) {
      await capture.cancel().catch(() => undefined);
      throw error;
    }
  }

  async start() {
    if (this.stopped || this.started) throw new Error("voice_capture_stopped");
    this.started = true;
    try {
      const context = new AudioContext();
      this.context = context;
      await context.audioWorklet.addModule(`${import.meta.env.BASE_URL}pcm-worklet.js`);
      const source = context.createMediaStreamSource(this.stream);
      const node = new AudioWorkletNode(context, "sparkclaw-pcm-capture", {
        numberOfInputs: 1,
        numberOfOutputs: 1,
        outputChannelCount: [1]
      });
      const mute = context.createGain();
      mute.gain.value = 0;
      this.source = source;
      this.node = node;
      this.mute = mute;
      this.resampler = new StatefulPCM16Resampler(context.sampleRate);
      const firstSample = new Promise<void>((resolve) => { this.firstSampleResolve = resolve; });
      node.port.onmessage = (event: MessageEvent<WorkletMessage>) => this.handleWorkletMessage(event);
      source.connect(node);
      node.connect(mute);
      mute.connect(context.destination);
      this.accepting = true;
      await context.resume();
      await this.waitUntilLive(firstSample);
      if (this.stopped) throw new VoiceCaptureError("voice_capture_interrupted", "microphone capture stopped while starting");
      this.startMonitoring();
    } catch (error) {
      await this.cancel().catch(() => undefined);
      throw error;
    }
  }

  stop(): Promise<CapturedPCM> {
    if (this.stopPromise) return this.stopPromise;
    if (this.stopped) return Promise.reject(new Error("voice_capture_stopped"));
    this.stopPromise = this.finishCapture();
    return this.stopPromise;
  }

  async cancel() {
    if (this.stopPromise) {
      await this.stopPromise.catch(() => undefined);
      return;
    }
    if (this.stopped) return;
    this.monitoring = false;
    this.accepting = false;
    this.stream.getTracks().forEach((track) => track.stop());
    this.chunks.length = 0;
    await this.teardown();
  }

  private async finishCapture(): Promise<CapturedPCM> {
    this.monitoring = false;
    this.firstSampleResolve?.();
    this.firstSampleResolve = null;
    this.stream.getTracks().forEach((track) => track.stop());
    await this.flushWorklet();
    const tail = this.resampler?.flush() ?? new Int16Array();
    if (tail.length > 0) this.acceptCanonicalSamples(tail);
    this.accepting = false;
    const samples = mergePCM16Chunks(this.chunks);
    await this.teardown();
    return {
      samples,
      sampleRate: CANONICAL_SAMPLE_RATE,
      durationMs: Math.round((samples.length / CANONICAL_SAMPLE_RATE) * 1000)
    };
  }

  private handleWorkletMessage(event: MessageEvent<WorkletMessage>) {
    if (event.data.type !== "samples" || !this.accepting || this.stopped) return;
    const canonical = this.resampler?.process(event.data.samples) ?? new Int16Array();
    if (canonical.length > 0) this.acceptCanonicalSamples(canonical);
    this.lastSampleAt = Date.now();
    if (this.firstSampleResolve) {
      this.firstSampleResolve();
      this.firstSampleResolve = null;
    }
    this.options.onLevel(Math.min(1, Math.max(0, event.data.level * 5)));
  }

  private acceptCanonicalSamples(samples: Int16Array) {
    if (this.options.retainSamples !== false) this.chunks.push(samples);
    this.options.onSamples?.(samples);
  }

  private async flushWorklet() {
    if (!this.node || !this.accepting) return;
    const id = ++this.flushID;
    await new Promise<void>((resolve) => {
      let timeout = 0;
      const listener = (event: MessageEvent<WorkletMessage>) => {
        if (event.data.type !== "flushed" || event.data.id !== id) return;
        window.clearTimeout(timeout);
        this.node?.port.removeEventListener("message", listener);
        resolve();
      };
      timeout = window.setTimeout(() => {
        this.node?.port.removeEventListener("message", listener);
        resolve();
      }, 250);
      this.node?.port.addEventListener("message", listener);
      this.node?.port.postMessage({ type: "flush", id });
    });
  }

  private async teardown() {
    if (this.stopped) return;
    this.stopped = true;
    this.monitoring = false;
    if (this.watchdog) window.clearInterval(this.watchdog);
    for (const track of this.stream.getTracks()) track.removeEventListener("ended", this.onTrackEnded);
    this.context?.removeEventListener("statechange", this.onContextStateChange);
    if (this.node) this.node.port.onmessage = null;
    this.source?.disconnect();
    this.node?.disconnect();
    this.mute?.disconnect();
    this.options.onLevel(0);
    if (this.context && this.context.state !== "closed") await this.context.close().catch(() => undefined);
  }

  private async waitUntilLive(firstSample: Promise<void>) {
    let timeout = 0;
    try {
      await Promise.race([
        firstSample,
        new Promise<void>((_resolve, reject) => {
          timeout = window.setTimeout(() => reject(new VoiceCaptureError(
            "voice_capture_start_timeout",
            "microphone did not produce audio samples"
          )), FIRST_SAMPLE_TIMEOUT_MS);
        })
      ]);
    } finally {
      window.clearTimeout(timeout);
    }
  }

  private startMonitoring() {
    this.monitoring = true;
    for (const track of this.stream.getTracks()) track.addEventListener("ended", this.onTrackEnded);
    this.context?.addEventListener("statechange", this.onContextStateChange);
    this.watchdog = window.setInterval(() => {
      if (this.stopped || !this.monitoring) return;
      const liveTrack = this.stream.getAudioTracks().some((track) => track.readyState === "live");
      if (liveTrack && this.context?.state === "running" && Date.now() - this.lastSampleAt > SAMPLE_STALL_TIMEOUT_MS) {
        this.reportFailure(new VoiceCaptureError("voice_capture_interrupted", "microphone audio stopped unexpectedly"));
      }
    }, 1000);
  }

  private readonly onTrackEnded = () => {
    this.reportFailure(new VoiceCaptureError("voice_device_disconnected", "microphone disconnected"));
  };

  private readonly onContextStateChange = () => {
    if (this.context?.state === "closed" || String(this.context?.state) === "interrupted") {
      this.reportFailure(new VoiceCaptureError("voice_capture_interrupted", "microphone audio context stopped"));
    }
  };

  private reportFailure(error: VoiceCaptureError) {
    if (this.stopped || !this.monitoring || this.failureReported) return;
    this.failureReported = true;
    this.monitoring = false;
    void this.stop()
      .then((captured) => this.options.onFailure?.(error, captured))
      .catch(() => this.options.onFailure?.(error));
  }
}

async function acquireMicrophone(deviceId = "") {
  try {
    return { stream: await navigator.mediaDevices.getUserMedia(microphoneConstraints(deviceId)), usedDefaultFallback: false };
  } catch (error) {
    if (!deviceId || !isUnavailableMicrophoneError(error)) throw error;
    return { stream: await navigator.mediaDevices.getUserMedia(microphoneConstraints("")), usedDefaultFallback: true };
  }
}

function microphoneConstraints(deviceId: string): MediaStreamConstraints {
  return {
    audio: {
      ...(deviceId ? { deviceId: { exact: deviceId } } : {}),
      channelCount: { ideal: 1 },
      echoCancellation: { ideal: true },
      noiseSuppression: { ideal: true },
      autoGainControl: { ideal: true }
    }
  };
}
