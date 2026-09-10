import fs from 'node:fs/promises';
import path from 'node:path';
import crypto from 'node:crypto';
import {execFile} from 'node:child_process';
import {promisify} from 'node:util';
import {fileURLToPath} from 'node:url';
export const repository=fileURLToPath(new URL('../../../../',import.meta.url));
export const hash=value=>crypto.createHash('sha256').update(value).digest('hex');
export const identity=extension=>[...hash(extension).slice(0,32)].map(c=>String.fromCharCode(97+parseInt(c,16))).join('');
export async function stageNative(directory,extension,phase=0,negativeFixture=false){
  if(negativeFixture){
    // Append an unrecognized system script to the policy input only, without
    // adding it to the product's recognized UUID/source digest map.
    const code=`import sys,copy,base64
from pathlib import Path
sys.path.insert(0,sys.argv[1])
import browser_components as c
original=c.make_provisioning
def fixture(m):
 p=original(m)
 target=next(script for script in p['scripts'] if script['uuid']=='80700eb2-0fe8-46ea-902f-f4463134057d')
 if sys.argv[3]=='tampered':
  target['source']=base64.b64encode(base64.b64decode(target['source'])+b'\\n// changed fixture bytes\\n').decode()
  return p
 other=copy.deepcopy(target);other['uuid']='45854f60-f62b-4e49-98f9-f6a024c78330';other['name']='Unknown fixture script'
 other['source']=base64.b64encode(b'// ==UserScript==\\n// @name Unknown fixture script\\n// @namespace fixture\\n// @version 1\\n// @match https://chatgpt.com/*\\n// ==/UserScript==\\n').decode()
 p['scripts'].append(other)
 return p
c.make_provisioning=fixture
c.stage(Path(sys.argv[2]))`;
    await promisify(execFile)('python3',['-c',code,path.join(repository,'scripts'),directory,String(negativeFixture)]);
  }else await promisify(execFile)('python3',[path.join(repository,'scripts/browser_components.py'),'stage',directory]);
  const policy=Object.values(JSON.parse(await fs.readFile(path.join(directory,'policy.json'),'utf8'))['3rdparty'].extensions)[0];
  const meta=JSON.parse(await fs.readFile(path.join(directory,'tampermonkey/manifest.json'),'utf8'));
  // Simulate actual extension upgrades, retaining the installed path/profile.
  if(phase)meta.version=meta.version.replace(/\d+$/,value=>String(Number(value)+phase));
  await fs.rm(extension,{recursive:true,force:true});
  await fs.cp(path.join(directory,'tampermonkey'),extension,{recursive:true});
  const workerPath=path.join(extension,meta.background.service_worker);
  let source=await fs.readFile(workerPath,'utf8');
  const marker='xt.managed.get(["jsonImport"],(n=>{e=n,t(!0)}))';
  if(source.split(marker).length!==2)throw new Error('fixture_policy_transport_changed');
  // Only replace managed-policy transport. Native download, hash validation,
  // import queue, installation and origin inspection remain intact.
  source=source.replace('if(!xt.managed.supported)return e;','').replace(marker,'((callback)=>callback('+JSON.stringify(policy)+'))((n=>{e=n,t(!0)}))');
  await fs.writeFile(workerPath,source);
  await fs.writeFile(path.join(extension,'manifest.json'),JSON.stringify(meta));
  return {policy,id:identity(extension)};
}
export async function prepareNativeProfile(profile,id){
  await fs.mkdir(path.join(profile,'Default'),{recursive:true});
  await fs.writeFile(path.join(profile,'Default/Preferences'),JSON.stringify({extensions:{ui:{developer_mode:true},settings:{[id]:{user_scripts_enabled:true}}}}));
}
export async function waitNativeImport(context,id,policy){
  const worker=context.serviceWorkers().find(worker=>new URL(worker.url()).host===id)||await context.waitForEvent('serviceworker',{predicate:worker=>new URL(worker.url()).host===id});
  for(let n=0;n<90;n++){
    const state=await worker.evaluate(async()=>chrome.storage.local.get(null));
    if(state['!misc.managed.consumed']?.value?.[policy.jsonImport[0].hash])return worker;
    await new Promise(resolve=>setTimeout(resolve,500));
  }
  throw new Error('native_import_did_not_complete');
}
