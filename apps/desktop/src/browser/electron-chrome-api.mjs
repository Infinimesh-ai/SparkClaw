import fs from "node:fs/promises";
import path from "node:path";

import { publicTab } from "./page-registry.mjs";

export class ElectronChromeAPI {
  constructor({ registry, connectionID, downloadRegistry }) {
    this.registry = registry;
    this.connectionID = connectionID;
    this.downloadRegistry = downloadRegistry;
    this.debugger = {
      attach: (target, version) => this.#attach(target, version),
      detach: (target) => this.#detach(target),
      sendCommand: (target, method, params) => this.#sendCommand(target, method, params),
      onEvent: new ChromeEvent(),
      onDetach: new ChromeEvent(),
    };
    this.tabs = {
      create: (options) => this.#createTab(options),
      remove: (ids) => this.#removeTabs(ids),
      update: (tabID) => Promise.resolve(this.registry.publicTab(this.connectionID, tabID)),
      get: (tabID) => Promise.resolve(this.registry.publicTab(this.connectionID, tabID)),
      onCreated: new ChromeEvent(),
      onRemoved: new ChromeEvent(),
    };
    this.downloads = {
      onCreated: downloadRegistry.eventFor(connectionID),
      search: (query) => downloadRegistry.search(connectionID, query),
      cancel: (id) => downloadRegistry.cancel(connectionID, id),
      removeFile: (id) => downloadRegistry.removeFile(connectionID, id),
      erase: (query) => downloadRegistry.erase(connectionID, query),
    };
  }

  dispose() {
    this.downloadRegistry.releaseConnection(this.connectionID);
  }

  notifyDebuggerDetach(record, reason) {
    if (!record.attached) return;
    record.attached = false;
    record.childSessions.clear();
    record.childTargetTypes.clear();
    this.debugger.onDetach.emit({ tabId: record.tabID }, reason);
  }

  async #createTab(options) {
    if (!plainObject(options) || (options.url !== undefined && typeof options.url !== "string") ||
        options.active !== false || options.pinned !== false || options.windowId !== 1) {
      throw new Error("Invalid task tab creation");
    }
    const record = this.registry.createTaskPage(this.connectionID, options.url || "about:blank");
    return publicTab(record);
  }

  async #removeTabs(candidate) {
    const ids = Array.isArray(candidate) ? candidate : [candidate];
    if (!ids.length) throw new Error("Tab is outside the task allowlist");
    for (const tabID of ids) this.registry.get(this.connectionID, tabID);
    for (const tabID of ids) this.registry.destroy(tabID, "canceled_by_user");
  }

  async #attach(target, version) {
    const record = this.#record(target);
    if (target.sessionId !== undefined || version !== "1.3" || record.attached) {
      throw new Error("Task debugger attachment is invalid");
    }
    record.webContents.debugger.attach(version);
    record.attached = true;
    record.debuggerMessage = (_event, method, params, sessionID) => {
      if (method === "Target.attachedToTarget" && typeof params?.sessionId === "string") {
        record.childSessions.add(params.sessionId);
        if (typeof params.targetInfo?.type === "string") record.childTargetTypes.add(params.targetInfo.type);
      } else if (method === "Target.detachedFromTarget" && typeof params?.sessionId === "string") {
        record.childSessions.delete(params.sessionId);
      } else if (method === "Page.javascriptDialogOpening") {
        const dismissal = sessionID
          ? record.webContents.debugger.sendCommand("Page.handleJavaScriptDialog", { accept: false }, sessionID)
          : record.webContents.debugger.sendCommand("Page.handleJavaScriptDialog", { accept: false });
        void dismissal
          .then(() => { record.dialogDismissals++; }, (error) => {
            record.dialogError = error instanceof Error ? error.message : "unknown error";
          });
      }
      this.debugger.onEvent.emit(
        { tabId: record.tabID, ...(sessionID ? { sessionId: sessionID } : {}) },
        method,
        params ?? {},
      );
    };
    record.debuggerDetach = (_event, reason) => {
      this.#removeDebuggerListeners(record);
      this.notifyDebuggerDetach(record, reason || "target_closed");
    };
    record.webContents.debugger.on("message", record.debuggerMessage);
    record.webContents.debugger.on("detach", record.debuggerDetach);
    await record.webContents.debugger.sendCommand("Page.enable");
    return {};
  }

  async #detach(target) {
    const record = this.#record(target);
    if (target.sessionId !== undefined || !record.attached) throw new Error("Electron task debugger is not attached");
    record.webContents.debugger.detach();
    this.#removeDebuggerListeners(record);
    this.notifyDebuggerDetach(record, "canceled_by_user");
    return {};
  }

  async #sendCommand(target, method, params = {}) {
    const record = this.#record(target);
    const commandParams = params == null ? {} : params;
    if (!record.attached) {
      throw new Error(`Electron task debugger is not attached (${record.tabID}:${method})`);
    }
    if (typeof method !== "string" || !plainObject(commandParams)) {
      throw new Error("Electron task debugger command is invalid");
    }
    const sessionID = target.sessionId;
    if (sessionID !== undefined && (!record.childSessions.has(sessionID) || typeof sessionID !== "string")) {
      throw new Error("Nested debugger session is outside the task");
    }
    return await record.webContents.debugger.sendCommand(method, commandParams, sessionID);
  }

  #record(target) {
    if (!plainObject(target) || !Number.isInteger(target.tabId) ||
        Object.keys(target).some((key) => !["tabId", "sessionId"].includes(key))) {
      throw new Error("Task debugger target is invalid");
    }
    return this.registry.get(this.connectionID, target.tabId);
  }

  #removeDebuggerListeners(record) {
    if (record.debuggerMessage) record.webContents.debugger.removeListener("message", record.debuggerMessage);
    if (record.debuggerDetach) record.webContents.debugger.removeListener("detach", record.debuggerDetach);
    record.debuggerMessage = null;
    record.debuggerDetach = null;
  }
}

export class ElectronDownloadRegistry {
  constructor(browserSession, pageRegistry, downloadRoot) {
    this.items = new Map();
    this.pageRegistry = pageRegistry;
    this.downloadRoot = downloadRoot;
    this.events = new Map();
    this.connectionDirectories = new Map();
    this.cleanups = new Set();
    this.nextID = 1;
    browserSession.on("will-download", (_event, item, webContents) => {
      const id = this.nextID++;
      const connectionID = this.pageRegistry.downloadOwner(webContents.id);
      const directory = connectionID ? this.connectionDirectories.get(connectionID) : "";
      const taskItems = connectionID
        ? [...this.items.values()].filter((value) => value.connectionID === connectionID).length
        : 0;
      let rejected = false;
      const record = {
        id,
        item,
        webContentsID: webContents.id,
        connectionID,
        url: item.getURL(),
        finalUrl: item.getURL(),
        filename: "",
        startTime: new Date().toISOString(),
        state: "in_progress",
        exists: true,
        fileSize: 0,
      };
      if (connectionID && (!directory || taskItems >= 100)) {
        record.state = "interrupted";
        record.exists = false;
        rejected = true;
      } else if (connectionID) {
        record.filename = path.join(directory, `download-${id}`);
        item.setSavePath(record.filename);
      } else {
        record.filename = item.getSavePath();
      }
      this.items.set(id, record);
      if (record.connectionID) this.events.get(record.connectionID)?.emit(this.#public(record));
      item.on("updated", () => {
        record.fileSize = item.getReceivedBytes();
        if (record.connectionID && record.state === "in_progress" && record.fileSize > 110 << 20) item.cancel();
      });
      item.once("done", (_doneEvent, state) => {
        record.state = state === "completed" ? "complete" : "interrupted";
        record.filename = item.getSavePath();
        record.fileSize = item.getReceivedBytes();
      });
      if (rejected) item.cancel();
    });
  }

  async prepare() {
    await fs.mkdir(this.downloadRoot, { recursive: true, mode: 0o700 });
    await fs.chmod(this.downloadRoot, 0o700);
    const stat = await fs.lstat(this.downloadRoot);
    if (!stat.isDirectory() || stat.isSymbolicLink() || stat.uid !== process.getuid()) {
      throw new Error("Electron download root is invalid");
    }
  }

  async registerConnection(connectionID) {
    if (this.connectionDirectories.has(connectionID)) throw new Error("Electron download connection already exists");
    const directory = path.join(this.downloadRoot, connectionID);
    await fs.mkdir(directory, { mode: 0o700 });
    await fs.chmod(directory, 0o700);
    this.connectionDirectories.set(connectionID, directory);
  }

  eventFor(connectionID) {
    let event = this.events.get(connectionID);
    if (!event) {
      event = new ChromeEvent();
      this.events.set(connectionID, event);
    }
    return event;
  }

  releaseConnection(connectionID) {
    this.events.delete(connectionID);
    const directory = this.connectionDirectories.get(connectionID);
    this.connectionDirectories.delete(connectionID);
    for (const [id, item] of this.items) {
      if (item.connectionID !== connectionID) continue;
      if (item.state === "in_progress") item.item.cancel();
      this.items.delete(id);
    }
    if (directory) {
      const cleanup = fs.rm(directory, { recursive: true, force: true }).finally(() => this.cleanups.delete(cleanup));
      this.cleanups.add(cleanup);
    }
  }

  async search(connectionID, query) {
    let values = [...this.items.values()].filter((item) => item.connectionID === connectionID);
    if (Number.isSafeInteger(query?.id)) values = values.filter((item) => item.id === query.id);
    if (typeof query?.startedAfter === "string") {
      const after = Date.parse(query.startedAfter);
      values = values.filter((item) => Date.parse(item.startTime) >= after);
    }
    if (Number.isSafeInteger(query?.limit)) values = values.slice(0, query.limit);
    return values.map((item) => this.#public(item));
  }

  async cancel(connectionID, id) {
    this.#owned(connectionID, id).item.cancel();
  }

  async removeFile(connectionID, id) {
    const item = this.#owned(connectionID, id);
    if (item.filename) {
      try {
        const stat = await fs.lstat(item.filename);
        const directory = this.connectionDirectories.get(connectionID);
        if (!directory || !stat.isFile() || stat.isSymbolicLink() || stat.uid !== process.getuid() ||
            path.dirname(item.filename) !== directory || await fs.realpath(item.filename) !== item.filename) {
          throw new Error("Download file is outside the task");
        }
        await fs.rm(item.filename);
      } catch (error) {
        if (error?.code !== "ENOENT") throw error;
      }
    }
    item.exists = false;
  }

  async erase(connectionID, query) {
    if (!Number.isSafeInteger(query?.id)) return [];
    this.#owned(connectionID, query.id);
    const existed = this.items.delete(query.id);
    return existed ? [query.id] : [];
  }

  #owned(connectionID, id) {
    const item = this.items.get(id);
    if (!item || item.connectionID !== connectionID) throw new Error("Download is outside the task");
    return item;
  }

  async close() {
    for (const connectionID of [...this.connectionDirectories.keys()]) this.releaseConnection(connectionID);
    await Promise.allSettled([...this.cleanups]);
    await fs.rm(this.downloadRoot, { recursive: true, force: true });
  }

  #public(record) {
    return {
      id: record.id,
      url: record.url,
      finalUrl: record.finalUrl,
      filename: record.filename,
      startTime: record.startTime,
      state: record.state,
      exists: record.exists,
      fileSize: record.fileSize,
    };
  }
}

export class ChromeEvent {
  constructor() {
    this.listeners = new Set();
  }

  addListener(listener) {
    if (typeof listener !== "function") throw new TypeError("Listener is invalid");
    this.listeners.add(listener);
  }

  removeListener(listener) {
    this.listeners.delete(listener);
  }

  emit(...args) {
    for (const listener of [...this.listeners]) listener(...args);
  }
}

function plainObject(value) {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}
