// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { PCMInputCapture } from "./pcmCapture";

class FakeTrack extends EventTarget {
  readyState: MediaStreamTrackState = "live";
  stop = vi.fn(() => { this.readyState = "ended"; });
  getSettings = () => ({ deviceId: "effective-device" });

  disconnect() {
    this.readyState = "ended";
    this.dispatchEvent(new Event("ended"));
  }
}

class FakeStream {
  constructor(readonly track: FakeTrack) {}
  getTracks = () => [this.track];
  getAudioTracks = () => [this.track];
}

class FakeAudioNode {
  connect = vi.fn(() => this);
  disconnect = vi.fn();
}

class FakePort extends EventTarget {
  onmessage: ((event: MessageEvent) => void) | null = null;

  postMessage(message: { type: string; id?: number }) {
    if (message.type === "flush") this.emit({ type: "flushed", id: message.id });
  }

  emit(data: unknown) {
    const event = new MessageEvent("message", { data });
    this.onmessage?.(event);
    this.dispatchEvent(event);
  }
}

class FakeWorkletNode extends FakeAudioNode {
  readonly port = new FakePort();

  constructor() {
    super();
    window.setTimeout(() => this.port.emit({ type: "samples", samples: new Float32Array(4096), level: 0.1 }), 0);
  }
}

class FakeAudioContext extends EventTarget {
  sampleRate = 48000;
  state: AudioContextState = "running";
  destination = new FakeAudioNode();
  source = new FakeAudioNode();
  gain = Object.assign(new FakeAudioNode(), { gain: { value: 1 } });
  addModule = vi.fn(async () => {});
  resume = vi.fn(async () => {});
  close = vi.fn(async () => {
    this.state = "closed";
    this.dispatchEvent(new Event("statechange"));
  });
  createMediaStreamSource = vi.fn(() => this.source);
  createGain = vi.fn(() => this.gain);
}

Object.defineProperty(FakeAudioContext.prototype, "audioWorklet", {
  get(this: FakeAudioContext) { return { addModule: this.addModule }; }
});

describe("PCMInputCapture", () => {
  let track: FakeTrack;
  let stream: FakeStream;
  let context: FakeAudioContext;
  let getUserMedia: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    track = new FakeTrack();
    stream = new FakeStream(track);
    context = new FakeAudioContext();
    getUserMedia = vi.fn(async () => stream);
    Object.defineProperty(navigator, "mediaDevices", {
      configurable: true,
      value: { getUserMedia }
    });
    function AudioContextMock() {
      return context;
    }
    Object.defineProperty(AudioContextMock.prototype, "audioWorklet", { configurable: true, value: {} });
    vi.stubGlobal("AudioContext", AudioContextMock);
    vi.stubGlobal("AudioWorkletNode", FakeWorkletNode);
  });

  afterEach(() => vi.unstubAllGlobals());

  it("falls back once when the preferred device has disappeared", async () => {
    getUserMedia
      .mockRejectedValueOnce(new DOMException("missing", "NotFoundError"))
      .mockResolvedValueOnce(stream);

    const capture = await PCMInputCapture.start({ deviceId: "missing-device", onLevel: () => {} });
    expect(capture.usedDefaultFallback).toBe(true);
    expect(getUserMedia).toHaveBeenCalledTimes(2);
    expect(getUserMedia.mock.calls[0][0].audio.deviceId).toEqual({ exact: "missing-device" });
    expect(getUserMedia.mock.calls[1][0].audio.deviceId).toBeUndefined();
    await capture.cancel();
    expect(track.stop).toHaveBeenCalledOnce();
    expect(context.source.disconnect).toHaveBeenCalledOnce();
    expect(context.close).toHaveBeenCalledOnce();
  });

  it("reports runtime device loss after releasing every capture resource", async () => {
    let resolveFailure: (() => void) | undefined;
    const failed = new Promise<void>((resolve) => { resolveFailure = resolve; });
    const onFailure = vi.fn(() => resolveFailure?.());
    await PCMInputCapture.start({ onLevel: () => {}, onFailure });

    track.disconnect();
    await failed;
    expect(onFailure).toHaveBeenCalledWith(
      expect.objectContaining({ code: "voice_device_disconnected" }),
      expect.objectContaining({ sampleRate: 16000, samples: expect.any(Int16Array) })
    );
    expect(track.stop).toHaveBeenCalledOnce();
    expect(context.source.disconnect).toHaveBeenCalledOnce();
    expect(context.gain.disconnect).toHaveBeenCalledOnce();
    expect(context.close).toHaveBeenCalledOnce();
  });
});
