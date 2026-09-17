import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import crypto from 'node:crypto';
import {spawn} from 'node:child_process';
import {once} from 'node:events';
import {fileURLToPath} from 'node:url';
import test from 'node:test';
import {runProcess,MAX_CLI_OUTPUT_BYTES} from '../src/cli-runtime.mjs';
import {PlaywrightCLITask} from '../src/cli-task.mjs';

const helper=fileURLToPath(new URL('../src/cli-read-batch.mjs',import.meta.url));
const fixture=fileURLToPath(new URL('./fixtures/fake-cli-session.mjs',import.meta.url));
const token='private-fixture-extension-token';
async function harness(t,mode='normal'){
 const root=await fs.mkdtemp(path.join(os.tmpdir(),'batch-session-'));
 const output=path.join(root,'output'),cache=path.join(root,'cache');await fs.mkdir(output);await fs.mkdir(cache);
 const sessionID=`session_${'a'.repeat(32)}`,name=`sc-cli-${crypto.createHash('sha256').update(sessionID).digest('hex').slice(0,20)}`,log=path.join(root,'calls.jsonl');
 const env={...process.env,XDG_CACHE_HOME:cache,PLAYWRIGHT_MCP_EXTENSION_TOKEN:token};
 const daemon=spawn(process.execPath,[fixture,name,mode,log],{cwd:output,env,stdio:['ignore','pipe','pipe']});
 let stderr='';daemon.stderr.on('data',chunk=>stderr+=chunk);
 await Promise.race([once(daemon.stdout,'data'),once(daemon,'exit').then(()=>{throw new Error('fixture startup failed: '+stderr);})]);
 t.after(async()=>{daemon.kill('SIGKILL');await once(daemon,'close').catch(()=>{});await fs.rm(root,{recursive:true,force:true});});
 await fs.writeFile(path.join(root,'metadata.json'),JSON.stringify({pid:daemon.pid,session_name:name}));
 const requestPath=path.join(root,'read-batch-00000000-0000-0000-0000-000000000000.json');
 await fs.writeFile(requestPath,JSON.stringify({sessionName:name,taskIndex:0,ownerTabs:[{title:'Owner',url:'https://owner.test/',current:false,crashed:false}],origins:['https://mail.google.test'],code:'async page=>({})',actionTimeoutMS:500,readTimeoutMS:mode==='hang'?100:1000}),{mode:0o600});
 let spawns=0;
 const countedSpawn=(executable,args,options)=>{spawns++;assert.equal(args.some(arg=>arg.includes(token)),false);return spawn(executable,args,options);};
 return {root,log,task:(batchReadCommands)=>{
  const task=new PlaywrightCLITask({entryPoint:fileURLToPath(new URL('../node_modules/@playwright/cli/playwright-cli.js',import.meta.url)),batchReadCommands,
   state:{sessionID,directory:root,outputDir:output,environment:{XDG_CACHE_HOME:cache},secretValues:[]},registration:{provider:'gmail',operation:'collect_page',origins:['https://mail.google.test'],timeoutMS:10000},
   actionTimeoutMS:1000,navigationTimeoutMS:1000,spawn:countedSpawn,extraEnv:{},token});
  task.taskIndex=0;task.ownerTabs=[{title:'Owner',url:'https://owner.test/',current:false,crashed:false}];return task;
 },run:async(signal,timeoutMS=5000)=>{
  const raw=await runProcess(countedSpawn,process.execPath,[helper,'--batch-request',requestPath],{cwd:output,env,timeoutMS,signal,secrets:[token],forbiddenOutputValues:[token]});
  assert.equal(raw.includes(token),false);return JSON.parse(raw);
 },spawns:()=>spawns,calls:async()=>{try{return(await fs.readFile(log,'utf8')).trim().split('\n').map(line=>JSON.parse(line).command);}catch{return[];}}};
}

test('pinned Session protocol executes pre/read/post in one helper process',async t=>{
 const h=await harness(t),result=await h.run();assert.equal(JSON.parse(result.output).result.ok,true);
 assert.deepEqual(await h.calls(),['tab-list','run-code','tab-list']);assert.equal(h.spawns(),1);
});
test('production task transport A/B keeps three backend checks while reducing process launches',async t=>{
 for(const enabled of [true,false]){
  const h=await harness(t);const result=await h.task(enabled).runReadCode('async page=>({ok:true})');
  assert.equal(result.ok,true);assert.deepEqual(await h.calls(),['tab-list','run-code','tab-list']);assert.equal(h.spawns(),enabled?1:3);
  assert.equal((await fs.readdir(h.root)).filter(name=>name.startsWith('read-batch-')).length,1,'only fixture request remains; production request removed');
 }
});
test('registrations with same-origin signed-out detection retain the original guarded path',async t=>{
 const h=await harness(t),task=h.task(true);
 task.registration.signedOutURL=url=>url==='https://mail.google.test/';
 await assert.rejects(task.runReadCode('async page=>({ok:true})'),error=>error.code==='email_login_required');
 assert.deepEqual(await h.calls(),['tab-list']);
 assert.equal(h.spawns(),1);
});
for(const [mode,commands,reason]of [
 ['pre-owner',['tab-list'],'page_topology_changed'],
 ['pre-origin',['tab-list'],'page_unregistered_other_origin'],
 ['post-owner',['tab-list','run-code','tab-list'],'page_topology_changed'],
 ['foreign',['tab-list','run-code','tab-list'],'page_unregistered_other_origin'],
 ['private-output',['tab-list','run-code'],'forbidden_output'],
 ['private-error',['tab-list','run-code'],'forbidden_output'],
 ['hang',['tab-list','run-code'],'timeout'],
 ['overflow',['tab-list','run-code'],'output_overflow'],
])test(`batch fails closed: ${mode}`,async t=>{
 const h=await harness(t,mode),result=await h.run();assert.equal(result.output,undefined);assert.equal(result.error.reason,reason);assert.deepEqual(await h.calls(),commands);
});
test('selection changes require a fresh topology observation before reading',async t=>{
 const h=await harness(t,'select'),result=await h.run();assert.ok(result.output);assert.deepEqual(await h.calls(),['tab-list','tab-select','tab-list','run-code','tab-list']);
});
test('outer abort/deadline kill the helper rather than leaving pending commands',async t=>{
 for(const abort of [false,true]){
  const h=await harness(t,'hang-long'),controller=new AbortController();
  const pending=h.run(controller.signal,abort?5000:250);
  if(abort){
   for(let attempt=0;attempt<50&&!(await h.calls()).includes('run-code');attempt++)await new Promise(resolve=>setTimeout(resolve,10));
   controller.abort();
  }
  await assert.rejects(pending,error=>error.code===(abort?'browser_extension_unavailable':'browser_script_timeout'));
  assert.deepEqual(await h.calls(),['tab-list','run-code']);
 }
});
