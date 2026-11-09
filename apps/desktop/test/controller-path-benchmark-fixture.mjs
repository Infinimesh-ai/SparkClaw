// Opt-in comparison of actual old/new source mains and Gateways over encrypted
// disposable ISCP Relays. Each sample is a new, independently approved task.
import { app, BrowserWindow, net, session, webContents } from "electron";
import assert from "node:assert/strict";
import fs from "node:fs/promises";
import { writeFileSync } from "node:fs";
import path from "node:path";
import crypto from "node:crypto";
import { pathToFileURL } from "node:url";
import { execFileSync } from "node:child_process";

const root=process.env.SPARKCLAW_BROWSER_BENCH_ROOT;
const lab=process.env.SPARKCLAW_CONTROLLER_ISCP_LAB;
const mode=process.env.SPARKCLAW_BROWSER_BENCH_MODE;
const count=Number(process.env.SPARKCLAW_BROWSER_BENCH_SAMPLES||"10");
const effectDelayMS=Number(process.env.SPARKCLAW_BROWSER_BENCH_EFFECT_DELAY_MS||"0");
assert.ok(root&&path.isAbsolute(root));assert.ok(lab?.startsWith("/private/tmp/sx-controller-iscp-"));
assert.ok(["legacy","controller"].includes(mode));assert.ok(Number.isInteger(count)&&count>=1&&count<=20);
assert.ok(Number.isInteger(effectDelayMS)&&effectDelayMS>=0&&effectDelayMS<=1000);
const metadata=JSON.parse(await fs.readFile(path.join(lab,"run.json"),"utf8"));
assert.ok(metadata.qualified_operations.includes(mode==="legacy"?"browser.command":"browser.controller.execute"));
const directory=path.join(lab,"evidence",`benchmark-${mode}-${Date.now()}`);
await fs.mkdir(directory,{mode:0o700});
process.env.SPARKCLAW_DESKTOP_ISCP_LOCAL_TEST="1";
process.env.SPARKCLAW_DESKTOP_ISCP_CONFIG=path.join(lab,"desktop-profile.json");
// Reuse this lab's enrolled local installation; a fresh directory is not a
// second authorized installation and must not clone its existing credentials.
process.env.SPARKCLAW_DESKTOP_USER_DATA_DIR=path.join(lab,mode==="controller"?"controller-native-profile":"controller-path-benchmark-legacy-profile");
await fs.mkdir(process.env.SPARKCLAW_DESKTOP_USER_DATA_DIR,{mode:0o700,recursive:true});
const report={schema_version:1,passed:false,mode,source_revision:execFileSync("git",["rev-parse","HEAD"],{cwd:root,encoding:"utf8"}).trim(),
  scope:"source_main_and_mock_Gateway_over_local_encrypted_ISCP_Relay",platform:process.platform,architecture:process.arch,versions:process.versions,
  boundary:"ClientStore enqueue/submit through durable delivery and observed native fill; includes scripted approval/reconciliation polling",
  local_dispatch_boundary:"desktop reverse handler only; decoded JSON bytes, excluding ISCP encryption/framing and Gateway round trip",
  profile:process.env.SPARKCLAW_DESKTOP_USER_DATA_DIR,
  async_effect_delay_ms:effectDelayMS,
  directory,samples:[],direct_business_http_attempts:0,checks:{}};
report.gateway_binary_sha256=metadata.gateway_binary_sha256;
report.helper_binary_sha256=metadata.helper_binary_sha256;
assert.equal(crypto.createHash("sha256").update(await fs.readFile(path.join(lab,"bin/gateway-linux"))).digest("hex"),report.gateway_binary_sha256);
if(mode==="controller")report.controller_bundle_manifest_sha256=crypto.createHash("sha256").update(await fs.readFile(path.join(root,"apps/desktop/bin/browser-controller/manifest.json"))).digest("hex");
report.mock_source_sha256=crypto.createHash("sha256").update(await fs.readFile(path.join(root,"services/gateway/internal/modelrouter/mock.go"))).digest("hex");
const sourceDelta=execFileSync("git",["diff","HEAD","--","apps/desktop/src","tools/browser-controller/src","services/gateway/internal"],{cwd:root});
report.source_delta_sha256=crypto.createHash("sha256").update(sourceDelta).digest("hex");
await fs.writeFile(path.join(directory,"source-delta.patch"),sourceDelta,{mode:0o600});
if(mode==="legacy")report.baseline=JSON.parse(await fs.readFile(path.join(lab,"benchmark-baseline.json"),"utf8"));
const config=JSON.parse(await fs.readFile(path.join(lab,"gateway-config.json"),"utf8"));
const catalog=JSON.parse(await fs.readFile(path.join(lab,"model.profiles.json"),"utf8"));
assert.equal(catalog.profiles[config.model.capacity_profile].mock,true);
const importSource=filename=>import(pathToFileURL(path.join(root,filename)));
const Agent=mode==="legacy"?(await importSource("apps/desktop/src/browser/host-agent.mjs")).BrowserHostAgent:
  (await importSource("apps/desktop/src/browser/controller-forwarder.mjs")).BrowserControllerForwarder;
const agents=new Set(),events=[];
const start=Agent.prototype.start,dispatch=Agent.prototype.handleReverse;
Agent.prototype.start=function(...args){agents.add(this);return start.apply(this,args);};
Agent.prototype.handleReverse=async function(request,authority){
  const began=performance.now();
  let nativeObservation;
  if(effectDelayMS&&request.body?.operation==="page.snapshot"){
    const pages=webContents.getAllWebContents().filter(page=>page.getURL()==="https://controller.fixture.test/"&&page.debugger.isAttached());
    for(const page of pages){
      const observation=await page.executeJavaScript('({value:document.querySelector("input").value,text:document.querySelector("#draft").textContent,quiet_age_ms:window.__fixtureEffectAt===undefined?null:performance.now()-window.__fixtureEffectAt})');
      nativeObservation??=observation;
      if(observation.value==="Alice"){nativeObservation=observation;break;}
    }
  }
  const reply=await dispatch.call(this,request,authority);
  events.push({operation:request.operation,inner_operation:request.body?.operation,command_id:request.body?.command_id,
    conversation_id:request.body?.binding?.local_conversation_id,status:reply.status,outcome:reply.body?.outcome,
    elapsed_ms:performance.now()-began,...(nativeObservation?{native_observation:nativeObservation}:{}),request_json_bytes:Buffer.byteLength(JSON.stringify(request)),response_json_bytes:Buffer.byteLength(JSON.stringify(reply))});
  return reply;
};
const originalFetch=net.fetch.bind(net);
net.fetch=(...args)=>{
  if(/^https?:/u.test(String(args[0]?.url||args[0]))){report.direct_business_http_attempts++;throw Error("Direct business HTTP forbidden");}
  return originalFetch(...args);
};
globalThis.fetch=()=>{report.direct_business_http_attempts++;throw Error("Node business HTTP forbidden");};
const started=performance.now();
try{await importSource("apps/desktop/src/main/main.mjs");}
catch(error){
  report.error=error.stack||String(error);
  writeFileSync(path.join(directory,"benchmark.json"),JSON.stringify(report,null,2)+"\n",{mode:0o600});
  console.error(report.error);app.exit(1);
}
let window;
const evaluate=source=>window.webContents.executeJavaScript(source,true);
const pause=ms=>new Promise(resolve=>setTimeout(resolve,ms));
async function until(check,label,timeout=60000){const end=performance.now()+timeout;while(performance.now()<end){if(await check())return;await pause(50);}throw Error(`Timed out: ${label}`);}
const save=()=>writeFileSync(path.join(directory,"benchmark.json"),JSON.stringify(report,null,2)+"\n",{mode:0o600});
const timer=setTimeout(()=>{report.error="benchmark deadline exceeded";save();app.quit();},count*60000+90000);
app.on("will-quit",()=>{
  report.checks.owned_shutdown=[...agents].every(agent=>mode==="legacy"?!agent.leases.size:agent.runtime.child===null&&agent.runtime.state==="stopped");
  if(!report.checks.owned_shutdown)report.passed=false;
  save();console.log(JSON.stringify({passed:report.passed,directory,summary:report.summary,error:report.error}));
  if(!report.passed)app.exit(1);
});
void app.whenReady().then(async()=>{
  try{
    await until(async()=>{window=BrowserWindow.getAllWindows().find(item=>item.webContents.getURL()==="sparkclaw-app://workbench/index.html");return window&&await evaluate("Boolean(window.sparkclawClientStore&&document.querySelector('.localWorkbench'))").catch(()=>false);},"workbench");
    await until(async()=>(await evaluate("window.sparkclawDesktop.localConnection()")).state==="connected","ISCP connection");
    await until(()=>agents.size>0&&[...agents].every(agent=>agent.state==="connected"),"browser authority");
    for(const agent of agents){await agent.connecting;await agent.refreshFences();}
    const before=(await evaluate("window.sparkclawDesktop.state()")).browser_host.unknown_writes;
    report.main_ready_ms=performance.now()-started;report.unknown_fences_before=before;
    const browserSession=session.fromPartition("persist:sparkclaw-browser-default");
    await browserSession.protocol.handle("https",request=>{
      assert.equal(new URL(request.url).hostname,"controller.fixture.test");
      const update=effectDelayMS?`const value=e.target.value;setTimeout(()=>{document.querySelector("#draft").textContent=value;window.__fixtureEffectAt=performance.now()},${effectDelayMS})`:'document.querySelector("#draft").textContent=e.target.value';
      return new Response(`<!doctype html><meta charset="utf-8"><title>Controller path benchmark</title><h1>Browser benchmark</h1><label>Display name<input aria-label="Display name"></label><p id="draft">Empty draft</p><script>document.querySelector("input").addEventListener("input",e=>{${update}})</script>`,{headers:{"content-type":"text/html"}});
    });
    window.webContents.session.webRequest.onBeforeRequest({urls:["http://*/*","https://*/*","ws://*/*","wss://*/*"]},(_request,callback)=>{report.direct_business_http_attempts++;callback({cancel:true});});
    console.log(JSON.stringify({event:"benchmark_ready",mode,directory}));
    for(let index=0;index<count;index++){
      const conversation=await evaluate(`window.sparkclawClientStore.create(${JSON.stringify(`Browser ${mode} benchmark ${index+1}`)})`);
      await evaluate(`window.sparkclawDesktop.selectConversation(${JSON.stringify(conversation.id)})`);
      const priorPages=new Set(webContents.getAllWebContents().map(page=>page.id));
      const offset=events.length,began=performance.now();
      const sample={index:index+1,start_state:index===0?"first_task_in_fresh_main":"subsequent_task_same_main",conversation_id:conversation.id,approvals:0};
      report.samples.push(sample);save();
      const task=await evaluate(`(async()=>{const s=window.sparkclawClientStore;const t=await s.enqueue(${JSON.stringify(conversation.id)},"在 https://controller.fixture.test/ 页面 Display name 字段填写 Alice。",[]);await s.submit(t.request_id);return t;})()`);
      sample.request_id=task.request_id;
      let state;
      await until(async()=>{
        await evaluate(`window.sparkclawClientStore.reconcile(${JSON.stringify(task.request_id)})`);
        state=await evaluate(`window.sparkclawClientStore.read(${JSON.stringify(conversation.id)})`);
        const current=state.tasks.find(row=>row.request_id===task.request_id),approval=current?.approvals?.find(row=>row.state==="pending"&&row.actionable);
        if(approval){
          assert.equal(sample.approvals,0);assert.equal(approval.tool,"browser.type");
          assert.equal(events.slice(offset).filter(event=>event.inner_operation===(mode==="legacy"?"fill":"page.fill")).length,0);
          sample.approvals++;await evaluate(`window.sparkclawClientStore.decideApproval(${JSON.stringify(task.request_id)},${JSON.stringify(approval.approval_id)},${JSON.stringify(approval.digest)},"approve")`);return false;
        }
        return current&&["delivered","failed","canceled","unknown"].includes(current.status);
      },"approved native form",90000);
      sample.delivery_ms=performance.now()-began;sample.status=state.tasks.find(row=>row.request_id===task.request_id).status;
      sample.wire_events=events.slice(offset);sample.reverse_calls=sample.wire_events.length;
      sample.result_summary=state.messages.filter(row=>row.role==="assistant").map(row=>row.content).join("\n").slice(0,4000);
      assert.equal(sample.status,"delivered");assert.equal(sample.approvals,1);
      const page=webContents.getAllWebContents().find(page=>!priorPages.has(page.id)&&page.getURL()==="https://controller.fixture.test/");assert.ok(page);
      const value=await page.executeJavaScript('({value:document.querySelector("input").value,text:document.querySelector("#draft").textContent})');
      assert.deepEqual(value,{value:"Alice",text:"Alice"});assert.equal(page.debugger.isAttached(),false);assert.equal(page.getLastWebPreferences().sandbox,true);
      assert.equal(sample.wire_events.filter(event=>event.inner_operation===(mode==="legacy"?"fill":"page.fill")).length,1);
      if(effectDelayMS&&mode==="controller"){
        const filled=sample.wire_events.findIndex(event=>event.inner_operation==="page.fill");
        const snapshots=sample.wire_events.slice(filled+1).filter(event=>event.inner_operation==="page.snapshot");
        assert.ok(snapshots.length);
        for(const event of snapshots){
          assert.equal(event.native_observation?.text,"Alice");
          assert.ok(event.native_observation.quiet_age_ms>=config.adapters.browserAutomation.settleQuietPeriodMs);
        }
        sample.delayed_effect_and_quiet_period_verified=true;
      }
      sample.observed_native_fill=true;sample.elapsed_ms=performance.now()-began;
      sample.request_json_bytes=sample.wire_events.reduce((sum,event)=>sum+event.request_json_bytes,0);
      sample.response_json_bytes=sample.wire_events.reduce((sum,event)=>sum+event.response_json_bytes,0);
      save();console.log(JSON.stringify({event:"benchmark_sample",mode,index:sample.index,elapsed_ms:sample.elapsed_ms,reverse_calls:sample.reverse_calls}));
    }
    const after=(await evaluate("window.sparkclawDesktop.state()")).browser_host.unknown_writes;
    const sort=rows=>[...rows].sort((a,b)=>a.command_id.localeCompare(b.command_id));assert.deepEqual(sort(after),sort(before));
    assert.equal(report.direct_business_http_attempts,0);
    const metrics=rows=>{const times=rows.map(row=>row.elapsed_ms).sort((a,b)=>a-b);return {samples:times.length,median_ms:(times[Math.floor((times.length-1)/2)]+times[Math.floor(times.length/2)])/2,p95_nearest_rank_ms:times[Math.ceil(times.length*.95)-1],minimum_ms:times[0],maximum_ms:times.at(-1)};};
    report.summary={all:metrics(report.samples),first:metrics(report.samples.slice(0,1)),subsequent:count>1?metrics(report.samples.slice(1)):null};
    report.dispatch_summary=Object.fromEntries([...new Set(report.samples.flatMap(sample=>sample.wire_events.map(event=>event.inner_operation||event.operation)))].map(operation=>{
      const rows=report.samples.flatMap(sample=>sample.wire_events).filter(event=>(event.inner_operation||event.operation)===operation);
      return [operation,{...metrics(rows),request_json_bytes:rows.reduce((sum,event)=>sum+event.request_json_bytes,0),response_json_bytes:rows.reduce((sum,event)=>sum+event.response_json_bytes,0)}];
    }));
    report.checks={...report.checks,each_task_approved_once_and_native_effect_verified:true,prior_unknown_fences_preserved:true,no_business_http_bypass:true};report.passed=true;
  }catch(error){report.error=error.stack||String(error);console.error(report.error);report.connection=await evaluate("window.sparkclawDesktop.localConnection().then(value=>value.state)").catch(()=>null);}
  finally{clearTimeout(timer);save();app.quit();}
});
