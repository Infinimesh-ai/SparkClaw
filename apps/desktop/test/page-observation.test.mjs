import assert from "node:assert/strict";
import test from "node:test";
import { capturePageObservation, observePage, OBSERVATION_SIGNALS, validateObservationOptions } from "../src/browser/page-observation.mjs";

const options = { timeout_ms: 1000, quiet_period_ms: 100, poll_interval_ms: 25, signals: OBSERVATION_SIGNALS, minimum_samples: 2 };
function fixture() {
  const sample = { title: "Test", ready_state: "complete", text: "Ready", controls: [] };
  const frame = { frameTreeNodeId: 1, processId: 100, routingId: 2, url: "https://example.test/", executeJavaScript: async () => ({ ...sample }) };
  const frames = [frame]; frame.framesInSubtree = frames;
  const record = { pageRef: "page_" + "a".repeat(32), documentRevision: 1, webContents: { mainFrame: frame, getURL: () => frame.url, isLoading: () => false } };
  return { record, frame, frames, sample, capture: signal => capturePageObservation(record, () => {}, signal) };
}

test("local wait resets quiet time for iframe content and returns compact document evidence", async () => {
  const f = fixture(); let reads = 0, changedAt;
  const child = { ...f.frame, frameTreeNodeId: 2, routingId: 3, url: "https://other.test/", executeJavaScript: async () => ({ ...f.sample, text: ++reads < 3 ? "Before" : "After" }) };
  f.frames.push(child);
  const result = await observePage({ options, assertCurrent() {}, capture: async signal => {
    const value = await f.capture(signal); if (reads === 3) changedAt = performance.now(); return value;
  } });
  assert.equal(result.status, "stable");
  assert.ok(performance.now() - changedAt >= 100);
  assert.ok(result.observations >= 6);
  assert.equal(result.frame_count, 1);
  assert.match(result.document_version, /^[a-f0-9]{64}$/);
  assert.match(result.state_digest, /^[a-f0-9]{64}$/);
  assert.equal(result.page_ref, f.record.pageRef);
  assert.ok(Buffer.byteLength(JSON.stringify(result)) < 2048);
  assert.equal(JSON.stringify(result).includes("Before"), false);
  assert.equal(Object.hasOwn(result, "html"), false);
});

test("same URL reload and native iframe replacement change document version, content changes do not", async () => {
  const f = fixture(), before = await f.capture();
  f.sample.text = "Updated";
  const content = await f.capture();
  assert.equal(content.document_version, before.document_version);
  assert.notEqual(content.state_digest, before.state_digest);
  f.record.documentRevision++;
  const reload = await f.capture();
  assert.equal(reload.url, before.url);
  assert.notEqual(reload.document_version, before.document_version);
  f.frame.routingId++;
  assert.notEqual((await f.capture()).document_version, reload.document_version);
});

test("navigation during a frame read cannot publish a mixed document", async () => {
  const f = fixture();
  f.frame.executeJavaScript = async () => { f.record.documentRevision++; return f.sample; };
  await assert.rejects(f.capture(), { code: "document_changed" });
  const result = await observePage({ capture: f.capture, options: { ...options, timeout_ms: 125 }, assertCurrent() {} });
  assert.equal(result.status, "timeout");
  assert.equal(result.observations, 0);
});

test("pending navigation cannot settle using the previous document's complete readyState", async () => {
  const f = fixture(); f.record.webContents.isLoading = () => true;
  const result = await observePage({ options: { ...options, timeout_ms: 150 }, assertCurrent() {}, capture: f.capture });
  assert.equal(result.status, "timeout");
  assert.equal(result.ready_state, "complete");
  assert.equal(result.loading, true);
  assert.equal(result.frames_ready, false);
});

test("incomplete iframe, continuous changes and missing coverage cannot settle", async t => {
  for (const kind of ["loading", "changing", "missing"]) await t.test(kind, async () => {
    const f = fixture(); let sequence = 0;
    f.frames.push({ ...f.frame, routingId: 3, executeJavaScript: async () => {
      if (kind === "missing") throw new Error("detached renderer");
      return { ...f.sample, ready_state: kind === "loading" ? "loading" : "complete", text: String(++sequence) };
    } });
    const result = await observePage({ options: { ...options, timeout_ms: 150 }, assertCurrent() {}, capture: f.capture });
    assert.equal(result.status, kind === "missing" ? "failed" : "timeout");
    assert.equal(result.reason_code, kind === "missing" ? "browser_observation_unavailable" : "browser_settle_timeout");
  });
});

test("deadline bounds a hung renderer and task revocation or caller cancellation rejects late evidence", async () => {
  const started = performance.now();
  const timedOut = await observePage({ options: { ...options, timeout_ms: 100 }, assertCurrent() {}, capture: () => new Promise(() => {}) });
  assert.equal(timedOut.status, "timeout");
  assert.ok(performance.now() - started < 700);
  const f = fixture(); let valid = true;
  await assert.rejects(observePage({ options, assertCurrent() { if (!valid) throw new Error("task revoked"); },
    capture: async () => { const result = await f.capture(); valid = false; return result; } }), /task revoked/);
  const abort = new AbortController();
  const canceled = observePage({ options, signal: abort.signal, assertCurrent() {}, capture: () => new Promise(() => {}) });
  abort.abort(new Error("caller canceled"));
  await assert.rejects(canceled, /caller canceled/);
});

test("before digest requires an actual effect unless unchanged is explicitly allowed", async () => {
  const f = fixture(), before_digest = (await f.capture()).state_digest;
  const unchanged = { ...options, timeout_ms: 150, before_digest };
  assert.equal((await observePage({ options: unchanged, capture: f.capture, assertCurrent() {} })).status, "timeout");
  const allowed = await observePage({ options: { ...unchanged, allow_no_change: true }, capture: f.capture, assertCurrent() {} });
  assert.equal(allowed.status, "stable"); assert.equal(allowed.state_changed, false);
  for (const patch of [{ timeout_ms: 20001 }, { quiet_period_ms: 0 }, { poll_interval_ms: 101 }, { signals: ["url"] }, { minimum_samples: 1 }, { script: "evil" }]) {
    assert.throws(() => validateObservationOptions({ ...options, ...patch }));
  }
});
