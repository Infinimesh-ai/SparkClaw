import crypto from 'node:crypto';
import fs from 'node:fs/promises';
import net from 'node:net';
import path from 'node:path';
import {PlaywrightCLITask} from './cli-task.mjs';
import {createInvocationState} from './cli-runtime.mjs';
import {listenOwnerOnlyUnixSocket} from './unix-socket.mjs';
import {ControllerError, invalidRequest} from './errors.mjs';

const PROVIDERS = new Set(['qq_mail', 'gmail', 'outlook']);
const REASONS = new Set(['qq_inbound_envelope', 'gmail_topic_invalidation', 'outlook_delivery_change', 'frame_limit', 'buffer_limit', 'framing', 'channel_error', 'unclassified_notification']);
const KINDS = new Set(['document', 'liveness', 'evidence', 'chunk', 'heartbeat', 'channel_open', 'channel_closed', 'degraded', 'mailbox_changed']);

export function validateMailObserverInput(provider, input) {
  if (!PROVIDERS.has(provider) || !input || Object.keys(input).sort().join(',') !== 'account_address,action,owner_scope,schema_version' ||
      !['start', 'status', 'stop'].includes(input.action) || input.schema_version !== 1 ||
      typeof input.account_address !== 'string' || input.account_address.length > 320 ||
      !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(input.account_address) || !/^[a-f0-9]{64}$/.test(input.owner_scope)) {
    throw invalidRequest();
  }
}

// The observer is a parked owned task, not an in-flight provider invocation.
// Only start/stop runs through the Controller gate; events arrive independently
// through a binding and owner-only socket while Reader reservations are free.
export class ResidentMailObservers {
  constructor(factory, {runtimeRoot, evidenceLimit = 256, taskFactory = options => new PlaywrightCLITask(options), evidence = process.env.SPARKCLAW_MAIL_OBSERVER_EVIDENCE === '1', healthMS = 15000} = {}) {
    this.factory = factory;
    this.root = runtimeRoot ?? path.join(factory.runtimeRoot, 'observers');
    this.limit = evidenceLimit;
    this.taskFactory = taskFactory;
    this.evidence = evidence;
    this.healthMS = healthMS;
    this.recoveries = new Map();
    this.slots = new Map();
    this.connections = new Set();
    this.closed = false;
  }

  async prepare() {
    if (this.server) return;
    await fs.mkdir(this.root, {recursive: true, mode: 0o700});
    this.socketPath = path.join(this.root, `e${process.pid}.sock`);
    this.server = net.createServer(socket => {
      if (this.connections.size >= 16) {socket.destroy(); return;}
      this.connections.add(socket);
      socket.on('close', () => this.connections.delete(socket));
      socket.on('error', () => {});
      let pending = '';
      socket.setEncoding('utf8');
      socket.on('data', chunk => {
        pending += chunk;
        if (pending.length > 131072) { socket.destroy(); return; }
        for (;;) {
          const at = pending.indexOf('\n'); if (at < 0) break;
          const line = pending.slice(0, at); pending = pending.slice(at + 1);
          if (line.length > 16384) { socket.destroy(); return; }
          try { this.receive(JSON.parse(line)); } catch { socket.destroy(); return; }
        }
      });
    });
    await listenOwnerOnlyUnixSocket(this.server, this.socketPath);
    this.healthTimer = setInterval(() => this.checkHealth(), this.healthMS);
    this.healthTimer.unref();
  }

  receive({key, value, dropped}) {
    const slot = [...this.slots.values()].find(item => item.key === key);
    if (!slot || !value || !KINDS.has(value.kind) || typeof value.document !== 'string' || value.document.length > 64 ||
        !Number.isSafeInteger(value.sequence) || value.sequence < 1 || typeof value.account_ok !== 'boolean') return;
    if (slot.document !== value.document) {
      slot.retiredDocuments ??= new Set();
      if (slot.retiredDocuments.has(value.document) || value.kind !== 'document' || value.sequence !== 1) return;
      if (slot.document) slot.retiredDocuments.add(slot.document);
      // A storm of replacement documents is a broken observer, not an unlimited
      // generation cache. A new owned task must be explicitly created.
      if (slot.retiredDocuments.size > 16) {slot.state = 'degraded'; return;}
      slot.document = value.document; slot.sequence = 0; slot.epoch = crypto.randomUUID();
      slot.state = 'starting'; slot.resyncRequired = true;
    }
    if (value.kind === 'mailbox_changed' && !['qq_inbound_envelope', 'gmail_topic_invalidation', 'outlook_delivery_change'].includes(value.reason)) return;
    if (value.sequence <= slot.sequence) return;
    const gap = Boolean(slot.sequence && value.sequence !== slot.sequence + 1 || dropped);
    if (gap) slot.resyncRequired = true;
    slot.sequence = value.sequence;
    slot.lastEvent = Date.now();
    if (value.account_ok) {if (slot.state !== 'degraded') slot.state = 'watching';}
    else if (Date.now() - slot.started > 30000) slot.state = 'login_required';
    if (value.kind === 'degraded') { slot.state = 'degraded'; slot.resyncRequired = true; }
    slot.counts[value.kind] = (slot.counts[value.kind] ?? 0) + 1;
    if (value.kind === 'mailbox_changed' && value.account_ok) slot.hints++;
    this.factory.mailObserverSink?.(argsProvider(slot), slot,
      gap ? 'resync_required' : value.account_ok ? value.kind : 'state', value.reason);
    if (gap && value.kind === 'mailbox_changed' && value.account_ok) this.factory.mailObserverSink?.(argsProvider(slot), slot, value.kind, value.reason);
    if (value.kind !== 'liveness' && (value.kind !== 'evidence' || this.evidence)) {
      const record = {at: new Date().toISOString(), document: value.document, sequence: value.sequence, kind: value.kind, account_ok: value.account_ok};
      if (REASONS.has(value.reason)) record.reason = value.reason;
      if (this.evidence && value.kind === 'evidence') {record.channel = String(value.channel).slice(0, 40); record.shape = value.shape;}
      if (Number.isSafeInteger(value.size) && value.size >= 0 && value.size <= 65536) record.size = value.size;
      const bytes = Buffer.byteLength(JSON.stringify(record));
      slot.bufferBytes ??= 0;
      if (bytes > 16000) {slot.dropped++; return;}
      while (slot.events.length && (slot.events.length >= this.limit || slot.bufferBytes + bytes > 32768)) {
        slot.bufferBytes -= Buffer.byteLength(JSON.stringify(slot.events.shift())); slot.dropped++;
      }
      slot.events.push(record);
      slot.bufferBytes += bytes;
    }
  }

  async run(args) {
    const recovery = this.recoveries.get(args.provider);
    if (recovery) await recovery;
    return this.startOrInspect(args);
  }

  async startOrInspect(args) {
    const {provider, input, token, credentialGeneration} = args;
    validateMailObserverInput(provider, input);
    if (this.closed) throw new Error('observer_closed');
    const identity = crypto.createHash('sha256').update(JSON.stringify([token, credentialGeneration, provider, input.account_address.toLowerCase(), input.owner_scope])).digest('hex');
    const prior = this.slots.get(provider);
    if (prior && prior.identity !== identity) throw new ControllerError('browser_page_stale', 'observer binding does not match', {status: 409});
    if (input.action === 'stop') { if (prior) await this.stop(provider); return {state: 'stopped'}; }
    if (input.action === 'status') return this.status(provider);
    if (prior) {
      if (this.status(provider).state !== 'degraded') return this.status(provider);
      await this.stop(provider);
    }
    const slot = {identity, key: crypto.randomBytes(32).toString('hex'), started: Date.now(), state: 'starting', epoch: crypto.randomUUID(),
      events: [], counts: {}, sequence: 0, hints: args.recoveryHints ?? 0, restarts: args.restarts ?? 0, dropped: 0, resyncRequired: true, lastEvent: 0, ready: false, args: {...args, signal: undefined}};
    this.slots.set(provider, slot);
    let phase = 'runtime';
    try {
      const registration = {...this.factory.registry.entries.get(`${provider}:read`), timeoutMS: 120000};
      slot.runtime = await createInvocationState(this.factory.runtimeRoot, `session_${crypto.randomBytes(16).toString('hex')}`, {}, 'read');
      const configPath = path.join(slot.runtime.directory, 'observer.json');
      await fs.writeFile(configPath, JSON.stringify({key: slot.key, socket: this.socketPath, evidence: this.evidence, provider, account: input.account_address.toLowerCase(),
        origins: registration.origins.filter(origin => !/login|accounts|www\.microsoft/.test(origin))}), {mode: 0o600, flag: 'wx'});
      slot.client = this.taskFactory({...this.factory, registration, state: slot.runtime, token, signal: args.signal, mailObserverConfig: configPath});
      phase = 'attach';
      await slot.client.attach();
      phase = 'create_task_page';
      await slot.client.createTaskPage();
      phase = 'navigate';
      await slot.client.navigate(registration.loginURL);
      await slot.client.prepareBackgroundPage();
      phase = 'account';
      await slot.client.prepareMailRound(input.account_address);
      slot.client.signal = undefined;
      if (slot.state !== 'degraded') slot.state = 'watching';
      slot.ready = true;
      return this.status(provider);
    } catch (error) {
      try { this.factory.diagnostic?.({event: 'mail_observer_start_failed', provider, phase,
        code: /^[a-z_]{1,80}$/.test(error?.code ?? '') ? error.code : 'observer_failed'}); } catch {}
      await this.stop(provider); throw error;
    }
  }

  status(provider) {
    const slot = this.slots.get(provider);
    if (!slot) return {state: 'stopped'};
    return {state: Date.now() - (slot.lastEvent || slot.started) > 45000 ? 'degraded' : slot.state,
      provider, qualified: false, rules_version: 1, restarts: slot.restarts ?? 0, epoch: slot.epoch, sequence: slot.sequence, hints: slot.hints, counts: {...slot.counts},
      dropped: slot.dropped, resync_required: slot.resyncRequired, events: [...slot.events],
      age_ms: Date.now() - slot.started, last_event_age_ms: slot.lastEvent ? Date.now() - slot.lastEvent : null};
  }

  checkHealth() {
    if (this.closed) return;
    for (const [provider, slot] of this.slots) {
      if (!slot.ready || this.recoveries.has(provider) || slot.state === 'login_required') continue;
      if (Date.now() - (slot.lastEvent || slot.started) <= 45000 && slot.state !== 'degraded') continue;
      slot.state = 'degraded'; slot.resyncRequired = true;
      // Three bounded reconstructions per lease. A failed recovery is visible
      // and never loops in a busy CLI poll; an explicit start may retry later.
      if (slot.restarts >= 3 || slot.recoveryFailed || Date.now() < (slot.retryAt ?? 0)) continue;
      slot.retryAt ??= Date.now() + 15000 * 2 ** slot.restarts;
      if (Date.now() < slot.retryAt) continue;
      const args = {...slot.args, restarts: slot.restarts + 1, recoveryHints: slot.hints};
      const work = this.startOrInspect(args).catch(error => {
        try {this.factory.diagnostic?.({event:'mail_observer_recovery_failed',provider,code:/^[a-z_]{1,80}$/.test(error?.code ?? '') ? error.code : 'observer_failed'});} catch {}
        if (!this.slots.has(provider)) {slot.key = ''; slot.state = 'degraded'; slot.client = slot.runtime = undefined; slot.recoveryFailed = true; this.slots.set(provider, slot);}
      }).finally(() => this.recoveries.delete(provider));
      this.recoveries.set(provider, work);
    }
  }

  async stop(provider) {
    const slot = this.slots.get(provider); if (!slot) return;
    slot.state = 'stopped'; slot.key = '';
    let cleanupWarning;
    for (const action of [() => slot.client?.closeTaskPage(), () => slot.client?.stop()]) {
      try { await action(); } catch (error) { cleanupWarning ??= error; }
    }
    // Losing the page is exactly the failure recovery must handle. Once the
    // owned CLI has been stopped/reaped, that stale-page error cannot prevent
    // reconstruction. Failure of the process ownership fence still blocks it.
    await slot.runtime?.reapDaemon();
    await slot.runtime?.remove();
    this.slots.delete(provider);
    if (cleanupWarning) {
      try {this.factory.diagnostic?.({event:'mail_observer_retired',provider,reason:'owned_runtime_reaped'});} catch {}
    }

  }

  async close() {
    this.closed = true;
    clearInterval(this.healthTimer);
    await Promise.allSettled([...this.recoveries.values()]);
    let failure;
    for (const provider of [...this.slots.keys()]) {try {await this.stop(provider);} catch (error) {failure ??= error;}}
    for (const socket of this.connections) socket.destroy();
    if (this.server) await new Promise(resolve => this.server.close(resolve));
    if (this.socketPath) await fs.rm(this.socketPath, {force: true});
    if (failure) throw failure;
  }
}

function argsProvider(slot) { return slot.args.provider; }
