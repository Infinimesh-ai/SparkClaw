import crypto from "node:crypto";
import fs from "node:fs/promises";
import net from "node:net";
import path from "node:path";

import { RelayConnection } from "@sparkclaw/browser-bridge/relay-connection";

import { ElectronChromeAPI, ElectronDownloadRegistry } from "./electron-chrome-api.mjs";
import { publicTab } from "./page-registry.mjs";
import {
  ELECTRON_ADAPTER_PROTOCOL_VERSION,
  ELECTRON_ADAPTER_VERSION,
  MAX_ADAPTER_MESSAGE_BYTES,
  bindingEquals,
  parseOpenRequest,
  parsePersonalRequest,
  parsePlaywrightConnectionURL,
  parseRegisterRequest,
  secureEqual,
} from "./protocol.mjs";

const REGISTRATION_TTL_MS = 15_000;

export class ElectronAdapterServer {
  constructor({ socketPath, secretPath, downloadRoot, runtimeGeneration, registry, browserSession, scriptHost, clipboard, lifecycle,
    qualification = false }) {
    this.socketPath = socketPath;
    this.secretPath = secretPath;
    this.runtimeGeneration = runtimeGeneration;
    this.registry = registry;
    this.browserSession = browserSession;
    this.scriptHost = scriptHost;
    this.clipboard = clipboard;
    this.lifecycle = lifecycle;
    this.qualification = qualification;
    this.downloadRegistry = new ElectronDownloadRegistry(browserSession, registry, downloadRoot);
    this.registrations = new Map();
    this.relays = new Map();
    this.server = null;
    this.runtimeSecret = crypto.randomBytes(32).toString("base64url");
  }

  async start() {
    await this.downloadRegistry.prepare();
    const parent = path.dirname(this.socketPath);
    await fs.mkdir(parent, { recursive: true, mode: 0o700 });
    await fs.chmod(parent, 0o700);
    const parentStat = await fs.lstat(parent);
    if (!parentStat.isDirectory() || parentStat.isSymbolicLink() || parentStat.uid !== process.getuid()) {
      throw new Error("Electron adapter runtime directory is invalid");
    }
    await removeEndpoint(this.socketPath, "socket");
    await removeEndpoint(this.secretPath, "secret");
    await fs.writeFile(this.secretPath, `${this.runtimeSecret}\n`, { mode: 0o600, flag: "wx" });
    this.server = net.createServer((client) => this.#handleClient(client));
    this.server.on("error", (error) => {
      if (this.server?.listening) {
        process.stderr.write(`Electron adapter server failed: ${error instanceof Error ? error.message : "unknown error"}\n`);
      }
    });
    await new Promise((resolve, reject) => {
      const failed = (error) => reject(error);
      this.server.once("error", failed);
      this.server.listen(this.socketPath, () => {
        this.server.removeListener("error", failed);
        resolve();
      });
    });
    await fs.chmod(this.socketPath, 0o600);
    return this;
  }

  async close() {
    for (const relay of this.relays.values()) relay.close("Electron runtime stopped");
    this.relays.clear();
    this.registrations.clear();
    if (this.server) {
      await new Promise((resolve) => this.server.close(() => resolve()));
      this.server = null;
    }
    await fs.rm(this.socketPath, { force: true });
    await fs.rm(this.secretPath, { force: true });
    await this.downloadRegistry.close();
    this.runtimeSecret = "";
  }

  #handleClient(client) {
    client.setEncoding("utf8");
    let input = "";
    const timer = setTimeout(() => client.destroy(), 10_000);
    timer.unref?.();
    client.on("data", (chunk) => {
      input += chunk;
      if (Buffer.byteLength(input) > MAX_ADAPTER_MESSAGE_BYTES) return client.destroy();
      const newline = input.indexOf("\n");
      if (newline < 0) return;
      clearTimeout(timer);
      client.pause();
      let request;
      try {
        request = JSON.parse(input.slice(0, newline));
      } catch {
        return this.#respond(client, { schema_version: 1, state: "rejected" });
      }
      void this.#dispatch(request).then(
        (response) => this.#respond(client, response),
        (error) => {
          if (this.qualification) {
            process.stderr.write(`Electron adapter request failed: ${error instanceof Error ? error.message : "unknown error"}\n`);
          }
          this.#respond(client, { schema_version: 1, state: "rejected" });
        },
      );
    });
    client.on("error", () => clearTimeout(timer));
  }

  async #dispatch(request) {
    if (request?.schema_version === 1 && request.operation === "status" &&
        Object.keys(request).sort().join("\n") === "operation\nschema_version") {
      return {
        schema_version: 1,
        state: "ready",
        runtime_kind: "electron",
        adapter_version: ELECTRON_ADAPTER_VERSION,
        protocol_version: ELECTRON_ADAPTER_PROTOCOL_VERSION,
        runtime_generation: this.runtimeGeneration,
      };
    }
    if (request?.operation === "registerConnection") return this.#register(request);
    if (request?.operation === "openConnection") return await this.#open(request);
    if (request?.operation === "openPersonalPage") return await this.#openPersonal(request);
    if (request?.operation === "qualificationSnapshot") return this.#qualificationSnapshot(request);
    if (request?.operation === "qualificationShowTask") return this.#qualificationShowTask(request);
    if (request?.operation === "qualificationHideTask") return this.#qualificationHideTask(request);
    if (request?.operation === "qualificationSetClipboard") return this.#qualificationSetClipboard(request);
    if (request?.operation === "qualificationOpenDevTools") return await this.#qualificationOpenDevTools(request);
    if (request?.operation === "qualificationTriggerDialog") return await this.#qualificationTriggerDialog(request);
    if (request?.operation === "qualificationLifecycle") return await this.#qualificationLifecycle(request);
    if (request?.operation === "qualificationCrashTask") return this.#qualificationCrashTask(request);
    if (request?.operation === "qualificationManagedScripts") return this.#qualificationManagedScripts(request);
    if (request?.operation === "qualificationInvokeScriptMenu") return this.#qualificationInvokeScriptMenu(request);
    throw new Error("Electron adapter request is invalid");
  }

  #register(raw) {
    const request = parseRegisterRequest(raw);
    if (!secureEqual(request.runtimeSecret, this.runtimeSecret)) {
      throw new Error("Electron adapter authentication failed");
    }
    this.#expireRegistrations();
    const credential = crypto.randomBytes(32).toString("base64url");
    this.registrations.set(credential, {
      tokenHash: request.tokenHash,
      binding: request.binding,
      expiresAt: Date.now() + REGISTRATION_TTL_MS,
    });
    return {
      schema_version: 1,
      state: "registered",
      runtime_kind: "electron",
      protocol_version: ELECTRON_ADAPTER_PROTOCOL_VERSION,
      runtime_generation: this.runtimeGeneration,
      connection_credential: credential,
    };
  }

  async #open(raw) {
    const request = parseOpenRequest(raw);
    this.#expireRegistrations();
    const registration = this.registrations.get(request.credential);
    this.registrations.delete(request.credential);
    if (!registration || registration.expiresAt < Date.now() ||
        !bindingEquals(registration.binding, request.binding)) {
      throw new Error("Electron adapter credential is invalid");
    }
    const connectionInfo = parsePlaywrightConnectionURL(request.connectionURL);
    if (!secureEqual(connectionInfo.tokenHash, registration.tokenHash)) {
      throw new Error("Electron adapter token is invalid");
    }

    const connectionID = crypto.randomUUID();
    const connection = { id: connectionID, binding: request.binding, clientName: connectionInfo.clientName };
    this.registry.registerConnection(connection);
    let initialRecord;
    let socket;
    try {
      await this.downloadRegistry.registerConnection(connectionID);
      initialRecord = await this.registry.createInitialTaskPage(connectionID);
      socket = await openWebSocket(connectionInfo.relayURL);
      const chromeAPI = new ElectronChromeAPI({
        registry: this.registry,
        connectionID,
        downloadRegistry: this.downloadRegistry,
      });
      this.registry.bindConnectionFacade(connectionID, chromeAPI);
      const relay = new RelayConnection({
        webSocket: socket,
        chromeAPI,
        initialTab: publicTab(initialRecord),
        taskWindowID: 1,
        allowTaskHandoff: false,
      });
      relay.onclose = () => {
        this.relays.delete(connectionID);
        this.registry.closeConnection(connectionID, "relay_closed");
      };
      this.relays.set(connectionID, relay);
      relay.attachInitialTab(publicTab(initialRecord));
      relay.didInitialize();
      return {
        schema_version: 1,
        state: "opened",
        runtime_kind: "electron",
        adapter_version: ELECTRON_ADAPTER_VERSION,
        protocol_version: ELECTRON_ADAPTER_PROTOCOL_VERSION,
        runtime_generation: this.runtimeGeneration,
        page_ref: initialRecord.pageRef,
      };
    } catch (error) {
      socket?.close();
      this.registry.closeConnection(connectionID, "connection_failed");
      this.downloadRegistry.releaseConnection(connectionID);
      throw error;
    }
  }

  async #openPersonal(raw) {
    const request = parsePersonalRequest(raw, { allowLoopbackHTTP: this.qualification });
    if (!secureEqual(request.runtimeSecret, this.runtimeSecret)) {
      throw new Error("Electron adapter authentication failed");
    }
    const record = await this.registry.createPersonalPage(request.url);
    return {
      schema_version: 1,
      state: "opened",
      runtime_kind: "electron",
      runtime_generation: this.runtimeGeneration,
      page_ref: record.pageRef,
    };
  }

  #qualificationSnapshot(request) {
    this.#authenticateQualification(request, ["operation", "runtime_secret", "schema_version"]);
    return {
      schema_version: 1,
      state: "completed",
      runtime_kind: "electron",
      runtime_generation: this.runtimeGeneration,
      pages: this.registry.snapshot(),
    };
  }

  #qualificationShowTask(request) {
    this.#authenticateQualification(request, ["operation", "page_ref", "runtime_secret", "schema_version"]);
    if (typeof request.page_ref !== "string" || request.page_ref.length > 128) {
      throw new Error("Qualification page reference is invalid");
    }
    this.registry.showQualified(request.page_ref);
    return {
      schema_version: 1,
      state: "completed",
      runtime_kind: "electron",
      runtime_generation: this.runtimeGeneration,
      page_ref: request.page_ref,
    };
  }

  #qualificationHideTask(request) {
    this.#authenticateQualification(request, ["operation", "runtime_secret", "schema_version"]);
    this.registry.hideObservedTask();
    return this.#qualificationCompleted();
  }

  #qualificationSetClipboard(request) {
    this.#authenticateQualification(request, ["operation", "runtime_secret", "schema_version", "text"]);
    if (typeof request.text !== "string" || request.text.length > 1024) throw new Error("Qualification clipboard is invalid");
    this.clipboard.writeText(request.text);
    return this.#qualificationCompleted();
  }

  async #qualificationOpenDevTools(request) {
    this.#authenticateQualification(request, ["operation", "page_ref", "runtime_secret", "schema_version"]);
    if (typeof request.page_ref !== "string" || request.page_ref.length > 128) {
      throw new Error("Qualification page reference is invalid");
    }
    const opened = await this.registry.openQualifiedDevTools(request.page_ref);
    return { ...this.#qualificationCompleted(), opened };
  }

  async #qualificationTriggerDialog(request) {
    this.#authenticateQualification(request, ["operation", "page_ref", "runtime_secret", "schema_version"]);
    if (typeof request.page_ref !== "string" || request.page_ref.length > 128) {
      throw new Error("Qualification page reference is invalid");
    }
    await this.registry.triggerQualifiedDialog(request.page_ref);
    return this.#qualificationCompleted();
  }

  async #qualificationLifecycle(request) {
    this.#authenticateQualification(request, ["action", "operation", "runtime_secret", "schema_version"]);
    if (!["status", "close", "show", "crash_workbench", "flush_browser_state"].includes(request.action)) {
      throw new Error("Qualification lifecycle action is invalid");
    }
    return {
      ...this.#qualificationCompleted(),
      lifecycle: await this.lifecycle(request.action),
    };
  }

  #qualificationCrashTask(request) {
    this.#authenticateQualification(request, ["operation", "page_ref", "runtime_secret", "schema_version"]);
    if (typeof request.page_ref !== "string" || request.page_ref.length > 128) {
      throw new Error("Qualification page reference is invalid");
    }
    this.registry.crashQualifiedPage(request.page_ref);
    return this.#qualificationCompleted();
  }

  #qualificationCompleted() {
    return {
      schema_version: 1,
      state: "completed",
      runtime_kind: "electron",
      runtime_generation: this.runtimeGeneration,
    };
  }

  #qualificationManagedScripts(request) {
    this.#authenticateQualification(request, ["operation", "runtime_secret", "schema_version"]);
    return {
      schema_version: 1,
      state: "completed",
      runtime_kind: "electron",
      runtime_generation: this.runtimeGeneration,
      managed_scripts: this.scriptHost.qualificationSnapshot(),
    };
  }

  #qualificationInvokeScriptMenu(request) {
    this.#authenticateQualification(request, [
      "command_id", "operation", "page_ref", "runtime_secret", "schema_version",
    ]);
    if (typeof request.page_ref !== "string" || request.page_ref.length > 128 ||
        typeof request.command_id !== "string" || request.command_id.length > 128) {
      throw new Error("Qualification script command is invalid");
    }
    this.scriptHost.invokeQualification(request.page_ref, request.command_id);
    return {
      schema_version: 1,
      state: "completed",
      runtime_kind: "electron",
      runtime_generation: this.runtimeGeneration,
    };
  }

  #authenticateQualification(request, keys) {
    if (!this.qualification || !request || typeof request !== "object" || Array.isArray(request) ||
        Object.keys(request).sort().join("\n") !== [...keys].sort().join("\n") ||
        request.schema_version !== 1 || !secureEqual(request.runtime_secret, this.runtimeSecret)) {
      throw new Error("Qualification request is invalid");
    }
  }

  #expireRegistrations() {
    const now = Date.now();
    for (const [credential, registration] of this.registrations) {
      if (registration.expiresAt < now) this.registrations.delete(credential);
    }
  }

  #respond(client, response) {
    if (!client.destroyed) client.end(`${JSON.stringify(response)}\n`);
  }
}

async function openWebSocket(url) {
  return await new Promise((resolve, reject) => {
    const socket = new WebSocket(url);
    const timer = setTimeout(() => {
      socket.close();
      reject(new Error("Playwright relay connection timed out"));
    }, 6000);
    timer.unref?.();
    socket.addEventListener("open", () => {
      clearTimeout(timer);
      resolve(socket);
    }, { once: true });
    socket.addEventListener("error", () => {
      clearTimeout(timer);
      reject(new Error("Playwright relay connection failed"));
    }, { once: true });
  });
}

async function removeEndpoint(endpoint, kind) {
  try {
    const stat = await fs.lstat(endpoint);
    if (stat.isSymbolicLink() || stat.uid !== process.getuid() ||
        (kind === "socket" ? !stat.isSocket() : !stat.isFile())) {
      throw new Error(`Electron adapter ${kind} endpoint is invalid`);
    }
    await fs.rm(endpoint);
  } catch (error) {
    if (error?.code !== "ENOENT") throw error;
  }
}
