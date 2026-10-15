import assert from 'node:assert/strict';
import path from 'node:path';
import {pathToFileURL} from 'node:url';
import test from 'node:test';

const runtime = process.env.APP_CLI_RECOVERY_RUNTIME_ROOT
  ? pathToFileURL(path.resolve(process.env.APP_CLI_RECOVERY_RUNTIME_ROOT) + path.sep)
  : new URL('../', import.meta.resolve('@infinimesh/app-cli-runtime/release'));
const {MailboxClient} = await import(new URL('applications/mail/client.mjs', runtime));
const {digest} = await import(new URL('src/protocol.mjs', runtime));
const file = (index, sha) => ({name:'same.txt',size_bytes:8,sha256:'sha256:'+sha.repeat(64),
  path:`.sparkclaw-mail-send-${'a'.repeat(32)}/0${index}/same.txt`});
const original = () => ({schema_version:1,operation:'send',provider:'qq_mail',account:'default',
  account_address:'owner@example.test',invocation_id:'reviewed-invocation',mode:'compose',
  message:{to:['sink@example.test'],cc:[],subject:'Reviewed',body:{format:'text',content:'Body'},
    attachments:[file(0,'a'),file(1,'b')]}});
function harness(args = original(), status = 'uncertain') {
  const calls = [], admission = {request:{arguments:args},existing:true};
  const completed = {kind:'task',task:{id:'original-task',status:'completed'},data:{status:'sent'}};
  const mailbox = Object.create(MailboxClient.prototype);
  mailbox.config = {owner_id:'owner'};
  mailbox.describe = () => ({binding:{manifest:{id:'mail.qq_mail'}},command:'send',
    spec:{script_id:'qq-send',revision:1,source_checksum:'fixture'}});
  mailbox.client = {
    restore(identity) {calls.push(['restore',identity]);return admission;},
    async control(value, op) {assert.equal(value,admission);calls.push([op]);return {kind:'task',task:{id:'original-task',status}};},
    refresh(value, id, op) {assert.equal(value,admission);assert.equal(id,'original-task');calls.push([op]);return {admission:value,acknowledged:Promise.resolve(completed)};},
    async wait(value, response) {assert.equal(value,admission);return response;},
    authorize() {assert.fail('reconcile must not admit a new send');},
    invoke() {assert.fail('reconcile must not invoke or reupload');},
  };
  const request = {operation:'send',provider:'qq_mail',taskID:args.invocation_id,scriptID:'qq-send',revision:1,
    input:{...structuredClone(args),mode:'reconcile'}};
  for(const item of request.input.message.attachments ?? []) item.path='';
  return {mailbox,request,calls,args};
}

test('receipt-only reconciliation restores the original task after attachment staging expires', async () => {
  const {mailbox,request,calls,args} = harness(), before = structuredClone(args);
  const result = await mailbox.execute(request);
  assert.equal(result.state,'completed');
  assert.deepEqual(calls.map(value=>value[0]),['restore','lookup','reconcile']);
  assert.equal(calls[0][1].request_key,digest({taskID:args.invocation_id,app:'mail.qq_mail',command:'send'}));
  assert.deepEqual(args,before,'the original signed intent must remain unchanged');
  assert.equal(request.input.message.attachments[0].path,'');
});

test('receipt lookup rejects changed approved content, identities, attachment order and hidden fields', async t => {
  const changes = {
    hash:r=>{r.message.attachments[0].sha256='sha256:'+'c'.repeat(64);},
    name:r=>{r.message.attachments[0].name='changed.txt';},
    size:r=>{r.message.attachments[0].size_bytes++;},
    order:r=>{r.message.attachments.reverse();},
    added:r=>{r.message.attachments.push({...file(2,'c'),path:''});},
    removed:r=>{r.message.attachments.pop();},
    body:r=>{r.message.body.content='changed';},
    subject:r=>{r.message.subject='changed';},
    recipient:r=>{r.message.to=['other@example.test'];},
    cc:r=>{r.message.cc=['other@example.test'];},
    account:r=>{r.account_address='other@example.test';},
    invocation:r=>{r.invocation_id='other-invocation';},
    reply:r=>{r.reply_target={provider_message_id:'other'};},
    extra:r=>{r.message.attachments[0].content_base64='Zm9yZ2Vk';},
  };
  for (const [name,change] of Object.entries(changes)) await t.test(name,async()=>{
    const {mailbox,request,calls} = harness();change(request.input);
    await assert.rejects(mailbox.execute(request));
    assert.deepEqual(calls.map(value=>value[0]),['restore']);
  });
});

test('legacy sends without attachments still use their original admission',async()=>{
  const args=original();delete args.message.attachments;
  const {mailbox,request,calls}=harness(args);
  assert.equal((await mailbox.execute(request)).state,'completed');
  assert.deepEqual(calls.map(value=>value[0]),['restore','lookup','reconcile']);
});
