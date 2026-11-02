import crypto from "node:crypto";
import { approvalInputs } from "./approval-input.mjs";
import { parseStrictJSON } from "./strict-json.mjs";
import { clarificationRecordDigest, clarificationSelection, conversationContextDigest, validateClarificationRecord } from "./conversation-context.mjs";
import { CLIENT_ACTIONS_SCHEMA, SchedulePresentations, validateScheduleAction } from "./schedule-presentation.mjs";
import fs from "node:fs";
import path from "node:path";
import { DatabaseSync } from "node:sqlite";
import { availableOwnedFile, captureFileBoundary, readOwnedFile } from "./local-file-boundary.mjs";
import { WORKBENCH_LIMITS } from "../shared/workbench-limits.mjs";
import { calendarMissed, nextCalendarTime, parseScheduleRequest, recurrenceSpec, zonedTime } from "./local-schedule-time.mjs";
import { initialQuestionTitle, migrateConversationTitles } from "./client-conversation-titles.mjs";

export const CLIENT_SCHEMA_VERSION = 16;
export const CLIENT_LIMITS = WORKBENCH_LIMITS;
const CONTEXT_RECORDS_SCHEMA = `CREATE TABLE message_clarifications(message_id TEXT PRIMARY KEY REFERENCES messages(id),record_json TEXT NOT NULL);
  CREATE TABLE handle_upload_intents(request_id TEXT NOT NULL REFERENCES tasks(request_id),file_id TEXT NOT NULL,
    sha256 TEXT NOT NULL,operation_id TEXT NOT NULL,state TEXT NOT NULL,PRIMARY KEY(request_id,file_id));`;
const CLARIFICATION_SELECTIONS_SCHEMA = `CREATE TABLE clarification_selections(
  clarification_id TEXT PRIMARY KEY, request_id TEXT UNIQUE NOT NULL REFERENCES tasks(request_id),
  message_id TEXT UNIQUE NOT NULL REFERENCES messages(id), option_number INTEGER NOT NULL CHECK(option_number IN (1,2)),
  record_sha256 TEXT NOT NULL);`;
const MESSAGE_DELIVERIES_SCHEMA = `CREATE TABLE message_deliveries(message_id TEXT PRIMARY KEY REFERENCES messages(id),
  request_id TEXT NOT NULL,sequence INTEGER NOT NULL,UNIQUE(request_id,sequence),
  FOREIGN KEY(request_id,sequence) REFERENCES deliveries(request_id,sequence));`;

const UUID = /^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/u;
const ID = /^[A-Za-z0-9][A-Za-z0-9_.:-]{0,159}$/u;
const RECORDING_APPLICATIONS_SCHEMA = `CREATE TABLE recording_applications(scope TEXT NOT NULL, request_id TEXT NOT NULL,
  conversation_id TEXT NOT NULL, draft_revision INTEGER NOT NULL, PRIMARY KEY(scope,request_id));`;

export function localContentType(name) {
  return ({ png: 'image/png', jpg: 'image/jpeg', jpeg: 'image/jpeg', gif: 'image/gif', webp: 'image/webp',
    pdf: 'application/pdf', txt: 'text/plain', md: 'text/plain', csv: 'text/csv' })[name.split('.').at(-1)?.toLowerCase()] ?? 'application/octet-stream';
}

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
      if (![0, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, CLIENT_SCHEMA_VERSION].includes(version)) throw new Error("ClientStore schema is unsupported; use this release's fresh workbench directory");
      if (version === 0) this.#initialize();
      if (version === 6) this.#transaction(() => {
        this.db.exec(`CREATE TABLE execution_projection(request_id TEXT PRIMARY KEY REFERENCES tasks(request_id),revision INTEGER NOT NULL,termination_reason TEXT NOT NULL,approval_receipts TEXT NOT NULL,event_state TEXT NOT NULL);
          CREATE TABLE event_projection(scope TEXT PRIMARY KEY,cursor TEXT NOT NULL,revision INTEGER NOT NULL,snapshot TEXT NOT NULL,epoch TEXT NOT NULL DEFAULT '');
          PRAGMA user_version=8;`);
      });
      if (version === 7) this.#transaction(() => { this.db.exec(`ALTER TABLE event_projection ADD COLUMN epoch TEXT NOT NULL DEFAULT ''; PRAGMA user_version=8;`); });
      if (version !== 0 && version < 9) this.#transaction(() => {
        this.db.exec(`CREATE TABLE message_files(message_id TEXT NOT NULL REFERENCES messages(id),file_id TEXT NOT NULL REFERENCES files(id),PRIMARY KEY(message_id,file_id));
          ALTER TABLE conversations ADD COLUMN hidden INTEGER NOT NULL DEFAULT 0;
          ALTER TABLE schedule_definitions ADD COLUMN calendar TEXT NOT NULL DEFAULT '{}';
          PRAGMA user_version=9;`);
        // Older assistant messages and deliveries are committed together, in
        // identical insertion order. Backfill only when the counts agree.
        for (const conversation of this.db.prepare("SELECT id FROM conversations").all()) {
          const messages = this.db.prepare("SELECT id FROM messages WHERE conversation_id=? AND role='assistant' ORDER BY rowid").all(conversation.id);
          const deliveries = this.db.prepare("SELECT d.request_id,d.sequence FROM deliveries d JOIN tasks t ON t.request_id=d.request_id WHERE t.conversation_id=? ORDER BY d.rowid").all(conversation.id);
          if (messages.length === deliveries.length) deliveries.forEach((delivery, index) => {
            for (const file of this.db.prepare("SELECT file_id FROM delivery_files WHERE request_id=? AND sequence=?").all(delivery.request_id, delivery.sequence)) {
              this.db.prepare("INSERT OR IGNORE INTO message_files VALUES(?,?)").run(messages[index].id, file.file_id);
            }
          });
          for (const task of this.db.prepare("SELECT context_json,created_at FROM tasks WHERE conversation_id=?").all(conversation.id)) {
            const context = JSON.parse(task.context_json);
            const matching = this.db.prepare("SELECT id FROM messages WHERE conversation_id=? AND role='user' AND content=? AND created_at=?").all(conversation.id, context.messages.at(-1)?.content ?? '', task.created_at);
            if (matching.length === 1) for (const file of context.input_files ?? []) this.db.prepare("INSERT OR IGNORE INTO message_files SELECT ?,id FROM files WHERE id=? AND conversation_id=?").run(matching[0].id, file.id, conversation.id);
          }
        }
      });
      if (version !== 0 && version < 10) this.#transaction(() => {
        migrateConversationTitles(this.db);
        this.db.exec("PRAGMA user_version=10;");
      });
      // Historical rows have no reliable original-task linkage. Keep every ID
      // and byte intact; only newly committed deliveries acquire this mapping.
      if (version !== 0 && version < 11) this.#transaction(() => {
        this.db.exec(`${MESSAGE_DELIVERIES_SCHEMA} PRAGMA user_version=11;`);
      });
      if (version !== 0 && version < 12) this.#transaction(() => {
        this.db.exec(`${RECORDING_APPLICATIONS_SCHEMA} PRAGMA user_version=12;`);
      });
      if (version !== 0 && version < 13) this.#transaction(() => {
        this.db.exec(`${CLIENT_ACTIONS_SCHEMA} PRAGMA user_version=13;`);
      });
      if (version !== 0 && version < 14) this.#transaction(() => {
        this.db.exec(`${CONTEXT_RECORDS_SCHEMA} PRAGMA user_version=14;`);
      });
      if (version !== 0 && version < 15) this.#transaction(() => {
        this.db.exec(`${CLARIFICATION_SELECTIONS_SCHEMA} PRAGMA user_version=15;`);
      });
      if (version !== 0 && version < 16) this.#transaction(() => {
        this.db.exec(`ALTER TABLE execution_approvals ADD COLUMN input_form_json TEXT NOT NULL DEFAULT '';
          ALTER TABLE execution_approvals ADD COLUMN input_values_json TEXT NOT NULL DEFAULT '';
          ALTER TABLE execution_approvals ADD COLUMN inputs_digest TEXT NOT NULL DEFAULT '';
          PRAGMA user_version=16;`);
      });
      this.clientActions = new SchedulePresentations(this, scopeKey);
      // A renderer may have displayed a claimed action before a process crash.
      // Preserve uncertainty without replaying navigation on the next launch.
      this.db.exec("UPDATE client_actions SET state='skipped' WHERE state IN ('pending','claimed')");
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
          created_at TEXT NOT NULL, updated_at TEXT NOT NULL, hidden INTEGER NOT NULL DEFAULT 0,
          title_pending INTEGER NOT NULL DEFAULT 0 CHECK(title_pending IN (0,1)));
        CREATE TABLE messages(id TEXT PRIMARY KEY, conversation_id TEXT NOT NULL REFERENCES conversations(id),
          role TEXT NOT NULL CHECK(role IN ('user','assistant')), content TEXT NOT NULL, created_at TEXT NOT NULL);
        CREATE TABLE drafts(scope TEXT NOT NULL, conversation_id TEXT NOT NULL, content TEXT NOT NULL,
          local_file_ids TEXT NOT NULL, revision INTEGER NOT NULL, PRIMARY KEY(scope,conversation_id));
        ${RECORDING_APPLICATIONS_SCHEMA}
        CREATE TABLE tasks(id TEXT PRIMARY KEY, conversation_id TEXT NOT NULL REFERENCES conversations(id),
          request_id TEXT UNIQUE NOT NULL, input_digest TEXT NOT NULL, context_json TEXT NOT NULL,
          status TEXT NOT NULL, created_at TEXT NOT NULL, explicitly_submitted INTEGER NOT NULL DEFAULT 0,
          updated_at TEXT, submission_claim TEXT);
        ${CLIENT_ACTIONS_SCHEMA}
        ${CONTEXT_RECORDS_SCHEMA}
        ${CLARIFICATION_SELECTIONS_SCHEMA}
        CREATE TABLE files(id TEXT PRIMARY KEY, conversation_id TEXT NOT NULL REFERENCES conversations(id),
          name TEXT NOT NULL, sha256 TEXT NOT NULL, size INTEGER NOT NULL, created_at TEXT NOT NULL);
        CREATE TABLE message_files(message_id TEXT NOT NULL REFERENCES messages(id),file_id TEXT NOT NULL REFERENCES files(id),PRIMARY KEY(message_id,file_id));
        CREATE TABLE deliveries(request_id TEXT NOT NULL REFERENCES tasks(request_id), sequence INTEGER NOT NULL,
          digest TEXT NOT NULL, acknowledged INTEGER NOT NULL DEFAULT 0, PRIMARY KEY(request_id, sequence));
        ${MESSAGE_DELIVERIES_SCHEMA}
        CREATE TABLE delivery_files(request_id TEXT NOT NULL, sequence INTEGER NOT NULL,
          file_id TEXT NOT NULL REFERENCES files(id), remote_id TEXT NOT NULL,
          PRIMARY KEY(request_id, sequence, remote_id),
          FOREIGN KEY(request_id, sequence) REFERENCES deliveries(request_id, sequence));
        CREATE TABLE schedule_definitions(id TEXT PRIMARY KEY, interval_ms INTEGER NOT NULL, calendar TEXT NOT NULL DEFAULT '{}',
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
          input_form_json TEXT NOT NULL DEFAULT '',input_values_json TEXT NOT NULL DEFAULT '',inputs_digest TEXT NOT NULL DEFAULT '',
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
    return this.db.prepare("SELECT id,title,created_at,updated_at FROM conversations WHERE scope=? AND hidden=0 ORDER BY updated_at DESC,id")
      .all(scopeKey(scope));
  }

  create(scope, title, hidden = false, autoTitle = false) {
    if (typeof autoTitle !== "boolean") throw new Error("Automatic conversation title flag is invalid");
    title = boundedText(title, 320, "Conversation title").trim() || "New conversation";
    const now = new Date().toISOString();
    const conversation = { id: crypto.randomUUID(), title, created_at: now, updated_at: now };
    this.db.prepare("INSERT INTO conversations(id,scope,title,created_at,updated_at,hidden,title_pending) VALUES(?,?,?,?,?,?,?)")
      .run(conversation.id, scopeKey(scope), title, now, now, hidden ? 1 : 0, autoTitle && !hidden ? 1 : 0);
    return conversation;
  }

  rename(scope, conversationID, title) {
    this.#conversation(scope, conversationID);
    title = boundedText(title, 320, "Conversation title").trim();
    if (!title) throw new Error("Conversation title is empty");
    this.db.prepare("UPDATE conversations SET title=?,title_pending=0,updated_at=? WHERE id=?").run(title, new Date().toISOString(), conversationID);
    return this.list(scope).find(conversation => conversation.id === conversationID);
  }

  remove(scope, conversationID) {
    const files = this.#transaction(() => {
      this.#conversation(scope, conversationID);
      const pending = this.db.prepare(`SELECT 1 FROM tasks WHERE conversation_id=? AND
        (submission_claim IS NOT NULL OR
          (explicitly_submitted=1 AND status NOT IN ('delivered','failed','canceled','delivery_expired','delivery_too_large')) OR
          (explicitly_submitted=0 AND status NOT IN ('awaiting_runtime','scheduled_local','schedule_missed','schedule_canceled')))
        LIMIT 1`).get(conversationID);
      if (pending) return null;
      const records = this.db.prepare("SELECT id FROM files WHERE conversation_id=?").all(conversationID);
      for (const file of records) uuid(file.id);
      const root = fs.lstatSync(this.filesRoot);
      if (!root.isDirectory() || root.dev !== this.fileBoundary.stat.dev || root.ino !== this.fileBoundary.stat.ino) throw new Error("Local file directory changed");
      const requests = "SELECT request_id FROM tasks WHERE conversation_id=?";
      const definitions = this.db.prepare(`SELECT DISTINCT s.schedule_id FROM schedules s JOIN tasks t
        ON t.request_id=s.request_id WHERE t.conversation_id=?`).all(conversationID);
      for (const table of ["clarification_selections", "client_actions", "message_deliveries", "delivery_files", "deliveries", "execution_approvals", "execution_projection", "handle_upload_intents", "schedules"]) {
        this.db.prepare(`DELETE FROM ${table} WHERE request_id IN (${requests})`).run(conversationID);
      }
      for (const definition of definitions) {
        this.db.prepare("DELETE FROM schedule_definitions WHERE id=? AND NOT EXISTS (SELECT 1 FROM schedules WHERE schedule_id=?)")
          .run(definition.schedule_id, definition.schedule_id);
      }
      this.db.prepare("DELETE FROM message_clarifications WHERE message_id IN (SELECT id FROM messages WHERE conversation_id=?)").run(conversationID);
      this.db.prepare("DELETE FROM message_files WHERE message_id IN (SELECT id FROM messages WHERE conversation_id=?)").run(conversationID);
      for (const table of ["tasks", "messages", "files"]) {
        this.db.prepare(`DELETE FROM ${table} WHERE conversation_id=?`).run(conversationID);
      }
      this.db.prepare("DELETE FROM drafts WHERE scope=? AND conversation_id=?").run(scopeKey(scope), conversationID);
      this.db.prepare("DELETE FROM conversations WHERE id=?").run(conversationID);
      return records;
    });
    if (!files) return { deleted: false, reason: "pending_execution" };
    // Commit manifests first. A crash before unlink leaves only orphaned UUID
    // files, which the existing startup cleanup reclaims without restoring data.
    try {
      for (const file of files) fs.rmSync(path.join(this.filesRoot, file.id), { force: true });
      if (files.length) syncDirectory(this.filesRoot);
    } catch {
      return { deleted: true, cleanup_pending: true };
    }
    return { deleted: true };
  }

  read(scope, conversationID) {
    this.#conversation(scope, conversationID);
    return {
      client_actions: this.clientActions.list(conversationID),
      messages: this.db.prepare(`SELECT m.id,m.role,m.content,m.created_at,d.request_id FROM messages m
        LEFT JOIN message_deliveries d ON d.message_id=m.id WHERE m.conversation_id=? ORDER BY m.rowid`).all(conversationID).map(({ request_id, ...message }) => ({ ...message,
        ...(request_id ? { request_id } : {}),
        attachments: this.db.prepare("SELECT f.id AS artifact_id,f.name,f.size AS bytes FROM message_files m JOIN files f ON f.id=m.file_id WHERE m.message_id=? ORDER BY m.rowid").all(message.id)
          .map(file => ({ ...file, rel_path: `local:${file.artifact_id}/${file.name}`, content_type: localContentType(file.name) })) })),
      tasks: this.db.prepare("SELECT id,request_id,status,explicitly_submitted,created_at FROM tasks WHERE conversation_id=? ORDER BY rowid").all(conversationID)
        .map((task) => ({ ...task, ...this.executionProjection(scope, task.request_id), approvals: this.approvals(scope, task.request_id) })),
      files: this.db.prepare("SELECT id,name,sha256,size,created_at FROM files WHERE conversation_id=? ORDER BY rowid").all(conversationID).map(file => ({ ...file, content_type: localContentType(file.name) })),
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

  recordingApplied(scope, conversationID, requestID) {
    uuid(requestID);
    if (conversationID !== "") this.#conversation(scope, conversationID);
    const row = this.db.prepare("SELECT conversation_id FROM recording_applications WHERE scope=? AND request_id=?").get(scopeKey(scope), requestID);
    if (row && row.conversation_id !== conversationID) throw new Error("Recording belongs to another conversation");
    return Boolean(row);
  }

  saveDraft(scope, conversationID, content, localFileIDs, revision, recordingRequestID) {
    if (recordingRequestID !== undefined) uuid(recordingRequestID);
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
      if (recordingRequestID !== undefined) this.recordingApplied(scope, conversationID, recordingRequestID);
      this.db.prepare(`INSERT INTO drafts VALUES(?,?,?,?,?) ON CONFLICT(scope,conversation_id) DO UPDATE SET
        content=excluded.content,local_file_ids=excluded.local_file_ids,revision=excluded.revision`)
        .run(scopeKey(scope), conversationID, content, JSON.stringify(localFileIDs), revision + 1);
      // The transcript and its consumption evidence commit together. Clearing
      // the draft or losing the later recording ACK cannot make it insertable.
      if (recordingRequestID !== undefined) this.db.prepare("INSERT INTO recording_applications VALUES(?,?,?,?) ON CONFLICT(scope,request_id) DO NOTHING")
        .run(scopeKey(scope), recordingRequestID, conversationID, revision + 1);
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

  enqueueDraft(scope, conversationID, draftConversationID, revision, allowHistoryFiles = false) {
    if (draftConversationID !== "" && draftConversationID !== conversationID) throw new Error("Draft belongs to a different conversation");
    const draft = this.draft(scope, draftConversationID);
    const task = this.enqueue(scope, conversationID, draft.content, draft.local_file_ids, undefined, undefined,
      { conversationID: draftConversationID, revision }, allowHistoryFiles);
    return { task, draft: this.draft(scope, draftConversationID) };
  }

  enqueue(scope, conversationID, content, localFileIDs = [], scheduleSpec, recoveringScheduleID, consumedDraft, allowHistoryFiles = false) {
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
    const messageID = crypto.randomUUID();
    let repeatedTask;
    this.#transaction(() => {
      if (consumedDraft && this.draft(scope, consumedDraft.conversationID).revision !== consumedDraft.revision) throw new Error("Draft changed before submission; review the saved draft");
      if (recoveringScheduleID && this.scheduledRequest(scope, recoveringScheduleID).state !== "missed") throw new Error("Only an unexecuted missed schedule can run now");
      const rows = this.db.prepare("SELECT id,role,content FROM messages WHERE conversation_id=? ORDER BY rowid DESC LIMIT ?")
        .all(conversationID, CLIENT_LIMITS.contextMessages);
      if (!inputFiles.length && !scheduleSpec && rows[0]?.role === "user") {
        const priorSelection = this.db.prepare(`SELECT s.*,t.id,t.status,t.created_at FROM clarification_selections s
          JOIN tasks t ON t.request_id=s.request_id WHERE s.message_id=?`).get(rows[0].id);
        const question = rows[1]?.role === "assistant" ? this.db.prepare("SELECT record_json FROM message_clarifications WHERE message_id=?").get(rows[1].id) : undefined;
        if (priorSelection && question) {
          const record = JSON.parse(question.record_json), choice = clarificationSelection(content, record, rows[1].content);
          if (choice) {
            if (choice !== priorSelection.option_number || clarificationRecordDigest(record) !== priorSelection.record_sha256) throw new Error("Clarification selection is already queued");
            repeatedTask = { id: priorSelection.id, request_id: priorSelection.request_id, status: priorSelection.status, created_at: priorSelection.created_at };
            if (consumedDraft) this.db.prepare("UPDATE drafts SET content='',local_file_ids='[]',revision=revision+1 WHERE scope=? AND conversation_id=?")
              .run(scopeKey(scope), consumedDraft.conversationID);
            return;
          }
        }
      }
      // Current explicit inputs own their identity; do not also project an
      // older occurrence as an implicit history resource.
      const seenHandles = new Set(inputFiles.map(file => file.id));
      const projectedNames = new Map();
      const history = rows.map(({ id, role, content }, index) => {
        const files = []; let filesUnavailable = false;
        const records = this.db.prepare(`SELECT f.id,f.name,f.size,f.sha256 FROM message_files mf JOIN files f ON f.id=mf.file_id
          WHERE mf.message_id=? AND f.conversation_id=? ORDER BY mf.rowid`).all(id, conversationID);
        for (const file of records) {
          if (seenHandles.has(file.id)) continue;
          if (!allowHistoryFiles || scheduleSpec || files.length >= CLIENT_LIMITS.resultFiles || seenHandles.size - inputFiles.length >= 32 || !UUID.test(file.id) || file.size > CLIENT_LIMITS.resultBytes) { filesUnavailable = true; continue; }
          try { validFileName(file.name); } catch { filesUnavailable = true; continue; }
          if (!/^[a-f0-9]{64}$/u.test(file.sha256) || !availableOwnedFile(this.fileBoundary, file.id, file.size)) { filesUnavailable = true; continue; }
          seenHandles.add(file.id); files.push(file);
          if (!projectedNames.has(file.name)) projectedNames.set(file.name, new Set());
          projectedNames.get(file.name).add(file.id);
        }
        // Only the immediately preceding assistant question can be executable.
        // Legacy, malformed and stale records remain readable local history.
        let clarification;
        const row = index === 0 && role === "assistant" && !scheduleSpec ? this.db.prepare("SELECT record_json FROM message_clarifications WHERE message_id=?").get(id) : undefined;
        if (row) {
          try {
            const record = JSON.parse(row.record_json);
            validateClarificationRecord(record);
            if (record.prompt_sha256 === hash(content)) clarification = record;
          } catch { /* Preserve its text; never manufacture a valid record. */ }
        }
        return { id, role, content, ...(files.length ? { files } : {}),
          ...(filesUnavailable ? { files_unavailable: true } : {}), ...(clarification ? { clarification } : {}) };
      }).reverse();
      // A bounded projection may omit a same-name file from an older message.
      // Count identities in SQLite, without reading or uploading their content.
      for (const [name, ids] of projectedNames) {
        const count = this.db.prepare(`SELECT COUNT(DISTINCT f.id) AS n FROM files f JOIN message_files mf ON mf.file_id=f.id
          WHERE f.conversation_id=? AND f.name=?`).get(conversationID, name).n;
        if (count > ids.size) for (const message of history) {
          if (message.files?.some(file => file.name === name)) message.files_unavailable = true;
        }
      }
      const previous = history.at(-1);
      const selection = previous?.clarification && !inputFiles.length ? clarificationSelection(content, previous.clarification, previous.content) : 0;
      let selectedRecord, pinnedHistory;
      if (selection) {
        selectedRecord = previous.clarification;
        if (Date.parse(selectedRecord.expires_at) <= Date.now() || Date.parse(selectedRecord.issued_at) > Date.now()) throw new Error("Clarification expired; describe your request again");
        const original = this.request(scope, selectedRecord.request_id);
        if (original.conversation_id !== conversationID) throw new Error("Clarification belongs to another conversation");
        const source = JSON.parse(original.context_json);
        const sourceIndex = source.messages.findIndex(message => message.id === selectedRecord.source_message_id);
        const sourceMessages = source.messages.slice(0, sourceIndex + 1);
        const originalInputs = sourceIndex === source.messages.length - 1 ? source.input_files ?? [] : [];
        if (sourceIndex < 0 || hash(sourceMessages.at(-1).content) !== selectedRecord.source_text_sha256 ||
            conversationContextDigest(sourceMessages, originalInputs) !== selectedRecord.context_sha256) throw new Error("Clarification context changed; describe your request again");
        pinnedHistory = structuredClone(sourceMessages);
        for (const message of pinnedHistory) delete message.clarification;
        const last = pinnedHistory.at(-1), files = new Map((last.files ?? []).map(file => [file.id, file]));
        for (const file of originalInputs) {
          const prior = files.get(file.id);
          if (prior && ['name', 'size', 'sha256'].some(key => prior[key] !== file[key])) throw new Error("Clarification file binding changed");
          files.set(file.id, file);
        }
        if (files.size) last.files = [...files.values()];
        if (new Set(pinnedHistory.flatMap(message => (message.files ?? []).map(file => file.id))).size > CLIENT_LIMITS.resultFiles) throw new Error("Clarification context exceeds the file limit; describe your request again");
        for (const message of pinnedHistory) for (const file of message.files ?? []) {
          const current = this.db.prepare("SELECT id,name,size,sha256 FROM files WHERE id=? AND conversation_id=?").get(file.id, conversationID);
          if (!allowHistoryFiles || !current || ['name', 'size', 'sha256'].some(key => current[key] !== file[key]) || !availableOwnedFile(this.fileBoundary, file.id, file.size)) throw new Error("Clarification file unavailable; attach it again");
        }
        pinnedHistory.push(previous);
      }
      const envelope = { schema_version: 2, ...scope, installation_id: this.installationID,
        local_conversation_id: conversationID, local_task_id: taskID, request_id: requestID, messages: [],
        ...(inputFiles.length ? { input_files: inputFiles } : {}),
        ...(!scheduleSpec ? { client_presentation: { schema_version: 1, schedule_show: true,
          timezone: Intl.DateTimeFormat().resolvedOptions().timeZone, requested_at: now } } : {}) };
      const messageBudget = CLIENT_LIMITS.contextBytes - Buffer.byteLength(JSON.stringify(envelope)) + 2;
      const selectedMessages = [...(pinnedHistory ?? history), { id: messageID, role: "user", content }];
      let context = boundedContext(selectedMessages, messageBudget);
      if (!pinnedHistory) {
        for (;;) {
          const represented = new Map();
          for (const message of context) for (const file of message.files ?? []) {
            if (!represented.has(file.name)) represented.set(file.name, new Set());
            represented.get(file.name).add(file.id);
          }
          for (const [name, ids] of represented) {
            const count = this.db.prepare(`SELECT COUNT(DISTINCT f.id) AS n FROM files f JOIN message_files mf ON mf.file_id=f.id
              WHERE f.conversation_id=? AND f.name=?`).get(conversationID, name).n;
            if (count > ids.size) for (const message of context) if (message.files?.some(file => file.name === name)) message.files_unavailable = true;
          }
          const bounded = boundedContext(context, messageBudget);
          if (bounded.length === context.length) { context = bounded; break; }
          context = bounded;
        }
      }
      if (pinnedHistory && (context.length !== selectedMessages.length || context.some((message, index) => message.id !== selectedMessages[index].id || message.content !== selectedMessages[index].content))) {
        throw new Error("Clarification context exceeds the limit; describe your request again");
      }
      const snapshot = JSON.stringify({ ...envelope, messages: context });
      if (inputFiles.reduce((sum, file) => sum + file.size, Buffer.byteLength(snapshot)) > 32 * 1024 * 1024) throw new Error("Input exceeds 32 MiB task budget");
      const digest = hash(snapshot);
      this.db.prepare("INSERT INTO messages VALUES(?,?,?,?,?)")
        .run(messageID, conversationID, "user", content, now);
      for (const file of inputFiles) this.db.prepare("INSERT INTO message_files VALUES(?,?)").run(messageID, file.id);
      this.db.prepare("INSERT INTO tasks(id,conversation_id,request_id,input_digest,context_json,status,created_at) VALUES(?,?,?,?,?,?,?)")
        .run(taskID, conversationID, requestID, digest, snapshot, scheduleSpec ? "scheduled_local" : "awaiting_runtime", now);
      if (selectedRecord) this.db.prepare("INSERT INTO clarification_selections VALUES(?,?,?,?,?)")
        .run(selectedRecord.id, requestID, messageID, selection, clarificationRecordDigest(selectedRecord));
      if (scheduleSpec) {
        this.db.prepare("INSERT INTO schedule_definitions(id,interval_ms,state,calendar) VALUES(?,?,'active',?)").run(requestID, scheduleSpec.intervalMS, JSON.stringify(scheduleSpec.calendar ?? {}));
        this.db.prepare("INSERT INTO schedules(request_id,schedule_id,due_at,state,created_at) VALUES(?,?,?,'saved',?)")
          .run(requestID, requestID, scheduleSpec.dueAt, now);
      }
      if (recoveringScheduleID) this.db.prepare("UPDATE schedules SET state='run_now',recovery_request_id=? WHERE request_id=?").run(requestID, recoveringScheduleID);
      this.db.prepare(`UPDATE conversations SET title=CASE WHEN title_pending=1 AND hidden=0 THEN ? ELSE title END,
        title_pending=0,updated_at=? WHERE id=?`).run(initialQuestionTitle(content), now, conversationID);
      if (consumedDraft) this.db.prepare("UPDATE drafts SET content='',local_file_ids='[]',revision=revision+1 WHERE scope=? AND conversation_id=?")
        .run(scopeKey(scope), consumedDraft.conversationID);
    });
    // Network submission is deliberately a separate operation. This return is
    // durable local queuing, not server acceptance or completed execution.
    return repeatedTask ?? { id: taskID, request_id: requestID, status: scheduleSpec ? "scheduled_local" : "awaiting_runtime", created_at: now };
  }

  schedule(scope, conversationID, content, dueAt, now = Date.now(), intervalMS = 0, calendar = {}) {
    const maxDelay = 366 * 24 * 60 * 60 * 1000;
    if (typeof dueAt !== "string" || !Number.isFinite(Date.parse(dueAt)) ||
        Date.parse(dueAt) <= now || Date.parse(dueAt) > now + maxDelay) throw new Error("Schedule must be within the next 366 days");
    if (!Number.isSafeInteger(intervalMS) || intervalMS < 0 ||
        (intervalMS !== 0 && (intervalMS < 60000 || intervalMS > maxDelay))) throw new Error("Schedule interval must be 0 or between one minute and 366 days");
    if (Object.keys(calendar).length && (!['daily', 'weekly', 'monthly', 'weekdays'].includes(calendar.kind) || !calendar.timezone)) throw new Error('Schedule recurrence is invalid');
    const task = this.enqueue(scope, conversationID, content, [], { dueAt: new Date(dueAt).toISOString(), intervalMS, calendar });
    return this.scheduledRequest(scope, task.request_id);
  }

  scheduledRequest(scope, requestID) {
    const task = this.request(scope, requestID);
    const schedule = this.db.prepare(`SELECT s.*,d.interval_ms,d.calendar,d.state AS definition_state FROM schedules s
      JOIN schedule_definitions d ON d.id=s.schedule_id WHERE s.request_id=?`).get(requestID);
    if (!schedule) throw new Error("Local schedule not found");
    return { ...task, ...schedule };
  }

  createScheduleRequest(scope, request, timezone, now = Date.now()) {
    const plan = parseScheduleRequest(request, timezone, now);
    return this.#transaction(() => {
      const conversation = this.create(scope, 'Scheduled tasks', true);
      return this.schedule(scope, conversation.id, plan.content, plan.dueAt, now, plan.intervalMS, plan.calendar);
    });
  }

  listSchedules(scope) {
    const rows = this.db.prepare(`SELECT s.request_id FROM schedules s JOIN tasks t ON t.request_id=s.request_id
      JOIN conversations c ON c.id=t.conversation_id WHERE c.scope=? ORDER BY s.due_at DESC,s.rowid DESC`).all(scopeKey(scope));
    const seen = new Set();
    return rows.map(row => this.scheduledRequest(scope, row.request_id)).filter(row => {
      if (['canceled', 'run_now', 'delivered'].includes(row.state)) return false;
      if (row.definition_state !== 'active' && row.state !== 'missed' && !['claimed', 'submission_pending', 'accepted', 'running', 'cancel_pending', 'unknown'].includes(row.state)) return false;
      const key = `${row.schedule_id}:${row.state === 'missed' ? 'missed' : row.state === 'saved' ? 'waiting' : row.request_id}`;
      if (seen.has(key)) return false;
      seen.add(key); return true;
    }).map(row => {
      const calendar = JSON.parse(row.calendar);
      const content = JSON.parse(row.context_json).messages.at(-1).content;
      const conversation = this.db.prepare('SELECT title FROM conversations WHERE id=?').get(row.conversation_id);
      return { id: row.request_id, session_id: row.conversation_id, title: content.slice(0, 120), text: content, due_time: row.due_at,
        timezone: calendar.timezone || Intl.DateTimeFormat().resolvedOptions().timeZone,
        recurrence: calendar.kind || (row.interval_ms ? `every ${row.interval_ms / 60000} minutes` : ''),
        status: row.state === 'saved' ? 'pending' : row.state === 'missed' ? 'missed' : ['accepted', 'running'].includes(row.state) ? 'sending' : 'submitted',
        updated_at: this.scheduleVersion(row), editable: row.state === 'saved' && row.definition_state === 'active' && !row.explicitly_submitted,
        cancelable: row.definition_state === 'active' || ['claimed', 'submission_pending', 'accepted', 'running', 'cancel_pending', 'unknown'].includes(row.state),
        endpoint: { channel: 'desktop', software_display_name: 'SparkX', conversation_label: conversation.title, status: 'active' },
        local_state: row.state, missed_count: row.missed_count };
    }).sort((a, b) => a.due_time.localeCompare(b.due_time));
  }

  scheduleVersion(row) {
    return hash(JSON.stringify([row.context_json, row.due_at, row.interval_ms, row.calendar, row.state, row.definition_state, row.status]));
  }

  editSchedule(scope, requestID, expectedVersion, { text, dueTime, timezone, recurrence }, now = Date.now()) {
    const dueAt = zonedTime(dueTime, timezone);
    const spec = recurrenceSpec(recurrence, timezone, dueAt);
    return this.#transaction(() => {
      const row = this.scheduledRequest(scope, requestID);
      if (this.scheduleVersion(row) !== expectedVersion || row.state !== 'saved' || row.definition_state !== 'active' || row.explicitly_submitted) throw new Error('Schedule changed or is already executing; refresh before editing.');
      // Keep submitted snapshots immutable. Replacement is atomic and retires
      // only an unclaimed local definition; prior occurrences remain auditable.
      this.cancelSchedule(scope, requestID);
      return this.schedule(scope, row.conversation_id, text, dueAt, now, spec.intervalMS, spec.calendar);
    });
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
      const calendar = JSON.parse(schedule.calendar);
      this.#advanceSchedule(schedule, calendar.kind ? Date.parse(nextCalendarTime(schedule.due_at, calendar)) : Date.parse(schedule.due_at) + schedule.interval_ms, now);
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
      const calendar = JSON.parse(schedule.calendar);
      const skipped = calendar.kind ? calendarMissed(schedule.due_at, calendar, now) : undefined;
      const count = skipped?.count ?? (schedule.interval_ms ? Math.floor((now - due) / schedule.interval_ms) + 1 : 1);
      const lastDue = skipped ? Date.parse(skipped.lastDue) : due + (count - 1) * schedule.interval_ms;
      // A closed-form missed range bounds a years-long offline scan. Every
      // skipped due time remains derivable from due_at/interval/count; none is queued.
      this.db.prepare("UPDATE schedules SET state='missed',missed_count=?,missed_until=? WHERE request_id=?")
        .run(count, new Date(lastDue).toISOString(), requestID);
      this.db.prepare("UPDATE tasks SET status='schedule_missed',updated_at=? WHERE request_id=?").run(new Date(now).toISOString(), requestID);
      this.#advanceSchedule(schedule, skipped ? Date.parse(skipped.nextDue) : lastDue + schedule.interval_ms, now);
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
    if (!schedule.interval_ms && !JSON.parse(schedule.calendar).kind) {
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

  claimHandleUpload(scope, requestID, manifest) {
    const task = this.request(scope, requestID);
    const envelope = JSON.parse(task.context_json);
    const declared = (envelope.messages ?? []).flatMap(message => message.files ?? []).find(file => file.id === manifest.file_id);
    if (!declared || declared.sha256 !== manifest.sha256 || declared.size !== manifest.size ||
        (envelope.input_files ?? []).some(file => file.id === declared.id)) throw new Error("Undeclared conversation file request");
    return this.#transaction(() => {
      let row = this.db.prepare("SELECT * FROM handle_upload_intents WHERE request_id=? AND file_id=?").get(requestID, declared.id);
      if (row && row.sha256 !== declared.sha256) throw new Error("Conversation file upload binding changed");
      if (!row) {
        this.db.prepare("INSERT INTO handle_upload_intents VALUES(?,?,?,?,?)").run(requestID, declared.id, declared.sha256, crypto.randomUUID(), "ready");
        row = this.db.prepare("SELECT * FROM handle_upload_intents WHERE request_id=? AND file_id=?").get(requestID, declared.id);
      }
      if (row.state !== "ready") return null;
      this.db.prepare("UPDATE handle_upload_intents SET state='unknown' WHERE request_id=? AND file_id=? AND state='ready'").run(requestID, declared.id);
      return { ...row, state: "unknown" };
    });
  }

  finishHandleUpload(scope, requestID, fileID, operationID, state) {
    this.request(scope, requestID);
    if (!UUID.test(fileID) || !UUID.test(operationID) || !["ready", "done"].includes(state)) throw new Error("Invalid file upload receipt");
    const result = this.db.prepare("UPDATE handle_upload_intents SET state=? WHERE request_id=? AND file_id=? AND operation_id=? AND state='unknown'")
      .run(state, requestID, fileID, operationID);
    if (result.changes !== 1) throw new Error("Conversation file upload receipt conflict");
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
        const cached = this.db.prepare("SELECT digest,decision,inputs_digest FROM execution_approvals WHERE request_id=? AND approval_id=?").get(event.request_id, receipt.approval_id);
        if (cached && (cached.digest !== receipt.digest || cached.decision && (cached.decision !== receipt.decision || cached.inputs_digest !== (receipt.inputs_digest ?? "")))) throw new Error("Approval receipt conflicts with local decision");
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
    return this.db.prepare("SELECT approval_id,digest,tool,summary,arguments_json,state,decision,expires_at,input_form_json,input_values_json,inputs_digest FROM execution_approvals WHERE request_id=? ORDER BY rowid")
      .all(requestID).map(({ arguments_json, input_form_json, input_values_json, ...row }) => ({ ...row, arguments: JSON.parse(arguments_json), ...(input_form_json ? { input_form: JSON.parse(input_form_json) } : {}), ...(input_values_json ? { input_values: JSON.parse(input_values_json) } : {}) }));
  }

  syncApprovals(scope, requestID, pending, executionState, expiresAt) {
    this.request(scope, requestID);
    if (!Array.isArray(pending) || pending.length > 32 || (pending.length && executionState !== "running")) throw new Error("Invalid active approval state");
    if (pending.length && (typeof expiresAt !== "string" || !Number.isFinite(Date.parse(expiresAt)))) throw new Error("Invalid approval deadline");
    const seen = new Set();
    for (const row of pending) {
      if (!row || Object.keys(row).sort().join(",") !== (Object.hasOwn(row, "input_form") ? "approval_id,arguments,digest,input_form,summary,tool" : "approval_id,arguments,digest,summary,tool") ||
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
        const formJSON = row.input_form ? JSON.stringify(row.input_form) : "";
        if (prior) {
          if (prior.digest !== row.digest || prior.tool !== row.tool || prior.arguments_json !== argumentsJSON || prior.summary !== row.summary || prior.expires_at !== expiresAt || prior.input_form_json !== formJSON) throw new Error("Approval replay changed content or deadline");
        } else this.db.prepare("INSERT INTO execution_approvals(request_id,approval_id,digest,tool,summary,arguments_json,state,decision,expires_at,input_form_json) VALUES(?,?,?,?,?,?,'pending',NULL,?,?)")
          .run(requestID, row.approval_id, row.digest, row.tool, row.summary, argumentsJSON, expiresAt, formJSON);
      }
      for (const prior of this.db.prepare("SELECT approval_id,state,decision FROM execution_approvals WHERE request_id=?").all(requestID)) {
        if (seen.has(prior.approval_id) || !["pending", "decision_pending"].includes(prior.state)) continue;
        const state = prior.decision ? "decision_unknown" : executionState === "running" ? "resolved" : "expired";
        this.db.prepare("UPDATE execution_approvals SET state=? WHERE request_id=? AND approval_id=?").run(state, requestID, prior.approval_id);
      }
    });
  }

  approvalDecision(scope, requestID, approvalID, digest, decision, resolved = false, inputs) {
    const row = this.approvals(scope, requestID).find((approval) => approval.approval_id === approvalID);
    if (!row || row.digest !== digest || !["approve", "reject"].includes(decision)) throw new Error("Approval identity or digest mismatch");
    const checked = approvalInputs(row.input_form, decision, inputs);
    if (row.decision && (row.decision !== decision || row.inputs_digest !== checked.digest)) throw new Error("Approval decision or inputs cannot be changed");
    if (!["pending", "decision_pending", "approved", "rejected"].includes(row.state)) throw new Error("Approval is no longer actionable");
    const state = resolved ? (decision === "approve" ? "approved" : "rejected") : "decision_pending";
    this.db.prepare("UPDATE execution_approvals SET state=?,decision=?,input_values_json=?,inputs_digest=? WHERE request_id=? AND approval_id=?")
      .run(state, decision, checked.json, checked.digest, requestID, approvalID);
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
    if (result.clarification) {
      const record = result.clarification, context = JSON.parse(task.context_json);
      const source = context.messages.find(message => message.id === record.source_message_id);
      if (record.request_id !== request_id || !source || source.role !== "user" || hash(source.content) !== record.source_text_sha256 ||
          conversationContextDigest(context.messages.slice(0, context.messages.indexOf(source) + 1), source === context.messages.at(-1) ? context.input_files : []) !== record.context_sha256) throw new Error("Clarification request binding changed");
    }
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
        const messageID = crypto.randomUUID();
        this.db.prepare("INSERT INTO messages VALUES(?,?,?,?,?)")
          .run(messageID, task.conversation_id, "assistant", result.content, now);
        if (result.clarification) this.db.prepare("INSERT INTO message_clarifications VALUES(?,?)").run(messageID, JSON.stringify(result.clarification));
        this.db.prepare("INSERT INTO deliveries(request_id,sequence,digest) VALUES(?,?,?)").run(request_id, sequence, digest);
        if (result.client_action) this.clientActions.insert(task, result.client_action);
        this.db.prepare("INSERT INTO message_deliveries(message_id,request_id,sequence) VALUES(?,?,?)").run(messageID, request_id, sequence);
        for (const record of staged) {
          this.#fileManifest(task.conversation_id, record);
          this.db.prepare("INSERT INTO message_files VALUES(?,?)").run(messageID, record.id);
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
    const record = { id, name, sha256: hash(value), size: value.byteLength, content_type: localContentType(name), created_at: new Date().toISOString() };
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
    const depth = this.transactionDepth ?? 0, savepoint = `client_store_${depth}`;
    this.db.exec(depth ? `SAVEPOINT ${savepoint}` : "BEGIN IMMEDIATE");
    this.transactionDepth = depth + 1;
    try {
      const result = operation();
      this.db.exec(depth ? `RELEASE ${savepoint}` : "COMMIT");
      return result;
    } catch (error) {
      this.db.exec(depth ? `ROLLBACK TO ${savepoint}; RELEASE ${savepoint};` : "ROLLBACK");
      throw error;
    } finally { this.transactionDepth = depth; }
  }
}

export function boundedContext(history, byteLimit = CLIENT_LIMITS.contextBytes) {
  const result = [];
  for (const message of history.slice(-CLIENT_LIMITS.contextMessages).reverse()) {
    if (!["user", "assistant"].includes(message.role)) throw new Error("Context role is invalid");
    const content = boundedText(message.role === "assistant" && !message.clarification ? truncateUTF8(message.content, CLIENT_LIMITS.inputBytes) : message.content,
      CLIENT_LIMITS.inputBytes, "Context message");
    if (message.files_unavailable !== undefined && typeof message.files_unavailable !== "boolean") throw new Error("Context file availability is invalid");
    if (message.id !== undefined && (typeof message.id !== "string" || !ID.test(message.id))) throw new Error("Context message identity is invalid");
    const projected = { ...(message.id ? { id: message.id } : {}), role: message.role, content,
      ...(message.files_unavailable === true ? { files_unavailable: true } : {}), ...(message.files?.length ? { files: message.files } : {}),
      ...(message.role === "assistant" && message.clarification ? { clarification: message.clarification } : {}) };
    if (Buffer.byteLength(JSON.stringify([projected, ...result])) > byteLimit) break;
    result.unshift(projected);
  }
  if (!result.length) throw new Error("Context is empty");
  return result;
}

export function parseResultPayload(payload) {
  const value = parseStrictJSON(payload);
  if (!value || Object.keys(value).some(key => !["client_action", "clarification", "content", "files"].includes(key)) ||
      typeof value.content !== "string" || value.content.includes("\u0000") || !Array.isArray(value.files) ||
      value.files.length > CLIENT_LIMITS.resultFiles) throw new Error("Invalid result payload");
  if (Object.hasOwn(value, "client_action")) validateScheduleAction(value.client_action);
  if (Object.hasOwn(value, "clarification")) {
    validateClarificationRecord(value.clarification);
    if (value.clarification.prompt_sha256 !== hash(value.content)) throw new Error("Clarification text binding changed");
  }
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
