// One short-lived transport process; the existing task-owned CLI daemon still
// executes every pre/read/post command. This file never starts a browser/daemon.
import fs from 'node:fs/promises';
import net from 'node:net';
import path from 'node:path';
import {createRequire} from 'node:module';
import {fileURLToPath} from 'node:url';
import {assertExpectedOrigin,assertTaskTopology,parseTabs,sanitizeTabListOutput} from './cli-page-guards.mjs';
import {ControllerError} from './errors.mjs';
import {MAX_CLI_OUTPUT_BYTES,clientContractError,clientTimeoutError,clientUnavailableError,pageStale,classifyProcessExit} from './cli-runtime.mjs';

async function guardedReadBatch(request, run, token) {
  if(!Number.isSafeInteger(request.taskIndex)||request.taskIndex<0||!Array.isArray(request.ownerTabs)||
      !Array.isArray(request.origins)||!request.origins.length||typeof request.code!=='string'||Buffer.byteLength(request.code)>72<<10||
      !Number.isSafeInteger(request.actionTimeoutMS)||request.actionTimeoutMS<1||!Number.isSafeInteger(request.readTimeoutMS)||request.readTimeoutMS<1)throw clientContractError();
  const invoke=async(args,tabList=false)=>{
    const response=await run(args,args._[0]==='run-code'?request.readTimeoutMS:request.actionTimeoutMS);
    if(typeof response?.text!=='string'||Buffer.byteLength(response.text)>MAX_CLI_OUTPUT_BYTES)throw clientContractError('output_overflow');
    const raw=tabList&&!response.isError?sanitizeTabListOutput(response.text,token):response.text;
    if(token&&raw.includes(token))throw clientContractError('forbidden_output');
    if(response.isError)throw clientUnavailableError(undefined,classifyProcessExit(raw,'').reason);
    return raw;
  };
  const topology=tabs=>assertTaskTopology(tabs,request.taskIndex,request.ownerTabs);
  const selected=async()=>{
    let tabs=parseTabs(await invoke({_:['tab-list']},true));topology(tabs);
    if(!tabs[request.taskIndex].current){
      await invoke({_:['tab-select',String(request.taskIndex)]});
      tabs=parseTabs(await invoke({_:['tab-list']},true));topology(tabs);
      if(!tabs[request.taskIndex].current)throw pageStale('page_topology_changed');
    }
    assertExpectedOrigin(tabs[request.taskIndex].url,undefined,request.origins);
  };
  await selected();
  const output=await invoke({_:['run-code',request.code]});
  await selected();
  let value;try{value=JSON.parse(output);}catch{throw clientContractError();}
  if(!value||!Object.hasOwn(value,'result'))throw clientContractError();
  for(const url of [value.initial_url,value.final_url])assertExpectedOrigin(url,undefined,request.origins);
  return output;
}

async function main() {
  const args=process.argv.slice(2),requestPath=args[1],directory=path.dirname(process.cwd());
  if(args.length!==2||args[0]!=='--batch-request'||!path.isAbsolute(requestPath)||path.dirname(requestPath)!==directory||
      !/^read-batch-[0-9a-f-]{36}\.json$/.test(path.basename(requestPath)))throw clientContractError();
  const stat=await fs.lstat(requestPath);
  if(!stat.isFile()||stat.isSymbolicLink()||stat.size>512<<10||(stat.mode&0o077)||await fs.realpath(requestPath)!==requestPath)throw clientContractError();
  const request=JSON.parse(await fs.readFile(requestPath,'utf8'));
  const metadata=JSON.parse(await fs.readFile(path.join(directory,'metadata.json'),'utf8'));
  if(!/^sc-cli-[a-f0-9]{20}$/.test(request.sessionName)||metadata.session_name!==request.sessionName||!Number.isSafeInteger(metadata.pid)||metadata.pid<=1)throw clientContractError();
  const command=await fs.readFile(`/proc/${metadata.pid}/cmdline`);
  if(!command.toString().split('\0').includes(request.sessionName))throw clientContractError();
  const require=createRequire(import.meta.url);
  if(require('../node_modules/playwright-core/package.json').version!=='1.63.0-alpha-2026-08-31')throw clientContractError();
  const {Session}=require('../node_modules/playwright-core/lib/tools/cli-client/session.js');
  const {Registry,createClientInfo}=require('../node_modules/playwright-core/lib/tools/cli-client/registry.js');
  const info=createClientInfo();
  if(!path.resolve(info.daemonProfilesDir).startsWith(path.resolve(directory,'cache')+path.sep))throw clientContractError();
  const entry=await new Registry(new Map()).loadEntry(info,request.sessionName);
  const entryStat=await fs.lstat(entry.file);
  if(!entryStat.isFile()||entryStat.isSymbolicLink()||await fs.realpath(entry.file)!==path.resolve(entry.file))throw clientContractError();
  if(entry.config.name!==request.sessionName||entry.config.version!==info.version||entry.config.attached!==true)throw clientContractError();
  const socketStat=await fs.lstat(entry.config.socketPath);
  if(!socketStat.isSocket()||socketStat.isSymbolicLink()||socketStat.uid!==process.getuid())throw clientContractError();
  let timeoutMS=0,timedOut=false,overflow=false;
  // Session.run provides the pinned protocol. Bound its socket before handing
  // it to the upstream framing decoder, which otherwise has an unbounded buffer.
  class BoundedSession extends Session {
    async _connect(){
      return new Promise((resolve,reject)=>{
        const socket=net.createConnection(this.config.socketPath,()=>resolve({socket}));
        let bytes=0;
        const timer=setTimeout(()=>{timedOut=true;socket.destroy();reject(clientTimeoutError('timeout'));},timeoutMS);
        socket.on('data',chunk=>{bytes+=chunk.length;if(bytes>16*MAX_CLI_OUTPUT_BYTES){overflow=true;socket.destroy();}});
        socket.once('close',()=>clearTimeout(timer));socket.on('error',()=>reject(clientUnavailableError()));
      });
    }
  }
  const session=new BoundedSession(entry);
  const output=await guardedReadBatch(request,async(args,ms)=>{
    timeoutMS=ms;timedOut=false;overflow=false;
    try{return await session.run(info,args,{raw:true,json:false});}
    catch(cause){if(timedOut)throw clientTimeoutError('timeout');if(overflow)throw clientContractError('output_overflow');throw cause;}
  },process.env.PLAYWRIGHT_MCP_EXTENSION_TOKEN);
  return {output};
}

if(process.argv[1]&&path.resolve(process.argv[1])===fileURLToPath(import.meta.url)){
  let unexpectedDiagnostic=false;
  console.error=()=>{unexpectedDiagnostic=true;};
  let envelope;
  try{envelope=await main();if(unexpectedDiagnostic)throw clientContractError();}
  catch(error){const safe=error instanceof ControllerError?error:clientUnavailableError();envelope={error:{code:safe.code,reason:safe.diagnosticReason}};}
  process.stdout.write(JSON.stringify(envelope));
}
