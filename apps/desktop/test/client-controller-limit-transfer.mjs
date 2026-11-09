import assert from "node:assert/strict";

// Explicit synthetic padding AFTER the real native action/validation. This
// tests the exact cache/wire limit, not MCP's smaller output budget or UDS input.
export async function installLimitTransfer(application, mode) {
  if (mode !== "exact_limit") return;
  await application.evaluate(({ app }) => {
    const require = process.getBuiltinModule("node:module").createRequire(app.getAppPath() + "/package.json");
    const assert = require("node:assert/strict"), crypto = require("node:crypto");
    const { ControllerResults, RESULT_LIMIT } = require(app.getAppPath() + "/src/browser/controller-results.mjs");
    const { BrowserControllerForwarder } = require(app.getAppPath() + "/src/browser/controller-forwarder.mjs");
    const { ClientControllerRuntime } = require(app.getAppPath() + "/src/browser/controller-runtime.mjs");
    const { wireBytes } = require(app.getAppPath() + "/src/browser/controller-wire.mjs");
    const trace = globalThis.__chunkQualification, chunks = new Map();
    const put = ControllerResults.prototype.put;
    ControllerResults.prototype.put = function(reservation, command, payload) {
      if (command.operation !== "page.fill") return put.call(this, reservation, command, payload);
      assert.equal(trace.limit, undefined);
      const padded = structuredClone(payload), originalBytes = Buffer.byteLength(JSON.stringify(payload));
      padded.result.title = "";
      padded.result.title = "x".repeat(RESULT_LIMIT - Buffer.byteLength(JSON.stringify(padded)));
      assert.equal(Buffer.byteLength(JSON.stringify(padded)), RESULT_LIMIT);
      const descriptor = put.call(this, reservation, command, padded);
      trace.limit = { kind: "synthetic_cache_result_title_padding_not_native_MCP_output", original_bytes: originalBytes,
        total_bytes: descriptor.total_bytes, sha256: descriptor.sha256, command_id: command.command_id,
        local_task_id: command.binding.local_task_id, descriptor_ready_ms: performance.now(), expires_at: descriptor.expires_at,
        retained_cache_bytes: this.bytes, native_executes_after_descriptor: 0,
        chunk_count: 0, raw_chunk_bytes: 0, max_encoded_envelope_bytes: 0 };
      return descriptor;
    };
    const nativeRequest = ClientControllerRuntime.prototype.request;
    ClientControllerRuntime.prototype.request = function(action, ...args) {
      if (trace.limit && action === "execute") trace.limit.native_executes_after_descriptor++;
      return nativeRequest.call(this, action, ...args);
    };
    const dispatch = BrowserControllerForwarder.prototype.handleReverse;
    BrowserControllerForwarder.prototype.handleReverse = async function(request, authority) {
      const received = performance.now(), limit = trace.limit;
      if (limit && request.body?.binding?.local_task_id === limit.local_task_id &&
          request.operation === "browser.controller.execute" && request.body.operation !== "page.fill") {
        limit.follow_up_execute_received_ms ||= received;
        limit.follow_up_operation ||= request.body.operation;
      }
      const reply = await dispatch.call(this, request, authority);
      if (limit && request.body?.binding?.local_task_id === limit.local_task_id &&
          request.operation === "browser.controller.execute" && request.body.operation !== "page.fill") {
        limit.follow_up_reply = { command_id: request.body.command_id, outcome: reply.body?.outcome,
          error_code: reply.body?.error_code, local_journal_present: this.journal.rows.has(request.body.command_id) };
      }
      if (request.operation === "browser.controller.chunk" && request.body.command_id === trace.limit?.command_id) {
        const limit = trace.limit;
        assert.equal(reply.status, 200); assert.equal(chunks.has(request.body.offset), false);
        const bytes = Buffer.from(reply.body.data_base64, "base64"); assert.equal(bytes.length, request.body.length);
        chunks.set(request.body.offset, bytes);
        limit.first_chunk_received_ms ||= received; limit.last_chunk_reply_ready_ms = performance.now();
        limit.chunk_count++; limit.raw_chunk_bytes += bytes.length;
        limit.max_encoded_envelope_bytes = Math.max(limit.max_encoded_envelope_bytes,
          wireBytes({ type: "task.result", profile: request.profile, id: request.id, status: reply.status, body: reply.body }));
        if (limit.raw_chunk_bytes === limit.total_bytes) {
          const digest = crypto.createHash("sha256"); let offset = 0;
          for (const [start, bytes] of [...chunks].sort(([a], [b]) => a - b)) {
            assert.equal(start, offset); offset += bytes.length; digest.update(bytes);
          }
          limit.reassembled_sha256 = digest.digest("hex"); assert.equal(limit.reassembled_sha256, limit.sha256);
          chunks.clear();
        }
      }
      return reply;
    };
  });
}

export function verifyLimitTransfer(evidence) {
  const limit = evidence.trace.limit;
  assert.equal(limit.total_bytes, 10 << 20); assert.equal(limit.raw_chunk_bytes, limit.total_bytes);
  assert.equal(limit.chunk_count, 320); assert.equal(limit.reassembled_sha256, limit.sha256);
  assert.ok(limit.max_encoded_envelope_bytes <= 63488);
  assert.ok(limit.follow_up_execute_received_ms >= limit.last_chunk_reply_ready_ms);
  // A maximum result plus earlier live read results leaves less than the next
  // 10 MiB reservation. The fixed 20 MiB cache must refuse that execute rather
  // than evict bytes needed for retries or claim end-to-end workflow success.
  assert.ok(limit.retained_cache_bytes > 10 << 20 && limit.retained_cache_bytes <= 20 << 20);
  assert.equal(limit.follow_up_reply.outcome, "not_dispatched");
  assert.equal(limit.follow_up_reply.error_code, "browser_result_capacity");
  assert.equal(limit.follow_up_reply.local_journal_present, false);
  assert.equal(limit.native_executes_after_descriptor, 0);
  assert.equal(evidence.terminal_task.status, "delivered");
  assert.ok(evidence.assistant_messages.some(message => /workflow (?:stopped|is blocked)/iu.test(message)));
  assert.ok(evidence.trace.events.some(row => row.operation === "browser.controller.release" && row.outcome === "completed"));
  evidence.limit_timing = {
    boundary: "desktop_descriptor_ready_to_next_same_task_execute_received_includes_Gateway_verification_and_downstream_work",
    descriptor_to_next_operation_ms: limit.follow_up_execute_received_ms - limit.descriptor_ready_ms,
    chunk_serving_span_ms: limit.last_chunk_reply_ready_ms - limit.first_chunk_received_ms,
    next_operation: limit.follow_up_operation, samples: 1,
  };
  evidence.checks.exact_10_MiB_synthetic_result_transfers_before_capacity_refusal = true;
  evidence.checks.follow_up_refused_before_native_dispatch_and_release_still_completes = true;
}
