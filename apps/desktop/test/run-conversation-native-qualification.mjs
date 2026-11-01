// Use the production desktop and Agent in an isolated Linux Gateway/ISCP lab.
const realModel = process.argv[2] === '--real-model';
if (process.argv.length > (realModel ? 3 : 2)) throw new Error('Usage: run-conversation-native-qualification.mjs [--real-model]');
if (!process.env.SPARKCLAW_STREAM_FIXTURE_LINUX_IMAGE) throw new Error('Explicit prepared Linux fixture image required');
process.env.TZ = 'Asia/Shanghai';
process.argv = [process.argv[0], process.argv[1], '1', realModel ? '--conversation-real-model' : '--conversation'];
await import('./run-stream-native-qualification.mjs');
