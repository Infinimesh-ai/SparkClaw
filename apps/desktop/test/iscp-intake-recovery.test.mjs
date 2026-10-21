import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { EventEmitter } from 'node:events';
import { PassThrough, Writable } from 'node:stream';
import { ISCPTransport, ISCP_OPERATIONS } from '../src/main/iscp-transport.mjs';
import { mutationProtector } from './fixtures/mutation-protector.mjs';
const origin='https://iscp.invalid';
const originalBody={intake_enabled:true,expected_mailbox_version:0};
async function eventually(check){for(let n=0;n<100;n++){if(check())return;await new Promise(resolve=>setTimeout(resolve,5));}assert.fail('recovery did not settle');}
async function fixture(t,{operation='mail.intake.update',capability=true}={}){
 const root=fs.mkdtempSync(path.join(os.tmpdir(),'intake-recovery-'));
 const f={calls:[],resolved:false,drop:false,conflict:false,effects:0};
 const response={status:409,body:{code:'email_intake_not_applied',provider:'gmail',provider_version:1,retryable:false}};
 const caps={schema_version:2,profile:'sparkclaw.workbench.transport.v2',session_id:'s',authorization_revision:1,expires_at:new Date(Date.now()+60000).toISOString(),binding:{deployment_id:'d',owner_id:'o',client_id:'c'},operations:['mail.intake.update','mail.providers.update','mail.drafts.send','operations.receipt',...(capability?['mail.intake.reconcile']:[])]};
 const spawnProcess=()=>{
  const child=new EventEmitter();child.stdout=new PassThrough();child.stderr=new PassThrough();child.exitCode=null;child.kill=()=>{child.exitCode=0;};
  child.send=frame=>child.stdout.write(JSON.stringify({ipc_version:1,...frame})+'\n');
  child.stdin=new Writable({write(chunk,_,done){const call=JSON.parse(chunk);f.calls.push(call);const op=call.request.operation;let status=200,body;
   if(op==='operations.receipt')body={operation_id:f.original.operation_id,input_digest:'original-digest',state:f.resolved?'completed':'unknown',...(f.resolved?{response}: {})};
   else if(op==='mail.intake.reconcile'){
    const recovered=JSON.parse(Buffer.from(call.body_base64,'base64').toString('utf8'));assert.equal(recovered.operation_id,f.original.operation_id);assert.deepEqual(recovered.original.body,originalBody);assert.equal(call.request.installation_id,'installation');
    if(f.conflict){status=409;body={code:'operation_outcome_unknown'};}
    else{f.resolved=true;body={operation_id:f.original.operation_id,input_digest:f.wrongDigest?'different':'original-digest',state:'completed',response};if(f.drop){done();return;}}
   }else{f.effects++;body={mailbox:{id:'box',version:2}};}
   child.send({type:'response',id:call.id,response:{type:'task.result',profile:caps.profile,id:call.id,status,body}});done();
  }});
  queueMicrotask(()=>{child.send({type:'hello',operations:ISCP_OPERATIONS,max_request_bytes:65536,max_response_bytes:65536});child.send({type:'capabilities',capabilities:caps});child.send({type:'state',state:'transport_ready'});});return child;
 };
 f.transport=new ISCPTransport({origin,configPath:'/private/test.json',installationID:'installation',spawnProcess,timeoutMS:35,journalRoot:root,mutationProtector});
 await f.transport.start();
 const request={operation,params:{provider:'gmail'},body:originalBody,installation_id:'installation',type:'task.invoke',profile:caps.profile};
 f.resource=operation==='mail.drafts.send'?'mail-draft:list':'mail-provider:gmail:update';
 f.original=f.transport.mutations.prepare(f.resource,request,{rawBody:JSON.stringify(originalBody),deadline:Date.now()+60000,authorizationRevision:1,actionID:'00000000-0000-4000-8000-000000000001'});
 f.transport.mutations.markAttempted(f.resource,f.original.operation_id);
 f.restart=async()=>{f.transport.close();await f.transport.start();};
 t.after(()=>{f.transport.close();fs.rmSync(root,{recursive:true,force:true});});return f;
}
test('READY resolves the original intake with no repeated PATCH; fresh foreground action uses new version',async t=>{
 const f=await fixture(t);await f.restart();await eventually(()=>f.transport.mutations.read(f.resource)?.phase==='terminal');
 assert.equal(f.effects,0);assert.equal(f.calls.filter(c=>c.request.operation==='mail.intake.reconcile').length,1);
 const first=await f.transport.fetch(origin+'/api/email/providers/gmail',{method:'PATCH',body:JSON.stringify(originalBody)});
 assert.equal(first.status,409);assert.equal(f.transport.mutations.read(f.resource).operation_id,f.original.operation_id);
 const next=await f.transport.fetch(origin+'/api/email/providers/gmail',{method:'PATCH',body:JSON.stringify({...originalBody,expected_mailbox_version:1}),headers:{'x-sparkclaw-action-id':'00000000-0000-4000-8000-000000000002'}});
 assert.equal(next.status,200);assert.equal(f.effects,1);assert.notEqual(f.transport.mutations.read(f.resource).operation_id,f.original.operation_id);
});
test('lost reconcile response recovers the same original result after restart without a second mutation',async t=>{
 const f=await fixture(t);f.drop=true;await f.restart();await eventually(()=>f.resolved);await new Promise(resolve=>setTimeout(resolve,50));
 assert.equal(f.transport.mutations.read(f.resource).phase,'attempted');f.drop=false;await f.restart();await eventually(()=>f.transport.mutations.read(f.resource).phase==='terminal');
 assert.equal(f.effects,0);assert.equal(f.calls.filter(c=>c.request.operation==='mail.intake.reconcile').length,1);
 assert.equal(f.transport.mutations.read(f.resource).operation_id,f.original.operation_id);
});
test('refreshed provider version still delivers the original negative result before a new foreground write',async t=>{
 const f=await fixture(t);await f.restart();await eventually(()=>f.transport.mutations.read(f.resource)?.phase==='terminal');
 const body=JSON.stringify({...originalBody,expected_mailbox_version:1});
 const first=await f.transport.fetch(origin+'/api/email/providers/gmail',{method:'PATCH',body,headers:{'x-sparkclaw-action-id':'00000000-0000-4000-8000-000000000002'}});
 assert.equal(first.status,409);assert.equal((await first.json()).code,'email_intake_not_applied');assert.equal(f.effects,0);
 const next=await f.transport.fetch(origin+'/api/email/providers/gmail',{method:'PATCH',body,headers:{'x-sparkclaw-action-id':'00000000-0000-4000-8000-000000000003'}});
 assert.equal(next.status,200);assert.equal(f.effects,1);
});
for(const options of [{operation:'mail.drafts.send'},{operation:'mail.providers.update'},{capability:false}])test(`only qualified intake may reconcile: ${JSON.stringify(options)}`,async t=>{
 const f=await fixture(t,options);await f.restart();await new Promise(resolve=>setTimeout(resolve,30));
 assert.equal(f.transport.mutations.read(f.resource).phase,'attempted');assert.equal(f.calls.filter(c=>c.request.operation==='mail.intake.reconcile').length,0);assert.equal(f.effects,0);
});
for(const failure of ['conflict','wrongDigest'])test(`unsafe intake reconciliation keeps original fence: ${failure}`,async t=>{
 const f=await fixture(t);f[failure]=true;await f.restart();await eventually(()=>f.calls.some(c=>c.request.operation==='mail.intake.reconcile'));await new Promise(resolve=>setTimeout(resolve,10));
 assert.equal(f.transport.mutations.read(f.resource).phase,'attempted');assert.equal(f.effects,0);assert.equal(f.transport.mutations.read(f.resource).operation_id,f.original.operation_id);
});
