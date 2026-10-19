import crypto from 'node:crypto';
import path from 'node:path';
import { ISCPRecordingJournal } from './iscp-recording-journal.mjs';
const UUID=/^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/u;
const identity=request=>request&&UUID.test(request.session_id)&&request.request_id?.startsWith('voice-')&&UUID.test(request.request_id.slice(6));
const fingerprint=value=>crypto.createHash('sha256').update(JSON.stringify(value)).digest('hex');
const unknown=()=>new Error('Recording outcome is unknown; transcription will not be repeated');

// Only the original provider operation can settle a recording. Encrypted
// metadata/results survive restart; captured PCM is never persisted or replayed.
export class ISCPRecordingClient {
 constructor(auth,options={}){Object.assign(this,{auth,...options});this.active=new Map();this.flights=new Map();}
 close(){for(const value of this.active.values())value.abort.abort();this.active.clear();}
 #ready(){return this.auth.descriptor?.transport==='iscp'&&this.auth.status.state==='connected'&&this.auth.status.capabilities?.speech===true;}
 #storage(){
  const auth=this.auth,scope=this.getScope?.()??(auth.transport?.objectScopeKey&&Number.isSafeInteger(auth.status.authorization_revision)?JSON.stringify([auth.transport.objectScopeKey,auth.status.authorization_revision]):undefined);
  if(!scope)throw new Error('Recording data authority is unavailable');
  if(scope!==this.scope){this.close();this.journal=new ISCPRecordingJournal(this.root??path.join(auth.userDataDirectory||path.dirname(auth.descriptorPath),'iscp-recordings'),scope,this.protector??auth.defaultVault??auth.vault);this.scope=scope;}
  return this.journal;
 }
 #current(journal,generation){if(!this.#ready()||generation!==this.auth.generation||journal!==this.#storage())throw new Error('Recording authorization changed');}
 async #lookup(journal,key,generation){
  const flight=journal.scope+':'+key;
  if(this.flights.has(flight))return this.flights.get(flight);
  const run=(async()=>{
   const row=journal.read(key);if(row.response)return row;
   if(row.phase!=='attempted')throw unknown();
   const receipt=await this.auth.invokeISCP('operations.receipt',undefined,{params:{operation_id:row.operation_id}});
   this.#current(journal,generation);
   if(receipt?.operation_id!==row.operation_id||receipt.state!=='completed'||!Number.isInteger(receipt.response?.status)||receipt.response.status<200||receipt.response.status>599)throw unknown();
   if(receipt.response.status<400)validateResult(receipt.response.body,row);
   return journal.update(key,{response:receipt.response,phase:'terminal'});
  })().finally(()=>{if(this.flights.get(flight)===run)this.flights.delete(flight);});
  this.flights.set(flight,run);return run;
 }
 async start(){
  if(!this.#ready())return;
  const journal=this.#storage(),generation=this.auth.generation;
  for(const row of journal.entries()){
   this.#current(journal,generation);
   if(row.phase==='attempted'&&!this.active.has(row.key))await this.#lookup(journal,row.key,generation).catch(()=>{});
  }
 }
 async pending(request){
  if(!request||Object.keys(request).join()!=='session_id'||!UUID.test(request.session_id))throw new Error('Recording session is invalid');
  if(!this.#ready())return[];
  const journal=this.#storage(),generation=this.auth.generation;
  await this.start();this.#current(journal,generation);
  return journal.entries().filter(row=>row.session_id===request.session_id&&row.phase==='terminal'&&row.response?.status<400&&row.anchor)
   .map(row=>({operation_id:row.operation_id,session_id:row.session_id,request_id:row.request_id,anchor:row.anchor,result:this.#result(row)}));
 }
 async acknowledge(request){
  if(!identity(request)||Object.keys(request).sort().join()!=='operation_id,request_id,session_id'||!UUID.test(request.operation_id))throw new Error('Recording identity is invalid');
  if(!this.#ready())throw new Error('Recorded transcription is unavailable');
  const journal=this.#storage(),key=journal.key(request),row=journal.read(key);
  if(!row?.response||row.operation_id!==request.operation_id||!['terminal','applied'].includes(row.phase))throw unknown();
  journal.update(key,{phase:'applied'});return{acknowledged:true};
 }
 #result(row){
  if(row.response.status>=400)throw new Error(row.response.body?.error||'The original transcription failed');
  return{...validateResult(row.response.body,row),operation_id:row.operation_id,...(row.audio_released?{audio_retained:false}:{})};
 }
 async transcribe(request){
  const auth=this.auth;
  if(!this.#ready())throw new Error('Recorded transcription is unavailable');
  const advertised=auth.capabilityReport?.limits?.purpose_bytes?.speech_recording;
  const maxBytes=Number.isSafeInteger(advertised)&&advertised>0?Math.min(advertised,25*1024*1024):3*1024*1024;
  if(!identity(request)||!['bytes,language,request_id,session_id','anchor,bytes,language,request_id,session_id'].includes(Object.keys(request).sort().join())||!(request.bytes instanceof Uint8Array)||request.bytes.length<44||request.bytes.length>maxBytes||typeof request.language!=='string'||request.language.length>32)throw new Error('Recording is invalid');
  if(request.anchor!==undefined)validateAnchor(request.anchor,request.session_id);
  const journal=this.#storage(),key=journal.key(request),generation=auth.generation;
  if(this.active.size)throw new Error('Another recording is being transcribed');
  const metadata={session_id:request.session_id,request_id:request.request_id,language:request.language,size:request.bytes.length,sha256:crypto.createHash('sha256').update(request.bytes).digest('hex')};
  let row=journal.read(key);
  if(row&&row.fingerprint!==fingerprint(metadata))throw new Error('Recording identity was reused with different audio');
  if(!row){
   const legacy=auth.transport.mutations?.read('recording:'+request.session_id+':'+request.request_id);
   if(legacy&&!auth.transport.mutations.matches(legacy,metadata))throw new Error('Recording identity was reused with different audio');
   row=journal.update(key,{...metadata,fingerprint:fingerprint(metadata),operation_id:legacy?.operation_id??crypto.randomUUID(),phase:legacy?'attempted':'prepared',not_after:Date.now()+(auth.transport.timeoutMS||30000),...(request.anchor?{anchor:request.anchor}:{}),audio_released:legacy?.audio_released===true});
  }
  const abort=new AbortController(),state={abort,generation,provider:false,request};this.active.set(key,state);
  try{
   if(row.response)return this.#result(row);
   if(row.phase==='attempted')return this.#result(await this.#lookup(journal,key,generation));
   if(row.phase!=='prepared'||Date.now()>=row.not_after)throw new Error('Original recording expired before submission');
   const object=await auth.transport.objects.upload(request.bytes,{purpose:'speech_recording',name:'recording.wav',media_type:'audio/wav'},abort.signal);state.object=object;
   abort.signal.throwIfAborted();this.#current(journal,generation);
   if(Date.now()>=row.not_after)throw new Error('Original recording expired before submission');
   journal.update(key,{phase:'attempted',object});state.provider=true;
   const result=await auth.invokeISCP('speech.transcribe',{session_id:request.session_id,request_id:request.request_id,language:request.language,audio_object:object},{operationID:row.operation_id,signal:abort.signal});
   this.#current(journal,generation);validateResult(result,request);
   row=journal.update(key,{phase:'terminal',response:{status:200,body:result}});
   abort.signal.throwIfAborted();
   try{await auth.invokeISCP('object.release',{object_id:object.object_id,version:object.version});this.#current(journal,generation);row=journal.update(key,{audio_released:true});}catch{/* Preserve terminal receipt even when best-effort audio release fails. */}
   this.#current(journal,generation);return this.#result(row);
  }finally{if(this.active.get(key)===state)this.active.delete(key);}
 }
 async cancel(request){
  if(!identity(request)||Object.keys(request).sort().join()!=='request_id,session_id')throw new Error('Recording identity is invalid');
  const state=this.active.get(this.journal?.key(request));if(!state)return{cancelled:true};
  state.abort.abort();
  if(state.generation!==this.auth.generation)return{cancelled:true};
  const reply=state.provider?await this.auth.invokeISCP('speech.cancel',request):{cancelled:true};
  if(state.object)await this.auth.invokeISCP('object.release',{object_id:state.object.object_id,version:state.object.version}).catch(()=>{});
  return reply;
 }
}
function validateResult(result,request){if(!result||result.session_id!==request.session_id||result.request_id!==request.request_id||typeof result.text!=='string')throw new Error('Transcription result identity differs');return result;}
function validateAnchor(anchor,sessionID){if(!anchor||Object.keys(anchor).sort().join()!=='draft,selectionEnd,selectionStart,sessionId'||anchor.sessionId!==sessionID||typeof anchor.draft!=='string'||anchor.draft.length>1<<20||!Number.isInteger(anchor.selectionStart)||!Number.isInteger(anchor.selectionEnd)||anchor.selectionStart<0||anchor.selectionEnd<anchor.selectionStart||anchor.selectionEnd>anchor.draft.length)throw new Error('Recording draft anchor is invalid');}
