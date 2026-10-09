const ID=/^[A-Za-z0-9][A-Za-z0-9_.:-]{0,159}$/u;
// Trusted-main IPC keeps audio windows, operation names and authentication out
// of page scripts. Disconnect destroys the session; a new one needs open again.
export class ISCPSpeechClient {
 constructor(auth){this.auth=auth;this.sessions=new Map();}
 close(){this.openingAbort?.abort();this.opening=false;for(const value of this.sessions.values())value.abort.abort();this.sessions.clear();}
 async dispatch(request){
  const {auth}=this;
  if([...this.sessions.values()].some(value=>value.generation!==auth.generation))this.close();
  if(auth.descriptor?.transport!=='iscp'||auth.status.state!=='connected'||auth.status.capabilities?.surfaces?.speech_realtime?.enabled!==true)throw new Error('Realtime speech is unavailable');
  if(!request||typeof request!=='object'||!['open','frame','events','finish','cancel'].includes(request.action))throw new Error('Invalid speech operation');
  if(request.action==='open'){
   if(this.sessions.size||this.opening)throw new Error('Another microphone session is active');
   if(!ID.test(request.request_id)||!ID.test(request.session_id)||typeof request.language!=='string'||request.language.length>32)throw new Error('Invalid speech session identity');
   const generation=auth.generation,abort=new AbortController();this.openingAbort=abort;this.opening=true;
   let result;try{result=await auth.invokeISCP('speech.session.open',{session_id:request.session_id,request_id:request.request_id,language:request.language},{signal:abort.signal});}finally{this.opening=false;}
   if(generation!==auth.generation||!ID.test(result.session_id))throw new Error('Speech authorization changed');
   this.sessions.set(result.session_id,{generation,abort,inflight:0,next:1,samples:0,finished:false});return result;
  }
  const state=this.sessions.get(request.session_id);
  if(!state||state.generation!==auth.generation)throw new Error('Speech session is unavailable');
  const common={session_id:request.session_id},options={signal:state.abort.signal};
  if(request.action==='cancel'){this.sessions.delete(request.session_id);state.abort.abort();return auth.invokeISCP('speech.session.cancel',common);}
  if(request.action==='frame'){
   if(state.finished||state.inflight>=2||!(request.bytes instanceof Uint8Array)||request.bytes.length<2||request.bytes.length>3200||request.bytes.length%2||request.sequence!==state.next||state.samples+request.bytes.length/2>16000*300)throw new Error('Speech sequence or credit window exceeded');
   state.inflight++;state.next++;state.samples+=request.bytes.length/2;
   try{return await auth.invokeISCP('speech.session.frame',{...common,sequence:request.sequence,pcm16:Buffer.from(request.bytes).toString('base64')},options);}
   catch(error){this.sessions.delete(request.session_id);state.abort.abort();throw error;}finally{state.inflight--;}
  }
  if(request.action==='events'){
   if(!Number.isSafeInteger(request.after)||request.after<0)throw new Error('Speech event cursor is invalid');
   return auth.invokeISCP('speech.session.events',{...common,after:request.after},options);
  }
  if(state.inflight||state.finished||request.last_sequence!==state.next-1||request.total_samples!==state.samples||!['manual_stop','silence_stop','max_duration'].includes(request.reason))throw new Error('Speech finish does not cover captured audio');
  state.finished=true;
  return auth.invokeISCP('speech.session.finish',{...common,last_sequence:request.last_sequence,total_samples:request.total_samples,reason:request.reason},options);
 }
}
