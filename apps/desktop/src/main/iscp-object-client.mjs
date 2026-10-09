import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';

const HASH=/^[a-f0-9]{64}$/u;
const ID=/^[A-Za-z0-9][A-Za-z0-9_.:-]{0,159}$/u;
const digest=(bytes)=>crypto.createHash('sha256').update(bytes).digest('hex');
const MAX_BYTES=64*1024*1024;

// Every checkpoint follows fsync of its bytes. A session/Grant changes transport,
// never the immutable identity of an upload or download. No execution is retried.
export class ISCPObjectClient {
  constructor({root,scope,call,chunkBytes=8192}) {
    if (!path.isAbsolute(root)||!Number.isSafeInteger(chunkBytes)||chunkBytes<1024||chunkBytes>8192) throw new Error('Invalid object journal configuration');
    Object.assign(this,{root,scope,call,chunkBytes});
    fs.mkdirSync(root,{recursive:true,mode:0o700});
    const info=fs.lstatSync(root);if(!info.isDirectory()||info.isSymbolicLink()||(info.mode&0o077))throw new Error('Object journal must be private');
    this.scopeHash=digest(JSON.stringify(scope));this.pending=new Map();
  }
  upload(bytes,{purpose,name='object',media_type='application/octet-stream'},signal) {
    bytes=Buffer.from(bytes);
    if(!ID.test(purpose)||typeof name!=='string'||!name||name.length>255||bytes.length>MAX_BYTES)throw new Error('Invalid upload manifest');
    const manifest={purpose,name,media_type,size:bytes.length,sha256:digest(bytes)};
    const key=digest(JSON.stringify([this.scopeHash,manifest]));
    return this.#once(key,()=>this.#upload(key,bytes,manifest,signal));
  }
  async #upload(key,bytes,manifest,signal) {
    let journal=this.#load(key);
    if(!journal){journal={schema_version:1,scope:this.scopeHash,direction:'upload',manifest,transfer_id:crypto.randomUUID(),next:0};this.#save(key,journal);}
    if(journal.direction!=='upload'||JSON.stringify(journal.manifest)!==JSON.stringify(manifest))throw new Error('Object upload journal conflict');
    if(journal.object){await this.#call('object.describe',journal.object,signal);return journal.object;}
    await this.#call('transfer.open',{transfer_id:journal.transfer_id,...manifest},signal);
    // Re-send only the last unconfirmed chunk. Receivers reject conflicting bytes
    // and return the original durable receipt for an identical chunk/commit.
    for(let offset=journal.next;offset<bytes.length;offset+=this.chunkBytes){
      signal?.throwIfAborted();const content=bytes.subarray(offset,Math.min(offset+this.chunkBytes,bytes.length));
      await this.#call('transfer.chunk',{transfer_id:journal.transfer_id,index:offset/this.chunkBytes,offset,sha256:digest(content),data_base64:content.toString('base64')},signal);
      journal.next=offset+content.length;this.#save(key,journal);
    }
    const receipt=await this.#call('transfer.commit',{transfer_id:journal.transfer_id},signal);
    const object=receipt.object??receipt;
    validateObject(object);
    if(object.sha256!==manifest.sha256||object.size!==manifest.size)throw new Error('Object commit integrity mismatch');
    journal.object=object;this.#save(key,journal);return object;
  }
  download(object,signal) {
    validateObject(object);
    const key=digest(JSON.stringify([this.scopeHash,'download',object.object_id,object.version,object.sha256]));
    return this.#once(key,()=>this.#download(key,object,signal));
  }
  async #download(key,object,signal) {
    let journal=this.#load(key);
    if(!journal){journal={schema_version:1,scope:this.scopeHash,direction:'download',object,next:0,chunks:[]};this.#save(key,journal);}
    if(journal.direction!=='download'||JSON.stringify(journal.object)!==JSON.stringify(object))throw new Error('Object download journal conflict');
    const chunks=[];
    for(const chunk of journal.chunks){const bytes=fs.readFileSync(this.#chunkPath(key,chunk.index));if(bytes.length!==chunk.length||digest(bytes)!==chunk.sha256)throw new Error('Object journal integrity mismatch');chunks.push(bytes);}
    for(let offset=journal.next;offset<object.size;){
      signal?.throwIfAborted();const length=Math.min(this.chunkBytes,object.size-offset);
      const received=await this.#call('object.read',{object_id:object.object_id,version:object.version,offset,length},signal);
      const bytes=decode(received.data_base64);
      if(received.offset!==offset||bytes.length!==length||received.sha256!==digest(bytes))throw new Error('Object chunk integrity mismatch');
      const index=journal.chunks.length;atomicWrite(this.#chunkPath(key,index),bytes);
      journal.chunks.push({index,length:bytes.length,sha256:received.sha256});journal.next=offset+bytes.length;this.#save(key,journal);
      chunks.push(bytes);offset=journal.next;
    }
    const content=Buffer.concat(chunks);
    if(content.length!==object.size||digest(content)!==object.sha256)throw new Error('Object final integrity mismatch');
    return content;
  }
  async #call(operation,body,signal){signal?.throwIfAborted();const value=await this.call(operation,body,{signal});signal?.throwIfAborted();return value;}
  #once(key,operation){if(this.pending.has(key))return this.pending.get(key);const promise=operation().finally(()=>this.pending.delete(key));this.pending.set(key,promise);return promise;}
  #load(key){try{const raw=fs.readFileSync(path.join(this.root,key+'.json'));if(raw.length>4*1024*1024)throw new Error('Object journal exceeds bound');const value=JSON.parse(raw);if(value.schema_version!==1||value.scope!==this.scopeHash)throw new Error('Object journal scope mismatch');return value;}catch(error){if(error.code==='ENOENT')return undefined;throw error;}}
  #save(key,value){atomicWrite(path.join(this.root,key+'.json'),Buffer.from(JSON.stringify(value)));}
  #chunkPath(key,index){return path.join(this.root,`${key}.${index}.chunk`);}
}
export function validateObject(object){if(!object||!ID.test(object.object_id)||!Number.isSafeInteger(object.version)||object.version<1||!Number.isSafeInteger(object.size)||object.size<0||object.size>MAX_BYTES||!HASH.test(object.sha256))throw new Error('Invalid object reference');return object;}
function decode(value){if(typeof value!=='string'||value.length>12000||!/^([A-Za-z0-9+/]{4})*([A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/u.test(value))throw new Error('Invalid object bytes');return Buffer.from(value,'base64');}
function atomicWrite(filename,bytes){const temporary=filename+'.'+crypto.randomUUID()+'.tmp';const fd=fs.openSync(temporary,'wx',0o600);try{fs.writeFileSync(fd,bytes);fs.fsyncSync(fd);}finally{fs.closeSync(fd);}try{fs.renameSync(temporary,filename);const directory=fs.openSync(path.dirname(filename),'r');try{fs.fsyncSync(directory);}finally{fs.closeSync(directory);}}finally{fs.rmSync(temporary,{force:true});}}
