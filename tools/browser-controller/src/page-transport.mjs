// Parent side of cli-page-transport.mjs: one resident helper per task page.
// The caller keeps every guard, retry and effect decision; this class only
// replaces "spawn playwright-cli.js, wait for exit" with one request to a
// process that is already connected to the same daemon. Deadline, abort and
// overflow end the helper exactly as they would end a per-command process.
import {fileURLToPath} from 'node:url';
import {MAX_CLI_OUTPUT_BYTES, clientContractError, clientTimeoutError, clientUnavailableError} from './cli-runtime.mjs';

export const PAGE_TRANSPORT_HELPER = fileURLToPath(new URL('./cli-page-transport.mjs', import.meta.url));
// A send issues its page calls back to back; a parked page releases the helper.
export const PAGE_TRANSPORT_IDLE_MS = 30_000;
const MAX_RESPONSE_LINE_BYTES = 16 * MAX_CLI_OUTPUT_BYTES;

// Raised only before a request was written: the command never reached the
// daemon, so the caller may run it through its ordinary per-command process.
export class PageTransportUnavailable extends Error {
  constructor(reason) {super('page transport unavailable'); this.reason = reason;}
}

export class PageTransport {
  constructor({spawn, cwd, env, sessionName, idleMS = PAGE_TRANSPORT_IDLE_MS, helper = PAGE_TRANSPORT_HELPER}) {
    Object.assign(this, {spawnImpl: spawn, cwd, env, sessionName, idleMS, helper});
    this.child = null;
    this.pending = null;
    this.nextID = 1;
    this.spawns = 0;
    this.requests = 0;
  }

  get busy() {return Boolean(this.pending || this.starting);}

  // Sends one request and resolves with the helper's response object.
  async request(message, {timeoutMS, signal}) {
    if (signal?.aborted) throw clientUnavailableError();
    if (this.busy) throw new PageTransportUnavailable('busy');
    const started = Date.now();
    if (!this.child) await this.#start(timeoutMS, signal);
    const remaining = Math.max(1, timeoutMS - (Date.now() - started));
    clearTimeout(this.idleTimer);
    const child = this.child, id = this.nextID++;
    this.requests += 1;
    return await new Promise((resolve, reject) => {
      const timer = setTimeout(() => fail(clientTimeoutError('timeout')), remaining);
      timer.unref?.();
      const abort = () => fail(clientUnavailableError());
      signal?.addEventListener('abort', abort, {once: true});
      const finish = () => {
        clearTimeout(timer);
        signal?.removeEventListener('abort', abort);
        this.pending = null;
      };
      // A command that may have reached the daemon is never retried: the
      // helper is discarded and the failure is reported like a killed CLI.
      const fail = error => {
        if (this.pending?.id !== id) return;
        finish();
        this.#discard(child);
        reject(error);
      };
      this.pending = {id, child, fail, resolve: response => {
        if (response.id !== id) return fail(clientContractError('transport_protocol'));
        finish();
        this.#armIdle();
        resolve(response);
      }};
      try {child.stdin.write(`${JSON.stringify({id, ...message})}\n`);}
      catch {fail(clientUnavailableError(undefined, 'process_exit'));}
    });
  }

  async close() {
    clearTimeout(this.idleTimer);
    const child = this.child;
    if (!child) return;
    this.child = null;
    this.pending?.fail(clientUnavailableError());
    await new Promise(resolve => {
      if (child.exitCode !== null || child.signalCode !== null) return resolve();
      const kill = setTimeout(() => child.kill('SIGKILL'), 1000);
      kill.unref?.();
      child.once('close', () => {clearTimeout(kill); resolve();});
      try {child.stdin.end();} catch {child.kill('SIGKILL');}
    });
  }

  #armIdle() {
    clearTimeout(this.idleTimer);
    this.idleTimer = setTimeout(() => {void this.close();}, this.idleMS);
    this.idleTimer.unref?.();
  }

  #discard(child) {
    if (this.child === child) this.child = null;
    child.kill('SIGKILL');
  }

  async #start(timeoutMS, signal) {
    this.starting = true;
    let child;
    try {
      child = this.spawnImpl(process.execPath, [this.helper, '--serve', this.sessionName], {
        cwd: this.cwd, env: this.env, stdio: ['pipe', 'pipe', 'pipe'], windowsHide: true,
      });
    } catch {
      this.starting = false;
      throw new PageTransportUnavailable('spawn_error');
    }
    this.spawns += 1;
    let ready;
    // Output of a discarded helper must never settle a later request.
    const current = () => this.pending?.child === child ? this.pending : null;
    const readiness = new Promise((resolve, reject) => {ready = {resolve, reject};});
    const onLine = line => {
      let value;
      try {value = JSON.parse(line);} catch {value = null;}
      if (ready) {
        const settle = ready; ready = null;
        if (value?.ready === true) settle.resolve();
        else settle.reject(new PageTransportUnavailable(value?.error?.reason ?? 'startup_protocol'));
      } else if (!value) current()?.fail(clientContractError('transport_protocol'));
      else if (current()) current().resolve(value);
      else child.kill('SIGKILL');
    };
    child.stderr?.resume();
    // Partial lines are joined once complete, so a large response costs
    // linear time and the bound below trips promptly.
    let partial = [], partialBytes = 0;
    child.stdout.on('data', chunk => {
      let start = 0;
      for (let at = chunk.indexOf(10); at >= 0; at = chunk.indexOf(10, start)) {
        partial.push(chunk.subarray(start, at));
        const line = Buffer.concat(partial).toString('utf8');
        partial = []; partialBytes = 0; start = at + 1;
        onLine(line);
      }
      if (start < chunk.length) {partial.push(chunk.subarray(start)); partialBytes += chunk.length - start;}
      if (partialBytes > MAX_RESPONSE_LINE_BYTES) {
        partial = []; partialBytes = 0;
        if (ready) ready.reject(new PageTransportUnavailable('startup_protocol'));
        current()?.fail(clientContractError('output_overflow'));
        child.kill('SIGKILL');
      }
    });
    child.once('error', () => {
      ready?.reject(new PageTransportUnavailable('spawn_error'));
      current()?.fail(clientUnavailableError(undefined, 'spawn_error'));
    });
    child.once('close', () => {
      if (this.child === child) this.child = null;
      ready?.reject(new PageTransportUnavailable('startup_exit'));
      current()?.fail(clientUnavailableError(undefined, 'process_exit'));
    });
    child.stdin.on('error', () => {});
    const timer = setTimeout(() => ready?.reject(new PageTransportUnavailable('startup_timeout')), Math.max(1, timeoutMS));
    timer.unref?.();
    const abort = () => ready?.reject(clientUnavailableError());
    signal?.addEventListener('abort', abort, {once: true});
    try {
      await readiness;
      this.child = child;
    } catch (error) {
      child.kill('SIGKILL');
      throw error;
    } finally {
      clearTimeout(timer);
      signal?.removeEventListener('abort', abort);
      this.starting = false;
    }
  }
}
