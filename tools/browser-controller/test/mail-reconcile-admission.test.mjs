import assert from 'node:assert/strict';
import test from 'node:test';
import {localMailFixture, eventually} from './fixtures/local-mail-runtime.mjs';
import {validateManagedSend} from '../mail-runtime/applications/mail/lib/managed-send.mjs';
import {openSendJournal} from '../mail-runtime/applications/mail/lib/send-journal.mjs';
import {createNotSentReceipt} from '../mail-runtime/applications/mail/lib/send-recovery.mjs';

async function fixture(t) {
  let effects = 0, reads = 0, proof;
  const f = await localMailFixture(t);
  const input = {schema_version: 1, operation: 'send', provider: 'outlook', account: 'default',
    account_address: 'owner@example.test', invocation_id: 'original-invocation', mode: 'compose',
    message: {to: ['sink@example.test'], cc: [], subject: 'Synthetic', body: {format: 'text', content: 'Fixture only'}}};
  const key = 'mail-outlook\0send', entry = f.factory.commands.get(key);
  f.factory.commands.set(key, {...entry, handler: {
    async run(input, context) {
      context.beforeEffect(); effects++;
      const request = validateManagedSend(input, 'outlook');
      proof = createNotSentReceipt(request, context, 'EMAIL_ATTACHMENT_UPLOAD_FAILED', 'pre_dispatch_failure');
      const journal = await openSendJournal(f.factory.emailWorkspaceRoot, request);
      assert.equal(await journal.write('not_sent', proof), true);
      throw Object.assign(new Error('synthetic preparation failure'), {code: 'EMAIL_ATTACHMENT_UPLOAD_FAILED'});
    },
    async reconcile(input, context) {reads++; return entry.handler.reconcile(input, context);},
  }});
  const request = f.request('outlook', 'send', input);
  const original = await f.factory.runScript(request);
  assert.equal(original.state, 'failed'); assert.equal(original.result.code, 'send_outcome_unknown');
  const recovery = {...request, input: {...input, mode: 'reconcile'}};
  return {...f, proof, recovery, effectCounts: () => ({effects, reads}),
    journal: () => openSendJournal(f.factory.emailWorkspaceRoot, validateManagedSend(input, 'outlook')),
    blockLane() {
      let release; const pending = new Promise(resolve => {release = resolve;});
      f.factory.lanes.set(`fixture-owner\0${entry.spec.resource}`, pending);
      t.after(release);
      return release;
    }};
}

test('first local reconciliation returns the original durable proof without opening a browser', async t => {
  const f = await fixture(t), before = f.counts.created;
  const result = await f.factory.runScript(f.recovery);
  assert.equal(result.state, 'completed'); assert.equal(result.result.status, 'not_sent');
  assert.deepEqual(result.result, f.proof);
  assert.equal(f.counts.created, before);
  assert.deepEqual((await f.factory.runScript(f.recovery)).result, f.proof);
  assert.equal(f.effectCounts().effects, 1);
});

test('queued local reconciliation joins concurrent callers without reexecuting the original send', async t => {
  const f = await fixture(t), release = f.blockLane();
  const first = f.factory.runScript(f.recovery);
  await eventually(() => f.factory.inflight.size === 1);
  assert.deepEqual(f.effectCounts(), {effects: 1, reads: 0});
  const second = f.factory.runScript(f.recovery);
  release();
  const results = await Promise.all([first, second]);
  assert.equal(results[0].result.status, 'not_sent'); assert.deepEqual(results[1], results[0]);
  assert.deepEqual(f.effectCounts(), {effects: 1, reads: 1});
});

test('cancelling queued local recovery preserves the original proof and cannot dispatch', async t => {
  const f = await fixture(t), release = f.blockLane(), abort = new AbortController();
  const pending = f.factory.runScript({...f.recovery, signal: abort.signal});
  await eventually(() => f.factory.inflight.size === 1);
  abort.abort(); release();
  assert.equal((await pending).state, 'failed');
  assert.deepEqual((await f.journal()).saved.receipt, f.proof);
  assert.deepEqual(f.effectCounts(), {effects: 1, reads: 0});
  assert.deepEqual((await f.factory.runScript(f.recovery)).result, f.proof);
});

test('shutdown fences queued recovery and cold restart recovers original evidence without replay', async t => {
  const f = await fixture(t), release = f.blockLane();
  const pending = f.factory.runScript(f.recovery);
  await eventually(() => f.factory.active.size === 1);
  const closing = f.factory.close();
  await eventually(() => [...f.factory.active].every(task => task.abort.signal.aborted));
  release(); await closing;
  assert.equal((await pending).state, 'failed');
  assert.deepEqual(f.effectCounts(), {effects: 1, reads: 0});
  const restarted = await localMailFixture(t, {root: f.root});
  const result = await restarted.factory.runScript(f.recovery);
  assert.equal(result.state, 'completed'); assert.deepEqual(result.result, f.proof);
  assert.equal(restarted.counts.created, 0);
});

test('failed admission and a lost local completion reply cannot reissue or erase a send outcome', async t => {
  const f = await fixture(t);
  await assert.rejects(f.factory.runScript({...f.recovery, revision: f.recovery.revision + 1}), {code: 'browser_script_unavailable'});
  assert.deepEqual(f.effectCounts(), {effects: 1, reads: 0});
  assert.deepEqual((await f.journal()).saved.receipt, f.proof);
  // Drop the controller reply after completion, as a disconnected caller would.
  await f.factory.runScript(f.recovery);
  const result = await f.factory.runScript(f.recovery);
  assert.equal(result.state, 'completed'); assert.deepEqual(result.result, f.proof);
  assert.equal(f.effectCounts().effects, 1);
});
