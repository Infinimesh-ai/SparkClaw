import crypto from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { projectLinkCapabilities } from "./link-capabilities.mjs";
import { LinkDomainClient, LinkRequestNotSentError, DOMAIN_PROFILE } from "./link-domain-client.mjs";
import { LinkError, LinkTransport, LINK_REVERSE_OPERATIONS } from "./link-transport.mjs";
import { CONTROLLER_OPERATIONS, wireBytes } from "../browser/controller-wire.mjs";
import { MCD_HOST_OPERATIONS } from "./mcd/host.mjs";

// Reverse replies stay within one link frame.
const REVERSE_RESPONSE_BYTES = 480 * 1024;

export const LINK_CREDENTIAL_PREFIX = "sparkclaw-link-v1.";
// An internal origin: requests never leave the main process by HTTP, the
// path only selects the domain operation.
export const LINK_ORIGIN = "https://backend.sparkclaw-link.invalid";
const CODE = /^\d{12}$/u;

// Keep the UUID derivation in sync with the Gateway's linkInstallationID.
// Reverse calls and domain state use this installation ID for the same device.
export function linkInstallationID(deviceID) {
  const sum = crypto.createHash("sha256").update(`sparkclaw-link-installation-v1\u0000${deviceID}`).digest();
  const b = Buffer.from(sum.subarray(0, 16));
  b[6] = (b[6] & 0x0f) | 0x40;
  b[8] = (b[8] & 0x3f) | 0x80;
  const hex = b.toString("hex");
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}

// Projects the existing capability surfaces for a link connection.
export function linkCapabilities(manifest, browserRuntimeAvailable = true) {
  return projectLinkCapabilities(manifest, browserRuntimeAvailable);
}

// LinkAuth is the desktop's backend connection on the link layer. It keeps
// the interface the main-process clients already use (status, connection,
// authorizedFetch, invokeDomain, transport) while every request travels over
// one LinkTransport: core operations directly, business domains with
// domain.call through LinkDomainClient. A device joins once with a
// SparkClaw access credential.
export class LinkAuth {
  constructor({ stateDirectory, userDataDirectory, vault, readLocalFile, appVersion = "", hostName = os.hostname(), packaged = false, resourcesPath,
    linkFactory = (options) => new LinkTransport(options), onChange = () => {}, onCapabilitiesChanged = onChange, onLock = () => {}, onReverse,
    onIdentity = () => {}, onJoined, browserRuntimeAvailable = false, timeoutMS = 30000 }) {
    Object.assign(this, { stateDirectory, userDataDirectory, vault, defaultVault: vault, readLocalFile, appVersion, hostName, onChange, onCapabilitiesChanged, onLock, onReverse, onIdentity, onJoined, browserRuntimeAvailable, timeoutMS });
    this.descriptorPath = path.join(userDataDirectory, "backend.json");
    this.generation = 0;
    this.authorizationRevoked = false;
    this.transportStage = "closed";
    this.status = Object.freeze({ schema_version: 1, state: "incomplete_setup" });
    this.link = linkFactory({ stateDirectory, packaged, resourcesPath, describe: this.#describe(), reverse: LINK_REVERSE_OPERATIONS,
      onReverse: (op, params) => this.#reverse(op, params), requestTimeoutMS: timeoutMS });
    this.link.on("state", ({ path: linkPath }) => this.#linkState(linkPath));
    this.link.on("event", (frame) => this.#event(frame));
  }

  #describe() {
    return { class: "desktop", name: this.hostName, app: `SparkX ${this.appVersion}`.trim(),
      conditions: { browser_surface: true, attachments: true, microphone: true, screen: "large" } };
  }

  setBrowserRuntimeAvailable(available) {
    if (typeof available !== "boolean") throw new Error("Browser runtime availability must be boolean");
    if (this.browserRuntimeAvailable === available) return;
    this.browserRuntimeAvailable = available;
    if (this.descriptor) this.#set(this.status.state, this.onCapabilitiesChanged);
  }

  async initialize() {
    let summary;
    try { summary = await this.link.status(); }
    catch { return this.#set("service_unavailable"); }
    if (!summary?.joined) return this.#set("incomplete_setup");
    this.#adopt(summary);
    // The window opens with the cached conversations; the link connects
    // in the background (design 3.12: cached conversations stay readable).
    this.#set("service_unavailable");
    void this.#open().catch(() => {});
    return this.status;
  }

  // The existing connection entry accepts a SparkClaw access credential.
  async enroll(value) {
    const credential = typeof value === "string" ? value.trim() : "";
    if (!credential.startsWith(LINK_CREDENTIAL_PREFIX) && !CODE.test(credential)) throw new Error("Enter a SparkClaw access credential issued by the console");
    if (this.joining) throw new Error("Joining is already in progress");
    this.joining = true;
    try {
      const previous = this.summary;
      if (previous?.joined) { await this.#lock(); await this.link.leave().catch(() => this.link.forget()); }
      const summary = await this.link.join(credential, this.hostName);
      if (!summary?.joined) throw new Error("The console did not accept this credential");
      this.authorizationRevoked = false;
      this.#adopt(summary);
      await this.#open();
      return this.status;
    } finally { this.joining = false; }
  }

  #adopt(summary) {
    this.summary = summary;
    this.installationID = linkInstallationID(summary.device_id);
    // The backend's deployment id (from device.describe, remembered for
    // offline starts) scopes the local cache; the host device id stands
    // in until the first description.
    const known = this.#knownIdentity();
    const hostID = (known?.device_id === summary.device_id && known.deployment_id) || summary.host_device_id || summary.domain_id;
    this.descriptor = Object.freeze({ schemaVersion: 3, transport: "link", origin: LINK_ORIGIN, deploymentID: hostID, ownerID: undefined,
      clientID: `link-${summary.device_id}`, deviceID: summary.device_id, domainID: summary.domain_id, domainName: summary.domain_name,
      hostName: summary.host_name || "", deviceName: summary.device_name || "", console: summary.console || "" });
    // The last verified owner lets cached conversations open while the
    // backend is offline; it is replaced by every device.describe.
    if (known?.device_id === summary.device_id && typeof known.owner_id === "string" && known.owner_id) {
      this.descriptor = Object.freeze({ ...this.descriptor, ownerID: known.owner_id });
      this.connection = Object.freeze({ schema_version: 3, transport: "link", origin: LINK_ORIGIN, deploymentID: hostID, ownerID: known.owner_id,
        clientID: this.descriptor.clientID, identityVerified: true, binding: summary.device_id });
    } else this.connection = undefined;
    this.onJoined?.(this.installationID);
  }

  #identityFile() { return path.join(this.stateDirectory, "link-identity.json"); }
  #knownIdentity() { try { return JSON.parse(fs.readFileSync(this.#identityFile(), "utf8")); } catch { return undefined; } }
  #saveIdentity(ownerID, deploymentID) {
    try {
      const filename = this.#identityFile(), temporary = `${filename}.${crypto.randomUUID()}.tmp`;
      fs.writeFileSync(temporary, JSON.stringify({ device_id: this.descriptor.deviceID, owner_id: ownerID, ...(deploymentID ? { deployment_id: deploymentID } : {}) }), { mode: 0o600 });
      fs.renameSync(temporary, filename);
    } catch { /* the identity is re-verified on the next connection */ }
  }

  async #open() {
    const generation = ++this.generation;
    this.transportStage = "connecting";
    this.#set(this.link.online ? "reconnecting" : "service_unavailable");
    try { await this.link.open(this.#describe()); }
    catch (error) {
      if (generation !== this.generation) return;
      if (["unauthorized", "revoked"].includes(error?.code)) { this.authorizationRevoked = true; this.#set("invalid_authentication"); return; }
      this.#set("service_unavailable");
      this.#scheduleOpen();
      return;
    }
    if (this.link.online) await this.#connect(generation);
  }

  #scheduleOpen() {
    if (this.openTimer || this.suspended || this.authorizationRevoked) return;
    this.openAttempt = (this.openAttempt || 0) + 1;
    this.openTimer = setTimeout(() => { this.openTimer = undefined; void this.#open().catch(() => {}); }, Math.min(30000, 1000 * 2 ** Math.min(this.openAttempt, 5)));
    this.openTimer.unref?.();
  }

  #linkState(linkPath) {
    if (!this.descriptor || this.suspended) return;
    if (linkPath) {
      this.openAttempt = 0;
      if (this.status.state !== "connected" && !this.connecting) void this.#connect(this.generation);
      return;
    }
    // Backend offline: operations fail at once (design 3.12). Cached
    // conversations stay readable because the identity is retained.
    if (this.status.state === "connected" || this.status.state === "reconnecting") { ++this.generation; this.#set("service_unavailable"); }
  }

  async #connect(generation) {
    if (this.connecting) return this.connecting;
    const run = async () => {
      if (!this.link.online) return;
      this.#set("reconnecting");
      let described;
      try { described = await this.link.request("device.describe", {}); }
      catch (error) {
        if (["unauthorized", "revoked"].includes(error?.code)) { this.authorizationRevoked = true; this.#set("invalid_authentication"); }
        else this.#set("service_unavailable");
        return;
      }
      if (generation !== this.generation && this.status.state === "connected") return;
      this.principal = described.principal;
      this.features = described.features || [];
      this.domains = described.domains || [];
      const deploymentID = typeof described.deployment_id === "string" && described.deployment_id ? described.deployment_id : this.descriptor.deploymentID;
      const identity = { deploymentID, ownerID: described.principal.owner_id, clientID: this.descriptor.clientID };
      const changed = this.connection?.ownerID !== identity.ownerID || this.connection?.clientID !== identity.clientID || this.connection?.deploymentID !== identity.deploymentID;
      this.connection = Object.freeze({ schema_version: 3, transport: "link", origin: LINK_ORIGIN, ...identity, identityVerified: true, binding: this.descriptor.deviceID });
      this.descriptor = Object.freeze({ ...this.descriptor, ownerID: identity.ownerID, deploymentID });
      if (changed) { this.#saveIdentity(identity.ownerID, deploymentID); await this.onIdentity(this.identity()); }
      await this.#startPipeline();
      if (!this.link.online) { this.#set("service_unavailable"); return; }
      this.generation++;
      this.authorizationRevoked = false;
      this.#set("connected");
    };
    this.connecting = run().finally(() => { this.connecting = undefined; });
    return this.connecting;
  }

  // Business domains are reached with domain.call, one client per link
  // connection so a change of backend identity never mixes their state.
  async #startPipeline() {
    this.transport?.close();
    const binding = { deployment_id: this.connection.deploymentID, owner_id: this.connection.ownerID, client_id: this.connection.clientID };
    const transport = new LinkDomainClient({
      link: this.link, origin: LINK_ORIGIN, binding, installationID: this.installationID, domains: this.domains,
      readLocalFile: this.readLocalFile, timeoutMS: this.timeoutMS,
      canSendMailAttachments: () => this.status.capabilities?.surfaces?.mail_send_attachments?.enabled === true,
      journalRoot: path.join(this.userDataDirectory, "link-objects"), mutationProtector: this.vault,
    });
    this.transport = transport;
    this.transportStage = "transport_ready";
    await transport.start();
  }

  // Reverse calls from the backend reach the original browser and
  // McDonald's handlers with the request shape they take.
  async #reverse(op, params) {
    const transport = this.transport;
    if (!transport || this.status.state !== "connected" || transport.state !== "transport_ready") throw new LinkError("backend_unavailable", "The device link is not ready");
    if (!CONTROLLER_OPERATIONS.includes(op) && !MCD_HOST_OPERATIONS.includes(op)) return { status: 403, error_code: "permission_denied", error: "Reverse operation is not accepted" };
    const envelope = params && typeof params === "object" && !Array.isArray(params) && Object.hasOwn(params, "body") ? params : { body: params ?? {} };
    const request = { type: "task.invoke", profile: DOMAIN_PROFILE, id: crypto.randomUUID(), operation: op,
      installation_id: envelope.installation_id || this.installationID,
      ...(envelope.params ? { params: envelope.params } : {}), ...(envelope.request_id ? { request_id: envelope.request_id } : {}),
      ...(envelope.operation_id ? { operation_id: envelope.operation_id } : {}), body: envelope.body };
    const session = { session_id: transport.sessionID, authorization_revision: 1 };
    const chunk = op === "browser.controller.chunk";
    if (chunk && (this.reverseChunks || 0) >= 4) return { status: 429, error_code: "throttled", error: "Browser result capacity is unavailable" };
    let reply;
    try {
      if (chunk) this.reverseChunks = (this.reverseChunks || 0) + 1;
      reply = await this.onReverse?.(request, session);
    } catch { reply = { status: 500, error_code: "internal_error", error: "Browser command failed" }; }
    finally { if (chunk) this.reverseChunks--; }
    reply ??= { status: 503, error_code: "capability_unavailable", error: "Browser is unavailable" };
    const response = { status: reply.status, ...(reply.body !== undefined ? { body: reply.body } : {}),
      ...(reply.error_code ? { error_code: reply.error_code } : {}), ...(reply.error ? { error: String(reply.error) } : {}) };
    if (wireBytes(response) > REVERSE_RESPONSE_BYTES) return { status: 413, error_code: "resource_limit", error: "Browser response exceeds the transfer limit" };
    return response;
  }

  #event(frame) {
    if (frame.topic === "device" && frame.data?.state === "revoked") {
      this.authorizationRevoked = true;
      ++this.generation;
      this.transport?.close();
      void this.#lock();
      this.#set("invalid_authentication");
    }
  }

  identity() {
    const c = this.connection;
    return c ? { deployment_id: c.deploymentID, owner_id: c.ownerID, client_id: c.clientID } : null;
  }

  retry() {
    if (!this.descriptor) return Promise.resolve(this.#set("incomplete_setup"));
    if (this.authorizationRevoked) return Promise.resolve(this.#set("invalid_authentication"));
    this.suspended = false;
    clearTimeout(this.openTimer); this.openTimer = undefined;
    if (!this.link.opened) return this.#open().then(() => this.status);
    if (this.link.online) return this.#connect(this.generation).then(() => this.status);
    return Promise.resolve(this.#set("service_unavailable"));
  }

  async invokeDomain(operation, body, options = {}) {
    if (this.status.state !== "connected" || !this.transport) throw new Error("The backend is unreachable");
    const generation = this.generation;
    const value = await this.transport.invoke(operation, body, options);
    if (generation !== this.generation) throw new Error("Backend connection changed");
    return value;
  }

  // The original outcome of a change whose reply was lost; never runs it.
  async replayDomain(operation, body, options = {}) {
    if (this.status.state !== "connected" || !this.transport) throw new Error("The backend is unreachable");
    const generation = this.generation;
    const value = await this.transport.replay(operation, body, options);
    if (generation !== this.generation) throw new Error("Backend connection changed");
    return value;
  }

  authorizedFetch(raw, init = {}) { return this.#fetch(raw, init, 180000); }

  authorizedMailFileFetch(raw, init = {}) {
    const url = new URL(raw);
    const cached = /^\/api\/v1\/mail\/[A-Za-z0-9_.:-]+\/messages\/[A-Za-z0-9_.:-]+\/attachments\/[A-Za-z0-9_.:-]+$/u.test(url.pathname) && !url.search;
    const original = /^\/api\/email\/messages\/[A-Za-z0-9][A-Za-z0-9_.:-]{0,159}\/file$/u.test(url.pathname) && [...url.searchParams].every(([key, value]) => key === "part_id" && /^[A-Za-z0-9][A-Za-z0-9_.:-]{0,159}$/u.test(value)) && [...url.searchParams].length <= 1;
    if (!(cached || original) || url.hash || url.username || url.password || (init.method && init.method !== "GET")) throw new Error("Mail attachment path is invalid");
    return this.#fetch(raw, init, 180000);
  }

  async #fetch(raw, init, timeoutMS) {
    if (this.status.state !== "connected" || !this.transport) return new Response(null, { status: 503 });
    const url = new URL(raw);
    if (url.origin !== LINK_ORIGIN) throw new Error("Backend origin is invalid");
    const generation = this.generation;
    const signal = AbortSignal.any([AbortSignal.timeout(timeoutMS), ...(init.signal ? [init.signal] : [])]);
    try {
      const response = await this.transport.fetch(raw, { ...init, signal });
      if (generation !== this.generation) return new Response(null, { status: 503 });
      return response;
    } catch (error) {
      if (error instanceof LinkRequestNotSentError) throw error;
      throw new Error("Backend request is unavailable");
    }
  }

  // The link layer has no separate sign-in; a joined device is unlocked.
  login() { return this.retry(); }
  configure() { throw new Error("This desktop joins its backend with a SparkClaw access credential"); }
  connectionCredential() { throw new Error("Access credentials are issued on the SparkClaw console"); }
  deleteAuthorization() { throw new Error("Devices are removed on the SparkClaw console"); }
  checkNewAuthorization() { return this.retry(); }

  // Leaving clears this device's membership; it must join again.
  async logout() {
    await this.#lock();
    ++this.generation;
    this.transport?.close(); this.transport = undefined;
    await this.link.leave().catch(() => this.link.forget().catch(() => {}));
    this.descriptor = undefined; this.connection = undefined; this.summary = undefined; this.authorizationRevoked = false;
    fs.rmSync(this.#identityFile(), { force: true });
    return this.#set("incomplete_setup");
  }

  async #lock() { await Promise.resolve(this.onLock()).catch(() => {}); }

  // The computer's network changed or it woke up: the link picks its path
  // again (LAN first, otherwise the ISCP Relay).
  networkChanged() { this.link.networkChanged?.(); }

  suspend() {
    this.suspended = true;
    ++this.generation;
    clearTimeout(this.openTimer); this.openTimer = undefined;
    this.transport?.close();
    if (this.descriptor) this.#set("service_unavailable");
  }

  close() {
    this.suspended = true;
    clearTimeout(this.openTimer);
    this.transport?.close(); this.transport = undefined;
    this.link.close();
  }

  #set(state, notify = this.onChange) {
    const d = this.descriptor;
    const manifest = state === "connected" ? this.transport?.capabilities : undefined;
    const capabilities = d ? linkCapabilities(manifest, this.browserRuntimeAvailable) : undefined;
    this.status = Object.freeze({ schema_version: 1, state,
      ...(d ? { authorization_revision: 1, transport_stage: this.authorizationRevoked ? "authorization_revoked" : this.transportStage, capabilities,
        backend: { schema_version: 3, transport: "link", origin: d.origin, deployment_id: d.deploymentID, ...(d.ownerID ? { owner_id: d.ownerID } : {}),
          client_id: d.clientID, domain_id: d.domainID, domain_name: d.domainName, host_name: d.hostName, device_name: d.deviceName } } : {}),
      ...(this.connection ? { client_id: this.connection.clientID, owner_id: this.connection.ownerID } : {}) });
    notify(this.status);
    return this.status;
  }
}

export { LinkError };
