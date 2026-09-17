import assert from 'node:assert/strict';
import test from 'node:test';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import {PlaywrightCLIClientFactory} from '../src/cli-client.mjs';
import {PlaywrightCLITask} from '../src/cli-task.mjs';
import {ControllerError} from '../src/errors.mjs';

function fixture(failures){
  const calls=[];
  const action=name=>async()=>{calls.push(name);if(failures.has(name))throw new Error(name);};
  const lease={client:{token:'private',signal:{},closeTaskPage:action('page'),stop:action('stop')},state:{reapDaemon:action('reap'),remove:action('remove')}};
  const factory=new PlaywrightCLIClientFactory();
  return {lease,calls,dispose:()=>factory.mailReads.dispose(lease)};
}
test('mail cleanup retry does not reenter CLI after successful reaping and runtime removal',async()=>{
  const {lease,calls,dispose}=fixture(new Set(['page','stop']));
  await assert.rejects(dispose(),/page/);assert.deepEqual(calls,['page','stop','reap','remove']);
  assert.equal(lease.client.token,'');assert.equal(lease.client.signal,undefined);
  await dispose();assert.equal(calls.length,4);
});
test('failed reaping preserves runtime metadata and retries only unfinished cleanup',async()=>{
  const failures=new Set(['reap']),{lease,calls,dispose}=fixture(failures);
  await assert.rejects(dispose(),/reap/);assert.deepEqual(calls,['page','stop','reap']);
  assert.equal(lease.client.token,'private');assert.equal(lease.cleanup.removed,undefined);
  failures.clear();await dispose();assert.deepEqual(calls,['page','stop','reap','reap','remove']);
});
test('runtime removal retry never reopens already-reaped CLI',async()=>{
  const failures=new Set(['remove']),{calls,dispose}=fixture(failures);
  await assert.rejects(dispose(),/remove/);failures.clear();await dispose();
  assert.deepEqual(calls,['page','stop','reap','remove','remove']);
});

function roundOptions(){return {provider:'gmail',operation:'collect_page',scriptID:'gmail.collect_page',revision:1,credentialGeneration:7,token:'private',sessionID:'session_'+'a'.repeat(32),input:{owner_scope:'a'.repeat(64),discovery:{provider_mode:'time_range',account_address:'owner@example.test'}}};}
function roundRegistry(handler){
  const registration={provider:'gmail',operation:'collect_page',timeoutMS:1000,sourceChecksum:'sha256:'+'b'.repeat(64),origins:['https://mail.google.com'],loginURL:'https://mail.google.com/mail/u/0/',validate(){},handler};
  return {registration,prepare:async()=>{},resolve:()=>registration};
}
async function warmFailureFixture(mode){
  let failing=true,handlerCalls=0;const calls=[],abort=new AbortController();
  const error=()=>new ControllerError('browser_extension_unavailable','test cleanup unavailable',{status:503,retryable:true});
  const registry=roundRegistry(async()=>{handlerCalls++;if(mode==='handler')throw error();if(mode==='cancel')abort.abort();return {status:'empty',failures:[]};});
  const factory=new PlaywrightCLIClientFactory({registry});const options={...roundOptions(),signal:abort.signal};
  const lease={createdAt:Date.now(),client:{token:'private',renewReadInvocation(){},async prepareMailRound(){if(mode==='stale')throw new ControllerError('browser_page_stale','stale');},async parkMailRound(){if(mode==='park')throw error();if(mode==='abort_after_park')abort.abort();},async closeTaskPage(){calls.push('page');},async stop(){calls.push('stop');}},
    state:{async reapDaemon(){calls.push('reap');if(failing)throw error();},async remove(){calls.push('remove');}}};
  const key=factory.mailReads.identity({...options,registration:registry.registration});await factory.mailReads.take('gmail',key);factory.mailReads.keep('gmail',lease);
  return {factory,options,lease,calls,handlerCalls:()=>handlerCalls,recover:()=>{failing=false;}};
}
test('pooled handler, cancellation, park and stale-rebuild cleanup failures retain fenced metadata',async()=>{
  for(const mode of ['handler','cancel','park','stale']){
    const h=await warmFailureFixture(mode);
    await assert.rejects(h.factory.runScript(h.options),{code:'browser_extension_unavailable'});
    assert.equal(h.calls.includes('remove'),false,mode);
    assert.equal(h.factory.mailReads.slots.get('gmail').cleanupFailed,true,mode);
    assert.equal(h.factory.mailReads.slots.get('gmail').lease,h.lease,mode);
    assert.equal(h.handlerCalls(),mode==='stale'?0:1,mode);
    await assert.rejects(h.factory.drainIdleMailReads(),{code:'browser_extension_unavailable'});
    h.recover();await h.factory.drainIdleMailReads();assert.equal(h.calls.at(-1),'remove');assert.equal(h.factory.mailReads.slots.size,0);
    assert.equal(h.calls.filter(x=>x==='page').length,1);assert.equal(h.calls.filter(x=>x==='stop').length,1);await h.factory.close();
  }
});
test('abort occurring during parking never returns the lease to idle pool',async()=>{
  const h=await warmFailureFixture('abort_after_park');h.recover();
  const result=await h.factory.runScript(h.options);assert.equal(result.state,'completed');
  assert.equal(h.options.signal.aborted,true);assert.equal(h.factory.mailReads.slots.size,0);
  assert.deepEqual(h.calls,['page','stop','reap','remove']);await h.factory.close();
});
test('cold partial initialization cleanup failure preserves its runtime until reaping succeeds',async t=>{
  const root=await fs.mkdtemp(path.join(os.tmpdir(),'sparkclaw-pool-cleanup-'));
  let failing=true,removeCalls=0;const registry=roundRegistry(async()=>{throw new Error('handler must not run');});
  const factory=new PlaywrightCLIClientFactory({registry,runtimeRoot:path.join(root,'cli-runtime')});
  t.after(async()=>{failing=false;await factory.close();await fs.rm(root,{recursive:true,force:true});});
  await factory.prepare();
  t.mock.method(PlaywrightCLITask.prototype,'attach',async function(){
    const reap=this.state.reapDaemon.bind(this.state);
    this.state.reapDaemon=async()=>{if(failing)throw new ControllerError('browser_extension_unavailable','reap failed');await reap();};
    const remove=this.state.remove.bind(this.state);this.state.remove=async()=>{removeCalls++;await remove();};
    throw new ControllerError('browser_extension_unavailable','attach failed');
  });
  t.mock.method(PlaywrightCLITask.prototype,'closeTaskPage',async()=>{});
  t.mock.method(PlaywrightCLITask.prototype,'stop',async()=>{});
  await assert.rejects(factory.runScript(roundOptions()),{code:'browser_extension_unavailable'});
  assert.equal(removeCalls,0);assert.equal((await fs.readdir(factory.runtimeRoot)).length,1);
  assert.equal(factory.mailReads.slots.get('gmail').cleanupFailed,true);
  failing=false;await factory.drainIdleMailReads();assert.equal(removeCalls,1);assert.deepEqual(await fs.readdir(factory.runtimeRoot),[]);
});
