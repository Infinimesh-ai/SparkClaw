import assert from "node:assert/strict";
import test from "node:test";
import { contentDigest, nativePageCommand } from "../src/browser/native-page-commands.mjs";
import { encodedBytes, RESULT_BUDGET_BYTES } from "../src/browser/host-agent.mjs";

const binding = { page_id: "page_fixture", page_generation: 1 };
// The isolated world script is fixed; this fixture only returns page states.
function page(states, { visible = true } = {}) {
  let index = 0;
  return { view: { getVisible: () => visible }, webContents: { isDestroyed: () => false,
    executeJavaScriptInIsolatedWorld: async () => states[Math.min(index++, states.length - 1)] } };
}
const state = (text, extra = {}) => ({ url: "https://example.test/", title: "Example", text, ready_state: "complete", ...extra });

test("stable wait settles inside the page and reports the snapshot content digest", async () => {
  const record = page([state("loading", { ready_state: "loading" }), state("changing"), state("final"), state("final")]);
  const result = await nativePageCommand(record, "wait", { mode: "stable_state" }, binding, () => {});
  assert.equal(result.status, "stable"); assert.equal(result.state_digest, contentDigest("Example", "final"));
  assert.equal(result.state_changed, true); assert.equal(result.surface_visible, true); assert.ok(result.observations >= 3);
});

test("stable wait without a change times out unless no change is allowed", async () => {
  const before = contentDigest("Example", "same");
  const unchanged = await nativePageCommand(page([state("same")], { visible: false }), "wait", { mode: "stable_state", before_digest: before, timeout_ms: 700 }, binding, () => {});
  assert.equal(unchanged.status, "timeout"); assert.equal(unchanged.state_changed, false); assert.equal(unchanged.surface_visible, false);
  const allowed = await nativePageCommand(page([state("same")]), "wait", { mode: "stable_state", before_digest: before, allow_no_change: true }, binding, () => {});
  assert.equal(allowed.status, "stable"); assert.equal(allowed.state_changed, false);
});

test("snapshots link to the previous snapshot of the page and stay inside the result budget", async () => {
  const controls = Array.from({ length: 120 }, (_, i) => ({ ref: `s:e${i}`, uid: `s:e${i}`, role: "link", name: `<${"n".repeat(170)}>`, label: "x".repeat(170), tag: "a", type: "", visible: true, enabled: true, clickable: true, editable: false, value_kind: "text", box: { x: 1, y: 2, width: 3, height: 4 } }));
  const record = page([{ url: "https://example.test/", title: "Example", controls, text: "body" }]);
  const first = await nativePageCommand(record, "snapshot", {}, binding, () => {});
  const second = await nativePageCommand(record, "snapshot", {}, binding, () => {});
  assert.equal(first.snapshot.previous_snapshot_id, ""); assert.equal(first.snapshot.repeated, false);
  assert.equal(second.snapshot.previous_snapshot_id, first.snapshot_id); assert.equal(second.snapshot.repeated, true);
  assert.equal(second.content_digest, contentDigest("Example", "body"));
  assert.ok(encodedBytes(second) <= RESULT_BUDGET_BYTES, "the snapshot fits the reverse response");
  assert.equal(second.snapshot.truncated, true); assert.ok(second.snapshot.controls_returned < 120); assert.equal(second.snapshot.controls_total, 120);
});

test("page reads are cut to the result budget by encoded bytes", async () => {
  const read = await nativePageCommand(page([state("&".repeat(24000))]), "read", { max_chars: 24000 }, binding, () => {});
  assert.equal(read.truncated, true); assert.ok(encodedBytes(read) <= RESULT_BUDGET_BYTES);
});
