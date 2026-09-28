import crypto from "node:crypto";
import fs from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

import {
  app,
  BrowserWindow,
  clipboard,
  ipcMain,
  Menu,
  net as electronNet,
  powerMonitor,
  protocol,
  session,
  shell,
  WebContentsView,
} from "electron";

import { ElectronAdapterServer } from "../browser/adapter-server.mjs";
import { ManagedScriptHost } from "../browser/managed-script-host.mjs";
import { PageRegistry } from "../browser/page-registry.mjs";
import { adapterSecretPath, adapterSocketPath } from "../browser/protocol.mjs";
import { BrowserPresentation } from "./presentation.mjs";
import { DesktopCapability } from "./desktop-capability.mjs";
import { connectionStatus, loadLocalBackendConnection, loadLocalBackendDescriptor, verifyLocalBackend } from "./local-backend.mjs";
import { OwnerBrowserServices } from "./owner-browser-services.mjs";
import { linuxLoginStartup } from "./login-startup.mjs";
import { resolveDesktopConnectionPaths } from "./connection-paths.mjs";

const MODULE_DIR = path.dirname(fileURLToPath(import.meta.url));
const DESKTOP_ICON_PATH = path.join(MODULE_DIR, "..", "assets", "icon.png");
const workbenchQualification = process.argv.includes("--qualification-workbench");
const qualification = process.argv.includes("--qualification") || workbenchQualification;
const configuredUserData = process.env.SPARKCLAW_DESKTOP_USER_DATA_DIR?.trim();
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
let window;
let adapter;
let scriptHost;
let desktopCapability;
let ownerBrowserServices;
let registry;

void app.whenReady().then(start).catch((error) => {
  process.stderr.write(`SparkClaw Electron failed: ${error instanceof Error ? error.message : "unknown error"}\n`);
  app.exit(1);
});

async function start() {
  const internalProtocolHandler = (request) => {
    const url = new URL(request.url);
    if (url.hostname !== "extension-connect" || url.pathname !== "") {
      return new Response("Not found", { status: 404, headers: { "content-type": "text/plain" } });
    }
    return new Response("<!doctype html><meta charset=utf-8><title>SparkClaw task</title>", {
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
  let localBackend;
  let localBackendStatus;
	let configuredOrigin = "http://127.0.0.1:18790";
	try {
		configuredOrigin = (await loadLocalBackendDescriptor(await localBackendPaths())).origin;
	} catch {
		// The incomplete-setup UI still needs a safe CSP origin. A repaired
		// custom-port descriptor is picked up on the next application start.
	}
  try {
    localBackend = await loadLocalBackendConnection(await localBackendPaths());
    localBackendStatus = await verifyLocalBackend(localBackend, electronNet.fetch);
  } catch {
		localBackend = unavailableLocalBackend(configuredOrigin);
    localBackendStatus = connectionStatus("incomplete_setup");
  }
  const setLocalBackendStatus = (status) => {
    localBackendStatus = status;
    if (window && !window.isDestroyed()) {
      window.webContents.send("sparkclaw-local-backend:state", status);
    }
  };
  const refreshLocalBackend = async () => {
    try {
      const candidate = await loadLocalBackendConnection(await localBackendPaths());
			if (candidate.origin !== configuredOrigin) {
				setLocalBackendStatus(connectionStatus("incomplete_setup"));
				return localBackendStatus;
			}
      const verified = await verifyLocalBackend(candidate, electronNet.fetch);
      if (verified.state === "connected") localBackend = candidate;
      setLocalBackendStatus(verified);
    } catch {
      setLocalBackendStatus(connectionStatus("incomplete_setup"));
    }
    return localBackendStatus;
  };
  ipcMain.handle("sparkclaw-local-backend:status", () => localBackendStatus);
  ipcMain.handle("sparkclaw-local-backend:retry", async () => {
    setLocalBackendStatus(connectionStatus("reconnecting"));
    return refreshLocalBackend();
  });
  ipcMain.handle("sparkclaw-desktop:login-startup", async (_event, enabled) => {
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
  const speechOrigin = localBackend.origin;
  workbenchSession.protocol.handle("sparkclaw-app", workbenchProtocolHandler(
    webchatDistPath(), electronNet, () => localBackend, () => localBackendStatus,
    setLocalBackendStatus,
  ));

  window = new BrowserWindow({
    title: qualification ? "SparkClaw Electron Qualification" : "SparkClaw",
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
      additionalArguments: [`--sparkclaw-speech-base=${speechOrigin}`],
    },
  });
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
  powerMonitor.on("resume", () => {
    setLocalBackendStatus(connectionStatus("reconnecting"));
    void refreshLocalBackend();
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
  ownerBrowserServices = await new OwnerBrowserServices({
    browserSession,
    workbenchSession,
    registry,
    workbench: window,
    downloadsRoot: qualification
      ? path.join(app.getPath("userData"), "owner-downloads")
      : path.join(app.getPath("downloads"), "SparkClaw"),
    shell,
    onChange: () => desktopCapability?.changed(),
  }).start();
  desktopCapability = new DesktopCapability({
    ipcMain,
    window,
    registry,
    presentation,
    browserServices: ownerBrowserServices,
    runtimeGeneration,
  }).start();
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
  await window.loadURL(workbenchURL);
  const workbenchEvidence = workbenchQualification ? await waitForWorkbench() : undefined;

  window.show();
  process.stdout.write(`${JSON.stringify({
    event: "sparkclaw_electron_ready",
    runtime_kind: "electron",
    runtime_generation: runtimeGeneration,
    adapter_socket: adapterSocketPath(),
    electron_version: process.versions.electron,
    chromium_version: process.versions.chrome,
    node_version: process.versions.node,
    architecture: process.arch,
    qualification,
    ...(workbenchQualification ? workbenchEvidence : {}),
  })}\n`);

  app.on("second-instance", () => {
    if (!window.isVisible()) window.show();
    window.focus();
  });
  app.on("window-all-closed", () => {});
  app.on("before-quit", () => { quitting = true; });
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
  if (quitting) return;
  quitting = true;
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
  const trusted = (webContents, origin) => webContents === targetWindow.webContents &&
    canonicalOrigin(origin) === "sparkclaw-app://workbench";
  targetSession.setPermissionCheckHandler((webContents, permission, origin, details) =>
    trusted(webContents, origin) && permission === "media" && details?.mediaType === "audio");
  targetSession.setPermissionRequestHandler((webContents, permission, callback, details) => {
    const requestingOrigin = details?.requestingUrl ? originOf(details.requestingUrl) : "";
    const mediaTypes = Array.isArray(details?.mediaTypes) ? details.mediaTypes : [];
    callback(trusted(webContents, requestingOrigin) && permission === "media" &&
      mediaTypes.includes("audio") && !mediaTypes.includes("video"));
  });
  targetSession.webRequest.onBeforeSendHeaders({
    urls: [`${speechOrigin.replace(/^http/u, "ws")}/*`],
  }, (details, callback) => {
    const requestHeaders = { ...details.requestHeaders, Origin: speechOrigin };
    callback({ requestHeaders });
  });
}

function originOf(raw) {
  return canonicalOrigin(raw);
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

function unavailableLocalBackend(origin = "http://127.0.0.1:18790") {
  return Object.freeze({
		origin,
    deploymentID: "",
    clientID: "",
    ownerID: "",
    actorID: "",
    clientName: "",
    authorization: "",
  });
}

function workbenchProtocolHandler(root, network, getLocalBackend, getConnectionStatus, setConnectionStatus) {
  return async (request) => {
    try {
      const url = new URL(request.url);
      if (url.hostname !== "workbench") return new Response("Not found", { status: 404 });
      const localBackend = getLocalBackend();
      const proxy = proxyTarget(url, localBackend.origin);
      if (proxy) {
        const state = getConnectionStatus().state;
        if (state === "identity_conflict") return new Response("Local backend identity conflict", { status: 409 });
        if (state === "invalid_authentication") return new Response("Local desktop authentication is invalid", { status: 401 });
        if (state !== "connected") return new Response("Local backend is unavailable", { status: 503 });
        try {
          const response = await proxyWorkbenchRequest(network, request, proxy, localBackend.authorization);
          if (response.status === 401 || response.status === 403) {
            setConnectionStatus(connectionStatus("invalid_authentication"));
          }
          return response;
        } catch {
          setConnectionStatus(connectionStatus("service_unavailable"));
          return new Response("Local backend is unavailable", { status: 503 });
        }
      }
      const pathname = decodeURIComponent(url.pathname === "/" ? "/index.html" : url.pathname);
      const filename = path.resolve(root, `.${pathname}`);
      if (!filename.startsWith(`${path.resolve(root)}${path.sep}`)) return new Response("Not found", { status: 404 });
      const content = await fs.readFile(filename);
      return new Response(content, { headers: workbenchHeaders(filename, localBackend.origin) });
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

async function proxyWorkbenchRequest(network, request, target, authorization) {
  const headers = new Headers(request.headers);
  headers.delete("origin");
  headers.delete("referer");
  headers.delete("proxy-authorization");
  headers.set("authorization", authorization);
  const method = request.method.toUpperCase();
  const body = method === "GET" || method === "HEAD" ? undefined : await request.arrayBuffer();
  const response = await network.fetch(target, { method, headers, body, redirect: "manual" });
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
    <main><h1>SparkClaw Desktop</h1><p>Electron browser runtime is ready.</p>
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
