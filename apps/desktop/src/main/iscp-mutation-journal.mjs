import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
const hash=(value)=>crypto.createHash('sha256').update(value).digest('hex');

// Only opaque IDs, revisions and request fingerprints are retained. Credentials
// and mail bodies never enter this journal. An unknown mutation is reconciled by
// its original operation ID before a different mutation can use the resource.
export class ISCPMutationJournal {
  constructor(root,scope){this.root=root;this.scope=hash(JSON.stringify(scope));fs.mkdirSync(root,{recursive:true,mode:0o700});const st=fs.lstatSync(root);if(!st.isDirectory()||st.isSymbolicLink()||(st.mode&0o077))throw new Error('Mutation journal must be private');}
  read(resource){try{const value=JSON.parse(fs.readFileSync(this.#path(resource),'utf8'));if(value.scope!==this.scope||value.resource!==hash(resource)||!value.operation_id||!value.fingerprint)throw new Error('Mutation journal conflict');return value;}catch(error){if(error.code==='ENOENT')return;throw error;}}
  begin(resource,request,revision){const prior=this.read(resource);const fingerprint=hash(JSON.stringify(request));if(prior){if(prior.fingerprint!==fingerprint)throw new Error('The previous settings change requires reconciliation');return prior;}const value={schema_version:1,scope:this.scope,resource:hash(resource),operation_id:crypto.randomUUID(),fingerprint,expected_revision:revision||''};this.#write(resource,value);return value;}
  markReleased(resource,id){const value=this.read(resource);if(value?.operation_id!==id)throw new Error('Mutation receipt mismatch');this.#write(resource,{...value,audio_released:true});}
  complete(resource,id){if(this.read(resource)?.operation_id!==id)throw new Error('Mutation receipt mismatch');fs.unlinkSync(this.#path(resource));this.#sync();}
  matches(row,request){return row.fingerprint===hash(JSON.stringify(request));}
  #path(resource){return path.join(this.root,hash(this.scope+':'+resource)+'.json');}
  #write(resource,value){const filename=this.#path(resource),tmp=filename+'.'+crypto.randomUUID()+'.tmp';const fd=fs.openSync(tmp,'wx',0o600);try{fs.writeFileSync(fd,JSON.stringify(value));fs.fsyncSync(fd);}finally{fs.closeSync(fd);}fs.renameSync(tmp,filename);this.#sync();}
  #sync(){const fd=fs.openSync(this.root,'r');try{fs.fsyncSync(fd);}finally{fs.closeSync(fd);}}
}
