import { spawn } from "node:child_process";
import crypto from "node:crypto";
import path from "node:path";
import { fileURLToPath } from "node:url";

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
  constructor({ configPath, origin, expectedIdentity, packaged = false, resourcesPath, spawnProcess = spawn, timeoutMS = 30000, onState = () => {} }) {
    Object.assign(this, { configPath, origin, expectedIdentity, packaged, resourcesPath, spawnProcess, timeoutMS, onState });
    this.pending = new Map(); this.generation = 0; this.state = "closed";
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
    const child = this.child; this.child = undefined;
    child?.stdin.destroy(); child?.kill();
    if (child) { const timer = setTimeout(() => { if (child.exitCode === null) child.kill("SIGKILL"); }, 1000); timer.unref?.(); }
    this.buffer = Buffer.alloc(0); this.hello = false;
    this.#state("closed");
  }

  async fetch(raw, init = {}) {
    if (this.state !== "transport_ready") throw new ISCPRequestNotSentError("ISCP transport is unavailable", "unavailable");
    let request;
    try { request = mapISCPRequest(raw, init, this.origin); }
    catch (error) { throw new ISCPRequestNotSentError(error.message); }
    if (this.pending.size >= 4) throw new ISCPRequestNotSentError("ISCP request concurrency limit reached", "capacity");
    if (init.signal?.aborted) throw new ISCPRequestNotSentError("ISCP request was canceled", "canceled");
    const id = crypto.randomUUID(); request.id = id;
    if (Buffer.byteLength(JSON.stringify(request)) > MAX_BYTES) throw new ISCPRequestNotSentError("ISCP request exceeds the test profile limit");
    return new Promise((resolve, reject) => {
      const signal = init.signal;
      const finish = (handler, value) => {
        clearTimeout(timer); signal?.removeEventListener("abort", cancel); this.pending.delete(id); handler(value);
      };
      const cancel = () => finish(reject, new Error("ISCP request was canceled"));
      const timer = setTimeout(() => finish(reject, new Error("ISCP request deadline exceeded")), this.timeoutMS);
      this.pending.set(id, { request, resolve: (value) => finish(resolve, value), reject: (error) => finish(reject, error) });
      signal?.addEventListener("abort", cancel, { once: true });
      try {
        const { body, ...wireRequest } = request;
        const rawBody = init.body === undefined || init.body === null ? undefined : (typeof init.body === "string" ? init.body : new TextDecoder("utf-8", { fatal: true }).decode(init.body));
        this.child.stdin.write(`${JSON.stringify({ ipc_version: 1, type: "call", id, request: wireRequest, ...(rawBody !== undefined ? { body_base64: Buffer.from(rawBody, "utf8").toString("base64") } : {}) })}\n`);
      }
      catch { this.#fail("disconnected"); }
    });
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
      if (frame.type === "state") {
        if (!["verifying_relay", "discovery_failed", "connecting", "relay_ready", "handshaking", "transport_ready", "disconnected", "authorization_expired", "authorization_revoked", "closed"].includes(frame.state)) { this.#fail("disconnected"); return; }
        if (frame.state === "discovery_failed") { this.#fail("disconnected"); return; }
        if (["disconnected", "authorization_expired", "authorization_revoked", "closed"].includes(frame.state)) { this.#fail(frame.state); return; }
        this.#state(frame.state);
        if (frame.state === "transport_ready") { clearTimeout(this.readyTimer); this.readyResolve?.(); this.readyResolve = this.readyReject = this.readyPromise = undefined; }
      } else if (frame.type === "response") {
        const call = this.pending.get(frame.id);
        if (!call) continue; // Late canceled calls cannot cross the request fence.
        const response = frame.response;
        if (response?.type !== "task.result" || response.profile !== ISCP_PROFILE || response.id !== call.request.id ||
            !Number.isInteger(response.status) || response.status < 200 || response.status > 599 ||
            Buffer.byteLength(JSON.stringify(response)) > MAX_BYTES || (response.error !== undefined && typeof response.error !== "string")) { this.#fail("disconnected"); return; }
        const body = response.body === undefined ? null : JSON.stringify(response.body);
        if ([204, 205, 304].includes(response.status) && body !== null) { this.#fail("disconnected"); return; }
        call.resolve(new Response(body, { status: response.status, headers: { "content-type": "application/json" } }));
      } else { this.#fail("disconnected"); return; }
    }
  }

  #fail(state) { this.close(); this.#state(state); }
  #state(state) { this.state = state; this.onState(state); }
}

export function mapISCPRequest(raw, init, origin) {
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
    if (Buffer.byteLength(rawBody) > ISCP_BODY_BYTES) throw new Error("ISCP request exceeds the test profile limit");
    try { body = JSON.parse(rawBody); } catch { throw new Error("ISCP request body must be JSON"); }
  }
  if (method === "GET" && body !== undefined) throw new Error("ISCP request body is invalid");
  if (operation === "execution.submit") {
    if (body?.input_files?.length) throw new Error("File inputs are unavailable through ISCP");
    requestID = body?.request_id;
    if (!UUID.test(requestID || "")) throw new Error("ISCP execution request identity is invalid");
  }
  return { type: "task.invoke", profile: ISCP_PROFILE, operation, ...(installationID ? { installation_id: installationID } : {}), ...(inputDigest ? { input_digest: inputDigest } : {}), ...(body !== undefined ? { body } : {}), ...(requestID ? { request_id: requestID } : {}) };
}
