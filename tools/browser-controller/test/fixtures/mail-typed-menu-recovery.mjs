import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import {localMailFixture} from './local-mail-runtime.mjs';
import {sendManagedMail, validateManagedSend} from '../../mail-runtime/applications/mail/lib/managed-send.mjs';
import {createNotSentReceipt} from '../../mail-runtime/applications/mail/lib/send-recovery.mjs';
import {openSendJournal} from '../../mail-runtime/applications/mail/lib/send-journal.mjs';

// A controlled compose adapter isolates the production upload/menu branch. Its
// native chooser, attachmentDOM, managed-send catch, signed Host, durable send
// admission, journal and receipt-only recovery use the production local runtime.
export async function assertTypedMenuRecovery(t,{tab,root,manifest,reason}) {
  const input={schema_version:1,operation:'send',provider:'outlook',account:'default',account_address:'owner@example.test',invocation_id:'menu-proof-fixture',mode:'compose',
    message:{to:['sink@example.test'],cc:[],subject:'Synthetic',body:{format:'text',content:'Body'},attachments:manifest}};
  const hash=value=>crypto.createHash('sha256').update(value).digest('hex');let filled=false,effects=0,hostAcquires=0;
  const providerTab={...tab,
    runReadCode:code=>code.startsWith('async page=>{await page.goto(')?true:tab.runReadCode(code),
    async inspect(code){
      if(code.includes('function attachmentDOM('))return tab.inspect(code);
      if(code.includes('account_hash'))return {origin:'https://outlook.live.com/mail/0/',result:{url:'https://outlook.live.com/mail/0/',account_hash:hash(input.account_address)}};
      if(code.includes('function managedSendDOM(')){
        if(code.includes('"discard"'))return {result:{discarded:true}};
        if(code.includes('"readback"'))return {result:{to_count:filled?1:0,cc_count:0,to_hash:hash(JSON.stringify(input.message.to)),cc_hash:hash('[]'),subject_hash:hash(input.message.subject),body_hash:hash(input.message.body.content),send_ready:true,linked:true}};
        if(code.includes('"editor"'))return {result:{ready:true,has_subject:true,has_cc:false}};
        if(code.includes('"open"'))return {result:{opened:true}};
        assert.fail('unexpected compose fixture operation');
      }
      return {result:{ids:[]}};
    },async focus(){},async press(){},async fill(selector){if(selector==='[data-sc-mail-control="to"]')filled=true;},
    async click(){assert.fail('menu failure must never reach provider Send');}};
  const f = await localMailFixture(t);
  // The synthetic browser fixture stages attachments in its own private root.
  f.factory.emailWorkspaceRoot = root;
  const key = 'mail-outlook\0send', entry = f.factory.commands.get(key);
  let originalEvidence;
  f.factory.commands.set(key, {...entry, handler: {async run(args, context) {
    originalEvidence = context.executionEvidence;
    try {return {data: await sendManagedMail(args, 'outlook', {emailWorkspaceRoot: root,
      beforeEffect: () => {context.beforeEffect(); effects++;}, withSendTab: callback => callback(providerTab),
      notSentReceipt: (request, error) => createNotSentReceipt(request, context, error, 'pre_dispatch_failure')})};}
    catch (error) {throw Object.assign(error, {code: error.code.toUpperCase()});}
  }}});
  const request = f.request('outlook', 'send', input);
  const result = await f.factory.runScript(request);
  assert.equal(result.task.status, 'uncertain'); assert.equal(effects, 1);
  const journal = () => openSendJournal(root, validateManagedSend(input, 'outlook'));
  const persisted = (await journal()).saved;
  assert.equal(persisted.stage, 'not_sent'); assert.equal(persisted.receipt.not_sent.kind, 'pre_dispatch_failure');
  assert.equal(persisted.receipt.not_sent.reason, reason);
  for (const [key, value] of Object.entries(originalEvidence)) assert.equal(persisted.receipt.not_sent[key], value);
  assert.equal(await (await journal()).write('dispatching'), false);
  f.factory.commands.set(key, entry);
  f.factory.hostClient.acquire = async () => {hostAcquires++; assert.fail('reconciliation must not acquire browser');};
  const recovered = await f.factory.runScript({...request, input: {...input, mode: 'reconcile'}});
  assert.equal(recovered.state, 'completed'); assert.deepEqual(recovered.result, persisted.receipt);
  assert.equal(effects, 1); assert.equal(hostAcquires, 0);
}
