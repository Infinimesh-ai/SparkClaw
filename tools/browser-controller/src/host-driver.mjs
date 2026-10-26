import crypto from 'node:crypto';
import fs from 'node:fs/promises';
import path from 'node:path';
import net from 'node:net';
import {fileURLToPath} from 'node:url';
import {HostPage} from './host-page.mjs';
import {createInvocationState, prepareRuntimeRoot, clientContractError} from './cli-runtime.mjs';
import {nativeSocketPath, queryBridgeTaskStatus} from './bridge-native-protocol.mjs';
import {ControllerError} from './errors.mjs';
import {registerElectronConnection} from './electron-adapter-client.mjs';
import {listenOwnerOnlyUnixSocket} from './unix-socket.mjs';

const CLEANUP_FENCE = 'cleanup-fence.json';
const FENCED_SESSION = /^session-[0-9a-f]{24}$/u;
// Bounds how often a fenced admission may ask the Bridge for its task tabs.
const CLEANUP_PROOF_INTERVAL_MS = 15_000;
// A fenced Host rejects every admission; log the rejection, not each retry.
const FENCED_DIAGNOSTIC_INTERVAL_MS = 10 * 60_000;

// The pinned HostPort supplies its authenticated grant and ledger task UUID.
// Request keys may be legacy caller text: validate their protocol shape, then
// retain only a digest for joining the existing operation without exposing it.
function taskCorrelation(task, grant) {
  const result = {correlation: 'incomplete'};
  if (typeof task !== 'string' || !/^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/iu.test(task) ||
      (grant?.task_id != null && grant.task_id !== task)) return result;
  result.task_id = task;
  const key = grant?.request_key;
  if (typeof key !== 'string' || !/^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/u.test(key)) return result;
  result.operation_key_sha256 = crypto.createHash('sha256').update(key).digest('hex');
  result.correlation = 'complete';
  return result;
}

// Resource ownership only: application selectors, origin sets, hooks, budgets
// and preparation assets arrive exclusively from the verified release binding.
// The verified binding names the scheduler resource and its sharing. Only
// whole-profile work (`exclusive`) waits for every provider and drains idle
// Reader pages. A mail `mutation` (send, mark-read) owns its task page in its
// provider's mutation family: it overlaps inbound pages and other providers.
export function reservationPolicy(spec, activity) {
  return {resource: spec.host.family ?? spec.resource, exclusive: activity === 'exclusive'};
}

// Which task pages run their CLI commands through one resident transport
// process (cli-page-transport.mjs) instead of one playwright-cli.js process per
// command. `mutation` (default) covers send and mark-read pages; `all` adds
// Reader/watch pages; `off` restores per-command processes everywhere.
export function pageTransportEnabled(activity, setting = process.env.SPARKCLAW_BROWSER_PAGE_TRANSPORT) {
  const mode = (setting ?? '').trim() || 'mutation';
  if (mode === 'off') return false;
  if (mode === 'all') return true;
  return activity === 'mutation';
}

export class ApplicationHostDriver {
  constructor(options) {Object.assign(this, options); this.handles = new Set(); this.connections = new Set();}
  async prepare() {
    // A page-scope fence is proved or retained at admission, not at startup:
    // the Bridge may connect after the Controller does.
    await this.assertCleanupClear({startup: true});
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
  async assertCleanupClear({startup = false} = {}) {
    const fence = await this.#readCleanupFence();
    if (!fence) return;
    if (fence.scope === 'page') {
      const outcome = await this.#proveCleanup(fence);
      if (outcome === 'cleared' || startup) return;
      throw this.#fencedError(fence, outcome);
    }
    // Owned processes were not proved gone, or a fence predates scopes:
    // only an operator can establish that nothing of this Host still runs.
    throw this.#fencedError(fence, 'operator_required');
  }
  async #readCleanupFence() {
    let raw;
    try {raw = await fs.readFile(path.join(this.runtimeRoot, CLEANUP_FENCE), 'utf8');}
    catch (error) {if (error.code === 'ENOENT') return null; throw error;}
    try {
      const value = JSON.parse(raw);
      const sessions = Array.isArray(value?.sessions) ? value.sessions.filter(name => FENCED_SESSION.test(name)) : [];
      return {raw, scope: value?.scope === 'page' ? 'page' : 'process', sessions};
    } catch {return {raw, scope: 'process', sessions: []};}
  }
  // A page-scope fence records that every owned process was reaped but the
  // task page closure itself was unproven (typically a page already closed
  // under the CLI). Healthy receive resources may be retired first; proof
  // still requires no Host resource and no task-owned/stale Bridge tab.
  #proveCleanup(fence) {
    // Concurrent admissions share one proof; it is dropped once settled.
    if (!this.cleanupProof) {
      const proof = this.#attemptCleanupProof(fence);
      this.cleanupProof = proof;
      proof.finally(() => {if (this.cleanupProof === proof) this.cleanupProof = null;}).catch(() => {});
    }
    return this.cleanupProof;
  }
  async #attemptCleanupProof(fence) {
    if (Date.now() - (this.lastCleanupProofMS ?? 0) < CLEANUP_PROOF_INTERVAL_MS) return 'proof_deferred';
    this.lastCleanupProofMS = Date.now();
    if (this.handles.size) {
      const handles = [...this.handles];
      // A count of resident pages cannot identify a failed write page. Retire
      // only known read/watch resources, then use the original zero-tab proof.
      if (!this.drainCleanupReads || handles.some(handle => handle.closed || handle.cleanupFailure || handle.bootstrap ||
          !['read', 'watch'].includes(handle.activityKind) ||
          [...handle.activities.values()].some(activity => !['read', 'watch'].includes(activity.kind)))) return 'resources_open';
      try {await this.drainCleanupReads(handles);} catch {return 'resources_open';}
      if (this.handles.size || (await this.#readCleanupFence())?.raw !== fence.raw) return 'resources_open';
    }
    const windows = await (this.bridgeTaskStatus ?? defaultBridgeTaskStatus)();
    if (!windows) return 'bridge_unavailable';
    if (windows.some(window => window.task_tabs || window.stale_task_tabs)) return 'task_tabs_present';
    // A resource admitted before the fence may have failed while the Bridge
    // answered; its newer fence must not be cleared by this proof.
    if (this.handles.size || (await this.#readCleanupFence())?.raw !== fence.raw) return 'resources_open';
    await this.#clearCleanupFence(fence, windows.length);
    return 'cleared';
  }
  async #clearCleanupFence(fence, windows) {
    for (const name of fence.sessions) {
      const directory = path.join(this.runtimeRoot, name);
      if (![...this.handles].some(handle => handle.state?.directory === directory)) await fs.rm(directory, {recursive: true, force: true});
    }
    await fs.rm(path.join(this.runtimeRoot, CLEANUP_FENCE), {force: true});
    const directory = await fs.open(this.runtimeRoot, 'r');
    try {await directory.sync();} finally {await directory.close();}
    this.lastFencedDiagnosticMS = 0;
    this.diagnostic?.({event: 'application_cleanup_fence_cleared', scope: fence.scope, sessions: fence.sessions.length, windows});
  }
  #fencedError(fence, outcome) {
    if (Date.now() - (this.lastFencedDiagnosticMS ?? 0) >= FENCED_DIAGNOSTIC_INTERVAL_MS) {
      this.lastFencedDiagnosticMS = Date.now();
      this.diagnostic?.({event: 'application_cleanup_fenced', scope: fence.scope, reason: outcome});
    }
    return new ControllerError('browser_cleanup_pending', 'browser task page cleanup is not yet proven', {
      status: 503, retryable: true, diagnosticReason: 'application_cleanup_fenced',
    });
  }
  async fenceCleanup(handle, error, scope = 'process') {
    handle.cleanupFailure = error;
    const existing = await this.#readCleanupFence();
    const sessions = new Set(existing?.sessions ?? []);
    const name = handle.state?.directory && path.basename(handle.state.directory);
    if (name && FENCED_SESSION.test(name)) sessions.add(name);
    // An unproven process fence is never narrowed by a later page failure.
    const recorded = existing?.scope === 'process' ? 'process' : scope;
    const file = path.join(this.runtimeRoot, CLEANUP_FENCE);
    const temporary = file + '.' + crypto.randomUUID();
    const fd = await fs.open(temporary, 'wx', 0o600);
    try {
      await fd.writeFile(JSON.stringify({schema_version: 1, reason: 'owned_resource_cleanup_unproven', scope: recorded,
        sessions: [...sessions], fenced_ms: Date.now()}));
      await fd.sync();
    }
    finally {await fd.close();}
    await fs.rename(temporary, file);
    const directory = await fs.open(this.runtimeRoot, 'r');
    try {await directory.sync();} finally {await directory.close();}
  }
  async create({task, epoch, generation, spec, resource, grant}) {
    await this.assertCleanupClear();
    const sessionID = `session_${crypto.randomBytes(16).toString('hex')}`;
    const state = await createInvocationState(this.runtimeRoot, sessionID, {}, 'host');
    const handle = {state, page: null, events: [], sequence: 0, eventKey: crypto.randomBytes(32).toString('hex'),
      activities: new Map(), epoch, generation, closed: false, resource, activityKind: spec.host.activity ?? 'exclusive'};
    this.handles.add(handle);
    let phase = 'reserve';
    try {
      const policy = reservationPolicy(spec, spec.host.activity);
      handle.bootstrap = await this.controller.reserveApplication({taskID: task, ...policy,
        waitMS: Math.max(0, Math.min(30000, spec.timeout_ms ?? 30000, grant.execution_expires_ms - Date.now()))});
      if (policy.exclusive) await this.drainIdle?.();
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
        signal: handle.bootstrap.abortController.signal, batchReadCommands: true,
        pageTransport: pageTransportEnabled(handle.activityKind, this.pageTransportMode)});
      phase = 'attach'; await handle.page.attach(); phase = 'create_page'; await handle.page.createTaskPage();
      return handle;
    } catch (error) {
      this.diagnostic?.({event: 'application_host_failed', phase, code: error.code,
        reason: error.diagnosticReason, command: error.diagnosticCommand, ...taskCorrelation(task, grant)});
      try {await this.close(handle);} catch (cleanupError) {cleanupError.resourceHandle = handle; throw cleanupError;}
      throw error;
    }
  }
  async beginActivity(handle, {id, kind, spec, resource, grant, signal, task}) {
    const reservation = handle.bootstrap ?? await this.controller.reserveApplication({taskID: task, ...reservationPolicy(spec, kind), signal,
      waitMS: Math.max(0, Math.min(30000, spec.timeout_ms ?? 30000, grant.execution_expires_ms - Date.now()))});
    handle.bootstrap = null;
    // A pooled page can outlive its creator, so correlation belongs to the
    // admitted activity and must never be inherited from the page handle.
    handle.activities.set(id, {reservation, kind, spec, resource, correlation: taskCorrelation(task, grant)});
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
    const page = handle.page;
    // A retained page still carries the retired lease's aborted signal and
    // deadline. Refresh its authority before every page call, including the
    // secret reload that is the first action of a newly admitted send.
    page.registration = {...page.registration, ...spec.host.page};
    page.signal = current.reservation ? AbortSignal.any([signal, current.reservation.abortController.signal]) : signal;
    page.deadline = Date.now() + Math.min(spec.timeout_ms, 300000);
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
      await handle.page.reloadSecrets();
      return true;
    }
    if (method === 'download') {
      const destination = path.resolve(args[1]);
      const root = path.resolve(handle.resource.workspace_root ?? '');
      if (!path.isAbsolute(handle.resource.workspace_root ?? '') || !destination.startsWith(root + path.sep)) throw clientContractError();
    }
    let result;
    try {result = await page[method](...args.map(value => value === null ? undefined : value));}
    catch (error) {
      this.diagnostic?.({event: 'application_host_failed', phase: method, code: error.code,
        reason: error.diagnosticReason, command: error.diagnosticCommand, ...(current.correlation ?? {correlation: 'incomplete'})}); throw error;
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
    // Host hello can revisit a fenced slot before another create attempts
    // recovery. A reaped handle is idempotent only after the same strict proof;
    // retain its original failure for the already-issued task receipt.
    if (handle?.closed) {
      if (handle.cleanupFailure) {
        await this.assertCleanupClear();
      }
      return;
    }
    if (handle?.cleanupFailure) throw handle.cleanupFailure;
    if (!handle) return;
    let failure;
    const diagnose = (phase, error) => this.diagnostic?.({event: 'application_host_cleanup', phase,
      code: error.code, reason: error.diagnosticReason, command: error.diagnosticCommand});
    try {await handle.page?.closeTaskPage();} catch (error) {diagnose('close_page', error); failure = error;}
    try {await handle.page?.stop();} catch (error) {diagnose('stop_cli', error); failure ??= error;}
    // Reaping is the final owned-process fence, even if the page call failed.
    try {await handle.state.reapDaemon();} catch (error) {diagnose('reap', error); await this.fenceCleanup(handle, error, 'process'); throw error;}
    if (failure) await this.fenceCleanup(handle, failure, 'page');
    else await handle.state.remove();
    if (handle.bootstrap) {this.controller.finishApplication(handle.bootstrap); handle.bootstrap = null;}
    for (const id of handle.activities.keys()) await this.revokeActivity(handle, id);
    handle.closed = true; this.handles.delete(handle);
    if (failure) throw failure;
  }
  async shutdown() {
    let failure;
    try {
      const outcomes = await Promise.allSettled([...this.handles].map(handle => this.close(handle)));
      failure = outcomes.find(result => result.status === 'rejected')?.reason;
    } catch (error) {failure = error;}
    finally {
      // Failed owned-page cleanup retains its fence and original error. It
      // must not keep this unrelated listener alive until systemd kills us.
      for (const socket of this.connections) socket.destroy();
      try {if (this.events) await new Promise(resolve => this.events.close(resolve));}
      catch (error) {failure ??= error;}
      try {if (this.eventSocket) await fs.rm(this.eventSocket, {force: true});}
      catch (error) {failure ??= error;}
    }
    if (failure) throw failure;
  }
}

// Only a deployment that names its Bridge socket (the systemd unit does) is
// asked; tests and Electron hosts without a Bridge never prove a fence.
function defaultBridgeTaskStatus() {
  if (!process.env.SPARKCLAW_BROWSER_BRIDGE_NATIVE_SOCKET?.trim()) return Promise.resolve(null);
  let socketPath;
  try {socketPath = nativeSocketPath();} catch {return Promise.resolve(null);}
  return queryBridgeTaskStatus(socketPath);
}
