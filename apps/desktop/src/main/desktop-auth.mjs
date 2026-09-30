import fs from "node:fs/promises";
import path from "node:path";
import crypto from "node:crypto";
import { parseBackendDescriptor, loadLocalBackendDescriptor, loadLocalBackendConnection } from "./local-backend.mjs";
import { isTLSIdentityError, pinnedHTTPSFetch } from "./pinned-https.mjs";

export class DesktopAuth {
  constructor({ vault, descriptorPath, legacyPaths, qualification = false, fetcher, onChange = () => {}, onLock = () => {} }) {
    Object.assign(this, { vault, descriptorPath, legacyPaths, qualification, fetcher, onChange, onLock });
    this.requests = new Set();
    this.generation = 0;
    this.vaultOperations = Promise.resolve();
    this.status = Object.freeze({ schema_version: 1, state: "incomplete_setup" });
  }

  async initialize() {
    try { this.descriptor = await loadLocalBackendDescriptor({ descriptorPath: this.descriptorPath }); }
    catch {
      try { this.descriptor = await loadLocalBackendDescriptor(this.legacyPaths); } catch { return this.#set("incomplete_setup"); }
    }
    if (this.qualification && this.legacyPaths) {
      try { this.connection = await loadLocalBackendConnection(this.legacyPaths); return this.retry(); } catch { /* new-install gate */ }
    }
    if (!this.vault.available()) return this.#set("secure_storage_unavailable");
    try {
      const saved = await this.vault.load();
      if (saved && saved.binding === descriptorBinding(this.descriptor)) this.connection = Object.freeze({ ...saved, origin: this.descriptor.origin });
      else if (saved) await this.vault.clear();
    } catch { await this.vault.clear(); }
    return this.connection ? this.retry() : this.#set("locked");
  }

  async configure(value) {
    const descriptor = parseBackendDescriptor(value);
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

  async retry() {
    if (!this.connection) return this.#set(this.descriptor ? "locked" : "incomplete_setup");
    const generation = this.generation;
    this.#set("reconnecting");
    const result = await this.#identity(this.connection);
    if (generation !== this.generation) return this.status;
    if (result.state === "invalid_authentication" || result.state === "identity_conflict") return this.logout(result.state);
    return this.#set(result.state);
  }

  async logout(state = "locked") {
    const generation = ++this.generation;
    this.connection = undefined;
    for (const request of this.requests) request.abort();
    this.requests.clear();
    await this.onLock();
    await this.#vault(() => this.vault.clear());
    return generation === this.generation ? this.#set(this.descriptor ? state : "incomplete_setup") : this.status;
  }

  async authorizedFetch(raw, init = {}) {
    if (this.status.state !== "connected" || !this.connection) return new Response(null, { status: 401 });
    const url = new URL(raw);
    if (url.origin !== this.descriptor.origin) throw new Error("Backend origin is invalid");
    const generation = this.generation;
    const controller = new AbortController();
    this.requests.add(controller);
    const headers = new Headers(init.headers);
    headers.set("authorization", this.connection.authorization);
    try {
      const response = await this.#fetch()(raw, { ...init, headers, redirect: "manual", signal: AbortSignal.any([controller.signal, AbortSignal.timeout(30000), ...(init.signal ? [init.signal] : [])]) });
      if (generation !== this.generation) { controller.abort(); return new Response(null, { status: 401 }); }
      if (response.status === 401) {
        await this.logout("invalid_authentication");
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
              if (bytes > 1 << 20) { controller.abort(); requests.delete(controller); await reader.cancel(); stream.error(new Error("Backend response exceeds the allowed size")); }
              else stream.enqueue(chunk.value);
            }
          } catch (error) { requests.delete(controller); stream.error(error); }
        },
        async cancel() { requests.delete(controller); controller.abort(); await reader.cancel().catch(() => {}); },
      });
      return new Response(body, { status: response.status, statusText: response.statusText, headers: response.headers });
    } catch (error) {
      this.requests.delete(controller);
      if (generation === this.generation) {
        if (isTLSIdentityError(error)) await this.logout("identity_conflict");
        else this.#set("service_unavailable");
      }
      throw new Error("Backend request is unavailable");
    }
  }

  #fetch() { return this.descriptor.schemaVersion === 2 ? pinnedHTTPSFetch(this.descriptor) : this.fetcher; }

  #vault(operation) {
    const pending = this.vaultOperations.then(operation);
    this.vaultOperations = pending.catch(() => {});
    return pending;
  }

  async #identity(candidate) {
    try {
      const response = await this.#fetch()(`${candidate.origin}/api/workbench/identity`, {
        headers: { Authorization: candidate.authorization, Accept: "application/json" },
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
      return { state: "connected", identity: { ownerID: body.owner_id, clientID: body.client_id } };
    } catch (error) { return { state: isTLSIdentityError(error) ? "identity_conflict" : "service_unavailable" }; }
  }

  #set(state) {
    this.status = Object.freeze({ schema_version: 1, state, ...(this.descriptor ? { backend: {
      schema_version: this.descriptor.schemaVersion || 1,
      origin: this.descriptor.origin, deployment_id: this.descriptor.deploymentID,
      ...(this.descriptor.ownerID ? { owner_id: this.descriptor.ownerID } : {}),
      ...(this.descriptor.certificateSHA256 ? { tls_certificate_sha256: this.descriptor.certificateSHA256 } : {}),
    } } : {}), ...(this.connection ? { client_id: this.connection.clientID, owner_id: this.connection.ownerID } : {}) });
    this.onChange(this.status);
    return this.status;
  }
}

function descriptorBinding(descriptor) {
  return crypto.createHash("sha256").update(JSON.stringify([descriptor.origin, descriptor.deploymentID, descriptor.ownerID || "", descriptor.certificateSHA256 || "", descriptor.ca || ""])).digest("hex");
}

export function authorizeWorkbenchSender(event, window) {
  const frame = event.senderFrame;
  if (!window || event.sender !== window.webContents || frame !== window.webContents.mainFrame ||
      !frame || !frame.url.startsWith("sparkclaw-app://workbench/") || new URL(frame.url).host !== "workbench") {
    throw new Error("Desktop authentication sender is not trusted");
  }
}
