import test from 'node:test';import assert from 'node:assert/strict';import { ISCPSpeechClient } from '../src/main/iscp-audio-client.mjs';
test('audio IPC bounds credits and sequence; reconnect does not reopen a microphone session',async()=>{
 let release;const calls=[];const auth={generation:1,descriptor:{transport:'iscp'},status:{state:'connected',capabilities:{surfaces:{speech_realtime:{enabled:true}}}},invokeISCP:async(op,body)=>{calls.push({op,body});if(op.endsWith('.open'))return{session_id:'speech',ready:{event:'ready',protocol:'sparkclaw.speech.realtime.v1',format:{sample_rate:16000,channels:1,bits_per_sample:16,frame_ms:100},limits:{max_frame_samples:1600,max_audio_seconds:60}}};if(op.endsWith('.frame'))return new Promise(resolve=>{release=resolve;});return{};}};const client=new ISCPSpeechClient(auth);
 await client.dispatch({action:'open',session_id:'conversation',request_id:'recording',language:'auto'});
 await assert.rejects(client.dispatch({action:'frame',session_id:'speech',sequence:2,bytes:new Uint8Array(3200)}),/sequence/);
 const frame=client.dispatch({action:'frame',session_id:'speech',sequence:1,bytes:new Uint8Array(3200)});
 await assert.rejects(client.dispatch({action:'finish',session_id:'speech',last_sequence:1,total_samples:1600,reason:'manual_stop'}),/finish/);
 release({accepted_sequence:1});await frame;auth.generation++;await assert.rejects(client.dispatch({action:'events',session_id:'speech',after:0}),/unavailable/);
 assert.equal(calls.filter(call=>call.op==='speech.session.open').length,1);
});
