const CHANNEL = "sparkclaw-client-store:invoke";

export class ClientStoreCapability {
  constructor({ ipcMain, window, store, getIdentity, exportFile }) {
    Object.assign(this, { ipcMain, window, store, getIdentity, exportFile });
  }
  start() {
    this.ipcMain.handle(CHANNEL, (event, request) => this.dispatch(event, request));
    return this;
  }
  close() { this.ipcMain.removeHandler(CHANNEL); }

  async dispatch(event, request) {
    const frame = event.senderFrame;
    let url;
    try { url = frame ? new URL(frame.url) : null; } catch { url = null; }
    if (event.sender !== this.window.webContents || frame !== this.window.webContents.mainFrame ||
        !url || url.protocol !== "sparkclaw-app:" || url.hostname !== "workbench" || url.username || url.password) {
      throw new Error("ClientStore sender is not trusted");
    }
    const identity = this.getIdentity();
    if (!identity) throw new Error("ClientStore is locked; sign in to this backend first");
    if (!request || typeof request !== "object" || Array.isArray(request) || request.schema_version !== 1) {
      throw new Error("Invalid ClientStore request");
    }
    const scope = { deployment_id: identity.deployment_id, owner_id: identity.owner_id, client_id: identity.client_id };
    switch (request.operation) {
      case "list":
        keys(request, []);
        return this.store.list(scope);
      case "create":
        keys(request, ["title"]);
        return this.store.create(scope, request.title);
      case "read":
        keys(request, ["conversation_id"]);
        return this.store.read(scope, request.conversation_id);
      case "enqueue":
        keys(request, ["conversation_id", "content"]);
        return this.store.enqueue(scope, request.conversation_id, request.content);
      case "saveFile":
        keys(request, ["conversation_id", "name", "bytes"]);
        return this.store.saveFile(scope, request.conversation_id, request.name, request.bytes);
      case "exportFile":
        keys(request, ["file_id"]);
        return this.exportFile(this.store.file(scope, request.file_id));
      default:
        throw new Error("ClientStore operation is unavailable");
    }
  }
}

function keys(request, fields) {
  const expected = ["schema_version", "operation", ...fields].sort().join(",");
  if (Object.keys(request).sort().join(",") !== expected) throw new Error("Invalid ClientStore request fields");
}
