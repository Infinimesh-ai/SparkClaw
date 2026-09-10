// Outlook binds Worker.postMessage before an asynchronous userscript may load.
// The Controller installs this read-only hook at document creation in its owned
// tab. The managed Reader remains the only provider of list/download operations.
export function installOutlookEarlyBridge() {
  if(window.top!==window || !['https://outlook.live.com','https://outlook.office.com','https://outlook.office365.com'].includes(location.origin))return null;
  if(window.SparkClawOutlookEarlyBridge)return window.SparkClawOutlookEarlyBridge;
  const NativeWorker=window.Worker,NativeChannel=window.MessageChannel;
  if(!NativeWorker||!NativeChannel)return null;
  const queue=[],listeners=[];let consumer=null;
  const observe=(worker,message)=>{
    const operation=message?.argumentList?.[0]?.value?.operationName;
    if(!['ItemRows','ConversationRows','ItemExport'].includes(operation))return;
    const item={worker,message:structuredClone(message)};
    if(consumer)consumer.request(item.worker,item.message);
    else {queue.push(item);if(queue.length>40)queue.shift();}
  };
  const Worker=class extends NativeWorker {postMessage(message,...rest){try{observe(this,message);}catch{}return super.postMessage(message,...rest);}};
  const Channel=class extends NativeChannel {constructor(){super();for(const port of [this.port1,this.port2]){
    const listener=event=>consumer?.result(event,port);port.addEventListener('message',listener);listeners.push([port,listener]);
  }}};
  window.Worker=Worker;window.MessageChannel=Channel;
  const bridge=Object.freeze({attach(next){consumer=next;for(const item of queue.splice(0))consumer.request(item.worker,item.message);},detach(next){if(consumer===next)consumer=null;},dispose(){consumer=null;queue.length=0;for(const [port,listener]of listeners)port.removeEventListener('message',listener);listeners.length=0;if(window.Worker===Worker)window.Worker=NativeWorker;if(window.MessageChannel===Channel)window.MessageChannel=NativeChannel;delete window.SparkClawOutlookEarlyBridge;}});
  Object.defineProperty(window,'SparkClawOutlookEarlyBridge',{configurable:true,value:bridge});
  return bridge;
}
