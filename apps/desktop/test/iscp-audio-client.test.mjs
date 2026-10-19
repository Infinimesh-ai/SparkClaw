import test from 'node:test';
import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { ISCPSpeechClient } from '../src/main/iscp-audio-client.mjs';

const ready = {event:'ready',protocol:'sparkclaw.speech.realtime.v1',format:{sample_rate:16000,channels:1,bits_per_sample:16,frame_ms:100},limits:{max_frame_samples:1600,max_audio_seconds:60}};
const open = {action:'open',session_id:'conversation',request_id:'recording',language:'auto'};
const finish = {action:'finish',session_id:'speech',last_sequence:1,total_samples:1600,reason:'manual_stop'};
const frame = {action:'frame',session_id:'speech',sequence:1,bytes:new Uint8Array(3200)};
function fixture(t) {
 const root=fs.mkdtempSync(path.join(os.tmpdir(),'iscp-speech-'));t.after(()=>fs.rmSync(root,{recursive:true,force:true}));
 const key=crypto.randomBytes(32);
 const protector={
  sealMutationRecord(row){const iv=crypto.randomBytes(12),cipher=crypto.createCipheriv('aes-256-gcm',key,iv);const bytes=Buffer.concat([cipher.update(JSON.stringify(row)),cipher.final()]);return Buffer.concat([iv,cipher.getAuthTag(),bytes]);},
  openMutationRecord(raw){const decipher=crypto.createDecipheriv('aes-256-gcm',key,raw.subarray(0,12));decipher.setAuthTag(raw.subarray(12,28));return JSON.parse(Buffer.concat([decipher.update(raw.subarray(28)),decipher.final()]));}
 };
 const calls=[];
 const auth={generation:1,descriptor:{transport:'iscp'},transport:{objectScopeKey:'original-binding'},status:{state:'connected',authorization_revision:1,capabilities:{surfaces:{speech_realtime:{enabled:true}}}},invokeISCP:async(op,body,options)=>{calls.push({op,body,options});if(op.endsWith('.open'))return{session_id:'speech',ready};if(op.endsWith('.frame'))return{accepted_sequence:body.sequence};if(op.endsWith('.events'))return{session_id:'speech',events:[],closed:false};return{};}};
 const options={root,protector};return{root,protector,auth,calls,options,client:new ISCPSpeechClient(auth,options)};
}
test('audio IPC bounds credits and sequence; a replaced generation never resends PCM',async t=>{
 const f=fixture(t);let release;const invoke=f.auth.invokeISCP;
 f.auth.invokeISCP=(op,body,options)=>op.endsWith('.frame')?new Promise(resolve=>{release=resolve;}):invoke(op,body,options);
 await f.client.dispatch(open);
 await assert.rejects(f.client.dispatch({...frame,sequence:2}),/sequence/);
 const sending=f.client.dispatch(frame);
 await assert.rejects(f.client.dispatch(finish),/finish/);
 release({accepted_sequence:1});await sending;
 f.auth.generation++;await assert.rejects(f.client.dispatch({...frame,sequence:2}),/sequence/);
 assert.equal(f.calls.filter(call=>call.op==='speech.session.open').length,1);
});
test('lost finish reconciles its original ID; terminal survives cancel and main restart encrypted',async t=>{
 const f=fixture(t);await f.client.dispatch(open);await f.client.dispatch(frame);
 const invoke=f.auth.invokeISCP;let finishID;
 f.auth.invokeISCP=async(op,body,options)=>{if(op==='speech.session.finish'){finishID=options.operationID;f.calls.push({op,body,options});throw new Error('reply lost');}
 if(op==='operations.receipt'){assert.equal(options.params.operation_id,finishID);return{operation_id:finishID,state:'completed',response:{status:200,body:{finishing:true}}};}
 if(op==='speech.session.events')return{session_id:'speech',events:[{sequence:1,event:{event:'final',revision:1,text:'private final transcript'}}],closed:true};
 return invoke(op,body,options);};
 await assert.rejects(f.client.dispatch(finish),/reply lost/);
 f.auth.generation++;assert.deepEqual(await f.client.dispatch(finish),{finishing:true});
 const result=await f.client.dispatch({action:'events',session_id:'speech',after:0});
 await f.client.dispatch({action:'cancel',session_id:'speech'});
 f.client.close();
 const restarted=new ISCPSpeechClient(f.auth,f.options);
 f.auth.invokeISCP=()=>{throw new Error('cached terminal must not make a network call');};
 assert.deepEqual(await restarted.dispatch({action:'events',session_id:'speech',after:0}),result);
 assert.equal(f.calls.filter(call=>call.op==='speech.session.finish').length,1);
 for(const file of fs.readdirSync(f.root))assert.equal(fs.readFileSync(path.join(f.root,file)).includes(Buffer.from('private final transcript')),false);
 f.auth.status.authorization_revision=2;
 await assert.rejects(restarted.dispatch({action:'events',session_id:'speech',after:0}),/unavailable/);
});
test('lost open followed by missing receipt does not open another provider stream',async t=>{
 const f=fixture(t);let id,opens=0,lookups=0;
 f.auth.invokeISCP=async(op,body,options)=>{if(op==='speech.session.open'){opens++;id=options.operationID;throw new Error('lost');}
 if(op==='operations.receipt'){lookups++;assert.equal(options.params.operation_id,id);throw Object.assign(new Error('missing'),{status:404});}
 throw new Error('unexpected');};
 await assert.rejects(f.client.dispatch(open),/lost/);
 const restarted=new ISCPSpeechClient(f.auth,f.options);
 await assert.rejects(restarted.dispatch(open),/missing/);
 await assert.rejects(restarted.dispatch(open),/missing/);
 assert.equal(opens,1);assert.equal(lookups,2);
});
test('cancel keeps its original operation when its response is lost',async t=>{
 const f=fixture(t);await f.client.dispatch(open);let id,count=0;
 f.auth.invokeISCP=async(op,body,options)=>{if(op==='speech.session.cancel'){count++;id=options.operationID;throw new Error('lost cancel');}
 assert.equal(op,'operations.receipt');assert.equal(options.params.operation_id,id);return{operation_id:id,state:'completed',response:{status:200,body:{cancelled:true}}};};
 await assert.rejects(f.client.dispatch({action:'cancel',session_id:'speech'}),/lost cancel/);
 assert.deepEqual(await new ISCPSpeechClient(f.auth,f.options).dispatch({action:'cancel',session_id:'speech'}),{cancelled:true});
 assert.equal(count,1);
});
test('unavailable secure persistence cannot start a provider operation',async t=>{
 const f=fixture(t);f.protector.sealMutationRecord=()=>{throw new Error('keychain locked');};
 await assert.rejects(f.client.dispatch(open),/keychain locked/);assert.equal(f.calls.length,0);
});

test('a receipt for another operation cannot settle a lost finish',async t=>{
 const f=fixture(t);await f.client.dispatch(open);await f.client.dispatch(frame);
 f.auth.invokeISCP=async op=>{if(op==='speech.session.finish')throw new Error('lost');return{operation_id:crypto.randomUUID(),state:'completed',response:{status:200,body:{finishing:true}}};};
 await assert.rejects(f.client.dispatch(finish),/lost/);
 await assert.rejects(f.client.dispatch(finish),/unknown/);
 const row=f.client.journal.find('speech');assert.equal(row.finish.result,undefined);
});

test('close always aborts and clears live streams when the keychain becomes unavailable',async t=>{
 const f=fixture(t);await f.client.dispatch(open);const state=f.client.sessions.get('speech');
 f.protector.sealMutationRecord=()=>{throw new Error('disk unavailable');};
 f.protector.openMutationRecord=()=>{throw new Error('keychain unavailable');};
 assert.doesNotThrow(()=>f.client.close());assert.equal(state.abort.signal.aborted,true);assert.equal(f.client.sessions.size,0);
 await assert.rejects(f.client.dispatch(open),/keychain unavailable/);
 assert.equal(f.calls.filter(call=>call.op==='speech.session.open').length,1);
});
