import fs from "node:fs/promises";
import syncFS from "node:fs";
import path from "node:path";
import crypto from "node:crypto";
import { parseBackendDescriptor, loadLocalBackendDescriptor, loadLocalBackendConnection } from "./local-backend.mjs";
import { isTLSIdentityError, pinnedHTTPSFetch } from "./pinned-https.mjs";
import { createConnectionCredential, parseConnectionCredential } from "./connection-credential.mjs";
import { isISCPCapabilityReportCurrent, projectISCPCapabilities } from "./iscp-capabilities.mjs";
import { loadISCPProfile } from "./iscp-profile.mjs";
import { ISCP_CONNECTION_PREFIX, parseISCPConnectionCredential } from "./iscp-connection-credential.mjs";
import { enrollNodeISCP, hasPendingNodeEnrollment } from "./iscp-node-enrollment.mjs";
import { ISCP_LAUNCH_PROFILE_NAME, persistNodeISCPLaunchProfile } from "./iscp-launch-profile.mjs";
import { LANMailClient } from "./lan-mail-client.mjs";
import { ISCPTransport, ISCP_OPERATIONS, ISCP_BODY_BYTES, ISCPRequestNotSentError, mapISCPRequest } from "./iscp-transport.mjs";
import { traceISCPRead } from "./iscp-read-diagnostics.mjs";

const ISCP_STARTUP_READS = new Set(["/api/config", "/readyz", "/api/owner", "/api/notifications", "/api/clients", "/api/workbench/identity", "/api/email/sync-status"]);

export class DesktopAuth {
  constructor({ readLocalFile, vault, descriptorPath, qualificationPaths, installationID, qualification = false, requireLAN = false, fetcher, iscpProfilePath, allowLocalISCPTest = false, allowNodeManagedISCP = false, nodeEnroller = enrollNodeISCP, launchSelectionPath, userDataDirectory, selectionWriter = persistNodeISCPLaunchProfile, packaged = false, resourcesPath, transportFactory = (options) => new ISCPTransport(options), onChange = () => {}, onLock = () => {} }) {
    Object.assign(this, { readLocalFile, vault, descriptorPath, qualificationPaths, installationID, qualification, requireLAN, fetcher, iscpProfilePath, allowLocalISCPTest, allowNodeManagedISCP, nodeEnroller, launchSelectionPath, userDataDirectory, selectionWriter, packaged, resourcesPath, transportFactory, onChange, onLock });
    this.requests = new Set();
    this.defaultVault = vault;
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
    const nodeSelected = this.descriptor?.mode === "node-managed";
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
        if (nodeSelected) await fs.rm(this.launchSelectionPath || path.join(path.dirname(this.descriptorPath), ISCP_LAUNCH_PROFILE_NAME), { force: true });
      } finally { await fs.rm(temporary, { force: true }); }
    });
    if (generation !== this.generation) return this.status;
    if (nodeSelected) { this.iscpProfilePath = undefined; this.allowNodeManagedISCP = false; this.vault = this.defaultVault; }
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
    if (typeof value === "string" && value.startsWith(ISCP_CONNECTION_PREFIX)) {
      const parsed = parseISCPConnectionCredential(value, { allowExpired: true });
      if (this.qualification || this.enrollmentPending) throw new Error("ISCP enrollment is unavailable or already in progress");
      if (this.deletionIntent?.state === "pending") throw new Error("Reconcile the pending authorization deletion before enrolling another connection");
      const pending = this.#enrollNode(value, parsed.payload);
      this.enrollmentPending = pending;
      try { return await pending; } finally { if (this.enrollmentPending === pending) this.enrollmentPending = undefined; }
    }
    const parsed = parseConnectionCredential(value);
    await this.configure(parsed.descriptor);
    return this.login(parsed.token);
  }

  async #enrollNode(value, invitation) {
    if (!this.vault.available()) throw new Error("Secure credential storage is unavailable; the existing connection is retained");
    const generation = this.generation, controller = new AbortController();
    const directory = path.join(path.dirname(this.descriptorPath), "iscp-enrollment", crypto.createHash("sha256").update(value).digest("hex"));
    if (Date.parse(invitation.expires_at) <= Date.now() && !await hasPendingNodeEnrollment(directory, this.installationID, invitation.domain_id)) throw new Error("ISCP connection credential is expired");
    if (generation !== this.generation) throw new Error("ISCP enrollment was canceled");
    this.requests.add(controller);
    let candidateTransport, committed = false, activatedGeneration;
    try {
      const result = await this.nodeEnroller({ credential: value, directory, installationID: this.installationID, signal: controller.signal,
        packaged: this.packaged, resourcesPath: this.resourcesPath });
      if (generation !== this.generation || controller.signal.aborted) throw new Error("Canceled enrollment");
      const profile = await loadISCPProfile(result.profile_path), descriptor = profile.descriptor;
      if (descriptor.mode !== "node-managed" || JSON.stringify(descriptor) !== JSON.stringify(parseBackendDescriptor(result.descriptor)) || path.dirname(profile.helperConfigPath) !== directory ||
          descriptor.deploymentID !== invitation.deployment_id || descriptor.ownerID !== invitation.owner_id || descriptor.clientID !== invitation.client_id ||
          descriptor.domainID !== invitation.domain_id || descriptor.relayURL !== invitation.origin || descriptor.responderDeviceID !== invitation.responder_identity.device_id ||
          descriptor.responderKeyThumbprint !== invitation.responder_identity.public_key.kid) throw new Error("Enrollment identity differs");
      candidateTransport = this.#transportFor(profile);
      const cancel = () => candidateTransport.close();
      controller.signal.addEventListener("abort", cancel, { once: true });
      const record = { schema_version: 2, transport: "iscp", mode: "node-managed", testMode: false, configPath: profile.configPath,
        origin: descriptor.origin, deploymentID: descriptor.deploymentID, ownerID: descriptor.ownerID, clientID: descriptor.clientID,
        binding: descriptorBinding(descriptor), identityVerified: true };
      const candidateVault = this.defaultVault.forISCPProfile?.(record.binding) || this.vault;
      const identity = await this.#identity(record, descriptor, candidateTransport);
      if (identity.state !== "connected" || !candidateTransport.capabilities) throw new Error("Enrollment identity unavailable");
      const report = await candidateTransport.invoke("capabilities.get");
      if (!isISCPCapabilityReportCurrent(candidateTransport.capabilities, report)) throw new Error("Enrollment capabilities invalid");
      if (generation !== this.generation || controller.signal.aborted) throw new Error("Canceled enrollment");
      await this.#vault(async () => {
        if (generation !== this.generation || controller.signal.aborted) throw new Error("Canceled enrollment");
        const previous = await candidateVault.load();
        try {
          await candidateVault.save(record);
          if (generation !== this.generation || controller.signal.aborted) throw new Error("Canceled enrollment");
          this.selectionWriter({ filename: this.launchSelectionPath || path.join(path.dirname(this.descriptorPath), ISCP_LAUNCH_PROFILE_NAME),
            profilePath: profile.configPath, userDataDirectory: this.userDataDirectory || path.dirname(this.descriptorPath) });
        } catch (error) {
          if (candidateVault === this.vault) {
            if (previous) await candidateVault.save(previous); else await candidateVault.clear();
          }
          throw error;
        }
        clearTimeout(this.capabilityTimer); clearTimeout(this.capabilityExpiryTimer); clearTimeout(this.reconnectTimer);
        this.requests.delete(controller);
        activatedGeneration = ++this.generation;
        for (const request of this.requests) request.abort(); this.requests.clear();
        this.transport?.close();
        this.descriptor = descriptor; this.connection = Object.freeze(record); this.transport = candidateTransport; this.vault = candidateVault;
        this.iscpProfilePath = profile.configPath; this.allowNodeManagedISCP = true; this.allowLocalISCPTest = false;
        this.deletionIntent = undefined; this.authorizationRevoked = false; this.suspended = false;
        this.capabilityReport = report; this.transportStage = "transport_ready"; this.reconnectAttempt = 0;
        committed = true;
      });
      await Promise.resolve(this.onLock()).catch(() => {});
      if (activatedGeneration !== this.generation || this.transport !== candidateTransport) return this.status;
      this.#scheduleCapabilityRefresh();
      return this.#set("connected");
    } catch {
      throw new Error("ISCP enrollment could not be completed; the existing connection is retained. Retry the same credential to reconcile.");
    } finally {
      this.requests.delete(controller);
      if (!committed) candidateTransport?.close();
    }
  }

  connectionCredential(token) {
    if (!this.descriptor) throw new Error("Backend identity is unavailable");
    if (this.descriptor.mode === "node-managed") return this.#nodeConnectionCredential(token);
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

  async #nodeConnectionCredential(value) {
    const generation = this.generation, descriptor = this.descriptor;
    if (this.status.state !== "connected" || !this.connection?.identityVerified) throw new Error("Reconnect to verify the issuer before creating a connection credential");
    const { payload } = parseISCPConnectionCredential(value);
    const profile = await loadISCPProfile(this.iscpProfilePath), issuer = profile.nodeIssuer;
    const sameKey = (left, right) => left?.device_id === right?.device_id && left?.domain_id === right?.domain_id && left?.public_key?.kid === right?.public_key?.kid && left?.public_key?.public === right?.public_key?.public;
    if (generation !== this.generation || descriptor !== this.descriptor || !issuer || payload.deployment_id !== descriptor.deploymentID || payload.owner_id !== descriptor.ownerID ||
        payload.domain_id !== descriptor.domainID || payload.origin !== descriptor.relayURL || payload.issuer_route !== issuer.issuerRoute || payload.tls_certificate_pem !== issuer.tlsCertificatePEM ||
        payload.responder_identity.device_id !== descriptor.responderDeviceID || payload.responder_identity.public_key.kid !== descriptor.responderKeyThumbprint ||
        !sameKey(payload.issuer_identity, issuer.issuerIdentity) || !sameKey(payload.relay_signer_identity, issuer.relaySignerIdentity) || !sameKey(payload.invitation_signer_identity, issuer.invitationSignerIdentity)) throw new Error("Issued connection credential differs from the verified backend issuer");
    // clients.issue returns this issuer-signed one-use credential in its existing
    // token field. Preserve that exact value for the original copy interaction.
    return value;
  }

  retry() {
    if (this.descriptor?.transport === "iscp" && this.authorizationRevoked) return Promise.resolve(this.#set("invalid_authentication"));
    if (!this.connection) return Promise.resolve(this.#set(this.descriptor ? "locked" : "incomplete_setup"));
    const generation = this.generation, transport = this.transport;
    if (this.retryFlight?.generation === generation && this.retryFlight.transport === transport) return this.retryFlight.promise;
    const flight = { generation, transport };
    this.retryFlight = flight;
    this.needsIdentity = false;
    // Publish the flight before any synchronous transport/status callback can
    // request verification again. Ready, timers and explicit retry share it.
    flight.promise = Promise.resolve().then(() => this.#retry(generation, transport)).finally(() => {
      if (this.retryFlight === flight) this.retryFlight = undefined;
    });
    return flight.promise;
  }

  async #retry(generation, transport) {
    const descriptor = this.descriptor;
    const current = () => generation === this.generation && transport === this.transport && descriptor === this.descriptor && this.connection;
    if (!current()) return this.status;
    this.suspended = false;
    clearTimeout(this.reconnectTimer); this.reconnectTimer = undefined;
    this.#set("reconnecting");
    const result = await this.#identity(this.connection, descriptor, transport);
    if (!current()) return this.status;
    if (descriptor.transport === "iscp") {
      if (result.state === "connected") {
        if (this.transport.capabilities) {
          const refreshed = await this.#readCapabilityReport(generation);
          if (!refreshed.current || !current()) return this.status;
          if (refreshed.error) this.capabilityReport = undefined;
          this.#scheduleCapabilityRefresh();
        }
        this.reconnectAttempt = 0;
        if (!this.connection.identityVerified) {
          const verified = Object.freeze({ ...this.connection, identityVerified: true });
          try { await this.#vault(() => current() ? this.vault.save(verified) : undefined); }
          catch { return current() ? this.#set("secure_storage_unavailable") : this.status; }
          if (!current()) return this.status;
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

  async deleteAuthorization() {
    if(this.descriptor?.transport!=="iscp"||!this.transport)throw new Error("Authorization management is unavailable");
    let intent=this.deletionIntent;
    if(!intent) {
      const revision=this.transport.capabilities?.authorization_revision;
      if(!Number.isSafeInteger(revision)||revision<1)throw new Error("Reconnect to verify the authorization before deletion");
      intent={schema_version:1,profile:this.iscpProfilePath,binding:descriptorBinding(this.descriptor),operation_id:crypto.randomUUID(),expected_revision:revision,state:"pending"};
      this.#saveDeletionIntent(intent);
    }
    this.authorizationRevoked=true;this.suspend();await this.onLock();this.#set("invalid_authentication");
    try {
      const receipt=await this.transport.control("authorization_delete",intent.operation_id,intent.expected_revision);
      this.#saveDeletionIntent({...intent,state:"revoked",receipt});
    } finally { this.#set("invalid_authentication"); }
    return this.status;
  }

  async checkNewAuthorization() {
    const intent=this.deletionIntent;
    if(!intent||intent.state!=="revoked"||!intent.receipt||!this.transport||!this.connection)throw new Error("First reconcile the original authorization deletion");
    const profile=await loadISCPProfile(this.iscpProfilePath);
    if(descriptorBinding(profile.descriptor)!==intent.binding)throw new Error("A new authorization must retain the selected backend identity");
    this.transport.close();this.transport.configPath=profile.helperConfigPath;
    const result=await this.#identity(this.connection);
    if(result.state!=="connected"||!Number.isSafeInteger(this.transport.capabilities?.authorization_revision)||this.transport.capabilities.authorization_revision<=intent.receipt.authorization_revision) {this.transport.close();throw new Error("No newer verified authorization is installed");}
    const report=await this.transport.invoke("capabilities.get");
    if(report.authorization_revision!==this.transport.capabilities.authorization_revision){this.transport.close();throw new Error("New authorization revision differs");}
    const filename=this.#deletionFilename();
    await fs.rename(filename,path.join(path.dirname(filename),`authorization-deletion.${intent.operation_id}.revoked.json`));
    const directory=syncFS.openSync(path.dirname(filename),'r');try{syncFS.fsyncSync(directory);}finally{syncFS.closeSync(directory);}
    this.deletionIntent=undefined;this.authorizationRevoked=false;this.suspended=false;this.capabilityReport=report;
    return this.retry();
  }

  #saveDeletionIntent(intent) {
    const filename=this.#deletionFilename(), temporary=filename+"."+crypto.randomUUID()+".tmp";
    syncFS.mkdirSync(path.dirname(filename),{recursive:true,mode:0o700});const fd=syncFS.openSync(temporary,"wx",0o600);
    try{syncFS.writeFileSync(fd,JSON.stringify(intent));syncFS.fsyncSync(fd);}finally{syncFS.closeSync(fd);}
    syncFS.renameSync(temporary,filename);const directory=syncFS.openSync(path.dirname(filename),'r');try{syncFS.fsyncSync(directory);}finally{syncFS.closeSync(directory);}
    this.deletionIntent=intent;
  }

  #deletionFilename() {
    const name = this.descriptor?.mode === "node-managed" ? `authorization-deletion.${descriptorBinding(this.descriptor)}.json` : "authorization-deletion.json";
    return path.join(path.dirname(this.descriptorPath), name);
  }

  async logout(state = "locked") {
    clearTimeout(this.capabilityTimer);
    clearTimeout(this.capabilityExpiryTimer);
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
    if(this.transport?.capabilities) {
      const value=JSON.parse(body);
      if(Buffer.byteLength(body)>8*1024*1024 || value.input_files?.length && !this.status.capabilities?.files) throw new Error("ISCP input capability or budget is unavailable; your draft is retained");
      return;
    }
    const request = mapISCPRequest(`${this.descriptor.origin}/api/v1/executions`, { method: "POST", body }, this.descriptor.origin);
    const overhead = Buffer.byteLength(JSON.stringify({ ...request, id: crypto.randomUUID() })) - Buffer.byteLength(JSON.stringify(request.body));
    if (Buffer.byteLength(body) > ISCP_BODY_BYTES || Buffer.byteLength(body) + overhead > 65536) throw new Error("ISCP request exceeds the 64 KiB test profile limit; your draft is retained");
  }

  async invokeISCP(operation, body, options = {}) {
    if(this.descriptor?.transport !== "iscp" || this.status.state !== "connected") throw new Error("ISCP connection is unavailable");
    const generation=this.generation;const value=await this.transport.invoke(operation,body,options);
    if(generation!==this.generation)throw new Error("ISCP authentication changed");
    return value;
  }

  async authorizedFetch(raw, init = {}) {
    // The mail domain permits a 200 KiB body whose JSON escaping may expand
    // sixfold. Keep this narrow exception aligned with its encoded limit.
    const pathname = new URL(raw).pathname;
    const mailDraft = /^\/api\/email\/drafts(?:\/|$)/u.test(pathname) || pathname === '/api/email/replies/polish';
    // Render artifacts have their own 2 MiB bound plus a small response envelope.
    const mailPreview = /^\/api\/email\/messages\/[A-Za-z0-9][A-Za-z0-9_.:-]{0,159}\/render-preview$/u.test(pathname);
    return this.#authorizedFetch(raw, init, mailPreview ? (2 << 20) + (64 << 10) : mailDraft ? 2 << 20 : 1 << 20);
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
    const cached = /^\/api\/v1\/mail\/[A-Za-z0-9_.:-]+\/messages\/[A-Za-z0-9_.:-]+\/attachments\/[A-Za-z0-9_.:-]+$/u.test(url.pathname) && !url.search;
    const original = /^\/api\/email\/messages\/[A-Za-z0-9][A-Za-z0-9_.:-]{0,159}\/file$/u.test(url.pathname) && [...url.searchParams].every(([key, value]) => key === 'part_id' && /^[A-Za-z0-9][A-Za-z0-9_.:-]{0,159}$/u.test(value)) && [...url.searchParams].length <= 1;
    if (!(cached || original) || url.hash || url.username || url.password || (init.method && init.method !== "GET")) throw new Error("Mail attachment path is invalid");
    return this.#authorizedFetch(raw, init, 64 * 1024 * 1024);
  }

  async #authorizedFetch(raw, init, byteLimit) {
    if (this.status.state !== "connected" || !this.connection) return new Response(null, { status: 401 });
    const url = new URL(raw);
    if (url.origin !== this.descriptor.origin) throw new Error("Backend origin is invalid");
    if (this.descriptor.transport === "iscp" && !this.transport?.capabilities) mapISCPRequest(raw, init, this.descriptor.origin);
    const generation = this.generation;
    const mailMutation = ["POST", "PUT"].includes((init.method || "GET").toUpperCase()) && /^\/api\/email\/drafts(?:\/[^/]+(?:\/send)?)?$/u.test(url.pathname) && !url.search;
    const mailReconcile = (init.method || "GET").toUpperCase() === "POST" && /^\/api\/email\/drafts\/[^/]+\/reconcile$/u.test(url.pathname) && !url.search;
    const mailLogin = (init.method || "GET").toUpperCase() === "POST" && /^\/api\/email\/providers\/(outlook|qq_mail|gmail)\/(check|login-browser)$/u.test(url.pathname) && !url.search;
    const method = (init.method || 'GET').toUpperCase();
    const startupRead = this.descriptor.transport === "iscp" && method === "GET" && ISCP_STARTUP_READS.has(url.pathname) && !url.search && !url.hash && init.body == null;
    const transport = this.transport;
    const mailPopup = !url.search && (method === 'POST' && ['/api/email/replies/polish','/api/email/source/cleanup'].includes(url.pathname) || method === 'DELETE' && /^\/api\/email\/conversations\/[A-Za-z0-9][A-Za-z0-9_.:-]{0,159}$/u.test(url.pathname) || method === 'PATCH' && /^\/api\/email\/providers\/(outlook|qq_mail|gmail)$/u.test(url.pathname));
    const requestTimeout = mailMutation || mailReconcile || mailLogin || mailPopup ? 180000 : 30000;
    const controller = new AbortController();
    this.requests.add(controller);
    const headers = new Headers(init.headers);
    if (this.descriptor.transport !== "iscp") headers.set("authorization", this.connection.authorization);
    let signal, deadlineSignal;
    const startedMS = process.env.SPARKCLAW_PRIVATE_ISCP_READ_DIAGNOSTICS === "1" ? Date.now() : undefined;
    const diagnostic = (phase, error, responseStatus) => { if (startupRead) traceISCPRead({ phase, path: url.pathname, auth: this, error, generation, transport, signal, callerSignal: init.signal, deadlineSignal, controllerSignal: controller.signal, responseStatus, startedMS }); };
    try {
      deadlineSignal = AbortSignal.timeout(requestTimeout);
      signal = AbortSignal.any([controller.signal, deadlineSignal, ...(init.signal ? [init.signal] : [])]);
      diagnostic("request");
      let fetcher = this.#fetch();
      if (this.descriptor.schemaVersion === 2 && this.installationID && url.pathname.startsWith("/api/email/")) {
        const scope = { deployment_id: this.connection.deploymentID, owner_id: this.connection.ownerID, client_id: this.connection.clientID, installation_id: this.installationID };
        const scopeKey = JSON.stringify(scope);
        if (this.lanMailScope !== scopeKey) {
          this.lanMail = new LANMailClient({ root: path.join(path.dirname(this.descriptorPath), "lan-mail-mutations"), scope, readLocalFile: this.readLocalFile,
            generation: () => this.generation, ready: () => this.status.state === "connected" });
          this.lanMailScope = scopeKey;
        }
        const pinned = fetcher;
        fetcher = (target, options) => this.lanMail.fetch(target, options, (nextTarget, nextOptions) => {
          if (generation !== this.generation || !this.connection) throw new Error("Attachment authentication changed");
          const trusted = new Headers(nextOptions.headers);
          trusted.set("authorization", this.connection.authorization);
          trusted.set("X-SparkClaw-Installation", this.installationID);
          return pinned(nextTarget, { ...nextOptions, headers: trusted, redirect: "manual", signal });
        });
      }
      const fetchOptions = { ...init, headers, redirect: "manual", signal };
      const response = startupRead ? await this.#startupRead(fetcher, raw, fetchOptions, generation, transport) : await fetcher(raw, fetchOptions);
      diagnostic("response", undefined, response.status);
      if (generation !== this.generation) { controller.abort(); return new Response(null, { status: 401 }); }
      if (response.status === 401 || (this.descriptor.transport === "iscp" && !this.transport?.capabilities && response.status === 403)) {
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
      diagnostic("failure", error);
      this.requests.delete(controller);
      // Leaving a view cancels its safe read, including an already-dispatched
      // mail-status poll. That is not a transport failure or NotSent proof.
      // A real helper reset remains authoritative through its state callback.
      if (startupRead && init.signal?.aborted && !controller.signal.aborted && generation === this.generation && transport === this.transport && transport.state === "transport_ready" && this.status.state === "connected") throw error;
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

  async #startupRead(fetcher, raw, init, generation, transport) {
    let delay = 25, throttled = 0;
    while (true) {
      if (generation !== this.generation || transport !== this.transport || this.status.state !== "connected" || !this.connection) throw new ISCPRequestNotSentError("ISCP authentication changed", "canceled");
      if (init.signal.aborted) throw new ISCPRequestNotSentError("ISCP startup read was canceled", "canceled");
      try {
        const response = await fetcher(raw, init);
        if (response.status !== 429 || throttled >= 3 || response.headers.get("x-sparkclaw-error-code") !== "throttled" || response.headers.get("x-sparkclaw-retryable") !== "true") return response;
        let details;
        // This JSON already passed the helper's bounded response frame check.
        try { details = await response.clone().json(); } catch { return response; }
        if (!details || typeof details !== "object" || Array.isArray(details) || details.error_code !== "throttled" || details.retryable !== true || details.retry_after_ms !== undefined && (!Number.isSafeInteger(details.retry_after_ms) || details.retry_after_ms < 0 || details.retry_after_ms > 5000)) return response;
        await response.body?.cancel();
        await waitForStartupAdmission(init.signal, Math.max(details.retry_after_ms ?? 250, Math.min(1000, 250 * 2 ** throttled++)));
      }
      catch (error) {
        // Only local pre-write admission proves the request did not reach the
        // helper. Sent failures, remote errors and all mutations keep their
        // original behavior; every wait shares the caller's total deadline.
        if (!(error instanceof ISCPRequestNotSentError) || error.reason !== "capacity") throw error;
        await waitForStartupAdmission(init.signal, delay);
        delay = Math.min(250, delay * 2);
      }
    }
  }

  #fetch(descriptor = this.descriptor, transport = this.transport) {
    if (descriptor.transport === "iscp") return transport.fetch.bind(transport);
    return descriptor.schemaVersion === 2 ? pinnedHTTPSFetch(descriptor) : this.fetcher;
  }

  suspend() {
    this.suspended = true; clearTimeout(this.capabilityTimer); clearTimeout(this.capabilityExpiryTimer); ++this.generation; clearTimeout(this.reconnectTimer); this.reconnectTimer = undefined;
    for (const request of this.requests) request.abort();
    this.requests.clear(); this.transport?.close();
    if (this.descriptor?.transport === "iscp" && this.connection) this.#set("service_unavailable");
  }

  close() { this.suspend(); this.transport = undefined; }

  async #initializeISCP() {
    if (this.qualification) return this.#set("incomplete_setup");
    let profile;
    try { profile = await loadISCPProfile(this.iscpProfilePath); } catch { return this.#set("incomplete_setup"); }
    if (profile.descriptor.mode === "node-managed" ? !this.allowNodeManagedISCP : !this.allowLocalISCPTest) return this.#set("incomplete_setup");
    this.descriptor = profile.descriptor;
    if (this.descriptor.mode === "node-managed") this.vault = this.defaultVault.forISCPProfile?.(descriptorBinding(this.descriptor)) || this.vault;
    try {
      const filename=this.#deletionFilename();
      const info=await fs.lstat(filename);if(!info.isFile()||info.isSymbolicLink()||(info.mode&0o077)||info.size>8192)throw new Error("Authorization deletion record is unsafe");
      const intent=JSON.parse(await fs.readFile(filename,"utf8"));
      if(intent.schema_version!==1||intent.profile!==this.iscpProfilePath||intent.binding!==descriptorBinding(this.descriptor))throw new Error("Authorization deletion binding differs");
      this.deletionIntent=intent;this.authorizationRevoked=true;
    } catch(error) {if(error.code!=="ENOENT")return this.#set("locked");}
    if (!this.vault.available()) return this.#set("secure_storage_unavailable");
    try {
      const binding = descriptorBinding(this.descriptor);
      const saved = await this.vault.load();
      if (this.descriptor.mode === "node-managed" && (!saved || saved.binding !== binding || saved.configPath !== profile.configPath || saved.transport !== "iscp" || saved.mode !== "node-managed" || saved.testMode !== false)) return this.#set("locked");
      const record = { schema_version: 2, transport: "iscp", testMode: this.descriptor.testMode, ...(this.descriptor.mode ? { mode: this.descriptor.mode } : {}), configPath: profile.configPath,
        origin: this.descriptor.origin, deploymentID: this.descriptor.deploymentID, ownerID: this.descriptor.ownerID,
        clientID: this.descriptor.clientID, binding,
        identityVerified: saved?.transport === "iscp" && saved.binding === binding && saved.configPath === profile.configPath && saved.identityVerified === true };
      if (saved && (saved.binding !== binding || saved.transport !== "iscp" || saved.configPath !== profile.configPath)) await this.vault.clear();
      if (!saved || saved.binding !== binding || saved.configPath !== profile.configPath || saved.transport !== "iscp") await this.vault.save(record);
      this.connection = Object.freeze(record);
      this.transport = this.#transportFor(profile);
    } catch { return this.#set("locked"); }
    if(this.deletionIntent) {
      if(this.deletionIntent.state==='pending')try{await this.deleteAuthorization();}catch{/* Keep the durable pending intent visible. */}
      return this.#set("invalid_authentication");
    }
    return this.retry();
  }

  #transportFor(profile) {
    const descriptor = profile.descriptor;
    const transport = this.transportFactory({ configPath: profile.helperConfigPath, origin: descriptor.origin,
        clientOperationVault: descriptor.mode === "node-managed" ? this.defaultVault.forISCPProfile?.(descriptorBinding(descriptor)) : undefined,
        expectedBinding: { deployment_id: descriptor.deploymentID, owner_id: descriptor.ownerID, client_id: descriptor.clientID },
        expectedIdentity: { domain_id: descriptor.domainID, initiator_device_id: descriptor.initiatorDeviceID,
          responder_device_id: descriptor.responderDeviceID, responder_key_thumbprint: descriptor.responderKeyThumbprint, relay_url: descriptor.relayURL, relay_profile: descriptor.relayProfile },
        packaged: this.packaged, resourcesPath: this.resourcesPath,
        installationID: this.installationID, readLocalFile: this.readLocalFile,
        canSendMailAttachments: () => projectISCPCapabilities(this.transport?.capabilities, this.capabilityReport).surfaces.mail_send_attachments.enabled,
        journalRoot: path.join(path.dirname(this.descriptorPath), "iscp-objects"),
        onCapabilities: () => { if (this.transport !== transport) return; if(this.status.state === "connected") this.#set("connected"); },
        onState: (state) => {
          if (this.transport !== transport) return;
          this.transportStage = state;
          if (["verifying_relay", "connecting", "relay_ready", "handshaking", "negotiating", "capability_negotiation_failed"].includes(state) && this.connection && !this.suspended && !this.authorizationRevoked) {
            if (this.status.state === "connected") {
              ++this.generation; this.needsIdentity = true;
              for (const request of this.requests) request.abort(); this.requests.clear();
            }
            this.#set("reconnecting");
          }
          if (state === "transport_ready" && this.needsIdentity && this.connection && !this.suspended && !this.authorizationRevoked) {
            this.needsIdentity = false;
            const generation = this.generation;
            queueMicrotask(() => { if (generation === this.generation && this.transport === transport && this.connection && !this.suspended && !this.authorizationRevoked) void this.retry(); });
          }
          if (["disconnected", "authorization_expired", "authorization_revoked", "identity_conflict"].includes(state) && this.connection && !this.suspended) {
            ++this.generation;
            for (const request of this.requests) request.abort(); this.requests.clear();
            // Expired short Grants can be renewed under the retained standing
            // authorization. Only the helper's verified revocation is final.
            if (state === "authorization_revoked") this.authorizationRevoked = true;
            clearTimeout(this.reconnectTimer); this.reconnectTimer = undefined;
            this.#set(this.authorizationRevoked ? "invalid_authentication" : state === "identity_conflict" ? "identity_conflict" : "service_unavailable");
            if (["disconnected", "authorization_expired"].includes(state)) this.#scheduleReconnect();
          }
        } });
    return transport;
  }

  async #readCapabilityReport(generation) {
    const transport = this.transport;
    const current = () => generation === this.generation && transport === this.transport;
    try {
      const report = await transport.invoke("capabilities.get");
      if (!current()) return { current: false };
      if (!isISCPCapabilityReportCurrent(transport.capabilities, report)) throw new Error("ISCP capability report is invalid");
      this.capabilityReport = report;
      return { current: true };
    } catch (error) {
      return { current: current(), error };
    }
  }

  #scheduleCapabilityRefresh(attempt = 0, delayMS = 30000) {
    clearTimeout(this.capabilityTimer);
    this.capabilityTimer = setTimeout(async () => {
      if (this.status.state !== "connected" || this.suspended || this.authorizationRevoked) return;
      const refreshed = await this.#readCapabilityReport(this.generation);
      if (!refreshed.current) return;
      if (refreshed.error) {
        // Only local admission proves this read never reached the helper. A
        // remote 429, timeout, protocol failure or changed authorization closes
        // the projection immediately; none may extend the last report's TTL.
        const capacity = refreshed.error instanceof ISCPRequestNotSentError && refreshed.error.reason === "capacity";
        if (capacity && attempt < 4) {
          if (!isISCPCapabilityReportCurrent(this.transport?.capabilities, this.capabilityReport)) {
            this.capabilityReport = undefined;
            this.#set("connected");
          }
          this.#scheduleCapabilityRefresh(attempt + 1, 250 * 2 ** attempt);
          return;
        }
        this.capabilityReport = undefined;
      }
      this.#set("connected");
      this.#scheduleCapabilityRefresh();
    }, delayMS);
    this.capabilityTimer.unref?.();
  }

  #scheduleCapabilityExpiry() {
    clearTimeout(this.capabilityExpiryTimer);
    if (this.status.state !== "connected" || !isISCPCapabilityReportCurrent(this.transport?.capabilities, this.capabilityReport)) return;
    const expires = Math.min(Date.parse(this.transport.capabilities.expires_at), Date.parse(this.capabilityReport.expires_at));
    this.capabilityExpiryTimer = setTimeout(() => {
      // Publishing again runs the normal binding and expiry checks. In-flight
      // reads and bounded capacity retries cannot keep an expired report live.
      this.#set("connected");
    }, Math.max(1, expires - Date.now()));
    this.capabilityExpiryTimer.unref?.();
  }

  #scheduleReconnect() {
    if (this.descriptor?.transport !== "iscp" || !this.connection || this.suspended || this.authorizationRevoked || this.reconnectTimer) return;
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

  async #identity(candidate, descriptor = this.descriptor, transport = this.transport) {
    try {
      const timeoutMS = descriptor.transport === "iscp" ? (transport.timeoutMS || 30000) : 5000;
      if (descriptor.transport === "iscp") await transport.start();
      const response = await this.#fetch(descriptor, transport)(`${candidate.origin}/api/workbench/identity`, {
        headers: { ...(candidate.authorization ? { Authorization: candidate.authorization } : {}), Accept: "application/json" },
        redirect: "manual", signal: AbortSignal.timeout(timeoutMS),
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
        const binding = await this.#fetch(descriptor, transport)(`${candidate.origin}/api/v1/installations`, {
          method: "POST", headers: { ...(candidate.authorization ? { Authorization: candidate.authorization } : {}), "Content-Type": "application/json", Accept: "application/json" },
          body: JSON.stringify({ schema_version: 1, installation_id: this.installationID }),
          redirect: "manual", signal: AbortSignal.timeout(timeoutMS),
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
    this.status = Object.freeze({ schema_version: 1, state, ...(this.transport?.capabilities?.authorization_revision ? {authorization_revision:this.transport.capabilities.authorization_revision} : {}), ...(this.deletionIntent ? { authorization_deletion: { state: this.deletionIntent.state, operation_id: this.deletionIntent.operation_id } } : {}), ...(this.descriptor ? { backend: {
      schema_version: this.descriptor.schemaVersion || 1,
      origin: this.descriptor.origin, deployment_id: this.descriptor.deploymentID,
      ...(this.descriptor.ownerID ? { owner_id: this.descriptor.ownerID } : {}),
      ...(this.descriptor.transport === "iscp" ? { transport: "iscp", client_id: this.descriptor.clientID, domain_id: this.descriptor.domainID,
        initiator_device_id: this.descriptor.initiatorDeviceID, responder_device_id: this.descriptor.responderDeviceID,
        responder_key_thumbprint: this.descriptor.responderKeyThumbprint, relay_url: this.descriptor.relayURL, relay_profile: this.descriptor.relayProfile, test_mode: this.descriptor.testMode, ...(this.descriptor.mode ? { mode: this.descriptor.mode } : {}) } : {}),
      ...(this.descriptor.certificateSHA256 ? { tls_certificate_sha256: this.descriptor.certificateSHA256 } : {}),
    } } : {}), ...(this.descriptor?.transport === "iscp" ? { transport_stage: this.transportStage, test_mode: this.descriptor.testMode, ...(this.descriptor.mode ? { mode: this.descriptor.mode } : {}),
      capabilities: projectISCPCapabilities(this.transport?.capabilities, this.capabilityReport) } : {}), ...(this.connection && (this.connection.transport !== "iscp" || this.connection.identityVerified) ? { client_id: this.connection.clientID, owner_id: this.connection.ownerID } : {}) });
    this.#scheduleCapabilityExpiry();
    this.onChange(this.status);
    return this.status;
  }
}

function waitForStartupAdmission(signal, delay) {
  return new Promise((resolve, reject) => {
    const cancel = () => { clearTimeout(timer); signal.removeEventListener("abort", cancel); reject(new ISCPRequestNotSentError("ISCP startup read was canceled", "canceled")); };
    const timer = setTimeout(() => { signal.removeEventListener("abort", cancel); resolve(); }, delay);
    signal.addEventListener("abort", cancel, { once: true });
    if (signal.aborted) cancel();
  });
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
