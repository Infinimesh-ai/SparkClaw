import crypto from 'node:crypto';
import {ControllerError, invalidRequest} from './errors.mjs';
import {parseToken, parseGeneration, parseProfileID, requireExactObject} from './protocol.mjs';

const providers = new Set(['qq_mail', 'gmail', 'outlook']);
const states = new Set(['starting', 'watching', 'degraded', 'login_required', 'stopped']);
const reasons = new Set(['qq_inbound_envelope', 'gmail_topic_invalidation', 'outlook_delivery_change']);
const digest = value => crypto.createHash('sha256').update(value).digest('hex');
const stale = () => new ControllerError('browser_page_stale', 'mail observer lease is stale', {status:409, retryable:true});

// A bounded, acknowledged long-poll feed over the owner-only Controller socket.
// Waiting and lease renewal never enter the browser/Reader reservation gate.
export class MailObserverFeed {
  constructor(controller, {leaseMS=90000, pollMS=25000, limit=128, now=Date.now}={}) {
    this.controller=controller; this.leaseMS=leaseMS; this.pollMS=pollMS; this.limit=limit; this.now=now;
    this.epoch=crypto.randomUUID(); this.sequence=0; this.acked=0; this.events=[];
    this.bindings=new Map(); this.work=new Map(); this.waiters=new Set(); this.owned=new Set(); this.closed=false;
    if (controller.scriptFactory) controller.scriptFactory.mailObserverSink=(provider, slot, kind, reason) => this.observe(provider, slot, kind, reason);
    this.timer=setInterval(() => this.tick(), 5000); this.timer.unref();
  }

  auth(input, proof=false) {
    parseProfileID(input.profile_id, this.controller.profileID);
    const token=parseToken(input.token), generation=parseGeneration(input.credential_generation, 'credential_generation');
    const hash=digest(token);
    if (this.closed || !proof && (hash !== this.hash || generation !== this.generation || this.now() >= this.expires)) throw stale();
    return {token, generation, hash};
  }

  async reconcile(input) {
    requireExactObject(input, ['profile_id','token','credential_generation','bindings']);
    const auth=this.auth(input,true);
    if (!Array.isArray(input.bindings) || input.bindings.length>3) throw invalidRequest();
    const desired=new Map();
    for (const b of input.bindings) {
      requireExactObject(b,['provider','owner_scope','mailbox_id','account_address','binding_generation']);
      if (!providers.has(b.provider) || desired.has(b.provider) || !/^[a-f0-9]{64}$/.test(b.owner_scope) ||
          typeof b.mailbox_id!=='string' || !/^[a-zA-Z0-9_-]{1,128}$/.test(b.mailbox_id) ||
          typeof b.account_address!=='string' || b.account_address.length>320 || !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(b.account_address)) throw invalidRequest();
      parseGeneration(b.binding_generation,'binding_generation');
      desired.set(b.provider,{...b, identity:digest(JSON.stringify([auth.token,auth.generation,b.provider,b.account_address.toLowerCase(),b.owner_scope]))});
    }
    try {
      if (auth.generation < (this.generation ?? 0)) throw stale();
      if (auth.hash!==this.hash || auth.generation!==this.generation) {
        // No native call on routine renewals. An unproved token cannot replace
        // the currently valid subscriptions or learn their events.
        await this.controller.validateToken({profile_id:input.profile_id,token:auth.token});
        if (auth.generation < (this.generation ?? 0)) throw stale();
        this.hash=auth.hash; this.generation=auth.generation; this.epoch=crypto.randomUUID();
        this.events=[]; this.sequence=0; this.acked=0; this.notify();
      }
      this.token=auth.token; this.expires=this.now()+this.leaseMS;
      for (const [provider, b] of this.bindings) {
        const next=desired.get(provider);
        if (!next || next.identity!==b.identity || next.mailbox_id!==b.mailbox_id || next.binding_generation!==b.binding_generation) {
          b.retired=true; this.bindings.delete(provider); this.work.get(provider)?.abort.abort();
          if (this.controller.scriptFactory?.sharedMailPages) this.controller.scriptFactory.mailObservers?.revoke?.(provider);
        }
      }
      for (const [provider,b] of desired) if (!this.bindings.has(provider)) {
        b.state='starting'; this.bindings.set(provider,b); this.publish(b,'resync_required','registration');
      }
      this.tick();
      return {schema_version:1, epoch:this.epoch, credential_generation:this.generation, lease_ms:this.leaseMS,
        watches:[...this.bindings.values()].map(b=>({provider:b.provider,mailbox_id:b.mailbox_id,binding_generation:b.binding_generation,watch_epoch:b.watchEpoch??'',state:b.state,sequence:this.sequence}))};
    } finally {input.token='';}
  }

  observe(provider,slot,kind,reason) {
    const b=this.bindings.get(provider);
    if (!b || b.identity!==slot.identity || this.now()>=this.expires) return;
    if (b.watchEpoch!==slot.epoch) {b.watchEpoch=slot.epoch; this.publish(b,'resync_required','document_replaced');}
    const state=states.has(slot.state)?slot.state:'degraded';
    if (state!==b.state) {
      b.state=state; this.publish(b,'watch_state','state_changed');
      if (state==='degraded' || state==='login_required') this.publish(b,'resync_required','observer_degraded');
    }
    if (kind==='mailbox_changed' && reasons.has(reason)) this.publish(b,kind,reason);
    else if (kind==='resync_required') this.publish(b,kind,'sequence_gap');
  }

  publish(b,kind,reason) {
    // Overflow replaces the entire backlog with one catch-up per binding.
    // Unacknowledged loss can never silently become an empty healthy feed.
    if (this.events.length>=this.limit) {
      this.events=[];
      for (const item of this.bindings.values()) this.append(item,'resync_required','buffer_overflow');
    }
    this.append(b,kind,reason); this.notify();
  }
  append(b,kind,reason) {
    this.events.push({epoch:this.epoch,sequence:++this.sequence,provider:b.provider,owner_scope:b.owner_scope,
      mailbox_id:b.mailbox_id,binding_generation:b.binding_generation,credential_generation:this.generation,
      watch_epoch:b.watchEpoch ?? '',kind,reason,state:b.state,observed_at:new Date(this.now()).toISOString()});
  }
  notify() {for (const resolve of this.waiters) resolve(); this.waiters.clear();}

  async poll(input,signal) {
    requireExactObject(input,['profile_id','token','credential_generation','epoch']);
    this.auth(input);
    if (input.epoch!==this.epoch) throw stale();
    if (this.waiters.size) throw new ControllerError('browser_busy','mail feed already has a waiting consumer',{status:409,retryable:true});
    try {
      if (!this.events.length && !signal?.aborted) await new Promise(resolve => {
        const done=() => {clearTimeout(timer); this.waiters.delete(done); signal?.removeEventListener('abort',done); resolve();};
        const timer=setTimeout(done,this.pollMS); this.waiters.add(done); signal?.addEventListener('abort',done,{once:true});
      });
      this.auth(input); if (input.epoch!==this.epoch) throw stale();
      return {schema_version:1,epoch:this.epoch,events:this.events.slice(0,64)};
    } finally {input.token='';}
  }
  ack(input) {
    requireExactObject(input,['profile_id','token','credential_generation','epoch','sequence']);
    try {
      this.auth(input);
      if (input.epoch!==this.epoch || !Number.isSafeInteger(input.sequence) || input.sequence<0 || input.sequence>this.sequence) throw stale();
      this.acked=Math.max(this.acked,input.sequence); this.events=this.events.filter(e => e.sequence>this.acked);
      return {schema_version:1};
    } finally {input.token='';}
  }

  tick() {
    if (this.closed) return;
    if (this.now()>=this.expires) {
      this.bindings.clear(); this.token=undefined; this.notify();
      for (const work of this.work.values()) work.abort.abort();
      if (this.controller.scriptFactory?.sharedMailPages) for (const provider of providers) this.controller.scriptFactory.mailObservers?.revoke?.(provider);
    }
    for (const provider of providers) {
      if (this.work.has(provider)) continue;
      const b=this.bindings.get(provider), manager=this.controller.scriptFactory?.mailObservers;
      const slot=manager?.slots.get(provider);
      if (b && manager?.recoveries.has(provider)) continue;
      if (b && !slot && (manager?.pendingRecovery?.get(provider)?.restarts??0)>=3) continue;
      if (!b && (!slot || !this.owned.has(provider))) continue;
      if (b && slot?.identity===b.identity && slot.ready && manager.status(provider).state!=='degraded') {
        this.observe(provider,{...slot,state:manager.status(provider).state},'state'); continue;
      }
      if (b && slot?.identity===b.identity && slot.ready && manager.status(provider).state==='degraded' &&
          (slot.restarts>=3 || this.now()<(slot.retryAt??0))) {
        this.observe(provider,{...slot,state:'degraded'},'state'); continue;
      }
      if (b && this.now()<(b.retryAt??0)) continue;
      const abort=new AbortController();
      const promise=(async () => {
        if (slot) {await manager.recoveries.get(provider); await manager.stop(provider,
          {recover:Boolean(b && slot.identity===b.identity && manager.status(provider).state==='degraded')}); this.owned.delete(provider);}
        if (!b || this.bindings.get(provider)!==b || this.closed) return;
        this.owned.add(provider);
        await this.controller.runScript({profile_id:this.controller.profileID,token:this.token,credential_generation:this.generation,
          task_id:`mail-observer-${provider}`,provider,operation:'observe',script_id:`${provider}.observe`,revision:1,wait_timeout_ms:30000,
          input:{schema_version:1,action:'start',account_address:b.account_address,owner_scope:b.owner_scope}}, {signal:abort.signal});
      })().catch(() => {
        if (b && this.bindings.get(provider)===b) {b.state='degraded'; b.retryAt=this.now()+60000; this.publish(b,'watch_state','start_failed');}
      }).finally(() => this.work.delete(provider));
      this.work.set(provider,{promise,abort});
    }
  }
  async close() {
    this.closed=true; clearInterval(this.timer); this.notify(); this.bindings.clear(); this.token=undefined;
    for (const w of this.work.values()) w.abort.abort();
    await Promise.allSettled([...this.work.values()].map(w=>w.promise));
  }
}
