import { desktopCapability } from "../desktop/capability";
import type { SpeechRealtimeEvent } from "../api/types";
import type { SpeechRealtimeFinal, SpeechRealtimeFailure } from "./realtimeSpeech";

type Handlers = {
  onPartial: (event: { revision: number; text: string; language: string; audioEndMs: number }) => void;
  onFailure: (failure: SpeechRealtimeFailure) => void;
};
type Reply = {
  session_id?: string;
  ready?: SpeechRealtimeEvent;
  accepted_sequence?: number;
  events?: Array<{ sequence: number; event: SpeechRealtimeEvent }>;
  closed?: boolean;
};

export class ISCPSpeechRealtimeClient {
  private session = "";
  private closed = false;
  private finishing = false;
  private tail = new Int16Array();
  private queued = 0;
  private sequence = 1;
  private samples = 0;
  private cursor = 0;
  private revision = 0;
  private pipeline = Promise.resolve();
  private timer = 0;
  private finalResolve?: (value: SpeechRealtimeFinal) => void;
  private finalReject?: (error: SpeechRealtimeFailure) => void;

  constructor(private handlers: Handlers) {}

  static async connect(request: { session_id: string; request_id: string; language: string }, handlers: Handlers) {
    const client = new ISCPSpeechRealtimeClient(handlers);
    const result = await client.rpc({ action: "open", ...request });
    if (typeof result.session_id !== "string" || !result.session_id) throw new Error("Invalid speech negotiation");
    client.session = result.session_id;
    const ready = result.ready;
    if (ready?.event !== "ready" || ready.protocol !== "sparkclaw.speech.realtime.v1" ||
      ready.format?.sample_rate !== 16000 || ready.format.channels !== 1 || ready.format.bits_per_sample !== 16 ||
      ready.format.frame_ms !== 100 || ready.limits?.max_frame_samples !== 1600 ||
      !Number.isFinite(ready.limits.max_audio_seconds) || ready.limits.max_audio_seconds <= 0 || ready.limits.max_audio_seconds > 300) {
      await client.cancel();
      throw new Error("Invalid speech negotiation");
    }
    client.timer = window.setTimeout(() => void client.poll(), 0);
    return client;
  }

  push(samples: Int16Array) {
    if (this.closed || this.finishing || !samples.length) return;
    if (this.queued + this.tail.length + samples.length > 80000) { this.fail("speech_stream_overrun"); return; }
    const next = new Int16Array(this.tail.length + samples.length);
    next.set(this.tail); next.set(samples, this.tail.length);
    let offset = 0;
    while (next.length - offset >= 1600) { this.frame(next.slice(offset, offset + 1600)); offset += 1600; }
    this.tail = next.slice(offset);
  }

  private frame(samples: Int16Array) {
    const sequence = this.sequence++;
    this.samples += samples.length;
    this.queued += samples.length;
    this.pipeline = this.pipeline.then(async () => {
      if (this.closed) throw new Error("Speech capture ended");
      const bytes = new Uint8Array(samples.length * 2);
      const view = new DataView(bytes.buffer);
      samples.forEach((sample, index) => view.setInt16(index * 2, sample, true));
      const reply = await this.rpc({ action: "frame", session_id: this.session, sequence, bytes });
      if (reply.accepted_sequence !== sequence) throw new Error("Speech acknowledgement differs");
      this.queued -= samples.length;
    }).catch(() => this.fail("speech_stream_gap"));
  }

  async finish(reason: "manual_stop" | "silence_stop" | "max_duration") {
    if (this.closed || this.finishing) throw new Error("Speech session ended");
    this.finishing = true;
    if (this.tail.length) { this.frame(this.tail); this.tail = new Int16Array(); }
    const final = new Promise<SpeechRealtimeFinal>((resolve, reject) => { this.finalResolve = resolve; this.finalReject = reject; });
    void final.catch(() => {});
    const deadline = window.setTimeout(() => this.fail("speech_timeout"), 12000);
    try {
      await this.pipeline;
      if (this.closed) throw new Error("Speech input incomplete");
      await this.rpc({ action: "finish", session_id: this.session, last_sequence: this.sequence - 1, total_samples: this.samples, reason });
      return await final;
    } finally { window.clearTimeout(deadline); }
  }

  async cancel() {
    if (this.closed) return;
    this.closed = true;
    window.clearTimeout(this.timer);
    this.tail = new Int16Array(); this.queued = 0;
    this.finalReject?.({ code: "speech_cancelled", retryable: false });
    this.finalResolve = this.finalReject = undefined;
    await this.rpc({ action: "cancel", session_id: this.session }).catch(() => {});
  }

  async closeForFallback() { await this.cancel(); }

  private async poll() {
    if (this.closed) return;
    try {
      const result = await this.rpc({ action: "events", session_id: this.session, after: this.cursor });
      if (this.closed) return;
      if (result.session_id !== this.session || !Array.isArray(result.events) || result.events.length > 64) throw new Error("Invalid speech events");
      for (const item of result.events) {
        if (!Number.isSafeInteger(item.sequence) || item.sequence < 1) throw new Error("Invalid speech sequence");
        if (item.sequence <= this.cursor) continue;
        if (item.sequence !== this.cursor + 1) throw new Error("Speech event gap");
        this.cursor = item.sequence;
        const event = item.event;
        if (event.event === "partial" || event.event === "final") {
          const revision = event.revision ?? 0;
          if (!Number.isSafeInteger(revision) || revision <= this.revision || typeof event.text !== "string" || event.text.length > 65536) throw new Error("Invalid speech revision");
          this.revision = revision;
          if (event.event === "partial") {
            this.handlers.onPartial({ revision, text: event.text, language: event.language || "", audioEndMs: event.audio_end_ms || 0 });
          } else {
            if (!this.finishing || !this.finalResolve) throw new Error("Unexpected speech final");
            this.finalResolve({ revision, text: event.text, language: event.language || "", durationMs: event.duration_ms || 0, inferenceMs: event.inference_ms || 0, model: event.model || "", stopReason: event.stop_reason || "" });
            this.finalResolve = this.finalReject = undefined;
            await this.cancel();
            return;
          }
        } else if (event.event === "ack") {
          if (!Number.isSafeInteger(event.accepted_sequence) || (event.accepted_sequence ?? 0) < 1 || (event.accepted_sequence ?? 0) >= this.sequence) throw new Error("Invalid provider acknowledgement");
        } else {
          // An upstream fallback event also ends this session. The renderer must
          // never turn an ISCP stream failure into a second HTTP transcription.
          throw new Error("Speech stream ended");
        }
      }
      if (result.closed) throw new Error("Speech stream ended without final");
    } catch { this.fail("speech_stream_gap"); }
    finally { if (!this.closed) this.timer = window.setTimeout(() => void this.poll(), 250); }
  }

  private fail(code: string) {
    if (this.closed) return;
    const failure = { code, retryable: false };
    this.finalReject?.(failure);
    this.handlers.onFailure(failure);
    void this.cancel();
  }

  private async rpc(request: Record<string, unknown>): Promise<Reply> {
    const desktop = desktopCapability();
    if (!desktop?.speechStream) throw new Error("ISCP speech is unavailable");
    return await desktop.speechStream(request) as Reply;
  }
}
