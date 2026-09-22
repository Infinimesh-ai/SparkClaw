import crypto from "node:crypto";
import fs from "node:fs/promises";
import path from "node:path";

const IPC = Object.freeze({
  config: "sparkclaw:managed-script-config",
  get: "sparkclaw:managed-script-get",
  set: "sparkclaw:managed-script-set",
  menuRegister: "sparkclaw:managed-script-menu-register",
  menuRun: "sparkclaw:managed-script-menu-run",
  status: "sparkclaw:managed-script-status",
});
const QUALIFICATION_SCRIPT_ID = "sparkclaw.qualification.managed-script";
const KEY = /^[A-Za-z0-9_.:-]{1,128}$/u;
const MAX_VALUE_BYTES = 16 << 10;

export class ManagedScriptHost {
  constructor({ browserSession, ipcMain, Menu, preloadPath, manifestPath, storagePath,
    qualification = false, qualificationOrigin = "" }) {
    this.browserSession = browserSession;
    this.ipcMain = ipcMain;
    this.Menu = Menu;
    this.preloadPath = preloadPath;
    this.manifestPath = manifestPath;
    this.storagePath = storagePath;
    this.qualification = qualification;
    this.qualificationOrigin = qualificationOrigin;
    this.registry = null;
    this.definitions = new Map();
    this.values = new Map();
    this.statuses = new Map();
    this.menus = new Map();
    this.persisting = Promise.resolve();
    this.preloadID = "";
    this.handlers = [];
  }

  async prepare() {
    const manifest = JSON.parse(await fs.readFile(this.manifestPath, "utf8"));
    if (manifest?.schema_version !== 1 || !Array.isArray(manifest.scripts)) {
      throw new Error("Desktop managed script manifest is invalid");
    }
    for (const script of manifest.scripts) {
      if (!validDefinition(script)) throw new Error("Desktop managed script definition is invalid");
      this.definitions.set(script.id, Object.freeze(script));
    }
    if (this.qualification) {
      this.definitions.set(QUALIFICATION_SCRIPT_ID, Object.freeze({
        id: QUALIFICATION_SCRIPT_ID,
        version: "1",
        sha256: "0".repeat(64),
        origins: [this.qualificationOrigin],
      }));
    }
    await this.#load();
    this.#installIPC();
    this.preloadID = this.browserSession.registerPreloadScript({ type: "frame", filePath: this.preloadPath });
  }

  setRegistry(registry) {
    if (this.registry) throw new Error("Managed script registry is already bound");
    this.registry = registry;
  }

  bind(record) {
    record.webContents.on("did-start-navigation", (_event, _url, _inPlace, isMainFrame) => {
      if (isMainFrame) this.#clearDocument(record.webContents.id);
    });
    record.webContents.on("context-menu", (event) => {
      if (record.role !== "personal") return;
      const commands = [...(this.menus.get(record.webContents.id)?.values() ?? [])];
      if (!commands.length) return;
      event.preventDefault();
      this.Menu.buildFromTemplate(commands.map((command) => ({
        label: command.label,
        click: () => this.#invoke(record.webContents, command.id),
      }))).popup();
    });
  }

  stateForUI() {
    return [...this.statuses.entries()].flatMap(([webContentsID, statuses]) => {
      const record = this.registry?.recordForWebContents(webContentsID);
      if (!record) return [];
      return [...statuses.values()].map((status) => ({ page_ref: record.pageRef, ...status }));
    });
  }

  qualificationSnapshot() {
    if (!this.qualification) throw new Error("Managed script qualification is unavailable");
    const values = this.values.get(QUALIFICATION_SCRIPT_ID) ?? new Map();
    return {
      statuses: this.stateForUI().filter((status) => status.script_id === QUALIFICATION_SCRIPT_ID),
      values: Object.fromEntries(values),
      menus: [...this.menus.entries()].flatMap(([webContentsID, commands]) => {
        const record = this.registry?.recordForWebContents(webContentsID);
        return record ? [...commands.values()].map((command) => ({
          page_ref: record.pageRef,
          command_id: command.id,
          label: command.label,
        })) : [];
      }),
    };
  }

  invokeQualification(pageRef, commandID) {
    if (!this.qualification || typeof commandID !== "string") throw new Error("Qualification command is invalid");
    const record = this.registry?.getByRef(pageRef);
    const command = this.menus.get(record?.webContents.id)?.get(commandID);
    if (!record || !command || command.scriptID !== QUALIFICATION_SCRIPT_ID) {
      throw new Error("Qualification command is unavailable");
    }
    this.#invoke(record.webContents, commandID);
  }

  async close() {
    if (this.preloadID) this.browserSession.unregisterPreloadScript(this.preloadID);
    for (const [channel, listener] of this.handlers) this.ipcMain.removeListener(channel, listener);
    this.handlers = [];
    await this.persisting;
  }

  #installIPC() {
    this.#on(IPC.config, (event) => {
      event.returnValue = this.#sender(event) ? {
        qualification_origin: this.qualification ? this.qualificationOrigin : "",
      } : { qualification_origin: "" };
    });
    this.#on(IPC.get, (event, request) => {
      event.returnValue = this.#storageRequest(event, request, false);
    });
    this.#on(IPC.set, (event, request) => {
      event.returnValue = this.#storageRequest(event, request, true);
    });
    this.#on(IPC.menuRegister, (event, request) => {
      event.returnValue = this.#registerMenu(event, request);
    });
    this.#on(IPC.status, (event, request) => this.#recordStatus(event, request));
  }

  #on(channel, listener) {
    this.ipcMain.on(channel, listener);
    this.handlers.push([channel, listener]);
  }

  #storageRequest(event, request, write) {
    const sender = this.#sender(event, request?.script_id);
    if (!sender || !KEY.test(request?.key ?? "")) return { ok: false };
    if (!write) {
      const values = this.values.get(request.script_id);
      return { ok: true, value: values?.has(request.key) ? values.get(request.key) : cloneValue(request.value) };
    }
    let value;
    try {
      value = cloneValue(request.value);
      if (Buffer.byteLength(JSON.stringify(value)) > MAX_VALUE_BYTES) return { ok: false };
    } catch {
      return { ok: false };
    }
    let values = this.values.get(request.script_id);
    if (!values) {
      values = new Map();
      this.values.set(request.script_id, values);
    }
    values.set(request.key, value);
    this.#schedulePersist();
    return { ok: true, value: undefined };
  }

  #registerMenu(event, request) {
    const sender = this.#sender(event, request?.script_id);
    if (!sender || typeof request?.label !== "string" || !request.label.trim() || request.label.length > 100) {
      return { ok: false };
    }
    const commandID = `menu_${crypto.randomUUID().replaceAll("-", "")}`;
    let commands = this.menus.get(sender.record.webContents.id);
    if (!commands) {
      commands = new Map();
      this.menus.set(sender.record.webContents.id, commands);
    }
    if (commands.size >= 32) return { ok: false };
    commands.set(commandID, {
      id: commandID,
      label: request.label.trim(),
      scriptID: request.script_id,
    });
    return { ok: true, command_id: commandID };
  }

  #recordStatus(event, request) {
    const sender = this.#sender(event, request?.script_id);
    const definition = sender?.definition;
    if (!sender || request?.version !== definition.version || request?.sha256 !== definition.sha256 ||
        !["ready", "failed"].includes(request?.state) ||
        !["main", "isolated", "qualification"].includes(request?.world) ||
        Object.keys(request).sort().join("\n") !== "script_id\nsha256\nstate\nversion\nworld") return;
    let statuses = this.statuses.get(sender.record.webContents.id);
    if (!statuses) {
      statuses = new Map();
      this.statuses.set(sender.record.webContents.id, statuses);
    }
    statuses.set(request.script_id, { ...request });
  }

  #sender(event, scriptID) {
    const record = this.registry?.recordForWebContents(event.sender.id);
    if (!record || record.destroyed || event.sender.session !== this.browserSession ||
        event.senderFrame !== event.sender.mainFrame) return null;
    if (!scriptID) return { record };
    const definition = this.definitions.get(scriptID);
    if (!definition) return null;
    let origin;
    try {
      origin = new URL(event.senderFrame.url).origin;
    } catch {
      return null;
    }
    return definition.origins.includes(origin) ? { record, definition } : null;
  }

  #clearDocument(webContentsID) {
    this.statuses.delete(webContentsID);
    this.menus.delete(webContentsID);
  }

  #invoke(webContents, commandID) {
    if (!webContents.isDestroyed()) webContents.send(IPC.menuRun, commandID);
  }

  async #load() {
    try {
      const raw = JSON.parse(await fs.readFile(this.storagePath, "utf8"));
      if (raw?.schema_version !== 1 || !raw.values || typeof raw.values !== "object") return;
      for (const [scriptID, values] of Object.entries(raw.values)) {
        if (!this.definitions.has(scriptID) || !values || typeof values !== "object" || Array.isArray(values)) continue;
        const admitted = new Map();
        for (const [key, value] of Object.entries(values)) {
          if (!KEY.test(key)) continue;
          try {
            const cloned = cloneValue(value);
            if (Buffer.byteLength(JSON.stringify(cloned)) <= MAX_VALUE_BYTES) admitted.set(key, cloned);
          } catch {}
        }
        this.values.set(scriptID, admitted);
      }
    } catch (error) {
      if (error?.code !== "ENOENT") throw error;
    }
  }

  #schedulePersist() {
    const snapshot = {
      schema_version: 1,
      values: Object.fromEntries([...this.values].map(([scriptID, values]) => [scriptID, Object.fromEntries(values)])),
    };
    this.persisting = this.persisting.then(async () => {
      const directory = path.dirname(this.storagePath);
      await fs.mkdir(directory, { recursive: true, mode: 0o700 });
      await fs.chmod(directory, 0o700);
      const temporary = `${this.storagePath}.${process.pid}.${crypto.randomUUID()}.tmp`;
      await fs.writeFile(temporary, `${JSON.stringify(snapshot)}\n`, { mode: 0o600, flag: "wx" });
      await fs.rename(temporary, this.storagePath);
    }).catch((error) => {
      process.stderr.write(`Managed script storage failed: ${error instanceof Error ? error.message : "unknown error"}\n`);
    });
  }
}

function validDefinition(value) {
  return value && typeof value === "object" && typeof value.id === "string" &&
    typeof value.version === "string" && /^[0-9a-f]{64}$/u.test(value.sha256) &&
    Array.isArray(value.origins) && value.origins.length > 0 && value.origins.every(validHTTPSOrigin);
}

function validHTTPSOrigin(value) {
  try {
    const url = new URL(value);
    return url.protocol === "https:" && url.origin === value && !url.username && !url.password && !url.port;
  } catch {
    return false;
  }
}

function cloneValue(value) {
  if (value === undefined) return undefined;
  return structuredClone(value);
}
