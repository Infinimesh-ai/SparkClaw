// Exercise the original native composer, ClientStore, helper and real backend.
// Run sequentially against an explicitly selected profile; never replay work.
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import assert from 'node:assert/strict';
import { DatabaseSync } from 'node:sqlite';
import { parseArgs } from 'node:util';
import { _electron } from '../tools/browser-controller/node_modules/playwright/index.mjs';

const { values } = parseArgs({ options: {
  executable: { type: 'string' }, 'user-data': { type: 'string' }, output: { type: 'string' },
  label: { type: 'string' }, samples: { type: 'string', default: '5' },
  'long-reply': { type: 'boolean', default: false },
} });
for (const key of ['executable', 'user-data', 'output']) assert.ok(path.isAbsolute(values[key] || ''), `${key} must be absolute`);
assert.match(values.label || '', /^[a-z0-9-]{1,50}$/u);
const samples = Number(values.samples); assert.ok(Number.isInteger(samples) && samples >= 1 && samples <= 20);
const output = values.output; fs.mkdirSync(output, { recursive: true, mode: 0o700 });
assert.equal(fs.statSync(output).mode & 0o077, 0);
const traces = path.join(output, 'trace'); fs.mkdirSync(traces, { mode: 0o700 });
let db;
const originalIDs = new Set();
const report = { schema_version: 1, label: values.label, measured_at: new Date().toISOString(), samples: [],
  boundary: 'original native composer to local durable result, visible text and delivery ACK; sequential independent conversations',
  trace_directory: traces };
const save = () => fs.writeFileSync(path.join(output, 'benchmark.json'), JSON.stringify(report, null, 2), { mode: 0o600 });
const wait = async (fn, timeout = 180000) => {
  const end = performance.now() + timeout;
  do { const value = await fn(); if (value) return value; await new Promise(resolve => setTimeout(resolve, 50)); } while (performance.now() < end);
  throw new Error('Native benchmark timed out; preserve original request for reconciliation');
};
const env = { ...process.env, SPARKCLAW_DESKTOP_USER_DATA_DIR: values['user-data'], SPARKCLAW_PRIVATE_LATENCY_TRACE_DIR: traces };
for (const key of ['ELECTRON_RUN_AS_NODE', 'SPARKCLAW_DESKTOP_ISCP_CONFIG', 'SPARKCLAW_DESKTOP_ISCP_LOCAL_TEST', 'SPARKCLAW_PRIVATE_ISCP_READ_DIAGNOSTICS']) delete env[key];
let app, page;
try {
  app = await _electron.launch({ executablePath: values.executable, args: [], env, timeout: 30000 });
  await app.firstWindow({ timeout: 120000 });
  page = await wait(() => app.windows().find(p => p.url().startsWith('sparkclaw-app://workbench')));
  const connected = await wait(async () => { const s = await page.evaluate(() => window.sparkclawDesktop.localConnection()); return s.state === 'connected' && s; });
  db = new DatabaseSync(path.join(values['user-data'], 'workbench/client.sqlite'), { readOnly: true });
  assert.equal(db.prepare('PRAGMA quick_check').get().quick_check, 'ok');
  assert.equal(db.prepare("SELECT count(*) AS n FROM tasks WHERE status IN ('submission_pending','accepted','running','cancel_pending','saved')").get().n, 0, 'Let existing executions drain first');
  for (const row of db.prepare('SELECT request_id FROM tasks').all()) originalIDs.add(row.request_id);
  report.original_task_count = originalIDs.size;
  report.identity = { deployment_id: connected.deployment_id, owner_id: connected.owner_id, client_id: connected.client_id };
  console.log(JSON.stringify({ label: values.label, stage: 'connected' }));
  const prompts = Array.from({ length: samples }, () => ({ kind: 'greeting', text: '你好' }));
  if (values['long-reply']) prompts.push({ kind: 'long', text: '请直接用中文写一篇约600字的短文，介绍雨后公园的景色。不需要搜索、工具或文件，只输出短文。' });
  for (const [index, prompt] of prompts.entries()) {
    await page.locator('.newConversationButton').click();
    await page.locator('.composer textarea').fill(prompt.text);
    await page.evaluate(() => {
      window.__iscpBenchmark?.observer?.disconnect();
      const state = window.__iscpBenchmark = { start: null, first: null, changes: [] };
      document.querySelector('.composer').addEventListener('submit', () => { state.start = performance.now(); state.start_wall = Date.now(); }, { once: true, capture: true });
      state.observer = new MutationObserver(() => {
        if (state.start === null) return;
        const content = [...document.querySelectorAll('.message.assistant .messageContent')].map(node => node.textContent).join('');
        if (!content.trim() || content === state.last) return;
        state.last = content;
        state.first ??= performance.now() - state.start;
        state.changes.push({ elapsed_ms: performance.now() - state.start, characters: content.length });
      });
      state.observer.observe(document.body, { childList: true, subtree: true, characterData: true });
    });
    await page.locator('.composer .sendButton').click();
    const task = await wait(() => db.prepare('SELECT request_id,conversation_id,status,created_at FROM tasks ORDER BY rowid DESC').all().find(row => !originalIDs.has(row.request_id)));
    originalIDs.add(task.request_id);
    const sample = { index: index + 1, kind: prompt.kind, request_id: task.request_id, conversation_id: task.conversation_id, created_at: task.created_at };
    report.samples.push(sample); save();
    const result = await wait(() => db.prepare("SELECT content,created_at FROM messages WHERE conversation_id=? AND role='assistant' ORDER BY rowid DESC LIMIT 1").get(task.conversation_id));
    sample.complete_saved_ms = Date.parse(result.created_at) - Date.parse(task.created_at);
    sample.result_sha256 = crypto.createHash('sha256').update(result.content).digest('hex'); sample.result_characters = result.content.length;
    await wait(async () => (await page.evaluate(() => window.__iscpBenchmark.first)) !== null);
    await wait(() => db.prepare('SELECT status FROM tasks WHERE request_id=?').get(task.request_id).status === 'delivered');
    const delivered = db.prepare('SELECT status,updated_at FROM tasks WHERE request_id=?').get(task.request_id);
    sample.ack_complete_ms = Date.parse(delivered.updated_at) - Date.parse(task.created_at); sample.state = delivered.status;
    const visual = await page.evaluate(() => { const s = window.__iscpBenchmark; s.observer.disconnect(); return { first_visible_ms: s.first, changes: s.changes }; });
    Object.assign(sample, visual);
    await page.screenshot({ path: path.join(output, `${values.label}-${index + 1}.png`) });
    save(); console.log(JSON.stringify({ label: values.label, ...sample }));
  }
  report.completed = true; save();
} catch (error) {
  report.completed = false; report.failure = error?.name || 'Error'; save();
  if (page) await page.screenshot({ path: path.join(output, 'failure.png') }).catch(() => {});
  throw error;
} finally {
  db?.close();
  if (app) { const timer = setTimeout(() => app.process().kill('SIGTERM'), 15000); try { await app.close(); } finally { clearTimeout(timer); } }
}
