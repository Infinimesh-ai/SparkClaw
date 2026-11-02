import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { MedicineStore } from '../src/main/medicine-store.mjs';
import { MedicineSyncClient } from '../src/main/medicine-sync-client.mjs';
const scope={deployment_id:'deployment',owner_id:'owner'};
const item=(id='1',version=1)=>({id:'med_'+id.repeat(32),version,name:'药品'+id,category:'other',quantity:8,unit:'tablet',expires_on:'2027-11-30',no_expiry:false,location:'',usage:'',notes:'',created_at:'2026-11-02T00:00:00Z',updated_at:'2026-11-02T00:00:00Z'});
const page=(items,more=false,cursor='done',base=0,sequence=1,mode='snapshot')=>({schema_version:1,epoch:'epoch',mode,sequence,base_sequence:base,events:items.map((i,index)=>({sequence:mode==='snapshot'?sequence:base+index+1,id:i.id,item:i,deleted:false})),cursor,more});
function fixture(t){const root=fs.mkdtempSync(path.join(os.tmpdir(),'medicine-sync-'));const f={root,store:new MedicineStore(root)};f.restart=()=>{f.store.close();return f.store=new MedicineStore(root);};t.after(()=>{f.store.close();fs.rmSync(root,{recursive:true,force:true});});return f;}

test('snapshot pages keep original inventory visible until the last page and survive reopening',t=>{
 const f=fixture(t);let store=f.store;const old=store.save(scope,{...item('a'),id:''});
 store.apply(scope,page([item()],true,'page2',0,2),'');store=f.restart();assert.equal(store.list(scope).items[0].id,old.id);
 assert.throws(()=>store.apply(scope,page([item('2')],false,'done',0,3),'page2'),/snapshot changed/);
 store.apply(scope,page([item('2')],false,'done',0,2),'page2');assert.equal(store.list(scope).items.length,2);assert.equal(store.version(scope,item().id),1);
 store.apply(scope,page([{...item(),quantity:7},{...item(),version:2,quantity:6}],false,'delta',2,4,'delta'),'done');
 assert.equal(store.list(scope).items.find(i=>i.id===item().id).quantity,6);assert.equal(store.cursor(scope),'delta');
 assert.throws(()=>store.apply(scope,page([item('3')]),'stale'),/cursor changed/);
 assert.equal(store.list({deployment_id:'deployment',owner_id:'other'}).items.length,0);
});

test('version two migration preserves a backup and imports only acknowledged legacy rows',t=>{
 const f=fixture(t);let store=f.store;const old=store.save(scope,{...item(),id:''});
 store.db.exec('DROP TABLE legacy_items; DROP TABLE imports; DROP TABLE sync_state; DROP TABLE snapshots; DROP TABLE staged; DROP TABLE mutations; ALTER TABLE items DROP COLUMN version; PRAGMA user_version=2');
 store=f.restart();assert.equal(store.db.prepare('PRAGMA user_version').get().user_version,3);
 assert.equal(store.pendingImport(scope)[0].id,old.id);
 store.imported(scope,[{...old,version:1}]);assert.equal(store.pendingImport(scope).length,0);
 assert.equal(store.db.prepare('SELECT COUNT(*) AS n FROM legacy_items').get().n,1);
 assert.equal(store.list(scope).items[0].version,1);
});

function fakeClient(t,store,fetch){const connection={deploymentID:'deployment',ownerID:'owner',clientID:'client',origin:'https://gateway',authorization:'token'};
 const client=new MedicineSyncClient({store,getConnection:()=>connection,getFetch:()=>fetch,installationID:'installation',intervalMS:1e8});client.active=true;t.after(()=>client.close());return client;}
const json=value=>new Response(JSON.stringify(value),{status:200,headers:{'content-type':'application/json'}});

test('a lost write response is recovered once and the same user retry never creates another medicine',async t=>{
 const f=fixture(t);let store=f.store;let created,operation,posts=0;
 const client=fakeClient(t,store,async(url,init)=>{
  const request=init.body?JSON.parse(init.body):{};
  if(url.endsWith('/sync'))return json(request.cursor?page(created?[created]:[],false,'done',0,created?1:0,'delta'):page(created?[created]:[],false,'done',0,created?1:0));
  if(url.endsWith('/items')){posts++;created=item();operation=request.operation_id;throw new Error('lost response');}
  if(url.includes('/operations/'))return json({operation_id:operation,batch:{operation_id:operation,items:[{entry_id:'single',after:created,deleted:false}],sequence:1,digest:'a'.repeat(64)}});
  throw new Error('unexpected path');
 });
 const draft={...item(),id:'',version:undefined};const action=crypto.randomUUID();
 await assert.rejects(client.save(draft,action),/lost response/);assert.equal(store.mutation(scope).state,'attempted');
 store=f.restart();client.store=store;
 const recovered=await client.save(draft,action);assert.equal(recovered.id,created.id);assert.equal(posts,1);assert.equal(store.list(scope).items.length,1);
});

test('unknown response followed by absent receipt never retries the write',async t=>{
 const {store}=fixture(t);let posts=0;
 const client=fakeClient(t,store,async(url,init)=>{
  if(url.endsWith('/sync'))return json(page([],false,'done',0,0));
  if(url.endsWith('/items')){posts++;throw new Error('lost response');}
  return new Response(null,{status:404});
 });
 const draft={...item(),id:'',version:undefined};await assert.rejects(client.save(draft,crypto.randomUUID()));
 await assert.rejects(client.save(draft,crypto.randomUUID()));assert.equal(posts,1);assert.equal(store.list(scope).items.length,0);
});

test('background synchronization does not replace the version the owner edited',async t=>{
 const {store}=fixture(t);let posted;
 const client=fakeClient(t,store,async(url,init)=>{
  if(url.endsWith('/sync'))return json(page([{...item(),version:4}],false,'done',0,4));
  posted=JSON.parse(init.body);return new Response(null,{status:409});
 });
 await assert.rejects(client.save({...item(),quantity:3},crypto.randomUUID()),/修改/);
 assert.equal(posted.base_version,1);assert.equal(store.version(scope,item().id),4);assert.equal(store.list(scope).items[0].quantity,8);
 client.close();await client.refresh();assert.equal(store.list(scope).items.length,1);await assert.rejects(client.save(item(),crypto.randomUUID()),/连接/);
});


test('large legacy imports use bounded batches and apply a completed import after a crash',async t=>{
 const {store}=fixture(t);const originals=[];
 for(let n=0;n<100;n++)originals.push(store.save(scope,{...item(),id:'',notes:'中'.repeat(500)}));
 store.db.prepare('INSERT INTO legacy_items SELECT scope,id,name,category,quantity,unit,expires_on,no_expiry,location,usage,notes,created_at,updated_at FROM items').run();
 const first=originals.slice(0,3).map(x=>({...x,version:1}));
 const pending=store.beginMutation(scope,{kind:'import',body:{items:originals.slice(0,3)}});store.attempted(scope,pending.operation_id);store.finishMutation(scope,pending.operation_id,{items:first});
 let imports=0;const client=fakeClient(t,store,async(url,init)=>{
  const body=JSON.parse(init.body);
  if(url.endsWith('/import')){imports++;assert.ok(Buffer.byteLength(init.body)<50*1024);return json({operation_id:body.operation_id,items:body.items.map(x=>({...x,version:1}))});}
  const offset=Number(body.cursor)||0;const values=originals.slice(offset,offset+20).map(x=>({...x,version:1}));return json(page(values,offset+20<100,String(offset+20),0,100));
 });
 await client.sync();const settled=imports;await client.refresh();assert.equal(imports,settled);assert.ok(client.lastSync>0);assert.ok(imports>1);assert.equal(store.pendingImport(scope).length,0);assert.equal(store.list(scope).items.length,100);assert.equal(store.mutation(scope),undefined);
});
