const ACTIVE = new Set(["saved", "registering", "leased"]);
const EXECUTION = new Set(["accepted", "running", "completed", "delivered", "unknown", "delivery_expired", "failed", "canceled"]);
const AUTH_GENERATION = Symbol("schedule_auth_generation");

// Single-run definitions are client-owned. The server sees only a renewable,
// memory-only lease; offline/expired leases never promise a future execution.
export class ScheduleClient {
  constructor({ auth, store, execution, getIdentity, intervalMS = 10000, now = Date.now, onChange = () => {} }) {
    Object.assign(this, { auth, store, execution, getIdentity, intervalMS, now, onChange });
    this.operations = new Map();
    this.controller = new AbortController();
    this.closed = false;
  }
  start() {
    this.closed = false;
    if (this.controller.signal.aborted) this.controller = new AbortController();
    if (!this.timer) this.timer = setInterval(() => void this.reconcilePending().catch(() => {}), this.intervalMS);
    this.timer.unref?.();
    void this.reconcilePending().catch(() => {});
    return this;
  }
  close() { this.closed = true; this.controller.abort(); clearInterval(this.timer); this.timer = undefined; }

  async create(scope, conversationID, content, dueAt) {
    scope = this.#boundScope(scope);
    this.#sameIdentity(scope);
    const schedule = this.store.schedule(scope, conversationID, content, dueAt, this.now());
    this.onChange();
    // Persistence precedes all remote registration, including an offline save.
    if (this.auth.status.state === "connected") {
      try { await this.reconcile(scope, schedule.request_id); }
      catch (error) {
        this.#sameIdentity(scope);
        // The explicit definition is already committed. A lost registration
        // leaves its same ID for lookup/renew; returning it prevents UI retries
        // from accidentally creating a second scheduled definition.
      }
    }
    return this.#view(scope, schedule.request_id);
  }

  async reconcile(scope, requestID) {
    scope = this.#boundScope(scope);
    return this.#serialized(scope, requestID, async () => {
      let schedule = this.store.scheduledRequest(scope, requestID);
      if (!ACTIVE.has(schedule.state) && schedule.state !== "cancel_pending") return this.#view(scope, requestID);
      if (this.closed || this.auth.status.state !== "connected") return this.#view(scope, requestID);
      // Lookup before any registration/renew after restart prevents two fires.
      if (schedule.explicitly_submitted && await this.#lookupExecution(scope, schedule)) return this.#view(scope, requestID);
      const leaseValid = schedule.lease_expires_at && Date.parse(schedule.lease_expires_at) > this.now();
      if (schedule.state === "cancel_pending") {
        if (!leaseValid) this.store.setScheduleState(scope, requestID, "canceled");
        return this.#view(scope, requestID);
      }
      if (Date.parse(schedule.due_at) <= this.now() && !leaseValid) {
        this.store.setScheduleState(scope, requestID, "missed");
        this.onChange();
        return this.#view(scope, requestID);
      }
      if (schedule.state === "leased") {
        const response = await this.#fetch(scope, `/api/r3/schedules/${requestID}/renew`, {});
        if (response.status !== 404) {
          await this.#accept(scope, schedule, response);
          return this.#view(scope, requestID);
        }
        await response.body?.cancel();
        if (await this.#lookupExecution(scope, schedule)) return this.#view(scope, requestID);
        if (Date.parse(schedule.due_at) <= this.now()) {
          this.store.setScheduleState(scope, requestID, "missed");
          this.onChange();
          return this.#view(scope, requestID);
        }
      }
      schedule = this.store.markScheduleRegistered(scope, requestID);
      const response = await this.#fetch(scope, "/api/r3/schedules/lease", {
        schema_version: 1, due_at: schedule.due_at, context: schedule.context_json, digest: schedule.input_digest,
      });
      await this.#accept(scope, schedule, response);
      return this.#view(scope, requestID);
    });
  }

  async cancel(scope, requestID) {
    scope = this.#boundScope(scope);
    return this.#serialized(scope, requestID, async () => {
      const schedule = this.store.scheduledRequest(scope, requestID);
      if (!ACTIVE.has(schedule.state) && schedule.state !== "cancel_pending") throw new Error("Schedule cannot be canceled");
      this.store.setScheduleState(scope, requestID, "cancel_pending", schedule.lease_expires_at);
      if (!schedule.explicitly_submitted) {
        this.store.setScheduleState(scope, requestID, "canceled"); this.onChange(); return this.#view(scope, requestID);
      }
      const response = await this.#fetch(scope, `/api/r3/schedules/${requestID}/cancel`, {});
      if (response.status === 404) {
        await response.body?.cancel();
        if (!await this.#lookupExecution(scope, schedule)) this.store.setScheduleState(scope, requestID, "canceled");
      } else await this.#accept(scope, schedule, response);
      this.onChange();
      return this.#view(scope, requestID);
    });
  }

  async runNow(scope, requestID) {
    scope = this.#boundScope(scope);
    return this.#serialized(scope, requestID, async () => {
      const schedule = this.store.scheduledRequest(scope, requestID);
      if (!["missed", "admission_rejected"].includes(schedule.state)) throw new Error("Only an unexecuted missed schedule can run now");
      const task = this.store.runScheduledNow(scope, requestID);
      this.onChange();
      await this.execution.submit(scope, task.request_id);
      return this.#view(scope, requestID);
    });
  }

  async reconcilePending() {
    if (this.closed || this.polling || this.auth.status.state !== "connected") return;
    const scope = this.getIdentity();
    if (!scope) return;
    this.polling = true;
    try {
      const rows = this.store.schedulePending(scope);
      for (let i = 0; i < rows.length && !this.closed; i += 4) {
        await Promise.allSettled(rows.slice(i, i + 4).map((row) => this.reconcile(scope, row.request_id)));
      }
    } finally { this.polling = false; }
  }

  async #accept(scope, schedule, response) {
    if (!response.ok) { await response.body?.cancel(); throw new Error("Schedule lease is unavailable; the local definition is preserved"); }
    const event = await json(response);
    this.#sameIdentity(scope);
    if (event?.schema_version !== 1 || event.request_id !== schedule.request_id) throw new Error("Schedule response identity mismatch");
    if (event.state === "leased") {
      const expires = Date.parse(event.lease_expires_at);
      if (!Number.isFinite(expires) || expires <= this.now() || expires > this.now() + 60000) throw new Error("Invalid schedule lease deadline");
      this.store.setScheduleState(scope, schedule.request_id, "leased", event.lease_expires_at);
    } else if (event.state === "admission_rejected" || event.state === "canceled") {
      this.store.setScheduleState(scope, schedule.request_id, event.state);
    } else if (EXECUTION.has(event.state)) {
      await this.execution.reconcile(scope, schedule.request_id);
      this.#recordExecution(scope, schedule.request_id, event.state);
    } else throw new Error("Invalid schedule lease state");
    this.onChange();
  }

  async #lookupExecution(scope, schedule) {
    this.#sameIdentity(scope);
    const response = await this.auth.authorizedR3Fetch(`${this.auth.descriptor.origin}/api/r3/executions/${schedule.request_id}`, {
      headers: { "X-SparkClaw-Installation": this.store.installationID },
      signal: this.controller.signal,
    });
    if (response.status === 404) { await response.body?.cancel(); return false; }
    if (!response.ok) { await response.body?.cancel(); throw new Error("Scheduled execution status is unavailable"); }
    const event = await json(response);
    this.#sameIdentity(scope);
    if (event?.schema_version !== 1 || event.request_id !== schedule.request_id || event.input_digest !== schedule.input_digest || !EXECUTION.has(event.state)) throw new Error("Scheduled execution identity mismatch");
    await this.execution.reconcile(scope, schedule.request_id);
    this.#recordExecution(scope, schedule.request_id, event.state);
    this.onChange();
    return true;
  }

  #fetch(scope, route, value) {
    this.#sameIdentity(scope);
    if (this.closed || this.auth.status.state !== "connected") throw new Error("Schedule backend is unavailable; the local definition is preserved");
    return this.auth.authorizedR3Fetch(`${this.auth.descriptor.origin}${route}`, {
      method: "POST", headers: { "Content-Type": "application/json", "X-SparkClaw-Installation": this.store.installationID }, body: JSON.stringify(value),
      signal: this.controller.signal,
    });
  }
  #recordExecution(scope, requestID, state) {
    const task = this.store.request(scope, requestID);
    this.store.setScheduleState(scope, requestID, task.status === "delivered" ? "delivered" : task.status === "saved" ? "completed" : state);
  }
  #sameIdentity(scope) {
    const current = this.getIdentity();
    if (this.closed || !current || JSON.stringify([scope.deployment_id, scope.owner_id, scope.client_id]) !==
        JSON.stringify([current.deployment_id, current.owner_id, current.client_id]) ||
        (Object.hasOwn(scope, AUTH_GENERATION) && scope[AUTH_GENERATION] !== this.auth.generation)) throw new Error("Schedule authentication changed");
  }
  #boundScope(scope) { return { ...scope, [AUTH_GENERATION]: this.auth.generation }; }
  #view(scope, requestID) {
    const schedule = this.store.scheduledRequest(scope, requestID);
    return { request_id: requestID, due_at: schedule.due_at, state: schedule.state, lease_expires_at: schedule.lease_expires_at };
  }
  #serialized(scope, requestID, operation) {
    this.#sameIdentity(scope);
    this.store.scheduledRequest(scope, requestID);
    const key = JSON.stringify([scope, requestID]);
    const pending = (this.operations.get(key) || Promise.resolve()).catch(() => {}).then(() => { this.#sameIdentity(scope); return operation(); });
    this.operations.set(key, pending);
    return pending.finally(() => { if (this.operations.get(key) === pending) this.operations.delete(key); });
  }
}

async function json(response) {
  const bytes = new Uint8Array(await response.arrayBuffer());
  if (bytes.byteLength > 8 * 1024 * 1024) throw new Error("Schedule response exceeds result budget");
  return JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(bytes));
}
