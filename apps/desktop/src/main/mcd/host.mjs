import crypto from 'node:crypto';
import {isDeepStrictEqual} from 'node:util';

export const MCD_HOST_OPERATIONS = Object.freeze(['mcd.account.ensure', 'mcd.checkout.prepare', 'mcd.checkout.commit', 'mcd.checkout.abort', 'mcd.operation.status', 'mcd.orders.query']);
const digest = value => crypto.createHash('sha256').update(value).digest('hex');
const sha = value => typeof value === 'string' && /^[a-f0-9]{64}$/u.test(value);
const id = value => typeof value === 'string' && /^[A-Za-z0-9_-]{1,128}$/u.test(value);
const fields = (value, keys) => value && typeof value === 'object' && !Array.isArray(value) && Object.keys(value).sort().join(',') === [...keys].sort().join(',');

// Main-process deterministic host. No model, renderer credential input, generic
// mutation journal or HTTP fallback participates in quote preparation.
export class MCDHost {
  constructor({ session, context, call, approvals, now = Date.now }) { Object.assign(this, { session, context, call, approvals, now }); this.transfers = new Map(); }
  close() { this.active?.abort(); for (const row of this.transfers.values()) { clearTimeout(row.timer); for (const part of row.parts.values()) part.fill(0); } this.transfers.clear(); }
  peer(peer) {
    const c = this.context();
    if (!c || c.session_id !== peer?.session_id || c.authorization_revision !== peer?.authorization_revision) throw new Error('McDonald’s host session changed');
    return c;
  }
  async handleReverse(request, peer) {
    try {
      const current = this.peer(peer);
      if (!MCD_HOST_OPERATIONS.includes(request.operation) || !current.operations?.includes(request.operation) || !current.qualified_operations?.includes(request.operation) || request.installation_id !== current.installation_id || Buffer.byteLength(JSON.stringify(request.body)) > 48 * 1024) throw new Error();
      if (request.operation === 'mcd.account.ensure') return { status: 200, body: await this.session.status() };
      return await this.session.exclusive(async () => {
        const { binding, resource } = this.session.scope();
        let row = await this.session.vault.loadMCDConnection(resource);
        this.session.validate(row, binding);
        const body = request.body;
        const scope = body?.scope;
        const identity = row.identity || row.envelope?.identity || row.pending?.identity;
        if (!fields(scope, ['deployment_id','owner_id','client_id','installation_id','account_ref','member_digest','credential_revision','execution_id']) || !id(scope.execution_id) || !Number.isSafeInteger(scope.credential_revision) || scope.credential_revision < 1 || !identity || Object.keys(identity).some(key => identity[key] !== scope[key])) throw new Error();
        const prepare = request.operation === 'mcd.checkout.prepare';
        const commit = request.operation === 'mcd.checkout.commit';
        const orders = request.operation === 'mcd.orders.query';
        if ((prepare || commit) && (row.state !== 'active' || row.envelope.credential_revision !== scope.credential_revision || this.now() >= row.envelope.session_not_after_ms)) throw new Error();
        let input;
        if (commit) {
          if (!fields(body,['scope','operation_key','purchase_id','handoff_epoch','approval_id','review']) || !id(body.operation_key) || body.operation_key.length > 80 || !id(body.purchase_id) || !Number.isSafeInteger(body.handoff_epoch) || body.handoff_epoch < 1 || !id(body.approval_id) || !row.host_account || row.host_account.material_digest !== row.envelope.material_digest) throw new Error();
          // The renderer has already persisted its explicit decision before
          // sending it to Policy. Both approved and decision_pending are valid:
          // the reverse call may arrive before the approval response itself.
          const approval = this.approvals?.(binding,scope.execution_id)?.find(a=>a.approval_id===body.approval_id);
          if (!approval || approval.tool !== 'mcd.purchase.commit' || approval.decision !== 'approve' || !['approved','decision_pending'].includes(approval.state) || !sha(approval.digest)) throw new Error();
          if (!approval.arguments || typeof approval.arguments !== 'object' || Array.isArray(approval.arguments)) throw new Error();
          // Keep runtime verifier metadata in the saved approval and its
          // original digest; compare only the native purchase review fields.
          const approvedReview = { ...approval.arguments };
          delete approvedReview._verifier;
          const expires = typeof approval.expires_at==='number' ? approval.expires_at : Date.parse(approval.expires_at);
          if (!Number.isSafeInteger(expires) || expires <= this.now() || expires > this.now()+900000 || !fields(body.review,['purchase_id','purchase_digest','amount_cents','currency','store_code','pickup_at','eat_type','payment_method','credential_revision','store_name','items','discount_names']) || !fields(approvedReview,Object.keys(body.review)) || !isDeepStrictEqual(body.review,approvedReview) || body.review.purchase_id!==body.purchase_id || body.review.credential_revision!==scope.credential_revision || body.review.payment_method!=='ARCHCARD' || body.review.currency!=='CNY') throw new Error();
          input={operation_key:body.operation_key,purchase_id:body.purchase_id,handoff_epoch:body.handoff_epoch,authority:{approval_id:approval.approval_id,approval_digest:approval.digest,expires_at_ms:expires,review:body.review}};
        } else if (orders) {
          if(!fields(body,['scope','limit','offset']) || !Number.isSafeInteger(body.limit) || body.limit<1 || body.limit>20 || !Number.isSafeInteger(body.offset) || body.offset<0 || body.offset>10000)throw new Error();
          input={limit:body.limit,offset:body.offset};
        } else if (prepare) {
          if (!fields(body, ['scope','operation_key','transfer']) || !id(body.operation_key)) throw new Error();
          const envelope = this.receive(scope, body.operation_key, body.transfer);
          if (!envelope) return { status: 200, body: { state: 'chunk_received', operation_key: body.operation_key, index: body.transfer.index } };
          input = { operation_key: body.operation_key, envelope };
        } else {
          const keys = request.operation === 'mcd.checkout.abort' ? ['scope','operation_key','purchase_id','handoff_epoch','reason'] : ['scope','operation_key'];
          if (!fields(body, keys)) throw new Error();
          input = { ...body }; delete input.scope;
        }
        // The platform anchor is committed before initializing control tables
        // in the existing ledger. Losing either side never creates a fresh DB.
        const initialize = !row.host_ledger;
        if (initialize) {
          row = { ...row, host_ledger: 'initialization_claimed' };
          await this.session.vault.saveMCDConnection(resource, row);
        }
        this.peer(peer); this.session.current(binding);
        const controller=new AbortController();this.active=controller;
        let result;
        try { result = await this.call({ operation: commit ? 'host.commit' : orders ? 'host.orders' : prepare ? 'host.prepare' : request.operation === 'mcd.checkout.abort' ? 'host.abort' : 'host.status',
          params: { source: row.source, binding: scope, material_digest: row.envelope?.material_digest || null, initialize, request: input,
            ...((commit || orders || request.operation==='mcd.operation.status') && row.host_account ? {account_json:row.host_account.account_json} : {}) } },{signal:controller.signal}); }
        finally {if(this.active===controller)this.active=undefined;}
        if(controller.signal.aborted)throw new Error();
        this.peer(peer); this.session.current(binding);
        if (row.host_ledger !== 'ready') await this.session.vault.saveMCDConnection(resource, { ...row, host_ledger: 'ready' });
        return { status: 200, body: result };
      });
    } catch { return { status: 503, error_code: 'capability_unavailable', error: 'McDonald’s operation requires recovery' }; }
  }
  receive(scope, key, part) {
    if (!fields(part, ['digest','size','count','index','expires_at_ms','data']) || !sha(part.digest) || !Number.isSafeInteger(part.size) || part.size < 1 || part.size > 256 * 1024 || !Number.isSafeInteger(part.count) || part.count !== Math.ceil(part.size / 8192) || !Number.isSafeInteger(part.index) || part.index < 0 || part.index >= part.count || !Number.isSafeInteger(part.expires_at_ms) || part.expires_at_ms <= this.now() || part.expires_at_ms > this.now() + 120000 || typeof part.data !== 'string' || part.data.length > 10924) throw new Error();
    for (const [id, row] of this.transfers) if (row.expires <= this.now()) { clearTimeout(row.timer); for (const value of row.parts.values()) value.fill(0); this.transfers.delete(id); }
    const transferID = digest(JSON.stringify([scope,key]));
    let row = this.transfers.get(transferID);
    const binding = JSON.stringify([part.digest,part.size,part.count,part.expires_at_ms]);
    if (!row) {
      if (this.transfers.size >= 4) throw new Error();
      row = { binding, expires: part.expires_at_ms, parts: new Map() }; this.transfers.set(transferID,row);
      row.timer=setTimeout(()=>{for(const bytes of row.parts.values())bytes.fill(0);this.transfers.delete(transferID);},part.expires_at_ms-this.now());row.timer.unref?.();
    }
    if (row.binding !== binding) throw new Error();
    const bytes = Buffer.from(part.data,'base64');
    if (bytes.toString('base64') !== part.data || bytes.length !== Math.min(8192,part.size-part.index*8192)) { bytes.fill(0); throw new Error(); }
    const previous = row.parts.get(part.index);
    if (previous) { const equal = previous.equals(bytes); bytes.fill(0); if (!equal) throw new Error(); }
    else row.parts.set(part.index,bytes);
    if (row.parts.size !== part.count) return undefined;
    const payload = Buffer.concat(Array.from({length:part.count},(_,i)=>row.parts.get(i)));
    try {
      if (digest(payload)!==part.digest) throw new Error();
      const envelope=JSON.parse(payload.toString('utf8'));
      if (envelope.expires_at_ms!==part.expires_at_ms || !fields(envelope.scope,Object.keys(scope)) || Object.keys(scope).some(key=>envelope.scope[key]!==scope[key])) throw new Error();
      return envelope;
    } finally { payload.fill(0); clearTimeout(row.timer); for (const value of row.parts.values()) value.fill(0); this.transfers.delete(transferID); }
  }
}
