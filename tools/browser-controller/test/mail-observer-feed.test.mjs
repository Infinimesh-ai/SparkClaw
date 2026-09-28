import test from 'node:test';
import assert from 'node:assert/strict';
import {MailObserverFeed} from '../src/mail-observer-feed.mjs';

const token='observer-feed-test-token';
const auth=()=>({profile_id:'default',token,credential_generation:7});
const binding=()=>({provider:'gmail',owner_scope:'a'.repeat(64),mailbox_id:'mailbox-test',binding_generation:2,account_address:'private@example.test'});
async function fixture(t,options={}) {
  const calls=[]; const slots=new Map();
  const controller={profileID:'default',scriptFactory:{mailObservers:{slots,recoveries:new Map(),status:()=>({state:'watching'}),async stop(provider){calls.push('stop');slots.delete(provider);}}},
    async validateToken(input){calls.push('proof');if(input.token!==token)throw new Error('denied');},
    async runScript(input){calls.push('start');const b=feed.bindings.get(input.provider);slots.set(input.provider,{identity:b.identity,ready:true,state:'watching',epoch:'document-1'});}};
  const feed=new MailObserverFeed(controller,{pollMS:20,...options});t.after(()=>feed.close());
  const lease=await feed.reconcile({...auth(),bindings:[binding()]});
  await Promise.all([...feed.work.values()].map(w=>w.promise));feed.tick();
  return {feed,lease,calls,slots,controller};
}
test('native proof occurs once; metadata replays until ack and a parked reader is untouched',async t=>{
 const {feed,lease,calls,slots}=await fixture(t);
 await feed.reconcile({...auth(),bindings:[binding()]});assert.equal(calls.filter(c=>c==='proof').length,1);assert.equal(calls.filter(c=>c==='start').length,1);
 feed.observe('gmail',slots.get('gmail'),'mailbox_changed','gmail_topic_invalidation');
 const request=()=>({...auth(),epoch:lease.epoch});
 const first=await feed.poll(request());assert.ok(first.events.some(e=>e.kind==='mailbox_changed'));assert.equal(JSON.stringify(first).includes('private@'),false);
 assert.deepEqual(await feed.poll(request()),first);
 feed.ack({...request(),sequence:first.events.at(-1).sequence});assert.deepEqual((await feed.poll(request())).events,[]);
 const pending=feed.poll(request());feed.observe('gmail',slots.get('gmail'),'mailbox_changed','gmail_topic_invalidation');
 assert.equal((await pending).events[0].kind,'mailbox_changed');
 await assert.rejects(feed.poll({...request(),token:'invalid-other-token'}));
 await assert.rejects(feed.reconcile({...auth(),token:'invalid-other-token',bindings:[]}));assert.equal(feed.bindings.size,1);
});
test('overflow retains one resync for each affected binding and old epochs cannot ack',async t=>{
 const {feed,lease,slots}=await fixture(t,{limit:8});
 for(let i=0;i<40;i++)feed.observe('gmail',slots.get('gmail'),'mailbox_changed','gmail_topic_invalidation');
 assert.ok(feed.events.length<=8);assert.ok(feed.events.some(e=>e.reason==='buffer_overflow'));
 assert.throws(()=>feed.ack({...auth(),epoch:'old-controller',sequence:1}));
 assert.throws(()=>feed.ack({...auth(),epoch:lease.epoch,sequence:feed.sequence+1}));
});
test('a degraded channel produces one catch-up and a bounded state transition',async t=>{
 const {feed,slots}=await fixture(t);const slot=slots.get('gmail');slot.state='degraded';
 feed.observe('gmail',slot,'degraded','unclassified_notification');const sequence=feed.sequence;
 assert.equal(feed.events.at(-1).reason,'observer_degraded');
 feed.observe('gmail',slot,'degraded','unclassified_notification');assert.equal(feed.sequence,sequence);
});
test('intent expiration closes the owned observer without touching unmanaged test sessions',async t=>{
 let now=1000;const {feed,calls,slots}=await fixture(t,{now:()=>now,leaseMS:100});
 slots.set('outlook',{identity:'unmanaged',ready:true});now+=101;feed.tick();
 await Promise.all([...feed.work.values()].map(w=>w.promise));assert.equal(feed.bindings.size,0);assert.equal(slots.has('gmail'),false);assert.equal(slots.has('outlook'),true);
 assert.ok(calls.includes('stop'));await assert.rejects(feed.poll({...auth(),epoch:feed.epoch}));
});
test('intent renewal does not destroy a task while the resident supervisor is recovering it',async t=>{
 const {feed,slots,calls,controller}=await fixture(t);
 slots.get('gmail').ready=false;controller.scriptFactory.mailObservers.recoveries.set('gmail',Promise.resolve());
 feed.tick();await new Promise(setImmediate);assert.deepEqual(calls,['proof','start']);
});
test('rebinding rejects old document events and stop cannot be undone by an in-flight start',async t=>{
 const {feed,slots}=await fixture(t);const old={...slots.get('gmail')};
 await feed.reconcile({...auth(),bindings:[{...binding(),account_address:'new@example.test',binding_generation:3}]});
 await Promise.all([...feed.work.values()].map(w=>w.promise));const seq=feed.sequence;
 feed.observe('gmail',old,'mailbox_changed','gmail_topic_invalidation');assert.equal(feed.sequence,seq);
 await feed.reconcile({...auth(),bindings:[]});await Promise.all([...feed.work.values()].map(w=>w.promise));assert.equal(slots.has('gmail'),false);
});
