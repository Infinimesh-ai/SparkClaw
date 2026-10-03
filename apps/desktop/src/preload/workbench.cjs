const { contextBridge, ipcRenderer } = require("electron");

const invoke = (operation, fields = {}) => ipcRenderer.invoke("sparkclaw-desktop:invoke", {
  schema_version: 1,
  operation,
  ...fields,
});
const speechArgument = process.argv.find((value) => value.startsWith("--sparkclaw-speech-base="));
const speechBase = speechArgument ? speechArgument.slice("--sparkclaw-speech-base=".length) : "";

if (process.argv.includes("--sparkclaw-client-store=1")) {
  const storeInvoke = (operation, fields = {}) => ipcRenderer.invoke("sparkclaw-client-store:invoke", {
    schema_version: 1, operation, ...fields,
  });
  contextBridge.exposeInMainWorld("sparkclawClientStore", Object.freeze({
    schemaVersion: 1,
    list: () => storeInvoke("list"),
    create: (title) => storeInvoke("create", { title }),
    read: (conversation_id) => storeInvoke("read", { conversation_id }),
    enqueue: (conversation_id, content, local_file_ids = []) => storeInvoke("enqueue", {
      conversation_id, content, ...(local_file_ids.length ? { local_file_ids } : {}),
    }),
    submit: (request_id) => storeInvoke("submit", { request_id }),
    reconcile: (request_id) => storeInvoke("reconcile", { request_id }),
    cancel: (request_id) => storeInvoke("cancel", { request_id }),
    saveFile: (conversation_id, name, bytes) => storeInvoke("saveFile", { conversation_id, name, bytes }),
    exportFile: (file_id) => storeInvoke("exportFile", { file_id }),
  }));
  const mailInvoke = (operation, fields = {}) => ipcRenderer.invoke("sparkclaw-mail-sync:invoke", {
    schema_version: 1, operation, ...fields,
  });
  contextBridge.exposeInMainWorld("sparkclawMailSync", Object.freeze({
    catalog: () => mailInvoke("catalog"),
    refreshCatalog: () => mailInvoke("refreshCatalog"),
    read: (mailbox_id) => mailInvoke("read", { mailbox_id }),
    sync: (mailbox_id) => mailInvoke("sync", { mailbox_id }),
  }));
}

contextBridge.exposeInMainWorld("sparkclawDesktop", Object.freeze({
  runtimeKind: "electron",
  capabilityVersion: 1,
  gatewayBase: "sparkclaw-app://workbench/desktop-gateway",
  speechBase,
  localConnection: () => ipcRenderer.invoke("sparkclaw-local-backend:status"),
  retryLocalConnection: () => ipcRenderer.invoke("sparkclaw-local-backend:retry"),
  configureBackend: (descriptor) => ipcRenderer.invoke("sparkclaw-local-backend:configure", descriptor),
  login: (token) => ipcRenderer.invoke("sparkclaw-local-backend:login", token),
  logout: () => ipcRenderer.invoke("sparkclaw-local-backend:logout"),
  loginStartup: (enabled) => ipcRenderer.invoke("sparkclaw-desktop:login-startup", enabled),
  onLocalConnection: (listener) => {
    if (typeof listener !== "function") throw new TypeError("Desktop connection listener is invalid");
    const wrapped = (_event, status) => listener(status);
    ipcRenderer.on("sparkclaw-local-backend:state", wrapped);
    return () => ipcRenderer.removeListener("sparkclaw-local-backend:state", wrapped);
  },
  state: () => invoke("state"),
  selectConversation: (local_conversation_id) => invoke("selectConversation", { local_conversation_id }),
  grantBrowserHost: () => invoke("grantBrowserHost"),
  createPersonal: (url) => invoke("createPersonal", url ? { url } : {}),
  navigatePersonal: (pageRef, url) => invoke("navigatePersonal", { page_ref: pageRef, url }),
  personalNavigation: (pageRef, action) => invoke("personalNavigation", { page_ref: pageRef, action }),
  closePersonal: (pageRef) => invoke("closePersonal", { page_ref: pageRef }),
  presentPersonal: (pageRef) => invoke("presentPersonal", { page_ref: pageRef }),
  observeTask: (pageRef) => invoke("observeTask", { page_ref: pageRef }),
  hideBrowser: () => invoke("hideBrowser"),
  setBounds: (bounds, revision) => invoke("setBounds", { bounds, revision }),
  respondPermission: (permissionRef, allow) => invoke("respondPermission", { permission_ref: permissionRef, allow }),
  cancelDownload: (downloadRef) => invoke("cancelDownload", { download_ref: downloadRef }),
  showDownload: (downloadRef) => invoke("showDownload", { download_ref: downloadRef }),
  onState: (listener) => {
    if (typeof listener !== "function") throw new TypeError("Desktop state listener is invalid");
    const wrapped = (_event, state) => listener(state);
    ipcRenderer.on("sparkclaw-desktop:state", wrapped);
    return () => ipcRenderer.removeListener("sparkclaw-desktop:state", wrapped);
  },
}));
