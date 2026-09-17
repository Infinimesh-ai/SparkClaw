import assert from 'node:assert/strict';
import test from 'node:test';
import {MailReadPool,MAIL_READ_IDLE_MS} from '../src/mail-read-pool.mjs';

function identity(overrides={}) {
  return {provider:'gmail',operation:'collect_page',input:{owner_scope:'a'.repeat(64),discovery:{provider_mode:'time_range',account_address:'Owner@example.test'}},credentialGeneration:7,token:'private-token',registration:{sourceChecksum:'sha256:'+'b'.repeat(64)},...overrides};
}
function fixture(options={}) {
  let now=1000;const disposed=[];
  const pool=new MailReadPool({dispose:async lease=>{disposed.push(lease);},now:()=>now,...options});
  return {pool,disposed,setNow:value=>{now=value;},lease:()=>({createdAt:now})};
}
test('mail pool identity separates every authority and excludes non-timeline operations',async()=>{
  const {pool}=fixture();const base=identity(),key=pool.identity(base);
  assert.match(key,/^[a-f0-9]{64}$/u);
  for(const change of [{provider:'qq_mail'},{credentialGeneration:8},{token:'changed'},{registration:{sourceChecksum:'changed'}},
    {input:{...base.input,owner_scope:'c'.repeat(64)}},{input:{...base.input,discovery:{...base.input.discovery,account_address:'other@example.test'}}}]) assert.notEqual(pool.identity({...base,...change}),key);
  assert.equal(pool.identity({...base,input:{...base.input,discovery:{...base.input.discovery,account_address:'owner@example.test'}}}),key);
  for(const operation of ['probe','send','read','capture','discover','mark_read']) assert.equal(pool.identity({...base,operation}),null);
  for(const change of [{provider:'unknown'},{credentialGeneration:0},{input:{...base.input,owner_scope:'bad'}},{input:{...base.input,discovery:{provider_mode:'change_cursor'}}}]) assert.equal(pool.identity({...base,...change}),null);
  assert.equal(new MailReadPool({idleMS:0,dispose:async()=>{}}).identity(base),null);
  assert.throws(()=>new MailReadPool({idleMS:MAIL_READ_IDLE_MS+1,dispose:async()=>{}}));
  assert.throws(()=>new MailReadPool({}));
});
test('one lease per provider reuses within idle limit while providers remain independent',async()=>{
  const {pool,disposed,lease}=fixture();
  assert.equal(await pool.take('gmail','key'),null);
  await assert.rejects(pool.take('gmail','key'),{code:'browser_busy'});
  assert.equal(await pool.take('outlook','other'),null);pool.discard('outlook');
  const first=lease();assert.equal(pool.keep('gmail',first),true);
  assert.equal(await pool.take('gmail','key'),first);assert.equal(pool.keep('gmail',first),true);
  assert.equal(await pool.take('gmail','changed'),null);assert.deepEqual(disposed,[first]);
  pool.discard('gmail');await pool.close();
});
test('idle expiry, maximum age and clock rollback cannot reuse old lease',async()=>{
  for(const mode of ['idle','maximum','clock']){
    const {pool,disposed,lease,setNow}=fixture();await pool.take('gmail','key');const first=lease();pool.keep('gmail',first);
    if(mode==='maximum') {first.createdAt=1000-2*60*60_000;setNow(1001);}
    else setNow(mode==='clock'?999:1000+MAIL_READ_IDLE_MS);
    assert.equal(await pool.take('gmail','key'),null);assert.deepEqual(disposed,[first]);pool.discard('gmail');await pool.close();
  }
});
test('background cleanup failure fences reads and exclusive drain until disposal succeeds',async t=>{
  t.mock.timers.enable({apis:['setTimeout']});
  let failing=true,calls=0;const diagnostics=[];
  const {pool,lease}=fixture({idleMS:10,dispose:async()=>{calls++;if(failing)throw new Error('cleanup');},diagnostic:event=>{diagnostics.push(event);return Promise.reject(new Error('telemetry'));}});
  await pool.take('gmail','key');pool.keep('gmail',lease());t.mock.timers.tick(10);
  await Promise.all([...pool.closing]);
  assert.equal(diagnostics.length,1);assert.equal(pool.slots.get('gmail').cleanupFailed,true);
  await assert.rejects(pool.take('gmail','key'),/cleanup/);
  await assert.rejects(pool.drain(),/cleanup/);assert.equal(pool.slots.size,1);
  failing=false;await pool.drain();assert.equal(pool.slots.size,0);assert.equal(calls,4);await pool.close();
});
test('drain waits for in-flight timer disposal and shutdown prevents new or returned leases',async t=>{
  t.mock.timers.enable({apis:['setTimeout']});let resolve;const pending=new Promise(done=>{resolve=done;});
  const {pool,lease}=fixture({idleMS:10,dispose:()=>pending});await pool.take('gmail','key');pool.keep('gmail',lease());t.mock.timers.tick(10);
  let closed=false;const closing=pool.close().then(()=>{closed=true;});await Promise.resolve();assert.equal(closed,false);
  await assert.rejects(pool.take('gmail','key'),{code:'browser_controller_stopping'});assert.equal(pool.keep('gmail',lease()),false);
  resolve();await closing;assert.equal(closed,true);assert.equal(pool.slots.size,0);
});
test('failed checkout cleanup retains old lease and drain attempts all idle providers',async()=>{
  const seen=[];let failing=true;
  const {pool,lease}=fixture({dispose:async value=>{seen.push(value);if(failing&&value.fail)throw new Error('cleanup');}});
  const first={...lease(),fail:true},second=lease();await pool.take('gmail','old');pool.keep('gmail',first);
  await pool.take('outlook','second');pool.keep('outlook',second);
  await assert.rejects(pool.take('gmail','new'),/cleanup/);
  assert.equal(pool.slots.get('gmail').lease,first);
  await assert.rejects(pool.drain(),/cleanup/);assert.ok(seen.includes(second));assert.equal(pool.slots.size,1);
  failing=false;await pool.close();assert.equal(pool.slots.size,0);
});
