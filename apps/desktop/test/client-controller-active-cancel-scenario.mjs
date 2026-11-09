import assert from "node:assert/strict";
import fs from "node:fs/promises";
import { execFile } from "node:child_process";
import { promisify } from "node:util";

export async function activeCancelScenario({ evidence, until, evaluate, forwarders }) {
  const forwarder=[...forwarders][0];
  const personal=(await evaluate("window.sparkclawDesktop.createPersonal()")).page_ref;
  const a=await evaluate("window.sparkclawClientStore.create('Controller active cancellation')");
  const b=await evaluate("window.sparkclawClientStore.create('Controller queued after cancellation')");
  await evaluate(`window.sparkclawDesktop.selectConversation(${JSON.stringify(a.id)})`);
  const submit=(id,prompt)=>evaluate(`(async()=>{const s=window.sparkclawClientStore,t=await s.enqueue(${JSON.stringify(id)},${JSON.stringify(prompt)},[]);await s.submit(t.request_id);return t;})()`);
  const read=async(id,request)=>{await evaluate(`window.sparkclawClientStore.reconcile(${JSON.stringify(request)})`);return evaluate(`window.sparkclawClientStore.read(${JSON.stringify(id)})`);};
  const first=await submit(a.id,"在 https://controller.fixture.test/ 页面 Display name 字段填写 Alice。");
  let state,approval;
  await until(async()=>{state=await read(a.id,first.request_id);approval=state.tasks.find(t=>t.request_id===first.request_id)?.approvals?.find(row=>row.state==="pending"&&row.actionable);return Boolean(approval);},"active field approval",45000);
  assert.equal(approval.tool,"browser.type");
  const old={pid:forwarder.runtime.child.pid,incarnation:forwarder.runtime.incarnation,directory:forwarder.runtime.directory};
  const second=await submit(b.id,"打开 https://controller.fixture.test/b 网页");
  await until(()=>forwarder.admission.queue.length===1,"second task queued behind active approval",8000);
  await evaluate(`window.sparkclawClientStore.cancel(${JSON.stringify(first.request_id)})`);
  await until(async()=>{state=await read(a.id,first.request_id);return ["canceled","unknown"].includes(state.tasks.find(t=>t.request_id===first.request_id)?.status);},"active cancellation reconciles",30000);
  evidence.tasks.push(state);
  assert.equal(await evaluate(`window.sparkclawClientStore.decideApproval(${JSON.stringify(first.request_id)},${JSON.stringify(approval.approval_id)},${JSON.stringify(approval.digest)},"approve").then(()=>false,()=>true)`),true);
  await until(async()=>{state=await read(b.id,second.request_id);return ["delivered","failed","canceled","unknown"].includes(state.tasks.find(t=>t.request_id===second.request_id)?.status);},"other queued task completes",60000);
  evidence.tasks.push(state);
  assert.equal(state.tasks.find(t=>t.request_id===second.request_id).status,"delivered");
  const page=forwarder.registry.conversationPages.get(b.id)?.webContents;
  assert.equal(page?.getURL(),"https://controller.fixture.test/b");
  assert.equal(page.debugger.isAttached(),false);assert.equal(page.getLastWebPreferences().sandbox,true);
  assert.notEqual(forwarder.runtime.incarnation,old.incarnation);assert.notEqual(forwarder.runtime.child.pid,old.pid);
  await assert.rejects(fs.stat(old.directory),{code:"ENOENT"});
  const groups=(await promisify(execFile)("ps",["-axo","pgid="],{timeout:10000})).stdout.split("\n").map(line=>Number(line.trim()));
  assert.equal(groups.includes(old.pid),false);
  assert.equal(forwarder.registry.conversationPages.has(a.id),false);
  assert.equal(forwarder.registry.selectedConversationID,a.id);
  assert.equal(evidence.wire_events.some(event=>event.inner_operation==="page.fill"),false);
  const desktop=await evaluate("window.sparkclawDesktop.state()");
  assert.ok(desktop.pages.some(page=>page.page_ref===personal&&page.role==="personal"));
  const sorted=rows=>[...rows].sort((a,b)=>a.command_id.localeCompare(b.command_id));
  assert.deepEqual(sorted(desktop.browser_host.unknown_writes),sorted(evidence.unknown_fences_before));
  assert.equal(evidence.direct_business_http_attempts,0);
  evidence.active_cancel={old,new_pid:forwarder.runtime.child.pid,new_incarnation:forwarder.runtime.incarnation};
  Object.assign(evidence.checks,{active_cancel_retires_approval_without_fill:true,active_cancel_removes_owned_processes_pages_files:true,
    other_queued_task_starts_after_cleanup:true,active_cancel_preserves_personal_page_selection_and_prior_fences:true});
  evidence.passed=true;
}
