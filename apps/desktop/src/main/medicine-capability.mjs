const CHANNEL = "sparkclaw-medicine-box:invoke";
const FIELDS = { list: [], save: ["medicine"], remove: ["medicine_id"] };
const MEDICINE_FIELDS = ["id", "name", "category", "quantity", "unit", "expires_on", "no_expiry", "location", "usage", "notes"];

// The renderer supplies only an operation and its own fields; the account the
// inventory belongs to comes from the authenticated main-process identity.
export class MedicineBoxCapability {
  constructor({ ipcMain, window, store, client, getIdentity }) { Object.assign(this, { ipcMain, window, store, client, getIdentity }); }
  start() { this.ipcMain.handle(CHANNEL, (event, request) => this.dispatch(event, request)); return this; }
  close() { this.ipcMain.removeHandler(CHANNEL); }
  async #write(action) {
    try { return await action(); } catch (error) { if (error.status === 409) return { medicine_error: "medicine_conflict" }; throw error; }
  }
  async dispatch(event, request) {
    let url; try { url = new URL(event.senderFrame?.url); } catch {}
    if (event.sender !== this.window.webContents || event.senderFrame !== this.window.webContents.mainFrame || !url || url.protocol !== "sparkclaw-app:" || url.hostname !== "workbench" || url.username || url.password) throw new Error("Medicine box sender is not trusted");
    if (!request || typeof request !== "object" || Array.isArray(request) || request.schema_version !== 1 || !Object.hasOwn(FIELDS, request.operation)) throw new Error("Medicine box operation is unavailable");
    if (Object.keys(request).sort().join(",") !== ["schema_version", "operation", ...FIELDS[request.operation], ...(Object.hasOwn(request, "action_id") ? ["action_id"] : []), ...(request.operation === "remove" && Object.hasOwn(request, "base_version") ? ["base_version"] : [])].sort().join(",")) throw new Error("Invalid medicine box request fields");
    if (request.action_id !== undefined && (request.operation === "list" || !/^[a-f0-9-]{36}$/u.test(request.action_id))) throw new Error("Invalid medicine action identity");
    const identity = this.getIdentity();
    if (!identity) throw new Error("Medicine box is locked");
    const scope = { deployment_id: identity.deployment_id, owner_id: identity.owner_id };
    switch (request.operation) {
      case "list": { void this.client?.refresh().catch(() => {}); return this.store.list(scope); }
      case "save": {
        const medicine = request.medicine;
        if (!medicine || typeof medicine !== "object" || Array.isArray(medicine) || Object.keys(medicine).sort().join(",") !== [...MEDICINE_FIELDS, ...(Object.hasOwn(medicine, "version") ? ["version"] : [])].sort().join(",")) throw new Error("Invalid medicine fields");
        if (!this.client) throw new Error("Medicine sync is unavailable");
        return this.#write(() => this.client.save(medicine, request.action_id));
      }
      case "remove": { if (!this.client) throw new Error("Medicine sync is unavailable"); return this.#write(() => this.client.remove(request.medicine_id, request.base_version, request.action_id)); }
    }
  }
}
