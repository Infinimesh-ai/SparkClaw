// Read-only diagnostic adapter for pinned App-CLI .41. Do not import its clients
// or Ledger: even construction and identity lookups can modify source evidence.
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import {fileURLToPath} from 'node:url';
import {spawnSync} from 'node:child_process';
import {DatabaseSync} from 'node:sqlite';
import net from 'node:net';

const SCRIPT = fileURLToPath(import.meta.url);
const MAX_RECORD = 1048576, MAX_ROWS = 2000, MAX_OUTPUT = 1048576, MAX_BYTES = 268435456;
const PROFILE = '(version 1) (allow default) (deny file-write*) (deny network*)';
const STATES = new Set(['prepared', 'pending', 'running', 'blocked', 'uncertain', 'completed', 'failed', 'cancelled', 'resolved']);
const TERMINAL = new Set(['completed', 'failed', 'cancelled', 'resolved']);
const DISPATCH = new Set(['reserved', 'consume_pending', 'released', 'voided']);
const APPS = new Set(['mail-gmail', 'mail-outlook', 'mail-qq-mail']);
const COMMANDS = new Set(['probe', 'send', 'read', 'discover', 'capture', 'enumerate_thread', 'mark_read', 'collect_page', 'watch']);
const EVENTS = new Set(['admitted', 'state', 'cancel_requested', 'authorization_renewed', 'effect_started', 'dispatch_reserved',
  'dispatch_released', 'dispatch_voided', 'result_acknowledged', 'resolved', 'gap', 'host_cleanup_failed', 'mailbox_changed', 'watch_ready']);
const REASONS = new Set(['EXECUTOR_RESTARTED', 'ROLLBACK_UNVERIFIED', 'ROLLBACK_RELEASED', 'RESOLVED_BY_USER',
  'AUTHORIZATION_EXPIRED', 'AUTHORIZATION_REVOKED', 'CANCEL_REQUESTED', 'DEADLINE_EXCEEDED', 'HANDLER_FAILED',
  'HOST_CLEANUP_FAILED', 'BROWSER_EXTENSION_UNAVAILABLE', 'EMAIL_NETWORK_READ_FAILED', 'EMAIL_SEND_OUTCOME_UNKNOWN',
  'executor_restarted', 'rollback_recovery']);
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/u;
const HASH = /^[0-9a-f]{64}$/u;
const KEY = /^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/u;
const OP = /^op3\.(0|[1-9][0-9]{0,14})\.(0|[1-9][0-9]{0,14})\.([0-9a-f]{32})$/u;
const empty = gap => ({schema_version: 1, tasks: [], gaps: gap ? [gap] : [], rows_read: 0, bytes_read: 0});
const integer = value => Number.isSafeInteger(value) && value >= 0;
const text = (value, max = 256) => typeof value === 'string' && value.isWellFormed() && value.length > 0 && Buffer.byteLength(value) <= max && !/[\x00-\x1f\x7f]/u.test(value);
function operationKey(value) {
  const match = typeof value === 'string' && OP.exec(value);
  return Boolean(match && Number.isSafeInteger(Number(match[1])) && Number.isSafeInteger(Number(match[2])));
}
function requestKey(value) {
  return typeof value === 'string' && KEY.test(value) && (!/^(?:op3|sch3|s3)\./u.test(value) || operationKey(value));
}
function validateInput(input) {
  return input && typeof input === 'object' && !Array.isArray(input) && text(input.path, 4096) && path.isAbsolute(input.path) &&
    text(input.principal) && text(input.owner) && HASH.test(input.owner_scope ?? '') &&
    (input.index_directory === undefined || text(input.index_directory, 4096) && path.isAbsolute(input.index_directory)) &&
    Array.isArray(input.operations) && input.operations.length > 0 && input.operations.length <= MAX_ROWS &&
    (input.max_rows === undefined || integer(input.max_rows) && input.max_rows > 0 && input.max_rows <= MAX_ROWS) &&
    (input.max_record_bytes === undefined || integer(input.max_record_bytes) && input.max_record_bytes > 0 && input.max_record_bytes <= MAX_RECORD);
}

// The only production entry: even stat/open of a source happens in the child.
// readOnly/query_only alone do not prevent SQLite from writing WAL shared memory.
export function readLedger(input) {
  if (!validateInput(input)) return empty('ledger_input_invalid');
  if (process.platform !== 'darwin') return empty('ledger_write_guard_unavailable');
  const env = {...process.env}; delete env.NODE_OPTIONS; delete env.NODE_PATH;
  let source, encoded;
  try {
    // The Go command embeds this module and evaluates its bytes. Its guarded
    // child must reuse those bytes, never trust a helper found in the cwd.
    const evalAt = process.execArgv.findIndex(arg => arg === '--eval' || arg === '-e');
    source = /^\[eval[0-9]+\]$/u.test(path.basename(SCRIPT)) && evalAt >= 0
      ? process.execArgv[evalAt + 1] : `${fs.readFileSync(SCRIPT, 'utf8')}\nawait runCLI();`;
    encoded = JSON.stringify(input);
    if (Buffer.byteLength(encoded) > MAX_RECORD) return empty('ledger_input_invalid');
  } catch {return empty('ledger_reader_failed');}
  const child = spawnSync('/usr/bin/sandbox-exec', ['-p', PROFILE, process.execPath, '--no-warnings', '--input-type=module', '--eval', source, '--', '--guarded-reader'],
    {input: encoded, encoding: 'utf8', timeout: 9000, killSignal: 'SIGKILL', maxBuffer: MAX_OUTPUT, env});
  if (child.error?.code === 'ETIMEDOUT') return empty('ledger_time_limit');
  if (child.error?.code === 'ENOBUFS') return empty('ledger_output_limit');
  if (child.error || child.status !== 0) return empty('ledger_write_guard_unavailable');
  try {
    const result = JSON.parse(child.stdout);
    if (result?.schema_version !== 1 || !Array.isArray(result.tasks) || !Array.isArray(result.gaps)) return empty('ledger_reader_failed');
    return result;
  } catch { return empty('ledger_reader_failed'); }
}

// Exact .41 canonical encoding for the string/object values used by index keys.
function canonical(value) {
  let tag, payload;
  if (typeof value === 'string') {tag = 's'; payload = Buffer.from(value);}
  else {
    tag = 'o'; payload = Buffer.concat(Object.keys(value).sort((a, b) => Buffer.compare(Buffer.from(a), Buffer.from(b)))
      .flatMap(key => [canonical(key), canonical(value[key])]));
  }
  return Buffer.concat([Buffer.from(`${tag}${payload.length}:`), payload]);
}
const digest = value => crypto.createHash('sha256').update(canonical(value)).digest('hex');

// Reject duplicate keys, excess nesting and malformed UTF-8 in identity proofs.
function strictJSON(buffer) {
  const source = new TextDecoder('utf-8', {fatal: true}).decode(buffer);
  let at = 0, nodes = 0;
  const whitespace = () => {while (/[\x20\x09\x0a\x0d]/u.test(source[at] ?? 'x')) at++;};
  function string() {
    const start = at++;
    while (at < source.length) {
      if (source[at] === '\\') {at += 2; continue;}
      if (source[at++] === '"') return JSON.parse(source.slice(start, at));
    }
    throw new Error('invalid');
  }
  function value(depth) {
    if (++nodes > 10000 || depth > 32) throw new Error('invalid');
    whitespace(); const first = source[at];
    if (first === '"') {string(); return;}
    if (first === '{' || first === '[') {
      const object = first === '{', end = object ? '}' : ']'; at++; whitespace();
      const keys = new Set(); if (source[at] === end) {at++; return;}
      for (;;) {
        if (object) {
          whitespace(); if (source[at] !== '"') throw new Error('invalid');
          const key = string(); if (keys.has(key)) throw new Error('invalid'); keys.add(key);
          whitespace(); if (source[at++] !== ':') throw new Error('invalid');
        }
        value(depth + 1); whitespace();
        if (source[at] === end) {at++; return;}
        if (source[at++] !== ',') throw new Error('invalid');
      }
    }
    const match = /^(?:null|true|false|-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?)/u.exec(source.slice(at));
    if (!match) throw new Error('invalid'); at += match[0].length;
  }
  value(0); whitespace(); if (at !== source.length) throw new Error('invalid');
  return JSON.parse(source);
}
function sameFile(a, b) {return a.dev === b.dev && a.ino === b.ino && a.size === b.size && a.mtimeMs === b.mtimeMs && a.mode === b.mode;}
function readProof(file, limit, result, gap) {
  let fd;
  try {
    fd = fs.openSync(file, fs.constants.O_RDONLY | fs.constants.O_NOFOLLOW);
    const before = fs.fstatSync(fd);
    if (!before.isFile()) {gap('ledger_record_invalid'); return {invalid: true};}
    if (before.size > limit) {gap('ledger_record_limit'); return {invalid: true};}
    if (result.bytes_read + before.size > MAX_BYTES) {gap('ledger_byte_limit'); return {invalid: true};}
    const buffer = Buffer.alloc(before.size + 1), length = fs.readSync(fd, buffer, 0, buffer.length, 0);
    result.bytes_read += length;
    if (length > limit) {gap('ledger_record_limit'); return {invalid: true};}
    if (length !== before.size || !sameFile(before, fs.fstatSync(fd))) {gap('ledger_source_changed'); return {invalid: true};}
    return {value: strictJSON(buffer.subarray(0, length)), bytes: buffer.subarray(0, length)};
  } catch (error) {
    if (error.code === 'ENOENT') return {missing: true};
    gap('ledger_record_invalid'); return {invalid: true};
  } finally {if (fd !== undefined) fs.closeSync(fd);}
}

const REQUIRED = {
  metadata: {key: 'TEXT', value: 'INTEGER'},
  tasks: {id: 'TEXT', principal: 'TEXT', owner: 'TEXT', request_key: 'TEXT', app: 'TEXT', command: 'TEXT', request: 'TEXT',
    status: 'TEXT', reason: 'TEXT', effect: 'INTEGER', attempt: 'INTEGER', dispatch: 'TEXT', terminal_revision: 'INTEGER', result_digest: 'TEXT', ack: 'TEXT', sequence: 'INTEGER'},
  events: {task: 'TEXT', sequence: 'INTEGER', type: 'TEXT', payload: 'TEXT'},
};
function schemaValid(db) {
  for (const [table, required] of Object.entries(REQUIRED)) {
    if (db.prepare('SELECT type FROM sqlite_schema WHERE name=?').get(table)?.type !== 'table') return false;
    const columns = db.prepare('SELECT name,type,pk,hidden FROM pragma_table_xinfo(?) LIMIT 129').all(table);
    if (columns.length > 128) return false;
    const byName = new Map(columns.map(column => [column.name, column]));
    if (Object.entries(required).some(([name, type]) => byName.get(name)?.type !== type || byName.get(name)?.hidden !== 0)) return false;
    if (table === 'metadata' && byName.get('key').pk !== 1 || table === 'tasks' && byName.get('id').pk !== 1 ||
      table === 'events' && (byName.get('task').pk !== 1 || byName.get('sequence').pk !== 2)) return false;
  }
  for (const [table, required] of Object.entries({metadata: ['key'], tasks: ['id', 'principal,owner,request_key'], events: ['task,sequence']})) {
    const indexes = db.prepare(`SELECT il.name, ii.seqno, ii.name AS column_name, ii.coll FROM pragma_index_list(?) il,
      pragma_index_xinfo(il.name) ii WHERE il."unique"=1 AND il.partial=0 AND ii.key=1 ORDER BY il.name,ii.seqno LIMIT 65`).all(table);
    if (indexes.length > 64) return false;
    const groups = new Map();
    for (const row of indexes) {if (!groups.has(row.name)) groups.set(row.name, []); groups.get(row.name).push(row);}
    if (required.some(columns => ![...groups.values()].some(index =>
      index.every(column => column.coll === 'BINARY') && index.map(column => column.column_name).join(',') === columns))) return false;
  }
  return true;
}

// SQLite parses request JSON only after byte/validity checks. Required binding
// keys must occur once; duplicate owner_scope never grants access to a task.
const REQUEST_VALID = `CASE WHEN typeof(request)='text' AND length(CAST(request AS BLOB))<=? AND json_valid(request)
  THEN CASE WHEN json_type(request)='object' AND json_type(request,'$.arguments')='object'
    AND (SELECT count(*) FROM json_each(request) WHERE key='arguments')=1
    AND (SELECT count(*) FROM json_each(request) WHERE key='app')=1
    AND (SELECT count(*) FROM json_each(request) WHERE key='command')=1
    AND (SELECT count(*) FROM json_each(request) WHERE key='request_key')=1
    AND json_extract(request,'$.app') COLLATE BINARY=app COLLATE BINARY AND json_extract(request,'$.command') COLLATE BINARY=command COLLATE BINARY
    AND json_extract(request,'$.request_key') COLLATE BINARY=request_key COLLATE BINARY
    THEN CASE WHEN (SELECT count(*) FROM json_each(request,'$.arguments') WHERE key='owner_scope')=1
      AND json_extract(request,'$.arguments.owner_scope')=? THEN 1
      WHEN (SELECT count(*) FROM json_each(request,'$.arguments') WHERE key='owner_scope')=0 THEN 2 ELSE 0 END
    ELSE 0 END ELSE 0 END`;
const bounded = column => `CASE WHEN typeof(${column})='text' AND length(CAST(${column} AS BLOB))<=256 THEN ${column} END AS ${column}`;
const TASK_SQL = `SELECT ${['id','request_key','app','command','status','reason','dispatch','result_digest'].map(bounded).join(',')},
  reason IS NOT NULL AS reason_present,
  result_digest IS NOT NULL AS result_digest_present,
  dispatch IS NULL OR typeof(dispatch)='text' AND length(CAST(dispatch AS BLOB))<=256 AS dispatch_valid,
  effect,attempt,terminal_revision,sequence,
  length(CAST(request AS BLOB))+coalesce(length(CAST(ack AS BLOB)),0) AS record_bytes,
  CASE WHEN typeof(ack)='text' AND length(CAST(ack AS BLOB))<=? THEN ack END AS ack,
  ack IS NULL AS ack_absent, ${REQUEST_VALID} AS scope_valid
  FROM tasks WHERE principal COLLATE BINARY=? AND owner COLLATE BINARY=? AND request_key COLLATE BINARY=? LIMIT 1`;
const EVENT_SQL = `SELECT sequence, ${bounded('type')}, length(CAST(payload AS BLOB)) AS record_bytes,
  CASE WHEN typeof(payload)='text' AND length(CAST(payload AS BLOB))<=? AND json_valid(payload)
    THEN CASE WHEN json_type(payload)='object'
      AND (SELECT count(*) FROM json_each(payload) WHERE key='status')<=1
      AND (SELECT count(*) FROM json_each(payload) WHERE key='reason')<=1 THEN 1 ELSE 0 END ELSE 0 END AS valid,
  CASE WHEN typeof(payload)='text' AND length(CAST(payload AS BLOB))<=? AND json_valid(payload)
    THEN CASE WHEN json_type(payload,'$.reason') IS NULL OR json_type(payload,'$.reason')='null' THEN NULL
      WHEN length(CAST(json_extract(payload,'$.reason') AS BLOB))<=256 THEN json_extract(payload,'$.reason') ELSE 'unclassified' END END AS reason,
  CASE WHEN typeof(payload)='text' AND length(CAST(payload AS BLOB))<=? AND json_valid(payload)
    THEN CASE WHEN json_type(payload,'$.status') IS NULL OR json_type(payload,'$.status')='null' THEN NULL
      WHEN length(CAST(json_extract(payload,'$.status') AS BLOB))<=256 THEN json_extract(payload,'$.status') ELSE 'unclassified' END END AS status
  FROM events WHERE task COLLATE BINARY=? ORDER BY sequence LIMIT ?`;
function ackState(row, gap) {
  if (row.ack_absent === 1) return 'pending';
  try {
    const ack = strictJSON(Buffer.from(row.ack ?? ''));
    if (!TERMINAL.has(row.status) || !HASH.test(row.result_digest ?? '') || !ack || Array.isArray(ack) || Object.keys(ack).sort().join(',') !== 'at_ms,receipt_id,receiver_id,terminal_revision' ||
      !text(ack.receiver_id) || !text(ack.receipt_id) || !integer(ack.at_ms) || !integer(ack.terminal_revision) ||
      ack.terminal_revision < 1 || ack.terminal_revision !== row.terminal_revision) throw new Error('invalid');
    return 'acknowledged';
  } catch {gap('ledger_ack_invalid'); return 'incomplete';}
}
function safeReason(reason, gap) {
  if (reason === null || reason === undefined) return undefined;
  if (REASONS.has(reason)) return reason;
  gap('ledger_value_unclassified'); return 'unclassified';
}
function validReceipt(binding) {
  return binding && typeof binding === 'object' && !Array.isArray(binding) && UUID.test(binding.task_id ?? '') &&
    integer(binding.terminal_revision) && binding.terminal_revision > 0 && HASH.test(binding.result_digest ?? '');
}
function receiptMatches(binding, row) {
  return validReceipt(binding) && row.id === binding.task_id && row.terminal_revision === binding.terminal_revision &&
    row.result_digest === binding.result_digest;
}
function requestBinding(request, input, op, key) {
  if (!request || request.app !== op.app || request.command !== op.command || request.request_key !== key ||
    !request.arguments || typeof request.arguments !== 'object' || Array.isArray(request.arguments)) return false;
  // A head is only a mapping candidate. The task must independently match the
  // caller's owner-scoped retained receipt before an absent scope is accepted.
  return Object.hasOwn(request.arguments, 'owner_scope') ? request.arguments.owner_scope === input.owner_scope : validReceipt(op.receipt_binding);
}
function resolveKey(op, input, result, gap, limit) {
  if (!op || typeof op !== 'object' || Array.isArray(op) ||
    op.app !== undefined && !APPS.has(op.app) || op.command !== undefined && !COMMANDS.has(op.command) ||
    op.task_id !== undefined && !UUID.test(op.task_id) ||
    op.receipt_binding !== undefined && !validReceipt(op.receipt_binding) ||
    op.request_key !== undefined && !requestKey(op.request_key) ||
    op.invocation !== undefined && !requestKey(op.invocation)) {gap('ledger_identity_invalid'); return null;}
  if (!op.invocation || operationKey(op.invocation)) {
    const key = op.request_key ?? op.invocation;
    if (!key || op.invocation && key !== op.invocation) {gap('ledger_identity_invalid'); return null;}
    return key;
  }
  if (!input.index_directory || !APPS.has(op.app) || !COMMANDS.has(op.command)) {gap('legacy_identity_mapping_missing'); return null;}
  const legacyKey = digest({taskID: op.invocation, app: op.app, command: op.command});
  const headDigest = digest({principal: input.principal, owner: input.owner, request_key: legacyKey});
  const head = readProof(path.join(input.index_directory, `${headDigest}.head.json`), limit, result, gap);
  const alias = readProof(path.join(input.index_directory, `alias-${digest({owner: input.owner, taskID: op.invocation, app: op.app, command: op.command})}.json`), limit, result, gap);
  if (head.invalid || alias.invalid) {gap('legacy_identity_mapping_missing'); return null;}
  let headKey;
  if (!head.missing) {
    if (head.value?.schema_version !== 1 || !integer(head.value.revision) || head.value.revision < 1 ||
      !requestBinding(head.value.request, input, op, legacyKey)) {gap('legacy_identity_mapping_conflict'); return null;}
    headKey = legacyKey;
  } else {
    // Do not invoke ProductClient.head()'s migration/repair of the old index.
    const older = readProof(path.join(input.index_directory, `${headDigest}.json`), limit, result, gap);
    if (older.invalid) {gap('legacy_identity_mapping_missing'); return null;}
    if (!older.missing) {
      if (!text(older.value?.reference) || !requestBinding(older.value.request, input, op, legacyKey)) {gap('legacy_identity_mapping_conflict'); return null;}
      headKey = legacyKey;
    }
  }
  const aliasKey = alias.missing ? undefined : alias.value?.request_key;
  if (aliasKey !== undefined && !operationKey(aliasKey) || !alias.missing && aliasKey === undefined || headKey && aliasKey && headKey !== aliasKey) {
    gap('legacy_identity_mapping_conflict'); return null;
  }
  const key = headKey ?? aliasKey;
  if (!key) {gap('legacy_identity_mapping_missing'); return null;}
  if (op.request_key && op.request_key !== key) {gap('legacy_identity_mapping_conflict'); return null;}
  return key;
}

// Test seam for an OS write-denying sandbox/read-only mount. Production never
// selects this from input; its entry always invokes readLedger's real guard.
export function readLedgerUnderGuard(input) {
  if (!validateInput(input)) return empty('ledger_input_invalid');
  const result = empty(), gap = code => {if (!result.gaps.includes(code)) result.gaps.push(code);};
  const limit = input.max_record_bytes ?? MAX_RECORD, rows = input.max_rows ?? MAX_ROWS;
  let db;
  try {
    let stat;
    try {stat = fs.lstatSync(input.path);} catch (error) {gap(error.code === 'ENOENT' ? 'ledger_source_missing' : 'ledger_source_unreadable'); return result;}
    if (!stat.isFile() || stat.isSymbolicLink()) {gap('ledger_source_invalid'); return result;}
    db = new DatabaseSync(input.path, {readOnly: true, enableForeignKeyConstraints: false, allowExtension: false, timeout: 100});
    db.enableDefensive(true);
    db.exec('PRAGMA query_only=ON; PRAGMA trusted_schema=OFF; BEGIN');
    if (!schemaValid(db)) {gap('ledger_schema_unsupported'); return result;}
    const metadata = Object.fromEntries(db.prepare(`SELECT key,
      CASE WHEN typeof(value)='integer' AND value BETWEEN 0 AND 9007199254740991 THEN value END AS value FROM metadata WHERE key IN
      ('schema_version','epoch','commit_sequence','generation','open_epoch','sealed_through','authorization_generation') LIMIT 8`).all().map(row => [row.key, row.value]));
    if (metadata.schema_version !== 2 || Object.keys(metadata).length !== 7 || Object.values(metadata).some(value => !integer(value)) ||
      metadata.generation < 1 || metadata.epoch < 1 || metadata.open_epoch < 1) {gap('ledger_schema_unsupported'); return result;}
    const markerPath = path.join(path.dirname(input.path), 'authority.json');
    const marker = readProof(markerPath, limit, result, gap);
    if (marker.missing) {gap('ledger_authority_missing'); return result;}
    if (marker.invalid || marker.value?.schema_version !== 2 || !integer(marker.value.epoch) || !integer(marker.value.commit_sequence) ||
      marker.value.epoch > metadata.epoch || marker.value.commit_sequence > metadata.commit_sequence) {gap('ledger_authority_invalid'); return result;}
    const findTask = db.prepare(TASK_SQL), findEvents = db.prepare(EVENT_SQL), seen = new Set();
    for (const op of input.operations) {
      if (result.rows_read >= rows) {gap('ledger_row_limit'); break;}
      const key = resolveKey(op, input, result, gap, limit);
      if (!key) continue;
      const row = findTask.get(limit, limit, input.owner_scope, input.principal, input.owner, key);
      if (!row) {gap('ledger_task_missing'); continue;}
      result.rows_read++; result.bytes_read += integer(row.record_bytes) ? row.record_bytes : 0;
      if (result.bytes_read > MAX_BYTES) {gap('ledger_byte_limit'); break;}
      if (!integer(row.record_bytes) || row.record_bytes > limit) {gap('ledger_record_limit'); continue;}
      // .41 send lacks owner_scope. Only its caller's owner-scoped durable
      // Gateway receipt supplies an equivalent binding; a mismatched/present
      // scope can never fall back to the receipt. No timestamp joins.
      const ownerBinding = row.scope_valid === 1 ? 'request_owner_scope'
        : row.scope_valid === 2 && row.request_key === key && receiptMatches(op.receipt_binding, row) ? 'receipt' : null;
      if (!ownerBinding) {gap('ledger_scope_unverified'); continue;}
      if (op.task_id && row.id !== op.task_id || op.app && row.app !== op.app || op.command && row.command !== op.command) {gap('ledger_identity_conflict'); continue;}
      if (!UUID.test(row.id ?? '') || !requestKey(row.request_key) || !APPS.has(row.app) || !COMMANDS.has(row.command) ||
        !STATES.has(row.status) || ![0, 1].includes(row.effect) || !integer(row.attempt) || !integer(row.terminal_revision) ||
        !integer(row.sequence) || !row.dispatch_valid || row.dispatch !== null && !DISPATCH.has(row.dispatch)) {gap('ledger_record_invalid'); continue;}
      if (seen.has(row.id)) continue; seen.add(row.id);
      if (row.result_digest_present && !HASH.test(row.result_digest ?? '')) gap('ledger_value_unclassified');
      const item = {id: row.id, request_key: row.request_key, ...(op.invocation ? {invocation: op.invocation} : {}), status: row.status,
        reason: safeReason(row.reason ?? (row.reason_present ? 'unclassified' : undefined), gap), effect: row.effect, attempt: row.attempt, dispatch: row.dispatch, terminal_revision: row.terminal_revision,
        ack: ackState(row, gap), sequence: row.sequence, owner_scope: input.owner_scope, owner_binding: ownerBinding, app: row.app, command: row.command, events: []};
      item.result_digest = HASH.test(row.result_digest ?? '') ? row.result_digest : '';
      let previous = 0;
      const remaining = rows - result.rows_read;
      for (const event of findEvents.iterate(limit, limit, limit, row.id, remaining)) {
        result.rows_read++; result.bytes_read += integer(event.record_bytes) ? event.record_bytes : 0;
        if (result.bytes_read > MAX_BYTES) {gap('ledger_byte_limit'); break;}
        if (!integer(event.record_bytes) || event.record_bytes > limit) {gap('ledger_record_limit'); continue;}
        if (!event.valid || !integer(event.sequence) || event.sequence < 1 || event.sequence > row.sequence) {gap('ledger_event_invalid'); continue;}
        if (event.sequence !== previous + 1) gap('ledger_event_gap'); previous = event.sequence;
        if (!EVENTS.has(event.type)) {gap('ledger_value_unclassified'); continue;}
        const projected = {sequence: event.sequence, type: event.type};
        const reason = safeReason(event.reason, gap); if (reason !== undefined) projected.reason = reason;
        if (event.status !== null) {if (STATES.has(event.status)) projected.status = event.status; else gap('ledger_value_unclassified');}
        item.events.push(projected);
      }
      if (previous !== row.sequence) {gap('ledger_event_gap'); if (result.rows_read >= rows) gap('ledger_row_limit');}
      result.tasks.push(item);
      if (Buffer.byteLength(JSON.stringify(result)) > MAX_OUTPUT - 1024) {result.tasks.pop(); gap('ledger_output_limit'); break;}
    }
    const markerAfter = readProof(markerPath, limit, result, gap);
    if (!markerAfter.bytes || !marker.bytes.equals(markerAfter.bytes)) gap('ledger_source_changed');
    db.exec('COMMIT');
  } catch {gap(db ? 'ledger_read_failed' : 'ledger_open_readonly_failed');}
  finally {try {db?.close();} catch {gap('ledger_read_failed');}}
  return result;
}
function kernelText(file) {
  const fd = fs.openSync(file, 'r');
  try {
    const buffer = Buffer.alloc(MAX_RECORD + 1); let length = 0;
    while (length < buffer.length) {
      const count = fs.readSync(fd, buffer, length, buffer.length - length, null);
      if (!count) return buffer.subarray(0, length).toString('utf8');
      length += count;
    }
  } finally {fs.closeSync(fd);}
  throw new Error('kernel evidence limit');
}
async function guardedProcess() {
  // A direct private-worker invocation must not read outside the guard. Opening
  // /dev/null for writing has no effects, but the file-write* sandbox denies it.
  if (process.platform === 'darwin') {
    try {const fd = fs.openSync('/dev/null', 'w'); fs.closeSync(fd); return false;}
    catch (error) {return error.code === 'EPERM';}
  }
  if (process.platform !== 'linux' || process.pid !== 1) return false;
  try {
    const directory = fs.opendirSync('/proc');
    try {
      for (let count = 0;; count++) {
        if (count >= 4096) return false;
        const entry = directory.readSync(); if (!entry) break;
        if (/^\d+$/u.test(entry.name) && entry.name !== '1') return false;
      }
    } finally {directory.closeSync();}
    const status = kernelText('/proc/self/status');
    const capability = key => {
      const match = new RegExp(`^${key}:\\s+([0-9a-f]+)$`, 'mu').exec(status);
      return match ? BigInt(`0x${match[1]}`) : -1n;
    };
    if (!/^NoNewPrivs:\s+1$/mu.test(status) || !/^Seccomp:\s+2$/mu.test(status) ||
        typeof LINUX_LEDGER_READ_CAPABILITY !== 'bigint' ||
        !['CapEff', 'CapPrm', 'CapBnd'].every(key => capability(key) === LINUX_LEDGER_READ_CAPABILITY) ||
        !['CapInh', 'CapAmb'].every(key => capability(key) === 0n)) return false;
    const mounts = kernelText('/proc/self/mountinfo').trim().split('\n');
    let proc = false;
    for (const line of mounts) {
      const [left, right] = line.split(' - '), fields = left.split(' '), options = new Set(fields[5]?.split(','));
      if (!right || !['ro', 'nodev', 'nosuid'].every(option => options.has(option))) return false;
      if (right.split(' ')[0] === 'proc') {
        if (fields[4] !== '/proc' && !fields[4].startsWith('/proc/')) return false;
        if (fields[4] === '/proc') proc = true;
      }
    }
    if (!proc) return false;
    // nodev also closes device-file writes on an otherwise read-only mount.
    try {const fd = fs.openSync('/dev/null', fs.constants.O_WRONLY); fs.closeSync(fd); return false;}
    catch (error) {if (error.code !== 'EACCES') return false;}
    // Seccomp must deny socket creation, including UNIX-domain command paths.
    // The probe uses an unbound abstract address and never sends any data.
    return await new Promise(resolve => {
      const socket = net.createConnection({path: `\0sparkclaw-readonly-${crypto.randomUUID()}`});
      socket.once('error', error => resolve(error.code === 'EPERM'));
      socket.once('connect', () => {socket.destroy(); resolve(false);});
    });
  } catch {return false;}
}
export async function runCLI() {
  let input, bytes = 0, exceeded = false; const chunks = [];
  for await (const chunk of process.stdin) {
    bytes += chunk.length; if (bytes > MAX_RECORD) {exceeded = true; break;} chunks.push(chunk);
  }
  try {input = exceeded ? undefined : strictJSON(Buffer.concat(chunks));} catch {input = undefined;}
  const result = process.argv.includes('--guarded-reader')
    ? (await guardedProcess() ? readLedgerUnderGuard(input) : empty('ledger_write_guard_unavailable')) : readLedger(input);
  process.stdout.write(`${JSON.stringify(result)}\n`);
}
