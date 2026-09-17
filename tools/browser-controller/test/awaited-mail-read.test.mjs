import assert from 'node:assert/strict';
import test from 'node:test';
import hook from '../src/awaited-mail-read.cjs';

test('only task-gated marked mail reads skip ambient completion, while still awaiting the callback', async()=>{
  const original=process.env.SPARKCLAW_AWAITED_MAIL_READ;
  try {
    for(const enabled of ['0','1'])for(const marked of [false,true])for(const file of [false,true]){
      process.env.SPARKCLAW_AWAITED_MAIL_READ=enabled;
      let ambient=0,completed=false;
      const tab={waitForCompletion:async callback=>{ambient++;return callback();}};
      const params={code:(marked?hook.MARKER:'')+'async()=>true',...(file?{filename:'fixture'}:{})};
      const result=await hook.waitForMailRead(tab,params,async()=>{await new Promise(resolve=>setImmediate(resolve));completed=true;return 42;});
      assert.equal(completed,true);assert.equal(result,42);
      assert.equal(ambient,enabled==='1'&&marked&&!file?0:1);
    }
  } finally {if(original===undefined)delete process.env.SPARKCLAW_AWAITED_MAIL_READ;else process.env.SPARKCLAW_AWAITED_MAIL_READ=original;}
});

test('awaited read preserves callback failure; legacy callbacks keep ambient policy',async()=>{
  const original=process.env.SPARKCLAW_AWAITED_MAIL_READ;
  try {
    process.env.SPARKCLAW_AWAITED_MAIL_READ='1';let legacy=0;
    const tab={waitForCompletion:async callback=>{legacy++;return callback();}};
    await assert.rejects(hook.waitForMailRead(tab,{code:hook.MARKER},async()=>{throw new Error('native failure');}),/native failure/);
    assert.equal(legacy,0);
    await assert.rejects(hook.waitForMailRead(tab,{code:'unmarked'},async()=>{throw new Error('legacy failure');}),/legacy failure/);
    assert.equal(legacy,1);
  } finally {if(original===undefined)delete process.env.SPARKCLAW_AWAITED_MAIL_READ;else process.env.SPARKCLAW_AWAITED_MAIL_READ=original;}
});
