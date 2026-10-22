// Resident page transport: one process per task page executes the same pinned
// CLI commands (and guarded read batches) that HostPage otherwise launches as
// one playwright-cli.js process each. Every command still runs in the existing
// task-owned CLI daemon; this file never starts a browser or daemon.
//
// Protocol (newline-delimited JSON): after proving daemon ownership the child
// writes {"ready":true}. Each request {"id","args"} is one CLI invocation and is
// answered with the exit code, stdout and stderr that playwright-cli.js would
// have produced; {"id","batch"} runs cli-read-batch's guarded read and answers
// with its envelope. Requests run strictly one at a time. Deadlines, abort and
// output classification belong to the parent, which kills this process.
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import {ControllerError} from './errors.mjs';
import {guardedReadBatch, openPinnedSession} from './cli-read-batch.mjs';
import {MAX_CLI_OUTPUT_BYTES, clientContractError, clientUnavailableError} from './cli-runtime.mjs';

export const MAX_TRANSPORT_REQUEST_BYTES = 512 << 10;
// Commands HostPage issues through `run`; lifecycle commands (attach, close,
// detach, list, ...) keep their own processes and are refused here.
export const TRANSPORT_COMMANDS = new Set(['click', 'eval', 'fill', 'goto', 'press', 'run-code', 'tab-close', 'tab-list', 'tab-select']);
const GLOBAL_OPTIONS = ['json', 'raw', 'session'];
const BOOLEAN_OPTIONS = ['all', 'g', 'help', 'json', 'raw', 'version'];

class Capture {
  constructor() {this.stdout = ''; this.stderr = '';}
  write(stream, values) {
    const text = values.map(value => typeof value === 'string' ? value : String(value)).join(' ') + '\n';
    if (stream === 'stdout') this.stdout += text; else this.stderr += text;
  }
}

async function serve(sessionName) {
  const pinned = await openPinnedSession(sessionName, path.dirname(process.cwd()));
  const require = pinned.require;
  const help = require('../node_modules/playwright-core/lib/tools/cli-client/help.json');
  const {minimist} = require('../node_modules/playwright-core/lib/tools/cli-client/minimist.js');
  const {TextOutput} = require('../node_modules/playwright-core/lib/tools/cli-client/output.js');
  const {resolveSessionName} = require('../node_modules/playwright-core/lib/tools/cli-client/registry.js');
  const fs = await import('node:fs/promises');
  const token = process.env.PLAYWRIGHT_MCP_EXTENSION_TOKEN;
  let capture = null;
  for (const [method, stream] of [['log', 'stdout'], ['info', 'stdout'], ['debug', 'stdout'], ['error', 'stderr'], ['warn', 'stderr']]) {
    console[method] = (...values) => capture?.write(stream, values);
  }

  // Mirrors playwright-cli's program(): same parser, flag/argument validation,
  // messages and exit codes, then Session.run against the pinned daemon.
  async function command(argv) {
    if (!Array.isArray(argv) || !argv.every(value => typeof value === 'string')) throw clientContractError();
    const args = minimist(argv, {boolean: [...help.booleanOptions, ...BOOLEAN_OPTIONS], string: ['_']});
    if (args.s) {args.session = args.s; delete args.s;}
    if (args.g) {args.global = true; delete args.g;}
    const name = args._?.[0];
    if (args.json || args.version || args.v || args.help || args.h || !name || !TRANSPORT_COMMANDS.has(name) ||
        resolveSessionName(args.session) !== sessionName) throw clientContractError();
    const definition = help.commands[name];
    const output = new TextOutput();
    const unknown = Object.keys(args).filter(key => key !== '_' && !GLOBAL_OPTIONS.includes(key) && !(key in definition.flags));
    if (unknown.length) {
      return {code: 1, stdout: '', stderr: `Unknown option${unknown.length > 1 ? 's' : ''}: ${unknown.map(flag => `--${flag}`).join(', ')}\n\n${definition.help}\n`};
    }
    const positional = args._.slice(1);
    if (positional.length > definition.args.length && !definition.variadicArg) {
      return {code: 1, stdout: '', stderr: `error: too many arguments: expected ${definition.args.length}, received ${positional.length}\n\n${definition.help}\n`};
    }
    try {await fs.lstat(pinned.entry.file);}
    catch {
      return {code: 1, stderr: '', stdout: `The browser '${sessionName}' is not open, please run open first\n\n  playwright-cli -s=${sessionName} open [params]\n`};
    }
    if (definition.raw) args.raw = true;
    const raw = Boolean(args.raw);
    for (const option of GLOBAL_OPTIONS) delete args[option];
    let result;
    try {result = await pinned.run(args, {raw, json: output.json});}
    catch (error) {
      if (error instanceof ControllerError && error.diagnosticReason === 'output_overflow') return {overflow: true};
      // The bounded session reports socket failures without text; the CLI
      // reports the same condition as a closed browser session.
      const text = error instanceof ControllerError && !error.diagnosticReason
        ? `Error: Browser '${sessionName}' is not open. Run\n\n  playwright-cli -s=${sessionName} open\n\nto start the browser session.`
        : String(error?.stack ?? error);
      return {code: 1, stdout: '', stderr: `${text}\n`};
    }
    if (typeof result?.text !== 'string') throw clientContractError();
    return {code: result.isError ? 1 : 0, stdout: `${result.text}\n`, stderr: ''};
  }

  async function batch(request) {
    if (!request || request.sessionName !== sessionName) throw clientContractError();
    try {
      const output = await guardedReadBatch(request, (args, ms) => pinned.run(args, {timeoutMS: ms}), token);
      if (capture.stderr) throw clientContractError();
      return {envelope: {output}};
    } catch (error) {
      const safe = error instanceof ControllerError ? error : clientUnavailableError();
      return {envelope: {error: {code: safe.code, reason: safe.diagnosticReason}}};
    }
  }

  let buffer = Buffer.alloc(0), chain = Promise.resolve();
  const handle = async line => {
    let request;
    try {request = JSON.parse(line);} catch {process.exit(2);}
    if (!request || !Number.isSafeInteger(request.id) || (request.args === undefined) === (request.batch === undefined)) process.exit(2);
    capture = new Capture();
    let response;
    try {response = request.batch ? await batch(request.batch) : await command(request.args);}
    catch (error) {
      const safe = error instanceof ControllerError ? error : clientContractError();
      response = {error: {code: safe.code, reason: safe.diagnosticReason ?? 'transport_contract'}};
    }
    if (!request.batch && response.code !== undefined) {
      response.stdout = capture.stdout + response.stdout;
      response.stderr = capture.stderr + response.stderr;
      if (Buffer.byteLength(response.stdout) > MAX_CLI_OUTPUT_BYTES || Buffer.byteLength(response.stderr) > MAX_CLI_OUTPUT_BYTES) response = {overflow: true};
    }
    capture = null;
    process.stdout.write(`${JSON.stringify({id: request.id, ...response})}\n`);
  };
  process.stdin.on('data', chunk => {
    buffer = Buffer.concat([buffer, chunk]);
    for (;;) {
      const at = buffer.indexOf(10);
      if (at < 0) break;
      const line = buffer.subarray(0, at).toString('utf8');
      buffer = buffer.subarray(at + 1);
      chain = chain.then(() => handle(line));
    }
    if (buffer.length > MAX_TRANSPORT_REQUEST_BYTES) process.exit(2);
  });
  process.stdin.once('end', () => {chain.then(() => process.exit(0));});
  process.stdout.write('{"ready":true}\n');
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const args = process.argv.slice(2);
  const fail = error => {
    const safe = error instanceof ControllerError ? error : clientUnavailableError();
    process.stdout.write(`${JSON.stringify({error: {code: safe.code, reason: safe.diagnosticReason ?? 'transport_startup'}})}\n`);
    process.exit(1);
  };
  console.error = () => {};
  if (args.length !== 2 || args[0] !== '--serve' || !/^sc-cli-[a-f0-9]{20}$/.test(args[1])) fail(clientContractError());
  else serve(args[1]).catch(fail);
}
