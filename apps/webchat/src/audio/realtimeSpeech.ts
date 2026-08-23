import type {
  SpeechRealtimeEvent,
  SpeechRealtimeTicket
} from "../api/types";

export const SPEECH_REALTIME_PROTOCOL = "sparkclaw.speech.realtime.v1";
export const SPEECH_REALTIME_SAMPLE_RATE = 16_000;
export const SPEECH_REALTIME_FRAME_SAMPLES = 1_600;
const MAX_BUFFERED_AUDIO_BYTES = SPEECH_REALTIME_SAMPLE_RATE * 2 * 5;
const READY_TIMEOUT_MS = 5_000;
const FINAL_TIMEOUT_MS = 12_000;
const RELEASE_TIMEOUT_MS = 2_000;

export type SpeechRealtimeFailure = {
  code: string;
  retryable: boolean;
  detail?: string;
};

export type SpeechRealtimeFinal = {
  revision: number;
  text: string;
  language: string;
  durationMs: number;
  inferenceMs: number;
  model: string;
  stopReason: string;
};

type Handlers = {
  onPartial: (event: { revision: number; text: string; language: string; audioEndMs: number }) => void;
  onFailure: (failure: SpeechRealtimeFailure) => void;
};

export class SpeechRealtimeClient {
  private readonly socket: WebSocket;
  private readonly ticket: SpeechRealtimeTicket;
  private readonly handlers: Handlers;
  private tail = new Int16Array();
  private nextSequence = 0;
  private sentSamples = 0;
  private revision = 0;
  private ready = false;
  private terminal = false;
  private finishResolve: ((value: SpeechRealtimeFinal) => void) | null = null;
  private finishReject: ((reason: SpeechRealtimeFailure) => void) | null = null;
  private readyResolve: (() => void) | null = null;
  private readyReject: ((reason: SpeechRealtimeFailure) => void) | null = null;
  private closeResolve: (() => void) | null = null;
  private readyTimer = 0;
  private finalTimer = 0;

  private constructor(ticket: SpeechRealtimeTicket, handlers: Handlers) {
    this.ticket = ticket;
    this.handlers = handlers;
    this.socket = new WebSocket(webSocketURL(ticket.url));
    this.socket.binaryType = "arraybuffer";
    this.socket.onmessage = (event) => this.handleMessage(event);
    this.socket.onerror = () => this.fail({ code: "speech_model_unavailable", retryable: true });
    this.socket.onclose = () => {
      this.closeResolve?.();
      this.closeResolve = null;
      if (!this.terminal) this.fail({ code: "speech_model_unavailable", retryable: true });
    };
  }

  static async connect(ticket: SpeechRealtimeTicket, handlers: Handlers) {
    validateTicket(ticket);
    const client = new SpeechRealtimeClient(ticket, handlers);
    await client.waitUntilReady();
    return client;
  }

  push(samples: Int16Array) {
    if (!this.ready || this.terminal || samples.length === 0) return;
    const combined = new Int16Array(this.tail.length + samples.length);
    combined.set(this.tail);
    combined.set(samples, this.tail.length);
    let offset = 0;
    while (combined.length - offset >= SPEECH_REALTIME_FRAME_SAMPLES) {
      this.sendFrame(combined.subarray(offset, offset + SPEECH_REALTIME_FRAME_SAMPLES));
      if (this.terminal) return;
      offset += SPEECH_REALTIME_FRAME_SAMPLES;
    }
    this.tail = combined.slice(offset);
  }

  finish(reason: "manual_stop" | "silence_stop" | "max_duration") {
    if (!this.ready || this.terminal) {
      return Promise.reject<SpeechRealtimeFinal>({ code: "speech_model_unavailable", retryable: true });
    }
    if (this.tail.length > 0) {
      this.sendFrame(this.tail);
      this.tail = new Int16Array();
    }
    if (this.terminal || this.nextSequence === 0) {
      return Promise.reject<SpeechRealtimeFinal>({ code: "speech_too_short", retryable: false });
    }
    const result = new Promise<SpeechRealtimeFinal>((resolve, reject) => {
      this.finishResolve = resolve;
      this.finishReject = reject;
    });
    this.socket.send(JSON.stringify({
      event: "finish",
      last_sequence: this.nextSequence - 1,
      captured_ms: Math.round((this.sentSamples / SPEECH_REALTIME_SAMPLE_RATE) * 1000),
      reason
    }));
    this.finalTimer = window.setTimeout(() => {
      if (!this.finishReject || this.terminal) return;
      this.fail({ code: "speech_timeout", retryable: true });
    }, FINAL_TIMEOUT_MS);
    return result;
  }

  async closeForFallback() {
    if (this.socket.readyState === WebSocket.CLOSED) return;
    this.terminal = true;
    const closed = new Promise<void>((resolve) => { this.closeResolve = resolve; });
    if (this.socket.readyState === WebSocket.OPEN) {
      this.socket.send(JSON.stringify({ event: "cancel", last_sequence: Math.max(0, this.nextSequence - 1) }));
    }
    this.socket.close(1000, "fallback");
    this.clearTimers();
    let releaseTimer = 0;
    try {
      await Promise.race([
        closed,
        new Promise<void>((resolve) => { releaseTimer = window.setTimeout(resolve, RELEASE_TIMEOUT_MS); })
      ]);
    } finally {
      window.clearTimeout(releaseTimer);
    }
  }

  async cancel() {
    this.terminal = true;
    this.clearTimers();
    this.tail = new Int16Array();
    if (this.socket.readyState === WebSocket.OPEN) {
      this.socket.send(JSON.stringify({ event: "cancel", last_sequence: Math.max(0, this.nextSequence - 1) }));
    }
    if (this.socket.readyState < WebSocket.CLOSING) this.socket.close(1000, "cancelled");
  }

  private waitUntilReady() {
    return new Promise<void>((resolve, reject) => {
      this.readyResolve = resolve;
      this.readyReject = reject;
      this.readyTimer = window.setTimeout(() => {
        if (this.ready || this.terminal) return;
        this.fail({ code: "speech_timeout", retryable: true });
      }, READY_TIMEOUT_MS);
    });
  }

  private clearTimers() {
    window.clearTimeout(this.readyTimer);
    window.clearTimeout(this.finalTimer);
    this.readyTimer = 0;
    this.finalTimer = 0;
  }

  private sendFrame(samples: Int16Array) {
    const byteLength = 8 + samples.length * 2;
    if (this.socket.readyState !== WebSocket.OPEN || this.socket.bufferedAmount + byteLength > MAX_BUFFERED_AUDIO_BYTES) {
      this.fail({ code: "speech_stream_overrun", retryable: true });
      return;
    }
    const frame = new ArrayBuffer(byteLength);
    const view = new DataView(frame);
    view.setUint32(0, this.nextSequence, false);
    view.setUint32(4, samples.length, false);
    for (let index = 0; index < samples.length; index += 1) {
      view.setInt16(8 + index * 2, samples[index], true);
    }
    this.socket.send(frame);
    this.nextSequence += 1;
    this.sentSamples += samples.length;
  }

  private handleMessage(message: MessageEvent) {
    if (this.terminal || typeof message.data !== "string") {
      if (!this.terminal) this.fail({ code: "speech_stream_protocol_error", retryable: false });
      return;
    }
    let event: SpeechRealtimeEvent;
    try {
      event = JSON.parse(message.data) as SpeechRealtimeEvent;
    } catch {
      this.fail({ code: "speech_stream_protocol_error", retryable: false });
      return;
    }
    if (!this.ready) {
      if (!validReady(event, this.ticket)) {
        this.fail({ code: event.code || "speech_stream_protocol_error", retryable: event.retryable === true });
        return;
      }
      this.ready = true;
      window.clearTimeout(this.readyTimer);
      this.readyTimer = 0;
      this.readyResolve?.();
      this.readyResolve = null;
      this.readyReject = null;
      return;
    }
    switch (event.event) {
      case "ack":
        if (!Number.isInteger(event.accepted_sequence) || (event.accepted_sequence ?? -1) >= this.nextSequence) {
          this.fail({ code: "speech_stream_protocol_error", retryable: false });
        }
        return;
      case "partial": {
        const revision = event.revision ?? 0;
        if (!Number.isInteger(revision) || revision <= this.revision || typeof event.text !== "string") {
          this.fail({ code: "speech_stream_protocol_error", retryable: false });
          return;
        }
        this.revision = revision;
        this.handlers.onPartial({
          revision,
          text: event.text,
          language: event.language ?? "",
          audioEndMs: event.audio_end_ms ?? 0
        });
        return;
      }
      case "final": {
        const revision = event.revision ?? 0;
        if (!Number.isInteger(revision) || revision <= this.revision || typeof event.text !== "string" || !this.finishResolve) {
          this.fail({ code: "speech_stream_protocol_error", retryable: false });
          return;
        }
        this.revision = revision;
        this.terminal = true;
        this.clearTimers();
        this.finishResolve({
          revision,
          text: event.text,
          language: event.language ?? "",
          durationMs: event.duration_ms ?? Math.round((this.sentSamples / SPEECH_REALTIME_SAMPLE_RATE) * 1000),
          inferenceMs: event.inference_ms ?? 0,
          model: event.model ?? "",
          stopReason: event.stop_reason ?? ""
        });
        this.finishResolve = null;
        this.finishReject = null;
        this.socket.close(1000, "complete");
        return;
      }
      case "fallback":
      case "error":
        this.fail({ code: event.code || "speech_inference_failed", retryable: event.retryable === true });
        return;
      default:
        this.fail({ code: "speech_stream_protocol_error", retryable: false });
    }
  }

  private fail(failure: SpeechRealtimeFailure) {
    if (this.terminal) return;
    const wasReady = this.ready;
    this.terminal = true;
    this.clearTimers();
    this.readyReject?.(failure);
    this.readyResolve = null;
    this.readyReject = null;
    this.finishReject?.(failure);
    this.finishResolve = null;
    this.finishReject = null;
    if (this.socket.readyState < WebSocket.CLOSING) this.socket.close(1011, "realtime failure");
    if (wasReady) this.handlers.onFailure(failure);
  }
}

function validateTicket(ticket: SpeechRealtimeTicket) {
  if (!ticket.id || !ticket.url || ticket.protocol !== SPEECH_REALTIME_PROTOCOL ||
    ticket.format?.sample_rate !== SPEECH_REALTIME_SAMPLE_RATE || ticket.format.channels !== 1 ||
    ticket.format.bits_per_sample !== 16 || ticket.format.frame_ms !== 100 ||
    ticket.limits?.max_frame_samples !== SPEECH_REALTIME_FRAME_SAMPLES || ticket.limits.max_audio_seconds <= 0) {
    throw { code: "speech_stream_protocol_error", retryable: false } satisfies SpeechRealtimeFailure;
  }
}

function validReady(event: SpeechRealtimeEvent, ticket: SpeechRealtimeTicket) {
  return event.event === "ready" && event.protocol === SPEECH_REALTIME_PROTOCOL &&
    event.format?.sample_rate === ticket.format.sample_rate && event.format.channels === 1 &&
    event.format.bits_per_sample === 16 && event.format.frame_ms === ticket.format.frame_ms &&
    event.limits?.max_frame_samples === ticket.limits.max_frame_samples &&
    event.limits.max_audio_seconds === ticket.limits.max_audio_seconds;
}

function webSocketURL(path: string) {
  const target = new URL(path, window.location.href);
  target.protocol = target.protocol === "https:" ? "wss:" : "ws:";
  return target.toString();
}
