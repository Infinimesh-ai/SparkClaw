import crypto from "node:crypto";
import { traceLatency, latencyNow } from "./latency-trace.mjs";

const ACTIVE = new Set(["submission_pending", "accepted", "running", "cancel_pending"]);
const TERMINAL = new Set(["completed", "failed", "canceled", "unknown", "delivery_expired", "delivered"]);
const STATES = new Set(["accepted", "running", "approval_pending", "browser_login_blocked", ...TERMINAL]);
const UUID = /^[a-f0-9]{8}-[a-f0-9]{4}-[1-8][a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/u;
const keys = "content,digest,epoch,input_digest,request_id,revision,schema_version,state,truncated";
const identity = scope => JSON.stringify([scope?.deployment_id, scope?.owner_id, scope?.client_id]);
const digest = text => crypto.createHash("sha256").update(text).digest("hex");

// Progress is an authenticated, temporary display projection. It never owns a
// message, durable result receipt, delivery ACK or permission to submit work.
export class ExecutionProgressClient {
  constructor({ auth, store, getIdentity, onChange, onTerminal }) {
    Object.assign(this, { auth, store, getIdentity, onChange, onTerminal });
    this.entries = new Map(); this.lifetime = 0; this.closed = false;
  }

  resume() { this.closed = false; }
  close() {
    this.closed = true; this.lifetime++;
    const visible = [...this.entries.values()].some(entry => entry.snapshot?.content);
    for (const entry of this.entries.values()) entry.controller.abort();
    this.entries.clear();
    if (visible) this.onChange();
  }

  ensure(scope, task) {
    const key = this.#key(scope, task.request_id), prior = this.entries.get(key);
    if (!this.#supported() || !this.#active(task)) { this.forget(scope, task.request_id); return; }
    if (prior && this.#current(prior)) {
      if (prior.running || prior.terminal || prior.retryAfter > Date.now()) return;
    } else if (prior) this.forget(scope, task.request_id);
    const manifest = this.auth.transport.capabilities;
    const entry = { scope: { ...scope }, requestID: task.request_id, inputDigest: task.input_digest,
      generation: this.auth.generation, lifetime: this.lifetime, session: manifest.session_id,
      authorizationRevision: manifest.authorization_revision, controller: new AbortController(), running: true };
    this.entries.set(key, entry);
    void this.#run(entry);
  }

  read(scope, requestID) {
    const entry = this.entries.get(this.#key(scope, requestID));
    return entry && this.#current(entry) && this.#active(this.store.request(scope, requestID)) ? entry.snapshot?.content : undefined;
  }

  forget(scope, requestID, notify = true) {
    const key = this.#key(scope, requestID), entry = this.entries.get(key);
    if (!entry) return;
    this.entries.delete(key); entry.controller.abort();
    if (notify && entry.snapshot?.content) this.onChange();
  }

  #supported() {
    const manifest = this.auth.transport?.capabilities;
    return !this.closed && this.auth.status.state === "connected" && this.auth.descriptor?.transport === "iscp" &&
      manifest?.schema_version === 2 && manifest.profile === "sparkclaw.workbench.transport.v2" &&
      Date.parse(manifest.expires_at) > Date.now() && manifest.operations?.includes("execution.progress");
  }
  #active(task) { return task.explicitly_submitted === 1 && ACTIVE.has(task.status); }
  #key(scope, requestID) { return JSON.stringify([identity(scope), requestID]); }
  #current(entry) {
    const manifest = this.auth.transport?.capabilities;
    return this.#supported() && !entry.controller.signal.aborted && entry.lifetime === this.lifetime &&
      entry.generation === this.auth.generation && identity(entry.scope) === identity(this.getIdentity()) &&
      entry.session === manifest.session_id && entry.authorizationRevision === manifest.authorization_revision &&
      this.entries.get(this.#key(entry.scope, entry.requestID)) === entry;
  }
  #same(entry) { if (!this.#current(entry)) throw new Error("Execution progress authentication changed"); }

  async #run(entry) {
    try {
      while (this.#current(entry)) {
        const started = latencyNow();
        traceLatency("execution.progress.start", { request_id: entry.requestID });
        const reply = await this.auth.invokeISCP("execution.progress", {
          input_digest: entry.inputDigest, after_revision: entry.snapshot?.revision ?? 0, wait_ms: 10000,
        }, { params: { request_id: entry.requestID }, requestID: entry.requestID, signal: entry.controller.signal });
        this.#same(entry);
        if (!this.#active(this.store.request(entry.scope, entry.requestID))) { this.forget(entry.scope, entry.requestID); return; }
        validate(reply, entry);
        const changed = !entry.snapshot || entry.snapshot.epoch !== reply.epoch || entry.snapshot.revision !== reply.revision;
        entry.snapshot = reply;
        traceLatency("execution.progress.received", { request_id: entry.requestID, state: reply.state,
          count: Buffer.byteLength(reply.content), duration_ms: latencyNow() - started });
        if (changed) this.onChange();
        if (TERMINAL.has(reply.state)) {
          entry.terminal = true;
          traceLatency("execution.progress.terminal", { request_id: entry.requestID, state: reply.state });
          await this.onTerminal(entry.scope, entry.requestID);
          return;
        }
        // A peer that returns immediately cannot turn an idle long poll into a
        // busy loop. Normal ten-second waits do not incur an additional delay.
        await pause(Math.max(0, 250 - (latencyNow() - started)), entry.controller.signal);
      }
    } catch {
      if (this.entries.get(this.#key(entry.scope, entry.requestID)) === entry) {
        const visible = Boolean(entry.snapshot?.content);
        entry.snapshot = undefined; entry.terminal = false; entry.retryAfter = Date.now() + 2000;
        if (visible) this.onChange();
        traceLatency("execution.progress.failed", { request_id: entry.requestID });
      }
    } finally {
      entry.running = false;
      traceLatency("execution.progress.end", { request_id: entry.requestID });
    }
  }
}

function validate(reply, entry) {
  if (!reply || Object.keys(reply).sort().join(",") !== keys || reply.schema_version !== 1 ||
      reply.request_id !== entry.requestID || reply.input_digest !== entry.inputDigest || !UUID.test(reply.epoch) ||
      !Number.isSafeInteger(reply.revision) || reply.revision < 1 || typeof reply.content !== "string" ||
      Buffer.byteLength(reply.content) > 8192 || Buffer.from(reply.content).toString("utf8") !== reply.content ||
      reply.content.includes("\0") || !/^[a-f0-9]{64}$/u.test(reply.digest) || digest(reply.content) !== reply.digest ||
      !STATES.has(reply.state) || typeof reply.truncated !== "boolean") throw new Error("Invalid execution progress");
  const previous = entry.snapshot;
  if (!previous || previous.epoch !== reply.epoch) return;
  if (reply.revision < previous.revision || reply.revision === previous.revision &&
      ["content", "digest", "state", "truncated"].some(key => previous[key] !== reply[key]) ||
      !TERMINAL.has(reply.state) && (!reply.content.startsWith(previous.content) || previous.truncated && (!reply.truncated || reply.content !== previous.content))) {
    throw new Error("Execution progress revision conflict");
  }
}

function pause(milliseconds, signal) {
  if (milliseconds <= 0) return Promise.resolve();
  return new Promise((resolve, reject) => {
    const finish = () => { signal.removeEventListener("abort", cancel); resolve(); };
    const timer = setTimeout(finish, milliseconds); timer.unref?.();
    const cancel = () => { clearTimeout(timer); signal.removeEventListener("abort", cancel); reject(new Error("Execution progress closed")); };
    signal.addEventListener("abort", cancel, { once: true });
    if (signal.aborted) cancel();
  });
}
