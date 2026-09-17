import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import {stageNative,prepareNativeProfile,waitNativeImport,repository as root,hash} from './helpers/native-components.mjs';
const playwrightPath=process.env.SPARKCLAW_TEST_PLAYWRIGHT;
const active='80700eb2-0fe8-46ea-902f-f4463134057d',retired='c6378bd8-6136-4f1b-81be-2c0fed94baf3',user='cc23958a-8f92-42c8-8d8b-fb5727429cf1';

test('native Tampermonkey lifecycle: fresh import, upgrade retirement, user-copy preservation and redeploy', {skip:!playwrightPath||!process.env.SPARKCLAW_TEST_COMPONENT_LIFECYCLE,timeout:180000}, async()=>{
  const {chromium}=await import(playwrightPath);
  const temp=await fs.mkdtemp(path.join(os.tmpdir(),'sc-native-lifecycle-'));
  let expectedUser;
  try{
    for(let phase=0;phase<3;phase++){
      const stage=path.join(temp,'stage-'+phase),profile=path.join(temp,'profile'),extension=path.join(temp,'installed');
      const {policy,id}=await stageNative(stage,extension,phase);
      if(phase===0)await prepareNativeProfile(profile,id);
      const context=await chromium.launchPersistentContext(profile,{headless:true,executablePath:process.env.SPARKCLAW_TEST_CHROMIUM,args:['--no-sandbox','--disable-extensions-except='+extension,'--load-extension='+extension]});
      try{
        const worker=await waitNativeImport(context,id,policy);
        const page=await context.newPage();
        await page.route('https://chatgpt.com/**',route=>route.fulfill({contentType:'text/html',body:'<!doctype html><nav><a href="/c/one">Fixture</a></nav><main></main>'}));
        await page.goto('https://chatgpt.com/');
        // Check actual native script execution, after first-install inspection.
        await page.locator('#sparkclaw-ai-export-bridge').waitFor({state:'attached',timeout:15000});
        assert.equal(await page.locator('#export-controls-container, #export-outline-container, #sparkclaw-batch-controls').count(),0);
        const state=await worker.evaluate(async()=>chrome.storage.local.get(null));
        assert.equal(state['!extdb.@meta#'+active].value.evilness,0,'pinned system script must be executable');
        assert.equal(state['!extdb.@meta#'+active].value.system,true);
        assert.equal(state['!extdb.@meta#'+active].value.enabled,true);
        const manifest=JSON.parse(await fs.readFile(path.join(root,'configs/browser-components.json'),'utf8'));
        assert.equal(hash(state['!extdb.@source#'+active].value),manifest.scripts.find(script=>script.uuid===active).sha256);
        if(phase===0){
          const fixture={};
          for(const uid of [retired,user])for(const [key,value]of Object.entries(state)){
            if(!key.includes('#'+active))continue;
            const copy=structuredClone(value);
            if(key.startsWith('!extdb.@meta#'))Object.assign(copy.value,{uuid:uid,name:'ChatGPT Exporter',system:uid===retired});
            if(key.startsWith('!extdb.@source#'))copy.value='// fixture copy remains unchanged';
            fixture[key.replace('#'+active,'#'+uid)]=copy;
          }
          expectedUser=Object.fromEntries(Object.entries(fixture).filter(([key])=>key.includes('#'+user)));
          // Simulate an older deployed version, retaining its native identity.
          fixture['!extdb.@meta#'+active]=structuredClone(state['!extdb.@meta#'+active]);fixture['!extdb.@meta#'+active].value.version='0.0.0';fixture['!extdb.@meta#'+active].value.evilness=12;
          fixture['!extdb.@source#'+active]={origin:'normal',value:'// old deployed bytes'};
          await worker.evaluate(async fixture=>chrome.storage.local.set(fixture),fixture);
        }else{
          assert.equal(state['!extdb.@meta#'+retired],undefined,'retired managed identity is removed');
          for(const [key,value]of Object.entries(expectedUser))assert.deepEqual(state[key],value,'ordinary user copy must be byte-preserved');
          const managedIDs=Object.keys(state).filter(key=>key.startsWith('!extdb.@meta#')&&state[key].value.system).map(key=>key.split('#')[1]).sort();
          assert.equal(managedIDs.length,manifest.scripts.length);
          assert.deepEqual(managedIDs,manifest.scripts.map(script=>script.uuid).sort());
        }
      }finally{await context.close();}

    }
  }finally{await fs.rm(temp,{recursive:true,force:true});}
});


test('native first-install inspection rejects changed bytes even at a recognized managed UUID', {skip:!playwrightPath||!process.env.SPARKCLAW_TEST_COMPONENT_LIFECYCLE,timeout:60000},async()=>{
  const {chromium}=await import(playwrightPath);
  const temp=await fs.mkdtemp(path.join(os.tmpdir(),'sc-native-origin-'));
  let context;
  try{
    const extension=path.join(temp,'installed'),profile=path.join(temp,'profile');
    const {policy,id}=await stageNative(path.join(temp,'stage'),extension,0,'tampered');
    await prepareNativeProfile(profile,id);
    context=await chromium.launchPersistentContext(profile,{headless:true,executablePath:process.env.SPARKCLAW_TEST_CHROMIUM,args:['--no-sandbox','--disable-extensions-except='+extension,'--load-extension='+extension]});
    const worker=await waitNativeImport(context,id,policy);
    let state;
    for(let attempt=0;attempt<50;attempt++){
      state=await worker.evaluate(async()=>chrome.storage.local.get(null));
      if(state['!extdb.@meta#'+active]?.value.evilness===12)break;
      await new Promise(resolve=>setTimeout(resolve,100));
    }
    assert.equal(state['!extdb.@meta#'+active].value.system,true);
    assert.equal(state['!extdb.@meta#'+active].value.evilness,12,'matching UUID alone must not bypass origin inspection');
    const manifest=JSON.parse(await fs.readFile(path.join(root,'configs/browser-components.json'),'utf8'));
    assert.notEqual(hash(state['!extdb.@source#'+active].value),manifest.scripts.find(script=>script.uuid===active).sha256);
  }finally{await context?.close();await fs.rm(temp,{recursive:true,force:true});}
});
