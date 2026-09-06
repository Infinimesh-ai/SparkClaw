// Derived from Microsoft Playwright packages/extension/src/relayConnection.ts
// at 260eae31113073927b93c5c7591b5ae039952dd0. Apache-2.0.

import { HANDOFF_EVALUATE_FUNCTION, HANDOFF_MARKER } from "./protocol.mjs";

const WEBSOCKET_OPEN = 1;

const ALLOWED_COMMANDS = new Set([
  "chrome.debugger.attach",
  "chrome.debugger.detach",
  "chrome.debugger.sendCommand",
  "chrome.tabs.create",
  "chrome.tabs.remove",
]);
const EVENT_METHODS = [
  "chrome.debugger.onEvent",
  "chrome.debugger.onDetach",
  "chrome.tabs.onCreated",
  "chrome.tabs.onRemoved",
];
const BLOCKED_CDP_METHODS = new Set([
  "Browser.close",
  "Network.clearBrowserCache",
  "Network.clearBrowserCookies",
  "Network.deleteCookies",
  "Network.getAllCookies",
  "Network.getCookies",
  "Network.setCookie",
  "Network.setCookies",
  "Storage.clearCookies",
  "Storage.getCookies",
  "Storage.setCookies",
  "Target.getBrowserContexts",
  "Target.getTargets",
  "Target.setDiscoverTargets",
]);

export class RelayConnection {
  constructor({ webSocket, chromeAPI = chrome, initialTab, taskWindowID = initialTab?.windowId }) {
    if (!webSocket || !validTab(initialTab) || !Number.isInteger(taskWindowID)) {
      throw new TypeError("Relay connection requires one initial task tab");
    }
    this.webSocket = webSocket;
    this.chrome = chromeAPI;
    this.taskWindowID = taskWindowID;
    this.allowedTabs = new Set([initialTab.id]);
    this.attachedTabs = new Set();
    this.handoffGrants = new Set();
    this.eventListeners = [];
    this.closed = false;
    this.onclose = null;
    this.ontaballowed = null;
    this.ontabattached = null;
    this.ontabdetached = null;
    this.onhandoff = null;
    this.onhandoffcomplete = null;
    this.#installEventForwarders();
    this.webSocket.onmessage = (event) => {
      void this.#onMessage(event);
    };
    this.webSocket.onclose = () => this.#onClose();
  }

  didInitialize() {
    this.#send({ method: "extension.initialized", params: [] });
  }

  attachInitialTab(tab) {
    if (!validTab(tab) || !this.allowedTabs.has(tab.id)) throw new Error("Task tab is not allowed");
    this.#send({ method: "chrome.tabs.onCreated", params: [publicTab(tab)] });
  }

  connectedTabIds() {
    return [...this.allowedTabs];
  }

  moveTaskWindow(tabId, windowId) {
    if (!this.allowedTabs.has(tabId) || !Number.isInteger(windowId)) {
      throw new Error("Task window move is invalid");
    }
    this.taskWindowID = windowId;
  }

  close(message) {
    if (this.webSocket.readyState === WEBSOCKET_OPEN) this.webSocket.close(1000, message);
    this.#onClose();
  }

  releaseTab(tabId) {
    if (!this.allowedTabs.has(tabId)) return;
    if (this.attachedTabs.has(tabId)) {
      void this.chrome.debugger.detach({ tabId }).catch(() => {});
      this.#markDetached(tabId);
      this.#send({ method: "chrome.debugger.onDetach", params: [{ tabId }, "canceled_by_user"] });
    }
    this.allowedTabs.delete(tabId);
    this.handoffGrants.delete(tabId);
    this.#checkEmpty();
  }

  async handleCommand(message) {
    if (!message || typeof message !== "object" || !Number.isSafeInteger(message.id) ||
        !ALLOWED_COMMANDS.has(message.method) ||
        (message.params !== undefined && !Array.isArray(message.params))) {
      throw new Error("Invalid bridge command");
    }
    const args = message.params ?? [];
    switch (message.method) {
      case "chrome.tabs.create":
        return this.#createTaskTab(args);
      case "chrome.tabs.remove":
        return this.#removeTaskTabs(args);
      case "chrome.debugger.attach":
        return this.#attachDebugger(args);
      case "chrome.debugger.detach":
        return this.#detachDebugger(args);
      case "chrome.debugger.sendCommand":
        return this.#sendDebuggerCommand(args);
      default:
        throw new Error("Unknown bridge command");
    }
  }

  async #createTaskTab(args) {
    if (args.length !== 1 || !plainObject(args[0])) throw new Error("Invalid tab creation");
    const rawURL = args[0].url;
    if (rawURL !== undefined && (typeof rawURL !== "string" || rawURL.length > 16384)) {
      throw new Error("Invalid task URL");
    }
    const tab = await this.chrome.tabs.create({
      ...(rawURL === undefined ? {} : { url: rawURL }),
      active: false,
      pinned: false,
      windowId: this.taskWindowID,
    });
    if (!validTab(tab) || tab.windowId !== this.taskWindowID) throw new Error("Task tab creation failed");
    this.#allowTab(tab);
    return publicTab(tab);
  }

  async #removeTaskTabs(args) {
    if (args.length !== 1) throw new Error("Invalid tab removal");
    const ids = Array.isArray(args[0]) ? args[0] : [args[0]];
    if (ids.length === 0 || ids.some((id) => !Number.isInteger(id) || !this.allowedTabs.has(id))) {
      throw new Error("Tab is outside the task allowlist");
    }
    await this.chrome.tabs.remove(args[0]);
    for (const id of ids) this.#forgetTab(id);
    return {};
  }

  async #attachDebugger(args) {
    const tabId = targetTabID(args[0]);
    if (args.length !== 2 || !this.allowedTabs.has(tabId) || typeof args[1] !== "string") {
      throw new Error("Tab is outside the task allowlist");
    }
    const result = await this.chrome.debugger.attach(args[0], args[1]);
    this.attachedTabs.add(tabId);
    this.ontabattached?.(tabId);
    return result ?? {};
  }

  async #detachDebugger(args) {
    const tabId = targetTabID(args[0]);
    if (args.length !== 1 || !this.attachedTabs.has(tabId)) throw new Error("Task tab is not attached");
    const result = await this.chrome.debugger.detach(args[0]);
    this.#markDetached(tabId);
    this.#checkEmpty();
    return result ?? {};
  }

  async #sendDebuggerCommand(args) {
    const tabId = targetTabID(args[0]);
    const method = args[1];
    if (args.length < 2 || args.length > 3 || !this.attachedTabs.has(tabId) || typeof method !== "string") {
      throw new Error("Task tab is not attached");
    }
    if (BLOCKED_CDP_METHODS.has(method)) throw new Error("Browser-wide command is unavailable");
    const params = args[2];
    const grantsHandoff = isHandoffMarker(method, params);
    if (method === "Page.bringToFront") {
      if (!this.handoffGrants.delete(tabId)) return {};
      this.onhandoff?.(tabId);
    }
    const result = await this.chrome.debugger.sendCommand(...args) ?? {};
    if (method === "Page.bringToFront") await this.onhandoffcomplete?.(tabId);
    if (grantsHandoff) {
      this.onhandoff?.(tabId);
      await this.onhandoffcomplete?.(tabId);
      this.handoffGrants.add(tabId);
    }
    return result;
  }

  #allowTab(tab) {
    if (this.allowedTabs.has(tab.id)) return;
    this.allowedTabs.add(tab.id);
    this.ontaballowed?.(tab.id);
  }

  #forgetTab(tabId) {
    this.allowedTabs.delete(tabId);
    this.#markDetached(tabId);
    this.handoffGrants.delete(tabId);
  }

  #markDetached(tabId) {
    if (!this.attachedTabs.delete(tabId)) return;
    this.ontabdetached?.(tabId);
  }

  #installEventForwarders() {
    for (const fullMethod of EVENT_METHODS) {
      const target = resolveChromeMember(this.chrome, fullMethod);
      const listener = (...args) => {
        void this.#onChromeEvent(fullMethod, args);
      };
      target.obj[target.name].addListener(listener);
      this.eventListeners.push(() => target.obj[target.name].removeListener(listener));
    }
  }

  async #onChromeEvent(fullMethod, args) {
    if (this.closed) return;
    if (fullMethod === "chrome.tabs.onCreated") {
      const tab = args[0];
      if (!validTab(tab) || !this.allowedTabs.has(tab.openerTabId)) return;
      if (tab.windowId !== this.taskWindowID) {
        await this.chrome.tabs.remove(tab.id).catch(() => {});
        return;
      }
      this.#allowTab(tab);
      await this.chrome.tabs.update(tab.id, { active: false }).catch(() => {});
      this.#send({ method: fullMethod, params: [publicTab(tab)] });
      return;
    }
    const tabId = fullMethod === "chrome.tabs.onRemoved" ? args[0] : targetTabID(args[0]);
    if (!Number.isInteger(tabId) || !this.allowedTabs.has(tabId)) return;
    if (fullMethod.startsWith("chrome.debugger.") && !this.attachedTabs.has(tabId)) return;
    this.#send({ method: fullMethod, params: args });
    if (fullMethod === "chrome.debugger.onDetach") this.#markDetached(tabId);
    if (fullMethod === "chrome.tabs.onRemoved") this.#forgetTab(tabId);
    this.#checkEmpty();
  }

  async #onMessage(event) {
    let message;
    try {
      message = JSON.parse(event.data);
      const result = await this.handleCommand(message);
      this.#send({ id: message.id, result });
    } catch (error) {
      this.#send({ id: message?.id, error: error instanceof Error ? error.message : "Bridge command failed" });
    }
  }

  #send(message) {
    if (!this.closed && this.webSocket.readyState === WEBSOCKET_OPEN) {
      this.webSocket.send(JSON.stringify(message));
    }
  }

  #checkEmpty() {
    if (this.allowedTabs.size === 0) this.close("All task tabs released");
  }

  #onClose() {
    if (this.closed) return;
    this.closed = true;
    for (const remove of this.eventListeners) remove();
    this.eventListeners = [];
    for (const tabId of [...this.attachedTabs]) {
      void this.chrome.debugger.detach({ tabId }).catch(() => {});
      this.#markDetached(tabId);
    }
    this.allowedTabs.clear();
    this.handoffGrants.clear();
    this.onclose?.();
  }
}

function isHandoffMarker(method, params) {
  if (!new Set(["Runtime.callFunctionOn", "Runtime.evaluate"]).has(method) || !plainObject(params)) return false;
  if (method === "Runtime.evaluate") return params.expression === HANDOFF_EVALUATE_FUNCTION;
  if (typeof params.functionDeclaration !== "string" || typeof params.objectId !== "string" ||
      !params.functionDeclaration.includes("utilityScript") || !params.functionDeclaration.includes(".evaluate(") ||
      params.returnByValue !== true || params.awaitPromise !== true || params.userGesture !== true ||
      !Array.isArray(params.arguments) || params.arguments.length !== 6 ||
      typeof params.arguments[0]?.objectId !== "string") return false;
  return serializedHandoffFunction(params.arguments[5]?.value);
}

function serializedHandoffFunction(value) {
  if (typeof value !== "string") return false;
  if (value === HANDOFF_EVALUATE_FUNCTION) return true;
  let parsed;
  try {
    parsed = JSON.parse(value);
  } catch {
    return false;
  }
  return plainObject(parsed) && Object.keys(parsed).length === 1 &&
    parsed.s === HANDOFF_EVALUATE_FUNCTION;
}

function targetTabID(value) {
  return plainObject(value) && Number.isInteger(value.tabId) ? value.tabId : undefined;
}

function validTab(tab) {
  return plainObject(tab) && Number.isInteger(tab.id) && Number.isInteger(tab.windowId);
}

function publicTab(tab) {
  return {
    id: tab.id,
    windowId: tab.windowId,
    active: Boolean(tab.active),
    pinned: Boolean(tab.pinned),
    url: typeof tab.url === "string" ? tab.url : "",
    title: typeof tab.title === "string" ? tab.title : "",
    ...(Number.isInteger(tab.openerTabId) ? { openerTabId: tab.openerTabId } : {}),
  };
}

function plainObject(value) {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}

function resolveChromeMember(chromeAPI, fullMethod) {
  const parts = fullMethod.split(".");
  let object = chromeAPI;
  for (let index = 1; index < parts.length - 1; index++) object = object?.[parts[index]];
  const name = parts.at(-1);
  if (!object?.[name]?.addListener) throw new Error(`Chrome event is unavailable: ${fullMethod}`);
  return { obj: object, name };
}
