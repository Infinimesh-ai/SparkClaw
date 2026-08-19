export const CANONICAL_SAMPLE_RATE = 16_000;

export class StatefulPCM16Resampler {
  private readonly sourceSamplesPerOutput: number;
  private windowRemaining: number;
  private weightedSum = 0;
  private weight = 0;

  constructor(readonly sourceRate: number, readonly targetRate = CANONICAL_SAMPLE_RATE) {
    if (!Number.isFinite(sourceRate) || sourceRate <= 0 || !Number.isFinite(targetRate) || targetRate <= 0) {
      throw new Error("voice_capture_invalid_sample_rate");
    }
    this.sourceSamplesPerOutput = sourceRate / targetRate;
    this.windowRemaining = this.sourceSamplesPerOutput;
  }

  process(input: Float32Array) {
    if (input.length === 0) return new Int16Array();
    const output: number[] = [];
    for (const rawSample of input) {
      const sample = Math.max(-1, Math.min(1, rawSample));
      let sourceWeight = 1;
      while (sourceWeight > 1e-9) {
        const consumed = Math.min(sourceWeight, this.windowRemaining);
        this.weightedSum += sample * consumed;
        this.weight += consumed;
        sourceWeight -= consumed;
        this.windowRemaining -= consumed;
        if (this.windowRemaining <= 1e-9) {
          output.push(floatToPCM16(this.weightedSum / Math.max(this.weight, 1e-9)));
          this.weightedSum = 0;
          this.weight = 0;
          this.windowRemaining = this.sourceSamplesPerOutput;
        }
      }
    }
    return Int16Array.from(output);
  }

  flush() {
    if (this.weight <= 1e-9) return new Int16Array();
    const output = Int16Array.of(floatToPCM16(this.weightedSum / this.weight));
    this.weightedSum = 0;
    this.weight = 0;
    this.windowRemaining = this.sourceSamplesPerOutput;
    return output;
  }
}

export function floatToPCM16(sample: number) {
  const clamped = Math.max(-1, Math.min(1, sample));
  return Math.round(clamped < 0 ? clamped * 0x8000 : clamped * 0x7fff);
}

export function mergePCM16Chunks(chunks: Int16Array[]) {
  const total = chunks.reduce((sum, chunk) => sum + chunk.length, 0);
  const merged = new Int16Array(total);
  let offset = 0;
  for (const chunk of chunks) {
    merged.set(chunk, offset);
    offset += chunk.length;
  }
  return merged;
}
