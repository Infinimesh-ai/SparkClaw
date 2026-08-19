import { describe, expect, it } from "vitest";
import { StatefulPCM16Resampler } from "./pcm16";

describe("StatefulPCM16Resampler", () => {
  it("produces the same canonical PCM across arbitrary callback boundaries", () => {
    const input = Float32Array.from({ length: 48_000 }, (_, index) => Math.sin(index / 31) * 0.5);
    const whole = new StatefulPCM16Resampler(48_000);
    const expected = join([whole.process(input), whole.flush()]);

    const split = new StatefulPCM16Resampler(48_000);
    const actual = join([
      split.process(input.subarray(0, 4_097)),
      split.process(input.subarray(4_097, 19_333)),
      split.process(input.subarray(19_333)),
      split.flush()
    ]);

    expect(actual).toEqual(expected);
    expect(actual).toHaveLength(16_000);
  });

  it("preserves duration for non-integer device sample-rate ratios", () => {
    const resampler = new StatefulPCM16Resampler(44_100);
    const output = join([resampler.process(new Float32Array(44_100).fill(0.25)), resampler.flush()]);
    expect(output).toHaveLength(16_000);
    expect(output[500]).toBeCloseTo(8192, 0);
  });
});

function join(chunks: Int16Array[]) {
  const output = new Int16Array(chunks.reduce((sum, chunk) => sum + chunk.length, 0));
  let offset = 0;
  for (const chunk of chunks) {
    output.set(chunk, offset);
    offset += chunk.length;
  }
  return output;
}
