import test from 'node:test';import assert from 'node:assert/strict';import fs from 'node:fs';import path from 'node:path';import os from 'node:os';import crypto from 'node:crypto';
import { ISCPObjectClient } from '../src/main/iscp-object-client.mjs';
const hash=bytes=>crypto.createHash('sha256').update(bytes).digest('hex');
test('upload resumes original transfer after lost chunk receipt; download verifies durable chunks after restart',async t=>{
 const root=fs.mkdtempSync(path.join(os.tmpdir(),'iscp-objects-'));t.after(()=>fs.rmSync(root,{recursive:true,force:true}));const bytes=crypto.randomBytes(20000);let id;let lost=true;const received=new Map();const downloaded=[];
 const object={object_id:'object',version:1,size:bytes.length,sha256:hash(bytes)};
 const call=async(op,body)=>{if(op==='transfer.open'){id??=body.transfer_id;assert.equal(id,body.transfer_id);return {};}
 if(op==='transfer.chunk'){assert.equal(body.transfer_id,id);const old=received.get(body.index);if(old)assert.equal(old,body.data_base64);received.set(body.index,body.data_base64);if(body.index===1&&lost){lost=false;throw new Error('lost response');}return {};}
 if(op==='transfer.commit')return{state:'committed',object};if(op==='object.read'){downloaded.push(body.offset);const data=bytes.subarray(body.offset,body.offset+body.length);return{offset:body.offset,data_base64:data.toString('base64'),sha256:hash(data),eof:body.offset+body.length===bytes.length};}return object;};
 const options={root,scope:{owner_id:'owner'},call};const first=new ISCPObjectClient(options);
 await assert.rejects(first.upload(bytes,{purpose:'execution_request'}),/lost/);
 const restarted=new ISCPObjectClient(options);assert.deepEqual(await restarted.upload(bytes,{purpose:'execution_request'}),object);assert.equal(received.size,3);
 assert.deepEqual(await restarted.download(object),bytes);const after=new ISCPObjectClient(options);assert.deepEqual(await after.download(object),bytes);assert.equal(downloaded.length,3);
 const chunk=fs.readdirSync(root).find(name=>name.endsWith('.0.chunk'));fs.writeFileSync(path.join(root,chunk),Buffer.alloc(8192));await assert.rejects(after.download(object),/integrity/);
});

test('download disk quotas preserve the last checkpoint and expiry removes only transfer cache files',async t=>{
 const root=fs.mkdtempSync(path.join(os.tmpdir(),'iscp-quota-'));t.after(()=>fs.rmSync(root,{recursive:true,force:true}));const bytes=Buffer.alloc(20000,4);let reads=0;
 const object={object_id:'object',version:1,size:bytes.length,sha256:hash(bytes),expires_at:new Date(Date.now()+60000).toISOString()};
 const options={root,scope:{owner:'owner'},call:async(_op,body)=>{reads++;const content=bytes.subarray(body.offset,body.offset+body.length);return{offset:body.offset,data_base64:content.toString('base64'),sha256:hash(content)};}};
 const bounded=new ISCPObjectClient({...options,diskQuota:10000});await assert.rejects(bounded.download(object),/quota/);
 const resumed=new ISCPObjectClient({...options,diskQuota:30000});assert.deepEqual(await resumed.download(object),bytes);assert.equal(reads,4);
 for(const name of fs.readdirSync(root).filter(name=>name.endsWith('.json'))){const filename=path.join(root,name),row=JSON.parse(fs.readFileSync(filename));row.expires_at=new Date(0).toISOString();fs.writeFileSync(filename,JSON.stringify(row));}
 new ISCPObjectClient(options);assert.deepEqual(fs.readdirSync(root),[]);
});

test('a released immutable object starts a fresh transfer for a later explicit upload',async t=>{
 const root=fs.mkdtempSync(path.join(os.tmpdir(),'iscp-released-'));t.after(()=>fs.rmSync(root,{recursive:true,force:true}));
 const bytes=Buffer.alloc(44),ids=[];let version=0;
 const client=new ISCPObjectClient({root,scope:{owner:'owner'},call:async(op,body)=>{if(op==='transfer.open'){ids.push(body.transfer_id);return{};}if(op==='object.describe'){assert.deepEqual(Object.keys(body).sort(),['object_id','version']);throw Object.assign(new Error('released'),{status:404});}if(op==='transfer.commit')return{object:{object_id:'audio',version:++version,size:bytes.length,sha256:hash(bytes)}};return{};}});
 await client.upload(bytes,{purpose:'speech_recording'});await client.upload(bytes,{purpose:'speech_recording'});
 assert.equal(ids.length,2);assert.notEqual(ids[0],ids[1]);
});
