import { describe, expect, it } from "vitest";
import { encodeSpeechWAV, resampleMono, SPEECH_SAMPLE_RATE, VoiceAudioError } from "./wav";

describe("speech WAV encoding", () => {
  it("encodes canonical 16 kHz mono PCM16 WAV", async () => {
    const samples = new Float32Array(16000);
    samples[0] = -1;
    samples[1] = 1;
    const wav = encodeSpeechWAV({ samples, sampleRate: 16000, durationMs: 1000 }, 60, 3 << 20);
    const view = new DataView(await wav.arrayBuffer());
    expect(wav.type).toBe("audio/wav");
    expect(String.fromCharCode(...new Uint8Array(view.buffer, 0, 4))).toBe("RIFF");
    expect(String.fromCharCode(...new Uint8Array(view.buffer, 8, 4))).toBe("WAVE");
    expect(view.getUint16(20, true)).toBe(1);
    expect(view.getUint16(22, true)).toBe(1);
    expect(view.getUint32(24, true)).toBe(SPEECH_SAMPLE_RATE);
    expect(view.getUint16(34, true)).toBe(16);
    expect(view.byteLength).toBe(44 + 16000 * 2);
  });

  it("downsamples without changing the requested duration", () => {
    const input = Float32Array.from({ length: 480 }, (_, index) => index / 480);
    const output = resampleMono(input, 48000, 16000);
    expect(output).toHaveLength(160);
    expect(output[0]).toBeCloseTo((input[0] + input[1] + input[2]) / 3);
  });

  it("rejects recordings below the minimum duration", () => {
    expect(() => encodeSpeechWAV({ samples: new Float32Array(100), sampleRate: 16000, durationMs: 100 }, 60, 3 << 20))
      .toThrowError(VoiceAudioError);
  });
});
