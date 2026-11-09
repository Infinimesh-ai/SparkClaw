// Opt-in qualification of the actual candidate executable, ASAR and resources.
// Playwright observes the real main/preload/UI; no packaged file is replaced.
import assert from "node:assert/strict";
import fs from "node:fs/promises";
import path from "node:path";
import { execFileSync } from "node:child_process";
import { pathToFileURL } from "node:url";

const directory=process.env.SPARKCLAW_CONTROLLER_ISCP_LAB;
const executablePath=process.env.SPARKCLAW_CONTROLLER_CANDIDATE;
assert.ok(directory&&path.isAbsolute(directory)&&path.basename(directory).startsWith("sx-controller-iscp-"));
assert.ok(executablePath&&path.isAbsolute(executablePath));
assert.ok(!executablePath.startsWith("/Applications/"),"Qualification requires a separate candidate");
const resources=path.resolve(path.dirname(executablePath),process.platform==="darwin"?"../Resources":"resources");
const {_electron}=await import(pathToFileURL(path.join(resources,"browser-controller/node_modules/playwright-core/index.mjs")));
await fs.mkdir(path.join(directory,"controller-native-profile"),{recursive:true,mode:0o700});
const evidence={schema_version:1,scope:"packaged_candidate_disposable_ISCP_mock_model",passed:false,checks:{},tasks:[]};
let application,window;
let pausedRelay;
const pause=ms=>new Promise(resolve=>setTimeout(resolve,ms));
async function until(check,label,timeout=60000){
  const end=performance.now()+timeout;
  while(performance.now()<end){if(await check())return;await pause(100);}
  throw new Error(`Timed out: ${label}`);
}
const nativePages=()=>application.evaluate(async({webContents})=>Promise.all(webContents.getAllWebContents()
  .filter(page=>page.getURL()==="https://controller.fixture.test/")
  .map(async page=>({id:page.id,value:await page.executeJavaScript('document.querySelector("input").value'),
    debugger:page.debugger.isAttached(),sandbox:page.getLastWebPreferences().sandbox}))));
const byCommand=(a,b)=>a.command_id.localeCompare(b.command_id);
try{
  application=await _electron.launch({executablePath,timeout:45000,chromiumSandbox:true,env:{...process.env,
    SPARKCLAW_DESKTOP_ISCP_LOCAL_TEST:"1",SPARKCLAW_DESKTOP_ISCP_CONFIG:path.join(directory,"desktop-profile.json"),
    SPARKCLAW_DESKTOP_USER_DATA_DIR:path.join(directory,"controller-native-profile")}});
  application.process().stderr.on("data",chunk=>process.stderr.write(chunk));
  evidence.runtime=await application.evaluate(({app,safeStorage})=>({packaged:app.isPackaged,versions:process.versions,
    executable:process.execPath,resources:process.resourcesPath,profile:app.getPath("userData"),pid:process.pid,
    secure_storage:{available:safeStorage.isEncryptionAvailable(),
      backend:process.platform==="linux"?safeStorage.getSelectedStorageBackend():"native"}}));
  assert.equal(evidence.runtime.packaged,true);
  assert.equal(evidence.runtime.executable,executablePath);
  assert.equal(evidence.runtime.profile,path.join(directory,"controller-native-profile"));
  assert.equal(evidence.runtime.secure_storage.available,true);
  assert.ok(!["basic_text","unknown"].includes(evidence.runtime.secure_storage.backend));
  evidence.audit=JSON.parse(await fs.readFile(path.join(evidence.runtime.resources,"client-package-audit.json"),"utf8"));
  assert.equal(evidence.audit.browser_controller.files,207);
  evidence.checks.actual_packaged_runtime=true;
  await until(async()=>{window=application.windows().find(page=>page.url()==="sparkclaw-app://workbench/index.html");return Boolean(window);},"packaged workbench target");
  await window.waitForSelector(".localWorkbench");
  await until(async()=>(await window.evaluate(()=>window.sparkclawDesktop.localConnection())).state==="connected","ISCP connection");
  const connection=await window.evaluate(()=>window.sparkclawDesktop.localConnection());
  assert.equal(connection.backend.transport,"iscp");assert.equal(connection.capabilities.browser,true);
  evidence.unknown_before=(await window.evaluate(()=>window.sparkclawDesktop.state())).browser_host.unknown_writes;
  await application.evaluate(async({session,net})=>{
    globalThis.__controllerQualificationHTTP=0;
    const original=net.fetch.bind(net);
    net.fetch=(...args)=>{if(/^https?:/u.test(String(args[0]?.url||args[0]))){globalThis.__controllerQualificationHTTP++;throw Error("Business HTTP forbidden");}return original(...args);};
    globalThis.fetch=()=>{globalThis.__controllerQualificationHTTP++;throw Error("Node business HTTP forbidden");};
    await session.fromPartition("persist:sparkclaw-browser-default").protocol.handle("https",request=>{
      if(new URL(request.url).hostname!=="controller.fixture.test")throw Error("Unexpected fixture destination");
      return new Response('<!doctype html><meta charset="utf-8"><title>Packaged Controller</title><h1>Packaged transport evidence</h1><label>Display name<input aria-label="Display name"></label><p id="draft">Empty draft</p><script>document.querySelector("input").addEventListener("input",e=>document.querySelector("#draft").textContent=e.target.value)</script>',{headers:{"content-type":"text/html"}});
    });
    session.fromPartition("persist:sparkclaw-workbench-default").webRequest.onBeforeRequest({urls:["http://*/*","https://*/*","ws://*/*","wss://*/*"]},(_request,callback)=>{globalThis.__controllerQualificationHTTP++;callback({cancel:true});});
  });
  const title=`Packaged Controller ${Date.now()}`;
  const conversation=await window.evaluate(title=>window.sparkclawClientStore.create(title),title);
  evidence.conversation_id=conversation.id;
  await window.evaluate(id=>window.sparkclawDesktop.selectConversation(id),conversation.id);
  const task=await window.evaluate(async id=>{const s=window.sparkclawClientStore;const t=await s.enqueue(id,"在 https://controller.fixture.test/ 页面 Display name 字段填写 Alice。",[]);await s.submit(t.request_id);return t;},conversation.id);
  let state,approved=false;
  await until(async()=>{
    await window.evaluate(id=>window.sparkclawClientStore.reconcile(id),task.request_id);
    state=await window.evaluate(id=>window.sparkclawClientStore.read(id),conversation.id);
    const current=state.tasks.find(row=>row.request_id===task.request_id);
    const approval=current?.approvals?.find(row=>row.state==="pending"&&row.actionable);
    if(approval){
      assert.equal(approved,false);assert.equal(approval.tool,"browser.type");
      assert.ok((await nativePages()).every(page=>page.value===""));
      evidence.processes=execFileSync("ps",["-axo","pid,ppid,pgid,command"],{encoding:"utf8"}).split("\n")
        .filter(line=>line.includes(evidence.runtime.resources+"/browser-controller/"));
      assert.ok(evidence.processes.some(line=>line.includes("src/client-main.mjs")));
      assert.ok(evidence.processes.some(line=>line.includes("@playwright/mcp/cli.js")));
      evidence.checks.controller_and_MCP_from_package=true;
      await window.evaluate(({request,approval})=>window.sparkclawClientStore.decideApproval(request,approval.approval_id,approval.digest,"approve"),{request:task.request_id,approval});
      approved=true;
    }
    return current&&["delivered","failed","canceled","unknown"].includes(current.status);
  },"packaged browser task delivery",90000);
  evidence.tasks.push(state);assert.equal(state.tasks.find(row=>row.request_id===task.request_id).status,"delivered");
  evidence.pages=await nativePages();
  assert.ok(evidence.pages.some(page=>page.value==="Alice"&&!page.debugger&&page.sandbox));
  if(process.platform==="linux"){
    const mainNamespace=await fs.readlink(`/proc/${evidence.runtime.pid}/ns/user`);
    const renderers=await application.evaluate(({webContents})=>webContents.getAllWebContents()
      .filter(page=>page.getURL()==="https://controller.fixture.test/")
      .map(page=>page.getOSProcessId()));
    assert.ok(renderers.length>0);
    evidence.renderer_isolation=await Promise.all(renderers.map(async pid=>{
      const status=await fs.readFile(`/proc/${pid}/status`,"utf8");
      const userNamespace=await fs.readlink(`/proc/${pid}/ns/user`);
      assert.match(status,/^NoNewPrivs:\s+1$/mu);assert.match(status,/^Seccomp:\s+2$/mu);
      assert.notEqual(userNamespace,mainNamespace);
      return {pid,user_namespace:userNamespace,main_user_namespace:mainNamespace,no_new_privs:true,seccomp_filter:true};
    }));
    evidence.checks.linux_renderer_isolation=true;
  }
  assert.equal(approved,true);
  evidence.checks.approved_fill_and_retained_sandboxed_native_view=true;
  const unknown=(await window.evaluate(()=>window.sparkclawDesktop.state())).browser_host.unknown_writes;
  assert.deepEqual([...unknown].sort(byCommand),[...evidence.unknown_before].sort(byCommand));
  evidence.checks.baseline_unknown_fences_preserved=true;
  // The fixture used typed IPC to enqueue. Reload the original UI from its
  // durable store before selecting the conversation through its normal sidebar.
  await window.reload();
  await window.waitForSelector(".localWorkbench");
  await window.locator(".sessionSelect").filter({hasText:title}).click();
  const taskPage=(await window.evaluate(()=>window.sparkclawDesktop.state())).pages.find(page=>page.role==="task"&&page.local_conversation_id===conversation.id);
  assert.ok(taskPage);
  await window.getByRole("button",{name:/^(打开浏览器|Open browser)$/u}).click();
  await window.locator(".desktopTabSelect").filter({hasText:"Packaged Controller"}).click();
  await until(async()=>(await window.evaluate(()=>window.sparkclawDesktop.state())).presentation.presented_page_ref===taskPage.page_ref,"retained task presentation");
  const normal=(await window.evaluate(()=>window.sparkclawDesktop.state())).presentation.panel_bounds;
  await window.getByRole("button",{name:/^(加宽浏览器|Widen browser)$/u}).click();
  await until(async()=>(await window.evaluate(()=>window.sparkclawDesktop.state())).presentation.panel_bounds.width>normal.width,"native panel widening");
  await window.getByRole("button",{name:/^(恢复浏览器宽度|Restore browser width)$/u}).click();
  await until(async()=>(await window.evaluate(()=>window.sparkclawDesktop.state())).presentation.panel_bounds.width===normal.width,"native panel width restoration");
  await window.bringToFront();
  await application.evaluate(({BrowserWindow})=>{const main=BrowserWindow.getAllWindows()[0];main.show();main.focus();});
  await window.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
  const pageCapture=await application.evaluate(async({webContents})=>{
    const page=webContents.getAllWebContents().find(page=>page.getURL()==="https://controller.fixture.test/");
    return (await page.capturePage()).toPNG().toString("base64");
  });
  await fs.writeFile(path.join(directory,"evidence/controller-packaged-task-page.png"),Buffer.from(pageCapture,"base64"));
  const capture=await application.evaluate(async({BrowserWindow})=>(await BrowserWindow.getAllWindows()[0].capturePage()).toPNG().toString("base64"));
  await fs.writeFile(path.join(directory,"evidence/controller-packaged-workbench.png"),Buffer.from(capture,"base64"));
  if(process.env.SPARKCLAW_CONTROLLER_UI_OBSERVE==="1"){
    const resume=path.join(directory,"evidence/controller-ui-observed");
    await fs.rm(resume,{force:true});
    console.log("packaged_native_UI_ready_for_visual_observation");
    await until(async()=>fs.access(resume).then(()=>true,()=>false),"visual observation",90000);
  }
  // Native inset title bars portal these controls outside the panel element.
  await window.locator(".desktopBrowserWindowActions").getByRole("button",{name:/^(收起浏览器|Collapse browser)$/u}).click();
  await until(async()=>!(await window.evaluate(()=>window.sparkclawDesktop.state())).presentation.presented_page_ref,"native panel hiding");
  assert.ok((await nativePages()).some(page=>page.value==="Alice"));
  await window.getByRole("button",{name:/^(打开浏览器|Open browser)$/u}).click();
  await window.locator(".desktopTabSelect").filter({hasText:"Packaged Controller"}).click();
  await until(async()=>(await window.evaluate(()=>window.sparkclawDesktop.state())).presentation.presented_page_ref===taskPage.page_ref,"native panel reopening");
  evidence.checks.original_panel_tabs_hide_reopen_widen_restore=true;
  if(process.env.SPARKCLAW_CONTROLLER_RELAY_FAULT==="1"){
    const metadata=JSON.parse(await fs.readFile(path.join(directory,"run.json"),"utf8"));
    const container=JSON.parse(execFileSync("docker",["inspect",metadata.relay_container],{encoding:"utf8"}))[0];
    assert.equal(container.Config.Labels?.["io.sparkclaw.iscp.lab"],metadata.lab_id);
    assert.equal(container.State.Running,true);
    const personal=(await window.evaluate(()=>window.sparkclawDesktop.state())).pages.filter(page=>page.role==="personal").map(page=>page.page_ref);
    execFileSync("docker",["pause",container.Id]);pausedRelay=container.Id;
    const interrupted=await window.evaluate(()=>fetch(window.sparkclawDesktop.gatewayBase+"/api/owner",{signal:AbortSignal.timeout(2000)}).then(response=>response.ok,()=>false));
    assert.equal(interrupted,false);
    execFileSync("docker",["unpause",pausedRelay]);pausedRelay=undefined;
    const reconnected=await window.evaluate(()=>window.sparkclawDesktop.retryLocalConnection());
    assert.equal(reconnected.state,"connected");
    const original=await window.evaluate(id=>window.sparkclawClientStore.read(id),conversation.id);
    assert.equal(original.tasks.filter(row=>row.request_id===task.request_id).length,1);
    assert.equal(original.tasks.find(row=>row.request_id===task.request_id).status,"delivered");
    const restored=await window.evaluate(()=>window.sparkclawDesktop.state());
    assert.ok(personal.every(ref=>restored.pages.some(page=>page.page_ref===ref&&page.role==="personal")));
    assert.deepEqual([...restored.browser_host.unknown_writes].sort(byCommand),[...evidence.unknown_before].sort(byCommand));
    assert.equal(await window.evaluate(()=>fetch(window.sparkclawDesktop.gatewayBase+"/api/owner").then(response=>response.status)),200);
    evidence.checks.relay_block_has_no_bypass_and_same_profile_recovers=true;
  }
  evidence.direct_business_http_attempts=await application.evaluate(()=>globalThis.__controllerQualificationHTTP);
  assert.equal(evidence.direct_business_http_attempts,0);
  evidence.passed=true;
}catch(error){
  evidence.error=error.stack||String(error);console.error(evidence.error);
  evidence.observed_targets=application?.windows().map(page=>page.url());
  if(application)evidence.native_targets=await application.evaluate(({webContents})=>webContents.getAllWebContents().map(page=>({url:page.getURL(),loading:page.isLoading(),type:page.getType()}))).catch(()=>null);
  if(window){evidence.connection=await window.evaluate(()=>window.sparkclawDesktop.localConnection()).catch(()=>null);evidence.body=await window.locator("body").innerText().catch(()=>"");await window.screenshot({path:path.join(directory,"evidence/controller-packaged-failure.png")}).catch(()=>{});}
}finally{
  if(pausedRelay)execFileSync("docker",["unpause",pausedRelay]);
  if(application){
    await application.close().catch(error=>{evidence.passed=false;evidence.quit_error=String(error);});
    const remaining=execFileSync("ps",["-axo","pid,ppid,pgid,command"],{encoding:"utf8"}).split("\n")
      .filter(line=>line.includes(evidence.runtime?.resources+"/browser-controller/"));
    evidence.checks.normal_quit_stops_packaged_children=remaining.length===0;
    if(remaining.length){evidence.remaining_processes=remaining;evidence.passed=false;}
  }
  await fs.writeFile(path.join(directory,"evidence/controller-packaged.json"),JSON.stringify(evidence,null,2)+"\n",{mode:0o600});
  console.log(JSON.stringify({passed:evidence.passed,checks:evidence.checks}));
  if(!evidence.passed)process.exitCode=1;
}
