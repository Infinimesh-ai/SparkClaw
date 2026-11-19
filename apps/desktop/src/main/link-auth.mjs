import crypto from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { projectISCPCapabilities, ISCP_SURFACES } from "./iscp-capabilities.mjs";
import { ISCPTransport, ISCPRequestNotSentError } from "./iscp-transport.mjs";
import { LinkDomainWire, linkManifest, V2_PROFILE } from "./link-domain-wire.mjs";
import { LinkError, LinkTransport, LINK_REVERSE_OPERATIONS } from "./link-transport.mjs";

export const LINK_CREDENTIAL_PREFIX = "sparkclaw-link-v1.";
// An internal origin: requests never leave the main process by HTTP, the
// path only selects the domain operation.
export const LINK_ORIGIN = "https://backend.sparkclaw-link.invalid";
const CODE = /^\d{12}$/u;

// The installation identity the Gateway derives for a link device
// (link_domain_bridge.go linkInstallationID), so reverse calls and domain
// state agree on one installation per device.
export function linkInstallationID(deviceID) {
  const sum = crypto.createHash("sha256").update(`sparkclaw-link-installation-v1\u0000${deviceID}`).digest();
  const b = Buffer.from(sum.subarray(0, 16));
  b[6] = (b[6] & 0x0f) | 0x40;
  b[8] = (b[8] & 0x3f) | 0x80;
  const hex = b.toString("hex");
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}

// Operations the core link layer serves in place of the old v2 table rows
// (approvals, execution inputs and files). They make the original surfaces
// available; they are never sent through domain.call.
const CORE_SUBSTITUTES = Object.freeze(["approvals.list", "approvals.get", "approvals.decide", "execution.input.put", "execution.file.get"]);

// Projects the existing capability surfaces for a link connection. Every
// feature is open unless the backend lacks the domain or the device lacks
// the condition (design 3.9); there is no per-feature qualification report.
export function linkCapabilities(manifest, browserRuntimeAvailable = true, now = Date.now()) {
  if (!manifest) return projectISCPCapabilities(undefined, undefined, now, browserRuntimeAvailable);
  const projected = { ...manifest, operations: [...new Set([...manifest.operations, ...CORE_SUBSTITUTES])] };
  const report = { schema_version: 2, profile: V2_PROFILE, session_id: manifest.session_id, authorization_revision: manifest.authorization_revision, expires_at: manifest.expires_at,
    capabilities: ISCP_SURFACES.map((id) => ({ id, qualified: true, enabled: true, permitted: true, supported: true, dependencies_ready: true })) };
  return projectISCPCapabilities(projected, report, now, browserRuntimeAvailable);
}

// LinkAuth is the desktop's backend connection on the link layer. It keeps
// the interface the main-process clients already use (status, connection,
// authorizedFetch, invokeISCP, transport) while every request travels over
// one LinkTransport: core operations directly, business domains through
// domain.call. A device joins once with a SparkClaw access credential.
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
    const hostID = summary.host_device_id || summary.domain_id;
    this.descriptor = Object.freeze({ schemaVersion: 3, transport: "link", origin: LINK_ORIGIN, deploymentID: hostID, ownerID: undefined,
      clientID: `link-${summary.device_id}`, deviceID: summary.device_id, domainID: summary.domain_id, domainName: summary.domain_name,
      hostName: summary.host_name || "", deviceName: summary.device_name || "", console: summary.console || "" });
    // The last verified owner lets cached conversations open while the
    // backend is offline; it is replaced by every device.describe.
    const known = this.#knownIdentity();
    if (known?.device_id === summary.device_id && typeof known.owner_id === "string" && known.owner_id) {
      this.descriptor = Object.freeze({ ...this.descriptor, ownerID: known.owner_id });
      this.connection = Object.freeze({ schema_version: 3, transport: "link", origin: LINK_ORIGIN, deploymentID: hostID, ownerID: known.owner_id,
        clientID: this.descriptor.clientID, identityVerified: true, binding: summary.device_id });
    } else this.connection = undefined;
    this.onJoined?.(this.installationID);
  }

  #identityFile() { return path.join(this.stateDirectory, "link-identity.json"); }
  #knownIdentity() { try { return JSON.parse(fs.readFileSync(this.#identityFile(), "utf8")); } catch { return undefined; } }
  #saveIdentity(ownerID) {
    try {
      const filename = this.#identityFile(), temporary = `${filename}.${crypto.randomUUID()}.tmp`;
      fs.writeFileSync(temporary, JSON.stringify({ device_id: this.descriptor.deviceID, owner_id: ownerID }), { mode: 0o600 });
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
    this.wire?.disconnect();
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
      const identity = { deploymentID: this.descriptor.deploymentID, ownerID: described.principal.owner_id, clientID: this.descriptor.clientID };
      const changed = this.connection?.ownerID !== identity.ownerID || this.connection?.clientID !== identity.clientID;
      this.connection = Object.freeze({ schema_version: 3, transport: "link", origin: LINK_ORIGIN, ...identity, identityVerified: true, binding: this.descriptor.deviceID });
      this.descriptor = Object.freeze({ ...this.descriptor, ownerID: identity.ownerID });
      if (changed) { this.#saveIdentity(identity.ownerID); await this.onIdentity(this.identity()); }
      await this.#startPipeline();
      if (!this.link.online) { this.#set("service_unavailable"); return; }
      this.generation++;
      this.authorizationRevoked = false;
      this.#set("connected");
    };
    this.connecting = run().finally(() => { this.connecting = undefined; });
    return this.connecting;
  }

  // The business-domain pipeline runs over a fresh wire per link connection.
  async #startPipeline() {
    this.transport?.close();
    const binding = { deployment_id: this.connection.deploymentID, owner_id: this.connection.ownerID, client_id: this.connection.clientID };
    const transport = new ISCPTransport({
      configPath: path.join(this.stateDirectory, "link-domain"), origin: LINK_ORIGIN, expectedBinding: binding,
      installationID: this.installationID, readLocalFile: this.readLocalFile, timeoutMS: this.timeoutMS,
      canSendMailAttachments: () => this.status.capabilities?.surfaces?.mail_send_attachments?.enabled === true,
      journalRoot: path.join(this.userDataDirectory, "link-objects"), mutationProtector: this.vault,
      spawnProcess: () => {
        const wire = new LinkDomainWire({ link: this.link, binding, domains: this.domains, installationID: this.installationID });
        this.wire = wire;
        return wire;
      },
      onCapabilities: () => { if (this.transport === transport && this.status.state === "connected") this.#set("connected", this.onCapabilitiesChanged); },
      onReverse: (request, session) => this.transport === transport && this.status.state === "connected" ? this.onReverse?.(request, session) : undefined,
      onState: (state) => {
        if (this.transport !== transport) return;
        this.transportStage = state;
        if (state === "disconnected" && this.status.state === "connected") { ++this.generation; this.#set("service_unavailable"); }
      },
    });
    this.transport = transport;
    await transport.start();
  }

  // Reverse calls from the backend reach the same handlers that answered the
  // previous helper's reverse_request frames.
  async #reverse(op, params) {
    if (!this.wire || this.status.state !== "connected") throw new LinkError("backend_unavailable", "The device link is not ready");
    return this.wire.reverse(op, params);
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

  async invokeISCP(operation, body, options = {}) {
    if (this.status.state !== "connected" || !this.transport) throw new Error("The backend is unreachable");
    const generation = this.generation;
    const value = await this.transport.invoke(operation, body, options);
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
      if (error instanceof ISCPRequestNotSentError) throw error;
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
