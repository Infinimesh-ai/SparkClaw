import crypto from 'node:crypto';
import fs from 'node:fs/promises';
import path from 'node:path';
import net from 'node:net';
import {fileURLToPath} from 'node:url';
import {HostPage} from './host-page.mjs';
import {createInvocationState, prepareRuntimeRoot, clientContractError} from './cli-runtime.mjs';
import {registerElectronConnection} from './electron-adapter-client.mjs';
import {listenOwnerOnlyUnixSocket} from './unix-socket.mjs';

// Resource ownership only: application selectors, origin sets, hooks, budgets
// and preparation assets arrive exclusively from the verified release binding.
export class ApplicationHostDriver {
  constructor(options) {Object.assign(this, options); this.handles = new Set(); this.connections = new Set();}
  async prepare() {
    await prepareRuntimeRoot(this.runtimeRoot);
    this.eventSocket = path.join(this.runtimeRoot, `events-${process.pid}.sock`);
    this.events = net.createServer(socket => {
      if (this.connections.size >= 64) {socket.destroy(); return;}
      this.connections.add(socket); socket.on('close', () => this.connections.delete(socket)); socket.on('error', () => {});
      let buffer = Buffer.alloc(0);
      socket.on('data', chunk => {
        buffer = Buffer.concat([buffer, chunk]);
        if (buffer.length > 65536) {socket.destroy(); return;}
        for (;;) {
          const at = buffer.indexOf(10); if (at < 0) break;
          const line = buffer.subarray(0, at); buffer = buffer.subarray(at + 1);
          if (line.length > 16384) {socket.destroy(); return;}
          try {
            const value = JSON.parse(line);
            const handle = [...this.handles].find(item => item.eventKey === value.key);
            if (!handle || handle.closed || !value.value) continue;
            handle.sequence++;
            handle.events.push({sequence: handle.sequence, value: value.value, dropped: value.dropped ?? 0});
            while (handle.events.length > 128) handle.events.shift();
          } catch {socket.destroy(); return;}
        }
      });
    });
    await listenOwnerOnlyUnixSocket(this.events, this.eventSocket);
  }
  async create({task, epoch, generation, spec, resource, grant}) {
    const sessionID = `session_${crypto.randomBytes(16).toString('hex')}`;
    const state = await createInvocationState(this.runtimeRoot, sessionID, {}, 'host');
    const handle = {state, page: null, events: [], sequence: 0, eventKey: crypto.randomBytes(32).toString('hex'),
      activities: new Map(), epoch, generation, closed: false, resource};
    this.handles.add(handle);
    let phase = 'reserve';
    try {
      handle.bootstrap = await this.controller.reserveApplication({taskID: task, resource: spec.resource,
        exclusive: spec.host.activity === 'exclusive'});
      if (spec.host.activity === 'exclusive') await this.drainIdle?.();
      const registration = {...spec.host.page};
      registration.beforeNavigationSources = await Promise.all((registration.beforeNavigationAssets ?? []).map(async relative => {
        const absolute = path.resolve(this.releaseRoot, relative);
        if (!absolute.startsWith(this.releaseRoot + path.sep)) throw clientContractError();
        return fs.readFile(absolute, 'utf8');
      }));
      const leaseFile = path.join(state.directory, 'lease.json');
      // The bootstrap activity is bounded by the actual grant in the port.
      // It is replaced with the admitted activity before application actions.
      await fs.writeFile(leaseFile, JSON.stringify({epoch, generation,
        activities: [{id: 'bootstrap', kind: 'read', expires_ms: Math.min(Date.now() + 30000, grant.execution_expires_ms)}]}), {mode: 0o600});
      let hookConfig;
      if (registration.hook) {
        const module = path.resolve(this.releaseRoot, registration.hook.module);
        if (!module.startsWith(this.releaseRoot + path.sep)) throw clientContractError();
        hookConfig = path.join(state.directory, 'hook.json');
        await fs.writeFile(hookConfig, JSON.stringify({...registration.hook, module, key: handle.eventKey, socket: this.eventSocket,
          account: resource.account_address, shared: true, leaseFile}), {mode: 0o600});
      }
      const electronConnection = this.electronAdapter ? await registerElectronConnection({adapter: this.electronAdapter, token: resource.token,
        binding: {task_id: task, session_id: sessionID, controller_generation: this.controller.controllerGeneration,
          session_generation: epoch, page_generation: 1}}) : null;
      handle.page = new HostPage({...this, registration, state, token: resource.token, leaseFile, hookConfig, electronConnection,
        signal: handle.bootstrap.abortController.signal, batchReadCommands: true});
      phase = 'attach'; await handle.page.attach(); phase = 'create_page'; await handle.page.createTaskPage();
      return handle;
    } catch (error) {
      this.diagnostic?.({event: 'application_host_failed', phase, code: error.code,
        reason: error.diagnosticReason, command: error.diagnosticCommand});
      try {await this.close(handle);} catch (cleanupError) {cleanupError.resourceHandle = handle; throw cleanupError;}
      throw error;
    }
  }
  async beginActivity(handle, {id, kind, spec, resource, signal, task}) {
    const reservation = handle.bootstrap ?? await this.controller.reserveApplication({taskID: task, resource: spec.resource, exclusive: kind === 'exclusive', signal});
    handle.bootstrap = null;
    handle.activities.set(id, {reservation, kind, spec, resource});
  }
  async updateLease(handle, stamp) {
    if (!handle || handle.closed) return;
    const file = path.join(handle.state.directory, 'lease.json'), temporary = file + '.' + crypto.randomUUID();
    await fs.writeFile(temporary, JSON.stringify(stamp), {mode: 0o600}); await fs.rename(temporary, file);
  }
  async call(handle, method, args, {signal, activity, spec}) {
    if (handle.closed) throw clientContractError();
    const current = handle.activities.get(activity);
    if (!current) throw clientContractError();
    if (method === 'hookEvents') {
      const cursor = args[0] ?? 0;
      if (!Number.isSafeInteger(cursor) || cursor < 0) throw clientContractError();
      const events = handle.events.filter(event => event.sequence > cursor).slice(0, 32);
      return {events, cursor: events.at(-1)?.sequence ?? cursor,
        gap: cursor > handle.sequence || Boolean(handle.events[0] && cursor < handle.events[0].sequence - 1)};
    }
    if (method === 'setSecrets') {
      const secrets = args[0];
      if (!secrets || Object.keys(secrets).length > 104 || Object.entries(secrets).some(([key, value]) =>
        !/^[A-Z][A-Z0-9_]{0,63}$/u.test(key) || typeof value !== 'string' || Buffer.byteLength(value) > 204800)) throw clientContractError();
      handle.state.secretsPath = path.join(handle.state.directory, 'secrets.json');
      await fs.writeFile(handle.state.secretsPath, JSON.stringify({secrets}), {mode: 0o600});
      handle.state.secretValues = Object.values(secrets).filter(Boolean);
      handle.state.secretName = value => {
        if (!value) return '';
        const entry = Object.entries(secrets).find(([, candidate]) => candidate === value);
        if (!entry) throw clientContractError(); return entry[0];
      };
      return true;
    }
    const page = handle.page;
    page.registration = {...page.registration, ...spec.host.page};
    page.signal = current.reservation ? AbortSignal.any([signal, current.reservation.abortController.signal]) : signal; page.deadline = Date.now() + Math.min(spec.timeout_ms, 300000);
    if (method === 'download') {
      const destination = path.resolve(args[1]);
      const root = path.resolve(handle.resource.workspace_root ?? '');
      if (!path.isAbsolute(handle.resource.workspace_root ?? '') || !destination.startsWith(root + path.sep)) throw clientContractError();
    }
    let result;
    try {result = await page[method](...args.map(value => value === null ? undefined : value));}
    catch (error) {
      this.diagnostic?.({event: 'application_host_failed', phase: method, code: error.code,
        reason: error.diagnosticReason, command: error.diagnosticCommand}); throw error;
    }
    if (method === 'hookActivate' && current.kind === 'watch' && current.reservation) {
      this.controller.finishApplication(current.reservation); current.reservation = null;
    }
    return result;
  }
  async revokeActivity(handle, id) {
    const activity = handle?.activities.get(id);
    if (activity?.reservation) this.controller.finishApplication(activity.reservation);
    handle?.activities.delete(id);
  }
  async serializeResult(handle, result) {
    const bytes = Buffer.from(JSON.stringify(result ?? null));
    if (bytes.length <= 256 * 1024) return {result: result ?? null};
    if (bytes.length > 16 * 1024 * 1024) throw clientContractError('output_overflow');
    const file = path.join(handle.state.outputDir, `transfer-${crypto.randomUUID()}.json`);
    await fs.writeFile(file, bytes, {flag: 'wx', mode: 0o600});
    return {artifact: {path: file, bytes: bytes.length, sha256: crypto.createHash('sha256').update(bytes).digest('hex')}};
  }
  async close(handle) {
    if (!handle || handle.closed) return;
    let failure;
    const diagnose = (phase, error) => this.diagnostic?.({event: 'application_host_cleanup', phase,
      code: error.code, reason: error.diagnosticReason, command: error.diagnosticCommand});
    try {await handle.page?.closeTaskPage();} catch (error) {diagnose('close_page', error); failure = error;}
    try {await handle.page?.stop();} catch (error) {diagnose('stop_cli', error); failure ??= error;}
    // Reaping is the final owned-process fence, even if the page call failed.
    try {await handle.state.reapDaemon();} catch (error) {diagnose('reap', error); throw error;}
    await handle.state.remove();
    if (handle.bootstrap) {this.controller.finishApplication(handle.bootstrap); handle.bootstrap = null;}
    for (const id of handle.activities.keys()) await this.revokeActivity(handle, id);
    handle.closed = true; this.handles.delete(handle);
    if (failure) throw failure;
  }
  async shutdown() {
    await Promise.all([...this.handles].map(handle => this.close(handle)));
    for (const socket of this.connections) socket.destroy();
    if (this.events) await new Promise(resolve => this.events.close(resolve));
    if (this.eventSocket) await fs.rm(this.eventSocket, {force: true});
  }
}
