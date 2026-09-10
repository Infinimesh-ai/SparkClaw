import fs from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import crypto from 'node:crypto';
import {spawn} from 'node:child_process';
import {PlaywrightCLIClientFactory} from '../../../tools/browser-controller/src/cli-client.mjs';
import {ProviderScriptRegistry} from '../../../tools/browser-controller/src/provider-scripts.mjs';
import {READ_PROVIDERS,discoverEmail,readEmail} from '../../../scripts/email/read.mjs';
import {GMAIL_SEND_SELECTORS} from '../../../scripts/email/gmail-send.mjs';
import {QQMAIL_SELECTORS} from '../../../scripts/email/qqmail-send.mjs';
import {OUTLOOK_RECIPIENT_STATE_EXPRESSION,OUTLOOK_SEND_SELECTOR} from '../../../scripts/email/outlook-send.mjs';
import {prepareOutlookList,outlookListEvidence} from '../../../scripts/email/lib/outlook-list.mjs';

const provider=process.env.SPARKCLAW_INTAKE_PROVIDER??'qq_mail',mode=process.env.SPARKCLAW_INTAKE_MODE??'discover';
const base=path.dirname(new URL(import.meta.url).pathname),workspace=path.join(base,'workspace');
await fs.chmod(base,0o700);await fs.mkdir(workspace,{recursive:true,mode:0o700});
const chunks=[];for await(const chunk of process.stdin)chunks.push(chunk);
const payload=JSON.parse(Buffer.concat(chunks).toString());for(const chunk of chunks)chunk.fill(0);
const save=async(name,value)=>fs.writeFile(path.join(base,name),JSON.stringify(value,null,2),{mode:0o600});
const input={schema_version:1,operation:'discover',provider,account:'default',owner_scope:crypto.createHash('sha256').update('owner').digest('hex'),invocation_id:`intake-${provider}-${mode}-1`};
const config=READ_PROVIDERS[provider];if(!config)throw new Error('invalid provider');
const handler=async(_,runtime)=>{
 if(mode==='outlook_evidence')return runtime.withReadTab(async tab=>{
  await tab.runReadCode(`async page=>{await page.addInitScript(()=>{
   if(window.top!==window||!location.hostname.startsWith('outlook.'))return;
   globalThis.__intakeCounts=[];const visit=v=>{if(!v||typeof v!=='object')return;
    if(Array.isArray(v.Conversations))for(const c of v.Conversations)globalThis.__intakeCounts.push({id:c.ConversationId?.Id,local:c.MessageCount,global:c.GlobalMessageCount,unread:c.UnreadCount,global_unread:c.GlobalUnreadCount,items:c.ItemIds?.length,global_items:c.GlobalItemIds?.length});
    for(const x of Object.values(v))if(x&&typeof x==='object')visit(x);
   };const observe=(url,text)=>{try{const u=new URL(url,location.href);if(u.origin===location.origin&&/startupdata\\.ashx|service\\.svc/u.test(u.pathname)&&text.length<2000000)visit(JSON.parse(text));}catch{}};
   const open=XMLHttpRequest.prototype.open;XMLHttpRequest.prototype.open=function(method,url,...args){this.addEventListener('load',()=>{if(!this.responseType||this.responseType==='text')observe(url,this.responseText);},{once:true});return open.call(this,method,url,...args);};
   const fetch=globalThis.fetch;globalThis.fetch=async function(...args){const r=await fetch.apply(this,args);void r.clone().text().then(t=>observe(r.url,t)).catch(()=>{});return r;};
  });return true;}`);
  if(await tab.runReadCode(`async page=>page.evaluate(()=>!!document.querySelector('button[aria-label="Unread"]'))`))await tab.click('button:has-text("Clear filter")');
  const key=await prepareOutlookList(tab);
  const rows=await tab.runReadCode(`async page=>page.evaluate(async()=>{const end=Date.now()+10000;while(Date.now()<end){const rows=[...document.querySelectorAll('[role="option"][data-convid]')].filter(n=>n.getBoundingClientRect().width>0&&n.textContent.includes('SparkClaw intake attachment verification 20260908-A'));if(rows.length)return rows.map(n=>({provider_selection_id:n.getAttribute('data-convid'),unread:/\\bUnread\\b/u.test(n.getAttribute('aria-label')??''),label:n.getAttribute('aria-label')}));await new Promise(resolve=>setTimeout(resolve,100));}return [];})`);
  const evidence=await outlookListEvidence(tab,key,{rows});await save('outlook-self-evidence.json',evidence);
  await save('outlook-self-counts.json',await tab.runReadCode(`async page=>page.evaluate(()=>globalThis.__intakeCounts.filter(c=>${JSON.stringify(rows.map(r=>r.provider_selection_id))}.includes(c.id)))`));
  if(rows.length===1&&!rows[0].unread&&process.env.SPARKCLAW_INTAKE_MARK_UNREAD==='1') {
   await tab.runReadCode(`async page=>{await page.locator(${JSON.stringify('[role="option"][data-convid="'+rows[0].provider_selection_id+'"]')}).click({button:'right'});return true;}`);
   const menu=await tab.runReadCode(`async page=>page.evaluate(()=>[...document.querySelectorAll('[role="menuitem"]')].map(n=>({text:n.textContent,aria:n.getAttribute('aria-label')})))`);await save('outlook-unread-menu.json',menu);
   await tab.runReadCode(`async page=>{await page.getByRole('menuitem',{name:'Mark as unread',exact:true}).click();return true;}`);
  }
  return {provider,stage:'self_list_evidence',rows:rows.length,singletons:evidence.rows.filter(r=>r.single_message_proven).length,unread:rows.map(r=>r.unread)};
 });
 if(mode==='discover'||mode==='discover_thread'||mode==='gmail_evidence') {
  if(mode==='gmail_evidence')await runtime.withReadTab(tab=>tab.runReadCode(`async page=>page.evaluate(()=>{const old=XMLHttpRequest.prototype.open;globalThis.__intakeEvidence=[];XMLHttpRequest.prototype.open=function(method,url,...args){if(new URL(url,location.href).pathname.endsWith('/i/bv'))this.addEventListener('load',()=>{if(!this.responseType||this.responseType==='text')globalThis.__intakeEvidence.push(this.responseText.slice(0,500000));},{once:true});return old.call(this,method,url,...args);};return true;})`));
  let result;
  try{result=await discoverEmail(input,runtime,provider);}catch(e){await runtime.withReadTab(async tab=>save(provider+'-discovery-debug.json',await tab.runReadCode(`async page=>page.evaluate(()=>[...document.querySelectorAll(${JSON.stringify(config.rows)})].map(n=>({attrs:Object.fromEntries([...n.attributes].map(a=>[a.name,a.value])),text:n.textContent?.slice(0,500)})))`)));throw e;}
  await save(provider+(mode==='discover_thread'?'-thread':'')+'-discovery.json',result);
  if(mode==='gmail_evidence')await runtime.withReadTab(async tab=>save(provider+'-evidence.json',await tab.runReadCode(`async page=>page.evaluate(()=>({network:globalThis.__intakeEvidence,rows:[...document.querySelectorAll('tr.zA')].filter(n=>n.textContent.includes('SparkClaw intake attachment verification 20260908-A')).map(n=>({html:n.outerHTML}))}))`)));
  const selfID=await runtime.withReadTab(tab=>tab.runReadCode(`async page=>page.evaluate(()=>{
   const selector=${JSON.stringify(config.rows)},subject='SparkClaw intake attachment verification 20260908-A';
   const rows=[...document.querySelectorAll(selector)].filter(n=>n.getBoundingClientRect().width>0&&n.textContent.includes(subject));
   if(rows.length!==1)return null;return rows[0].getAttribute('data-mailid')??rows[0].querySelector('[data-legacy-last-message-id]')?.getAttribute('data-legacy-last-message-id')??rows[0].getAttribute('data-convid');
  })`));
  const targets=result.candidates.filter(c=>c.provider_message_id===selfID||c.provider_selection_id===selfID);
  if(targets.length===1)await save(provider+'-self-target.json',targets[0]);
  return {provider,status:result.status,count:result.candidates.length,coverage:result.coverage};
 }
 if(mode==='capture') {
  const target=JSON.parse(await fs.readFile(path.join(base,provider+'-self-target.json'),'utf8'));if(!target)throw new Error('no target');
  const result=await readEmail({...input,operation:'capture',target},runtime,provider);await save(provider+'-receipt.json',result);
  return {provider,status:result.status,attachments:result.capture?.attachments_count,read:result.capture?.read_state};
 }
 return runtime.withReadTab(async tab=>{
  const account=await tab.runReadCode(`async page=>page.evaluate(()=>{
   const provider=${JSON.stringify(provider)};
   const text=provider==='gmail'?document.querySelector('[aria-label^="Google Account:"]')?.getAttribute('aria-label'):provider==='qq_mail'?document.querySelector('.frame-header .profile-user-info .user-email')?.textContent:[...document.querySelectorAll('[role="treeitem"][aria-level="1"][data-folder-name]')].map(n=>n.title).join(' ');
   return text?.match(/[A-Za-z0-9.!#$%&'*+/=?^_\x60{|}~-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}/u)?.[0]??'';
  })`);
  if(!account)throw new Error('no verified self address');
  await save(provider+'-self.json',{account});
  const compose=provider==='gmail'?GMAIL_SEND_SELECTORS.compose:provider==='qq_mail'?QQMAIL_SELECTORS.composeButton:'button[aria-label="New mail"]';
  await tab.click(compose);
  if(mode==='outlook_attach') {
   await tab.click('button[aria-label="Attach file"]');
   const ui=await tab.runReadCode(`async page=>page.evaluate(()=>({inputs:[...document.querySelectorAll('input[type="file"]')].map(n=>({accept:n.accept,id:n.id,html:n.outerHTML})),menus:[...document.querySelectorAll('[role="menuitem"]')].map(n=>({text:n.textContent,aria:n.getAttribute('aria-label')}))}))`);
   await save(provider+'-attach-ui.json',ui);return {provider,stage:'attachment_menu_inspected'};
  }
  if(mode==='send'&&provider==='outlook') {
   const subject='SparkClaw intake attachment verification 20260908-A';
   const body='Synthetic self-mail. Verify UTF-8 body: 邮件底座验证。\nAttachment must contain the matching test marker.';
   const filename='sparkclaw-验证.txt',bytes=Buffer.from('SparkClaw attachment fixture 20260908-A\n中文附件内容\n');
   await tab.fill('[contenteditable="true"][aria-label="To"]',account);
   if(!await tab.runReadCode(`async page=>page.evaluate(()=>document.querySelector('[contenteditable="true"][aria-label="To"]').textContent===${JSON.stringify(account)})`))throw new Error('recipient mismatch');
   await tab.press('Enter');await tab.fill('input[aria-label="Subject"]',subject);await tab.fill('[contenteditable="true"][aria-label="Message body"]',body);
   await fs.writeFile(path.join(base,filename),bytes,{mode:0o600});
   const upload=await tab.runReadCode(`async page=>{try{await page.locator('input[type="file"][data-testid="local-computer-filein"]:not([accept])').first().setInputFiles(${JSON.stringify(path.join(base,filename))});return {ok:true};}catch(e){return {ok:false,error:String(e.message).slice(0,1000)};}}`);
   await save(provider+'-upload.json',upload);if(!upload.ok)throw new Error('upload failed');
   const verified=await tab.runReadCode(`async page=>page.evaluate(async()=>{
    const end=Date.now()+20000;while(Date.now()<end){
     if(document.body.innerText.includes(${JSON.stringify(filename)})&&!document.querySelector('[role="progressbar"]'))return {
      recipient:(${OUTLOOK_RECIPIENT_STATE_EXPRESSION})().valid,
      subject:document.querySelector('input[aria-label="Subject"]').value===${JSON.stringify(subject)},
      body:document.querySelector('[contenteditable="true"][aria-label="Message body"]').innerText.trim()===${JSON.stringify(body)},attachment:true};
     await new Promise(resolve=>setTimeout(resolve,100));
    }return null;
   })`);
   await save(provider+'-presend.json',verified);if(!verified||Object.values(verified).some(v=>v!==true)){
    await save(provider+'-recipient-debug.json',await tab.runReadCode(`async page=>page.evaluate(()=>({field:document.querySelector('[aria-label="To"]')?.outerHTML,chips:[...document.querySelectorAll('[draggable="true"][aria-label]')].map(n=>({html:n.outerHTML})),inputs:[...document.querySelectorAll('[aria-label]')].map(n=>n.getAttribute('aria-label')).filter(v=>/recipient|^To|收件/iu.test(v))}))`));
    throw new Error('presend mismatch');
   }
   await fs.writeFile(path.join(base,provider+'-send-intent.json'),JSON.stringify({subject,body,filename,sha256:crypto.createHash('sha256').update(bytes).digest('hex'),verified}),{mode:0o600,flag:'wx'});
   await tab.click(OUTLOOK_SEND_SELECTOR);
   const closed=await tab.runReadCode(`async page=>page.evaluate(async()=>{const end=Date.now()+10000;while(Date.now()<end){if(!document.querySelector('[contenteditable="true"][aria-label="Message body"]'))return true;await new Promise(resolve=>setTimeout(resolve,100));}return false;})`);
   await save(provider+'-send-result.json',{compose_closed:closed,delivery:'unconfirmed'});return {provider,compose_closed:closed,self_address_verified:true,attachment:true};
  }
  if((mode==='send'||mode==='send_thread')&&provider==='gmail') {
   const subject='SparkClaw intake attachment verification 20260908-A';
   const body=mode==='send_thread'?'Synthetic second message to verify conversation isolation.':'Synthetic self-mail. Verify UTF-8 body: 邮件底座验证。\nAttachment must contain the matching test marker.';
   const filename='sparkclaw-验证.txt',bytes=Buffer.from('SparkClaw attachment fixture 20260908-A\n中文附件内容\n');
   await tab.fill(GMAIL_SEND_SELECTORS.recipientInput,account);await tab.press('Enter');
   await tab.fill(GMAIL_SEND_SELECTORS.subject,subject);await tab.fill(GMAIL_SEND_SELECTORS.body,body);
   await fs.writeFile(path.join(base,filename),bytes,{mode:0o600});
   await tab.runReadCode(`async page=>{await page.locator('input[type="file"][name="Filedata"]').setInputFiles(${JSON.stringify(path.join(base,filename))});return true;}`);
   const verified=await tab.runReadCode(`async page=>page.evaluate(async()=>{
    const end=Date.now()+20000;while(Date.now()<end){
     const root=document.querySelector('[role="dialog"]');
     const chips=[...root.querySelectorAll('[role="option"][data-hovercard-id]')];
     if(root.innerText.includes(${JSON.stringify(filename)})&&!root.querySelector('[role="progressbar"]'))return {
      recipient:chips.length===1&&chips[0].getAttribute('data-hovercard-id')===${JSON.stringify(account)},
      subject:root.querySelector('input[name="subjectbox"]').value===${JSON.stringify(subject)},
      body:root.querySelector('[role="textbox"][contenteditable="true"]').innerText.trim()===${JSON.stringify(body)},attachment:true};
     await new Promise(resolve=>setTimeout(resolve,100));
    }return null;
   })`);
   await save(provider+'-presend.json',verified);if(!verified||Object.values(verified).some(v=>v!==true))throw new Error('presend mismatch');
   await fs.writeFile(path.join(base,provider+(mode==='send_thread'?'-thread':'')+'-send-intent.json'),JSON.stringify({subject,body,filename,sha256:crypto.createHash('sha256').update(bytes).digest('hex'),verified}),{mode:0o600,flag:'wx'});
   await tab.click(GMAIL_SEND_SELECTORS.send);
   const sent=await tab.runReadCode(`async page=>page.evaluate(async()=>{const end=Date.now()+10000;while(Date.now()<end){if([...document.querySelectorAll('.bAq')].some(n=>/Message sent/u.test(n.textContent)))return true;await new Promise(resolve=>setTimeout(resolve,100));}return false;})`);
   await save(provider+(mode==='send_thread'?'-thread':'')+'-send-result.json',{sent});return {provider,sent,self_address_verified:true,attachment:true};
  }
  if(mode==='send') {
   if(provider!=='qq_mail')throw new Error('sender not qualified');
   const subject='SparkClaw intake attachment verification 20260908-A';
   const body='Synthetic self-mail. Verify UTF-8 body: 邮件底座验证。\nAttachment must contain the matching test marker.';
   const filename='sparkclaw-验证.txt',bytes=Buffer.from('SparkClaw attachment fixture 20260908-A\n中文附件内容\n');
   await tab.fill(QQMAIL_SELECTORS.recipient,account);
   const recipientVerified=await tab.runReadCode(`async page=>page.evaluate(()=>document.querySelector('input[aria-label="To"]').value===${JSON.stringify(account)})`);
   if(!recipientVerified)throw new Error('recipient mismatch');
   await tab.click(QQMAIL_SELECTORS.subject);
   await tab.fill(QQMAIL_SELECTORS.subject,subject);
   await tab.fill(QQMAIL_SELECTORS.body,body);
   await fs.writeFile(path.join(base,filename),bytes,{mode:0o600});
   const upload=await tab.runReadCode(`async page=>{try{await page.locator('input[type="file"]').setInputFiles(${JSON.stringify(path.join(base,filename))});return {ok:true};}catch(e){return {ok:false,error:String(e.message).slice(0,1000)};}}`);
   await save(provider+'-upload.json',upload);if(!upload.ok)throw new Error('upload failed');
   const verified=await tab.runReadCode(`async page=>page.evaluate(async()=>{
    const end=Date.now()+20000;while(Date.now()<end){
     const root=document.querySelector('.mail-compose-page');
     const chips=[...root.querySelectorAll('.receiver-editor .xmail-cmp-account')];
     const text=root.innerText;
     if(text.replace(/\\s+/gu,'').includes(${JSON.stringify(filename)})&&!/Uploading|上传中/u.test(text))return {
      recipient:chips.length===1&&!chips[0].classList.contains('cmp-account-invalid')&&!(root.querySelector('input[aria-label="To"]')?.value),
      subject:root.querySelector('input[aria-label="Subject"]').value===${JSON.stringify(subject)},
      body:root.querySelector('[contenteditable="true"]').innerText.replace(/\\r\\n?/gu,'\\n').trim()===${JSON.stringify(body)},attachment:true};
     await new Promise(resolve=>setTimeout(resolve,100));
    }return null;
   })`);
   await save(provider+'-presend.json',verified);
   if(!verified||Object.values(verified).some(v=>v!==true)) {
    await save(provider+'-draft-debug.json',await tab.runReadCode(`async page=>page.evaluate(()=>({text:document.querySelector('.mail-compose-page')?.innerText,html:document.querySelector('.mail-compose-page')?.outerHTML.slice(0,35000)}))`));
    throw new Error('pre-send verification failed');
   }
   const intent=path.join(base,provider+'-send-intent.json');
   await fs.writeFile(intent,JSON.stringify({subject,body,filename,sha256:crypto.createHash('sha256').update(bytes).digest('hex'),verified}),{mode:0o600,flag:'wx'});
   await tab.click(QQMAIL_SELECTORS.sendButton);
   const sent=await tab.runReadCode(`async page=>page.evaluate(async()=>{
    const end=Date.now()+15000;while(Date.now()<end){
     if(/^#\\/list\\/3/u.test(location.hash)&&[...document.querySelectorAll('.mail-subject')].some(n=>n.textContent.trim()===${JSON.stringify(subject)}))return true;
     await new Promise(resolve=>setTimeout(resolve,100));
    }return false;
   })`);
   await save(provider+'-send-result.json',{sent});
   return {provider,sent,self_address_verified:true,attachment:true};
  }
  const ui=await tab.runReadCode(`async page=>page.evaluate(()=>({inputs:[...document.querySelectorAll('input')].map(n=>({type:n.type,name:n.name,aria:n.getAttribute('aria-label'),multiple:n.multiple})),editors:[...document.querySelectorAll('[contenteditable="true"]')].map(n=>({role:n.role,aria:n.getAttribute('aria-label'),class:n.className})),buttons:[...document.querySelectorAll('button,[role="button"]')].filter(n=>n.getBoundingClientRect().width>0).map(n=>({text:n.textContent?.trim(),aria:n.getAttribute('aria-label'),title:n.title})).slice(-80)}))`);
  await save(provider+'-compose.json',ui);
  return {provider,stage:'compose_inspected',inputs:ui.inputs.length};
 });
};
const operation=mode==='discover'?'discover':mode==='capture'?'capture':'read';
const entry={provider,operation,scriptID:provider+'.intake_qualification',revision:1,loginURL:config.url,origins:config.origins,downloadOrigins:[...config.origins,...(provider==='gmail'?['https://mail-attachment.googleusercontent.com']:[])],timeoutMS:180000,handler,sourceFiles:['data/qualifications/email-intake-20260908/run.mjs'],validate:()=>{}};
const diagnosticSpawn=(exe,args,options)=>{const child=spawn(exe,args,options);let output='';child.stdout?.on('data',b=>{if(output.length<4096)output+=b.toString();});child.once('close',code=>{if(code!==0&&output.trim())void save(provider+'-cli-failure.json',{output:output.replaceAll(payload.token,'[redacted]')});output='';});return child;};
const factory=new PlaywrightCLIClientFactory({spawn:diagnosticSpawn,registry:new ProviderScriptRegistry([entry]),emailWorkspaceRoot:workspace,runtimeRoot:path.join(base,'cli-runtime'),executablePath:path.join(os.homedir(),'.local/share/sparkclaw/browser-controller/bin/browser-bridge-launcher'),userDataDir:path.join(os.homedir(),'.local/share/sparkclaw/browser/default/user-data'),diagnostic:value=>console.error(JSON.stringify(value))});
try{
 await factory.prepare();const result=await factory.runScript({token:payload.token,sessionID:'session_'+crypto.randomBytes(16).toString('hex'),provider,operation,scriptID:entry.scriptID,revision:1,input});
 if(result.state!=='completed')throw Object.assign(new Error('not completed'),{code:result.result?.code??'not_completed'});console.log(JSON.stringify(result.result));
}catch(e){console.error(JSON.stringify({code:e.code??'qualification_failed',reason:e.diagnosticReason??'unclassified'}));process.exitCode=1;}finally{payload.token='';}
