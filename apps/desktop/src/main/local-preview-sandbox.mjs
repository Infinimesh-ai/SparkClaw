import { spawn } from 'node:child_process';
import { realpath, access, writeFile, readFile, rm } from 'node:fs/promises';
import path from 'node:path';
import { homedir } from 'node:os';
import { fileURLToPath } from 'node:url';
import { createRequire } from 'node:module';
import { previewError } from './local-preview-limits.mjs';

const require = createRequire(import.meta.url);
export const PREVIEW_ENGINE_VERSION = '0.2.3';
const unpacked = value => value.replace(/\.asar([/\\])/, '.asar.unpacked$1');
const sourceDirectory = unpacked(path.dirname(fileURLToPath(import.meta.url)));
const quote = value => JSON.stringify(value);

export function systemFontDirectories(platform = process.platform) {
  return platform === 'darwin' ? ['/System/Library/Fonts', '/Library/Fonts'] : ['/usr/share/fonts', '/usr/local/share/fonts'];
}

async function dependencyRoots() {
  const roots = new Set();
  for (const name of ['exceljs', 'papaparse', 'yauzl', '@deepseek-ai/libreoffice-kit']) {
    let resolved;
    try { resolved = await realpath(unpacked(require.resolve(name))); }
    catch { throw previewError('preview_unavailable', `The bundled preview dependency ${name} is missing.`); }
    // The package and its transitive modules are application code, never user content.
    const marker = `${path.sep}node_modules${path.sep}`;
    const at = resolved.lastIndexOf(marker);
    if (at < 0) throw previewError('preview_unavailable', 'The preview runtime is not in a recognized package directory.');
    roots.add(resolved.slice(0, at + marker.length - 1));
  }
  return [...roots];
}

export function macSandboxProfile({ runtime, codeRoots, jobDirectory, fontDirectories, readOnlyFiles = [] }) {
  const readRoots = [...new Set([
    ...codeRoots, jobDirectory, ...fontDirectories, '/System/Library', '/usr/lib',
    // A Node or Electron executable can require siblings (ICU, dylibs, Resources).
    path.dirname(runtime), ...(path.basename(path.dirname(runtime)) === 'MacOS' ? [path.resolve(runtime, '../../Frameworks')] : []),
  ])];
  return `(version 1)\n(deny default)\n(allow process*)\n(allow signal (target self))\n(allow sysctl-read)\n(allow mach-lookup (global-name "com.apple.system.logger") (global-name "com.apple.fonts"))\n(allow file-read-metadata)\n(allow file-read-data (literal "/"))\n${readRoots.map(root => `(allow file-read* (subpath ${quote(root)}))`).join('\n')}\n(allow file-read* (literal "/dev/null") (literal "/dev/urandom") (literal "/dev/random"))\n(allow file-write* (subpath ${quote(jobDirectory)}) (literal "/dev/null"))\n${readOnlyFiles.map(file => `(deny file-write* (literal ${quote(file)}))`).join('\n')}\n(deny network*)\n`;
}

export async function sandboxCommand({ jobDirectory, worker = path.join(sourceDirectory, 'local-preview-worker.mjs'),
  runtime = process.execPath, platform = process.platform, extraArguments = [], codeRoots, readOnlyFiles = [], fontDirectories = systemFontDirectories(platform) }) {
  runtime = await realpath(runtime);
  jobDirectory = await realpath(jobDirectory);
  worker = await realpath(unpacked(worker));
  readOnlyFiles = await Promise.all(readOnlyFiles.map(file => realpath(file)));
  const roots = [...new Set([await realpath(sourceDirectory), path.dirname(worker), ...(codeRoots ?? await dependencyRoots())])];
  // Node resolves package scopes above each ESM entry; admit only package.json,
  // not the surrounding checkout/profile directory.
  for (const root of [...roots]) {
    let parent = root;
    while (true) { roots.push(path.join(parent, 'package.json')); const next = path.dirname(parent); if (next === parent) break; parent = next; }
  }
  // Preserve only the truthful OS home locator; cache discovery is disabled and
  // sandbox permissions still deny every read there. No credentials are inherited.
  const environment = { HOME: homedir(), LANG: 'en_US.UTF-8', LC_ALL: 'en_US.UTF-8', TMPDIR: jobDirectory, TMP: jobDirectory, TEMP: jobDirectory,
    ELECTRON_RUN_AS_NODE: '1', NODE_NO_WARNINGS: '1' };
  const nodeArguments = ['--max-old-space-size=384', worker, ...extraArguments];
  if (platform === 'darwin') {
    try { await access('/usr/bin/sandbox-exec'); } catch { throw previewError('preview_unavailable', 'macOS preview isolation is unavailable.'); }
    const profile = path.join(jobDirectory, 'isolation.sb');
    await writeFile(profile, macSandboxProfile({ runtime, codeRoots: roots, jobDirectory, fontDirectories, readOnlyFiles }), { mode: 0o600 });
    return { command: '/usr/bin/sandbox-exec', args: ['-f', profile, runtime, ...nodeArguments], options: { cwd: jobDirectory, env: environment, detached: true, stdio: ['ignore', 'pipe', 'pipe'] } };
  }
  if (platform === 'linux') {
    const binary = ['/usr/bin/bwrap', '/bin/bwrap'];
    let command;
    for (const candidate of binary) { try { await access(candidate); command = candidate; break; } catch {} }
    if (!command) throw previewError('preview_unavailable', 'Install the packaged Bubblewrap isolation support to enable heavy previews.');
    const readRoots = [...new Set([...roots, ...fontDirectories, path.dirname(runtime), '/usr/lib', '/usr/lib64', '/lib', '/lib64', '/usr/share/icu'])];
    const args = ['--unshare-all', '--die-with-parent', '--new-session', '--proc', '/proc', '--dev', '/dev', '--tmpfs', '/tmp'];
    for (const root of readRoots) { try { await access(root); args.push('--ro-bind', root, root); } catch {} }
    args.push('--bind', jobDirectory, jobDirectory);
    for (const file of readOnlyFiles) args.push('--ro-bind', file, file);
    args.push('--chdir', jobDirectory, '--', runtime, ...nodeArguments);
    return { command, args, options: { cwd: jobDirectory, env: environment, detached: true, stdio: ['ignore', 'pipe', 'pipe'] } };
  }
  throw previewError('preview_unavailable', 'Heavy document previews are unavailable on this platform.');
}

// A positive conversion is not isolation evidence. This probe separately attempts
// a forbidden read, a forbidden write, and an IPv4 connection from the worker.
export async function verifyPreviewSandbox({ jobDirectory, outsidePath, runtime = process.execPath, codeRoots }) {
  const probe = path.join(jobDirectory, 'isolation-probe.mjs');
  const result = path.join(jobDirectory, 'isolation-result.json');
  await writeFile(probe, `import fs from 'node:fs/promises';import net from 'node:net';\nconst outside=${JSON.stringify(outsidePath)},result=${JSON.stringify(result)};\nlet readDenied=false,writeDenied=false,networkDenied=false;try{await fs.readFile(outside)}catch(e){readDenied=['EPERM','EACCES','ENOENT'].includes(e.code)}\ntry{await fs.writeFile(outside+'.write','forbidden')}catch(e){writeDenied=['EPERM','EACCES','ENOENT'].includes(e.code)}\nawait new Promise(resolve=>{const socket=net.connect({host:'127.0.0.1',port:9});socket.on('connect',()=>{socket.destroy();resolve()});socket.on('error',e=>{networkDenied=['EPERM','EACCES','ENETUNREACH'].includes(e.code);resolve()});socket.setTimeout(1000,()=>{socket.destroy();resolve()})});await fs.writeFile(result,JSON.stringify({readDenied,writeDenied,networkDenied}));`, { mode: 0o600 });
  const launch = await sandboxCommand({ jobDirectory, worker: probe, runtime, codeRoots });
  await new Promise((resolve, reject) => {
    const child = spawn(launch.command, launch.args, launch.options);
    let stderr = '';
    child.stderr.on('data', chunk => { stderr = (stderr + chunk.toString()).slice(0, 2048); });
    const timer = setTimeout(() => { try { process.kill(-child.pid, 'SIGKILL'); } catch {} }, 5000);
    child.on('error', error => { clearTimeout(timer); reject(error); });
    child.on('close', code => { clearTimeout(timer); code === 0 ? resolve() : reject(previewError('preview_unavailable', `Preview isolation probe failed (${code}): ${stderr}`)); });
  });
  const checks = JSON.parse(await readFile(result, 'utf8'));
  await Promise.all([rm(probe, { force: true }), rm(result, { force: true })]);
  if (!checks.readDenied || !checks.writeDenied || !checks.networkDenied) throw previewError('preview_unavailable', 'Preview isolation did not deny all forbidden operations.');
  return checks;
}
