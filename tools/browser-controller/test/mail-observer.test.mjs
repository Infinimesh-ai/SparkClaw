import test from 'node:test';
import assert from 'node:assert/strict';
import vm from 'node:vm';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import {installMailObserverPage} from '../src/mail-observer-page.mjs';
import {classifyMailNotification} from '../src/mail-notification-rules.mjs';
import {ResidentMailObservers} from '../src/mail-observers.mjs';

function pageFixture(provider,{dormant=false}={}) {
  const events = [], sockets = [], timers = new Set();
  class Socket extends EventTarget { constructor() {super(); sockets.push(this);} }
  class XHR extends EventTarget {
    open(method, url) {this.url = url;}
    send(body) {this.sent = body; return 'native-send';}
    progress(body) {this.responseText = body; this.dispatchEvent(new Event('progress'));}
  }
  class Worker extends EventTarget {postMessage(value) {this.sent=value; return 'native-post';}}
  class Channel {constructor() {this.port1=new EventTarget();this.port2=new EventTarget();}}
  const nativeFetch=async()=>({ok:true,clone(){throw new Error('observer must not clone response');}});
  const origin = {gmail:'https://mail.google.com',qq_mail:'https://wx.mail.qq.com',outlook:'https://outlook.live.com'}[provider];
  const context = {crypto, Worker, MessageChannel:Channel, XMLHttpRequest: XHR, WebSocket: Socket, URL, TextDecoder, Event, atob,
    location:{origin,href:origin+'/'},
    setInterval(fn) {timers.add(fn); return fn;}, clearInterval(fn) {timers.delete(fn);},
    fetch: nativeFetch,
    __sparkclawMailObservation: async event => events.push(event),
    SparkClawMailReader:{provider,version:'0.2.0',checkAccount(){return true;}}};
  context.window=context;context.top=context;
  vm.runInNewContext(`(${installMailObserverPage.toString()})(${JSON.stringify({provider,origins:[origin],account:'test@example.test',evidence:true,dormant})}, ${classifyMailNotification.toString()})`,context);
  return {context,events,sockets,timers,nativeFetch,Worker,Channel};
}

test('dormant early hooks emit no pre-registration backlog and account checks do not traverse Reader rows',async()=>{
  const f=pageFixture('gmail',{dormant:true});
  let checks=0;
  f.context.SparkClawMailReader.checkAccount=()=>{checks++;return true;};
  f.context.SparkClawMailReader.snapshot=()=>{throw new Error('row traversal is forbidden');};
  const xhr=new f.context.XMLHttpRequest();
  xhr.open('POST','/punctual/multi-watch/channel');xhr.send();
  xhr.progress('3\n{}\n');
  assert.equal(f.events.length,0);
  assert.equal(f.context.__sparkclawMailObserver.activate(),true);
  assert.equal(f.context.__sparkclawMailObserver.activate(),true);
  await new Promise(setImmediate);
  assert.equal(f.events.filter(event=>event.kind==='document').length,1);
  assert.equal(f.events.filter(event=>event.kind==='mailbox_changed').length,0);
  assert.equal(checks,1);
});

test('Gmail fragmented length frames and multiple frames preserve the native XHR return', () => {
  const f=pageFixture('gmail'),xhr=new f.context.XMLHttpRequest();
  xhr.open('POST','/mail/u/0/punctual/multi-watch/channel');
  assert.equal(xhr.send('original'),'native-send');
  assert.equal(xhr.sent,'original');
  const data=JSON.stringify([[1,[1,2,7]]]),packet=`${data.length}\n${data}`;
  for(let i=1;i<=packet.length;i++)xhr.progress(packet.slice(0,i));
  xhr.progress(packet+'\n'+packet);
  assert.equal(f.events.filter(event=>event.kind==='evidence').length,2);
  assert.equal(f.events.filter(event=>event.kind==='mailbox_changed').length,0);
  assert.equal(f.events.find(event=>event.kind==='evidence').account_ok,true);
});

test('Outlook native Worker callbacks pass through unchanged and emit metadata without cloning fetch', async () => {
  const f=pageFixture('outlook');
  assert.equal(f.context.fetch,f.nativeFetch);
  assert.equal((await f.context.fetch('/owa/notificationchannel')).ok,true);
  const worker=new f.context.Worker();
  const request={argumentList:[{value:{operationName:'subscribeToRowNotifications',requestId:3}}]};
  assert.equal(worker.postMessage(request),'native-post');assert.equal(worker.sent,request);
  await new Promise(setImmediate);
  const channel=new f.context.MessageChannel();
  let forwarded;
  channel.port1.addEventListener('message',event=>{forwarded=event.data;});
  const message={type:'APPLY',argumentList:[{value:3},{value:{data:{subscribeToRowNotifications:{EventType:'RowAdded',Conversation:{ConversationId:{Id:'native-test'},LastDeliveryTime:'2026-09-24T09:00:00Z',MessageCount:1,ItemIds:[{Id:'item-test'}],ConversationTopic:'private subject'}}}}}]};
  channel.port1.dispatchEvent(new MessageEvent('message',{data:message}));
  assert.equal(forwarded,message);
  const hint=f.events.find(event=>event.kind==='mailbox_changed');
  assert.equal(hint.reason,'outlook_delivery_change');
  assert.equal(JSON.stringify(f.events).includes('private subject'),false);
  f.context.__sparkclawMailObserver.dispose();
  assert.equal(f.context.Worker,f.Worker);assert.equal(f.context.MessageChannel,f.Channel);
});

test('unbounded/malformed streams degrade and release parser buffer without affecting native XHR', () => {
  const f=pageFixture('gmail'),xhr=new f.context.XMLHttpRequest();
  xhr.open('POST','/punctual/multi-watch/channel');xhr.send();xhr.progress('9'.repeat(131073));
  assert.equal(f.events.filter(event=>event.kind==='degraded').length,1);
  xhr.progress('9'.repeat(131074));
  assert.equal(f.events.filter(event=>event.kind==='degraded').length,1);
  f.context.__sparkclawMailObserver.dispose();
  assert.equal(f.timers.size,0);
});

test('resident receive is bounded, deduplicated and refuses unbound/malformed events', () => {
  const observer=new ResidentMailObservers({runtimeRoot:'/tmp/unused'}, {evidenceLimit:2,evidence:true});
  const slot={key:'key',document:'doc',sequence:0,started:Date.now(),events:[],counts:{},hints:0,dropped:0};
  observer.slots.set('gmail',slot);
  const event=sequence=>({key:'key',value:{document:'doc',sequence,kind:'evidence',account_ok:true}});
  observer.receive({...event(1),key:'wrong'});assert.equal(slot.sequence,0);
  observer.receive(event(1));observer.receive(event(1));assert.equal(slot.events.length,1);
  observer.receive(event(2));observer.receive(event(4));
  assert.equal(slot.events.length,2);assert.equal(slot.dropped,1);assert.equal(slot.resyncRequired,true);
  observer.receive({key:'key',value:{document:'doc',sequence:5,kind:'execute',account_ok:true}});
  assert.equal(slot.sequence,4);
});

test('old documents cannot replace the current generation; diagnostic bytes remain bounded', () => {
  const observer=new ResidentMailObservers({runtimeRoot:'/tmp/unused'},{evidence:true});
  const slot={key:'key',document:'old',sequence:1,started:Date.now(),events:[],counts:{},hints:0,dropped:0};
  observer.slots.set('gmail',slot);
  const receive=(document,sequence,kind,extra={})=>observer.receive({key:'key',value:{document,sequence,kind,account_ok:true,...extra}});
  receive('new',1,'document');receive('old',2,'mailbox_changed');
  assert.equal(slot.document,'new');assert.equal(slot.hints,0);
  for(let seq=2;seq<30;seq++)receive('new',seq,'evidence',{shape:'x'.repeat(4000)});
  assert.ok(slot.bufferBytes<=32768);assert.ok(Buffer.byteLength(JSON.stringify(observer.status('gmail')))<64000);
  receive('new',30,'degraded');receive('new',31,'liveness');
  assert.equal(observer.status('gmail').state,'degraded');
});

test('start returns a parked lease, repeated start reuses it, scope changes fence it, and stop cleans only its owned runtime', async () => {
  const root=await fs.mkdtemp(path.join(os.tmpdir(),'sc-mail-watch-'));
  const calls=[];
  const factory={runtimeRoot:root,registry:{entries:new Map([['gmail:read',{provider:'gmail',operation:'read',origins:['https://mail.google.com'],loginURL:'https://mail.google.com/'}]])}};
  const observer=new ResidentMailObservers(factory,{taskFactory:()=>Object.fromEntries(['attach','createTaskPage','navigate','prepareBackgroundPage','prepareMailRound','activateMailObserver','closeTaskPage','stop'].map(name=>[name,async()=>{calls.push(name);}]))});
  const args={provider:'gmail',token:'proof',credentialGeneration:1,input:{schema_version:1,action:'start',account_address:'test@example.test',owner_scope:'a'.repeat(64)}};
  try {
    await observer.prepare();
    assert.equal((await observer.run(args)).state,'watching');
    await observer.run(args);assert.equal(calls.filter(name=>name==='attach').length,1);
    await assert.rejects(observer.run({...args,input:{...args.input,owner_scope:'b'.repeat(64)}}),error=>error.code==='browser_page_stale');
    await observer.run({...args,input:{...args.input,action:'stop'}});
    assert.deepEqual(calls.slice(-2),['closeTaskPage','stop']);
    assert.equal((await fs.readdir(root)).filter(name=>name.startsWith('session-')).length,0);
  } finally {await observer.close();await fs.rm(root,{recursive:true,force:true});}
});

test('health recovery is bounded, preserves pending hints and does not restart for an account mismatch', async () => {
  const observer=new ResidentMailObservers({runtimeRoot:'/tmp/unused'});
  const calls=[];
  observer.startOrInspect=async args=>{calls.push(args);observer.slots.get(args.provider).state='watching';};
  const slot={ready:true,state:'watching',lastEvent:Date.now()-60000,started:Date.now()-60000,restarts:0,hints:4,args:{provider:'gmail',input:{action:'start'}},retryAt:Date.now()-1};
  observer.slots.set('gmail',slot);
  observer.checkHealth();await Promise.all([...observer.recoveries.values()]);
  assert.equal(calls.length,1);assert.equal(calls[0].recoveryHints,4);assert.equal(calls[0].restarts,1);
  slot.state='login_required';observer.checkHealth();assert.equal(calls.length,1);
  slot.state='degraded';slot.restarts=3;observer.checkHealth();assert.equal(calls.length,1);
  observer.slots.clear();await observer.close();
});

test('production status drops untrusted payload fields and cannot turn a diagnostic reason into a hint', () => {
  const observer=new ResidentMailObservers({runtimeRoot:'/tmp/unused'});
  const slot={key:'key',document:'doc',sequence:0,started:Date.now(),events:[],counts:{},hints:0,dropped:0};observer.slots.set('gmail',slot);
  const value={document:'doc',sequence:1,kind:'mailbox_changed',reason:'gmail_topic_invalidation',account_ok:true,subject:'private subject',shape:{body:'private body'}};
  observer.receive({key:'key',value});
  assert.equal(slot.hints,1);assert.equal(JSON.stringify(observer.status('gmail')).includes('private'),false);
  observer.receive({key:'key',value:{...value,sequence:2,reason:'frame_limit'}});assert.equal(slot.hints,1);
});


test('an already lost page can be reconstructed only after its owned daemon is reaped', async () => {
  const observer=new ResidentMailObservers({runtimeRoot:'/tmp/unused'});
  const calls=[];
  observer.slots.set('outlook',{key:'key',client:{async closeTaskPage(){throw new Error('page gone');},async stop(){calls.push('stop');}},runtime:{async reapDaemon(){calls.push('reap');},async remove(){calls.push('remove');}}});
  await observer.stop('outlook');assert.deepEqual(calls,['stop','reap','remove']);assert.equal(observer.slots.size,0);
  observer.slots.set('outlook',{key:'key',runtime:{async reapDaemon(){throw new Error('ownership mismatch');},async remove(){throw new Error('must not remove');}}});
  await assert.rejects(observer.stop('outlook'),/ownership mismatch/);assert.equal(observer.slots.size,1);
});
