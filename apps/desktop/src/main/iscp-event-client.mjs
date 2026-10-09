// Events are invalidations. Durable execution lookup remains authoritative and
// is the only path that commits a result or acknowledges delivery.
export class ISCPEventClient {
 constructor({auth,store,execution,getIdentity,onEvents=()=>{},intervalMS=2000}){Object.assign(this,{auth,store,execution,getIdentity,onEvents,intervalMS});this.generation=0;}
 start(){if(this.timer)return;this.closed=false;this.timer=setInterval(()=>void this.poll().catch(()=>{}),this.intervalMS);this.timer.unref?.();void this.poll().catch(()=>{});}
 close(){this.closed=true;this.generation++;clearInterval(this.timer);this.timer=undefined;this.controller?.abort();}
 async poll(){
  if(this.closed||this.polling||this.auth.status.state!=='connected'||this.auth.status.capabilities?.events!==true)return;
  const scope=this.getIdentity();if(!scope)return;this.polling=true;const generation=this.generation,authGeneration=this.auth.generation;const controller=new AbortController();this.controller=controller;
  const same=()=>{if(this.closed||generation!==this.generation||authGeneration!==this.auth.generation)throw new Error('Event authentication changed');};
  try {
   let previous=this.store.eventProjection(scope),reply;
   const snapshot=()=>this.auth.invokeISCP('events.snapshot',{request_ids:this.store.pending(scope).slice(0,100).map(row=>row.request_id)},{signal:controller.signal});
   if(!previous.cursor)reply=await snapshot();
   else try{reply=await this.auth.invokeISCP('events.pull',{cursor:previous.cursor,limit:100},{signal:controller.signal});}
   catch(error){if(!['cursor_gap','cursor_invalid','cursor_expired','snapshot_required'].includes(error.code))throw error;reply=await snapshot();}
   same();
   if(typeof reply.cursor!=='string'||!reply.cursor||reply.cursor.length>4096||!Number.isSafeInteger(reply.revision)||reply.revision<1||!Array.isArray(reply.events??[])||(reply.events??[]).length>100)throw new Error('Invalid event response');
   const events=reply.events??[];
   let sequence=previous.revision;
   for(const event of events){if(!Number.isSafeInteger(event.sequence)||event.sequence<=sequence||typeof event.category!=='string'||!['tasks','approvals','notifications','settings','email'].includes(event.category))throw new Error('Invalid event sequence');sequence=event.sequence;}
   if(events.length&&!reply.snapshot)throw new Error('Event changed without an authoritative snapshot');
   this.store.commitEventProjection(scope,{previous_cursor:previous.cursor,cursor:reply.cursor,revision:reply.revision,snapshot:reply.snapshot??previous.snapshot});
   same();
   await this.auth.invokeISCP('events.ack',{cursor:reply.cursor},{signal:controller.signal});
   same();
   if(reply.snapshot){await this.execution.reconcilePending();same();this.onEvents({categories:[...new Set(events.length?events.map(event=>event.category):['tasks','approvals','notifications','settings','email'])]});}
  }finally{this.polling=false;if(this.controller===controller)this.controller=undefined;}
 }
}
