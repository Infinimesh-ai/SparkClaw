import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { ClientStore, parseResultPayload } from '../src/main/client-store.mjs';
import { ClientStoreCapability } from '../src/main/client-store-capability.mjs';
import { ExecutionClient } from '../src/main/execution-client.mjs';
import { filterLocalSchedules } from '../src/main/schedule-presentation.mjs';

const scope = { deployment_id: 'schedule-test', owner_id: 'owner', client_id: 'client' };
const hash = value => crypto.createHash('sha256').update(value).digest('hex');
function fixture(t) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'schedule-presentation-'));
  let store = new ClientStore(root);
  t.after(() => { store.close(); fs.rmSync(root, { recursive: true, force: true }); });
  const conversation = store.create(scope, 'Original conversation');
  const queued = store.enqueue(scope, conversation.id, '显示明天的日程');
  const task = store.request(scope, queued.request_id);
  const context = JSON.parse(task.context_json);
  const action = { schema_version: 1, type: 'schedule.show', action_id: task.request_id, request_id: task.request_id,
    local_conversation_id: conversation.id, timezone: context.client_presentation.timezone,
    date_start: '', date_end: '', expires_at: new Date(Date.now() + 300000).toISOString() };
  const payload = JSON.stringify({ content: '正在打开本机日程。', files: [], client_action: action });
  const delivery = { request_id: task.request_id, sequence: 1, digest: hash(payload), payload };
  store.markSubmitted(scope, task.request_id);
  return { get store() { return store; }, conversation, task, context, action, delivery,
    restart() { store.close(); store = new ClientStore(root); },
    deliver(value = action) { const payload = JSON.stringify({ content: '正在打开本机日程。', files: [], client_action: value });
      return store.commitDelivery(scope, { ...delivery, payload, digest: hash(payload) }, new Map()); } };
}

test('original input and presentation capability enter the immutable backend envelope; action delivery is atomic and replay stable', t => {
  const f = fixture(t);
  assert.equal(f.context.messages.at(-1).content, '显示明天的日程');
  assert.equal(f.context.client_presentation.schedule_show, true);
  f.deliver(); f.deliver();
  assert.equal(f.store.read(scope, f.conversation.id).messages.length, 2);
  assert.equal(f.store.read(scope, f.conversation.id).client_actions.length, 1);
  assert.deepEqual(f.store.clientActions.claim(scope, f.task.request_id, f.conversation.id), f.action);
  assert.equal(f.store.clientActions.claim(scope, f.task.request_id, f.conversation.id), null);
  assert.throws(() => f.store.clientActions.claim({ ...scope, owner_id: 'another' }, f.task.request_id, f.conversation.id), /not found/);
  f.store.clientActions.finish(scope, f.task.request_id, 'shown');
  f.store.clientActions.finish(scope, f.task.request_id, 'shown');
  assert.throws(() => f.store.clientActions.finish(scope, f.task.request_id, 'failed'), /conflicts/);
  f.restart();
  assert.equal(f.store.clientActions.claim(scope, f.task.request_id, f.conversation.id), null);
  assert.deepEqual(f.store.clientActions.pending(scope), [{ action_id: f.task.request_id, status: 'shown' }]);
});

test('unnegotiated, cross-request, cross-conversation and malformed actions do not commit a message or receipt', t => {
  const f = fixture(t);
  for (const action of [null, { ...f.action, type: 'schedule.delete' }, { ...f.action, action_id: crypto.randomUUID() },
    { ...f.action, local_conversation_id: crypto.randomUUID() }, { ...f.action, timezone: 'Bad/Zone' },
    { ...f.action, date_start: '2026-02-30', date_end: '2026-03-01' }, { ...f.action, extra: 'ignored?' }]) {
    assert.throws(() => f.deliver(action));
    assert.equal(f.store.receipt(scope, f.task.request_id), null);
    assert.equal(f.store.read(scope, f.conversation.id).messages.length, 1);
  }
  const context = { ...f.context }; delete context.client_presentation;
  const raw = JSON.stringify(context);
  f.store.db.prepare('UPDATE tasks SET context_json=?,input_digest=? WHERE request_id=?').run(raw, hash(raw), f.task.request_id);
  assert.throws(() => f.deliver(), /original request/);
  assert.throws(() => parseResultPayload(JSON.stringify({ content: 'text', files: [], client_action: null })));
});

test('restart after a claim and expired actions cannot navigate again or claim success', t => {
  const f = fixture(t); f.deliver();
  assert.throws(() => f.store.clientActions.finish(scope, f.task.request_id, 'shown'), /claim/);
  f.store.clientActions.claim(scope, f.task.request_id, f.conversation.id); f.restart();
  assert.deepEqual(f.store.clientActions.pending(scope), [{ action_id: f.task.request_id, status: 'skipped' }]);
  assert.equal(f.store.clientActions.claim(scope, f.task.request_id, f.conversation.id), null);
  const other = fixture(t); other.deliver();
  assert.equal(other.store.clientActions.claim(scope, other.task.request_id, other.conversation.id, Date.now() + 600000), null);
  assert.equal(other.store.clientActions.pending(scope)[0].status, 'skipped');
});

test('date filter uses the requested local calendar boundary including DST and an empty range means all', () => {
  const rows = ['2026-10-29T15:59:59Z', '2026-10-29T16:00:00Z', '2026-10-30T15:59:59Z', '2026-10-30T16:00:00Z'].map(due_time => ({ due_time }));
  assert.deepEqual(filterLocalSchedules(rows, { date_start: '2026-10-30', date_end: '2026-10-31', timezone: 'Asia/Shanghai' }), rows.slice(1, 3));
  assert.equal(filterLocalSchedules(rows, { date_start: '', date_end: '', timezone: 'UTC' }), rows);
  const dst = ['2026-11-01T03:59:59Z', '2026-11-01T04:00:00Z', '2026-11-02T04:59:59Z', '2026-11-02T05:00:00Z'].map(due_time => ({ due_time }));
  assert.deepEqual(filterLocalSchedules(dst, { date_start: '2026-11-01', date_end: '2026-11-02', timezone: 'America/New_York' }), dst.slice(1, 3));
});

test('native claim reads local schedules, enforces scope and records read failure without reporting shown', async t => {
  const f = fixture(t); f.deliver();
  const frame = { url: 'sparkclaw-app://workbench/index.html' }, webContents = { mainFrame: frame };
  const event = { sender: webContents, senderFrame: frame };
  const capability = new ClientStoreCapability({ window: { webContents }, getIdentity: () => scope,
    store: f.store, schedules: { list() { throw new Error('local disk read failed'); } } });
  const request = { schema_version: 1, operation: 'claimSchedulePresentation', request_id: f.task.request_id,
    conversation_id: f.conversation.id, expected_scope: hash(JSON.stringify(scope)), view_id: f.store.clientActions.beginView(scope).view_id };
  await assert.rejects(capability.dispatch(event, { ...request, expected_scope: 'other' }), /authentication changed/);
  await assert.rejects(capability.dispatch(event, { ...request, conversation_id: crypto.randomUUID() }), /conversation changed/);
  await assert.rejects(capability.dispatch(event, request), /local disk read failed/);
  assert.equal(f.store.clientActions.pending(scope)[0].status, 'failed');
  assert.equal(await capability.dispatch(event, request), null);
});

test('presentation receipt survives a lost ACK and restart, retries only the original ACK and never resubmits input', async t => {
  const f = fixture(t); f.deliver(); f.store.clientActions.claim(scope, f.task.request_id, f.conversation.id);
  f.store.clientActions.finish(scope, f.task.request_id, 'shown');
  const calls = []; let fail = true;
  const auth = { generation: 1, status: { state: 'connected' }, descriptor: { origin: 'https://backend.invalid' },
    authorizedExecutionFetch: async (url, init) => { calls.push({ url, body: JSON.parse(init.body) });
      if (fail) throw new Error('lost response'); return new Response(JSON.stringify({ state: 'delivered' })); } };
  let client = new ExecutionClient({ auth, store: f.store, getIdentity: () => scope });
  await client.flushPresentationReceipts(scope); client.close(); f.restart();
  assert.equal(f.store.clientActions.pending(scope).length, 1);
  fail = false; client = new ExecutionClient({ auth, store: f.store, getIdentity: () => scope });
  await client.flushPresentationReceipts(scope); client.close();
  assert.equal(f.store.clientActions.pending(scope).length, 0);
  assert.equal(calls.length, 2);
  for (const call of calls) {
    assert.equal(call.url, `https://backend.invalid/api/v1/executions/${f.task.request_id}/ack`);
    assert.deepEqual(call.body.client_action_receipt, { action_id: f.task.request_id, status: 'shown' });
  }
  assert.equal(f.store.read(scope, f.conversation.id).tasks.length, 1);
});

test('v12 migration preserves installation, original contexts, history and drafts', t => {
  const f = fixture(t); const installation = f.store.installationID;
  f.store.saveDraft(scope, f.conversation.id, 'Unsent draft', [], 0);
  const before = f.store.read(scope, f.conversation.id);
  f.store.db.exec('DROP TABLE client_actions; PRAGMA user_version=12;'); f.restart();
  assert.equal(f.store.installationID, installation);
  assert.deepEqual(f.store.read(scope, f.conversation.id), before);
  assert.equal(f.store.request(scope, f.task.request_id).context_json, f.task.context_json);
  assert.equal(f.store.draft(scope, f.conversation.id).content, 'Unsent draft');
});

test('renderer reload abandons an uncertain claim and fences late callbacks from its previous view', t => {
  const f = fixture(t); f.deliver();
  const old = f.store.clientActions.beginView(scope);
  f.store.clientActions.verifyView(scope, old.view_id);
  f.store.clientActions.claim(scope, f.task.request_id, f.conversation.id);
  const next = f.store.clientActions.beginView(scope);
  assert.notEqual(next.view_id, old.view_id);
  assert.throws(() => f.store.clientActions.verifyView(scope, old.view_id), /view changed/);
  assert.equal(f.store.clientActions.pending(scope)[0].status, 'skipped');
  assert.equal(f.store.clientActions.claim(scope, f.task.request_id, f.conversation.id), null);
});

test('pending background results expire without reopening history, and startup abandons undelivered navigation', t => {
  const f = fixture(t); f.deliver();
  assert.equal(f.store.clientActions.pending(scope).length, 0);
  assert.equal(f.store.clientActions.pending(scope, Date.now() + 600000)[0].status, 'skipped');
  const other = fixture(t); other.deliver(); other.restart();
  assert.equal(other.store.clientActions.pending(scope)[0].status, 'skipped');
});
