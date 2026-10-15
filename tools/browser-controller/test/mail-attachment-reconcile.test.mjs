import assert from 'node:assert/strict';
import test from 'node:test';
import {localMailFixture} from './fixtures/local-mail-runtime.mjs';
import {validateManagedSend} from '../mail-runtime/applications/mail/lib/managed-send.mjs';
import {openSendJournal} from '../mail-runtime/applications/mail/lib/send-journal.mjs';
import {createNotSentReceipt} from '../mail-runtime/applications/mail/lib/send-recovery.mjs';

const file = (index, sha) => ({name: 'same.txt', size_bytes: 8, sha256: 'sha256:' + sha.repeat(64),
  path: `.sparkclaw-mail-send-${'a'.repeat(32)}/0${index}/same.txt`});
const original = () => ({schema_version: 1, operation: 'send', provider: 'qq_mail', account: 'default',
  account_address: 'owner@example.test', invocation_id: 'reviewed-invocation', mode: 'compose',
  message: {to: ['sink@example.test'], cc: [], subject: 'Reviewed', body: {format: 'text', content: 'Body'},
    attachments: [file(0, 'a'), file(1, 'b')]}});

async function harness(t, args = original()) {
  const f = await localMailFixture(t);
  const key = 'mail-qq-mail\0send', entry = f.factory.commands.get(key);
  let effects = 0, receipt;
  f.factory.commands.set(key, {...entry, handler: {...entry.handler, async run(input, context) {
    context.beforeEffect(); effects++;
    const request = validateManagedSend(input, 'qq_mail');
    receipt = createNotSentReceipt(request, context, 'EMAIL_ATTACHMENT_UPLOAD_FAILED', 'pre_dispatch_failure');
    const journal = await openSendJournal(f.factory.emailWorkspaceRoot, request);
    assert.equal(await journal.write('not_sent', receipt), true);
    throw Object.assign(new Error('synthetic upload failure'), {code: 'EMAIL_ATTACHMENT_UPLOAD_FAILED'});
  }}});
  const request = f.request('qq_mail', 'send', args);
  assert.equal((await f.factory.runScript(request)).result.code, 'send_outcome_unknown');
  f.factory.commands.set(key, entry);
  const recovery = {...request, input: {...structuredClone(args), mode: 'reconcile'}};
  for (const attachment of recovery.input.message.attachments ?? []) attachment.path = '';
  return {...f, recovery, receipt, effects: () => effects};
}

test('receipt-only local reconciliation retains original proof after attachment staging expires', async t => {
  const args = original(), before = structuredClone(args), f = await harness(t, args);
  const beforeHosts = f.counts.created;
  const result = await f.factory.runScript(f.recovery);
  assert.equal(result.state, 'completed'); assert.deepEqual(result.result, f.receipt);
  assert.deepEqual(args, before, 'approved input must remain unchanged');
  assert.equal(f.recovery.input.message.attachments[0].path, '');
  assert.equal(f.counts.created, beforeHosts); assert.equal(f.effects(), 1);
});

test('local receipt lookup rejects altered content, identities, attachment order and hidden fields', async t => {
  const changes = {
    hash: r => {r.message.attachments[0].sha256 = 'sha256:' + 'c'.repeat(64);},
    name: r => {r.message.attachments[0].name = 'changed.txt';},
    size: r => {r.message.attachments[0].size_bytes++;},
    order: r => {r.message.attachments.reverse();},
    added: r => {r.message.attachments.push({...file(2, 'c'), path: ''});},
    removed: r => {r.message.attachments.pop();},
    body: r => {r.message.body.content = 'changed';},
    subject: r => {r.message.subject = 'changed';},
    recipient: r => {r.message.to = ['other@example.test'];},
    cc: r => {r.message.cc = ['other@example.test'];},
    account: r => {r.account_address = 'other@example.test';},
    invocation: r => {r.invocation_id = 'other-invocation';},
    reply: r => {r.reply_target = {provider_message_id: 'other'};},
    extra: r => {r.message.attachments[0].content_base64 = 'Zm9yZ2Vk';},
  };
  for (const [name, change] of Object.entries(changes)) await t.test(name, async t => {
    const f = await harness(t), beforeHosts = f.counts.created; change(f.recovery.input);
    await assert.rejects(f.factory.runScript(f.recovery));
    assert.equal(f.counts.created, beforeHosts); assert.equal(f.effects(), 1);
  });
});

test('managed sends without attachments retain their original proof', async t => {
  const args = original(); delete args.message.attachments;
  const f = await harness(t, args);
  assert.deepEqual((await f.factory.runScript(f.recovery)).result, f.receipt);
  assert.equal(f.effects(), 1);
});
