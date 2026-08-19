import { describe, expect, it } from "vitest";
import { SilenceDetector } from "./silenceDetector";

describe("SilenceDetector", () => {
  it("is disabled by default mode and never decides", () => {
    const detector = new SilenceDetector("off");
    expect(detector.state).toBe("disabled");
    expect(detector.process(tone(16_000, 0.5))).toBeNull();
  });

  it("stops once after confirmed speech and standard trailing silence", () => {
    const detector = new SilenceDetector("standard");
    expect(detector.process(tone(4_000, 0.002))).toBeNull();
    expect(detector.process(tone(8_000, 0.35))).toBeNull();
    expect(detector.state).toBe("speech_active");
    expect(detector.process(tone(19_000, 0.001))).toBeNull();
    expect(detector.process(tone(400, 0.001))).toBe("auto_stop");
    expect(detector.process(tone(16_000, 0.001))).toBeNull();
  });

  it("cancels after ten seconds when speech was never confirmed", () => {
    const detector = new SilenceDetector("patient");
    expect(detector.process(tone(159_680, 0.002))).toBeNull();
    expect(detector.process(tone(320, 0.002))).toBe("no_speech_cancel");
  });

  it("does not arm on a short loud transient", () => {
    const detector = new SilenceDetector("standard");
    expect(detector.process(tone(4_000, 0.002))).toBeNull();
    expect(detector.process(tone(1_600, 0.8))).toBeNull();
    expect(detector.process(tone(20_000, 0.001))).toBeNull();
    expect(detector.state).toBe("waiting_for_speech");
  });
});

function tone(length: number, amplitude: number) {
  const value = Math.round(amplitude * 0x7fff);
  return new Int16Array(length).fill(value);
}
