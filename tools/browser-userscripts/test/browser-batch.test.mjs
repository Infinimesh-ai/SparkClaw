import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { exportTimeline } from '../../../scripts/ai-chat/batch-export.mjs';
const playwrightPath = process.env.SPARKCLAW_TEST_PLAYWRIGHT;
const source = await fs.readFile(new URL('../revivalstack.user.js', import.meta.url), 'utf8');

test('four-platform real Chromium: userscript discovery, capture, workspace save and rerun', {skip: !playwrightPath, timeout:240000}, async (t) => {
  const {chromium} = await import(playwrightPath);
  const browser = await chromium.launch({headless:true,executablePath:process.env.SPARKCLAW_TEST_CHROMIUM,args:['--no-sandbox']});
  const workspaceRoot = await fs.mkdtemp(path.join(os.tmpdir(),'sparkclaw-batch-test-'));
  const fixtures = {
    chatgpt: {host:'chatgpt.com',prefix:'/c/',body:'<section data-testid="conversation-turn-1"><h4>You said</h4><div class="whitespace-pre-wrap">Question</div></section><section data-testid="conversation-turn-2"><h4>ChatGPT said</h4><div class="markdown"><p>Answer</p><pre><code>print(42)</code></pre></div></section>'},
    claude: {host:'claude.ai',prefix:'/chat/',body:'<div data-testid="user-message">Question</div><div class="font-claude-response"><div><div class="grid-cols-1"><p>Answer</p><pre><code>print(42)</code></pre></div></div></div>'},
    gemini: {host:'gemini.google.com',prefix:'/app/',body:'<user-query><div class="query-content">Question</div></user-query><model-response><message-content><p>Answer</p><pre><code>print(42)</code></pre></message-content></model-response>'},
    grok: {host:'grok.com',prefix:'/c/',body:'<div id="response-1" class="items-end"><div class="response-content-markdown">Question</div></div><div id="response-2"><div class="response-content-markdown"><p>Answer</p><pre><code>print(42)</code></pre></div></div>'}
  };
  try {
    for (const [provider, f] of Object.entries(fixtures)) {
      const context=await browser.newContext();
      await context.route('**/*', route=>route.fulfill({contentType:'text/html',body:`<!doctype html><title>Fixture</title><nav><a href="${f.prefix}two">Newer</a><a href="${f.prefix}one">Older</a></nav><main>${f.body}</main>`}));
      await context.addInitScript(() => { window.GM_getValue=(_,value)=>value;window.GM_setValue=()=>{};window.GM_registerMenuCommand=()=>{}; });
      await context.addInitScript({content:source});
      const original=await context.newPage(); await original.goto('https://'+f.host+'/unrelated-email-test');
      const opts={context,provider,workspaceRoot,accountScope:'fixture',timeoutMS:60000};
      const first=await exportTimeline(opts);
      t.diagnostic(provider + ' first pass: ' + JSON.stringify({exported:first.exported,failed:first.failed}));
      assert.deepEqual(first.exported,['one','two']); assert.equal(first.failed.length,0);
      assert.equal(first.status,'saved_visible_history');
      const ledger=JSON.parse(await fs.readFile(path.join(first.directory,'ledger.json'),'utf8'));
      const doc=JSON.parse(await fs.readFile(path.join(first.directory,ledger.one.path),'utf8'));
      assert.equal(doc.messages.length,2); assert.match(doc.messages[1].content,/print\(42\)/);
      const second=await exportTimeline(opts);
      assert.deepEqual(second.skipped,['one','two']);assert.equal(second.exported.length,0);
      assert.equal(context.pages().length,1);assert.match(original.url(),/unrelated-email-test$/);
      await context.close();
      t.diagnostic(provider + ' rerun and isolation passed');
    }
  } finally { await browser.close(); await fs.rm(workspaceRoot,{recursive:true,force:true}); }
});
