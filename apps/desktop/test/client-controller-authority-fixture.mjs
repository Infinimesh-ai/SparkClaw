// Opt-in native lifecycle qualification. Only a labelled disposable Gateway is
// restarted; its identity, files, client profile and prior fences are retained.
import { app, BrowserWindow, net, session, webContents } from "electron";
import assert from "node:assert/strict";
import crypto from "node:crypto";
import fs from "node:fs/promises";
import { writeFileSync } from "node:fs";
import path from "node:path";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { BrowserControllerForwarder } from "../src/browser/controller-forwarder.mjs";
import { ClientControllerRuntime } from "../src/browser/controller-runtime.mjs";
import { containerState } from "../../../scripts/lib/iscp-docker-lab.mjs";

const lab=process.env.SPARKCLAW_CONTROLLER_ISCP_LAB;
const kind=process.env.SPARKCLAW_CONTROLLER_AUTHORITY_FAULT;
assert.ok(lab?.startsWith("/private/tmp/sx-controller-iscp-"));
assert.ok(["gateway-before-approval","gateway-after-fill"].includes(kind));
const metadata=JSON.parse(await fs.readFile(path.join(lab,"run.json"),"utf8"));
const gateway=containerState(metadata.gateway_container,metadata.lab_id);
assert.equal(gateway?.State.Running,true);
assert.equal(Object.keys(gateway.HostConfig.PortBindings||{}).length,0);
const directory=path.join(lab,"evidence",`controller-authority-${kind}-${Date.now()}`);
await fs.mkdir(directory,{mode:0o700});
process.env.SPARKCLAW_DESKTOP_ISCP_LOCAL_TEST="1";
process.env.SPARKCLAW_DESKTOP_ISCP_CONFIG=path.join(lab,"desktop-profile.json");
process.env.SPARKCLAW_DESKTOP_USER_DATA_DIR=path.join(lab,"controller-native-profile");
await fs.mkdir(process.env.SPARKCLAW_DESKTOP_USER_DATA_DIR,{recursive:true,mode:0o700});
const evidence={schema_version:1,passed:false,kind,directory,scope:"source_main_and_mock_Gateway_over_encrypted_local_Relay",
  forwarder_sha256:crypto.createHash("sha256").update(await fs.readFile(new URL("../src/browser/controller-forwarder.mjs",import.meta.url))).digest("hex"),
  gateway_binary_sha256:metadata.gateway_binary_sha256,versions:process.versions,checks:{},wire_events:[],local_calls:[],tasks:[],direct_business_http_attempts:0};
const agents=new Set();
let window,oldFill,triggered=false;
const run=promisify(execFile),pause=ms=>new Promise(resolve=>setTimeout(resolve,ms));
const evaluate=script=>window.webContents.executeJavaScript(script,true);
const save=()=>writeFileSync(path.join(directory,"evidence.json"),JSON.stringify(evidence,null,2)+"\n",{mode:0o600});
const sorted=rows=>[...rows].sort((a,b)=>a.command_id.localeCompare(b.command_id));
async function until(predicate,label,timeout=45000){
  const deadline=performance.now()+timeout;
  while(performance.now()<deadline){if(await predicate())return;await pause(50);}
  throw Error(`Authority fixture timeout: ${label}`);
}
const start=BrowserControllerForwarder.prototype.start;
BrowserControllerForwarder.prototype.start=function(...args){agents.add(this);return start.apply(this,args);};
const reverse=BrowserControllerForwarder.prototype.handleReverse;
BrowserControllerForwarder.prototype.handleReverse=async function(request,authority){
  if(request.body?.operation==="page.fill")oldFill={request:structuredClone(request),authority:structuredClone(authority)};
  const reply=await reverse.call(this,request,authority);
  evidence.wire_events.push({operation:request.operation,inner_operation:request.body?.operation,command_id:request.body?.command_id,
    status:reply.status,outcome:reply.body?.outcome,error_code:reply.body?.error_code,
    ...(request.operation==="browser.controller.status"?{controller_status:reply.body}: {})});
  return reply;
};
async function restartGateway(agent){
  const before=containerState(metadata.gateway_container,metadata.lab_id);
  assert.equal(before?.Id,gateway.Id);assert.equal(before.State.Running,true);
  const oldSession=agent.session.session_id,incarnation=agent.runtime.incarnation,pid=agent.runtime.child.pid,runtimeDirectory=agent.runtime.directory;
  evidence.restart={container_id:before.Id,started_before:before.State.StartedAt,old_session:oldSession,old_incarnation:incarnation,controller_pid:pid,runtime_directory:runtimeDirectory};save();
  await run("docker",["kill","--signal=KILL",metadata.gateway_container],{timeout:20000});
  await until(()=>agent.auth.status.state!=="connected","desktop observes Gateway loss");
  await until(()=>agent.runtime.child===null&&agent.runtime.state==="stopped","owned Controller cleanup");
  assert.equal([...agent.registry.records.values()].filter(page=>page.role==="task").length,0);
  await assert.rejects(fs.stat(runtimeDirectory),{code:"ENOENT"});
  const groups=(await run("ps",["-axo","pgid="],{timeout:10000})).stdout.split("\n").map(line=>Number(line.trim()));
  assert.equal(groups.includes(pid),false);
  evidence.checks.gateway_loss_closes_owned_pages_processes_and_files=true;
  await run("docker",["start",metadata.gateway_container],{timeout:20000});
  const after=containerState(metadata.gateway_container,metadata.lab_id);
  assert.equal(after.Id,before.Id);assert.notEqual(after.State.StartedAt,before.State.StartedAt);
  await until(()=>agent.auth.status.state==="connected"&&agent.state==="connected"&&agent.session?.session_id!==oldSession,"same-profile authority recovery",60000);
  await agent.connecting;await agent.refreshFences();
  evidence.restart.new_session=agent.session.session_id;
  evidence.checks.same_container_profile_and_new_session_recover=true;save();
}
const local=ClientControllerRuntime.prototype.request;
ClientControllerRuntime.prototype.request=async function(action,body,options){
  evidence.local_calls.push({action,operation:body.operation});
  const response=await local.call(this,action,body,options);
  if(kind==="gateway-after-fill"&&action==="execute"&&body.operation==="page.fill"&&!triggered){
    triggered=true;assert.ok(oldFill);
    const agent=[...agents].find(item=>item.runtime===this);
    const page=webContents.getAllWebContents().find(page=>page.getURL()==="https://controller.fixture.test/"&&page.debugger.isAttached());
    evidence.observed_effect=await page.executeJavaScript('({value:document.querySelector("input").value,text:document.querySelector("#draft").textContent})');
    assert.deepEqual(evidence.observed_effect,{value:"Alice",text:"Alice"});
    evidence.command={command_id:oldFill.request.body.command_id,digest:oldFill.request.body.digest};
    const row=JSON.parse(await fs.readFile(path.join(agent.journal.root,evidence.command.command_id+".json"),"utf8"));
    assert.equal(row.state,"dispatched");assert.equal(row.digest,evidence.command.digest);
    evidence.checks.effect_precedes_terminal_durability=true;save();
    await restartGateway(agent);
    // Keep the old local continuation alive until a new authority has actually
    // executed work. Its eventual failure must not retire the new incarnation.
    const fresh=await evaluate("window.sparkclawClientStore.create('Controller before stale reply')");
    await evaluate(`window.sparkclawDesktop.selectConversation(${JSON.stringify(fresh.id)})`);
    const next=await submit(fresh.id,"打开 https://controller.fixture.test/ 网页");
    let state;
    await until(async()=>{state=await readTask(fresh.id,next.request_id);return ["delivered","failed","canceled","unknown"].includes(state.tasks.find(row=>row.request_id===next.request_id)?.status);},"new task while old local reply is held",60000);
    evidence.tasks.push(state);save();
    assert.equal(state.tasks.find(row=>row.request_id===next.request_id).status,"delivered");
    assert.equal(agent.registry.conversationPages.get(fresh.id)?.webContents.getURL(),"https://controller.fixture.test/");
    evidence.replacement_before_late_reply={conversation_id:fresh.id,incarnation:agent.runtime.incarnation,pid:agent.runtime.child.pid};save();
    throw Error("Injected Gateway death and loss of the local fill reply before durable completion");
  }
  return response;
};
const originalFetch=net.fetch.bind(net);
net.fetch=(...args)=>{if(/^https?:/u.test(String(args[0]?.url||args[0]))){evidence.direct_business_http_attempts++;throw Error("Direct business HTTP forbidden");}return originalFetch(...args);};
globalThis.fetch=()=>{evidence.direct_business_http_attempts++;throw Error("Node business HTTP forbidden");};
await import("../src/main/main.mjs");
const deadline=setTimeout(()=>{evidence.error="authority fixture deadline";save();app.quit();},180000);
app.on("will-quit",()=>{
  evidence.checks.normal_quit_cleans_owned_runtime=[...agents].every(agent=>agent.runtime.child===null&&agent.runtime.state==="stopped");
  if(!evidence.checks.normal_quit_cleans_owned_runtime)evidence.passed=false;
  save();console.log(JSON.stringify({passed:evidence.passed,kind,directory,checks:evidence.checks,error:evidence.error}));
  if(!evidence.passed)app.exit(1);
});
async function readTask(conversation,request){
  await evaluate(`window.sparkclawClientStore.reconcile(${JSON.stringify(request)})`);
  return evaluate(`window.sparkclawClientStore.read(${JSON.stringify(conversation)})`);
}
async function submit(conversation,prompt){
  return evaluate(`(async()=>{const s=window.sparkclawClientStore;const t=await s.enqueue(${JSON.stringify(conversation)},${JSON.stringify(prompt)},[]);await s.submit(t.request_id);return t;})()`);
}
void app.whenReady().then(async()=>{
  try{
    await until(async()=>{window=BrowserWindow.getAllWindows().find(item=>item.webContents.getURL()==="sparkclaw-app://workbench/index.html");return window&&await evaluate("Boolean(window.sparkclawClientStore&&document.querySelector('.localWorkbench'))").catch(()=>false);},"workbench");
    await until(()=>agents.size===1&&[...agents][0].state==="connected","Controller authority");
    const agent=[...agents][0];await agent.connecting;await agent.refreshFences();
    evidence.fences_before=(await evaluate("window.sparkclawDesktop.state()")).browser_host.unknown_writes;
    evidence.personal_page=(await evaluate("window.sparkclawDesktop.createPersonal()")).page_ref;
    await session.fromPartition("persist:sparkclaw-browser-default").protocol.handle("https",request=>{
      assert.equal(new URL(request.url).hostname,"controller.fixture.test");
      return new Response('<!doctype html><title>Controller authority fixture</title><label>Display name<input aria-label="Display name"></label><p id="draft">Empty draft</p><script>document.querySelector("input").addEventListener("input",e=>document.querySelector("#draft").textContent=e.target.value)</script>',{headers:{"content-type":"text/html"}});
    });
    window.webContents.session.webRequest.onBeforeRequest({urls:["http://*/*","https://*/*","ws://*/*","wss://*/*"]},(_request,callback)=>{evidence.direct_business_http_attempts++;callback({cancel:true});});
    const conversation=await evaluate(`window.sparkclawClientStore.create(${JSON.stringify(kind)})`);
    await evaluate(`window.sparkclawDesktop.selectConversation(${JSON.stringify(conversation.id)})`);
    const task=await submit(conversation.id,"在 https://controller.fixture.test/ 页面 Display name 字段填写 Alice。");
    evidence.task={conversation_id:conversation.id,request_id:task.request_id};
    let approval,state;
    await until(async()=>{
      state=await readTask(conversation.id,task.request_id);
      approval=state.tasks.find(row=>row.request_id===task.request_id)?.approvals?.find(row=>row.state==="pending"&&row.actionable);
      return Boolean(approval);
    },"original field approval");
    assert.equal(approval.tool,"browser.type");assert.equal(evidence.local_calls.filter(row=>row.operation==="page.fill").length,0);
    if(kind==="gateway-before-approval"){
      await restartGateway(agent);
      await until(async()=>{state=await readTask(conversation.id,task.request_id);return ["failed","canceled","delivered","unknown"].includes(state.tasks.find(row=>row.request_id===task.request_id)?.status);},"interrupted approval terminates");
      const rejected=await evaluate(`window.sparkclawClientStore.decideApproval(${JSON.stringify(task.request_id)},${JSON.stringify(approval.approval_id)},${JSON.stringify(approval.digest)},"approve").then(()=>false,()=>true)`);
      assert.equal(rejected,true);
      assert.equal(evidence.local_calls.filter(row=>row.operation==="page.fill").length,0);
      assert.equal(state.tasks.find(row=>row.request_id===task.request_id).approvals?.some(row=>row.state==="pending"&&row.actionable)||false,false);
      evidence.checks.gateway_restart_retires_approval_without_fill=true;
    }else{
      await evaluate(`window.sparkclawClientStore.decideApproval(${JSON.stringify(task.request_id)},${JSON.stringify(approval.approval_id)},${JSON.stringify(approval.digest)},"approve")`);
      await until(()=>triggered&&evidence.restart?.new_session&&agent.journal.rows.get(evidence.command.command_id)?.state==="unknown","uncertain dispatched write is durable unknown",90000);
      state=await readTask(conversation.id,task.request_id);await agent.refreshFences();
      assert.equal(agent.runtime.incarnation,evidence.replacement_before_late_reply.incarnation);
      assert.equal(agent.runtime.child?.pid,evidence.replacement_before_late_reply.pid);
      assert.equal(agent.registry.conversationPages.get(evidence.replacement_before_late_reply.conversation_id)?.webContents.getURL(),"https://controller.fixture.test/");
      evidence.checks.late_old_failure_preserves_replacement_controller=true;
      const fences=(await evaluate("window.sparkclawDesktop.state()")).browser_host.unknown_writes;
      assert.equal(fences.find(row=>row.command_id===evidence.command.command_id)?.digest,evidence.command.digest);
      assert.deepEqual(sorted(fences.filter(row=>row.command_id!==evidence.command.command_id)),sorted(evidence.fences_before));
      const before=evidence.local_calls.length;
      assert.equal((await reverse.call(agent,oldFill.request,oldFill.authority)).status,503);
      assert.equal((await reverse.call(agent,oldFill.request,agent.auth.transport.capabilities)).status,503);
      assert.equal(evidence.local_calls.length,before);
      assert.equal(evidence.local_calls.filter(row=>row.operation==="page.fill").length,1);
      evidence.checks.old_authority_and_binding_cannot_replay_unknown_fill=true;
      await evaluate(`window.sparkclawDesktop.reconcileBrowserHost(${JSON.stringify(evidence.command.command_id)},${JSON.stringify(evidence.command.digest)},"observed_completed")`);
      evidence.checks.only_observed_fixture_effect_reconciled=true;
    }
    evidence.tasks.push(state);
    const fresh=await evaluate("window.sparkclawClientStore.create('Controller Gateway recovery')");
    await evaluate(`window.sparkclawDesktop.selectConversation(${JSON.stringify(fresh.id)})`);
    const next=await submit(fresh.id,"打开 https://controller.fixture.test/ 网页");
    await until(async()=>{state=await readTask(fresh.id,next.request_id);return ["delivered","failed","canceled","unknown"].includes(state.tasks.find(row=>row.request_id===next.request_id)?.status);},"fresh task after recovery",60000);
    evidence.tasks.push(state);evidence.runtime_after_recovery=agent.runtime.snapshot();save();
    assert.equal(state.tasks.find(row=>row.request_id===next.request_id).status,"delivered");
    const page=agent.registry.conversationPages.get(fresh.id)?.webContents;
    assert.equal(page?.getURL(),"https://controller.fixture.test/");assert.equal(page.debugger.isAttached(),false);assert.equal(page.getLastWebPreferences().sandbox,true);
    assert.notEqual(agent.runtime.incarnation,evidence.restart.old_incarnation);
    const desktop=await evaluate("window.sparkclawDesktop.state()");
    assert.ok(desktop.pages.some(page=>page.page_ref===evidence.personal_page&&page.role==="personal"));
    assert.deepEqual(sorted(desktop.browser_host.unknown_writes),sorted(evidence.fences_before));
    assert.equal(evidence.direct_business_http_attempts,0);
    evidence.checks.fresh_incarnation_preserves_personal_page_and_prior_fences=true;
    evidence.checks.no_business_http_bypass=true;evidence.passed=true;
  }catch(error){evidence.error=error.stack||String(error);console.error(evidence.error);}
  finally{
    clearTimeout(deadline);save();
    // A failed observation does not leave this task's original Gateway stopped.
    if(containerState(metadata.gateway_container,metadata.lab_id)?.State.Running===false)await run("docker",["start",metadata.gateway_container],{timeout:20000}).catch(()=>{});
    app.quit();
  }
});
