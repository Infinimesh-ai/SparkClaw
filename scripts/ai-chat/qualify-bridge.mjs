#!/usr/bin/env node
// Qualification-only adapter for SparkClaw's task-scoped native Browser Bridge.
// Uses the caller's existing browser-control credential; never reads site tokens.
import fs from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import crypto from 'node:crypto';
import { execFile } from 'node:child_process';
import { promisify, parseArgs } from 'node:util';
import { fileURLToPath } from 'node:url';
import { exportTimeline } from './batch-export.mjs';

const {values} = parseArgs({ options: { providers:{type:'string',default:'chatgpt,claude,gemini,grok'}, workspace:{type:'string'}, account:{type:'string'}, help:{type:'boolean'} } });
if (values.help) {
  console.log('Usage: node scripts/ai-chat/qualify-bridge.mjs --workspace /absolute/private/workspace --account account-workspace-label [--providers chatgpt,claude,gemini,grok]\nRequires PLAYWRIGHT_MCP_EXTENSION_TOKEN, PLAYWRIGHT_MCP_EXECUTABLE_PATH (Bridge launcher), PLAYWRIGHT_MCP_USER_DATA_DIR; optional SPARKCLAW_PLAYWRIGHT_CLI_ENTRY. Loads the local candidate into owned test pages, samples at most two conversations per provider, then closes only the test session. No managed-script deployment.');
} else {
  await qualify();
}
async function qualify() {
  const providers = values.providers.split(',');
  if (!providers.length || providers.some(p=>!['chatgpt','claude','gemini','grok'].includes(p)) || new Set(providers).size!==providers.length || !values.workspace || !path.isAbsolute(values.workspace) || !values.account?.trim()) throw new Error('qualification_arguments_invalid');
  const token=process.env.PLAYWRIGHT_MCP_EXTENSION_TOKEN;
  if (!token || !path.isAbsolute(process.env.PLAYWRIGHT_MCP_EXECUTABLE_PATH || '') || !path.isAbsolute(process.env.PLAYWRIGHT_MCP_USER_DATA_DIR || '')) throw new Error('configured_bridge_environment_required');
  const cliEntry=process.env.SPARKCLAW_PLAYWRIGHT_CLI_ENTRY || fileURLToPath(new URL('../../tools/browser-controller/node_modules/@playwright/cli/playwright-cli.js',import.meta.url));
  await fs.access(cliEntry);
  const workspaceRoot=await fs.realpath(values.workspace);
  const temporary=await fs.mkdtemp(path.join(os.tmpdir(),'sparkclaw-ai-qualification-'));
  const session='sc-ai-qualification-'+crypto.randomUUID();
  const exec=promisify(execFile);
  const env={...process.env,PLAYWRIGHT_MCP_SNAPSHOT_MODE:'none',PLAYWRIGHT_MCP_CODEGEN:'none',PLAYWRIGHT_MCP_OUTPUT_DIR:temporary};
  let attached=false, sequence=0;
  async function cli(args) {
    let stdout;
    try { ({stdout}=await exec(process.execPath,[cliEntry,'--json','-s='+session,...args],{cwd:temporary,env,timeout:150000,maxBuffer:16<<20})); }
    catch { throw new Error('bridge_command_failed'); }
    const raw=JSON.parse(stdout);
    if(raw.isError) throw new Error(String(raw.error || 'bridge_command_failed').replaceAll(token,'[redacted]').slice(0,200));
    let value=raw.result;
    if(typeof value==='string'){try{return JSON.parse(value);}catch{}}
    return value;
  }
  async function command(body) {
    const filename=path.join(temporary,'command-'+(++sequence)+'.js');
    await fs.writeFile(filename,'async page => {const p=page.context().pages().at(-1); '+body+'}',{mode:0o600});
    return cli(['run-code','--filename',filename]);
  }
  const source=await fs.readFile(new URL('../../tools/browser-userscripts/revivalstack.user.js',import.meta.url),'utf8');
  if (source.split('UIManager.init();').length!==2) throw new Error('candidate_bootstrap_changed');
  // Candidate extraction and batching are unchanged. Qualification gives the
  // temporary candidate a separate hidden bridge from the installed script.
  const injection='(function(){const GM_getValue=(_,v)=>v;const GM_setValue=()=>{};const GM_registerMenuCommand=()=>{};\n'+source.replaceAll('sparkclaw-ai-export-bridge','sparkclaw-live-ai-export-bridge').replace('UIManager.init();','UIManager.installAutomationBridge();')+'\n})();';
  const summary={schema:'sparkclaw.ai-chat.live-qualification.v1',candidate_sha256:crypto.createHash('sha256').update(source).digest('hex'),injection_sha256:crypto.createHash('sha256').update(injection).digest('hex'),sample_limit:2,temporary_injection:true,providers:[]};
  const reportPath=path.join(workspaceRoot,'live-qualification-'+crypto.randomUUID()+'.json');
  try {
    await cli(['attach','--extension=chromium']);attached=true;
    for(const provider of providers) {
      let discovered=0, sourceURL='', lastOperation='';
      const context={newPage:async()=>{
        await command('await page.context().newPage();return true;');
        return {
          goto:async url=>{
            const meta=await command(`await p.goto(${JSON.stringify(url)},{waitUntil:'domcontentloaded',timeout:45000});return {url:p.url(),index:page.context().pages().indexOf(p)};`);
            sourceURL=meta.url;
            await cli(['tab-select',String(meta.index)]);
            // Exact existing Bridge marker enables background rendering without
            // activating an owner tab or moving the user's browser window.
            await cli(['eval','() => "sparkclaw-browser-bridge-background-input-v1"']);
            await command(`await p.evaluate(${JSON.stringify(injection)});return true;`);
          },
          url:()=>sourceURL,
          locator:selector=>({
            waitFor:async options=>command(`await p.locator(${JSON.stringify(selector)}).waitFor(${JSON.stringify(options)});return true;`),
            evaluate:async(fn,arg)=>{lastOperation=arg;return command(`return await p.locator(${JSON.stringify(selector)}).evaluate(${fn.toString()},${JSON.stringify(arg)});`);},
            getAttribute:async name=>command(`return await p.locator(${JSON.stringify(selector)}).getAttribute(${JSON.stringify(name)});`),
            innerText:async()=>command(`return await p.locator(${JSON.stringify(selector)}).innerText();`),
            textContent:async()=>{
              let text=await command(`return await p.locator(${JSON.stringify(selector)}).textContent();`);
              if(typeof text!=='string') throw new Error('script_output_not_text');
              if(lastOperation==='timeline.scan') {const index=JSON.parse(text);discovered=index.conversations.length;index.conversations=index.conversations.slice(0,2);text=JSON.stringify(index);}
              return text;
            }
          }),
          waitForFunction:async(fn,arg,options)=>command(`await p.waitForFunction(${fn.toString()},${JSON.stringify(arg)},${JSON.stringify({...options,timeout:90000})});return true;`),
          close:async()=>command('await p.close();return true;')
        };
      }};
      console.log(JSON.stringify({provider,phase:'begin'}));
      try {
        const receipt=await exportTimeline({context,provider,workspaceRoot,accountScope:values.account,timeoutMS:240000,bridgeID:'sparkclaw-live-ai-export-bridge'});
        const result={provider,status:receipt.status,discovered,sampled:receipt.timeline.conversations.length,exported:receipt.exported.length,skipped:receipt.skipped.length,failed:receipt.failed.length,manifest:receipt.manifest_path};
        summary.providers.push(result);console.log(JSON.stringify(result));
        if(receipt.failed.length)process.exitCode=1;
      } catch(error) {const result={provider,status:'failed',error:error.message,manifest:error.manifest_path};summary.providers.push(result);console.log(JSON.stringify(result));process.exitCode=1;}
      await fs.writeFile(reportPath,JSON.stringify(summary,null,2),{mode:0o600});
    }
  } finally {
    if(attached) {await cli(['tab-close','0']).catch(()=>{});await cli(['detach']).catch(()=>{});}
    await fs.rm(temporary,{recursive:true,force:true});
  }
  console.log(JSON.stringify({report:reportPath}));
}
