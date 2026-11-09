// Opt-in authorization lifecycle proof for a dedicated, disposable device.
// Never delete a shared qualification device's authorization or reset its data.
import { app, BrowserWindow, net, session, webContents } from "electron";
import assert from "node:assert/strict";
import crypto from "node:crypto";
import fs from "node:fs/promises";
import { writeFileSync } from "node:fs";
import { DatabaseSync } from "node:sqlite";
import path from "node:path";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { BrowserControllerForwarder } from "../src/browser/controller-forwarder.mjs";
import { ClientControllerRuntime } from "../src/browser/controller-runtime.mjs";
import { containerState } from "../../../scripts/lib/iscp-docker-lab.mjs";

const lab=process.env.SPARKCLAW_CONTROLLER_ISCP_LAB;
const phase=process.env.SPARKCLAW_CONTROLLER_AUTHORITY_PHASE;
assert.ok(lab?.startsWith("/private/tmp/sx-controller-iscp-authority-"));
assert.ok(["lock","revoke","revoked-restart"].includes(phase));
const metadata=JSON.parse(await fs.readFile(path.join(lab,"run.json"),"utf8"));
assert.equal(metadata.authorization_lifetime,"until_revoked");
assert.equal(containerState(metadata.gateway_container,metadata.lab_id)?.State.Running,true);
const profile=path.join(lab,"controller-native-profile");
process.env.SPARKCLAW_DESKTOP_ISCP_LOCAL_TEST="1";
process.env.SPARKCLAW_DESKTOP_ISCP_CONFIG=path.join(lab,"desktop-profile.json");
process.env.SPARKCLAW_DESKTOP_USER_DATA_DIR=profile;
await fs.mkdir(profile,{recursive:true,mode:0o700});
const directory=path.join(lab,"evidence",`controller-authority-${phase}-${Date.now()}`);
await fs.mkdir(directory,{mode:0o700});
const evidence={schema_version:1,passed:false,phase,directory,scope:"source_main_and_disposable_authorization_over_encrypted_local_Relay",
  forwarder_sha256:crypto.createHash("sha256").update(await fs.readFile(new URL("../src/browser/controller-forwarder.mjs",import.meta.url))).digest("hex"),
  gateway_binary_sha256:metadata.gateway_binary_sha256,versions:process.versions,checks:{},local_calls:[],direct_business_http_attempts:0};
const save=()=>writeFileSync(path.join(directory,"evidence.json"),JSON.stringify(evidence,null,2)+"\n",{mode:0o600});
const run=promisify(execFile),pause=ms=>new Promise(resolve=>setTimeout(resolve,ms));
const agents=new Set();let window,oldCommand,triggered=false;
const evaluate=script=>window.webContents.executeJavaScript(script,true);
async function until(predicate,label,timeout=45000){
  const deadline=performance.now()+timeout;
  while(performance.now()<deadline){if(await predicate())return;await pause(50);}
  throw Error(`Authorization Controller timeout: ${label}`);
}
function retainedData(){
  const db=new DatabaseSync(path.join(profile,"workbench/client.sqlite"),{readOnly:true});
  try{return JSON.parse(JSON.stringify({
    installation_id:db.prepare("SELECT value FROM metadata WHERE key='installation_id'").get().value,
    conversations:db.prepare("SELECT id FROM conversations ORDER BY id").all().map(row=>row.id),
    messages:db.prepare("SELECT id,conversation_id,role,content,created_at FROM messages ORDER BY id").all(),
    tasks:db.prepare("SELECT id,conversation_id,request_id,input_digest,context_json,created_at FROM tasks ORDER BY id").all(),
  }));}finally{db.close();}
}
function requireRetained(before){
  const after=retainedData();assert.equal(after.installation_id,before.installation_id);
  for(const key of ["conversations","messages","tasks"]){
    const rows=new Map(after[key].map(row=>[typeof row==="string"?row:row.id,row]));
    for(const row of before[key])assert.deepEqual(rows.get(typeof row==="string"?row:row.id),row);
  }
}
const start=BrowserControllerForwarder.prototype.start;
BrowserControllerForwarder.prototype.start=function(...args){agents.add(this);return start.apply(this,args);};
const reverse=BrowserControllerForwarder.prototype.handleReverse;
BrowserControllerForwarder.prototype.handleReverse=function(request,authority){
  if(request.operation==="browser.controller.execute")oldCommand={request:structuredClone(request),authority:structuredClone(authority)};
  return reverse.call(this,request,authority);
};
async function devices(){
  await evaluate("document.querySelector('.sidebarAccountTrigger').click()");
  await evaluate("[...document.querySelectorAll('.sidebarAccountMenuItem')].find(button=>/settings|设置/i.test(button.textContent)).click()");
  await until(()=>evaluate("Boolean(document.querySelector('.settingsPageNavigation'))"),"original settings");
  await evaluate("[...document.querySelectorAll('.settingsPageNavigation button')].find(button=>/devices|设备/i.test(button.textContent)).click()");
}
async function click(labels){
  assert.equal(await evaluate(`(()=>{const b=[...document.querySelectorAll('button')].find(b=>${JSON.stringify(labels)}.includes(b.textContent.trim()));if(!b||b.disabled)return false;b.click();return true;})()`),true);
}
async function retire(agent){
  evidence.retirement={pid:agent.runtime.child.pid,directory:agent.runtime.directory,incarnation:agent.runtime.incarnation};
  evidence.journal_before=[...agent.journal.rows.values()].map(row=>structuredClone(row));
  evidence.retained_before=retainedData();save();
  await devices();
  if(phase==="lock"){
    assert.equal(await evaluate("(()=>{const b=document.querySelector('.clientLogout');if(!b||b.disabled)return false;b.click();return true})()"),true);
    await until(async()=> (await evaluate("window.sparkclawDesktop.localConnection()")).state==="locked","original logout locks device");
  }else{
    await click(["Delete this device authorization","删除此设备授权"]);
    await click(["Confirm deletion","确认删除授权"]);
    await until(async()=> (await evaluate("window.sparkclawDesktop.localConnection()")).authorization_deletion?.state==="revoked","signed deletion receipt");
  }
  await until(()=>agent.runtime.state==="stopped"&&agent.runtime.child===null,"Controller and MCP cleanup");
  assert.equal([...agent.registry.records.values()].filter(page=>page.role==="task").length,0);
  assert.ok([...agent.registry.records.values()].some(page=>page.pageRef===evidence.personal_page&&page.role==="personal"));
  await assert.rejects(fs.stat(evidence.retirement.directory),{code:"ENOENT"});
  const groups=(await run("ps",["-axo","pgid="],{timeout:10000})).stdout.split("\n").map(line=>Number(line.trim()));
  assert.equal(groups.includes(evidence.retirement.pid),false);
  const count=evidence.local_calls.length;
  assert.equal((await reverse.call(agent,oldCommand.request,oldCommand.authority)).status,503);
  assert.equal(evidence.local_calls.length,count);
  requireRetained(evidence.retained_before);
  for(const row of evidence.journal_before)if(row.command_id!==evidence.command?.command_id)assert.deepEqual(agent.journal.rows.get(row.command_id),row);
  assert.ok((await session.fromPartition("persist:sparkclaw-browser-default").cookies.get({name:"controller-authority-marker"})).some(cookie=>cookie.value==="preserved"));
  evidence.checks.original_settings_retire_only_owned_execution=true;
  evidence.checks.old_authority_refused_and_local_history_personal_page_cookie_retained=true;save();
}
const local=ClientControllerRuntime.prototype.request;
ClientControllerRuntime.prototype.request=async function(action,body,options){
  evidence.local_calls.push({action,operation:body.operation});
  const response=await local.call(this,action,body,options);
  if(phase==="revoke"&&action==="execute"&&body.operation==="page.fill"&&!triggered){
    triggered=true;
    const agent=[...agents].find(agent=>agent.runtime===this);
    const page=webContents.getAllWebContents().find(page=>page.getURL()==="https://controller.fixture.test/"&&page.debugger.isAttached());
    assert.equal(await page.executeJavaScript('document.querySelector("input").value'),"Alice");
    evidence.command={command_id:oldCommand.request.body.command_id,digest:oldCommand.request.body.digest};
    assert.equal(agent.journal.rows.get(evidence.command.command_id).state,"dispatched");
    evidence.checks.fill_observed_before_terminal_receipt=true;
    await retire(agent);
    throw Error("Injected loss of local fill reply after authorization deletion");
  }
  return response;
};
const originalFetch=net.fetch.bind(net);
net.fetch=(...args)=>{if(/^https?:/u.test(String(args[0]?.url||args[0]))){evidence.direct_business_http_attempts++;throw Error("Direct business HTTP forbidden");}return originalFetch(...args);};
globalThis.fetch=()=>{evidence.direct_business_http_attempts++;throw Error("Node business HTTP forbidden");};
await import("../src/main/main.mjs");
const timer=setTimeout(()=>{evidence.error="authorization fixture deadline";save();app.quit();},180000);
app.on("will-quit",()=>{
  evidence.checks.normal_quit_cleans_owned_runtime=[...agents].every(agent=>agent.runtime.child===null&&agent.runtime.state==="stopped");
  if(!evidence.checks.normal_quit_cleans_owned_runtime)evidence.passed=false;
  save();console.log(JSON.stringify({passed:evidence.passed,phase,directory,checks:evidence.checks,error:evidence.error}));
  if(!evidence.passed)app.exit(1);
});
void app.whenReady().then(async()=>{
  try{
    await until(async()=>{window=BrowserWindow.getAllWindows().find(item=>item.webContents.getURL()==="sparkclaw-app://workbench/index.html");return window&&await evaluate("Boolean(window.sparkclawDesktop)").catch(()=>false);},"workbench preload");
    window.webContents.session.webRequest.onBeforeRequest({urls:["http://*/*","https://*/*","ws://*/*","wss://*/*"]},(_request,callback)=>{evidence.direct_business_http_attempts++;callback({cancel:true});});
    if(phase==="revoked-restart"){
      const checkpoint=JSON.parse(await fs.readFile(path.join(lab,"evidence/controller-revoked-checkpoint.json"),"utf8"));
      await until(async()=> (await evaluate("window.sparkclawDesktop.localConnection()")).authorization_deletion?.state==="revoked","persisted authorization deletion");
      const status=await evaluate("window.sparkclawDesktop.localConnection()");
      assert.equal(status.state,"invalid_authentication");assert.equal(status.authorization_deletion.operation_id,checkpoint.deletion_operation_id);
      requireRetained(checkpoint.retained_before);
      const row=JSON.parse(await fs.readFile(path.join(profile,"workbench/browser-journal",checkpoint.command.command_id+".json"),"utf8"));
      assert.equal(row.state,"unknown");assert.equal(row.digest,checkpoint.command.digest);
      assert.ok((await session.fromPartition("persist:sparkclaw-browser-default").cookies.get({name:"controller-authority-marker"})).some(cookie=>cookie.value==="preserved"));
      assert.equal(agents.size,0);assert.equal(evidence.local_calls.length,0);
      assert.equal(await evaluate("window.sparkclawDesktop.checkNewAuthorization().then(()=>false,()=>true)"),true);
      assert.equal((await evaluate("window.sparkclawDesktop.localConnection()")).state,"invalid_authentication");
      evidence.checks.revoked_restart_retains_identity_history_cookie_and_unknown_write_without_runtime=true;
    }else{
      await until(()=>agents.size===1&&[...agents][0].state==="connected","Controller authority");
      const agent=[...agents][0];await agent.connecting;await agent.refreshFences();
      evidence.fences_before=agent.snapshot().unknown_writes;
      evidence.personal_page=(await evaluate("window.sparkclawDesktop.createPersonal()")).page_ref;
      const browser=session.fromPartition("persist:sparkclaw-browser-default");
      await browser.cookies.set({url:"https://controller.fixture.test/",name:"controller-authority-marker",value:"preserved",expirationDate:Date.now()/1000+86400});
      await browser.cookies.flushStore();
      await browser.protocol.handle("https",request=>{
        assert.equal(new URL(request.url).hostname,"controller.fixture.test");
        return new Response('<!doctype html><title>Controller revocation fixture</title><label>Display name<input aria-label="Display name"></label>',{headers:{"content-type":"text/html"}});
      });
      const conversation=await evaluate(`window.sparkclawClientStore.create(${JSON.stringify("Controller "+phase)})`);
      await evaluate(`window.sparkclawDesktop.selectConversation(${JSON.stringify(conversation.id)})`);
      const task=await evaluate(`(async()=>{const s=window.sparkclawClientStore,t=await s.enqueue(${JSON.stringify(conversation.id)},"在 https://controller.fixture.test/ 页面 Display name 字段填写 Alice。",[]);await s.submit(t.request_id);return t;})()`);
      evidence.task={conversation_id:conversation.id,request_id:task.request_id};let approval;
      await until(async()=>{
        await evaluate(`window.sparkclawClientStore.reconcile(${JSON.stringify(task.request_id)})`);
        const state=await evaluate(`window.sparkclawClientStore.read(${JSON.stringify(conversation.id)})`);
        evidence.last_task_state=state;
        if(["delivered","failed","canceled","unknown"].includes(state.tasks.find(row=>row.request_id===task.request_id)?.status))throw Error("Task terminated before field approval; see last_task_state");
        approval=state.tasks.find(row=>row.request_id===task.request_id)?.approvals?.find(row=>row.state==="pending"&&row.actionable);
        return Boolean(approval);
      },"pending approval");
      assert.equal(approval.tool,"browser.type");
      const decide=`window.sparkclawClientStore.decideApproval(${JSON.stringify(task.request_id)},${JSON.stringify(approval.approval_id)},${JSON.stringify(approval.digest)},"approve")`;
      if(phase==="lock"){
        await retire(agent);
        assert.equal(await evaluate(`${decide}.then(()=>false,()=>true)`),true);
        assert.equal(evidence.local_calls.filter(row=>row.operation==="page.fill").length,0);
        evidence.checks.locked_pending_approval_cannot_fill=true;
      }else{
        await evaluate(decide);
        await until(()=>triggered&&evidence.checks.original_settings_retire_only_owned_execution&&agent.journal.rows.get(evidence.command.command_id)?.state==="unknown","revoked pending write becomes unknown",90000);
        const row=JSON.parse(await fs.readFile(path.join(agent.journal.root,evidence.command.command_id+".json"),"utf8"));
        assert.equal(row.state,"unknown");assert.equal(row.digest,evidence.command.digest);
        assert.equal(evidence.local_calls.filter(row=>row.operation==="page.fill").length,1);
        const status=await evaluate("window.sparkclawDesktop.localConnection()");
        evidence.deletion_operation_id=status.authorization_deletion.operation_id;
        await fs.writeFile(path.join(lab,"evidence/controller-revoked-checkpoint.json"),JSON.stringify({retained_before:evidence.retained_before,command:evidence.command,deletion_operation_id:evidence.deletion_operation_id}),{mode:0o600,flag:"wx"});
        evidence.checks.revoked_applied_write_is_durable_unknown_and_never_replayed=true;
      }
    }
    assert.equal(evidence.direct_business_http_attempts,0);evidence.checks.no_business_http_bypass=true;evidence.passed=true;
  }catch(error){evidence.error=error.stack||String(error);console.error(evidence.error);}
  finally{clearTimeout(timer);save();app.quit();}
});
