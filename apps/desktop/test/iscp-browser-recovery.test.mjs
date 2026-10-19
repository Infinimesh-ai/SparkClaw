import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { EventEmitter } from 'node:events';
import { PassThrough, Writable } from 'node:stream';
import { ISCPTransport, ISCP_OPERATIONS } from '../src/main/iscp-transport.mjs';
import { ISCPMutationJournal } from '../src/main/iscp-mutation-journal.mjs';
import { browserMutationResource, expireBrowserGrant, settleBrowserLifecycle } from '../src/main/iscp-browser-recovery.mjs';
import { mutationProtector } from './fixtures/mutation-protector.mjs';
function directory(t) { const root=fs.mkdtempSync(path.join(os.tmpdir(),'browser-recovery-'));t.after(()=>fs.rmSync(root,{recursive:true,force:true}));return root; }
const request=(operation,body={})=>({operation,body,installation_id:'installation'});
function result(journal, req, body, status=200) {
 const resource=browserMutationResource(req), row=journal.prepare(resource,req,{deadline:Date.now()+60000,authorizationRevision:1});
 journal.markAttempted(resource,row.operation_id);journal.saveResult(resource,row.operation_id,{status,body});journal.complete(resource,row.operation_id);return row;
}
test('browser successor requires original terminal grant expiry or the exact confirmed closed epoch', t=>{
 const root=directory(t),journal=new ISCPMutationJournal(root,{client:'client'},mutationProtector);
 const grant=request('browser.host.grant'),registration=request('browser.host.register',{host_id:'host',runtime_generation:'runtime',grant_token:'PRIVATE-GRANT'});
 const g=result(journal,grant,{host_id:'host',expires_at:new Date(Date.now()-1000).toISOString()});
 expireBrowserGrant(journal,grant);assert.equal(journal.read(browserMutationResource(grant)),undefined);
 assert.equal(fs.readdirSync(root).filter(name=>name.endsWith('.done')).length,1);
 result(journal,registration,{host_id:'host',connection_epoch:'new-epoch'});
 settleBrowserLifecycle(journal,request('browser.host.close',{host_id:'host',connection_epoch:'old-epoch'}),{closed:true});
 assert.equal(journal.read(browserMutationResource(registration)).response.body.connection_epoch,'new-epoch');
 settleBrowserLifecycle(journal,request('browser.host.close',{host_id:'host',connection_epoch:'new-epoch'}),{closed:true});
 assert.equal(journal.read(browserMutationResource(registration)),undefined);
 const pending=journal.prepare(browserMutationResource(grant),grant,{deadline:Date.now()+60000});journal.markAttempted(browserMutationResource(grant),pending.operation_id);
 expireBrowserGrant(journal,grant);assert.equal(journal.read(browserMutationResource(grant)).phase,'attempted');
 assert.notEqual(pending.operation_id,g.operation_id);
});
test('lost automatic grant/register results reconcile original IDs instead of revoking another grant or changing epoch', async t=>{
 const root=directory(t),receipts=new Map(),effects=new Map(),calls=[];let child,lose=true;
 const capabilities={schema_version:2,profile:'sparkclaw.workbench.transport.v2',session_id:'session',authorization_revision:1,expires_at:new Date(Date.now()+60000).toISOString(),binding:{deployment_id:'d',owner_id:'o',client_id:'c'},operations:['browser.host.grant','browser.host.register','browser.host.close','operations.receipt']};
 const transport=new ISCPTransport({origin:'https://iscp.invalid',configPath:'/private/test.json',journalRoot:root,mutationProtector,installationID:'installation',timeoutMS:25,spawnProcess:()=>{
  child=new EventEmitter();child.stdout=new PassThrough();child.stderr=new PassThrough();child.kill=()=>{};
  child.send=frame=>child.stdout.write(JSON.stringify({ipc_version:1,...frame})+'\n');
  child.stdin=new Writable({write(chunk,_,done){
   const call=JSON.parse(chunk);calls.push(call.request);let body;
   if(call.request.operation==='operations.receipt')body={state:'completed',operation_id:call.request.params.operation_id,response:{status:200,body:receipts.get(call.request.params.operation_id)}};
   else {
    effects.set(call.request.operation,(effects.get(call.request.operation)||0)+1);
    body=call.request.operation==='browser.host.grant'?{host_id:'host',grant_token:'PRIVATE-GRANT',expires_at:new Date(Date.now()+60000).toISOString()}:call.request.operation==='browser.host.close'?{closed:true}:{host_id:'host',connection_epoch:'original-epoch'};
    receipts.set(call.request.operation_id,body);
    if(lose){done();return;}
   }
   child.send({type:'response',id:call.id,response:{type:'task.result',profile:capabilities.profile,id:call.id,status:200,body}});done();
  }});
  queueMicrotask(()=>{child.send({type:'hello',operations:ISCP_OPERATIONS,max_request_bytes:65536,max_response_bytes:65536});child.send({type:'capabilities',capabilities});child.send({type:'state',state:'transport_ready'});});return child;
 }});t.after(()=>transport.close());await transport.start();
 await assert.rejects(transport.invoke('browser.host.grant',{}),/deadline/);
 const grant=await transport.invoke('browser.host.grant',{});assert.equal(grant.host_id,'host');assert.equal(effects.get('browser.host.grant'),1);
 const registration={host_id:'host',grant_token:grant.grant_token,runtime_generation:'runtime'};
 await assert.rejects(transport.invoke('browser.host.register',registration),/deadline/);
 assert.equal((await transport.invoke('browser.host.register',registration)).connection_epoch,'original-epoch');
 assert.equal(effects.get('browser.host.register'),1);
 assert.equal((await transport.invoke('browser.host.register',registration)).connection_epoch,'original-epoch');assert.equal(effects.get('browser.host.register'),1);
 lose=false;await transport.invoke('browser.host.close',{host_id:'host',connection_epoch:'original-epoch'});
 await transport.invoke('browser.host.register',registration);assert.equal(effects.get('browser.host.register'),2);
 const registrations=calls.filter(call=>call.operation==='browser.host.register');assert.notEqual(registrations[0].operation_id,registrations[1].operation_id);
 for(const file of fs.readdirSync(path.join(root,'mutations'))) assert.equal(fs.readFileSync(path.join(root,'mutations',file)).includes(Buffer.from('PRIVATE-GRANT')),false);
});
