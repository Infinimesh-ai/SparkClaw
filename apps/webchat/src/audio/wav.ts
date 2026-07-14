import type { CapturedPCM } from "./pcmCapture";

export const SPEECH_SAMPLE_RATE = 16000;
export const SPEECH_MIN_DURATION_MS = 300;

export class VoiceAudioError extends Error {
  readonly code: "speech_too_short" | "speech_too_large" | "speech_unsupported_format";

  constructor(code: VoiceAudioError["code"], message: string) {
    super(message);
    this.code = code;
  }
}

export function encodeSpeechWAV(capture: CapturedPCM, maxDurationSeconds: number, maxUploadBytes: number) {
  if (capture.durationMs < SPEECH_MIN_DURATION_MS) {
    throw new VoiceAudioError("speech_too_short", "recording is too short");
  }
  if (capture.durationMs > maxDurationSeconds * 1000) {
    throw new VoiceAudioError("speech_too_large", "recording exceeds the duration limit");
  }
  if (capture.sampleRate <= 0 || capture.samples.length === 0) {
    throw new VoiceAudioError("speech_unsupported_format", "recording format is invalid");
  }
  const samples = resampleMono(capture.samples, capture.sampleRate, SPEECH_SAMPLE_RATE);
  const buffer = new ArrayBuffer(44 + samples.length * 2);
  const view = new DataView(buffer);
  writeASCII(view, 0, "RIFF");
  view.setUint32(4, buffer.byteLength - 8, true);
  writeASCII(view, 8, "WAVE");
  writeASCII(view, 12, "fmt ");
  view.setUint32(16, 16, true);
  view.setUint16(20, 1, true);
  view.setUint16(22, 1, true);
  view.setUint32(24, SPEECH_SAMPLE_RATE, true);
  view.setUint32(28, SPEECH_SAMPLE_RATE * 2, true);
  view.setUint16(32, 2, true);
  view.setUint16(34, 16, true);
  writeASCII(view, 36, "data");
  view.setUint32(40, samples.length * 2, true);
  for (let index = 0; index < samples.length; index += 1) {
    const sample = Math.max(-1, Math.min(1, samples[index]));
    view.setInt16(44 + index * 2, sample < 0 ? sample * 0x8000 : sample * 0x7fff, true);
  }
  if (buffer.byteLength > maxUploadBytes) {
    throw new VoiceAudioError("speech_too_large", "recording exceeds the upload limit");
  }
  return new Blob([buffer], { type: "audio/wav" });
}

export function resampleMono(input: Float32Array, sourceRate: number, targetRate: number) {
  if (sourceRate === targetRate) return input.slice();
  const outputLength = Math.max(1, Math.round((input.length * targetRate) / sourceRate));
  const output = new Float32Array(outputLength);
  if (sourceRate > targetRate) {
    const ratio = sourceRate / targetRate;
    for (let index = 0; index < outputLength; index += 1) {
      const start = Math.floor(index * ratio);
      const end = Math.min(input.length, Math.max(start + 1, Math.floor((index + 1) * ratio)));
      let sum = 0;
      for (let sourceIndex = start; sourceIndex < end; sourceIndex += 1) sum += input[sourceIndex];
      output[index] = sum / (end - start);
    }
    return output;
  }
  const ratio = sourceRate / targetRate;
  for (let index = 0; index < outputLength; index += 1) {
    const position = index * ratio;
    const left = Math.floor(position);
    const right = Math.min(input.length - 1, left + 1);
    const fraction = position - left;
    output[index] = input[left] * (1 - fraction) + input[right] * fraction;
  }
  return output;
}

function writeASCII(view: DataView, offset: number, value: string) {
  for (let index = 0; index < value.length; index += 1) view.setUint8(offset + index, value.charCodeAt(index));
}
