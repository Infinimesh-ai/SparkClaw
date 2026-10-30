import { app, BrowserWindow, net } from "electron";
import assert from "node:assert/strict";
import fs from "node:fs/promises";
import path from "node:path";
import http from "node:http";
import { ExecutionClient } from "../src/main/execution-client.mjs";
import { ClientStore } from "../src/main/client-store.mjs";

const root = process.env.SPARKCLAW_STREAM_FIXTURE_ROOT;
if (!root?.startsWith("/private/tmp/")) throw new Error("Private schedule fixture root required");
process.env.SPARKCLAW_DESKTOP_ISCP_LOCAL_TEST = "1";
process.env.SPARKCLAW_DESKTOP_ISCP_CONFIG = path.join(root, "desktop-profile.json");
process.env.SPARKCLAW_DESKTOP_USER_DATA_DIR = path.join(root, "profile");
const realModel = process.env.SPARKCLAW_STREAM_FIXTURE_MODE === "schedule-real-model";
const evidence = { schema_version: 1, event: "sparkclaw_schedule_native", production_entry: true, production_agent: true,
  mock: !realModel, production_data: false, platform: process.platform, electron: process.versions.electron,
  direct_business_attempts: 0, local_schedule_reads: 0, cases: [], requests: [], receipts: [] };
let execution, window, failRead = false;
const deliveries = new Map();
const start = ExecutionClient.prototype.start;
ExecutionClient.prototype.start = function (...args) { execution = this; return start.apply(this, args); };
const commit = ClientStore.prototype.commitDelivery;
ClientStore.prototype.commitDelivery = function (scope, delivery, files) {
  const result = commit.call(this, scope, delivery, files);
  deliveries.set(delivery.request_id, { delivery, files });
  return result;
};
const listSchedules = ClientStore.prototype.listSchedules;
ClientStore.prototype.listSchedules = function (...args) {
  evidence.local_schedule_reads++;
  if (failRead) { failRead = false; throw new Error("Isolated schedule read failure"); }
  return listSchedules.apply(this, args);
};
const blocked = () => { evidence.direct_business_attempts++; throw new Error("Direct business HTTP forbidden by native schedule fixture"); };
globalThis.fetch = blocked;
const originalFetch = net.fetch.bind(net);
net.fetch = (...args) => /^https?:/u.test(String(args[0]?.url || args[0])) ? blocked() : originalFetch(...args);
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
async function until(check, label, timeout = realModel ? 100000 : 30000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) { if (await check()) return; await delay(50); }
  throw new Error(`Timed out: ${label}`);
}
function sourceEvidence() {
  return new Promise((resolve, reject) => {
    const request = http.get(`${process.env.SPARKCLAW_STREAM_FIXTURE_ORIGIN}/fixture/evidence`, {
      headers: { Authorization: `Bearer ${process.env.SPARKCLAW_STREAM_FIXTURE_TOKEN}` }, timeout: 3000,
    }, response => {
      let data = "";
      response.setEncoding("utf8"); response.on("data", part => { data += part; });
      response.on("end", () => { try { assert.equal(response.statusCode, 200); resolve(JSON.parse(data)); } catch (error) { reject(error); } });
    });
    request.on("timeout", () => request.destroy(new Error("Fixture evidence timeout"))); request.on("error", reject);
  });
}
const deadline = setTimeout(() => { console.error("Native schedule qualification deadline exceeded"); app.exit(1); }, 410000);
await import("../src/main/main.mjs");
void app.whenReady().then(async () => {
  try {
    await until(async () => { window = BrowserWindow.getAllWindows()[0]; return window && !window.webContents.isLoading() &&
      await window.webContents.executeJavaScript("Boolean(window.sparkclawClientStore && document.querySelector('.localWorkbench'))"); }, "original workbench");
    const evaluate = source => window.webContents.executeJavaScript(source, true);
    await until(async () => execution && (await evaluate("window.sparkclawDesktop.localConnection()")).state === "connected", "ISCP connection");
    window.show(); window.focus();
    window.webContents.session.webRequest.onBeforeRequest({ urls: ["http://*/*", "https://*/*", "ws://*/*", "wss://*/*"] }, (_request, callback) => {
      evidence.direct_business_attempts++; callback({ cancel: true });
    });
    const scope = execution.getIdentity();
    assert.ok(scope);
    const date = (days, hour = 12) => { const d = new Date(); d.setDate(d.getDate() + days); d.setHours(hour, 0, 0, 0); return d.toISOString(); };
    const tomorrow = date(1), nextDay = date(2);
    const originA = execution.store.create(scope, "Native schedule origin A", true);
    const originB = execution.store.create(scope, "Native schedule origin B", true);
    const titles = ["local-only schedule A", "local-only schedule B", "local-only later schedule"];
    execution.store.schedule(scope, originA.id, titles[0], tomorrow);
    execution.store.schedule(scope, originB.id, titles[1], date(1, 14));
    execution.store.schedule(scope, originB.id, titles[2], nextDay);
    const otherScope = { ...scope, owner_id: `${scope.owner_id}-other` };
    const other = execution.store.create(otherScope, "Other owner", true);
    execution.store.schedule(otherScope, other.id, "other-owner schedule must remain invisible", tomorrow);
    execution.onChange();
    const setInput = async (selector, value) => evaluate(`(() => {
      const input=document.querySelector(${JSON.stringify(selector)}); if(!input) throw new Error('Input missing');
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype,'value').set.call(input,${JSON.stringify(value)});
      input.dispatchEvent(new Event('input',{bubbles:true}));
    })()`);
    const click = selector => evaluate(`(() => { const target=document.querySelector(${JSON.stringify(selector)}); if(!target) throw new Error('Control missing'); target.click(); })()`);
    const settle = () => evaluate("new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)))");
    const rows = () => evaluate("[...document.querySelectorAll('.scheduleRow > strong')].map(row=>row.textContent)");
    const back = async () => {
      await click(".topbar .workspaceBackButton");
      await until(() => evaluate("!document.querySelector('.schedulePage') && !document.querySelector('.chatColumn').hidden"), "original chat return");
    };
    const send = async input => {
      const previous = new Set(deliveries.keys());
      await until(() => evaluate("Boolean(document.querySelector('.composer textarea:not(:disabled)'))"), "composer ready");
      await setInput(".composer textarea", input);
      await until(() => evaluate("Boolean(document.querySelector('.composer .sendButton:not(:disabled)'))"), "send enabled");
      await click(".composer .sendButton");
      let id;
      await until(() => { id = [...deliveries.keys()].find(key => !previous.has(key)); return id; }, "backend result delivery");
      const task = execution.store.request(scope, id);
      const payload = JSON.parse(deliveries.get(id).delivery.payload);
      evidence.requests.push({ request_id: id, input, local_conversation_id: task.conversation_id, client_action: payload.client_action ?? null });
      return { id, task, payload };
    };
    const receipt = async (requestID, expected) => {
      let status;
      await until(async () => {
        const response = await execution.auth.authorizedExecutionFetch(`${execution.auth.descriptor.origin}/api/v1/executions/${requestID}`, {
          headers: { "X-SparkClaw-Installation": execution.store.installationID },
        });
        assert.ok(response.ok, "original execution status remains readable");
        status = await response.json();
        return status.client_action_receipt?.status === expected;
      }, `persisted ${expected} receipt`);
      assert.equal(status.client_action_receipt.action_id, requestID);
      evidence.receipts.push({ request_id: requestID, receipt: status.client_action_receipt });
    };

    const ordinary = await send("你好，请用一句话介绍你自己。");
    assert.equal(ordinary.payload.client_action, undefined);
    assert.equal(await evaluate("Boolean(document.querySelector('.schedulePage'))"), false);
    evidence.cases.push("ordinary chat retains original conversation");
    const shown = await send("显示明天的日程");
    assert.equal(shown.payload.client_action?.type, "schedule.show");
    assert.equal(shown.payload.client_action.request_id, shown.id);
    assert.equal(shown.payload.client_action.action_id, shown.id);
    assert.equal(shown.payload.client_action.local_conversation_id, shown.task.conversation_id);
    await until(async () => (await rows()).length === 2, "filtered local schedule rows");
    assert.deepEqual((await rows()).sort(), titles.slice(0, 2).sort());
    assert.equal(await evaluate("Boolean(document.querySelector('.sidebarScheduleLink') && document.querySelector('.scheduleSuggestions') && document.querySelector('.scheduleBar'))"), true);
    await receipt(shown.id, "shown");
    await fs.writeFile(path.join(root, "native-schedule-filtered.png"), (await window.webContents.capturePage()).toPNG(), { mode: 0o600 });
    evidence.cases.push("backend natural-language route, local owner-scoped read, original filtered schedule page, durable shown receipt");

    await click(".scheduleRow .scheduleRowActions button:first-child");
    await until(() => evaluate("Boolean(document.querySelector('form.scheduleDialog:not(.scheduleCreateDialog) textarea'))"), "original edit dialog");
    await setInput("form.scheduleDialog textarea", "local-only schedule edited");
    await click("form.scheduleDialog button[type=submit]");
    await until(async () => (await rows()).includes("local-only schedule edited"), "original local edit");
    await click(".scheduleRow .dangerIconButton");
    await until(() => evaluate("Boolean(document.querySelector('.scheduleDeleteDialog'))"), "original delete confirmation");
    await click(".scheduleDeleteDialog .secondaryButton");
    assert.equal((await rows()).length, 2);
    await click(".scheduleRow .dangerIconButton");
    await click(".scheduleDeleteDialog .dangerButton");
    await until(async () => (await rows()).length === 1, "original delete");
    evidence.cases.push("original edit, delete-cancel and confirmed delete preserve behavior");

    await back();
    await setInput(".composer textarea", "未发送的本地草稿");
    const repeated = deliveries.get(shown.id);
    execution.store.commitDelivery(scope, repeated.delivery, repeated.files);
    execution.onChange(); execution.onChange();
    await execution.reconcile(scope, shown.id);
    await delay(3300); await settle();
    assert.equal(await evaluate("Boolean(document.querySelector('.schedulePage'))"), false);
    assert.equal(await evaluate("document.querySelector('.composer textarea').value"), "未发送的本地草稿");
    const conversationTitle = execution.store.list(scope).find(row => row.id === shown.task.conversation_id)?.title;
    assert.ok(conversationTitle);
    await click(".newConversationButton");
    await until(() => evaluate("Boolean(document.querySelector('.workbenchWelcome'))"), "new conversation");
    await evaluate(`(() => { const row=[...document.querySelectorAll('.sessionSelect')].find(row=>row.textContent.includes(${JSON.stringify(conversationTitle)})); if(!row)throw new Error('Original conversation missing'); row.click(); })()`);
    await until(() => evaluate("document.querySelector('.composer textarea')?.value === '未发送的本地草稿'"), "original conversation draft restored");
    assert.equal(await evaluate("Boolean(document.querySelector('.schedulePage'))"), false);
    evidence.cases.push("duplicate delivery, refresh and historical conversation reopening do not repeat navigation or lose drafts");

    await click(".sidebarScheduleLink");
    await until(async () => (await rows()).length === 2, "manual entry resets date filter");
    assert.ok((await rows()).includes(titles[2]));
    await click(".workbenchPageHeader > .primaryButton");
    await until(() => evaluate("Boolean(document.querySelector('.scheduleCreateDialog'))"), "original create dialog");
    await click(".scheduleCreateDialog .attachmentRemove");
    await click(".scheduleToggle");
    assert.equal(await evaluate("Boolean(document.querySelector('.scheduleList'))"), false);
    await click(".scheduleToggle");
    await until(async () => (await rows()).length === 2, "original expand control");
    for (let i = 0; i < 2; i++) {
      await click(".scheduleRow .dangerIconButton");
      await until(() => evaluate("Boolean(document.querySelector('.scheduleDeleteDialog'))"), "delete confirmation");
      await click(".scheduleDeleteDialog .dangerButton");
      await until(async () => (await rows()).length === 1 - i, "local list after deletion");
    }
    await back();
    const empty = await send("显示我的日程");
    assert.equal(empty.payload.client_action?.type, "schedule.show");
    await until(() => evaluate("Boolean(document.querySelector('.schedulePage .scheduleEmpty'))"), "original empty schedule view");
    await receipt(empty.id, "shown");
    await fs.writeFile(path.join(root, "native-schedule-empty.png"), (await window.webContents.capturePage()).toPNG(), { mode: 0o600 });
    evidence.cases.push("manual entry, create-dialog cancel, collapse/expand and empty-list presentation");

    await back();
    failRead = true;
    const failed = await send("显示我的日程");
    assert.equal(failed.payload.client_action?.type, "schedule.show");
    await receipt(failed.id, "failed");
    await until(() => evaluate("Boolean(document.querySelector('.localFeedback[role=alert]'))"), "visible local read failure");
    evidence.cases.push("local read failure is reported and acknowledged as failed");

    evidence.source = await sourceEvidence();
    assert.equal(evidence.source.model.kind, "production_agent");
    assert.equal(evidence.source.model.mock, !realModel);
    assert.equal(evidence.source.direct_business_attempts, 0);
    assert.equal(evidence.direct_business_attempts, 0);
    assert.equal(evidence.source.iscp_operations["execution.submit"], evidence.requests.length);
    for (const request of evidence.requests) {
      assert.equal(evidence.source.execution_starts[request.request_id], 1);
      const input = evidence.source.schedule.inputs.find(row => row.request_id === request.request_id);
      assert.equal(input?.input, request.input);
      assert.equal(input.client_presentation?.schedule_show, true);
      assert.equal(input.client_presentation?.schema_version, 1);
      assert.equal(input.local_conversation_id, request.local_conversation_id);
      assert.equal(input.local_schedule_content_present, false);
    }
    const sourceText = JSON.stringify(evidence.source.schedule.inputs);
    for (const title of [...titles, "local-only schedule edited", "other-owner schedule must remain invisible"]) assert.equal(sourceText.includes(title), false);
    assert.equal(execution.store.listSchedules(otherScope).length, 1);
    evidence.summary = { cases: evidence.cases.length, requests: evidence.requests.length, receipts: evidence.receipts.length,
      production_agent: true, mock: !realModel, original_ui: true, local_schedule_contents_uploaded: false, direct_business_attempts: 0 };
    evidence.passed = true;
    await fs.writeFile(path.join(root, "native-evidence.json"), JSON.stringify(evidence, null, 2), { mode: 0o600 });
    console.log(JSON.stringify({ event: evidence.event, passed: true, summary: evidence.summary }));
    clearTimeout(deadline); app.quit();
  } catch (error) {
    evidence.passed = false; evidence.error = error.message;
    try { await fs.writeFile(path.join(root, "native-schedule-failed.png"), (await window.webContents.capturePage()).toPNG(), { mode: 0o600 }); } catch { /* Window may be unavailable. */ }
    await fs.writeFile(path.join(root, "native-evidence.json"), JSON.stringify(evidence, null, 2), { mode: 0o600 });
    console.error(error.stack); clearTimeout(deadline); app.exit(1);
  }
});
