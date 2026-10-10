import crypto from 'node:crypto';

const scope = (connection) => ({deployment_id:connection.deploymentID,owner_id:connection.ownerID,client_id:connection.clientID});
const ID = /^[A-Za-z0-9][A-Za-z0-9_.:-]{0,159}$/u;
const validPart = part => part?.available === true && /^sha256:[a-f0-9]{64}$/u.test(part.sha256) && Number.isSafeInteger(part.size) && part.size >= 0 && part.size <= 64*1024*1024;
export class MailSyncClient {
  constructor({store,localStore,getConnection,getFetch,getFileFetch,preferCurrentFiles=()=>false,installationID,ensureInstallation=async()=>{}}) {
    Object.assign(this,{store,localStore,getConnection,getFetch,getFileFetch,preferCurrentFiles,installationID,ensureInstallation});
    this.inflight=new Map();this.controllers=new Set();this.generation=0;this.active=true;
  }
  start(){this.active=true;return this;}
  close(){this.active=false;this.generation++;for(const controller of this.controllers)controller.abort();this.controllers.clear();this.inflight.clear();}
  #assertActive(generation,signal){if(!this.active||generation!==this.generation)throw new Error('Mail sync is paused; reconnect before synchronizing');signal?.throwIfAborted();}
  #connection(){const connection=this.getConnection();if(!connection) throw new Error('Mail sync is locked; sign in first');return connection;}
  read(mailbox){return this.store.read(scope(this.#connection()),mailbox);}
  catalog(){return this.store.catalog(scope(this.#connection()));}
  async saveAttachment(mailbox,mailID,partID,conversationID){
    const generation=this.generation;this.#assertActive(generation);
    if (typeof mailbox !== 'string' || !ID.test(mailbox) || typeof mailID !== 'string' || !ID.test(mailID) || typeof partID !== 'string' || partID && !ID.test(partID)) throw new Error('Mail attachment identity is invalid');
    const connection=this.#connection(); const identity=scope(connection);
    if(!this.localStore||!this.getFileFetch)throw new Error('Attachment saving is unavailable');
    this.localStore.read(identity,conversationID);
    const mail=this.store.read(identity,mailbox).messages.find((value)=>value.id===mailID);
    let part=mail?.attachments.find((value)=>value.id===partID);
    const cached = !this.preferCurrentFiles() && !!mail && partID !== '';
    let route = `/api/v1/mail/${encodeURIComponent(mailbox)}/messages/${encodeURIComponent(mailID)}/attachments/${encodeURIComponent(partID)}`;
    if (cached && !validPart(part)) throw new Error('Synchronize an available attachment within 64 MiB before saving it');
    await this.ensureInstallation();
    this.#assertActive(generation);
    if(this.getConnection()!==connection)throw new Error('Mail login changed');
    if (!cached) {
      // The full popup reads current service projections directly and must not
      // require a separate cache synchronization before downloading a file.
      const message = await this.#request(`/api/email/messages/${encodeURIComponent(mailID)}`, {}, generation);
      if (message.id !== mailID || message.mailbox_id !== mailbox) throw new Error('Mail attachment ownership differs');
      part = partID ? message.attachments?.find(value => value.id === partID) : { name: 'original.eml', available: message.original_available };
      if (!part?.available || typeof part.name !== 'string' || !part.name || part.name.length > 1024) throw new Error('The mail attachment is unavailable');
      route = `/api/email/messages/${encodeURIComponent(mailID)}/file${partID ? `?part_id=${encodeURIComponent(partID)}` : ''}`;
    }
    this.#assertActive(generation);
    if(this.getConnection()!==connection)throw new Error('Mail login changed');
    const controller=new AbortController();this.controllers.add(controller);
    const signal=AbortSignal.any([controller.signal,AbortSignal.timeout(30000)]);
    try {
      const response=await this.getFileFetch()(`${connection.origin}${route}`,{method:'GET',headers:{Authorization:connection.authorization,'X-SparkClaw-Installation':this.installationID},redirect:'manual',signal});
      this.#assertActive(generation,signal);
      if(!response.ok)throw new Error('The mail attachment is unavailable; synchronize and retry');
      if (!cached) {
        const length = response.headers.get('content-length'), digest = response.headers.get('x-sparkclaw-digest');
        if (!/^(0|[1-9][0-9]{0,8})$/u.test(length || '') || !/^[a-f0-9]{64}$/u.test(digest || '')) throw new Error('Mail attachment manifest is unavailable');
        part = { ...part, size: Number(length), sha256: `sha256:${digest}` };
        if (!validPart(part)) throw new Error('Mail attachment exceeds the 64 MiB limit');
      }
      const reader=response.body?.getReader();if(!reader)throw new Error('Attachment response is empty');
      const chunks=[];const hash=crypto.createHash('sha256');let size=0;
      try{for(;;){const {done,value}=await reader.read();this.#assertActive(generation,signal);if(done)break;size+=value.byteLength;if(size>part.size||size>64*1024*1024)throw new Error('Mail attachment size differs from its manifest');hash.update(value);chunks.push(value);}}
      finally{await reader.cancel().catch(()=>{});}
      if(size!==part.size||`sha256:${hash.digest('hex')}`!==part.sha256)throw new Error('Mail attachment integrity differs from its manifest');
      this.#assertActive(generation,signal);
      if(this.getConnection()!==connection)throw new Error('Mail login changed');
      return this.localStore.saveFile(identity,conversationID,part.name,Buffer.concat(chunks));
    } finally {this.controllers.delete(controller);}
  }
  async #request(path, {method='GET', body}={},generation=this.generation){
    this.#assertActive(generation);
    const connection=this.#connection();
    const controller=new AbortController();this.controllers.add(controller);
    const signal=AbortSignal.any([controller.signal,AbortSignal.timeout(30000)]);
    try {
      const response=await this.getFetch()(`${connection.origin}${path}`,{method, ...(body!==undefined?{body}:{}),headers:{Authorization:connection.authorization,Accept:'application/json','X-SparkClaw-Installation':this.installationID,...(body!==undefined?{'Content-Type':'application/json'}:{})},redirect:'manual',signal});
      this.#assertActive(generation,signal);
      if(response.status===401||response.status===403)throw new Error('Mail authorization expired; sign in again');
      if(response.status===409){const error=new Error('Mail cursor reset required');error.code='MAIL_RESET';throw error;}
      if(!response.ok)throw new Error('Mail service is unavailable; the last complete cache is retained');
      if(this.getConnection()!==connection) throw new Error('Mail login changed while synchronizing');
      const reader=response.body?.getReader();if(!reader)throw new Error('Mail response is empty');const chunks=[];let size=0;
      try {for(;;){const {done,value}=await reader.read();this.#assertActive(generation,signal);if(done)break;size+=value.byteLength;if(size>1024*1024)throw new Error('Mail response exceeds capacity');chunks.push(value);}}finally{await reader.cancel().catch(()=>{});}
      this.#assertActive(generation,signal);
      if(this.getConnection()!==connection) throw new Error('Mail login changed while synchronizing');
      return JSON.parse(Buffer.concat(chunks).toString('utf8'));
    } finally {this.controllers.delete(controller);}
  }
  async refreshCatalog(){const generation=this.generation;this.#assertActive(generation);const connection=this.#connection();await this.ensureInstallation();this.#assertActive(generation);const result=await this.#request('/api/v1/mail/mailboxes',{},generation);this.#assertActive(generation);if(this.getConnection()!==connection)throw new Error('Mail login changed');return this.store.catalog(scope(connection),result.mailboxes);}
  sync(mailbox){
    const connection=this.#connection();const key=JSON.stringify([scope(connection),mailbox]);
    if(this.inflight.has(key))return this.inflight.get(key);
    const generation=this.generation;
    const operation=this.#sync(connection,mailbox,generation).finally(()=>{if(this.inflight.get(key)===operation)this.inflight.delete(key);});this.inflight.set(key,operation);return operation;
  }
  async #sync(connection,mailbox,generation){
    this.#assertActive(generation);await this.ensureInstallation();this.#assertActive(generation);const identity=scope(connection);let resets=0;
    // The service and disk are bounded; avoid a busy mailbox keeping one IPC
    // request alive indefinitely. The next explicit sync resumes the cursor.
    for(let pages=0;pages<250;pages++){
      this.#assertActive(generation);
      if(this.getConnection()!==connection)throw new Error('Mail login changed');
      const cursor=this.store.cursor(identity,mailbox);
      let response;
      try {response=await this.#request(`/api/v1/mail/${encodeURIComponent(mailbox)}/sync`,{method:'POST',body:JSON.stringify({cursor,limit:100})},generation);}
      catch(error){this.#assertActive(generation);if(error.code==='MAIL_RESET'&&resets++<3){this.store.reset(identity,mailbox);continue;}throw error;}
      this.#assertActive(generation);
      const cached=this.store.apply(identity,mailbox,response,cursor);if(!response.more)return cached;
    }
    throw new Error('Mail sync paused at its page limit; retry to continue');
  }
}
