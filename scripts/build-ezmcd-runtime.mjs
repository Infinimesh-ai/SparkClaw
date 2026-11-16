import fs from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawnSync } from 'node:child_process';
import crypto from 'node:crypto';
import { verifyMCDRuntime } from '../apps/desktop/src/main/mcd/runtime.mjs';
import contract from '../tools/ezmcd-adapter/runtime-contract.json' with { type: 'json' };
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'..');
if(process.platform!=='darwin'||process.arch!=='arm64')throw new Error('The McDonald’s desktop bundle is qualified only for macOS arm64; keep this capability disabled on other targets');
const output=path.join(root,'apps/desktop/bin/ezmcd');
function run(command,args){const r=spawnSync(command,args,{cwd:root,stdio:'inherit'});if(r.error||r.status!==0)throw new Error('McDonald’s runtime build failed');}
run(process.execPath,['scripts/sync-ezmcd-contract.mjs','--check']);
try{const info=await verifyMCDRuntime(path.join(output,'bin/python3'));process.stdout.write(JSON.stringify(info)+'\n');process.exit(0);}catch(error){
 if(process.argv.includes('--check'))throw error;
 try{const row=JSON.parse(await fs.readFile(path.join(output,'manifest.json')));if(row.kind!=='sparkclaw-mcd-runtime-v1')throw new Error('Unrecognized runtime output');await fs.rename(output,output+'-retained-'+Date.now());}
 catch(error){if(error.code!=='ENOENT')throw error;}
}
const python=process.env.SPARKCLAW_MCD_BUILD_PYTHON||'python3.11';
const probe=spawnSync(python,['-I','-B','-c','import sys; print(sys.base_prefix)'],{encoding:'utf8'});
if(probe.error||probe.status!==0)throw new Error('Install the pinned standalone Python toolchain for build-time packaging');
const prefix=probe.stdout.trim(),build=path.join(root,'.cache','ezmcd-release-'+contract.adapter_source_sha256.slice(0,16));
try{await fs.access(path.join(build,'manifest.json'));run('python3',['scripts/setup-ezmcd-adapter.py','--check','--output',build]);}
catch(error){try{await fs.access(build);throw error;}catch(found){if(found.code!=='ENOENT')throw found;}
 run('python3',['scripts/setup-ezmcd-adapter.py','--python',python,'--output',build]);}
run('python3',['scripts/package-ezmcd-runtime.py','--build',build,'--runtime-prefix',prefix,'--output',output]);
const pinsFile=path.join(root,'apps/desktop/src/shared/ezmcd-bundle-manifests.json');
const pins=JSON.parse(await fs.readFile(pinsFile,'utf8'));
pins[process.platform+'-'+process.arch]=crypto.createHash('sha256').update(await fs.readFile(path.join(output,'manifest.json'))).digest('hex');
await fs.writeFile(pinsFile,JSON.stringify(pins,null,2)+'\n');
process.stdout.write(JSON.stringify(await verifyMCDRuntime(path.join(output,'bin/python3')))+'\n');
