import test from 'node:test';
import assert from 'node:assert/strict';
import { batchConversation, scanBatchTimeline, runTimelineBatch } from '../timeline-core.mjs';

const urls = { chatgpt: 'https://chatgpt.com/c/one', claude: 'https://claude.ai/chat/one', gemini: 'https://gemini.google.com/app/one', grok: 'https://grok.com/c/one' };
for (const [provider, url] of Object.entries(urls)) {
  test(provider + ': scans virtualized pages, sorts timeline, rejects other hosts', async () => {
    let step = 0;
    const result = await scanBatchTimeline(provider, { check() {}, snapshot: async () => ({ items: [{url: step ? url.replace('one','two') : url, updated: step ? '2020-01-01' : '2021-01-01'}], end: step > 0 }), advance: async () => { step++; } });
    assert.deepEqual(result.conversations.map(c => c.id), ['two', 'one']);
    assert.equal(result.complete, false);
    assert.equal(batchConversation(provider, url.replace(new URL(url).host, 'evil.test')), null);
    assert.equal(batchConversation(provider, url.replace('https:', 'http:')), null);
  });
  test(provider + ': save failure retries; confirmed bytes skip; unknown dates recheck content', async () => {
    const item = {id:'one', url, updated:null}, ledger = {}, files = new Map();
    let fail = true, saves = 0, captures = 0, content = 'first';
    const options = { provider, timeline:{provider,conversations:[item]}, ledger, check(){},
      capture: async () => { captures++; return JSON.stringify({author:provider,url,exporter:'fixture',date:captures,messages:[{author:'ai',content}]}); },
      save: async (_, text) => { saves++; if(fail) throw new Error('disk_full'); files.set('one.json',text); return {path:'one.json',sha256:'verified'}; },
      verify: async row => files.has(row.path), commit: async () => {} };
    assert.equal((await runTimelineBatch(options)).failed.length,1);
    assert.equal(ledger.one, undefined);
    fail = false;
    assert.equal((await runTimelineBatch(options)).exported.length,1);
    assert.equal((await runTimelineBatch(options)).skipped.length,1);
    assert.equal(saves,2); assert.equal(captures,3);
    content = 'updated';
    assert.equal((await runTimelineBatch(options)).exported.length,1);
    files.clear();
    assert.equal((await runTimelineBatch(options)).exported.length,1);
  });
}
test('failed enumeration never returns a successful partial list', async () => {
  await assert.rejects(scanBatchTimeline('chatgpt',{check(){},snapshot:async()=>({items:[],blocked:true}),advance:async()=>{}}), /blocked/);
  await assert.rejects(scanBatchTimeline('chatgpt',{check(){},snapshot:async()=>({items:[],end:true}),advance:async()=>{}}), /not_found/);
});
test('cancellation stops scanning', async () => {
  await assert.rejects(scanBatchTimeline('grok',{check(){throw new Error('canceled');}}), /canceled/);
});
test('mismatched conversation never saves or checkpoints', async () => {
  await assert.rejects(runTimelineBatch({provider:'claude',timeline:{provider:'grok',conversations:[]}}), /timeline_invalid/);
  let saved = false;
  const result = await runTimelineBatch({provider:'claude',timeline:{provider:'claude',conversations:[{id:'one',url:urls.claude}]},ledger:{},check(){},capture:async()=>JSON.stringify({author:'grok',messages:[]}),save:async()=>{saved=true;},verify:async()=>true,commit:async()=>{throw new Error('should not commit');}});
  assert.equal(saved,false); assert.equal(result.failed.length,1);
});
test('checkpoint failure does not mark export complete', async () => {
  const ledger={};
  const result=await runTimelineBatch({provider:'grok',timeline:{provider:'grok',conversations:[{id:'one',url:urls.grok}]},ledger,check(){},capture:async()=>JSON.stringify({author:'grok',url:urls.grok,exporter:'fixture',messages:[{author:'user',content:'hello'}]}),save:async()=>({path:'file.json'}),verify:async()=>true,commit:async()=>{throw new Error('checkpoint disk full');}});
  assert.equal(result.failed.length,1);assert.equal(ledger.one,undefined);
});

test('delayed website hydration is not mistaken for an empty history', async () => {
  let step=0;
  const result=await scanBatchTimeline('grok',{check(){},snapshot:async()=>({end:true,items:step<10?[]:[{url:urls.grok}]}),advance:async()=>{step++;}});
  assert.equal(result.conversations.length,1);
});
