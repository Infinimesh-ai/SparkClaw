import crypto from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import { DatabaseSync } from "node:sqlite";

export const CLIENT_SCHEMA_VERSION = 1;
export const CLIENT_LIMITS = Object.freeze({
  inputBytes: 16 * 1024,
  contextBytes: 96 * 1024,
  contextMessages: 32,
  fileBytes: 64 * 1024 * 1024,
});

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
    const databasePath = path.join(root, "client.sqlite");
    privateFile(databasePath);
    this.db = new DatabaseSync(databasePath);
    try {
      this.db.exec("PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL; PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;");
      const version = this.db.prepare("PRAGMA user_version").get().user_version;
      if (version > CLIENT_SCHEMA_VERSION) throw new Error("ClientStore schema is newer than this application; preserve data and upgrade");
      if (version === 0) this.#initialize();
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
        CREATE TABLE tasks(id TEXT PRIMARY KEY, conversation_id TEXT NOT NULL REFERENCES conversations(id),
          request_id TEXT UNIQUE NOT NULL, input_digest TEXT NOT NULL, context_json TEXT NOT NULL,
          status TEXT NOT NULL, created_at TEXT NOT NULL);
        CREATE TABLE files(id TEXT PRIMARY KEY, conversation_id TEXT NOT NULL REFERENCES conversations(id),
          name TEXT NOT NULL, sha256 TEXT NOT NULL, size INTEGER NOT NULL, created_at TEXT NOT NULL);
        CREATE TABLE deliveries(request_id TEXT NOT NULL REFERENCES tasks(request_id), sequence INTEGER NOT NULL,
          digest TEXT NOT NULL, PRIMARY KEY(request_id, sequence));
        CREATE INDEX conversations_by_scope ON conversations(scope, updated_at);
        PRAGMA user_version=1;
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
      tasks: this.db.prepare("SELECT id,request_id,status,created_at FROM tasks WHERE conversation_id=? ORDER BY rowid").all(conversationID),
      files: this.db.prepare("SELECT id,name,sha256,size,created_at FROM files WHERE conversation_id=? ORDER BY rowid").all(conversationID),
    };
  }

  enqueue(scope, conversationID, content) {
    this.#conversation(scope, conversationID);
    content = boundedText(content, CLIENT_LIMITS.inputBytes, "Input").trim();
    if (!content) throw new Error("Input is empty");
    const requestID = crypto.randomUUID();
    const taskID = crypto.randomUUID();
    const now = new Date().toISOString();
    this.#transaction(() => {
      const history = this.db.prepare("SELECT role,content FROM messages WHERE conversation_id=? ORDER BY rowid DESC LIMIT ?")
        .all(conversationID, CLIENT_LIMITS.contextMessages).reverse();
      const envelope = { schema_version: 1, ...scope, installation_id: this.installationID,
        local_conversation_id: conversationID, local_task_id: taskID, request_id: requestID, messages: [] };
      const messageBudget = CLIENT_LIMITS.contextBytes - Buffer.byteLength(JSON.stringify(envelope)) + 2;
      const context = boundedContext([...history, { role: "user", content }], messageBudget);
      const snapshot = JSON.stringify({ ...envelope, messages: context });
      const digest = hash(snapshot);
      this.db.prepare("INSERT INTO messages VALUES(?,?,?,?,?)")
        .run(crypto.randomUUID(), conversationID, "user", content, now);
      this.db.prepare("INSERT INTO tasks VALUES(?,?,?,?,?,?,?)")
        .run(taskID, conversationID, requestID, digest, snapshot, "awaiting_runtime", now);
      this.db.prepare("UPDATE conversations SET updated_at=? WHERE id=?").run(now, conversationID);
    });
    // Network submission is deliberately a separate operation. This return is
    // durable local queuing, not server acceptance or completed execution.
    return { id: taskID, request_id: requestID, status: "awaiting_runtime", created_at: now };
  }

  context(scope, requestID) {
    uuid(requestID);
    const task = this.db.prepare(`SELECT t.context_json FROM tasks t JOIN conversations c ON c.id=t.conversation_id
      WHERE t.request_id=? AND c.scope=?`).get(requestID, scopeKey(scope));
    if (!task) throw new Error("Local request not found");
    return JSON.parse(task.context_json);
  }

  // Only a trusted main-process ExecutionClient may call this after verifying
  // an authenticated event. Renderer IPC deliberately does not expose it.
  commitResult(scope, { request_id, sequence, digest, content }) {
    uuid(request_id);
    if (!Number.isSafeInteger(sequence) || sequence < 1 || !/^[a-f0-9]{64}$/u.test(digest) || hash(content) !== digest) {
      throw new Error("Invalid delivery envelope");
    }
    content = boundedText(content, CLIENT_LIMITS.contextBytes, "Result");
    return this.#transaction(() => {
      const task = this.db.prepare(`SELECT t.* FROM tasks t JOIN conversations c ON c.id=t.conversation_id
        WHERE t.request_id=? AND c.scope=?`).get(request_id, scopeKey(scope));
      if (!task) throw new Error("Local request not found");
      const prior = this.db.prepare("SELECT digest FROM deliveries WHERE request_id=? AND sequence=?").get(request_id, sequence);
      if (prior) {
        if (prior.digest !== digest) throw new Error("Delivery replay changed content");
        return { request_id, sequence, digest, durable: true };
      }
      const latest = this.db.prepare("SELECT COALESCE(MAX(sequence),0) AS seq FROM deliveries WHERE request_id=?").get(request_id).seq;
      if (sequence !== latest + 1) throw new Error("Delivery sequence gap");
      this.db.prepare("INSERT INTO messages VALUES(?,?,?,?,?)")
        .run(crypto.randomUUID(), task.conversation_id, "assistant", content, new Date().toISOString());
      this.db.prepare("INSERT INTO deliveries VALUES(?,?,?)").run(request_id, sequence, digest);
      this.db.prepare("UPDATE tasks SET status='saved' WHERE request_id=?").run(request_id);
      return { request_id, sequence, digest, durable: true };
    });
  }

  saveFile(scope, conversationID, name, value) {
    this.#conversation(scope, conversationID);
    name = boundedText(name, 240, "File name");
    if (!name || name === "." || name === ".." || /[\/\\\u0000-\u001f\u007f]/u.test(name)) throw new Error("Invalid file name");
    if (!(value instanceof Uint8Array) || value.byteLength > CLIENT_LIMITS.fileBytes) throw new Error("Invalid or oversized file");
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
      this.db.prepare("INSERT INTO files VALUES(?,?,?,?,?,?)")
        .run(id, conversationID, name, record.sha256, record.size, record.created_at);
      return record;
    } catch (error) {
      if (fd !== undefined) fs.closeSync(fd);
      fs.rmSync(pending, { force: true });
      fs.rmSync(destination, { force: true });
      throw error;
    }
  }

  file(scope, fileID) {
    uuid(fileID);
    const record = this.db.prepare(`SELECT f.* FROM files f JOIN conversations c ON c.id=f.conversation_id
      WHERE f.id=? AND c.scope=?`).get(fileID, scopeKey(scope));
    if (!record) throw new Error("Local file not found");
    const filename = path.join(this.filesRoot, fileID);
    privateFile(filename, false);
    const content = fs.readFileSync(filename);
    if (content.byteLength !== record.size || hash(content) !== record.sha256) throw new Error("Local file verification failed");
    return { name: record.name, content };
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
    const content = boundedText(message.content, CLIENT_LIMITS.inputBytes, "Context message");
    if (Buffer.byteLength(JSON.stringify([{ role: message.role, content }, ...result])) > byteLimit) break;
    result.unshift({ role: message.role, content });
  }
  if (!result.length) throw new Error("Context is empty");
  return result;
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
