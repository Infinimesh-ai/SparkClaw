import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import crypto from 'node:crypto';
import { LinkRecordingClient } from '../src/main/link-recording-client.mjs';
const session='11111111-1111-4111-8111-111111111111',requestID='voice-22222222-2222-4222-8222-222222222222';
const request={session_id:session,request_id:requestID,language:'auto',bytes:new Uint8Array(44)};
function fixture(t){
 const root=fs.mkdtempSync(path.join(os.tmpdir(),'link-recording-'));t.after(()=>fs.rmSync(root,{recursive:true,force:true}));
 const calls=[];const receipts=new Map();let uploads=0;
 const key=crypto.randomBytes(32);
 const protector={sealMutationRecord(row){const iv=crypto.randomBytes(12),cipher=crypto.createCipheriv('aes-256-gcm',key,iv);const ct=Buffer.concat([cipher.update(JSON.stringify(row)),cipher.final()]);return Buffer.concat([iv,cipher.getAuthTag(),ct]);},openMutationRecord(raw){const cipher=crypto.createDecipheriv('aes-256-gcm',key,raw.subarray(0,12));cipher.setAuthTag(raw.subarray(12,28));return JSON.parse(Buffer.concat([cipher.update(raw.subarray(28)),cipher.final()]));}};
 const transcribe=(body,options)=>{const result={text:'original',request_id:requestID,session_id:session,audio_retained:true};receipts.set(options.operationID,{operation_id:options.operationID,state:'completed',response:{status:200,body:result},body});return result;};
 const auth={generation:1,userDataDirectory:root,defaultVault:protector,descriptor:{transport:'link'},status:{state:'connected',authorization_revision:1,capabilities:{speech:true}},
  transport:{objectScopeKey:'original-binding',capabilities:{authorization_revision:1},objects:{upload:async()=>{uploads++;return{object_id:'33333333-3333-4333-8333-333333333333',version:1,purpose:'speech_recording'};}}},
  invokeDomain:async(op,body,options)=>{calls.push({op,body,options});if(op==='speech.transcribe')return transcribe(body,options);return{cancelled:true};},
  // The backend's receipt of the original call (replay_only): never runs it.
  replayDomain:async(op,body,options)=>{calls.push({op,body,options,replay:true});const receipt=receipts.get(options.operationID);
   if(!receipt)throw Object.assign(new Error('missing'),{code:'operation_not_found'});
   if(receipt.state!=='completed')throw Object.assign(new Error('unknown'),{code:'operation_outcome_unknown'});
   assert.deepEqual(body,receipt.body,'the replay carries the original request');return{operation_id:receipt.operation_id,state:'completed',response:receipt.response};}};
 return{root,auth,protector,calls,receipts,get uploads(){return uploads;}};
}
test('recorded transcription retains operation identity and exact input across restart',async t=>{
 const f=fixture(t);const first=new LinkRecordingClient(f.auth);
 assert.equal((await first.transcribe(request)).text,'original');
 const restarted=new LinkRecordingClient(f.auth);
 assert.equal((await restarted.transcribe(request)).text,'original');
 assert.equal(f.calls.filter(call=>call.op==='object.release').length,0,'uploaded audio is a link domain object that expires on its own');
 assert.equal(f.calls.filter(call=>call.op==='speech.transcribe'&&!call.replay).length,1);assert.equal(f.uploads,1);
 await assert.rejects(restarted.transcribe({...request,bytes:new Uint8Array(44).fill(1)}),/different audio/);
 assert.equal(f.calls.filter(call=>call.op==='speech.transcribe'&&!call.replay).length,1);
});
test('unknown transcription receipt never uploads or invokes the provider again',async t=>{
 const f=fixture(t);const invoke=f.auth.invokeDomain;f.auth.invokeDomain=async(...args)=>{const result=await invoke(...args);if(args[0]==='speech.transcribe')throw new Error('reply lost');return result;};
 await assert.rejects(new LinkRecordingClient(f.auth).transcribe(request),/reply lost/);
 const operation=f.calls.find(call=>call.op==='speech.transcribe'&&!call.replay).options.operationID;
 f.receipts.set(operation,{...f.receipts.get(operation),state:'unknown'});
 await assert.rejects(new LinkRecordingClient(f.auth).transcribe(request),/unknown|missing/);
 assert.equal(f.uploads,1);assert.equal(f.calls.filter(call=>call.op==='speech.transcribe'&&!call.replay).length,1);
});
test('cancel aborts the original provider call and uses only its scoped recording identity',async t=>{
 const f=fixture(t);const original=f.auth.invokeDomain;let started;
 const ready=new Promise(resolve=>{started=resolve;});
 f.auth.invokeDomain=async(op,body,options)=>{if(op==='speech.transcribe'){started();return new Promise((_resolve,reject)=>options.signal.addEventListener('abort',()=>reject(new Error('cancelled')),{once:true}));}return original(op,body,options);};
 const client=new LinkRecordingClient(f.auth);const pending=client.transcribe(request);const rejected=assert.rejects(pending,/cancelled/);await ready;
 await client.cancel({session_id:session,request_id:requestID});await rejected;
 assert.deepEqual(f.calls.find(call=>call.op==='speech.cancel').body,{session_id:session,request_id:requestID});
 assert.equal(client.active.size,0);
});

test('missing receipt after attempted transcription never uploads or starts a second provider call', async t => {
 const f=fixture(t);const invoke=f.auth.invokeDomain;f.auth.invokeDomain=async(...args)=>{const result=await invoke(...args);if(args[0]==='speech.transcribe')throw new Error('reply lost');return result;};
 await assert.rejects(new LinkRecordingClient(f.auth).transcribe(request),/reply lost/);
 f.receipts.clear();
 await assert.rejects(new LinkRecordingClient(f.auth).transcribe(request),/unknown|missing/);
 assert.equal(f.uploads,1);assert.equal(f.calls.filter(call=>call.op==='speech.transcribe'&&!call.replay).length,1);
});

const anchor={sessionId:session,draft:'private original draft',selectionStart:8,selectionEnd:8};
test('READY retrieves the original attempted recording, persists its terminal and exposes only its original session until ACK',async t=>{
 const f=fixture(t),invoke=f.auth.invokeDomain;
 f.auth.invokeDomain=async(...args)=>{const value=await invoke(...args);if(args[0]==='speech.transcribe')throw new Error('IPC lost');return value;};
 await assert.rejects(new LinkRecordingClient(f.auth).transcribe({...request,anchor}),/IPC lost/);
 const client=new LinkRecordingClient(f.auth);await client.start();
 const pending=await client.pending({session_id:session});assert.equal(pending.length,1);assert.deepEqual(pending[0].anchor,anchor);
 assert.equal(pending[0].result.text,'original');assert.equal(f.uploads,1);assert.equal(f.calls.filter(c=>c.op==='speech.transcribe'&&!c.replay).length,1);
 assert.deepEqual(await client.pending({session_id:'33333333-3333-4333-8333-333333333333'}),[]);
 f.receipts.clear();f.auth.invokeDomain=f.auth.replayDomain=()=>{throw new Error('saved result must not need the server');};
 const restarted=new LinkRecordingClient(f.auth);assert.deepEqual(await restarted.pending({session_id:session}),pending);
 for(const file of fs.readdirSync(path.join(f.root,'link-recordings')))assert.equal(fs.readFileSync(path.join(f.root,'link-recordings',file)).includes(Buffer.from(anchor.draft)),false);
 await assert.rejects(restarted.acknowledge({session_id:session,request_id:requestID,operation_id:crypto.randomUUID()}),/unknown/);
 await restarted.acknowledge({session_id:session,request_id:requestID,operation_id:pending[0].operation_id});
 assert.deepEqual(await restarted.pending({session_id:session}),[]);
 assert.equal((await restarted.transcribe({...request,anchor})).text,'original');
 f.auth.status.authorization_revision=2;assert.deepEqual(await restarted.pending({session_id:session}),[]);
});
test('late or mismatched original receipt cannot publish a recording terminal',async t=>{
 const f=fixture(t);f.auth.invokeDomain=async(op)=>{if(op==='speech.transcribe')throw new Error('lost');throw new Error('unexpected');};
 await assert.rejects(new LinkRecordingClient(f.auth).transcribe({...request,anchor}),/lost/);
 const client=new LinkRecordingClient(f.auth);let release;
 f.auth.replayDomain=async(_op,_body,options)=>new Promise(resolve=>{release=()=>resolve({operation_id:options.operationID,state:'completed',response:{status:200,body:{...request,bytes:undefined,text:'late'}}});});
 const pending=client.start();await Promise.resolve();f.auth.generation++;release();await pending;
 assert.equal(client.journal.entries()[0].response,undefined);
 f.auth.replayDomain=async()=>({operation_id:crypto.randomUUID(),state:'completed',response:{status:200,body:{session_id:session,request_id:requestID,text:'wrong'}}});
 await client.start();assert.equal(client.journal.entries()[0].response,undefined);assert.equal(f.uploads,1);
});
test('secure recording failure prevents provider work and close always aborts live work',async t=>{
 const f=fixture(t);f.protector.sealMutationRecord=()=>{throw new Error('keychain locked');};
 const client=new LinkRecordingClient(f.auth);await assert.rejects(client.transcribe({...request,anchor}),/keychain locked/);assert.equal(f.uploads,0);
 const abort=new AbortController();client.active.set('test',{abort});assert.doesNotThrow(()=>client.close());assert.equal(abort.signal.aborted,true);assert.equal(client.active.size,0);
});
