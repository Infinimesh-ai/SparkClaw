const scope = (connection) => ({deployment_id:connection.deploymentID,owner_id:connection.ownerID,client_id:connection.clientID});
export class MailSyncClient {
  constructor({store,getConnection,getFetch,installationID,ensureInstallation=async()=>{}}) {Object.assign(this,{store,getConnection,getFetch,installationID,ensureInstallation});this.inflight=new Map();}
  #connection(){const connection=this.getConnection();if(!connection) throw new Error('Mail sync is locked; sign in first');return connection;}
  read(mailbox){return this.store.read(scope(this.#connection()),mailbox);}
  catalog(){return this.store.catalog(scope(this.#connection()));}
  async #request(path){
    const connection=this.#connection();
    const response=await this.getFetch()(`${connection.origin}${path}`,{headers:{Authorization:connection.authorization,Accept:'application/json','X-SparkClaw-Installation':this.installationID},redirect:'manual',signal:AbortSignal.timeout(30000)});
    if(response.status===401||response.status===403)throw new Error('Mail authorization expired; sign in again');
    if(response.status===409){const error=new Error('Mail cursor reset required');error.code='MAIL_RESET';throw error;}
    if(!response.ok)throw new Error('Mail service is unavailable; the last complete cache is retained');
    if(this.getConnection()!==connection) throw new Error('Mail login changed while synchronizing');
    const reader=response.body?.getReader();if(!reader)throw new Error('Mail response is empty');const chunks=[];let size=0;
    try {for(;;){const {done,value}=await reader.read();if(done)break;size+=value.byteLength;if(size>1024*1024+8192)throw new Error('Mail response exceeds capacity');chunks.push(value);}}finally{await reader.cancel().catch(()=>{});}
    if(this.getConnection()!==connection) throw new Error('Mail login changed while synchronizing');
    return JSON.parse(Buffer.concat(chunks).toString('utf8'));
  }
  async refreshCatalog(){const connection=this.#connection();await this.ensureInstallation();const result=await this.#request('/api/r3/mail/mailboxes');if(this.getConnection()!==connection)throw new Error('Mail login changed');return this.store.catalog(scope(connection),result.mailboxes);}
  sync(mailbox){
    const connection=this.#connection();const key=JSON.stringify([scope(connection),mailbox]);
    if(this.inflight.has(key))return this.inflight.get(key);
    const operation=this.#sync(connection,mailbox).finally(()=>this.inflight.delete(key));this.inflight.set(key,operation);return operation;
  }
  async #sync(connection,mailbox){
    await this.ensureInstallation();const identity=scope(connection);let resets=0;
    // The service and disk are bounded; avoid a busy mailbox keeping one IPC
    // request alive indefinitely. The next explicit sync resumes the cursor.
    for(let pages=0;pages<250;pages++){
      if(this.getConnection()!==connection)throw new Error('Mail login changed');
      const cursor=this.store.cursor(identity,mailbox);
      let response;
      try {response=await this.#request(`/api/r3/mail/${encodeURIComponent(mailbox)}/sync?limit=100${cursor?`&cursor=${encodeURIComponent(cursor)}`:''}`);}
      catch(error){if(error.code==='MAIL_RESET'&&resets++<3){this.store.reset(identity,mailbox);continue;}throw error;}
      const cached=this.store.apply(identity,mailbox,response,cursor);if(!response.more)return cached;
    }
    throw new Error('Mail sync paused at its page limit; retry to continue');
  }
}
