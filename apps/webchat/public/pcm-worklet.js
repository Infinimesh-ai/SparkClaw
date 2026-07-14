class SparkClawPCMProcessor extends AudioWorkletProcessor {
  constructor() {
    super();
    this.buffer = new Float32Array(4096);
    this.length = 0;
    this.levelSum = 0;
    this.levelCount = 0;
    this.port.onmessage = (event) => {
      if (event.data?.type === "flush") {
        this.emitBuffer();
        this.port.postMessage({ type: "flushed", id: event.data.id });
      }
    };
  }

  process(inputs) {
    const channels = inputs[0];
    if (!channels || channels.length === 0 || channels[0].length === 0) {
      return true;
    }
    const frameLength = channels[0].length;
    for (let index = 0; index < frameLength; index += 1) {
      let sample = 0;
      for (const channel of channels) {
        sample += channel[index] || 0;
      }
      sample /= channels.length;
      this.buffer[this.length] = sample;
      this.length += 1;
      this.levelSum += sample * sample;
      this.levelCount += 1;
      if (this.length === this.buffer.length) {
        this.emitBuffer();
      }
    }
    return true;
  }

  emitBuffer() {
    if (this.length === 0) return;
    const samples = this.buffer.slice(0, this.length);
    const level = Math.sqrt(this.levelSum / Math.max(1, this.levelCount));
    this.port.postMessage({ type: "samples", samples, level }, [samples.buffer]);
    this.length = 0;
    this.levelSum = 0;
    this.levelCount = 0;
  }
}

registerProcessor("sparkclaw-pcm-capture", SparkClawPCMProcessor);
