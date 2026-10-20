import { spawn } from "node:child_process";
import { traceLatency, latencyNow } from "./latency-trace.mjs";
import crypto from "node:crypto";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { mapV2Route, requireOperation } from "./iscp-routes.mjs";
import { ISCPMutationJournal } from "./iscp-mutation-journal.mjs";
import { LocalMailAttachmentError, localAttachmentErrorResponse, prepareMailAttachments } from "./iscp-mail-attachments.mjs";
import { ISCPObjectClient } from "./iscp-object-client.mjs";
import { browserMutationResource, expireBrowserGrant, settleBrowserLifecycle } from "./iscp-browser-recovery.mjs";
export const ISCP_V2_PROFILE = "sparkclaw.workbench.transport.v2";

export const ISCP_PROFILE = "sparkclaw.workbench.transport.v1";
export const ISCP_OPERATIONS = Object.freeze(["workbench.identity", "installation.bind", "presentation.config", "presentation.owner", "presentation.ready", "execution.submit", "execution.lookup", "execution.cancel", "execution.ack"]);
const MAX_BYTES = 65536;
const FRAME_BYTES = MAX_BYTES + 8192;
export const ISCP_BODY_BYTES = MAX_BYTES - 2048;
const UUID = /^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/u;
const CLIENT_UI_KEY = /^client-[a-f0-9]{48}$/u;
const MODULE_DIR = path.dirname(fileURLToPath(import.meta.url));
const CAPACITIES = Object.freeze({v1:4,business:4,control:1,bulk:2,events:1,audio:2}); // Legacy peer compatibility only.
const MAIL_READ_LIMIT = 3; // Reserve the fourth business slot for explicit actions.
const ADMISSION_QUEUE_LIMIT = 24;
const BEFORE_SEND = Symbol('durable mutation attempt');
const ORIGINAL_DEADLINE = Symbol('original mutation deadline');
function requestTimeout(operation, timeoutMS) {
  // The Weixin provider can hold a QR request for up to 35 seconds.
  // Keep one bounded operation deadline across admission, send and recovery.
  if (timeoutMS === 30000 && ["settings.bindings.start", "settings.bindings.poll"].includes(operation)) return 60000;
  return ["mail.drafts.save", "mail.drafts.send", "mail.drafts.reconcile", "mail.drafts.resolve", "mail.providers.check", "mail.providers.login", "mail.intake.update", "mail.replies.polish", "mail.source.cleanup", "mail.conversations.delete"].includes(operation) && timeoutMS === 30000 ? 180000 : timeoutMS;
}
function mutationResponse(value) {
  return new Response([204, 205, 304].includes(value.status) ? null : JSON.stringify(value.body), { status: value.status, headers: { 'content-type': 'application/json' } });
}

function mailMutationResource(request) {
  const { operation, params = {}, body = {} } = request;
  if (operation.startsWith('mail.drafts.')) return `mail-draft:${params.draft || body.id || 'list'}${operation === 'mail.drafts.reconcile' ? ':reconcile' : operation === 'mail.drafts.resolve' ? ':resolve' : ''}`;
  if (operation.startsWith('mail.providers.') || operation === 'mail.intake.update') return `mail-provider:${params.provider || 'list'}:${operation.split('.').at(-1)}`;
  if (params.mail) return `mail-message:${params.mail}`;
  if (params.conversation) return `mail-conversation:${params.conversation}`;
  if (params.rule) return `mail-rule:${params.rule}`;
  if (params.warning) return `mail-warning:${params.warning}`;
  // These operations schedule bounded service work or update a batch. Their
  // original opaque receipt must resolve before another request of that kind.
  return operation;
}

// Only local validation before a pipe write may prove that a request was not
// sent. Timeouts, helper exits and aborts after write retain an unknown outcome.
export class ISCPRequestNotSentError extends Error {
  constructor(message, reason = "validation") {
    super(message);
    this.name = "ISCPRequestNotSentError";
    this.reason = reason;
  }
}

export function iscpHelperExecutable({ packaged = false, resourcesPath } = {}) {
  return packaged ? path.join(resourcesPath, "iscp-workbench") : path.resolve(MODULE_DIR, "../../bin/iscp-workbench");
}

// Private pipes are the only Desktop ↔ helper boundary. The helper owns SDK
// credentials and encrypted Relay traffic; this adapter accepts fixed RPC only.
export class ISCPTransport {
  constructor({ clientOperationVault, readLocalFile, canSendMailAttachments = () => false, configPath, origin, expectedIdentity, expectedBinding, installationID, packaged = false, resourcesPath, spawnProcess = spawn, timeoutMS = 30000, onState = () => {}, onCapabilities = () => {}, journalRoot, mutationProtector }) {
    Object.assign(this, { clientOperationVault, readLocalFile, canSendMailAttachments, configPath, origin, expectedIdentity, expectedBinding, installationID, packaged, resourcesPath, spawnProcess, timeoutMS, onState, onCapabilities, journalRoot, mutationProtector });
    this.resourceRevisions = new Map();
    this.presentationPending = new Set();
    this.mutationFlights = new Map();
    this.admissionQueue = new Set();
    this.pending = new Map(); this.generation = 0; this.state = "closed";
  }

  async control(type, operationID, expectedRevision) {
    if(!["authorization_delete","authorization_delete_receipt"].includes(type)||!UUID.test(operationID)||!Number.isSafeInteger(expectedRevision)||expectedRevision<1)throw new Error("Authorization deletion identity is invalid");
    const id=crypto.randomUUID();
    return new Promise((resolve,reject)=>{
      const child=this.spawnProcess(iscpHelperExecutable(this),["-config",this.configPath,"-control-only"],{stdio:["pipe","pipe","pipe"],windowsHide:true});
      let buffer=Buffer.alloc(0),done=false,verified=false;
      const finish=(error,value)=>{if(done)return;done=true;clearTimeout(timer);child.stdin.destroy();child.kill();error?reject(error):resolve(value);};
      const timer=setTimeout(()=>finish(new Error("Authorization deletion requires reconciliation")),this.timeoutMS);
      child.stderr.on('data',()=>{});child.on('error',()=>finish(new Error('Authorization control is unavailable')));child.on('exit',()=>finish(new Error('Authorization control is unavailable')));child.stdin.on('error',()=>finish(new Error('Authorization control is unavailable')));
      child.stdout.on('data',chunk=>{
        buffer=Buffer.concat([buffer,chunk]);if(buffer.length>FRAME_BYTES){finish(new Error('Authorization receipt exceeds bounds'));return;}
        for(;;){const offset=buffer.indexOf(10);if(offset<0)return;const line=buffer.subarray(0,offset);buffer=buffer.subarray(offset+1);let frame;try{frame=JSON.parse(line);}catch{finish(new Error('Authorization receipt is invalid'));return;}
          if(frame.ipc_version!==1){finish(new Error('Authorization receipt is invalid'));return;}
          if(frame.type==='hello') {if(frame.control_only!==true||this.expectedIdentity&&Object.keys(this.expectedIdentity).some(key=>frame.identity?.[key]!==this.expectedIdentity[key])){finish(new Error('Authorization control identity differs'));return;}verified=true;continue;}
          if(frame.type==='state')continue;
          if(!verified){finish(new Error('Authorization receipt preceded identity'));return;}
          if(frame.type!=='authorization_receipt'||frame.id!==id){finish(new Error('Authorization receipt identity differs'));return;}
          if(frame.error){finish(new Error('Authorization deletion requires reconciliation'));return;}
          const receipt=frame.receipt;
          if(receipt?.operation_id!==operationID||receipt.expected_revision!==expectedRevision||!Number.isSafeInteger(receipt.authorization_revision)||receipt.authorization_revision!==expectedRevision+1||receipt.state!=='revoked'||!Number.isFinite(Date.parse(receipt.deleted_at))){finish(new Error('Authorization receipt is invalid'));return;}
          finish(undefined,receipt);return;
        }
      });
      child.stdin.write(`${JSON.stringify({ipc_version:1,type,id,operation_id:operationID,expected_revision:expectedRevision})}\n`);
    });
  }

  async start() {
    if (this.state === "transport_ready") return;
    if (this.readyPromise) return this.readyPromise;
    this.close();
    const generation = this.generation;
    this.readyPromise = new Promise((resolve, reject) => { this.readyResolve = resolve; this.readyReject = reject; });
    const ready = this.readyPromise;
    this.readyTimer = setTimeout(() => this.#fail("disconnected"), this.timeoutMS);
    this.#state("connecting");
    try {
      if (!path.isAbsolute(this.configPath || "")) throw new Error("Invalid ISCP configuration path");
      const child = this.spawnProcess(iscpHelperExecutable(this), ["-config", this.configPath], { stdio: ["pipe", "pipe", "pipe"], windowsHide: true });
      this.child = child;
      this.buffer = Buffer.alloc(0); this.hello = false;
      child.stdout.on("data", (chunk) => { if (generation === this.generation) this.#read(chunk); });
      // Do not echo helper errors: even SDK diagnostics may contain credentials.
      child.stderr.on("data", () => {});
      child.on("error", () => { if (generation === this.generation) this.#fail("disconnected"); });
      child.on("exit", () => { if (generation === this.generation) this.#fail("disconnected"); });
      child.stdin.on("error", () => { if (generation === this.generation) this.#fail("disconnected"); });
    } catch { this.#fail("disconnected"); }
    return ready;
  }

  async relaySubmissionStatus(original) {
    if (this.state !== 'transport_ready') throw new Error('ISCP transport is unavailable');
    const id = crypto.randomUUID();
    return new Promise((resolve, reject) => {
      const finish = (handler, value) => { clearTimeout(timer); this.pending.delete(id); handler(value); };
      const timer = setTimeout(() => finish(reject, new Error('Relay submission status is unavailable')), Math.min(this.timeoutMS, 5000));
      this.pending.set(id, { request: { operation: 'relay_submission_status' }, originalSubmission: original,
        resolve: value => finish(resolve, value), reject: error => finish(reject, error) });
      try { this.child.stdin.write(`${JSON.stringify({ ipc_version: 1, type: 'relay_submission_status', id, message_id: original.message_id })}\n`); }
      catch { this.#fail('disconnected'); }
    });
  }

  close() {
    ++this.generation;
    clearTimeout(this.readyTimer);
    clearInterval(this.recoveryTimer); this.recoveryTimer = undefined;
    this.readyReject?.(new Error("ISCP transport is unavailable"));
    this.readyResolve = this.readyReject = this.readyPromise = undefined;
    for (const call of this.admissionQueue) call.fail(new ISCPRequestNotSentError("ISCP transport is unavailable", "unavailable"));
    for (const call of this.pending.values()) call.reject(new Error("ISCP transport is unavailable"));
    this.pending.clear();
    this.mutationFlights.clear();
    this.admission = undefined;
    this.capabilities = undefined; this.onCapabilities(undefined);
    this.capabilityValidUntil = undefined;
    this.streamSchemaVersion = undefined;
    const child = this.child; this.child = undefined;
    child?.stdin.destroy(); child?.kill();
    if (child) { const timer = setTimeout(() => { if (child.exitCode === null) child.kill("SIGKILL"); }, 1000); timer.unref?.(); }
    this.buffer = Buffer.alloc(0); this.hello = false;
    this.#state("closed");
  }

  async fetch(raw, init = {}) {
    if (this.state !== "transport_ready") throw new ISCPRequestNotSentError("ISCP transport is unavailable", "unavailable");
    // An empty body (for example a bodyless DELETE from the renderer) is no body.
    if (init.body !== undefined && init.body !== null && (init.body === "" || init.body.byteLength === 0)) init = { ...init, body: undefined };
    if (!this.capabilities) {
      let request;
      try { request = mapISCPRequest(raw, init, this.origin); }
      catch (error) { throw new ISCPRequestNotSentError(error.message); }
      return this.#dispatch(request, init, typeof init.body === "string" ? init.body : init.body === undefined ? undefined : new TextDecoder("utf-8", { fatal: true }).decode(init.body));
    }
    const url = new URL(raw);
    if(url.origin !== this.origin) throw new ISCPRequestNotSentError("ISCP backend path is invalid");
    const method = (init.method || "GET").toUpperCase();
    let body;
    if(init.body instanceof Uint8Array && method === 'PUT') {
      const mapped=mapV2Route(url,method);
      if(mapped.operation!=='execution.input.put'||!this.objects)throw new ISCPRequestNotSentError("Binary input is unavailable");
      const headers=new Headers(init.headers);
      const object=await this.objects.upload(init.body,{purpose:'execution_input',name:mapped.params.file_id,media_type:'application/octet-stream'},init.signal);
      return this.#request({...mapped,type:'task.invoke',profile:ISCP_V2_PROFILE,body_object:object,request_id:mapped.params.request_id,installation_id:headers.get('x-sparkclaw-installation'),input_digest:headers.get('x-sparkclaw-digest')},init);
    }
    if(init.body !== undefined && init.body !== null) {
      try {body=JSON.parse(typeof init.body === "string" ? init.body : new TextDecoder("utf-8",{fatal:true}).decode(init.body));}
      catch {throw new ISCPRequestNotSentError("ISCP request body must be JSON");}
    }
    let mapped;
    try { mapped=mapV2Route(url,method,body); if(!this.capabilities.operations.includes(mapped.operation)) { try { mapped=mapISCPRequest(raw,init,this.origin,true); } catch { /* The qualified operation itself will reject before send. */ } } }
    catch(error) {
      // v1 base operations remain valid inside the v2 envelope, including the
      // original execution request whose byte digest must survive unchanged.
      try { mapped=mapISCPRequest(raw,init,this.origin,true); }
      catch {throw new ISCPRequestNotSentError(error.message);}
    }
    const headers=new Headers(init.headers);
    const actionID = headers.get('x-sparkclaw-action-id');
    if (actionID && !UUID.test(actionID)) throw new ISCPRequestNotSentError('Action identity is invalid');
    if (actionID) init = { ...init, actionID };
    const request={...mapped,...(this.installationID?{installation_id:this.installationID}:{}),type:"task.invoke",profile:ISCP_V2_PROFILE,...(mapped.params?.request_id?{request_id:mapped.params.request_id}:{}),
      ...(headers.get("x-sparkclaw-installation")?{installation_id:headers.get("x-sparkclaw-installation")}:{}),
      ...(headers.get("x-sparkclaw-digest")?{input_digest:headers.get("x-sparkclaw-digest")}:{}),
      ...(init.operationID?{operation_id:init.operationID}:{}),...(init.expectedRevision?{expected_revision:init.expectedRevision}:{})};
    const rawBody=mapped.body !== body && mapped.operation === 'mail.drafts.save' ? JSON.stringify(mapped.body) : init.body === undefined ? undefined : typeof init.body === "string" ? init.body : new TextDecoder("utf-8",{fatal:true}).decode(init.body);
    if (request.operation.startsWith('clients.')) return this.#clients(request, init, rawBody, headers);
    if(request.operation.startsWith("settings.") || request.operation.startsWith("notifications.") || request.operation.startsWith("mail.") && requireOperation(request.operation).mutation || request.operation.startsWith("mail.drafts.") || request.operation.startsWith("mail.providers.") || request.operation === "execution.approval") return this.#presentation(request,init,rawBody);
    return this.#request(request,init,rawBody);
  }

  async #clients(request, init, rawBody, headers) {
    if (request.operation === 'clients.list') return this.#request(request, init, rawBody);
    if (!this.clientOperationVault) throw new ISCPRequestNotSentError('Native secure operation storage is unavailable');
    const requested = request.operation === 'clients.issue' ? headers.get('idempotency-key') : undefined;
    const legacyIssue = request.operation === 'clients.issue' && CLIENT_UI_KEY.test(requested || '');
    if (request.operation === 'clients.issue' && !UUID.test(requested || '') && !legacyIssue) throw new ISCPRequestNotSentError('Device issuance requires its original operation identity');
    const resource = request.operation === 'clients.issue' ? `clients.issue:${requested}` : `${request.operation}:${request.params.id}`;
    if (this.presentationPending.has(resource)) throw new ISCPRequestNotSentError('A device operation is already in progress', 'capacity');
    this.presentationPending.add(resource);
    try {
      const fingerprint = crypto.createHash('sha256').update(JSON.stringify(request)).digest('hex');
      let row = await this.clientOperationVault.loadClientOperation(resource);
      if (row && row.fingerprint !== fingerprint) throw new ISCPRequestNotSentError('The original device operation must be reconciled');
      if (!row) {
        row = { schema_version: 1, operation: request.operation, operation_id: UUID.test(requested || '') ? requested : crypto.randomUUID(), fingerprint };
        await this.clientOperationVault.saveClientOperation(resource, row);
      }
      // A lost issuer response replays the original mutation itself. Its sealed
      // receipt stays with the issuer; no credential enters a plaintext journal.
      const response = await this.#request({ ...request, operation_id: row.operation_id }, init, rawBody);
      // The original UI keeps its opaque key until it has received the native
      // credential. Retain only its encrypted UUID/fingerprint mapping so a
      // lost IPC result, including after restart, replays the issuer receipt.
      if (!legacyIssue && (response.ok || response.status < 500 && response.status !== 429)) await this.clientOperationVault.clearClientOperation(resource);
      return response;
    } finally { this.presentationPending.delete(resource); }
  }

  async #presentation(request,init,rawBody) {
    const spec=requireOperation(request.operation);
    const family=request.operation.split(".")[1];
    const resource=request.operation.startsWith("mail.") ? mailMutationResource(request) : request.operation === "execution.approval" ? `approval:${request.params?.request_id}:${request.params?.approval_id}` : request.operation.startsWith("notifications.") ? "notifications" : family==='owner' ? 'owner' : family==='connectors' ? 'connectors' : family === 'bindings' ? `binding:${request.params?.binding_id || request.params?.channel || 'list'}` : family === 'browser' ? 'browser-extension' : family === 'mcp' ? `mcp:${request.operation.split('.')[2]}:${request.params?.ticket_id || request.params?.binding_id || ''}` : `integration:${request.params?.integration_id || ''}`;
    // Concurrent settings and popup snapshots are reads, not competing mutation intents.
    // Reading state must remain possible while an uncertain write is reconciled.
    if (!spec.mutation) return this.#applyPresentation(resource, spec, request, init, rawBody);
    if (this.presentationPending.has(resource)) throw new ISCPRequestNotSentError("A change to this resource is already in progress", "capacity");
    this.presentationPending.add(resource);
    try { return await this.#applyPresentation(resource, spec, request, init, rawBody); }
    finally { this.presentationPending.delete(resource); }
  }

  async #applyPresentation(resource, spec, request, init, rawBody) {
    if (!spec.mutation) return this.#presentationResponse(resource, await this.#request(request, init, rawBody));
    const journal = this.mutations;
    if (!journal) throw new ISCPRequestNotSentError("Durable settings recovery is unavailable");
    let record = journal.read(resource);
    const revision = this.resourceRevisions.get(resource);
    const confirmedSuccessor = record?.phase === 'applied' && !browserMutationResource(request) && (
      init.actionID && record.action_id && init.actionID !== record.action_id ||
      !(init.actionID && record.action_id) && revision && record.response?.body?.revision && revision !== record.response.body.revision);
    if (confirmedSuccessor) {
      journal.retire(resource, record.operation_id, { reason: 'new_foreground_action', ...(init.actionID ? { action_id: init.actionID } : {}), ...(revision ? { revision } : {}) });
      record = undefined;
    }
    if (record?.phase === 'applied' && (record.response?.body?.code === 'operation_not_sent' || receiptCapacityRejected(record.response.status, record.response.body))) {
      // Only a new foreground action can succeed a locally closed never-sent
      // intent. READY recovery never renews its deadline or creates a new ID.
      journal.retire(resource, record.operation_id, { reason: 'explicit_retry_after_non_submission' });
      record = undefined;
    }
    if (record && !(record.phase === 'applied' && !journal.matches(record, request))) {
      const response = await this.#reconcileMutation(journal, resource, record, init);
      if (!journal.matches(record, request)) {
        if (journal.read(resource)?.response) journal.complete(resource, record.operation_id);
        throw new Error("The previous change was reconciled. Review the current value before making another change");
      }
      if (journal.read(resource)?.response) journal.complete(resource, record.operation_id);
      else if (record.schema_version === 1) journal.complete(resource, record.operation_id);
      return this.#presentationResponse(resource, response);
    }
    record = journal.prepare(resource, request, { rawBody, revision, actionID: init.actionID,
      deadline: Date.now() + requestTimeout(request.operation, this.timeoutMS), authorizationRevision: this.capabilities.authorization_revision });
    const response = await this.#reconcileMutation(journal, resource, record, init);
    if (journal.read(resource)?.response) journal.complete(resource, record.operation_id);
    return this.#presentationResponse(resource, response);
  }

  async #reconcileMutation(journal, resource, record, init = {}) {
    const key = `${journal.scope}:${record.operation_id}`;
    if (this.mutationFlights.has(key)) return (await this.mutationFlights.get(key)).clone();
    const generation = this.generation, scopeKey = this.objectScopeKey;
    const current = () => {
      if (generation !== this.generation || scopeKey !== this.objectScopeKey || journal !== this.mutations || this.state !== 'transport_ready') throw new ISCPRequestNotSentError('ISCP authentication changed', 'unavailable');
    };
    const run = async () => {
      current();
      if (record.response) return mutationResponse(record.response);
      if (record.schema_version !== 2 || record.phase !== 'prepared') {
        // Transport evidence may explain an uncertain send, but never proves
        // that a business operation did not start. Always query its receipt.
        if (record.relay_submission) {
          const status = await this.relaySubmissionStatus(record.relay_submission).catch(() => undefined);
          current();
          if (status) journal.saveTransport(resource, record.operation_id, record.relay_submission, status.state);
        }
        const receipt = await this.invoke('operations.receipt', undefined, { params: { operation_id: record.operation_id }, signal: init.signal });
        current();
        if (receipt.operation_id !== record.operation_id || receipt.state !== 'completed' || !receipt.response || !Number.isInteger(receipt.response.status)) throw new Error('The previous change has an unknown outcome; it will not be repeated');
        if (record.schema_version === 2) journal.saveResult(resource, record.operation_id, receipt.response);
        return mutationResponse(receipt.response);
      }
      if (record.authorization_revision !== this.capabilities.authorization_revision) {
        journal.cancelPrepared(resource, record.operation_id, 'Original change authority changed before it was sent');
        return mutationResponse(journal.read(resource).response);
      }
      if (record.not_after <= Date.now()) {
        journal.cancelPrepared(resource, record.operation_id, 'Original change expired before it was sent');
        return mutationResponse(journal.read(resource).response);
      }
      let request = record.request, rawBody = record.raw_body;
      try {
        request = await prepareMailAttachments(this, request, init.signal);
        current();
        if (request !== record.request) rawBody = JSON.stringify(request.body);
        request = { ...request, operation_id: record.operation_id, ...(record.expected_revision ? { expected_revision: record.expected_revision } : {}) };
        const response = await this.#request(request, { ...init, [ORIGINAL_DEADLINE]: record.not_after,
          [BEFORE_SEND]: () => {
            if (record.authorization_revision !== this.capabilities?.authorization_revision) {
              journal.cancelPrepared(resource, record.operation_id, 'Original change authority changed before it was sent');
              throw new ISCPRequestNotSentError('ISCP authentication changed', 'canceled');
            }
            current(); journal.markAttempted(resource, record.operation_id);
          } }, rawBody);
        current();
        const body = await response.clone().json().catch(() => undefined);
        const submission = transportSubmission(body);
        if (submission) journal.saveTransport(resource, record.operation_id, submission);
        const unresolved = [body?.code, body?.error_code, response.headers.get('x-sparkclaw-error-code')].some(code =>
          ['operation_outcome_unknown', 'operation_receipt_unavailable'].includes(code));
        // Unclassified server failures cannot prove whether an effect committed.
        if (!unresolved && (response.status < 500 || receiptCapacityRejected(response.status, body))) journal.saveResult(resource, record.operation_id, { status: response.status, ...(body !== undefined ? { body } : {}) });
        return response;
      } catch (error) {
        if (error instanceof LocalMailAttachmentError) {
          current(); journal.cancelPrepared(resource, record.operation_id, error.message);
          return localAttachmentErrorResponse(error);
        }
        if (error instanceof ISCPRequestNotSentError && ['canceled', 'validation'].includes(error.reason)) journal.cancelPrepared(resource, record.operation_id);
        throw error;
      }
    };
    const flight = run(); this.mutationFlights.set(key, flight);
    try { return (await flight).clone(); }
    finally { if (this.mutationFlights.get(key) === flight) this.mutationFlights.delete(key); }
  }

  #wakeMutationRecovery() {
    if (this.state !== 'transport_ready' || !this.capabilities?.operations.includes('operations.receipt') || !this.mutations) return;
    const generation = this.generation, journal = this.mutations;
    if (this.recoveryScan?.generation === generation && this.recoveryScan.journal === journal) return;
    const scan = { generation, journal }; this.recoveryScan = scan;
    void (async () => {
      for (const record of journal.pending()) {
        if (generation !== this.generation || journal !== this.mutations || this.state !== 'transport_ready') break;
        if (this.presentationPending.has(record.resource_key)) continue;
        // Recovery never widens current capabilities or the original deadline.
        if (!this.capabilities.operations.includes(record.operation)) continue;
        await this.#reconcileMutation(journal, record.resource_key, record).catch(() => {});
      }
    })().catch(() => {}).finally(() => { if (this.recoveryScan === scan) this.recoveryScan = undefined; });
  }

  async #presentationResponse(resource,response) {
    if(!response.ok)return response;
    const result=await response.json();
    if(result && typeof result.revision==='string' && Object.hasOwn(result,'value')) {
      this.resourceRevisions.set(resource,result.revision);
      for(const [id,revision] of Object.entries(result.value?.resource_revisions||{}))this.resourceRevisions.set(`integration:${id}`,revision);
      return new Response(JSON.stringify(result.value),{status:response.status,headers:{'content-type':'application/json'}});
    }
    return new Response(JSON.stringify(result),{status:response.status,headers:{'content-type':'application/json'}});
  }

  async invoke(operation,body,options={}) {
    const spec=requireOperation(operation);
    const request={type:"task.invoke",profile:ISCP_V2_PROFILE,operation,...(body!==undefined?{body}:{}),
      ...(options.params?{params:options.params}:{}),...(options.operationID?{operation_id:options.operationID}:{}),
      ...(options.expectedRevision?{expected_revision:options.expectedRevision}:{}),
      ...((options.installationID||this.installationID)?{installation_id:options.installationID||this.installationID}:{}),...(options.requestID?{request_id:options.requestID}:{})};
    const browserResource = browserMutationResource(request);
    let response;
    if (browserResource) {
      if (this.presentationPending.has(browserResource)) throw new ISCPRequestNotSentError('A browser lifecycle operation is already in progress', 'capacity');
      this.presentationPending.add(browserResource);
      try {
        expireBrowserGrant(this.mutations, request);
        response = await this.#applyPresentation(browserResource, spec, request, options, body === undefined ? undefined : JSON.stringify(body));
      } finally { this.presentationPending.delete(browserResource); }
    } else {
      if(spec.mutation&&!request.operation_id)request.operation_id=crypto.randomUUID();
      response=await this.#request(request,options,body===undefined?undefined:JSON.stringify(body));
    }
    const result=await response.json();
    if(!response.ok){
      const bodyCode=typeof result.code==='string'&&/^[a-z][a-z0-9_]{0,79}$/u.test(result.code)?result.code:undefined;
      const error=new Error(result.error || (receiptCapacityRejected(response.status, result)
        ? 'Backend operation storage is full. Retry after the service is repaired.'
        : `ISCP operation failed (${bodyCode || result.error_code || response.status})`));
      const submission = transportSubmission(result);
      if (submission) error.relaySubmission = submission;
      // Domain adapters return their typed code in the authenticated JSON body.
      // Preserve envelope/header precedence except the closed legacy event
      // error: older Gateways wrapped cursor_gap with generic revision_conflict.
      error.code=result.error_code||response.headers.get("x-sparkclaw-error-code")||bodyCode;
      if(response.status===409&&response.headers.get("x-sparkclaw-error-code")==='revision_conflict'&&
        response.headers.get("x-sparkclaw-retryable")!=='true'&&Object.keys(result).length===2&&result.retryable===false&&
        (operation==='events.pull'&&bodyCode==='cursor_gap'||operation.startsWith('browser.host.')&&bodyCode==='browser_fenced'))error.code=bodyCode;
      settleBrowserLifecycle(this.mutations, request, undefined, error.code);
      error.status=response.status;error.retryable=result.retryable===true||response.headers.get("x-sparkclaw-retryable")==="true";throw error;
    }
    settleBrowserLifecycle(this.mutations, request, result);
    return result;
  }

  // Streams have no ordinary RPC timeout or concurrency allowance. The owner
  // bounds opening/recovery and releases observation with its AbortSignal.
  async streamCall(operation, body, options = {}) {
    const manifest = this.capabilities;
    if (this.state !== "transport_ready" || this.streamSchemaVersion !== 1 || manifest?.stream_schema_version !== 1 ||
        Date.parse(manifest.expires_at) <= Date.now() || !manifest.operations?.includes(operation) || operation !== "execution.progress.stream") {
      throw new ISCPRequestNotSentError("Matched execution stream schema is unavailable", "unavailable");
    }
    if (!UUID.test(options.requestID || "") || typeof options.onEvent !== "function" || options.signal?.aborted) {
      throw new ISCPRequestNotSentError("Execution stream identity or observer is invalid");
    }
    const id = crypto.randomUUID(), generation = this.generation;
    const request = { type: "task.invoke", profile: ISCP_V2_PROFILE, id, operation, body, request_id: options.requestID,
      installation_id: options.installationID || this.installationID, ...(options.params ? { params: options.params } : {}) };
    if (Buffer.byteLength(JSON.stringify(request)) > MAX_BYTES) throw new ISCPRequestNotSentError("Execution stream request exceeds bounds");
    return new Promise((resolve, reject) => {
      let completed = false, released = false, cleanupTimer, started;
      const current = () => generation === this.generation && this.state === "transport_ready" &&
        this.capabilities?.session_id === manifest.session_id && this.capabilities?.authorization_revision === manifest.authorization_revision &&
        Date.parse(this.capabilities?.expires_at) > Date.now() && performance.now() < this.capabilityValidUntil;
      const release = () => { released = true; clearTimeout(cleanupTimer); options.signal?.removeEventListener("abort", cancel); this.pending.delete(id); };
      const cancel = (error = new Error("Execution stream observation canceled")) => {
        if (released) return;
        // Cancel this observation only; the original execution and other calls
        // are neither canceled nor replayed, even after a terminal response.
        if (generation === this.generation && this.child) {
          try { this.child.stdin.write(`${JSON.stringify({ ipc_version: 1, type: "stream_cancel", id })}\n`); } catch { /* Local cleanup is sufficient. */ }
        }
        release(); if (!completed) reject(error instanceof Error ? error : new Error("Execution stream observation canceled"));
      };
      const fail = error => { release(); if (!completed) reject(error); };
      const call = { request, stream: true, capacityClass: "stream", reject: fail,
        event: (event, authorityValidForMS, authorityDeadlineMS) => {
          if (released || generation !== this.generation) return;
          if (!current()) { cancel(); return; }
          if (event?.subscription_id !== id) { cancel(new Error("Execution stream subscription differs")); return; }
          if (!Number.isSafeInteger(authorityValidForMS) || authorityValidForMS <= 0 || authorityValidForMS > 300000 ||
              !Number.isSafeInteger(authorityDeadlineMS) || authorityDeadlineMS < 0) {
            cancel(Object.assign(new Error("Execution stream authority deadline is invalid"), { code: "stream_authority_invalid" })); return;
          }
          // The main-process origin precedes the helper's stream_call receipt.
          // Subtracting its longer elapsed time is conservative and prevents
          // queued old events from turning their former remaining TTL into a
          // new lease. A refresh may move the helper-relative deadline beyond
          // five minutes; the remaining duration still cannot exceed five.
          const remaining = Math.floor(Math.min(authorityValidForMS, authorityDeadlineMS - (performance.now() - started)));
          if (remaining <= 0) {
            cancel(Object.assign(new Error("Execution stream authority expired in transit"), { status: 403, code: "grant_expired" })); return;
          }
          try { options.onAuthority?.(remaining); if (!released) options.onEvent(event); } catch (error) { cancel(error); }
        },
        resolve: response => {
          if (completed || released) return;
          if (!current()) { cancel(); return; }
          if (response.status !== 200 || response.body_object) {
            fail(Object.assign(new Error(response.error || "Execution stream failed"), { status: response.status, code: response.error_code })); return;
          }
          completed = true; resolve(response.body);
          // A terminal response can overtake its final text frames. Keep only
          // this subscription routable through the specified two-second gap.
          cleanupTimer = setTimeout(release, 2000); cleanupTimer.unref?.();
        } };
      this.pending.set(id, call);
      options.signal?.addEventListener("abort", cancel, { once: true });
      try { started = performance.now(); options.onOpen?.(id); this.child.stdin.write(`${JSON.stringify({ ipc_version: 1, type: "stream_call", id, request })}\n`); }
      catch { fail(new Error("Execution stream transport is unavailable")); }
    });
  }

  async #request(request,init,rawBody) {
    if(!this.capabilities || Date.parse(this.capabilities.expires_at)<=Date.now() || !this.capabilities.operations.includes(request.operation)) throw new ISCPRequestNotSentError("This capability is unavailable through ISCP");
    if(rawBody!==undefined && Buffer.byteLength(rawBody)>ISCP_BODY_BYTES) {
      if(!this.objects)throw new ISCPRequestNotSentError("Object transfer is unavailable");
      const object=await this.objects.upload(Buffer.from(rawBody),{purpose:['execution.submit','tools.invoke'].includes(request.operation)?'execution_request':'request_body',name:'request.json',media_type:'application/json'},init.signal);
      request={...request,body_object:object};delete request.body;rawBody=undefined;
    }
    const spec=requireOperation(request.operation);
    if(spec.mutation&&!request.operation_id)request.operation_id=crypto.randomUUID();
    const response=await this.#dispatch(request,init,rawBody);
    return response;
  }

  #queueAdmission(request, init, rawBody, deadline, capacityClass, mailRead) {
    // A full popup or bulk queue must not consume admission room for an
    // explicit action or a different control/transfer class.
    const queued = [...this.admissionQueue].filter(call => call.capacityClass === capacityClass && call.mailRead === mailRead).length;
    if (queued >= ADMISSION_QUEUE_LIMIT) throw new ISCPRequestNotSentError('ISCP request queue limit reached', 'capacity');
    const generation = this.generation;
    const session = this.capabilities?.session_id, revision = this.capabilities?.authorization_revision;
    return new Promise((resolve, reject) => {
      const release = () => { clearTimeout(timer); init.signal?.removeEventListener('abort', canceled); this.admissionQueue.delete(call); };
      const canceled = () => call.fail(new ISCPRequestNotSentError('ISCP request was canceled', 'canceled'));
      const call = { request, init, rawBody, deadline, capacityClass, mailRead, generation, session, revision, resolve, reject, release, fail: error => { release(); reject(error); } };
      const timer = setTimeout(() => call.fail(new ISCPRequestNotSentError('ISCP request admission deadline exceeded', 'capacity')), Math.max(0, deadline - Date.now()));
      traceLatency("rpc.queued", { operation: request.operation, request_id: request.request_id, count: this.admissionQueue.size });
      this.admissionQueue.add(call);
      init.signal?.addEventListener('abort', canceled, {once:true});
      if (init.signal?.aborted) canceled();
      else queueMicrotask(() => this.#drainAdmissions());
    });
  }

  #drainAdmissions() {
    // Wait only before the pipe write, never replay sent work. Explicit actions
    // take precedence over popup snapshots; independent classes
    // cannot block each other at the head of the queue.
    const calls = [...this.admissionQueue].sort((a, b) => Number(a.mailRead) - Number(b.mailRead));
    for (const call of calls) {
      if (call.generation !== this.generation || this.state !== 'transport_ready') { call.fail(new ISCPRequestNotSentError('ISCP transport is unavailable', 'unavailable')); continue; }
      if (call.session !== this.capabilities?.session_id || call.revision !== this.capabilities?.authorization_revision) { call.fail(new ISCPRequestNotSentError('ISCP authentication changed', 'canceled')); continue; }
      const pending = [...this.pending.values()];
      if (!this.#unboundedRPC(call.request) && (pending.filter(row => row.capacityClass === call.capacityClass).length >= CAPACITIES[call.capacityClass] || call.mailRead && pending.filter(row => row.mailRead).length >= MAIL_READ_LIMIT)) continue;
      call.release();
      this.#dispatch(call.request, call.init, call.rawBody, call.deadline).then(call.resolve, call.reject);
    }
  }

  #unboundedRPC(request) {
    if (request.profile === ISCP_V2_PROFILE) return this.capabilities?.limits?.concurrent === 0 && Date.parse(this.capabilities.expires_at) > Date.now();
    return this.admission?.profile === request.profile && this.admission.concurrent === 0 && Date.parse(this.admission.expires_at) > Date.now();
  }

  async #dispatch(request,init,rawBody,admissionDeadline) {
    if(this.state!=="transport_ready")throw new ISCPRequestNotSentError("ISCP transport is unavailable","unavailable");
    const spec=request.profile===ISCP_V2_PROFILE?requireOperation(request.operation):undefined;
    const capacityClass=spec?.capacity_class || 'v1';
    if(!Object.hasOwn(CAPACITIES,capacityClass))throw new ISCPRequestNotSentError("ISCP capacity class is unavailable");
    const mailRead = capacityClass === 'business' && request.operation.startsWith('mail.') && !spec.mutation;
    const timeoutMS = requestTimeout(request.operation, this.timeoutMS);
    admissionDeadline ??= init[ORIGINAL_DEADLINE] ?? Date.now() + timeoutMS;
    if (admissionDeadline <= Date.now()) throw new ISCPRequestNotSentError('ISCP request admission deadline exceeded', 'capacity');
    if (spec && (!this.capabilities || Date.parse(this.capabilities.expires_at) <= Date.now() || !this.capabilities.operations.includes(request.operation))) throw new ISCPRequestNotSentError('This capability is unavailable through ISCP');
    if (init.signal?.aborted) throw new ISCPRequestNotSentError("ISCP request was canceled", "canceled");
    const occupied=[...this.pending.values()].filter(call=>call.capacityClass===capacityClass).length;
    if (!this.#unboundedRPC(request) && (occupied >= CAPACITIES[capacityClass] || mailRead && [...this.pending.values()].filter(call => call.mailRead).length >= MAIL_READ_LIMIT)) return this.#queueAdmission(request, init, rawBody, admissionDeadline, capacityClass, mailRead);
    const id=crypto.randomUUID();request={...request,id};
    if (Buffer.byteLength(JSON.stringify(request)) > MAX_BYTES) throw new ISCPRequestNotSentError("ISCP request exceeds the test profile limit");
    const generation=this.generation;
    const traceStarted=latencyNow();
    const traceFields={rpc_id:id,request_id:request.request_id,operation:request.operation};
    traceLatency("rpc.dispatch",traceFields);
    const response=await new Promise((resolve,reject)=>{
      const signal=init.signal;
      const finish=(handler,value)=>{clearTimeout(timer);signal?.removeEventListener("abort",cancel);this.pending.delete(id);traceLatency("rpc.response",{...traceFields,duration_ms:latencyNow()-traceStarted,ok:handler===resolve,status:value?.status});handler(value);queueMicrotask(() => this.#drainAdmissions());};
      const cancel=()=>finish(reject,new Error("ISCP request was canceled"));
      const timer=setTimeout(()=>finish(reject,new Error("ISCP request deadline exceeded")),Math.max(1, admissionDeadline - Date.now()));
      this.pending.set(id,{request,capacityClass,mailRead,...(process.env.SPARKCLAW_PRIVATE_ISCP_READ_DIAGNOSTICS === '1' ? { diagnosticStartedMS: Date.now() } : {}),resolve:(value)=>finish(resolve,value),reject:(error)=>finish(reject,error)});
      signal?.addEventListener("abort",cancel,{once:true});
      // A failed durable fence is local to this call and must not kill the
      // shared helper. After the fence, any pipe failure remains unknown.
      try { init[BEFORE_SEND]?.(); } catch (error) { finish(reject, error); return; }
      try {const {body,...wireRequest}=request;this.child.stdin.write(`${JSON.stringify({ipc_version:1,type:"call",id,request:wireRequest,...(rawBody!==undefined?{body_base64:Buffer.from(rawBody,"utf8").toString("base64")}: {})})}\n`);}
      catch {this.#fail("disconnected");}
    });
    if(generation!==this.generation)throw new Error("ISCP authentication changed");
    if(response.body_object) {
      if(!this.objects)throw new Error("Object transfer is unavailable");
      const bytes=await this.objects.download(response.body_object,init.signal);
      return new Response(bytes,{status:response.status,headers:{"content-type":response.body_object.media_type||"application/json", "content-length": String(response.body_object.size), "x-sparkclaw-digest": response.body_object.sha256, ...(response.body_object.name ? { "x-sparkclaw-file-name": encodeURIComponent(response.body_object.name) } : {})}});
    }
    const payload=response.status>=400 && response.body===undefined ? {error:response.error||"ISCP operation failed",error_code:response.error_code||"operation_failed",retryable:response.retryable===true,...(response.retry_after_ms?{retry_after_ms:response.retry_after_ms}:{})} : response.body;
    const body=payload===undefined?null:JSON.stringify(payload);
    return new Response(body,{status:response.status,headers:{"content-type":"application/json",...(response.error_code?{"x-sparkclaw-error-code":response.error_code}:{}),...(response.retryable!==undefined?{"x-sparkclaw-retryable":String(response.retryable)}:{})}});
  }

  #read(chunk) {
    if (!Buffer.isBuffer(chunk)) chunk = Buffer.from(chunk);
    this.buffer = Buffer.concat([this.buffer, chunk]);
    while (true) {
      const newline = this.buffer.indexOf(10);
      if (newline === -1) { if (this.buffer.length > FRAME_BYTES) this.#fail("disconnected"); return; }
      if (newline > FRAME_BYTES) { this.#fail("disconnected"); return; }
      const line = this.buffer.subarray(0, newline); this.buffer = this.buffer.subarray(newline + 1);
      let frame;
      try { frame = JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(line)); }
      catch { this.#fail("disconnected"); return; }
      if (frame?.ipc_version !== 1) { this.#fail("disconnected"); return; }
      if (!this.hello) {
        if (frame.type !== "hello") { this.#fail("disconnected"); return; }
        if (!Array.isArray(frame.operations) || ISCP_OPERATIONS.some((operation) => !frame.operations.includes(operation)) ||
            frame.max_request_bytes !== MAX_BYTES || frame.max_response_bytes !== MAX_BYTES) { this.#fail("disconnected"); return; }
        if (this.expectedIdentity && (!frame.identity || Object.keys(this.expectedIdentity).some((key) => frame.identity[key] !== this.expectedIdentity[key]))) {
          this.#fail("identity_conflict"); return;
        }
        this.streamSchemaVersion = frame.stream_schema_version;
        this.hello = true; continue;
      }
      if (frame.type === 'admission') {
        if (![ISCP_PROFILE, ISCP_V2_PROFILE].includes(frame.profile) || ![0,4].includes(frame.concurrent) || typeof frame.session_id !== 'string' || !frame.session_id || frame.session_id.length > 160 || !Number.isSafeInteger(frame.authorization_revision) || frame.authorization_revision < (frame.profile === ISCP_PROFILE ? 0 : 1) || !Number.isFinite(Date.parse(frame.expires_at)) || Date.parse(frame.expires_at) <= Date.now()) { this.#fail('disconnected'); return; }
        this.admission = Object.freeze(frame);
        if (frame.profile === ISCP_PROFILE) { this.capabilities = undefined; this.onCapabilities(undefined); }
        queueMicrotask(() => this.#drainAdmissions());
      } else if (frame.type === "capabilities") {
        const value=frame.capabilities;
        if(value?.schema_version!==2||value.profile!==ISCP_V2_PROFILE||typeof value.session_id!=='string'||!value.session_id||value.session_id.length>160||!Array.isArray(value.operations)||value.operations.length>256||!value.operations.every((op)=>typeof op==='string')||!Number.isSafeInteger(value.authorization_revision)||value.authorization_revision<1||!Number.isFinite(Date.parse(value.expires_at))||Date.parse(value.expires_at)<=Date.now()||!value.binding) {this.#fail("disconnected");return;}
        if(this.expectedBinding && Object.keys(this.expectedBinding).some((key)=>value.binding[key]!==this.expectedBinding[key])){this.#fail("identity_conflict");return;}
        try {
        if(this.journalRoot){const scope={...value.binding,installation_id:this.installationID,authorization_revision:value.authorization_revision};const key=JSON.stringify(scope);if(this.objectScopeKey!==key){this.objects=new ISCPObjectClient({root:path.join(this.journalRoot,'objects'),scope,call:this.invoke.bind(this)});this.mutations=new ISCPMutationJournal(path.join(this.journalRoot,'mutations'),{...value.binding,installation_id:this.installationID},this.mutationProtector);this.objectScopeKey=key;}}
        } catch {this.#fail('disconnected');return;}
        const prior = this.capabilities;
        this.capabilityValidUntil = prior?.session_id === value.session_id && prior.authorization_revision === value.authorization_revision && Number.isFinite(this.capabilityValidUntil)
          ? this.capabilityValidUntil + Date.parse(value.expires_at) - Date.parse(prior.expires_at)
          : performance.now() + Math.max(0, Date.parse(value.expires_at) - Date.now());
        this.capabilities=Object.freeze(value);
        this.onCapabilities(value);
        queueMicrotask(() => this.#wakeMutationRecovery());
      } else if (frame.type === "state") {
        if (!["verifying_relay", "discovery_failed", "connecting", "relay_ready", "handshaking", "negotiating", "capability_negotiation_failed", "transport_ready", "disconnected", "authorization_expired", "authorization_revoked", "closed"].includes(frame.state)) { this.#fail("disconnected"); return; }
        if (frame.state === "discovery_failed") { this.#fail("disconnected"); return; }
        if (["disconnected", "authorization_expired", "authorization_revoked", "closed"].includes(frame.state)) { this.#fail(frame.state); return; }
        this.#state(frame.state);
        if (frame.state === "transport_ready") {
          clearTimeout(this.readyTimer); this.readyResolve?.(); this.readyResolve = this.readyReject = this.readyPromise = undefined;
          if (!this.recoveryTimer) { this.recoveryTimer = setInterval(() => this.#wakeMutationRecovery(), 5000); this.recoveryTimer.unref?.(); }
          queueMicrotask(() => this.#wakeMutationRecovery());
        }
      } else if (frame.type === "stream_event") {
        const call = this.pending.get(frame.id);
        if (!call) continue;
        if (!call.stream) { this.#fail("disconnected"); return; }
        call.event(frame.event, frame.authority_valid_for_ms, frame.authority_deadline_ms);
      } else if (frame.type === 'relay_submission_status') {
        const call = this.pending.get(frame.id);
        if (!call) continue;
        if (!call.originalSubmission || frame.message_id !== call.originalSubmission.message_id || typeof frame.found !== 'boolean') { this.#fail('disconnected'); return; }
        if (!frame.found) { call.resolve(undefined); continue; }
        const status = frame.submission;
        if (!status || Object.entries(call.originalSubmission).some(([key, value]) => status[key] !== value) || !['pending', 'unknown', 'accepted', 'absent', 'expired', 'rejected'].includes(status.state)) { this.#fail('disconnected'); return; }
        call.resolve(status);
      } else if (frame.type === "response") {
        const call = this.pending.get(frame.id);
        if (!call) continue; // Late canceled calls cannot cross the request fence.
        const response = frame.response;
        if (response?.type !== "task.result" || response.profile !== call.request.profile || response.id !== call.request.id ||
            !Number.isInteger(response.status) || response.status < 200 || response.status > 599 ||
            Buffer.byteLength(JSON.stringify(response)) > MAX_BYTES || (response.error !== undefined && typeof response.error !== "string")) { this.#fail("disconnected"); return; }
        const body = response.body === undefined ? null : JSON.stringify(response.body);
        if ([204, 205, 304].includes(response.status) && body !== null) { this.#fail("disconnected"); return; }
        call.resolve(response);
      } else { this.#fail("disconnected"); return; }
    }
  }

  #fail(state) { this.close(); this.#state(state); }
  #state(state) { this.state = state; this.onState(state); }
}

function receiptCapacityRejected(status, body) {
  // The authenticated Gateway returns this closed response before persisting
  // an intent or dispatching a mutation. Generic 5xx/missing receipts stay unknown.
  return status === 507 && body?.code === 'operation_receipt_capacity' && body.retryable === false &&
    Object.keys(body).sort().join() === 'code,retryable';
}

function transportSubmission(result) {
  const value = result?.relay_submission;
  if (result?.code !== 'transport_outcome_unknown' || !value || Object.keys(value).sort().join() !== 'envelope_digest,history_id,message_id,not_after,session_id') return;
  const id = /^[A-Za-z0-9][A-Za-z0-9_.:-]{0,159}$/u, digest = /^[A-Za-z0-9_-]{43}$/u;
  if (!id.test(value.message_id) || !id.test(value.session_id) || !digest.test(value.history_id) || !digest.test(value.envelope_digest) || !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$/u.test(value.not_after) || !Number.isFinite(Date.parse(value.not_after))) return;
  return { ...value };
}

export function mapISCPRequest(raw, init, origin, expanded = false) {
  const url = new URL(raw);
  if (url.origin !== origin || url.search || url.hash || url.username || url.password || /%/u.test(url.pathname)) throw new Error("ISCP backend path is invalid");
  const method = (init.method || "GET").toUpperCase();
  let operation, requestID;
  const fixed = { "GET /api/workbench/identity": "workbench.identity", "POST /api/v1/installations": "installation.bind", "GET /api/config": "presentation.config", "GET /api/owner": "presentation.owner", "GET /readyz": "presentation.ready", "POST /api/v1/executions": "execution.submit" };
  operation = fixed[`${method} ${url.pathname}`];
  if (!operation) {
    const match = /^\/api\/v1\/executions\/([^/]+)(?:\/(cancel|ack))?$/u.exec(url.pathname);
    if (match && UUID.test(match[1]) && ((method === "GET" && !match[2]) || (method === "POST" && match[2]))) { requestID = match[1]; operation = match[2] ? `execution.${match[2]}` : "execution.lookup"; }
  }
  if (!operation) throw new Error("This capability is unavailable through ISCP");
  const headers = new Headers(init.headers);
  const installationID = headers.get("x-sparkclaw-installation");
  const inputDigest = headers.get("x-sparkclaw-digest");
  if (installationID && !UUID.test(installationID)) throw new Error("ISCP installation identity is invalid");
  if (inputDigest && !/^[a-f0-9]{64}$/u.test(inputDigest)) throw new Error("ISCP input digest is invalid");
  let body;
  if (init.body !== undefined && init.body !== null) {
    const rawBody = typeof init.body === "string" ? init.body : new TextDecoder("utf-8", { fatal: true }).decode(init.body);
    if (!expanded && Buffer.byteLength(rawBody) > ISCP_BODY_BYTES) throw new Error("ISCP request exceeds the test profile limit");
    try { body = JSON.parse(rawBody); } catch { throw new Error("ISCP request body must be JSON"); }
  }
  if (method === "GET" && body !== undefined) throw new Error("ISCP request body is invalid");
  if (operation === "execution.submit") {
    if (!expanded && body?.input_files?.length) throw new Error("File inputs are unavailable through ISCP");
    requestID = body?.request_id;
    if (!UUID.test(requestID || "")) throw new Error("ISCP execution request identity is invalid");
  }
  return { type: "task.invoke", profile: ISCP_PROFILE, operation, ...(installationID ? { installation_id: installationID } : {}), ...(inputDigest ? { input_digest: inputDigest } : {}), ...(body !== undefined ? { body } : {}), ...(requestID ? { request_id: requestID } : {}) };
}
