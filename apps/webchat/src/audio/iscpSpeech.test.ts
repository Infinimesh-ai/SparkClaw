// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { ISCPSpeechRealtimeClient } from "./iscpSpeech";
import type { SpeechRealtimeEvent } from "../api/types";
import type { SparkClawDesktop } from "../desktop/types";

const ready: SpeechRealtimeEvent = { event: "ready", protocol: "sparkclaw.speech.realtime.v1", format: { sample_rate: 16000, channels: 1, bits_per_sample: 16, frame_ms: 100 }, limits: { max_frame_samples: 1600, max_audio_seconds: 60 } };
const request = { session_id: "conversation", request_id: "voice-request", language: "auto" };
function fixture() {
  const handlers = { onPartial: vi.fn(), onFailure: vi.fn() };
  const packets: Array<{ sequence: number; event: SpeechRealtimeEvent }> = [];
  const rpc = vi.fn(async (input: Record<string, unknown>): Promise<unknown> => {
    if (input.action === "open") return { session_id: "stream", ready };
    if (input.action === "frame") return { accepted_sequence: input.sequence };
    if (input.action === "events") return { session_id: "stream", events: packets.splice(0), closed: false };
    return {};
  });
  window.sparkclawDesktop = { runtimeKind: "electron", capabilityVersion: 1, speechStream: rpc } as unknown as SparkClawDesktop;
  return { rpc, handlers, packets };
}
beforeEach(() => {
  vi.useFakeTimers();
  vi.stubGlobal("fetch", vi.fn(() => { throw new Error("HTTP must not be reached"); }));
  vi.stubGlobal("WebSocket", vi.fn(() => { throw new Error("WebSocket must not be reached"); }));
});
afterEach(() => { delete window.sparkclawDesktop; vi.useRealTimers(); vi.unstubAllGlobals(); });

it("uses sequence one, bounded PCM frames, replacing partials and an authoritative final through IPC", async () => {
  const f = fixture(); const client = await ISCPSpeechRealtimeClient.connect(request, f.handlers);
  client.push(Int16Array.from({ length: 1700 }, (_, index) => index));
  f.packets.push({ sequence: 1, event: { event: "partial", revision: 1, text: "first" } });
  await vi.advanceTimersByTimeAsync(0);
  f.packets.push({ sequence: 2, event: { event: "partial", revision: 2, text: "replacement" } });
  await vi.advanceTimersByTimeAsync(250);
  expect(f.handlers.onPartial.mock.calls.map(call => call[0].text)).toEqual(["first", "replacement"]);
  const final = client.finish("manual_stop");
  f.packets.push({ sequence: 3, event: { event: "final", revision: 3, text: "authoritative" } });
  await vi.advanceTimersByTimeAsync(250);
  expect((await final).text).toBe("authoritative");
  const frames = f.rpc.mock.calls.map(call => call[0]).filter(input => input.action === "frame");
  expect(frames.map(frame => frame.sequence)).toEqual([1, 2]);
  expect(frames.map(frame => (frame.bytes as Uint8Array).length)).toEqual([3200, 200]);
  expect(new DataView((frames[0].bytes as Uint8Array).buffer).getInt16(400, true)).toBe(200);
  expect(f.rpc).toHaveBeenCalledWith({ action: "finish", session_id: "stream", last_sequence: 2, total_samples: 1700, reason: "manual_stop" });
  expect(f.handlers.onFailure).not.toHaveBeenCalled();
  expect(fetch).not.toHaveBeenCalled(); expect(WebSocket).not.toHaveBeenCalled();
});

it("terminates capture at the five second hard queue bound without HTTP fallback or auto reopen", async () => {
  const f = fixture(); const client = await ISCPSpeechRealtimeClient.connect(request, f.handlers);
  client.push(new Int16Array(80001));
  await vi.advanceTimersByTimeAsync(1000);
  expect(f.handlers.onFailure).toHaveBeenCalledWith({ code: "speech_stream_overrun", retryable: false });
  expect(f.rpc.mock.calls.filter(call => call[0].action === "open")).toHaveLength(1);
  expect(f.rpc.mock.calls.filter(call => call[0].action === "frame")).toHaveLength(0);
  expect(fetch).not.toHaveBeenCalled(); expect(WebSocket).not.toHaveBeenCalled();
});

it("rejects frame acknowledgement mismatch and an event sequence gap", async () => {
  const f = fixture(); const client = await ISCPSpeechRealtimeClient.connect(request, f.handlers);
  f.rpc.mockImplementationOnce(async () => ({ accepted_sequence: 2 }));
  client.push(new Int16Array(1600));
  await vi.advanceTimersByTimeAsync(0);
  expect(f.handlers.onFailure).toHaveBeenCalledOnce();
  expect(f.rpc).toHaveBeenCalledWith({ action: "cancel", session_id: "stream" });
  const other = fixture(); const second = await ISCPSpeechRealtimeClient.connect(request, other.handlers);
  other.packets.push({ sequence: 2, event: { event: "partial", revision: 1, text: "lost first event" } });
  await vi.advanceTimersByTimeAsync(0);
  expect(other.handlers.onFailure).toHaveBeenCalledOnce();
  await second.cancel();
});

it("cancels a pending final immediately and rejects incompatible audio negotiation", async () => {
  const f = fixture(); const client = await ISCPSpeechRealtimeClient.connect(request, f.handlers);
  client.push(new Int16Array(1600));
  await vi.advanceTimersByTimeAsync(0);
  const final = client.finish("manual_stop");
  const outcome = final.catch(error => error);
  await vi.advanceTimersByTimeAsync(0);
  await client.cancel();
  expect(await outcome).toEqual({ code: "speech_cancelled", retryable: false });
  f.rpc.mockImplementationOnce(async () => ({ session_id: "invalid", ready: { ...ready, format: { ...ready.format, sample_rate: 48000 } } }));
  await expect(ISCPSpeechRealtimeClient.connect(request, f.handlers)).rejects.toThrow("Invalid speech negotiation");
  expect(f.rpc).toHaveBeenCalledWith({ action: "cancel", session_id: "invalid" });
});
