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
