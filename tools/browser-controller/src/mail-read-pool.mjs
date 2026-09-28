import crypto from 'node:crypto';
import {ControllerError} from './errors.mjs';

export const MAIL_READ_IDLE_MS = 30 * 60_000;

// At most one owned, idle read page per provider. A lease never contains caller
// input, an active HTTP reservation, or a send capability. No durable pool cache.
export class MailReadPool {
  constructor({idleMS = MAIL_READ_IDLE_MS, dispose, diagnostic = () => {}, now = Date.now}) {
    if (!Number.isSafeInteger(idleMS) || idleMS < 0 || idleMS > MAIL_READ_IDLE_MS) throw new TypeError('invalid mail read idle bound');
    if (typeof dispose !== 'function') throw new TypeError('mail read dispose is required');
    this.idleMS = idleMS;
    this.dispose = dispose;
    this.diagnostic = diagnostic;
    this.now = now;
    this.slots = new Map();
    this.closing = new Set();
    this.closed = false;
  }

  identity({provider, operation, input, credentialGeneration, token, registration, observerChecksum = ''}) {
    if (operation !== 'collect_page' || !['qq_mail','gmail','outlook'].includes(provider) ||
        input?.discovery?.provider_mode !== 'time_range' || !/^[a-f0-9]{64}$/u.test(input?.owner_scope ?? '') ||
        typeof input.discovery.account_address !== 'string' || !input.discovery.account_address ||
        !Number.isSafeInteger(credentialGeneration) || credentialGeneration < 1) return null;
    return crypto.createHash('sha256').update(JSON.stringify([provider,input.owner_scope,
      input.discovery.account_address.toLowerCase(),credentialGeneration,token,registration.sourceChecksum,observerChecksum])).digest('hex');
  }

  async take(provider, key) {
    if (this.closed) throw new ControllerError('browser_controller_stopping','browser controller is stopping',{status:503,retryable:true});
    const prior = this.slots.get(provider);
    if (prior?.busy) throw new ControllerError('browser_busy','browser session is busy',{status:409,retryable:true});
    if (prior?.retiring) {
      await this.retire(provider);
      return this.take(provider,key);
    }
    if (prior?.watch && prior.key !== key) throw new ControllerError('browser_page_stale','mail observer binding does not match',{status:409});
    clearTimeout(prior?.timer);
    const slot = {key,busy:true,lease:null,watch:prior?.watch ?? false};
    this.slots.set(provider,slot);
    try {
      const now=this.now();
      if (prior?.lease && !prior.cleanupFailed && prior.key === key && (prior.watch || now>=prior.usedAt && now-prior.usedAt < this.idleMS)) {
        slot.lease = prior.lease;
        return slot.lease;
      }
      if (prior?.lease) await this.dispose(prior.lease);
      return null;
    } catch (error) {
      // Failed disposal must fence subsequent reads and exclusive mutations
      // until cleanup succeeds; never forget an owned process/page silently.
      if(prior?.lease){prior.busy=false;prior.cleanupFailed=true;this.slots.set(provider,prior);}
      else this.slots.delete(provider);
      throw error;
    }
  }

  keep(provider, lease, {watch = false} = {}) {
    const slot = this.slots.get(provider);
    if (this.closed || !slot?.busy || slot.retiring || !this.idleMS && !slot.watch && !watch) return false;
    slot.lease = lease;
    slot.busy = false;
    slot.watch ||= watch;
    slot.usedAt = this.now();
    if (slot.watch) return true;
    slot.timer = setTimeout(() => {
      if (this.slots.get(provider) !== slot || slot.busy) return;
      slot.busy = true;
      let cleanupFailed=false;
      const closing = Promise.resolve().then(()=>this.dispose(lease)).catch(() => {
        cleanupFailed=true;
        try {Promise.resolve(this.diagnostic({event:'browser_mail_pool_cleanup_failed',provider})).catch(()=>{});} catch {}
      }).finally(() => {
        if (this.slots.get(provider) === slot) {
          if(cleanupFailed){slot.busy=false;slot.cleanupFailed=true;}
          else this.slots.delete(provider);
        }
        this.closing.delete(closing);
      });
      this.closing.add(closing);
    },this.idleMS);
    slot.timer.unref?.();
    return true;
  }

  discard(provider) {
    const slot = this.slots.get(provider);
    clearTimeout(slot?.timer);
    this.slots.delete(provider);
    slot?.finishRetirement?.resolve();
  }

  // A revoked watch stops admitting callbacks immediately. If a Reader is
  // borrowing the page, its normal finalizer observes `retiring` and performs
  // the single owned cleanup after the borrower quiesces.
  async retire(provider) {
    const slot = this.slots.get(provider);
    if (!slot) return;
    clearTimeout(slot.timer);
    slot.watch = false;
    slot.retiring = true;
    if (slot.retirement) return slot.retirement;
    if (slot.busy) {
      slot.retirement = new Promise((resolve,reject) => {slot.finishRetirement={resolve,reject};});
      return slot.retirement;
    }
    if (!slot.retirement) {
      slot.busy = true;
      slot.retirement = Promise.resolve().then(async () => {
        try {
          if (slot.lease) await this.dispose(slot.lease);
          if (this.slots.get(provider) === slot) this.slots.delete(provider);
        } catch (error) {
          slot.cleanupFailed = true;
          throw error;
        } finally {
          slot.busy = false;
          this.closing.delete(slot.retirement);
          slot.retirement = null;
        }
      });
      this.closing.add(slot.retirement);
    }
    return slot.retirement;
  }

  fenceFailedCleanup(provider, lease) {
    const slot = this.slots.get(provider);
    if (!slot?.busy) throw new ControllerError('browser_busy','browser session is busy',{status:409,retryable:true});
    clearTimeout(slot.timer);
    slot.lease=lease;
    slot.cleanupFailed=true;
    slot.busy=false;
    slot.finishRetirement?.reject(new ControllerError('browser_extension_unavailable','mail page cleanup failed',{status:503,retryable:true}));
    slot.finishRetirement=undefined;
    slot.retirement=null;
  }

  async drain() {
    await Promise.all([...this.closing]);
    let failure;
    for (const [provider,slot] of this.slots) {
      if (slot.busy) throw new ControllerError('browser_busy','browser session is busy',{status:409,retryable:true});
      clearTimeout(slot.timer);
      slot.busy = true;
      try {if(slot.lease) await this.dispose(slot.lease);this.slots.delete(provider);}
      catch(error) {failure ??= error;slot.busy=false;slot.cleanupFailed=true;}
    }
    if (failure) throw failure;
  }

  async close() {
    this.closed = true;
    await this.drain();
  }
}
