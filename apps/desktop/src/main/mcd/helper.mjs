import { spawn } from 'node:child_process';
import path from 'node:path';
import { verifyMCDRuntime } from './runtime.mjs';

// No shell, environment credentials, shared MCP connection or runtime install.
export async function callMCDHelper(python, request, { spawnProcess = spawn, signal, role = 'issuer', verifyBundle = false } = {}) {
  if (!path.isAbsolute(python)) throw new Error('McDonald’s runtime is unavailable');
  if (!['issuer', 'host'].includes(role)) throw new Error('McDonald’s helper role is unavailable');
  if (verifyBundle) await verifyMCDRuntime(python);
  const input = Buffer.from(JSON.stringify(request));
  if (input.length > 320 * 1024) { input.fill(0); throw new Error('McDonald’s request exceeds capacity'); }
  return new Promise((resolve, reject) => {
    let child;
    try { child = spawnProcess(python, ['-I', '-B', '-m', 'sparkclaw_mcd.helper', '--role', role], { stdio: ['pipe', 'pipe', 'pipe'], windowsHide: true }); }
    catch { input.fill(0); reject(new Error('McDonald’s runtime is unavailable')); return; }
    let output = Buffer.alloc(0), done = false;
    const finish = (error, value) => {
      if (done) return; done = true; clearTimeout(timer); signal?.removeEventListener('abort', abort);
      input.fill(0); output.fill(0); child.stdin.destroy(); child.kill('SIGKILL');
      error ? reject(error) : resolve(value);
    };
    const abort = () => finish(new Error('McDonald’s connection was interrupted'));
    const timer = setTimeout(abort, 90000); timer.unref?.();
    signal?.addEventListener('abort', abort, { once: true });
    child.stderr.on('data', () => {});
    child.on('error', abort); child.stdin.on('error', abort);
    child.stdout.on('data', chunk => {
      if (output.length + chunk.length > 320 * 1024) { abort(); return; }
      const previous = output; output = Buffer.concat([previous, chunk]); previous.fill(0); chunk.fill(0);
    });
    child.on('close', code => {
      if (done) return;
      try {
        const response = JSON.parse(output.toString('utf8'));
        if (code !== 0 || response.ok !== true || !response.result) throw new Error();
        finish(undefined, response.result);
      } catch { finish(new Error('McDonald’s session could not be verified')); }
    });
    if (signal?.aborted) abort(); else child.stdin.end(input);
  });
}
