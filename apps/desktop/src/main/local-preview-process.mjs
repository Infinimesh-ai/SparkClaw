import { spawn, execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { opendir, lstat } from 'node:fs/promises';
import path from 'node:path';
import { previewError } from './local-preview-limits.mjs';

const run = promisify(execFile);
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));

export async function treeDiskBytes(directory, limit) {
  let total = 0, count = 0;
  const pending = [directory];
  while (pending.length) {
    let stream;
    try { stream = await opendir(pending.pop()); } catch (error) { if (error.code === 'ENOENT') continue; throw error; }
    for await (const entry of stream) {
      if (++count > 30_000) throw previewError('limit_exceeded', 'Preview temporary storage has too many entries.');
      const fullPath = path.join(stream.path, entry.name);
      const metadata = await lstat(fullPath).catch(error => { if (error.code === 'ENOENT') return null; throw error; });
      if (!metadata) continue;
      if (metadata.isDirectory()) pending.push(fullPath);
      else total += metadata.size;
      if (total > limit) throw previewError('limit_exceeded', 'Preview temporary storage exceeds its budget.');
    }
  }
  return total;
}

export async function processTree(root, known = new Set()) {
  const { stdout } = await run('/bin/ps', ['-axo', 'pid=,ppid=,rss=,stat='], { timeout: 1000, maxBuffer: 2 * 1024 * 1024, env: {} });
  const rows = stdout.trim().split('\n').map(line => line.trim().split(/\s+/)).map(([pid, parent, rss, state]) => ({ pid: Number(pid), parent: Number(parent), bytes: Number(rss) * 1024, state }));
  const selected = new Set([root, ...known]);
  let changed;
  do { changed = false; for (const row of rows) if (selected.has(row.parent) && !selected.has(row.pid)) { selected.add(row.pid); changed = true; } } while (changed);
  const live = rows.filter(row => selected.has(row.pid) && !row.state?.startsWith('Z'));
  for (const row of live) known.add(row.pid);
  return { pids: live.map(row => row.pid), bytes: live.reduce((sum, row) => sum + row.bytes, 0) };
}

export async function runPreviewProcess({ launch, jobDirectory, rootDirectory, limits, signal, onObservation = () => {} }) {
  if (signal.aborted) throw signal.reason;
  const startedAt = Date.now();
  const child = spawn(launch.command, launch.args, launch.options);
  const known = new Set();
  let spawnedError, closed = false, code, failure, monitoring = false, peakMemory = 0, peakDisk = 0, stoppedAt;
  const closedPromise = new Promise(resolve => {
    child.on('error', error => { spawnedError = error; });
    child.on('close', value => { closed = true; code = value; resolve(); });
  });
  // Do not retain converter output: it can contain document text or private paths.
  child.stdout?.resume(); child.stderr?.resume();
  const kill = signalName => {
    try { if (child.pid) process.kill(-child.pid, signalName); } catch {}
    for (const pid of known) { try { process.kill(pid, signalName); } catch {} }
  };
  const stop = error => { if (!failure) stoppedAt = Date.now(); failure ??= error; kill('SIGTERM'); };
  const aborted = () => stop(signal.reason ?? previewError('cancelled', 'Preview cancelled.'));
  signal.addEventListener('abort', aborted, { once: true });
  let forceTimer;
  const monitor = async () => {
    if (monitoring || closed) return;
    monitoring = true;
    try {
      const [tree, disk] = await Promise.all([processTree(child.pid, known), treeDiskBytes(jobDirectory, limits.taskDiskBytes), treeDiskBytes(rootDirectory, limits.diskBytes)]);
      peakMemory = Math.max(peakMemory, tree.bytes); peakDisk = Math.max(peakDisk, disk);
      if (tree.bytes > limits.memoryBytes) stop(previewError('limit_exceeded', 'The document parser exceeded the process memory budget.'));
    } catch (error) { stop(error.code ? error : previewError('preview_unavailable', 'Preview resource monitoring is unavailable.')); }
    finally { monitoring = false; }
    if (failure && !forceTimer) forceTimer = setTimeout(() => kill('SIGKILL'), limits.killGraceMs);
  };
  const timer = setInterval(() => void monitor(), limits.monitorMs);
  const deadline = setTimeout(() => stop(previewError('timeout', 'The local preview timed out.')), limits.deadlineMs);
  const escalation = setInterval(() => { if (failure && !forceTimer) forceTimer = setTimeout(() => kill('SIGKILL'), limits.killGraceMs); }, Math.min(100, limits.monitorMs));
  void monitor();
  try {
    await closedPromise;
    clearInterval(timer);
    while (monitoring) await delay(10);
    // Parent exit does not prove converter descendants exited. Kill and join the
    // process group plus observed descendants before allowing directory cleanup.
    const cleanupStartedAt = Date.now();
    kill('SIGKILL');
    const until = Date.now() + limits.cleanupMs;
    while (child.pid && (await processTree(child.pid, known)).pids.length) {
      kill('SIGKILL');
      if (Date.now() > until) throw previewError('cleanup_pending', 'Preview processes have not exited. Retry clearing previews.');
      await delay(25);
    }
    onObservation({ peakMemoryBytes: peakMemory, peakDiskBytes: peakDisk, durationMs: Date.now() - startedAt,
      processJoinMs: Date.now() - cleanupStartedAt, ...(stoppedAt ? { stopToExitMs: Date.now() - stoppedAt } : {}) });
    if (spawnedError) throw previewError('preview_unavailable', 'The isolated preview process could not start.');
    if (failure) throw failure;
    return { code, peakMemoryBytes: peakMemory, peakDiskBytes: peakDisk };
  } finally {
    clearInterval(timer); clearInterval(escalation); clearTimeout(deadline); clearTimeout(forceTimer);
    signal.removeEventListener('abort', aborted);
  }
}
