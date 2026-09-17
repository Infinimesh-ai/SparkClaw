import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import {execFile} from 'node:child_process';
import {promisify} from 'node:util';
import {pathToFileURL} from 'node:url';
import {stageNative,prepareNativeProfile,waitNativeImport,repository,hash} from './helpers/native-components.mjs';
import {connectBatchBridge} from '../../../scripts/ai-chat/bridge-context.mjs';
import {exportTimeline} from '../../../scripts/ai-chat/batch-export.mjs';
const enabled=process.env.SPARKCLAW_TEST_NATIVE_BRIDGE && process.env.SPARKCLAW_TEST_PLAYWRIGHT;

test('installed pinned userscript initializes and exports through the production native Bridge', {skip:!enabled,timeout:150000},async()=>{
  const {chromium}=await import(process.env.SPARKCLAW_TEST_PLAYWRIGHT);
  const temp=await fs.mkdtemp(path.join(os.tmpdir(),'sc-installed-bridge-'));
  const profile=path.join(temp,'profile'),extension=path.join(temp,'tampermonkey');
  const originalEnv={...process.env};
  let context,connection;
  try{
    const {policy,id}=await stageNative(path.join(temp,'stage'),extension,0,true);
    await prepareNativeProfile(profile,id);
    const launcher=path.join(temp,'launcher.mjs');
    await fs.writeFile(launcher,'#!/usr/bin/env node\nawait import('+JSON.stringify(pathToFileURL(path.join(repository,'tools/browser-controller/src/browser-bridge-launcher.mjs')).href)+');\n',{mode:0o700});
    const nativeHost=path.join(temp,'native-host.mjs');
    await fs.writeFile(nativeHost,'#!/usr/bin/env node\nawait import('+JSON.stringify(pathToFileURL(path.join(repository,'tools/browser-controller/src/browser-bridge-native-host.mjs')).href)+');\n',{mode:0o700});
    await fs.mkdir(path.join(profile,'NativeMessagingHosts'),{recursive:true,mode:0o700});
    await fs.writeFile(path.join(profile,'NativeMessagingHosts/com.sparkclaw.browser_bridge.json'),JSON.stringify({name:'com.sparkclaw.browser_bridge',description:'Isolated fixture native host',path:nativeHost,type:'stdio',allowed_origins:['chrome-extension://mmlmfjhmonkocbjadbfplnigmagldckm/']}),{mode:0o600});
    const socket=path.join(temp,'bridge-native.sock');
    Object.assign(process.env,{
      SPARKCLAW_BROWSER_BRIDGE_NATIVE_SOCKET:socket,
      PLAYWRIGHT_MCP_EXECUTABLE_PATH:launcher,
      PLAYWRIGHT_MCP_USER_DATA_DIR:profile,
      SPARKCLAW_PLAYWRIGHT_CLI_ENTRY:process.env.SPARKCLAW_TEST_CLI || path.join(repository,'tools/browser-controller/node_modules/@playwright/cli/playwright-cli.js')
    });
    const bridge=path.join(repository,'tools/browser-bridge');
    // Both extensions and their native messaging host are real. The socket,
    // fresh credential, profile and pages belong exclusively to this fixture.
    context=await chromium.launchPersistentContext(profile,{headless:true,env:{...process.env},executablePath:process.env.SPARKCLAW_TEST_CHROMIUM,args:['--no-sandbox','--disable-extensions-except='+extension+','+bridge,'--load-extension='+extension+','+bridge]});
    const worker=await waitNativeImport(context,id,policy);
    await context.route('https://chatgpt.com/**',route=>route.fulfill({contentType:'text/html',body:'<!doctype html><title>Installed fixture</title><nav><a href="/c/one">Fixture chat</a></nav><main><section data-testid="conversation-turn-1"><h4>You said</h4><div class="whitespace-pre-wrap">Native installed question</div></section><section data-testid="conversation-turn-2"><h4>ChatGPT said</h4><div class="markdown"><p>Native installed answer</p></div></section></main>'}));
    const owner=await context.newPage();await owner.goto('https://chatgpt.com/c/owner');
    await owner.locator('#sparkclaw-ai-export-bridge').waitFor({state:'attached',timeout:20000});
    assert.equal(await owner.locator('#export-controls-container, #export-outline-container, #sparkclaw-batch-controls').count(),0);
    const state=await worker.evaluate(async()=>chrome.storage.local.get(null));
    assert.equal(state['!extdb.@meta#80700eb2-0fe8-46ea-902f-f4463134057d'].value.evilness,0);
    assert.equal(state['!extdb.@meta#45854f60-f62b-4e49-98f9-f6a024c78330'].value.evilness,12,'unrecognized system scripts retain native unfamiliar-origin protection');
    const status=await context.newPage();await status.goto('chrome-extension://mmlmfjhmonkocbjadbfplnigmagldckm/status.html');
    process.env.PLAYWRIGHT_MCP_EXTENSION_TOKEN=await status.locator('#token').innerText();
    assert.ok(process.env.PLAYWRIGHT_MCP_EXTENSION_TOKEN);
    await status.close();
    for(let n=0;n<30;n++){
      try{await promisify(execFile)(launcher,['--check'],{env:process.env});break;}
      catch(error){if(n===29)throw error;await new Promise(r=>setTimeout(r,500));}
    }
    connection=await connectBatchBridge();
    const options={context:connection.context,provider:'chatgpt',workspaceRoot:temp,accountScope:'isolated-installed-fixture',timeoutMS:90000};
    const first=await exportTimeline(options);
    assert.deepEqual(first.exported,['one']);assert.equal(first.status,'saved_visible_history');
    const ledger=JSON.parse(await fs.readFile(path.join(first.directory,'ledger.json'),'utf8'));
    const bytes=await fs.readFile(path.join(first.directory,ledger.one.path));
    assert.equal(hash(bytes),ledger.one.sha256);
    assert.deepEqual(JSON.parse(bytes).messages.map(x=>x.content),['Native installed question','Native installed answer']);
    const second=await exportTimeline(options);assert.deepEqual(second.skipped,['one']);
    assert.equal(owner.isClosed(),false);assert.equal(owner.url(),'https://chatgpt.com/c/owner');
    await connection.close();connection=undefined;
    assert.equal(owner.isClosed(),false);
  }finally{
    await connection?.close().catch(()=>{});await context?.close();
    for(const key of Object.keys(process.env))if(!(key in originalEnv))delete process.env[key];Object.assign(process.env,originalEnv);
    await fs.rm(temp,{recursive:true,force:true});
  }
});
