import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import {connectBatchBridge} from '../../../scripts/ai-chat/bridge-context.mjs';
import {exportTimeline} from '../../../scripts/ai-chat/batch-export.mjs';

test('production Bridge: unlimited export, checkpoint reuse and fixed Page ownership despite popups or target loss', async()=>{
  const root=await fs.mkdtemp(path.join(os.tmpdir(),'sc-bridge-contract-'));
  const entry=path.join(root,'cli.mjs'),statePath=path.join(root,'state.json');
  await fs.writeFile(entry,`import fs from 'node:fs/promises';
const statePath=${JSON.stringify(statePath)};
let state={pages:[{id:'connection',url:'about:blank',tags:[]}],closed:0,next:0};try{state=JSON.parse(await fs.readFile(statePath,'utf8'));}catch{}
const args=process.argv.slice(2);let result=true,error;
const objects=new Map();
const context={pages:()=>state.pages.filter(row=>!row.closed).map(object),newPage:async()=>{
 const row={id:'target-'+(++state.next),url:'about:blank',tags:[]};state.pages.push(row);
 // A site creates an additional page after the batch page has been created.
 state.pages.push({id:'popup-'+state.next,url:'about:blank',tags:[]});return object(row);
}};
function object(row){
 if(objects.has(row.id))return objects.get(row.id);
 const p={context:()=>context,isClosed:()=>!!row.closed,url:()=>row.url,goto:async url=>{row.url=url;},close:async()=>{row.closed=true;state.closed++;},
 locator:()=>({waitFor:async()=>{},evaluate:async(_,operation)=>{row.operation=operation;},getAttribute:async()=>'ready',innerText:async()=>'ready',textContent:async()=>JSON.stringify(row.operation==='timeline.scan'?{provider:'chatgpt',coverage:'visible-history',conversations:['one','two','three'].map(id=>({id,url:'https://chatgpt.com/c/'+id,updated:null})),collections:[]}:{author:'chatgpt',url:row.url,title:'Fixture',exporter:'installed-fixture',messages:[{author:'user',content:'Question'},{author:'ai',content:'Answer'}]})}),
 waitForFunction:async(fn,arg,options)=>{state.waitTimeout=options.timeout;}};
 for(const tag of row.tags)Object.defineProperty(p,tag,{value:true});objects.set(row.id,p);return p;
}
try{
 if(args.includes('run-code')){const code=await fs.readFile(args[args.indexOf('--filename')+1],'utf8');if(code.includes('UIManager')||code.includes('GM_getValue'))throw new Error('production_must_not_inject');result=await (0,eval)('('+code+')')(context.pages().at(-1));}
}catch(e){error=e.message;}
for(const row of state.pages){const p=objects.get(row.id);if(p)row.tags=Object.getOwnPropertyNames(p).filter(k=>k.startsWith('sparkclaw'));}
await fs.writeFile(statePath,JSON.stringify(state));console.log(JSON.stringify(error?{isError:true,error}:{result:JSON.stringify(result)}));`);
  const before={...process.env};
  Object.assign(process.env,{PLAYWRIGHT_MCP_EXTENSION_TOKEN:'fixture-private-token',PLAYWRIGHT_MCP_EXECUTABLE_PATH:'/fixture/launcher',PLAYWRIGHT_MCP_USER_DATA_DIR:'/fixture/profile',SPARKCLAW_PLAYWRIGHT_CLI_ENTRY:entry});
  try{
    const connection=await connectBatchBridge();
    try{
      const opts={context:connection.context,provider:'chatgpt',workspaceRoot:root,accountScope:'fixture'};
      const first=await exportTimeline(opts);assert.deepEqual(first.exported,['one','two','three']);
      const second=await exportTimeline(opts);assert.deepEqual(second.skipped,['one','two','three']);
      const page=await connection.context.newPage();await page.close();await page.close();
      let state=JSON.parse(await fs.readFile(statePath,'utf8'));
      assert.equal(state.closed,3);
      assert.ok(state.waitTimeout>90000,'production timeout must not be truncated to qualification timeout');
      const lost=await connection.context.newPage();
      state=JSON.parse(await fs.readFile(statePath,'utf8'));
      state.pages.find(row=>row.id==='target-4').closed=true;await fs.writeFile(statePath,JSON.stringify(state));
      await assert.rejects(lost.goto('https://chatgpt.com/c/unintended'),/batch_page_lost/);
      await assert.rejects(lost.close(),/batch_page_lost/);
      state=JSON.parse(await fs.readFile(statePath,'utf8'));
      assert.equal(state.closed,3,'lost target must not close a replacement page');
      for(const row of state.pages.filter(row=>row.id.startsWith('popup-'))){assert.equal(row.url,'about:blank');assert.equal(row.closed,undefined);}
    }finally{await connection.close();}
    const state=JSON.parse(await fs.readFile(statePath,'utf8'));
    assert.equal(state.pages[0].closed,true,'connection cleanup binds its original Page too');
    assert.ok(state.pages.filter(row=>row.id.startsWith('popup-')).every(row=>!row.closed));
  }finally{
    for(const key of Object.keys(process.env))if(!(key in before))delete process.env[key];Object.assign(process.env,before);
    await fs.rm(root,{recursive:true,force:true});
  }
});
