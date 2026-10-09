import { EventEmitter } from 'node:events';

// An authenticated ISCP control channel with the existing restricted host
// protocol. This adapter never constructs a URL or opens a WebSocket.
export async function connectISCPHostChannel(auth, headers, {signal}={}) {
 if(auth.status.capabilities?.browser!==true||auth.status.state!=='connected')throw new Error('Browser capability is unavailable');
 const generation=auth.generation;
 const host_id=headers['X-SparkClaw-Host-ID'];
 const result=await auth.invokeISCP('browser.host.register',{host_id,grant_token:headers['X-SparkClaw-Host-Grant'],runtime_generation:headers['X-SparkClaw-Runtime']},{installationID:headers['X-SparkClaw-Installation'],signal});
 const welcome=result.welcome??result;
 if(!welcome.connection_epoch)throw new Error('Browser host registration is invalid');
 const channel=new EventEmitter();let closed=false,cursor=0,polling=false,timer;
 const binding={host_id,connection_epoch:welcome.connection_epoch};
 const current=()=>!closed&&generation===auth.generation&&auth.status.state==='connected'&&auth.status.capabilities?.browser===true&&!signal?.aborted;
 channel.close=()=>{if(closed)return;closed=true;clearTimeout(timer);signal?.removeEventListener('abort',channel.close);void auth.invokeISCP('browser.host.close',binding).catch(()=>{});channel.emit('close');};
 channel.send=(message)=>{if(!current())throw new Error('Browser host is disconnected');
  const operation=message.type==='heartbeat'?'browser.host.heartbeat':'browser.host.reply';
  if(!['heartbeat','result'].includes(message.type))throw new Error('Browser host message is invalid');
  void auth.invokeISCP(operation,{...binding,message}).catch(()=>channel.close());
 };
 const poll=async()=>{if(!current()){channel.close();return;}if(polling)return;polling=true;
  try {const result=await auth.invokeISCP('browser.host.poll',{...binding,after:cursor},{signal});
   if(!current())return;if(!Array.isArray(result.messages)||result.messages.length>64)throw new Error('Browser queue exceeds bounds');
   for(const item of result.messages){if(!Number.isSafeInteger(item.sequence)||item.sequence<1)throw new Error('Browser sequence is invalid');if(item.sequence<=cursor)continue;if(item.sequence!==cursor+1)throw new Error('Browser command gap');channel.emit('message',item.body);cursor=item.sequence;}
  }catch{channel.close();}finally{polling=false;if(!closed)timer=setTimeout(()=>void poll(),1000);}
 };
 signal?.addEventListener('abort',channel.close,{once:true});
 timer=setTimeout(()=>{if(!current()){channel.close();return;}void poll();},0);
 return channel;
}
