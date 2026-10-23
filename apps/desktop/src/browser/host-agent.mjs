import crypto from "node:crypto";
import path from "node:path";
import { connectPinnedHostSocket } from "./host-socket.mjs";
import { HostJournal } from "./host-journal.mjs";
import { nativePageCommand } from "./native-page-commands.mjs";
import { isTLSIdentityError } from "../main/pinned-https.mjs";

const ID = /^[A-Za-z0-9][A-Za-z0-9_.:-]{0,159}$/u;
const HASH = /^[a-f0-9]{64}$/u;
const BINDING_KEYS = ["owner_id", "client_id", "installation_id", "local_conversation_id", "local_task_id", "host_id", "runtime_generation", "connection_epoch", "lease_id", "page_id", "page_generation", "authorization_digest", "lease_expires_at"];
const OPS = { acquire: [], release: [], navigate: ["url"], read: ["max_chars"], snapshot: [], click: ["ref", "snapshot_id"], fill: ["ref", "snapshot_id", "value"], select: ["ref", "snapshot_id", "value"], screenshot: [], wait: ["milliseconds", "mode", "expected_url", "before_digest", "allow_no_change", "require_ready_state", "timeout_ms"] };
const WRITE = new Set(["click", "fill", "select"]);
// A verified desktop connection enables its own embedded browser automatically
// on both ISCP and pinned HTTPS LAN. Invalid commands and unknown writes retain
// their existing fences; connecting never authorizes replaying a website write.
const AUTO_GRANT_STATES = new Set(["ungranted", "suspended", "reconnecting", "disconnected", "lease_expired", "grant_expired", "unavailable"]);
const AUTO_GRANT_MAX_DELAY_MS = 60000;
// One reverse command must answer before the 30 s ISCP call timeout, so a
// slow page reports its own outcome instead of leaving it to the transport.
export const ISCP_COMMAND_DEADLINE_MS = 25000;
// The whole reverse response, with binding and envelope, must fit one 64 KiB
// ISCP message after the helper's JSON escaping.
export const RESULT_BUDGET_BYTES = 56 << 10;
// ISCP leases follow the task and its session; they carry no expiry.
const ISCP_NO_EXPIRY = "0001-01-01T00:00:00Z";

export class BrowserHostAgent {
  constructor({ auth, registry, userDataDir, onChange = () => {}, connect = connectPinnedHostSocket, execute = nativePageCommand, commandDeadlineMS = ISCP_COMMAND_DEADLINE_MS }) {
    Object.assign(this, { auth, registry, onChange, connect, execute, commandDeadlineMS });
    this.journal = new HostJournal(path.join(userDataDir, "workbench", "browser-journal"));
    this.runtime = registry.runtimeGeneration;
    this.state = "ungranted";
    this.generation = 0;
    this.autoConnectRevision = 0;
    this.leases = new Map(); this.queues = new Map(); this.remoteFences = new Map();
  }
  async start(scope) {
    if (!ID.test(scope?.installation_id)) throw new Error("Browser installation is unavailable");
    await this.stop();
    await this.journal.load();
    this.scope = Object.freeze({ installation_id: scope.installation_id, owner_id: this.auth.connection?.ownerID, client_id: this.auth.connection?.clientID });
    if (!ID.test(this.scope.owner_id) || !ID.test(this.scope.client_id)) throw new Error("Browser client identity is unavailable");
    this.#state("ungranted"); return this.snapshot();
  }
  snapshot() {
    const rows = new Map();
    for (const row of this.scope ? this.journal.unknown(this.scope) : []) rows.set(row.command_id, row);
    for (const [id, fence] of this.remoteFences) rows.set(id, { ...fence.scope, command_id: id, digest: fence.digest });
    return { state: this.state, role: "client_embedded", unknown_writes: [...rows.values()].map((row) => ({ command_id: row.command_id, digest: row.digest, local_conversation_id: row.local_conversation_id, local_task_id: row.local_task_id })) };
  }
  // Main-owned connection setup. Credentials and host grants never cross IPC
  // or become local renderer configuration.
  async grant() {
    if (this.#iscp()) throw new Error("ISCP browser commands use the authenticated device session");
    if (!this.#browserReady()) throw new Error("Browser host requires a connected desktop identity and browser capability");
    const authority = { scope: this.scope, authGeneration: this.auth.generation, connection: this.auth.connection, runtime: this.runtime };
    if (this.grantFlight && this.#sameGrantAuthority(this.grantFlight.authority)) return this.grantFlight.promise;
    if (this.state === "connected" && this.socket && !this.abort?.signal.aborted && Date.parse(this.grantRecord?.expires_at) > Date.now() && this.connectedAuthority && this.#sameGrantAuthority(this.connectedAuthority)) return this.snapshot();
    const flight = { authority };
    this.grantFlight = flight;
    // Publish the flight before any onChange callback can reenter grant().
    flight.promise = Promise.resolve().then(() => this.#grant(authority));
    try { return await flight.promise; }
    finally { if (this.grantFlight === flight) this.grantFlight = undefined; }
  }
  #sameGrantAuthority(authority) {
    return authority.scope === this.scope && authority.authGeneration === this.auth.generation && authority.connection === this.auth.connection && authority.runtime === this.runtime;
  }
  async #grant(authority) {
    if (!this.#sameGrantAuthority(authority)) throw new Error("Browser host was fenced");
    const generation = ++this.generation;
    const current = () => generation === this.generation && this.#sameGrantAuthority(authority);
    this.#disconnect("reconnecting");
    const response = await this.#fetch("browser.host.grant", "/api/v1/browser/hosts/grants", {
      method: "POST", headers: { "content-type": "application/json", "X-SparkClaw-Installation": this.scope.installation_id }, body: "{}",
    });
    if (!response.ok) throw new Error("Browser host permission was rejected");
    const grant = await response.json();
    if (!current() || !ID.test(grant.host_id) || typeof grant.grant_token !== "string" || !HASH.test(grant.grant_digest) || !Number.isFinite(Date.parse(grant.expires_at))) throw new Error("Browser host grant is invalid");
    this.grantRecord = grant;
    const abort = new AbortController(); this.abort = abort;
    try {
      const socket = await this.connect(this.auth.descriptor, {
        Authorization: this.auth.connection.authorization,
        "X-SparkClaw-Installation": this.scope.installation_id,
        "X-SparkClaw-Host-ID": grant.host_id,
        "X-SparkClaw-Host-Grant": grant.grant_token,
        "X-SparkClaw-Runtime": this.runtime,
      }, { signal: abort.signal });
      if (!current()) { socket.close(); throw new Error("Browser host was fenced"); }
      this.socket = socket;
      await new Promise((resolve, reject) => {
        let welcomed = false;
        const timeout = setTimeout(() => { socket.close(); reject(new Error("Browser host handshake timed out")); }, 10000);
        socket.on("close", () => { clearTimeout(timeout); if (!welcomed) reject(new Error("Browser host handshake failed")); if (generation === this.generation) this.#disconnect("disconnected"); });
        socket.on("message", (message) => {
          if (!current()) return;
          if (!welcomed) {
            if (message?.schema_version !== 1 || message.type !== "welcome" || message.host_id !== grant.host_id || message.runtime_generation !== this.runtime || message.authorization_digest !== grant.grant_digest || !ID.test(message.connection_epoch) || message.lease_seconds !== 30 || message.heartbeat_seconds !== 10) { socket.close(); reject(new Error("Browser host welcome is invalid")); return; }
            welcomed = true; clearTimeout(timeout); this.epoch = message.connection_epoch;
            this.heartbeat = setInterval(() => { try { socket.send({ schema_version: 1, type: "heartbeat" }); } catch { this.#disconnect("disconnected"); } }, 10000);
            this.heartbeat.unref?.();
            this.liveness = setTimeout(() => this.#disconnect("lease_expired"), 30000); this.liveness.unref?.();
            this.grantTimer = setTimeout(() => this.#disconnect("grant_expired"), Math.max(1, Math.min(900000, Date.parse(grant.expires_at) - Date.now()))); this.grantTimer.unref?.();
            this.connectedAuthority = authority; this.#state("connected"); resolve(); return;
          }
          if (message?.schema_version !== 1) { this.#disconnect("fenced"); return; }
          if (message.type === "renew") this.#renew(message);
          else if (message.type === "command") this.#enqueue(message, generation);
          else this.#disconnect("fenced");
        });
      });
      await this.refreshFences();
      if (!current()) throw new Error("Browser host was fenced");
      return this.snapshot();
    } catch (error) {
      if (generation === this.generation && (isTLSIdentityError(error) || error?.status === 401)) await this.auth.logout?.(isTLSIdentityError(error) ? "identity_conflict" : "invalid_authentication");
      if (generation === this.generation && !["fenced", "reconciliation_conflict"].includes(this.state)) this.#disconnect("unavailable");
      throw new Error("Browser host channel is unavailable");
    }
  }
  autoConnect() {
    clearTimeout(this.autoGrantTimer); this.autoGrantTimer = undefined;
    if (this.#iscp()) { this.connectISCP(); return; }
    if (!this.#browserReady() || !AUTO_GRANT_STATES.has(this.state)) return;
    if (this.autoGranting) { this.pendingAutoConnectRevision = this.autoConnectRevision; return; }
    const revision = this.autoConnectRevision;
    this.autoGranting = true;
    this.grant().then(() => { this.autoGrantDelay = 0; }, () => {
      if (revision !== this.autoConnectRevision || !this.#browserReady() || !AUTO_GRANT_STATES.has(this.state)) return;
      this.autoGrantDelay = Math.min(AUTO_GRANT_MAX_DELAY_MS, (this.autoGrantDelay || 1000) * 2);
      this.#scheduleAutoGrant(this.autoGrantDelay);
    }).finally(() => {
      this.autoGranting = false;
      const pending = this.pendingAutoConnectRevision;
      this.pendingAutoConnectRevision = undefined;
      if (pending !== undefined && pending !== revision && pending === this.autoConnectRevision) this.autoConnect();
    });
  }
  // ISCP: the embedded browser runs on its own. Nothing is granted, polled or
  // renewed; the Gateway sends reverse browser.command calls on the session
  // that already authenticates this device, only while automating a task.
  connectISCP(session = this.auth.transport?.capabilities) {
    if (!this.#iscp() || !this.#browserReady() || ["fenced", "reconciliation_conflict"].includes(this.state)) return;
    if (!ID.test(session?.session_id || "") || !Number.isSafeInteger(session.authorization_revision) || session.authorization_revision < 1) return;
    if (this.iscpSession?.session_id === session.session_id && this.iscpSession.authorization_revision === session.authorization_revision && this.state === "connected") return;
    // A new session ends every lease of the old one; uncertain writes stay in
    // the fences, which are read once for this session and never replayed.
    this.generation++;
    this.#disconnect("connected");
    this.iscpSession = Object.freeze({ session_id: session.session_id, authorization_revision: session.authorization_revision });
    const generation = this.generation;
    this.refreshFences().catch(() => {}).finally(() => { if (generation === this.generation) this.onChange(); });
  }
  async handleReverse(request, session) {
    const reply = (status, code, error) => ({ status, error_code: code, error });
    if (request?.operation !== "browser.command" || !request.body || typeof request.body !== "object") return reply(403, "permission_denied", "Reverse operation is not accepted");
    if (!this.#iscp() || !this.#browserReady()) return reply(503, "capability_unavailable", "Browser is unavailable");
    this.connectISCP(session);
    if (this.state !== "connected" || this.iscpSession?.session_id !== session?.session_id) return reply(503, "capability_unavailable", "Browser is unavailable");
    const command = request.body;
    try { validateCommand(command); if (!this.#validISCPBinding(command.binding, command.operation) || command.digest !== commandDigest(command)) throw new Error("Command binding differs"); }
    catch { return { status: 200, body: resultMessage(command, "failed", {}, "command_fenced") }; }
    const generation = this.generation, key = command.binding.lease_id;
    const queued = this.queues.get(key) || { promise: Promise.resolve(), count: 0 };
    if (this.queues.size >= 32 && !this.queues.has(key) || queued.count >= 64) return { status: 200, body: resultMessage(command, "failed", {}, "command_fenced") };
    queued.count++;
    const result = queued.promise.then(() => this.#command(command, generation, performance.now() + this.commandDeadlineMS));
    queued.promise = result.catch(() => {}).finally(() => { queued.count--; if (!queued.count && this.queues.get(key) === queued) this.queues.delete(key); });
    this.queues.set(key, queued);
    return { status: 200, body: await result };
  }
  #iscp() { return this.auth.descriptor?.transport === "iscp"; }
  #validISCPBinding(binding, operation) {
    const session = this.iscpSession;
    if (!session || !binding || !BINDING_KEYS.every((key) => key in binding) || Object.keys(binding).length !== BINDING_KEYS.length ||
        !BINDING_KEYS.filter((key) => key.endsWith("_id") || key === "runtime_generation" || key === "connection_epoch").every((key) => ID.test(binding[key])) ||
        binding.owner_id !== this.scope?.owner_id || binding.client_id !== this.scope?.client_id || binding.installation_id !== this.scope?.installation_id ||
        binding.connection_epoch !== session.session_id || binding.runtime_generation !== `iscp_${session.session_id}` ||
        binding.host_id !== iscpHostID(this.scope, session.session_id) || binding.authorization_digest !== iscpAuthorizationDigest(session.session_id, session.authorization_revision) ||
        binding.lease_expires_at !== ISCP_NO_EXPIRY || !Number.isSafeInteger(binding.page_generation) || binding.page_generation < 1) return false;
    const lease = this.leases.get(binding.lease_id);
    return operation === "acquire" ? !lease : Boolean(lease) && sameBinding(lease.binding, binding);
  }
  #browserReady() {
    return Boolean(this.scope) && this.auth.status.state === "connected" && this.auth.status.capabilities?.browser !== false &&
      (this.auth.descriptor?.transport === "iscp" ? this.auth.status.capabilities?.browser === true : this.auth.descriptor?.schemaVersion === 2);
  }
  #scheduleAutoGrant(delay) {
    clearTimeout(this.autoGrantTimer);
    this.autoGrantTimer = setTimeout(() => this.autoConnect(), delay); this.autoGrantTimer.unref?.();
  }
  async stop() { this.generation++; this.#stopAutoConnect(); this.#disconnect("ungranted"); this.scope = undefined; this.remoteFences.clear(); }
  async suspend() { this.generation++; this.#stopAutoConnect(); this.#disconnect("suspended"); }
  #stopAutoConnect() {
    this.autoConnectRevision++;
    this.pendingAutoConnectRevision = undefined;
    clearTimeout(this.autoGrantTimer); this.autoGrantTimer = undefined;
  }
  async refreshFences() {
    if (!this.scope || this.auth.status.state !== "connected") throw new Error("Browser reconciliation identity is unavailable");
    const generation = this.generation; const scope = this.scope;
    const response = await this.#fetch("browser.receipt", "/api/v1/browser/hosts/fences", { headers: { "X-SparkClaw-Installation": scope.installation_id } });
    if (!response.ok) throw new Error("Browser write fences are unavailable");
    const result = await response.json();
    if (generation !== this.generation || scope !== this.scope) throw new Error("Browser reconciliation identity changed");
    if (!result || Object.keys(result).join() !== "fences" || !Array.isArray(result.fences) || result.fences.length > 1024) throw new Error("Browser fence response is invalid");
    const fences = new Map();
    for (const row of result.fences) {
      const keys = ["command_id", "scope", "host_id", "runtime_generation", "connection_epoch", "lease_id", "page_id", "page_generation", "authorization_digest", "digest", "write", "state", "updated_at"];
      const scopedKeys = ["owner_id", "client_id", "installation_id", "local_conversation_id", "local_task_id"];
      if (!row || Object.keys(row).sort().join() !== keys.sort().join() || !row.scope || Object.keys(row.scope).sort().join() !== scopedKeys.sort().join() ||
          scopedKeys.some((key) => !ID.test(row.scope[key])) || ["owner_id", "client_id", "installation_id"].some((key) => row.scope[key] !== scope[key]) ||
          !ID.test(row.command_id) || !["host_id", "runtime_generation", "connection_epoch", "lease_id", "page_id"].every((key) => ID.test(row[key])) || !HASH.test(row.digest) || !HASH.test(row.authorization_digest) ||
          row.state !== "unknown" || row.write !== true || !Number.isSafeInteger(row.page_generation) || row.page_generation < 1 || !Number.isFinite(Date.parse(row.updated_at)) || fences.has(row.command_id)) throw new Error("Browser fence identity is invalid");
      const local = this.journal.rows.get(row.command_id);
      if (local && (local.digest !== row.digest || scopedKeys.some((key) => local[key] !== row.scope[key]))) { this.#disconnect("reconciliation_conflict"); throw new Error("Browser fence digest conflicts with the local journal"); }
      fences.set(row.command_id, Object.freeze(row));
    }
    this.remoteFences = fences; this.onChange(); return this.snapshot();
  }
  async reconcile(commandID, digest, outcome) {
    if (!ID.test(commandID) || !HASH.test(digest) || !["observed_completed", "observed_not_applied"].includes(outcome)) throw new Error("Browser write reconciliation is invalid");
    await this.refreshFences();
    const local = this.journal.rows.get(commandID);
    const remote = this.remoteFences.get(commandID);
    const scopedKeys = ["owner_id", "client_id", "installation_id"];
    const localBound = local && local.digest === digest && scopedKeys.every((key) => local[key] === this.scope?.[key]);
    if (!(remote?.digest === digest || localBound && local.state === "unknown") || local && !localBound) throw new Error("Browser write reconciliation is invalid");
    const response = await this.#fetch("browser.reconcile", "/api/v1/browser/hosts/reconcile", { method: "POST", headers: { "content-type": "application/json", "X-SparkClaw-Installation": this.scope.installation_id }, body: JSON.stringify({ command_id: commandID, digest, outcome }) });
    if (!response.ok) throw new Error("Backend write reconciliation failed");
    if (local) await this.journal.reconcile(commandID, digest, outcome, Boolean(remote));
    else await this.journal.recordReconciled(remote, outcome);
    this.remoteFences.delete(commandID); this.onChange(); return this.snapshot();
  }
  async #fetch(operation,route,init) {
    if(this.auth.descriptor?.transport !== "iscp")return this.auth.authorizedFetch(`${this.auth.descriptor.origin}${route}`,init);
    const body=init?.body?JSON.parse(init.body):{};
    const result=await this.auth.invokeISCP(operation,body,{installationID:this.scope.installation_id});
    return new Response(JSON.stringify(result),{status:200,headers:{'content-type':'application/json'}});
  }
  #renew(message) {
    if (message.connection_epoch !== this.epoch || !Array.isArray(message.bindings) || message.bindings.length > 32) { this.#disconnect("fenced"); return; }
    clearTimeout(this.liveness);
    this.liveness = setTimeout(() => this.#disconnect("lease_expired"), 30000); this.liveness.unref?.();
    for (const binding of message.bindings) {
      const lease = this.leases.get(binding.lease_id);
      if (!lease || !sameBinding(lease.binding, binding)) continue;
      if (!this.#validBinding(binding)) { this.#disconnect("fenced"); return; }
      lease.binding = binding;
      lease.deadline = performance.now() + Math.min(30000, Math.max(0, Date.parse(binding.lease_expires_at) - Date.now()));
      clearTimeout(lease.timer); lease.timer = setTimeout(() => this.#expire(binding.lease_id), Math.max(1, lease.deadline - performance.now())); lease.timer.unref?.();
    }
  }
  #enqueue(command, generation) {
    try { validateCommand(command); if (!this.#validBinding(command.binding) || command.digest !== commandDigest(command)) throw new Error("Command binding differs"); }
    catch { this.#disconnect("fenced"); return; }
    const key = command.binding.lease_id;
    if (this.queues.size >= 32 && !this.queues.has(key)) { this.#disconnect("fenced"); return; }
    const queued = this.queues.get(key) || { promise: Promise.resolve(), count: 0 };
    if (++queued.count > 64) { this.#disconnect("fenced"); return; }
    queued.promise = queued.promise.then(() => this.#command(command, generation)).catch(() => this.#disconnect("fenced")).finally(() => { queued.count--; if (!queued.count && this.queues.get(key) === queued) this.queues.delete(key); });
    this.queues.set(key, queued);
  }
  async #command(command, generation, deadline) {
    const b = command.binding; const write = WRITE.has(command.operation);
    let begun = false;
    let timer;
    const work = async () => {
      if (generation !== this.generation || this.state !== "connected") throw new Error("Host generation is stale");
      if (command.operation === "acquire") {
        if (this.journal.unknown(this.scope).some((row) => row.local_conversation_id === b.local_conversation_id) || this.leases.has(b.lease_id)) throw new Error("Conversation write outcome is unknown");
        const record = this.registry.acquireHostPage(b);
        await record.hostInitialLoad;
        if (generation !== this.generation || this.state !== "connected") throw new Error("Host was fenced during page acquisition");
        // An acquisition that outlived its deadline was already reported as
        // failed; give the conversation's page back instead of holding it.
        if (deadline !== undefined && performance.now() >= deadline) { this.registry.releaseHostPage(b); throw new Error("Browser command deadline"); }
        const lease = { binding: b, record, deadline: Infinity };
        if (!this.#iscp()) {
          lease.deadline = performance.now() + Math.min(30000, Math.max(0, Date.parse(b.lease_expires_at) - Date.now()));
          lease.timer = setTimeout(() => this.#expire(b.lease_id), Math.max(1, lease.deadline - performance.now())); lease.timer.unref?.();
        }
        this.leases.set(b.lease_id, lease);
        return ["completed", { page_id: b.page_id, page_ref: record.pageRef }];
      }
      const fence = () => {
        const lease = this.leases.get(b.lease_id);
        if (generation !== this.generation || this.state !== "connected" || !lease || !sameBinding(lease.binding, b) || performance.now() >= lease.deadline || deadline !== undefined && performance.now() >= deadline) throw new Error("Browser command lease is fenced");
        this.registry.requireHostPage(b);
      };
      fence();
      if (command.operation === "release") {
        const lease = this.leases.get(b.lease_id); clearTimeout(lease.timer);
        this.registry.releaseHostPage(b); this.leases.delete(b.lease_id); return ["completed", { released: true }];
      }
      if (write) {
        const state = await this.journal.begin(command);
        if (state !== "new") return ["unknown", {}, "write_replay_requires_reconciliation"];
        begun = true; fence();
      }
      const result = await this.execute(this.registry.requireHostPage(b), command.operation, command.arguments, b, fence);
      fence();
      if (write) await this.journal.finish(command.command_id, "completed");
      return ["completed", result];
    };
    let outcome;
    try {
      const run = work();
      // A command still running after its deadline reports for itself in the
      // local journal; the Gateway already holds its write as unknown.
      run.catch(() => {});
      outcome = deadline === undefined ? await run : await Promise.race([run, new Promise((_, reject) => { timer = setTimeout(() => reject(new Error("Browser command deadline")), Math.max(1, deadline - performance.now())); })]);
    } catch {
      if (write && begun) await this.journal.finish(command.command_id, "unknown").catch(() => {});
      outcome = [write && begun ? "unknown" : "failed", {}, begun ? "write_outcome_unknown" : "command_fenced"];
    } finally { clearTimeout(timer); this.onChange(); }
    return this.#result(command, ...outcome);
  }
  #result(command, status, output, errorCode) {
    const message = resultMessage(command, status, output, errorCode);
    if (this.#iscp()) return message;
    if (this.socket && this.state === "connected") this.socket.send(message);
    return message;
  }
  #validBinding(binding) {
    return binding && BINDING_KEYS.every((key) => key in binding) && Object.keys(binding).length === BINDING_KEYS.length &&
      BINDING_KEYS.filter((key) => key.endsWith("_id") || key === "runtime_generation" || key === "connection_epoch").every((key) => ID.test(binding[key])) &&
      binding.owner_id === this.scope?.owner_id && binding.client_id === this.scope?.client_id && binding.installation_id === this.scope?.installation_id &&
      binding.host_id === this.grantRecord?.host_id && binding.runtime_generation === this.runtime && binding.connection_epoch === this.epoch && binding.authorization_digest === this.grantRecord?.grant_digest &&
      Number.isSafeInteger(binding.page_generation) && binding.page_generation > 0 && Number.isFinite(Date.parse(binding.lease_expires_at)) && Date.parse(binding.lease_expires_at) > Date.now() && Date.parse(binding.lease_expires_at) <= Date.now() + 31000;
  }
  #expire(id) {
    const lease = this.leases.get(id); if (!lease) return;
    this.leases.delete(id); clearTimeout(lease.timer);
    this.registry.closeConnection(id, "lease_expired"); this.onChange();
  }
  #disconnect(state) {
    this.connectedAuthority = undefined;
    clearInterval(this.heartbeat); clearTimeout(this.liveness); clearTimeout(this.grantTimer);
    this.abort?.abort(); this.abort = undefined;
    const socket = this.socket; this.socket = undefined; socket?.close();
    for (const lease of this.leases.values()) clearTimeout(lease.timer);
    this.leases.clear(); this.queues.clear(); this.registry.closeHostPages("host_disconnected"); this.epoch = undefined; this.grantRecord = undefined; this.iscpSession = undefined;
    this.#state(state);
    if (!this.#iscp() && ["disconnected", "lease_expired", "grant_expired"].includes(state) && !this.autoGranting && this.#browserReady()) this.#scheduleAutoGrant(this.autoGrantDelay || 1000);
  }
  #state(value) { this.state = value; this.onChange(); }
}

// Both sides derive these from the session, so a binding the Gateway did not
// build for this exact device session can never address a page.
const sha256 = (value) => crypto.createHash("sha256").update(value, "utf8").digest("hex");
export function iscpHostID(scope, sessionID) { return `iscp_${sha256(`sparkclaw.browser.iscp.host.v1\0${scope.owner_id}\0${scope.client_id}\0${scope.installation_id}\0${sessionID}`).slice(0, 32)}`; }
export function iscpAuthorizationDigest(sessionID, revision) { return sha256(`sparkclaw.browser.iscp.authorization.v1\0${sessionID}\0${revision}`); }
// Bytes after the helper re-encodes JSON, which escapes <, >, & and U+2028/9.
export function encodedBytes(value) { return Buffer.byteLength(JSON.stringify(value).replace(/[<>&\u2028\u2029]/gu, "\\u0000")); }
function resultMessage(command, status, output, errorCode) {
  let message = { schema_version: 1, type: "result", command_id: command?.command_id, binding: command?.binding, status, output, ...(errorCode ? { error_code: errorCode } : {}) };
  // An oversized read fails; a write keeps its outcome and drops only output.
  if (encodedBytes(output) > RESULT_BUDGET_BYTES) message = { ...message, status: WRITE.has(command?.operation) || status === "unknown" ? status : "failed", output: {}, error_code: "result_budget_exceeded" };
  return message;
}
function sameBinding(a, b) { return BINDING_KEYS.filter((key) => key !== "lease_expires_at").every((key) => a?.[key] === b?.[key]); }
export function commandDigest(command) { return crypto.createHash("sha256").update(canonical({ ...command, digest: "" })).digest("hex"); }
function canonical(value) {
  if (Array.isArray(value)) return `[${value.map(canonical).join(",")}]`;
  if (value && typeof value === "object") return `{${Object.keys(value).sort().map((key) => `${canonical(key)}:${canonical(value[key])}`).join(",")}}`;
  return JSON.stringify(value).replaceAll("<", "\\u003c").replaceAll(">", "\\u003e").replaceAll("&", "\\u0026").replaceAll("\u2028", "\\u2028").replaceAll("\u2029", "\\u2029");
}
function validateCommand(command) {
  if (!command || Object.keys(command).sort().join() !== ["schema_version", "type", "command_id", "binding", "operation", "arguments", "digest"].sort().join() || command.schema_version !== 1 || command.type !== "command" || !ID.test(command.command_id) || !HASH.test(command.digest) || !Object.hasOwn(OPS, command.operation)) throw new Error("Browser command is invalid");
  const args = command.arguments;
  if (!args || typeof args !== "object" || Array.isArray(args) || Object.keys(args).some((key) => !OPS[command.operation].includes(key)) || Buffer.byteLength(JSON.stringify(args)) > 24 << 10) throw new Error("Browser arguments are invalid");
  if (command.operation === "navigate") { const url = new URL(args.url); if (typeof args.url !== "string" || args.url.length > 16384 || url.protocol !== "https:" || url.username || url.password) throw new Error("Browser URL is invalid"); }
  if (WRITE.has(command.operation) && (typeof args.ref !== "string" || args.ref.length > 256 || !args.ref || typeof args.snapshot_id !== "string" || args.snapshot_id.length > 160 || !args.snapshot_id)) throw new Error("Snapshot reference is invalid");
  if (["fill", "select"].includes(command.operation) && (typeof args.value !== "string" || args.value.length > 16384)) throw new Error("Draft value is invalid");
  if (command.operation === "read" && args.max_chars !== undefined && (!Number.isSafeInteger(args.max_chars) || args.max_chars < 1 || args.max_chars > 24000)) throw new Error("Read limit is invalid");
  if (command.operation === "wait" && args.mode === undefined && (Object.keys(args).join() !== "milliseconds" || !Number.isSafeInteger(args.milliseconds) || args.milliseconds < 0 || args.milliseconds > 5000)) throw new Error("Wait limit is invalid");
  if (command.operation === "wait" && args.mode !== undefined && (args.mode !== "stable_state" || "milliseconds" in args ||
      args.expected_url !== undefined && (typeof args.expected_url !== "string" || args.expected_url.length > 16384) ||
      args.before_digest !== undefined && !HASH.test(args.before_digest) ||
      ["allow_no_change", "require_ready_state"].some((key) => args[key] !== undefined && typeof args[key] !== "boolean") ||
      args.timeout_ms !== undefined && (!Number.isSafeInteger(args.timeout_ms) || args.timeout_ms < 500 || args.timeout_ms > 15000))) throw new Error("Stable wait is invalid");
}
