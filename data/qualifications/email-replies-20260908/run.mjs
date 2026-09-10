import fs from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import crypto from 'node:crypto';
import {spawn} from 'node:child_process';
import {PlaywrightCLIClientFactory} from '../../../tools/browser-controller/src/cli-client.mjs';
import {ProviderScriptRegistry} from '../../../tools/browser-controller/src/provider-scripts.mjs';
import {READ_PROVIDERS,discoverEmail,readEmail,providerDOM} from '../../../scripts/email/read.mjs';
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
const handler=async(_,runtime)=>runtime.withReadTab(async tab=>{
 const prior=JSON.parse(await fs.readFile(path.join(base,'../email-intake-20260908/'+provider+'-self.json'),'utf8'));
 const current=await tab.runReadCode(`async page=>page.evaluate(()=>(${providerDOM.toString()})(${JSON.stringify(provider)},'account'))`);
 if(!current.account_address)return {provider,stage:'account_unavailable',account_match:false};
 if(provider!=='outlook'&&current.account_address.toLowerCase()!==prior.account.toLowerCase())return {provider,stage:'account_changed',account_match:false};
 if(provider==='gmail') {
  await tab.fill('input[name="q"]','in:inbox subject:"SparkClaw intake attachment verification 20260908-A"');await tab.press('Enter');
  const target=JSON.parse(await fs.readFile(path.join(base,'../email-intake-20260908/gmail-self-target.json'),'utf8'));
  const selector=`tr.zA:has([data-legacy-last-message-id="${target.provider_message_id}"]):visible`;
  await tab.runReadCode(`async page=>{await page.locator(${JSON.stringify(selector)}).waitFor({state:'visible',timeout:10000});return true;}`);
  await tab.click(selector);
  await save('gmail-detail.json',await tab.runReadCode(`async page=>page.evaluate(()=>({messages:[...document.querySelectorAll('.adn[data-legacy-message-id]')].map(n=>({id:n.getAttribute('data-legacy-message-id'),sender:n.querySelector('[email]')?.getAttribute('email')})),controls:[...document.querySelectorAll('button,[role="button"]')].filter(n=>n.getBoundingClientRect().width).map(n=>({text:n.textContent,aria:n.getAttribute('aria-label'),tooltip:n.getAttribute('data-tooltip')}))}))`));
  if(mode==='reply_prepare') {
   const detail=JSON.parse(await fs.readFile(path.join(base,'gmail-detail.json'),'utf8'));
   const source=detail.messages.filter(m=>m.id===target.provider_message_id);
   if(source.length!==1||source[0].sender.toLowerCase()!==current.account_address.toLowerCase())throw new Error('not a verified self mail');
   if(!await tab.runReadCode(`async page=>page.locator('[contenteditable="true"][aria-label="Message Body"]:visible').count()`))await tab.click('button[aria-label="Reply"]');
   await save('gmail-reply-ui.json',await tab.runReadCode(`async page=>page.evaluate(()=>({editors:[...document.querySelectorAll('[contenteditable="true"]')].map(n=>({html:n.outerHTML,parent:n.parentElement.parentElement.parentElement.outerHTML})),buttons:[...document.querySelectorAll('button,[role="button"]')].filter(n=>n.getBoundingClientRect().width).map(n=>({text:n.textContent,aria:n.getAttribute('aria-label'),tooltip:n.getAttribute('data-tooltip')}))}))`));
   await save('gmail-reply-ancestors.json',await tab.runReadCode(`async page=>page.evaluate(()=>{let n=document.querySelector('[contenteditable="true"][aria-label="Message Body"]');const result=[];for(let i=0;n&&i<16;i++,n=n.parentElement)result.push({tag:n.tagName,attrs:Object.fromEntries([...n.attributes].map(a=>[a.name,a.value])),recipients:[...n.querySelectorAll('[email],[data-hovercard-id],input[name="to"],input[name="cc"],input[name="bcc"]')].map(x=>({tag:x.tagName,attrs:Object.fromEntries([...x.attributes].map(a=>[a.name,a.value]))}))});return result;})`));
  }
  return {provider,stage:'fixture_inspected'};
 }
 if(provider!=='outlook')return {provider,stage:'account_verified',account_match:true};
 await tab.runReadCode(`async page=>{
 page.on('response',async response=>{try{
 if(new URL(response.url()).origin!==new URL(page.url()).origin)return;
 const text=await response.text();if(text.length>10000000)return;
 const value=JSON.parse(text),records=[];let visited=0;
 const visit=(v,depth=0)=>{if(!v||typeof v!=='object'||depth>24||++visited>20000)return;
 if(v.ItemId||v.itemId||v.InternetMessageId)records.push(JSON.parse(JSON.stringify(v,(k,x)=>/Body|Preview|Recipients|Sender|From|EmailAddress|Subject|DisplayName|Name|MimeContent/i.test(k)?undefined:x)));
 for(const x of Object.values(v))if(x&&typeof x==='object')visit(x,depth+1);};visit(value);
 await page.evaluate(({records,url,keys})=>{globalThis.__replyNetwork??=[];globalThis.__replyNetwork.push({records,url,keys});},{records,url:new URL(response.url()).pathname,keys:Object.keys(value)});
 }catch{}});
 await page.addInitScript(()=>{
 if(window.top!==window||!location.hostname.startsWith('outlook.'))return;
 globalThis.__replyEvidence=[];
 const visit=(v,depth=0)=>{if(!v||typeof v!=='object'||depth>24)return;
 if(v.ItemId||v.itemId||v.ConversationId||v.InternetMessageId)globalThis.__replyEvidence.push(JSON.parse(JSON.stringify(v,(k,x)=>/Body|Preview|Recipients|Sender|From|EmailAddress|Subject|DisplayName|Name|MimeContent/i.test(k)?undefined:x)));
 for(const x of Object.values(v))if(x&&typeof x==='object')visit(x,depth+1);
 };
 const observe=(url,text)=>{try{const u=new URL(url,location.href);if(u.origin===location.origin&&text.length<10000000)visit(JSON.parse(text));}catch{}};
 const open=XMLHttpRequest.prototype.open;XMLHttpRequest.prototype.open=function(method,url,...args){this.addEventListener('load',()=>{if(!this.responseType||this.responseType==='text')observe(url,this.responseText);},{once:true});return open.call(this,method,url,...args);};
 const fetch=globalThis.fetch;globalThis.fetch=async function(...args){const r=await fetch.apply(this,args);if(r.url.includes(location.origin))void r.clone().text().then(t=>observe(r.url,t)).catch(()=>{});return r;};
 });await page.reload({waitUntil:'domcontentloaded',timeout:15000});return true;}`);
 const selector='[role="option"][data-convid]:visible';
 await save('outlook-before.json',await tab.runReadCode(`async page=>page.evaluate(()=>({url:location.href,rows:[...document.querySelectorAll('[role="option"][data-convid]')].map(n=>({id:n.getAttribute('data-convid'),fixture:n.textContent.includes('SparkClaw intake attachment verification 20260908-A'),visible:!!n.getBoundingClientRect().width})),buttons:[...document.querySelectorAll('button')].filter(n=>n.getBoundingClientRect().width).map(n=>n.getAttribute('aria-label'))}))`));
 await tab.runReadCode(`async page=>{await page.locator(${JSON.stringify(selector)}).waitFor({state:'visible',timeout:10000});return true;}`);
 await tab.click('button[aria-label="展开对话"],button[aria-label="Expand conversation"]');
 await save('outlook-expanded.json',await tab.runReadCode(`async page=>page.evaluate(()=>({dom:document.body.outerHTML,network:globalThis.__replyNetwork,evidence:globalThis.__replyEvidence}))`));
 if(mode==='item') {
  const target=await tab.runReadCode(`async page=>page.evaluate(()=>{const conversations=globalThis.__replyEvidence.filter(x=>x.ItemIds&&x.GlobalItemIds);if(conversations.length!==1)return null;const c=conversations[0];const ids=new Set(c.ItemIds.map(x=>x.Id)),drafts=new Set((c.DraftItemIds??[]).map(x=>x.Id));const rows=[...document.querySelectorAll('[role="listitem"]')].map(n=>({n,id:n.parentElement.parentElement.id})).filter(x=>ids.has(x.id)&&!drafts.has(x.id));if(rows.length!==2)return null;return {provider_message_id:rows[1].id,provider_selection_id:c.ConversationId.Id};})`);
  if(!target)throw new Error('no per-item proof');await save('outlook-target.json',{...target,account_address:current.account_address});
  const itemSelector=`[id="${target.provider_message_id}"] [role="listitem"]`;
  await tab.runReadCode(`async page=>{await page.locator(${JSON.stringify(itemSelector)}).click({button:'right'});return true;}`);
  await save('outlook-item-menu.json',await tab.runReadCode(`async page=>page.evaluate(()=>[...document.querySelectorAll('[role="menuitem"]')].filter(n=>n.getBoundingClientRect().width).map(n=>({text:n.textContent,aria:n.getAttribute('aria-label')})))`));
  await tab.press('Escape');await tab.click(itemSelector);
  await save('outlook-item-detail.json',await tab.runReadCode(`async page=>page.evaluate(()=>({url:location.href,focused:document.querySelector('#focused')?.outerHTML,rows:[...document.querySelectorAll('[role="listitem"]')].map(n=>({id:n.parentElement.parentElement.id,attrs:Object.fromEntries([...n.attributes].map(a=>[a.name,a.value])),checkbox:n.querySelector('[role="checkbox"]')?.getAttribute('aria-checked')}))}))`));
  await tab.runReadCode(`async page=>{await page.locator('#focused button[aria-label="More items"]').click();return true;}`);
  await save('outlook-message-menu.json',await tab.runReadCode(`async page=>page.evaluate(()=>[...document.querySelectorAll('[role="menuitem"],[role="menu"] [role="button"]')].filter(n=>n.getBoundingClientRect().width).map(n=>({text:n.textContent,aria:n.getAttribute('aria-label')})))`));
  return {provider,stage:'item_inspected'};
 }
 if(mode==='expand')return {provider,stage:'list_expanded'};
 await tab.click(selector);
 await tab.runReadCode(`async page=>{await page.locator('[data-test-id="mailMessageBodyContainer"]').first().waitFor({state:'visible',timeout:10000});return true;}`);
 await save('outlook-open.json',await tab.runReadCode(`async page=>page.evaluate(()=>({url:location.href,dom:document.body.outerHTML,network:globalThis.__replyNetwork}))`));
 await save('outlook-detail.json',await tab.runReadCode(`async page=>page.evaluate(()=>({
 url:location.href,
 bodies:[...document.querySelectorAll('[id^="UniqueMessageBody"],[role="document"][aria-label="Message body"]')].map(n=>({html:n.outerHTML,parents:[n.parentElement,n.parentElement.parentElement,n.parentElement.parentElement.parentElement].map(p=>({tag:p.tagName,attrs:Object.fromEntries([...p.attributes].map(a=>[a.name,a.value]))}))})),
 buttons:[...document.querySelectorAll('button')].filter(n=>n.getBoundingClientRect().width).map(n=>({aria:n.getAttribute('aria-label'),text:n.textContent,attrs:Object.fromEntries([...n.attributes].map(a=>[a.name,a.value]))})),
 evidence:globalThis.__replyEvidence
 }))`));
 return {provider,stage:'detail inspected'};
});
const operation='read';
const entry={provider,operation,scriptID:provider+'.intake_qualification',revision:1,loginURL:config.url,origins:config.origins,downloadOrigins:[...config.origins,...(provider==='gmail'?['https://mail-attachment.googleusercontent.com']:[])],timeoutMS:180000,handler,sourceFiles:['data/qualifications/email-replies-20260908/run.mjs'],validate:()=>{}};
const diagnosticSpawn=(exe,args,options)=>{const child=spawn(exe,args,options);let output='';child.stdout?.on('data',b=>{if(output.length<4096)output+=b.toString();});child.once('close',code=>{if(code!==0&&output.trim())void save(provider+'-cli-failure.json',{output:output.replaceAll(payload.token,'[redacted]')});output='';});return child;};
const factory=new PlaywrightCLIClientFactory({spawn:diagnosticSpawn,registry:new ProviderScriptRegistry([entry]),emailWorkspaceRoot:workspace,runtimeRoot:path.join(base,'cli-runtime'),executablePath:path.join(os.homedir(),'.local/share/sparkclaw/browser-controller/bin/browser-bridge-launcher'),userDataDir:path.join(os.homedir(),'.local/share/sparkclaw/browser/default/user-data'),diagnostic:value=>console.error(JSON.stringify(value))});
try{
 await factory.prepare();const result=await factory.runScript({token:payload.token,sessionID:'session_'+crypto.randomBytes(16).toString('hex'),provider,operation,scriptID:entry.scriptID,revision:1,input});
 if(result.state!=='completed')throw Object.assign(new Error('not completed'),{code:result.result?.code??'not_completed'});console.log(JSON.stringify(result.result));
}catch(e){console.error(JSON.stringify({code:e.code??'qualification_failed',reason:e.diagnosticReason??'unclassified'}));process.exitCode=1;}finally{payload.token='';}
