import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { DatabaseSync } from 'node:sqlite';
import { ClientStore, CLIENT_SCHEMA_VERSION, CLIENT_LIMITS, parseResultPayload } from '../src/main/client-store.mjs';
import { canonicalContextJSON, clarificationSelection, conversationContextDigest, validateClarificationRecord } from '../src/main/conversation-context.mjs';

const scope = { deployment_id: 'deployment_test', owner_id: 'owner_test', client_id: 'client_test' };
const hash = value => crypto.createHash('sha256').update(value).digest('hex');
function fixture(t) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'sparkclaw-context-v2-'));
  const store = new ClientStore(root); const conversation = store.create(scope, 'context');
  t.after(() => { try { store.close(); } catch {} fs.rmSync(root, { recursive: true, force: true }); });
  return { root, store, conversation };
}
function recordFor(store, task, prompt, overrides = {}) {
  const context = store.context(scope, task.request_id), now = Math.floor(Date.now() / 1000) * 1000;
  return { schema_version: 2, id: crypto.randomUUID(), request_id: task.request_id,
    source_message_id: context.messages.at(-1).id, source_text_sha256: hash(context.messages.at(-1).content),
    context_sha256: conversationContextDigest(context.messages, context.input_files), prompt_sha256: hash(prompt),
    issued_at: new Date(now).toISOString().replace('.000Z', 'Z'), expires_at: new Date(now + 3600000).toISOString().replace('.000Z', 'Z'),
    graph_revision: 'graph.1', catalog_revision: 'catalog.1', calibration_revision: 'calibration.1',
    options: [{ n: 1, kind: 'route', candidate_id: 'document.read#read', binding_sha256: 'b'.repeat(64) }], mac: 'a'.repeat(64), ...overrides };
}
function deliver(store, task, prompt, record) {
  store.markSubmitted(scope, task.request_id);
  const payload = JSON.stringify({ content: prompt, files: [], ...(record ? { clarification: record } : {}) });
  store.commitDelivery(scope, { request_id: task.request_id, sequence: 1, digest: hash(payload), payload }, new Map());
  store.acknowledge(scope, task.request_id, 1, hash(payload));
}

test('canonical context has stable Unicode/control encoding and input-handle promotion preserves its digest', () => {
  assert.equal(canonicalContextJSON({ z: '<中>\u2028/🐾\n', a: ['"', '\\', false, 12] }), '{"a":["\\\"","\\\\",false,12],"z":"<中>\u2028/🐾\\u000a"}');
  for (const invalid of [null, -1, 1.5, '\ud800', { invalidKey: 1 }, { a: undefined }]) assert.throws(() => canonicalContextJSON(invalid));
  const file = { id: 'file_1', name: '中.txt', size: 3, sha256: hash('中') };
  const original = [{ id: 'message_1', role: 'user', content: '读取这个' }];
  assert.equal(conversationContextDigest(original, [file]), conversationContextDigest([{ ...original[0], files: [file] }]));
  assert.notEqual(conversationContextDigest(original, [file]), conversationContextDigest([{ ...original[0], files_unavailable: true }], [file]));
  assert.throws(() => conversationContextDigest([{ ...original[0], files: [{ ...file, size: 9 }] }], [file]), /Conflicting/);
  // Shared with Go clarification.TestContextDigestBindsMessagesFilesAndMissingMarker.
  assert.equal(conversationContextDigest([{ id: 'm1', role: 'user', content: '中文\n', files: [
    { id: 'b', name: 'b.txt', size: 0, sha256: hash('') }, { id: 'a', name: 'a.txt', size: 1, sha256: hash('a') },
  ] }]), '6e7a95c5cc4b78adc0990cd995fffe4b280b2352ae892eea5f6c8f9278afe1c3');
});

test('clarification payload rejects missing, duplicate, mismatched and invalid option fields', t => {
  const { store, conversation } = fixture(t), task = store.enqueue(scope, conversation.id, 'read');
  const prompt = '1. Read the document', record = recordFor(store, task, prompt);
  validateClarificationRecord(record);
  const changes = [{ schema_version: 1 }, { extra: true }, { expires_at: record.issued_at }, { issued_at: '2026-02-30T00:00:00Z' },
    { options: [{ ...record.options[0], delivery_mode: 'current_route' }] },
    { options: [...record.options, { n: 2, kind: 'file', file_id: 'file_2', binding_sha256: 'b'.repeat(64) }] }];
  for (const changed of changes) assert.throws(() => validateClarificationRecord({ ...record, ...changed }), /Invalid clarification/);
  const raw = JSON.stringify({ content: prompt, files: [], clarification: record });
  assert.throws(() => parseResultPayload(raw.replace('"schema_version":2', '"schema_version":1,"schema_version":2')), /JSON object key/);
  assert.throws(() => parseResultPayload(JSON.stringify({ content: 'changed displayed target', files: [], clarification: record })), /text binding/);
  assert.equal(clarificationSelection('Read the document', record, prompt), 1);
  assert.equal(clarificationSelection('1, but send it elsewhere', record, prompt), 0);
});

test('selection pins original file context and one successor across two stores and restart', t => {
  const { root, store, conversation } = fixture(t);
  const file = store.saveFile(scope, conversation.id, 'report.md', Buffer.from('original file'));
  const original = store.enqueue(scope, conversation.id, 'read this', [file.id], undefined, undefined, undefined, true);
  const prompt = '1. Read report.md', record = recordFor(store, original, prompt);
  deliver(store, original, prompt, record);
  const other = new ClientStore(root); t.after(() => { try { other.close(); } catch {} });
  const first = store.enqueue(scope, conversation.id, '1', [], undefined, undefined, undefined, true);
  const second = other.enqueue(scope, conversation.id, 'first', [], undefined, undefined, undefined, true);
  assert.equal(first.request_id, second.request_id);
  const next = store.context(scope, first.request_id);
  assert.equal(next.input_files, undefined);
  assert.deepEqual(next.messages[0].files, [{ id: file.id, name: file.name, size: file.size, sha256: file.sha256 }]);
  assert.equal(conversationContextDigest(next.messages.slice(0, 1)), record.context_sha256);
  assert.equal(next.messages[0].id, record.source_message_id);
  assert.equal(new Set(next.messages.map(message => message.id)).size, next.messages.length);
  assert.equal(store.db.prepare('SELECT count(*) AS n FROM clarification_selections').get().n, 1);
  assert.equal(store.read(scope, conversation.id).messages.filter(message => message.role === 'user').length, 2);
  other.close(); const restarted = new ClientStore(root);
  assert.equal(restarted.enqueue(scope, conversation.id, '1', [], undefined, undefined, undefined, true).request_id, first.request_id);
  restarted.close();
});

test('selection intent failure rolls back task and message while keeping the owner draft', t => {
  const { store, conversation } = fixture(t), original = store.enqueue(scope, conversation.id, 'read it');
  const prompt = '1. Read it'; deliver(store, original, prompt, recordFor(store, original, prompt));
  store.saveDraft(scope, conversation.id, '1', [], 0);
  store.db.exec("CREATE TRIGGER fail_selection BEFORE INSERT ON clarification_selections BEGIN SELECT RAISE(ABORT,'selection storage failed'); END;");
  assert.throws(() => store.enqueueDraft(scope, conversation.id, conversation.id, 1), /selection storage failed/);
  assert.equal(store.read(scope, conversation.id).messages.length, 2);
  assert.equal(store.draft(scope, conversation.id).content, '1');
  assert.equal(store.db.prepare('SELECT count(*) AS n FROM clarification_selections').get().n, 0);
});

test('legacy clarification remains readable but cannot become a numbered continuation', t => {
  const { store, conversation } = fixture(t), original = store.enqueue(scope, conversation.id, 'original request');
  deliver(store, original, 'old numbered question');
  const assistant = store.read(scope, conversation.id).messages.at(-1);
  const legacy = { schema_version: 1, id: 'old' };
  store.db.prepare('INSERT INTO message_clarifications VALUES(?,?)').run(assistant.id, JSON.stringify(legacy));
  const next = store.enqueue(scope, conversation.id, '1');
  assert.equal(store.context(scope, next.request_id).messages.at(-2).clarification, undefined);
  assert.equal(store.db.prepare('SELECT record_json FROM message_clarifications').get().record_json, JSON.stringify(legacy));
  assert.equal(store.db.prepare('SELECT count(*) AS n FROM clarification_selections').get().n, 0);
});

test('unavailable clarification files reject selection before queuing and preserve original data', t => {
  const { store, conversation } = fixture(t);
  const file = store.saveFile(scope, conversation.id, 'read.txt', Buffer.from('source'));
  const original = store.enqueue(scope, conversation.id, 'read it', [file.id]);
  const prompt = '1. Read'; deliver(store, original, prompt, recordFor(store, original, prompt));
  fs.unlinkSync(path.join(store.filesRoot, file.id));
  store.saveDraft(scope, conversation.id, '1', [], 0);
  assert.throws(() => store.enqueueDraft(scope, conversation.id, conversation.id, 1, true), /file unavailable/);
  assert.equal(store.draft(scope, conversation.id).content, '1');
  assert.equal(store.read(scope, conversation.id).messages.length, 2);
});

test('reissued questions remain bound to the original owner source and expired choices preserve drafts', t => {
  const { store, conversation } = fixture(t), original = store.enqueue(scope, conversation.id, 'original owner request');
  const firstPrompt = '1. Read it', firstRecord = recordFor(store, original, firstPrompt);
  deliver(store, original, firstPrompt, firstRecord);
  const firstChoice = store.enqueue(scope, conversation.id, '1');
  const renewedPrompt = '1. Read the original request again';
  const renewed = recordFor(store, firstChoice, renewedPrompt, {
    source_message_id: firstRecord.source_message_id, source_text_sha256: firstRecord.source_text_sha256,
    context_sha256: firstRecord.context_sha256,
  });
  deliver(store, firstChoice, renewedPrompt, renewed);
  const secondChoice = store.enqueue(scope, conversation.id, 'first');
  const context = store.context(scope, secondChoice.request_id);
  assert.equal(context.messages.length, 3);
  assert.equal(context.messages[0].id, firstRecord.source_message_id);
  assert.equal(context.messages[0].content, 'original owner request');
  assert.equal(context.messages[1].content, renewedPrompt);
  const expiredPrompt = '1. Expired choice';
  const expired = recordFor(store, secondChoice, expiredPrompt, {
    issued_at: '2026-01-01T00:00:00Z', expires_at: '2026-01-02T00:00:00Z',
    source_message_id: firstRecord.source_message_id, source_text_sha256: firstRecord.source_text_sha256,
    context_sha256: firstRecord.context_sha256,
  });
  deliver(store, secondChoice, expiredPrompt, expired);
  store.saveDraft(scope, conversation.id, '1', [], 0);
  assert.throws(() => store.enqueueDraft(scope, conversation.id, conversation.id, 1), /expired/);
  assert.equal(store.draft(scope, conversation.id).content, '1');
  assert.equal(store.db.prepare('SELECT count(*) AS n FROM clarification_selections').get().n, 2);
});

test('incomplete file groups survive deletion, handle-limit and history-window trimming', t => {
  const { store, conversation } = fixture(t);
  const files = Array.from({ length: 32 }, (_, i) => store.saveFile(scope, conversation.id, `file-${i}.txt`, Buffer.from(`${i}`)));
  store.enqueue(scope, conversation.id, 'first files', files.map(file => file.id));
  const latest = store.saveFile(scope, conversation.id, 'file-31.txt', Buffer.from('new source'));
  store.enqueue(scope, conversation.id, 'new file', [latest.id]);
  const task = store.enqueue(scope, conversation.id, 'read it', [], undefined, undefined, undefined, true);
  let context = store.context(scope, task.request_id);
  assert.equal(context.messages.flatMap(message => message.files ?? []).length, 32);
  assert.equal(context.messages.find(message => message.content === 'first files').files_unavailable, true);
  assert.equal(context.messages.find(message => message.content === 'new file').files_unavailable, true);
  fs.unlinkSync(path.join(store.filesRoot, latest.id));
  const deleted = store.enqueue(scope, conversation.id, 'read the most recent file', [], undefined, undefined, undefined, true);
  context = store.context(scope, deleted.request_id);
  assert.equal(context.messages.find(message => message.content === 'new file').files_unavailable, true);
  assert.equal(context.messages.find(message => message.content === 'new file').files, undefined);
  for (let i = 0; i < 33; i++) store.enqueue(scope, conversation.id, `other request ${i}`);
  const replacement = store.saveFile(scope, conversation.id, 'file-31.txt', Buffer.from('replacement'));
  store.enqueue(scope, conversation.id, 'replacement', [replacement.id]);
  const windowed = store.enqueue(scope, conversation.id, 'read file-31.txt', [], undefined, undefined, undefined, true);
  assert.equal(store.context(scope, windowed.request_id).messages.find(message => message.content === 'replacement').files_unavailable, true);
});

test('v14 to v15 migrates atomically without changing original contexts or unknown upload intents', t => {
  const { root, store, conversation } = fixture(t), original = store.enqueue(scope, conversation.id, 'preserved');
  const before = store.request(scope, original.request_id), installation = store.installationID;
  store.db.prepare('INSERT INTO handle_upload_intents VALUES(?,?,?,?,?)').run(original.request_id, crypto.randomUUID(), 'b'.repeat(64), crypto.randomUUID(), 'unknown');
  const unknown = store.db.prepare('SELECT * FROM handle_upload_intents').get();
  store.db.exec('ALTER TABLE execution_approvals DROP COLUMN input_form_json; ALTER TABLE execution_approvals DROP COLUMN input_values_json; ALTER TABLE execution_approvals DROP COLUMN inputs_digest; DROP TABLE clarification_selections; PRAGMA user_version=14'); store.close();
  const migrated = new ClientStore(root);
  assert.equal(migrated.installationID, installation);
  assert.deepEqual(migrated.request(scope, original.request_id), before);
  assert.deepEqual(migrated.db.prepare('SELECT * FROM handle_upload_intents').get(), unknown);
  assert.equal(migrated.db.prepare('PRAGMA user_version').get().user_version, CLIENT_SCHEMA_VERSION);
  migrated.db.exec("ALTER TABLE execution_approvals DROP COLUMN input_form_json; ALTER TABLE execution_approvals DROP COLUMN input_values_json; ALTER TABLE execution_approvals DROP COLUMN inputs_digest; DROP TABLE clarification_selections; CREATE TABLE clarification_selections(conflict TEXT); INSERT INTO clarification_selections VALUES('preserved'); PRAGMA user_version=14"); migrated.close();
  assert.throws(() => new ClientStore(root), /already exists/);
  const inspect = new DatabaseSync(path.join(root, 'client.sqlite'));
  assert.equal(inspect.prepare('PRAGMA user_version').get().user_version, 14);
  assert.equal(inspect.prepare('SELECT conflict FROM clarification_selections').get().conflict, 'preserved');
  inspect.close();
});
