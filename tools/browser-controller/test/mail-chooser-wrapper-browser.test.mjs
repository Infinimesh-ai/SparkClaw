import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import {pathToFileURL} from 'node:url';
import {spawn} from 'node:child_process';
import fs from 'node:fs/promises';
import http from 'node:http';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import {chromium} from 'playwright';
import {PlaywrightMCPClientFactory} from '../src/mcp-client.mjs';
const enabled=process.env.SPARKCLAW_MAIL_BROWSER_TEST==='1';
const marker='/* app-cli:awaited-code:v1 */\n/* app-cli:owned-file-chooser:v1 */\n';
async function fixture(t){
  const root=await fs.mkdtemp(path.join(os.tmpdir(),'chooser-wrapper-'));
  const lease=path.join(root,'lease.json');await fs.writeFile(lease,JSON.stringify({epoch:1,generation:1,activities:[{id:'fixture',kind:'read',expires_ms:Date.now()+120000}]}),{mode:0o600});
  const server=http.createServer((req,res)=>{res.setHeader('content-type','text/html');res.end('<!doctype html><button id="choose" onclick="document.getElementById(\'owned\').click()">Choose</button><input id="owned" type="file" hidden><input id="foreign" type="file" hidden>');});
  await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
  t.after(async()=>{await new Promise(resolve=>server.close(resolve));await fs.rm(root,{recursive:true,force:true});});
  const probe=await chromium.launchServer({headless:true}),executablePath=probe.process().spawnfile;await probe.close();
  const factory=new PlaywrightMCPClientFactory({executablePath,userDataDir:path.join(root,'profile'),outputRoot:path.join(root,'mcp-output'),
    connectTimeoutMS:15000,extraEnv:{APP_CLI_LEASE_FILE:lease,APP_CLI_AWAITED_CODE:'1'},
    spawn:(command,args,options)=>{const env={...options.env};delete env.PLAYWRIGHT_MCP_EXTENSION_TOKEN;return spawn(command,[...args.filter(a=>a!=='--extension'),'--headless'],{...options,env});}});
  const client=await factory.open({token:'synthetic-fixture',sessionID:'owned-file-chooser'});t.after(()=>client.close());
  await client.createTaskPage();await client.execute('page.navigate',{url:`http://127.0.0.1:${server.address().port}/`});
  const call=code=>client.rpc.request('tools/call',{name:'browser_run_code_unsafe',arguments:{code:marker+code}},15000);
  const inspect=()=>client.rpc.request('tools/call',{name:'browser_evaluate',arguments:{function:'()=>({available:true})'}},15000);
  return {call,inspect,client,root:await fs.realpath(root)};
}
const ownedTransfer=`async page=>{
 const pending=page.waitForEvent('filechooser',{timeout:3000});await page.locator('#choose').click();const chooser=await pending;
 const proof=await page.evaluate(node=>{if(node!==document.getElementById('owned'))throw new Error('foreign input');const dt=new DataTransfer();dt.items.add(new File(['synthetic'],'owned.txt'));node.files=dt.files;node.dispatchEvent(new Event('change',{bubbles:true}));return node.files.length===1;},chooser.element());
 if(!proof)throw new Error('transfer missing');page[Symbol.for('sparkclaw.app-cli.owned-file-chooser.v1')]={chooser,input:chooser.element()};return {transferred:true};
}`;
test('real pinned wrapper clears only the completed owned chooser and permits the next inspection',{skip:!enabled,timeout:30000},async t=>{
 const f=await fixture(t),result=await f.call(ownedTransfer);assert.notEqual(result.isError,true,JSON.stringify(result));
 const inspected=await f.inspect();assert.notEqual(inspected.isError,true,JSON.stringify(inspected));
});

test('real pinned wrapper retains an uncertified chooser and refuses subsequent tools',{skip:!enabled,timeout:30000},async t=>{
 const f=await fixture(t);
 const result=await f.call(`async page=>{const pending=page.waitForEvent('filechooser');await page.locator('#choose').click();await pending;return true;}`);
 assert.equal(result.isError,true);assert.match(JSON.stringify(result),/host_owned_chooser_unverified/);
 const inspected=await f.inspect();assert.equal(inspected.isError,true);assert.match(JSON.stringify(inspected),/does not handle the modal state/);
 // The existing modal blocks a new call before its callback can affect state.
 const again=await f.call(ownedTransfer);assert.equal(again.isError,true);assert.match(JSON.stringify(again),/does not handle the modal state/);
});

test('real pinned wrapper refuses multiple same-call chooser events',{skip:!enabled,timeout:30000},async t=>{
 const f=await fixture(t);
 const result=await f.call(`async page=>{
   let pending=page.waitForEvent('filechooser');await page.locator('#choose').click();const first=await pending;
   pending=page.waitForEvent('filechooser');await page.locator('#choose').click();await pending;
   page[Symbol.for('sparkclaw.app-cli.owned-file-chooser.v1')]={chooser:first,input:first.element()};return true;
 }`);
 assert.equal(result.isError,true);assert.match(JSON.stringify(result),/host_owned_chooser_unverified/);
 const inspected=await f.inspect();assert.equal(inspected.isError,true);assert.match(JSON.stringify(inspected),/does not handle the modal state/);
});

// This exercises the installed wrapper with the actual application upload code,
// not a lookalike setInputFiles/path upload or an unwrapped Chromium page.
test('actual shared-Ribbon byte upload survives the pinned MCP wrapper and final inspection',{skip:!enabled,timeout:30000},async t=>{
 const runtime=process.env.APP_CLI_RECOVERY_RUNTIME_ROOT?pathToFileURL(path.resolve(process.env.APP_CLI_RECOVERY_RUNTIME_ROOT)+path.sep):new URL('../',import.meta.resolve('@infinimesh/app-cli-runtime/release'));
 const {uploadManagedAttachments,verifyManagedAttachments}=await import(new URL('applications/mail/lib/workspace-attachments.mjs',runtime));
 const f=await fixture(t);
 const request=async(name,args)=>{
   const result=await f.client.rpc.request('tools/call',{name,arguments:args},15000);
   assert.notEqual(result.isError,true,JSON.stringify(result));
   const text=result.content.filter(item=>item.type==='text').map(item=>item.text).join('\n');
   const match=text.match(/### Result\n([\s\S]*?)(?:\n### |$)/u);assert.ok(match,text);
   return JSON.parse(match[1].trim());
 };
 await request('browser_evaluate',{function:`()=>{
   document.body.innerHTML='<div data-automation-type="RibbonBottomBarContainer"><button data-automation-type="RibbonFlyoutAnchor" aria-haspopup="true" aria-expanded="false" aria-label="Attach files">Attach</button></div><div><input id="decoy" type="file" data-testid="local-computer-filein" multiple hidden><input id="actual" type="file" data-testid="local-computer-filein" multiple hidden></div><div id="composer"><div contenteditable="true" aria-label="Message body">Synthetic body</div><button id="send">Send</button><div id="rows"></div></div>';
   const root=document.getElementById('composer'),input=document.getElementById('actual'),button=document.querySelector('[aria-haspopup]');
   window.__sparkclawManagedMail={provider:'outlook',ownershipChecked:true,root,body:root.querySelector('[contenteditable]'),send:root.querySelector('#send')};
   window.sendClicks=0;window.uploadCount=0;root.querySelector('#send').onclick=()=>sendClicks++;
   button.onclick=()=>setTimeout(()=>{button.setAttribute('aria-expanded','true');const menu=document.createElement('div');menu.setAttribute('role','menu');const action=document.createElement('button');action.setAttribute('role','menuitem');action.textContent='Browse this computer';action.onclick=()=>input.click();menu.append(action);document.body.append(menu);},100);
   input.onchange=()=>{uploadCount++;for(const file of input.files){const row=document.createElement('div');row.setAttribute('data-attachment-id','fixture');const name=document.createElement('span');name.title=file.name;name.textContent=file.name;const remove=document.createElement('button');remove.setAttribute('aria-label','Remove attachment');remove.textContent='Remove';row.append(name,remove);document.getElementById('rows').append(row);}};
   return true;
 }`});
 const bytes=Buffer.from('Synthetic wrapper bytes\n'),name='wrapper.txt',relative=`.sparkclaw-mail-send-${'d'.repeat(32)}/00/${name}`;
 await fs.mkdir(path.dirname(path.join(f.root,relative)),{recursive:true,mode:0o700});await fs.writeFile(path.join(f.root,relative),bytes,{mode:0o600});
 const manifest=[{path:relative,name,size_bytes:bytes.length,sha256:'sha256:'+crypto.createHash('sha256').update(bytes).digest('hex')}];
 const tab={attachmentSecretSlots:8,setAttachmentSecrets:async()=>{},runReadCode:code=>request('browser_run_code_unsafe',{code}),inspect:async code=>({result:await request('browser_evaluate',{function:code})})};
 let effects=0;await uploadManagedAttachments(tab,'outlook',f.root,manifest,()=>effects++);
 await verifyManagedAttachments(tab,'outlook',manifest);
 assert.deepEqual(await request('browser_evaluate',{function:'()=>({sendClicks,uploadCount})'}),{sendClicks:0,uploadCount:1});assert.equal(effects,1);
});
