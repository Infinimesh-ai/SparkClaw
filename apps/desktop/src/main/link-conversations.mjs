import crypto from "node:crypto";
import { localContentType } from "./client-store.mjs";
import { LinkError, linkNotSent } from "./link-transport.mjs";

const PAGE = 30;
const CHUNK = 256 * 1024;
const MAX_DOWNLOAD = 64 * 1024 * 1024;
const WATCHED = 8;
const sha256 = (value) => crypto.createHash("sha256").update(value).digest("hex");

function stableUUID(value) {
  const b = crypto.createHash("sha256").update(value).digest().subarray(0, 16);
  b[6] = (b[6] & 0x0f) | 0x40; b[8] = (b[8] & 0x3f) | 0x80;
  const h = Buffer.from(b).toString("hex");
  return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`;
}

// Errors a user can act on, in the workbench's own words.
function userError(error, fallback) {
  if (error instanceof LinkError) {
    if (error.code === "backend_unavailable") return new Error("The backend is offline. Check the connection and try again; your input is kept.");
    if (error.code === "busy") return new Error("Another message is still running in this conversation. Wait for it to finish, then send again.");
    if (error.code === "revoked" || error.code === "unauthorized") return new Error("This device was removed from the SparkClaw console. Join again with a new access credential.");
    if (error.code === "not_found") return new Error("This conversation no longer exists on the backend.");
    if (["quota_exceeded", "too_large"].includes(error.code)) return new Error(error.message || "The attachment exceeds the backend limit.");
  }
  return error instanceof Error && !(error instanceof LinkError) ? error : new Error(fallback);
}

function unsentReason(error) {
  if (error.code === "busy" || error.code === "conflict") return "Not sent: another message is still running in this conversation.";
  if (error.code === "backend_unavailable") return "Not sent: the backend is offline.";
  return `Not sent: ${error.message || error.code}`;
}

function localApproval(approval, decided) {
  let detail = approval.detail;
  if (typeof detail === "string") { try { detail = JSON.parse(detail); } catch { detail = undefined; } }
  detail = detail && typeof detail === "object" ? detail : {};
  const args = detail.arguments && typeof detail.arguments === "object" && !Array.isArray(detail.arguments) ? detail.arguments : {};
  const status = decided?.state || ({ pending: "pending", approved: "approved", rejected: "rejected" }[approval.status] ?? "resolved");
  const created = Date.parse(approval.created_at || detail.created_at || "");
  return {
    approval_id: approval.id,
    digest: sha256(JSON.stringify([approval.id, approval.tool_name || detail.tool || "", args])),
    tool: approval.tool_name || detail.tool || "",
    summary: approval.title || approval.summary || detail.summary || "",
    arguments: args,
    state: status,
    decision: decided?.decision ?? (approval.status === "approved" ? "approve" : approval.status === "rejected" ? "reject" : null),
    expires_at: approval.expires_at || new Date((Number.isFinite(created) ? created : Date.now()) + 15 * 60000).toISOString(),
    actionable: status === "pending",
    ...(detail.input_form ? { input_form: detail.input_form } : {}),
    ...(detail.input_values ? { input_values: detail.input_values } : {}),
  };
}

// LinkConversations makes the backend the single source of truth for
// conversations (design section 4) while the renderer keeps the ClientStore
// interface. Headers are mirrored with sync.changes, messages are cached
// page by page, a task is sent with message.send, its live preview comes
// from run events, and approvals resolve on the backend.
export class LinkConversations {
  constructor({ auth, store, getIdentity, onChange = () => {}, onCancelTask = () => {}, onTaskTerminal = () => {}, now = Date.now }) {
    Object.assign(this, { auth, store, getIdentity, onChange, onCancelTask, onTaskTerminal, now });
    this.live = new Map();          // remote session id -> { runID, preview, progress }
    this.approvalCache = new Map(); // request id -> LocalApproval[]
    this.decisions = new Map();     // request id:approval id -> { state, decision }
    this.watching = [];
    this.refreshed = new Map();
    this.flights = new Map();
    this.subscribed = new Set();
    this.operations = new Map();
    this.epoch = 0;
    this.closed = true;
    this.onEvent = (frame) => this.#event(frame);
    // Subscriptions belong to one backend connection. When the path goes
    // down or switches (LAN <-> ISCP), the runs in progress are subscribed
    // again on the new path and their status is read back (design 3.6).
    this.onState = ({ path, previous }) => {
      if (path === previous) return;
      this.subscribed.clear();
      this.epoch++;
      if (path && previous && !this.closed) void this.reconcilePending().catch(() => {});
    };
    auth.link.on("event", this.onEvent);
    auth.link.on("state", this.onState);
  }

  #link() { return this.auth.link; }
  #request(op, params, options) { return this.#link().request(op, params, options); }

  #changed() {
    if (this.changeTimer) return;
    this.changeTimer = setTimeout(() => { this.changeTimer = undefined; this.onChange(); }, 50);
    this.changeTimer.unref?.();
  }

  // Runs on every connected state: the cursor sync, then the runs this
  // device still has in progress.
  start() {
    const scope = this.getIdentity();
    if (!scope) return this;
    this.closed = false;
    this.epoch++;
    if (this.store.linkCutover(scope)) this.onChange();
    void this.sync().then(() => this.reconcilePending({ wake: true })).catch(() => {});
    return this;
  }

  close() { this.closed = true; this.epoch++; this.subscribed.clear(); }
  pauseProgress() { this.subscribed.clear(); }
  flushPresentationReceipts() { return Promise.resolve(); }

  #serialized(key, operation) {
    const pending = (this.operations.get(key) || Promise.resolve()).catch(() => {}).then(operation);
    this.operations.set(key, pending);
    return pending.finally(() => { if (this.operations.get(key) === pending) this.operations.delete(key); });
  }

  // ---- sync

  sync() {
    if (this.syncFlight) { this.syncAgain = true; return this.syncFlight; }
    this.syncFlight = (async () => {
      do {
        this.syncAgain = false;
        await this.#sync();
      } while (this.syncAgain);
    })().finally(() => { this.syncFlight = undefined; });
    return this.syncFlight;
  }

  async #sync() {
    const scope = this.getIdentity();
    if (!scope || !this.#link().online) return;
    let cursor = this.store.linkCursor(scope), changed = false;
    const stale = new Set();
    for (let page = 0; page < 1000; page++) {
      const result = await this.#request("sync.changes", { cursor, limit: 100 });
      if (this.store.linkScopeCutover(scope, result.scope)) {
        // The feed now covers only this device's conversations (design
        // 4.4): the cleared cache is filled again from the start.
        this.#forgetConversations();
        cursor = 0; changed = true; stale.clear();
        continue;
      }
      for (const header of result.sessions || []) {
        const applied = this.store.applyLinkHeader(scope, header);
        changed = true;
        if (applied.deleted) { this.watching = this.watching.filter(id => id !== applied.conversation_id); continue; }
        if (applied.stale && (this.watching.includes(applied.conversation_id) || header.running)) stale.add(applied.conversation_id);
      }
      if (Number.isSafeInteger(result.cursor) && result.cursor >= cursor) { cursor = result.cursor; this.store.setLinkCursor(scope, cursor); }
      if (!result.more) break;
    }
    if (changed) this.onChange();
    await Promise.allSettled([...stale].map(id => this.refreshMessages(scope, id)));
  }

  #forgetConversations() {
    this.live.clear();
    this.approvalCache.clear();
    this.decisions.clear();
    this.refreshed.clear();
    this.subscribed.clear();
    this.watching = [];
  }

  // Fetches what the cache lacks: the latest page for an uncached
  // conversation, otherwise every message after the newest cached one.
  refreshMessages(scope, conversationID) {
    const key = `refresh:${conversationID}`;
    if (this.flights.has(key)) return this.flights.get(key);
    const flight = (async () => {
      const row = this.store.linkConversation(scope, conversationID);
      if (!row) return false;
      let changed = false;
      if (!row.cached_seq) {
        const page = await this.#request("session.messages", { session_id: row.remote_id, limit: PAGE });
        changed = this.store.applyLinkMessages(scope, conversationID, page.messages || [], { more: page.more, oldestPage: true }) || changed;
        if (page.session) this.store.applyLinkHeader(scope, page.session);
      } else {
        let after = row.cached_seq;
        for (let i = 0; i < 10; i++) {
          const page = await this.#request("session.messages", { session_id: row.remote_id, after_seq: after, limit: 100 });
          const messages = page.messages || [];
          if (messages.length) changed = this.store.applyLinkMessages(scope, conversationID, messages) || changed;
          if (page.session) this.store.applyLinkHeader(scope, page.session);
          const last = messages.at(-1)?.seq ?? after;
          if (!messages.length || last <= after || last >= (page.session?.last_seq ?? last)) break;
          after = last;
        }
      }
      this.refreshed.set(conversationID, this.epoch);
      if (changed) this.#changed();
      return changed;
    })().finally(() => this.flights.delete(key));
    this.flights.set(key, flight);
    return flight;
  }

  // Opening a conversation shows the cache; the latest messages follow.
  touch(scope, conversationID) {
    this.watching = [conversationID, ...this.watching.filter(id => id !== conversationID)].slice(0, WATCHED);
    const row = this.store.linkConversation(scope, conversationID);
    if (!row || !this.#link().online) return;
    if (this.refreshed.get(conversationID) !== this.epoch || (row.last_seq || 0) > (row.cached_seq || 0)) void this.refreshMessages(scope, conversationID).catch(() => {});
  }

  async loadOlder(scope, conversationID) {
    const row = this.store.linkConversation(scope, conversationID);
    if (!row || !row.more_before) return { has_older: false };
    try {
      const page = await this.#request("session.messages", { session_id: row.remote_id, ...(row.oldest_seq ? { before_seq: row.oldest_seq } : {}), limit: PAGE });
      this.store.applyLinkMessages(scope, conversationID, page.messages || [], { more: page.more, oldestPage: true });
    } catch (error) { throw userError(error, "Older messages could not be loaded."); }
    this.#changed();
    return { has_older: Boolean(this.store.linkConversation(scope, conversationID)?.more_before) };
  }

  // ---- conversations

  async create(scope, title, autoTitle = false) {
    let header;
    try { header = await this.#request("session.create", { title, client_request_id: crypto.randomUUID() }); }
    catch (error) { throw userError(error, "The conversation could not be created."); }
    const applied = this.store.applyLinkHeader(scope, header, { title, titlePending: autoTitle });
    this.refreshed.set(applied.conversation_id, this.epoch);
    return this.store.list(scope).find(row => row.id === applied.conversation_id);
  }

  async rename(scope, conversationID, title) {
    const row = this.store.linkConversation(scope, conversationID);
    if (row) {
      try { await this.#request("session.rename", { session_id: row.remote_id, title: title.trim() }); }
      catch (error) { throw userError(error, "The conversation could not be renamed."); }
      this.store.markLinkRename(scope, conversationID, false);
    }
    return this.store.rename(scope, conversationID, title);
  }

  async remove(scope, conversationID) {
    if (!this.store.removable(scope, conversationID)) return { deleted: false, reason: "pending_execution" };
    const row = this.store.linkConversation(scope, conversationID);
    if (row) {
      try { await this.#request("session.delete", { session_id: row.remote_id }); }
      catch (error) { throw userError(error, "The conversation could not be deleted."); }
    }
    this.watching = this.watching.filter(id => id !== conversationID);
    return this.store.remove(scope, conversationID);
  }

  async archive(scope, conversationID, archived) {
    const row = this.store.linkConversation(scope, conversationID);
    if (!row) throw new Error("This conversation is not on the backend.");
    let header;
    try { header = await this.#request("session.archive", { session_id: row.remote_id, archived: archived === true }); }
    catch (error) { throw userError(error, "The archive state could not be saved."); }
    this.store.applyLinkHeader(scope, header);
    this.#changed();
    return { archived: archived === true };
  }

  archivedIDs(scope) { return this.store.archivedIDs(scope); }

  // A generated file is fetched from the backend when it is first opened.
  async ensureFile(scope, fileID) {
    const attachment = this.store.linkAttachment(scope, fileID);
    if (!attachment) return;
    try { this.store.file(scope, fileID); return; } catch { /* not downloaded yet */ }
    const chunks = [];
    let offset = 0, total = 0;
    try {
      for (;;) {
        const part = await this.#request("object.read", { object_id: attachment.object_id, offset, length: CHUNK }, { timeoutMS: 60000 });
        const bytes = Buffer.from(part.data || "", "base64");
        if (part.offset !== offset) throw new Error("Attachment chunk is out of order");
        chunks.push(bytes); offset += bytes.length; total += bytes.length;
        if (total > MAX_DOWNLOAD) throw new Error("The attachment exceeds the local file limit.");
        if (part.eof || !bytes.length) break;
      }
    } catch (error) { throw userError(error, "The attachment could not be downloaded."); }
    this.store.saveLinkAttachment(scope, fileID, new Uint8Array(Buffer.concat(chunks)));
    this.#changed();
  }

  // ---- runs (the ExecutionClient interface)

  progressContent(scope, requestID) {
    let task;
    try { task = this.store.linkTask(scope, requestID); } catch { return undefined; }
    if (!["running", "approval_pending", "submission_pending", "cancel_pending"].includes(task.status)) return undefined;
    const row = this.store.linkConversation(scope, task.conversation_id);
    const live = row && this.live.get(row.remote_id);
    if (!live || (task.run_id && live.runID && live.runID !== task.run_id)) return undefined;
    return live.preview || undefined;
  }

  approvals(scope, requestID) {
    return (this.approvalCache.get(requestID) || []).map(a => {
      const decided = this.decisions.get(`${requestID}:${a.approval_id}`);
      const connected = this.auth.status.state === "connected";
      return { ...a, ...(decided ? { state: decided.state, decision: decided.decision } : {}), actionable: connected && !decided && a.state === "pending" };
    });
  }

  #remoteSession(scope, task, content) {
    const row = this.store.linkConversation(scope, task.conversation_id);
    if (row) return Promise.resolve(row);
    // A schedule's local conversation joins the backend on its first run,
    // once even when several of its occurrences are due together.
    return this.#serialized(`remote:${task.conversation_id}`, async () => {
      const joined = this.store.linkConversation(scope, task.conversation_id);
      if (joined) return joined;
      const header = await this.#request("session.create", { title: content.slice(0, 80) || "Scheduled task", client_request_id: task.conversation_id });
      this.store.applyLinkHeader(scope, header, { localID: task.conversation_id });
      return this.store.linkConversation(scope, task.conversation_id);
    });
  }

  async #upload(scope, task, remoteID, files) {
    const refs = [];
    for (const file of files) {
      const local = this.store.file(scope, file.id);
      const contentType = localContentType(local.name);
      let state = await this.#request("object.upload.begin", { upload_key: `${task.request_id}:${file.id}`, name: local.name, size: local.size,
        content_type: contentType, sha256: local.sha256, session_id: remoteID });
      // Resumes from what the backend already holds.
      while (!state.object_id && state.received < local.size) {
        const size = Math.min(state.chunk_size || CHUNK, CHUNK);
        const data = Buffer.from(local.content.subarray(state.received, state.received + size)).toString("base64");
        state = await this.#request("object.upload.chunk", { upload_id: state.upload_id, offset: state.received, data }, { timeoutMS: 60000 });
      }
      if (!state.object_id) state = await this.#request("object.upload.commit", { upload_id: state.upload_id }, { timeoutMS: 60000 });
      refs.push({ object_id: state.object_id, name: local.name, size: local.size, content_type: contentType, sha256: local.sha256 });
    }
    return refs;
  }

  async submit(scope, requestID, { canSubmit = () => true, scheduleClaim } = {}) {
    return this.#serialized(requestID, async () => {
      if (!canSubmit()) throw new Error("Submission availability changed");
      const original = this.store.linkTask(scope, requestID);
      const claimed = scheduleClaim && original.submission_claim === scheduleClaim && original.status === "submission_pending";
      if (original.explicitly_submitted && !claimed) { await this.#reconcile(scope, original); return this.#view(scope, requestID); }
      if (!this.#link().online || this.auth.status.state !== "connected") throw userError(new LinkError("backend_unavailable"), "");
      const context = JSON.parse(original.context_json);
      const content = context.messages.at(-1)?.content ?? "";
      let row, attachments;
      try {
        row = await this.#remoteSession(scope, original, content);
        if (row.rename_pending) {
          const conversation = this.store.list(scope).find(c => c.id === original.conversation_id);
          if (conversation) await this.#request("session.rename", { session_id: row.remote_id, title: conversation.title });
          this.store.markLinkRename(scope, original.conversation_id, false);
        }
        attachments = await this.#upload(scope, original, row.remote_id, context.input_files || []);
      } catch (error) { throw userError(error, "The message could not be prepared for sending."); }
      if (!canSubmit()) throw new Error("Submission availability changed");
      const { first, task } = this.store.markSubmitted(scope, requestID, scheduleClaim);
      if (!first) { await this.#reconcile(scope, task); return this.#view(scope, requestID); }
      // Subscribe first, so the preview starts with the first delta.
      await this.#subscribe(scope, row.remote_id).catch(() => {});
      const seq = this.live.get(row.remote_id)?.seq;
      this.live.set(row.remote_id, { runID: "", preview: "", progress: "", requestID, ...(Number.isSafeInteger(seq) ? { seq } : {}) });
      let result;
      try {
        result = await this.#request("message.send", { session_id: row.remote_id, client_message_id: requestID, content,
          ...(attachments.length ? { attachments } : {}), client_timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
          ...presentationFor(context, original.conversation_id) });
      } catch (error) {
        // Refused by the backend's handler, or never delivered: either way
        // the message was not admitted.
        const unsent = linkNotSent(error) || (error instanceof LinkError && ["busy", "conflict", "invalid_request", "not_found", "too_large", "quota_exceeded", "unsupported_capability"].includes(error.code));
        this.live.delete(row.remote_id);
        // Nothing is queued (design 3.12): an unsent message fails at once
        // and stays readable in this conversation. A schedule's claim is
        // released so its scheduler records the occurrence as missed.
        if (unsent && scheduleClaim) this.store.restoreUnsentSubmission(scope, requestID, scheduleClaim);
        else if (unsent) this.store.setLinkRunState(scope, requestID, "failed", { error: unsentReason(error) });
        this.onChange();
        // A sent request without a reply is retried with the same
        // client_message_id when the link returns (design 3.12).
        throw userError(error, "The message could not be sent.");
      }
      this.store.acceptLinkSend(scope, requestID, { runID: result.run_id, message: result.message });
      const live = this.live.get(row.remote_id);
      if (live) {
        if (live.runID && live.runID !== result.run_id) { live.preview = ""; live.progress = ""; }
        live.runID = result.run_id;
        if (live.final) { const final = live.final; delete live.final; this.#applyStatus(scope, requestID, final); }
      }
      this.onChange();
      return this.#view(scope, requestID);
    });
  }

  reconcile(scope, requestID) {
    return this.#serialized(requestID, async () => {
      const task = this.store.linkTask(scope, requestID);
      if (!task.explicitly_submitted) throw new Error("Task has not been explicitly submitted");
      await this.#reconcile(scope, task);
      return this.#view(scope, requestID);
    });
  }

  async reconcilePending() {
    const scope = this.getIdentity();
    if (!scope || !this.#link().online) return;
    const rows = this.store.linkPending(scope);
    // A claimed occurrence belongs to its scheduler until it is sent; the
    // scheduler reconciles it itself.
    await Promise.allSettled(rows.map(row => this.#serialized(row.request_id, async () => {
      const task = this.store.linkTask(scope, row.request_id);
      if (!task.submission_claim) await this.#reconcile(scope, task);
    })));
  }

  async #reconcile(scope, task) {
    const row = this.store.linkConversation(scope, task.conversation_id);
    if (!row) {
      // Its conversation never reached the backend, so neither did the
      // message (a crash before the first send).
      if (task.status === "submission_pending" && !task.run_id) {
        this.store.setLinkRunState(scope, task.request_id, "failed", { error: "Not sent: the message never reached the backend. Send it again if it is still needed." });
        this.#terminal(scope, task.request_id);
        this.onChange();
      }
      return;
    }
    if (!task.run_id) {
      if (task.status !== "submission_pending") return;
      // The original send got no reply. A retry only learns its outcome
      // (design 3.12: nothing is queued): the same client_message_id returns
      // the original admission, and a message that never arrived is not run
      // late. The attachments are resolved again from their upload keys, so
      // the retry carries exactly the original input.
      const context = JSON.parse(task.context_json);
      let result;
      try {
        const attachments = await this.#upload(scope, task, row.remote_id, context.input_files || []);
        result = await this.#request("message.send", { session_id: row.remote_id, client_message_id: task.request_id, content: context.messages.at(-1)?.content ?? "",
          ...(attachments.length ? { attachments } : {}), client_timezone: Intl.DateTimeFormat().resolvedOptions().timeZone, ...presentationFor(context, task.conversation_id), replay_only: true });
      } catch (error) {
        if (!(error instanceof LinkError && error.code === "not_found" && error.notSent)) throw error;
        this.store.setLinkRunState(scope, task.request_id, "failed", { error: "Not sent: the message never reached the backend. Send it again if it is still needed." });
        this.#terminal(scope, task.request_id);
        this.onChange();
        return;
      }
      this.store.acceptLinkSend(scope, task.request_id, { runID: result.run_id, message: result.message });
      task = this.store.linkTask(scope, task.request_id);
    }
    await this.#subscribe(scope, row.remote_id).catch(() => {});
    const status = await this.#request("run.status", { session_id: row.remote_id, run_id: task.run_id });
    if (status.run_id === task.run_id && status.state) { this.#applyStatus(scope, task.request_id, status); return; }
    // The backend no longer tracks this run: its stored reply decides.
    await this.refreshMessages(scope, task.conversation_id).catch(() => {});
    const done = this.store.linkReplied(scope, task.request_id);
    this.store.setLinkRunState(scope, task.request_id, done ? "completed" : "failed", done ? {} : { error: "The run ended without a reply. Send the message again if it is still needed." });
    this.#terminal(scope, task.request_id);
    this.onChange();
  }

  async #subscribe(scope, remoteID) {
    if (this.subscribed.has(remoteID)) return;
    const result = await this.#request("run.subscribe", { session_id: remoteID });
    this.subscribed.add(remoteID);
    const status = result?.status;
    const live = this.live.get(remoteID) || { runID: "", preview: "", progress: "" };
    // Events after this snapshot continue from its seq. Events that
    // overtook the reply are newer than the snapshot: read the run back.
    if (Number.isSafeInteger(status?.seq) && (live.seq || 0) > status.seq) { this.live.set(remoteID, live); void this.#resync(this.getIdentity(), remoteID).catch(() => {}); return; }
    if (Number.isSafeInteger(status?.seq)) live.seq = status.seq;
    if (status?.run_id && ["running", "approval_pending"].includes(status.state)) {
      if (live.runID !== status.run_id) Object.assign(live, { runID: status.run_id, preview: status.preview || "", progress: status.progress || "" });
      else if ((status.preview || "").length > live.preview.length) live.preview = status.preview;
    }
    this.live.set(remoteID, live);
  }

  // Events were lost (a seq gap): the snapshot replaces the preview.
  async #resync(scope, remoteID) {
    const key = `resync:${remoteID}`;
    if (this.flights.has(key)) return this.flights.get(key);
    const flight = (async () => {
      const status = await this.#request("run.status", { session_id: remoteID });
      const live = this.live.get(remoteID) || { runID: "", preview: "", progress: "" };
      if (Number.isSafeInteger(status?.seq)) live.seq = status.seq;
      if (status?.run_id) {
        if (live.runID !== status.run_id) Object.assign(live, { runID: status.run_id, progress: "" });
        live.preview = status.preview || "";
        live.progress = status.progress || live.progress;
      }
      this.live.set(remoteID, live);
      const task = status?.run_id ? this.store.linkTaskByRun(scope, status.run_id) : undefined;
      if (task) this.#applyStatus(scope, task.request_id, status);
      else this.#changed();
    })().finally(() => this.flights.delete(key));
    this.flights.set(key, flight);
    return flight;
  }

  #event(frame) {
    const scope = this.getIdentity();
    if (!scope) return;
    if (frame.topic === "sync") { void this.sync().catch(() => {}); return; }
    if (frame.topic === "approval") { void this.#refreshApprovals(scope).catch(() => {}); return; }
    if (frame.topic !== "run") return;
    const event = frame.data || {};
    if (typeof event.session_id !== "string") return;
    const live = this.live.get(event.session_id) || { runID: "", preview: "", progress: "" };
    this.live.set(event.session_id, live);
    // Events are numbered per conversation. A repeat (the same event over a
    // second path) is ignored; a gap means some were lost, so the run is
    // read back instead of showing a preview with a hole in it.
    if (Number.isSafeInteger(event.seq) && event.seq > 0) {
      if (Number.isSafeInteger(live.seq) && event.seq <= live.seq) return;
      const gap = Number.isSafeInteger(live.seq) && event.seq > live.seq + 1;
      live.seq = event.seq;
      if (gap) { void this.#resync(scope, event.session_id).catch(() => {}); return; }
    }
    if (event.run_id && live.runID && live.runID !== event.run_id) { live.runID = event.run_id; live.preview = ""; live.progress = ""; }
    if (event.run_id && !live.runID) live.runID = event.run_id;
    if (event.kind === "delta" && typeof event.text === "string") { live.preview += event.text; this.#changed(); return; }
    if (event.kind === "progress") { live.progress = event.text || ""; return; }
    if (!event.status) return;
    const task = event.run_id ? this.store.linkTaskByRun(scope, event.run_id) : undefined;
    if (!task) {
      // The final event can overtake the send reply; keep it for the send.
      if (event.kind === "final" && live.requestID) live.final = event.status;
      return;
    }
    this.#applyStatus(scope, task.request_id, event.status);
  }

  #applyStatus(scope, requestID, status) {
    if (status.preview && ["running", "approval_pending"].includes(status.state)) {
      const row = this.store.linkConversation(scope, this.store.linkTask(scope, requestID).conversation_id);
      const live = row && this.live.get(row.remote_id);
      if (live && status.preview.length > live.preview.length) live.preview = status.preview;
    }
    if (Array.isArray(status.pending_approvals)) {
      const pending = status.pending_approvals.filter(a => !a.run_id || a.run_id === status.run_id);
      this.approvalCache.set(requestID, pending.map(a => localApproval(a)));
    }
    const state = status.state === "idle" ? undefined : status.state;
    if (!state) return;
    const error = status.error?.message;
    this.store.setLinkRunState(scope, requestID, state, { finalMessage: status.final_message, error });
    if (!["running", "approval_pending"].includes(state)) {
      if (state === "completed" || state === "failed" || state === "cancelled") this.approvalCache.delete(requestID);
      this.#terminal(scope, requestID);
      const task = this.store.linkTask(scope, requestID);
      void this.refreshMessages(scope, task.conversation_id).catch(() => {});
    }
    this.onChange();
  }

  #terminal(scope, requestID) {
    let task;
    try { task = this.store.linkTask(scope, requestID); } catch { return; }
    const row = this.store.linkConversation(scope, task.conversation_id);
    if (row) this.live.delete(row.remote_id);
    this.onTaskTerminal({ owner_id: scope.owner_id, client_id: scope.client_id, installation_id: this.store.installationID,
      local_conversation_id: task.conversation_id, local_task_id: task.id }, task.status === "canceled" ? "task_canceled" : "task_ended");
  }

  async #refreshApprovals(scope) {
    const rows = this.store.linkPending(scope);
    for (const { request_id } of rows) {
      const task = this.store.linkTask(scope, request_id);
      const row = this.store.linkConversation(scope, task.conversation_id);
      if (!row) continue;
      const list = await this.#request("approval.list", { session_id: row.remote_id, pending: true });
      const pending = (list.approvals || []).filter(a => !a.run_id || !task.run_id || a.run_id === task.run_id);
      this.approvalCache.set(request_id, pending.map(a => localApproval(a)));
      if (pending.length && task.status === "running") this.store.setLinkRunState(scope, request_id, "approval_pending");
    }
    this.onChange();
  }

  async decideApproval(scope, requestID, approvalID, digest, decision, inputs) {
    return this.#serialized(`approval:${requestID}`, async () => {
      const approval = (this.approvalCache.get(requestID) || []).find(a => a.approval_id === approvalID);
      if (!approval || approval.digest !== digest || !["approve", "reject"].includes(decision)) throw new Error("Approval identity or digest mismatch");
      const key = `${requestID}:${approvalID}`;
      const prior = this.decisions.get(key);
      if (prior && prior.decision !== decision) throw new Error("Approval decision cannot be changed");
      this.decisions.set(key, { state: "decision_pending", decision });
      this.onChange();
      try {
        await this.#request("approval.resolve", { approval_id: approvalID, decision, ...(inputs ? { inputs } : {}),
          idempotency_key: stableUUID(`sparkx-approval\u0000${requestID}\u0000${approvalID}\u0000${decision}`) }, { timeoutMS: 120000 });
      } catch (error) {
        if (linkNotSent(error)) this.decisions.delete(key);
        else this.decisions.set(key, { state: "decision_unknown", decision });
        this.onChange();
        throw userError(error, "The approval decision could not be recorded.");
      }
      this.decisions.set(key, { state: decision === "approve" ? "approved" : "rejected", decision });
      this.approvalCache.set(requestID, (this.approvalCache.get(requestID) || []).filter(a => a.approval_id !== approvalID));
      const task = this.store.linkTask(scope, requestID);
      if (task.status === "approval_pending" && !(this.approvalCache.get(requestID) || []).length) this.store.setLinkRunState(scope, requestID, "running");
      this.onChange();
      void this.reconcile(scope, requestID).catch(() => {});
      return { resolved: true };
    });
  }

  async cancel(scope, requestID) {
    return this.#serialized(requestID, async () => {
      const task = this.store.linkTask(scope, requestID);
      if (!task.explicitly_submitted || !["submission_pending", "accepted", "running", "approval_pending", "cancel_pending"].includes(task.status)) throw new Error("Task cannot be canceled");
      const row = this.store.linkConversation(scope, task.conversation_id);
      this.onCancelTask({ owner_id: scope.owner_id, client_id: scope.client_id, installation_id: this.store.installationID,
        local_conversation_id: task.conversation_id, local_task_id: task.id });
      let status;
      try { status = await this.#request("run.cancel", { session_id: row.remote_id, ...(task.run_id ? { run_id: task.run_id } : {}) }); }
      catch (error) { throw userError(error, "The task could not be canceled."); }
      if (status?.state === "cancelled" || status?.state === "idle") this.store.setLinkRunState(scope, requestID, "cancelled");
      else if (status) this.#applyStatus(scope, requestID, status);
      this.#terminal(scope, requestID);
      this.onChange();
      return this.#view(scope, requestID);
    });
  }

  #view(scope, requestID) {
    const task = this.store.linkTask(scope, requestID);
    return { id: task.id, request_id: task.request_id, status: task.status, created_at: task.created_at };
  }
}

// The desktop's local schedule display (schedule-presentation.mjs) is
// declared with the message, as the previous execution envelope did; the
// backend answers with a client action in the final message.
function presentationFor(context, conversationID) {
  const presentation = context?.client_presentation;
  if (!presentation?.schedule_show) return {};
  return { client_presentation: { schema_version: presentation.schema_version, schedule_show: true, timezone: presentation.timezone,
    requested_at: presentation.requested_at, local_conversation_id: conversationID } };
}
