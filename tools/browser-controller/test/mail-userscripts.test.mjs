import assert from 'node:assert/strict';
import test from 'node:test';
import vm from 'node:vm';
import {installReader} from '../../../scripts/email/userscripts/lib/reader-core.mjs';

function fixture() {
  const nodes=[],listeners={};
  class XHR {
    open() {} send() {}
    addEventListener(_name, callback) {this.callback=callback;}
    deliver(url, value) {this.open('GET',url);this.send();this.status=200;this.responseText=JSON.stringify(value);this.callback();}
  }
  const context=vm.createContext({URL,URLSearchParams,Map,Date,JSON,Object,Number,Error,TextDecoder,Uint8Array,Response,Blob,AbortSignal,AbortController,
    XMLHttpRequest:XHR,MutationObserver:class {observe(){} disconnect(){}},
    location:{origin:'https://mail.google.com',href:'https://mail.google.com/mail/u/0/'},
    document:{addEventListener(name,callback){listeners[name]=callback;},removeEventListener(){},body:{append(node){nodes.push(node);}},createElement:()=>({remove(){}})},
    requests:[],fetch:async url=>{context.requests.push(url);const response=new Response(context.originalBytes??'From: sender@example.test\r\nMessage-ID: <fixture@example.test>\r\n\r\nSynthetic original.');if(context.responseURL)Object.defineProperty(response,'url',{value:context.responseURL});return response;},open(){},nodes,account:'owner@example.test',
  });
  vm.runInContext('window=globalThis;window.top=window;',context);
  vm.runInContext(`(${installReader.toString()})({provider:'gmail',origins:['https://mail.google.com'],account:()=>account,listURL:u=>u.pathname==='/list',parse:value=>value.rows});`,context);
  return {context,reader:context.SparkClawMailReader,deliver:rows=>new XHR().deliver('https://mail.google.com/list?sid=private-session-fixture',{rows}),nodes,XHR,listeners};
}
const interval={account_address:'owner@example.test',interval_start:'2026-09-10T00:00:00Z',interval_end:'2026-09-11T00:00:00Z'};
const row=(id,extra={})=>({provider_message_id:id,received_at:'2026-09-10T00:00:00Z',draft:false,sent:false,...extra});

test('managed reader admits receipt interval ties regardless of unread and de-duplicates observations',()=>{
  const f=fixture();
  f.deliver([row('read',{unread:false}),row('unread',{unread:true}),row('old',{received_at:'2026-09-09T23:59:59Z'}),row('next',{received_at:interval.interval_end}),row('draft',{draft:true}),row('sent',{sent:true}),row('unknown',{received_at:null}),row('group',{grouped:true})]);
  f.deliver([row('read',{unread:false})]);
  const result=f.reader.snapshot(interval);
  assert.deepEqual(Array.from(result.rows,r=>r.provider_message_id),['read','unread']);
  assert.equal(result.unsupported_rows,2);assert.equal(result.scan_complete,false);
  assert.ok(!JSON.stringify(result).includes('private-session-fixture'));
});

test('direct EML URLs require a native original confirmed after capture and an observed target',async()=>{
  const f=fixture(),target={account_address:interval.account_address,provider_message_id:'a'};
  f.deliver([row('a',{native_message_id:'msg-f:10'}),row('b',{native_message_id:'msg-f:11'})]);
  assert.throws(()=>f.reader.prepareOriginal(target),{code:'email_network_original_unqualified'});
  f.reader.armOriginal(target);
  f.reader.observeNativeOriginalURL('https://mail.google.com/mail/u/0?view=att&permmsgid=msg-f%3A10&ik=private-token-fixture');
  assert.throws(()=>f.reader.prepareOriginal(target),{code:'email_network_original_unqualified'});
  f.reader.confirmOriginal(target);
  const result=await f.reader.prepareOriginal({...target,provider_message_id:'b'});
  assert.equal(result.selector,'#sparkclaw-mail-original');
  const url=new URL(f.context.requests.at(-1));
  assert.match(f.nodes.at(-1).href,/^blob:/);
  let stopped=false;f.listeners.click({target:f.nodes.at(-1),stopImmediatePropagation(){stopped=true;},preventDefault(){assert.fail('native download default must be preserved');}});assert.equal(stopped,true);
  assert.equal(url.searchParams.get('permmsgid'),'msg-f:11');
  assert.equal(url.searchParams.get('ik'),'private-token-fixture');
  assert.equal(await (await fetch(f.nodes.at(-1).href)).text(),'From: sender@example.test\r\nMessage-ID: <fixture@example.test>\r\n\r\nSynthetic original.');
  assert.ok(!JSON.stringify(result).includes('private-token-fixture'));
  assert.throws(()=>f.reader.prepareOriginal({...target,provider_message_id:'never-observed'}),{code:'email_network_target_unobserved'});
  f.deliver([row('Cgroup',{grouped:true})]);assert.throws(()=>f.reader.prepareOriginal({...target,provider_message_id:'Cgroup'}),{code:'email_network_target_unobserved'});
});

test('network originals preserve binary bytes and reject an HTML login response',async()=>{
  const f=fixture(),target={account_address:interval.account_address,provider_message_id:'a'};
  f.deliver([row('a')]);
  const learn=()=>{f.reader.armOriginal(target);f.reader.observeNativeOriginalURL('https://mail.google.com/download?id=a');f.reader.confirmOriginal(target);};
  learn();f.context.originalBytes=Buffer.concat([Buffer.from('From: sender@example.test\r\n\r\n'),Buffer.from([0,127,128,255])]);
  await f.reader.prepareOriginal(target);
  assert.deepEqual(Buffer.from(await(await fetch(f.nodes.at(-1).href)).arrayBuffer()),f.context.originalBytes);
  f.context.originalBytes='<html>Please sign in</html>';
  await assert.rejects(f.reader.prepareOriginal(target),{code:'email_network_original_unqualified'});
  assert.throws(()=>f.reader.prepareOriginal(target),{code:'email_network_original_unqualified'});
});

test('Gmail originals allow the observed attachment redirect and reject other final origins',async()=>{
  const f=fixture(),target={account_address:interval.account_address,provider_message_id:'a'};
  f.deliver([row('a')]);f.reader.armOriginal(target);f.reader.observeNativeOriginalURL('https://mail.google.com/download?id=a');f.reader.confirmOriginal(target);
  f.context.responseURL='https://mail-attachment.googleusercontent.com/attachment/';
  assert.equal((await f.reader.prepareOriginal(target)).selector,'#sparkclaw-mail-original');
  f.context.responseURL='https://unknown.example.test/attachment/';
  await assert.rejects(f.reader.prepareOriginal(target),{code:'email_network_original_unqualified'});
});

test('foreign URLs, ambiguous target parameters and wrong acknowledgements cannot seed direct reads',()=>{
  for(const url of ['https://foreign.example/download?id=a','https://mail.google.com/download?id=a&other=a','https://mail.google.com/download?id=other']){
    const f=fixture(),target={account_address:interval.account_address,provider_message_id:'a'};
    f.deliver([row('a')]);f.reader.armOriginal(target);f.reader.observeNativeOriginalURL(url);f.reader.confirmOriginal(target);
    assert.throws(()=>f.reader.prepareOriginal(target),{code:'email_network_original_unqualified'});
  }
});

test('account changes fail closed and disposal restores only owned network hooks',()=>{
  const f=fixture();f.deliver([row('a')]);f.reader.snapshot(interval);
  f.context.account='other@example.test';
  assert.throws(()=>f.reader.snapshot(interval),{code:'email_account_identity_mismatch'});
  const newerHook=()=>{};f.context.fetch=newerHook;
  f.reader.dispose();assert.equal(f.context.fetch,newerHook);assert.equal(f.context.SparkClawMailReader,undefined);
});

import {networkListPage} from '../../../scripts/email/lib/network-reader.mjs';
const options={...interval,lane:'recent_inbound',continuation:'',limit:2};
const networkRow=id=>({...row(id),provider_thread_id:'thread-f:10',inbox:true,unread:false});

test('network batches drain individual members before advancing page and only final qualified page can complete',async()=>{
  const tab={runReadCode:async()=>({provider:'gmail',account_address:interval.account_address,page:0,rows:['a','b','c'].map(networkRow),unsupported_rows:0,has_next:true})};
  const first=await networkListPage(tab,'gmail',options,{});
  assert.deepEqual(first.discovery.candidates.map(r=>r.provider_message_id),['a','b']);
  assert.equal(first.discovery.coverage.scan_complete,false);
  const second=await networkListPage(tab,'gmail',{...options,continuation:first.discovery.coverage.continuation},{});
  assert.deepEqual(second.discovery.candidates.map(r=>r.provider_message_id),['c']);
  const cursor=second.discovery.coverage.continuation;
  tab.runReadCode=async code=>code.includes('\"page\":0')?{provider:'gmail',account_address:interval.account_address,page:0,rows:['a','b','c'].map(networkRow),unsupported_rows:0,has_next:true}:({provider:'gmail',account_address:interval.account_address,page:1,rows:[],unsupported_rows:0,has_next:false});
  const last=await networkListPage(tab,'gmail',{...options,continuation:cursor},{});
  assert.equal(last.discovery.status,'empty');
  assert.equal(last.discovery.coverage.boundary_qualified,true);
  assert.equal(last.discovery.coverage.continuation,undefined);
  await assert.rejects(networkListPage(tab,'gmail',{...options,interval_end:'2026-09-12T00:00:00Z',continuation:cursor},{}),{code:'email_cursor_invalid'});
});

test('an unqualified member fences the page instead of allowing a later page to advance the watermark',async()=>{
  const tab={runReadCode:async()=>({provider:'gmail',account_address:interval.account_address,page:0,rows:[networkRow('a')],unsupported_rows:1,has_next:true})};
  const result=await networkListPage(tab,'gmail',options,{});
  const cursor=JSON.parse(Buffer.from(result.discovery.coverage.continuation.slice(3),'base64url'));
  assert.equal(cursor.p,0);assert.equal(cursor.o,0);
  assert.equal(result.discovery.coverage.boundary_qualified,false);
  assert.equal(result.discovery.coverage.reason,'network_rows_unqualified');
});

test('Gmail HTTP paging reuses the observed query and session headers without exporting them',async()=>{
  const f=fixture();f.reader.dispose();
  f.context.structuredClone=structuredClone;f.context.AbortSignal=AbortSignal;
  f.XHR.prototype.setRequestHeader=function(){};
  let request;
  const value=[];value[0]=0;value[3]=0;
  const thread=[];thread[4]=[networkRow('a')];value[19]=[[null,[[thread]]]];
  f.context.fetch=async(url,options)=>{request={url,options};return new Response(JSON.stringify(value));};
  vm.runInContext(`(${installReader.toString()})({provider:'gmail',origins:['https://mail.google.com'],account:()=>account,listURL:u=>u.pathname==='/list',parse:value=>(value[19][0][1]??[]).map(c=>c[0][4][0])});`,f.context);
  const body=[[],null,[777]];body[0][0]=123;body[0][1]=50;body[0][9]=0;body[0][15]=[];
  body[0][3]=`-in:trash -in:spam -in:drafts after:${Math.floor(Date.parse(interval.interval_start)/1000)-1} before:${Math.ceil(Date.parse(interval.interval_end)/1000)}`;
  const xhr=new f.XHR();xhr.open('POST','https://mail.google.com/list');xhr.setRequestHeader('x-framework-xsrf-token','private-fixture');xhr.send(JSON.stringify(body));
  const result=await f.context.SparkClawMailReader.listPage({...interval,page:0});
  const sent=JSON.parse(request.options.body);
  assert.equal(sent[0][9],0);assert.equal(sent[2][0],0);assert.equal(sent[0][3],body[0][3]);
  assert.equal(request.options.headers['x-framework-xsrf-token'],'private-fixture');
  assert.equal(result.has_next,false);assert.equal(result.rows.length,1);
  assert.ok(!JSON.stringify(result).includes('private-fixture'));
  request=null;
  await assert.rejects(f.context.SparkClawMailReader.listPage({...interval,interval_end:'2026-09-12T00:00:00Z'}),{code:'email_network_list_unqualified'});
  assert.equal(request,null);
  value[19]=[[1,null,0,0]];
  f.context.location.hash='#search/'+encodeURIComponent(body[0][3]);
  f.context.document.querySelectorAll=selector=>selector==='tr.zA'?[]:[{getClientRects:()=>[{}],textContent:'No messages matched your search.'}];
  const empty=await f.context.SparkClawMailReader.listPage({...interval,page:0});
  assert.equal(empty.rows.length,0);assert.equal(empty.has_next,false);
  f.context.document.querySelectorAll=()=>[];
  await assert.rejects(f.context.SparkClawMailReader.listPage({...interval,page:0}),{code:'email_network_list_unqualified'});
});

import {installOutlookEarlyBridge} from '../../../scripts/email/userscripts/lib/outlook-early-bridge.mjs';
import {installOutlookTransport} from '../../../scripts/email/userscripts/lib/outlook-transport.mjs';
import {webcrypto} from 'node:crypto';

test('Outlook Worker replay binds the startup Inbox, validates individual receipts and isolates its RPC callbacks',async()=>{
  const ports=[],sent=[],observed=[];
  class Port { constructor(){this.listeners=[];ports.push(this);}addEventListener(_,fn){this.listeners.push(fn);}removeEventListener(_,fn){this.listeners=this.listeners.filter(v=>v!==fn);}postMessage(value){sent.push(value);} }
  class Channel {constructor(){this.port1=new Port();this.port2=new Port();}}
  let reply={data:{itemRows:{edges:[{node:{ItemId:{Id:'message'},ConversationId:{Id:'thread'},DateTimeReceived:interval.interval_start,IsDraft:false,IsRead:true,ParentFolderId:{Id:'inbox-id'}}}],indexedOffset:1,pageInfo:{hasNextPage:false}}}};
  let propagated=false;
  class Worker {postMessage(message){
    const body=message.argumentList[0].value;
    if(body.requestId<700000000)return;
    observed.push(structuredClone(body.variables));
    queueMicrotask(()=>{const event={data:{id:'reply',type:'APPLY',path:['next'],argumentList:[{type:'RAW',value:body.requestId},{type:'RAW',value:reply}]},stopImmediatePropagation(){this.stopped=true;}};for(const listener of ports[0].listeners){listener(event);if(event.stopped)break;}});
  }}
  const context=vm.createContext({window:{Worker,MessageChannel:Channel},location:{origin:'https://outlook.live.com'},Date,Map,Error,Object,Number,URL,Uint32Array,Uint8Array,TextEncoder,crypto:webcrypto,structuredClone,setTimeout,clearTimeout,
    account:()=>interval.account_address,getInbox:()=>({id:'inbox-id',account:interval.account_address}),receiveRows:rows=>observed.push(rows),originalURL:()=>{}});
  vm.runInContext(`window.top=window;installOutlookEarlyBridge=${installOutlookEarlyBridge.toString()};installOutlookEarlyBridge();channel=new window.MessageChannel();worker=new window.Worker();`,context);
  ports[0].addEventListener('message',()=>{propagated=true;});
  const request=(operationName,folderId)=>({id:'native',type:'APPLY',path:['execute'],argumentList:[{type:'RAW',value:{operationName,requestId:1,context:{},variables:{folderId,mailboxInfo:{mailboxSmtpAddress:interval.account_address},pagingInfo:{},viewFilter:'All',focusedViewFilter:operationName==='ItemRows'?'None':'Focused',sortBy:{isDraftsFolder:true}}}}]});
  context.worker.postMessage(request('ItemRows','drafts-id'));
  context.worker.postMessage(request('ConversationRows','inbox-id'));
  context.worker.postMessage(request('ConversationRows','sent-id'));
  vm.runInContext(`transport=(${installOutlookTransport.toString()})({account,getInbox,receiveRows,originalURL});`,context);
  const result=await context.transport.listPage({...interval,page:0});
  assert.equal(result.rows.length,1);assert.equal(result.rows[0].unread,false);assert.equal(result.has_next,false);
  assert.equal(observed[0].folderId,'inbox-id');assert.equal(observed[0].sortBy.isDraftsFolder,false);
  assert.equal(propagated,false);assert.equal(sent.at(-1).id,'reply');
  reply.data.itemRows.edges[0].node.ParentFolderId.Id='sent-id';
  const wrong=await context.transport.listPage({...interval,page:0});
  assert.equal(wrong.rows.length,0);assert.equal(wrong.unsupported_rows,1);
  context.transport.dispose();context.window.SparkClawOutlookEarlyBridge.dispose();assert.equal(context.window.Worker,Worker);assert.equal(ports[0].listeners.length,1);
});

test('a shifted previously scanned page prevents terminal watermark advancement',async()=>{
  const tab={runReadCode:async()=>({provider:'gmail',account_address:interval.account_address,page:0,rows:[networkRow('a')],unsupported_rows:0,has_next:true})};
  const first=await networkListPage(tab,'gmail',options,{});
  tab.runReadCode=async code=>code.includes('"page":0')?
    {provider:'gmail',account_address:interval.account_address,page:0,rows:[networkRow('changed')],unsupported_rows:0,has_next:true}:
    {provider:'gmail',account_address:interval.account_address,page:1,rows:[],unsupported_rows:0,has_next:false};
  const final=await networkListPage(tab,'gmail',{...options,continuation:first.discovery.coverage.continuation},{});
  assert.equal(final.discovery.coverage.scan_complete,false);
  assert.equal(final.discovery.coverage.reason,'network_page_changed');
  assert.equal(JSON.parse(Buffer.from(final.discovery.coverage.continuation.slice(3),'base64url')).p,0);
});

import {parseGmailReceivedList} from '../../../scripts/email/lib/gmail-list.mjs';
test('received network inventory preserves inbound siblings of an alternate-family sent reply',()=>{
  const message=(id,native,sent=false)=>{const m=[];m[0]=native;m[55]=id;m[6]=Date.parse(interval.interval_start);m[10]=sent?['^f']:['^i'];return m;};
  const thread=[];thread[3]='thread-f:10';thread[4]=[message('a','msg-f:10'),message('b','msg-a:r11',true),message('c','msg-f:12')];
  const source=[];source[19]=[[null,[[thread]]]];
  const rows=parseGmailReceivedList(source);
  assert.deepEqual(rows.map(r=>r.provider_message_id),['a','c']);
  assert.deepEqual(rows.map(r=>r.native_message_id),['msg-f:10','msg-f:12']);
  thread[4][2][0]='msg-f:99';
  assert.deepEqual(parseGmailReceivedList(source).map(r=>r.provider_message_id),['a']);
});

test('Outlook Inbox evidence alone never certifies all received folders as complete',async()=>{
  const tab={runReadCode:async()=>({provider:'outlook',account_address:interval.account_address,page:0,rows:[{...networkRow('a'),provider_selection_id:'thread',provider_thread_id:'thread'}],unsupported_rows:0,has_next:false,scope:'inbox_loaded'})};
  const value=await networkListPage(tab,'outlook',options,{});
  assert.equal(value.discovery.status,'partial');assert.equal(value.discovery.coverage.boundary_qualified,false);
  assert.equal(value.discovery.coverage.reason,'folder_scope_and_pagination_unqualified');
  assert.equal(value.discovery.candidates[0].provider_selection_id,'thread');
  assert.equal(value.listed.rows[0].inventory_complete,false);
});

test('Gmail needs the observed response cursor as well as the native page index',async()=>{
  const f=fixture();f.reader.dispose();f.context.structuredClone=structuredClone;f.context.AbortSignal=AbortSignal;f.XHR.prototype.setRequestHeader=function(){};
  const requests=[];
  f.context.fetch=async(_url,options)=>{
    const body=JSON.parse(options.body);requests.push(body);const value=[];value[0]=0;value[3]=body[0][9]===0?1:0;value[19]=[];
    value[13]=[];value[13][8]=value[13][9]='opaque-native-cursor-fixture';
    return new Response(JSON.stringify(value));
  };
  vm.runInContext(`(${installReader.toString()})({provider:'gmail',origins:['https://mail.google.com'],account:()=>account,listURL:u=>u.pathname==='/list',parse:()=>[]});`,f.context);
  const body=[[],null,[100]];body[0][0]=123;body[0][1]=50;body[0][7]=1000;body[0][9]=0;body[0][15]=[];
  body[0][3]=`-in:trash -in:spam -in:drafts after:${Math.floor(Date.parse(interval.interval_start)/1000)-1} before:${Math.ceil(Date.parse(interval.interval_end)/1000)}`;
  const xhr=new f.XHR();xhr.open('POST','https://mail.google.com/list');xhr.send(JSON.stringify(body));
  await assert.rejects(f.context.SparkClawMailReader.listPage({...interval,page:1}),{code:'email_network_list_unqualified'});
  await f.context.SparkClawMailReader.listPage({...interval,page:0});
  const terminal=await f.context.SparkClawMailReader.listPage({...interval,page:1});
  assert.equal(requests[1][0][9],1);assert.equal(requests[1][0][15][13],'opaque-native-cursor-fixture');
  assert.equal(requests[1][0][7],2000);assert.equal(requests[1][2][0],0);
  assert.equal(terminal.has_next,false);assert.ok(!JSON.stringify(terminal).includes('opaque-native-cursor-fixture'));
});

import {parseOutlookFolders} from '../../../scripts/email/userscripts/lib/outlook-folders.mjs';
test('Outlook folder coverage requires a full hierarchy and excludes hidden folders and trash descendants',()=>{
  const folder=(id,kind,parent='root',hidden='false')=>({FolderId:{Id:id},ParentFolderId:{Id:parent},FolderClass:'IPF.Note',DisplayName:id,DistinguishedFolderId:kind,
    ExtendedProperty:[{ExtendedFieldURI:{PropertyTag:'0x10f4',PropertyType:'Boolean'},Value:hidden}]});
  const folders=[folder('inbox','inbox'),folder('archive','archive'),folder('custom',undefined,'archive'),folder('deleted','deleteditems'),folder('trash-child',undefined,'deleted'),folder('hidden',undefined,'root','true')];
  const root={Folders:folders,IncludesLastItemInRange:true,TotalItemsInView:folders.length,ParentFolder:{FolderId:{Id:'root'}}};
  const value={owaUserConfig:{SessionSettings:{UserEmailAddress:interval.account_address}},findConversation:{Body:{FolderId:{Id:'inbox'}}},findFolders:{Body:{ResponseMessages:{Items:[{RootFolder:root}]}}}};
  const parsed=parseOutlookFolders(value);
  assert.equal(parsed.qualified,true);assert.deepEqual(parsed.folders.map(f=>f.id),['inbox','archive','custom']);
  root.IncludesLastItemInRange=false;assert.equal(parseOutlookFolders(value).qualified,false);
  root.IncludesLastItemInRange=true;folders[2].ParentFolderId.Id='missing';assert.equal(parseOutlookFolders(value).qualified,false);
  folders[2].ParentFolderId.Id='archive';folders[2].ExtendedProperty=[];assert.equal(parseOutlookFolders(value).qualified,false);
});

test('Outlook attachment aliases require the native export account and item proof before template reuse',async()=>{
  const f=fixture();f.reader.dispose();f.context.location={origin:'https://outlook.live.com',href:'https://outlook.live.com/mail/0/inbox'};
  vm.runInContext(`(${installReader.toString()})({provider:'outlook',origins:['https://outlook.live.com'],account:()=>account,listURL:u=>u.pathname==='/list',parse:value=>value.rows});`,f.context);
  const reader=f.context.SparkClawMailReader,target={account_address:interval.account_address,provider_message_id:'a'};
  new f.XHR().deliver('https://outlook.live.com/list',{rows:[row('a'),row('b')]});
  const url='https://attachment.outlook.live.net/owa/MSA%3Aopaque-alias/service.svc/s/DownloadMessage?id=a&token=private-fixture';
  for(const proof of [undefined,{account_address:'other@example.test',provider_message_id:'a'},{account_address:interval.account_address,provider_message_id:'wrong'}]){
    reader.armOriginal(target);reader.observeNativeOriginalURL(url,proof);reader.confirmOriginal(target);
    assert.throws(()=>reader.prepareOriginal(target),{code:'email_network_original_unqualified'});
  }
  reader.armOriginal(target);reader.observeNativeOriginalURL(url,target);reader.confirmOriginal(target);
  const result=await reader.prepareOriginal({...target,provider_message_id:'b'});
  assert.equal(new URL(f.context.requests.at(-1)).searchParams.get('id'),'b');
  assert.ok(!JSON.stringify(result).includes('private-fixture'));reader.dispose();
});
