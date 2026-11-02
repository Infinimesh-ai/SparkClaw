import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { approvalInputs } from '../src/main/approval-input.mjs';
import { ClientStore } from '../src/main/client-store.mjs';
const scope={deployment_id:'deployment',owner_id:'owner',client_id:'client'};
const form={schema_version:1,entries:[{id:'med1',label:'药品',summary:'待补全',fields:[{name:'quantity',label:'数量',type:'number',required:true,min:0,max:100000}]}]};
test('approval input digest matches Go sorted JSON and HTML-safe escaping without changing text',()=>{
 const values={b:{notes:'<&>\u2028\u2029'},a:{quantity:1.25}};
 const checked=approvalInputs(form,'approve',values);
 assert.equal(checked.json,'{"a":{"quantity":1.25},"b":{"notes":"\\u003c\\u0026\\u003e\\u2028\\u2029"}}');
 assert.equal(checked.digest,crypto.createHash('sha256').update(checked.json).digest('hex'));
 assert.equal(approvalInputs(form,'approve',{'2':{'3':'later'},'10':{'20':'earlier'}}).json,'{"10":{"20":"earlier"},"2":{"3":"later"}}');
 assert.deepEqual(checked.payload.inputs,values);assert.throws(()=>approvalInputs(form,'approve',undefined));assert.throws(()=>approvalInputs(form,'reject',values));
});
test('form and exact submitted values survive restart and a changed retry is rejected',t=>{
 const root=fs.mkdtempSync(path.join(os.tmpdir(),'approval-input-'));t.after(()=>fs.rmSync(root,{recursive:true,force:true}));let store=new ClientStore(root);
 const conversation=store.create(scope,'batch');const task=store.enqueue(scope,conversation.id,'add medicine');const expiry=new Date(Date.now()+60000).toISOString();
 const approval={approval_id:'approval',tool:'medicine.complete',summary:'complete',arguments:{},digest:'a'.repeat(64),input_form:form};
 store.syncApprovals(scope,task.request_id,[approval],'running',expiry);
 store.approvalDecision(scope,task.request_id,'approval',approval.digest,'approve',false,{med1:{quantity:2}});store.close();store=new ClientStore(root);
 try{
  const row=store.approvals(scope,task.request_id)[0];assert.deepEqual(row.input_form,form);assert.deepEqual(row.input_values,{med1:{quantity:2}});
  assert.throws(()=>store.approvalDecision(scope,task.request_id,'approval',approval.digest,'approve',false,{med1:{quantity:3}}),/cannot be changed/);
  store.approvalDecision(scope,task.request_id,'approval',approval.digest,'approve',true,{med1:{quantity:2}});
  assert.throws(()=>store.syncApprovals(scope,task.request_id,[{...approval,input_form:{...form,entries:[]}}],'running',expiry),/changed content/);
 }finally{store.close();}
});
