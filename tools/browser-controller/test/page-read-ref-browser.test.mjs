import assert from 'node:assert/strict';
import {spawn} from 'node:child_process';
import fs from 'node:fs/promises';
import http from 'node:http';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import {chromium} from 'playwright';
import {PlaywrightMCPClientFactory} from '../src/mcp-client.mjs';

const enabled = process.env.SPARKCLAW_MAIL_BROWSER_TEST === '1';
function find(snapshot, name) {
  for (const node of snapshot) {
    if (node?.name === name && node.ref) return node.ref;
    const child = node?.children && find(node.children, name);
    if (child) return child;
  }
}

test('real MCP/Chromium subtree reads require a fresh observed ref on the owned page', {skip:!enabled, timeout:30000}, async t => {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(),'page-read-ref-'));
  const html = `<!doctype html><html><head><title>Ref read fixture</title></head><body>
    <div class="${'oversized-interface-class '.repeat(6000)}">Outside subtree</div>
    <section role="group" aria-label="Owned composer" data-owner="expected">
      <input type="file" id="owned-file" multiple aria-controls="rows">
      <div contenteditable="true" role="textbox" aria-label="Message body">Synthetic body</div>
      <div id="rows"></div><button type="button">Fixture button</button>
    </section>
    <button type="button" onclick="setTimeout(()=>document.querySelector('section').remove(),2500)">Detach target later</button>
    </body></html>`;
  const server = http.createServer((req,res) => {res.setHeader('content-type','text/html');res.end(html);});
  await new Promise(resolve => server.listen(0,'127.0.0.1',resolve));
  t.after(async () => {
    await new Promise(resolve => server.close(resolve));
    await fs.rm(dir,{recursive:true,force:true});
  });
  const probe = await chromium.launchServer({headless:true});
  const executablePath = probe.process().spawnfile;
  await probe.close();
  const factory = new PlaywrightMCPClientFactory({
    executablePath, userDataDir:path.join(dir,'profile'),
    outputRoot:path.join(dir,'mcp-output'), connectTimeoutMS:10000,
    // Only the isolated synthetic fixture replaces Extension transport with a
    // private headless browser. The actual MCP, ref checks and read code run.
    spawn:(command,args,options) => {
      const env={...options.env};delete env.PLAYWRIGHT_MCP_EXTENSION_TOKEN;
      return spawn(command,[...args.filter(arg => arg !== '--extension'),'--headless'],{...options,env});
    },
  });
  let client;
  try {
    client = await factory.open({token:'synthetic-fixture-token',sessionID:'read-ref-fixture'});
    await client.createTaskPage();
    await client.execute('page.navigate',{url:`http://127.0.0.1:${server.address().port}/`});
    const whole = await client.execute('page.read',{});
    assert.equal(whole.page.html.length,120000);
    assert.equal(whole.page.html.includes('owned-file'),false);
    const snapshot = await client.execute('page.snapshot',{depth:64});
    const ref = find(snapshot.snapshot,'Owned composer');
    assert.ok(ref);
    const read = await client.execute('page.read',{ref});
    assert.ok(read.page.html.startsWith('<section'));
    assert.ok(read.page.html.includes('id="owned-file"'));
    assert.ok(read.page.html.includes('data-owner="expected"'));
    assert.equal(read.page.html.includes('oversized-interface-class'),false);
    assert.equal(read.page.text.includes('Outside subtree'),false);
    const bounded = await client.execute('page.read',{ref,max_chars:40});
    assert.equal(bounded.page.html.length,40);
    assert.equal((await client.execute('page.read',{ref})).page.html,read.page.html,'read leaves live DOM unchanged');
    for (const args of [{ref:'e999999'},{ref,page_id:'page_999'}]) {
      await assert.rejects(client.execute('page.read',args),error => error.code === 'browser_page_stale');
    }
    for (const args of [{ref:'#owned-file'},{ref:null},{ref,ancestor_depth:1},{ref,max_chars:120001}]) {
      await assert.rejects(client.execute('page.read',args),error => error.code === 'invalid_request');
    }
    await client.execute('tabs.new',{});
    await assert.rejects(client.execute('page.read',{ref,page_id:'page_2'}),error => error.code === 'browser_page_stale');
    await assert.rejects(client.execute('page.read',{ref,page_id:'page_1'}),error => error.code === 'browser_page_stale');
    const fresh = await client.execute('page.snapshot',{page_id:'page_1'});
    await client.execute('page.click',{page_id:'page_1',ref:find(fresh.snapshot,'Detach target later')});
    const detaching = await client.execute('page.snapshot',{page_id:'page_1'});
    const detachedRef = find(detaching.snapshot,'Owned composer');
    assert.ok(detachedRef);
    await client.execute('page.wait',{page_id:'page_1',duration_ms:3000});
    await assert.rejects(client.execute('page.read',{page_id:'page_1',ref:detachedRef}),error => error.code === 'browser_page_stale');
  } finally {
    await client?.close();
  }
});
