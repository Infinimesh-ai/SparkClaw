import assert from "node:assert/strict";
import test from "node:test";
import vm from "node:vm";
import { providerDOM, collectUnread, markRead, discoverEmail, READ_PROVIDERS } from "../../../scripts/email/read.mjs";

test('discovery returns multiple targets without opening, marking or leaking subjects',async()=>{
 const url=READ_PROVIDERS.qq_mail.url;
 const rows=[{provider_message_id:'a~fixture',unread:true,subject:'private'},{provider_message_id:'b',unread:true},{provider_message_id:'c',unread:false}];
 const tab={inspect:async()=>({origin:url,result:{url,account_address:'owner@example.test',rows,empty:false}}),click:async()=>assert.fail('discovery opened mail')};
 const result=await discoverEmail({schema_version:1,operation:'discover',provider:'qq_mail',account:'default',owner_scope:'a'.repeat(64),invocation_id:'discover-1'},{withReadTab:fn=>fn(tab)},'qq_mail');
 assert.deepEqual(result.candidates.map(v=>v.provider_message_id),['a~fixture','b']);
 assert.equal(result.coverage.scan_complete,false);
 assert.equal(JSON.stringify(result).includes('private'),false);
});

test('pinned capture rejects account mismatch before opening even when IDs match',async()=>{
 const url=READ_PROVIDERS.qq_mail.url;let opened=false;
 const tab={inspect:async()=>({origin:url,result:{url,account_address:'other@example.test',rows:[{provider_message_id:'a',provider_selection_id:'a',unread:false}],empty:false}}),click:async()=>{opened=true;}};
 await assert.rejects(collectUnread(tab,'qq_mail',{account_address:'owner@example.test',pinned_message_id:'a',pinned_selection_id:'a',onSelected:async()=>{}}),{code:'email_account_identity_mismatch'});
 assert.equal(opened,false);
});

function element(textContent='', attrs={}, children={}) {
  const node = {textContent,innerHTML:'<p>fixture</p>',className:attrs.class??'',isConnected:true,children:[],
    classList:{contains:name=>(attrs.class??'').split(' ').includes(name)},
    getBoundingClientRect:()=>({width:attrs.hidden?0:30,height:20}),
    getAttribute:name=>attrs[name]??null,
    querySelector:selector=>(children[selector]??[])[0]??null,
    querySelectorAll:selector=>children[selector]??[],
  };
  return node;
}

function evaluate(provider,phase,expected,{nodes={},url=READ_PROVIDERS[provider].url,state={}}={}) {
  return vm.runInNewContext(`(${providerDOM.toString()})(${JSON.stringify(provider)},${JSON.stringify(phase)},${JSON.stringify(expected)})`,{
    document:{querySelector:selector=>(nodes[selector]??[])[0]??null,querySelectorAll:selector=>nodes[selector]??[]},
    ...state,location:new URL(url),getComputedStyle:node=>({fontWeight:node?.weight??'400'}),
  });
}

test('Gmail projects the list last-message locator without treating it as single-message unread proof',()=>{
  const marker=element('',{'data-legacy-last-message-id':'message-1'});
  const nodes={
    'input[name="q"]':[{value:'in:inbox is:unread'}],
    'tr.zA':[element('',{class:'zA yO',hidden:true}),element('',{class:'zA zE'},{'[data-legacy-last-message-id]':[marker],'.bog':[element('Subject')]})],
  };
  const value=evaluate('gmail','list',{filtered:true},{nodes,url:'https://mail.google.com/mail/u/0/#search/in%3Ainbox+is%3Aunread'});
  assert.equal(value.rows.length,1);
  assert.equal(value.rows[0].provider_message_id,'message-1');
  assert.equal(value.rows[0].unread,true);
  assert.equal(value.rows[0].single_message_row,false);
});

test('Gmail singleton row proof works after the pinned mail becomes read and refuses a conversation count',()=>{
  const marker=element('',{'data-legacy-last-message-id':'abc','data-legacy-thread-id':'abc','data-legacy-last-non-draft-message-id':'abc','data-thread-id':'#thread-f:2748'});
  const sender=element('Sender',{class:'yP'});
  const senders=element('Sender',{}, {'.zF, .yP':[sender]});
  const row=element('',{class:'zA yO'},{'[data-legacy-last-message-id]':[marker],'.yW':[senders]});
  const nodes={'tr.zA':[row]};
  assert.equal(evaluate('gmail','list',{}, {nodes}).rows[0].single_message_row,true);
  senders.querySelectorAll=selector=>selector==='.bx0'?[element('2')]:selector==='.zF, .yP'?[sender]:[];
  assert.equal(evaluate('gmail','list',{}, {nodes}).rows[0].single_message_row,false);
});

test('Gmail differing legacy thread/message IDs are accepted only for the observed thread-a family',()=>{
 const sender=element('me');const senders=element('me',{}, {'.zF, .yP':[sender]});
 const attrs={'data-legacy-last-message-id':'abc','data-legacy-thread-id':'def','data-legacy-last-non-draft-message-id':'abc','data-thread-id':'#thread-f:2748'};
 const row=element('',{class:'zA zE'},{'[data-legacy-last-message-id]':[element('',attrs)],'.yW':[senders]});
 const nodes={'tr.zA':[row]};
 assert.equal(evaluate('gmail','list',{}, {nodes}).rows[0].single_message_row,false);
 attrs['data-thread-id']='#thread-a:r-123';
 assert.equal(evaluate('gmail','list',{}, {nodes}).rows[0].single_message_row,true);
});

test('filtered lists wait for loading and reject stale read rows instead of claiming empty',()=>{
  assert.equal(evaluate('gmail','list',{filtered:true},{nodes:{'input[name="q"]':[{value:'in:inbox is:unread'}]},url:'https://mail.google.com/mail/u/0/#search/in%3Ainbox+is%3Aunread'}),null);
  const nodes={'button[aria-label="Unread"], button[aria-label="未读"]':[element()], '[role="option"][data-convid]':[element('',{'data-convid':'thread','aria-label':'Read message'})]};
  assert.equal(evaluate('outlook','list',{filtered:true},{nodes}),null);
  nodes['[role="option"][data-convid]']=[];
  nodes['.ksePc']=[element('No unread messages')];
  assert.equal(evaluate('outlook','list',{filtered:true},{nodes}).empty,true);
});

test('QQ empty requires total inbox exhaustion and never assumes a virtual page is complete',()=>{
  const row=element('',{'data-mailid':'message-1'},{'.mail-subject':[element('Subject')]});
  const nodes={'.mail-list-page-item[data-mailid]':[row],'.mail-total-btn':[element('2 mails')]};
  assert.equal(evaluate('qq_mail','list',{}, {nodes}).empty,false);
  nodes['.mail-total-btn']=[element('1 mail')];
  assert.equal(evaluate('qq_mail','list',{}, {nodes}).empty,true);
  assert.equal(evaluate('qq_mail','list',{after_ids:['message-1']},{nodes}),null);
});

test('QQ original acquisition recognizes the actual message options panel',()=>{
  const nodes={'[role="menu"], [role="menuitem"], [role="menuitemradio"], .xmail-ui-panel-item':[element('Export as eml file'),element('Original Format')]};
  assert.equal(evaluate('qq_mail','menu',{required:'Export as eml file'},{nodes}).commands.includes('Export as eml file'),true);
  assert.equal(evaluate('qq_mail','menu',{required:'Export as eml file'}),null);
});

test('Gmail scopes a reply thread to the exact message and rejects duplicate target IDs',()=>{
  const message=element('',{'data-legacy-message-id':'message-1'},{'.a3s':[element('body')]});
  const nodes={'.adn[data-legacy-message-id]':[message]};
  assert.equal(evaluate('gmail','detail',{provider_message_id:'other'},{nodes}).error,'email_message_identity_mismatch');
  nodes['.adn[data-legacy-message-id]']=[message,element('',{'data-legacy-message-id':'different-message'})];
  assert.equal(evaluate('gmail','detail',{provider_message_id:'message-1'},{nodes}).body_text,'body');
  nodes['.adn[data-legacy-message-id]']=[message,message];
  assert.equal(evaluate('gmail','detail',{provider_message_id:'message-1'},{nodes}).error,'email_message_identity_ambiguous');
});

test('Outlook retry requires the same singleton ItemId before opening its conversation route',async()=>{
  const events=[], url=READ_PROVIDERS.outlook.url;
  const evidence=[
    {url,filtered:false},
    {url,account_address:'fixture@example.test',empty:false,rows:[{provider_selection_id:'thread-1',unread:false}]},
    {url,provider_selection_id:'thread-1',provider_message_id:'message-1'},
    {url,commands:['Download']},
    {url,commands:['Download as EML','Download as MSG']},
  ];
  const tab={inspect:async()=>({origin:url,result:evidence.shift()}),click:async selector=>events.push(selector),runReadCode:async code=>{events.push(code);return code.includes('return state?.records')?[{provider_selection_id:'thread-1',provider_message_id:'message-1',unread:false}]:true;}};
  await collectUnread(tab,'outlook',{pinned_message_id:'message-1',pinned_selection_id:'thread-1',onSelected:async()=>{}});
  assert.equal(events.some(event=>event==='[role="option"][data-convid="thread-1"]:visible'),true);
  assert.equal(events.some(event=>event.includes('page.goto(')),false);
});

test('QQ scans the next virtual page before selecting an unread mail',async()=>{
  const url=READ_PROVIDERS.qq_mail.url, selected=[];
  const evidence=[
    {url,account_address:'fixture@example.test',empty:false,total_count:2,rows:[{provider_message_id:'read-1',unread:false}]},
    {url,account_address:'fixture@example.test',empty:false,total_count:2,rows:[{provider_message_id:'unread-2',unread:true,subject:'Subject'}]},
    {url,account_address:'fixture@example.test',provider_message_id:'unread-2',subject:'Subject',body_text:'body',attachments:[],inventory_complete:false},
    {url,commands:['Export as eml file']},
  ];
  const tab={inspect:async()=>({origin:url,result:evidence.shift()}),runReadCode:async()=>true,click:async()=>{}};
  const message=await collectUnread(tab,'qq_mail',{onSelected:async row=>selected.push(row.provider_message_id)});
  assert.deepEqual(selected,['unread-2']);
  assert.equal(message.provider_message_id,'unread-2');
  assert.equal(message.inventory_complete,false);
  assert.equal(message.original.selector,'.xmail-ui-panel-item:has-text("Export as eml file"):visible');
});

test('Outlook detail uses singleton ItemId evidence and treats the URL as a conversation locator',()=>{
  const nodes={'[role="option"][data-convid][aria-selected="true"]':[element('',{'data-convid':'thread-1'})], '[id^="UniqueMessageBody"]':[element('body')]};
  const state={proof:{records:[{provider_selection_id:'thread-1',provider_message_id:'message-1'}]}};
  const expected={provider_selection_id:'thread-1',provider_message_id:'message-1',evidence_key:'proof'};
  const value=evaluate('outlook','detail',expected,{nodes,state,url:'https://outlook.live.com/mail/0/inbox/id/thread-1'});
  assert.equal(value.provider_message_id,'message-1');
  assert.equal(value.provider_selection_id,'thread-1');
  assert.equal(evaluate('outlook','detail',{...expected,provider_message_id:'thread-1'},{nodes,state,url:'https://outlook.live.com/mail/0/inbox/id/thread-1'}).error,'email_message_identity_ambiguous');
});

test('Outlook identifies the modern mailbox root only when title and direct label agree',()=>{
  const selector='[role="tree"] [role="treeitem"][aria-level="1"][data-folder-name]';
  const nodes={[selector]:[element('',{title:'fixture@example.test'},{':scope > span':[element('fixture@example.test')]})]};
  assert.equal(evaluate('outlook','account',{}, {nodes}).account_address,'fixture@example.test');
  nodes[selector]=[element('',{title:'other@example.test'},{':scope > span':[element('fixture@example.test')]})];
  assert.equal(evaluate('outlook','account',{}, {nodes}).account_address,'');
  nodes[selector]=[element('',{title:'fixture@example.test'},{':scope > span':[element('fixture@example.test')]}),element('',{title:'other@example.test'},{':scope > span':[element('other@example.test')]})];
  assert.equal(evaluate('outlook','account',{}, {nodes}).account_address,'');
});

test('Outlook refuses missing, changed or multiple message evidence on detail',()=>{
  const nodes={'[id^="UniqueMessageBody"]':[element('body')],'[role="option"][data-convid][aria-selected="true"]':[element('',{'data-convid':'thread-1'})]};
  const expected={provider_selection_id:'thread-1',provider_message_id:'message-1',evidence_key:'proof'};
  const state={proof:{records:[{provider_selection_id:'thread-1',provider_message_id:'message-1'}]}};
  const url='https://outlook.live.com/mail/0/inbox/id/thread-1';
  assert.equal(evaluate('outlook','detail',expected,{nodes,url}).error,'email_message_identity_ambiguous');
  assert.equal(evaluate('outlook','detail',expected,{nodes,state,url}).provider_message_id,'message-1');
  nodes['[id^="UniqueMessageBody"]']=[];
  assert.equal(evaluate('outlook','detail',expected,{nodes,state,url}),null);
  nodes['[id^="UniqueMessageBody"]']=[element('body'),element('other')];
  assert.equal(evaluate('outlook','detail',expected,{nodes,state,url}).error,'email_message_identity_ambiguous');
});

test('Outlook fresh selection pins a proven ItemId before opening and selects EML rather than MSG',async()=>{
  const events=[];
  let phase=0;
  const url=READ_PROVIDERS.outlook.url;
  const evidence=[
    {url,filtered:false},
    {url,account_address:'fixture@example.test',empty:false,rows:[{provider_selection_id:'thread-1',unread:true}]},
    {url,provider_selection_id:'thread-1',provider_message_id:'message-1'},
    {url,commands:['Download']},
    {url,commands:['Download as EML','Download as MSG']},
  ];
  const tab={click:async selector=>events.push(selector),inspect:async()=>({origin:url,result:evidence[phase++]}),runReadCode:async code=>code.includes('return state?.records')?[{provider_selection_id:'thread-1',provider_message_id:'message-1',unread:true}]:true};
  const result=await collectUnread(tab,'outlook',{onSelected:async value=>events.push(`pin:${value.provider_message_id??value.provider_selection_id}`)});
  assert.ok(events.indexOf('pin:message-1')<events.indexOf('[role="option"][data-convid="thread-1"]:visible'));
  assert.equal(events.includes('pin:thread-1'),false);
  assert.equal(result.original.selector,'[role="menu"] :text-is("Download as EML"):visible');
});

test('Gmail and Outlook refuse unproven unread conversations before selecting or opening a message',async()=>{
  for(const provider of ['gmail','outlook']) {
    const url=READ_PROVIDERS[provider].url,events=[];
    const tab={runReadCode:async()=>[],inspect:async()=>({origin:url,result:{url,empty:false,rows:[{provider_message_id:'message',provider_selection_id:'thread',unread:true}]}}),fill:async()=>{},press:async()=>{},click:async selector=>events.push(selector)};
    await assert.rejects(collectUnread(tab,provider,{onSelected:async()=>assert.fail('must not select an unproven unread item')}),{code:'email_message_identity_ambiguous'});
    assert.equal(events.some(selector=>selector.includes('data-convid')||selector.includes('data-legacy-last-message-id')),false);
  }
});

test('pinned retry never switches to another unread message and origin conflicts fail',async()=>{
  const url=READ_PROVIDERS.qq_mail.url;
  const tab={inspect:async()=>({origin:url,result:{url,empty:false,rows:[{provider_message_id:'other',unread:true}]}}),runReadCode:async()=>false};
  await assert.rejects(collectUnread(tab,'qq_mail',{pinned_message_id:'pinned',onSelected:async()=>assert.fail('must not select another message')}),{code:'email_pinned_message_unavailable'});
  tab.inspect=async()=>({origin:'https://wrong.test',result:{url,rows:[]}});
  await assert.rejects(collectUnread(tab,'qq_mail',{onSelected:async()=>{}}),{code:'email_provider_origin_invalid'});
});

test('QQ explicitly marks a captured unread item read and verifies the resulting item state',async()=>{
  const url=READ_PROVIDERS.qq_mail.url,events=[];
  const evidence=[{url,provider_message_id:'item',read_state:'unread'},{url,commands:['Mark as read']},{url,provider_message_id:'item',read_state:'read'}];
  const tab={inspect:async code=>{events.push(code);return {origin:url,result:evidence.shift()};},runReadCode:async code=>events.push(code)};
  assert.equal(await markRead(tab,'qq_mail',{provider_message_id:'item',account_address:'fixture@example.test'}),'read');
  assert.equal(events.some(code=>code.includes('Mark as read')),true);
  assert.equal(events.some(code=>code.includes('required_read_state')),true);
});

test('Gmail marks read through its actual text menu item and then confirms the unread command',async()=>{
  const url=READ_PROVIDERS.gmail.url,events=[];
  const evidence=[{url,provider_message_id:'item'},{url,commands:['Mark as read']},{url,commands:['Mark as unread']}];
  const tab={inspect:async()=>({origin:url,result:evidence.shift()}),runReadCode:async code=>events.push(code)};
  assert.equal(await markRead(tab,'gmail',{provider_message_id:'item',account_address:'fixture@example.test'}),'read');
  assert.equal(events.some(code=>code.includes(':text-is(\\"Mark as read\\")')),true);
});

test('read confirmation carries identity only, even when the captured body exceeds the CLI argument bound',async()=>{
  const url=READ_PROVIDERS.gmail.url;
  const evidence=[{url,provider_message_id:'item'},{url,commands:['Mark as unread']}];
  const tab={inspect:async code=>{assert.ok(Buffer.byteLength(code)<32<<10);assert.equal(code.includes('private body'),false);return {origin:url,result:evidence.shift()};},runReadCode:async()=>true};
  assert.equal(await markRead(tab,'gmail',{provider_message_id:'item',account_address:'fixture@example.test',body_html:'private body'.repeat(10000)}),'read');
});

const discoveryInput = (changes={})=>({schema_version:1,operation:'discover',provider:'qq_mail',account:'default',owner_scope:'a'.repeat(64),invocation_id:'interval',
 discovery:{lane:'unread',account_address:'owner@example.test',interval_start:'2026-09-08T00:00:00Z',interval_end:'2026-09-08T01:00:00Z',limit:50,continuation:'',...changes}});

test('bounded discovery continues more than 50 loaded rows and restarts overlap when the list changes',async()=>{
 const url=READ_PROVIDERS.qq_mail.url;
 let rows=Array.from({length:80},(_,i)=>({provider_message_id:`item-${i}`,provider_selection_id:`item-${i}`,unread:true}));
 const runtime={withReadTab:fn=>fn({runReadCode:async code=>code.includes('return state?')?{rows:rows.map(row=>({...row,folder:'inbox'})),total_count:rows.length,receipt_evidence:true,unsupported_rows:0}:true,inspect:async()=>({origin:url,result:{url,account_address:'owner@example.test',rows,empty:false}}),click:()=>assert.fail('discovery opened a message')})};
 const first=await discoverEmail(discoveryInput(),runtime,'qq_mail');
 assert.equal(first.candidates.length,50);assert.ok(first.coverage.continuation);assert.equal(first.threads.length,50);
 const second=await discoverEmail(discoveryInput({continuation:first.coverage.continuation}),runtime,'qq_mail');
 assert.equal(second.candidates.length,30);assert.equal(second.threads.length,30);assert.equal(second.candidates[0].provider_message_id,'item-50');
 rows=[{provider_message_id:'new',provider_selection_id:'new',unread:true},...rows];
 const changed=await discoverEmail(discoveryInput({continuation:first.coverage.continuation}),runtime,'qq_mail');
 assert.equal(changed.candidates[0].provider_message_id,'new');assert.equal(changed.coverage.scan_complete,false);
});

test('recent discovery never treats missing received ordering or an account switch as successful coverage',async()=>{
 const url=READ_PROVIDERS.qq_mail.url;
 const runtime={withReadTab:fn=>fn({runReadCode:async()=>null,inspect:async()=>({origin:url,result:{url,account_address:'owner@example.test',rows:[{provider_message_id:'old',unread:false}],empty:true}})})};
 const result=await discoverEmail(discoveryInput({lane:'recent_inbound'}),runtime,'qq_mail');
 assert.equal(result.status,'partial');assert.equal(result.candidates.length,0);assert.equal(result.coverage.scan_complete,false);
 await assert.rejects(discoverEmail(discoveryInput({account_address:'other@example.test'}),runtime,'qq_mail'),{code:'email_account_identity_mismatch'});
});

test('QQ recent intake includes already-read inbound mail by receipt time and rotates an ordinary folder',async()=>{
 let folder='inbox';
 const url=READ_PROVIDERS.qq_mail.url,folders={folders:[{id:1,folder:'inbox'},{id:2000,folder:'qq:2000'}],unsupported:0};
 const rows=()=>[{provider_message_id:folder+'new',provider_selection_id:folder+'new',provider_thread_id:folder+'new',folder,unread:false,received_at:'2026-09-08T00:30:00Z'},
  {provider_message_id:folder+'old',provider_selection_id:folder+'old',provider_thread_id:folder+'old',folder,unread:false,received_at:'2026-09-07T00:30:00Z'}];
 const runtime={withReadTab:fn=>fn({navigate:async url=>{assert.match(url,/#\/list\/2000$/u);folder='qq:2000';},
  runReadCode:async code=>{if(code.includes('page.goto')){assert.match(code,/#\/list\/2000/u);folder='qq:2000';}return code.includes('return state?')?{rows:rows(),total_count:2,receipt_evidence:true,folders,unsupported_rows:0}:true;},
  inspect:async()=>({origin:url,result:{url,account_address:'owner@example.test',rows:rows(),empty:false}})})};
 const first=await discoverEmail(discoveryInput({lane:'recent_inbound'}),runtime,'qq_mail');
 assert.deepEqual(first.candidates.map(row=>row.provider_message_id),['inboxnew']);assert.equal(first.coverage.ordering,'qq_totime');assert.equal(first.coverage.scan_complete,false);
 const moved=await discoverEmail(discoveryInput({lane:'recent_inbound',continuation:first.coverage.continuation}),runtime,'qq_mail');
 assert.deepEqual(moved.candidates.map(row=>row.folder),['qq:2000']);assert.equal(moved.threads[0].folder,'qq:2000');assert.equal(moved.coverage.scan_complete,false);
});

test('QQ bounded output can drain more than 100 observed targets without pre-truncating its inventory',async()=>{
 const url=READ_PROVIDERS.qq_mail.url;
 const rows=Array.from({length:175},(_,i)=>({provider_message_id:`item-${i}`,provider_selection_id:`item-${i}`,folder:'inbox',unread:true}));
 const runtime={withReadTab:fn=>fn({runReadCode:async code=>code.includes('return state?')?{rows,total_count:rows.length,receipt_evidence:true,unsupported_rows:0}:true,
  inspect:async()=>({origin:url,result:{url,account_address:'owner@example.test',rows,empty:false}})})};
 let continuation='';const ids=[];
 for(let batch=0;batch<4;batch++){
  const result=await discoverEmail(discoveryInput({continuation}),runtime,'qq_mail');ids.push(...result.candidates.map(row=>row.provider_message_id));continuation=result.coverage.continuation;
 }
 assert.equal(ids.length,175);assert.equal(new Set(ids).size,175);assert.equal(ids.at(-1),'item-174');
});

test('Gmail recent intake uses ordinary-mail search and admits read archive mail by member receipt time',async()=>{
 const url=READ_PROVIDERS.gmail.url,queries=[];
 const record={provider_message_id:'abc',provider_selection_id:'abc',provider_thread_id:'thread-f:2748',single_message_row:true,unread:false};
 const member={provider_message_id:'abc',provider_thread_id:'thread-f:2748',inbox:false,sent:false,draft:false,unread:false,received_at:'2026-09-08T00:30:00Z'};
 const runtime={withReadTab:fn=>fn({navigate:async url=>assert.ok(url.endsWith('#all')),fill:async(_selector,query)=>queries.push(query),press:async()=>{},
  inspect:async()=>({origin:url,result:{url,account_address:'owner@example.test',rows:[record],empty:false}}),
  runReadCode:async code=>code.includes('return state.records')?[member]:true})};
 const input=discoveryInput({lane:'recent_inbound'});input.provider='gmail';
 const result=await discoverEmail(input,runtime,'gmail');
 assert.equal(result.candidates.length,1);assert.equal(result.candidates[0].folder,'all');assert.equal(result.coverage.ordering,'gmail_internal_received');
 assert.match(queries[0],/^-in:trash -in:spam -in:drafts after:\d+ before:\d+$/u);assert.equal(result.coverage.scan_complete,false);
 member.sent=true;assert.equal((await discoverEmail(input,runtime,'gmail')).candidates.length,0);
 member.sent=false;member.received_at='2026-09-07T00:30:00Z';assert.equal((await discoverEmail(input,runtime,'gmail')).candidates.length,0);
});

test('background unread discovery accepts Go zero times or omitted times but recent discovery still requires an interval',async()=>{
 const url=READ_PROVIDERS.qq_mail.url;
 const runtime={withReadTab:fn=>fn({runReadCode:async()=>null,inspect:async()=>({origin:url,result:{url,account_address:'owner@example.test',rows:[{provider_message_id:'unread',provider_selection_id:'unread',unread:true}],empty:false}})})};
 for(const timestamps of [{interval_start:undefined,interval_end:undefined},{interval_start:'0001-01-01T00:00:00Z',interval_end:'0001-01-01T00:00:00Z'}]){
  const input=discoveryInput(timestamps);for(const key of ['interval_start','interval_end'])if(input.discovery[key]===undefined)delete input.discovery[key];
  assert.equal((await discoverEmail(input,runtime,'qq_mail')).candidates.length,1);
  input.discovery.lane='recent_inbound';
  await assert.rejects(discoverEmail(input,runtime,'qq_mail'),{code:'invalid_request'});
 }
 const unbound=discoveryInput({account_address:'',interval_start:'0001-01-01T00:00:00Z',interval_end:'0001-01-01T00:00:00Z'});
 await assert.rejects(discoverEmail(unbound,runtime,'qq_mail'),{code:'invalid_request'});
});

test('failure diagnostics contain only fixed counts and booleans, never mailbox or message text',async()=>{
 const {inspectionDiagnostics,warnInspectionFailure,inspect}=await import('../../../scripts/email/read.mjs');
 const secret='private-subject-and-body@example.test';
 const selected=element(secret,{'data-mailid':secret});
 const subject=element(secret),body=element(secret);
 const nodes={'.mail-list-page-item.mail-item-selected[data-mailid]':[selected],'.mail-detail-subject':[subject],'.mail-detail-content':[body]};
 const diagnostics=vm.runInNewContext(`(${inspectionDiagnostics.toString()})('qq_mail','detail',${JSON.stringify({provider_message_id:secret,subject:secret})})`,{
  document:{querySelector:selector=>nodes[selector]?.[0]??null,querySelectorAll:selector=>nodes[selector]??[]},URL,location:new URL(READ_PROVIDERS.qq_mail.url),getComputedStyle:()=>({display:'block',visibility:'visible'})});
 assert.equal(diagnostics.selected_count,1);assert.equal(diagnostics.subject_matches,true);assert.equal(diagnostics.body_visible,true);
 assert.equal(JSON.stringify(diagnostics).includes(secret),false);
 const warnings=[],previous=console.warn;console.warn=value=>warnings.push(value);
 try{
  warnInspectionFailure('qq_mail','detail','email_page_contract_changed',{...diagnostics,url:secret,subject:secret,provider_message_id:secret,rows:10001,menu_count:secret});
  const url=READ_PROVIDERS.gmail.url;
  await assert.rejects(inspect({inspect:async()=>({origin:url,result:{url,error:'email_page_contract_changed',diagnostics:{rows:2,unread_rows:0,query_matches:false,empty_marker:false,account:secret,commands:[secret]}}})},'gmail','list'),{code:'email_page_contract_changed'});
 }finally{console.warn=previous;}
 assert.equal(warnings.length,2);
 for(const warning of warnings){assert.equal(warning.includes(secret),false);assert.equal(warning.includes('url'),false);assert.equal(warning.includes('commands'),false);}
 assert.equal(JSON.parse(warnings[1]).phase,'list');assert.equal(JSON.parse(warnings[1]).query_matches,false);
});

test('Gmail failure diagnostics distinguish loading and allowlisted empty labels without returning text',async()=>{
 const {inspectionDiagnostics,warnInspectionFailure}=await import('../../../scripts/email/read.mjs');
 const empty=element('没有符合搜索条件的邮件。'),progress=element(''),main=element('');
 const nodes={'.TC':[empty],'.TC, .ae4':[empty],'[role="main"]':[main],'[role="progressbar"], progress':[progress],'input[name="q"]':[{value:'in:inbox is:unread'}]};
 const inspect=()=>vm.runInNewContext(`(${inspectionDiagnostics.toString()})('gmail','list',{})`,{
  document:{readyState:'complete',querySelector:selector=>nodes[selector]?.[0]??null,querySelectorAll:selector=>nodes[selector]??[]},
  location:new URL('https://mail.google.com/mail/u/0/#search/in%3Ainbox+is%3Aunread'),getComputedStyle:()=>({display:'block',visibility:'visible'})});
 const result=inspect();assert.equal(result.document_complete,true);assert.equal(result.tc_count,1);assert.equal(result.tc_visible,1);
 assert.equal(result.input_query_matches,true);assert.equal(result.visible_progress,true);assert.equal(result.empty_zh_matching_mail,true);
 assert.equal(JSON.stringify(result).includes('邮件'),false);
 empty.textContent='private@example.test';assert.equal(inspect().empty_zh_matching_mail,false);
 const warnings=[],previous=console.warn;console.warn=value=>warnings.push(value);
 try{warnInspectionFailure('gmail','list','email_page_contract_changed',result);}finally{console.warn=previous;}
 assert.equal(JSON.parse(warnings[0]).tc_visible,1);assert.equal(JSON.parse(warnings[0]).visible_progress,true);
});


test('Gmail accepts the observed empty-search sentence only in its visible result cell',()=>{
 const message='No messages matched your search. Try using search options such as sender, date, size and more.';
 const url='https://mail.google.com/mail/u/0/#search/in%3Ainbox+is%3Aunread';
 const marker=element(message),nodes={'.TC':[marker],'input[name="q"]':[{value:'in:inbox is:unread'}]};
 assert.equal(evaluate('gmail','list',{filtered:true},{nodes,url}).empty,true);
 nodes['.TC']=[];
 assert.equal(evaluate('gmail','list',{filtered:true},{nodes,url}),null,'loading with zero rows has no empty proof');
 nodes['.ae4']=[element(message)];nodes['.TC, .ae4']=nodes['.ae4'];
 assert.equal(evaluate('gmail','list',{filtered:true},{nodes,url}),null,'unrelated container text is not a result-cell marker');
 nodes['.TC']=[element(message,{hidden:true})];
 assert.equal(evaluate('gmail','list',{filtered:true},{nodes,url}),null,'hidden stale marker is not proof');
 nodes['.TC']=[marker];nodes['[role="main"][aria-busy="true"], [role="main"] [role="progressbar"]']=[element('')];
 assert.equal(evaluate('gmail','list',{filtered:true},{nodes,url}),null,'busy main region cannot confirm stale emptiness');
 delete nodes['[role="main"][aria-busy="true"], [role="main"] [role="progressbar"]'];
 assert.equal(evaluate('gmail','list',{filtered:true},{nodes,url:READ_PROVIDERS.gmail.url}),null,'query route must match');
});
