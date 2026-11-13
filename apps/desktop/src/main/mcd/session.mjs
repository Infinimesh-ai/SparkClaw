import crypto from 'node:crypto';

const OPS = ['mcd.session.provision', 'mcd.session.revoke', 'mcd.session.status'];
const ID = /^[A-Za-z0-9_-]{1,128}$/u;
const hash = value => crypto.createHash('sha256').update(JSON.stringify(value)).digest('hex');

// Native-only connection controller. Renderer methods receive metadata only;
// account selection, envelopes and credentials never cross the preload bridge.
export class MCDSession {
  constructor({ vault, context, issue, capture, selectSource, invoke, now = Date.now }) {
    Object.assign(this, { vault, context, issue, capture, selectSource, invoke, now });
    this.busy = false;
  }
  scope() {
    const c = this.context();
    if (!c || !OPS.every(op => c.operations?.includes(op))) throw new Error('McDonald’s connection is unavailable');
    return this.localScope();
  }
  localScope() {
    const c=this.context();
    if(!c)throw new Error('McDonald’s connection identity is unavailable');
    const binding = Object.fromEntries(['deployment_id', 'owner_id', 'client_id', 'installation_id'].map(k => [k, c[k]]));
    if (!Object.values(binding).every(v => typeof v === 'string' && ID.test(v))) throw new Error('McDonald’s connection identity is unavailable');
    return { binding, resource: hash(binding) };
  }
  async status() {
    try {
      const { binding, resource } = this.localScope();
      const row = await this.vault.loadMCDConnection(resource);
      let online=true;try{this.scope();}catch{online=false;}
      if (!row) return { available: online, state: 'disconnected', checkout_enabled: false };
      this.validate(row, binding);
      if(!online)return {available:false,disconnect_available:row.state!=='revoked',state:row.state==='revoked'?'disconnected':row.state==='revoking'?'revoking':'offline',checkout_enabled:false};
      const remote = await this.invoke('mcd.session.status');
      this.current(binding);
      const status = remote.connections?.find(item => item.connection_id === row.connection_id);
      return { available: true, disconnect_available:row.state!=='revoked', state: row.state === 'revoking' ? 'revoking' : row.state === 'revoked' ? 'disconnected' : status?.state || 'unavailable', checkout_enabled: false };
    } catch { return { available: false, state: 'unavailable', checkout_enabled: false }; }
  }
  validate(row, binding) {
    if (row.schema_version !== 1 || hash(row.binding) !== hash(binding) || !ID.test(row.connection_id) ||
        !['pending', 'active', 'revoking', 'revoked'].includes(row.state)) throw new Error('McDonald’s saved connection needs recovery');
  }
  current(binding) { if (hash(this.scope().binding) !== hash(binding)) throw new Error('McDonald’s connection changed'); }
  async exclusive(action) {
    if (this.busy) throw new Error('McDonald’s connection is already changing');
    let settled; this.idle = new Promise(resolve=>{settled=resolve;});
    this.busy = true; try { return await action(); } finally { this.busy = false; settled(); }
  }
  async whenIdle() { await this.idle; }
  async connect() { return this.exclusive(async () => {
    const { binding, resource } = this.scope();
    let row = await this.vault.loadMCDConnection(resource);
    if (row) this.validate(row, binding);
    if (row?.state === 'revoking') throw new Error('Finish disconnecting McDonald’s first');
    if (!row || row.state === 'revoked') {
      const source = await this.selectSource();
      if (!source) return { canceled: true };
      this.current(binding);
      const sameSource=row?.source?.root===source.root && row?.source?.alias===source.alias;
      row = { ...(sameSource?{account_ref:row.account_ref,identity:row.identity,credential_revision:row.credential_revision,material_digest:row.material_digest,host_ledger:row.host_ledger}:{}),
        schema_version: 1, binding, source, connection_id: crypto.randomUUID(), account_ref: sameSource?row.account_ref:crypto.randomUUID(), state: 'pending' };
    }
    if(row.pending && this.now() >= row.pending.session_not_after_ms) {
      // Expiry proves the old grant cannot authorize another operation. Keep
      // its high-water mark and advance from it after fresh member validation.
      row={...row,envelope:row.pending,pending:undefined,expired_provision:{request_id:row.pending.provision_request_id,digest:row.pending.envelope_digest,session_not_after_ms:row.pending.session_not_after_ms}};
    }
    if (!row.pending) {
      const previous = row.envelope;
      const envelope = await this.issue({ account_root: row.source.root, alias: row.source.alias,
        cart_enabled: this.context()?.cart_enabled === true,
        binding: { ...binding, account_ref: row.account_ref }, connection_id: row.connection_id,
        request_id: crypto.randomUUID(), previous: {
          credential_revision: previous?.credential_revision || row.credential_revision || 0, grant_revision: previous?.grant_revision || 0,
          material_digest: previous?.material_digest || row.material_digest || null, member_digest: previous?.identity?.member_digest || row.identity?.member_digest || null } });
      this.current(binding);
      if (envelope?.connection_id !== row.connection_id || hash(Object.fromEntries(Object.keys(binding).map(k => [k, envelope.identity?.[k]]))) !== hash(binding)) throw new Error('McDonald’s issued identity differs');
      let sealed = row.host_account;
      if (this.capture) {
        sealed = await this.capture({ source: row.source, material_digest: envelope.material_digest });
        this.current(binding);
        if (sealed?.material_digest !== envelope.material_digest || typeof sealed.account_json !== 'string' || Buffer.byteLength(sealed.account_json) > 32*1024) throw new Error('McDonald’s local host account differs');
      }
      row = { ...row, host_account: sealed, pending: envelope, state: 'pending' };
      // Persist the exact envelope before sending. A lost receipt retries the
      // original request and cannot extend its original lease by accident.
      await this.vault.saveMCDConnection(resource, row);
    }
    this.current(binding);
    const receipt = await this.invoke('mcd.session.provision', row.pending);
    this.current(binding);
    if (receipt.request_id !== row.pending.provision_request_id || receipt.digest !== row.pending.envelope_digest || receipt.connection_id !== row.connection_id) throw new Error('McDonald’s receipt requires recovery');
    row = { ...row, identity: row.pending.identity, credential_revision: row.pending.credential_revision, material_digest:row.pending.material_digest, envelope: row.pending, pending: undefined, receipt, state: 'active' };
    await this.vault.saveMCDConnection(resource, row);
    return { available: true, state: this.now() < row.envelope.session_not_after_ms ? 'ready' : 'expired', checkout_enabled: false };
  }); }
  async disconnect() { return this.exclusive(async () => {
    const { binding, resource } = this.localScope();
    let row = await this.vault.loadMCDConnection(resource);
    if (!row) return { state: 'disconnected', checkout_enabled: false };
    this.validate(row, binding);
    if (row.state === 'revoked') return { state: 'disconnected', checkout_enabled: false };
    const identity = row.identity || row.pending?.identity || row.envelope?.identity;
    if (!identity) throw new Error('McDonald’s connection needs recovery');
    row = { ...row, identity, state: 'revoking', revoke_id: row.revoke_id || crypto.randomUUID(), envelope:undefined, pending:undefined, host_account:undefined };
    await this.vault.saveMCDConnection(resource, row);
    try{this.scope();}catch{return {available:false,state:'revoking',disconnect_available:true,checkout_enabled:false};}
    this.current(binding);
    const receipt = await this.invoke('mcd.session.revoke', { identity, connection_id: row.connection_id, request_id: row.revoke_id });
    this.current(binding);
    if (receipt.disposition !== 'revoked' || receipt.connection_id !== row.connection_id) throw new Error('McDonald’s disconnect requires recovery');
    // Keep the connection tombstone and original receipts, remove v4 material.
    row = { ...row, state: 'revoked', envelope: undefined, pending: undefined, revocation: receipt };
    await this.vault.saveMCDConnection(resource, row);
    return { state: 'disconnected', checkout_enabled: false };
  }); }
  async disableLocal() { return this.exclusive(async()=>{
    const {binding,resource}=this.localScope();
    const row=await this.vault.loadMCDConnection(resource);
    if(!row)return;
    this.validate(row,binding);
    if(row.state==='revoked')return;
    const identity=row.identity||row.pending?.identity||row.envelope?.identity;
    if(!identity)throw new Error('McDonald’s connection needs recovery');
    await this.vault.saveMCDConnection(resource,{...row,identity,state:'revoking',revoke_id:row.revoke_id||crypto.randomUUID(),envelope:undefined,pending:undefined,host_account:undefined});
  }); }
  async maintain() {
    if(this.busy)return;
    const {binding,resource}=this.scope();
    const row=await this.vault.loadMCDConnection(resource);
    if(!row||row.state==='revoked')return;
    this.validate(row,binding);
    if(row.state==='revoking')return this.disconnect();
    if(row.state==='pending'||this.now() >= row.envelope.session_not_after_ms-3600000)return this.connect();
  }
}
