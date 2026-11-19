import crypto from "node:crypto";
import { EventEmitter } from "node:events";
import { ISCP_REGISTRY } from "./iscp-routes.mjs";
import { LINK_REVERSE_OPERATIONS, LinkError } from "./link-transport.mjs";

export const V2_PROFILE = "sparkclaw.workbench.transport.v2";
const V1_PROFILE = "sparkclaw.workbench.transport.v1";
const FRAME_BYTES = 65536;
const MANIFEST_TTL_MS = 60 * 60 * 1000;

// Operation groups the backend serves through core link operations or the
// console instead of domain.call (Gateway link_domain_bridge.go).
export const LINK_EXCLUDED_DOMAINS = Object.freeze(["execution", "approvals", "clients", "authorizations", "installation", "phone", "capabilities", "workbench", "state", "audio"]);
const V1_OPERATIONS = Object.freeze(["workbench.identity", "installation.bind", "presentation.config", "presentation.owner", "presentation.ready", "execution.submit", "execution.lookup", "execution.cancel", "execution.ack"]);
// The first-version presentation reads (/api/config, /api/owner, /readyz).
const PRESENTATION_READS = Object.freeze(["presentation.config", "presentation.owner", "presentation.ready"]);

// The v2 forward operations reachable through domain.call for the business
// domains the backend reported in device.describe.
export function linkDomainOperations(domains = []) {
  const allowed = new Set(domains);
  return ISCP_REGISTRY.filter((row) => row.version === 2 && row.direction === "forward")
    .map((row) => row.name)
    .filter((name) => { const prefix = name.split(".")[0]; return allowed.has(prefix) && !LINK_EXCLUDED_DOMAINS.includes(prefix); });
}

// The capabilities manifest the ISCP request pipeline expects from its
// helper. Over the link layer the session is the link connection and the
// authority is the device's console membership; there is no separate Grant
// revision, so the revision is fixed at 1 for one link connection.
export function linkManifest({ sessionID, binding, domains, reverse = LINK_REVERSE_OPERATIONS, now = Date.now() }) {
  return Object.freeze({
    schema_version: 2, profile: V2_PROFILE, session_id: sessionID, authorization_revision: 1,
    expires_at: new Date(now + MANIFEST_TTL_MS).toISOString(), binding,
    operations: Object.freeze([...linkDomainOperations(domains), ...PRESENTATION_READS, ...reverse]),
    // Reverse operations are declared at the link hello; the backend calls
    // only those this device serves.
    qualified_operations: Object.freeze([...reverse]),
    limits: Object.freeze({ concurrent: 0, body_bytes: FRAME_BYTES, response_bytes: FRAME_BYTES }),
  });
}

function notSent(error) {
  return error instanceof LinkError && error.code === "backend_unavailable" && !/connection lost/iu.test(error.message);
}

// LinkDomainWire stands in for the old ISCP helper process: the unchanged
// ISCP request pipeline (route mapping, revisions, mutation journal, object
// transfer) writes its helper frames here, and each v2 call is sent to the
// backend as one link domain.call. Reverse calls from the backend arrive as
// reverse_request frames, exactly as the old helper delivered them.
export class LinkDomainWire extends EventEmitter {
  constructor({ link, binding, domains, installationID, sessionID = `link:${crypto.randomUUID()}` }) {
    super();
    Object.assign(this, { link, binding, domains, installationID, sessionID });
    this.stdout = new EventEmitter();
    this.stderr = new EventEmitter();
    this.exitCode = null;
    this.reverseCalls = new Map();
    this.closed = false;
    this.stdin = {
      write: (line, callback) => { this.#input(line); callback?.(); return true; },
      destroy: () => this.kill(),
      end: () => this.kill(),
      on: () => this.stdin,
    };
    queueMicrotask(() => this.#hello());
  }

  #emit(frame) {
    if (this.closed) return;
    this.stdout.emit("data", Buffer.from(`${JSON.stringify({ ipc_version: 1, ...frame })}\n`));
  }

  #hello() {
    if (this.closed) return;
    this.#emit({ type: "hello", operations: V1_OPERATIONS, max_request_bytes: FRAME_BYTES, max_response_bytes: FRAME_BYTES });
    if (!this.link.online) { this.#emit({ type: "state", state: "disconnected" }); return; }
    this.#capabilities();
    this.#emit({ type: "state", state: "transport_ready" });
    // The manifest is renewed while this link connection lasts.
    this.renewal = setInterval(() => this.#capabilities(), MANIFEST_TTL_MS / 4);
    this.renewal.unref?.();
  }

  #capabilities() {
    this.manifest = linkManifest({ sessionID: this.sessionID, binding: this.binding, domains: this.domains });
    this.#emit({ type: "capabilities", capabilities: this.manifest });
  }

  // The link went offline: the pipeline fails its calls and the owner
  // reconnects with a new wire once a path is up again.
  disconnect() { this.#emit({ type: "state", state: "disconnected" }); }

  kill() {
    if (this.closed) return;
    this.closed = true;
    clearInterval(this.renewal);
    for (const call of this.reverseCalls.values()) call.reject(new LinkError("backend_unavailable", "Device link closed"));
    this.reverseCalls.clear();
    this.exitCode = 0;
    this.emit("exit", 0);
  }

  on(event, listener) { return super.on(event, listener); }

  #input(line) {
    let frame;
    try { frame = JSON.parse(String(line)); } catch { return; }
    if (frame.type === "call") void this.#call(frame);
    else if (frame.type === "reverse_response") {
      const call = this.reverseCalls.get(frame.id);
      if (call) { this.reverseCalls.delete(frame.id); call.resolve(frame.response); }
    } else if (frame.type === "relay_submission_status") this.#emit({ type: "relay_submission_status", id: frame.id, message_id: frame.message_id, found: false });
  }

  #respond(request, result) {
    let response = { type: "task.result", profile: request.profile, id: request.id, status: result.status,
      ...(result.body !== undefined && result.body !== null ? { body: result.body } : {}),
      ...(result.body_object ? { body_object: result.body_object } : {}),
      ...(result.error_code ? { error_code: result.error_code } : {}),
      ...(result.error ? { error: String(result.error) } : {}),
      ...(result.retryable !== undefined ? { retryable: result.retryable === true } : {}) };
    if ([204, 205, 304].includes(response.status)) delete response.body;
    // The pipeline drops its whole helper on an oversized frame; answer this
    // one call with a bounded failure instead.
    if (Buffer.byteLength(JSON.stringify(response)) > FRAME_BYTES) {
      response = { type: "task.result", profile: request.profile, id: request.id, status: 502, error_code: "resource_limit", error: "The backend response exceeds the transfer limit", retryable: false };
    }
    this.#emit({ type: "response", id: request.id, response });
  }

  async #call(frame) {
    const request = { ...frame.request, id: frame.request?.id || frame.id };
    let body;
    if (frame.body_base64 !== undefined) {
      try { body = JSON.parse(Buffer.from(frame.body_base64, "base64").toString("utf8")); }
      catch { this.#respond(request, { status: 400, error_code: "invalid_request", error: "Request body must be JSON" }); return; }
    }
    try {
      if (request.profile === V1_PROFILE || V1_OPERATIONS.includes(request.operation)) { this.#respond(request, await this.#v1(request)); return; }
      const domain = String(request.operation || "").split(".")[0];
      const args = {
        ...(request.params ? { params: request.params } : {}),
        ...(body !== undefined ? { body } : {}),
        ...(request.body_object ? { body_object: request.body_object } : {}),
        ...(request.expected_revision ? { expected_revision: request.expected_revision } : {}),
        ...(request.input_digest ? { input_digest: request.input_digest } : {}),
        ...(request.request_id ? { request_id: request.request_id } : {}),
      };
      const reply = await this.link.request("domain.call", { domain, method: request.operation, args,
        ...(request.operation_id ? { idempotency_key: request.operation_id } : {}) }, { timeoutMS: 170000 });
      const result = reply?.result;
      if (!result || !Number.isInteger(result.status)) throw new LinkError("internal", "Domain result is malformed");
      this.#respond(request, result);
    } catch (error) {
      this.#respond(request, failure(error));
    }
  }

  // Version-1 presentation reads have no v2 operation of the same shape.
  async #v1(request) {
    if (request.operation === "presentation.owner") {
      const reply = await this.link.request("domain.call", { domain: "settings", method: "settings.owner.get", args: {} });
      const result = reply?.result;
      if (result?.status === 200 && result.body && Object.hasOwn(result.body, "value")) return { status: 200, body: result.body.value };
      return result;
    }
    if (["presentation.config", "presentation.ready"].includes(request.operation)) {
      try {
        const reply = await this.link.request("domain.call", { domain: "presentation", method: request.operation, args: {} });
        return reply.result;
      } catch (error) {
        if (!(error instanceof LinkError) || error.code !== "unsupported_capability" || request.operation !== "presentation.ready") throw error;
      }
      // A backend that does not offer the readiness read yet: the link
      // answered, and speech readiness comes from the speech domain.
      const reply = await this.link.request("domain.call", { domain: "speech", method: "speech.status", args: {} });
      return { status: 200, body: { ok: true, ...(reply?.result?.status === 200 ? { speech: reply.result.body } : {}) } };
    }
    return { status: 501, error_code: "capability_unavailable", error: "This operation is served by the link layer" };
  }

  // Delivers a backend reverse call to the pipeline's reverse handler.
  reverse(op, params) {
    if (this.closed) return Promise.reject(new LinkError("backend_unavailable", "Device link closed"));
    const id = crypto.randomUUID();
    const envelope = params && typeof params === "object" && !Array.isArray(params) && Object.hasOwn(params, "body") ? params : { body: params ?? {} };
    const request = { type: "task.invoke", profile: V2_PROFILE, id, operation: op,
      installation_id: envelope.installation_id || this.installationID,
      ...(envelope.params ? { params: envelope.params } : {}), ...(envelope.request_id ? { request_id: envelope.request_id } : {}),
      ...(envelope.operation_id ? { operation_id: envelope.operation_id } : {}), body: envelope.body };
    return new Promise((resolve, reject) => {
      this.reverseCalls.set(id, { resolve: (response) => resolve({ status: response.status, ...(response.body !== undefined ? { body: response.body } : {}),
        ...(response.error_code ? { error_code: response.error_code } : {}), ...(response.error ? { error: response.error } : {}) }), reject });
      this.#emit({ type: "reverse_request", id, request });
    });
  }
}

function failure(error) {
  if (notSent(error)) return { status: 409, error_code: "backend_unavailable", error: "The backend is unreachable", body: { error: "The backend is unreachable", code: "operation_not_sent", error_code: "backend_unavailable", retryable: true }, retryable: true };
  if (error instanceof LinkError) {
    const status = { unsupported_capability: 501, invalid_request: 400, not_found: 404, conflict: 409, unauthorized: 401, revoked: 401, too_large: 413, quota_exceeded: 413, busy: 429, timeout: 504, cancelled: 499 }[error.code] || 503;
    return { status, error_code: error.code === "timeout" ? "operation_outcome_unknown" : error.code, error: error.message, retryable: error.retryable };
  }
  return { status: 503, error_code: "operation_outcome_unknown", error: "The backend request failed", retryable: true };
}
