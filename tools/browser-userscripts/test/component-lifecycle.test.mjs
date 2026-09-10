import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import crypto from 'node:crypto';
import {execFile} from 'node:child_process';
import {promisify} from 'node:util';
import {fileURLToPath} from 'node:url';
const playwrightPath=process.env.SPARKCLAW_TEST_PLAYWRIGHT;
const root=fileURLToPath(new URL('../../../',import.meta.url));
const active='80700eb2-0fe8-46ea-902f-f4463134057d',retired='c6378bd8-6136-4f1b-81be-2c0fed94baf3',user='cc23958a-8f92-42c8-8d8b-fb5727429cf1';
const hash=x=>crypto.createHash('sha256').update(x).digest('hex');

test('native Tampermonkey lifecycle: fresh import, upgrade retirement, user-copy preservation and redeploy', {skip:!playwrightPath||!process.env.SPARKCLAW_TEST_COMPONENT_LIFECYCLE,timeout:180000}, async()=>{
  const {chromium}=await import(playwrightPath);
  const temp=await fs.mkdtemp(path.join(os.tmpdir(),'sc-native-lifecycle-'));
  let previous, expectedUser;
  try{
    for(let phase=0;phase<3;phase++){
      const stage=path.join(temp,'stage-'+phase),profile=path.join(temp,'profile-'+phase),extension=path.join(stage,'tampermonkey');
      await promisify(execFile)('python3',[path.join(root,'scripts/browser_components.py'),'stage',stage]);
      const policy=Object.values(JSON.parse(await fs.readFile(path.join(stage,'policy.json'),'utf8'))['3rdparty'].extensions)[0];
      const meta=JSON.parse(await fs.readFile(path.join(extension,'manifest.json'),'utf8'));
      const workerPath=path.join(extension,meta.background.service_worker);
      let source=await fs.readFile(workerPath,'utf8');
      // Isolate the managed-policy transport only. Native download, checksum,
      // import queue, script installation, reconciliation and deletion run intact.
      source=source.replace('if(!xt.managed.supported)return e;','').replace('xt.managed.get(["jsonImport"],(n=>{e=n,t(!0)}))','((callback)=>callback('+JSON.stringify(policy)+'))((n=>{e=n,t(!0)}))');
      await fs.writeFile(workerPath,source);
      const id=[...hash(extension).slice(0,32)].map(c=>String.fromCharCode(97+parseInt(c,16))).join('');
      await fs.mkdir(path.join(profile,'Default'),{recursive:true});
      await fs.writeFile(path.join(profile,'Default/Preferences'),JSON.stringify({extensions:{ui:{developer_mode:true},settings:{[id]:{user_scripts_enabled:true}}}}));
      const database=path.join(profile,'Default/Local Extension Settings',id);
      if(previous)await fs.cp(previous,database,{recursive:true});
      const context=await chromium.launchPersistentContext(profile,{headless:true,executablePath:process.env.SPARKCLAW_TEST_CHROMIUM,args:['--no-sandbox','--disable-extensions-except='+extension,'--load-extension='+extension]});
      try{
        const worker=context.serviceWorkers()[0]||await context.waitForEvent('serviceworker');
        let state;
        for(let n=0;n<45;n++){
          state=await worker.evaluate(async()=>chrome.storage.local.get(null));
          if(state['!misc.managed.consumed']?.value?.[policy.jsonImport[0].hash])break;
          await new Promise(r=>setTimeout(r,500));
        }
        assert.ok(state['!misc.managed.consumed']?.value?.[policy.jsonImport[0].hash],'native import must complete');
        assert.equal(state['!extdb.@meta#'+active].value.system,true);
        assert.equal(state['!extdb.@meta#'+active].value.enabled,true);
        const manifest=JSON.parse(await fs.readFile(path.join(root,'configs/browser-components.json'),'utf8'));
        assert.equal(hash(state['!extdb.@source#'+active].value),manifest.scripts[0].sha256);
        if(phase===0){
          const fixture={};
          for(const uid of [retired,user])for(const [key,value]of Object.entries(state)){
            if(!key.includes('#'+active))continue;
            const copy=structuredClone(value);
            if(key.startsWith('!extdb.@meta#'))Object.assign(copy.value,{uuid:uid,name:'ChatGPT Exporter',system:uid===retired});
            fixture[key.replace('#'+active,'#'+uid)]=copy;
          }
          expectedUser=Object.fromEntries(Object.entries(fixture).filter(([key])=>key.includes('#'+user)));
          // Simulate an older deployed version, retaining its native identity.
          fixture['!extdb.@meta#'+active]=structuredClone(state['!extdb.@meta#'+active]);fixture['!extdb.@meta#'+active].value.version='0.0.0';
          fixture['!extdb.@source#'+active]={origin:'normal',value:'// old deployed bytes'};
          await worker.evaluate(async fixture=>chrome.storage.local.set(fixture),fixture);
        }else{
          assert.equal(state['!extdb.@meta#'+retired],undefined,'retired managed identity is removed');
          for(const [key,value]of Object.entries(expectedUser))assert.deepEqual(state[key],value,'ordinary user copy must be byte-preserved');
          assert.equal(Object.keys(state).filter(key=>key.startsWith('!extdb.@meta#')&&state[key].value.system).length,1);
        }
      }finally{await context.close();}
      previous=database;
    }
  }finally{await fs.rm(temp,{recursive:true,force:true});}
});
