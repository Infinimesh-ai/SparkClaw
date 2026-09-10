import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { capturePage, pageCaptureInvocation, validateCaptureInput } from '../../../scripts/email/lib/read-capture.mjs';

const request = () => ({schema_version:1,operation:'collect_page',provider:'gmail',account:'default',owner_scope:'a'.repeat(64),invocation_id:'email_page_job_unread',
  discovery:{account_address:'owner@example.test',lane:'unread',continuation:'',limit:50}});
const target = id => ({account_address:'owner@example.test',provider_message_id:id,provider_selection_id:id,provider_thread_id:id,folder:'inbox'});
const eml = Buffer.from('From: sender@example.test\r\nTo: owner@example.test\r\nSubject: Fixture\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nBody\r\n');

async function fixture(t) {
  const root = await fs.mkdtemp(path.join(os.tmpdir(),'email-page-'));
  t.after(()=>fs.rm(root,{force:true,recursive:true}));
  const events = [];
  const f = {root,events,targets:[target('a'),target('b')],tabs:0,scans:0};
  const tab = {download:async (_selector,destination)=>{
    events.push(`download:${f.current}`);
    if (f.onDownload) await f.onDownload();
    await fs.writeFile(destination,f.bytes ?? eml,{flag:'wx',mode:0o600});
  }};
  f.runtime = {emailWorkspaceRoot:root,withReadTab:async callback=>{f.tabs++;return callback(tab);}};
  f.adapter = {
    discover:async sameTab=>{
      assert.equal(sameTab,tab);f.scans++;
      return {listed:{url:'https://mail.google.com/mail/u/0/#search/test'},discovery:{schema_version:1,provider:'gmail',status:'partial',account_address:'owner@example.test',
        candidates:[...f.targets],coverage:{scope:'inbox_unread',lane:'unread',scan_complete:false,scanned_rows:f.targets.length,unsupported_rows:0,limited:true},observed_at:new Date().toISOString()}};
    },
    collect:async (sameTab,provider,options,_listed,recovering)=>{
      assert.equal(sameTab,tab);assert.equal(provider,'gmail');
      f.current=options.pinned_message_id;
      events.push(`open:${f.current}:${recovering}`);
      // Both the bounded page and exact target journal exist before any opening effect.
      const pages=await fs.readdir(path.join(root,'email',request().owner_scope,'pages'));
      const saved=JSON.parse(await fs.readFile(path.join(root,'email',request().owner_scope,'pages',pages[0])));
      assert.equal(saved.complete,false);
      const invocation=pageCaptureInvocation(request(),'gmail',target(f.current));
      const digest=crypto.createHash('sha256').update(`gmail\0${invocation}`).digest('hex');
      const journal=JSON.parse(await fs.readFile(path.join(root,'email',request().owner_scope,'invocations',`${digest}.json`)));
      assert.equal(journal.identity.provider_message_id,f.current);
      if(f.onCollect)await f.onCollect(options);
      const message={...target(f.current),subject:'Fixture',inventory_complete:true,attachments:[],body_text:'Body',read_state:'unread',original:{selector:'original'}};
      await options.onSelected(message);
      return message;
    },
    markRead:async()=>{events.push(`mark:${f.current}`);return 'read';},
    restore:async sameTab=>{assert.equal(sameTab,tab);events.push('restore');},
  };
  f.run=overrides=>capturePage({...request(),...overrides},f.runtime,'gmail',f.adapter);
  return f;
}

test('page captures N originals and confirms read serially in one tab, with durable selection before effects',async t=>{
  const f=await fixture(t);const result=await f.run();
  assert.equal(f.tabs,1);assert.equal(f.scans,1);
  assert.deepEqual(f.events,['open:a:false','download:a','mark:a','restore','open:b:false','download:b','mark:b']);
  assert.equal(result.captures.length,2);assert.deepEqual(result.failures,[]);
  assert.ok(result.captures.every(c=>c.result.capture.read_state==='read'));
  assert.deepEqual(result.discovery_options,request().discovery);
  assert.match(result.page_id,/^page_[a-f0-9]{64}$/u);
  const replay=await f.run({discovery:{...request().discovery,continuation:`${'b'.repeat(64)}:1`}});
  assert.deepEqual(replay,result);assert.equal(f.tabs,1);
  f.targets=[...f.targets,target('c')];
  const next=await f.run({ack_page_id:result.page_id});
  assert.notEqual(next.page_id,result.page_id);assert.equal(f.scans,2);
  assert.equal(f.events.filter(e=>e==='download:a').length,1);
  assert.equal(f.events.filter(e=>e.startsWith('open:c')).length,1);
});

test('interrupted page resumes saved selection even when unread discovery would no longer include completed mail',async t=>{
  const f=await fixture(t);const abort=new AbortController();f.runtime.signal=abort.signal;
  f.onDownload=async()=>{if(f.current==='b'){abort.abort();throw new Error('interrupted');}};
  await assert.rejects(f.run());
  f.targets=[];f.onDownload=null;f.runtime.signal=new AbortController().signal;
  const result=await f.run();
  assert.equal(f.scans,1);assert.equal(result.captures.length,2);
  assert.equal(f.events.filter(e=>e==='download:a').length,1);
  assert.ok(f.events.includes('open:b:true'));
  assert.deepEqual(result.failures,[]);
});

test('individual identity failure retains prior success and never downloads the wrong message',async t=>{
  const f=await fixture(t);
  f.onCollect=async options=>{if(f.current==='b')await options.onSelected({...target('other'),subject:'Fixture'});};
  const result=await f.run();
  assert.equal(result.captures.length,1);assert.equal(result.failures.length,1);
  assert.equal(result.failures[0].target.provider_message_id,'b');
  assert.equal(result.failures[0].error_code,'email_capture_invalid');
  assert.equal(f.events.includes('download:b'),false);
});

test('completed page replay verifies original files before returning or acknowledging receipts',async t=>{
  const f=await fixture(t);const result=await f.run();
  const manifestPath=path.join(f.root,result.captures[0].result.capture.manifest_path);
  const manifest=JSON.parse(await fs.readFile(manifestPath));
  await fs.writeFile(path.join(f.root,manifest.files[0].path),'corrupt');
  await assert.rejects(f.run(),{code:'email_capture_invalid'});
  assert.equal(f.tabs,1);
});

test('page contract rejects oversized pages and cross-operation acknowledgements',()=>{
  validateCaptureInput(request(),'gmail');
  for(const invalid of [
    {...request(),discovery:{...request().discovery,limit:51}},
    {...request(),ack_page_id:'wrong'},
    {...request(),operation:'discover',ack_page_id:`page_${'a'.repeat(64)}`},
  ])assert.throws(()=>validateCaptureInput(invalid,'gmail'),{code:'invalid_request'});
});

test('unacknowledged failures retry the saved targets without repeating completed captures',async t=>{
  const f=await fixture(t);f.onCollect=async()=>{if(f.current==='b')throw Object.assign(new Error('failed'),{code:'email_pinned_message_unavailable'});};
  const first=await f.run();assert.equal(first.failures.length,1);
  f.targets=[];f.onCollect=null;
  const recovered=await f.run();
  assert.equal(recovered.page_id,first.page_id);assert.equal(f.scans,1);
  assert.equal(recovered.captures.length,2);assert.deepEqual(recovered.failures,[]);
  assert.equal(f.events.filter(e=>e==='download:a').length,1);
});

test('page target invocation matches the Gateway cross-language identity fixture',()=>{
  assert.equal(pageCaptureInvocation(request(),'gmail',{...target('message'),account_address:'Owner@Example.Test'}),
    'email_capture_c3ac21b3fbf0a810eddacec1bdf30c1221425019c39edb35686a4d7b7b1ff496');
});

test('production QQ page adapter initializes once and reads every selected original in the same list tab',async t=>{
  const {collectEmailPage,READ_PROVIDERS}=await import('../../../scripts/email/read.mjs');
  const f=await fixture(t);const url=READ_PROVIDERS.qq_mail.url;
  const rows=['a','b'].map(id=>({...target(id),subject:'Fixture',unread:true}));
  let setups=0,tabs=0,downloads=0,opened='';
  const tab={
    inspect:async expression=>{
      const call=expression.match(/\)\("qq_mail","([^"]+)",(.*?)(?:,\(function|\);)/u);assert.ok(call);
      const phase=call[1],expected=JSON.parse(call[2]);
      const result=phase==='list'?{rows,empty:false}:phase==='detail'?{...expected,subject:'Fixture',inventory_complete:true,attachments:[],body_text:'Body',read_state:'read'}:
        phase==='menu'?{commands:['Export as eml file','Mark as unread']}:{};
      return {origin:url,result:{url,account_address:'owner@example.test',...result}};
    },
    runReadCode:async code=>{
      if(code.includes('page.reload'))setups++;
      if(code.includes('return state?'))return {rows,total_count:2,receipt_evidence:true,unsupported_rows:0,folders:{folders:[{id:1,folder:'inbox'}],unsupported:0}};
      assert.equal(code.includes('page.goto'),false,'live batch must not reload list for each mail');
      return true;
    },
    click:async selector=>{opened=selector.match(/data-mailid="([^"]+)"/u)?.[1]??opened;},
    press:async()=>{},
    download:async(_selector,destination)=>{assert.ok(['a','b'].includes(opened));downloads++;await fs.writeFile(destination,eml,{flag:'wx',mode:0o600});},
  };
  const result=await collectEmailPage({...request(),provider:'qq_mail'},{emailWorkspaceRoot:f.root,withReadTab:async callback=>{tabs++;return callback(tab);}},'qq_mail');
  assert.equal(setups,1);assert.equal(tabs,1);assert.equal(downloads,2);
  assert.deepEqual(result.failures,[]);assert.equal(result.captures.length,2);
});

test('production Gmail page adapter captures proven conversation members and excludes drafts without repeating setup',async t=>{
  const {collectEmailPage,READ_PROVIDERS}=await import('../../../scripts/email/read.mjs');
  const f=await fixture(t),url=READ_PROVIDERS.gmail.url;
  const row={provider_message_id:'b',provider_selection_id:'b',provider_thread_id:'thread-f:10',unread:true,single_message_row:false,subject:'Fixture'};
  const members=['a','b','c'].map(id=>({provider_message_id:id,provider_thread_id:'thread-f:10',unread:true,inbox:true,sent:false,draft:id==='c',observed_message_count:3}));
  let setups=0,queries=0,tabs=0,downloads=0;
  const tab={
    inspect:async expression=>{
      const call=expression.match(/\)\("gmail","([^"]+)",([^\n]*)\);/u);assert.ok(call);
      const phase=call[1],expected=JSON.parse(call[2]);
      const result=phase==='list'?{rows:[row],empty:false}:phase==='detail'?{...expected,subject:'Fixture',inventory_complete:true,attachments:[],body_text:'Body',read_state:'read'}:
        phase==='menu'?{commands:['Download message','Mark as unread']}:{};
      return {origin:url,result:{url,account_address:'owner@example.test',...result}};
    },
    runReadCode:async code=>{if(code.includes('page.reload'))setups++;return code.includes('return state.records')?members:true;},
    click:async()=>{},fill:async()=>{queries++;},press:async()=>{},
    download:async(_selector,destination)=>{downloads++;await fs.writeFile(destination,eml,{flag:'wx',mode:0o600});},
  };
  const result=await collectEmailPage(request(),{emailWorkspaceRoot:f.root,withReadTab:async callback=>{tabs++;return callback(tab);}},'gmail');
  assert.equal(setups,1);assert.equal(queries,1);assert.equal(tabs,1);assert.equal(downloads,2);
  assert.deepEqual(result.failures,[]);
  assert.deepEqual(result.captures.map(c=>c.target.provider_message_id),['a','b']);
  assert.ok(result.discovery.coverage.scanned_rows >= 2, 'one thread contains two logical message observations');
});


test('partial original from extraction limits is replayable and acknowledgeable without redownload',async t=>{
  const f=await fixture(t);
  f.targets=[target('a')];
  f.bytes=Buffer.from(['From: sender@example.test','To: owner@example.test','Subject: Fixture','MIME-Version: 1.0','Content-Type: multipart/mixed; boundary="many"','',
    '--many','Content-Type: text/plain','','Body',
    ...Array.from({length:21},(_,i)=>['--many','Content-Type: application/octet-stream',`Content-Disposition: attachment; filename="part-${i}.txt"`,'','attachment'].join('\r\n')),
    '--many--',''].join('\r\n'));
  const first=await f.run();assert.equal(first.captures[0].result.status,'partial');assert.deepEqual(first.failures,[]);
  const replay=await f.run();assert.equal(replay.page_id,first.page_id);assert.equal(f.tabs,1);
  const next=await f.run({ack_page_id:first.page_id});assert.notEqual(next.page_id,first.page_id);
  assert.equal(f.events.filter(e=>e==='download:a').length,1);
});

test('new page can reuse verified captured mail after a folder or locator change without reopening',async t=>{
  const f=await fixture(t);f.targets=[target('a')];
  const first=await f.run();
  f.targets=[{...target('a'),folder:'all',provider_selection_id:'new-thread'}];
  const next=await f.run({ack_page_id:first.page_id});
  assert.deepEqual(next.failures,[]);
  assert.equal(next.captures[0].target.folder,'all');
  assert.equal(next.captures[0].result.capture.manifest_path,first.captures[0].result.capture.manifest_path);
  assert.equal(f.events.filter(e=>e==='download:a').length,1);
  assert.equal(f.events.filter(e=>e.startsWith('open:a')).length,1);
});


test('unread and recent lanes share one verified original while keeping independent page acknowledgements',async t=>{
  const f=await fixture(t);f.targets=[target('a')];
  const unread=await f.run();
  const originalDiscover=f.adapter.discover;
  f.adapter.discover=async (...args)=>{const value=await originalDiscover(...args);value.discovery.coverage.lane='recent_inbound';return value;};
  const recent=await f.run({invocation_id:'email_page_job_recent_inbound',discovery:{...request().discovery,lane:'recent_inbound',interval_start:'2026-09-08T00:00:00Z',interval_end:'2026-09-09T00:00:00Z'}});
  assert.notEqual(recent.page_id,unread.page_id);
  assert.deepEqual(recent.captures[0].result.capture,unread.captures[0].result.capture);
  assert.equal(f.events.filter(e=>e==='download:a').length,1);
  assert.equal(f.events.filter(e=>e.startsWith('open:a')).length,1);
});

test('page replay reconciles read confirmation with its current durable read-state record',async t=>{
  const f=await fixture(t);f.targets=[target('a')];const first=await f.run();
  assert.equal(first.captures[0].result.capture.read_state,'read');
  const statePath=path.join(path.dirname(path.join(f.root,first.captures[0].result.capture.manifest_path)),'read-state.json');
  await fs.writeFile(statePath,JSON.stringify({schema_version:1,state:'unknown',observed:'unknown'}));
  const replay=await f.run();assert.equal(replay.captures[0].result.capture.read_state,'unknown');
  assert.equal(f.tabs,1);
});

test('production Outlook page adapter proves an omitted list account through the current account menu',async t=>{
  const {collectEmailPage,READ_PROVIDERS}=await import('../../../scripts/email/read.mjs');
  const f=await fixture(t),url=READ_PROVIDERS.outlook.url;
  const row={provider_selection_id:'thread-a',unread:true,subject:'Fixture'};
  const record={provider_selection_id:'thread-a',provider_message_id:'message-a',unread:true};
  let setups=0,accountChecks=0,downloads=0;
  const tab={
    inspect:async expression=>{
      const call=expression.match(/\)\("outlook","([^"]+)",([^\n]*)\);/u);assert.ok(call);
      const phase=call[1],expected=JSON.parse(call[2]);
      const result=phase==='list'?{rows:[row],empty:false}:phase==='filter'?{filtered:true}:
        phase==='account'?{account_address:'owner@example.test'}:
        phase==='detail'?{...expected,account_address:'owner@example.test',subject:'Fixture',inventory_complete:true,attachments:[],body_text:'Body',read_state:'read'}:
        phase==='menu'?{commands:expected.original_eml?['Download as eml']:['Download','Mark as unread']}:{};
      if(phase==='account')accountChecks++;
      return {origin:url,result:{url,...result}};
    },
    runReadCode:async code=>{if(code.includes('page.reload'))setups++;return code.includes('return state?.records')?[record]:true;},
    click:async()=>{},press:async()=>{},
    download:async(_selector,destination)=>{downloads++;await fs.writeFile(destination,eml,{flag:'wx',mode:0o600});},
  };
  const result=await collectEmailPage({...request(),provider:'outlook'},{emailWorkspaceRoot:f.root,withReadTab:callback=>callback(tab)},'outlook');
  assert.equal(setups,1);assert.equal(accountChecks,2);assert.equal(downloads,1);
  assert.deepEqual(result.failures,[]);assert.equal(result.captures[0].target.provider_message_id,'message-a');
});

test('acknowledged sub-pages drain all51 frozen thread members after unread search loses the thread',async t=>{
  const {collectEmailPage,READ_PROVIDERS}=await import('../../../scripts/email/read.mjs');
  const f=await fixture(t),url=READ_PROVIDERS.gmail.url;
  const row={provider_message_id:'32',provider_selection_id:'32',provider_thread_id:'thread-f:10',unread:true,single_message_row:false,subject:'Fixture'};
  const members=Array.from({length:51},(_,i)=>({provider_message_id:i.toString(16),provider_thread_id:'thread-f:10',unread:true,inbox:true,sent:false,draft:false,observed_message_count:51}));
  let allRead=false,query='',setups=0,downloads=0;
  const tab={
    inspect:async expression=>{
      const call=expression.match(/\)\("gmail","([^"]+)",([^\n]*)\);/u);assert.ok(call);
      const phase=call[1],expected=JSON.parse(call[2]);
      const hidden=allRead&&query.includes('is:unread');
      const result=phase==='list'?{rows:hidden?[]:[{...row,unread:!allRead}],empty:hidden}:phase==='detail'?{...expected,subject:'Fixture',inventory_complete:true,attachments:[],body_text:'Body',read_state:'read'}:
        phase==='menu'?{commands:['Download message','Mark as unread']}:{};
      return {origin:url,result:{url,account_address:'owner@example.test',...result}};
    },
    runReadCode:async code=>{if(code.includes('page.reload'))setups++;return code.includes('return state.records')?members.map(m=>({...m,unread:!allRead})):true;},
    click:async()=>{},fill:async(_selector,value)=>{query=value;},press:async()=>{},
    download:async(_selector,destination)=>{allRead=true;downloads++;await fs.writeFile(destination,eml,{flag:'wx',mode:0o600});},
  };
  const runtime={emailWorkspaceRoot:f.root,withReadTab:callback=>callback(tab)};
  const first=await collectEmailPage(request(),runtime,'gmail');
  assert.equal(first.captures.length,50);assert.deepEqual(first.failures,[]);assert.equal(setups,1);
  const second=await collectEmailPage({...request(),ack_page_id:first.page_id},runtime,'gmail');
  assert.equal(second.captures.length,1);assert.deepEqual(second.failures,[]);
  assert.equal(second.captures[0].target.provider_message_id,'32');
  assert.equal(second.discovery_options.continuation,first.discovery.coverage.continuation);
  assert.notEqual(second.page_id,first.page_id);assert.equal(downloads,51);
  // Second sub-page only initializes pinned recovery, never a fresh unread scan.
  assert.equal(query,'in:inbox');assert.equal(setups,2);
});

test('observed empty bounded batches finish without mail effects and preserve partial scope/checkpoint',async t=>{
  for(const reason of ['loaded_rows_only','native_folder_scan_partial','folder_scope_and_pagination_unqualified']) {
    const f=await fixture(t);f.targets=[];
    const discover=f.adapter.discover;
    f.adapter.discover=async tab=>{
      const result=await discover(tab);
      result.discovery.coverage.reason=reason;
      result.discovery.coverage.continuation=`${'b'.repeat(64)}:1`;
      return result;
    };
    const result=await f.run();
    assert.equal(result.status,'empty');
    assert.equal(result.discovery.status,'partial');
    assert.equal(result.discovery.coverage.scan_complete,false);
    assert.equal(result.discovery.coverage.reason,reason);
    assert.equal(result.discovery.coverage.continuation,`${'b'.repeat(64)}:1`);
    assert.deepEqual(result.captures,[]);assert.deepEqual(result.failures,[]);
    assert.deepEqual(f.events,[]);assert.equal(f.tabs,1);assert.equal(f.scans,1);
    const replay=await f.run();
    assert.deepEqual(replay,result);assert.equal(f.tabs,1,'unacknowledged empty page must replay without another browser');
  }
});

test('unsupported or evidence-unavailable zero-candidate batches remain partial',async t=>{
  for(const [reason,unsupported] of [['loaded_rows_only',1],['receipt_order_and_folder_scope_unqualified',0],['new_unknown_evidence_gap',0],['',0]]) {
    const f=await fixture(t);f.targets=[];
    const discover=f.adapter.discover;
    f.adapter.discover=async tab=>{
      const result=await discover(tab);
      result.discovery.coverage.reason=reason;
      result.discovery.coverage.unsupported_rows=unsupported;
      result.discovery.coverage.scanned_rows=unsupported;
      return result;
    };
    const result=await f.run();
    assert.equal(result.status,'partial');
    assert.deepEqual(f.events,[]);
    assert.equal(result.discovery.coverage.reason,reason);
    assert.equal(result.discovery.coverage.unsupported_rows,unsupported);
  }
});
