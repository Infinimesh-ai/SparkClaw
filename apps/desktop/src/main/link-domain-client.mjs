import crypto from "node:crypto";
import path from "node:path";
import { traceLatency, latencyNow } from "./latency-trace.mjs";
import { PRESENTATION_ROUTES, REASON_STATUS, TYPED_METHODS, consoleRoute, linkDomainOperations, mapRoute, operationSpec } from "./link-domain-routes.mjs";
import { LinkMutationJournal } from "./link-mutation-journal.mjs";
import { LocalMailAttachmentError, localAttachmentErrorResponse, prepareMailAttachments } from "./link-mail-attachments.mjs";
import { LINK_REVERSE_OPERATIONS, LinkError, linkNotSent } from "./link-transport.mjs";

export const DOMAIN_PROFILE = "sparkclaw.workbench.transport.v2";
// A link frame carries 600 KiB. Bodies above this travel as a domain object.
export const INLINE_BODY_BYTES = 480 * 1024;
const CHUNK_BYTES = 256 * 1024;
const MAX_OBJECT_BYTES = 64 * 1024 * 1024;
const UUID = /^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/u;
const BEFORE_SEND = Symbol("durable mutation attempt");
const ORIGINAL_DEADLINE = Symbol("original mutation deadline");
const sha256 = (bytes) => crypto.createHash("sha256").update(bytes).digest("hex");

// A request that certainly did not reach the backend's handler: the caller
// may show the change as not made and let the user try again.
export class LinkRequestNotSentError extends Error {
  constructor(message, reason = "validation") {
    super(message);
    this.name = "LinkRequestNotSentError";
    this.reason = reason;
  }
}

function requestTimeout(operation, timeoutMS) {
  // The Weixin provider can hold a QR request for up to 35 seconds.
  if (timeoutMS === 30000 && ["settings.bindings.start", "settings.bindings.poll"].includes(operation)) return 60000;
  // Medicine recognition is one vision model call on a reduced photo.
  if (timeoutMS === 30000 && operation === "medicine.recognize") return 120000;
  // Reading a typed medicine is one short text model call.
  if (timeoutMS === 30000 && operation === "medicine.parse") return 60000;
  return ["mail.drafts.save", "mail.drafts.send", "mail.drafts.reconcile", "mail.drafts.resolve", "mail.providers.check", "mail.providers.login", "mail.intake.update", "mail.replies.polish", "mail.source.cleanup", "mail.conversations.delete"].includes(operation) && timeoutMS === 30000 ? 180000 : timeoutMS;
}

// A reconciliation keeps one durable intent per uncertain browser write.
function browserReconcileResource(request) {
  if (request.operation === "browser.reconcile") return `browser-reconcile:${request.installation_id || ""}:${request.body?.command_id}`;
}

function mailMutationResource(request) {
  const { operation, params = {}, body = {} } = request;
  if (operation.startsWith("mail.drafts.")) return `mail-draft:${params.draft || body.id || "list"}${operation === "mail.drafts.reconcile" ? ":reconcile" : operation === "mail.drafts.resolve" ? ":resolve" : ""}`;
  if (operation.startsWith("mail.providers.") || operation === "mail.intake.update") return `mail-provider:${params.provider || "list"}:${operation.split(".").at(-1)}`;
  if (params.mail) return `mail-message:${params.mail}`;
  if (params.conversation) return `mail-conversation:${params.conversation}`;
  if (params.rule) return `mail-rule:${params.rule}`;
  if (params.warning) return `mail-warning:${params.warning}`;
  // Bounded service work or a batch update: its original receipt must
  // resolve before another request of that kind.
  return operation;
}

function presentationResource(request) {
  const family = request.operation.split(".")[1];
  if (request.operation.startsWith("medicine.")) return `medicine:${request.body?.operation_id || request.params?.item || "box"}`;
  if (request.operation.startsWith("mail.")) return mailMutationResource(request);
  if (request.operation.startsWith("notifications.")) return "notifications";
  if (family === "owner") return "owner";
  if (family === "connectors") return "connectors";
  if (family === "bindings") return `binding:${request.params?.binding_id || request.params?.channel || "list"}`;
  if (family === "browser") return "browser-extension";
  if (family === "mcp") return `mcp:${request.operation.split(".")[2]}:${request.params?.ticket_id || request.params?.binding_id || ""}`;
  return `integration:${request.params?.integration_id || ""}`;
}

// Changes recorded in the mutation journal: settings, notifications and the
// mail and medicine changes the original screens make.
function durableChange(operation) {
  const spec = operationSpec(operation);
  return operation.startsWith("medicine.") && spec.mutation || operation.startsWith("settings.") || operation.startsWith("notifications.") ||
    operation.startsWith("mail.") && spec.mutation || operation.startsWith("mail.drafts.") || operation.startsWith("mail.providers.");
}

const CODE_STATUS = Object.freeze({ unsupported_capability: 501, invalid_request: 400, not_found: 404, conflict: 409, unauthorized: 401, revoked: 401, too_large: 413, quota_exceeded: 413, busy: 429, timeout: 504, cancelled: 499 });

function jsonResponse(status, body, headers = {}) {
  return new Response([204, 205, 304].includes(status) || body === undefined ? null : JSON.stringify(body), { status, headers: { "content-type": "application/json", ...headers } });
}

function mutationResponse(value) { return jsonResponse(value.status, value.body); }

// The domain result as the original screens read it: an HTTP status, a JSON
// body and the typed error code in x-sparkclaw-error-code.
function resultResponse(result) {
  const payload = result.status >= 400 && result.body === undefined
    ? { error: result.error || "The backend operation failed", error_code: result.error_code || "operation_failed", retryable: result.retryable === true }
    : result.body;
  return jsonResponse(result.status, payload, {
    ...(result.error_code ? { "x-sparkclaw-error-code": result.error_code } : {}),
    ...(result.retryable !== undefined ? { "x-sparkclaw-retryable": String(result.retryable === true) } : {}) });
}

// A link failure in the shape the original screens handle. A typed
// method's failure carries its domain reason, which keeps the original
// status and error code.
function failureResult(error) {
  if (error instanceof LinkError && error.reason && !linkNotSent(error)) {
    const status = REASON_STATUS[error.reason] ?? CODE_STATUS[error.code] ?? 503;
    return { status, error_code: error.reason, error: error.message || error.reason, retryable: error.retryable,
      body: { code: error.reason, error: error.message || error.reason, error_code: error.reason, retryable: error.retryable } };
  }
  if (linkNotSent(error)) return { status: 409, error_code: "backend_unavailable", error: "The backend is unreachable", body: { error: "The backend is unreachable", code: "operation_not_sent", error_code: "backend_unavailable", retryable: true }, retryable: true };
  if (error instanceof LinkError) {
    if (error.code === "outcome_unknown") return { status: 409, error_code: "operation_outcome_unknown", error: error.message, body: { error: error.message, code: "operation_outcome_unknown", error_code: "operation_outcome_unknown", retryable: false }, retryable: false };
    const status = CODE_STATUS[error.code] || 503;
    return { status, error_code: error.code === "timeout" ? "operation_outcome_unknown" : error.code, error: error.message, retryable: error.retryable };
  }
  return { status: 503, error_code: "operation_outcome_unknown", error: "The backend request failed", retryable: true };
}

// The typed fields of a request to a typed method.
function typedRequest(spec, body, params) {
  return spec.typed ? { args: { ...(body ?? {}), ...(params ?? {}) }, typed: true, ...(spec.reply ? { reply: true } : {}) } : {};
}

// The arguments a typed request is sent with: its own, with the revision a
// settings change was made against.
function callArgs(request) {
  return { ...(request.args ?? {}), ...(request.reply && request.expected_revision ? { expected_revision: request.expected_revision } : {}) };
}

// A change recorded before its domain moved to typed methods is sent as
// one: the records its route named and the body's fields.
function typedRecord(request) {
  const typed = TYPED_METHODS.get(request.operation);
  if (!typed || request.typed) return request;
  return { ...request, typed: true, ...(typed.reply ? { reply: true } : {}), args: { ...(request.body ?? {}), ...(request.params ?? {}) } };
}

// LinkDomainClient reaches the business domains with link domain.call
// (docs/workbench-retirement-migration.md M3). It keeps the request
// interface the main-process clients and the original screens use: fetch
// for HTTP-shaped routes and invoke for named operations. Each change is
// recorded with its idempotency key before it is sent; a change whose reply
// was lost is answered from the backend's receipt with replay_only, so it
// never runs twice. Large bodies and results travel as domain objects.
export class LinkDomainClient {
  constructor({ link, origin, binding, installationID, domains = [], readLocalFile, canSendMailAttachments = () => false, journalRoot, mutationProtector, timeoutMS = 30000 }) {
    Object.assign(this, { link, origin, binding, expectedBinding: binding, installationID, domains, readLocalFile, canSendMailAttachments, journalRoot, mutationProtector, timeoutMS });
    this.resourceRevisions = new Map();
    this.presentationPending = new Set();
    this.mutationFlights = new Map();
    this.generation = 0;
    this.state = "closed";
  }

  // The operations this connection reaches, in the manifest shape the
  // capability projection reads.
  #manifest() {
    return Object.freeze({ schema_version: 2, profile: DOMAIN_PROFILE, session_id: this.sessionID, authorization_revision: 1,
      expires_at: new Date(Date.now() + 365 * 24 * 3600 * 1000).toISOString(), binding: this.binding,
      // Reverse operations are declared at the link hello; the backend calls
      // only those this device serves.
      operations: Object.freeze([...linkDomainOperations(this.domains), ...Object.values(PRESENTATION_ROUTES), ...LINK_REVERSE_OPERATIONS]),
      limits: Object.freeze({ concurrent: 0, body_bytes: INLINE_BODY_BYTES, response_bytes: INLINE_BODY_BYTES }) });
  }

  start() {
    this.close();
    this.sessionID = `link:${crypto.randomUUID()}`;
    const scope = { ...this.binding, installation_id: this.installationID };
    this.objectScopeKey = JSON.stringify({ ...scope, authorization_revision: 1 });
    if (this.journalRoot) this.mutations = new LinkMutationJournal(path.join(this.journalRoot, "mutations"), scope, this.mutationProtector);
    this.objects = new LinkDomainObjects(this);
    this.capabilities = this.#manifest();
    this.state = "transport_ready";
    this.recoveryTimer = setInterval(() => this.#wakeMutationRecovery(), 5000);
    this.recoveryTimer.unref?.();
    queueMicrotask(() => this.#wakeMutationRecovery());
    return Promise.resolve();
  }

  close() {
    ++this.generation;
    clearInterval(this.recoveryTimer); this.recoveryTimer = undefined;
    this.mutationFlights.clear();
    this.capabilities = undefined;
    this.state = "closed";
  }

  #current(generation) {
    if (generation !== this.generation || this.state !== "transport_ready") throw new LinkRequestNotSentError("Backend connection changed", "unavailable");
  }

  // ---- HTTP-shaped routes of the original screens

  async fetch(raw, init = {}) {
    if (this.state !== "transport_ready") throw new LinkRequestNotSentError("The backend is unreachable", "unavailable");
    const url = new URL(raw);
    if (url.origin !== this.origin) throw new LinkRequestNotSentError("Backend path is invalid");
    const method = (init.method || "GET").toUpperCase();
    if (init.body !== undefined && init.body !== null && (init.body === "" || init.body.byteLength === 0)) init = { ...init, body: undefined };
    let body;
    if (init.body !== undefined && init.body !== null) {
      try { body = JSON.parse(typeof init.body === "string" ? init.body : new TextDecoder("utf-8", { fatal: true }).decode(init.body)); }
      catch { throw new LinkRequestNotSentError("Request body must be JSON"); }
    }
    // The original screens' domain routes first; the first-version
    // presentation reads cover the rest (/api/config, /readyz).
    const presentation = !url.search && body === undefined ? PRESENTATION_ROUTES[`${method} ${url.pathname}`] : undefined;
    if (consoleRoute(url)) return jsonResponse(410, { error: "Devices join and leave on the SparkClaw console", code: "devices_are_managed_on_the_console" });
    let mapped;
    try { mapped = mapRoute(url, method, body); }
    catch (error) { if (presentation) return this.#presentationRead(presentation, init); throw new LinkRequestNotSentError(error.message); }
    if (presentation && !this.capabilities.operations.includes(mapped.operation)) return this.#presentationRead(presentation, init);
    const headers = new Headers(init.headers);
    const actionID = headers.get("x-sparkclaw-action-id");
    if (actionID && !UUID.test(actionID)) throw new LinkRequestNotSentError("Action identity is invalid");
    if (actionID) init = { ...init, actionID };
    const request = { ...mapped, installation_id: headers.get("x-sparkclaw-installation") || this.installationID,
      ...(mapped.params?.request_id ? { request_id: mapped.params.request_id } : {}),
      ...(headers.get("x-sparkclaw-digest") ? { input_digest: headers.get("x-sparkclaw-digest") } : {}),
      ...(init.operationID ? { operation_id: init.operationID } : {}), ...(init.expectedRevision ? { expected_revision: init.expectedRevision } : {}) };
    if (durableChange(request.operation)) return this.#presentation(request, init);
    return this.#request(request, init);
  }

  // The first-version presentation reads.
  async #presentationRead(operation, init) {
    try {
      if (operation === "presentation.owner") {
        const result = (await this.#domainCall({ operation: "settings.owner.get", typed: true, reply: true, args: {} }, init)).result;
        if (result?.status === 200 && result.body && Object.hasOwn(result.body, "value")) return jsonResponse(200, result.body.value);
        return resultResponse(result);
      }
      try { return resultResponse((await this.#domainCall({ operation, args: {} }, init, "presentation")).result); }
      catch (error) {
        if (!(error instanceof LinkError) || error.code !== "unsupported_capability" || operation !== "presentation.ready") throw error;
      }
      // A backend without the readiness read: the link answered, and speech
      // readiness comes from the speech domain.
      const speech = await this.#domainCall({ operation: "speech.status", typed: true, args: {} }, init).then(reply => reply.result).catch(() => undefined);
      return jsonResponse(200, { ok: true, ...(speech ? { speech } : {}) });
    } catch (error) { return resultResponse(failureResult(error)); }
  }

  // ---- durable changes

  async #presentation(request, init) {
    const spec = operationSpec(request.operation);
    const resource = presentationResource(request);
    // Reading state stays possible while an uncertain write is reconciled.
    if (!spec.mutation) return this.#presentationResponse(resource, await this.#request(request, init));
    if (this.presentationPending.has(resource)) throw new LinkRequestNotSentError("A change to this resource is already in progress", "capacity");
    this.presentationPending.add(resource);
    try { return await this.#applyPresentation(resource, request, init); }
    finally { this.presentationPending.delete(resource); }
  }

  async #applyPresentation(resource, request, init) {
    const journal = this.mutations;
    if (!journal) throw new LinkRequestNotSentError("Durable change recovery is unavailable");
    let record = journal.read(resource);
    const revision = this.resourceRevisions.get(resource);
    const confirmedSuccessor = record?.phase === "applied" && !browserReconcileResource(request) && (
      init.actionID && record.action_id && init.actionID !== record.action_id ||
      !(init.actionID && record.action_id) && revision && record.response?.body?.revision && revision !== record.response.body.revision);
    if (confirmedSuccessor) {
      journal.retire(resource, record.operation_id, { reason: "new_foreground_action", ...(init.actionID ? { action_id: init.actionID } : {}), ...(revision ? { revision } : {}) });
      record = undefined;
    }
    if (record?.phase === "applied" && record.response?.body?.code === "operation_not_sent") {
      // Only a new foreground action succeeds a change that was never sent.
      journal.retire(resource, record.operation_id, { reason: "explicit_retry_after_non_submission" });
      record = undefined;
    }
    if (record && !(record.phase === "applied" && !journal.matches(record, request))) {
      const response = await this.#reconcileMutation(journal, resource, record, init);
      if (!journal.matches(record, request)) {
        const receivingNotApplied = record.operation === "mail.intake.update" && request.operation === record.operation &&
          journal.read(resource)?.response?.body?.code === "email_intake_not_applied";
        if (journal.read(resource)?.response) journal.complete(resource, record.operation_id);
        // Deliver the original explanation before admitting a later action.
        if (receivingNotApplied) return this.#presentationResponse(resource, response);
        throw new Error("The previous change was reconciled. Review the current value before making another change");
      }
      if (journal.read(resource)?.response || record.schema_version === 1) journal.complete(resource, record.operation_id);
      return this.#presentationResponse(resource, response);
    }
    record = journal.prepare(resource, request, { revision, actionID: init.actionID, deadline: Date.now() + requestTimeout(request.operation, this.timeoutMS), authorizationRevision: 1 });
    const response = await this.#reconcileMutation(journal, resource, record, init);
    if (journal.read(resource)?.response) journal.complete(resource, record.operation_id);
    return this.#presentationResponse(resource, response);
  }

  async #reconcileMutation(journal, resource, record, init = {}) {
    const key = `${journal.scope}:${record.operation_id}`;
    if (this.mutationFlights.has(key)) return (await this.mutationFlights.get(key)).clone();
    const generation = this.generation;
    const current = () => { if (journal !== this.mutations) throw new LinkRequestNotSentError("Backend connection changed", "unavailable"); this.#current(generation); };
    const run = async () => {
      current();
      if (record.response) return mutationResponse(record.response);
      if (record.schema_version !== 2 || record.phase !== "prepared") {
        // The change was sent and its reply was lost: the backend's receipt
        // answers, and the change is never sent again.
        let receipt;
        try { receipt = await this.#replay(record.request, record.operation_id, init); }
        catch (error) {
          if (error instanceof LinkError && error.code === "not_found" && record.schema_version === 2) {
            // The backend records every change before running it: none ran.
            current(); journal.saveResult(resource, record.operation_id, { status: 409, body: { error: "The original change did not reach the backend", code: "operation_not_sent" } });
            return mutationResponse(journal.read(resource).response);
          }
          if (error instanceof LinkError && error.code === "outcome_unknown" && record.schema_version === 2 && record.operation === "mail.intake.update" &&
              this.capabilities.operations.includes("mail.intake.reconcile")) receipt = await this.#reconcileIntake(record, init);
          else if (error instanceof LinkError && error.code === "outcome_unknown") throw new Error("The previous change has an unknown outcome; it will not be repeated");
          else throw error;
        }
        current();
        if (record.schema_version === 2) journal.saveResult(resource, record.operation_id, receipt);
        return mutationResponse(receipt);
      }
      if (record.not_after <= Date.now()) {
        journal.cancelPrepared(resource, record.operation_id, "Original change expired before it was sent");
        return mutationResponse(journal.read(resource).response);
      }
      let request = record.request;
      try {
        request = await prepareMailAttachments(this, request, init.signal);
        current();
        request = { ...request, operation_id: record.operation_id, ...(record.expected_revision ? { expected_revision: record.expected_revision } : {}) };
        const response = await this.#request(request, { ...init, [ORIGINAL_DEADLINE]: record.not_after,
          [BEFORE_SEND]: () => { current(); journal.markAttempted(resource, record.operation_id); } });
        current();
        const body = await response.clone().json().catch(() => undefined);
        const unresolved = [body?.code, body?.error_code, response.headers.get("x-sparkclaw-error-code")].some(code => ["operation_outcome_unknown", "operation_receipt_unavailable"].includes(code));
        const notSent = body?.code === "operation_not_sent";
        // Unclassified server failures cannot prove whether an effect committed.
        if (!unresolved && (response.status < 500 || notSent)) journal.saveResult(resource, record.operation_id, { status: response.status, ...(body !== undefined ? { body } : {}) });
        return response;
      } catch (error) {
        if (error instanceof LocalMailAttachmentError) {
          current(); journal.cancelPrepared(resource, record.operation_id, error.message);
          return localAttachmentErrorResponse(error);
        }
        if (error instanceof LinkRequestNotSentError && ["canceled", "validation"].includes(error.reason)) journal.cancelPrepared(resource, record.operation_id);
        throw error;
      }
    };
    const flight = run(); this.mutationFlights.set(key, flight);
    try { return (await flight).clone(); }
    finally { if (this.mutationFlights.get(key) === flight) this.mutationFlights.delete(key); }
  }

  // An unfinished receiving-mode change is settled by the mail domain from
  // the original request; the backend checks it against its own receipt.
  async #reconcileIntake(record, init) {
    // The original change is found again by its key and its arguments.
    const original = typedRecord({ ...record.request, ...(record.expected_revision ? { expected_revision: record.expected_revision } : {}) });
    const resolved = await this.invoke("mail.intake.reconcile", { idempotency_key: record.operation_id, original: callArgs(original) }, { signal: init.signal });
    if (resolved?.idempotency_key !== record.operation_id || resolved.state !== "completed" || !Number.isInteger(resolved.response?.status)) throw new Error("The previous change has an unknown outcome; it will not be repeated");
    return { status: resolved.response.status, ...(resolved.response.body !== undefined ? { body: resolved.response.body } : {}) };
  }

  // Changes whose reply was lost are reconciled once the backend is reachable.
  #wakeMutationRecovery() {
    if (this.state !== "transport_ready" || !this.mutations || !this.link.online) return;
    const generation = this.generation, journal = this.mutations;
    if (this.recoveryScan?.generation === generation && this.recoveryScan.journal === journal) return;
    const scan = { generation, journal }; this.recoveryScan = scan;
    void (async () => {
      for (const record of journal.pending()) {
        if (generation !== this.generation || journal !== this.mutations || this.state !== "transport_ready") break;
        if (this.presentationPending.has(record.resource_key)) continue;
        if (!this.capabilities.operations.includes(record.operation)) continue;
        await this.#reconcileMutation(journal, record.resource_key, record).catch(() => {});
      }
    })().catch(() => {}).finally(() => { if (this.recoveryScan === scan) this.recoveryScan = undefined; });
  }

  async #presentationResponse(resource, response) {
    if (!response.ok) return response;
    const result = await response.json();
    if (result && typeof result.revision === "string" && Object.hasOwn(result, "value")) {
      this.resourceRevisions.set(resource, result.revision);
      for (const [id, revision] of Object.entries(result.value?.resource_revisions || {})) this.resourceRevisions.set(`integration:${id}`, revision);
      return jsonResponse(response.status, result.value);
    }
    return jsonResponse(response.status, result);
  }

  // ---- named operations

  async invoke(operation, body, options = {}) {
    const spec = operationSpec(operation);
    const request = { operation, ...(body !== undefined ? { body } : {}), ...typedRequest(spec, body, options.params), ...(options.params ? { params: options.params } : {}),
      ...(options.operationID ? { operation_id: options.operationID } : {}), ...(options.expectedRevision ? { expected_revision: options.expectedRevision } : {}),
      installation_id: options.installationID || this.installationID, ...(options.requestID ? { request_id: options.requestID } : {}) };
    const browserResource = browserReconcileResource(request);
    let response;
    if (browserResource) {
      if (this.presentationPending.has(browserResource)) throw new LinkRequestNotSentError("A browser reconciliation is already in progress", "capacity");
      this.presentationPending.add(browserResource);
      try { response = await this.#applyPresentation(browserResource, request, options); }
      finally { this.presentationPending.delete(browserResource); }
    } else {
      if (spec.mutation && !request.operation_id) request.operation_id = crypto.randomUUID();
      response = await this.#request(request, options);
    }
    const result = await response.json().catch(() => ({}));
    if (!response.ok) {
      const bodyCode = typeof result?.code === "string" && /^[a-z][a-z0-9_]{0,79}$/u.test(result.code) ? result.code : undefined;
      const error = new Error(result?.error || `Backend operation failed (${bodyCode || result?.error_code || response.status})`);
      error.code = result?.error_code || response.headers.get("x-sparkclaw-error-code") || bodyCode;
      error.status = response.status;
      error.retryable = result?.retryable === true || response.headers.get("x-sparkclaw-retryable") === "true";
      throw error;
    }
    return result;
  }

  // The original outcome of a change sent with operationID, without ever
  // running it again: {state: "completed", response} or an error coded
  // operation_not_found (it never ran) or operation_outcome_unknown.
  async replay(operation, body, options = {}) {
    if (!UUID.test(options.operationID || "")) throw new Error("Replay needs the original operation identity");
    const request = { operation, ...(body !== undefined ? { body } : {}), ...typedRequest(operationSpec(operation), body, options.params), ...(options.params ? { params: options.params } : {}),
      ...(options.expectedRevision ? { expected_revision: options.expectedRevision } : {}), installation_id: options.installationID || this.installationID,
      ...(options.requestID ? { request_id: options.requestID } : {}) };
    try { return { state: "completed", operation_id: options.operationID, response: await this.#replay(request, options.operationID, options) }; }
    catch (error) {
      if (error instanceof LinkError && ["not_found", "outcome_unknown"].includes(error.code)) {
        throw Object.assign(new Error(error.code === "not_found" ? "The original operation never ran" : "The original operation has an unknown outcome"), { code: error.code === "not_found" ? "operation_not_found" : "operation_outcome_unknown" });
      }
      throw error;
    }
  }

  async #replay(request, operationID, init = {}) {
    request = typedRecord(request);
    const reply = await this.#domainCall({ ...request, operation_id: operationID }, { ...init, replayOnly: true });
    if (request.typed && !request.reply) return { status: 200, body: reply?.result };
    const result = reply?.result;
    if (!result || !Number.isInteger(result.status)) throw new LinkError("internal", "Domain result is malformed");
    let body = result.body;
    if (result.body_object) body = JSON.parse(Buffer.from(await this.objects.download(result.body_object, init.signal)).toString("utf8"));
    return { status: result.status, ...(body !== undefined ? { body } : {}) };
  }

  // ---- the link call

  // One domain.call: a typed method with its own arguments, and the
  // revision a settings change was made against. Arguments too large for
  // one frame travel as a domain object.
  async #domainCall(request, init = {}, domain = request.operation.split(".")[0]) {
    request = typedRecord(request);
    const args = callArgs(request);
    const raw = Buffer.from(JSON.stringify(args));
    const argsObject = raw.length > INLINE_BODY_BYTES ? await this.objects.upload(raw, { purpose: "domain_args", name: "args.json", media_type: "application/json" }, init.signal) : undefined;
    const timeoutMS = Math.max(1, (init[ORIGINAL_DEADLINE] ?? Date.now() + requestTimeout(request.operation, this.timeoutMS)) - Date.now());
    return this.link.request("domain.call", { domain, method: request.operation, ...(argsObject ? { args_object: argsObject.object_id } : { args }),
      ...(request.operation_id ? { idempotency_key: request.operation_id } : {}), ...(init.replayOnly ? { replay_only: true } : {}) }, { timeoutMS, signal: init.signal });
  }

  async #request(request, init = {}) {
    if (this.state !== "transport_ready") throw new LinkRequestNotSentError("The backend is unreachable", "unavailable");
    if (!this.capabilities.operations.includes(request.operation)) throw new LinkRequestNotSentError("This capability is unavailable");
    const spec = operationSpec(request.operation);
    if (spec.mutation && !request.operation_id) request = { ...request, operation_id: crypto.randomUUID() };
    if (init.signal?.aborted) throw new LinkRequestNotSentError("Request was canceled", "canceled");
    const generation = this.generation;
    const started = latencyNow();
    traceLatency("rpc.dispatch", { operation: request.operation, request_id: request.request_id });
    let result;
    try {
      init[BEFORE_SEND]?.();
      const reply = await this.#domainCall(request, init);
      // A typed method answers with its result; the original screens read
      // it as a successful response.
      result = request.typed && !request.reply ? { status: 200, body: reply?.result ?? null } : reply?.result;
      if (!result || !Number.isInteger(result.status)) throw new LinkError("internal", "Domain result is malformed");
    } catch (error) {
      if (error instanceof LinkRequestNotSentError) throw error;
      result = failureResult(error);
    }
    traceLatency("rpc.response", { operation: request.operation, duration_ms: latencyNow() - started, status: result.status });
    if (generation !== this.generation) throw new LinkRequestNotSentError("Backend connection changed", "unavailable");
    if (result.body_object) {
      const bytes = await this.objects.download(result.body_object, init.signal);
      return new Response(bytes, { status: result.status, headers: { "content-type": result.body_object.media_type || "application/json", "content-length": String(result.body_object.size),
        "x-sparkclaw-digest": result.body_object.sha256, ...(result.body_object.name ? { "x-sparkclaw-file-name": encodeURIComponent(result.body_object.name) } : {}) } });
    }
    return resultResponse(result);
  }
}

// Domain objects over the link object operations (link contract section 4).
export class LinkDomainObjects {
  constructor(client) { this.client = client; }

  async upload(bytes, { purpose, name = "object", media_type = "application/octet-stream" }, signal) {
    bytes = Buffer.from(bytes);
    if (bytes.length > MAX_OBJECT_BYTES) throw new Error("Object exceeds the transfer limit");
    const digest = sha256(bytes);
    const link = this.client.link;
    let state = await link.request("object.upload.begin", { upload_key: `${purpose}:${crypto.randomUUID()}`, name, size: bytes.length, content_type: media_type, sha256: digest, purpose }, { signal });
    while (!state.object_id && state.received < bytes.length) {
      signal?.throwIfAborted();
      const size = Math.min(state.chunk_size || CHUNK_BYTES, CHUNK_BYTES);
      state = await link.request("object.upload.chunk", { upload_id: state.upload_id, offset: state.received, data: bytes.subarray(state.received, state.received + size).toString("base64") }, { timeoutMS: 60000, signal });
    }
    if (!state.object_id) state = await link.request("object.upload.commit", { upload_id: state.upload_id }, { timeoutMS: 60000, signal });
    return { object_id: state.object_id, version: 1, size: bytes.length, sha256: digest, purpose, name, media_type };
  }

  async download(ref, signal) {
    if (!Number.isSafeInteger(ref?.size) || ref.size < 0 || ref.size > MAX_OBJECT_BYTES) throw new Error("Object reference is invalid");
    const chunks = [];
    let offset = 0;
    for (;;) {
      signal?.throwIfAborted();
      const part = await this.client.link.request("object.read", { object_id: ref.object_id, offset, length: CHUNK_BYTES }, { timeoutMS: 60000, signal });
      const bytes = Buffer.from(part.data || "", "base64");
      if (part.offset !== offset) throw new Error("Object chunk is out of order");
      chunks.push(bytes); offset += bytes.length;
      if (offset > ref.size) throw new Error("Object exceeds its reference");
      if (part.eof || !bytes.length) break;
    }
    const bytes = Buffer.concat(chunks);
    if (bytes.length !== ref.size || (ref.sha256 && sha256(bytes) !== ref.sha256)) throw new Error("Object content differs from its reference");
    return bytes;
  }
}
