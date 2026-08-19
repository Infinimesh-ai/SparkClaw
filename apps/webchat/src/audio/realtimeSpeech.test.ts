// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { SpeechRealtimeEvent, SpeechRealtimeTicket } from "../api/types";
import { SpeechRealtimeClient } from "./realtimeSpeech";

class FakeWebSocket {
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSING = 2;
  static readonly CLOSED = 3;
  static instances: FakeWebSocket[] = [];
  readonly sent: unknown[] = [];
  readyState = FakeWebSocket.OPEN;
  bufferedAmount = 0;
  binaryType = "";
  onmessage: ((event: MessageEvent) => void) | null = null;
  onerror: (() => void) | null = null;
  onclose: (() => void) | null = null;

  constructor(readonly url: string) {
    FakeWebSocket.instances.push(this);
  }

  send(value: unknown) {
    this.sent.push(value);
  }

  close() {
    if (this.readyState === FakeWebSocket.CLOSED) return;
    this.readyState = FakeWebSocket.CLOSED;
    this.onclose?.();
  }

  emit(event: SpeechRealtimeEvent) {
    this.onmessage?.(new MessageEvent("message", { data: JSON.stringify(event) }));
  }
}

const ticket: SpeechRealtimeTicket = {
  id: "speech-rt-a",
  url: "/api/speech/realtime?ticket=secret",
  expires_at: new Date(Date.now() + 30_000).toISOString(),
  protocol: "sparkclaw.speech.realtime.v1",
  format: { sample_rate: 16_000, channels: 1, bits_per_sample: 16, frame_ms: 100 },
  limits: { max_audio_seconds: 60, max_frame_samples: 1_600 }
};

describe("SpeechRealtimeClient", () => {
  beforeEach(() => {
    FakeWebSocket.instances = [];
    vi.stubGlobal("WebSocket", FakeWebSocket);
  });

  afterEach(() => vi.unstubAllGlobals());

  it("sends exact PCM frames and resolves the authoritative final", async () => {
    const onPartial = vi.fn();
    const onFailure = vi.fn();
    const connecting = SpeechRealtimeClient.connect(ticket, { onPartial, onFailure });
    const socket = FakeWebSocket.instances[0];
    socket.emit(ready());
    const client = await connecting;

    client.push(Int16Array.from({ length: 1_700 }, (_, index) => index));
    expect(socket.sent).toHaveLength(1);
    const first = new DataView(socket.sent[0] as ArrayBuffer);
    expect(first.getUint32(0, false)).toBe(0);
    expect(first.getUint32(4, false)).toBe(1_600);
    expect(first.getInt16(8 + 200 * 2, true)).toBe(200);

    socket.emit({ event: "partial", revision: 1, text: "live", language: "English", audio_end_ms: 100 });
    expect(onPartial).toHaveBeenCalledWith(expect.objectContaining({ revision: 1, text: "live" }));
    const finalizing = client.finish("manual_stop");
    expect(socket.sent).toHaveLength(3);
    const tail = new DataView(socket.sent[1] as ArrayBuffer);
    expect(tail.getUint32(0, false)).toBe(1);
    expect(tail.getUint32(4, false)).toBe(100);
    expect(JSON.parse(socket.sent[2] as string)).toMatchObject({
      event: "finish", last_sequence: 1, captured_ms: 106, reason: "manual_stop"
    });
    socket.emit({ event: "final", revision: 2, text: "complete", duration_ms: 106, inference_ms: 20 });
    await expect(finalizing).resolves.toMatchObject({ revision: 2, text: "complete", durationMs: 106 });
    expect(onFailure).not.toHaveBeenCalled();
  });

  it("fails closed when browser websocket buffering exceeds five seconds", async () => {
    const onFailure = vi.fn();
    const connecting = SpeechRealtimeClient.connect(ticket, { onPartial: vi.fn(), onFailure });
    const socket = FakeWebSocket.instances[0];
    socket.emit(ready());
    const client = await connecting;
    socket.bufferedAmount = 160_000;

    client.push(new Int16Array(1_600));

    expect(onFailure).toHaveBeenCalledWith({ code: "speech_stream_overrun", retryable: true });
    expect(socket.sent).toHaveLength(0);
  });
});

function ready(): SpeechRealtimeEvent {
  return {
    event: "ready",
    protocol: ticket.protocol,
    format: ticket.format,
    limits: ticket.limits
  };
}
