import crypto from 'node:crypto';
const UUID=/^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/u;
const identity=request=>request&&UUID.test(request.session_id)&&request.request_id?.startsWith('voice-')&&UUID.test(request.request_id.slice(6));

// A recording retains one operation identity across explicit retries and process
// restarts. Only a missing receipt may start transcription; unknown never does.
export class ISCPRecordingClient {
 constructor(auth){this.auth=auth;this.active=new Map();}
 close(){for(const value of this.active.values())value.abort.abort();this.active.clear();}
 async transcribe(request){
  const auth=this.auth;
  if(auth.descriptor?.transport!=='iscp'||auth.status.state!=='connected'||auth.status.capabilities?.speech!==true)throw new Error('Recorded transcription is unavailable');
  const advertised=auth.capabilityReport?.limits?.purpose_bytes?.speech_recording;
  const maxBytes=Number.isSafeInteger(advertised)&&advertised>0?Math.min(advertised,25*1024*1024):3*1024*1024;
  if(!identity(request)||Object.keys(request).sort().join()!=='bytes,language,request_id,session_id'||!(request.bytes instanceof Uint8Array)||request.bytes.length<44||request.bytes.length>maxBytes||typeof request.language!=='string'||request.language.length>32)throw new Error('Recording is invalid');
  if(this.active.size)throw new Error('Another recording is being transcribed');
  const key=request.session_id+':'+request.request_id,abort=new AbortController(),generation=auth.generation;
  const state={abort,generation,provider:false,request};
  const resource='recording:'+key,journal=auth.transport.mutations;
  if(!journal){this.active.delete(key);throw new Error('Durable recording recovery is unavailable');}
  const identityRecord={session_id:request.session_id,request_id:request.request_id,language:request.language,size:request.bytes.length,sha256:crypto.createHash('sha256').update(request.bytes).digest('hex')};
  const previous=journal.read(resource);
  if(previous&&!journal.matches(previous,identityRecord)){this.active.delete(key);throw new Error('Recording identity was reused with different audio');}
  const record=previous||journal.begin(resource,identityRecord),operationID=record.operation_id;
  this.active.set(key,state);
  try{
   let receipt;
   try{receipt=await auth.invokeISCP('operations.receipt',undefined,{params:{operation_id:operationID},signal:abort.signal});}
   catch(error){if(error.status!==404)throw error;}
   abort.signal.throwIfAborted();
   if(receipt){if(receipt.operation_id!==operationID||receipt.state!=='completed'||!receipt.response)throw new Error('Recording outcome is unknown; transcription will not be repeated');if(receipt.response.status>=400)throw new Error(receipt.response.body?.error||'The original transcription failed');return {...validateResult(receipt.response.body,request),...(record.audio_released?{audio_retained:false}:{})};}
   const object=await auth.transport.objects.upload(request.bytes,{purpose:'speech_recording',name:'recording.wav',media_type:'audio/wav'},abort.signal);state.object=object;
   abort.signal.throwIfAborted();if(generation!==auth.generation)throw new Error('Recording authorization changed');
   state.provider=true;
   const result=await auth.invokeISCP('speech.transcribe',{session_id:request.session_id,request_id:request.request_id,language:request.language,audio_object:object},{operationID,signal:abort.signal});
   abort.signal.throwIfAborted();validateResult(result,request);
   try{await auth.invokeISCP('object.release',{object_id:object.object_id,version:object.version});journal.markReleased(resource,operationID);return {...result,audio_retained:false};}
   catch{return {...result,audio_retained:true,audio_expires_at:object.expires_at};}
  }finally{if(this.active.get(key)===state)this.active.delete(key);}
 }
 async cancel(request){
  if(!identity(request)||Object.keys(request).sort().join()!=='request_id,session_id')throw new Error('Recording identity is invalid');
  const state=this.active.get(request.session_id+':'+request.request_id);if(!state)return{cancelled:true};
  state.abort.abort();
  if(state.generation!==this.auth.generation)return{cancelled:true};
  const reply=state.provider?await this.auth.invokeISCP('speech.cancel',request):{cancelled:true};
  if(state.object)await this.auth.invokeISCP('object.release',{object_id:state.object.object_id,version:state.object.version}).catch(()=>{});
  return reply;
 }
}

function validateResult(result,request){if(!result||result.session_id!==request.session_id||result.request_id!==request.request_id||typeof result.text!=='string')throw new Error('Transcription result identity differs');return result;}
