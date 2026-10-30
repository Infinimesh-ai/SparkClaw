// A disposable Linux Gateway/ISCP lab with the production Mac desktop. The
// default model is deterministic, but input still runs through the real Agent.
// Use --real-model only with an explicit private model configuration.
const realModel = process.argv[2] === "--real-model";
if (process.argv.length > (realModel ? 3 : 2)) throw new Error("Usage: run-schedule-native-qualification.mjs [--real-model]");
if (!process.env.SPARKCLAW_STREAM_FIXTURE_LINUX_IMAGE) throw new Error("Set SPARKCLAW_STREAM_FIXTURE_LINUX_IMAGE to a prepared Linux test image");
process.env.TZ = "Asia/Shanghai";
process.argv = [process.argv[0], process.argv[1], "1", realModel ? "--schedule-real-model" : "--schedule"];
await import("./run-stream-native-qualification.mjs");
