import assert from 'node:assert/strict';
import test from 'node:test';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import {PlaywrightCLIClientFactory} from '../src/cli-client.mjs';
import {ControllerError} from '../src/errors.mjs';

const provider = 'gmail';
const account = 'owner@example.test';
const scope = 'a'.repeat(64);
const token = 'qualification-token';
const readInput = () => ({owner_scope:scope,discovery:{provider_mode:'time_range',account_address:account}});
const watchInput = action => ({schema_version:1,action,owner_scope:scope,account_address:account});

test('mail pages share by default and retain explicit separate-page overrides',()=>{
  const name='SPARKCLAW_MAIL_SHARED_PAGE_CANDIDATE';
  const previous=process.env[name];
  try {
    delete process.env[name];
    assert.equal(new PlaywrightCLIClientFactory().sharedMailPages,true);
    assert.equal(new PlaywrightCLIClientFactory({sharedMailPages:false}).sharedMailPages,false);
    process.env[name]='0';
    assert.equal(new PlaywrightCLIClientFactory().sharedMailPages,false);
    assert.equal(new PlaywrightCLIClientFactory({sharedMailPages:true}).sharedMailPages,true);
    process.env[name]='1';
    assert.equal(new PlaywrightCLIClientFactory().sharedMailPages,true);
    assert.equal(new PlaywrightCLIClientFactory({sharedMailPages:false}).sharedMailPages,false);
  } finally {
    if (previous===undefined) delete process.env[name];
    else process.env[name]=previous;
  }
});

async function fixture(t) {
  const root = await fs.mkdtemp(path.join(os.tmpdir(),'sparkclaw-shared-page-'));
  const calls = [], tasks = [];
  const read = {provider,operation:'collect_page',sourceChecksum:'read-v1',loginURL:'https://mail.google.com/',
    origins:['https://mail.google.com'],timeoutMS:120_000,validate(){},handler:async()=>({status:'empty',failures:[]})};
  const observe = {provider,operation:'observe',sourceChecksum:'observe-v1',validate(){}};
  const registry = {entries:new Map([['gmail:collect_page',read],['gmail:observe',observe]]),
    async prepare(){},resolve({operation}){return operation==='observe'?observe:read;}};
  const factory = new PlaywrightCLIClientFactory({registry,runtimeRoot:path.join(root,'cli-runtime'),mailReadIdleMS:20,
    mailTaskFactory:options=>{
      const id=tasks.length+1;
      const client={...options,signal:options.signal,
        renewReadInvocation(registration,signal){calls.push([id,'renew']);this.registration=registration;this.signal=signal;},
        async attach(){calls.push([id,'attach']);},async createTaskPage(){assert.ok(this.mailObserverConfig);calls.push([id,'page']);},
        async navigate(){calls.push([id,'navigate']);},async prepareBackgroundPage(){calls.push([id,'background']);},
        async prepareMailRound(_account,reused){calls.push([id,reused?'reset':'prepare']);},
        async activateMailObserver(){calls.push([id,'activate']);this.signal=undefined;},
        async parkMailRound(){calls.push([id,'park']);this.signal=undefined;},
        async closeTaskPage(){calls.push([id,'close-page']);},async stop(){calls.push([id,'stop']);}};
      tasks.push(client);return client;
    }});
  await factory.prepare();
  t.after(async()=>{await factory.close();await fs.rm(root,{recursive:true,force:true});});
  const common={provider,token,credentialGeneration:1,sessionID:'session_'+'b'.repeat(32),taskID:'mail-task',
    controllerGeneration:'c'.repeat(32),sessionGeneration:1,pageGeneration:1};
  const watch=action=>factory.runScript({...common,operation:'observe',scriptID:'gmail.observe',revision:1,input:watchInput(action)});
  const round=()=>factory.runScript({...common,operation:'collect_page',scriptID:'gmail.collect_page',revision:1,input:readInput()});
  return {factory,calls,tasks,watch,round};
}

test('watch-first rounds borrow one headed page and retain its daemon past idle expiry',async t=>{
  const f=await fixture(t);
  assert.equal((await f.watch('start')).result.state,'watching');
  await f.round();await f.round();
  assert.equal(f.tasks.length,1);
  assert.equal(f.calls.filter(([,name])=>name==='attach').length,1);
  assert.equal(f.calls.filter(([,name])=>name==='navigate').length,1);
  assert.equal(f.calls.filter(([,name])=>name==='park').length,2);
  await new Promise(resolve=>setTimeout(resolve,35));
  assert.equal(f.tasks.length,1);
  assert.equal(f.factory.mailReads.slots.get(provider).watch,true);
  await f.watch('stop');
  assert.equal(f.factory.mailReads.slots.size,0);
  assert.equal(f.calls.filter(([,name])=>name==='close-page').length,1);
});

test('Reader-first page has early dormant hooks and watch activates without navigation',async t=>{
  const f=await fixture(t);
  await f.round();
  assert.equal(f.tasks.length,1);
  assert.ok(f.tasks[0].mailObserverConfig);
  assert.equal(f.factory.mailObservers.slots.size,0);
  await f.watch('start');
  assert.equal(f.tasks.length,1);
  assert.equal(f.calls.filter(([,name])=>name==='navigate').length,1);
  assert.equal(f.calls.filter(([,name])=>name==='activate').length,1);
  await f.watch('stop');
});

test('revoking a watch during a borrowed read defers one owned cleanup',async t=>{
  const f=await fixture(t);
  await f.watch('start');
  const key=f.factory.sharedMailKey({provider,ownerScope:scope,account,token,credentialGeneration:1});
  const lease=await f.factory.mailReads.take(provider,key);
  f.factory.mailObservers.revoke(provider);
  assert.equal(f.factory.mailObservers.slots.size,0);
  assert.equal(f.factory.mailReads.slots.get(provider).retiring,true);
  assert.equal(f.calls.filter(([,name])=>name==='close-page').length,0);
  assert.equal(f.factory.mailReads.keep(provider,lease),false);
  const retirement=f.factory.mailReads.retire(provider);
  assert.equal(f.calls.filter(([,name])=>name==='close-page').length,0,'borrower still owns final cleanup');
  await f.factory.mailReads.dispose(lease);
  f.factory.mailReads.discard(provider);
  await retirement;
  assert.equal(f.calls.filter(([,name])=>name==='close-page').length,1);
});

test('exclusive drain suspends the watch and a later registration creates one replacement',async t=>{
  const f=await fixture(t);
  await f.watch('start');await f.round();
  await f.factory.drainIdleMailReads();
  assert.equal(f.factory.mailObservers.slots.size,0);
  assert.equal(f.factory.mailReads.slots.size,0);
  assert.equal(f.calls.filter(([,name])=>name==='close-page').length,1);
  await f.watch('start');
  assert.equal(f.tasks.length,2);
  assert.equal(f.calls.filter(([,name])=>name==='navigate').length,2);
  await f.watch('stop');
  assert.equal(f.calls.filter(([,name])=>name==='close-page').length,2);
});

test('a replaced document retires the watched lease before a Reader rebuild',async t=>{
  const f=await fixture(t);
  await f.watch('start');
  f.tasks[0].prepareMailRound=async (_account,reused)=>{
    if(reused)throw new ControllerError('browser_page_stale','document replaced',{status:409});
  };
  await f.round();
  assert.equal(f.tasks.length,2);
  assert.equal(f.factory.mailObservers.slots.size,0);
  assert.equal(f.factory.mailReads.slots.get(provider).watch,false);
  assert.equal(f.calls.filter(([,name])=>name==='close-page').length,1);
});
