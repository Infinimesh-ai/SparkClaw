import { EventEmitter } from "node:events";

// A fake link-helper process speaking the NDJSON protocol of
// services/link/cmd/link-helper. handlers[method] answers helper methods;
// handlers.request(op, params) answers link operations.
export function fakeHelper(handlers = {}) {
  const sent = [];
  const children = [];
  const spawnProcess = () => {
    const child = new EventEmitter();
    child.stdout = new EventEmitter();
    child.stderr = new EventEmitter();
    child.killed = false;
    child.kill = () => { child.killed = true; };
    const emit = (value) => child.stdout.emit("data", `${JSON.stringify({ ipc_version: 1, ...value })}\n`);
    child.emitLine = emit;
    child.stdin = new EventEmitter();
    child.stdin.write = (line) => {
      for (const text of String(line).split("\n").filter(Boolean)) {
        const message = JSON.parse(text);
        sent.push(message);
        void (async () => {
          if (message.method === "reverse_response") { handlers.reverseResponse?.(message); return; }
          const handler = message.method === "request" ? handlers.request : handlers[message.method];
          try {
            const result = message.method === "request" ? await handler?.(message.op, message.params, message) : await handler?.(message);
            emit({ id: message.id, result: result ?? { ok: true } });
          } catch (error) {
            emit({ id: message.id, error: { code: error.code || "internal", message: error.message, retryable: error.retryable, ...(error.notSent ? { not_sent: true } : {}), ...(error.reason ? { reason: error.reason } : {}) } });
          }
        })();
      }
      return true;
    };
    child.stdin.end = () => {};
    child.stdin.destroy = () => {};
    children.push(child);
    queueMicrotask(() => emit({ event: "hello", version: "sparkclaw.link.v1" }));
    return child;
  };
  return {
    sent, children, spawnProcess,
    get child() { return children.at(-1); },
    state(path) { this.child.emitLine({ event: "state", path, hello: path ? { version: "sparkclaw.link.v1" } : null }); },
    event(topic, data, subscription = "") { this.child.emitLine({ event: "frame", frame: { type: "event", id: `e${Math.random()}`, topic, subscription, data } }); },
    reverse(id, op, params) { this.child.emitLine({ event: "reverse", reverse_id: id, op, params }); },
  };
}

export function linkError(code, message = code, retryable = false, reason = "") {
  return Object.assign(new Error(message), { code, retryable, ...(reason ? { reason } : {}) });
}

export const tick = (ms = 0) => new Promise((resolve) => setTimeout(resolve, ms));

export async function until(check, timeoutMS = 2000) {
  const deadline = Date.now() + timeoutMS;
  for (;;) {
    const value = await check();
    if (value) return value;
    if (Date.now() > deadline) throw new Error("condition not reached");
    await tick(5);
  }
}

// A fake backend for link core operations: an in-memory conversation store
// with sync cursor, message paging, runs and approvals.
export function fakeBackend() {
  const sessions = new Map();
  const messages = new Map();
  let revision = 0, seq = 0, ids = 0;
  const calls = [];
  const header = (s) => ({ id: s.id, title: s.title, revision: s.revision, last_seq: (messages.get(s.id) || []).at(-1)?.seq ?? 0,
    preview: (messages.get(s.id) || []).at(-1)?.content ?? "", archived: s.archived, deleted: s.deleted, created_at: s.created_at, updated_at: s.updated_at });
  const touch = (s) => { s.revision = ++revision; s.updated_at = new Date().toISOString(); };
  const backend = {
    calls, sessions, messages,
    createSession(title, id = `s_${++ids}`) {
      const s = { id, title, revision: 0, archived: false, deleted: false, created_at: new Date().toISOString(), updated_at: new Date().toISOString() };
      touch(s); sessions.set(id, s); messages.set(id, []); return s;
    },
    addMessage(sessionID, role, content, extra = {}) {
      const m = { id: `m_${++ids}`, session_id: sessionID, seq: ++seq, role, content, created_at: new Date().toISOString(), ...extra };
      messages.get(sessionID).push(m); touch(sessions.get(sessionID)); return m;
    },
    touchSession(id) { touch(sessions.get(id)); },
    deleteSession(id) { const s = sessions.get(id); s.deleted = true; touch(s); messages.set(id, []); },
    handlers: {},
    async request(op, params) {
      calls.push({ op, params });
      if (backend.handlers[op]) return backend.handlers[op](params);
      switch (op) {
        case "device.describe": return { principal: { owner_id: "owner", device_id: "desktop-1", class: "desktop" }, features: [], domains: ["mail", "settings", "events", "object", "transfer", "operations", "speech", "medicine", "notifications", "presentation", "browser", "mcd", "tools"] };
        case "sync.changes": {
          const changed = [...sessions.values()].filter(s => s.revision > params.cursor).sort((a, b) => a.revision - b.revision);
          const limit = params.limit || 100, page = changed.slice(0, limit);
          return { sessions: page.map(header), cursor: page.at(-1)?.revision ?? params.cursor, more: changed.length > limit, ...(backend.scope ? { scope: backend.scope } : {}) };
        }
        case "session.create": { const s = backend.createSession(params.title); return header(s); }
        case "session.rename": { const s = sessions.get(params.session_id); s.title = params.title; touch(s); return header(s); }
        case "session.archive": { const s = sessions.get(params.session_id); s.archived = params.archived; touch(s); return header(s); }
        case "session.delete": backend.deleteSession(params.session_id); return { session_id: params.session_id, deleted: true };
        case "session.messages": {
          const all = messages.get(params.session_id) || [];
          const limit = params.limit || 30;
          if (params.after_seq) return { session: header(sessions.get(params.session_id)), messages: all.filter(m => m.seq > params.after_seq).slice(0, limit) };
          const before = params.before_seq ? all.filter(m => m.seq < params.before_seq) : all;
          const page = before.slice(-limit);
          return { session: header(sessions.get(params.session_id)), messages: page, more: before.length > page.length };
        }
        case "run.subscribe": return { subscription: `run:${params.session_id}`, status: { session_id: params.session_id, state: "idle" } };
        case "run.status": return backend.runStatus?.(params) ?? { session_id: params.session_id, state: "idle" };
        case "message.send": {
          const prior = (messages.get(params.session_id) || []).find(m => m.client_message_id === params.client_message_id);
          if (prior) return { message: prior, run_id: prior.run_id, replayed: true };
          if (params.replay_only) throw Object.assign(new Error("this message never reached the backend"), { code: "not_found", notSent: true });
          const user = backend.addMessage(params.session_id, "user", params.content, { client_message_id: params.client_message_id, attachments: params.attachments });
          user.run_id = backend.nextRun?.() ?? `run_${user.seq}`;
          return { message: user, run_id: user.run_id };
        }
        default: throw Object.assign(new Error(`unexpected ${op}`), { code: "unsupported_capability" });
      }
    },
  };
  return backend;
}
