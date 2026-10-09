import crypto from "node:crypto";
import { CLIENT_LIMITS, parseResultPayload } from "./client-store.mjs";
import { ISCPRequestNotSentError } from "./iscp-transport.mjs";

const SERVER_STATES = new Set(["accepted", "running", "completed", "failed", "canceled", "unknown", "delivery_expired", "delivered"]);
const AUTH_GENERATION = Symbol("execution_auth_generation");
const CLIENT_LIFETIME = Symbol("execution_client_lifetime");
const sha256 = (value) => crypto.createHash("sha256").update(value).digest("hex");

// The main process owns credentials and immutable requests. Timers can only
// look up explicitly submitted IDs and retry durable ACKs; they never POST work.
export class ExecutionClient {
  constructor({ auth, store, getIdentity, intervalMS = 5000, onChange = () => {} }) {
    Object.assign(this, { auth, store, getIdentity, intervalMS, onChange });
    this.operations = new Map();
    this.verifiedApprovals = new Map();
    this.controller = new AbortController();
    this.closed = false;
    this.lifetime = 0;
  }

  start() {
    this.closed = false;
    if (this.controller.signal.aborted) this.controller = new AbortController();
    if (!this.timer) this.timer = setInterval(() => void this.reconcilePending().catch(() => {}), this.intervalMS);
    this.timer.unref?.();
    void this.reconcilePending().catch(() => {});
    return this;
  }

  close() { this.lifetime++; this.closed = true; this.verifiedApprovals.clear(); this.controller.abort(); clearInterval(this.timer); this.timer = undefined; }

  async submit(scope, requestID, { canSubmit = () => true, scheduleClaim } = {}) {
    scope = this.#boundScope(scope);
    return this.#serialized(scope, requestID, async () => {
      if (!canSubmit()) throw new Error("Execution submission availability changed");
      const original = this.store.request(scope, requestID);
      const claimed = scheduleClaim && original.submission_claim === scheduleClaim && original.status === "submission_pending";
      if (original.explicitly_submitted && !claimed) {
        // Even a 404 cannot prove that a lost admission had no external effect.
        // Explicit buttons and background recovery both reconcile the original
        // request; another execution always requires a new user request ID.
        await this.#lookup(scope, original);
        return this.#view(scope, requestID);
      }
      this.auth.validateExecutionRequest?.(original.context_json);
      // Uploading immutable, digest-bound inputs cannot execute this task. Keep
      // its submission fence untouched until staging completes, so an explicit
      // retry after interruption or restart can safely upload the same files.
      if (JSON.parse(original.context_json).input_files?.length) await this.#uploadInputs(scope, original);
      if (!canSubmit()) throw new Error("Execution submission availability changed");
      this.#sameIdentity(scope);
      if (this.auth.status.state !== "connected") throw new Error("Execution backend is unavailable; your input is preserved");
      const { first, task } = this.store.markSubmitted(scope, requestID, scheduleClaim);
      if (!first) { await this.#lookup(scope, task); return this.#view(scope, requestID); }
      let admissionResponseReceived = false;
      try {
        const response = await this.#fetch(scope, "/api/v1/executions", {
          method: "POST", headers: { "Content-Type": "application/json", "X-SparkClaw-Digest": task.input_digest }, body: task.context_json,
        });
        admissionResponseReceived = true;
        if (!response.ok) {
          await response.body?.cancel();
          if ([400, 413, 422].includes(response.status)) this.store.setExecutionState(scope, requestID, "failed");
          throw new Error("Execution submission was not accepted");
        }
        await this.#accept(scope, task, await json(response));
      } catch (error) {
        if (!admissionResponseReceived && error instanceof ISCPRequestNotSentError) this.store.restoreUnsentSubmission(scope, requestID, scheduleClaim);
        // Only the transport's local pre-write proof releases the intent. Once
        // sent, even a lookup 404 cannot authorize replay of this execution.
        else await this.#lookup(scope, this.store.request(scope, requestID)).catch(() => {});
        throw error;
      } finally { this.onChange(); }
      return this.#view(scope, requestID);
    });
  }

  async reconcile(scope, requestID) {
    scope = this.#boundScope(scope);
    return this.#serialized(scope, requestID, async () => {
      const task = this.store.request(scope, requestID);
      if (!task.explicitly_submitted) throw new Error("Task has not been explicitly submitted");
      await this.#lookup(scope, task);
      this.onChange();
      return this.#view(scope, requestID);
    });
  }

  async cancel(scope, requestID) {
    scope = this.#boundScope(scope);
    return this.#serialized(scope, requestID, async () => {
      const task = this.store.request(scope, requestID);
      if (!task.explicitly_submitted || !["submission_pending", "accepted", "running", "cancel_pending"].includes(task.status)) throw new Error("Task cannot be canceled");
      this.store.setExecutionState(scope, requestID, "cancel_pending");
      try {
        const response = await this.#fetch(scope, `/api/v1/executions/${requestID}/cancel`, {
          method: "POST", headers: { "Content-Type": "application/json" }, body: "{}",
        });
        if (!response.ok) { await response.body?.cancel(); throw new Error("Execution cancellation is awaiting reconciliation"); }
        await this.#accept(scope, task, await json(response));
      } catch (error) { await this.#lookup(scope, task).catch(() => {}); throw error; }
      finally { this.onChange(); }
      return this.#view(scope, requestID);
    });
  }

  approvals(scope, requestID) {
    const verification = this.verifiedApprovals.get(requestID);
    const current = this.getIdentity();
    const task = this.store.request(scope, requestID);
    const active = !this.closed && this.auth.status.state === "connected" && current &&
      task.status === "running" && JSON.stringify([scope.deployment_id, scope.owner_id, scope.client_id]) ===
      JSON.stringify([current.deployment_id, current.owner_id, current.client_id]) && verification?.generation === this.auth.generation &&
      verification?.expiresAt > Date.now() && verification?.verifiedAt > Date.now() - 30000;
    return this.store.approvals(scope, requestID).map((approval) => ({ ...approval,
      actionable: Boolean(active && ["pending", "decision_pending"].includes(approval.state) && verification.ids.has(`${approval.approval_id}:${approval.digest}`)),
    }));
  }

  async decideApproval(scope, requestID, approvalID, digest, decision) {
    scope = this.#boundScope(scope);
    return this.#serialized(scope, requestID, async () => {
      if (typeof approvalID !== "string" || !/^[A-Za-z0-9][A-Za-z0-9_.:-]{0,159}$/u.test(approvalID) ||
          typeof digest !== "string" || !/^[a-f0-9]{64}$/u.test(digest) || !["approve", "reject"].includes(decision)) throw new Error("Invalid approval decision");
      let row = this.store.approvals(scope, requestID).find((approval) => approval.approval_id === approvalID);
      if (!row || row.digest !== digest || (row.decision && row.decision !== decision)) throw new Error("Approval identity, digest or decision mismatch");
      if (["approved", "rejected"].includes(row.state)) return { resolved: true };
      const task = this.store.request(scope, requestID);
      // A cached local row is display-only. Every explicit decision starts with
      // a fresh authenticated lookup of this exact active execution.
      if (!await this.#lookup(scope, task)) throw new Error("Approval execution is unavailable");
      row = this.approvals(scope, requestID).find((approval) => approval.approval_id === approvalID);
      if (row && ["approved", "rejected"].includes(row.state) && row.decision === decision) return { resolved: true };
      if (!row?.actionable || row.digest !== digest) throw new Error("Approval expired or requires renewed verification");
      this.store.approvalDecision(scope, requestID, approvalID, digest, decision);
      this.onChange();
      try {
        const response = await this.#fetch(scope, `/api/v1/executions/${requestID}/approvals/${encodeURIComponent(approvalID)}`, {
          method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ digest, decision, ...(this.auth.descriptor?.transport === "iscp" ? {input_digest:task.input_digest} : {}) }),
          ...(this.auth.descriptor?.transport === "iscp" ? {expectedRevision:String(this.store.executionProjection(scope,requestID).revision)} : {}),
        });
        if (!response.ok) { await response.body?.cancel(); throw new Error("Approval decision is awaiting reconciliation"); }
        const receipt = await json(response);
        this.#sameIdentity(scope);
        if (receipt?.resolved !== true || Object.keys(receipt).join(",") !== "resolved") throw new Error("Invalid approval receipt");
        this.store.approvalDecision(scope, requestID, approvalID, digest, decision, true);
        await this.#lookup(scope, task);
        return { resolved: true };
      } catch (error) {
        // A lost decision response never replays the execution or chooses a
        // decision automatically. Lookup updates the retained original row.
        await this.#lookup(scope, task).catch(() => {});
        throw error;
      } finally { this.onChange(); }
    });
  }

  async reconcilePending() {
    if (this.closed || this.polling || this.auth.status.state !== "connected") return;
    const scope = this.getIdentity();
    if (!scope) return;
    this.polling = true;
    try {
      const requests = this.store.pending(scope);
      // Keep network concurrency and shutdown work bounded.
      // The four-call ISCP budget also serves startup presentation and local
      // schedule dispatch. Recovery leaves room for those ordinary clients.
      const width = this.auth.descriptor?.transport === "iscp" ? 1 : 4;
      for (let i = 0; i < requests.length && !this.closed; i += width) {
        await Promise.allSettled(requests.slice(i, i + width).map((task) => this.reconcile(scope, task.request_id)));
      }
    } finally { this.polling = false; }
  }

  async #lookup(scope, task) {
    const receipt = this.store.receipt(scope, task.request_id);
    if (receipt && !receipt.acknowledged) { await this.#ack(scope, receipt); return true; }
    const response = await this.#fetch(scope, `/api/v1/executions/${task.request_id}`);
    if (response.status === 404) { this.verifiedApprovals.delete(task.request_id); await response.body?.cancel(); return false; }
    // A bodyless v1 lookup cannot exceed an input budget. Its authenticated 413
    // denotes a result that this transport cannot deliver, not absent execution.
    // Preserve the original ID without truncating, ACKing or polling forever.
    if (this.auth.descriptor?.transport === "iscp" && response.status === 413) {
      await response.body?.cancel();
      this.#sameIdentity(scope);
      this.verifiedApprovals.delete(task.request_id);
      this.store.syncApprovals(scope, task.request_id, [], "delivery_too_large");
      this.store.setExecutionState(scope, task.request_id, "delivery_too_large");
      return true;
    }
    if (!response.ok) { await response.body?.cancel(); throw new Error("Execution status is unavailable"); }
    await this.#accept(scope, task, await json(response));
    return true;
  }

  async #accept(scope, task, event) {
    this.#sameIdentity(scope);
    if (!event || event.schema_version !== 1 || event.request_id !== task.request_id ||
        event.input_digest !== task.input_digest || !SERVER_STATES.has(event.state)) throw new Error("Execution identity or digest mismatch");
    if (!this.store.acceptExecutionProjection(scope, event)) return;
    const pending = event.pending_approvals ?? [];
    this.store.syncApprovals(scope, task.request_id, pending, event.state, event.execution_expires_at);
    if (pending.length) {
      this.verifiedApprovals.set(task.request_id, { generation: this.auth.generation, expiresAt: Date.parse(event.execution_expires_at),
        verifiedAt: Date.now(), ids: new Set(pending.map((approval) => `${approval.approval_id}:${approval.digest}`)) });
    } else this.verifiedApprovals.delete(task.request_id);
    if (event.state !== "completed") {
      if (event.result) throw new Error("Unexpected execution result");
      this.store.setExecutionState(scope, task.request_id, event.state);
      return;
    }
    const result = event.result;
    if (!result || Object.keys(result).sort().join(",") !== "digest,payload,sequence" || result.sequence !== 1 ||
        typeof result.payload !== "string" || Buffer.byteLength(result.payload) > CLIENT_LIMITS.resultBytes ||
        typeof result.digest !== "string" || sha256(result.payload) !== result.digest) throw new Error("Result digest verification failed");
    const payload = parseResultPayload(result.payload);
    let bytes = Buffer.byteLength(result.payload);
    const files = new Map();
    for (const manifest of payload.files) {
      bytes += manifest.size;
      if (bytes > CLIENT_LIMITS.resultBytes) throw new Error("Delivery exceeds result budget");
      const response = await this.#fetch(scope, `/api/v1/executions/${task.request_id}/files/${encodeURIComponent(manifest.id)}`);
      if (!response.ok) { await response.body?.cancel(); throw new Error("Delivered file is unavailable"); }
      const content = new Uint8Array(await response.arrayBuffer());
      if (content.byteLength !== manifest.size || sha256(content) !== manifest.sha256) throw new Error("Delivered file verification failed");
      files.set(manifest.id, content);
    }
    this.#sameIdentity(scope);
    const receipt = this.store.commitDelivery(scope, { request_id: task.request_id, ...result }, files);
    await this.#ack(scope, receipt);
  }

  async #uploadInputs(scope, task) {
    const envelope = JSON.parse(task.context_json);
    for (const manifest of envelope.input_files || []) {
      const file = this.store.file(scope, manifest.id);
      if (file.content.byteLength !== manifest.size || sha256(file.content) !== manifest.sha256) throw new Error("Local input file verification failed");
      const response = await this.#fetch(scope, `/api/v1/inputs/${task.request_id}/files/${manifest.id}`, {
        method: "PUT", headers: { "Content-Type": "application/octet-stream", "X-SparkClaw-Digest": manifest.sha256,
          "X-SparkClaw-Installation": this.store.installationID }, body: file.content,
      });
      if (!response.ok) { await response.body?.cancel(); throw new Error("Input file upload was not accepted"); }
      await response.body?.cancel();
    }
  }

  async #ack(scope, receipt) {
    this.#sameIdentity(scope);
    // Reverify disk contents immediately before exposing a durable ACK.
    this.store.receipt(scope, receipt.request_id);
    const response = await this.#fetch(scope, `/api/v1/executions/${receipt.request_id}/ack`, {
      method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ sequence: receipt.sequence, digest: receipt.digest, durable: true }),
    });
    if (!response.ok) { await response.body?.cancel(); throw new Error("Delivery acknowledgement is pending"); }
    await response.body?.cancel();
    this.#sameIdentity(scope);
    this.store.acknowledge(scope, receipt.request_id, receipt.sequence, receipt.digest);
  }

  #fetch(scope, route, init) {
    this.#sameIdentity(scope);
    if (this.closed || this.auth.status.state !== "connected") throw new Error("Execution backend is unavailable; your input is preserved");
    const headers = new Headers(init?.headers);
    headers.set("X-SparkClaw-Installation", this.store.installationID);
    return this.auth.authorizedExecutionFetch(`${this.auth.descriptor.origin}${route}`, { ...init, headers, signal: this.controller.signal });
  }
  #sameIdentity(scope) {
    const current = this.getIdentity();
    if (this.closed || !current || JSON.stringify([scope.deployment_id, scope.owner_id, scope.client_id]) !==
        JSON.stringify([current.deployment_id, current.owner_id, current.client_id]) ||
        (Object.hasOwn(scope, AUTH_GENERATION) && scope[AUTH_GENERATION] !== this.auth.generation) ||
        (Object.hasOwn(scope, CLIENT_LIFETIME) && scope[CLIENT_LIFETIME] !== this.lifetime)) throw new Error("Execution authentication changed");
  }
  #boundScope(scope) { return { ...scope, [AUTH_GENERATION]: this.auth.generation, [CLIENT_LIFETIME]: this.lifetime }; }
  #view(scope, requestID) {
    const task = this.store.request(scope, requestID);
    return { id: task.id, request_id: task.request_id, status: task.status,
      explicitly_submitted: task.explicitly_submitted, created_at: task.created_at };
  }
  #serialized(scope, requestID, operation) {
    this.#sameIdentity(scope);
    this.store.request(scope, requestID);
    const key = JSON.stringify([scope, requestID]);
    const pending = (this.operations.get(key) || Promise.resolve()).catch(() => {}).then(() => { this.#sameIdentity(scope); return operation(); });
    this.operations.set(key, pending);
    return pending.finally(() => { if (this.operations.get(key) === pending) this.operations.delete(key); });
  }
}

async function json(response) {
  const bytes = new Uint8Array(await response.arrayBuffer());
  if (bytes.byteLength > CLIENT_LIMITS.resultBytes) throw new Error("Execution response exceeds result budget");
  return JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(bytes));
}
