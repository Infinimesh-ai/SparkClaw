import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import {spawn} from 'node:child_process';
import {once} from 'node:events';
import {randomBytes} from 'node:crypto';
import {fileURLToPath} from 'node:url';
import {createInvocationState,prepareRuntimeRoot,runProcess} from '../src/cli-runtime.mjs';

const fixture=fileURLToPath(new URL('./fixtures/pending-attach-daemon.mjs',import.meta.url));
async function setup(t){
 const root=await fs.mkdtemp(path.join(os.tmpdir(),'sc-attach-fault-'));
 const runtime=path.join(root,'cli-runtime');await prepareRuntimeRoot(runtime);
 const state=await createInvocationState(runtime,'session_'+randomBytes(16).toString('hex'),{},'read');
 const session='sc-cli-'+randomBytes(10).toString('hex');
 await state.writeAttachIntent(session,fixture);
 t.after(()=>fs.rm(root,{recursive:true,force:true}));
 return {state,session,runtime};
}
async function daemon(t,state,session,overrides={}){
 const child=spawn(process.execPath,[fixture,session],{cwd:state.outputDir,env:{...process.env,...state.environment,...overrides},stdio:['ignore','ignore','ignore','ipc']});
 await once(child,'message');
 t.after(async()=>{if(child.exitCode===null&&child.signalCode===null){const done=once(child,'exit');child.kill('SIGKILL');await done;}});
 return child;
}
test('attach timeout without returned PID reaps only exact private daemon and allows removal',{skip:process.platform!=='linux'},async t=>{
 const {state,session}=await setup(t);const child=await daemon(t,state,session);
 await assert.rejects(runProcess(spawn,process.execPath,['-e','setInterval(()=>{},1000)'],{cwd:state.outputDir,env:process.env,secrets:[],forbiddenOutputValues:[],timeoutMS:30}),e=>e.code==='browser_script_timeout');
 await assert.rejects(fs.access(path.join(state.directory,'metadata.json')));
 await state.reapDaemon();assert.notEqual(child.signalCode,null);
 await state.remove();await assert.rejects(fs.access(state.directory));
});
test('missing PID with different cache refuses termination and preserves startup fence',{skip:process.platform!=='linux'},async t=>{
 const {state,session,runtime}=await setup(t);const child=await daemon(t,state,session,{XDG_CACHE_HOME:state.directory});
 await assert.rejects(state.reapDaemon());await assert.rejects(state.remove());
 await assert.rejects(prepareRuntimeRoot(runtime));
 assert.equal(child.signalCode,null);await fs.access(state.directory);
});
test('same session from an unexpected daemon entry cannot be reaped',{skip:process.platform!=='linux'},async t=>{
 const {state,session}=await setup(t);const child=await daemon(t,state,session);
 await fs.writeFile(path.join(state.directory,'attach-intent.json'),JSON.stringify({session_name:session,daemon_entry:fixture+'.wrong'}));
 await assert.rejects(state.reapDaemon());await assert.rejects(state.remove());assert.equal(child.signalCode,null);
});
test('malformed private attach intent fences rather than deleting evidence',{skip:process.platform!=='linux'},async t=>{
 const {state}=await setup(t);await fs.writeFile(path.join(state.directory,'attach-intent.json'),'{}');
 await assert.rejects(state.reapDaemon());await assert.rejects(state.remove());await fs.access(state.directory);
});
test('attach failed before daemon launch safely removes its empty private state',{skip:process.platform!=='linux'},async t=>{
 const {state}=await setup(t);await state.reapDaemon();await state.remove();await assert.rejects(fs.access(state.directory));
});
