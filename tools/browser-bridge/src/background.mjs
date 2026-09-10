// Derived from Microsoft Playwright packages/extension/src/background.ts and
// connectedTabGroup.ts at 260eae31113073927b93c5c7591b5ae039952dd0.
// Apache-2.0.

import {
  BRIDGE_EXTENSION_ID,
  BRIDGE_VERSION,
  NATIVE_HOST_NAME,
  SUPPORTED_PROTOCOL_VERSION,
  boundedClientName,
  exactKeys,
  parseConnectionPageURL,
  parseRelayURL,
} from "./protocol.mjs";
import { RelayConnection } from "./relay-connection.mjs";

const GROUP_TITLE_PREFIX = "SparkClaw task";
const GROUP_COLORS = ["green", "blue", "cyan", "yellow", "purple", "orange"];
const MAX_OWNER_TAB_HISTORY = 16;
const PENDING_CONNECTION_TTL_MS = 6500;
const RELAY_CONNECT_TIMEOUT_MS = 5000;
const OWNED_GROUP_IDS_KEY = "sparkclawTaskGroupIDs";

export class SparkClawBrowserBridge {
  constructor({
    chromeAPI = chrome,
    WebSocketClass = WebSocket,
    setTimeoutFn = globalThis.setTimeout.bind(globalThis),
    clearTimeoutFn = globalThis.clearTimeout.bind(globalThis),
    pendingConnectionTTLMS = PENDING_CONNECTION_TTL_MS,
    staleCleanupDelays = [1000, 10_000],
  } = {}) {
    this.chrome = chromeAPI;
    this.WebSocketClass = WebSocketClass;
    this.setTimeout = setTimeoutFn;
    this.clearTimeout = clearTimeoutFn;
    this.pendingConnectionTTLMS = pendingConnectionTTLMS;
    this.connections = new Map();
    this.pending = new Map();
    this.nextConnectionID = 0;
    this.nativePort = null;
    this.nativeReconnectTimer = null;
    this.focus = new FocusTracker(chromeAPI);
    this.ownedGroups = new OwnedGroupRegistry(chromeAPI);
    this.chrome.runtime.onMessage.addListener((message, sender, respond) =>
      this.#onMessage(message, sender, respond));
    this.chrome.action.onClicked.addListener(() => {
      void this.chrome.tabs.create({ url: this.chrome.runtime.getURL("status.html"), active: true });
    });
    this.chrome.tabs.onRemoved.addListener((tabId) => this.#clearPending(tabId));
    this.chrome.runtime.onStartup.addListener(() => this.#scheduleStaleTaskCleanup(1000));
    for (const delayMS of staleCleanupDelays) this.#scheduleStaleTaskCleanup(delayMS);
    this.#connectNativeHost();
  }

  #onMessage(message, sender, respond) {
    if (!sender?.tab?.id || !this.#isBridgePage(sender)) {
      respond({ success: false, error: "Bridge page is required" });
      return false;
    }
    switch (message?.type) {
      case "connectionRequested":
        if (!this.#isBridgePage(sender, "connect.html")) {
          respond({ success: false, error: "Task connection page is invalid" });
          return false;
        }
        void this.#requestConnection(message, sender).then(
          () => respond({ success: true }),
          (error) => respond({ success: false, error: safeError(error) }),
        );
        return true;
      case "connectToTask":
        if (!this.#isBridgePage(sender, "connect.html")) {
          respond({ success: false, error: "Task connection page is invalid" });
          return false;
        }
        void this.#connectToTask(message, sender).then(
          () => respond({ success: true }),
          (error) => respond({ success: false, error: safeError(error) }),
        );
        return true;
      case "discardConnectionPage":
        if (!this.#isBridgePage(sender, "connect.html") || !exactKeys(message, ["type"])) {
          respond({ success: false, error: "Task connection page is invalid" });
          return false;
        }
        this.#clearPending(sender.tab.id);
        this.#releaseTab(sender.tab.id);
        respond({ success: true });
        void this.focus.closeTaskTabs([sender.tab.id]).catch(() => this.#scheduleStaleTaskCleanup(1000));
        return false;
      case "getConnectionStatus":
        if (!this.#isBridgePage(sender, "status.html")) {
          respond({ success: false, error: "Status page is required" });
          return false;
        }
        if (!exactKeys(message, ["type"])) {
          respond({ success: false, error: "Invalid request" });
          return false;
        }
        respond({
          success: true,
          connections: [...this.connections].map(([id, group]) => ({
            id,
            clientName: group.clientName,
            connectedTabIds: group.connectedTabIds(),
          })),
        });
        this.#scheduleStaleTaskCleanup(0);
        return false;
      case "disconnect":
        if (!this.#isBridgePage(sender, "status.html")) {
          respond({ success: false, error: "Status page is required" });
          return false;
        }
        if (!exactKeys(message, ["type", "connectionId"]) || !Number.isInteger(message.connectionId)) {
          respond({ success: false, error: "Invalid request" });
          return false;
        }
        this.connections.get(message.connectionId)?.close("Owner disconnected task");
        respond({ success: true });
        return false;
      case "keepalive":
        respond({ success: true });
        return false;
      default:
        respond({ success: false, error: "Unknown request" });
        return false;
    }
  }

  async #requestConnection(message, sender) {
    if (!exactKeys(message, ["type", "mcpRelayUrl"])) throw new Error("Invalid request");
    const tabId = sender.tab.id;
    const relayURL = parseRelayURL(message.mcpRelayUrl);
    this.#clearPending(tabId);
    this.#releaseTab(tabId);
    await ungroupTabs(this.chrome, [tabId]);
    this.focus.allowTaskTab(tabId);
    const pending = { relayURL, timer: null };
    this.pending.set(tabId, pending);
    pending.timer = this.setTimeout(() => {
      pending.timer = null;
      if (!this.#clearPending(tabId, pending)) return;
      this.#releaseTab(tabId);
      void this.focus.closeTaskTabs([tabId]).catch(() => this.#scheduleStaleTaskCleanup(1000));
    }, this.pendingConnectionTTLMS);
    await this.focus.restoreAfterConnectionPage(sender.tab);
  }

  async #connectToTask(message, sender) {
    if (!exactKeys(message, ["type"], ["clientName"])) throw new Error("Invalid request");
    const selectorTab = await this.chrome.tabs.get(sender.tab.id);
    if (!this.#isConnectTab(selectorTab)) throw new Error("Task connection page is invalid");
    const pending = this.pending.get(selectorTab.id);
    if (!pending) throw new Error("Pending client connection closed");
    const webSocket = await openRelayConnection(this.WebSocketClass, pending.relayURL, this);
    if (!this.#clearPending(selectorTab.id, pending)) {
      webSocket.close(1000, "Pending client connection closed");
      throw new Error("Pending client connection closed");
    }
    const relay = new RelayConnection({
      webSocket,
      chromeAPI: this.chrome,
      initialTab: selectorTab,
      taskWindowID: selectorTab.windowId,
    });
    this.focus.allowTaskTab(selectorTab.id);
    relay.ontaballowed = (tabId) => this.focus.allowTaskTab(tabId);
    relay.ontabdetached = (tabId) => this.focus.releaseTaskTab(tabId);
    const id = ++this.nextConnectionID;
    const group = new TaskTabGroup({
      chromeAPI: this.chrome,
      relay,
      initialTab: selectorTab,
      clientName: boundedClientName(message.clientName),
      style: uniqueGroupStyle(message.clientName, [...this.connections.values()]),
      focus: this.focus,
      ownedGroups: this.ownedGroups,
    });
    relay.onhandoff = (tabId) => {
      group.beginHandoff(tabId);
      this.focus.grantHandoff(tabId);
    };
    relay.onhandoffcomplete = async (tabId) => {
      const windowId = await this.focus.focusHandoffWindow(tabId);
      if (Number.isInteger(windowId)) relay.moveTaskWindow(tabId, windowId);
      await group.completeHandoff(tabId);
      this.#focusNativeHandoffWindow();
    };
    group.onclose = () => {
      this.connections.delete(id);
      this.#scheduleStaleTaskCleanup(1000);
    };
    this.connections.set(id, group);
    relay.attachInitialTab(selectorTab);
    relay.didInitialize();
  }

  #releaseTab(tabId) {
    for (const group of this.connections.values()) group.releaseTab(tabId);
  }

  #clearPending(tabId, expected) {
    const pending = this.pending.get(tabId);
    if (!pending || (expected && pending !== expected)) return false;
    this.pending.delete(tabId);
    if (pending.timer !== null) this.clearTimeout(pending.timer);
    return true;
  }

  #protectedTaskTabIDs() {
    const tabIDs = new Set(this.pending.keys());
    for (const group of this.connections.values()) {
      for (const tabId of group.connectedTabIds()) tabIDs.add(tabId);
    }
    return tabIDs;
  }

  #scheduleStaleTaskCleanup(delayMS, retries = 2) {
    const timer = this.setTimeout(() => {
      void cleanupStaleTaskTabs(this.chrome, this.focus, this.ownedGroups,
        () => this.#protectedTaskTabIDs()).catch(() => {
        if (retries > 0) this.#scheduleStaleTaskCleanup(10_000, retries - 1);
      });
    }, delayMS);
    timer?.unref?.();
  }

  #connectNativeHost() {
    if (typeof this.chrome.runtime.connectNative !== "function" || this.nativePort) return;
    let port;
    try {
      port = this.chrome.runtime.connectNative(NATIVE_HOST_NAME);
    } catch {
      this.#scheduleNativeReconnect();
      return;
    }
    this.nativePort = port;
    port.postMessage({
      type: "bridgeReady",
      extension_id: BRIDGE_EXTENSION_ID,
      version: BRIDGE_VERSION,
      protocol_version: SUPPORTED_PROTOCOL_VERSION,
    });
    port.onMessage.addListener((message) => void this.#onNativeMessage(port, message));
    port.onDisconnect.addListener(() => {
      if (this.nativePort === port) this.nativePort = null;
      this.#scheduleNativeReconnect();
    });
  }

  #scheduleNativeReconnect() {
    if (this.nativeReconnectTimer !== null) return;
    this.nativeReconnectTimer = this.setTimeout(() => {
      this.nativeReconnectTimer = null;
      this.#connectNativeHost();
    }, 1000);
  }

  async #onNativeMessage(port, message) {
    if (!exactKeys(message, ["type", "id", "url"]) || message.type !== "openConnection" ||
        !Number.isSafeInteger(message.id) || message.id < 1) {
      return;
    }
    let success = false;
    try {
      const url = parseConnectionPageURL(message.url);
      await this.focus.openBackgroundConnectionPage(url);
      success = true;
    } catch {
      // The native host receives only a fixed result; connection details remain in-browser.
    }
    if (this.nativePort === port) {
      port.postMessage({ type: "openConnectionResult", id: message.id, success });
    }
  }

  #focusNativeHandoffWindow() {
    if (!this.nativePort) return;
    try {
      this.nativePort.postMessage({ type: "focusHandoff" });
    } catch {
      // Browser focus remains best-effort after the in-browser handoff completes.
    }
  }

  #isBridgePage(sender, expectedPath = "") {
    if (sender.id !== this.chrome.runtime.id || typeof sender.url !== "string") return false;
    try {
      const url = new URL(sender.url);
      return url.protocol === "chrome-extension:" && url.hostname === this.chrome.runtime.id &&
        (!expectedPath || url.pathname === `/${expectedPath}`);
    } catch {
      return false;
    }
  }

  #isConnectTab(tab) {
    return typeof tab?.url === "string" &&
      tab.url.startsWith(`chrome-extension://${this.chrome.runtime.id}/connect.html?`);
  }
}

export class TaskTabGroup {
  constructor({ chromeAPI, relay, initialTab, clientName, style, focus, ownedGroups = new OwnedGroupRegistry(chromeAPI) }) {
    this.chrome = chromeAPI;
    this.relay = relay;
    this.clientName = clientName;
    this.style = style;
    this.focus = focus;
    this.ownedGroups = ownedGroups;
    this.groupID = null;
    this.groupingQueue = Promise.resolve();
    this.groupingTabs = new Set();
    this.groupChanges = new Map();
    this.closed = false;
    this.createdGroupIDs = new Set();
    this.ownedTabs = new Set([initialTab.id]);
    this.handoffTabs = new Set();
    this.onclose = null;
    this.onUpdated = (tabId, changeInfo) => {
      if (changeInfo.groupId !== undefined) void this.#groupChanged(tabId, changeInfo.groupId);
    };
    this.onRemoved = (tabId) => this.ownedTabs.delete(tabId);
    this.chrome.tabs.onUpdated.addListener(this.onUpdated);
    this.chrome.tabs.onRemoved.addListener(this.onRemoved);
    const relayTabAllowed = this.relay.ontaballowed;
    this.relay.ontaballowed = (tabId) => {
      if (this.closed) return;
      relayTabAllowed?.(tabId);
      this.ownedTabs.add(tabId);
    };
    this.relay.ontabattached = (tabId) => {
      if (this.closed) return;
      this.ownedTabs.add(tabId);
      void this.#addToGroup(tabId);
    };
    this.relay.onclose = () => this.#closed();
  }

  connectedTabIds() {
    return [...new Set([...this.relay.connectedTabIds(), ...this.ownedTabs])];
  }

  releaseTab(tabId) {
    this.ownedTabs.delete(tabId);
    this.focus.releaseTaskTab(tabId);
    this.relay.releaseTab(tabId);
  }

  close(reason) {
    this.relay.close(reason);
  }

  beginHandoff(tabId) {
    if (this.ownedTabs.has(tabId)) this.handoffTabs.add(tabId);
  }

  async completeHandoff(tabId) {
    if (!this.handoffTabs.has(tabId) || !this.ownedTabs.has(tabId)) return;
    try {
      await this.#addToGroup(tabId, true);
    } finally {
      this.handoffTabs.delete(tabId);
    }
  }

  #addToGroup(tabId, createGroup = false) {
    // Chrome can emit groupId changes before tabs.group resolves. Serialize
    // creation and reconcile those events only after the assigned ID is known.
    const pending = this.groupingQueue.then(() => this.#groupTab(tabId, createGroup));
    this.groupingQueue = pending.catch(() => {});
    return this.groupingQueue;
  }

  async #groupTab(tabId, createGroup) {
    if (this.closed || !this.ownedTabs.has(tabId)) return;
    this.groupingTabs.add(tabId);
    try {
      let assignedGroup;
      if (this.groupID === null || createGroup) {
        assignedGroup = await this.chrome.tabs.group({ tabIds: [tabId] });
        this.groupID = assignedGroup;
        this.createdGroupIDs.add(assignedGroup);
        await this.ownedGroups.add(assignedGroup);
        if (!this.closed) await this.chrome.tabGroups.update(assignedGroup, this.style);
      } else {
        assignedGroup = this.groupID;
        await this.chrome.tabs.group({ groupId: assignedGroup, tabIds: [tabId] });
      }
      // Explicit owner release can race a pending Chrome grouping operation.
      // Remove our presentation only if the tab still occupies that exact group.
      if (!this.ownedTabs.has(tabId)) {
        const tab = await this.chrome.tabs.get(tabId).catch(() => null);
        if (tab?.groupId === assignedGroup) await ungroupTabs(this.chrome, [tabId]);
        this.groupChanges.delete(tabId);
      }
    } catch {
      // Grouping is presentation-only; task authorization remains in RelayConnection.
    } finally {
      this.groupingTabs.delete(tabId);
      const observed = this.groupChanges.get(tabId);
      this.groupChanges.delete(tabId);
      if (observed !== undefined) await this.#groupChanged(tabId, observed);
    }
  }

  async #groupChanged(tabId, groupId) {
    if (this.groupingTabs.has(tabId)) {
      this.groupChanges.set(tabId, groupId);
      return;
    }
    if (this.groupID === null) return;
    if (this.handoffTabs.has(tabId)) return;
    if (groupId === this.groupID && !this.ownedTabs.has(tabId)) {
      await ungroupTabs(this.chrome, [tabId]);
      return;
    }
    if (groupId !== this.groupID && this.ownedTabs.has(tabId)) this.releaseTab(tabId);
  }

  #closed() {
    if (this.closed) return this.cleanupPromise;
    this.closed = true;
    this.cleanupPromise = (async () => {
      // Include IDs returned by a Chrome grouping call that was already in flight.
      await this.groupingQueue;
      const tabs = [...this.ownedTabs];
      const groupIDs = [...this.createdGroupIDs];
      try {
        if (tabs.length) await this.focus.closeTaskTabs(tabs, tabId => this.ownedTabs.has(tabId));
        await this.ownedGroups.remove(groupIDs);
      } catch {
        // Keep persisted group ownership so the stale-task sweep can retry.
      } finally {
        this.chrome.tabs.onUpdated.removeListener(this.onUpdated);
        this.chrome.tabs.onRemoved.removeListener(this.onRemoved);
        this.ownedTabs.clear();
        this.handoffTabs.clear();
        this.createdGroupIDs.clear();
        this.groupChanges.clear();
        this.onclose?.();
      }
    })();
    return this.cleanupPromise;
  }
}

// Records the tab-group IDs this Bridge created so stale cleanup after a
// Service Worker restart closes only SparkClaw-owned groups, never an owner
// group that merely shares the "SparkClaw task" title. chrome.storage.session
// survives worker restarts and is cleared with the browser session, matching
// the lifetime of tab-group IDs.
export class OwnedGroupRegistry {
  constructor(chromeAPI) {
    this.storage = chromeAPI.storage?.session ?? null;
    this.queue = Promise.resolve();
  }

  list() {
    return this.#serialized(async () => this.#read());
  }

  add(groupID) {
    return this.#update((ids) => ids.add(groupID));
  }

  remove(groupIDs) {
    return this.#update((ids) => { for (const groupID of groupIDs) ids.delete(groupID); });
  }

  #update(mutate) {
    return this.#serialized(async () => {
      const ids = await this.#read();
      mutate(ids);
      if (this.storage) await this.storage.set({ [OWNED_GROUP_IDS_KEY]: [...ids] }).catch(() => {});
      return ids;
    });
  }

  #serialized(operation) {
    const result = this.queue.then(operation);
    this.queue = result.catch(() => {});
    return result;
  }

  async #read() {
    if (!this.storage) return new Set();
    const stored = await this.storage.get(OWNED_GROUP_IDS_KEY).catch(() => ({}));
    const ids = stored?.[OWNED_GROUP_IDS_KEY];
    return new Set(Array.isArray(ids) ? ids.filter(Number.isInteger) : []);
  }
}

export class FocusTracker {
  constructor(chromeAPI) {
    this.chrome = chromeAPI;
    this.ownerActiveTabs = new Map();
    this.taskTabs = new Set();
    this.handoffGrants = new Set();
    this.handoffWindows = new Map();
    this.chrome.tabs.onActivated.addListener(({ tabId, windowId }) => {
      if (this.taskTabs.has(tabId)) {
        if (!this.handoffGrants.delete(tabId)) void this.#restoreWhenReady(windowId, tabId);
        return;
      }
      void this.chrome.tabs.get(tabId).then((tab) => {
        if (this.taskTabs.has(tabId) || this.#isBridgeURL(tab.url)) {
          this.#forgetTab(tabId, windowId);
          if (this.#isConnectionURL(tab.url)) void this.#restoreWhenReady(windowId, tabId);
          return;
        }
        this.#rememberOwnerTab(windowId, tabId);
      }).catch(() => this.#forgetTab(tabId, windowId));
    });
    this.chrome.tabs.onUpdated.addListener((tabId, changeInfo, tab) => {
      const url = changeInfo.url ?? tab?.url;
      if (!this.#isBridgeURL(url)) {
        if (tab?.active && !this.taskTabs.has(tabId)) this.#rememberOwnerTab(tab.windowId, tabId);
        return;
      }
      this.#forgetTab(tabId, tab?.windowId);
      if (tab?.active && this.#isConnectionURL(url)) {
        this.allowTaskTab(tabId);
        void this.#restoreWhenReady(tab.windowId, tabId);
      }
    });
    this.chrome.tabs.onRemoved.addListener((tabId) => {
      this.releaseTaskTab(tabId);
      this.#forgetTab(tabId);
    });
    this.ready = this.#seed();
  }

  allowTaskTab(tabId) {
    if (Number.isInteger(tabId)) {
      this.taskTabs.add(tabId);
      this.#forgetTab(tabId);
    }
  }

  releaseTaskTab(tabId) {
    this.taskTabs.delete(tabId);
    this.handoffGrants.delete(tabId);
    this.handoffWindows.delete(tabId);
  }

  grantHandoff(tabId) {
    if (this.taskTabs.has(tabId)) this.handoffGrants.add(tabId);
  }

  async openBackgroundConnectionPage(url) {
    await this.ready;
    const lastFocused = await this.chrome.windows.getLastFocused().catch(() => null);
    let windowId = Number.isInteger(lastFocused?.id) ? lastFocused.id : null;
    if (windowId === null || !this.ownerActiveTabs.has(windowId)) {
      windowId = [...this.ownerActiveTabs.keys()].at(-1) ?? null;
    }
    if (!Number.isInteger(windowId)) throw new Error("Owner browser window is unavailable");
    const tab = await this.chrome.tabs.create({ url, active: false, pinned: false, windowId });
    if (!Number.isInteger(tab?.id) || tab.windowId !== windowId) {
      throw new Error("Background connection tab creation failed");
    }
    this.allowTaskTab(tab.id);
    return tab.id;
  }

  async focusHandoffWindow(tabId) {
    if (!this.taskTabs.has(tabId)) return null;
    const tab = await this.chrome.tabs.get(tabId).catch(() => null);
    if (!tab || !Number.isInteger(tab.windowId)) return null;
    const handoffWindowID = this.handoffWindows.get(tabId);
    if (handoffWindowID !== tab.windowId) {
      this.handoffGrants.add(tabId);
      try {
        await this.chrome.tabs.ungroup([tabId]);
        const handoffWindow = await this.chrome.windows.create({
          tabId,
          focused: true,
          type: "normal",
        });
        if (!Number.isInteger(handoffWindow?.id)) throw new Error("Task handoff window was not created");
        this.handoffWindows.set(tabId, handoffWindow.id);
        return handoffWindow.id;
      } finally {
        this.handoffGrants.delete(tabId);
      }
    }
    if (!tab.active) {
      this.handoffGrants.add(tabId);
      await this.chrome.tabs.update(tabId, { active: true }).catch(() => {});
    }
    this.handoffGrants.delete(tabId);
    await this.chrome.windows.update(tab.windowId, { focused: true }).catch(() => {});
    return tab.windowId;
  }

  async restoreAfterConnectionPage(connectTab) {
    this.allowTaskTab(connectTab.id);
    // Restore only the owner tab; background connections must preserve OS window focus.
    await this.#restoreWhenReady(connectTab.windowId, connectTab.id);
  }

  async closeTaskTabs(tabIds, canClose = () => true) {
    const ids = [...new Set(tabIds.filter(Number.isInteger))];
    if (ids.length === 0) return [];
    for (const tabId of ids) this.allowTaskTab(tabId);
    await this.ready;
    const tabs = await Promise.all(ids.map((tabId) => this.chrome.tabs.get(tabId).catch(() => null)));
    const taskWindows = new Set(tabs.filter((tab) => Number.isInteger(tab?.windowId)).map((tab) => tab.windowId));
    for (const windowId of taskWindows) await this.#restoreOwnerTab(windowId);
    // A connection may become active while Chrome's asynchronous queries or
    // focus restoration are pending. Check ownership again at the actual close.
    const closing = ids.filter(canClose);
    await closeTaskTabs(this.chrome, closing);
    for (const windowId of taskWindows) await this.#restoreOwnerTab(windowId);
    for (const tabId of closing) this.releaseTaskTab(tabId);
    return closing;
  }

  async #restoreOwnerTab(windowId, excludedTabId) {
    const history = [...(this.ownerActiveTabs.get(windowId) ?? [])].reverse();
    for (const tabId of history) {
      if (tabId === excludedTabId || this.taskTabs.has(tabId)) continue;
      const tab = await this.chrome.tabs.get(tabId).catch(() => null);
      if (!tab || tab.windowId !== windowId || this.#isBridgeURL(tab.url)) {
        this.#forgetTab(tabId, windowId);
        continue;
      }
      await this.chrome.tabs.update(tabId, { active: true }).catch(() => {});
      this.#rememberOwnerTab(windowId, tabId);
      return;
    }
  }

  async #restoreWhenReady(windowId, excludedTabId) {
    await this.ready;
    await this.#restoreOwnerTab(windowId, excludedTabId);
  }

  async #seed() {
    const tabs = await this.chrome.tabs.query({}).catch(() => []);
    tabs.sort((left, right) => (left.lastAccessed ?? 0) - (right.lastAccessed ?? 0));
    for (const tab of tabs) {
      if (Number.isInteger(tab.id) && Number.isInteger(tab.windowId) && !this.#isBridgeURL(tab.url)) {
        this.#rememberOwnerTab(tab.windowId, tab.id);
      }
    }
  }

  #isBridgeURL(url) {
    return typeof url === "string" && url.startsWith(`chrome-extension://${this.chrome.runtime.id}/`);
  }

  #isConnectionURL(url) {
    return typeof url === "string" &&
      url.startsWith(`chrome-extension://${this.chrome.runtime.id}/connect.html?`);
  }

  #rememberOwnerTab(windowId, tabId) {
    if (!Number.isInteger(windowId) || !Number.isInteger(tabId)) return;
    const history = (this.ownerActiveTabs.get(windowId) ?? []).filter((candidate) => candidate !== tabId);
    history.push(tabId);
    this.ownerActiveTabs.set(windowId, history.slice(-MAX_OWNER_TAB_HISTORY));
  }

  #forgetTab(tabId, onlyWindowId) {
    for (const [windowId, history] of this.ownerActiveTabs) {
      if (onlyWindowId !== undefined && windowId !== onlyWindowId) continue;
      const remaining = history.filter((candidate) => candidate !== tabId);
      if (remaining.length) this.ownerActiveTabs.set(windowId, remaining);
      else this.ownerActiveTabs.delete(windowId);
    }
  }
}

// Opens the relay WebSocket and settles once it is open. A socket that times
// out or errors is closed here so a failed attempt never leaves a half-open
// connection behind; the timers come from the Bridge so tests can drive them.
async function openRelayConnection(WebSocketClass, relayURL, timers) {
  const socket = new WebSocketClass(relayURL);
  await new Promise((resolve, reject) => {
    const fail = (message) => {
      timers.clearTimeout(timer);
      socket.onopen = null;
      socket.onerror = null;
      try {
        socket.close(1000, message);
      } catch {
        // A socket that already failed may refuse close(); nothing is left open.
      }
      reject(new Error(message));
    };
    const timer = timers.setTimeout(() => fail("Relay connection timeout"), RELAY_CONNECT_TIMEOUT_MS);
    socket.onopen = () => {
      timers.clearTimeout(timer);
      resolve();
    };
    socket.onerror = () => fail("Relay connection failed");
  });
  return socket;
}

export async function cleanupStaleTaskTabs(chromeAPI, focus, ownedGroups, protectedTabIDs = new Set()) {
  const protectedNow = () => typeof protectedTabIDs === "function" ? protectedTabIDs() : protectedTabIDs;
  const staleTabIDs = new Set();
  const ownedGroupIDs = await ownedGroups.list();
  const groups = await chromeAPI.tabGroups.query({});
  const staleGroups = new Map();
  for (const group of groups) {
    if (!ownedGroupIDs.has(group.id)) continue;
    const tabs = await chromeAPI.tabs.query({ groupId: group.id });
    const tabIDs = tabs.map((tab) => tab.id).filter(Number.isInteger);
    if (tabIDs.some((tabId) => protectedNow().has(tabId))) continue;
    for (const tabId of tabIDs) staleTabIDs.add(tabId);
    staleGroups.set(group.id, tabIDs);
  }
  const tabs = await chromeAPI.tabs.query({});
  const connectionPrefix = `chrome-extension://${chromeAPI.runtime.id}/connect.html?`;
  for (const tab of tabs) {
    if (Number.isInteger(tab.id) && typeof tab.url === "string" && tab.url.startsWith(connectionPrefix)) {
      staleTabIDs.add(tab.id);
    }
  }
  const closed = new Set(await focus.closeTaskTabs([...staleTabIDs], tabId => !protectedNow().has(tabId)));
  const liveGroupIDs = new Set(groups.map((group) => group.id));
  await ownedGroups.remove([...ownedGroupIDs].filter((groupID) =>
    !liveGroupIDs.has(groupID) || staleGroups.has(groupID) && staleGroups.get(groupID).every(tabId => closed.has(tabId))));
}

async function ungroupTabs(chromeAPI, tabIds) {
  if (tabIds.length === 0) return;
  await chromeAPI.tabs.ungroup(tabIds).catch(() => {});
}

async function closeTaskTabs(chromeAPI, tabIds) {
  if (tabIds.length === 0) return;
  // Removal also removes empty groups. Ungrouping first would erase ownership
  // evidence if removal fails, leaving an ordinary-looking orphan mailbox tab.
  try {
    await chromeAPI.tabs.remove(tabIds);
  } catch (error) {
    const remaining = await Promise.all(tabIds.map(tabId => chromeAPI.tabs.get(tabId).catch(() => null)));
    if (remaining.some(Boolean)) throw error;
  }
}

function uniqueGroupStyle(clientName, groups) {
  const base = `${GROUP_TITLE_PREFIX} · ${boundedClientName(clientName)}`;
  const titles = new Set(groups.map((group) => group.style.title));
  let title = base;
  for (let index = 2; titles.has(title); index++) title = `${base} (${index})`;
  const usedColors = new Set(groups.map((group) => group.style.color));
  return { title, color: GROUP_COLORS.find((color) => !usedColors.has(color)) ?? GROUP_COLORS[0] };
}

function safeError(error) {
  if (!(error instanceof Error)) return "Bridge request failed";
  return ["Invalid request", "Task connection page is invalid", "Pending client connection closed"]
    .includes(error.message) ? error.message : "Bridge request failed";
}
