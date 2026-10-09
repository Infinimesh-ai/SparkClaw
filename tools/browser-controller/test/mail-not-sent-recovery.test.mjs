import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import crypto from 'node:crypto';
import {pathToFileURL} from 'node:url';
import test from 'node:test';

const runtime = process.env.APP_CLI_RECOVERY_RUNTIME_ROOT
  ? pathToFileURL(path.resolve(process.env.APP_CLI_RECOVERY_RUNTIME_ROOT) + path.sep)
  : new URL('../', import.meta.resolve('@infinimesh/app-cli-runtime/release'));
const {Executor} = await import(new URL('src/executor.mjs',runtime));
const {Ledger} = await import(new URL('src/ledger.mjs',runtime));
const {SignedFileAuthorization} = await import(new URL('src/authorization.mjs',runtime));
const {digest,intentDigest} = await import(new URL('src/protocol.mjs',runtime));
const {mailHandlers} = await import(new URL('applications/mail/handlers.mjs',runtime));
const {outputSchema} = await import(new URL('applications/mail/output-schema.mjs',runtime));
const {sendManagedMail,validateManagedSend} = await import(new URL('applications/mail/lib/managed-send.mjs',runtime));
const {openSendJournal} = await import(new URL('applications/mail/lib/send-journal.mjs',runtime));
const {createNotSentReceipt} = await import(new URL('applications/mail/lib/send-recovery.mjs',runtime));
// Read the immutable .15 binding, not whichever release is currently installed.
import {spawnSync} from 'node:child_process';
const archive = new URL('../../../vendor/app-cli/infinimesh-app-cli-runtime-0.3.0-sparkclaw.15.tgz',import.meta.url);
const extracted = spawnSync('tar',['-xOf',archive.pathname,'package/bindings/mail-qq-mail.json'],{encoding:'utf8',timeout:5000});
assert.equal(extracted.status,0,extracted.stderr);
const oldBinding=JSON.parse(extracted.stdout);
const args = () => ({schema_version:1,operation:'send',provider:'qq_mail',account:'default',
  account_address:'owner@example.test',invocation_id:'reviewed-invocation',mode:'compose',
  message:{to:['sink@example.test'],cc:[],subject:'Reviewed',body:{format:'text',content:'Body'},attachments:[
    {name:'file.txt',size_bytes:1,sha256:'sha256:'+'a'.repeat(64),path:`.sparkclaw-mail-send-${'a'.repeat(32)}/00/file.txt`}]}});

async function fixture(t,{reason='EMAIL_ATTACHMENT_UPLOAD_UNVERIFIED',binding=oldBinding,future=false}={}) {
  const root=fs.mkdtempSync(path.join(os.tmpdir(),'mail-negative-proof-'));
  fs.chmodSync(root,0o700); t.after(()=>fs.rmSync(root,{recursive:true,force:true}));
  const key=path.join(root,'key');fs.writeFileSync(key,crypto.randomBytes(32),{mode:0o600});
  const authorization=new SignedFileAuthorization(path.join(root,'grants'),key);
  let ledger=new Ledger(path.join(root,'ledger'));
  let executor;
  t.after(async()=>{await executor?.close();ledger.close();});
  const resource={binding_digest:digest(binding),workspace_root:root};
  const request={protocol_version:'2.0',operation:'invoke',app:binding.manifest.id,command:'send',request_key:'original-key',
    arguments:args(),deadline_ms:Date.now()+60000};
  const grant={principal:'product-owner',owner:'owner',app:request.app,command:'send',request_key:request.request_key,
    side_effect:binding.manifest.commands.find(v=>v.name==='send').side_effect,intent_digest:intentDigest(request),
    operations:['invoke','lookup','status','reconcile','resume'],access_expires_ms:Date.now()+120000,
    execution_expires_ms:request.deadline_ms,max_deadline_ms:request.deadline_ms,task_id:null,revision:1};
  request.authorization_ref=authorization.issue(grant,resource);
  let sendEffects=0,hostAcquires=0;
  const handlers=await mailHandlers();
  const handlerName=binding.commands.send.handler;
  const originals={...handlers,[handlerName]:{async run(input,context){
    context.beforeEffect();sendEffects++;
    if(future)await sendManagedMail(input,'qq_mail',{emailWorkspaceRoot:root,
      notSentReceipt:(r,error)=>createNotSentReceipt(r,context,error,'pre_dispatch_failure'),
      withSendTab:async()=>{throw Object.assign(new Error(reason),{code:reason.toLowerCase()});}});
    throw Object.assign(new Error(reason),{code:reason});
  }}};
  executor=new Executor({ledger,authorization,bindings:[binding],handlers:originals,
    host:{async acquire(){return {async release(){}};}}});
  const started=await executor.control(request);
  await assert.rejects(executor.control({protocol_version:'2.0',operation:'reconcile',app:request.app,command:'send',
    authorization_ref:request.authorization_ref,task_id:started.task.id}),{code:'INVALID_TASK_STATE'});
  await executor.active.get(started.task.id)?.promise;
  assert.equal(ledger.get(started.task.id).status,'uncertain');
  assert.equal(ledger.get(started.task.id).effect,1);
  await executor.close();
  const current=structuredClone(oldBinding);
  current.manifest.commands.find(v=>v.name==='send').output_schema=outputSchema('qq_mail','send');
  current.manifest_digest=digest(current.manifest);
  const recoveryOptions={authorization,bindings:[current],handlers,
    host:{async acquire(){hostAcquires++;assert.fail('receipt reconciliation acquired a browser');}}};
  executor=new Executor({...recoveryOptions,ledger});
  const control=operation=>({protocol_version:'2.0',operation,app:request.app,command:'send',
    authorization_ref:request.authorization_ref,task_id:started.task.id});
  const reconcile=async()=>{
    await executor.control(control('reconcile'));
    await executor.active.get(started.task.id)?.promise;
    return executor.control(control('status'));
  };
  return {root,get ledger(){return ledger;},request,resource,reconcile,control,get executor(){return executor;},taskID:started.task.id,
    async restart(){await executor.close();ledger.close();ledger=new Ledger(path.join(root,'ledger'));executor=new Executor({...recoveryOptions,ledger});},
    journal:()=>openSendJournal(root,validateManagedSend(request.arguments,'qq_mail')),
    effects:()=>({sendEffects,hostAcquires})};
}

test('trusted .15 terminal upload failure reconciles once without dispatch, reupload or clearing effect',async t=>{
  const f=await fixture(t);
  assert.equal((await f.journal()).saved,null,'fixture must represent a pre-dispatch failure');
  await f.restart();
  const response=await f.reconcile();
  assert.equal(response.task.status,'completed');assert.equal(response.data.status,'not_sent');
  assert.equal(response.data.not_sent.invocation_id,f.request.arguments.invocation_id);
  assert.equal(response.data.not_sent.intent_digest,intentDigest(f.request));
  assert.equal(response.data.not_sent.resource_digest,digest(f.resource));
  assert.equal(response.data.not_sent.kind,'legacy_15_pre_dispatch_failure');
  assert.equal(f.ledger.get(f.taskID).effect,1);
  assert.equal((await f.journal()).saved.stage,'not_sent');
  assert.deepEqual(await f.executor.control(f.control('status')),response);
  await assert.rejects(f.executor.control(f.control('resume')),{code:'RELEASE_MISMATCH'});
  await assert.rejects(f.executor.control(f.control('reconcile')),{code:'INVALID_TASK_STATE'});
  assert.deepEqual(f.effects(),{sendEffects:1,hostAcquires:0});
});

test('absent dispatch journal is insufficient for unknown, restarted or unqualified old executions',async t=>{
  for(const reason of ['SEND_OUTCOME_UNKNOWN','EXECUTOR_RESTARTED','HOST_CLEANUP_FAILED'])await t.test(reason,async t=>{
    const f=await fixture(t,{reason});
    assert.equal((await f.reconcile()).task.status,'uncertain');
    assert.equal((await f.journal()).saved,null);
    assert.equal(f.ledger.get(f.taskID).effect,1);
  });
  await t.test('different binding',async t=>{
    const binding=structuredClone(oldBinding);binding.commands.send.revision++;
    const f=await fixture(t,{binding});
    assert.equal((await f.reconcile()).task.status,'uncertain');
    assert.equal((await f.journal()).saved,null);
  });
});

test('a dispatch claim prevents legacy negative proof even with the recognized terminal error',async t=>{
  const f=await fixture(t),journal=await f.journal();
  await journal.write('dispatching');
  assert.equal((await f.reconcile()).task.status,'uncertain');
  assert.equal((await f.journal()).saved.stage,'dispatching');
});

test('future pre-dispatch proof survives a lost failure reply and cannot be replaced by dispatch',async t=>{
  const f=await fixture(t,{future:true});
  const persisted=(await f.journal()).saved;
  assert.equal(persisted.stage,'not_sent');
  assert.equal(persisted.receipt.not_sent.kind,'pre_dispatch_failure');
  assert.equal(await (await f.journal()).write('dispatching'),false);
  await f.restart();
  const result=await f.reconcile();
  assert.deepEqual(result.data,persisted.receipt);
  assert.equal(f.ledger.epoch,2);
  assert.equal(f.ledger.get(f.taskID).effect,1);
  assert.deepEqual(f.effects(),{sendEffects:1,hostAcquires:0});
});

test('negative and dispatch claims are mutually exclusive across concurrent journal readers',async t=>{
  const f=await fixture(t),first=await f.journal(),second=await f.journal();
  const proof=createNotSentReceipt(validateManagedSend(f.request.arguments,'qq_mail'),{executionEvidence:{task_id:f.taskID,
    intent_digest:intentDigest(f.request),resource_digest:digest(f.resource),binding_digest:f.resource.binding_digest,ledger_epoch:1}},
    'EMAIL_ATTACHMENT_UPLOAD_UNVERIFIED','pre_dispatch_failure');
  const wins=await Promise.all([first.write('not_sent',proof),second.write('dispatching')]);
  assert.equal(wins.filter(Boolean).length,1);
  const state=(await f.journal()).saved.stage;
  assert.equal((await f.reconcile()).task.status,state==='not_sent'?'completed':'uncertain');
});
