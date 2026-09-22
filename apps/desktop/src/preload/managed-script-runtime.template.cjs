"use strict";

const { contextBridge, ipcRenderer, webFrame } = require("electron");

const MANAGED_SCRIPTS = /*__SPARKCLAW_MANAGED_SCRIPTS__*/;
const IPC = Object.freeze({
  config: "sparkclaw:managed-script-config",
  get: "sparkclaw:managed-script-get",
  set: "sparkclaw:managed-script-set",
  menuRegister: "sparkclaw:managed-script-menu-register",
  menuRun: "sparkclaw:managed-script-menu-run",
  status: "sparkclaw:managed-script-status",
});
const EXPORTER_WORLD_ID = 1004;
const callbacks = new Map();

if (window.top === window) installForTopFrame();

function installForTopFrame() {
  const config = ipcRenderer.sendSync(IPC.config);
  const qualificationOrigin = validOrigin(config?.qualification_origin) ? config.qualification_origin : "";
  for (const script of MANAGED_SCRIPTS) {
    if (!matches(script, location)) continue;
    if (script.grants.length === 1 && script.grants[0] === "none") installMainWorld(script);
    else installIsolatedWorld(script);
  }
  if (qualificationOrigin && location.origin === qualificationOrigin) installQualificationProof();
}

function installMainWorld(script) {
  let result;
  try {
    result = contextBridge.executeInMainWorld({
      func: script.install,
      args: [],
    });
  } catch {
    result = { state: "failed" };
  }
  report(script, result?.state === "ready" ? "ready" : "failed", "main");
}

function installIsolatedWorld(script) {
  const apiKey = `sparkclawGM_${script.id.replaceAll("-", "_")}`;
  try {
    contextBridge.exposeInIsolatedWorld(EXPORTER_WORLD_ID, apiKey, Object.freeze({
      get: (key, fallback) => syncValue(IPC.get, script.id, key, fallback),
      set: (key, value) => syncValue(IPC.set, script.id, key, value),
      registerMenu: (label, callback) => {
        if (typeof callback !== "function") throw new TypeError("Menu callback is invalid");
        const response = ipcRenderer.sendSync(IPC.menuRegister, { script_id: script.id, label });
        if (!response?.ok || typeof response.command_id !== "string") throw new Error("Menu registration failed");
        callbacks.set(response.command_id, callback);
        return response.command_id;
      },
    }));
    const code = `"use strict";{const api=globalThis[${JSON.stringify(apiKey)}];` +
      `Object.defineProperties(globalThis,{GM_getValue:{value:(key,fallback)=>api.get(key,fallback)},` +
      `GM_setValue:{value:(key,value)=>api.set(key,value)},` +
      `GM_registerMenuCommand:{value:(label,callback)=>api.registerMenu(label,callback)}});` +
      `${script.source}\n}`;
    void webFrame.executeJavaScriptInIsolatedWorld(EXPORTER_WORLD_ID, [{ code }]).then(
      () => report(script, "ready", "isolated"),
      () => report(script, "failed", "isolated"),
    );
  } catch {
    report(script, "failed", "isolated");
  }
}

function installQualificationProof() {
  const id = "sparkclaw.qualification.managed-script";
  const documentStart = contextBridge.executeInMainWorld({
    func: () => {
      globalThis.__sparkclawDocumentStartProof = document.readyState === "loading";
      return globalThis.__sparkclawDocumentStartProof;
    },
    args: [],
  });
  const script = { id, version: "1", sha256: "0".repeat(64) };
  try {
    const apiKey = "sparkclawQualificationGM";
    contextBridge.exposeInIsolatedWorld(EXPORTER_WORLD_ID, apiKey, Object.freeze({
      get: (key, fallback) => syncValue(IPC.get, id, key, fallback),
      set: (key, value) => syncValue(IPC.set, id, key, value),
      registerMenu: (label, callback) => {
        const response = ipcRenderer.sendSync(IPC.menuRegister, { script_id: id, label });
        if (!response?.ok || typeof callback !== "function") throw new Error("Qualification menu registration failed");
        callbacks.set(response.command_id, callback);
        return response.command_id;
      },
    }));
    const code = `"use strict";{const api=globalThis.${apiKey};` +
      `const previous=api.get("persistent-proof","missing");` +
      `api.set("persistent-proof","stored");` +
      `api.set("previous-proof",previous);` +
      `globalThis.__sparkclawGMProof=api.get("persistent-proof","");` +
      `api.registerMenu("Qualification command",()=>api.set("menu-proof","invoked"));}`;
    void webFrame.executeJavaScriptInIsolatedWorld(EXPORTER_WORLD_ID, [{ code }]).then(
      () => report(script, documentStart ? "ready" : "failed", "qualification"),
      () => report(script, "failed", "qualification"),
    );
  } catch {
    report(script, "failed", "qualification");
  }
}

ipcRenderer.on(IPC.menuRun, (_event, commandID) => {
  const callback = callbacks.get(commandID);
  if (callback) Promise.resolve().then(() => callback()).catch(() => {});
});

function syncValue(channel, scriptID, key, value) {
  const response = ipcRenderer.sendSync(channel, { script_id: scriptID, key, value });
  if (!response?.ok) throw new Error("Managed script storage failed");
  return response.value;
}

function matches(script, currentLocation) {
  return script.noframes && script.matches.some((match) =>
    currentLocation.origin === match.origin && currentLocation.pathname.startsWith(match.pathPrefix));
}

function report(script, state, world) {
  ipcRenderer.send(IPC.status, {
    script_id: script.id,
    version: script.version,
    sha256: script.sha256,
    state,
    world,
  });
}

function validOrigin(value) {
  if (typeof value !== "string" || value.length > 256) return false;
  try {
    const url = new URL(value);
    return url.origin === value && !url.username && !url.password && !url.hash && !url.search;
  } catch {
    return false;
  }
}
