import path from "node:path";
import { HostJournal } from "./host-journal.mjs";
import { ControllerAdmission } from "./controller-admission.mjs";
import { ControllerResults } from "./controller-results.mjs";
import { refreshBrowserFences, reconcileBrowserFence } from "./controller-receipts.mjs";
import { CONTROLLER_OPERATIONS, ID, exactObject, validateControllerBinding, validateControllerRequest, validateControllerLease,
  sameControllerBinding, sameControllerLease, wireBytes, positive } from "./controller-wire.mjs";

const PROFILE = "sparkclaw.workbench.transport.v2";
const WIRE_LIMIT = 63488;
const INLINE_LIMIT = 48 << 10;
const INLINE_RESULT = Symbol("inline-result-descriptor");

export class BrowserControllerForwarder {
  constructor({ auth, registry, runtime, userDataDir, onChange = () => {}, isTaskCanceled = () => false }) {
    Object.assign(this, { auth, registry, runtime, onChange, isTaskCanceled });
    this.journal = new HostJournal(path.join(userDataDir, "workbench/browser-journal"));
    this.remoteFences = new Map(); this.flights = new Map(); this.results = new ControllerResults();
    this.cancelledTasks = new Set();
    this.generation = 0; this.state = "ungranted"; this.admission = new ControllerAdmission();
    this.receiptTimeoutMS = 5000;
    this.authority = new AbortController(); this.lease = null;
    const stopped = runtime.onStopped;
    runtime.onStopped = reason => { this.#retireLease(); stopped?.(reason); this.onChange(); };
  }
  async start(scope) {
    await this.stop();
    await this.journal.load();
    this.scope = Object.freeze({ installation_id: scope?.installation_id, owner_id: this.auth.connection?.ownerID, client_id: this.auth.connection?.clientID });
    if (Object.values(this.scope).some(value => !ID.test(value))) throw new Error("Browser client identity is unavailable");
    await this.runtime.prepare();
    return this.snapshot();
  }
  snapshot() {
    const rows = new Map();
    for (const row of this.scope ? this.journal.unknown(this.scope) : []) rows.set(row.command_id, row);
    for (const row of this.journal.rows.values()) if (row.state === "dispatched" && this.flights.get(row.command_id)?.terminal &&
      ["owner_id", "client_id", "installation_id"].every(key => row[key] === this.scope?.[key])) rows.set(row.command_id, row);
    for (const [id, row] of this.remoteFences) rows.set(id, { ...row.scope, command_id: id, digest: row.digest });
    return { state: this.state, role: "client_embedded", unknown_writes: [...rows.values()].map(row => ({
      command_id: row.command_id, digest: row.digest, local_conversation_id: row.local_conversation_id, local_task_id: row.local_task_id })) };
  }
  autoConnect() { void this.#connect(this.auth.transport?.capabilities).catch(() => {}); }
  cancelTask(binding) {
    if (!this.scope || !["owner_id", "client_id", "installation_id"].every(key => binding?.[key] === this.scope[key]) ||
        !ID.test(binding?.local_conversation_id) || !ID.test(binding?.local_task_id)) return;
    const key = taskKey(binding);
    if (this.cancelledTasks.size >= 1024 && !this.cancelledTasks.has(key)) {
      this.state = "unavailable"; this.authority.abort(); this.admission.close();
    } else this.cancelledTasks.add(key);
    for (const flight of this.flights.values()) if (flight.taskKey === key) flight.abort.abort();
    if (this.state === "unavailable" || this.lease && taskKey(this.lease.binding) === key) {
      const generation = this.generation;
      const failed = () => {
        if (generation !== this.generation) return;
        this.state = "unavailable"; this.authority.abort(); this.admission.close(); this.onChange();
      };
      try { return this.runtime.stop("task_canceled").catch(failed); }
      catch { failed(); }
    }
  }
  async stop() { await this.suspend(); this.scope = undefined; this.remoteFences.clear(); this.state = "ungranted"; }
  async suspend() {
    this.generation++; this.authority.abort(); this.admission.close();
    this.session = undefined; this.state = "suspended";
    this.results.clear(); this.flights.clear();
    await this.runtime.stop("authority_changed");
    this.registry.closeHostPages("host_disconnected"); this.onChange();
  }
  refreshFences() { return refreshBrowserFences(this, () => { this.state = "reconciliation_conflict"; this.authority.abort(); void this.runtime.stop("reconciliation_conflict").catch(() => {}); }); }
  async reconcile(commandID, digest, outcome) {
    const local = this.journal.rows.get(commandID), remote = this.remoteFences.get(commandID);
    const row = local || (remote && { ...remote.scope, digest: remote.digest, state: "unknown" });
    if (row?.digest === digest && (row.state === "unknown" || remote?.digest === digest) &&
      ["observed_completed", "observed_not_applied"].includes(outcome) && this.lease?.binding.local_conversation_id === row.local_conversation_id) await this.runtime.stop("reconciliation");
    return reconcileBrowserFence(this, commandID, digest, outcome);
  }

  #authorized(session, bootstrap = false) {
    const manifest = this.auth.transport?.capabilities;
    return this.scope && this.auth.descriptor?.transport === "iscp" && this.auth.status.state === "connected" &&
      this.scope.owner_id === this.auth.connection?.ownerID && this.scope.client_id === this.auth.connection?.clientID &&
      ID.test(session?.session_id) && Number.isSafeInteger(session?.authorization_revision) && session.authorization_revision > 0 &&
      manifest?.session_id === session.session_id && manifest.authorization_revision === session.authorization_revision &&
      Date.parse(manifest.expires_at) > Date.now() && CONTROLLER_OPERATIONS.every(operation => manifest.operations?.includes(operation)) &&
      (bootstrap || this.auth.status.capabilities?.browser === true);
  }
  async #connect(session) {
    if (!this.#authorized(session, true)) throw failure("capability_unavailable");
    if (this.connecting) { await this.connecting; return this.#connect(session); }
    if (this.session?.session_id === session.session_id && this.session.authorization_revision === session.authorization_revision) {
      if (this.state !== "connected") throw failure("capability_unavailable");
      return;
    }
    this.connecting = (async () => {
      await this.suspend();
      if (!this.#authorized(session, true)) throw failure("capability_unavailable");
      this.authority = new AbortController(); this.admission = new ControllerAdmission();
      this.cancelledTasks.clear();
      this.session = Object.freeze({ session_id: session.session_id, authorization_revision: session.authorization_revision });
      this.state = "connected";
      try { await this.refreshFences(); }
      catch (error) {
        if (this.state !== "reconciliation_conflict") { this.state = "unavailable"; this.session = undefined; }
        throw error;
      }
    })().finally(() => { this.connecting = null; });
    return this.connecting;
  }

  async handleReverse(request, session) {
    const received = performance.now();
    if (!CONTROLLER_OPERATIONS.includes(request?.operation) || request.profile !== PROFILE) return rejected(403, "permission_denied");
    if (request.installation_id !== this.scope?.installation_id || !this.#authorized(session, request.operation.endsWith(".status"))) return rejected(503, "capability_unavailable");
    let mutation;
    try { mutation = validateControllerRequest(request.operation, request.body); }
    catch { return rejected(400, "invalid_request"); }
    try {
      if (request.operation !== "browser.controller.status") validateControllerBinding(request.body.binding, this.scope, session);
      await this.#connect(session);
    } catch { return rejected(503, "capability_unavailable"); }
    const limit = Math.min(WIRE_LIMIT, this.auth.transport.capabilities.limits?.body_bytes || WIRE_LIMIT,
      this.auth.transport.capabilities.limits?.response_bytes || WIRE_LIMIT);
    if (wireBytes(request) > limit) return rejected(413, "invalid_request");
    if (request.operation.endsWith(".status")) {
      const status = this.runtime.snapshot();
      const body = { schema_version: 1, ...status, limits: { ...status.limits, wire_bytes: limit, cache_bytes: 20 << 20,
        chunk_bytes: Math.max(1, Math.min(32 << 10, Math.floor((limit - 2048) * 3 / 4))),
        chunk_concurrency: Math.min(4, this.auth.transport.capabilities.limits?.concurrent || 4) } };
      return fits(request, body, limit) ? { status: 200, body } : rejected(413, "result_budget_exceeded");
    }
    if (request.operation.endsWith(".chunk")) {
      try {
        const body = this.results.read(request.body);
        if (!fits(request, body, limit)) return rejected(413, "result_budget_exceeded");
        return { status: 200, body };
      } catch { return rejected(404, "browser_result_unavailable"); }
    }
    const command = request.body;
    const prior = this.flights.get(command.command_id);
    if (prior) {
      if (prior.digest !== command.digest) return { status: 200, body: result(command, this.runtime.incarnation, "not_dispatched", "command_fenced") };
      if (prior.promise) return { status: 200, body: await prior.promise };
      if (prior.inline) {
        try { return { status: 200, body: { ...prior.reply, payload: this.results.inline(command, prior.inline) } }; }
        catch { return { status: 200, body: { ...prior.reply, error_code: "browser_result_unavailable" } }; }
      }
      return { status: 200, body: prior.reply };
    }
    if (mutation && this.journal.rows.has(command.command_id)) {
      const row = this.journal.rows.get(command.command_id);
      const outcome = row.digest !== command.digest ? "not_dispatched" : row.state === "completed" || row.state === "observed_completed" ? "completed" : row.state === "failed" || row.state === "observed_not_applied" ? "failed" : "unknown";
      return { status: 200, body: result(command, this.runtime.incarnation, outcome, row.digest !== command.digest ? "command_fenced" : "browser_result_unavailable") };
    }
    // Publish a command slot before any await, including the durable journal.
    // Terminal slots have bounded TTL; mutations remain fenced by the journal.
    if (this.flights.size >= 1024) return { status: 200, body: result(command, this.runtime.incarnation, "not_dispatched", "browser_busy") };
    const flight = { digest: command.digest, taskKey: taskKey(command.binding), abort: new AbortController() };
    if (this.cancelledTasks.has(flight.taskKey)) flight.abort.abort();
    this.flights.set(command.command_id, flight);
    const generation = this.generation;
    const deadline = received + command.timeout_ms;
    const pending = Promise.resolve().then(() => this.#dispatch(request, { mutation, generation, deadline, limit, signal: flight.abort.signal }))
      .catch(() => result(command, this.runtime.incarnation, "unknown", "browser_write_outcome_unknown"))
      .then(body => {
        flight.inline = body[INLINE_RESULT];
        delete body[INLINE_RESULT];
        flight.reply = { ...body };
        if (flight.inline) delete flight.reply.payload;
        flight.terminal = true; flight.promise = null; this.onChange();
        return body;
      });
    flight.promise = pending;
    void pending.finally(() => {
      const timer = setTimeout(() => { if (this.flights.get(command.command_id) === flight) this.flights.delete(command.command_id); }, 60000);
      timer.unref?.();
    });
    return { status: 200, body: await pending };
  }

  async #dispatch(request, context) {
    const command = request.body, action = request.operation.slice("browser.controller.".length);
    const signal = AbortSignal.any([this.authority.signal, context.signal, AbortSignal.timeout(Math.max(1, Math.ceil(context.deadline - performance.now())))]);
    let begun = false, dispatched = false, completed = false, reservation, acquiredSlot, task, local, incarnation = this.runtime.incarnation;
    const current = () => {
      if (signal.aborted || performance.now() >= context.deadline || context.generation !== this.generation || this.state !== "connected" || !this.#authorized(this.session)) throw failure("command_fenced");
      if (this.isTaskCanceled(command.binding)) throw failure("command_fenced");
      if (context.mutation && action !== "release" && this.#hasUnknown(command.binding, command.command_id)) throw failure("browser_write_outcome_unknown");
    };
    const work = async () => {
      current();
      if (action === "acquire") {
        acquiredSlot = await this.admission.acquire({ deadline: context.deadline, waitMS: command.wait_timeout_ms, signal });
        current();
        await abortable(this.runtime.start(), signal);
        current(); incarnation = this.runtime.incarnation;
        task = this.runtime.adapter.prepareControllerTask(command.binding, incarnation);
        local = { profile_id: "client_embedded", lane: "mcp", task_id: task.taskID, token: task.token,
          credential_generation: this.session.authorization_revision, wait_timeout_ms: 0, session_ttl_ms: command.session_ttl_ms };
      } else {
        const active = this.lease;
        // Release depends on stable session identity, including after a completed
        // action whose payload/page generation was lost during chunk transfer.
        const supplied = action === "release" && active ? { ...command.lease, page_generation: active.value.page_generation } : command.lease;
        if (!active || !sameControllerBinding(active.binding, command.binding) || !sameControllerLease(active.value, supplied) ||
            active.incarnation !== this.runtime.incarnation || Date.parse(active.value.expires_at) <= Date.now()) throw failure("browser_session_stale");
        incarnation = active.incarnation;
        local = { session_id: active.value.session_id, controller_generation: active.value.controller_generation, session_generation: active.value.session_generation };
        if (action === "execute") {
          reservation = this.results.reserve();
          if (!reservation) throw failure("browser_result_capacity");
          Object.assign(local, { page_generation: active.value.page_generation, operation: command.operation, arguments: command.arguments });
        }
      }
      if (wireBytes(local) > 48 << 10) throw failure("invalid_request");
      current();
      if (context.mutation) {
        if (await this.journal.begin(command) !== "new") throw failure("command_fenced");
        begun = true;
      }
      current();
      if (action === "release") this.runtime.adapter.beginControllerRelease(this.lease.taskID);
      dispatched = true;
      const response = await this.runtime.request(action, local, { signal, timeoutMS: Math.max(1, Math.min(25000, Math.floor(context.deadline - performance.now()))) });
      if (local.token) local.token = "";
      current();
      if (response.status < 200 || response.status >= 300) throw failure("browser_controller_unavailable");
      const payload = response.payload;
      if (action === "acquire") {
        validateControllerLease(payload);
        if (payload.controller_generation !== this.runtime.controllerGeneration || payload.credential_generation !== this.session.authorization_revision ||
            Date.parse(payload.expires_at) <= Date.now() || Date.parse(payload.expires_at) > Date.now() + command.session_ttl_ms + 1000) throw failure("browser_controller_unavailable");
        this.lease = { binding: command.binding, value: payload, taskID: task.taskID, incarnation, slot: acquiredSlot, tail: Promise.resolve(), pending: 0 };
        this.lease.timer = setTimeout(() => { void this.runtime.stop("controller_lease_expired").catch(() => {}); }, Math.max(1, Date.parse(payload.expires_at) - Date.now()));
        this.lease.timer.unref?.();
        this.runtime.state = "busy";
      } else if (action === "execute") {
        validateExecution(payload, command, this.lease.value, context.mutation);
        if (payload.result && typeof payload.result === "object" && !Array.isArray(payload.result)) {
          payload.result.surface_visible = this.runtime.adapter.controllerSurfaceVisible?.(this.lease.taskID) === true;
        }
        this.lease.value = { ...this.lease.value, page_generation: payload.page_generation };
      } else {
        exactObject(payload, ["schema_version", "state", "profile_id", "controller_generation", "session_generation"]);
        if (payload?.schema_version !== 1 || payload.state !== "released" || payload.profile_id !== "client_embedded" || payload.controller_generation !== command.lease.controller_generation || payload.session_generation !== command.lease.session_generation) throw failure("browser_controller_unavailable");
        this.runtime.adapter.revokeControllerTask(this.lease.taskID, "normal_release");
      }
      if (context.mutation) await this.journal.finish(command.command_id, "completed");
      completed = true;
      if (action === "release") { this.#retireLease(); this.runtime.state = "ready"; }
      const inline = { ...result(command, incarnation, "completed"), payload };
      // Store immutable bytes even when sending inline, to account for retained
      // execute data. Reserve release/status/acquire independently of this cache.
      const descriptor = reservation ? this.results.put(reservation, command, payload) : null;
      if (wireBytes(payload) <= INLINE_LIMIT && fits(request, inline, context.limit)) {
        if (descriptor) inline[INLINE_RESULT] = descriptor;
        return inline;
      }
      const chunked = { ...result(command, incarnation, "completed"), result: descriptor };
      if (!descriptor || !fits(request, chunked, context.limit)) throw failure("browser_result_unavailable");
      return chunked;
    };
    try {
      if (action === "acquire") return await work();
      const active = this.lease;
      if (!active || active.pending >= 32) throw failure("browser_busy");
      active.pending++;
      const previous = active.tail;
      let unlock;
      const finished = new Promise(resolve => { unlock = resolve; });
      active.tail = previous.catch(() => {}).then(() => finished);
      try { await abortable(previous, signal); return await work(); }
      finally { active.pending--; unlock(); }
    } catch (error) {
      // A local timeout must revoke the adapter and stop owned execution. It
      // cannot leave a delayed write running behind a returned error.
      if (begun && !completed) await this.journal.finish(command.command_id, dispatched ? "unknown" : "failed").catch(() => this.journal.markUncertain(command.command_id));
      // Authority loss/retirement already cleans the old process. A late reply
      // must still settle its journal, but cannot tear down a replacement.
      const ownsRuntime = context.generation === this.generation && (!incarnation || incarnation === this.runtime.incarnation);
      if (ownsRuntime && (dispatched && !completed || action === "acquire" && acquiredSlot && !completed)) {
        await this.runtime.stop("controller_call_failed").catch(() => {});
      }
      if (ownsRuntime && task && !this.lease) this.runtime.adapter?.revokeControllerTask(task.taskID);
      if (acquiredSlot && this.lease?.slot !== acquiredSlot && !this.runtime.closed) this.admission.release(acquiredSlot);
      const outcome = completed ? "completed" : dispatched && context.mutation ? "unknown" : dispatched ? "failed" : "not_dispatched";
      return result(command, incarnation, outcome, completed ? "browser_result_unavailable" : error.code || (dispatched ? "browser_write_outcome_unknown" : "browser_busy"));
    } finally {
      if (local?.token) local.token = "";
      if (task?.token) task.token = "";
      if (reservation) this.results.release(reservation);
      this.onChange();
    }
  }

  #hasUnknown(binding, exceptID) {
    return [...this.journal.rows.values()].some(row => ["unknown", "dispatched"].includes(row.state) && row.command_id !== exceptID &&
      row.owner_id === binding.owner_id && row.client_id === binding.client_id && row.installation_id === binding.installation_id && row.local_conversation_id === binding.local_conversation_id) ||
      [...this.remoteFences.values()].some(row => row.scope.local_conversation_id === binding.local_conversation_id);
  }
  #retireLease() {
    const active = this.lease; this.lease = null;
    if (active) {
      clearTimeout(active.timer);
      const admission = this.admission;
      // onStopped runs inside cleanup, before the supervisor's stop promise
      // settles. A queued acquire must not start in that final stopping window.
      if (this.runtime.stopping) void this.runtime.stopping.then(() => admission.release(active.slot), () => {});
      else admission.release(active.slot);
    }
  }
}

function taskKey(binding) {
  return JSON.stringify([binding.owner_id, binding.client_id, binding.installation_id, binding.local_conversation_id, binding.local_task_id]);
}

function validateExecution(payload, command, lease, mutation) {
  exactObject(payload, ["schema_version", "state", "profile_id", "lane", "session_id", "credential_generation", "controller_generation", "session_generation", "page_generation", "operation", "result"]);
  if (!payload || payload.schema_version !== 1 || payload.state !== "completed" || payload.profile_id !== "client_embedded" || payload.lane !== "mcp" ||
      ["session_id", "credential_generation", "controller_generation", "session_generation"].some(key => payload[key] !== lease[key]) ||
      payload.operation !== command.operation || !Object.hasOwn(payload, "result")) throw failure("browser_controller_unavailable");
  positive(payload.page_generation);
  if (payload.page_generation !== lease.page_generation + (mutation ? 1 : 0)) throw failure("browser_page_stale");
}
function failure(code) { return Object.assign(new Error(code), { code }); }
function rejected(status, code) {
  const wireCode = status === 413 ? "resource_limit" : status === 404 ? "not_found" : code;
  return { status, error_code: wireCode, error: "Browser Controller request is unavailable" };
}
function result(command, incarnation, outcome, errorCode) {
  return { schema_version: 1, command_id: command.command_id, digest: command.digest, binding: command.binding,
    supervisor_incarnation: incarnation, outcome, ...(errorCode ? { error_code: errorCode } : {}) };
}
function fits(request, body, limit) { return wireBytes({ type: "task.result", profile: request.profile, id: request.id, status: 200, body }) <= limit; }
function abortable(promise, signal) {
  if (signal.aborted) return Promise.reject(failure("browser_controller_timeout"));
  return new Promise((resolve, reject) => {
    const abort = () => reject(failure("browser_controller_timeout"));
    signal.addEventListener("abort", abort, { once: true });
    promise.then(resolve, reject).finally(() => signal.removeEventListener("abort", abort));
  });
}
