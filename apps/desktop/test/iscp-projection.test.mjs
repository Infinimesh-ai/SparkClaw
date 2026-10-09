import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { ClientStore } from '../src/main/client-store.mjs';
const scope = { deployment_id:'deployment', owner_id:'owner', client_id:'client' };
function fixture(t) { const root=fs.mkdtempSync(path.join(os.tmpdir(),'iscp-projection-')); const store=new ClientStore(root); t.after(()=>{store.close();fs.rmSync(root,{recursive:true,force:true});}); const conversation=store.create(scope,'test');const task=store.enqueue(scope,conversation.id,'test');store.markSubmitted(scope,task.request_id);return {root,store,task,conversation}; }
test('execution revisions preserve restart termination and reject old states across storage reopen',t=>{
 const {store,task,root,conversation}=fixture(t);
 const event={request_id:task.request_id,revision:4,state:'failed',termination_reason:'gateway_restarted_awaiting_approval',approval_receipts:[]};
 assert.equal(store.acceptExecutionProjection(scope,event),true);
 assert.equal(store.acceptExecutionProjection(scope,{...event,revision:3,state:'running',termination_reason:undefined}),false);
 assert.throws(()=>store.acceptExecutionProjection(scope,{...event,termination_reason:undefined}),/conflict/);
 const reopened=new ClientStore(root); try { assert.equal(reopened.read(scope,conversation.id).tasks[0].termination_reason,event.termination_reason); }finally{reopened.close();}
});
test('event snapshots and cursors commit together and reject previous-principal or stale cursor updates',t=>{
 const {store,root}=fixture(t);
 store.commitEventProjection(scope,{previous_cursor:'',cursor:'opaque-1',revision:1,snapshot:{execution:{state:'running'}}});
 assert.throws(()=>store.commitEventProjection(scope,{previous_cursor:'',cursor:'opaque-2',revision:2,snapshot:{}}),/cursor/);
 const other={...scope,owner_id:'other'}; assert.deepEqual(store.eventProjection(other),{cursor:'',revision:0,snapshot:{},epoch:''});
 const reopened=new ClientStore(root);try{assert.deepEqual(reopened.eventProjection(scope),{cursor:'opaque-1',revision:1,snapshot:{execution:{state:'running'}},epoch:''});}finally{reopened.close();}
});

test('new gateway epochs need an explicit authoritative reset before replacing a higher cursor revision',t=>{
 const {store}=fixture(t);
 store.commitEventProjection(scope,{previous_cursor:'',cursor:'old',revision:9,epoch:'old',reset:true,snapshot:{}});
 assert.throws(()=>store.commitEventProjection(scope,{previous_cursor:'old',cursor:'new',revision:1,epoch:'new',snapshot:{}}),/cursor/);
 store.commitEventProjection(scope,{previous_cursor:'old',cursor:'new',revision:1,epoch:'new',reset:true,snapshot:{}});
 assert.equal(store.eventProjection(scope).epoch,'new');
});
