import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { ISCPRecordingClient } from '../src/main/iscp-recording-client.mjs';
import { ISCPMutationJournal } from '../src/main/iscp-mutation-journal.mjs';
const session='11111111-1111-4111-8111-111111111111',requestID='voice-22222222-2222-4222-8222-222222222222';
const request={session_id:session,request_id:requestID,language:'auto',bytes:new Uint8Array(44)};
function fixture(t){
 const root=fs.mkdtempSync(path.join(os.tmpdir(),'iscp-recording-'));t.after(()=>fs.rmSync(root,{recursive:true,force:true}));
 const calls=[];const receipts=new Map();let uploads=0;
 const auth={generation:1,descriptor:{transport:'iscp'},status:{state:'connected',capabilities:{speech:true}},transport:{capabilities:{authorization_revision:1},mutations:new ISCPMutationJournal(root,{owner:'owner'}),objects:{upload:async()=>{uploads++;return{object_id:'audio',version:1};}}},invokeISCP:async(op,body,options)=>{calls.push({op,body,options});if(op==='operations.receipt'){if(receipts.has(options.params.operation_id))return receipts.get(options.params.operation_id);throw Object.assign(new Error('missing'),{status:404});}if(op==='speech.transcribe'){const result={text:'original',request_id:requestID,session_id:session,audio_retained:true};receipts.set(options.operationID,{operation_id:options.operationID,state:'completed',response:{status:200,body:result}});return result;}return{cancelled:true};}};
 return{root,auth,calls,receipts,get uploads(){return uploads;}};
}
test('recorded transcription retains operation identity, audio-release evidence and exact input across restart',async t=>{
 const f=fixture(t);const first=new ISCPRecordingClient(f.auth);
 assert.equal((await first.transcribe(request)).audio_retained,false);
 f.auth.transport.mutations=new ISCPMutationJournal(f.root,{owner:'owner'});
 const restarted=new ISCPRecordingClient(f.auth);
 assert.equal((await restarted.transcribe(request)).audio_retained,false);
 assert.equal(f.calls.filter(call=>call.op==='speech.transcribe').length,1);assert.equal(f.uploads,1);
 await assert.rejects(restarted.transcribe({...request,bytes:new Uint8Array(44).fill(1)}),/different audio/);
 assert.equal(f.calls.filter(call=>call.op==='speech.transcribe').length,1);
});
test('unknown transcription receipt never uploads or invokes the provider again',async t=>{
 const f=fixture(t);await new ISCPRecordingClient(f.auth).transcribe(request);
 const operation=f.calls.find(call=>call.op==='speech.transcribe').options.operationID;
 f.receipts.set(operation,{operation_id:operation,state:'unknown'});
 await assert.rejects(new ISCPRecordingClient(f.auth).transcribe(request),/unknown/);
 assert.equal(f.uploads,1);assert.equal(f.calls.filter(call=>call.op==='speech.transcribe').length,1);
});
test('cancel aborts the original provider call and uses only its scoped recording identity',async t=>{
 const f=fixture(t);const original=f.auth.invokeISCP;let started;
 const ready=new Promise(resolve=>{started=resolve;});
 f.auth.invokeISCP=async(op,body,options)=>{if(op==='speech.transcribe'){started();return new Promise((_resolve,reject)=>options.signal.addEventListener('abort',()=>reject(new Error('cancelled')),{once:true}));}return original(op,body,options);};
 const client=new ISCPRecordingClient(f.auth);const pending=client.transcribe(request);const rejected=assert.rejects(pending,/cancelled/);await ready;
 await client.cancel({session_id:session,request_id:requestID});await rejected;
 assert.deepEqual(f.calls.find(call=>call.op==='speech.cancel').body,{session_id:session,request_id:requestID});
 assert.equal(client.active.size,0);
});
