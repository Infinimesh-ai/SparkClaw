import fs from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import crypto from 'node:crypto';
import {PlaywrightCLIClientFactory} from '../../../tools/browser-controller/src/cli-client.mjs';
import {ProviderScriptRegistry} from '../../../tools/browser-controller/src/provider-scripts.mjs';
import {READ_PROVIDERS,collectUnread,markRead} from '../../../scripts/email/read.mjs';
import {captureUnread} from '../../../scripts/email/lib/read-capture.mjs';
import {prepareOutlookList,outlookListEvidence} from '../../../scripts/email/lib/outlook-list.mjs';

const root=process.env.SPARKCLAW_READ_QUAL_ROOT;
const output=path.join(root,'data/qualifications/email-read-loop-20260908');
await fs.chmod(output,0o700);
const provider=process.env.SPARKCLAW_READ_QUAL_PROVIDER??'gmail';
const config=READ_PROVIDERS[provider];
if(!config) throw new Error('invalid provider');
const chunks=[];for await(const chunk of process.stdin) chunks.push(chunk);
const payload=JSON.parse(Buffer.concat(chunks).toString());for(const chunk of chunks)chunk.fill(0);
const handler=async(_input,runtime)=>{
  if(process.env.SPARKCLAW_READ_QUAL_MODE==='pinned_check')return runtime.withReadTab(async tab=>{
    const owner=crypto.createHash('sha256').update('owner').digest('hex');
    const name=crypto.createHash('sha256').update(provider+'\0'+_input.invocation_id).digest('hex')+'.json';
    const journal=JSON.parse(await fs.readFile(path.join(workspace,'email',owner,'invocations',name),'utf8'));
    let pins=0;
    let result;
    try{result=await collectUnread(tab,provider,{pinned_message_id:journal.identity.provider_message_id,pinned_selection_id:journal.selection.provider_selection_id,capture_required:false,onSelected:async row=>{if(row.provider_message_id!==journal.identity.provider_message_id)throw new Error('wrong pinned message');pins++;}});}catch(e){
      const rows=await tab.runReadCode(`async page=>page.evaluate(()=>[...document.querySelectorAll('tr.zA')].filter(n=>n.getBoundingClientRect().height).map(n=>({id:n.querySelector('[data-legacy-last-message-id]')?.getAttribute('data-legacy-last-message-id'),senderClasses:[...n.querySelectorAll('.yW *')].map(c=>c.className)})))`);
      await fs.writeFile(path.join(output,'gmail-pinned-failure.json'),JSON.stringify({code:e.code,rows},null,2),{mode:0o600});throw e;
    }
    return {provider,stage:'pinned_identity_only',identity_matches:result.provider_message_id===journal.identity.provider_message_id,pins};
  });
  if(process.env.SPARKCLAW_READ_QUAL_MODE==='outlook_fibers')return runtime.withReadTab(async tab=>{
    const fibers=await tab.runReadCode(`async page=>page.evaluate(()=>{
      const result=[];let n=document.querySelector('[role="option"][data-convid]');
      for(let level=0;n&&level<10;level++,n=n.parentElement){
        const key=Object.keys(n).find(k=>k.startsWith('__reactFiber'));if(!key)continue;let f=n[key];
        for(let depth=0;f&&depth<30;depth++,f=f.return){const p=f.memoizedProps;if(!p||typeof p!=='object')continue;
          result.push({level,depth,keys:Object.keys(p),fields:Object.fromEntries(Object.entries(p).filter(([k,v])=>!/children|style/u.test(k)).map(([k,v])=>[k,v&&typeof v==='object'?Object.keys(v).slice(0,60):typeof v==='function'?'function':v]))});
        }break;
      }return result;
    })`);
    await fs.writeFile(path.join(output,'outlook-list-fibers.json'),JSON.stringify(fibers,null,2),{mode:0o600});return {stage:'list_fibers_only',layers:fibers.length};
  });
  if(process.env.SPARKCLAW_READ_QUAL_MODE==='outlook_initial')return runtime.withReadTab(async tab=>{
    await tab.runReadCode(`async page=>{await page.addInitScript(()=>{
      const state={records:[],items:[],requests:[]};globalThis.__readQualification=state;
      const project=(v)=>{if(!v||typeof v!=='object')return;
        if(Array.isArray(v.Conversations))state.records.push(...v.Conversations.map(c=>Object.fromEntries(Object.entries(c).filter(([k])=>/Id|Count|Read/u.test(k)))));
        if(v.ItemId?.Id)state.items.push({id:v.ItemId.Id,isRead:v.IsRead,internetMessageId:v.InternetMessageId??null,keys:Object.keys(v)});
        for(const x of Object.values(v))if(x&&typeof x==='object')project(x);
      };
      const observe=(url,value)=>{try{state.requests.push(new URL(url,location.href).pathname);project(JSON.parse(value));}catch{}};
      const open=XMLHttpRequest.prototype.open;
      XMLHttpRequest.prototype.open=function(method,url,...args){this.addEventListener('load',()=>{if(!this.responseType||this.responseType==='text')observe(url,this.responseText);},{once:true});return open.call(this,method,url,...args);};
      const fetch=globalThis.fetch;globalThis.fetch=async function(...args){const r=await fetch.apply(this,args);void r.clone().text().then(t=>observe(r.url,t)).catch(()=>{});return r;};
    });await page.reload();return true;}`);
    const network=await tab.runReadCode(`async page=>page.evaluate(async()=>{const end=Date.now()+10000;while(!globalThis.__readQualification?.records.length&&Date.now()<end)await new Promise(resolve=>setTimeout(resolve,100));return globalThis.__readQualification;})`);
    await fs.writeFile(path.join(output,'outlook-initial-network.json'),JSON.stringify(network,null,2),{mode:0o600});
    const id=network.records[0]?.ItemIds?.[0]?.Id;
    if(id&&network.records[0].GlobalMessageCount===1&&network.records[0].GlobalUnreadCount===0){
      await tab.click(`[role="option"][data-convid="${network.records[0].ConversationId.Id}"]:visible`);
      const detail=await tab.runReadCode(`async page=>page.evaluate(async()=>{const end=Date.now()+10000;while(!globalThis.__readQualification?.items.length&&Date.now()<end)await new Promise(resolve=>setTimeout(resolve,100));return {url:location.href,items:globalThis.__readQualification.items,bodies:[...document.querySelectorAll('[id^="UniqueMessageBody"]')].map(n=>({attrs:Object.fromEntries([...n.attributes].map(a=>[a.name,a.value]))})),ids:[...document.querySelectorAll('[data-itemid],[data-item-id],[data-convid]')].slice(0,12).map(n=>Object.fromEntries([...n.attributes].filter(a=>a.name.startsWith('data-')).map(a=>[a.name,a.value])))};})`);
      await fs.writeFile(path.join(output,'outlook-item-evidence.json'),JSON.stringify(detail,null,2),{mode:0o600});
      const fibers=await tab.runReadCode(`async page=>page.evaluate(()=>{
        const result=[];let n=document.querySelector('[id^="UniqueMessageBody"]');
        for(let level=0;n&&level<15;level++,n=n.parentElement){
          const key=Object.keys(n).find(k=>k.startsWith('__reactFiber'));
          if(!key)continue;let f=n[key];
          for(let depth=0;f&&depth<20;depth++,f=f.return){const p=f.memoizedProps;if(!p||typeof p!=='object')continue;result.push({level,depth,keys:Object.keys(p),ids:Object.fromEntries(Object.entries(p).filter(([k,v])=>typeof v==='string'&&/item.*id|conversation.*id|internetMessageId/iu.test(k)))});}
          break;
        }
        return result;
      })`);
      await fs.writeFile(path.join(output,'outlook-fibers.json'),JSON.stringify(fibers,null,2),{mode:0o600});
      return {provider,stage:'existing_read_item_only',route_matches:decodeURIComponent(new URL(detail.url).pathname.split('/').at(-1))===id,item_observed:detail.items.some(item=>item.id===id),bodies:detail.bodies.length};
    }
    return {provider,stage:'initial_list_only',records:network.records.length};
  });
  if(['capture','known_outlook'].includes(process.env.SPARKCLAW_READ_QUAL_MODE)) {
    const trace=[];
    let collector=collectUnread;
    if(process.env.SPARKCLAW_READ_QUAL_MODE==='known_outlook') {
      _input.invocation_id='read-loop-outlook-known-item-1';
      collector=async(tab,p,options)=>{
        if(options.pinned_message_id)return collectUnread(tab,p,options);
        const owner=crypto.createHash('sha256').update('owner').digest('hex');
        const journalName=crypto.createHash('sha256').update('outlook\0email_live_read_source_outlook_20260907').digest('hex')+'.json';
        const old=JSON.parse(await fs.readFile(path.join(root,'data/workspaces/email',owner,'invocations',journalName),'utf8'));
        const filtered=await tab.inspect(`()=>!!document.querySelector('button[aria-label="Unread"]')`);
        if(filtered.result)await tab.click('button:has-text("Clear filter")');
        const key=await prepareOutlookList(tab);
        const listed=await outlookListEvidence(tab,key,{rows:[{provider_selection_id:old.selection.provider_selection_id,unread:false}]});
        const record=listed.rows[0];
        if(!record.single_message_proven)throw new Error('known singleton unavailable');
        return collectUnread(tab,p,{...options,pinned_message_id:record.provider_message_id,pinned_selection_id:record.provider_selection_id});
      };
    }
    try {
      const result=await captureUnread(_input,{...runtime,withReadTab:callback=>runtime.withReadTab(tab=>callback({...tab,inspect:async code=>{try{const value=await tab.inspect(code);trace.push(value.result);return value;}catch(e){trace.push({inspect_error:e.code,reason:e.diagnosticReason});throw e;}}}))},provider,{collectUnread:collector,markRead:async(...args)=>{try{return await markRead(...args);}catch(e){trace.push({mark_error:e.code,reason:e.diagnosticReason});throw e;}}});
      await fs.writeFile(path.join(output,provider+(process.env.SPARKCLAW_READ_QUAL_MODE==='known_outlook'?'-known':'')+'-receipt.json'),JSON.stringify(result,null,2),{mode:0o600});
      return {provider,status:result.status,read_state:result.capture?.read_state??null,attachments:result.capture?.attachments_count??0};
    } finally {await fs.writeFile(path.join(output,provider+'-capture-trace.json'),JSON.stringify(trace,null,2),{mode:0o600});}
  }
  return runtime.withReadTab(async tab=>{
  if(provider==='gmail') {
    await tab.runReadCode(`async page=>page.evaluate(()=>{
      const original=XMLHttpRequest.prototype.open;
      globalThis.__readQualification={captured:[]};
      XMLHttpRequest.prototype.open=function(method,url,...args){
        if(new URL(url,location.href).pathname.endsWith('/i/bv'))this.addEventListener('load',()=>{
          if(!this.responseType||this.responseType==='text')globalThis.__readQualification.captured.push(this.responseText.slice(0,35000));
        },{once:true});
        return original.call(this,method,url,...args);
      };
      globalThis.__readQualification.restore=()=>{XMLHttpRequest.prototype.open=original;};
      return true;
    })`);
    await tab.fill('input[name="q"]','in:inbox is:unread');await tab.press('Enter');
    const network=await tab.runReadCode(`async page=>page.evaluate(async()=>{
      const deadline=Date.now()+5000;
      while(!globalThis.__readQualification.captured.length&&Date.now()<deadline)await new Promise(resolve=>setTimeout(resolve,100));
      const captured=globalThis.__readQualification.captured;
      globalThis.__readQualification.restore();delete globalThis.__readQualification;
      return {captured};
    })`);
    await fs.writeFile(path.join(output,'gmail-network.json'),JSON.stringify(network),{mode:0o600});
  }
  if(provider==='outlook') {
    await tab.runReadCode(`async page=>page.evaluate(()=>{
      const original=globalThis.fetch;
      globalThis.__readQualification={captured:[]};
      globalThis.fetch=async function(...args){
        const response=await original.apply(this,args);
        if(new URL(response.url).origin===location.origin)void response.clone().text().then(value=>{
          if(value.includes('Conversations')||value.includes('ConversationId'))globalThis.__readQualification.captured.push({path:new URL(response.url).pathname,value:value.slice(0,45000)});
        }).catch(()=>{});
        return response;
      };
      globalThis.__readQualification.restore=()=>{globalThis.fetch=original;};return true;
    })`);
    const before=await tab.runReadCode(`async page=>page.evaluate(()=>({rows:[...document.querySelectorAll('[role="option"][data-convid]')].slice(0,8).map(n=>({attrs:Object.fromEntries([...n.attributes].map(a=>[a.name,a.value])),html:n.outerHTML})),resources:performance.getEntriesByType('resource').map(r=>new URL(r.name).pathname).filter(p=>p.includes('service')||p.includes('owa'))}))`);
    await fs.writeFile(path.join(output,'outlook-before.json'),JSON.stringify(before),{mode:0o600});
    await tab.click('button[aria-label="Filter"]');await tab.click('[role="menuitemradio"][title="Unread"]');
    await tab.runReadCode(`async page=>page.evaluate(async()=>{const end=Date.now()+5000;while(!document.querySelector('.ksePc')&&Date.now()<end)await new Promise(resolve=>setTimeout(resolve,100));return true;})`);
    await tab.click('button:has-text("Clear filter")');
    const network=await tab.runReadCode(`async page=>page.evaluate(async()=>{const end=Date.now()+5000;while(!globalThis.__readQualification.captured.length&&Date.now()<end)await new Promise(resolve=>setTimeout(resolve,100));const captured=globalThis.__readQualification.captured;globalThis.__readQualification.restore();delete globalThis.__readQualification;return {captured};})`);
    await fs.writeFile(path.join(output,'outlook-network.json'),JSON.stringify(network),{mode:0o600});
  }
  const observation=await tab.runReadCode(`async page=>page.evaluate(provider=>{
    const visible=n=>{const r=n.getBoundingClientRect();return r.width>0&&r.height>0&&getComputedStyle(n).visibility!=='hidden';};
    const encode=(n,depth)=>({tag:n.tagName,attrs:Object.fromEntries([...n.attributes].map(a=>[a.name,a.value])),text:n.children.length?null:n.textContent,children:depth>0?[...n.children].filter(visible).map(child=>encode(child,depth-1)):[]});
    const selector=provider==='gmail'?'tr.zA':provider==='outlook'?'[role="option"][data-convid]':'.mail-list-page-item[data-mailid]';
    return {empty:provider==='outlook'?[...document.querySelectorAll('.ksePc')].filter(visible).map(n=>n.innerText):[],rows:[...document.querySelectorAll(selector)].filter(visible).slice(0,8).map(n=>encode(n,12)),buttons:[...document.querySelectorAll('button')].filter(visible).map(n=>({label:n.getAttribute('aria-label'),title:n.title,text:n.innerText})).slice(0,60)};
  },${JSON.stringify(provider)})`);
  await fs.writeFile(path.join(output,provider+'-list.json'),JSON.stringify(observation,null,2),{mode:0o600});
  return {provider,rows:observation.rows.length,stage:'list_inspection_only'};
});};
const entry={provider,operation:'read',scriptID:provider+'.read_loop_qualification',revision:1,loginURL:config.url,origins:config.origins,downloadOrigins:[...config.origins,...(provider==='gmail'?['https://mail-attachment.googleusercontent.com']:[])],timeoutMS:180000,handler,sourceFiles:['data/qualifications/email-read-loop-20260908/inspect.mjs'],validate:()=>{}};
const workspace=path.join(output,'workspace');await fs.mkdir(workspace,{recursive:true,mode:0o700});
const factory=new PlaywrightCLIClientFactory({registry:new ProviderScriptRegistry([entry]),emailWorkspaceRoot:workspace,runtimeRoot:path.join(output,'cli-runtime'),executablePath:path.join(os.homedir(),'.local/share/sparkclaw/browser-controller/bin/browser-bridge-launcher'),userDataDir:path.join(os.homedir(),'.local/share/sparkclaw/browser/default/user-data'),diagnostic:value=>console.error(JSON.stringify(value))});
try{
 await factory.prepare();const result=await factory.runScript({token:payload.token,sessionID:'session_'+crypto.randomBytes(16).toString('hex'),provider,operation:'read',scriptID:entry.scriptID,revision:1,input:{schema_version:1,operation:'read',provider,account:'default',owner_scope:crypto.createHash('sha256').update('owner').digest('hex'),invocation_id:'read-loop-'+provider+'-1'}});
 if(result.state!=='completed')throw Object.assign(new Error('not_completed'),{code:result.result?.code??'not_completed'});console.log(JSON.stringify(result.result));
}catch(e){console.error(JSON.stringify({code:e.code??'qualification_failed',reason:e.diagnosticReason??'unclassified'}));process.exitCode=1;}finally{payload.token='';}
