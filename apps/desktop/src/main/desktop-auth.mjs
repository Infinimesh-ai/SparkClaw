import fs from "node:fs/promises";
import path from "node:path";
import crypto from "node:crypto";
import { parseBackendDescriptor, loadLocalBackendDescriptor, loadLocalBackendConnection } from "./local-backend.mjs";
import { isTLSIdentityError, pinnedHTTPSFetch } from "./pinned-https.mjs";
import { createConnectionCredential, parseConnectionCredential } from "./connection-credential.mjs";
import { loadISCPProfile } from "./iscp-profile.mjs";
import { ISCPTransport, ISCP_OPERATIONS, ISCP_BODY_BYTES, ISCPRequestNotSentError, mapISCPRequest } from "./iscp-transport.mjs";

export class DesktopAuth {
  constructor({ vault, descriptorPath, qualificationPaths, installationID, qualification = false, requireLAN = false, fetcher, iscpProfilePath, allowLocalISCPTest = false, packaged = false, resourcesPath, transportFactory = (options) => new ISCPTransport(options), onChange = () => {}, onLock = () => {} }) {
    Object.assign(this, { vault, descriptorPath, qualificationPaths, installationID, qualification, requireLAN, fetcher, iscpProfilePath, allowLocalISCPTest, packaged, resourcesPath, transportFactory, onChange, onLock });
    this.requests = new Set();
    this.generation = 0;
    this.vaultOperations = Promise.resolve();
    this.status = Object.freeze({ schema_version: 1, state: "incomplete_setup" });
  }

  async initialize() {
    if (this.iscpProfilePath) return this.#initializeISCP();
    const qualificationPaths = this.qualification === true ? this.qualificationPaths : undefined;
    try { this.descriptor = await loadLocalBackendDescriptor(qualificationPaths || { descriptorPath: this.descriptorPath }); }
    catch { return this.#set("incomplete_setup"); }
    if ((this.requireLAN && this.descriptor.schemaVersion !== 2) || this.descriptor.transport === "iscp") {
      this.descriptor = undefined;
      return this.#set("incomplete_setup");
    }
    if (qualificationPaths) {
      try { this.connection = await loadLocalBackendConnection(qualificationPaths); return this.retry(); } catch { /* new-install gate */ }
    }
    if (!this.vault.available()) return this.#set("secure_storage_unavailable");
    try {
      const saved = await this.vault.load();
      if (saved && saved.binding === descriptorBinding(this.descriptor)) this.connection = Object.freeze({ ...saved, origin: this.descriptor.origin });
      else if (saved) await this.vault.clear();
    } catch {
      // A temporary OS key-store failure must lock access without destroying
      // the encrypted credential needed by a later launch. Explicit login,
      // logout and a confirmed binding mismatch still clear the old record.
      this.connection = undefined;
      return this.#set("locked");
    }
    return this.connection ? this.retry() : this.#set("locked");
  }

  async configure(value) {
    const descriptor = parseBackendDescriptor(value);
    if (descriptor.transport === "iscp") throw new Error("ISCP test profiles require the isolated launcher");
    if (this.requireLAN && descriptor.schemaVersion !== 2) {
      throw new Error("This desktop requires a version 2 HTTPS LAN backend description");
    }
    const generation = this.generation + 1;
    await this.logout();
    if (generation !== this.generation) return this.status;
    await this.#vault(async () => {
      if (generation !== this.generation) return;
      await fs.mkdir(path.dirname(this.descriptorPath), { recursive: true, mode: 0o700 });
      const temporary = `${this.descriptorPath}.${crypto.randomUUID()}.tmp`;
      try {
        await fs.writeFile(temporary, JSON.stringify(value), { mode: 0o600, flag: "wx" });
        await fs.rename(temporary, this.descriptorPath);
      } finally { await fs.rm(temporary, { force: true }); }
    });
    if (generation !== this.generation) return this.status;
    this.descriptor = descriptor;
    return this.#set(this.vault.available() ? "locked" : "secure_storage_unavailable");
  }

  async login(token) {
    if (!this.descriptor) return this.#set("incomplete_setup");
    if (this.descriptor.transport === "iscp") throw new Error("ISCP authentication is owned by the private helper profile");
    if (!this.vault.available()) return this.#set("secure_storage_unavailable");
    const generation = ++this.generation;
    this.connection = undefined;
    for (const request of this.requests) request.abort();
    this.requests.clear();
    this.#set("reconnecting");
    await this.#vault(() => this.vault.clear());
    await this.onLock();
    if (generation !== this.generation) return this.status;
    if (typeof token !== "string" || token !== token.trim() || token.length < 32 || token.length > 512 || /[\s\x00-\x1f\x7f]/u.test(token)) {
      return this.#set("invalid_authentication");
    }
    const candidate = { origin: this.descriptor.origin, deploymentID: this.descriptor.deploymentID, ownerID: this.descriptor.ownerID, authorization: `Bearer ${token}` };
    const result = await this.#identity(candidate);
    if (generation !== this.generation) return this.status;
    if (!result.identity) return this.#set(result.state);
    const record = { ...candidate, ...result.identity, binding: descriptorBinding(this.descriptor) };
    try { await this.#vault(() => generation === this.generation ? this.vault.save(record) : undefined); }
    catch { return generation === this.generation ? this.#set("secure_storage_unavailable") : this.status; }
    if (generation !== this.generation) return this.status;
    this.connection = Object.freeze(record);
    return this.#set("connected");
  }

  async enroll(value) {
    const parsed = parseConnectionCredential(value);
    await this.configure(parsed.descriptor);
    return this.login(parsed.token);
  }

  connectionCredential(token) {
    if (!this.descriptor) throw new Error("Backend identity is unavailable");
    if (this.descriptor.transport === "iscp") throw new Error("ISCP uses a private helper profile");
    return createConnectionCredential({
      schema_version: this.descriptor.schemaVersion,
      origin: this.descriptor.origin,
      deployment_id: this.descriptor.deploymentID,
      ...(this.descriptor.ownerID ? { owner_id: this.descriptor.ownerID } : {}),
      ...(this.descriptor.certificateSHA256 ? { tls_certificate_sha256: this.descriptor.certificateSHA256 } : {}),
      ...(this.descriptor.ca ? { tls_ca_pem: this.descriptor.ca } : {}),
    }, token);
  }

  async retry() {
    if (!this.connection) return this.#set(this.descriptor ? "locked" : "incomplete_setup");
    this.suspended = false;
    clearTimeout(this.reconnectTimer); this.reconnectTimer = undefined;
    const generation = this.generation;
    this.#set("reconnecting");
    const result = await this.#identity(this.connection);
    if (generation !== this.generation) return this.status;
    if (this.descriptor.transport === "iscp") {
      if (result.state === "connected") {
        this.reconnectAttempt = 0;
        if (!this.connection.identityVerified) {
          const verified = Object.freeze({ ...this.connection, identityVerified: true });
          try { await this.#vault(() => generation === this.generation ? this.vault.save(verified) : undefined); }
          catch { return generation === this.generation ? this.#set("secure_storage_unavailable") : this.status; }
          if (generation !== this.generation) return this.status;
          this.connection = verified;
        }
      }
      else if (result.state === "service_unavailable") this.#scheduleReconnect();
      else this.transport?.close();
      return this.#set(result.state);
    }
    if (result.state === "invalid_authentication" || result.state === "identity_conflict") return this.logout(result.state);
    return this.#set(result.state);
  }

  async logout(state = "locked") {
    const generation = ++this.generation;
    this.connection = undefined;
    clearTimeout(this.reconnectTimer); this.reconnectTimer = undefined;
    this.transport?.close(); this.transport = undefined;
    for (const request of this.requests) request.abort();
    this.requests.clear();
    await this.onLock();
    await this.#vault(() => this.vault.clear());
    return generation === this.generation ? this.#set(this.descriptor ? state : "incomplete_setup") : this.status;
  }

  validateExecutionRequest(body) {
    if (this.descriptor?.transport !== "iscp") return;
    const request = mapISCPRequest(`${this.descriptor.origin}/api/v1/executions`, { method: "POST", body }, this.descriptor.origin);
    const overhead = Buffer.byteLength(JSON.stringify({ ...request, id: crypto.randomUUID() })) - Buffer.byteLength(JSON.stringify(request.body));
    if (Buffer.byteLength(body) > ISCP_BODY_BYTES || Buffer.byteLength(body) + overhead > 65536) throw new Error("ISCP request exceeds the 64 KiB test profile limit; your draft is retained");
  }

  async authorizedFetch(raw, init = {}) {
    return this.#authorizedFetch(raw, init, 1 << 20);
  }

  // Only trusted main-process workbench clients call this; the renderer proxy keeps
  // the ordinary 1 MiB ceiling. Origin, pinned TLS and logout fencing are shared.
  async authorizedExecutionFetch(raw, init = {}) {
    const url = new URL(raw);
    if (!/^\/api\/v1\/(executions|inputs)(\/|$)/u.test(url.pathname) || url.search || url.hash || url.username || url.password) {
      throw new Error("workbench backend path is invalid");
    }
    return this.#authorizedFetch(raw, init, 8 * 1024 * 1024);
  }

  async authorizedMailFileFetch(raw, init = {}) {
    const url = new URL(raw);
    if (!/^\/api\/v1\/mail\/[^/]+\/messages\/[^/]+\/attachments\/[^/]+$/u.test(url.pathname) || url.search || url.hash || url.username || url.password || (init.method && init.method !== "GET")) throw new Error("Mail attachment path is invalid");
    return this.#authorizedFetch(raw, init, 64 * 1024 * 1024);
  }

  async #authorizedFetch(raw, init, byteLimit) {
    if (this.status.state !== "connected" || !this.connection) return new Response(null, { status: 401 });
    const url = new URL(raw);
    if (url.origin !== this.descriptor.origin) throw new Error("Backend origin is invalid");
    if (this.descriptor.transport === "iscp") mapISCPRequest(raw, init, this.descriptor.origin);
    const generation = this.generation;
    const controller = new AbortController();
    this.requests.add(controller);
    const headers = new Headers(init.headers);
    if (this.descriptor.transport !== "iscp") headers.set("authorization", this.connection.authorization);
    try {
      const response = await this.#fetch()(raw, { ...init, headers, redirect: "manual", signal: AbortSignal.any([controller.signal, AbortSignal.timeout(30000), ...(init.signal ? [init.signal] : [])]) });
      if (generation !== this.generation) { controller.abort(); return new Response(null, { status: 401 }); }
      if (response.status === 401 || (this.descriptor.transport === "iscp" && response.status === 403)) {
        if (this.descriptor.transport === "iscp") {
          ++this.generation; clearTimeout(this.reconnectTimer); this.reconnectTimer = undefined;
          for (const request of this.requests) request.abort(); this.requests.clear();
          this.transport.close(); this.#set("invalid_authentication");
        }
        else await this.logout("invalid_authentication");
        return new Response(null, { status: 401 });
      }
      // Streaming requests remain cancellable until their body is consumed.
      if (!response.body) { this.requests.delete(controller); return response; }
      const reader = response.body.getReader();
      const requests = this.requests;
      let bytes = 0;
      const body = new ReadableStream({
        async pull(stream) {
          try {
            const chunk = await reader.read();
            if (chunk.done) { requests.delete(controller); stream.close(); }
            else {
              bytes += chunk.value.length;
              if (bytes > byteLimit) { controller.abort(); requests.delete(controller); await reader.cancel(); stream.error(new Error("Backend response exceeds the allowed size")); }
              else stream.enqueue(chunk.value);
            }
          } catch (error) { requests.delete(controller); stream.error(error); }
        },
        async cancel() { requests.delete(controller); controller.abort(); await reader.cancel().catch(() => {}); },
      });
      return new Response(body, { status: response.status, statusText: response.statusText, headers: response.headers });
    } catch (error) {
      this.requests.delete(controller);
      // Capacity and local validation do not invalidate a healthy session. Keep
      // the pre-write proof so the execution client can retain unsent intent.
      if (error instanceof ISCPRequestNotSentError && error.reason !== "unavailable") throw error;
      if (generation === this.generation) {
        if (isTLSIdentityError(error)) await this.logout("identity_conflict");
        else { this.#set("service_unavailable"); this.#scheduleReconnect(); }
      }
      if (error instanceof ISCPRequestNotSentError) throw error;
      throw new Error("Backend request is unavailable");
    }
  }

  #fetch() {
    if (this.descriptor.transport === "iscp") return this.transport.fetch.bind(this.transport);
    return this.descriptor.schemaVersion === 2 ? pinnedHTTPSFetch(this.descriptor) : this.fetcher;
  }

  suspend() {
    this.suspended = true; ++this.generation; clearTimeout(this.reconnectTimer); this.reconnectTimer = undefined;
    for (const request of this.requests) request.abort();
    this.requests.clear(); this.transport?.close();
    if (this.descriptor?.transport === "iscp" && this.connection) this.#set("service_unavailable");
  }

  close() { this.suspend(); this.transport = undefined; }

  async #initializeISCP() {
    if (!this.allowLocalISCPTest || this.qualification) return this.#set("incomplete_setup");
    let profile;
    try { profile = await loadISCPProfile(this.iscpProfilePath); } catch { return this.#set("incomplete_setup"); }
    this.descriptor = profile.descriptor;
    if (!this.vault.available()) return this.#set("secure_storage_unavailable");
    try {
      const binding = descriptorBinding(this.descriptor);
      const saved = await this.vault.load();
      const record = { schema_version: 2, transport: "iscp", testMode: true, configPath: profile.configPath,
        origin: this.descriptor.origin, deploymentID: this.descriptor.deploymentID, ownerID: this.descriptor.ownerID,
        clientID: this.descriptor.clientID, binding,
        identityVerified: saved?.transport === "iscp" && saved.binding === binding && saved.configPath === profile.configPath && saved.identityVerified === true };
      if (saved && (saved.binding !== binding || saved.transport !== "iscp" || saved.configPath !== profile.configPath)) await this.vault.clear();
      if (!saved || saved.binding !== binding || saved.configPath !== profile.configPath || saved.transport !== "iscp") await this.vault.save(record);
      this.connection = Object.freeze(record);
      this.transport = this.transportFactory({ configPath: profile.helperConfigPath, origin: this.descriptor.origin,
        expectedIdentity: { domain_id: this.descriptor.domainID, initiator_device_id: this.descriptor.initiatorDeviceID,
          responder_device_id: this.descriptor.responderDeviceID, responder_key_thumbprint: this.descriptor.responderKeyThumbprint, relay_url: this.descriptor.relayURL, relay_profile: this.descriptor.relayProfile },
        packaged: this.packaged, resourcesPath: this.resourcesPath,
        onState: (state) => {
          this.transportStage = state;
          if (["verifying_relay", "connecting", "relay_ready", "handshaking"].includes(state) && this.connection && !this.suspended) {
            if (this.status.state === "connected") {
              ++this.generation; this.needsIdentity = true;
              for (const request of this.requests) request.abort(); this.requests.clear();
            }
            this.#set("reconnecting");
          }
          if (state === "transport_ready" && this.needsIdentity && this.connection && !this.suspended) {
            this.needsIdentity = false;
            queueMicrotask(() => { if (this.connection && !this.suspended) void this.retry(); });
          }
          if (["disconnected", "authorization_expired", "identity_conflict"].includes(state) && this.connection && !this.suspended) {
            ++this.generation;
            for (const request of this.requests) request.abort(); this.requests.clear();
            this.#set(state === "identity_conflict" ? "identity_conflict" : state === "authorization_expired" ? "invalid_authentication" : "service_unavailable");
            if (state === "disconnected") this.#scheduleReconnect();
          }
        } });
    } catch { return this.#set("locked"); }
    return this.retry();
  }

  #scheduleReconnect() {
    if (this.descriptor?.transport !== "iscp" || !this.connection || this.suspended || this.reconnectTimer) return;
    const delay = Math.min(30000, 1000 * 2 ** Math.min(this.reconnectAttempt || 0, 5));
    this.reconnectAttempt = (this.reconnectAttempt || 0) + 1;
    this.reconnectTimer = setTimeout(() => { this.reconnectTimer = undefined; void this.retry(); }, delay);
    this.reconnectTimer.unref?.();
  }

  #vault(operation) {
    const pending = this.vaultOperations.then(operation);
    this.vaultOperations = pending.catch(() => {});
    return pending;
  }

  async #identity(candidate) {
    try {
      if (this.descriptor.transport === "iscp") await this.transport.start();
      const response = await this.#fetch()(`${candidate.origin}/api/workbench/identity`, {
        headers: { ...(candidate.authorization ? { Authorization: candidate.authorization } : {}), Accept: "application/json" },
        redirect: "manual", signal: AbortSignal.timeout(5000),
      });
      if (response.status === 401 || response.status === 403) return { state: "invalid_authentication" };
      if (response.status >= 300 && response.status < 400) return { state: "identity_conflict" };
      if (!response.ok) return { state: "service_unavailable" };
      const reader = response.body?.getReader();
      if (!reader) return { state: "identity_conflict" };
      let total = 0;
      const chunks = [];
      while (true) {
        const chunk = await reader.read();
        if (chunk.done) break;
        total += chunk.value.length;
        if (total > 4096) { await reader.cancel(); return { state: "identity_conflict" }; }
        chunks.push(Buffer.from(chunk.value));
      }
      const body = JSON.parse(Buffer.concat(chunks).toString("utf8"));
      if (body?.deployment_id !== candidate.deploymentID ||
          (candidate.ownerID && body.owner_id !== candidate.ownerID) ||
          (candidate.clientID && body.client_id !== candidate.clientID) ||
          ![body.owner_id, body.client_id].every((value) => typeof value === "string" && value.length > 0 && value.length <= 160)) {
        return { state: "identity_conflict" };
      }
      if (this.installationID) {
        if (!/^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/u.test(this.installationID)) return { state: "identity_conflict" };
        const binding = await this.#fetch()(`${candidate.origin}/api/v1/installations`, {
          method: "POST", headers: { ...(candidate.authorization ? { Authorization: candidate.authorization } : {}), "Content-Type": "application/json", Accept: "application/json" },
          body: JSON.stringify({ schema_version: 1, installation_id: this.installationID }),
          redirect: "manual", signal: AbortSignal.timeout(5000),
        });
        if (binding.status === 401 || binding.status === 403) return { state: "invalid_authentication" };
        if (binding.status === 409 || (binding.status >= 300 && binding.status < 400)) return { state: "identity_conflict" };
        if (!binding.ok) return { state: "service_unavailable" };
        const bound = await boundedJSON(binding, 4096);
        if (bound.schema_version !== 1 || bound.installation_id !== this.installationID ||
            bound.client_id !== body.client_id || bound.owner_id !== body.owner_id || bound.deployment_id !== body.deployment_id) return { state: "identity_conflict" };
      } else if (!this.qualification) return { state: "incomplete_setup" };
      return { state: "connected", identity: { ownerID: body.owner_id, clientID: body.client_id } };
    } catch (error) { return { state: isTLSIdentityError(error) ? "identity_conflict" : "service_unavailable" }; }
  }

  #set(state) {
    this.status = Object.freeze({ schema_version: 1, state, ...(this.descriptor ? { backend: {
      schema_version: this.descriptor.schemaVersion || 1,
      origin: this.descriptor.origin, deployment_id: this.descriptor.deploymentID,
      ...(this.descriptor.ownerID ? { owner_id: this.descriptor.ownerID } : {}),
      ...(this.descriptor.transport === "iscp" ? { transport: "iscp", client_id: this.descriptor.clientID, domain_id: this.descriptor.domainID,
        initiator_device_id: this.descriptor.initiatorDeviceID, responder_device_id: this.descriptor.responderDeviceID,
        responder_key_thumbprint: this.descriptor.responderKeyThumbprint, relay_url: this.descriptor.relayURL, relay_profile: this.descriptor.relayProfile, test_mode: true } : {}),
      ...(this.descriptor.certificateSHA256 ? { tls_certificate_sha256: this.descriptor.certificateSHA256 } : {}),
    } } : {}), ...(this.descriptor?.transport === "iscp" ? { transport_stage: this.transportStage, test_mode: true,
      capabilities: { operations: ISCP_OPERATIONS, files: false, mail: false, browser: false, speech: false, approvals: false, settings: false } } : {}), ...(this.connection && (this.connection.transport !== "iscp" || this.connection.identityVerified) ? { client_id: this.connection.clientID, owner_id: this.connection.ownerID } : {}) });
    this.onChange(this.status);
    return this.status;
  }
}

async function boundedJSON(response, limit) {
  const reader = response.body?.getReader();
  if (!reader) throw new Error("Missing backend response");
  const chunks = [];
  let total = 0;
  while (true) {
    const chunk = await reader.read();
    if (chunk.done) break;
    total += chunk.value.byteLength;
    if (total > limit) { await reader.cancel(); throw new Error("Backend response exceeds the allowed size"); }
    chunks.push(Buffer.from(chunk.value));
  }
  return JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(Buffer.concat(chunks)));
}

function descriptorBinding(descriptor) {
  return crypto.createHash("sha256").update(JSON.stringify([descriptor.origin, descriptor.deploymentID, descriptor.ownerID || "", descriptor.certificateSHA256 || "", descriptor.ca || "", ...(descriptor.transport === "iscp" ? [descriptor.transport, descriptor.clientID, descriptor.domainID, descriptor.initiatorDeviceID, descriptor.responderDeviceID, descriptor.responderKeyThumbprint, descriptor.relayURL, descriptor.relayProfile] : [])])).digest("hex");
}

export function authorizeWorkbenchSender(event, window) {
  const frame = event.senderFrame;
  if (!window || event.sender !== window.webContents || frame !== window.webContents.mainFrame ||
      !frame || !frame.url.startsWith("sparkclaw-app://workbench/") || new URL(frame.url).host !== "workbench") {
    throw new Error("Desktop authentication sender is not trusted");
  }
}
