#!/usr/bin/env node
// Opt-in, isolated evidence controller. It creates only owned task pages and
// returns bounded network-envelope metadata, never provider payloads.
import path from 'node:path';
import fs from 'node:fs/promises';
import {PlaywrightCLIClientFactory} from '../src/cli-client.mjs';
import {BrowserController} from '../src/controller.mjs';
import {startUnixServer} from '../src/http-server.mjs';
import {PlaywrightMCPClientFactory} from '../src/mcp-client.mjs';
import {ProviderScriptRegistry} from '../src/provider-scripts.mjs';

const socketPath=process.env.SPARKCLAW_BROWSER_CONTROLLER_SOCKET;
const runtimeRoot=process.env.SPARKCLAW_BROWSER_CLI_RUNTIME_DIR;
if(!socketPath?.startsWith('/')||!runtimeRoot?.startsWith('/'))throw new Error('isolated absolute socket and runtime paths are required');
const browserOptions={
  browserChannel:process.env.SPARKCLAW_BROWSER_CHANNEL?.trim()||'chromium',
  executablePath:process.env.SPARKCLAW_BROWSER_EXECUTABLE?.trim()||'',
  userDataDir:process.env.SPARKCLAW_BROWSER_USER_DATA_DIR?.trim()||'',
};

const providers={
  qq_mail:{url:'https://wx.mail.qq.com/',origins:['https://mail.qq.com','https://wx.mail.qq.com']},
  gmail:{url:'https://mail.google.com/mail/u/0/#inbox',origins:['https://mail.google.com','https://accounts.google.com']},
  outlook:{url:'https://outlook.live.com/mail/',origins:['https://outlook.live.com','https://outlook.office.com','https://outlook.office365.com','https://login.live.com','https://login.microsoftonline.com','https://www.microsoft.com']},
};

// Test-only channel probe. Install before navigation so a long-lived response
// is observed from its first fragment. It never retains or returns raw data.
function installChannelEvidence(){
  const records=[];
  const counts={websocket_opens:0,websocket_frames:0,channel_opens:0,channel_responses:0,channel_ok:0,channel_empty:0,channel_stream_done:0,channel_errors:0,channel_chunks:0,channel_records:0,channel_bytes:0,channel_truncated:0};
  const shape=(value,depth=0,key='')=>{
    if(depth>8||value===null)return null;
    if(Array.isArray(value))return value.slice(0,8).map(item=>shape(item,depth+1,key));
    if(typeof value==='object')return Object.fromEntries(Object.entries(value).slice(0,20).map(([name,item])=>[
      /^[A-Za-z_][A-Za-z0-9_]{0,40}$/u.test(name)?name:'dynamic_key',shape(item,depth+1,name),
    ]));
    if(typeof value==='string')return ['type','event','action','kind','name'].includes(key)&&/^[A-Za-z_]{1,40}$/u.test(value)?value:'string';
    if(typeof value==='number'&&Number.isSafeInteger(value)&&value>=0&&value<=1000)return value;
    return typeof value;
  };
  const record=(kind,data)=>{
    counts.channel_records++;
    if(records.length>=96)return;
    let parsed;
    try{parsed=JSON.parse(data);}catch{}
    const syntax=()=>{
      let quoted=false,escaped=false,contents=false,result='';
      for(const character of data.slice(0,256)){
        if(quoted){
          if(escaped){escaped=false;continue;}
          if(character==='\\'){escaped=true;continue;}
          if(character==='"'){result+='"';quoted=false;contents=false;continue;}
          if(!contents){result+='s';contents=true;}
          continue;
        }
        if(character==='"'){result+='"';quoted=true;continue;}
        result+=/[0-9]/u.test(character)?'d':/[A-Za-z]/u.test(character)?'w':character;
      }
      return result;
    };
    records.push({kind,length:data.length,shape:parsed===undefined?'non_json':shape(parsed),...(parsed===undefined?{syntax:syntax()}: {})});
  };
  Object.defineProperty(window,'__sparkclawChannelEvidence',{value:{records,counts},configurable:false});
  const NativeWebSocket=window.WebSocket;
  window.WebSocket=class extends NativeWebSocket{
    constructor(url,...args){
      super(url,...args);
      if(!String(url).startsWith('wss://wx.mail.qq.com/socket'))return;
      counts.websocket_opens++;
      this.addEventListener('message',event=>{
        counts.websocket_frames++;
        if(typeof event.data==='string'&&event.data.length<=65536)record('qq_frame',event.data);
      });
    }
  };
  const nativeOpen=XMLHttpRequest.prototype.open;
  const nativeSend=XMLHttpRequest.prototype.send;
  XMLHttpRequest.prototype.open=function(method,url,...args){
    this.__sparkclawWatchChannel=String(url).includes('/punctual/multi-watch/channel');
    if(this.__sparkclawWatchChannel)counts.channel_opens++;
    return nativeOpen.call(this,method,url,...args);
  };
  XMLHttpRequest.prototype.send=function(...args){
    if(this.__sparkclawWatchChannel){
      let offset=0,pending='';
      this.addEventListener('progress',()=>{
        try{
          const body=this.responseText;
          if(typeof body!=='string'||body.length<offset)return;
          const delta=body.slice(offset);
          offset=body.length;
          counts.channel_chunks++;
          counts.channel_bytes+=delta.length;
          pending+=delta;
          if(pending.length>32768){counts.channel_truncated++;pending='';return;}
          for(;;){
            const newline=pending.indexOf('\n');
            if(newline<0)break;
            const header=pending.slice(0,newline);
            if(!/^[0-9]{1,5}$/u.test(header)){counts.channel_truncated++;pending='';break;}
            const length=Number(header);
            if(length>16384){counts.channel_truncated++;pending='';break;}
            if(pending.length<newline+1+length)break;
            const body=pending.slice(newline+1,newline+1+length);
            pending=pending.slice(newline+1+length).replace(/^\n/u,'');
            record('gmail_frame',body);
          }
        }catch{counts.channel_truncated++;}
      });
    }
    return nativeSend.apply(this,args);
  };
  const nativeFetch=window.fetch;
  window.fetch=function(...args){
    const requestURL=typeof args[0]==='string'?args[0]:args[0]?.url;
    const result=nativeFetch.apply(this,args);
    if(!String(requestURL).includes('/owa/notificationchannel'))return result;
    counts.channel_opens++;
    void result.then(async response=>{
      let reader;
      try{
        counts.channel_responses++;
        if(response.ok)counts.channel_ok++;
        reader=response.clone().body?.getReader();
        if(!reader){counts.channel_empty++;return;}
        const decoder=new TextDecoder();
        let buffer='';
        for(;;){
          const {value,done}=await reader.read();
          if(done){counts.channel_stream_done++;break;}
          counts.channel_chunks++;
          counts.channel_bytes+=value.byteLength;
          if(counts.channel_bytes>524288||buffer.length>32768){counts.channel_truncated++;break;}
          buffer+=decoder.decode(value,{stream:true});
          for(;;){
            const boundary=buffer.indexOf('\n\n');
            if(boundary<0)break;
            const frame=buffer.slice(0,boundary).replace(/\r/gu,'');
            buffer=buffer.slice(boundary+2);
            const data=frame.split('\n').filter(line=>line.startsWith('data:')).map(line=>line.slice(5).trimStart()).join('\n');
            if(data)record('outlook_sse',data);
          }
        }
      }catch{counts.channel_errors++;}
      finally{try{await reader?.cancel();}catch{}}
    }).catch(()=>{counts.channel_errors++;});
    return result;
  };
}
const registrations=Object.entries(providers).map(([provider,site])=>({
  provider,operation:'read',scriptID:`${provider}.notification_observe`,revision:1,
  ...(process.env.SPARKCLAW_TEST_NOTIFICATION_CHANNEL_PROBE!=='0'?{beforeNavigationScript:installChannelEvidence}:{}),
  loginURL:site.url,origins:site.origins,downloadOrigins:site.origins,timeoutMS:180_000,
  sourceFiles:['tools/browser-controller/test/live-notification-observer.mjs'],
  validate(input){
    if(input?.schema_version!==1||input.provider!==provider||input.operation!=='read'||input.account!=='default'||
      !Number.isSafeInteger(input.duration_ms)||input.duration_ms<1000||input.duration_ms>120000||
      typeof input.invocation_id!=='string'||!/^[A-Za-z0-9._:-]{1,128}$/u.test(input.invocation_id)||
      typeof input.ready_token!=='string'||!/^[a-f0-9]{32}$/u.test(input.ready_token)||
      typeof input.marker!=='string'||input.marker!==''&&!/^SCW-mail-[a-f0-9]{16}$/u.test(input.marker)||
      Object.keys(input).sort().join(',')!=='account,duration_ms,invocation_id,marker,operation,provider,ready_token,schema_version')throw new Error('invalid_request');
  },
  async handler(input,runtime){
    let stage='open_page';
    try { return await runtime.withReadTab(async tab=>{
	stage='channel_ready';
	if(process.env.SPARKCLAW_TEST_NOTIFICATION_CHANNEL_PROBE!=='0'){
	  let ready=false;
	  for(let attempt=0;attempt<20;attempt++){
	    const count=await tab.runReadCode(`async page=>page.evaluate(()=>window.__sparkclawChannelEvidence?.counts.${provider==='qq_mail'?'websocket_opens':'channel_chunks'}||0)`);
	    if(count>0){ready=true;break;}
	    await new Promise(resolve=>setTimeout(resolve,1000));
	  }
	  if(!ready)throw new Error('notification channel did not become observable');
	}
      const readyDir=process.env.SPARKCLAW_NOTIFICATION_OBSERVER_READY_DIR;
      if(!readyDir?.startsWith('/'))throw new Error('observer ready directory unavailable');
	  stage='ready_file';
      await fs.writeFile(path.join(readyDir,input.ready_token), 'ready', {flag:'wx',mode:0o600});
      const deadline=Date.now()+input.duration_ms;
	  stage='observation_window';
      while(Date.now()<deadline){
        await new Promise(resolve=>setTimeout(resolve,Math.min(5000,deadline-Date.now())));
      }
      return tab.runReadCode(`async page=>{
        const marker=${JSON.stringify(input.marker)};
        const markerVisible=marker?await page.evaluate(value=>(document.body?.innerText||'').includes(value),marker):false;
        const channel=await page.evaluate(()=>window.__sparkclawChannelEvidence||null);
        const nativeChannelResources=await page.evaluate(()=>performance.getEntriesByType('resource').filter(entry=>entry.name.includes('/punctual/multi-watch/channel')||entry.name.includes('/owa/notificationchannel')).length);
        return {schema_version:1,provider:${JSON.stringify(provider)},counts:{...channel?.counts,native_channel_resources:nativeChannelResources},events:channel?.records||[],marker_visible:markerVisible};
      }`);
    }); } catch(error) {
	const code=typeof error?.code==='string'&&/^[a-z_]{1,80}$/u.test(error.code)?error.code:'unknown';
	process.stderr.write(`${JSON.stringify({phase:'notification_observer_failure',provider,stage,code})}\n`);
	throw error;
	}
  },
}));
const registry=new ProviderScriptRegistry();
for(const [key,entry] of new ProviderScriptRegistry(registrations).entries)registry.entries.set(key,entry);
const cli=new PlaywrightCLIClientFactory({registry,runtimeRoot,emailWorkspaceRoot:process.env.SPARKCLAW_BROWSER_EMAIL_WORKSPACE_ROOT?.trim()||'',...browserOptions,
  diagnostic:event=>process.stderr.write(`${JSON.stringify({phase:event.phase,code:event.code,reason:event.reason,command:event.command})}\n`),
});
const mcp=new PlaywrightMCPClientFactory({...browserOptions,outputRoot:path.join(path.dirname(socketPath),'mcp-output')});
await Promise.all([cli.prepare(),mcp.prepare()]);
const controller=new BrowserController({profileID:'default',clientFactory:mcp,scriptFactory:cli});
const server=await startUnixServer({socketPath,controller});
process.stdout.write('isolated notification observer ready\n');
let closing=false;
for(const signal of ['SIGINT','SIGTERM'])process.on(signal,()=>{if(closing)return;closing=true;void server.close().finally(()=>process.exit());});
