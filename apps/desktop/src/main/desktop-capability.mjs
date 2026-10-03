const INVOKE_CHANNEL = "sparkclaw-desktop:invoke";
const STATE_CHANNEL = "sparkclaw-desktop:state";
const TRUSTED_ORIGIN = "sparkclaw-app://workbench";

export class DesktopCapability {
  constructor({ ipcMain, window, registry, presentation, browserServices, runtimeGeneration, authorizeSession = () => true, browserHost = null }) {
    this.ipcMain = ipcMain;
    this.window = window;
    this.registry = registry;
    this.presentation = presentation;
    this.browserServices = browserServices;
    this.runtimeGeneration = runtimeGeneration;
    this.authorizeSession = authorizeSession;
    this.browserHost = browserHost;
    this.revision = 1;
    this.layoutRevision = 0;
    this.emitQueued = false;
  }

  start() {
    this.ipcMain.handle(INVOKE_CHANNEL, (event, request) => this.#invoke(event, request));
    this.registry.setChangeListener(() => this.changed());
    return this;
  }

  close() {
    this.registry.setChangeListener(null);
    this.ipcMain.removeHandler(INVOKE_CHANNEL);
  }

  changed() {
    this.revision++;
    if (this.emitQueued) return;
    this.emitQueued = true;
    queueMicrotask(() => {
      this.emitQueued = false;
      if (!this.window.webContents.isDestroyed()) this.window.webContents.send(STATE_CHANNEL, this.snapshot());
    });
  }

  snapshot() {
    return Object.freeze({
      schema_version: 1,
      capability_version: 1,
      runtime_kind: "electron",
      runtime_generation: this.runtimeGeneration,
      revision: this.revision,
      pages: this.registry.desktopSnapshot(),
      browser_host: this.browserHost?.snapshot() || { state: "unavailable" },
      ...this.browserServices.snapshot(),
      presentation: this.presentation.status(),
    });
  }

  async #invoke(event, request) {
    this.#authorize(event);
    if (!this.authorizeSession()) throw new Error("Desktop session is locked");
    if (!plainObject(request) || request.schema_version !== 1 || typeof request.operation !== "string") {
      throw new Error("Desktop capability request is invalid");
    }
    switch (request.operation) {
      case "selectConversation":
        exactKeys(request, ["operation", "schema_version", "local_conversation_id"]);
        return { page_ref: this.registry.selectConversation(request.local_conversation_id) };
      case "reconcileBrowserHost":
        exactKeys(request, ["operation", "schema_version", "command_id", "digest", "outcome"]);
        if (!this.browserHost) throw new Error("Browser host is unavailable");
        return this.browserHost.reconcile(request.command_id, request.digest, request.outcome);
      case "grantBrowserHost":
        exactKeys(request, ["operation", "schema_version"]);
        if (!this.browserHost) throw new Error("Browser host is unavailable");
        return this.browserHost.grant();
      case "state":
        exactKeys(request, ["operation", "schema_version"]);
        return this.snapshot();
      case "createPersonal": {
        exactKeys(request, ["operation", "schema_version"], ["url"]);
        const url = request.url === undefined ? "about:blank" : personalURL(request.url);
        const record = await this.registry.createPersonalPage(url);
        return { page_ref: record.pageRef };
      }
      case "navigatePersonal": {
        exactKeys(request, ["operation", "page_ref", "schema_version", "url"]);
        await this.registry.navigatePersonal(pageRef(request.page_ref), personalURL(request.url));
        return { completed: true };
      }
      case "personalNavigation":
        exactKeys(request, ["action", "operation", "page_ref", "schema_version"]);
        await this.registry.personalNavigation(pageRef(request.page_ref), navigationAction(request.action));
        return { completed: true };
      case "closePersonal":
        exactKeys(request, ["operation", "page_ref", "schema_version"]);
        this.registry.closePersonal(pageRef(request.page_ref));
        return { completed: true };
      case "presentPersonal":
        exactKeys(request, ["operation", "page_ref", "schema_version"]);
        this.registry.showByRef(pageRef(request.page_ref), "personal");
        return { completed: true };
      case "observeTask":
        exactKeys(request, ["operation", "page_ref", "schema_version"]);
        this.registry.showByRef(pageRef(request.page_ref), "task");
        return { completed: true };
      case "hideBrowser":
        exactKeys(request, ["operation", "schema_version"]);
        this.registry.hidePresented();
        return { completed: true };
      case "setBounds":
        exactKeys(request, ["bounds", "operation", "revision", "schema_version"]);
        this.#setBounds(request.bounds, request.revision);
        return { completed: true };
      case "respondPermission":
        exactKeys(request, ["allow", "operation", "permission_ref", "schema_version"]);
        this.browserServices.respondPermission(permissionRef(request.permission_ref), request.allow);
        return { completed: true };
      case "cancelDownload":
        exactKeys(request, ["download_ref", "operation", "schema_version"]);
        this.browserServices.cancelDownload(downloadRef(request.download_ref));
        return { completed: true };
      case "showDownload":
        exactKeys(request, ["download_ref", "operation", "schema_version"]);
        this.browserServices.showDownload(downloadRef(request.download_ref));
        return { completed: true };
      default:
        throw new Error("Desktop capability operation is unavailable");
    }
  }

  #authorize(event) {
    const frame = event.senderFrame;
    if (event.sender !== this.window.webContents || frame !== this.window.webContents.mainFrame ||
        !frame || trustedOrigin(frame.url) !== TRUSTED_ORIGIN) {
      throw new Error("Desktop capability sender is not trusted");
    }
  }

  #setBounds(value, revision) {
    if (!Number.isSafeInteger(revision) || revision <= this.layoutRevision || !plainObject(value) ||
        ![value.x, value.y, value.width, value.height].every(Number.isSafeInteger)) {
      throw new Error("Desktop browser bounds are invalid");
    }
    const content = this.window.getContentBounds();
    if (value.x < 0 || value.y < 0 || value.width < 1 || value.width > 760 || value.height < 1 ||
        value.x + value.width > content.width || value.y + value.height > content.height) {
      throw new Error("Desktop browser bounds are outside the workbench");
    }
    this.layoutRevision = revision;
    this.presentation.setPanelBounds(value);
    this.changed();
  }
}

function trustedOrigin(raw) {
  try {
    const url = new URL(raw);
    return `${url.protocol}//${url.host}`;
  } catch {
    return "";
  }
}

function personalURL(value) {
  if (typeof value !== "string" || value.length > 4096) throw new Error("Personal URL is invalid");
  const url = new URL(value);
  if (url.protocol !== "https:" || url.username || url.password) throw new Error("Personal URL must use HTTPS");
  return url.toString();
}

function pageRef(value) {
  if (typeof value !== "string" || !/^page_[a-f0-9]{32}$/u.test(value)) throw new Error("Page reference is invalid");
  return value;
}

function navigationAction(value) {
  if (!["back", "forward", "reload"].includes(value)) throw new Error("Navigation action is invalid");
  return value;
}

function permissionRef(value) {
  if (typeof value !== "string" || !/^permission_[a-f0-9]{32}$/u.test(value)) throw new Error("Permission reference is invalid");
  return value;
}

function downloadRef(value) {
  if (typeof value !== "string" || !/^download_[a-f0-9]{32}$/u.test(value)) throw new Error("Download reference is invalid");
  return value;
}

function exactKeys(value, required, optional = []) {
  const allowed = new Set([...required, ...optional]);
  if (required.some((key) => !(key in value)) || Object.keys(value).some((key) => !allowed.has(key))) {
    throw new Error("Desktop capability request fields are invalid");
  }
}

function plainObject(value) {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}
