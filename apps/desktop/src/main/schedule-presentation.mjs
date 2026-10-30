import crypto from 'node:crypto';

// A presentation is a bounded instruction from the backend. It never contains
// schedule data, executable code, or authority to mutate a local schedule.
export const CLIENT_ACTIONS_SCHEMA = `CREATE TABLE client_actions(
  request_id TEXT PRIMARY KEY REFERENCES tasks(request_id), action_json TEXT NOT NULL,
  state TEXT NOT NULL CHECK(state IN ('pending','claimed','shown','failed','skipped')),
  acknowledged INTEGER NOT NULL DEFAULT 0);`;

const UUID = /^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/u;
const terminal = new Set(['shown', 'failed', 'skipped']);
const actionKeys = 'action_id,date_end,date_start,expires_at,local_conversation_id,request_id,schema_version,timezone,type';

export function validateScheduleFilter(value) {
  if (!value || Object.keys(value).sort().join(',') !== 'date_end,date_start,timezone' ||
      typeof value.timezone !== 'string' || !value.timezone || value.timezone === 'Local' || value.timezone.length > 80) throw new Error('Invalid schedule filter');
  try { new Intl.DateTimeFormat('en', { timeZone: value.timezone }).format(); }
  catch { throw new Error('Invalid schedule timezone'); }
  const validDate = (date) => typeof date === 'string' && /^\d{4}-\d{2}-\d{2}$/u.test(date) &&
    Number.isFinite(Date.parse(date)) && new Date(date).toISOString().slice(0, 10) === date;
  if (value.date_start === '' && value.date_end === '') return value;
  if (!validDate(value.date_start) || !validDate(value.date_end) || value.date_end <= value.date_start ||
      Date.parse(value.date_end) - Date.parse(value.date_start) > 366 * 86400000) throw new Error('Invalid schedule date range');
  return value;
}

export function validateScheduleAction(action) {
  if (!action || Object.keys(action).sort().join(',') !== actionKeys || action.schema_version !== 1 ||
      action.type !== 'schedule.show' || !UUID.test(action.request_id) || action.action_id !== action.request_id ||
      !UUID.test(action.local_conversation_id) || typeof action.expires_at !== 'string' ||
      !Number.isFinite(Date.parse(action.expires_at))) throw new Error('Invalid schedule presentation');
  validateScheduleFilter({ date_start: action.date_start, date_end: action.date_end, timezone: action.timezone });
  return action;
}

export function filterLocalSchedules(rows, filter) {
  if (!filter) return rows;
  validateScheduleFilter(filter);
  if (!filter.date_start) return rows;
  const formatter = new Intl.DateTimeFormat('en-CA', { timeZone: filter.timezone, year: 'numeric', month: '2-digit', day: '2-digit' });
  return rows.filter((row) => {
    if (!Number.isFinite(Date.parse(row.due_time))) return false;
    const parts = Object.fromEntries(formatter.formatToParts(new Date(row.due_time)).map(part => [part.type, part.value]));
    const date = `${parts.year}-${parts.month}-${parts.day}`;
    return date >= filter.date_start && date < filter.date_end;
  });
}

export class SchedulePresentations {
  constructor(store, scopeKey) { this.store = store; this.db = store.db; this.scopeKey = scopeKey; this.views = new Map(); }

  beginView(scope) {
    const key = this.scopeKey(scope), viewID = crypto.randomUUID();
    this.db.prepare(`UPDATE client_actions SET state='skipped' WHERE state='claimed' AND request_id IN
      (SELECT t.request_id FROM tasks t JOIN conversations c ON c.id=t.conversation_id WHERE c.scope=?)`).run(key);
    this.views.set(key, viewID);
    return { view_id: viewID };
  }

  verifyView(scope, viewID) {
    if (typeof viewID !== 'string' || this.views.get(this.scopeKey(scope)) !== viewID) throw new Error('Schedule presentation view changed');
  }

  // Called inside the result's text/files/receipt transaction.
  insert(task, action) {
    validateScheduleAction(action);
    const envelope = JSON.parse(task.context_json);
    if (!envelope.client_presentation?.schedule_show || action.request_id !== task.request_id ||
        action.local_conversation_id !== task.conversation_id || action.timezone !== envelope.client_presentation.timezone) {
      throw new Error('Schedule presentation does not match its original request');
    }
    this.db.prepare("INSERT INTO client_actions VALUES(?,?,'pending',0)").run(task.request_id, JSON.stringify(action));
  }

  list(conversationID) {
    return this.db.prepare('SELECT a.* FROM client_actions a JOIN tasks t USING(request_id) WHERE t.conversation_id=? ORDER BY a.rowid')
      .all(conversationID).map(({ action_json, state }) => ({ ...JSON.parse(action_json), state }));
  }

  claim(scope, requestID, conversationID, now = Date.now()) {
    const task = this.store.request(scope, requestID);
    if (task.conversation_id !== conversationID) throw new Error('Schedule presentation conversation changed');
    const row = this.db.prepare('SELECT * FROM client_actions WHERE request_id=?').get(requestID);
    if (!row || row.state !== 'pending') return null;
    const action = validateScheduleAction(JSON.parse(row.action_json));
    const state = Date.parse(action.expires_at) > now ? 'claimed' : 'skipped';
    const changed = this.db.prepare("UPDATE client_actions SET state=? WHERE request_id=? AND state='pending'").run(state, requestID);
    return changed.changes === 1 && state === 'claimed' ? action : null;
  }

  finish(scope, requestID, status) {
    this.store.request(scope, requestID);
    if (!terminal.has(status)) throw new Error('Invalid schedule presentation receipt');
    const row = this.db.prepare('SELECT state FROM client_actions WHERE request_id=?').get(requestID);
    if (!row || (terminal.has(row.state) && row.state !== status) || (row.state === 'pending' && status === 'shown')) {
      throw new Error('Schedule presentation receipt conflicts with its claim');
    }
    this.db.prepare('UPDATE client_actions SET state=? WHERE request_id=?').run(status, requestID);
    return { action_id: requestID, status };
  }

  pending(scope, now = Date.now()) {
    // Background results may belong to conversations the user never reopens.
    // Expiry still produces a durable terminal receipt without navigating.
    for (const row of this.db.prepare(`SELECT a.request_id,a.action_json FROM client_actions a JOIN tasks t USING(request_id)
      JOIN conversations c ON c.id=t.conversation_id WHERE c.scope=? AND a.state='pending'`).all(this.scopeKey(scope))) {
      if (Date.parse(JSON.parse(row.action_json).expires_at) <= now) {
        this.db.prepare("UPDATE client_actions SET state='skipped' WHERE request_id=? AND state='pending'").run(row.request_id);
      }
    }
    return this.db.prepare(`SELECT a.request_id,a.state FROM client_actions a JOIN tasks t USING(request_id)
      JOIN conversations c ON c.id=t.conversation_id WHERE c.scope=? AND a.acknowledged=0
      AND a.state IN ('shown','failed','skipped')`).all(this.scopeKey(scope))
      .map(row => ({ action_id: row.request_id, status: row.state }));
  }

  acknowledge(scope, receipt) {
    this.store.request(scope, receipt.action_id);
    const result = this.db.prepare('UPDATE client_actions SET acknowledged=1 WHERE request_id=? AND state=?').run(receipt.action_id, receipt.status);
    if (result.changes !== 1) throw new Error('Schedule presentation receipt changed');
  }
}
