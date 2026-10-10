import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import http from 'node:http';
import {spawn} from 'node:child_process';
import {pathToFileURL} from 'node:url';
import {chromium} from 'playwright';
import {PlaywrightMCPClientFactory} from '../src/mcp-client.mjs';
import {diagnoseEmptyOutlookMenu} from './fixtures/outlook-menu-diagnostic.mjs';
const enabled=process.env.SPARKCLAW_MAIL_BROWSER_TEST==='1';
const runtime=process.env.APP_CLI_RECOVERY_RUNTIME_ROOT?pathToFileURL(path.resolve(process.env.APP_CLI_RECOVERY_RUNTIME_ROOT)+path.sep):new URL('../',import.meta.resolve('@infinimesh/app-cli-runtime/release'));
const modules={...await import(new URL('applications/mail/lib/managed-send-dom.mjs',runtime)),...await import(new URL('applications/mail/lib/workspace-attachments.mjs',runtime)),verifySendAccount:async()=>{}};
for(const mode of ['normal','deeplink','old','hidden_old','nonempty','duplicate','overlay','hover_replacement'])test(`fixed diagnostic real wrapper: ${mode}`,{skip:!enabled,timeout:30000},async t=>{
 const root=await fs.mkdtemp(path.join(os.tmpdir(),'menu-diagnostic-'));
 const lease=path.join(root,'lease.json');await fs.writeFile(lease,JSON.stringify({epoch:1,generation:1,activities:[{id:'fixture',kind:'read',expires_ms:Date.now()+120000}]}),{mode:0o600});
 const html=`<!doctype html><style>button{min-width:80px;min-height:30px} [contenteditable]{min-width:100px;min-height:30px} #menu{position:absolute;top:160px;left:0;}</style><button id=new>New mail</button><div data-automation-type="RibbonBottomBarContainer"><button id=ribbon data-automation-type="RibbonFlyoutAnchor" aria-haspopup=true aria-expanded=false aria-label="Attach files">Attach</button></div><input id=file type=file data-testid=local-computer-filein multiple hidden><script>
 const mode=${JSON.stringify(mode)};window.sendClicks=0;window.uploads=0;window.newClicks=0;
 function compose(){const root=document.createElement('div');root.innerHTML='<input aria-label="To"><input aria-label="Subject"><div contenteditable="true" aria-label="Message body"></div><button id="send">Send</button>';root.querySelector('#send').onclick=()=>sendClicks++;document.body.append(root);if(mode==='nonempty')root.querySelector('[contenteditable]').textContent='old draft';return root;}
 document.getElementById('new').onclick=()=>{newClicks++;compose();if(mode==='duplicate')compose()};if(mode==='hidden_old')compose().hidden=true;if(mode==='old'||location.pathname==='/mail/0/deeplink/compose')compose();if(mode==='deeplink')document.getElementById('new').textContent='Unavailable';
 const ribbon=document.getElementById('ribbon');ribbon.onclick=()=>{ribbon.setAttribute('aria-expanded','true');const menu=document.createElement('div');menu.id='menu';menu.setAttribute('role','menu');const action=document.createElement('button');action.setAttribute('role','menuitem');action.textContent='Browse this computer';action.onclick=()=>document.getElementById('file').click();menu.append(action);document.body.append(menu);if(mode==='overlay'){const overlay=document.createElement('div');overlay.style='position:absolute;inset:0;z-index:999';document.body.append(overlay)}if(mode==='hover_replacement')action.onpointerover=()=>{const clone=action.cloneNode(true);clone.removeAttribute('data-sc-mail-local-file-action');action.replaceWith(clone)}};
 document.getElementById('file').onchange=()=>uploads++;
 </script>`;
 const server=http.createServer((req,res)=>{res.setHeader('content-type','text/html');res.end(html)});await new Promise(r=>server.listen(0,'127.0.0.1',r));
 t.after(async()=>{await new Promise(r=>server.close(r));await fs.rm(root,{recursive:true,force:true})});
 const browser=await chromium.launchServer({headless:true});const executablePath=browser.process().spawnfile;await browser.close();
 const factory=new PlaywrightMCPClientFactory({executablePath,userDataDir:path.join(root,'profile'),outputRoot:path.join(root,'mcp-output'),connectTimeoutMS:15000,extraEnv:{APP_CLI_LEASE_FILE:lease,APP_CLI_AWAITED_CODE:'1'},spawn:(command,args,options)=>{const env={...options.env};delete env.PLAYWRIGHT_MCP_EXTENSION_TOKEN;return spawn(command,[...args.filter(a=>a!=='--extension'),'--headless'],{...options,env})}});
 const client=await factory.open({token:'synthetic',sessionID:'menu-diagnostic'});t.after(()=>client.close());await client.createTaskPage();await client.execute('page.navigate',{url:`http://127.0.0.1:${server.address().port}`});
 const request=async(name,args)=>{const r=await client.rpc.request('tools/call',{name,arguments:args},15000);assert.notEqual(r.isError,true,JSON.stringify(r));const text=r.content.filter(i=>i.type==='text').map(i=>i.text).join('\n');const m=text.match(/### Result\n([\s\S]*?)(?:\n### |$)/u);return m?JSON.parse(m[1].trim()):null;};
 if(mode==='deeplink'){await request('browser_run_code_unsafe',{code:`async page=>{await page.route('https://outlook.live.com/**',route=>route.fulfill({contentType:'text/html',body:${JSON.stringify(html)}}));await page.goto('https://outlook.live.com/mail/0/sentitems');return true}`});}
 const tab={inspect:async expression=>{assert.ok(Buffer.byteLength(expression)+1024<=32<<10,'production Host inspect bound');return {result:await request('browser_evaluate',{function:expression})}},click:selector=>request('browser_run_code_unsafe',{code:`/* app-cli:awaited-code:v1 */\nasync page=>{await page.locator(${JSON.stringify(selector)}).click();return true}`}),runReadCode:code=>request('browser_run_code_unsafe',{code})};
 if(['old','hidden_old','nonempty','duplicate'].includes(mode)){
  await assert.rejects(diagnoseEmptyOutlookMenu(tab,modules,'fixture@example.invalid'),['old','hidden_old'].includes(mode)?/email_existing_draft/:/new_composer_not_proven_empty/);
  assert.deepEqual(await request('browser_evaluate',{function:'()=>({sendClicks,uploads})'}),{sendClicks:0,uploads:0});return;
 }
 const r=await diagnoseEmptyOutlookMenu(tab,modules,'fixture@example.invalid');
 assert.equal(r.no_upload,true);assert.equal(r.no_send,true);
 if(['normal','deeplink'].includes(mode)){assert.equal(r.chooser_events,1);assert.equal(r.action_error,null);assert.equal(r.after.native_action_seen,true);assert.equal(r.after.input_activated,true);}
 if(mode==='overlay'){assert.equal(r.action_error,'pointer_interception');assert.equal(r.before.hit_target_owned,false);assert.equal(r.chooser_events,0);}
 if(mode==='hover_replacement'){assert.equal(r.changes.action_detached,true);assert.equal(r.changes.action_replaced,true);assert.equal(r.after.marker_count,0);assert.equal(r.chooser_events,0);}
});
