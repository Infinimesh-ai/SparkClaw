import crypto from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import { DatabaseSync } from "node:sqlite";
import { captureFileBoundary, readOwnedFile } from "./local-file-boundary.mjs";
import { WORKBENCH_LIMITS } from "../shared/workbench-limits.mjs";

export const CLIENT_SCHEMA_VERSION = 8;
export const CLIENT_LIMITS = WORKBENCH_LIMITS;

const UUID = /^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/u;
const ID = /^[A-Za-z0-9][A-Za-z0-9_.:-]{0,159}$/u;

// This database contains only this installation's data. No server Store is
// opened, imported or used as a fallback, including on a local disk failure.
export class ClientStore {
  constructor(root) {
    if (!path.isAbsolute(root)) throw new Error("ClientStore requires an absolute private directory");
    privateDirectory(root);
    this.root = root;
    this.filesRoot = path.join(root, "files");
    privateDirectory(this.filesRoot);
    this.fileBoundary = captureFileBoundary(this.filesRoot);
    const databasePath = path.join(root, "client.sqlite");
    privateFile(databasePath);
    this.db = new DatabaseSync(databasePath);
    try {
      this.db.exec("PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL; PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;");
      const version = this.db.prepare("PRAGMA user_version").get().user_version;
      if (version !== 0 && version !== 6 && version !== 7 && version !== CLIENT_SCHEMA_VERSION) throw new Error("ClientStore schema is unsupported; use this release's fresh workbench directory");
      if (version === 0) this.#initialize();
      if (version === 6) this.#transaction(() => {
        this.db.exec(`CREATE TABLE execution_projection(request_id TEXT PRIMARY KEY REFERENCES tasks(request_id),revision INTEGER NOT NULL,termination_reason TEXT NOT NULL,approval_receipts TEXT NOT NULL,event_state TEXT NOT NULL);
          CREATE TABLE event_projection(scope TEXT PRIMARY KEY,cursor TEXT NOT NULL,revision INTEGER NOT NULL,snapshot TEXT NOT NULL,epoch TEXT NOT NULL DEFAULT '');
          PRAGMA user_version=${CLIENT_SCHEMA_VERSION};`);
      });
      if (version === 7) this.#transaction(() => { this.db.exec(`ALTER TABLE event_projection ADD COLUMN epoch TEXT NOT NULL DEFAULT ''; PRAGMA user_version=${CLIENT_SCHEMA_VERSION};`); });
      this.installationID = this.db.prepare("SELECT value FROM metadata WHERE key='installation_id'").get().value;
      // Incomplete atomic file writes are never treated as delivered files.
      const committedFiles = new Set(this.db.prepare("SELECT id FROM files").all().map((file) => file.id));
      for (const entry of fs.readdirSync(this.filesRoot)) {
        if (/^\.pending-[a-f0-9-]+$/u.test(entry) || (UUID.test(entry) && !committedFiles.has(entry))) {
          fs.unlinkSync(path.join(this.filesRoot, entry));
        }
      }
    } catch (error) {
      this.db.close();
      throw error;
    }
  }

  #initialize() {
    this.#transaction(() => {
      this.db.exec(`
        CREATE TABLE metadata(key TEXT PRIMARY KEY, value TEXT NOT NULL);
        CREATE TABLE conversations(id TEXT PRIMARY KEY, scope TEXT NOT NULL, title TEXT NOT NULL,
          created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
        CREATE TABLE messages(id TEXT PRIMARY KEY, conversation_id TEXT NOT NULL REFERENCES conversations(id),
          role TEXT NOT NULL CHECK(role IN ('user','assistant')), content TEXT NOT NULL, created_at TEXT NOT NULL);
        CREATE TABLE drafts(scope TEXT NOT NULL, conversation_id TEXT NOT NULL, content TEXT NOT NULL,
          local_file_ids TEXT NOT NULL, revision INTEGER NOT NULL, PRIMARY KEY(scope,conversation_id));
        CREATE TABLE tasks(id TEXT PRIMARY KEY, conversation_id TEXT NOT NULL REFERENCES conversations(id),
          request_id TEXT UNIQUE NOT NULL, input_digest TEXT NOT NULL, context_json TEXT NOT NULL,
          status TEXT NOT NULL, created_at TEXT NOT NULL, explicitly_submitted INTEGER NOT NULL DEFAULT 0,
          updated_at TEXT, submission_claim TEXT);
        CREATE TABLE files(id TEXT PRIMARY KEY, conversation_id TEXT NOT NULL REFERENCES conversations(id),
          name TEXT NOT NULL, sha256 TEXT NOT NULL, size INTEGER NOT NULL, created_at TEXT NOT NULL);
        CREATE TABLE deliveries(request_id TEXT NOT NULL REFERENCES tasks(request_id), sequence INTEGER NOT NULL,
          digest TEXT NOT NULL, acknowledged INTEGER NOT NULL DEFAULT 0, PRIMARY KEY(request_id, sequence));
        CREATE TABLE delivery_files(request_id TEXT NOT NULL, sequence INTEGER NOT NULL,
          file_id TEXT NOT NULL REFERENCES files(id), remote_id TEXT NOT NULL,
          PRIMARY KEY(request_id, sequence, remote_id),
          FOREIGN KEY(request_id, sequence) REFERENCES deliveries(request_id, sequence));
        CREATE TABLE schedule_definitions(id TEXT PRIMARY KEY, interval_ms INTEGER NOT NULL,
          state TEXT NOT NULL CHECK(state IN ('active','completed','canceled')));
        CREATE TABLE schedules(request_id TEXT PRIMARY KEY REFERENCES tasks(request_id),
          schedule_id TEXT NOT NULL REFERENCES schedule_definitions(id), due_at TEXT NOT NULL,
          state TEXT NOT NULL, created_at TEXT NOT NULL, claimed_at TEXT,
          missed_count INTEGER NOT NULL DEFAULT 0, missed_until TEXT,
          recovery_request_id TEXT REFERENCES tasks(request_id), UNIQUE(schedule_id,due_at));
        CREATE INDEX schedules_by_due ON schedules(state,due_at);
        CREATE TABLE execution_approvals(request_id TEXT NOT NULL REFERENCES tasks(request_id),
          approval_id TEXT NOT NULL,digest TEXT NOT NULL,tool TEXT NOT NULL,summary TEXT NOT NULL,
          arguments_json TEXT NOT NULL,state TEXT NOT NULL,decision TEXT,expires_at TEXT NOT NULL,
          PRIMARY KEY(request_id,approval_id));
        CREATE TABLE execution_projection(request_id TEXT PRIMARY KEY REFERENCES tasks(request_id),revision INTEGER NOT NULL,termination_reason TEXT NOT NULL,approval_receipts TEXT NOT NULL,event_state TEXT NOT NULL);
        CREATE TABLE event_projection(scope TEXT PRIMARY KEY,cursor TEXT NOT NULL,revision INTEGER NOT NULL,snapshot TEXT NOT NULL,epoch TEXT NOT NULL DEFAULT '');
        CREATE INDEX conversations_by_scope ON conversations(scope, updated_at);
        PRAGMA user_version=${CLIENT_SCHEMA_VERSION};
      `);
      this.db.prepare("INSERT INTO metadata VALUES('installation_id',?)").run(crypto.randomUUID());
    });
  }

  list(scope) {
    return this.db.prepare("SELECT id,title,created_at,updated_at FROM conversations WHERE scope=? ORDER BY updated_at DESC,id")
      .all(scopeKey(scope));
  }

  create(scope, title) {
    title = boundedText(title, 320, "Conversation title").trim() || "New conversation";
    const now = new Date().toISOString();
    const conversation = { id: crypto.randomUUID(), title, created_at: now, updated_at: now };
    this.db.prepare("INSERT INTO conversations VALUES(?,?,?,?,?)")
      .run(conversation.id, scopeKey(scope), title, now, now);
    return conversation;
  }

  read(scope, conversationID) {
    this.#conversation(scope, conversationID);
    return {
      messages: this.db.prepare("SELECT id,role,content,created_at FROM messages WHERE conversation_id=? ORDER BY rowid").all(conversationID),
      tasks: this.db.prepare("SELECT id,request_id,status,explicitly_submitted,created_at FROM tasks WHERE conversation_id=? ORDER BY rowid").all(conversationID)
        .map((task) => ({ ...task, ...this.executionProjection(scope, task.request_id), approvals: this.approvals(scope, task.request_id) })),
      files: this.db.prepare("SELECT id,name,sha256,size,created_at FROM files WHERE conversation_id=? ORDER BY rowid").all(conversationID),
      schedules: this.db.prepare(`SELECT s.*,d.interval_ms,d.state AS definition_state FROM schedules s
        JOIN schedule_definitions d ON d.id=s.schedule_id JOIN tasks t ON t.request_id=s.request_id
        WHERE t.conversation_id=? ORDER BY s.rowid`).all(conversationID),
    };
  }

  draft(scope, conversationID) {
    if (conversationID !== "") this.#conversation(scope, conversationID);
    const row = this.db.prepare("SELECT content,local_file_ids,revision FROM drafts WHERE scope=? AND conversation_id=?").get(scopeKey(scope), conversationID);
    return row ? { ...row, local_file_ids: JSON.parse(row.local_file_ids) } : { content: "", local_file_ids: [], revision: 0 };
  }

  saveDraft(scope, conversationID, content, localFileIDs, revision) {
    boundedText(content, CLIENT_LIMITS.inputBytes, "Draft");
    if (!Number.isSafeInteger(revision) || revision < 0 || !Array.isArray(localFileIDs) || localFileIDs.length > CLIENT_LIMITS.resultFiles ||
        new Set(localFileIDs).size !== localFileIDs.length) throw new Error("Invalid draft revision or attachments");
    for (const id of localFileIDs) {
      uuid(id);
      if (!this.db.prepare("SELECT id FROM files WHERE id=? AND conversation_id=?").get(id, conversationID)) throw new Error("Draft file is not in this conversation");
      this.file(scope, id);
    }
    return this.#transaction(() => {
      const prior = this.draft(scope, conversationID);
      if (prior.revision !== revision) throw new Error("Draft changed in another editor; reload before saving");
      this.db.prepare(`INSERT INTO drafts VALUES(?,?,?,?,?) ON CONFLICT(scope,conversation_id) DO UPDATE SET
        content=excluded.content,local_file_ids=excluded.local_file_ids,revision=excluded.revision`)
        .run(scopeKey(scope), conversationID, content, JSON.stringify(localFileIDs), revision + 1);
      return this.draft(scope, conversationID);
    });
  }

  moveWelcomeDraft(scope, conversationID, revision) {
    return this.#transaction(() => {
      const source = this.draft(scope, "");
      const destination = this.draft(scope, conversationID);
      if (!conversationID || source.revision !== revision || destination.revision !== 0) throw new Error("Draft changed before moving to the conversation");
      this.db.prepare("INSERT INTO drafts VALUES(?,?,?,?,1)").run(scopeKey(scope), conversationID, source.content, JSON.stringify(source.local_file_ids));
      this.db.prepare(`INSERT INTO drafts VALUES(?,'','','[]',1) ON CONFLICT(scope,conversation_id) DO UPDATE SET
        content='',local_file_ids='[]',revision=drafts.revision+1`).run(scopeKey(scope));
      return { source: this.draft(scope, ""), draft: this.draft(scope, conversationID) };
    });
  }

  enqueueDraft(scope, conversationID, draftConversationID, revision) {
    if (draftConversationID !== "" && draftConversationID !== conversationID) throw new Error("Draft belongs to a different conversation");
    const draft = this.draft(scope, draftConversationID);
    const task = this.enqueue(scope, conversationID, draft.content, draft.local_file_ids, undefined, undefined,
      { conversationID: draftConversationID, revision });
    return { task, draft: this.draft(scope, draftConversationID) };
  }

  enqueue(scope, conversationID, content, localFileIDs = [], scheduleSpec, recoveringScheduleID, consumedDraft) {
    this.#conversation(scope, conversationID);
    content = boundedText(content, CLIENT_LIMITS.inputBytes, "Input").trim();
    if (!content) throw new Error("Input is empty");
    if (!Array.isArray(localFileIDs) || localFileIDs.length > CLIENT_LIMITS.resultFiles || new Set(localFileIDs).size !== localFileIDs.length) throw new Error("Invalid input file selection");
    const inputFiles = localFileIDs.map((id) => {
      uuid(id);
      const record = this.db.prepare("SELECT id,name,size,sha256 FROM files WHERE id=? AND conversation_id=?").get(id, conversationID);
      if (!record || record.size > CLIENT_LIMITS.resultBytes) throw new Error("Input file is unavailable or exceeds 8 MiB");
      this.file(scope, id);
      return record;
    });
    if (new Set(inputFiles.map((file) => file.name)).size !== inputFiles.length) throw new Error("Selected input files need different names");
    const requestID = crypto.randomUUID();
    const taskID = crypto.randomUUID();
    const now = new Date().toISOString();
    this.#transaction(() => {
      if (consumedDraft && this.draft(scope, consumedDraft.conversationID).revision !== consumedDraft.revision) throw new Error("Draft changed before submission; review the saved draft");
      if (recoveringScheduleID && this.scheduledRequest(scope, recoveringScheduleID).state !== "missed") throw new Error("Only an unexecuted missed schedule can run now");
      const history = this.db.prepare("SELECT role,content FROM messages WHERE conversation_id=? ORDER BY rowid DESC LIMIT ?")
        .all(conversationID, CLIENT_LIMITS.contextMessages).reverse();
      const envelope = { schema_version: 1, ...scope, installation_id: this.installationID,
        local_conversation_id: conversationID, local_task_id: taskID, request_id: requestID, messages: [],
        ...(inputFiles.length ? { input_files: inputFiles } : {}) };
      const messageBudget = CLIENT_LIMITS.contextBytes - Buffer.byteLength(JSON.stringify(envelope)) + 2;
      const context = boundedContext([...history, { role: "user", content }], messageBudget);
      const snapshot = JSON.stringify({ ...envelope, messages: context });
      if (inputFiles.reduce((sum, file) => sum + file.size, Buffer.byteLength(snapshot)) > 32 * 1024 * 1024) throw new Error("Input exceeds 32 MiB task budget");
      const digest = hash(snapshot);
      this.db.prepare("INSERT INTO messages VALUES(?,?,?,?,?)")
        .run(crypto.randomUUID(), conversationID, "user", content, now);
      this.db.prepare("INSERT INTO tasks(id,conversation_id,request_id,input_digest,context_json,status,created_at) VALUES(?,?,?,?,?,?,?)")
        .run(taskID, conversationID, requestID, digest, snapshot, scheduleSpec ? "scheduled_local" : "awaiting_runtime", now);
      if (scheduleSpec) {
        this.db.prepare("INSERT INTO schedule_definitions VALUES(?,?,'active')").run(requestID, scheduleSpec.intervalMS);
        this.db.prepare("INSERT INTO schedules(request_id,schedule_id,due_at,state,created_at) VALUES(?,?,?,'saved',?)")
          .run(requestID, requestID, scheduleSpec.dueAt, now);
      }
      if (recoveringScheduleID) this.db.prepare("UPDATE schedules SET state='run_now',recovery_request_id=? WHERE request_id=?").run(requestID, recoveringScheduleID);
      this.db.prepare("UPDATE conversations SET updated_at=? WHERE id=?").run(now, conversationID);
      if (consumedDraft) this.db.prepare("UPDATE drafts SET content='',local_file_ids='[]',revision=revision+1 WHERE scope=? AND conversation_id=?")
        .run(scopeKey(scope), consumedDraft.conversationID);
    });
    // Network submission is deliberately a separate operation. This return is
    // durable local queuing, not server acceptance or completed execution.
    return { id: taskID, request_id: requestID, status: scheduleSpec ? "scheduled_local" : "awaiting_runtime", created_at: now };
  }

  schedule(scope, conversationID, content, dueAt, now = Date.now(), intervalMS = 0) {
    const maxDelay = 366 * 24 * 60 * 60 * 1000;
    if (typeof dueAt !== "string" || !Number.isFinite(Date.parse(dueAt)) ||
        Date.parse(dueAt) <= now || Date.parse(dueAt) > now + maxDelay) throw new Error("Schedule must be within the next 366 days");
    if (!Number.isSafeInteger(intervalMS) || intervalMS < 0 ||
        (intervalMS !== 0 && (intervalMS < 60000 || intervalMS > maxDelay))) throw new Error("Schedule interval must be 0 or between one minute and 366 days");
    const task = this.enqueue(scope, conversationID, content, [], { dueAt: new Date(dueAt).toISOString(), intervalMS });
    return this.scheduledRequest(scope, task.request_id);
  }

  scheduledRequest(scope, requestID) {
    const task = this.request(scope, requestID);
    const schedule = this.db.prepare(`SELECT s.*,d.interval_ms,d.state AS definition_state FROM schedules s
      JOIN schedule_definitions d ON d.id=s.schedule_id WHERE s.request_id=?`).get(requestID);
    if (!schedule) throw new Error("Local schedule not found");
    return { ...task, ...schedule };
  }

  runScheduledNow(scope, requestID) {
    const schedule = this.scheduledRequest(scope, requestID);
    if (schedule.state !== "missed") throw new Error("Only an unexecuted missed schedule can run now");
    return this.enqueue(scope, schedule.conversation_id, JSON.parse(schedule.context_json).messages.at(-1).content, [], undefined, requestID);
  }

  schedulePending(scope) {
    return this.db.prepare(`SELECT s.request_id FROM schedules s JOIN tasks t ON t.request_id=s.request_id
      JOIN conversations c ON c.id=t.conversation_id WHERE c.scope=? AND s.state IN
      ('saved','claimed','submission_pending','cancel_pending') ORDER BY s.due_at,s.rowid`).all(scopeKey(scope));
  }

  claimSchedule(scope, requestID, now) {
    return this.#transaction(() => {
      const schedule = this.scheduledRequest(scope, requestID);
      if (schedule.state !== "saved" || schedule.definition_state !== "active" || Date.parse(schedule.due_at) > now) return null;
      const claim = crypto.randomUUID();
      // Claim and submission intent are one durable transaction. Only this live
      // dispatch gets the capability to make the first POST; restart only GETs.
      this.db.prepare("UPDATE tasks SET explicitly_submitted=1,status='submission_pending',submission_claim=?,updated_at=? WHERE request_id=?")
        .run(claim, new Date(now).toISOString(), requestID);
      this.db.prepare("UPDATE schedules SET state='claimed',claimed_at=? WHERE request_id=?").run(new Date(now).toISOString(), requestID);
      this.#advanceSchedule(schedule, Date.parse(schedule.due_at) + schedule.interval_ms, now);
      return claim;
    });
  }

  missUnsentScheduleClaim(scope, requestID, claim) {
    return this.#transaction(() => {
      const schedule = this.scheduledRequest(scope, requestID);
      if (schedule.submission_claim !== claim || schedule.status !== "submission_pending") return;
      this.db.prepare("UPDATE tasks SET submission_claim=NULL,explicitly_submitted=0,status='schedule_missed' WHERE request_id=?").run(requestID);
      this.db.prepare("UPDATE schedules SET state='missed',missed_count=1,missed_until=due_at WHERE request_id=?").run(requestID);
    });
  }

  missSchedule(scope, requestID, now) {
    return this.#transaction(() => {
      const schedule = this.scheduledRequest(scope, requestID);
      if (schedule.state !== "saved" || Date.parse(schedule.due_at) > now) return false;
      const due = Date.parse(schedule.due_at);
      const count = schedule.interval_ms ? Math.floor((now - due) / schedule.interval_ms) + 1 : 1;
      const lastDue = due + (count - 1) * schedule.interval_ms;
      // A closed-form missed range bounds a years-long offline scan. Every
      // skipped due time remains derivable from due_at/interval/count; none is queued.
      this.db.prepare("UPDATE schedules SET state='missed',missed_count=?,missed_until=? WHERE request_id=?")
        .run(count, new Date(lastDue).toISOString(), requestID);
      this.db.prepare("UPDATE tasks SET status='schedule_missed',updated_at=? WHERE request_id=?").run(new Date(now).toISOString(), requestID);
      this.#advanceSchedule(schedule, lastDue + schedule.interval_ms, now);
      return true;
    });
  }

  cancelSchedule(scope, requestID) {
    return this.#transaction(() => {
      const schedule = this.scheduledRequest(scope, requestID);
      this.db.prepare("UPDATE schedule_definitions SET state='canceled' WHERE id=?").run(schedule.schedule_id);
      this.db.prepare(`UPDATE schedules SET state='canceled' WHERE schedule_id=? AND
        (state='saved' OR request_id IN (SELECT request_id FROM tasks WHERE submission_claim IS NOT NULL))`).run(schedule.schedule_id);
      this.db.prepare(`UPDATE tasks SET status='schedule_canceled',submission_claim=NULL WHERE request_id IN
        (SELECT request_id FROM schedules WHERE schedule_id=? AND state='canceled')`).run(schedule.schedule_id);
      return this.scheduledRequest(scope, requestID);
    });
  }

  #advanceSchedule(schedule, nextDue, now) {
    if (!schedule.interval_ms) {
      this.db.prepare("UPDATE schedule_definitions SET state='completed' WHERE id=?").run(schedule.schedule_id);
      return;
    }
    const requestID = crypto.randomUUID();
    const taskID = crypto.randomUUID();
    // Recurrence preserves the explicitly saved input/context. It receives a
    // distinct durable occurrence/request ID, never reuses the prior execution.
    const snapshot = JSON.stringify({ ...JSON.parse(schedule.context_json), local_task_id: taskID, request_id: requestID });
    const created = new Date(now).toISOString();
    this.db.prepare("INSERT INTO tasks(id,conversation_id,request_id,input_digest,context_json,status,created_at) VALUES(?,?,?,?,?,'scheduled_local',?)")
      .run(taskID, schedule.conversation_id, requestID, hash(snapshot), snapshot, created);
    this.db.prepare("INSERT INTO schedules(request_id,schedule_id,due_at,state,created_at) VALUES(?,?,?,'saved',?)")
      .run(requestID, schedule.schedule_id, new Date(nextDue).toISOString(), created);
  }

  context(scope, requestID) {
    return JSON.parse(this.request(scope, requestID).context_json);
  }

  request(scope, requestID) {
    uuid(requestID);
    const task = this.db.prepare(`SELECT t.* FROM tasks t JOIN conversations c ON c.id=t.conversation_id
      WHERE t.request_id=? AND c.scope=?`).get(requestID, scopeKey(scope));
    if (!task) throw new Error("Local request not found");
    if (hash(task.context_json) !== task.input_digest) throw new Error("Local immutable context verification failed");
    return task;
  }

  executionProjection(scope, requestID) {
    this.request(scope, requestID);
    const row = this.db.prepare("SELECT revision,termination_reason,approval_receipts,event_state FROM execution_projection WHERE request_id=?").get(requestID);
    return row ? { ...row, approval_receipts: JSON.parse(row.approval_receipts) } : { revision: 0, termination_reason: "", approval_receipts: [], event_state: "" };
  }

  acceptExecutionProjection(scope, event) {
    this.request(scope, event.request_id);
    if (event.revision === undefined) return true; // Existing HTTP/v1 servers.
    if (!Number.isSafeInteger(event.revision) || event.revision < 1 ||
        (event.termination_reason !== undefined && !["gateway_restarted_awaiting_approval", "gateway_restarted_after_approval"].includes(event.termination_reason)) ||
        !Array.isArray(event.approval_receipts ?? []) || (event.approval_receipts ?? []).length > 32) throw new Error("Invalid execution revision");
    const receipts = event.approval_receipts ?? [];
    for (const receipt of receipts) if (!receipt || !ID.test(receipt.approval_id) || !/^[a-f0-9]{64}$/u.test(receipt.digest) ||
      !["approve", "reject"].includes(receipt.decision) || !["decided", "decision_unknown"].includes(receipt.state) || !Number.isSafeInteger(receipt.revision) || receipt.revision < 1) throw new Error("Invalid approval receipt");
    return this.#transaction(() => {
      const previous = this.executionProjection(scope, event.request_id);
      if (event.revision < previous.revision) return false;
      if (event.revision === previous.revision && (previous.event_state !== event.state || previous.termination_reason !== (event.termination_reason ?? "") || JSON.stringify(previous.approval_receipts) !== JSON.stringify(receipts))) throw new Error("Execution revision conflict");
      if (["completed", "failed", "canceled", "unknown", "delivery_expired", "delivered"].includes(previous.event_state) && ["accepted", "running", "approval_pending", "browser_login_blocked"].includes(event.state)) throw new Error("Terminal execution cannot return to running");
      this.db.prepare(`INSERT INTO execution_projection VALUES(?,?,?,?,?) ON CONFLICT(request_id) DO UPDATE SET
        revision=excluded.revision,termination_reason=excluded.termination_reason,approval_receipts=excluded.approval_receipts,event_state=excluded.event_state`)
        .run(event.request_id, event.revision, event.termination_reason ?? "", JSON.stringify(receipts), event.state);
      for (const receipt of receipts) {
        const cached = this.db.prepare("SELECT digest,decision FROM execution_approvals WHERE request_id=? AND approval_id=?").get(event.request_id, receipt.approval_id);
        if (cached && (cached.digest !== receipt.digest || cached.decision && cached.decision !== receipt.decision)) throw new Error("Approval receipt conflicts with local decision");
        this.db.prepare("UPDATE execution_approvals SET state=?,decision=? WHERE request_id=? AND approval_id=?")
          .run(receipt.state === "decision_unknown" ? "decision_unknown" : receipt.decision === "approve" ? "approved" : "rejected", receipt.decision, event.request_id, receipt.approval_id);
      }
      return true;
    });
  }

  eventProjection(scope) {
    const row = this.db.prepare("SELECT cursor,revision,snapshot,epoch FROM event_projection WHERE scope=?").get(scopeKey(scope));
    return row ? { ...row, snapshot: JSON.parse(row.snapshot) } : { cursor: "", revision: 0, snapshot: {}, epoch: "" };
  }

  commitEventProjection(scope, { previous_cursor, cursor, revision, snapshot, epoch = "", reset = false }) {
    if (typeof epoch !== "string" || epoch.length > 160 || typeof reset !== "boolean" || typeof cursor !== "string" || cursor.length > 4096 || !Number.isSafeInteger(revision) || revision < 1 ||
        !snapshot || typeof snapshot !== "object" || Array.isArray(snapshot) || Buffer.byteLength(JSON.stringify(snapshot)) > CLIENT_LIMITS.resultBytes) throw new Error("Invalid event projection");
    return this.#transaction(() => {
      const previous = this.eventProjection(scope);
      if (previous.cursor !== previous_cursor || (!reset && (epoch !== previous.epoch || revision < previous.revision))) throw new Error("Event cursor conflict; obtain an authoritative snapshot");
      this.db.prepare(`INSERT INTO event_projection VALUES(?,?,?,?,?) ON CONFLICT(scope) DO UPDATE SET
        cursor=excluded.cursor,revision=excluded.revision,snapshot=excluded.snapshot,epoch=excluded.epoch`).run(scopeKey(scope), cursor, revision, JSON.stringify(snapshot), epoch);
      return this.eventProjection(scope);
    });
  }

  approvals(scope, requestID) {
    this.request(scope, requestID);
    return this.db.prepare("SELECT approval_id,digest,tool,summary,arguments_json,state,decision,expires_at FROM execution_approvals WHERE request_id=? ORDER BY rowid")
      .all(requestID).map(({ arguments_json, ...row }) => ({ ...row, arguments: JSON.parse(arguments_json) }));
  }

  syncApprovals(scope, requestID, pending, executionState, expiresAt) {
    this.request(scope, requestID);
    if (!Array.isArray(pending) || pending.length > 32 || (pending.length && executionState !== "running")) throw new Error("Invalid active approval state");
    if (pending.length && (typeof expiresAt !== "string" || !Number.isFinite(Date.parse(expiresAt)))) throw new Error("Invalid approval deadline");
    const seen = new Set();
    for (const row of pending) {
      if (!row || Object.keys(row).sort().join(",") !== "approval_id,arguments,digest,summary,tool" ||
          typeof row.approval_id !== "string" || !ID.test(row.approval_id) || seen.has(row.approval_id) ||
          typeof row.digest !== "string" || !/^[a-f0-9]{64}$/u.test(row.digest) || typeof row.tool !== "string" || !ID.test(row.tool) ||
          typeof row.summary !== "string" || row.summary.includes("\u0000") || !row.arguments ||
          typeof row.arguments !== "object" || Array.isArray(row.arguments) || Buffer.byteLength(JSON.stringify(row)) > 64 * 1024) throw new Error("Invalid approval snapshot");
      seen.add(row.approval_id);
    }
    this.#transaction(() => {
      for (const row of pending) {
        const prior = this.db.prepare("SELECT * FROM execution_approvals WHERE request_id=? AND approval_id=?").get(requestID, row.approval_id);
        const argumentsJSON = JSON.stringify(row.arguments);
        if (prior) {
          if (prior.digest !== row.digest || prior.tool !== row.tool || prior.arguments_json !== argumentsJSON || prior.summary !== row.summary || prior.expires_at !== expiresAt) throw new Error("Approval replay changed content or deadline");
        } else this.db.prepare("INSERT INTO execution_approvals VALUES(?,?,?,?,?,?,'pending',NULL,?)")
          .run(requestID, row.approval_id, row.digest, row.tool, row.summary, argumentsJSON, expiresAt);
      }
      for (const prior of this.db.prepare("SELECT approval_id,state,decision FROM execution_approvals WHERE request_id=?").all(requestID)) {
        if (seen.has(prior.approval_id) || !["pending", "decision_pending"].includes(prior.state)) continue;
        const state = prior.decision ? "decision_unknown" : executionState === "running" ? "resolved" : "expired";
        this.db.prepare("UPDATE execution_approvals SET state=? WHERE request_id=? AND approval_id=?").run(state, requestID, prior.approval_id);
      }
    });
  }

  approvalDecision(scope, requestID, approvalID, digest, decision, resolved = false) {
    const row = this.approvals(scope, requestID).find((approval) => approval.approval_id === approvalID);
    if (!row || row.digest !== digest || !["approve", "reject"].includes(decision)) throw new Error("Approval identity or digest mismatch");
    if (row.decision && row.decision !== decision) throw new Error("Approval decision cannot be reversed");
    if (!["pending", "decision_pending", "approved", "rejected"].includes(row.state)) throw new Error("Approval is no longer actionable");
    const state = resolved ? (decision === "approve" ? "approved" : "rejected") : "decision_pending";
    this.db.prepare("UPDATE execution_approvals SET state=?,decision=? WHERE request_id=? AND approval_id=?")
      .run(state, decision, requestID, approvalID);
    return { resolved };
  }

  markSubmitted(scope, requestID, scheduleClaim) {
    return this.#transaction(() => {
      const task = this.request(scope, requestID);
      if (task.explicitly_submitted) {
        if (!scheduleClaim || task.submission_claim !== scheduleClaim || task.status !== "submission_pending") return { first: false, task };
        this.db.prepare("UPDATE tasks SET submission_claim=NULL WHERE request_id=?").run(requestID);
        return { first: true, task: this.request(scope, requestID) };
      }
      if (task.status !== "awaiting_runtime") throw new Error("Task cannot be submitted");
      this.db.prepare("UPDATE tasks SET explicitly_submitted=1,status='submission_pending',updated_at=? WHERE request_id=?")
        .run(new Date().toISOString(), requestID);
      return { first: true, task: this.request(scope, requestID) };
    });
  }

  pending(scope) {
    return this.db.prepare(`SELECT t.request_id FROM tasks t JOIN conversations c ON c.id=t.conversation_id
      WHERE c.scope=? AND t.explicitly_submitted=1 AND t.status IN
      ('submission_pending','accepted','running','cancel_pending','saved') ORDER BY t.rowid`).all(scopeKey(scope));
  }

  restoreUnsentSubmission(scope, requestID, scheduleClaim) {
    return this.#transaction(() => {
      const task = this.request(scope, requestID);
      if (!task.explicitly_submitted || task.status !== "submission_pending" || task.submission_claim) throw new Error("Submission is no longer unsent");
      if (scheduleClaim) {
        // Let the owning scheduler permanently miss this unsent occurrence;
        // restoring its claim must not make a schedule eligible for catch-up.
        const schedule = this.scheduledRequest(scope, requestID);
        if (schedule.state !== "claimed") throw new Error("Schedule is no longer claimed");
        this.db.prepare("UPDATE tasks SET submission_claim=? WHERE request_id=?").run(scheduleClaim, requestID);
      } else {
        this.db.prepare("UPDATE tasks SET explicitly_submitted=0,status='awaiting_runtime',updated_at=? WHERE request_id=?")
          .run(new Date().toISOString(), requestID);
      }
    });
  }

  setExecutionState(scope, requestID, state) {
    const task = this.request(scope, requestID);
    if (!task.explicitly_submitted) throw new Error("Task has not been explicitly submitted");
    if (!["submission_pending", "accepted", "running", "cancel_pending", "failed", "canceled", "unknown", "delivery_expired", "delivery_too_large", "delivered"].includes(state)) {
      throw new Error("Invalid execution state");
    }
    // A durable delivery remains retryable for ACK after a lost response or expiry.
    const receipt = this.db.prepare("SELECT acknowledged FROM deliveries WHERE request_id=? ORDER BY sequence DESC LIMIT 1").get(requestID);
    if (receipt) state = receipt.acknowledged ? "delivered" : "saved";
    this.db.prepare("UPDATE tasks SET status=?,updated_at=? WHERE request_id=?")
      .run(state, new Date().toISOString(), requestID);
    this.db.prepare("UPDATE schedules SET state=? WHERE request_id=? AND state NOT IN ('run_now','missed')")
      .run(state === "saved" ? "completed" : state, requestID);
    return this.request(scope, requestID);
  }

  receipt(scope, requestID) {
    this.request(scope, requestID);
    const receipt = this.db.prepare("SELECT sequence,digest,acknowledged FROM deliveries WHERE request_id=? ORDER BY sequence DESC LIMIT 1").get(requestID);
    if (!receipt) return null;
    const files = this.db.prepare("SELECT file_id FROM delivery_files WHERE request_id=? AND sequence=?").all(requestID, receipt.sequence);
    for (const file of files) this.file(scope, file.file_id);
    return { request_id: requestID, ...receipt, durable: true };
  }

  acknowledge(scope, requestID, sequence, digest) {
    const receipt = this.receipt(scope, requestID);
    if (!receipt || receipt.sequence !== sequence || receipt.digest !== digest) throw new Error("Delivery receipt mismatch");
    this.#transaction(() => {
      this.db.prepare("UPDATE deliveries SET acknowledged=1 WHERE request_id=? AND sequence=?").run(requestID, sequence);
      this.db.prepare("UPDATE tasks SET status='delivered',updated_at=? WHERE request_id=?").run(new Date().toISOString(), requestID);
      this.db.prepare("UPDATE schedules SET state='delivered' WHERE request_id=?").run(requestID);
    });
  }

  // Bytes are staged and fsynced before the single SQLite text/manifest/receipt
  // commit. Crash leftovers are reclaimed on restart; no receipt exists until
  // every output is durably owned by this installation.
  commitDelivery(scope, { request_id, sequence, digest, payload }, fileContents) {
    uuid(request_id);
    if (!Number.isSafeInteger(sequence) || sequence < 1 || !/^[a-f0-9]{64}$/u.test(digest) ||
        typeof payload !== "string" || Buffer.byteLength(payload) > CLIENT_LIMITS.resultBytes || hash(payload) !== digest) {
      throw new Error("Invalid delivery envelope");
    }
    const result = parseResultPayload(payload);
    const task = this.request(scope, request_id);
    if (!task.explicitly_submitted) throw new Error("Task has not been explicitly submitted");
    const prior = this.receipt(scope, request_id);
    if (prior) {
      if (prior.sequence !== sequence || prior.digest !== digest) throw new Error("Delivery replay changed content");
      return prior;
    }
    if (sequence !== 1) throw new Error("Delivery sequence gap");
    if (!(fileContents instanceof Map) || fileContents.size !== result.files.length) throw new Error("Missing delivered files");
    let total = Buffer.byteLength(payload);
    const staged = [];
    let committed = false;
    try {
      for (const manifest of result.files) {
        const bytes = fileContents.get(manifest.id);
        if (!(bytes instanceof Uint8Array) || bytes.byteLength !== manifest.size || hash(bytes) !== manifest.sha256) throw new Error("Delivered file verification failed");
        total += bytes.byteLength;
        if (total > CLIENT_LIMITS.resultBytes) throw new Error("Delivery exceeds result budget");
        const record = this.#writeFile(manifest.name, bytes);
        staged.push({ ...record, remote_id: manifest.id });
      }
      this.#transaction(() => {
        const now = new Date().toISOString();
        this.db.prepare("INSERT INTO messages VALUES(?,?,?,?,?)")
          .run(crypto.randomUUID(), task.conversation_id, "assistant", result.content, now);
        this.db.prepare("INSERT INTO deliveries(request_id,sequence,digest) VALUES(?,?,?)").run(request_id, sequence, digest);
        for (const record of staged) {
          this.#fileManifest(task.conversation_id, record);
          this.db.prepare("INSERT INTO delivery_files VALUES(?,?,?,?)").run(request_id, sequence, record.id, record.remote_id);
        }
        this.db.prepare("UPDATE tasks SET status='saved',updated_at=? WHERE request_id=?").run(now, request_id);
        this.db.prepare("UPDATE schedules SET state='completed' WHERE request_id=?").run(request_id);
        this.db.prepare("UPDATE conversations SET updated_at=? WHERE id=?").run(now, task.conversation_id);
      });
      committed = true;
      return this.receipt(scope, request_id);
    } catch (error) {
      if (!committed) for (const record of staged) fs.rmSync(path.join(this.filesRoot, record.id), { force: true });
      throw error;
    }
  }

  saveFile(scope, conversationID, name, value) {
    this.#conversation(scope, conversationID);
    validFileName(name);
    if (!(value instanceof Uint8Array) || value.byteLength > CLIENT_LIMITS.fileBytes) throw new Error("Invalid or oversized file");
    const record = this.#writeFile(name, value);
    try { this.#fileManifest(conversationID, record); return record; }
    catch (error) { fs.rmSync(path.join(this.filesRoot, record.id), { force: true }); throw error; }
  }

  #fileManifest(conversationID, record) {
    this.db.prepare("INSERT INTO files VALUES(?,?,?,?,?,?)")
      .run(record.id, conversationID, record.name, record.sha256, record.size, record.created_at);
  }

  #writeFile(name, value) {
    const id = crypto.randomUUID();
    const pending = path.join(this.filesRoot, `.pending-${id}`);
    const destination = path.join(this.filesRoot, id);
    const record = { id, name, sha256: hash(value), size: value.byteLength, created_at: new Date().toISOString() };
    let fd;
    try {
      fd = fs.openSync(pending, fs.constants.O_WRONLY | fs.constants.O_CREAT | fs.constants.O_EXCL | fs.constants.O_NOFOLLOW, 0o600);
      fs.writeFileSync(fd, value);
      fs.fsyncSync(fd);
      fs.closeSync(fd); fd = undefined;
      fs.renameSync(pending, destination);
      syncDirectory(this.filesRoot);
      return record;
    } catch (error) {
      if (fd !== undefined) fs.closeSync(fd);
      fs.rmSync(pending, { force: true });
      fs.rmSync(destination, { force: true });
      throw error;
    }
  }

  listFiles(scope) {
    return this.db.prepare(`SELECT f.id,f.name,f.size,f.sha256,f.created_at FROM files f JOIN conversations c ON c.id=f.conversation_id
      WHERE c.scope=? ORDER BY f.created_at DESC,f.id`).all(scopeKey(scope));
  }

  file(scope, fileID, byteLimit = CLIENT_LIMITS.fileBytes) {
    uuid(fileID);
    const record = this.db.prepare(`SELECT f.* FROM files f JOIN conversations c ON c.id=f.conversation_id
      WHERE f.id=? AND c.scope=?`).get(fileID, scopeKey(scope));
    if (!record) throw new Error("Local file not found");
    if (!Number.isSafeInteger(byteLimit) || byteLimit < 0 || byteLimit > CLIENT_LIMITS.fileBytes || record.size > byteLimit) throw new Error("Local file exceeds the read limit");
    validFileName(record.name);
    const content = readOwnedFile(this.fileBoundary, fileID, record.size);
    if (content.byteLength !== record.size || hash(content) !== record.sha256) throw new Error("Local file verification failed");
    return { ...record, content };
  }

  close() { this.db.close(); }

  #conversation(scope, id) {
    uuid(id);
    if (!this.db.prepare("SELECT id FROM conversations WHERE id=? AND scope=?").get(id, scopeKey(scope))) {
      throw new Error("Local conversation not found");
    }
  }

  #transaction(operation) {
    this.db.exec("BEGIN IMMEDIATE");
    try {
      const result = operation();
      this.db.exec("COMMIT");
      return result;
    } catch (error) {
      this.db.exec("ROLLBACK");
      throw error;
    }
  }
}

export function boundedContext(history, byteLimit = CLIENT_LIMITS.contextBytes) {
  const result = [];
  for (const message of history.slice(-CLIENT_LIMITS.contextMessages).reverse()) {
    if (!["user", "assistant"].includes(message.role)) throw new Error("Context role is invalid");
    const content = boundedText(message.role === "assistant" ? truncateUTF8(message.content, CLIENT_LIMITS.inputBytes) : message.content,
      CLIENT_LIMITS.inputBytes, "Context message");
    if (Buffer.byteLength(JSON.stringify([{ role: message.role, content }, ...result])) > byteLimit) break;
    result.unshift({ role: message.role, content });
  }
  if (!result.length) throw new Error("Context is empty");
  return result;
}

export function parseResultPayload(payload) {
  const value = JSON.parse(payload);
  if (!value || Object.keys(value).sort().join(",") !== "content,files" ||
      typeof value.content !== "string" || value.content.includes("\u0000") || !Array.isArray(value.files) ||
      value.files.length > CLIENT_LIMITS.resultFiles) throw new Error("Invalid result payload");
  const ids = new Set();
  for (const file of value.files) {
    if (!file || Object.keys(file).sort().join(",") !== "id,name,sha256,size" || typeof file.id !== "string" ||
        !ID.test(file.id) || ids.has(file.id) || !Number.isSafeInteger(file.size) || file.size < 0 ||
        file.size > CLIENT_LIMITS.resultBytes || typeof file.sha256 !== "string" || !/^[a-f0-9]{64}$/u.test(file.sha256)) throw new Error("Invalid result file manifest");
    validFileName(file.name);
    ids.add(file.id);
  }
  return value;
}
function validFileName(name) {
  boundedText(name, 240, "File name");
  if (!name || name === "." || name === ".." || /[\/\\\u0000-\u001f\u007f]/u.test(name)) throw new Error("Invalid file name");
}
function truncateUTF8(value, bytes) {
  if (typeof value !== "string") throw new Error("Context message is invalid");
  if (Buffer.byteLength(value) <= bytes) return value;
  let end = bytes;
  const buffer = Buffer.from(value);
  while (end > 0 && (buffer[end] & 0xc0) === 0x80) end--;
  return buffer.subarray(0, end).toString("utf8");
}

function scopeKey(value) {
  if (!value || Object.keys(value).sort().join(",") !== "client_id,deployment_id,owner_id" ||
      Object.values(value).some((id) => typeof id !== "string" || !ID.test(id))) throw new Error("Client identity is invalid");
  return JSON.stringify([value.deployment_id, value.owner_id, value.client_id]);
}
function boundedText(value, bytes, label) {
  if (typeof value !== "string" || Buffer.byteLength(value) > bytes || value.includes("\u0000")) throw new Error(`${label} is invalid or oversized`);
  return value;
}
function uuid(value) { if (typeof value !== "string" || !UUID.test(value)) throw new Error("Invalid local identifier"); }
function hash(value) { return crypto.createHash("sha256").update(value).digest("hex"); }
function privateDirectory(filename) {
  if (!fs.existsSync(filename)) fs.mkdirSync(filename, { mode: 0o700, recursive: true });
  const stat = fs.lstatSync(filename);
  if (!stat.isDirectory() || stat.isSymbolicLink() || (stat.mode & 0o077) ||
      (process.getuid && stat.uid !== process.getuid())) throw new Error("ClientStore directory is not private and owned");
}
function privateFile(filename, create = true) {
  try {
    const stat = fs.lstatSync(filename);
    if (!stat.isFile() || stat.isSymbolicLink() || (stat.mode & 0o077) ||
        (process.getuid && stat.uid !== process.getuid())) throw new Error("ClientStore file is not private and owned");
  } catch (error) {
    if (!create || error.code !== "ENOENT") throw error;
    fs.closeSync(fs.openSync(filename, fs.constants.O_WRONLY | fs.constants.O_CREAT | fs.constants.O_EXCL | fs.constants.O_NOFOLLOW, 0o600));
  }
}
function syncDirectory(filename) {
  const fd = fs.openSync(filename, fs.constants.O_RDONLY);
  try { fs.fsyncSync(fd); } finally { fs.closeSync(fd); }
}
