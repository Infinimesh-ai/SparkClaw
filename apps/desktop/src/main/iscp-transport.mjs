import { spawn } from "node:child_process";
import crypto from "node:crypto";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { mapV2Route, requireOperation } from "./iscp-routes.mjs";
import { ISCPMutationJournal } from "./iscp-mutation-journal.mjs";
import { ISCPObjectClient } from "./iscp-object-client.mjs";
export const ISCP_V2_PROFILE = "sparkclaw.workbench.transport.v2";

export const ISCP_PROFILE = "sparkclaw.workbench.transport.v1";
export const ISCP_OPERATIONS = Object.freeze(["workbench.identity", "installation.bind", "presentation.config", "presentation.owner", "presentation.ready", "execution.submit", "execution.lookup", "execution.cancel", "execution.ack"]);
const MAX_BYTES = 65536;
const FRAME_BYTES = MAX_BYTES + 8192;
export const ISCP_BODY_BYTES = MAX_BYTES - 2048;
const UUID = /^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/u;
const MODULE_DIR = path.dirname(fileURLToPath(import.meta.url));

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
  constructor({ configPath, origin, expectedIdentity, expectedBinding, installationID, packaged = false, resourcesPath, spawnProcess = spawn, timeoutMS = 30000, onState = () => {}, onCapabilities = () => {}, journalRoot }) {
    Object.assign(this, { configPath, origin, expectedIdentity, expectedBinding, installationID, packaged, resourcesPath, spawnProcess, timeoutMS, onState, onCapabilities, journalRoot });
    this.resourceRevisions = new Map();
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

  close() {
    ++this.generation;
    clearTimeout(this.readyTimer);
    this.readyReject?.(new Error("ISCP transport is unavailable"));
    this.readyResolve = this.readyReject = this.readyPromise = undefined;
    for (const call of this.pending.values()) call.reject(new Error("ISCP transport is unavailable"));
    this.pending.clear();
    this.capabilities = undefined; this.onCapabilities(undefined);
    const child = this.child; this.child = undefined;
    child?.stdin.destroy(); child?.kill();
    if (child) { const timer = setTimeout(() => { if (child.exitCode === null) child.kill("SIGKILL"); }, 1000); timer.unref?.(); }
    this.buffer = Buffer.alloc(0); this.hello = false;
    this.#state("closed");
  }

  async fetch(raw, init = {}) {
    if (this.state !== "transport_ready") throw new ISCPRequestNotSentError("ISCP transport is unavailable", "unavailable");
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
    try { mapped=mapV2Route(url,method,body); }
    catch(error) {
      // v1 base operations remain valid inside the v2 envelope, including the
      // original execution request whose byte digest must survive unchanged.
      try { mapped=mapISCPRequest(raw,init,this.origin,true); }
      catch {throw new ISCPRequestNotSentError(error.message);}
    }
    const headers=new Headers(init.headers);
    const request={...mapped,...(this.installationID?{installation_id:this.installationID}:{}),type:"task.invoke",profile:ISCP_V2_PROFILE,...(mapped.params?.request_id?{request_id:mapped.params.request_id}:{}),
      ...(headers.get("x-sparkclaw-installation")?{installation_id:headers.get("x-sparkclaw-installation")}:{}),
      ...(headers.get("x-sparkclaw-digest")?{input_digest:headers.get("x-sparkclaw-digest")}:{}),
      ...(init.operationID?{operation_id:init.operationID}:{}),...(init.expectedRevision?{expected_revision:init.expectedRevision}:{})};
    const rawBody=init.body === undefined ? undefined : typeof init.body === "string" ? init.body : new TextDecoder("utf-8",{fatal:true}).decode(init.body);
    if(request.operation.startsWith("settings.") || request.operation.startsWith("notifications.") || request.operation === "execution.approval") return this.#presentation(request,init,rawBody);
    return this.#request(request,init,rawBody);
  }

  async #presentation(request,init,rawBody) {
    const spec=requireOperation(request.operation);
    const family=request.operation.split(".")[1];
    const resource=request.operation === "execution.approval" ? `approval:${request.params?.request_id}:${request.params?.approval_id}` : request.operation.startsWith("notifications.") ? "notifications" : family==='owner' ? 'owner' : family==='connectors' ? 'connectors' : `integration:${request.params?.integration_id || ''}`;
    let record;
    if(spec.mutation) {
      if(!this.mutations)throw new ISCPRequestNotSentError("Durable settings recovery is unavailable");
      const prior=this.mutations.read(resource);
      if(prior) {
        const receipt=await this.invoke('operations.receipt',undefined,{params:{operation_id:prior.operation_id},signal:init.signal});
        if(receipt.state!=='completed'||!receipt.response)throw new Error("The previous change has an unknown outcome; it will not be repeated");
        this.mutations.complete(resource,prior.operation_id);
        if(!this.mutations.matches(prior,request))throw new Error("The previous change was reconciled. Review the current value before making another change");
        return this.#presentationResponse(resource,new Response(JSON.stringify(receipt.response.body),{status:receipt.response.status}));
      }
      record=this.mutations.begin(resource,request,this.resourceRevisions.get(resource));
      request={...request,operation_id:record.operation_id,...(record.expected_revision?{expected_revision:record.expected_revision}:{})};
    }
    let response;
    try {response=await this.#request(request,init,rawBody);}
    catch(error){if(record && error instanceof ISCPRequestNotSentError)this.mutations.complete(resource,record.operation_id);throw error;}
    if(record)this.mutations.complete(resource,record.operation_id);
    return this.#presentationResponse(resource,response);
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
    if(spec.mutation&&!request.operation_id)request.operation_id=crypto.randomUUID();
    const response=await this.#request(request,options,body===undefined?undefined:JSON.stringify(body));
    const result=await response.json();
    if(!response.ok){const error=new Error(result.error||"ISCP operation failed");error.code=result.error_code||response.headers.get("x-sparkclaw-error-code");error.status=response.status;error.retryable=result.retryable===true||response.headers.get("x-sparkclaw-retryable")==="true";throw error;}
    return result;
  }

  async #request(request,init,rawBody) {
    if(!this.capabilities || Date.parse(this.capabilities.expires_at)<=Date.now() || !this.capabilities.operations.includes(request.operation)) throw new ISCPRequestNotSentError("This capability is unavailable through ISCP");
    if(rawBody!==undefined && Buffer.byteLength(rawBody)>ISCP_BODY_BYTES) {
      if(!this.objects)throw new ISCPRequestNotSentError("Object transfer is unavailable");
      const object=await this.objects.upload(Buffer.from(rawBody),{purpose:request.operation==='execution.submit'?'execution_request':'request_body',name:'request.json',media_type:'application/json'},init.signal);
      request={...request,body_object:object};delete request.body;rawBody=undefined;
    }
    const spec=requireOperation(request.operation);
    if(spec.mutation&&!request.operation_id)request.operation_id=crypto.randomUUID();
    const response=await this.#dispatch(request,init,rawBody);
    return response;
  }

  async #dispatch(request,init,rawBody) {
    if(this.state!=="transport_ready")throw new ISCPRequestNotSentError("ISCP transport is unavailable","unavailable");
    const capacityClass=request.profile===ISCP_V2_PROFILE?requireOperation(request.operation).capacity_class:'v1';
    const capacities={v1:4,business:4,control:1,bulk:2,events:1,audio:2};
    if(!Object.hasOwn(capacities,capacityClass))throw new ISCPRequestNotSentError("ISCP capacity class is unavailable");
    const occupied=[...this.pending.values()].filter(call=>call.capacityClass===capacityClass).length;
    if(occupied>=capacities[capacityClass])throw new ISCPRequestNotSentError("ISCP request concurrency limit reached","capacity");
    if (init.signal?.aborted) throw new ISCPRequestNotSentError("ISCP request was canceled", "canceled");
    const id=crypto.randomUUID();request={...request,id};
    if (Buffer.byteLength(JSON.stringify(request)) > MAX_BYTES) throw new ISCPRequestNotSentError("ISCP request exceeds the test profile limit");
    const generation=this.generation;
    const response=await new Promise((resolve,reject)=>{
      const signal=init.signal;
      const finish=(handler,value)=>{clearTimeout(timer);signal?.removeEventListener("abort",cancel);this.pending.delete(id);handler(value);};
      const cancel=()=>finish(reject,new Error("ISCP request was canceled"));
      const timer=setTimeout(()=>finish(reject,new Error("ISCP request deadline exceeded")),this.timeoutMS);
      this.pending.set(id,{request,capacityClass,resolve:(value)=>finish(resolve,value),reject:(error)=>finish(reject,error)});
      signal?.addEventListener("abort",cancel,{once:true});
      try {const {body,...wireRequest}=request;this.child.stdin.write(`${JSON.stringify({ipc_version:1,type:"call",id,request:wireRequest,...(rawBody!==undefined?{body_base64:Buffer.from(rawBody,"utf8").toString("base64")}: {})})}\n`);}
      catch {this.#fail("disconnected");}
    });
    if(generation!==this.generation)throw new Error("ISCP authentication changed");
    if(response.body_object) {
      if(!this.objects)throw new Error("Object transfer is unavailable");
      const bytes=await this.objects.download(response.body_object,init.signal);
      return new Response(bytes,{status:response.status,headers:{"content-type":response.body_object.media_type||"application/json"}});
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
        this.hello = true; continue;
      }
      if (frame.type === "capabilities") {
        const value=frame.capabilities;
        if(value?.schema_version!==2||value.profile!==ISCP_V2_PROFILE||typeof value.session_id!=='string'||!value.session_id||value.session_id.length>160||!Array.isArray(value.operations)||value.operations.length>256||!value.operations.every((op)=>typeof op==='string')||!Number.isSafeInteger(value.authorization_revision)||value.authorization_revision<1||!Number.isFinite(Date.parse(value.expires_at))||Date.parse(value.expires_at)<=Date.now()||!value.binding) {this.#fail("disconnected");return;}
        if(this.expectedBinding && Object.keys(this.expectedBinding).some((key)=>value.binding[key]!==this.expectedBinding[key])){this.#fail("identity_conflict");return;}
        try {
        if(this.journalRoot){const scope={...value.binding,installation_id:this.installationID,authorization_revision:value.authorization_revision};const key=JSON.stringify(scope);if(this.objectScopeKey!==key){this.objects=new ISCPObjectClient({root:path.join(this.journalRoot,'objects'),scope,call:this.invoke.bind(this)});this.mutations=new ISCPMutationJournal(path.join(this.journalRoot,'mutations'),scope);this.objectScopeKey=key;}}
        } catch {this.#fail('disconnected');return;}
        this.capabilities=Object.freeze(value);
        this.onCapabilities(value);
      } else if (frame.type === "state") {
        if (!["verifying_relay", "discovery_failed", "connecting", "relay_ready", "handshaking", "negotiating", "capability_negotiation_failed", "transport_ready", "disconnected", "authorization_expired", "authorization_revoked", "closed"].includes(frame.state)) { this.#fail("disconnected"); return; }
        if (frame.state === "discovery_failed") { this.#fail("disconnected"); return; }
        if (["disconnected", "authorization_expired", "authorization_revoked", "closed"].includes(frame.state)) { this.#fail(frame.state); return; }
        this.#state(frame.state);
        if (frame.state === "transport_ready") { clearTimeout(this.readyTimer); this.readyResolve?.(); this.readyResolve = this.readyReject = this.readyPromise = undefined; }
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
