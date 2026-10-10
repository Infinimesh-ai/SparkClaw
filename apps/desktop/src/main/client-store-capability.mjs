import crypto from "node:crypto";
import { localContentType } from "./client-store.mjs";
const CHANNEL = "sparkclaw-client-store:invoke";

export class ClientStoreCapability {
  constructor({ ipcMain, window, store, execution, schedules, getIdentity, exportFile, getCapabilities = () => undefined }) {
    Object.assign(this, { ipcMain, window, store, execution, schedules, getIdentity, exportFile, getCapabilities });
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
    const capabilities = this.getCapabilities();
    if (capabilities?.files === false && ["saveFile", "exportFile", "listFiles", "readFile"].includes(request.operation)) throw new Error("Files are unavailable through this transport");
    if (capabilities?.approvals === false && request.operation === "decideApproval") throw new Error("Approvals are unavailable through this transport");
    const scope = { deployment_id: identity.deployment_id, owner_id: identity.owner_id, client_id: identity.client_id };
    const draftScope = crypto.createHash("sha256").update(JSON.stringify(scope)).digest("hex");
    const draftResult = (value) => ({ ...value, scope_key: draftScope });
    const verifyDraftScope = () => { if (request.expected_scope !== draftScope) throw new Error("Draft authentication changed; reload this workbench"); };
    switch (request.operation) {
      case "list":
        keys(request, []);
        return this.store.list(scope);
      case "listFiles":
        keys(request, []);
        return this.store.listFiles(scope);
      case "create":
        keys(request, ["title"]);
        return this.store.create(scope, request.title);
      case "remove":
        keys(request, ["conversation_id"]);
        return this.store.remove(scope, request.conversation_id);
      case "rename":
        keys(request, ["conversation_id", "title"]);
        return this.store.rename(scope, request.conversation_id, request.title);
      case "read":
        keys(request, ["conversation_id"]);
        {
          const content = this.store.read(scope, request.conversation_id);
          if (this.execution) content.tasks = content.tasks.map((task) => ({ ...task, approvals: this.execution.approvals(scope, task.request_id) }));
          return content;
        }
      case "draft":
        keys(request, ["conversation_id"]);
        return draftResult(this.store.draft(scope, request.conversation_id));
      case "saveDraft":
        keys(request, ["conversation_id", "content", "local_file_ids", "revision", "expected_scope"]);
        verifyDraftScope();
        return draftResult(this.store.saveDraft(scope, request.conversation_id, request.content, request.local_file_ids, request.revision));
      case "moveWelcomeDraft":
        keys(request, ["conversation_id", "revision", "expected_scope"]);
        verifyDraftScope();
        {
          const moved = this.store.moveWelcomeDraft(scope, request.conversation_id, request.revision);
          return { source: draftResult(moved.source), draft: draftResult(moved.draft) };
        }
      case "enqueueDraft":
        keys(request, ["conversation_id", "draft_conversation_id", "revision", "expected_scope"]);
        verifyDraftScope();
        {
          const queued = this.store.enqueueDraft(scope, request.conversation_id, request.draft_conversation_id, request.revision);
          return { ...queued, draft: draftResult(queued.draft) };
        }
      case "enqueue":
        keys(request, ["conversation_id", "content", ...(Object.hasOwn(request, "local_file_ids") ? ["local_file_ids"] : [])]);
        return this.store.enqueue(scope, request.conversation_id, request.content, request.local_file_ids);
      case "submit":
      case "reconcile":
      case "cancel":
        keys(request, ["request_id"]);
        if (!this.execution) throw new Error("Execution client is unavailable");
        return this.execution[request.operation](scope, request.request_id);
      case "decideApproval":
        keys(request, ["request_id", "approval_id", "digest", "decision"]);
        if (!this.execution) throw new Error("Execution client is unavailable");
        return this.execution.decideApproval(scope, request.request_id, request.approval_id, request.digest, request.decision);
      case "scheduleCreate":
        keys(request, ["conversation_id", "content", "due_at", "interval_ms"]);
        if (!this.schedules) throw new Error("Schedule client is unavailable");
        return this.schedules.create(scope, request.conversation_id, request.content, request.due_at, request.interval_ms);
      case "listSchedules":
        keys(request, []);
        if (!this.schedules) throw new Error("Schedule client is unavailable");
        return this.schedules.list(scope);
      case "createScheduleRequest":
        keys(request, ["content", "timezone"]);
        if (!this.schedules) throw new Error("Schedule client is unavailable");
        return this.schedules.createRequest(scope, request.content, request.timezone);
      case "editSchedule":
        keys(request, ["request_id", "expected_version", "draft"]);
        if (!request.draft || typeof request.draft !== 'object' || Object.keys(request.draft).sort().join(',') !== 'dueTime,recurrence,text,timezone') throw new Error('Invalid schedule edit fields');
        if (!this.schedules) throw new Error("Schedule client is unavailable");
        return this.schedules.edit(scope, request.request_id, request.expected_version, request.draft);
      case "scheduleCheck":
      case "scheduleCancel":
      case "scheduleRunNow":
        keys(request, ["request_id"]);
        if (!this.schedules) throw new Error("Schedule client is unavailable");
        return this.schedules[{ scheduleCheck: "reconcile", scheduleCancel: "cancel", scheduleRunNow: "runNow" }[request.operation]](scope, request.request_id);
      case "saveFile":
        keys(request, ["conversation_id", "name", "bytes"]);
        return this.store.saveFile(scope, request.conversation_id, request.name, request.bytes);
      case "exportFile":
        keys(request, ["file_id"]);
        return this.exportFile(this.store.file(scope, request.file_id));
      case "readFile":
        keys(request, ["file_id"]);
        {
          const file = this.store.file(scope, request.file_id);
          return { name: file.name, content_type: localContentType(file.name), bytes: new Uint8Array(file.content) };
        }
      default:
        throw new Error("ClientStore operation is unavailable");
    }
  }
}

function keys(request, fields) {
  const expected = ["schema_version", "operation", ...fields].sort().join(",");
  if (Object.keys(request).sort().join(",") !== expected) throw new Error("Invalid ClientStore request fields");
}
