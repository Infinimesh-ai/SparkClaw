import fs from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import crypto from 'node:crypto';
import { createRequire } from 'node:module';
import { PlaywrightCLIClientFactory } from '../../../tools/browser-controller/src/cli-client.mjs';
import { ProviderScriptRegistry } from '../../../tools/browser-controller/src/provider-scripts.mjs';
import { collectUnread } from '../../../scripts/email/read.mjs';

const root=process.env.SPARKCLAW_DOM_QUAL_ROOT;
const output=path.join(root,'data/qualifications/email-dom-20260907');
await fs.chmod(output,0o700);
const stdin=[];for await(const chunk of process.stdin)stdin.push(chunk);
const payload=JSON.parse(Buffer.concat(stdin).toString());
for(const chunk of stdin)chunk.fill(0);
const owner=crypto.createHash('sha256').update('owner').digest('hex');
const provider=process.env.SPARKCLAW_DOM_QUAL_PROVIDER??'outlook';
const invocation=provider==='outlook'?'email_live_read_source_outlook_20260907':provider==='gmail'?'email_live_read_be736d3d22a5684d':'email_live_read_source_qq_20260907';
const journalName=crypto.createHash('sha256').update(provider+'\0'+invocation).digest('hex')+'.json';
const journal=JSON.parse(await fs.readFile(path.join(root,'data/workspaces/email',owner,'invocations',journalName),'utf8'));
const pinned=journal.identity;
if(!pinned?.provider_message_id)throw new Error('qualification_pin_missing');
const prefix=provider==='outlook'?'https://outlook.live.com/mail/0/inbox/id/':provider==='gmail'?'https://mail.google.com/mail/u/0/#all/':'https://wx.mail.qq.com/home/index#/list/1';
const loginURL=prefix+(provider==='qq_mail'?'':encodeURIComponent(pinned.provider_message_id));
const origins=[new URL(loginURL).origin];
const normalize=value=>String(value??'').normalize('NFC').replace(/\s+/gu,' ').trim();
let reference=null;
if(journal.receipt?.capture){
 const original=path.join(root,'data/workspaces',path.dirname(journal.receipt.capture.manifest_path),'message.eml');
 const {simpleParser}=createRequire(path.join(root,'tools/browser-controller/package.json'))('mailparser');
 reference=await simpleParser(await fs.readFile(original));
}
const records=[];
let operations=0;
const started=Date.now();
const handler=async(input,runtime)=>runtime.withReadTab(async tab=>{
 const read=async code=>{operations++;return tab.runReadCode(code)};
 if(provider==='qq_mail'){
  const tracked=new Proxy(tab,{get(target,key){const value=target[key];return typeof value==='function'?async(...args)=>{operations++;return value.apply(target,args)}:value}});
  const message=await collectUnread(tracked,provider,{pinned_message_id:pinned.provider_message_id,pinned_selection_id:journal.selection.provider_selection_id,onSelected:async selected=>{if(selected.provider_message_id!==pinned.provider_message_id)throw new Error('qualification_identity_changed')}});
  await fs.writeFile(path.join(output,provider+'-observations.json'),JSON.stringify(message,null,2),{mode:0o600});
  const result={provider,sample:'existing_pinned_read_message',eml_reference:false,identity_matches:message.provider_message_id===pinned.provider_message_id,account_matches:message.account_address.toLowerCase()===pinned.account_address.toLowerCase(),subject_present:!!message.subject,body_characters:message.body_text.length,body_html_characters:message.body_html.length,read_state:message.read_state,attachment_inventory_complete:message.inventory_complete,operations,elapsed_ms:Date.now()-started,original_export_invoked:false};
  await fs.writeFile(path.join(output,provider+'-result.json'),JSON.stringify(result,null,2),{mode:0o600});return result;
 }
 if(provider==='gmail'){
  const selector='.adn[data-legacy-message-id='+JSON.stringify(pinned.provider_message_id)+']';
  await read(`async page=>{await page.locator(${JSON.stringify(selector+' .a3s')}).waitFor({state:'visible',timeout:15000});return true;}`);
  await read(`async page=>{await page.locator(${JSON.stringify(selector+' .ajy')}).evaluate(node=>node.click());return true;}`);
  for(let round=0;round<2;round++)records.push(await read(`async page=>page.evaluate(selector=>{
   const messages=document.querySelectorAll(selector);if(messages.length!==1)throw new Error('qualification_identity_ambiguous');
   const message=messages[0],body=message.querySelector('.a3s');
   const visible=node=>{const r=node.getBoundingClientRect();return r.width>0&&r.height>0&&getComputedStyle(node).visibility!=='hidden'};
   return {message_id:message.getAttribute('data-legacy-message-id'),account_label:document.querySelector('[aria-label^="Google Account:"]')?.getAttribute('aria-label'),subject:document.querySelector('h2.hP')?.innerText,
    body_text:body.innerText,body_html:body.innerHTML,body_count:message.querySelectorAll('.a3s').length,
    addresses:Array.from(message.querySelectorAll('[email]')).filter(node=>!body.contains(node)).map(node=>({email:node.getAttribute('email'),name:node.innerText,tag:node.tagName,class:node.className})),
    dates:Array.from(message.querySelectorAll('.g3')).filter(visible).map(node=>({text:node.innerText,title:node.getAttribute('title')})),
    detail_rows:Array.from(document.querySelectorAll('.ajA tr')).filter(visible).map(row=>Array.from(row.querySelectorAll('th,td')).map(cell=>cell.innerText)),
    controls:Array.from(message.querySelectorAll('[role="button"],button')).filter(visible).map(node=>({tag:node.tagName,class:node.className,label:node.getAttribute('aria-label'),tooltip:node.getAttribute('data-tooltip'),title:node.getAttribute('title')}))};
  },${JSON.stringify(selector)})`));
  await fs.writeFile(path.join(output,provider+'-observations.json'),JSON.stringify(records,null,2),{mode:0o600});
  const result={provider,sample:'existing_pinned_read_message',eml_reference:false,identity_matches:records.every(r=>r.message_id===pinned.provider_message_id),account_matches:records.every(r=>(r.account_label??'').toLowerCase().includes(pinned.account_address.toLowerCase())),subject_present:!!records[0].subject,body_count:records[0].body_count,body_characters:records[0].body_text.length,body_stable:records.every(r=>r.body_text===records[0].body_text),address_node_count:records[0].addresses.length,date_node_count:records[0].dates.length,detail_fields:records[0].detail_rows.map(row=>row[0]?.trim().toLowerCase()).filter(label=>/^(from|to|reply-to|date|subject|mailed-by|signed-by|security|cc):?$/.test(label)),details_stable:records.every(r=>JSON.stringify(r.detail_rows)===JSON.stringify(records[0].detail_rows)),operations,elapsed_ms:Date.now()-started,controls:records[0].controls.map(c=>({tag:c.tag,class:c.class,category:/details|more|recipient|to:|from:|read|unread/i.exec((c.label??'')+' '+(c.tooltip??'')+' '+(c.title??''))?.[0]?.toLowerCase()??'other'}))};
  await fs.writeFile(path.join(output,provider+'-result.json'),JSON.stringify(result,null,2),{mode:0o600});
  return result;
 }
 await read(`async page=>{await page.locator('[id^="UniqueMessageBody"]').waitFor({state:'visible',timeout:15000});return true;}`);
 for(let round=0;round<2;round++){
  const observed=await read(`async page=>page.evaluate(()=>{
   const body=document.querySelector('[id^="UniqueMessageBody"]');
   if(!body)throw new Error('qualification_body_missing');
   const visible=node=>{const r=node.getBoundingClientRect();return r.width>0&&r.height>0&&getComputedStyle(node).visibility!=='hidden'};
   let scope=body;
   while(scope.parentElement&&scope.parentElement.tagName!=='BODY'&&!scope.parentElement.querySelector('[role="option"][data-convid]'))scope=scope.parentElement;
   const leaf=Array.from(scope.querySelectorAll('*')).filter(node=>!node.children.length&&!body.contains(node)&&visible(node));
   const parents=[];for(let node=body,count=0;node&&count<7;node=node.parentElement,count++)parents.push({tag:node.tagName,role:node.getAttribute('role'),class:typeof node.className==='string'?node.className:'',bodyCount:node.querySelectorAll('[id^="UniqueMessageBody"]').length});
   return {url:location.href,body_text:body.innerText,body_html:body.innerHTML,header_text:leaf.map(node=>node.innerText??node.textContent).filter(Boolean),
    controls:Array.from(scope.querySelectorAll('button,[role="button"]')).filter(visible).map(node=>({tag:node.tagName,class:node.className,label:node.getAttribute('aria-label'),title:node.getAttribute('title'),text:node.innerText,parent_text:node.parentElement?.innerText,attrs:Array.from(node.attributes).map(attr=>attr.name)})),parents,
    mailbox_accounts:Array.from(document.querySelectorAll('[role="tree"] [role="treeitem"][aria-level="1"][data-folder-name]')).filter(visible).map(node=>node.getAttribute('title')).filter(value=>/^[^ <>]+@[^ <>]+$/.test(value??'')),
    date_candidates:leaf.map(node=>({text:node.innerText??node.textContent,title:node.getAttribute('title'),datetime:node.getAttribute('datetime')})).filter(value=>/\\b20\\d{2}\\b/.test(value.text??'')&&/\\d[:：]\\d/.test(value.text??'')),
    browser_timezone:Intl.DateTimeFormat().resolvedOptions().timeZone,
    address_nodes:leaf.filter(node=>/@/.test(node.textContent??'')).map(node=>({tag:node.tagName,class:node.className,text:node.textContent,attrs:Array.from(node.attributes).map(attr=>attr.name)})),
    body_count:scope.querySelectorAll('[id^="UniqueMessageBody"]').length};
  })`);
  records.push(observed);
 }
 await fs.writeFile(path.join(output,provider+'-observations.json'),JSON.stringify(records,null,2),{mode:0o600});
 const first=records[0];
 const corpus=normalize(first.header_text.join(' '));
 const accessibleHeader=normalize([...first.header_text,...first.controls.map(c=>c.label??'')].join(' '));
 const addresses=(list)=>list?.value?.map(item=>item.address.toLowerCase())??[];
 const result={provider,sample:'existing_pinned_read_message',body_count:first.body_count,identity_matches:decodeURIComponent(new URL(first.url).pathname.split('/').at(-1))===pinned.provider_message_id,
  body_stable:records.every(item=>normalize(item.body_text)===normalize(first.body_text)),body_characters:first.body_text.length,
  eml_reference:!!reference,subject_match:reference?corpus.includes(normalize(reference.subject)):null,
  body_text_match:reference?normalize(reference.text)===normalize(first.body_text):null,
  account_matches:first.mailbox_accounts.length===1&&first.mailbox_accounts[0].toLowerCase()===pinned.account_address.toLowerCase(),
  from_header_present:reference?addresses(reference.from).every(address=>accessibleHeader.toLowerCase().includes(address)):null,
  to_header_present:reference?addresses(reference.to).every(address=>accessibleHeader.toLowerCase().includes(address)):null,
  recipient_control_count:first.controls.filter(c=>addresses(reference?.to).some(a=>(c.label??'').toLowerCase().includes(a))).length,
  date_candidate_count:first.date_candidates.length,
  reference_cc_count:reference?.cc?.value?.length??0,
  reference_attachments:reference?.attachments.length??null,operations,elapsed_ms:Date.now()-started,
  parents:first.parents,controls:first.controls.map(c=>({tag:c.tag,class:c.class,attrs:c.attrs,category:/details|more|recipient|to:|from:|read|unread/i.exec((c.label??'')+' '+(c.title??'')+' '+c.text)?.[0]?.toLowerCase()??'other'})),address_node_count:first.address_nodes.length};
 await fs.writeFile(path.join(output,provider+'-result.json'),JSON.stringify(result,null,2),{mode:0o600});
 return result;
});
const entry={provider,operation:'read',scriptID:provider+'.dom_qualification',revision:1,loginURL,origins,timeoutMS:180000,handler,sourceFiles:['data/qualifications/email-dom-20260907/capture.mjs'],validate:value=>{if(value.operation!=='read')throw new Error('qualification_input_invalid')}};
const factory=new PlaywrightCLIClientFactory({registry:new ProviderScriptRegistry([entry]),runtimeRoot:path.join(output,'cli-runtime'),executablePath:path.join(os.homedir(),'.local/share/sparkclaw/browser-controller/bin/browser-bridge-launcher'),userDataDir:path.join(os.homedir(),'.local/share/sparkclaw/browser/default/user-data'),diagnostic:event=>console.error(JSON.stringify(event))});
try{
 await factory.prepare();
 const result=await factory.runScript({token:payload.token,sessionID:'session_'+crypto.randomBytes(16).toString('hex'),provider,operation:'read',scriptID:entry.scriptID,revision:1,input:{schema_version:1,operation:'read',provider,account:'default',owner_scope:owner,invocation_id:'dom-qualification-'+crypto.randomUUID()}});
 if(result.state!=='completed'){console.error(JSON.stringify({state:result.state,code:result.result?.code}));process.exitCode=1}
 else console.log(JSON.stringify(result.result));
}catch(error){console.error(JSON.stringify({code:error.code??'qualification_failed',reason:error.diagnosticReason??'unclassified'}));process.exitCode=1}
finally{payload.token=''}
