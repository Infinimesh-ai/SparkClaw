import fs from 'node:fs/promises';
import crypto from 'node:crypto';
import path from 'node:path';
import {spawn} from 'node:child_process';
import {fileURLToPath} from 'node:url';
import {MailSendOperations} from './mail-send-operations.mjs';
import {openSendJournal} from '../mail-runtime/applications/mail/lib/send-journal.mjs';
import {isManagedSend, validateManagedSend} from '../mail-runtime/applications/mail/lib/managed-send.mjs';
import {privateDirectory} from '../mail-runtime/src/authorization.mjs';
import {mailHandlers} from '../mail-runtime/applications/mail/handlers.mjs';
import {BrowserHostClient, BrowserHostPort} from '../mail-runtime/src/host-port.mjs';
import {compileSchema, digest, encode, intentDigest, requireCondition, RuntimeError} from '../mail-runtime/src/protocol.mjs';
import {AI_PLATFORM_URLS} from './ai-platform-login.mjs';
import {scrubPlaywrightEnvironment} from './cli-runtime.mjs';
import {ControllerError} from './errors.mjs';
import {ApplicationHostDriver} from './host-driver.mjs';

// SparkClaw-owned mail runtime. The mail application under mail-runtime/ was
// taken from App-CLI 0.3.0-sparkclaw.20 and runs here in-process against the
// same BrowserHostPort, without the App-CLI executor, task ledger or Python
// lifecycle. Durable send safety stays in the application's own send journal.
const PACKAGE_ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
export const MAIL_RUNTIME_ROOT = path.join(PACKAGE_ROOT, 'mail-runtime');
const APPLICATIONS = {qq_mail: 'mail-qq-mail', outlook: 'mail-outlook', gmail: 'mail-gmail'};
const PRINCIPAL = 'product-owner';
const RECOVERABLE = new Set(['HOST_STALE', 'HOST_LEASE_EXPIRED', 'HOST_LEASE_INVALID', 'HOST_UNAVAILABLE', 'HOST_TIMEOUT',
  'EXECUTOR_STOPPING', 'HOST_RESOURCE_BUSY', 'BROWSER_BUSY', 'BROWSER_EXTENSION_UNAVAILABLE', 'BROWSER_PAGE_STALE']);
const WATCH_GRANT_MS = 300_000;

// Grants never leave this process, so they live in memory and are dropped
// with the task that owns them. The Host still verifies every signed request.
class MemoryAuthorization {
  constructor() {this.key = crypto.randomBytes(32); this.records = new Map();}
  mac(payload) {return crypto.createHmac('sha256', this.key).update(digest(payload)).digest('hex');}
  record(reference) {
    const record = this.records.get(reference);
    requireCondition(Boolean(record), 'AUTHORIZATION_INVALID');
    return record;
  }
  issue(grant, resource) {const reference = crypto.randomUUID(); this.records.set(reference, {grant, resource}); return reference;}
  revoke(references) {for (const reference of references) this.records.delete(reference);}
}

export class LocalMailRuntimeFactory {
  constructor(options = {}) {
    Object.assign(this, options);
    this.managesLifecycle = true;
    this.sharedMailPages = options.sharedMailPages ?? (process.env.SPARKCLAW_MAIL_SHARED_PAGE_CANDIDATE !== '0');
    this.watchChanges = new Map(); this.watches = new Map(); this.watchWork = new Map(); this.inflight = new Map(); this.lanes = new Map();
    this.active = new Set(); this.closing = false;
    this.ownerID = options.ownerID ?? `owner-${process.getuid()}`;
  }
  async prepare() {
    try {await this.prepareApplication();}
    catch (error) {
      await this.host?.close().catch(() => {});
      await this.driver?.shutdown().catch(() => {});
      this.host = null; this.driver = null; this.handlers = null;
      this.initializationError = 'application_release_unavailable';
      this.diagnostic?.({event: 'application_initialization_failed', code: this.initializationError});
      if (this.fixtureAssembly) throw error;
    }
  }
  async prepareApplication() {
    if (!path.isAbsolute(this.runtimeRoot ?? '') || !path.isAbsolute(this.stateDirectory ?? '')) return;
    this.bindings = new Map();
    for (const [provider, app] of Object.entries(APPLICATIONS)) {
      const binding = JSON.parse(await fs.readFile(path.join(MAIL_RUNTIME_ROOT, 'bindings', `${app}.json`), 'utf8'));
      requireCondition(binding.manifest?.id === app && binding.manifest_digest === digest(binding.manifest), 'RELEASE_MISMATCH');
      this.bindings.set(provider, binding);
    }
    this.handlers = await mailHandlers();
    this.commands = new Map();
    for (const binding of this.bindings.values()) {
      for (const manifest of binding.manifest.commands) {
        const spec = binding.commands[manifest.name], handler = this.handlers[spec?.handler];
        requireCondition(handler && typeof handler.run === 'function', 'RELEASE_MISMATCH');
        this.commands.set(`${binding.manifest.id}\0${manifest.name}`, {spec, handler, manifest,
          input: compileSchema(manifest.input_schema), output: compileSchema(manifest.output_schema)});
      }
    }
    privateDirectory(this.stateDirectory);
    this.sendOperations = new MailSendOperations(this.stateDirectory);
    this.authorization = new MemoryAuthorization();
    this.driver = new ApplicationHostDriver({...this,
      entryPoint: path.join(PACKAGE_ROOT, 'node_modules/@playwright/cli/playwright-cli.js'),
      releaseRoot: MAIL_RUNTIME_ROOT,
      spawn, connectTimeoutMS: this.connectTimeoutMS ?? 15000, actionTimeoutMS: this.actionTimeoutMS ?? 10000,
      navigationTimeoutMS: this.navigationTimeoutMS ?? 30000});
    await this.driver.prepare();
    const bindings = [...this.bindings.values()];
    this.host = new BrowserHostPort({authorization: this.authorization, bindings, driver: this.driver, stateDirectory: this.stateDirectory});
    // Requests cross the same signed, serialized protocol as the former
    // executor socket, so the Host keeps its validation and lease fencing.
    this.hostClient = new BrowserHostClient({authorization: this.authorization, bindings,
      transport: async request => JSON.parse(JSON.stringify(await this.host.control(JSON.parse(JSON.stringify(request)))))});
    await this.hostClient.connect(this.host.epoch + 1);
    this.ledgerEpoch = this.host.epoch;
  }
  bindController(controller) {if (this.driver) {this.driver.controller = controller; this.driver.drainIdle = () => this.host.drainIdle();}}
  info() {return {app_cli: this.handlers ? 'local-mail-runtime' : this.initializationError ?? 'unconfigured', browser_host: '1.0'};}
  #requireReady() {
    if (!this.handlers) throw new ControllerError('browser_script_unavailable', 'Mail runtime is not configured', {status: 503});
  }
  #describe(provider, operation) {
    const binding = this.bindings?.get(provider), command = operation === 'observe' ? 'watch' : operation;
    const entry = binding && this.commands.get(`${binding.manifest.id}\0${command}`);
    requireCondition(Boolean(entry), 'COMMAND_NOT_FOUND');
    return {binding, command, ...entry};
  }
  describe(provider, operation) {this.#requireReady(); return this.#describe(provider, operation).spec;}

  async runScript(request) {
    this.#requireReady();
    try {
      request = {...request, input: structuredClone(request.input)};
      if (request.operation === 'observe') return await this.#observe(request);
      return await this.#execute(request);
    } catch (error) {
      // Keep the real cause visible; Gateway still receives the stable codes.
      this.diagnostic?.({event: 'mail_runtime_failed', provider: request.provider, operation: request.operation,
        code: typeof error?.code === 'string' ? error.code : 'EXECUTION_FAILED'});
      if (error.code === 'HOST_RESOURCE_BUSY' || error.code === 'BROWSER_BUSY') throw new ControllerError('browser_busy', 'Browser is busy', {status: 409, retryable: true});
      if (['RELEASE_MISMATCH', 'COMMAND_NOT_FOUND'].includes(error.code)) throw new ControllerError('browser_script_unavailable', 'Mail application is unavailable', {status: 503});
      throw new ControllerError('browser_extension_unavailable', 'Mail execution is unavailable', {status: 503, cause: error});
    }
  }
  #admission({provider, operation, input, taskID, credentialGeneration, token, scriptID, revision}, {sessionKey, previousTask} = {}) {
    const described = this.#describe(provider, operation), {binding, command, spec, manifest} = described;
    requireCondition(scriptID === spec.script_id && revision === spec.revision, 'RELEASE_MISMATCH');
    const account = (input.account_address ?? input.discovery?.account_address ?? input.target?.account_address ?? '').toLowerCase();
    const pool_key = digest({owner: this.ownerID, provider, account, scope: input.owner_scope ?? '', credentialGeneration, binding: digest(binding)});
    const resource = {...(previousTask ? {previous_task_id: previousTask} : {}), binding_digest: digest(binding), profile_id: this.profileID ?? 'default',
      credential_generation: credentialGeneration, token, account_address: account, workspace_root: this.emailWorkspaceRoot, host_runtime_root: this.runtimeRoot,
      pool_key: ['collect_page', 'watch'].includes(command) && account ? (this.sharedMailPages ? pool_key : digest({pool_key, command})) : digest({taskID, command}),
      document_nonce: pool_key};
    const args = command === 'watch' ? {schema_version: 1, owner_scope: input.owner_scope, account_address: input.account_address} : input;
    requireCondition(described.input(args), 'INPUT_VALIDATION_FAILED');
    encode(args, 1536 * 1024);
    const request_key = digest({taskID: sessionKey ?? taskID, app: binding.manifest.id, command});
    const now = Date.now(), deadline = spec.renewable ? null : now + spec.timeout_ms;
    const request = {protocol_version: '2.0', operation: 'invoke', app: binding.manifest.id, command, request_key, arguments: args,
      ...(deadline ? {deadline_ms: deadline} : {})};
    const grant = {principal: PRINCIPAL, owner: this.ownerID, app: binding.manifest.id, command, request_key, side_effect: manifest.side_effect,
      intent_digest: intentDigest(request), operations: ['invoke', 'renew'], access_expires_ms: now + 86400000,
      execution_expires_ms: spec.renewable ? now + WATCH_GRANT_MS : deadline, max_deadline_ms: deadline, task_id: null, revision: 1};
    return {...described, request, grant, resource, references: [this.authorization.issue(grant, resource)]};
  }
  #renew(admission) {
    const grant = {...admission.grant, revision: admission.grant.revision + 1, execution_expires_ms: Date.now() + WATCH_GRANT_MS, access_expires_ms: Date.now() + WATCH_GRANT_MS * 2};
    admission.grant = grant; admission.references.push(this.authorization.issue(grant, admission.resource));
    // The Host keeps heartbeating with the newest reference; older ones go.
    this.authorization.revoke(admission.references.splice(0, admission.references.length - 2));
  }
  async #execute(request) {
    const admission = this.#admission(request);
    const key = admission.grant.request_key, resource = digest(admission.resource);
    const reconcile = request.operation === 'send' && request.input.mode === 'reconcile';
    let intent;
    try {intent = request.operation === 'send' ? this.sendOperations.intent(request.input) : digest(admission.request.arguments);}
    catch (error) {this.authorization.revoke(admission.references); throw error;}
    const existing = this.inflight.get(key);
    if (existing) {
      this.authorization.revoke(admission.references);
      // A lookup may wait for an in-flight original send, but must then check
      // its own approved input against that send's durable record.
      if (reconcile) requireCondition(this.sendOperations.intent(request.input, existing.mode) === existing.intent, 'REQUEST_KEY_CONFLICT');
      else requireCondition(existing.intent === intent && existing.resource === resource, 'REQUEST_KEY_CONFLICT');
      const cancel = () => existing.abort.abort();
      if (request.signal?.aborted) cancel();
      else request.signal?.addEventListener('abort', cancel, {once: true});
      try {
        const result = await existing.promise;
        return reconcile ? this.#execute(request) : result;
      } finally {request.signal?.removeEventListener('abort', cancel);}
    }
    const abort = new AbortController();
    request = {...request, signal: request.signal ? AbortSignal.any([request.signal, abort.signal]) : abort.signal};
    const promise = Promise.resolve().then(async () => {
      if (request.operation === 'send') {
        const cached = await this.#prepareSend(admission, request, reconcile);
        if (cached) return cached;
      }
      const outcome = await this.#run(admission, request, reconcile);
      return admission.send ? this.sendOperations.finish(admission.send, outcome) : outcome;
    }).then(outcome => this.#legacyResult(request.provider, request.operation, outcome))
      .finally(() => {this.inflight.delete(key); this.authorization.revoke(admission.references);});
    this.inflight.set(key, {intent, resource, promise, abort, mode: request.input.mode});
    return promise;
  }
  async #prepareSend(admission, request, reconcile) {
    const key = this.sendOperations.key(this.ownerID, this.profileID ?? 'default', request);
    const saved = this.sendOperations.read(key, request.input);
    const unknown = id => ({id, status: 'uncertain', reason: 'SEND_OUTCOME_UNKNOWN', data: null});
    if (saved) {
      if (!reconcile) requireCondition(saved.evidence.resource_digest === digest(admission.resource), 'REQUEST_KEY_CONFLICT');
      admission.send = saved;
      if (saved.outcome?.status === 'completed') requireCondition(admission.output(saved.outcome.data), 'OUTPUT_VALIDATION_FAILED');
      if (!reconcile && !saved.effect && saved.outcome?.status === 'blocked' && saved.deadline_ms > Date.now()) {
        // Retry only a proved pre-effect outage within the original deadline.
        // An interrupted admission has no such terminal proof and stays unknown.
        admission.request.deadline_ms = admission.grant.execution_expires_ms = admission.grant.max_deadline_ms = saved.deadline_ms;
        saved.outcome = null; this.sendOperations.write(saved);
        return null;
      }
      if (!reconcile || saved.outcome?.status === 'completed') return saved.outcome ?? unknown(saved.evidence.task_id);
      return null;
    }
    let evidence = {task_id: crypto.randomUUID(), intent_digest: admission.grant.intent_digest,
      resource_digest: digest(admission.resource), binding_digest: admission.resource.binding_digest, ledger_epoch: this.ledgerEpoch};
    let mode = request.input.mode, outcome = null, effect = reconcile;
    // Existing managed journals survive upgrades in the same workspace. A
    // journal is bound to provider/account/invocation and approved message bytes
    // before its original proof can be imported; missing evidence stays unknown.
    if (isManagedSend(request.input)) {
      const input = validateManagedSend(request.input, request.provider);
      const journal = await openSendJournal(this.emailWorkspaceRoot, input);
      if (reconcile) mode = journal.saved?.mode ?? (input.reply_target ? 'reply' : 'compose');
      if (journal.saved) {
        effect = true;
        const receipt = journal.saved.receipt;
        if (journal.saved.stage === 'sent' || reconcile && journal.saved.stage === 'not_sent') {
          requireCondition(receipt?.status === journal.saved.stage && admission.output(receipt) && receipt.recipient_digest === input.recipientDigest &&
            receipt.provider === request.provider && (!receipt.not_sent || receipt.not_sent.invocation_id === input.invocation_id), 'SEND_OUTCOME_UNKNOWN');
          if (receipt.not_sent) {
            const {task_id, intent_digest, resource_digest, binding_digest, ledger_epoch} = receipt.not_sent;
            evidence = {task_id, intent_digest, resource_digest, binding_digest, ledger_epoch};
          }
          outcome = {id: evidence.task_id, status: 'completed', reason: null, data: receipt};
        } else outcome = unknown(evidence.task_id);
      }
    }
    if (reconcile && !outcome) outcome = unknown(evidence.task_id);
    const result = this.sendOperations.create(key, request.input, evidence, {mode, effect, outcome, deadline: admission.request.deadline_ms});
    admission.send = result.saved;
    return result.fresh ? outcome : result.saved.outcome ?? unknown(result.saved.evidence.task_id);
  }
  #legacyResult(provider, operation, outcome) {
    const {spec} = this.#describe(provider, operation);
    if (outcome.status === 'completed') return {state: 'completed', result: outcome.data, sourceChecksum: spec.source_checksum};
    const code = outcome.status === 'uncertain' ? (operation === 'send' ? 'send_outcome_unknown' : 'email_source_recovery_pending') : (outcome.reason ?? 'provider_script_failed').toLowerCase();
    return {state: 'failed', result: {schema_version: 1, status: 'error', provider, code}, sourceChecksum: spec.source_checksum,
      task: {id: outcome.id, status: outcome.status}};
  }
  // Mirrors the former executor's task semantics without durable rows: one
  // lane per provider resource, effect tracking and deadline enforcement.
  #run(admission, request, reconcile, hooks = {}) {
    const {spec, handler, output, binding} = admission;
    requireCondition(!this.closing, 'EXECUTOR_DRAINING');
    requireCondition(admission.input(admission.request.arguments), 'INPUT_VALIDATION_FAILED');
    encode(admission.request.arguments, 1536 * 1024);
    const task = {id: admission.send?.evidence.task_id ?? crypto.randomUUID(), status: 'pending', effect: admission.send?.effect ?? false, abort: new AbortController(), stopReason: null};
    hooks.started?.(task);
    const lane = `${this.ownerID}\0${spec.resource ?? binding.manifest.id}`;
    const prior = this.lanes.get(lane) ?? Promise.resolve();
    const run = prior.catch(() => {}).then(() => this.#perform(task, admission, request, reconcile, hooks));
    this.lanes.set(lane, run); this.active.add(task);
    const stop = () => {task.stopReason ??= 'CANCEL_REQUESTED'; task.abort.abort();};
    if (request.signal?.aborted) stop();
    else request.signal?.addEventListener('abort', stop, {once: true});
    void run.finally(() => {
      this.active.delete(task); request.signal?.removeEventListener('abort', stop);
      if (this.lanes.get(lane) === run) this.lanes.delete(lane);
    }).catch(() => {});
    return run;
  }
  async #perform(task, admission, request, reconcile, hooks) {
    const {spec, handler, output, grant, resource} = admission;
    const deadline = admission.request.deadline_ms;
    let session, timer;
    const finish = (status, reason = null, data = null) => {task.status = status; return {id: task.id, status, reason, data};};
    try {
      const check = () => {
        if (Date.now() >= admission.grant.execution_expires_ms) {task.stopReason ??= 'AUTHORIZATION_EXPIRED'; task.abort.abort();}
        if (!reconcile && deadline && Date.now() >= deadline) {task.stopReason ??= 'DEADLINE_EXCEEDED'; task.abort.abort();}
        if (task.abort.signal.aborted) throw new RuntimeError(task.stopReason ?? 'CANCEL_REQUESTED');
      };
      check();
      task.status = 'running';
      timer = setInterval(() => {try {check();} catch { /* The handler receives the abort signal. */ }}, 100);
      timer.unref();
      const context = {task_id: task.id, epoch: this.ledgerEpoch, signal: task.abort.signal, resource,
        executionEvidence: Object.freeze(admission.send?.evidence ?? {task_id: task.id, intent_digest: grant.intent_digest,
          resource_digest: digest(resource), binding_digest: resource.binding_digest, ledger_epoch: this.ledgerEpoch}),
        reconciliation: reconcile && admission.send ? {status: admission.send.outcome?.status ?? 'uncertain',
          reason: admission.send.outcome?.reason ?? 'EXECUTION_INTERRUPTED', effect: admission.send.effect ? 1 : 0} : null, check,
        emit: (type, payload) => {check(); hooks.emit?.(type, payload);},
        beforeEffect: () => {check(); requireCondition(!reconcile && grant.side_effect !== 'read_only', 'EFFECT_DENIED'); if (admission.send) this.sendOperations.beforeEffect(admission.send); task.effect = true;},
        authorization: () => admission.references.at(-1)};
      if (spec.host && !(reconcile && spec.reconcile_requires_host === false)) {
        session = await this.hostClient.acquire({...context, binding: spec, resource});
        context.browser = session;
      }
      const result = await handler[reconcile ? 'reconcile' : 'run'](admission.request.arguments, context);
      check();
      let status = result?.status ?? 'completed';
      requireCondition(['completed', 'waiting_confirmation', 'uncertain', 'failed', 'blocked'].includes(status), 'OUTPUT_VALIDATION_FAILED');
      if (task.effect && ['blocked', 'waiting_confirmation'].includes(status)) status = 'uncertain';
      if (status === 'completed') {requireCondition(output(result.data), 'OUTPUT_VALIDATION_FAILED'); encode(result.data, 1000 * 1024);}
      if (session) {await session.release({park: status === 'completed' && result.retain === true && spec.host?.reuse_idle_ms > 0, invalidate: result.invalidate === true}); session = null;}
      check();
      return finish(status, result?.reason ?? null, status === 'completed' ? result.data : null);
    } catch (error) {
      let cleanupFailed = false;
      if (session) try {await session.release({invalidate: !task.stopReason});} catch {cleanupFailed = true;}
      const reason = cleanupFailed ? 'HOST_CLEANUP_FAILED' : task.stopReason ??
        (/^[A-Z][A-Z0-9_]{0,63}$/u.test(error.code) ? error.code : 'EXECUTION_FAILED');
      const status = task.effect ? 'uncertain' : cleanupFailed || RECOVERABLE.has(reason) ? 'blocked' :
        reason === 'CANCEL_REQUESTED' ? 'cancelled' : reason === 'AUTHORIZATION_EXPIRED' ? 'waiting_confirmation' : 'failed';
      return finish(status, reason);
    } finally {clearInterval(timer);}
  }

  async #observe(request) {
    const prior = this.watchChanges.get(request.provider) ?? Promise.resolve();
    const change = prior.catch(() => {}).then(() => this.#changeWatch(request));
    this.watchChanges.set(request.provider, change);
    try {return await change;} finally {if (this.watchChanges.get(request.provider) === change) this.watchChanges.delete(request.provider);}
  }
  async #changeWatch(request) {
    if (request.input.action === 'stop') return this.stopWatch(request.provider);
    if (request.input.action === 'status') {await this.pollWatch(request.provider); return this.watchStatus(request.provider);}
    return this.#startWatch(request);
  }
  async #startWatch(request) {
    const {binding} = this.#describe(request.provider, 'observe');
    const identity = digest([request.token, request.credentialGeneration, request.provider,
      request.input.account_address.toLowerCase(), request.input.owner_scope, digest(binding)]);
    const prior = this.watches.get(request.provider);
    if (prior && prior.identity === identity && ['pending', 'running'].includes(prior.task.status)) return this.watchStatus(request.provider);
    if (prior) await this.stopWatch(request.provider);
    // The feed matches events by this product identity, not the watch's own.
    const sinkIdentity = crypto.createHash('sha256').update(JSON.stringify([request.token, request.credentialGeneration,
      request.provider, request.input.account_address?.toLowerCase(), request.input.owner_scope])).digest('hex');
    const admission = this.#admission(request, {sessionKey: crypto.randomUUID(), previousTask: prior?.task.id});
    const watch = {identity, admission, task: {id: '', status: 'pending'}, cursor: 0, events: [], sequence: 0, epoch: '',
      ready: false, state: 'starting', onEvent: (slot, kind, reason) => this.mailObserverSink?.(request.provider, {...slot, identity: sinkIdentity}, kind, reason)};
    watch.promise = Promise.resolve().then(() => this.#run(admission, request, false, {
      started: task => {watch.task = task; watch.epoch = task.id;},
      emit: (type, payload) => {
        if (type === 'watch_ready') {watch.ready = true; watch.state = 'watching';}
        watch.events.push({sequence: ++watch.sequence, type, payload});
        while (watch.events.length > 1000) watch.events.shift();
      },
    })).finally(() => this.authorization.revoke(admission.references));
    void watch.promise.catch(() => {});
    this.watches.set(request.provider, watch);
    return this.watchStatus(request.provider);
  }
  watchStatus(provider) {
    const watch = this.watches.get(provider), {spec} = this.#describe(provider, 'observe');
    return {state: 'completed', sourceChecksum: spec.source_checksum, result: {schema_version: 1, provider,
      status: watch?.state ?? 'stopped', watch_epoch: watch?.epoch ?? '', resync_required: !watch?.ready}};
  }
  async pollWatch(provider, {renew = false} = {}) {
    if (this.watchWork.has(provider)) return this.watchWork.get(provider);
    const work = this.#poll(provider, renew).finally(() => this.watchWork.delete(provider));
    this.watchWork.set(provider, work); return work;
  }
  async #poll(provider, renew) {
    const watch = this.watches.get(provider); if (!watch) return null;
    if (renew && watch.task.status === 'running' && watch.admission.grant.execution_expires_ms - Date.now() < 60000) this.#renew(watch.admission);
    const first = watch.events[0]?.sequence ?? watch.sequence + 1;
    if (watch.cursor < first - 1) watch.onEvent(watch, 'resync_required');
    for (const event of watch.events.filter(value => value.sequence > watch.cursor)) {
      if (event.type === 'watch_ready') {watch.ready = true; watch.state = 'watching'; watch.onEvent(watch, 'resync_required');}
      if (event.type === 'gap') watch.onEvent(watch, 'resync_required');
      if (event.type === 'mailbox_changed') watch.onEvent(watch, 'mailbox_changed', event.payload.reason);
    }
    watch.cursor = watch.sequence; watch.events = [];
    if (watch.task.status === 'waiting_confirmation') watch.state = 'login_required';
    else if (['blocked', 'failed', 'uncertain'].includes(watch.task.status)) watch.state = 'degraded';
    else if (watch.task.status === 'cancelled') watch.state = 'stopped';
    watch.onEvent(watch, 'state'); return watch;
  }
  async stopWatch(provider) {
    const watch = this.watches.get(provider);
    if (watch) {
      watch.task.stopReason ??= 'CANCEL_REQUESTED'; watch.task.abort?.abort();
      await watch.promise.catch(() => {});
      this.watches.delete(provider);
    }
    return this.watchStatus(provider);
  }
  async drainIdleMailReads() {await this.host?.drainIdle();}
  async openProviderLogin(provider) {
    if (this.watches.has(provider)) await this.stopWatch(provider);
    this.#requireReady();
    const url = AI_PLATFORM_URLS[provider] ?? this.describe(provider, 'probe').host.page.loginURL;
    if (!path.isAbsolute(this.executablePath ?? '') || !path.isAbsolute(this.userDataDir ?? '')) throw new ControllerError('browser_extension_unavailable', 'Browser launcher is unavailable', {status: 503});
    const child = spawn(this.executablePath, [`--user-data-dir=${this.userDataDir}`, url], {
      env: scrubPlaywrightEnvironment({...process.env, ...this.extraEnv, ...(this.electronAdapter ? {
        SPARKCLAW_ELECTRON_ADAPTER_SOCKET: this.electronAdapter.socketPath,
        SPARKCLAW_ELECTRON_ADAPTER_SECRET_FILE: this.electronAdapter.secretPath,
      } : {})}), detached: true, stdio: 'ignore', windowsHide: true,
    });
    await new Promise((resolve, reject) => {child.once('spawn', resolve); child.once('error', reject);});
    child.unref(); return {provider};
  }
  async close() {
    let failure;
    this.closing = true;
    await Promise.allSettled([...this.watchChanges.values()]);
    try {await Promise.all([...this.watches.keys()].map(provider => this.stopWatch(provider)));} catch (error) {failure = error;}
    for (const task of this.active) {task.stopReason ??= 'EXECUTOR_STOPPING'; task.abort.abort();}
    await Promise.allSettled([...this.lanes.values()]);
    try {await this.host?.close();} catch (error) {failure ??= error;}
    try {await this.driver?.shutdown();} catch (error) {failure ??= error;}
    if (failure) throw failure;
  }
}
