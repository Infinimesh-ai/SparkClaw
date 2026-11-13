import test from 'node:test';
import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import { MCDHost, MCD_HOST_OPERATIONS } from '../src/main/mcd/host.mjs';
import { MCDSession } from '../src/main/mcd/session.mjs';

const hash = bytes => crypto.createHash('sha256').update(bytes).digest('hex');
function harness() {
  let now=100000, calls=[];
  const binding={ deployment_id:'deployment',owner_id:'owner',client_id:'client',installation_id:'mac' };
  const identity={...binding,account_ref:'account',member_digest:'a'.repeat(64)};
  let saved={schema_version:1,binding,identity,connection_id:'connection',state:'active',source:{root:'/private/original-ledger',alias:'personal'},envelope:{identity,credential_revision:1,material_digest:'b'.repeat(64),session_not_after_ms:200000}};
  const context={...binding,session_id:'session',authorization_revision:7,operations:[...MCD_HOST_OPERATIONS,'mcd.session.provision','mcd.session.revoke','mcd.session.status'],qualified_operations:[...MCD_HOST_OPERATIONS]};
  const session=new MCDSession({context:()=>context,vault:{loadMCDConnection:async()=>structuredClone(saved),saveMCDConnection:async(_,row)=>{saved=structuredClone(row);}}});
  const scope={...identity,credential_revision:1,execution_id:'execution'};
  const create=overrides=>new MCDHost({session,context:()=>context,now:()=>now,call:async request=>{calls.push(request);return {state:request.operation==='host.abort'?'aborted_before_write':'prepared',local_quote_ref:'original-ref'};},...overrides});
  return {create,context,scope,calls,saved:()=>saved,session,advance:ms=>{now+=ms;},peer:()=>({session_id:context.session_id,authorization_revision:context.authorization_revision}),request:(operation,body)=>({operation,body,installation_id:'mac'})};
}
function parts(scope, expiry=160000) {
  const bytes=Buffer.from(JSON.stringify({scope,expires_at_ms:expiry,padding:'x'.repeat(20000)}));
  return Array.from({length:Math.ceil(bytes.length/8192)},(_,index)=>({digest:hash(bytes),size:bytes.length,count:Math.ceil(bytes.length/8192),index,expires_at_ms:expiry,data:bytes.subarray(index*8192,(index+1)*8192).toString('base64')}));
}
test('private quote chunks require exact original digest, complete body and current session',async()=>{
 const h=harness(),host=h.create(),chunks=parts(h.scope);
 for(const part of chunks.slice(0,-1)) {
  const reply=await host.handleReverse(h.request('mcd.checkout.prepare',{scope:h.scope,operation_key:'prepare',transfer:part}),h.peer());
  assert.equal(reply.body.state,'chunk_received');assert.equal(h.calls.length,0);
 }
 const bad={...chunks[0],data:Buffer.alloc(8192).toString('base64')};
 assert.equal((await host.handleReverse(h.request('mcd.checkout.prepare',{scope:h.scope,operation_key:'prepare',transfer:bad}),h.peer())).status,503);
 const final=await host.handleReverse(h.request('mcd.checkout.prepare',{scope:h.scope,operation_key:'prepare',transfer:chunks.at(-1)}),h.peer());
 assert.equal(final.body.local_quote_ref,'original-ref');assert.equal(h.calls.length,1);
 assert.equal(h.calls[0].params.source.root,'/private/original-ledger');assert.equal(h.calls[0].params.initialize,true);
 assert.equal(h.saved().host_ledger,'ready');assert.equal(host.transfers.size,0);
 const oldPeer=h.peer();h.context.session_id='replacement';
 assert.equal((await host.handleReverse(h.request('mcd.operation.status',{scope:h.scope,operation_key:'prepare'}),oldPeer)).status,503);
 assert.equal(h.calls.length,1);
});

test('purchase independently checks native immutable approval and seals local account only to host stdin',async()=>{
 const h=harness();h.saved().host_account={material_digest:'b'.repeat(64),account_json:'private-v5-runtime-canary'};
 const review={purchase_id:'purchase',purchase_digest:'c'.repeat(64),amount_cents:1490,currency:'CNY',store_code:'3330613',store_name:'Fixture store',pickup_at:'2026-11-13T20:00:00+08:00',eat_type:'locker-in',payment_method:'ARCHCARD',credential_revision:1,items:[{name:'Fixture meal'}],discount_names:[]};
 const approval={approval_id:'approval',digest:'d'.repeat(64),tool:'mcd.purchase.commit',arguments:structuredClone(review),decision:'approve',state:'decision_pending',expires_at:160000};
 const body={scope:h.scope,operation_key:'commit',purchase_id:'purchase',handoff_epoch:1,approval_id:'approval',review};
 const host=h.create({approvals:(binding,execution)=>{assert.equal(binding.owner_id,'owner');assert.equal(execution,'execution');return [approval];}});
 for(const mutate of [()=>{approval.decision='reject';},()=>{approval.state='pending';},()=>{approval.arguments.amount_cents=100;},()=>{approval.tool='mcd.cart.write';}]){
  const original=structuredClone(approval);mutate();assert.equal((await host.handleReverse(h.request('mcd.checkout.commit',body),h.peer())).status,503);Object.assign(approval,original);
 }
 assert.equal(h.calls.length,0);
 assert.equal((await host.handleReverse(h.request('mcd.checkout.commit',body),h.peer())).status,200);
 assert.equal(h.calls[0].operation,'host.commit');assert.equal(h.calls[0].params.account_json,'private-v5-runtime-canary');
 assert.deepEqual(h.calls[0].params.request.authority.review,review);assert.equal(h.calls[0].params.request.authority.expires_at_ms,160000);
 h.advance(70000);assert.equal((await host.handleReverse(h.request('mcd.checkout.commit',body),h.peer())).status,503);assert.equal(h.calls.length,1);
});

test('locking host aborts active helper before disconnect can clear credentials',async()=>{
 const h=harness();let started;
 const ready=new Promise(resolve=>{started=resolve;});
 const host=h.create({call:(_,options)=>new Promise((resolve,reject)=>{started();options.signal.addEventListener('abort',()=>reject(new Error('stopped')),{once:true});})});
 const pending=host.handleReverse(h.request('mcd.operation.status',{scope:h.scope,operation_key:'original'}),h.peer());
 await ready;host.close();await h.session.whenIdle();assert.equal((await pending).status,503);assert.equal(h.session.busy,false);
});
test('abort before prepare attaches original ledger and restart never reinitializes it',async()=>{
 const h=harness();
 const body={scope:h.scope,operation_key:'abort',purchase_id:'purchase',handoff_epoch:1,reason:'user_abandonment'};
 assert.equal((await h.create().handleReverse(h.request('mcd.checkout.abort',body),h.peer())).body.state,'aborted_before_write');
 await h.create().handleReverse(h.request('mcd.checkout.abort',body),h.peer());
 assert.deepEqual(h.calls.map(c=>c.params.initialize),[true,false]);
 assert.deepEqual(h.calls[0].params.request,h.calls[1].params.request);
});
test('unqualified, expired, foreign and purchase requests never reach helper',async()=>{
 const h=harness(),host=h.create(),part=parts(h.scope)[0];
 const prepare=scope=>h.request('mcd.checkout.prepare',{scope,operation_key:'prepare',transfer:part});
 assert.equal((await host.handleReverse(prepare({...h.scope,owner_id:'other'}),h.peer())).status,503);
 h.context.qualified_operations=[];
 assert.equal((await host.handleReverse(prepare(h.scope),h.peer())).status,503);
 h.context.qualified_operations=[...MCD_HOST_OPERATIONS];
 assert.equal((await host.handleReverse(h.request('mcd.checkout.commit',{}),h.peer())).status,503);
 h.advance(70000);
 assert.equal((await host.handleReverse(prepare(h.scope),h.peer())).status,503);
 assert.equal(h.calls.length,0);
});
