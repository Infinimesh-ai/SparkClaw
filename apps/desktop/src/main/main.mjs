import crypto from "node:crypto";
import fs from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

import {
  app,
  BrowserWindow,
  clipboard,
  dialog,
  ipcMain,
  Menu,
  net as electronNet,
  powerMonitor,
  protocol,
  safeStorage,
  session,
  shell,
  WebContentsView,
} from "electron";

import { ElectronAdapterServer } from "../browser/adapter-server.mjs";
import { ManagedScriptHost } from "../browser/managed-script-host.mjs";
import { PageRegistry } from "../browser/page-registry.mjs";
import { BrowserHostAgent } from "../browser/host-agent.mjs";
import { adapterSecretPath, adapterSocketPath } from "../browser/protocol.mjs";
import { BrowserPresentation } from "./presentation.mjs";
import { DesktopCapability, isDesktopSessionAuthorized } from "./desktop-capability.mjs";
import { DesktopAuth, authorizeWorkbenchSender } from "./desktop-auth.mjs";
import { SecureCredentialStore } from "./secure-credential-store.mjs";
import { OwnerBrowserServices } from "./owner-browser-services.mjs";
import { linuxLoginStartup } from "./login-startup.mjs";
import { resolveDesktopConnectionPaths } from "./connection-paths.mjs";
import { ClientStore } from "./client-store.mjs";
import { ExecutionClient } from "./execution-client.mjs";
import { ScheduleClient } from "./schedule-client.mjs";
import { MailSyncStore } from "./mail-sync-store.mjs";
import { MailSyncClient } from "./mail-sync-client.mjs";
import { MailSyncCapability } from "./mail-sync-capability.mjs";
import { ClientStoreCapability } from "./client-store-capability.mjs";
import { exportLocalFile } from "./export-local-file.mjs";
import { ISCPEventClient } from "./iscp-event-client.mjs";
import { ISCPSpeechClient } from "./iscp-audio-client.mjs";
import { ISCPRecordingClient } from "./iscp-recording-client.mjs";
import { configureWorkbenchPermissions } from "./workbench-permissions.mjs";
import { proxyAPIAllowed } from "./workbench-proxy-policy.mjs";
import { bindWorkbenchActivation } from "./workbench-activation.mjs";
import { resolveISCPLaunchProfile } from "./iscp-launch-profile.mjs";

const MODULE_DIR = path.dirname(fileURLToPath(import.meta.url));
const DESKTOP_ICON_PATH = path.join(MODULE_DIR, "..", "assets", "icon.png");
const workbenchQualification = process.argv.includes("--qualification-workbench");
const qualification = process.argv.includes("--qualification") || workbenchQualification;
const defaultUserData = app.getPath("userData");
const { configuredUserData, iscpProfilePath, allowLocalISCPTest } = resolveISCPLaunchProfile({ defaultUserData, qualification });
if (configuredUserData) {
  if (!path.isAbsolute(configuredUserData)) throw new Error("Desktop user-data path must be absolute");
  app.setPath("userData", configuredUserData);
}

protocol.registerSchemesAsPrivileged([{
  scheme: "sparkclaw-internal",
  privileges: { standard: true, secure: true, supportFetchAPI: false, corsEnabled: false },
}]);
protocol.registerSchemesAsPrivileged([{
  scheme: "sparkclaw-app",
  privileges: { standard: true, secure: true, supportFetchAPI: true, corsEnabled: true },
}]);

if (!app.requestSingleInstanceLock()) app.quit();

let quitting = false;
let suspended = false;
let window;
let adapter;
let scriptHost;
let desktopCapability;
let ownerBrowserServices;
let registry;
let localStore;
let localStoreCapability;
let desktopAuth;
let executionClient;
let mailStore;
let mailClient;
let mailCapability;
let browserHost;
let scheduleClient;
let browserHostPreparation = Promise.resolve();

void app.whenReady().then(start).catch((error) => {
  process.stderr.write(`SparkX Electron failed: ${error instanceof Error ? error.message : "unknown error"}\n`);
  app.exit(1);
});

let iscpEvents;
let iscpSpeech;
let iscpRecording;

async function start() {
  const internalProtocolHandler = (request) => {
    const url = new URL(request.url);
    if (url.hostname !== "extension-connect" || url.pathname !== "") {
      return new Response("Not found", { status: 404, headers: { "content-type": "text/plain" } });
    }
    return new Response("<!doctype html><meta charset=utf-8><title>SparkX task</title>", {
      headers: {
        "content-type": "text/html; charset=utf-8",
        "content-security-policy": "default-src 'none'",
      },
    });
  };

  // Qualification already receives a disposable, per-run userData directory. Keep
  // the partition name stable inside that directory so a forced main-process
  // restart exercises the same persistence boundary as production.
  const profileSuffix = qualification ? "qualification" : "default";
  const workbenchSession = session.fromPartition(`persist:sparkclaw-workbench-${profileSuffix}`);
  const browserSession = session.fromPartition(`persist:sparkclaw-browser-${profileSuffix}`);
  secureSession(workbenchSession);
  secureSession(browserSession);
  protocol.handle("sparkclaw-internal", internalProtocolHandler);
  browserSession.protocol.handle("sparkclaw-internal", internalProtocolHandler);
  const qualificationPaths = qualification ? await localBackendPaths().catch(() => undefined) : undefined;
  if (!qualification) localStore = new ClientStore(path.join(app.getPath("userData"), "workbench"));
  desktopAuth = new DesktopAuth({
    installationID: localStore?.installationID,
    vault: new SecureCredentialStore({ directory: path.join(app.getPath("userData"), "authentication"), safeStorage }),
    descriptorPath: path.join(app.getPath("userData"), "backend.json"),
    qualificationPaths,
    qualification,
    requireLAN: process.platform === "darwin" && !qualification,
    fetcher: electronNet.fetch,
    iscpProfilePath, allowLocalISCPTest, packaged: app.isPackaged, resourcesPath: process.resourcesPath,
    onChange: (status) => {
      if (quitting) return;
      if (status.state === "connected" && !suspended) {
        executionClient?.start();
        if(status.capabilities?.events)iscpEvents?.start();else iscpEvents?.close();
        scheduleClient?.start();
        if (status.backend?.transport !== "iscp" || status.capabilities?.mail === true) mailClient?.start();
        else mailClient?.close();
        if (status.backend?.transport !== "iscp" || status.capabilities?.browser === true) void prepareBrowserHostScope().catch(() => {});
        else void browserHost?.suspend();
      } else {
        executionClient?.close();
        iscpEvents?.close();
        iscpSpeech?.close();
        iscpRecording?.close();
        scheduleClient?.close();
        mailClient?.close();
        void browserHost?.suspend();
      }
      if (window && !window.isDestroyed()) window.webContents.send("sparkclaw-local-backend:state", status);
    },
    onLock: async () => {
      executionClient?.close();
      iscpEvents?.close();
      iscpSpeech?.close();
      iscpRecording?.close();
      scheduleClient?.close();
      mailClient?.close();
      await browserHost?.stop();
      for (const [id] of registry?.connections || []) registry.closeConnection(id, "authentication_locked");
      await adapter?.close().catch(() => {});
      adapter = undefined;
    },
  });
  await desktopAuth.initialize();
  const trustedHandler = (channel, handler) => ipcMain.handle(channel, (event, ...args) => {
    authorizeWorkbenchSender(event, window);
    return handler(...args);
  });
  trustedHandler("sparkclaw-local-backend:status", () => desktopAuth.status);
  trustedHandler("sparkclaw-local-backend:retry", () => desktopAuth.retry());
  trustedHandler("sparkclaw-local-backend:configure", (descriptor) => desktopAuth.configure(descriptor));
  trustedHandler("sparkclaw-local-backend:login", (token) => desktopAuth.login(token));
  trustedHandler("sparkclaw-local-backend:enroll", (credential) => desktopAuth.enroll(credential));
  trustedHandler("sparkclaw-local-backend:connection-credential", (token) => desktopAuth.connectionCredential(token));
  trustedHandler("sparkclaw-local-backend:check-new-authorization", () => desktopAuth.checkNewAuthorization());
  trustedHandler("sparkclaw-local-backend:delete-authorization", () => desktopAuth.deleteAuthorization());
  trustedHandler("sparkclaw-local-backend:logout", () => desktopAuth.logout());
  iscpSpeech = new ISCPSpeechClient(desktopAuth);
  trustedHandler("sparkclaw-speech:stream", (request) => iscpSpeech.dispatch(request));
  iscpRecording = new ISCPRecordingClient(desktopAuth);
  trustedHandler("sparkclaw-speech:transcribe", (request) => iscpRecording.transcribe(request));
  trustedHandler("sparkclaw-speech:cancel-recording", (request) => iscpRecording.cancel(request));
  trustedHandler("sparkclaw-desktop:login-startup", async (enabled) => {
    if (!app.isPackaged) return { supported: false, enabled: false };
    if (typeof enabled !== "undefined" && typeof enabled !== "boolean") throw new TypeError("Invalid login startup value");
    if (process.platform === "linux") {
      const executable = process.env.APPIMAGE && path.isAbsolute(process.env.APPIMAGE) ? process.env.APPIMAGE : process.execPath;
      return linuxLoginStartup({ home: app.getPath("home"), executable, enabled });
    }
    if (process.platform === "darwin" || process.platform === "win32") {
      if (typeof enabled === "boolean") app.setLoginItemSettings({ openAtLogin: enabled });
      return { supported: true, enabled: app.getLoginItemSettings().openAtLogin };
    }
    return { supported: false, enabled: false };
  });
  const speechOrigin = desktopAuth.descriptor?.origin || "http://127.0.0.1:18790";
  workbenchSession.protocol.handle("sparkclaw-app", workbenchProtocolHandler(webchatDistPath(), desktopAuth));

  window = new BrowserWindow({
    title: qualification ? "SparkX Electron Qualification" : "SparkX",
    icon: DESKTOP_ICON_PATH,
    width: 1440,
    height: 900,
    minWidth: 1180,
    minHeight: 720,
    x: qualification ? 0 : undefined,
    y: qualification ? 0 : undefined,
    frame: !qualification,
    show: false,
    backgroundColor: "#111827",
    webPreferences: {
      session: workbenchSession,
      preload: path.join(MODULE_DIR, "..", "preload", "workbench.cjs"),
      sandbox: true,
      contextIsolation: true,
      nodeIntegration: false,
      webSecurity: true,
      additionalArguments: [`--sparkclaw-speech-base=${speechOrigin}`, ...(!qualification ? ["--sparkclaw-client-store=1"] : [])],
    },
  });
  if (!qualification) {
    const localChanged = () => {
      if (window && !window.isDestroyed()) window.webContents.send("sparkclaw-client-store:changed");
    };
    executionClient = new ExecutionClient({ auth: desktopAuth, store: localStore, getIdentity: localIdentity, onChange: localChanged }).start();
    iscpEvents = new ISCPEventClient({ auth: desktopAuth, store: localStore, execution: executionClient, getIdentity: localIdentity,
      onEvents: (event) => { localChanged(); if(window&&!window.isDestroyed())window.webContents.send("sparkclaw-backend-events",event); } });
    if(desktopAuth.status.capabilities?.events)iscpEvents.start();
    scheduleClient = new ScheduleClient({ auth: desktopAuth, store: localStore, execution: executionClient, getIdentity: localIdentity, onChange: localChanged }).start();
    {
    mailStore = new MailSyncStore(path.join(app.getPath("userData"), "workbench", "mail"));
    // DesktopAuth binds this installation before publishing connected state.
    mailClient = new MailSyncClient({
      store: mailStore, localStore, getConnection: () => desktopAuth.connection,
      getFetch: () => desktopAuth.authorizedFetch.bind(desktopAuth),
      getFileFetch: () => desktopAuth.authorizedMailFileFetch.bind(desktopAuth),
      installationID: localStore.installationID,
    });
    if (desktopAuth.status.state === "connected" && (desktopAuth.descriptor?.transport !== "iscp" || desktopAuth.status.capabilities?.mail === true)) mailClient.start();
    else mailClient.close();
    mailCapability = new MailSyncCapability({ ipcMain, window, client: mailClient, getCapabilities: () => desktopAuth.status.capabilities }).start();
    }
    localStoreCapability = new ClientStoreCapability({
      ipcMain, window, store: localStore, execution: executionClient, schedules: scheduleClient,
      getIdentity: localIdentity,
      getCapabilities: () => desktopAuth.status.capabilities,
      exportFile: (file) => exportLocalFile(file, { dialog, window }),
    }).start();
  }
  window.on("close", (event) => {
    if (!quitting) {
      event.preventDefault();
      window.hide();
    }
  });
  window.webContents.on("will-attach-webview", (event) => event.preventDefault());
  window.webContents.setWindowOpenHandler(() => ({ action: "deny" }));
  configureWorkbenchSession(workbenchSession, window, speechOrigin);
  const workbenchURL = qualification && !workbenchQualification
    ? workbenchDocument()
    : "sparkclaw-app://workbench/index.html";
  window.webContents.on("render-process-gone", () => {
    if (!quitting) void window.loadURL(workbenchURL);
  });
  powerMonitor.on("suspend", () => {
    suspended = true;
    desktopAuth.suspend();
    executionClient?.close();
    scheduleClient?.close();
    mailClient?.close();
    void browserHost?.suspend();
  });
  powerMonitor.on("resume", () => {
    suspended = false;
    void desktopAuth.retry();
  });

  const qualificationOrigin = qualificationURLOrigin(process.env.SPARKCLAW_DESKTOP_QUALIFICATION_ORIGIN);
  scriptHost = new ManagedScriptHost({
    browserSession,
    ipcMain,
    Menu,
    preloadPath: path.join(MODULE_DIR, "..", "preload", "managed-scripts.cjs"),
    manifestPath: path.join(MODULE_DIR, "..", "browser", "managed-scripts.json"),
    storagePath: path.join(app.getPath("userData"), "managed-scripts", "values.json"),
    qualification,
    qualificationOrigin,
  });
  await scriptHost.prepare();

  const shieldView = createShieldView(workbenchSession);
  await shieldView.webContents.loadURL(shieldDocument());
  const presentation = new BrowserPresentation({ window, shieldView });
  const runtimeGeneration = crypto.randomBytes(16).toString("hex");
  registry = new PageRegistry({
    runtimeGeneration,
    browserSession,
    createView: ({ role }) => createBrowserView(browserSession, role),
    presentation,
    scriptHost,
    qualification,
  });
  scriptHost.setRegistry(registry);
  if (!qualification) {
    browserHost = new BrowserHostAgent({
      auth: desktopAuth, registry, userDataDir: app.getPath("userData"),
      onChange: () => desktopCapability?.changed(),
    });
    await prepareBrowserHostScope().catch(() => {});
  }
  ownerBrowserServices = await new OwnerBrowserServices({
    browserSession,
    workbenchSession,
    registry,
    workbench: window,
    downloadsRoot: qualification
      ? path.join(app.getPath("userData"), "owner-downloads")
      : path.join(app.getPath("downloads"), "SparkX"),
    shell,
    onChange: () => desktopCapability?.changed(),
  }).start();
  desktopCapability = new DesktopCapability({
    ipcMain,
    window,
    registry,
    presentation,
    browserServices: ownerBrowserServices,
    browserHost: browserHost ? {
      snapshot: () => browserHost.snapshot(),
      grant: async () => {
        await prepareBrowserHostScope();
        return browserHost.grant();
      },
      reconcile: async (...args) => {
        await prepareBrowserHostScope();
        return browserHost.reconcile(...args);
      },
    } : null,
    runtimeGeneration,
    authorizeSession: () => qualification || isDesktopSessionAuthorized(desktopAuth),
    getCapabilities: () => desktopAuth?.status.capabilities,
  }).start();
  // The UDS adapter is a frozen Linux qualification transport. Production
  // browser execution uses the explicitly granted outbound Host transport. macOS
  // never reads Linux adapter secrets or starts the qualification owner service.
  if (qualification && process.platform === "linux") {
  adapter = new ElectronAdapterServer({
    socketPath: adapterSocketPath(),
    secretPath: adapterSecretPath(),
    downloadRoot: path.join(app.getPath("userData"), "task-downloads"),
    runtimeGeneration,
    registry,
    browserSession,
    scriptHost,
    clipboard,
    lifecycle: qualificationLifecycle,
    qualification,
  });
  await adapter.start();
  }
  await window.loadURL(workbenchURL);
  const workbenchEvidence = workbenchQualification ? await waitForWorkbench() : undefined;

  window.show();
  process.stdout.write(`${JSON.stringify({
    event: "sparkclaw_electron_ready",
    runtime_kind: "electron",
    runtime_generation: runtimeGeneration,
    ...(adapter ? { adapter_socket: adapterSocketPath() } : {}),
    browser_execution: adapter ? "legacy_qualification" : "host_grant_required",
    electron_version: process.versions.electron,
    chromium_version: process.versions.chrome,
    node_version: process.versions.node,
    architecture: process.arch,
    qualification,
    ...(workbenchQualification ? workbenchEvidence : {}),
  })}\n`);

  bindWorkbenchActivation(app, () => window);
  app.on("window-all-closed", () => {});
  app.on("before-quit", () => { quitting = true; desktopAuth.close(); executionClient?.close(); scheduleClient?.close(); mailClient?.close(); });
  process.on("SIGINT", () => void shutdown());
  process.on("SIGTERM", () => void shutdown());
}

async function waitForWorkbench() {
  const deadline = Date.now() + 8000;
  while (Date.now() < deadline) {
    const ready = await window.webContents.executeJavaScript(
      "Boolean(document.querySelector('.workbench') && window.sparkclawDesktop?.runtimeKind === 'electron')",
      true,
    ).catch(() => false);
    if (ready) {
      const connectivity = await window.webContents.executeJavaScript(`(async()=>{
        const gateway = await fetch('/desktop-gateway/desktop-probe', {
          method: 'POST', headers: {'Content-Type': 'text/plain'}, body: 'request-body'
        }).then(response=>response.text());
        const sse = await new Promise((resolve,reject)=>{
          const events = new EventSource('/desktop-gateway/desktop-sse');
          const timer = setTimeout(()=>{events.close();reject(new Error('sse timeout'))},3000);
          events.onmessage = event=>{clearTimeout(timer);events.close();resolve(event.data)};
          events.onerror = ()=>{clearTimeout(timer);events.close();reject(new Error('sse failed'))};
        });
        const speech = await new Promise((resolve,reject)=>{
          const base = new URL(window.sparkclawDesktop.speechBase);
          base.protocol = base.protocol === 'https:' ? 'wss:' : 'ws:';
          base.pathname = '/desktop-speech';
          const socket = new WebSocket(base);
          const timer = setTimeout(()=>{socket.close();reject(new Error('speech websocket timeout'))},3000);
          socket.onmessage = event=>{clearTimeout(timer);socket.close();resolve(event.data)};
          socket.onerror = ()=>{clearTimeout(timer);reject(new Error('speech websocket failed'))};
        });
        return {gateway, sse, speech};
      })()`, true);
      if (connectivity.gateway !== "gateway:package-proof:request-body" ||
          connectivity.sse !== "stream-ok" || connectivity.speech !== "speech-ok") {
        throw new Error("Packaged workbench proxy qualification failed");
      }
      const desktopUI = await window.webContents.executeJavaScript(`(async()=>{
        document.querySelector('.rightSidebarToggle')?.click();
        await new Promise(resolve=>setTimeout(resolve,100));
        const panel = Boolean(document.querySelector('.desktopBrowserPanel'));
        document.querySelector('.desktopToolLauncher button:not(:disabled)')?.click();
        await new Promise(resolve=>setTimeout(resolve,50));
        document.querySelector('.desktopNewTab')?.click();
        let state;
        for(let attempt=0;attempt<40;attempt++){
          state=await window.sparkclawDesktop.state();
          if(state.pages.some(page=>page.role==='personal') && state.presentation.panel_bounds.width>=480)break;
          await new Promise(resolve=>setTimeout(resolve,50));
        }
        const personal=state.pages.find(page=>page.role==='personal');
        if(personal)await window.sparkclawDesktop.closePersonal(personal.page_ref);
        return {panel, personal:Boolean(personal), bounded:state.presentation.panel_bounds.width>=480};
      })()`, true);
      if (!desktopUI.panel || !desktopUI.personal || !desktopUI.bounded) {
        throw new Error("Packaged desktop browser panel qualification failed");
      }
      return {
        workbench_loaded: true,
        desktop_browser_panel: true,
        desktop_ipc_bounded: true,
        gateway_http_proxy: true,
        gateway_sse_proxy: true,
        speech_websocket_proxy: true,
      };
    }
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
  throw new Error("Packaged workbench did not initialize");
}

async function shutdown() {
  desktopAuth?.close();
  if (quitting) return;
  quitting = true;
  executionClient?.close();
  scheduleClient?.close();
  mailClient?.close();
  mailCapability?.close();
  mailStore?.close();
  await browserHost?.stop();
  localStoreCapability?.close();
  localStore?.close();
  await adapter?.close().catch(() => {});
  await registry?.flushBrowserState().catch(() => {});
  await scriptHost?.close().catch(() => {});
  desktopCapability?.close();
  ownerBrowserServices?.close();
  app.quit();
}

async function qualificationLifecycle(action) {
  if (!qualification || !window) throw new Error("Qualification lifecycle is unavailable");
  if (action === "close") {
    window.close();
    await new Promise((resolve) => setImmediate(resolve));
  } else if (action === "show") {
    window.show();
    window.focus();
    await new Promise((resolve) => setImmediate(resolve));
  } else if (action === "crash_workbench") {
    window.webContents.forcefullyCrashRenderer();
    await new Promise((resolve) => setTimeout(resolve, 100));
  } else if (action === "flush_browser_state") {
    await registry.flushBrowserState();
  }
  return {
    visible: window.isVisible(),
    workbench_crashed: window.webContents.isCrashed(),
    workbench_url: window.webContents.getURL(),
  };
}

function qualificationURLOrigin(value) {
  if (!qualification || !value) return "";
  const url = new URL(value);
  if (url.origin !== value || url.protocol !== "http:" || !["127.0.0.1", "[::1]", "localhost"].includes(url.hostname)) {
    throw new Error("Qualification origin is invalid");
  }
  return value;
}

function createBrowserView(browserSession, role) {
  const view = new WebContentsView({
    webPreferences: {
      session: browserSession,
      sandbox: true,
      contextIsolation: true,
      nodeIntegration: false,
      webSecurity: true,
      allowRunningInsecureContent: false,
      backgroundThrottling: false,
      spellcheck: role === "personal",
    },
  });
  view.setBackgroundColor("#ffffff");
  return view;
}

function createShieldView(shieldSession) {
  const view = new WebContentsView({
    webPreferences: {
      session: shieldSession,
      sandbox: true,
      contextIsolation: true,
      nodeIntegration: false,
      webSecurity: true,
      backgroundThrottling: false,
    },
  });
  view.setBackgroundColor("#00000000");
  return view;
}

function secureSession(targetSession) {
  targetSession.setPermissionCheckHandler(() => false);
  targetSession.setPermissionRequestHandler((_webContents, _permission, callback) => callback(false));
  targetSession.setDevicePermissionHandler(() => false);
}

function configureWorkbenchSession(targetSession, targetWindow, speechOrigin) {
  configureWorkbenchPermissions(targetSession, targetWindow, () => desktopAuth?.status.state === "connected" && (desktopAuth.descriptor?.transport !== "iscp" || desktopAuth.status.capabilities?.speech === true));
  targetSession.webRequest.onBeforeRequest({ urls: ["http://*/*", "https://*/*", "ws://*/*", "wss://*/*"] }, (details, callback) => {
    callback({ cancel: !qualification || desktopAuth.status.state !== "connected" || canonicalOrigin(details.url) !== speechOrigin.replace(/^http/u, "ws") });
  });
  targetSession.webRequest.onBeforeSendHeaders({
    urls: [`${speechOrigin.replace(/^http/u, "ws")}/*`],
  }, (details, callback) => {
    const requestHeaders = { ...details.requestHeaders, Origin: speechOrigin };
    callback({ requestHeaders });
  });
}

function canonicalOrigin(raw) {
  try {
    const url = new URL(raw);
    return `${url.protocol}//${url.host}`;
  } catch {
    return "";
  }
}

function webchatDistPath() {
  const configured = process.env.SPARKCLAW_DESKTOP_WEBCHAT_DIST?.trim();
  if (configured) {
    if (!path.isAbsolute(configured)) throw new Error("Desktop WebChat distribution path must be absolute");
    return configured;
  }
  return app.isPackaged
    ? path.join(process.resourcesPath, "webchat")
    : path.resolve(MODULE_DIR, "../../../webchat/dist");
}

function localBackendPaths() {
  return resolveDesktopConnectionPaths({
    home: app.getPath("home"),
    packaged: app.isPackaged,
    moduleDirectory: MODULE_DIR,
  });
}

function workbenchProtocolHandler(root, auth) {
  return async (request) => {
    try {
      const url = new URL(request.url);
      if (url.hostname !== "workbench") return new Response("Not found", { status: 404 });
      const origin = auth.descriptor?.origin || "http://127.0.0.1:18790";
      const proxy = proxyTarget(url, origin);
      if (proxy) {
        const state = auth.status.state;
        if (state === "identity_conflict") return new Response("Backend identity conflict", { status: 409 });
        if (["invalid_authentication", "locked", "secure_storage_unavailable"].includes(state)) return new Response("Desktop authentication is locked", { status: 401 });
        if (state !== "connected") return new Response("Backend is unavailable", { status: 503 });
        // Desktop conversations stay in the local Store; proxy only supported services.
        if (!qualification && !proxyAPIAllowed(new URL(proxy).pathname)) return new Response("Service is unavailable in this workbench", { status: 501 });
        try { return await proxyWorkbenchRequest(auth, request, proxy); }
        catch { return new Response("Backend is unavailable", { status: 503 }); }
      }
      const pathname = decodeURIComponent(url.pathname === "/" ? "/index.html" : url.pathname);
      const filename = path.resolve(root, `.${pathname}`);
      if (!filename.startsWith(`${path.resolve(root)}${path.sep}`)) return new Response("Not found", { status: 404 });
      const content = await fs.readFile(filename);
      return new Response(content, { headers: workbenchHeaders(filename, qualification ? origin : "") });
    } catch (error) {
      return new Response(error?.code === "ENOENT" ? "WebChat build not found" : "Not found", {
        status: error?.code === "ENOENT" ? 503 : 404,
        headers: { "content-type": "text/plain; charset=utf-8" },
      });
    }
  };
}

function proxyTarget(url, origin) {
  const prefix = "/desktop-gateway";
  if (url.pathname === prefix || url.pathname.startsWith(`${prefix}/`)) {
    return `${origin}${url.pathname.slice(prefix.length) || "/"}${url.search}`;
  }
  return "";
}

async function proxyWorkbenchRequest(auth, request, target) {
  const headers = new Headers(request.headers);
  headers.delete("origin");
  headers.delete("referer");
  headers.delete("proxy-authorization");
  headers.delete("authorization");
  headers.delete("cookie");
  const method = request.method.toUpperCase();
  const body = method === "GET" || method === "HEAD" ? undefined : await request.arrayBuffer();
  const response = await auth.authorizedFetch(target, { method, headers, body, redirect: "manual", signal: request.signal });
  if (response.status >= 300 && response.status < 400) {
    return new Response("Cross-origin redirects are not allowed", { status: 502 });
  }
  return response;
}

function workbenchHeaders(filename, speechOrigin) {
  const extension = path.extname(filename).toLowerCase();
  const types = {
    ".css": "text/css; charset=utf-8",
    ".html": "text/html; charset=utf-8",
    ".js": "text/javascript; charset=utf-8",
    ".json": "application/json; charset=utf-8",
    ".png": "image/png",
    ".svg": "image/svg+xml",
    ".woff2": "font/woff2",
  };
  return {
    "content-type": types[extension] ?? "application/octet-stream",
    "content-security-policy": `default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self' data:; media-src 'self' blob:; connect-src 'self' ${speechOrigin.replace(/^http/u, "ws")}; object-src 'none'; frame-src 'none'; base-uri 'none'; form-action 'none'`,
    "x-content-type-options": "nosniff",
  };
}

function workbenchDocument() {
  const html = `<!doctype html><meta charset="utf-8"><meta name="color-scheme" content="dark">
    <meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'">
    <style>html,body{margin:0;background:#111827;color:#e5e7eb;font:14px system-ui;height:100%}
    main{padding:24px;width:700px}h1{font-size:20px}code{color:#93c5fd}</style>
    <main><h1>SparkX Desktop</h1><p>Electron browser runtime is ready.</p>
    <p>Task pages are presented behind a native input shield.</p></main>`;
  return `data:text/html;charset=utf-8,${encodeURIComponent(html)}`;
}

function shieldDocument() {
  const html = `<!doctype html><meta charset="utf-8">
    <meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'">
    <style>
    html,body{margin:0;width:100%;height:100%;background:transparent;overflow:hidden}
    body{cursor:not-allowed}</style><script>
    for(const type of ['dragenter','dragover','drop','contextmenu','paste'])
      addEventListener(type,event=>{event.preventDefault();event.stopImmediatePropagation()},{capture:true});
    addEventListener('keydown',event=>{event.preventDefault();event.stopImmediatePropagation()},{capture:true});
    </script>`;
  return `data:text/html;charset=utf-8,${encodeURIComponent(html)}`;
}

function localIdentity() {
  const connection = desktopAuth?.connection;
  if (!connection || (connection.transport === "iscp" && !connection.identityVerified) || !["connected", "reconnecting", "service_unavailable"].includes(desktopAuth.status.state)) return null;
  return { deployment_id: connection.deploymentID, owner_id: connection.ownerID, client_id: connection.clientID };
}

function prepareBrowserHostScope() {
  browserHostPreparation = browserHostPreparation.catch(() => {}).then(async () => {
    if (quitting || !browserHost || !localStore || desktopAuth.status.state !== "connected" || desktopAuth.descriptor?.transport === "iscp" && !desktopAuth.status.capabilities?.browser) return;
    const connection = desktopAuth.connection;
    const scope = browserHost.scope;
    if (scope?.installation_id !== localStore.installationID || scope.owner_id !== connection.ownerID || scope.client_id !== connection.clientID) {
      await browserHost.start({ installation_id: localStore.installationID });
    }
    await browserHost.refreshFences();
    desktopCapability?.changed();
  });
  return browserHostPreparation;
}
