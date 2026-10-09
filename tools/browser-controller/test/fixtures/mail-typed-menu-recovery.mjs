import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import crypto from 'node:crypto';

// A controlled compose adapter isolates the production upload/menu branch. Its
// native chooser, attachmentDOM, managed-send catch, signed admission, durable
// Ledger, journal and receipt-only recovery are all the production code paths.
export async function assertTypedMenuRecovery(t,{runtime,tab,root,manifest,reason}) {
  const {Executor}=await import(new URL('src/executor.mjs',runtime));
  const {Ledger}=await import(new URL('src/ledger.mjs',runtime));
  const {SignedFileAuthorization}=await import(new URL('src/authorization.mjs',runtime));
  const {digest,intentDigest}=await import(new URL('src/protocol.mjs',runtime));
  const {mailHandlers}=await import(new URL('applications/mail/handlers.mjs',runtime));
  const {sendManagedMail,validateManagedSend}=await import(new URL('applications/mail/lib/managed-send.mjs',runtime));
  const {createNotSentReceipt}=await import(new URL('applications/mail/lib/send-recovery.mjs',runtime));
  const {openSendJournal}=await import(new URL('applications/mail/lib/send-journal.mjs',runtime));
  const binding=JSON.parse(await fs.readFile(new URL('bindings/mail-outlook.json',runtime),'utf8'));
  const key=path.join(root,'key');await fs.writeFile(key,crypto.randomBytes(32),{mode:0o600});
  const authorization=new SignedFileAuthorization(path.join(root,'grants'),key),ledger=new Ledger(path.join(root,'ledger'));
  let executor; t.after(async()=>{await executor?.close();ledger.close();});
  const input={schema_version:1,operation:'send',provider:'outlook',account:'default',account_address:'owner@example.test',invocation_id:'menu-proof-fixture',mode:'compose',
    message:{to:['sink@example.test'],cc:[],subject:'Synthetic',body:{format:'text',content:'Body'},attachments:manifest}};
  const request={protocol_version:'2.0',operation:'invoke',app:binding.manifest.id,command:'send',request_key:'menu-key',arguments:input,deadline_ms:Date.now()+60000};
  const resource={binding_digest:digest(binding),workspace_root:root};
  request.authorization_ref=authorization.issue({principal:'product-owner',owner:'owner',app:request.app,command:'send',request_key:request.request_key,
    side_effect:binding.manifest.commands.find(c=>c.name==='send').side_effect,intent_digest:intentDigest(request),operations:['invoke','lookup','status','reconcile'],access_expires_ms:Date.now()+120000,
    execution_expires_ms:request.deadline_ms,max_deadline_ms:request.deadline_ms,task_id:null,revision:1},resource);
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
  const handlers=await mailHandlers();
  const original={...handlers,[binding.commands.send.handler]:{async run(args,context){
    try {return {data:await sendManagedMail(args,'outlook',{emailWorkspaceRoot:root,beforeEffect:()=>{context.beforeEffect();effects++;},
      withSendTab:callback=>callback(providerTab),notSentReceipt:(req,error)=>createNotSentReceipt(req,context,error,'pre_dispatch_failure')})};}
    catch(error){throw Object.assign(error,{code:error.code.toUpperCase()});}
  }}};
  executor=new Executor({ledger,authorization,bindings:[binding],handlers:original,host:{async acquire(){return {async release(){}};}}});
  const started=await executor.control(request);await executor.active.get(started.task.id)?.promise;
  const previous=ledger.get(started.task.id);assert.equal(previous.reason,reason);assert.equal(previous.status,'uncertain');assert.equal(previous.effect,1);assert.equal(effects,1);
  const journal=()=>openSendJournal(root,validateManagedSend(input,'outlook'));
  const persisted=(await journal()).saved;assert.equal(persisted.stage,'not_sent');assert.equal(persisted.receipt.not_sent.kind,'pre_dispatch_failure');assert.equal(persisted.receipt.not_sent.reason,reason);
  assert.equal(persisted.receipt.not_sent.intent_digest,intentDigest(request));assert.equal(persisted.receipt.not_sent.resource_digest,digest(resource));assert.equal(await(await journal()).write('dispatching'),false);
  await executor.close();
  executor=new Executor({ledger,authorization,bindings:[binding],handlers,host:{async acquire(){hostAcquires++;assert.fail('reconciliation must not acquire browser');}}});
  const control={protocol_version:'2.0',operation:'reconcile',app:request.app,command:'send',task_id:started.task.id,authorization_ref:request.authorization_ref};
  await executor.control(control);await executor.active.get(started.task.id)?.promise;
  const result=await executor.control({...control,operation:'status'});
  assert.equal(result.task.status,'completed');assert.deepEqual(result.data,persisted.receipt);assert.equal(ledger.get(started.task.id).effect,1);assert.equal(effects,1);assert.equal(hostAcquires,0);
}
