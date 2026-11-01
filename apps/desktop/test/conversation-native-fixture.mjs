import { app, BrowserWindow, net } from 'electron';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import http from 'node:http';
import crypto from 'node:crypto';
import { ExecutionClient } from '../src/main/execution-client.mjs';
import { ClientStore } from '../src/main/client-store.mjs';

const root = process.env.SPARKCLAW_STREAM_FIXTURE_ROOT;
if (!root?.startsWith('/private/tmp/')) throw new Error('Private conversation fixture root required');
process.env.SPARKCLAW_DESKTOP_ISCP_LOCAL_TEST = '1';
process.env.SPARKCLAW_DESKTOP_ISCP_CONFIG = path.join(root, 'desktop-profile.json');
process.env.SPARKCLAW_DESKTOP_USER_DATA_DIR = path.join(root, 'profile');
const realModel = process.env.SPARKCLAW_STREAM_FIXTURE_MODE === 'conversation-real-model';
const evidence = { schema_version: 1, mode: realModel ? 'real-model' : 'mock', production_entry: true, production_agent: true,
  production_data: false, direct_business_attempts: 0, cases: [], requests: [], layouts: [], failures: [], uploads: [], approvals: [] };
let execution, window;
const deliveries = new Map(), originalStart = ExecutionClient.prototype.start, originalCommit = ClientStore.prototype.commitDelivery;
const pendingObserved = new Map();
ExecutionClient.prototype.start = function (...args) {
  execution = this;
  if (!this.auth.__conversationObserved) {
    this.auth.__conversationObserved = true;
    const fetch = this.auth.authorizedExecutionFetch.bind(this.auth);
    this.auth.authorizedExecutionFetch = async (url, init = {}) => {
      const pathname = new URL(url).pathname, started = Date.now();
      const upload = pathname.match(/^\/api\/v1\/inputs\/([^/]+)\/files\/([^/]+)$/u);
      try {
        const response = await fetch(url, init);
        if (upload) evidence.uploads.push({ request_id: upload[1], file_id: upload[2], kind: init.operationID ? 'requested_history' : 'explicit_current', duration_ms: Date.now() - started,
          pending_notification_to_upload_ms: pendingObserved.has(`${upload[1]}/${upload[2]}`) ? started - pendingObserved.get(`${upload[1]}/${upload[2]}`) : null, status: response.status });
        if (/^\/api\/v1\/executions\/[^/]+$/u.test(pathname) && response.ok && (!init.method || init.method === 'GET')) {
          const event = await response.clone().json();
          for (const pending of event.pending_inputs ?? []) {
            const key = `${event.request_id}/${pending.file_id}`;
            if (!pendingObserved.has(key)) pendingObserved.set(key, Date.now());
          }
        }
        return response;
      } catch (error) {
        if (upload) evidence.uploads.push({ request_id: upload[1], file_id: upload[2], duration_ms: Date.now() - started, outcome: 'unknown' });
        throw error;
      }
    };
  }
  return originalStart.apply(this, args);
};
ClientStore.prototype.commitDelivery = function (scope, delivery, files) {
  const result = originalCommit.call(this, scope, delivery, files);
  deliveries.set(delivery.request_id, { delivery, files });
  return result;
};
const blocked = () => { evidence.direct_business_attempts++; throw new Error('Direct business HTTP forbidden by native conversation fixture'); };
globalThis.fetch = blocked;
const originalFetch = net.fetch.bind(net);
net.fetch = (...args) => /^https?:/u.test(String(args[0]?.url || args[0])) ? blocked() : originalFetch(...args);
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
async function until(check, label, timeout = realModel ? 140000 : 40000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) { if (await check()) return; await delay(50); }
  throw new Error(`Timed out: ${label}`);
}
const hash = bytes => crypto.createHash('sha256').update(bytes).digest('hex');
const sourceEvidence = () => new Promise((resolve, reject) => {
  const request = http.get(`${process.env.SPARKCLAW_STREAM_FIXTURE_ORIGIN}/fixture/evidence`, {
    headers: { Authorization: `Bearer ${process.env.SPARKCLAW_STREAM_FIXTURE_TOKEN}` }, timeout: 3000,
  }, response => { let data = ''; response.setEncoding('utf8'); response.on('data', part => { data += part; });
    response.on('end', () => { try { assert.equal(response.statusCode, 200); resolve(JSON.parse(data)); } catch (error) { reject(error); } }); });
  request.on('timeout', () => request.destroy(new Error('Fixture evidence timeout'))); request.on('error', reject);
});
const deadline = setTimeout(() => { console.error('Native conversation fixture deadline exceeded'); app.exit(1); }, 830000);
await import('../src/main/main.mjs');
void app.whenReady().then(async () => {
  try {
    await until(async () => { window = BrowserWindow.getAllWindows()[0]; return window && !window.webContents.isLoading() &&
      await window.webContents.executeJavaScript("Boolean(window.sparkclawClientStore && document.querySelector('.localWorkbench'))"); }, 'original workbench');
    const evaluate = source => window.webContents.executeJavaScript(source, true);
    const click = selector => evaluate(`(() => { const node=document.querySelector(${JSON.stringify(selector)}); if(!node)throw new Error('Missing control'); node.click(); })()`);
    const input = text => evaluate(`(() => {const node=document.querySelector('.composer textarea'); Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype,'value').set.call(node,${JSON.stringify(text)}); node.dispatchEvent(new Event('input',{bubbles:true}));})()`);
    await until(async () => execution && (await evaluate('window.sparkclawDesktop.localConnection()')).state === 'connected', 'ISCP connection');
    // Keep native qualification progressing when the owner covers this test window.
    window.webContents.setBackgroundThrottling(false);
    window.show(); window.focus();
    window.webContents.session.webRequest.onBeforeRequest({ urls: ['http://*/*', 'https://*/*', 'ws://*/*', 'wss://*/*'] }, (_request, callback) => { evidence.direct_business_attempts++; callback({ cancel: true }); });
    const scope = execution.getIdentity(); assert.ok(scope);
    const layout = async label => {
      const result = await evaluate(`(() => {const selectors=['.sidebar','.composer textarea','.newConversationButton','button[aria-label="Files"]']; return selectors.map(selector=>{const el=document.querySelector(selector); const r=el?.getBoundingClientRect(); return {selector,present:Boolean(el),width:r?.width,height:r?.height};});})()`);
      assert.ok(result.every(row => row.present && row.width > 0 && row.height > 0), `original controls missing: ${label}`);
      evidence.layouts.push({ label, controls: result });
    };
    const send = async text => {
      const before = new Set(deliveries.keys());
      const priorTasks = new Set(execution.store.db.prepare('SELECT request_id FROM tasks').all().map(row => row.request_id));
      await until(() => evaluate("Boolean(document.querySelector('.composer textarea:not(:disabled)'))"), 'composer enabled');
      await input(text);
      await until(() => evaluate("Boolean(document.querySelector('.composer .sendButton:not(:disabled)'))"), 'send enabled');
      const started = Date.now();
      await click('.composer .sendButton');
      let id;
      await until(async () => {
        id = [...deliveries.keys()].find(key => !before.has(key));
        if (id) return true;
        for (const task of execution.store.db.prepare('SELECT request_id,status FROM tasks').all()) {
          if (!priorTasks.has(task.request_id) && ['unknown', 'failed', 'canceled', 'delivery_expired'].includes(task.status)) throw new Error(`Execution ended without delivery: ${task.status} (${task.request_id})`);
        }
        return false;
      }, `result for ${text}`);
      const task = execution.store.request(scope, id), payload = JSON.parse(deliveries.get(id).delivery.payload);
      const deliveredMS = Date.now() - started;
      const message = execution.store.db.prepare('SELECT message_id FROM message_deliveries WHERE request_id=?').get(id);
      assert.ok(message);
      await until(() => evaluate(`(() => { const row=document.querySelector('article.message[data-message-id="${message.message_id}"]'); return Boolean(row) && row.querySelectorAll('.messageDocumentResult:not(:disabled)').length === ${payload.files.length}; })()`), 'delivered result and original file controls visible');
      evidence.requests.push({ request_id: id, input: text, conversation_id: task.conversation_id, end_to_end_duration_ms: deliveredMS, visible_result_duration_ms: Date.now() - started,
        content: payload.content, files: payload.files, clarification: payload.clarification ?? null });
      return { id, task, payload };
    };
    const upload = async (name, content) => {
      const before = new Set(execution.store.db.prepare('SELECT id FROM files').all().map(row => row.id));
      await until(() => evaluate("Boolean(document.querySelector('.documentUploadInput:not(:disabled)'))"), 'upload enabled');
      await evaluate(`(() => {const input=document.querySelector('.documentUploadInput'); const transfer=new DataTransfer(); transfer.items.add(new File([${JSON.stringify(content)}],${JSON.stringify(name)},{type:'text/markdown'})); input.files=transfer.files; input.dispatchEvent(new Event('change',{bubbles:true}));})()`);
      let file;
      await until(() => { file = execution.store.db.prepare('SELECT * FROM files').all().find(row => !before.has(row.id)); return file; }, 'durable original upload');
      await until(() => evaluate("Boolean(document.querySelector('.attachmentTray .attachmentChip'))"), 'original attachment tray');
      return file;
    };
    const newChat = async () => { await click('.newConversationButton'); await until(() => evaluate("Boolean(document.querySelector('.workbenchWelcome'))"), 'original new conversation'); };
    const bytesFor = (answer, manifest) => {
      const local = execution.store.db.prepare('SELECT file_id FROM delivery_files WHERE request_id=? AND remote_id=?').get(answer.id, manifest.id);
      assert.ok(local); return execution.store.file(scope, local.file_id).content;
    };
    await layout('welcome');
    await click('button[aria-label="Files"]');
    await until(() => evaluate("Boolean(document.querySelector('.localFilesPanel, .localFiles'))"), 'original Files panel');
    await fs.writeFile(path.join(root, 'native-conversation-files.png'), (await window.webContents.capturePage()).toPNG(), { mode: 0o600 });
    await click('button[aria-label="Files"]');
    const content = '# 验收报告\n\n项目：CONVERSATION-CONTEXT-42。\n\n目标：保留原文和文件身份，补充风险与验收方法。\n';
    const firstFile = await upload('report.md', content);
    const read = await send('读取 report.md，并概括其内容。');
    assert.ok(read.payload.content.trim());
    assert.ok(!/semantic_coverage_low|registered capability/iu.test(read.payload.content));
    evidence.cases.push('original attachment upload and first-turn document read');
    const followup = await send('概括一下发给我');
    assert.ok(followup.payload.content.trim());
    assert.ok(!/semantic_coverage_low|registered capability|workflow is blocked/iu.test(followup.payload.content));
    evidence.cases.push('second-turn business plus current-route delivery reads the historical file');
    const pure = await send('把刚才的文件发给我');
    assert.equal(pure.payload.files.length, 1, pure.payload.content);
    assert.equal(hash(bytesFor(pure, pure.payload.files[0])), pure.payload.files[0].sha256);
    evidence.cases.push('pure historical-file delivery returns verified independent output');
    await layout('history');
    await fs.writeFile(path.join(root, 'native-conversation-history.png'), (await window.webContents.capturePage()).toPNG(), { mode: 0o600 });

    // Explicit same-name matching spans earlier turns; each branch receives a
    // fresh signed question so both choices can be exercised without replay.
    for (const option of [1, 2]) {
      await newChat();
      const a = await upload('same.md', '# first\n\nSAME-NAME-ORIGINAL-A\n');
      await send('读取 same.md');
      const b = await upload('same.md', '# second\n\nSAME-NAME-ORIGINAL-B\n');
      await send('读取 same.md');
      const question = await send('把 same.md 发给我');
      assert.equal(question.payload.clarification?.schema_version, 2, question.payload.content);
      assert.equal(question.payload.clarification.options.length, 2);
      assert.ok(question.payload.clarification.options.every(choice => choice.kind === 'file'));
      const chosen = question.payload.clarification.options[option - 1];
      assert.ok([a.id, b.id].includes(chosen.file_id));
      const reply = await send(String(option));
      assert.equal(reply.payload.files.length, 1, reply.payload.content);
      assert.equal(hash(bytesFor(reply, reply.payload.files[0])), execution.store.file(scope, chosen.file_id).sha256);
      assert.equal(execution.store.db.prepare('SELECT count(*) AS n FROM clarification_selections WHERE clarification_id=?').get(question.payload.clarification.id).n, 1);
      evidence.cases.push(`same-name signed choice ${option} selects exactly its original file`);
    }
    await newChat();
    const gone = await upload('missing.md', '# missing\n\nMISSING-FILE-ORIGINAL\n');
    await send('读取 missing.md');
    await fs.unlink(path.join(execution.store.filesRoot, gone.id));
    const unavailable = await send('把刚才的文件发给我');
    assert.equal(unavailable.payload.files.length, 0);
    assert.match(unavailable.payload.content, /重新附加|attach.*again/iu);
    evidence.cases.push('missing latest file asks for reattachment without guessing an older file');
    const replaced = await upload('missing.md', '# replaced\n\nREATTACHED-NEW-IDENTITY\n');
    assert.notEqual(replaced.id, gone.id);
    const recovered = await send('把这个文件发给我');
    assert.equal(recovered.payload.files.length, 1, recovered.payload.content);
    assert.equal(hash(bytesFor(recovered, recovered.payload.files[0])), replaced.sha256);
    evidence.cases.push('explicit reattachment restores delivery with a new identity');
    const title = execution.store.list(scope).find(row => row.id === read.task.conversation_id).title;
    await newChat();
    await evaluate(`(() => {const row=[...document.querySelectorAll('.sessionSelect')].find(el=>el.textContent.includes(${JSON.stringify(title)})); if(!row)throw new Error('Original history entry missing'); row.click();})()`);
    await until(() => evaluate("document.querySelectorAll('article.message').length >= 6"), 'original conversation history');
    await layout('reopened history');
    assert.equal(execution.store.file(scope, firstFile.id).sha256, hash(Buffer.from(content)));
    evidence.source = await sourceEvidence();
    assert.equal(evidence.source.model.kind, 'production_agent'); assert.equal(evidence.source.model.mock, !realModel);
    assert.equal(evidence.source.direct_business_attempts, 0); assert.equal(evidence.direct_business_attempts, 0);
    assert.equal(evidence.source.iscp_operations['execution.submit'], evidence.requests.length);
    for (const request of evidence.requests) assert.equal(evidence.source.execution_starts[request.request_id], 1);
    assert.deepEqual(evidence.failures, [], 'native acceptance failures');
    evidence.summary = { cases: evidence.cases.length, requests: evidence.requests.length, original_ui: true, mock: !realModel,
      wrong_file_deliveries: 0, duplicate_execution: 0, direct_business_attempts: 0 };
    evidence.passed = true;
    await fs.writeFile(path.join(root, 'native-evidence.json'), JSON.stringify(evidence, null, 2), { mode: 0o600 });
    console.log(JSON.stringify({ event: 'sparkclaw_conversation_native', passed: true, summary: evidence.summary }));
    clearTimeout(deadline); app.quit();
  } catch (error) {
    evidence.passed = false; evidence.error = error.message;
    try { evidence.source = await sourceEvidence(); } catch {}
    try { await fs.writeFile(path.join(root, 'native-conversation-failed.png'), (await window.webContents.capturePage()).toPNG(), { mode: 0o600 }); } catch {}
    await fs.writeFile(path.join(root, 'native-evidence.json'), JSON.stringify(evidence, null, 2), { mode: 0o600 });
    console.error(error.stack); clearTimeout(deadline); app.exit(1);
  }
});
