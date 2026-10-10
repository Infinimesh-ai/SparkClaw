import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { ClientStore, CLIENT_SCHEMA_VERSION } from '../src/main/client-store.mjs';
import { ClientStoreCapability } from '../src/main/client-store-capability.mjs';
import { ScheduleClient } from '../src/main/schedule-client.mjs';
import { calendarMissed, nextCalendarTime, parseScheduleRequest, recurrenceSpec, zonedTime } from '../src/main/local-schedule-time.mjs';
const scope = { deployment_id: 'deployment', owner_id: 'owner', client_id: 'client' };
const hash = data => crypto.createHash('sha256').update(data).digest('hex');
function fixture(t) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'sparkclaw-original-ui-'));
  let store = new ClientStore(root);
  t.after(() => { store.close(); fs.rmSync(root, { recursive: true, force: true }); });
  return { root, get store() { return store; }, restart() { store.close(); store = new ClientStore(root); return store; } };
}

test('rename changes only the owned title and survives restart without touching drafts, requests or files', t => {
  const f = fixture(t), conversation = f.store.create(scope, 'Original');
  const file = f.store.saveFile(scope, conversation.id, 'input.txt', Buffer.from('source'));
  f.store.saveDraft(scope, conversation.id, 'unsent text', [file.id], 0);
  const task = f.store.enqueue(scope, conversation.id, 'execute later', [file.id]);
  const original = f.store.read(scope, conversation.id), snapshot = f.store.request(scope, task.request_id).context_json;
  assert.throws(() => f.store.rename({ ...scope, owner_id: 'other' }, conversation.id, 'Wrong owner'), /not found/);
  assert.throws(() => f.store.rename(scope, conversation.id, '  '), /empty/);
  f.store.rename(scope, conversation.id, '  Restored title  '); f.restart();
  assert.equal(f.store.list(scope)[0].title, 'Restored title');
  assert.deepEqual(f.store.read(scope, conversation.id), original);
  assert.equal(f.store.request(scope, task.request_id).context_json, snapshot);
  assert.equal(f.store.draft(scope, conversation.id).content, 'unsent text');
  assert.equal(f.store.file(scope, file.id).content.toString(), 'source');
});

test('input and output attachments retain their exact message association through restart and schema 8 upgrade', t => {
  const f = fixture(t), conversation = f.store.create(scope, 'Attachments');
  const input = f.store.saveFile(scope, conversation.id, 'input.png', Buffer.from('input bytes'));
  const task = f.store.enqueue(scope, conversation.id, 'task one', [input.id]);
  f.store.markSubmitted(scope, task.request_id);
  f.store.enqueue(scope, conversation.id, 'task two');
  const bytes = Buffer.from('output bytes'), manifest = { id: 'out', name: 'output.png', size: bytes.length, sha256: hash(bytes) };
  const payload = JSON.stringify({ content: 'result one', files: [manifest] });
  f.store.commitDelivery(scope, { request_id: task.request_id, sequence: 1, digest: hash(payload), payload }, new Map([['out', bytes]]));
  const original = f.store.read(scope, conversation.id);
  assert.equal(original.messages[0].attachments[0].content_type, 'image/png');
  assert.equal(original.messages[1].attachments.length, 0);
  assert.equal(original.messages[2].attachments[0].name, 'output.png');
  const installation = f.store.installationID;
  f.store.db.exec('DROP TABLE message_files; ALTER TABLE conversations DROP COLUMN hidden; ALTER TABLE schedule_definitions DROP COLUMN calendar; PRAGMA user_version=8;');
  f.restart();
  assert.equal(f.store.db.prepare('PRAGMA user_version').get().user_version, CLIENT_SCHEMA_VERSION);
  assert.equal(f.store.installationID, installation);
  assert.deepEqual(f.store.read(scope, conversation.id), original);
  assert.equal(f.store.file(scope, original.messages[2].attachments[0].artifact_id).content.toString(), 'output bytes');
});

test('original natural schedule entry creates local definitions globally without a new sidebar conversation', t => {
  const f = fixture(t), now = Date.parse('2026-10-10T10:00:00Z');
  f.store.createScheduleRequest(scope, '每个工作日早上 9 点整理昨天的项目进展，并把摘要发给我', 'Asia/Shanghai', now);
  f.store.createScheduleRequest(scope, 'Every 1 hour, check the project', 'UTC', now);
  assert.equal(f.store.list(scope).length, 0);
  const schedules = f.store.listSchedules(scope);
  assert.equal(schedules.length, 2);
  assert.equal(schedules[1].text, '整理昨天的项目进展，并把摘要发给我');
  assert.equal(schedules[1].due_time, '2026-10-12T01:00:00.000Z');
  assert.equal(schedules[1].recurrence, 'weekdays');
  assert.deepEqual(f.store.listSchedules({ ...scope, owner_id: 'other' }), []);
  const conversations = f.store.db.prepare('SELECT count(*) AS n FROM conversations').get().n;
  assert.throws(() => f.store.createScheduleRequest(scope, 'Make a report', 'UTC', now), /execution time/);
  assert.throws(() => f.store.createScheduleRequest(scope, '2026-10-01 09:00 make a report', 'UTC', now), /366 days/);
  assert.equal(f.store.db.prepare('SELECT count(*) AS n FROM conversations').get().n, conversations);
  f.restart(); assert.deepEqual(f.store.listSchedules(scope), schedules);
});

test('schedule edit replaces only an unclaimed definition atomically and rejects stale or admitted snapshots', t => {
  const f = fixture(t), now = Date.parse('2026-10-10T10:00:00Z');
  const original = f.store.createScheduleRequest(scope, 'Tomorrow at 9 AM, summarize progress', 'UTC', now);
  const first = f.store.listSchedules(scope)[0], oldSnapshot = f.store.request(scope, original.request_id).context_json;
  const draft = { text: 'Updated progress', dueTime: '2026-10-12T09:00', timezone: 'UTC', recurrence: 'every 1 hour' };
  assert.throws(() => f.store.editSchedule(scope, first.id, 'stale', draft, now), /changed/);
  f.store.db.exec("CREATE TRIGGER fail_new_schedule BEFORE INSERT ON schedules BEGIN SELECT RAISE(ABORT,'disk unavailable'); END;");
  assert.throws(() => f.store.editSchedule(scope, first.id, first.updated_at, draft, now), /disk unavailable/);
  assert.deepEqual(f.store.listSchedules(scope), [first]);
  f.store.db.exec('DROP TRIGGER fail_new_schedule;');
  const replacement = f.store.editSchedule(scope, first.id, first.updated_at, draft, now);
  assert.notEqual(replacement.request_id, first.id);
  assert.equal(f.store.scheduledRequest(scope, first.id).state, 'canceled');
  assert.equal(f.store.request(scope, first.id).context_json, oldSnapshot);
  assert.equal(f.store.listSchedules(scope)[0].text, draft.text);
  assert.equal(replacement.interval_ms, 3600000);
  const view = f.store.listSchedules(scope)[0];
  f.store.claimSchedule(scope, replacement.request_id, Date.parse(replacement.due_at));
  assert.throws(() => f.store.editSchedule(scope, replacement.request_id, view.updated_at, draft, now), /changed|executing/);
  assert.equal(f.store.listSchedules(scope).find(row => row.id === replacement.request_id).editable, false);
});

test('calendar schedules skip missed offline rounds and dispatch the next distinct occurrence once through the ordinary execution client', async t => {
  const f = fixture(t); let now = Date.parse('2026-10-09T07:00:00Z');
  const auth = { generation: 1, status: { state: 'connected' } }, calls = [];
  const execution = { submit: async (owned, id, options) => { options.canSubmit(); f.store.markSubmitted(owned, id, options.scheduleClaim); calls.push(id); }, reconcile: async () => {}, cancel: async () => {} };
  const client = new ScheduleClient({ auth, store: f.store, execution, getIdentity: () => scope, now: () => now, intervalMS: 999999 });
  t.after(() => client.close());
  const first = client.createRequest(scope, 'Every weekday at 9 AM, summarize progress', 'UTC');
  now = Date.parse('2026-10-12T10:00:00Z'); client.start();
  await client.reconcile(scope, first.request_id);
  assert.equal(f.store.scheduledRequest(scope, first.request_id).missed_count, 2);
  assert.equal(calls.length, 0);
  const future = f.store.listSchedules(scope).find(row => row.status === 'pending');
  assert.equal(future.due_time, '2026-10-13T09:00:00.000Z');
  now = Date.parse(future.due_time);
  await Promise.all([client.reconcile(scope, future.id), client.reconcile(scope, future.id)]);
  assert.deepEqual(calls, [future.id]);
  assert.notEqual(future.id, first.request_id);
  assert.equal(f.store.listSchedules(scope).find(row => row.status === 'pending').due_time, '2026-10-14T09:00:00.000Z');
  const payload = JSON.stringify({ content: 'completed round', files: [] }), digest = hash(payload);
  f.store.commitDelivery(scope, { request_id: future.id, sequence: 1, digest, payload }, new Map());
  assert.equal(f.store.listSchedules(scope).find(row => row.id === future.id).local_state, 'completed');
  f.store.acknowledge(scope, future.id, 1, digest);
  assert.equal(f.store.listSchedules(scope).some(row => row.id === future.id), false);
  assert.equal(f.store.listSchedules(scope).filter(row => row.status === 'pending').length, 1);
});

test('temporal input preserves task content, validates missing times and honors timezone, month-end and DST', () => {
  const now = Date.parse('2026-10-10T10:00:00Z');
  assert.equal(parseScheduleRequest('Tomorrow at 9 AM, summarize the last 3 days', 'UTC', now).content, 'summarize the last 3 days');
  assert.equal(parseScheduleRequest('At 9 AM summarize tomorrow’s plans every weekday', 'UTC', now).dueAt, '2026-10-11T09:00:00.000Z');
  assert.equal(parseScheduleRequest('十分钟后整理进展', 'UTC', now).dueAt, '2026-10-10T10:10:00.000Z');
  assert.throws(() => parseScheduleRequest('每月32号早上9点整理进展', 'UTC', now), /between 1 and 31/);
  assert.throws(() => zonedTime('2026-02-30T09:00', 'UTC'), /does not exist/);
  assert.throws(() => zonedTime('2026-10-11T09:00', 'Bad/Timezone'), /time zone/i);
  const monthly = recurrenceSpec('monthly', 'UTC', '2027-01-31T09:00:00Z').calendar;
  const february = nextCalendarTime('2027-01-31T09:00:00Z', monthly);
  assert.equal(february, '2027-02-28T09:00:00.000Z');
  assert.equal(nextCalendarTime(february, monthly), '2027-03-31T09:00:00.000Z');
  const daily = recurrenceSpec('daily', 'America/New_York', '2027-03-13T07:30:00Z').calendar;
  const gap = nextCalendarTime('2027-03-13T07:30:00Z', daily);
  assert.equal(gap, '2027-03-14T07:00:00.000Z');
  assert.equal(nextCalendarTime(gap, daily), '2027-03-15T06:30:00.000Z');
  const missed = calendarMissed('2026-10-09T09:00:00Z', recurrenceSpec('weekdays', 'UTC', '2026-10-09T09:00:00Z').calendar, Date.parse('2026-10-12T10:00:00Z'));
  assert.equal(missed.count, 2); assert.equal(missed.nextDue, '2026-10-13T09:00:00.000Z');
});

test('new presentation IPC accepts only owned identifiers and verified bytes, and cannot inject renderer paths or schedule authority', async t => {
  const f = fixture(t), conversation = f.store.create(scope, 'UI');
  const file = f.store.saveFile(scope, conversation.id, 'image.png', Buffer.from('verified bytes'));
  const frame = { url: 'sparkclaw-app://workbench/index.html' }, webContents = { mainFrame: frame };
  let enabled = true;
  const schedules = new ScheduleClient({ store: f.store, auth: { generation: 1 }, getIdentity: () => scope });
  const capability = new ClientStoreCapability({ window: { webContents }, store: f.store, schedules,
    getIdentity: () => scope, getCapabilities: () => ({ files: enabled }) });
  const event = { sender: webContents, senderFrame: frame }, read = { schema_version: 1, operation: 'readFile', file_id: file.id };
  assert.equal(new TextDecoder().decode((await capability.dispatch(event, read)).bytes), 'verified bytes');
  await assert.rejects(capability.dispatch(event, { ...read, path: '/tmp/secret' }), /fields/);
  await assert.rejects(capability.dispatch(event, { schema_version: 1, operation: 'rename', conversation_id: conversation.id, title: 'New', owner_id: 'other' }), /fields/);
  await assert.rejects(capability.dispatch(event, { schema_version: 1, operation: 'editSchedule', request_id: 'none', expected_version: 'none', draft: { text: 'x', dueTime: 'x', timezone: 'UTC', recurrence: 'none', submission_claim: 'injected' } }), /fields/);
  enabled = false; await assert.rejects(capability.dispatch(event, read), /unavailable/);
  enabled = true; fs.writeFileSync(path.join(f.root, 'files', file.id), 'corrupted');
  await assert.rejects(capability.dispatch(event, read), /verification|size|length/i);
});
