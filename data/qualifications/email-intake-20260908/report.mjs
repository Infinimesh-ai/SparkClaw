import fs from 'node:fs/promises';
import path from 'node:path';
import crypto from 'node:crypto';
import assert from 'node:assert/strict';
const base=path.dirname(new URL(import.meta.url).pathname);
const read=name=>fs.readFile(path.join(base,name),'utf8').then(JSON.parse);
const hash=value=>crypto.createHash('sha256').update(value).digest('hex');
const providers=[];
for(const provider of ['qq_mail','gmail','outlook']) {
 const wire=await read(provider+'-wire.json').catch(()=>null);
 const receipt=await read(provider+'-receipt.json').catch(e=>{if(e.code==='ENOENT')return null;throw e;});
 if(!receipt) {
  const discovery=await read(provider+'-discovery.json').catch(()=>null);
  const counts=await read(provider+'-self-counts.json').catch(()=>[]);
  providers.push({provider,status:'not_captured',discovery:discovery?{status:discovery.status,candidates:discovery.candidates.length,coverage:discovery.coverage}:null,conversation_counts:counts.map(({id,...record})=>record),wire});continue;
 }
 const manifestPath=path.join(base,'workspace',receipt.capture.manifest_path),dir=path.dirname(manifestPath);
 const bytes=await fs.readFile(manifestPath);assert.equal('sha256:'+hash(bytes),receipt.capture.manifest_sha256);
 const manifest=JSON.parse(bytes),intent=await read(provider+'-send-intent.json'),self=await read(provider+'-self.json');
 assert.equal(manifest.metadata.subject,intent.subject);
 assert.equal(manifest.metadata.from[0].address.toLowerCase(),self.account.toLowerCase());
 assert.equal(manifest.metadata.to[0].address.toLowerCase(),self.account.toLowerCase());
 assert.ok((await fs.readFile(path.join(dir,'body.txt'),'utf8')).includes(intent.body));
 for(const file of manifest.files){const data=await fs.readFile(path.join(base,'workspace',file.path));assert.equal(data.length,file.bytes);assert.equal('sha256:'+hash(data),file.sha256);}
 const parts=manifest.attachments.filter(p=>p.status==='available');assert.equal(parts.length,1);
 const attachment=await fs.readFile(path.join(dir,parts[0].path));assert.equal(hash(attachment),intent.sha256);
 const state=JSON.parse(await fs.readFile(path.join(dir,'read-state.json')));assert.equal(state.state,'confirmed');assert.equal(state.observed,'read');
 providers.push({provider,status:receipt.status,self_delivery_verified:true,body_verified:true,source_files:manifest.files.length,eml_bytes:(await fs.stat(path.join(dir,'message.eml'))).size,attachment_bytes:attachment.length,attachment_sha256:hash(attachment),read_state:state.observed,wire});
}
const report={schema_version:1,stage:'discovery_and_specified_capture',providers,limitations:['bounded loaded-list discovery; no complete pagination','multi-message conversations unsupported','no durable queue, mail Store or model integration'],alternative_eml_generation:false};
await fs.writeFile(path.join(base,'report.json'),JSON.stringify(report,null,2)+'\n',{mode:0o600});
console.log(JSON.stringify(report));
