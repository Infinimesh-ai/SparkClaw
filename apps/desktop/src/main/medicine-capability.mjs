const CHANNEL = "sparkclaw-medicine-box:invoke";
const FIELDS = { list: [], save: ["medicine"], remove: ["medicine_id"] };
const MEDICINE_FIELDS = ["id", "name", "spec", "manufacturer", "category", "quantity", "unit", "expires_on"];

// The renderer supplies only an operation and its own fields; the account the
// inventory belongs to comes from the authenticated main-process identity.
export class MedicineBoxCapability {
  constructor({ ipcMain, window, store, getIdentity }) { Object.assign(this, { ipcMain, window, store, getIdentity }); }
  start() { this.ipcMain.handle(CHANNEL, (event, request) => this.dispatch(event, request)); return this; }
  close() { this.ipcMain.removeHandler(CHANNEL); }
  async dispatch(event, request) {
    let url; try { url = new URL(event.senderFrame?.url); } catch {}
    if (event.sender !== this.window.webContents || event.senderFrame !== this.window.webContents.mainFrame || !url || url.protocol !== "sparkclaw-app:" || url.hostname !== "workbench" || url.username || url.password) throw new Error("Medicine box sender is not trusted");
    if (!request || typeof request !== "object" || Array.isArray(request) || request.schema_version !== 1 || !Object.hasOwn(FIELDS, request.operation)) throw new Error("Medicine box operation is unavailable");
    if (Object.keys(request).sort().join(",") !== ["schema_version", "operation", ...FIELDS[request.operation]].sort().join(",")) throw new Error("Invalid medicine box request fields");
    const identity = this.getIdentity();
    if (!identity) throw new Error("Medicine box is locked");
    const scope = { deployment_id: identity.deployment_id, owner_id: identity.owner_id };
    switch (request.operation) {
      case "list": return this.store.list(scope);
      case "save": {
        const medicine = request.medicine;
        if (!medicine || typeof medicine !== "object" || Array.isArray(medicine) || Object.keys(medicine).sort().join(",") !== [...MEDICINE_FIELDS].sort().join(",")) throw new Error("Invalid medicine fields");
        return this.store.save(scope, medicine);
      }
      case "remove": return this.store.remove(scope, request.medicine_id);
    }
  }
}
