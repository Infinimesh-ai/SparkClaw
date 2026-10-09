import assert from 'node:assert/strict';
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
  return {call,inspect};
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
