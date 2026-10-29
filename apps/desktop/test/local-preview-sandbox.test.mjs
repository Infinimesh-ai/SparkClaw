import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import { spawn, execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { verifyPreviewSandbox, sandboxCommand } from '../src/main/local-preview-sandbox.mjs';
import { runPreviewProcess, processTree } from '../src/main/local-preview-process.mjs';
import { previewLimits } from '../src/main/local-preview-limits.mjs';

test('actual OS sandbox denies data read, write and loopback outside a private task', { skip: !['darwin', 'linux'].includes(process.platform) }, async t => {
  const base = await fs.mkdtemp(path.join(os.tmpdir(), 'sparkx-isolation-'));
  t.after(() => fs.rm(base, { recursive: true, force: true }));
  const job = path.join(base, 'job'); await fs.mkdir(job);
  const outside = path.join(base, 'secret'); await fs.writeFile(outside, 'private');
  if (process.platform === 'linux') {
    try { await fs.access('/usr/bin/bwrap'); } catch { t.skip('Bubblewrap is not installed on this test host; heavy previews remain unavailable.'); return; }
  }
  assert.deepEqual(await verifyPreviewSandbox({ jobDirectory: job, outsidePath: outside, codeRoots: [] }), { readDenied: true, writeDenied: true, networkDenied: true, metadataDenied: true });
  assert.equal(await fs.readFile(outside, 'utf8'), 'private');
  await assert.rejects(fs.stat(outside + '.write'), { code: 'ENOENT' });
});

test('unsupported platforms cannot start an unsandboxed preview', async t => {
  const job = await fs.mkdtemp(path.join(os.tmpdir(), 'sparkx-no-isolation-'));
  t.after(() => fs.rm(job, { recursive: true, force: true }));
  const script = path.join(job, 'worker.mjs'); await fs.writeFile(script, '');
  await assert.rejects(sandboxCommand({ jobDirectory: job, worker: script, platform: 'unsupported', codeRoots: [] }), { code: 'preview_unavailable' });
});

test('Linux private root and temporary scaffolding are read-only outside the job', { skip: process.platform !== 'linux' }, async t => {
  const job = await fs.mkdtemp(path.join(os.tmpdir(), 'sparkx-readonly-root-'));
  t.after(() => fs.rm(job, { recursive: true, force: true }));
  const worker = path.join(job, 'worker.mjs');
  await fs.writeFile(worker, `import fs from 'node:fs/promises';const results=[];for(const p of ['/outside-job','/tmp/outside-job','/dev/shm/outside-job']){try{await fs.writeFile(p,'denied');results.push(false)}catch(e){results.push(['EROFS','EACCES','EPERM','ENOENT'].includes(e.code))}}await fs.writeFile('result.json',JSON.stringify(results));`);
  const launch = await sandboxCommand({ jobDirectory: job, worker, codeRoots: [] });
  const result = await runPreviewProcess({ launch, jobDirectory: job, rootDirectory: job, limits: previewLimits({ deadlineMs: 5000 }), signal: new AbortController().signal });
  assert.equal(result.code, 0);
  assert.deepEqual(JSON.parse(await fs.readFile(path.join(job, 'result.json'), 'utf8')), [true, true, true]);
});

test('private input snapshot cannot be rewritten, chmodded or removed by its worker', { skip: !['darwin', 'linux'].includes(process.platform) }, async t => {
  const job = await fs.realpath(await fs.mkdtemp(path.join(os.tmpdir(), 'sparkx-input-readonly-')));
  t.after(() => fs.rm(job, { recursive: true, force: true }));
  const input = path.join(job, 'input.csv'), script = path.join(job, 'worker.mjs');
  await fs.writeFile(input, 'original', { mode: 0o400 });
  await fs.writeFile(script, `import fs from 'node:fs/promises';const input=${JSON.stringify(input)};const denied=[];for(const op of [()=>fs.chmod(input,0o600),()=>fs.writeFile(input,'changed'),()=>fs.unlink(input)]){try{await op();denied.push(false)}catch(e){denied.push(['EPERM','EACCES','EROFS','EBUSY'].includes(e.code))}}await fs.writeFile('result.json',JSON.stringify(denied));`);
  const launch = await sandboxCommand({ jobDirectory: job, worker: script, codeRoots: [], readOnlyFiles: [input] });
  await new Promise((resolve, reject) => {
    const child = spawn(launch.command, launch.args, launch.options); const timeout = setTimeout(() => child.kill('SIGKILL'), 3000);
    child.on('error', reject); child.on('close', code => { clearTimeout(timeout); code === 0 ? resolve() : reject(new Error(`Probe exit ${code}`)); });
  });
  assert.deepEqual(JSON.parse(await fs.readFile(path.join(job, 'result.json'), 'utf8')), [true, true, true]);
  assert.equal(await fs.readFile(input, 'utf8'), 'original');
});

test('process memory and temporary-disk monitoring terminate the isolated task', { skip: !['darwin', 'linux'].includes(process.platform) }, async t => {
  for (const kind of ['memory', 'disk']) {
    const job = await fs.realpath(await fs.mkdtemp(path.join(os.tmpdir(), 'sparkx-resource-limit-')));
    t.after(() => fs.rm(job, { recursive: true, force: true }));
    const script = path.join(job, 'worker.mjs');
    await fs.writeFile(script, kind === 'memory' ? `globalThis.bytes=Buffer.alloc(64*1024*1024,1);setInterval(()=>{},1000);` : `import fs from 'node:fs';fs.writeFileSync('growth.bin',Buffer.alloc(2*1024*1024));setInterval(()=>{},1000);`);
    const launch = await sandboxCommand({ jobDirectory: job, worker: script, codeRoots: [] });
    const limits = previewLimits({ monitorMs: 50, killGraceMs: 50, deadlineMs: 5000, ...(kind === 'memory' ? { memoryBytes: 16 * 1024 * 1024 } : { taskDiskBytes: 1024 * 1024 }) });
    const started = Date.now();
    await assert.rejects(runPreviewProcess({ launch, jobDirectory: job, rootDirectory: job, limits, signal: new AbortController().signal }), { code: 'limit_exceeded' });
    assert.ok(Date.now() - started < 3000);
  }
});

test('cancellation joins both worker and its sleeping descendant before returning', { skip: !['darwin', 'linux'].includes(process.platform) }, async t => {
  const base = await fs.mkdtemp(path.join(os.tmpdir(), 'sparkx-preview-kill-'));
  t.after(() => fs.rm(base, { recursive: true, force: true }));
  const script = path.join(base, 'worker.mjs');
  await fs.writeFile(script, `import {spawn} from 'node:child_process';import fs from 'node:fs';const child=spawn(process.execPath,['-e','setInterval(()=>{},1000)'],{stdio:'ignore'});fs.writeFileSync('pids.json',JSON.stringify([process.pid,child.pid]));setInterval(()=>{},1000);`);
  const launch = await sandboxCommand({ jobDirectory: base, worker: script, codeRoots: [] });
  const controller = new AbortController();
  const pending = runPreviewProcess({ launch, jobDirectory: base, rootDirectory: base, limits: previewLimits({ monitorMs: 50, killGraceMs: 50, deadlineMs: 5000 }), signal: controller.signal });
  // Install rejection handling before cancelling.
  const rejected = assert.rejects(pending, { code: 'cancelled' });
  let pids;
  for (let attempt = 0; attempt < 100; attempt++) { try { pids = JSON.parse(await fs.readFile(path.join(base, 'pids.json'), 'utf8')); break; } catch { await new Promise(resolve => setTimeout(resolve, 20)); } }
  assert.equal(pids?.length, 2);
  if (process.platform === 'linux') {
    // Worker-written PIDs are in Bubblewrap's PID namespace. Observe the actual
    // host process tree before cancellation instead of accidentally probing PID 1.
    const { stdout } = await promisify(execFile)('/bin/ps', ['-axo', 'pid=,args='], { timeout: 1000, maxBuffer: 2 * 1024 * 1024 });
    const roots = stdout.split('\n').filter(line => line.includes(script)).map(line => Number(line.trim().split(/\s+/)[0]));
    assert.ok(roots.length >= 2, 'the sandbox supervisor and worker must be running');
    pids = [...new Set((await Promise.all(roots.map(pid => processTree(pid)))).flatMap(tree => tree.pids))];
    assert.ok(pids.length >= 3, 'the actual sandbox worker descendant must be observed');
  }
  controller.abort(Object.assign(new Error('cancelled'), { code: 'cancelled' }));
  await rejected;
  for (const pid of pids) assert.throws(() => process.kill(pid, 0), { code: 'ESRCH' });
});

for (const mode of ['crash', 'blocked']) test(`owner ${mode} cannot leave an unbounded isolated worker or descendant`, { skip: process.platform !== 'darwin', timeout: 10_000 }, async t => {
  const base = await fs.realpath(await fs.mkdtemp(path.join(os.tmpdir(), 'sparkx-preview-owner-crash-')));
  const script = path.join(base, 'worker.mjs'), ownerScript = path.join(base, 'owner.mjs');
  let owner, pids = [];
  t.after(async () => {
    owner?.kill('SIGCONT'); owner?.kill('SIGKILL');
    for (const pid of pids) { try { process.kill(pid, 'SIGKILL'); } catch {} }
    await fs.rm(base, { recursive: true, force: true });
  });
  await fs.writeFile(script, `import {spawn} from 'node:child_process';import fs from 'node:fs';const child=spawn(process.execPath,['-e','setInterval(()=>{},1000)'],{stdio:'ignore'});fs.writeFileSync('pids.json',JSON.stringify([process.pid,child.pid]));setInterval(()=>{},1000);`);
  await fs.writeFile(ownerScript, `import {sandboxCommand} from ${JSON.stringify(new URL('../src/main/local-preview-sandbox.mjs', import.meta.url).href)};import {runPreviewProcess} from ${JSON.stringify(new URL('../src/main/local-preview-process.mjs', import.meta.url).href)};import {previewLimits} from ${JSON.stringify(new URL('../src/main/local-preview-limits.mjs', import.meta.url).href)};const launch=await sandboxCommand({jobDirectory:${JSON.stringify(base)},worker:${JSON.stringify(script)},codeRoots:[]});await runPreviewProcess({launch,jobDirectory:${JSON.stringify(base)},rootDirectory:${JSON.stringify(base)},limits:previewLimits({deadlineMs:${mode === 'crash' ? 8000 : 500},monitorMs:50,cleanupMs:100,killGraceMs:50}),signal:new AbortController().signal});`);
  owner = spawn(process.execPath, [ownerScript], { stdio: 'ignore' });
  for (let n = 0; n < 100; n++) { try { pids = JSON.parse(await fs.readFile(path.join(base, 'pids.json'), 'utf8')); break; } catch { await new Promise(resolve => setTimeout(resolve, 20)); } }
  assert.equal(pids.length, 2, 'owned worker and descendant did not start');
  const exited = new Promise(resolve => owner.once('exit', resolve));
  owner.kill(mode === 'crash' ? 'SIGKILL' : 'SIGSTOP'); if (mode === 'crash') await exited;
  const alive = pid => { try { process.kill(pid, 0); return true; } catch (e) { if (e.code === 'ESRCH') return false; throw e; } };
  const started = Date.now();
  while (pids.some(alive) && Date.now() - started < 3000) await new Promise(resolve => setTimeout(resolve, 20));
  assert.deepEqual(pids.filter(alive), [], `preview processes survived ${mode} owner`);
  t.diagnostic(`Owner ${mode} process-tree exit: ${Date.now() - started}ms`);
});
