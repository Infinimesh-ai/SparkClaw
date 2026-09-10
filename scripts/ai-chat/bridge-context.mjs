// Production adapter for SparkClaw's task-scoped native Browser Bridge.
// Uses the caller's existing browser-control credential; never reads site tokens.
import fs from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import crypto from 'node:crypto';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { fileURLToPath } from 'node:url';

export async function connectBatchBridge() {
  const token=process.env.PLAYWRIGHT_MCP_EXTENSION_TOKEN;
  if (!token || !path.isAbsolute(process.env.PLAYWRIGHT_MCP_EXECUTABLE_PATH || '') || !path.isAbsolute(process.env.PLAYWRIGHT_MCP_USER_DATA_DIR || '')) throw new Error('configured_bridge_environment_required');
  const cliEntry=process.env.SPARKCLAW_PLAYWRIGHT_CLI_ENTRY || fileURLToPath(new URL('../../tools/browser-controller/node_modules/@playwright/cli/playwright-cli.js',import.meta.url));
  await fs.access(cliEntry);
  const temporary=await fs.mkdtemp(path.join(os.tmpdir(),'sparkclaw-ai-batch-'));
  const session='sc-ai-batch-'+crypto.randomUUID();
  const connectionKey='sparkclawConnection'+crypto.randomUUID();
  const exec=promisify(execFile);
  const env={...process.env,PLAYWRIGHT_MCP_SNAPSHOT_MODE:'none',PLAYWRIGHT_MCP_CODEGEN:'none',PLAYWRIGHT_MCP_OUTPUT_DIR:temporary};
  let attached=false, sequence=0;
  async function cli(args, timeout=150000) {
    let stdout;
    try { ({stdout}=await exec(process.execPath,[cliEntry,'--json','-s='+session,...args],{cwd:temporary,env,timeout,maxBuffer:16<<20})); }
    catch { throw new Error('bridge_command_failed'); }
    const raw=JSON.parse(stdout);
    if(raw.isError) throw new Error(String(raw.error || 'bridge_command_failed').replaceAll(token,'[redacted]').slice(0,200));
    let value=raw.result;
    if(typeof value==='string'){try{return JSON.parse(value);}catch{}}
    return value;
  }
  async function runCode(body, timeout) {
    const filename=path.join(temporary,'command-'+(++sequence)+'.js');
    await fs.writeFile(filename,'async page => {'+body+'}',{mode:0o600});
    return cli(['run-code','--filename',filename],timeout);
  }
  try {
    await cli(['attach','--extension=chromium']); attached=true;
    // Tag the host-side Playwright object, never website DOM/storage. These
    // objects persist in the CLI's MCP session across run-code invocations.
    await runCode(`Object.defineProperty(page,${JSON.stringify(connectionKey)},{value:true});return true;`);
  } catch(error) {
    if(attached)await cli(['detach']).catch(()=>{});
    await fs.rm(temporary,{recursive:true,force:true});throw error;
  }
  const context={newPage:async()=>{
    const pageKey='sparkclawBatchPage'+crypto.randomUUID();
    await runCode(`const owned=await page.context().newPage();Object.defineProperty(owned,${JSON.stringify(pageKey)},{value:true});return true;`);
    let closed=false, sourceURL='';
    const command=(body,timeout)=>{
      if(closed)throw new Error('batch_page_closed');
      return runCode(`const p=page.context().pages().find(candidate=>candidate[${JSON.stringify(pageKey)}]===true);if(!p||p.isClosed())throw new Error('batch_page_lost');`+body,timeout);
    };
    return {
      goto:async(url,options={})=>{
        const meta=await command(`await p.goto(${JSON.stringify(url)},${JSON.stringify({...options,waitUntil:'domcontentloaded'})});return {url:p.url(),index:page.context().pages().indexOf(p)};`);
        sourceURL=meta.url;
        await cli(['tab-select',String(meta.index)]);
        // Existing marker enables rendering in the selected task page.
        await cli(['eval','() => "sparkclaw-browser-bridge-background-input-v1"']);
      },
      url:()=>sourceURL,
      locator:selector=>({
        waitFor:options=>command(`await p.locator(${JSON.stringify(selector)}).waitFor(${JSON.stringify(options)});return true;`),
        evaluate:fn=>command(`return await p.locator(${JSON.stringify(selector)}).evaluate(${fn.toString()});`),
        getAttribute:name=>command(`return await p.locator(${JSON.stringify(selector)}).getAttribute(${JSON.stringify(name)});`),
        innerText:()=>command(`return await p.locator(${JSON.stringify(selector)}).innerText();`),
        inputValue:async()=>{
          const value=await command(`return await p.locator(${JSON.stringify(selector)}).inputValue();`);
          if(typeof value!=='string')throw new Error('script_output_not_text');
          return value;
        }
      }),
      waitForFunction:(fn,arg,options)=>command(`await p.waitForFunction(${fn.toString()},${JSON.stringify(arg)},${JSON.stringify(options)});return true;`,(options?.timeout||90000)+15000),
      close:async()=>{
        if(closed)return;
        const pending=command('await p.close();return true;');closed=true;
        return pending;
      }
    };
  }};
  return {context,close:async()=>{
    try {
      if(attached){
        await runCode(`const connection=page.context().pages().find(candidate=>candidate[${JSON.stringify(connectionKey)}]===true);if(connection&&!connection.isClosed())await connection.close();return true;`).catch(()=>{});
        await cli(['detach']);attached=false;
      }
    }finally{await fs.rm(temporary,{recursive:true,force:true});}
  }};
}
