// Opt-in native qualification of the real application and encrypted Relay.
// The page fixture and profile are disposable; no installed authority is read.
import { app, BrowserWindow, net, session, webContents } from "electron";
import assert from "node:assert/strict";
import fs from "node:fs/promises";
import { writeFileSync } from "node:fs";
import path from "node:path";
import { execFileSync } from "node:child_process";
import { BrowserControllerForwarder } from "../src/browser/controller-forwarder.mjs";
import { ISCPTransport } from "../src/main/iscp-transport.mjs";
import { ClientControllerRuntime } from "../src/browser/controller-runtime.mjs";
import { HostJournal } from "../src/browser/host-journal.mjs";
import { concurrentScenario } from "./client-controller-concurrency-scenario.mjs";
import { activeCancelScenario } from "./client-controller-active-cancel-scenario.mjs";
import { captureMainCrash, recoverMainCrash } from "./client-controller-main-restart-scenario.mjs";

const directory = process.env.SPARKCLAW_CONTROLLER_ISCP_LAB;
if (!directory?.startsWith("/private/tmp/sx-controller-iscp-") || !path.isAbsolute(directory)) throw new Error("Private Controller ISCP lab required");
const metadata = JSON.parse(await fs.readFile(path.join(directory, "run.json"), "utf8"));
assert.ok(metadata.qualified_operations.includes("browser.controller.execute"));
process.env.SPARKCLAW_DESKTOP_ISCP_LOCAL_TEST = "1";
process.env.SPARKCLAW_DESKTOP_ISCP_CONFIG = path.join(directory, "desktop-profile.json");
process.env.SPARKCLAW_DESKTOP_USER_DATA_DIR = path.join(directory, "controller-native-profile");
await fs.mkdir(process.env.SPARKCLAW_DESKTOP_USER_DATA_DIR, { mode: 0o700, recursive: true });
const evidence = { schema_version: 1, passed: false, scope: "production_main_and_Gateway_through_disposable_ISCP_Relay",
  model: "mock", direct_business_http_attempts: 0, checks: {}, tasks: [], approvals: [], wire_events: [], transport_states: [], transport_diagnostics: [] };
const startTransport = ISCPTransport.prototype.start;
ISCPTransport.prototype.start = function(...args){
  if(!this.__fixtureObserved){
    this.__fixtureObserved=true;
    const onState=this.onState;
    this.onState=state=>{evidence.transport_states.push({state,time:Date.now()});onState(state);};
    const spawn=this.spawnProcess;
    this.spawnProcess=(...values)=>{
      const child=spawn(...values);
      child.stderr.on("data",chunk=>{
        for(const line of chunk.toString().split("\n"))if(line.startsWith("fixture_"))evidence.transport_diagnostics.push(line);
      });
      return child;
    };
  }
  return startTransport.apply(this,args);
};
const dispatch = BrowserControllerForwarder.prototype.handleReverse;
const ownedRuntimes = new Set();
const forwarders = new Set();
const startForwarder = BrowserControllerForwarder.prototype.start;
BrowserControllerForwarder.prototype.start = function(...args) {
  forwarders.add(this);
  return startForwarder.apply(this,args);
};
const fault = process.env.SPARKCLAW_CONTROLLER_ISCP_FAULT;
const concurrency = process.env.SPARKCLAW_CONTROLLER_ISCP_CONCURRENCY === "1" ? concurrentScenario({evidence,until,
  expireWaiter:process.env.SPARKCLAW_CONTROLLER_ISCP_CONCURRENCY_EXPIRE==="1",
  cancelWaiter:process.env.SPARKCLAW_CONTROLLER_ISCP_CONCURRENCY_CANCEL==="1"}) : null;
if (concurrency && fault) throw new Error("Run concurrency and fault scenarios separately");
const mainCrash = fault === "main-crash-after-fill";
const crashFault = ["controller-crash-after-fill","mcp-crash-after-fill"].includes(fault) || mainCrash;
const teardownFault = crashFault || fault === "terminal-journal-failure";
if (fault && fault !== "late-write-reply" && !teardownFault) throw new Error("Unsupported fixture fault");
let pendingFill;
const localRequest = ClientControllerRuntime.prototype.request;
ClientControllerRuntime.prototype.request = async function(action, body, options) {
  const response = await localRequest.call(this, action, body, options);
  if (teardownFault && !evidence.fault && action === "execute" && body.operation === "page.fill") {
    const page = webContents.getAllWebContents().find(page => page.getURL() === "https://controller.fixture.test/");
    assert.ok(page);
    evidence.observed_before_crash = await page.executeJavaScript('({value:document.querySelector("input").value,text:document.querySelector("#draft").textContent})');
    evidence.sandbox_before_crash = page.getLastWebPreferences().sandbox;
    if (mainCrash) await captureMainCrash({ directory, evidence, runtime: this, forwarders, command: pendingFill });
    let crashPID=this.child.pid;
    if(fault==="mcp-crash-after-fill"){
      const rows=execFileSync("ps",["-axo","pid,pgid,command"],{encoding:"utf8"}).split("\n")
        .map(line=>line.match(/^\s*(\d+)\s+(\d+)\s+(.*)$/u))
        .filter(row=>row&&Number(row[2])===this.child.pid&&row[3].includes(path.join(this.bundleRoot,"node_modules/@playwright/mcp/cli.js")));
      assert.equal(rows.length,1,"exactly one owned MCP process is required");
      crashPID=Number(rows[0][1]);
    }
    evidence.fault = {kind:fault, command_id:pendingFill.command_id, digest:pendingFill.digest,
      boundary:crashFault?"local effect completed; UDS reply discarded before durable completion":"local effect completed; terminal journal writes rejected", pid:crashPID,controller_pid:this.child.pid,
      supervisor_incarnation:this.incarnation};
    if(crashFault){
      process.kill(crashPID, "SIGKILL");
      throw new Error("Injected owned-process crash and local reply loss");
    }
  }
  return response;
};
const finishJournal = HostJournal.prototype.finish;
HostJournal.prototype.finish = async function(id,state) {
  if(fault==="terminal-journal-failure"&&id===evidence.fault?.command_id)throw new Error("Injected terminal journal persistence failure");
  return finishJournal.call(this,id,state);
};
BrowserControllerForwarder.prototype.handleReverse = async function(request, authority) {
  const started=performance.now();
  ownedRuntimes.add(this.runtime);
  forwarders.add(this);
  concurrency?.before(request);
  if (request.body?.operation === "page.fill") pendingFill = request.body;
  const reply = await dispatch.call(this, request, authority);
  await concurrency?.after(request,reply,this);
  evidence.wire_events.push({ operation: request.operation, inner_operation: request.body?.operation, status: reply.status,
    outcome: reply.body?.outcome, error_code: reply.body?.error_code, command_id: request.body?.command_id,
    elapsed_ms:Math.round((performance.now()-started)*1000)/1000,
    ...(request.operation.endsWith(".chunk") ? {offset:request.body.offset,length:request.body.length} : {}) });
  if(fault === "late-write-reply" && !evidence.fault && request.body?.operation === "page.fill" && reply.body?.outcome === "completed"){
    evidence.fault={kind:fault,command_id:request.body.command_id,digest:request.body.digest};
    // Keep the authenticated session alive; the completed action's reply arrives
    // after the Gateway call deadline and must not authorize an automatic retry.
    await new Promise(resolve=>setTimeout(resolve,31000));
  }
  return reply;
};
const originalFetch = net.fetch.bind(net);
net.fetch = (...args) => {
  if (/^https?:/u.test(String(args[0]?.url || args[0]))) { evidence.direct_business_http_attempts++; throw new Error("Direct business HTTP denied in fixture"); }
  return originalFetch(...args);
};
globalThis.fetch = () => { evidence.direct_business_http_attempts++; throw new Error("Node business HTTP denied in fixture"); };
await import("../src/main/main.mjs");
let window;
const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
const evaluate = source => window.webContents.executeJavaScript(source, true);
async function until(check, label, timeout = 30000) {
  const deadline = performance.now()+timeout;
  while (performance.now()<deadline) { if (await check()) return; await pause(100); }
  throw new Error(`Timed out: ${label}`);
}
const timer = setTimeout(() => { console.error("Controller ISCP fixture deadline"); app.exit(2); }, 180000);
app.on("will-quit", () => {
  evidence.checks.normal_app_quit_stops_controller = ownedRuntimes.size > 0 && [...ownedRuntimes].every(runtime => runtime.child === null && runtime.state === "stopped");
  if (!evidence.checks.normal_app_quit_stops_controller) evidence.passed = false;
  writeFileSync(path.join(directory,"evidence/controller-native.json"),JSON.stringify(evidence,null,2)+"\n",{mode:0o600});
  console.log(JSON.stringify({passed:evidence.passed,checks:evidence.checks,evidence_file:path.join(directory,"evidence/controller-native.json")}));
});
void app.whenReady().then(async () => {
  try {
    await until(async () => { window=BrowserWindow.getAllWindows()[0];return window&&!window.webContents.isLoading()&&await evaluate("Boolean(window.sparkclawDesktop && window.sparkclawClientStore && document.querySelector('.localWorkbench'))"); }, "workbench");
    await until(async () => (await evaluate("window.sparkclawDesktop.localConnection()")).state === "connected", "ISCP connection");
    const status=await evaluate("window.sparkclawDesktop.localConnection()");
    assert.equal(status.backend.transport,"iscp");assert.equal(status.capabilities.browser,true);
    evidence.checks.negotiated_controller_operations=true;
    // Prior fault attempts keep their installation identity and durable fences.
    // A clean run must not erase them or add any new uncertain mutation.
    await until(()=>forwarders.size>0&&[...forwarders].every(forwarder=>forwarder.state==="connected"),"initial Controller fence synchronization");
    for(const forwarder of forwarders){await forwarder.connecting;await forwarder.refreshFences();}
    evidence.unknown_fences_before=(await evaluate("window.sparkclawDesktop.state()")).browser_host.unknown_writes;
    if (teardownFault) evidence.personal_page_ref=(await evaluate("window.sparkclawDesktop.createPersonal()")).page_ref;
    const browserSession=session.fromPartition("persist:sparkclaw-browser-default");
    await browserSession.protocol.handle("https", request => {
      const url=new URL(request.url);
      assert.equal(url.hostname,"controller.fixture.test");
      const large=process.env.SPARKCLAW_CONTROLLER_ISCP_LARGE === "1" ? '<div hidden>'+"bounded fixture content ".repeat(4500)+'</div>' : '';
      return new Response('<!doctype html><meta charset="utf-8"><title>Controller ISCP fixture</title><h1>Controller transport evidence</h1><label>Display name<input aria-label="Display name"></label><p id="draft">Empty draft</p>'+large+'<script>document.querySelector("input").addEventListener("input",e=>document.querySelector("#draft").textContent=e.target.value)</script>',{headers:{"content-type":"text/html"}});
    });
    window.webContents.session.webRequest.onBeforeRequest({urls:["http://*/*","https://*/*","ws://*/*","wss://*/*"]}, (_request,callback)=>{evidence.direct_business_http_attempts++;callback({cancel:true});});
    if (process.env.SPARKCLAW_CONTROLLER_RESTART_PHASE === "recover") {
      await recoverMainCrash({ directory, evidence, evaluate, until, forwarders });
      return;
    }
    if(concurrency){await concurrency.run({evaluate,forwarders});return;}
    if(process.env.SPARKCLAW_CONTROLLER_ISCP_ACTIVE_CANCEL==="1"){
      await activeCancelScenario({evidence,until,evaluate,forwarders});return;
    }
    let conversation;
    const prompts = ["打开 https://controller.fixture.test/ 网页", "在 https://controller.fixture.test/ 页面 Display name 字段填写 Alice。"];
    for (const prompt of process.env.SPARKCLAW_CONTROLLER_ISCP_FORM_ONLY === "1" ? prompts.slice(1) : prompts){
      conversation=await evaluate("window.sparkclawClientStore.create('Controller ISCP native')");
      await evaluate(`window.sparkclawDesktop.selectConversation(${JSON.stringify(conversation.id)})`);
      const task=await evaluate(`(async()=>{const s=window.sparkclawClientStore;const t=await s.enqueue(${JSON.stringify(conversation.id)},${JSON.stringify(prompt)},[]);await s.submit(t.request_id);return t;})()`);
      evidence.active_task = { conversation_id: conversation.id, request_id: task.request_id };
      let state;
      await until(async()=>{
        try { await evaluate(`window.sparkclawClientStore.reconcile(${JSON.stringify(task.request_id)})`); }
        catch { evidence.read_retries=(evidence.read_retries||0)+1;return false; }
        state=await evaluate(`window.sparkclawClientStore.read(${JSON.stringify(conversation.id)})`);
        const found=state.tasks.find(t=>t.request_id===task.request_id);
        const approval=found?.approvals?.find(row=>row.state==="pending"&&row.actionable);
        if(approval){
          assert.equal(approval.tool,"browser.type");
          assert.equal(evidence.approvals.length,0,"fixture allows one field approval only");
          assert.ok(!evidence.wire_events.some(event=>event.inner_operation==="page.fill"),"field mutated before approval");
          evidence.approvals.push({request_id:task.request_id,approval_id:approval.approval_id});
          await evaluate(`window.sparkclawClientStore.decideApproval(${JSON.stringify(task.request_id)},${JSON.stringify(approval.approval_id)},${JSON.stringify(approval.digest)},"approve")`);
          return false;
        }
        return found&&(["delivered","failed","canceled"].includes(found.status)||(fault&&evidence.fault&&found.status==="unknown"));
      },"browser workflow delivery",90000);
      evidence.tasks.push(state);
      assert.equal(state.tasks.find(t=>t.request_id===task.request_id).status,fault&&evidence.fault?"unknown":"delivered",JSON.stringify(state));
    }
    evidence.desktop=await evaluate("window.sparkclawDesktop.state()");
    evidence.native_pages=await Promise.all(webContents.getAllWebContents().filter(page=>page.getURL()==="https://controller.fixture.test/").map(async page=>({id:page.id,value:await page.executeJavaScript('document.querySelector("input").value')})));
    assert.ok(evidence.wire_events.some(event=>event.inner_operation==="page.fill"),"form workflow dispatched no fill action");
    const page=webContents.getAllWebContents().find(page=>evidence.native_pages.some(row=>row.id===page.id&&row.value==="Alice"));
    if (!teardownFault) assert.ok(page,"task page was not retained");
    const value=teardownFault ? evidence.observed_before_crash : await page.executeJavaScript('({value:document.querySelector("input").value,text:document.querySelector("#draft").textContent})');
    assert.deepEqual(value,{value:"Alice",text:"Alice"});
    if (!teardownFault) assert.equal(page.debugger.isAttached(),false);
    else {
      assert.equal(evidence.native_pages.length,0,"abnormal teardown retained an owned task page");
      assert.ok(evidence.desktop.pages.some(page=>page.page_ref===evidence.personal_page_ref&&page.role==="personal"));
      await until(()=>[...ownedRuntimes].every(runtime=>runtime.child===null&&runtime.state==="stopped"),"crashed process group cleanup");
      evidence.checks.uncertain_completion_closes_task_views_preserves_personal=true;
    }
    if(fault){
      assert.ok(evidence.fault,"write fault was not exercised");
      assert.equal(evidence.wire_events.filter(event=>event.inner_operation==="page.fill").length,1);
      for(const forwarder of forwarders)await forwarder.refreshFences();
      const uncertain=(await evaluate("window.sparkclawDesktop.state()")).browser_host.unknown_writes;
      assert.ok(uncertain.some(row=>row.command_id===evidence.fault.command_id&&row.digest===evidence.fault.digest));
      if(fault==="terminal-journal-failure"){
        const journal=[...forwarders].find(forwarder=>forwarder.journal.rows.has(evidence.fault.command_id)).journal;
        assert.equal(journal.rows.get(evidence.fault.command_id).state,"unknown");
        const durable=JSON.parse(await fs.readFile(path.join(journal.root,evidence.fault.command_id+".json"),"utf8"));
        assert.equal(durable.state,"dispatched");assert.equal(durable.digest,evidence.fault.digest);
        evidence.checks.failed_journal_keeps_durable_dispatch_and_visible_unknown=true;
      }
      assert.equal(await evaluate('fetch(window.sparkclawDesktop.gatewayBase+"/api/owner").then(response=>response.status)'),200);
      await evaluate(`window.sparkclawDesktop.reconcileBrowserHost(${JSON.stringify(evidence.fault.command_id)},${JSON.stringify(evidence.fault.digest)},"observed_completed")`);
      evidence.checks[teardownFault?"uncertain_completion_is_unknown_without_replay":"late_write_reply_is_unknown_without_replay"]=true;
      evidence.checks.unrelated_iscp_read_survives_write_failure=true;
      evidence.checks.observed_effect_reconciled_through_original_ipc=true;
      if(teardownFault){
        const recovery=await evaluate("window.sparkclawClientStore.create('Controller crash recovery')");
        await evaluate(`window.sparkclawDesktop.selectConversation(${JSON.stringify(recovery.id)})`);
        const recoveryTask=await evaluate(`(async()=>{const s=window.sparkclawClientStore;const t=await s.enqueue(${JSON.stringify(recovery.id)},"打开 https://controller.fixture.test/ 网页",[]);await s.submit(t.request_id);return t;})()`);
        let recovered;
        await until(async()=>{
          await evaluate(`window.sparkclawClientStore.reconcile(${JSON.stringify(recoveryTask.request_id)})`);
          recovered=await evaluate(`window.sparkclawClientStore.read(${JSON.stringify(recovery.id)})`);
          return ["delivered","failed","canceled","unknown"].includes(recovered.tasks.find(row=>row.request_id===recoveryTask.request_id)?.status);
        },"fresh Controller after crash",60000);
        assert.equal(recovered.tasks.find(row=>row.request_id===recoveryTask.request_id).status,"delivered");
        evidence.recovery_task=recovered;
        assert.ok([...ownedRuntimes].some(runtime=>runtime.incarnation&&runtime.incarnation!==evidence.fault.supervisor_incarnation));
        assert.equal(evidence.wire_events.filter(event=>event.inner_operation==="page.fill").length,1);
        assert.ok((await evaluate("window.sparkclawDesktop.state()")).pages.some(page=>page.page_ref===evidence.personal_page_ref));
        evidence.checks.fresh_controller_recovers_without_replaying_write=true;
      }
    }
    const desktop=await evaluate("window.sparkclawDesktop.state()");
    const byCommand=(a,b)=>a.command_id.localeCompare(b.command_id);
    assert.deepEqual([...desktop.browser_host.unknown_writes].sort(byCommand),[...evidence.unknown_fences_before].sort(byCommand));
    evidence.checks.unknown_fences_preserved_without_new_unknown=true;
    evidence.checks[fault?"original_workbench_unknown_task_recorded":"original_workbench_and_task_delivery"]=true;
    evidence.checks.shared_adapter_typed_form_and_native_effect=true;
    evidence.checks.original_per_field_approval=evidence.approvals.length===1;
    if (!teardownFault) evidence.checks.task_release_retains_view_without_debugger=true;
    evidence.checks.sandbox=(teardownFault?evidence.sandbox_before_crash:page.getLastWebPreferences().sandbox)===true;
    if(process.env.SPARKCLAW_CONTROLLER_ISCP_LARGE === "1"){
      assert.ok(evidence.wire_events.some(event=>event.operation==="browser.controller.chunk"));
      evidence.checks.large_native_result_over_encrypted_iscp=true;
    }
    assert.equal(evidence.direct_business_http_attempts,0);
    evidence.passed=true;
  } catch(error) {
    evidence.error=error.stack||String(error);console.error(evidence.error);
    evidence.connection_state=await evaluate("window.sparkclawDesktop.localConnection().then(value=>value.state)").catch(()=>"unavailable");
    evidence.desktop=await evaluate("window.sparkclawDesktop.state()").catch(()=>null);
    evidence.native_pages=await Promise.all(webContents.getAllWebContents().filter(page=>page.getURL()==="https://controller.fixture.test/").map(async page=>({id:page.id,value:await page.executeJavaScript('document.querySelector("input").value').catch(()=>null)})));
  }
  finally {
    clearTimeout(timer);
    await fs.writeFile(path.join(directory,"evidence/controller-native.json"),JSON.stringify(evidence,null,2)+"\n",{mode:0o600});
    app.quit();
  }
});
