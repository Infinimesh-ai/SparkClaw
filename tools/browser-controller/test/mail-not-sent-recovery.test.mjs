import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import test from 'node:test';
import {localMailFixture} from './fixtures/local-mail-runtime.mjs';
import {sendManagedMail, validateManagedSend} from '../mail-runtime/applications/mail/lib/managed-send.mjs';
import {openSendJournal} from '../mail-runtime/applications/mail/lib/send-journal.mjs';
import {QQ15, createNotSentReceipt, legacyNotSentReceipt} from '../mail-runtime/applications/mail/lib/send-recovery.mjs';

const args = (provider = 'qq_mail') => ({schema_version: 1, operation: 'send', provider, account: 'default',
  account_address: 'owner@example.test', invocation_id: 'reviewed-invocation', mode: 'compose',
  message: {to: ['sink@example.test'], cc: [], subject: 'Reviewed', body: {format: 'text', content: 'Body'}, attachments: [
    {name: 'file.txt', size_bytes: 1, sha256: 'sha256:' + 'a'.repeat(64), path: `.sparkclaw-mail-send-${'a'.repeat(32)}/00/file.txt`}]}});

async function fixture(t, {provider = 'qq_mail', reason = 'EMAIL_ATTACHMENT_UPLOAD_UNVERIFIED', proof = false,
  prepareFailure = false, releaseFailure = false} = {}) {
  const f = await localMailFixture(t);
  const input = args(provider), key = `mail-${provider.replace('_', '-')}\0send`, entry = f.factory.commands.get(key);
  let effects = 0, prepareCalls = 0, releaseCalls = 0, originalEvidence;
  f.factory.commands.set(key, {...entry, handler: {...entry.handler, async run(input, context) {
    context.beforeEffect(); effects++; originalEvidence = context.executionEvidence;
    if (releaseFailure) {
      const request = validateManagedSend(input, provider), journal = await openSendJournal(f.factory.emailWorkspaceRoot, request);
      const receipt = {schema_version: 1, provider, status: 'sent', recipient_digest: request.recipientDigest};
      assert.equal(await journal.write('dispatching'), true); assert.equal(await journal.write('sent', receipt), true);
      return {data: receipt};
    }
    if (proof) await sendManagedMail(input, provider, {emailWorkspaceRoot: f.factory.emailWorkspaceRoot,
      notSentReceipt: (request, error) => createNotSentReceipt(request, context, error, 'pre_dispatch_failure'),
      withSendTab: async callback => {
        const failure = () => {throw Object.assign(new Error(reason), {code: reason.toLowerCase()});};
        if (!prepareFailure) return failure();
        let reads = 0;
        return callback({async runReadCode() {return true;}, async inspect() {
          if (++reads === 1) return {result: {ids: []}};
          prepareCalls++; return failure();
        }, async click() {assert.fail('preparation failure must never click Send');}});
      },
    }).catch(error => {throw Object.assign(error, {code: error.code.toUpperCase()});});
    throw Object.assign(new Error(reason), {code: reason});
  }}});
  if (releaseFailure) f.factory.hostClient.acquire = async () => ({async release() {
    if (++releaseCalls === 1) throw Object.assign(new Error(reason), {code: reason});
  }});
  const request = f.request(provider, 'send', input);
  const result = await f.factory.runScript(request);
  assert.equal(result.task.status, 'uncertain');
  // Recovery must always use the production handler, including after a restart.
  f.factory.commands.set(key, entry);
  let current = f.factory;
  return {...f, input, originalEvidence,
    reconcile: () => current.runScript({...request, input: {...input, mode: 'reconcile'}}),
    async restart() {await current.close(); current = (await localMailFixture(t, {root: f.root})).factory;},
    journal: () => openSendJournal(f.factory.emailWorkspaceRoot, validateManagedSend(input, provider)),
    boundaryCalls: () => ({prepareCalls, releaseCalls}), effects: () => effects};
}

test('legacy negative evidence stays limited to the exact QQ binding and terminal phase', () => {
  const request = validateManagedSend(args(), 'qq_mail');
  const context = {executionEvidence: {task_id: 'legacy-task', intent_digest: 'a'.repeat(64), resource_digest: 'b'.repeat(64),
    binding_digest: QQ15, ledger_epoch: 1}, reconciliation: {status: 'uncertain', effect: 1, reason: 'EMAIL_ATTACHMENT_UPLOAD_UNVERIFIED'}};
  const receipt = legacyNotSentReceipt(request, context);
  assert.equal(receipt.not_sent.kind, 'legacy_15_pre_dispatch_failure');
  for (const reason of ['SEND_OUTCOME_UNKNOWN', 'EXECUTOR_RESTARTED', 'HOST_CLEANUP_FAILED', 'BROWSER_EXTENSION_UNAVAILABLE',
    'EMAIL_ATTACHMENT_CONTROL_UNAVAILABLE', 'EMAIL_DRAFT_FIELDS_UNVERIFIED']) {
    assert.equal(legacyNotSentReceipt(request, {...context, reconciliation: {...context.reconciliation, reason}}), null);
  }
  assert.equal(legacyNotSentReceipt(request, {...context, executionEvidence: {...context.executionEvidence, binding_digest: 'c'.repeat(64)}}), null);
  assert.equal(legacyNotSentReceipt(validateManagedSend(args('outlook'), 'outlook'), context), null);
  assert.equal(legacyNotSentReceipt(request, {...context, reconciliation: {...context.reconciliation, effect: 0}}), null);
});

for (const reason of ['SEND_OUTCOME_UNKNOWN', 'HOST_CLEANUP_FAILED', 'EMAIL_ATTACHMENT_UPLOAD_UNVERIFIED',
  'BROWSER_EXTENSION_UNAVAILABLE', 'EMAIL_ATTACHMENT_CONTROL_UNAVAILABLE', 'EMAIL_DRAFT_FIELDS_UNVERIFIED']) {
  test(`missing journal remains uncertain after restart for ${reason}`, async t => {
    const f = await fixture(t, {reason});
    assert.equal((await f.journal()).saved, null);
    await f.restart();
    assert.equal((await f.reconcile()).result.code, 'send_outcome_unknown');
    assert.equal((await f.journal()).saved, null); assert.equal(f.effects(), 1);
  });
}

test('durable pre-dispatch proof survives a lost failure reply and cannot be replaced by dispatch', async t => {
  const f = await fixture(t, {proof: true});
  const persisted = (await f.journal()).saved;
  assert.equal(persisted.stage, 'not_sent'); assert.equal(persisted.receipt.not_sent.kind, 'pre_dispatch_failure');
  assert.equal(await (await f.journal()).write('dispatching'), false);
  await f.restart();
  const recovered = await f.reconcile();
  assert.equal(recovered.state, 'completed'); assert.deepEqual(recovered.result, persisted.receipt);
  assert.deepEqual((await f.reconcile()).result, persisted.receipt); assert.equal(f.effects(), 1);
});

test('Outlook draft preparation failure persists original evidence before browser-free recovery', async t => {
  const f = await fixture(t, {provider: 'outlook', proof: true, prepareFailure: true, reason: 'BROWSER_EXTENSION_UNAVAILABLE'});
  const persisted = (await f.journal()).saved;
  assert.equal(persisted.stage, 'not_sent'); assert.ok(f.boundaryCalls().prepareCalls > 0);
  assert.equal(persisted.receipt.not_sent.reason, 'BROWSER_EXTENSION_UNAVAILABLE');
  for (const [key, value] of Object.entries(f.originalEvidence)) assert.equal(persisted.receipt.not_sent[key], value);
  assert.equal(await (await f.journal()).write('dispatching'), false);
  await f.restart();
  assert.deepEqual((await f.reconcile()).result, persisted.receipt); assert.equal(f.effects(), 1);
});

test('post-dispatch release failure is never converted into a negative receipt', async t => {
  for (const loseJournal of [false, true]) await t.test(loseJournal ? 'independent journal loss' : 'durable sent receipt', async t => {
    const f = await fixture(t, {provider: 'outlook', releaseFailure: true, reason: 'BROWSER_EXTENSION_UNAVAILABLE'});
    assert.equal(f.boundaryCalls().releaseCalls, 2); assert.equal((await f.journal()).saved.stage, 'sent');
    if (loseJournal) await fs.rm(path.join(f.factory.emailWorkspaceRoot, 'email-send'), {recursive: true});
    const result = await f.reconcile();
    if (loseJournal) {
      assert.equal(result.result.code, 'send_outcome_unknown'); assert.equal((await f.journal()).saved, null);
    } else {
      assert.equal(result.state, 'completed'); assert.equal(result.result.status, 'sent');
      assert.equal(result.result.not_sent, undefined); assert.equal((await f.journal()).saved.stage, 'sent');
    }
    assert.equal(f.effects(), 1);
  });
});

test('negative and dispatch claims are mutually exclusive across concurrent journal readers', async t => {
  const f = await fixture(t), first = await f.journal(), second = await f.journal();
  const proof = createNotSentReceipt(validateManagedSend(f.input, 'qq_mail'), {executionEvidence: f.originalEvidence},
    'EMAIL_ATTACHMENT_UPLOAD_UNVERIFIED', 'pre_dispatch_failure');
  const wins = await Promise.all([first.write('not_sent', proof), second.write('dispatching')]);
  assert.equal(wins.filter(Boolean).length, 1);
  const stage = (await f.journal()).saved.stage, result = await f.reconcile();
  assert.equal(result.state, stage === 'not_sent' ? 'completed' : 'failed');
  assert.equal(f.effects(), 1);
});
