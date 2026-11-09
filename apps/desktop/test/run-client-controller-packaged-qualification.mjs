// Opt-in qualification of the actual candidate executable, ASAR and resources.
// Playwright observes the real main/preload/UI; no packaged file is replaced.
import assert from "node:assert/strict";
import fs from "node:fs/promises";
import path from "node:path";
import crypto from "node:crypto";
import { execFileSync } from "node:child_process";
import { pathToFileURL } from "node:url";
import { qualifyPackagedOperations } from "./client-controller-packaged-operations.mjs";
import { qualifyPackagedScreenshot } from "./client-controller-packaged-screenshot.mjs";
import { qualificationLaunchEnvironment, verifyQualificationAuthorization } from "./client-controller-qualification-launch.mjs";

const directory=process.env.SPARKCLAW_CONTROLLER_ISCP_LAB;
const executablePath=process.env.SPARKCLAW_CONTROLLER_CANDIDATE;
const launcher=process.env.SPARKCLAW_CONTROLLER_LAUNCHER;
assert.ok(directory&&path.isAbsolute(directory)&&path.basename(directory).startsWith("sx-controller-iscp-"));
assert.ok(executablePath&&path.isAbsolute(executablePath));
assert.ok(!executablePath.startsWith("/Applications/"),"Qualification requires a separate candidate");
if(launcher){assert.ok(path.isAbsolute(launcher));assert.ok((await fs.stat(launcher)).isFile());}
const resources=path.resolve(path.dirname(executablePath),process.platform==="darwin"?"../Resources":"resources");
const {_electron}=await import(pathToFileURL(path.join(resources,"browser-controller/node_modules/playwright-core/index.mjs")));
await fs.mkdir(path.join(directory,"controller-native-profile"),{recursive:true,mode:0o700});
const evidence={schema_version:1,scope:"packaged_candidate_disposable_ISCP",passed:false,checks:{},tasks:[]};
let application,window;
let evaluateWorkbench=(fn,arg)=>window.evaluate(fn,arg);
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
async function qualifyCancellation(conversation, task, approval) {
  const personal=(await window.evaluate(()=>window.sparkclawDesktop.createPersonal())).page_ref;
  await window.evaluate(id=>window.sparkclawClientStore.cancel(id),task.request_id);
  await until(async()=>{
    const pages=await nativePages();
    const children=execFileSync("ps",["-axo","command="],{encoding:"utf8"}).split("\n").filter(line=>line.includes(evidence.runtime.resources+"/browser-controller/"));
    return pages.length===0&&children.length===0;
  },"packaged canceled task cleanup",15000);
  assert.equal(await window.evaluate(({request,approval})=>window.sparkclawClientStore.decideApproval(request,approval.approval_id,approval.digest,"approve").then(()=>false,()=>true),{request:task.request_id,approval}),true);
  const marker=await application.evaluate(({app},id)=>{
    const {DatabaseSync}=process.getBuiltinModule("node:sqlite");
    const {join}=process.getBuiltinModule("node:path");
    const db=new DatabaseSync(join(app.getPath("userData"),"workbench/client.sqlite"),{readOnly:true});
    try{return db.prepare("SELECT value FROM metadata WHERE key=?").get(`execution_cancel:${id}`)?.value;}
    finally{db.close();}
  },task.id);
  assert.equal(marker,"1");
  evidence.checks.packaged_cancel_retires_original_approval_and_owned_execution=true;
  evidence.checks.packaged_cancel_intent_is_durable=true;
  const fresh=await window.evaluate(async id=>{const s=window.sparkclawClientStore,t=await s.enqueue(id,"打开 https://controller.fixture.test/ 网页",[]);await s.submit(t.request_id);return t;},conversation.id);
  let state;
  await until(async()=>{
    await window.evaluate(id=>window.sparkclawClientStore.reconcile(id),fresh.request_id);
    state=await window.evaluate(id=>window.sparkclawClientStore.read(id),conversation.id);
    return ["delivered","failed","canceled","unknown"].includes(state.tasks.find(row=>row.request_id===fresh.request_id)?.status);
  },"packaged new task after cancellation",90000);
  evidence.tasks.push(state);assert.equal(state.tasks.find(row=>row.request_id===fresh.request_id).status,"delivered");
  evidence.pages=await nativePages();assert.equal(evidence.pages.length,1);
  assert.ok(evidence.pages.every(page=>page.value===""&&!page.debugger&&page.sandbox));
  const desktop=await window.evaluate(()=>window.sparkclawDesktop.state());
  assert.ok(desktop.pages.some(page=>page.page_ref===personal&&page.role==="personal"));
  assert.deepEqual([...desktop.browser_host.unknown_writes].sort(byCommand),[...evidence.unknown_before].sort(byCommand));
  evidence.checks.packaged_fresh_task_and_personal_data_survive_cancellation=true;
  evidence.direct_business_http_attempts=await application.evaluate(()=>globalThis.__controllerQualificationHTTP);
  assert.equal(evidence.direct_business_http_attempts,0);evidence.cancellation=true;
}
async function qualifyParentExit() {
  const rows=evidence.processes.map(line=>line.match(/^\s*(\d+)\s+(\d+)\s+(\d+)\s+(.*)$/u));
  const controllers=rows.filter(row=>row&&row[4].includes(evidence.runtime.resources+"/browser-controller/src/client-main.mjs"));
  assert.equal(controllers.length,1);
  const pid=Number(controllers[0][1]);assert.equal(Number(controllers[0][3]),pid);
  const environment=process.platform==="linux" ? await fs.readFile(`/proc/${pid}/environ`,"utf8") :
    execFileSync("ps",["eww","-p",String(pid),"-o","command="],{encoding:"utf8"});
  const socket=environment.match(/(?:^|[\s\0])SPARKCLAW_BROWSER_CONTROLLER_SOCKET=([^\s\0]+)/u)?.[1];
  assert.ok(socket);
  const runtimeDirectory=path.dirname(socket);
  const root=path.join(await fs.realpath("/tmp"),`sparkx-${process.getuid()}`);
  assert.equal(await fs.realpath(path.dirname(runtimeDirectory)),root);
  assert.match(path.basename(runtimeDirectory),/^c-[A-Za-z0-9]+$/u);
  const owned=await fs.lstat(runtimeDirectory);
  assert.equal(owned.uid,process.getuid());assert.equal(owned.mode&0o077,0);assert.equal(owned.isSymbolicLink(),false);
  const child=application.process();assert.equal(child.pid,evidence.runtime.pid);
  const exited=new Promise(resolve=>child.once("exit",(code,signal)=>resolve({code,signal})));
  const started=performance.now();child.kill("SIGKILL");
  evidence.parent_exit=await exited;
  application=undefined;window=undefined;
  assert.equal(evidence.parent_exit.signal,"SIGKILL");
  await until(()=>!execFileSync("ps",["-axo","pgid="],{encoding:"utf8"}).split("\n")
    .some(line=>Number(line.trim())===pid),"packaged orphan process group exit",8000);
  await assert.rejects(fs.lstat(runtimeDirectory),{code:"ENOENT"});
  evidence.parent_exit.cleanup_observed_ms=Math.round(performance.now()-started);
  evidence.parent_exit.boundary="original field approval pending; no fill approval granted";
  evidence.checks.packaged_parent_exit_removes_owned_group_and_runtime_files=true;
}
async function qualify() {
try{
  const configBytes=await fs.readFile(path.join(directory,"gateway-config.json"));
  const catalogBytes=await fs.readFile(path.join(directory,"model.profiles.json"));
  const config=JSON.parse(configBytes),catalog=JSON.parse(catalogBytes);
  const capacity=catalog.profiles[config.model.capacity_profile];
  assert.equal(capacity.executable,true);assert.equal(typeof capacity.mock,"boolean");
  evidence.provider={capacity_profile:config.model.capacity_profile,mock:capacity.mock,
    fast_model:config.model.fast.model,deep_model:config.model.deep.model,
    config_sha256:crypto.createHash("sha256").update(configBytes).digest("hex"),
    catalog_sha256:crypto.createHash("sha256").update(catalogBytes).digest("hex")};
  if(launcher)evidence.launcher={path:launcher,sha256:crypto.createHash("sha256").update(await fs.readFile(launcher)).digest("hex")};
  application=await _electron.launch({executablePath:launcher||executablePath,timeout:45000,chromiumSandbox:true,
    env:qualificationLaunchEnvironment(directory)});
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
  await until(async()=>(await window.evaluate(()=>window.sparkclawDesktop.state())).browser_host.state==="connected","Controller readiness before fence baseline");
  const connection=await window.evaluate(()=>window.sparkclawDesktop.localConnection());
  evidence.connection=connection;
  verifyQualificationAuthorization(connection);assert.equal(connection.capabilities.browser,true);
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
  if(process.env.SPARKCLAW_CONTROLLER_OPERATIONS==="1"||process.env.SPARKCLAW_CONTROLLER_SCREENSHOT==="1"){
    if(process.env.SPARKCLAW_CONTROLLER_SCREENSHOT==="1")await qualifyPackagedScreenshot({application,window,evidence,until});
    else await qualifyPackagedOperations({application,window,evidence,until});
    evidence.direct_business_http_attempts=await application.evaluate(()=>globalThis.__controllerQualificationHTTP);
    assert.equal(evidence.direct_business_http_attempts,0);
    evidence.passed=true;return;
  }
  const title=`Packaged Controller ${Date.now()}`;
  const conversation=await window.evaluate(title=>window.sparkclawClientStore.create(title),title);
  evidence.conversation_id=conversation.id;
  await window.evaluate(id=>window.sparkclawDesktop.selectConversation(id),conversation.id);
  const taskStarted=performance.now();
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
      if(process.env.SPARKCLAW_CONTROLLER_CANCELLATION==="1"){
        await qualifyCancellation(conversation,task,approval);return true;
      }
      if(process.env.SPARKCLAW_CONTROLLER_PARENT_EXIT==="1"){
        evidence.direct_business_http_attempts=await application.evaluate(()=>globalThis.__controllerQualificationHTTP);
        assert.equal(evidence.direct_business_http_attempts,0);
        await qualifyParentExit();return true;
      }
      await window.evaluate(({request,approval})=>window.sparkclawClientStore.decideApproval(request,approval.approval_id,approval.digest,"approve"),{request:task.request_id,approval});
      approved=true;
    }
    return current&&["delivered","failed","canceled","unknown"].includes(current.status);
  },"packaged browser task delivery",90000);
  if(evidence.parent_exit){evidence.passed=true;return;}
  if(evidence.cancellation){evidence.passed=true;return;}
  evidence.workflow_duration_ms=Math.round(performance.now()-taskStarted);
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
  if(process.env.SPARKCLAW_CONTROLLER_UI_LIFECYCLE==="1"){
    const nativeID=evidence.pages.find(page=>page.value==="Alice").id;
    evidence.renderer_crash=await application.evaluate(({BrowserWindow},targetID)=>{
      const main=BrowserWindow.getAllWindows().find(item=>item.webContents.getURL()==="sparkclaw-app://workbench/index.html");
      const rendererPID=main.webContents.getOSProcessId();
      return new Promise(resolve=>{
        main.webContents.once("render-process-gone",(_event,details)=>resolve({reason:details.reason,renderer_pid:rendererPID,
          visible_owned_views:main.contentView.children.filter(view=>view.webContents?.id===targetID&&view.getVisible()).length}));
        main.webContents.forcefullyCrashRenderer();
      });
    },nativeID);
    assert.ok(["crashed","killed"].includes(evidence.renderer_crash.reason));
    assert.equal(evidence.renderer_crash.visible_owned_views,0);
    // The pinned Playwright Page permanently closes its protocol scope on a
    // target crash. Observe the replacement renderer through the real main's
    // WebContents, without reloading it ourselves or changing product handlers.
    evaluateWorkbench=(fn,arg)=>application.evaluate(({BrowserWindow},{source,argument})=>{
      const main=BrowserWindow.getAllWindows().find(item=>item.webContents.getURL()==="sparkclaw-app://workbench/index.html");
      return main.webContents.executeJavaScript(`(${source})(${JSON.stringify(argument)??"undefined"})`,true);
    },{source:String(fn),argument:arg});
    await until(async()=>evaluateWorkbench(()=>Boolean(document.querySelector(".localWorkbench"))).catch(()=>false),"workbench reload after renderer crash");
    await until(()=>evaluateWorkbench(()=>{
      const composer=document.querySelector("textarea");return Boolean(composer&&!composer.disabled&&!document.querySelector(".localListNotice[role=status]"));
    }),"rehydrated draft and conversation list");
    await evaluateWorkbench(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
    evidence.renderer_crash.recovered_pid=await application.evaluate(({BrowserWindow})=>BrowserWindow.getAllWindows()[0].webContents.getOSProcessId());
    assert.notEqual(evidence.renderer_crash.recovered_pid,evidence.renderer_crash.renderer_pid);
    if(process.env.SPARKCLAW_CONTROLLER_UI_RECOVERY_OBSERVE==="1"){
      const resume=path.join(directory,"evidence/controller-ui-recovered");await fs.rm(resume,{force:true});
      console.log("packaged_recovered_UI_ready_for_visual_observation");
      await until(async()=>fs.access(resume).then(()=>true,()=>false),"recovered UI observation",90000);
    }
    await until(()=>evaluateWorkbench(title=>{
      const row=[...document.querySelectorAll(".sessionSelect")].find(item=>item.textContent.includes(title));
      if(!row||row.disabled)return false;row.click();return true;
    },title),"enabled original conversation after renderer crash");
    await until(()=>evaluateWorkbench(title=>document.querySelector('.sessionSelect[aria-current="page"]')?.textContent.includes(title),title),"conversation selection after renderer crash");
    await until(()=>evaluateWorkbench(()=>{
      const button=[...document.querySelectorAll("button")].find(item=>/^(打开浏览器|Open browser)$/u.test(item.getAttribute("aria-label")||item.getAttribute("title")||item.textContent));
      if(!button||button.disabled)return false;button.click();return true;
    }),"enabled original browser entry after renderer crash");
    await until(()=>evaluateWorkbench(()=>{
      const tab=[...document.querySelectorAll(".desktopTabSelect")].find(item=>item.textContent.includes("Packaged Controller"));
      if(!tab||tab.disabled)return false;tab.click();return true;
    }),"original task tab after renderer crash");
    await until(async()=>(await evaluateWorkbench(()=>window.sparkclawDesktop.state())).presentation.presented_page_ref===taskPage.page_ref,"explicit task selection after renderer crash");
    assert.ok((await nativePages()).some(page=>page.id===nativeID&&page.value==="Alice"&&!page.debugger&&page.sandbox));
    const history=await evaluateWorkbench(id=>window.sparkclawClientStore.read(id),conversation.id);
    assert.equal(history.tasks.filter(row=>row.request_id===task.request_id).length,1);
    evidence.checks.renderer_crash_hides_views_and_preserves_completed_task=true;
    if(process.platform==="darwin"){
      const closed=await application.evaluate(({BrowserWindow})=>{
        const main=BrowserWindow.getAllWindows()[0];const id=main.id;main.close();
        return {id,destroyed:main.isDestroyed(),visible:main.isVisible()};
      });
      assert.equal(closed.destroyed,false);assert.equal(closed.visible,false);
      const activated=await application.evaluate(({app,BrowserWindow})=>{
        app.emit("activate");const main=BrowserWindow.getAllWindows()[0];return {id:main.id,visible:main.isVisible()};
      });
      assert.equal(activated.id,closed.id);assert.equal(activated.visible,true);
      assert.ok((await nativePages()).some(page=>page.id===nativeID&&page.value==="Alice"));
      evidence.checks.mac_close_hides_and_activation_reuses_window=true;
    }
  }
  if(process.env.SPARKCLAW_CONTROLLER_RELAY_FAULT==="1"){
    const metadata=JSON.parse(await fs.readFile(path.join(directory,"run.json"),"utf8"));
    const container=JSON.parse(execFileSync("docker",["inspect",metadata.relay_container],{encoding:"utf8"}))[0];
    assert.equal(container.Config.Labels?.["io.sparkclaw.iscp.lab"],metadata.lab_id);
    assert.equal(container.State.Running,true);
    const personal=(await evaluateWorkbench(()=>window.sparkclawDesktop.state())).pages.filter(page=>page.role==="personal").map(page=>page.page_ref);
    execFileSync("docker",["pause",container.Id]);pausedRelay=container.Id;
    const interrupted=await evaluateWorkbench(()=>fetch(window.sparkclawDesktop.gatewayBase+"/api/owner",{signal:AbortSignal.timeout(2000)}).then(response=>response.ok,()=>false));
    assert.equal(interrupted,false);
    execFileSync("docker",["unpause",pausedRelay]);pausedRelay=undefined;
    const reconnected=await evaluateWorkbench(()=>window.sparkclawDesktop.retryLocalConnection());
    assert.equal(reconnected.state,"connected");
    const original=await evaluateWorkbench(id=>window.sparkclawClientStore.read(id),conversation.id);
    assert.equal(original.tasks.filter(row=>row.request_id===task.request_id).length,1);
    assert.equal(original.tasks.find(row=>row.request_id===task.request_id).status,"delivered");
    const restored=await evaluateWorkbench(()=>window.sparkclawDesktop.state());
    assert.ok(personal.every(ref=>restored.pages.some(page=>page.page_ref===ref&&page.role==="personal")));
    assert.deepEqual([...restored.browser_host.unknown_writes].sort(byCommand),[...evidence.unknown_before].sort(byCommand));
    assert.equal(await evaluateWorkbench(()=>fetch(window.sparkclawDesktop.gatewayBase+"/api/owner").then(response=>response.status)),200);
    evidence.checks.relay_block_has_no_bypass_and_same_profile_recovers=true;
  }
  evidence.direct_business_http_attempts=await application.evaluate(()=>globalThis.__controllerQualificationHTTP);
  assert.equal(evidence.direct_business_http_attempts,0);
  evidence.passed=true;
}catch(error){
  evidence.error=error.stack||String(error);console.error(evidence.error);
  evidence.observed_targets=application?.windows().map(page=>page.url());
  if(application)evidence.native_targets=await application.evaluate(({webContents})=>webContents.getAllWebContents().map(page=>({url:page.getURL(),loading:page.isLoading(),type:page.getType()}))).catch(()=>null);
  if(window){evidence.connection=await evaluateWorkbench(()=>window.sparkclawDesktop.localConnection()).catch(()=>null);evidence.body=await evaluateWorkbench(()=>document.body.innerText).catch(()=>"");evidence.desktop=await evaluateWorkbench(()=>window.sparkclawDesktop.state()).catch(()=>null);await window.screenshot({path:path.join(directory,"evidence/controller-packaged-failure.png")}).catch(()=>{});}
}finally{
  if(pausedRelay)execFileSync("docker",["unpause",pausedRelay]);
  if(application){
    if(evidence.instrumented_operation_observer)evidence.operation_trace=await application.evaluate(()=>globalThis.__controllerOperationTrace).catch(()=>null);
    if(evidence.instrumented_screenshot_observer)evidence.screenshot_trace=await application.evaluate(()=>globalThis.__controllerScreenshotTrace).catch(()=>null);
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
}
await qualify();
