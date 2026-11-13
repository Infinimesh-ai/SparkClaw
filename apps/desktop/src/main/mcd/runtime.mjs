import fs from 'node:fs/promises';
import path from 'node:path';
import crypto from 'node:crypto';
import contract from '../../shared/ezmcd-runtime-contract.json' with { type: 'json' };

const hash=bytes=>crypto.createHash('sha256').update(bytes).digest('hex');
const canonical=value=>value&&typeof value==='object'&&!Array.isArray(value)?`{${Object.keys(value).sort().map(k=>JSON.stringify(k)+':'+canonical(value[k])).join(',')}}`:Array.isArray(value)?`[${value.map(canonical).join(',')}]`:JSON.stringify(value);
export async function verifyMCDRuntime(python,{platform=process.platform,architecture=process.arch}={}) {
 if(!path.isAbsolute(python)||path.basename(python)!=='python3')throw new Error('McDonald’s runtime is unavailable');
 const root=path.dirname(path.dirname(python));
 for(let p=root;;p=path.dirname(p)){if((await fs.lstat(p)).isSymbolicLink())throw new Error('McDonald’s runtime is unavailable');if(path.dirname(p)===p)break;}
 const bytes=await fs.readFile(path.join(root,'manifest.json'));
 if(bytes.length>2*1024*1024)throw new Error('McDonald’s runtime manifest exceeds capacity');
 const pins=JSON.parse(await fs.readFile(new URL('../../shared/ezmcd-bundle-manifests.json',import.meta.url),'utf8'));
 if(hash(bytes)!==pins[platform+'-'+architecture])throw new Error('McDonald’s runtime manifest differs from the client release');
 const manifest=JSON.parse(bytes);
 if(manifest.schema_version!==1||manifest.kind!=='sparkclaw-mcd-runtime-v1'||manifest.platform!==platform||manifest.architecture!==({arm64:'arm64',x64:'x86_64'}[architecture])||canonical(manifest.contract)!==canonical(contract)||!manifest.files||Object.keys(manifest.files).length>6000||!manifest.files['bin/python3'])throw new Error('McDonald’s runtime does not match this client');
 let count=0,total=0;
 async function walk(dir,prefix=''){
  for(const entry of await fs.readdir(dir,{withFileTypes:true})){
   const name=prefix+entry.name,filename=path.join(dir,entry.name);
   if(entry.isSymbolicLink())throw new Error('McDonald’s runtime contains a link');
   if(entry.isDirectory()){await walk(filename,name+'/');continue;}
   if(!entry.isFile())throw new Error('McDonald’s runtime contains an unsupported file');
   if(name==='manifest.json')continue;
   const info=await fs.stat(filename);total+=info.size;
   if(total>160*1024*1024||info.size>40*1024*1024||!Object.hasOwn(manifest.files,name)||!(/^[a-f0-9]{64}$/u.test(manifest.files[name]))||hash(await fs.readFile(filename))!==manifest.files[name])throw new Error('McDonald’s runtime integrity check failed');
   count++;
  }
 }
 await walk(root);
 if(count!==Object.keys(manifest.files).length)throw new Error('McDonald’s runtime is incomplete');
 return {manifest_sha256:hash(bytes),source_sha256:contract.adapter_source_sha256,files:count,bytes:total};
}
