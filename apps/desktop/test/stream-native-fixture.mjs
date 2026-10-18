import { app, BrowserWindow, net } from "electron";
import assert from "node:assert/strict";
import crypto from "node:crypto";
import fs from "node:fs/promises";
import path from "node:path";
import http from "node:http";
import { performance } from "node:perf_hooks";
import { ExecutionClient } from "../src/main/execution-client.mjs";
import { ClientStore } from "../src/main/client-store.mjs";

const root = process.env.SPARKCLAW_STREAM_FIXTURE_ROOT;
const samples = Number(process.env.SPARKCLAW_STREAM_FIXTURE_SAMPLES);
const repetitions = Number(process.env.SPARKCLAW_STREAM_FIXTURE_REPETITIONS || "1");
const mode = process.env.SPARKCLAW_STREAM_FIXTURE_MODE || "controlled";
const realModel = mode === "real-model";
if (!root?.startsWith("/private/tmp/") || !Number.isSafeInteger(samples) || ![1, 10].includes(repetitions)) throw new Error("Private stream fixture environment required");
const expectedChars = 600 * repetitions;
process.env.SPARKCLAW_DESKTOP_ISCP_LOCAL_TEST = "1";
process.env.SPARKCLAW_DESKTOP_ISCP_CONFIG = path.join(root, "desktop-profile.json");
process.env.SPARKCLAW_DESKTOP_USER_DATA_DIR = path.join(root, "profile");
let execution, window, captureRows;
const evidence = { schema_version: 1, event: "sparkclaw_stream_native", mode, production_entry: true, production_agent: realModel, production_data: false, platform: process.platform, architecture: process.arch, electron: process.versions.electron, samples: [], direct_business_attempts: 0, commits: [], connection_state_events: [] };
const originalStart = ExecutionClient.prototype.start;
ExecutionClient.prototype.start = function (...args) {
  execution = this;
  if (!this.auth.__streamFixtureObserved) {
    this.auth.__streamFixtureObserved = true;
    const originalChange = this.auth.onChange;
    const record = () => {
      const session = this.auth.transport?.capabilities?.session_id;
      const event = { state: this.auth.status.state, generation: this.auth.generation, session_hash: session ? crypto.createHash("sha256").update(session).digest("hex") : null };
      const previous = evidence.connection_state_events.at(-1);
      if (!previous || ["state", "generation", "session_hash"].some(key => event[key] !== previous[key])) evidence.connection_state_events.push({ ...event, main_unix_ms: performance.timeOrigin + performance.now() });
    };
    this.auth.onChange = (...values) => { record(); return originalChange.apply(this.auth, values); };
    record();
  }
  return originalStart.apply(this, args);
};
const originalCommit = ClientStore.prototype.commitDelivery;
ClientStore.prototype.commitDelivery = function (scope, delivery, ...args) {
  const result = originalCommit.call(this, scope, delivery, ...args);
  const content = JSON.parse(delivery.payload).content;
  evidence.commits.push({ request_id: delivery.request_id, main_unix_ms: performance.timeOrigin + performance.now(), content_digest: crypto.createHash("sha256").update(content).digest("hex"), normalized_content_digest: crypto.createHash("sha256").update(content.replace(/\s+/gu, "")).digest("hex") });
  return result;
};
const blocked = () => { evidence.direct_business_attempts++; throw new Error("Direct business HTTP forbidden by native stream fixture"); };
globalThis.fetch = blocked;
const originalFetch = net.fetch.bind(net);
net.fetch = (...args) => /^https?:/u.test(String(args[0]?.url || args[0])) ? blocked() : originalFetch(...args);
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
async function until(test, label, timeout = 30000) { const end = Date.now() + timeout; while (Date.now() < end) { if (await test()) return; await delay(50); } throw new Error(`Timed out: ${label}`); }
const fixtureRead = route => new Promise((resolve, reject) => {
  assert.ok(["clock", "evidence"].includes(route));
  const request = http.get(`${process.env.SPARKCLAW_STREAM_FIXTURE_ORIGIN}/fixture/${route}`, { headers: { Authorization: `Bearer ${process.env.SPARKCLAW_STREAM_FIXTURE_TOKEN}` }, timeout: 3000 }, response => {
    let data = ""; response.setEncoding("utf8"); response.on("data", part => { data += part; }); response.on("end", () => { try { if (response.statusCode !== 200) throw new Error("Fixture read failed"); resolve(JSON.parse(data)); } catch (error) { reject(error); } });
  });
  request.on("timeout", () => request.destroy(new Error("Fixture evidence timeout"))); request.on("error", reject);
});
const lifetime = setTimeout(() => { console.error("Native stream qualification deadline exceeded"); app.exit(1); }, (realModel ? 290 : samples * 13 + 50) * 1000);
await import("../src/main/main.mjs");
void app.whenReady().then(async () => {
  try {
    await until(async () => { window = BrowserWindow.getAllWindows()[0]; return window && !window.webContents.isLoading() && await window.webContents.executeJavaScript("Boolean(window.sparkclawClientStore && document.querySelector('.localWorkbench'))"); }, "original workbench");
    const evaluate = source => window.webContents.executeJavaScript(source, true);
    captureRows = () => evaluate("Object.fromEntries(Object.entries(window.__streamFixture?.rows || {}).map(([id,{element,...row}]) => [id,row]))");
    await until(async () => execution && (await evaluate("window.sparkclawDesktop.localConnection()")).state === "connected", "matched ISCP connection");
    evidence.connection = { state: "connected", stream_schema_version: execution.auth.transport.capabilities.stream_schema_version, helper_stream_schema_version: execution.auth.transport.streamSchemaVersion };
    assert.equal(evidence.connection.stream_schema_version, 1);
    assert.equal(evidence.connection.helper_stream_schema_version, 1);
    window.show(); window.focus();
    window.webContents.session.webRequest.onBeforeRequest({ urls: ["http://*/*", "https://*/*", "ws://*/*", "wss://*/*"] }, (_request, callback) => { evidence.direct_business_attempts++; callback({ cancel: true }); });
    const clock = async () => {
      const checks = [];
      for (let n = 0; n < 8; n++) {
        const before = await evaluate("performance.timeOrigin + performance.now()");
        const server = await fixtureRead("clock");
        const after = await evaluate("performance.timeOrigin + performance.now()");
        checks.push({ offset_low_ms: before - server.unix_ms, offset_high_ms: after - server.unix_ms, uncertainty_ms: after - before });
      }
      return checks.sort((a, b) => a.uncertainty_ms - b.uncertainty_ms)[0];
    };
    const mainClock = async () => {
      const checks = [];
      for (let n = 0; n < 8; n++) {
        const before = await evaluate("performance.timeOrigin + performance.now()");
        const main = performance.timeOrigin + performance.now();
        const after = await evaluate("performance.timeOrigin + performance.now()");
        checks.push({ offset_low_ms: before - main, offset_high_ms: after - main, uncertainty_ms: after - before });
      }
      return checks.sort((a, b) => a.uncertainty_ms - b.uncertainty_ms)[0];
    };
    evidence.clock_before = await clock();
    evidence.main_clock_before = await mainClock();
    await evaluate(`(() => {
      window.__streamFixture = {rows: {}, stop: false, active_id: undefined, harness_scrolls: 0};
      const tailInView = element => {
        const content = element.querySelector('.messageContent');
        if (!content?.textContent) return false;
        const walker = document.createTreeWalker(content, NodeFilter.SHOW_TEXT); let last, node;
        while (node = walker.nextNode()) if (node.textContent.length) last = node;
        if (!last) return false;
        const range = document.createRange(); range.setStart(last, Math.max(0, last.textContent.length - 1)); range.setEnd(last, last.textContent.length);
        const tail = range.getBoundingClientRect(); const view = element.closest('.localHistory').getBoundingClientRect();
        return tail.height > 0 && tail.top >= Math.max(0, view.top) && tail.bottom <= Math.min(innerHeight, view.bottom);
      };
      function observe() {
        if (window.__streamFixture.stop) return;
        const groups = new Map();
        for (const element of document.querySelectorAll('article.message.assistant[data-scroll-anchor^="execution:"]')) {
          const id = element.dataset.scrollAnchor.slice('execution:'.length);
          const list = groups.get(id) || []; list.push(element); groups.set(id, list);
        }
        for (const [id, elements] of groups) {
          const element = elements[0];
          const row = window.__streamFixture.rows[id] ||= {observations: [], remounts: 0, remount_events: [], blanks: 0, missing_frames: 0, duplicates: 0, last: -1, element};
          if (row.element !== element) { row.remounts++; row.remount_events.push({renderer_unix_ms: performance.timeOrigin + performance.now(), message_id: element.dataset.messageId}); row.element = element; }
          if (elements.length !== 1) row.duplicates++;
          const chars = [...(element.querySelector('.messageContent')?.textContent || '')].length;
          const durable = !element.dataset.messageId.startsWith('progress:');
          if (id === window.__streamFixture.active_id && chars > 0 && !tailInView(element)) {
            element.scrollIntoView({block:'end', behavior:'instant'}); window.__streamFixture.harness_scrolls++;
          }
          if (chars === 0 && row.last > 0) row.blanks++;
          if (chars !== row.last || durable !== row.durable) {
            row.last = chars; row.durable = durable;
            requestAnimationFrame(() => row.observations.push({chars, durable, tail_in_view: chars > 0 && tailInView(element), renderer_unix_ms: performance.timeOrigin + performance.now()}));
          }
        }
        for (const [id, row] of Object.entries(window.__streamFixture.rows)) {
          if (row.last > 0 && !row.durable && !groups.has(id)) row.missing_frames++;
        }
        requestAnimationFrame(observe);
      }
      requestAnimationFrame(observe);
    })()`);
    for (let index = 0; index < samples; index++) {
      if (index > 0) {
        await evaluate("document.querySelector('.newConversationButton').click()");
        await until(() => evaluate("document.querySelectorAll('article.message').length === 0"), "new conversation through original UI");
      }
      await until(() => evaluate("Boolean(document.querySelector('.composer textarea:not(:disabled)'))"), "enabled original composer");
      const prompt = realModel ? "请用中文写一篇约600字的雨后公园散文。只输出纯文本正文，不要标题、Markdown、代码块或工具调用。" : `Controlled stream sample ${index + 1}`;
      await evaluate(`(() => { const input = document.querySelector('.composer textarea'); Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set.call(input, ${JSON.stringify(prompt)}); input.dispatchEvent(new Event('input', {bubbles:true})); })()`);
      await until(() => evaluate("Boolean(document.querySelector('.composer .sendButton:not(:disabled)'))"), "original send button");
      const submittedMS = await evaluate("(() => { const at = performance.timeOrigin + performance.now(); document.querySelector('.composer .sendButton').click(); return at; })()");
      let requestID;
      await until(async () => {
        const ids = await evaluate("Object.keys(window.__streamFixture.rows)");
        requestID = ids.find(id => !evidence.samples.some(sample => sample.request_id === id)); return requestID;
      }, "original task row");
      await evaluate(`window.__streamFixture.active_id = ${JSON.stringify(requestID)}`);
      if (mode === "recovery") {
        await until(() => evaluate(`window.__streamFixture.rows[${JSON.stringify(requestID)}]?.last >= 240`), "240 visible characters before helper interruption");
        const injectedMS = await evaluate("performance.timeOrigin + performance.now()");
        const child = execution.auth.transport.child; assert.ok(child?.pid);
        evidence.recovery = { request_id: requestID, injected_renderer_unix_ms: injectedMS, helper_process_terminated: true };
        child.kill("SIGKILL");
        await until(() => evaluate(`window.__streamFixture.rows[${JSON.stringify(requestID)}]?.observations.some(row => row.chars > 240 && row.renderer_unix_ms > ${injectedMS})`), "original stream recovery", 30000);
      }
      await until(() => {
        const state = execution.store.request(execution.getIdentity(), requestID).status;
        if (["failed", "unknown", "delivery_expired"].includes(state)) throw new Error(`Original execution ended ${state} before delivery`);
        return evaluate(`window.__streamFixture.rows[${JSON.stringify(requestID)}]?.observations.some(row => row.durable && ${realModel ? "row.chars > 0" : `row.chars === ${expectedChars}`})`);
      }, "durable same-position answer", realModel ? 240000 : 20000);
      const sample = await evaluate(`(async () => { const {element, ...row} = window.__streamFixture.rows[${JSON.stringify(requestID)}]; const content = element.querySelector('.messageContent')?.textContent || ''; const digest = async text => [...new Uint8Array(await crypto.subtle.digest('SHA-256', new TextEncoder().encode(text)))].map(byte => byte.toString(16).padStart(2, '0')).join(''); row.content_digest = await digest(content); row.normalized_content_digest = await digest(content.replace(/\\s+/gu, '')); return row; })()`);
      evidence.samples.push({ request_id: requestID, submitted_renderer_unix_ms: submittedMS, ...sample });
      assert.equal(sample.remounts, 0); if (mode !== "recovery") assert.equal(sample.blanks, 0); assert.equal(sample.duplicates, 0); assert.equal(sample.missing_frames, 0);
      assert.ok(sample.observations.filter(row => row.chars > 0).every(row => row.tail_in_view), "Growing answer tail must be in the original viewport");
      console.log(JSON.stringify({ event: "stream_native_sample", sample: index + 1, request_id: requestID, observations: sample.observations.length }));
    }
    await delay(100);
    evidence.clock_after = await clock();
    evidence.main_clock_after = await mainClock();
    evidence.final_rows = await captureRows();
    evidence.viewport = { separate_conversations: true, harness_scrolls: await evaluate("window.__streamFixture.harness_scrolls"), method: "original UI new conversation per sample; normal scrollIntoView follows answer tail when needed; does not qualify automatic scrolling" };
    for (const row of Object.values(evidence.final_rows)) {
      assert.equal(row.remounts, 0); if (mode !== "recovery") assert.equal(row.blanks, 0); assert.equal(row.duplicates, 0); assert.equal(row.missing_frames, 0);
    }
    evidence.source = await fixtureRead("evidence");
    assert.equal(evidence.source.direct_business_attempts, 0);
    assert.equal(evidence.direct_business_attempts, 0);
    assert.equal(evidence.source.iscp_operations["execution.submit"], samples);
    assert.equal(Object.keys(evidence.source.execution_starts).length, samples);
    assert.ok(Object.values(evidence.source.execution_starts).every(count => count === 1), "Every original request must execute only once");
    if (mode === "recovery") assert.ok(evidence.source.iscp_operations["execution.progress.stream"] >= 2);
    else assert.equal(evidence.source.iscp_operations["execution.progress.stream"], samples);
    assert.equal(evidence.source.iscp_operations["execution.progress"] || 0, 0);
    const low = Math.min(evidence.clock_before.offset_low_ms, evidence.clock_after.offset_low_ms);
    const high = Math.max(evidence.clock_before.offset_high_ms, evidence.clock_after.offset_high_ms);
    const mainLow = Math.min(evidence.main_clock_before.offset_low_ms, evidence.main_clock_after.offset_low_ms);
    const mainHigh = Math.max(evidence.main_clock_before.offset_high_ms, evidence.main_clock_after.offset_high_ms);
    const delays = [], first = [], visibleFractions = [], growthGaps = [], handoffs = [];
    for (const sample of evidence.samples) {
      const source = evidence.source.runs[sample.request_id]; assert.ok(source);
      const commit = evidence.commits.find(row => row.request_id === sample.request_id); assert.ok(commit);
      const durable = sample.observations.find(row => row.durable); assert.ok(durable);
      sample.commit_to_handoff_upper_ms = durable.renderer_unix_ms - commit.main_unix_ms - mainLow;
      handoffs.push(sample.commit_to_handoff_upper_ms);
      if (realModel) {
        assert.ok(source.final_bytes > 0 && !source.failed);
        assert.equal(commit.content_digest, source.final_digest);
        assert.equal(sample.normalized_content_digest, commit.normalized_content_digest);
        const visible = sample.observations.filter(row => row.chars > 0 && !row.durable);
        assert.ok(new Set(visible.map(row => row.chars)).size > 1, "Real Agent answer must grow before durable final delivery");
        assert.ok(visible[0].renderer_unix_ms < source.completed_unix_ms + low, "Real answer must appear before workflow completes");
        sample.submit_to_first_visible_ms = visible[0].renderer_unix_ms - sample.submitted_renderer_unix_ms;
        sample.workflow_start_to_first_visible_upper_ms = visible[0].renderer_unix_ms - source.started_unix_ms - low;
        sample.live_text_observations = visible.length;
        continue;
      }
      assert.equal(source.batches.length, 100);
      let visibleBeforeDone = 0;
      sample.delays_upper_ms = source.batches.map(batch => {
        const paint = sample.observations.find(row => row.chars >= batch.batch * 6 * repetitions); assert.ok(paint);
        if (paint.renderer_unix_ms < source.completed_unix_ms + low) visibleBeforeDone++;
        const delayUpper = paint.renderer_unix_ms - batch.produced_unix_ms - low;
        delays.push(delayUpper); if (batch.batch === 1) first.push(delayUpper);
        return delayUpper;
      });
      sample.visible_before_done = visibleBeforeDone / 100; visibleFractions.push(sample.visible_before_done);
      const growth = sample.observations.filter((row, index, rows) => row.chars > 0 && (!index || row.chars > rows[index - 1].chars));
      sample.max_growth_gap_ms = Math.max(0, ...growth.slice(1).map((row, index) => row.renderer_unix_ms - growth[index].renderer_unix_ms));
      growthGaps.push(sample.max_growth_gap_ms);
      assert.equal(sample.content_digest, source.batches.at(-1).digest);
      assert.equal(commit.content_digest, sample.content_digest);
      assert.ok(sample.visible_before_done >= 0.9, "At least 90% of answer must reach original renderer before done");
      if (mode !== "recovery") assert.ok(sample.max_growth_gap_ms < 1000, "Original answer must not stall for one second while batches continue");
      else {
        const resumed = sample.observations.find(row => row.chars > 240 && row.renderer_unix_ms > evidence.recovery.injected_renderer_unix_ms);
        evidence.recovery.visible_recovery_ms = resumed.renderer_unix_ms - evidence.recovery.injected_renderer_unix_ms;
        evidence.recovery.authority_replacement_blank_events = sample.blanks;
        assert.ok(evidence.connection_state_events.some(event => event.generation > evidence.connection_state_events[0].generation), "Helper replacement must create a new auth generation");
        assert.ok(evidence.recovery.visible_recovery_ms <= 3000, "Interrupted original answer must recover within three seconds");
      }
    }
    const percentile = (values, p) => [...values].sort((a, b) => a - b)[Math.ceil(values.length * p) - 1];
    evidence.summary = { samples, chars_per_sample: expectedChars, batches: delays.length, observation: "visible original DOM followed by next animation frame; compositor paint proxy", clock_error_bound_ms: high - low, main_clock_error_bound_ms: mainHigh - mainLow, first_upper_ms: Math.max(...first), p95_upper_ms: percentile(delays, 0.95), p99_upper_ms: percentile(delays, 0.99), max_growth_gap_ms: Math.max(...growthGaps), max_commit_to_handoff_upper_ms: Math.max(...handoffs), min_visible_before_done: Math.min(...visibleFractions), remounts: 0, blanks: 0, missing_frames: 0, duplicates: 0, stream_subscriptions: evidence.source.iscp_operations["execution.progress.stream"] };
    if (realModel) evidence.summary = { samples, production_agent: true, mock: false, observation: "visible original DOM followed by next animation frame; compositor paint proxy", clock_error_bound_ms: high - low, main_clock_error_bound_ms: mainHigh - mainLow, submit_to_first_visible_ms: evidence.samples[0].submit_to_first_visible_ms, live_text_observations: evidence.samples[0].live_text_observations, final_visible_chars: evidence.samples[0].last, max_commit_to_handoff_upper_ms: Math.max(...handoffs), remounts: 0, blanks: 0, missing_frames: 0, duplicates: 0, stream_subscriptions: evidence.source.iscp_operations["execution.progress.stream"] };
    else if (mode === "recovery") { evidence.summary.recovery_ms = evidence.recovery.visible_recovery_ms; evidence.summary.blanks = evidence.recovery.authority_replacement_blank_events; }
    else {
      assert.ok(evidence.summary.first_upper_ms <= 500);
      assert.ok(evidence.summary.p95_upper_ms <= 300);
      assert.ok(evidence.summary.p99_upper_ms <= 500);
    }
    await until(() => { const last = evidence.commits.at(-1); return last && execution.store.receipt(execution.getIdentity(), last.request_id)?.acknowledged === 1; }, "durable delivery ACK");
    evidence.receipts = evidence.samples.map(sample => execution.store.receipt(execution.getIdentity(), sample.request_id));
    assert.ok(evidence.receipts.every(receipt => receipt?.durable && receipt.acknowledged === 1));
    evidence.source = await fixtureRead("evidence");
    const screenshot = await window.webContents.capturePage();
    await fs.writeFile(path.join(root, "native-stream.png"), screenshot.toPNG(), { mode: 0o600 });
    evidence.passed = true;
  } catch (error) {
    evidence.passed = false; evidence.error = error.message; console.error(error.stack);
    try { evidence.final_rows = await captureRows?.(); } catch { /* Preserve the initial failure. */ }
    try { evidence.source ||= await fixtureRead("evidence"); } catch { /* Preserve the initial failure. */ }
    try { await fs.writeFile(path.join(root, "native-stream-failed.png"), (await window.webContents.capturePage()).toPNG(), {mode: 0o600}); } catch { /* May fail during process teardown. */ }
  }
  finally {
    clearTimeout(lifetime);
    execution?.close(); execution?.auth?.transport?.close();
    await fs.writeFile(path.join(root, "native-evidence.json"), JSON.stringify(evidence, null, 2), { mode: 0o600 });
    console.log(JSON.stringify({ event: "stream_native_done", passed: evidence.passed, summary: evidence.summary, error: evidence.error }));
    window?.destroy(); app.exit(evidence.passed ? 0 : 1);
  }
});
