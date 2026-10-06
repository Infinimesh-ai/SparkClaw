const AUTH_GENERATION = Symbol("schedule_auth_generation");
const CANCELABLE = new Set(["submission_pending", "accepted", "running", "cancel_pending"]);

// The workbench owns both future definitions and due-time admission. The backend
// only sees ordinary execution requests, after an exclusive durable local claim.
export class ScheduleClient {
  constructor({ auth, store, execution, getIdentity, intervalMS = 1000, now = Date.now, onChange = () => {} }) {
    Object.assign(this, { auth, store, execution, getIdentity, intervalMS, now, onChange });
    this.operations = new Map();
    this.closed = true;
    this.epoch = 0;
  }

  start() {
    if (this.closed) { this.closed = false; this.epoch++; this.online = undefined; }
    this.#availability();
    if (!this.timer) this.timer = setInterval(() => void this.reconcilePending().catch(() => {}), this.intervalMS);
    this.timer.unref?.();
    void this.reconcilePending().catch(() => {});
    return this;
  }

  // Authentication loss and Electron powerMonitor suspend both break continuity.
  // A later start never inherits a due-time admission window from the prior run.
  close() { this.closed = true; this.epoch++; this.online = undefined; clearInterval(this.timer); this.timer = undefined; }

  async create(scope, conversationID, content, dueAt, intervalMS = 0) {
    scope = this.#boundScope(scope);
    this.#sameIdentity(scope);
    const schedule = this.store.schedule(scope, conversationID, content, dueAt, this.now(), intervalMS);
    this.onChange();
    return this.#view(scope, schedule.request_id);
  }

  // Explicit status checks obey the same availability horizon as timer ticks.
  // No future registration or renewal API exists.
  async reconcile(scope, requestID) {
    scope = this.#boundScope(scope);
    const available = this.#availability();
    return this.#serialized(scope, requestID, async () => {
      const schedule = this.store.scheduledRequest(scope, requestID);
      if (schedule.state === "saved" && Date.parse(schedule.due_at) <= this.now()) {
        if (!available || available !== this.#availability() || Date.parse(schedule.due_at) <= available.since) {
          this.store.missSchedule(scope, requestID, this.now());
        } else {
          const claim = this.store.claimSchedule(scope, requestID, this.now());
          if (claim) {
            this.onChange();
            try {
              await this.execution.submit(scope, requestID, { scheduleClaim: claim,
                canSubmit: () => { this.#sameIdentity(scope); return available === this.#availability(); } });
            } catch (error) {
              // An unconsumed capability proves dispatch never started. Once
              // consumed, an uncertain write stays on ordinary reconciliation.
              this.store.missUnsentScheduleClaim(scope, requestID, claim);
              throw error;
            }
          }
        }
      } else if (schedule.explicitly_submitted && available) {
        // Includes a crash between claim and POST and a lost admission response.
        // A 404 cannot turn the original claim back into a runnable occurrence.
        await this.execution.reconcile(scope, requestID);
      }
      this.onChange();
      return this.#view(scope, requestID);
    }).catch((error) => {
      // A disk or connection failure ends due-time availability. A later scan
      // cannot use the previous online horizon to catch up an unclaimed row.
      this.online = undefined;
      throw error;
    });
  }

  async cancel(scope, requestID) {
    scope = this.#boundScope(scope);
    return this.#serialized(scope, requestID, async () => {
      // Canceling a definition always cancels future occurrences locally, even
      // offline. Already admitted work retains ordinary cancellation semantics.
      const schedule = this.store.cancelSchedule(scope, requestID);
      this.onChange();
      if (schedule.explicitly_submitted && CANCELABLE.has(schedule.status)) await this.execution.cancel(scope, requestID);
      return this.#view(scope, requestID);
    });
  }

  async runNow(scope, requestID) {
    scope = this.#boundScope(scope);
    return this.#serialized(scope, requestID, async () => {
      const task = this.store.runScheduledNow(scope, requestID);
      this.onChange();
      await this.execution.submit(scope, task.request_id);
      return this.#view(scope, requestID);
    });
  }

  async reconcilePending() {
    if (this.closed || this.polling) return;
    this.#availability();
    const scope = this.getIdentity();
    if (!scope) return;
    this.polling = true;
    try {
      // Limit network concurrency; future rows are advanced atomically with each
      // claim. A continuously-online delayed tick can admit the due occurrences
      // in later ticks; a recovery scan instead compresses all missed due times.
      const rows = this.store.schedulePending(scope);
      for (let i = 0; i < rows.length && !this.closed; i += 4) {
        await Promise.allSettled(rows.slice(i, i + 4).map((row) => this.reconcile(scope, row.request_id)));
      }
    } finally { this.polling = false; }
  }

  #availability() {
    if (this.closed || this.auth.status.state !== "connected") { this.online = undefined; return undefined; }
    const identity = JSON.stringify(this.getIdentity());
    if (!this.online || this.online.generation !== this.auth.generation || this.online.identity !== identity || this.now() < this.online.since) {
      this.online = { since: this.now(), generation: this.auth.generation, identity, epoch: this.epoch };
    }
    return this.online;
  }
  #sameIdentity(scope) {
    const current = this.getIdentity();
    if (!current || JSON.stringify([scope.deployment_id, scope.owner_id, scope.client_id]) !==
        JSON.stringify([current.deployment_id, current.owner_id, current.client_id]) ||
        (Object.hasOwn(scope, AUTH_GENERATION) && scope[AUTH_GENERATION] !== this.auth.generation)) throw new Error("Schedule authentication changed");
  }
  #boundScope(scope) { return { ...scope, [AUTH_GENERATION]: this.auth.generation }; }
  #view(scope, requestID) {
    const schedule = this.store.scheduledRequest(scope, requestID);
    return Object.fromEntries(["request_id", "schedule_id", "due_at", "state", "interval_ms", "definition_state", "claimed_at", "missed_count", "missed_until", "recovery_request_id"]
      .map((key) => [key, schedule[key]]));
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
