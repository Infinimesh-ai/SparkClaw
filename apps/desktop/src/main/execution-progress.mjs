import { traceLatency } from "./latency-trace.mjs";
import { ExecutionStreamProjection } from "./execution-stream-projection.mjs";

const ACTIVE = new Set(["accepted", "running", "cancel_pending"]);
const TERMINAL = new Set(["completed", "failed", "canceled", "unknown", "delivery_expired", "delivered"]);
const identity = scope => JSON.stringify([scope?.deployment_id, scope?.owner_id, scope?.client_id]);
const RETRIES = [250, 500, 1000, 2000, 5000];

// A process-local observer of an already accepted execution. Neither recovery
// nor a cursor can submit work, create a message, persist text or send an ACK.
export class ExecutionProgressClient {
  constructor({ auth, store, getIdentity, onChange, onTerminal, now = () => performance.now() }) {
    Object.assign(this, { auth, store, getIdentity, onChange, onTerminal, now });
    this.entries = new Map(); this.lifetime = 0; this.closed = false;
  }

  resume() { this.closed = false; }
  close() {
    this.closed = true; this.lifetime++;
    const visible = [...this.entries.values()].some(entry => entry.projection.read());
    for (const entry of this.entries.values()) { entry.controller.abort(); entry.attempt?.abort(); entry.projection.close(); }
    this.entries.clear();
    if (visible) this.onChange();
  }

  // The main lifecycle may pause an observation across transport loss while
  // retaining its one incident deadline. Account/logout uses close instead.
  pause({ authorityExpired = false } = {}) {
    for (const entry of this.entries.values()) {
      this.#incident(entry); entry.attempt?.abort(); entry.projection.disconnect();
      if (authorityExpired) { entry.authorityDeadline = 0; this.#discard(entry); }
    }
  }

  ensure(scope, task) {
    const key = this.#key(scope, task.request_id), prior = this.entries.get(key);
    if (!this.#active(task)) { this.forget(scope, task.request_id); return; }
    if (prior) {
      if (prior.inputDigest !== task.input_digest || !this.#identity(prior)) this.forget(scope, task.request_id);
      else return; // Includes awaiting_result: ordinary task lookup cannot restart an exhausted incident.
    }
    if (!this.#supported()) return;
    const entry = { scope: { ...scope }, requestID: task.request_id, inputDigest: task.input_digest,
      installationID: this.store.installationID, lifetime: this.lifetime, controller: new AbortController(),
      projection: new ExecutionStreamProjection({ requestID: task.request_id, inputDigest: task.input_digest, now: this.now }),
      state: "opening", incidentAt: this.now(), retry: 0 };
    this.entries.set(key, entry); void this.#run(entry);
  }

  read(scope, requestID) { return this.readProjection(scope, requestID)?.content; }
  readProjection(scope, requestID) {
    const entry = this.entries.get(this.#key(scope, requestID));
    if (!entry || !this.#identity(entry) || !this.#displayAuthority(entry) || !this.#active(this.store.request(scope, requestID))) return undefined;
    return entry.projection.read();
  }

  forget(scope, requestID, notify = true) {
    const key = this.#key(scope, requestID), entry = this.entries.get(key);
    if (!entry) return;
    const visible = Boolean(entry.projection.read());
    this.entries.delete(key); entry.controller.abort(); entry.attempt?.abort(); entry.projection.close(); entry.state = "closed";
    if (notify && visible) this.onChange();
  }

  #supported() {
    const transport = this.auth.transport, manifest = transport?.capabilities;
    return !this.closed && this.auth.status.state === "connected" && !this.auth.authorizationRevoked && this.auth.descriptor?.transport === "iscp" &&
      typeof transport?.streamCall === "function" && transport.streamSchemaVersion === 1 && manifest?.stream_schema_version === 1 &&
      manifest.schema_version === 2 && manifest.profile === "sparkclaw.workbench.transport.v2" &&
      (!Number.isFinite(transport.capabilityValidUntil) || performance.now() < transport.capabilityValidUntil) &&
      Date.parse(manifest.expires_at) > Date.now() && manifest.operations?.includes("execution.progress.stream");
  }
  #active(task) { return task?.explicitly_submitted === 1 && ACTIVE.has(task.status); }
  #key(scope, requestID) { return JSON.stringify([identity(scope), requestID]); }
  #identity(entry) {
    return !this.closed && !entry.controller.signal.aborted && entry.lifetime === this.lifetime &&
      entry.installationID === this.store.installationID && identity(entry.scope) === identity(this.getIdentity()) &&
      this.entries.get(this.#key(entry.scope, entry.requestID)) === entry && !this.auth.authorizationRevoked;
  }
  #authority(entry) {
    const manifest = this.auth.transport?.capabilities;
    return this.#supported() && entry.generation === this.auth.generation && entry.session === manifest.session_id &&
      entry.authorizationRevision === manifest.authorization_revision;
  }
  #displayAuthority(entry) {
    if (!(performance.now() < entry.authorityDeadline)) return false;
    const manifest = this.auth.transport?.capabilities;
    if (manifest && entry.authorizationRevision !== manifest.authorization_revision) return false;
    if (["reconnecting", "service_unavailable"].includes(this.auth.status.state)) return true;
    return this.#authority(entry);
  }
  #incident(entry) { entry.incidentAt ??= this.now(); }
  #expired(entry) { return entry.incidentAt !== undefined && this.now() - entry.incidentAt >= 30000; }
  #discard(entry) { const visible = Boolean(entry.projection.read()); entry.projection.discard(); if (visible) this.onChange(); }
  #recoverOriginal(entry) {
    // Do not await the ordinary authoritative lookup here: it has its own
    // deadline and must never prevent stream cancellation or gap processing.
    if (entry.lookup) return;
    entry.lookup = Promise.resolve().then(() => this.#identity(entry) ? this.onTerminal(entry.scope, entry.requestID) : undefined)
      .catch(() => {}).finally(() => { entry.lookup = undefined; });
  }

  async #run(entry) {
    try {
      while (this.#identity(entry)) {
        if (!this.#active(this.store.request(entry.scope, entry.requestID))) return;
        if (this.#expired(entry)) { entry.state = "awaiting_result"; this.#recoverOriginal(entry); return; }
        if (!this.#supported()) {
          this.#incident(entry); entry.state = "auth_paused";
          // Retain private verified text during a short connection outage;
          // actual authority expiry invalidates it even without a network call.
          if (!(performance.now() < entry.authorityDeadline)) this.#discard(entry);
          await pause(250, entry.controller.signal); continue;
        }
        const manifest = this.auth.transport.capabilities;
        if (entry.session && entry.authorizationRevision !== manifest.authorization_revision) {
          this.#incident(entry); this.#discard(entry);
        }
        entry.generation = this.auth.generation; entry.session = manifest.session_id; entry.authorizationRevision = manifest.authorization_revision;
        entry.authorityDeadline = Math.min(this.auth.transport.capabilityValidUntil ?? performance.now() + Math.max(0, Date.parse(manifest.expires_at) - Date.now()), performance.now() + 300000);
        entry.attempt = new AbortController();
        const attempt = entry.attempt, generation = entry.generation;
        const current = () => this.#identity(entry) && this.#authority(entry) && entry.attempt === attempt && !attempt.signal.aborted && generation === this.auth.generation;
        const openedAt = this.now(); let failure, finished = false, observed = false;
        entry.state = entry.incidentAt === undefined ? "opening" : "recovering";
        const cursor = entry.projection.cursor();
        const promise = this.auth.transport.streamCall("execution.progress.stream", {
          schema_version: 1, input_digest: entry.inputDigest, latest_open_at: new Date(Date.now() + 10000).toISOString(), ...(cursor ? { cursor } : {}),
        }, { requestID: entry.requestID, installationID: entry.installationID, params: { request_id: entry.requestID }, signal: attempt.signal,
          onOpen: id => entry.projection.open(id), onEvent: event => {
            if (!current() || !this.#active(this.store.request(entry.scope, entry.requestID))) return;
            try {
              const changed = entry.projection.event(event); observed = true; entry.state = "receiving";
              if (entry.projection.gapAt !== undefined) this.#incident(entry);
              if (!entry.projection.candidate && entry.projection.cursor() && entry.projection.gapAt === undefined) { entry.incidentAt = undefined; entry.retry = 0; }
              if (changed) { if (!current()) return; this.onChange(); }
              if (event.body?.kind === "state" && TERMINAL.has(event.body.state)) this.#recoverOriginal(entry);
            } catch (error) { failure = error; this.#discard(entry); attempt.abort(); }
          } }).then(value => {
          if (!current()) return;
          try {
            const before = entry.projection.read(); entry.projection.complete(value); finished = true;
            if (before && !entry.projection.read()) this.onChange();
          } catch (error) { failure = error; }
        }, error => { failure = error; });
        while (current() && !failure) {
          if (entry.projection.settled) { finished = true; break; }
          if (entry.projection.gapExpired || !observed && this.now() - openedAt >= 10000 || this.#expired(entry)) {
            failure = new Error("Execution stream recovery deadline exceeded"); break;
          }
          await pause(25, entry.controller.signal);
        }
        attempt.abort(); await promise; entry.projection.disconnect();
        if (!this.#identity(entry)) return;
        const reason = finished ? entry.projection.completion?.reason : undefined;
        if (reason === "terminal" || reason === "preview_unavailable" || reason === "canceled" || this.#expired(entry)) {
          entry.state = "awaiting_result"; this.#recoverOriginal(entry); return;
        }
        this.#incident(entry); entry.state = reason === "auth_expired" ? "auth_paused" : "recovering";
        if (["reset", "auth_expired"].includes(reason)) this.#discard(entry);
        this.#recoverOriginal(entry);
        traceLatency("execution.stream.recover", { request_id: entry.requestID });
        await pause(Math.min(RETRIES[Math.min(entry.retry++, RETRIES.length - 1)], Math.max(0, 30000 - (this.now() - entry.incidentAt))), entry.controller.signal);
      }
    } catch { /* Closing an observer never changes the durable task. */ }
    finally { entry.attempt?.abort(); if (!this.#identity(entry)) this.forget(entry.scope, entry.requestID); }
  }
}

function pause(milliseconds, signal) {
  return new Promise((resolve, reject) => {
    const finish = () => { signal.removeEventListener("abort", cancel); resolve(); };
    const timer = setTimeout(finish, milliseconds); timer.unref?.();
    const cancel = () => { clearTimeout(timer); signal.removeEventListener("abort", cancel); reject(new Error("Execution progress closed")); };
    signal.addEventListener("abort", cancel, { once: true }); if (signal.aborted) cancel();
  });
}
