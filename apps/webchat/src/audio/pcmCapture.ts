export type CapturedPCM = {
  samples: Float32Array;
  sampleRate: number;
  durationMs: number;
};

type WorkletMessage =
  | { type: "samples"; samples: Float32Array; level: number }
  | { type: "flushed"; id: number };

export class PCMInputCapture {
  private readonly stream: MediaStream;
  private readonly context: AudioContext;
  private readonly source: MediaStreamAudioSourceNode;
  private readonly node: AudioWorkletNode;
  private readonly mute: GainNode;
  private readonly chunks: Float32Array[] = [];
  private readonly onLevel: (level: number) => void;
  private flushID = 0;
  private stopped = false;

  private constructor(
    stream: MediaStream,
    context: AudioContext,
    source: MediaStreamAudioSourceNode,
    node: AudioWorkletNode,
    mute: GainNode,
    onLevel: (level: number) => void
  ) {
    this.stream = stream;
    this.context = context;
    this.source = source;
    this.node = node;
    this.mute = mute;
    this.onLevel = onLevel;
    this.node.port.onmessage = (event: MessageEvent<WorkletMessage>) => {
      if (event.data.type !== "samples" || this.stopped) return;
      this.chunks.push(event.data.samples);
      this.onLevel(Math.min(1, Math.max(0, event.data.level * 5)));
    };
  }

  static supported() {
    return Boolean(
      navigator.mediaDevices &&
      window.AudioContext &&
      "audioWorklet" in AudioContext.prototype
    );
  }

  static async start(onLevel: (level: number) => void) {
    if (!PCMInputCapture.supported()) {
      throw new Error("voice_capture_unsupported");
    }
    let stream: MediaStream | null = null;
    let context: AudioContext | null = null;
    try {
      stream = await navigator.mediaDevices.getUserMedia({
        audio: {
          channelCount: { ideal: 1 },
          echoCancellation: { ideal: true },
          noiseSuppression: { ideal: true },
          autoGainControl: { ideal: true }
        }
      });
      context = new AudioContext();
      await context.audioWorklet.addModule(`${import.meta.env.BASE_URL}pcm-worklet.js`);
      const source = context.createMediaStreamSource(stream);
      const node = new AudioWorkletNode(context, "sparkclaw-pcm-capture", {
        numberOfInputs: 1,
        numberOfOutputs: 1,
        outputChannelCount: [1]
      });
      const mute = context.createGain();
      mute.gain.value = 0;
      source.connect(node);
      node.connect(mute);
      mute.connect(context.destination);
      await context.resume();
      return new PCMInputCapture(stream, context, source, node, mute, onLevel);
    } catch (error) {
      stream?.getTracks().forEach((track) => track.stop());
      if (context && context.state !== "closed") await context.close().catch(() => undefined);
      throw error;
    }
  }

  async stop(): Promise<CapturedPCM> {
    if (this.stopped) throw new Error("voice_capture_stopped");
    this.stream.getTracks().forEach((track) => track.stop());
    await this.flush();
    const sampleRate = this.context.sampleRate;
    const samples = mergeChunks(this.chunks);
    await this.teardown();
    return {
      samples,
      sampleRate,
      durationMs: Math.round((samples.length / sampleRate) * 1000)
    };
  }

  async cancel() {
    if (this.stopped) return;
    this.stream.getTracks().forEach((track) => track.stop());
    this.chunks.length = 0;
    await this.teardown();
  }

  private async flush() {
    const id = ++this.flushID;
    await new Promise<void>((resolve) => {
      let timeout = 0;
      const listener = (event: MessageEvent<WorkletMessage>) => {
        if (event.data.type !== "flushed" || event.data.id !== id) return;
        window.clearTimeout(timeout);
        this.node.port.removeEventListener("message", listener);
        resolve();
      };
      timeout = window.setTimeout(() => {
        this.node.port.removeEventListener("message", listener);
        resolve();
      }, 250);
      this.node.port.addEventListener("message", listener);
      this.node.port.postMessage({ type: "flush", id });
    });
  }

  private async teardown() {
    if (this.stopped) return;
    this.stopped = true;
    this.node.port.onmessage = null;
    this.source.disconnect();
    this.node.disconnect();
    this.mute.disconnect();
    this.onLevel(0);
    if (this.context.state !== "closed") await this.context.close().catch(() => undefined);
  }
}

function mergeChunks(chunks: Float32Array[]) {
  const total = chunks.reduce((sum, chunk) => sum + chunk.length, 0);
  const merged = new Float32Array(total);
  let offset = 0;
  for (const chunk of chunks) {
    merged.set(chunk, offset);
    offset += chunk.length;
  }
  return merged;
}
