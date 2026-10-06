import crypto from "node:crypto";

import { ELECTRON_CONNECT_URL } from "./protocol.mjs";

const ALLOWED_ROLES = new Set(["personal", "task"]);

export class PageRegistry {
  constructor({ runtimeGeneration, browserSession, createView, presentation, scriptHost, qualification = false }) {
    this.runtimeGeneration = runtimeGeneration;
    this.browserSession = browserSession;
    this.createView = createView;
    this.presentation = presentation;
    this.scriptHost = scriptHost;
    this.qualification = qualification;
    this.nextTabID = 1000;
    this.records = new Map();
    this.references = new Map();
    this.connections = new Map();
    this.changeListener = null;
    this.conversationPages = new Map();
    this.selectedConversationID = "";
  }

  setChangeListener(listener) {
    this.changeListener = typeof listener === "function" ? listener : null;
  }

  // workbench host pages retain one actual embedded view per local conversation.
  // A lease binds exactly one controller; releasing it never selects another
  // conversation or makes the page available to the legacy relay.
  acquireHostPage(binding) {
    const conversationID = binding.local_conversation_id;
    let record = this.conversationPages.get(conversationID);
    if (record && !record.destroyed && record.connectionID) throw new Error("Conversation already has a browser controller");
    const connectionID = binding.lease_id;
    const localBinding = {
      task_id: binding.local_task_id,
      session_id: `session_${binding.lease_id.replace(/^lease_/, "")}`,
      controller_generation: binding.page_generation,
      session_generation: binding.page_generation,
      page_generation: binding.page_generation,
    };
    this.registerConnection({ id: connectionID, binding: localBinding });
    if (!record || record.destroyed) {
      record = this.#createRecord({ role: "task", connectionID, url: "about:blank" });
      this.conversationPages.set(conversationID, record);
      record.hostInitialLoad = record.webContents.loadURL("about:blank");
    } else {
      record.connectionID = connectionID;
      record.binding = localBinding;
      this.requireConnection(connectionID).tabIDs.add(record.tabID);
    }
    // Local ownership survives releasing the remote controller's lease.
    record.hostConversationID = conversationID;
    record.hostBinding = Object.freeze({ ...binding });
    if (this.selectedConversationID === conversationID) this.presentation.showTask(record);
    this.#changed();
    return record;
  }

  requireHostPage(binding) {
    const record = this.conversationPages.get(binding.local_conversation_id);
    const expected = record?.hostBinding;
    const keys = ["owner_id", "client_id", "installation_id", "local_conversation_id", "local_task_id", "host_id", "runtime_generation", "connection_epoch", "lease_id", "page_id", "page_generation", "authorization_digest"];
    if (!record || record.destroyed || record.connectionID !== binding.lease_id || !expected || keys.some((key) => expected[key] !== binding[key])) throw new Error("Embedded page binding is stale");
    return this.get(binding.lease_id, record.tabID);
  }

  releaseHostPage(binding) {
    const record = this.requireHostPage(binding);
    const connection = this.connections.get(binding.lease_id);
    connection?.facade?.dispose();
    this.connections.delete(binding.lease_id);
    record.connectionID = null;
    record.binding = null;
    record.hostBinding = null;
    this.#changed();
  }

  closeHostPages(reason = "host_fenced") {
    for (const record of this.conversationPages.values()) {
      if (record.connectionID) this.closeConnection(record.connectionID, reason);
      else this.destroy(record.tabID, reason);
    }
    this.conversationPages.clear();
  }

  selectConversation(conversationID) {
    if (typeof conversationID !== "string" || conversationID && !/^[A-Za-z0-9][A-Za-z0-9_.:-]{0,159}$/u.test(conversationID)) throw new Error("Local conversation is invalid");
    this.selectedConversationID = conversationID;
    this.presentation.hidePresented();
    const record = this.conversationPages.get(conversationID);
    if (record && !record.destroyed) this.presentation.showTask(record);
    this.#changed();
    return record?.pageRef || "";
  }

  registerConnection(connection) {
    if (!connection?.id || this.connections.has(connection.id)) throw new Error("Task connection is invalid");
    connection.tabIDs = new Set();
    this.connections.set(connection.id, connection);
  }

  bindConnectionFacade(connectionID, facade) {
    const connection = this.requireConnection(connectionID);
    if (connection.facade) throw new Error("Task connection facade is already bound");
    connection.facade = facade;
  }

  async createInitialTaskPage(connectionID) {
    const record = this.#createRecord({
      role: "task",
      connectionID,
      url: ELECTRON_CONNECT_URL,
      initial: true,
    });
    await record.webContents.loadURL(ELECTRON_CONNECT_URL);
    if (this.qualification) this.presentation.showTask(record);
    this.#changed();
    return record;
  }

  createTaskPage(connectionID, url = "about:blank", openerTabID) {
    const record = this.#createRecord({ role: "task", connectionID, url, openerTabID });
    void record.webContents.loadURL(url || "about:blank").catch(() => this.destroy(record.tabID, "load_failed"));
    this.#changed();
    return record;
  }

  async createPersonalPage(url = "about:blank") {
    const record = this.#createRecord({ role: "personal", url });
    await record.webContents.loadURL(url);
    this.presentation.showPersonal(record);
    this.#changed();
    return record;
  }

  get(connectionID, tabID) {
    const record = this.records.get(tabID);
    const connection = this.connections.get(connectionID);
    if (!record || !connection || record.role !== "task" || record.connectionID !== connectionID ||
        record.runtimeGeneration !== this.runtimeGeneration || record.destroyed ||
        !connection.tabIDs.has(tabID) || record.binding.session_id !== connection.binding.session_id ||
        record.binding.session_generation !== connection.binding.session_generation) {
      throw new Error("Tab is outside the task allowlist");
    }
    return record;
  }

  getPersonal(pageRef) {
    const tabID = this.references.get(pageRef);
    const record = this.records.get(tabID);
    if (!record || record.role !== "personal" || record.destroyed ||
        record.runtimeGeneration !== this.runtimeGeneration) {
      throw new Error("Personal page is unavailable");
    }
    return record;
  }

  getByRef(pageRef) {
    const tabID = this.references.get(pageRef);
    const record = this.records.get(tabID);
    if (!record || record.destroyed || record.runtimeGeneration !== this.runtimeGeneration) {
      throw new Error("Page reference is unavailable");
    }
    return record;
  }

  recordForWebContents(webContentsID) {
    for (const record of this.records.values()) {
      if (!record.destroyed && record.webContents.id === webContentsID) return record;
    }
    return null;
  }

  publicTab(connectionID, tabID) {
    return publicTab(this.get(connectionID, tabID));
  }

  downloadOwner(webContentsID) {
    for (const record of this.records.values()) {
      if (!record.destroyed && record.role === "task" && record.webContents.id === webContentsID) {
        return record.connectionID;
      }
    }
    return null;
  }

  showTask(connectionID, tabID) {
    const record = this.get(connectionID, tabID);
    this.presentation.showTask(record);
    this.#changed();
    return record.pageRef;
  }

  hideObservedTask() {
    this.presentation.hideTask();
    this.#changed();
  }

  showByRef(pageRef, role) {
    const record = this.getByRef(pageRef);
    if (record.role !== role) throw new Error("Page role is invalid");
    if (record.hostConversationID && record.hostConversationID !== this.selectedConversationID) throw new Error("Page belongs to another conversation");
    if (role === "personal") this.presentation.showPersonal(record);
    else this.presentation.showTask(record);
    this.#changed();
    return record;
  }

  hidePresented() {
    if (this.presentation.presented?.role === "task") this.presentation.hideTask();
    else this.presentation.hidePresented();
    this.#changed();
  }

  async navigatePersonal(pageRef, url) {
    const record = this.getPersonal(pageRef);
    await record.webContents.loadURL(url);
    this.#changed();
    return record;
  }

  async personalNavigation(pageRef, action) {
    const record = this.getPersonal(pageRef);
    const history = record.webContents.navigationHistory;
    if (action === "back" && history.canGoBack()) history.goBack();
    else if (action === "forward" && history.canGoForward()) history.goForward();
    else if (action === "reload") record.webContents.reload();
    else if (!["back", "forward", "reload"].includes(action)) throw new Error("Navigation action is invalid");
    this.#changed();
    return record;
  }

  closePersonal(pageRef) {
    const record = this.getPersonal(pageRef);
    return this.destroy(record.tabID, "closed_by_owner");
  }

  async flushBrowserState() {
    this.browserSession.flushStorageData();
    await this.browserSession.cookies.flushStore();
  }

  desktopSnapshot() {
    return [...this.records.values()].filter((record) => !record.destroyed && !record.initial).map((record) => ({
      page_ref: record.pageRef,
      role: record.role,
      task_id: record.binding?.task_id ?? "",
      local_conversation_id: record.hostBinding?.local_conversation_id || [...this.conversationPages].find(([, page]) => page === record)?.[0] || "",
      page_generation: record.hostBinding?.page_generation || record.localGeneration,
      title: record.webContents.getTitle(),
      url: safeURL(record.webContents),
      presented: this.presentation.isPresented(record),
      loading: record.webContents.isLoading(),
      crashed: record.webContents.isCrashed(),
      can_go_back: record.role === "personal" && record.webContents.navigationHistory.canGoBack(),
      can_go_forward: record.role === "personal" && record.webContents.navigationHistory.canGoForward(),
    }));
  }

  openQualifiedDevTools(pageRef) {
    const record = this.getByRef(pageRef);
    if (!this.qualification || record.role !== "task") throw new Error("Qualification page is unavailable");
    record.webContents.openDevTools({ mode: "detach", activate: false });
    return new Promise((resolve) => setTimeout(() => resolve(record.webContents.isDevToolsOpened()), 100));
  }

  async triggerQualifiedDialog(pageRef) {
    const record = this.getByRef(pageRef);
    if (!this.qualification || record.role !== "task") throw new Error("Qualification page is unavailable");
    await record.webContents.executeJavaScript(
      "setTimeout(()=>document.querySelector('#dialog-state').textContent='Dialog: '+(confirm('blocked task dialog')?'accepted':'dismissed'),0)",
      true,
    );
  }

  crashQualifiedPage(pageRef) {
    const record = this.getByRef(pageRef);
    if (!this.qualification || record.role !== "task") throw new Error("Qualification page is unavailable");
    record.webContents.forcefullyCrashRenderer();
  }

  showQualified(pageRef) {
    const tabID = this.references.get(pageRef);
    const record = this.records.get(tabID);
    if (!this.qualification || !record || record.role !== "task" || record.destroyed) {
      throw new Error("Qualification page is unavailable");
    }
    this.presentation.showTask(record);
  }

  destroy(tabID, reason = "removed") {
    const record = this.records.get(tabID);
    if (!record || record.destroyed) return false;
    record.destroyed = true;
    record.localGeneration++;
    this.records.delete(tabID);
    this.references.delete(record.pageRef);
    for (const [id, page] of this.conversationPages) if (page === record) this.conversationPages.delete(id);
    this.presentation.remove(record);
    this.#changed();
    if (record.connectionID) {
      const connection = this.connections.get(record.connectionID);
      connection?.tabIDs.delete(tabID);
      connection?.facade?.notifyDebuggerDetach(record, reason);
      connection?.facade?.tabs.onRemoved.emit(tabID, { isWindowClosing: false });
    }
    if (!record.webContents.isDestroyed()) record.webContents.close({ waitForBeforeUnload: false });
    return true;
  }

  closeConnection(connectionID, reason = "connection_closed") {
    const connection = this.connections.get(connectionID);
    if (!connection) return;
    for (const tabID of [...connection.tabIDs]) this.destroy(tabID, reason);
    connection.facade?.dispose();
    this.connections.delete(connectionID);
  }

  snapshot() {
    return [...this.records.values()].map((record) => ({
      page_ref: record.pageRef,
      role: record.role,
      task_id: record.binding?.task_id,
      session_id: record.binding?.session_id,
      runtime_generation: record.runtimeGeneration,
      page_generation: record.binding?.page_generation ?? record.localGeneration,
      tab_id: record.tabID,
      url: safeURL(record.webContents),
      presented: this.presentation.isPresented(record),
      bounds: record.view.getBounds(),
      child_target_types: this.qualification ? [...record.childTargetTypes].sort() : undefined,
      dialog_dismissals: this.qualification ? record.dialogDismissals : undefined,
      dialog_error: this.qualification ? record.dialogError : undefined,
    }));
  }

  requireConnection(connectionID) {
    const connection = this.connections.get(connectionID);
    if (!connection) throw new Error("Task connection is unavailable");
    return connection;
  }

  #createRecord({ role, connectionID = null, url, openerTabID, initial = false }) {
    if (!ALLOWED_ROLES.has(role)) throw new Error("Page role is invalid");
    const connection = role === "task" ? this.requireConnection(connectionID) : null;
    const opener = openerTabID === undefined ? null : role === "task"
      ? this.get(connectionID, openerTabID)
      : this.records.get(openerTabID);
    if (openerTabID !== undefined && (!opener || opener.role !== role || opener.destroyed)) {
      throw new Error("Popup role is invalid");
    }
    const view = this.createView({ role, browserSession: this.browserSession });
    const record = {
      tabID: this.nextTabID++,
      pageRef: `page_${crypto.randomUUID().replaceAll("-", "")}`,
      view,
      webContents: view.webContents,
      role,
      connectionID,
      binding: connection?.binding ?? null,
      runtimeGeneration: this.runtimeGeneration,
      localGeneration: 1,
      openerTabID,
      initial,
      destroyed: false,
      attached: false,
      childSessions: new Set(),
      childTargetTypes: new Set(),
      dialogDismissals: 0,
      dialogError: "",
      requestedURL: url,
    };
    this.records.set(record.tabID, record);
    this.references.set(record.pageRef, record.tabID);
    connection?.tabIDs.add(record.tabID);
    this.presentation.add(record);
    this.scriptHost?.bind(record);
    this.#secureRecord(record);
    return record;
  }

  #secureRecord(record) {
    const { webContents } = record;
    webContents.on("will-navigate", (event, url) => {
      if (!record.hostConversationID) return;
      try { const target = new URL(url); if (target.protocol !== "https:" || target.username || target.password) event.preventDefault(); }
      catch { event.preventDefault(); }
    });
    webContents.on("will-redirect", (event, url) => {
      if (!record.hostConversationID) return;
      try { const target = new URL(url); if (target.protocol !== "https:" || target.username || target.password) event.preventDefault(); }
      catch { event.preventDefault(); }
    });
    webContents.on("will-attach-webview", (event) => event.preventDefault());
    webContents.on("devtools-opened", () => webContents.closeDevTools());
    webContents.on("context-menu", (event) => {
      if (record.role === "task") event.preventDefault();
    });
    webContents.on("will-prevent-unload", (event) => {
      if (record.role === "task") event.preventDefault();
    });
    webContents.on("select-bluetooth-device", (event, _devices, callback) => {
      if (record.role === "task") {
        event.preventDefault();
        callback("");
      }
    });
    webContents.on("before-input-event", (event) => {
      if (record.role === "task") event.preventDefault();
    });
    webContents.on("focus", () => {
      if (record.role === "task") this.presentation.redirectTaskFocus();
    });
    webContents.on("render-process-gone", () => {
      if (record.connectionID) this.closeConnection(record.connectionID, "render_process_gone");
      else this.destroy(record.tabID, "render_process_gone");
    });
    for (const eventName of ["did-start-loading", "did-stop-loading", "did-navigate", "did-navigate-in-page", "page-title-updated"]) {
      webContents.on(eventName, () => this.#changed());
    }
    if (record.role === "personal") {
      webContents.on("did-stop-loading", () => void this.flushBrowserState().catch(() => {}));
    }
    webContents.once("destroyed", () => {
      if (!record.destroyed) this.destroy(record.tabID, "target_closed");
    });
    webContents.setWindowOpenHandler((details) => {
      if (record.destroyed || record.hostConversationID) return { action: "deny" };
      queueMicrotask(() => {
        try {
          const popup = this.#createRecord({
            role: record.role,
            connectionID: record.connectionID,
            url: details.url,
            openerTabID: record.tabID,
          });
          void popup.webContents.loadURL(details.url || "about:blank")
            .catch(() => this.destroy(popup.tabID, "load_failed"));
          if (record.connectionID) {
            this.connections.get(record.connectionID)?.facade?.tabs.onCreated.emit(publicTab(popup));
          } else {
            this.presentation.showPersonal(popup);
          }
        } catch {
          // The opener or its connection may have disappeared before the queued creation.
        }
      });
      return { action: "deny" };
    });
  }

  #changed() {
    queueMicrotask(() => this.changeListener?.());
  }
}

export function publicTab(record) {
  return {
    id: record.tabID,
    windowId: 1,
    active: record.initial,
    pinned: false,
    url: safeURL(record.webContents) || record.requestedURL || "",
    title: record.webContents.getTitle?.() || "",
    ...(Number.isInteger(record.openerTabID) ? { openerTabId: record.openerTabID } : {}),
  };
}

function safeURL(webContents) {
  try {
    return webContents.isDestroyed() ? "" : webContents.getURL();
  } catch {
    return "";
  }
}
