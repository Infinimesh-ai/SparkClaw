import { spawn } from 'node:child_process';
import path from 'node:path';

// This trusted supervisor is outside the parser sandbox. Its stdin is a pipe
// held only by the launching app, so EOF identifies that exact owner's death
// without looking up a stale PID. The parser cannot block this event loop.
const owner = Number(process.argv[2]), deadline = Number(process.argv[3]);
if (!Number.isSafeInteger(owner) || owner < 1 || process.ppid !== owner ||
    !Number.isSafeInteger(deadline) || deadline < 1 || deadline > 2_147_483_647 || typeof process.send !== 'function') process.exit(1);

const stop = () => {
  // The guardian is the leader of its freshly detached process group. Never
  // kill a PID read from disk or inventory unrelated system processes.
  try { process.kill(-process.pid, 'SIGKILL'); } catch { process.exit(1); }
};
setTimeout(stop, deadline);
process.on('SIGTERM', stop);
process.on('SIGINT', stop);
process.stdin.on('end', stop);
process.stdin.on('error', stop);

let input = '', started = false;
process.stdin.setEncoding('utf8');
process.stdin.on('data', chunk => {
  if (started || input.length + chunk.length > 256 * 1024) return stop();
  input += chunk;
  const end = input.indexOf('\n');
  if (end < 0) return;
  if (end !== input.length - 1) return stop();
  let launch;
  try { launch = JSON.parse(input.slice(0, end)); } catch { return stop(); }
  input = ''; started = true;
  if (!path.isAbsolute(launch.command) || !Array.isArray(launch.args) ||
      launch.args.some(value => typeof value !== 'string') || !path.isAbsolute(launch.options?.cwd)) return stop();
  const child = spawn(launch.command, launch.args, {
    cwd: launch.options.cwd, env: launch.options.env, detached: false, stdio: ['ignore', 'inherit', 'inherit'],
  });
  child.on('error', stop);
  child.on('exit', code => {
    // Keep the group leader alive until its descendants are killed, even when
    // the app dies at the same time. The parser never inherits this IPC channel.
    process.send({ type: 'preview-exit', code: code ?? 1 }, stop);
    setTimeout(stop, 50);
  });
});

// A live owner must deliver the launch description promptly. This also bounds
// a startup that never reaches the normal preview deadline in the parent.
setTimeout(() => { if (!started) stop(); }, Math.min(5000, deadline));
