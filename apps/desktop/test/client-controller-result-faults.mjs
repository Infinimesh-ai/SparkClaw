import assert from "node:assert/strict";

// Explicit state-fault injection into the real packaged cache. Limits and
// browser results are unchanged; synthetic occupancy is not browser evidence.
export async function installResultFaults(application, mode) {
  if (!["cache_full", "expired_result"].includes(mode)) return;
  await application.evaluate(({ app }, mode) => {
    const require = process.getBuiltinModule("node:module").createRequire(app.getAppPath() + "/package.json");
    const assert = require("node:assert/strict"), crypto = require("node:crypto");
    const { BrowserControllerForwarder } = require(app.getAppPath() + "/src/browser/controller-forwarder.mjs");
    const { RESULT_LIMIT, RESULT_CACHE_LIMIT, RESULT_TTL_MS } = require(app.getAppPath() + "/src/browser/controller-results.mjs");
    const dispatch = BrowserControllerForwarder.prototype.handleReverse, trace = globalThis.__chunkQualification;
    BrowserControllerForwarder.prototype.handleReverse = async function(request, authority) {
      const fill = request.body?.operation === "page.fill";
      if (fill && mode === "cache_full" && !trace.capacity) {
        assert.equal(this.results.maximum, RESULT_CACHE_LIMIT);
        assert.ok(this.results.bytes < RESULT_LIMIT);
        const control = globalThis.__resultFaultControl = { forwarder: this, authority, request, buffers: [] };
        const descriptors = [], overhead = Buffer.byteLength(JSON.stringify({ fixture: "" }));
        for (const size of [RESULT_LIMIT - this.results.bytes, RESULT_LIMIT]) {
          const reservation = this.results.reserve(); assert.ok(reservation);
          const descriptor = this.results.put(reservation, { ...request.body, command_id: "occupancy-" + crypto.randomUUID() },
            { fixture: "x".repeat(size - overhead) });
          descriptors.push(descriptor); control.buffers.push(this.results.entries.get(descriptor.result_id).bytes);
        }
        assert.equal(this.results.bytes, RESULT_CACHE_LIMIT); assert.equal(this.results.reserve(), null);
        trace.capacity = { kind: "synthetic_cache_occupancy", bytes: this.results.bytes, maximum: this.results.maximum,
          descriptors, expires_at: Math.max(...descriptors.map(row => Date.parse(row.expires_at))) };
      }
      const reply = await dispatch.call(this, request, authority);
      if (fill && mode === "cache_full" && request.body.command_id === globalThis.__resultFaultControl?.request.body.command_id) {
        assert.equal(reply.body.outcome, "not_dispatched"); assert.equal(reply.body.error_code, "browser_result_capacity");
        assert.equal(this.journal.rows.has(request.body.command_id), false);
        trace.capacity.refused_command = { command_id: request.body.command_id, digest: request.body.digest,
          outcome: reply.body.outcome, error_code: reply.body.error_code, local_journal_absent: true };
      }
      if (fill && mode === "expired_result" && !trace.fault && reply.body.outcome === "completed") {
        const clock = this.results.now, entry = this.results.entries.get(reply.body.result.result_id);
        assert.ok(entry); assert.equal(this.journal.rows.get(request.body.command_id)?.state, "completed");
        globalThis.__resultFaultControl = { forwarder: this, authority, request };
        trace.fault = { kind: "expired_result", cache_clock_advance_ms: RESULT_TTL_MS + 1,
          original_expires_at: entry.descriptor.expires_at, completed_before_expiry: true };
        try { this.results.now = () => clock() + RESULT_TTL_MS + 1; this.results.expire(); }
        finally { this.results.now = clock; }
        assert.ok(entry.bytes.every(byte => byte === 0)); assert.equal(this.results.bytes, 0);
      }
      return reply;
    };
  }, mode);
}

export async function verifyCacheRecovery({ application, window, evidence, until, conversation }) {
  const trace = evidence.trace;
  assert.equal(trace.fill_calls, 0); assert.ok(trace.capacity?.refused_command.local_journal_absent);
  const pages = await application.evaluate(async ({ webContents }) => Promise.all(webContents.getAllWebContents()
    .filter(page => page.getURL() === "https://controller.fixture.test/")
    .map(async page => ({ sandbox: page.getLastWebPreferences().sandbox, attached: page.debugger.isAttached(),
      dom: await page.executeJavaScript('({value:document.querySelector("input").value,inputs:window.fixtureInputs})') }))));
  assert.ok(pages.length && pages.every(page => page.sandbox && !page.attached && page.dom.inputs === 0 && page.dom.value === ""));
  assert.ok(trace.events.some(row => row.operation === "browser.controller.release" && row.outcome === "completed"));
  assert.equal(await window.evaluate(() => fetch(window.sparkclawDesktop.gatewayBase + "/api/owner").then(response => response.status)), 200);
  evidence.checks.full_cache_refuses_before_native_write_and_local_journal = true;
  evidence.checks.full_cache_keeps_release_and_unrelated_ISCP_available = true;
  console.log("packaged_cache_waiting_for_original_60_second_expiry");
  await until(() => Date.now() > trace.capacity.expires_at + 250, "real cache expiry", 75000);
  evidence.cache_recovery = await application.evaluate(() => {
    const { forwarder, buffers } = globalThis.__resultFaultControl;
    const reservation = forwarder.results.reserve();
    const result = { checked_at: Date.now(), recovered: Boolean(reservation), bytes: forwarder.results.bytes,
      expired_buffers_zeroed: buffers.every(bytes => bytes.every(byte => byte === 0)) };
    if (reservation) forwarder.results.release(reservation);
    globalThis.__resultFaultControl.buffers = [];
    return result;
  });
  assert.ok(evidence.cache_recovery.recovered && evidence.cache_recovery.expired_buffers_zeroed);
  assert.equal(evidence.cache_recovery.bytes, 0);
  const task = await window.evaluate(async id => {
    const store = window.sparkclawClientStore, task = await store.enqueue(id, "在 https://controller.fixture.test/ 页面 Display name 字段填写 Alice。", []);
    await store.submit(task.request_id); return task;
  }, conversation.id);
  let state, approvals = 0;
  await until(async () => {
    await window.evaluate(id => window.sparkclawClientStore.reconcile(id), task.request_id);
    state = await window.evaluate(id => window.sparkclawClientStore.read(id), conversation.id);
    const current = state.tasks.find(row => row.request_id === task.request_id);
    const approval = current?.approvals?.find(row => row.state === "pending" && row.actionable);
    if (approval) {
      assert.equal(approvals++, 0); assert.equal(approval.tool, "browser.type");
      await window.evaluate(({ request, approval }) => window.sparkclawClientStore.decideApproval(request, approval.approval_id, approval.digest, "approve"), { request: task.request_id, approval });
    }
    return ["delivered", "failed", "canceled", "unknown"].includes(current?.status);
  }, "fresh approved task after cache expiry");
  assert.equal(approvals, 1); assert.equal(state.tasks.find(row => row.request_id === task.request_id).status, "delivered");
  evidence.recovery_task = { ...task, conversation_id: conversation.id };
  evidence.recovery_state = state;
  evidence.trace = await application.evaluate(() => globalThis.__chunkQualification);
  assert.equal(evidence.trace.fill_calls, 1);
  evidence.checks.fixed_wall_clock_expiry_releases_capacity_for_new_approved_task = true;
}

export async function verifyExpiredWriteReplay({ application, evidence, until }) {
  const expires = Date.parse(evidence.trace.fault.original_expires_at);
  console.log("packaged_expiry_waiting_for_original_command_slot_TTL");
  await until(() => Date.now() > expires + 1000, "real command-slot expiry", 75000);
  evidence.expired_replay = await application.evaluate(async () => {
    const { forwarder, request, authority } = globalThis.__resultFaultControl;
    const slot_expired = !forwarder.flights.has(request.body.command_id);
    const reply = await globalThis.__chunkRawDispatch.call(forwarder, request, authority);
    return { slot_expired, outcome: reply.body.outcome, error_code: reply.body.error_code,
      payload_absent: !reply.body.payload && !reply.body.result, fill_calls: globalThis.__chunkQualification.fill_calls,
      journal_state: forwarder.journal.rows.get(request.body.command_id)?.state };
  });
  assert.equal(evidence.expired_replay.slot_expired, true);
  assert.equal(evidence.expired_replay.outcome, "completed");
  assert.equal(evidence.expired_replay.error_code, "browser_result_unavailable");
  assert.equal(evidence.expired_replay.payload_absent, true);
  assert.equal(evidence.expired_replay.fill_calls, 1); assert.equal(evidence.expired_replay.journal_state, "completed");
  evidence.checks.expired_payload_and_command_slot_cannot_replay_durable_completed_write = true;
}
