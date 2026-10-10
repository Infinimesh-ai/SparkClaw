#!/usr/bin/env node
// Operator-only qualification, not a Controller/Host public operation.
import fs from 'node:fs/promises';
import path from 'node:path';
import {fileURLToPath,pathToFileURL} from 'node:url';
import {spawn,execFileSync} from 'node:child_process';
import {createInterface} from 'node:readline';
import {once} from 'node:events';
import {diagnoseEmptyOutlookMenu,sanitizeMenuObservation} from './fixtures/outlook-menu-diagnostic.mjs';
if(process.env.SPARKCLAW_FIXED_OUTLOOK_DIAGNOSTIC!=='1'||process.argv.length!==2)throw new Error('explicit fixed diagnostic opt-in required');
const repo=process.env.SPARKCLAW_DIAGNOSTIC_REPOSITORY||fileURLToPath(new URL('../../../',import.meta.url));
const reservation=process.env.SPARKCLAW_DIAGNOSTIC_RESERVATION;
if(!reservation?.startsWith('/tmp/'))throw new Error('private reservation helper required');
const {ApplicationHostDriver}=await import(pathToFileURL(path.join(repo,'tools/browser-controller/src/host-driver.mjs')));
const {BrowserController}=await import(pathToFileURL(path.join(repo,'tools/browser-controller/src/controller.mjs')));
const pid=Number(execFileSync('systemctl',['--user','show','-p','MainPID','--value','sparkclaw-browser-controller.service'],{encoding:'utf8'}));
if(!Number.isSafeInteger(pid)||pid<=1)throw new Error('controller inactive');
const environment=Object.fromEntries((await fs.readFile(`/proc/${pid}/environ`)).toString().split('\0').filter(v=>v.includes('=')).map(v=>{const i=v.indexOf('=');return [v.slice(0,i),v.slice(i+1)];}));
const config=JSON.parse(await fs.readFile(environment.SPARKCLAW_APP_CLI_CONFIG,'utf8'));
const globalFence=path.join(environment.SPARKCLAW_BROWSER_CLI_RUNTIME_DIR,'cleanup-fence.json');
await fs.access(globalFence).then(()=>{throw new Error('production cleanup fenced');},e=>{if(e.code!=='ENOENT')throw e;});
const root=config.runtime_directory,runtime=pathToFileURL(root+path.sep);
const {verifyRelease}=await import(new URL('src/release.mjs',runtime));
verifyRelease(config);
const binding=JSON.parse(await fs.readFile(new URL('bindings/mail-outlook.json',runtime),'utf8'));
const {OUTLOOK_SENT_BASELINE_EXPRESSION}=await import(new URL('applications/mail/lib/outlook-send-proof.mjs',runtime));
const [managed,attachments,send]=await Promise.all([import(new URL('applications/mail/lib/managed-send-dom.mjs',runtime)),import(new URL('applications/mail/lib/workspace-attachments.mjs',runtime)),import(new URL('applications/mail/lib/managed-send.mjs',runtime))]);
const directory=await fs.mkdtemp('/tmp/sc-outlook-menu-');await fs.chmod(directory,0o700);
const evidence={schema_version:1,started_at:new Date().toISOString(),release_digest:config.release_digest,production_reservation:false,diagnostic_directory:directory,no_send:true,no_upload:true};
let hold,driver,controller,handle,heartbeat,pending=Promise.resolve(),released=false;
const signal=new AbortController();
try {
  hold=spawn('sudo',['-n','docker','exec','-i','-e','SPARKCLAW_FIXED_OUTLOOK_DIAGNOSTIC=1','sparkclaw-gateway-1',reservation],{stdio:['pipe','pipe','pipe']});
  hold.stderr.on('data',()=>{evidence.reservation_error=true;});
  const exited=once(hold,'exit');hold.once('exit',()=>{if(!released)signal.abort(new Error('production reservation lost'));});
  const lines=createInterface({input:hold.stdout});
  const input=await Promise.race([once(lines,'line').then(([s])=>JSON.parse(s)),exited.then(()=>{throw new Error('reservation failed');})]);
  if(input.reservation!==true||!input.token||!input.account)throw new Error('reservation unavailable');
  lines.close();evidence.production_reservation=true;
  controller=new BrowserController({clientFactory:{open:async()=>{throw new Error('no alternate browser path');}}});
  const diagnosticSpawn=(command,args,options)=>{const child=spawn(command,args,options);if(args.some(v=>v.includes('/* sparkclaw:empty-outlook-menu-diagnostic:v1 */'))){let raw='',overflow=false;child.stdout.on('data',chunk=>{if(Buffer.byteLength(raw)+chunk.length>16384){overflow=true;raw='';}else if(!overflow)raw+=chunk;});child.once('exit',()=>{if(!overflow){const value=sanitizeMenuObservation(raw);if(value)evidence.native_observation=value;}raw='';});}return child;};
  driver=new ApplicationHostDriver({spawn:diagnosticSpawn,controller,runtimeRoot:path.join(directory,'cli-runtime'),releaseRoot:root,
    entryPoint:path.join(repo,'tools/browser-controller/node_modules/@playwright/cli/playwright-cli.js'),
    executablePath:environment.SPARKCLAW_BROWSER_EXECUTABLE,userDataDir:environment.SPARKCLAW_BROWSER_USER_DATA_DIR,
    browserChannel:environment.SPARKCLAW_BROWSER_CHANNEL,connectTimeoutMS:15000,actionTimeoutMS:10000,navigationTimeoutMS:30000,
    diagnostic:e=>{(evidence.host_events??=[]).push(e);}});
  await driver.prepare();const spec=binding.commands.send,resource={token:input.token,workspace_root:directory};
  const grant={execution_expires_ms:Date.now()+180000};
  handle=await driver.create({task:'fixed-empty-outlook-menu',epoch:1,generation:1,spec,resource,grant});
  await driver.beginActivity(handle,{id:'diagnostic',kind:'exclusive',spec,resource,grant,signal:signal.signal,task:'fixed-empty-outlook-menu'});
  const renew=()=>driver.updateLease(handle,{epoch:1,generation:1,activities:[{id:'diagnostic',kind:'exclusive',expires_ms:Math.min(Date.now()+30000,grant.execution_expires_ms)}]});
  await renew();heartbeat=setInterval(()=>{pending=pending.then(renew).catch(e=>signal.abort(e));},10000);
  const call=(method,...args)=>{signal.signal.throwIfAborted();return driver.call(handle,method,args,{signal:signal.signal,activity:'diagnostic',spec});};
  await call('navigate',spec.host.page.loginURL);
  await call('prepareBackgroundPage');evidence.production_background_prepared=true;
  const baseline=(await call('inspect',`async()=>{const r=await (${OUTLOOK_SENT_BASELINE_EXPRESSION})();return {ready:Array.isArray(r?.ids)&&r.ids.length<=1000,count:Array.isArray(r?.ids)?r.ids.length:null}}`)).result;
  evidence.sent_baseline=baseline;if(!baseline?.ready)throw new Error('sent_baseline_unavailable');
  const tab=Object.fromEntries(['inspect','click','press','runReadCode'].map(method=>[method,(...args)=>call(method,...args)]));
  tab.checkpoint=phase=>{evidence.phase=phase;};
  tab.record=(phase,value)=>{(evidence.reads??={})[phase]=value;};
  evidence.result=await diagnoseEmptyOutlookMenu(tab,{...managed,...attachments,...send},input.account);
  input.token='';input.account='';
} catch(error) {
  evidence.error={code:error.code??null,reason:error.diagnosticReason??null,message:/^[a-z_]+$/u.test(error.message)?error.message:'fixed_diagnostic_failed'};process.exitCode=1;
} finally {
  clearInterval(heartbeat);await pending;
  try{await driver?.shutdown();await controller?.shutdown();evidence.owned_cleanup=true;}catch(error){evidence.owned_cleanup=false;evidence.cleanup_error={code:error.code,reason:error.diagnosticReason};process.exitCode=1;}
  // Production reservation remains held until all private resources have been
  // closed. A failure retains the reservation for operator cleanup until the
  // original bounded Session TTL; a private fence cannot unlock production.
  if(hold&&hold.exitCode===null&&evidence.owned_cleanup){released=true;hold.stdin.end('release\n');const [code]=await once(hold,'exit');evidence.reservation_released=code===0;}else {evidence.reservation_released=false;if(hold&&hold.exitCode===null){evidence.requires_operator_cleanup=true;await fs.writeFile(path.join(directory,'evidence.json'),JSON.stringify(evidence,null,2)+'\n',{mode:0o600});console.log(JSON.stringify(evidence));await once(hold,'exit');}}
  evidence.finished_at=new Date().toISOString();await fs.writeFile(path.join(directory,'evidence.json'),JSON.stringify(evidence,null,2)+'\n',{mode:0o600});
  console.log(JSON.stringify(evidence));
}
