import assert from "node:assert/strict";
import test from "node:test";
import crypto from "node:crypto";
import fs from "node:fs";
import { ExecutionStreamProjection, streamDigest } from "../src/main/execution-stream-projection.mjs";
const requestID = crypto.randomUUID(), inputDigest = "a".repeat(64), epoch = crypto.randomUUID(), generation = crypto.randomUUID();
const common = { request_id: requestID, input_digest: inputDigest, epoch, generation, revision: 1 };
function fixture() {
  let now = 0;
  const projection = new ExecutionStreamProjection({ requestID, inputDigest, now: () => now });
  const id = crypto.randomUUID(); projection.open(id);
  const frame = (body, stream_seq = 1) => ({ type: "iscp.application.stream.v1", schema_version: 1, subscription_id: id, stream_seq, body: { ...common, ...body } });
  const delta = (text, revision = 1, prefix = "") => ({ kind: "delta", revision, text, from_offset: Buffer.byteLength(prefix), to_offset: Buffer.byteLength(prefix + text), content_digest: streamDigest(prefix + text) });
  return { projection, id, frame, delta, tick: value => { now += value; } };
}
test("append uses raw UTF-8, permits ordinary brace/Markdown prefixes and verifies complete cumulative digest", () => {
  const f = fixture(), a = '{中文\n```js\n', b = 'const x = "🙂";\n```';
  assert.equal(f.projection.event(f.frame(f.delta(a))), true);
  f.projection.event(f.frame(f.delta(b, 2, a), 2));
  assert.equal(f.projection.read().content, a + b);
  assert.deepEqual(f.projection.cursor(), { epoch, generation, revision: 2, offset: Buffer.byteLength(a + b), content_digest: streamDigest(a + b) });
});
test("snapshot candidate never publishes partially and survives same-source warm reopen with the verified cursor", () => {
  const f = fixture(); f.projection.event(f.frame(f.delta("old")));
  f.projection.open(f.id); const snapshot_id = crypto.randomUUID(), text = "old中文🙂more";
  const base = { snapshot_id, revision: 3, total_bytes: Buffer.byteLength(text), content_digest: streamDigest(text) };
  f.projection.event(f.frame({ kind: "snapshot_begin", ...base }));
  f.projection.event(f.frame({ kind: "snapshot_chunk", snapshot_id, revision: 3, offset: 0, text: "old中文" }, 2));
  assert.equal(f.projection.read().content, "old");
  f.projection.event(f.frame({ kind: "snapshot_chunk", snapshot_id, revision: 3, offset: Buffer.byteLength("old中文"), text: "🙂more" }, 3));
  assert.equal(f.projection.read().content, "old");
  f.projection.event(f.frame({ kind: "snapshot_end", ...base }, 4));
  assert.equal(f.projection.read().content, text);
  f.projection.event(f.frame(f.delta("!", 4, text), 5)); assert.equal(f.projection.read().content, text + "!");
});
test("out-of-order frames wait for contiguous sequence and a gap expires at two seconds", () => {
  const f = fixture(); const second = f.frame(f.delta("二", 2, "一"), 2);
  f.projection.event(second); assert.equal(f.projection.read(), undefined);
  f.tick(1999); assert.equal(f.projection.gapExpired, false); f.tick(1); assert.equal(f.projection.gapExpired, true);
  f.projection.event(f.frame(f.delta("一"))); assert.equal(f.projection.read().content, "一二"); assert.equal(f.projection.gapExpired, false);
  assert.equal(f.projection.event(second), false);
  assert.throws(() => f.projection.event({ ...second, body: { ...second.body, text: "三" } }), /duplicate/);
  f.tick(30001); assert.throws(() => f.projection.event(second), /unproven/);
});
test("early completion waits for exactly its tail sequence; final close fences every later frame and control", () => {
  const f = fixture(); f.projection.complete({ schema_version: 1, last_stream_seq: 2, reason: "terminal" });
  assert.equal(f.projection.settled, false);
  f.projection.event(f.frame(f.delta("a"))); f.projection.event(f.frame(f.delta("b", 2, "a"), 2));
  assert.equal(f.projection.settled, true); f.projection.close();
  assert.equal(f.projection.event(f.frame(f.delta("c", 3, "ab"), 3)), false); assert.equal(f.projection.read(), undefined);
});
test("invalidation matches subscription and source; reset discards candidate immediately without waiting for text gaps", () => {
  const f = fixture(); f.projection.event(f.frame(f.delta("visible")));
  const control = { type: "iscp.application.stream.control.v1", schema_version: 1, subscription_id: f.id, action: "invalidate", body: { ...common, kind: "invalidate", reason: "reset" } };
  assert.equal(f.projection.event({ ...control, subscription_id: crypto.randomUUID() }), false);
  assert.equal(f.projection.event({ ...control, body: { ...control.body, generation: crypto.randomUUID() } }), false);
  assert.equal(f.projection.read().content, "visible");
  f.projection.event(control); assert.equal(f.projection.read(), undefined); assert.equal(f.projection.settled, true);
});
for (const [label, mutate] of [
  ["unknown field", b => ({ ...b, extra: true })], ["version", b => ({ ...b, epoch: "epoch" })],
  ["UTF-8", b => ({ ...b, text: "\ud800" })], ["digest", b => ({ ...b, content_digest: "0".repeat(64) })],
  ["byte boundary", b => ({ ...b, from_offset: 1 })], ["unsafe integer", b => ({ ...b, revision: Number.MAX_SAFE_INTEGER + 1 })],
  ["task", b => ({ ...b, request_id: crypto.randomUUID() })], ["UUID version", b => ({ ...b, generation: "11111111-1111-7111-8111-111111111111" })],
]) test(`invalid ${label} never advances receive sequence or publishes text`, () => {
  const f = fixture(); assert.throws(() => f.projection.event(f.frame(mutate(f.delta("中文")))));
  assert.equal(f.projection.sequence, 0); assert.equal(f.projection.read(), undefined);
});
test("source change is forbidden inside a subscription and a new source snapshot hides invalid old text", () => {
  const f = fixture(); f.projection.event(f.frame(f.delta("old")));
  assert.throws(() => f.projection.event(f.frame({ ...f.delta("!", 2, "old"), generation: crypto.randomUUID() }, 2)), /source drift/);
  f.projection.open(f.id);
  assert.equal(f.projection.event(f.frame({ kind: "snapshot_begin", generation: crypto.randomUUID(), snapshot_id: crypto.randomUUID(), total_bytes: 0, content_digest: streamDigest("") })), true);
  assert.equal(f.projection.read(), undefined);
});
test("failed snapshot digest leaves the prior verified preview untouched", () => {
  const f = fixture(); f.projection.event(f.frame(f.delta("old"))); f.projection.open(f.id);
  const base = { snapshot_id: crypto.randomUUID(), total_bytes: 3, content_digest: streamDigest("new"), revision: 2 };
  f.projection.event(f.frame({ kind: "snapshot_begin", ...base }));
  f.projection.event(f.frame({ kind: "snapshot_chunk", snapshot_id: base.snapshot_id, revision: 2, offset: 0, text: "bad" }, 2));
  assert.throws(() => f.projection.event(f.frame({ kind: "snapshot_end", ...base }, 3)), /digest/);
  assert.equal(f.projection.read().content, "old"); assert.equal(f.projection.sequence, 2);
});
test("business event allowed fields stay tied to the checked-in strict protocol schema", () => {
  const schema = JSON.parse(fs.readFileSync(new URL("../../../packages/protocol/execution-stream.v1.schema.json", import.meta.url).pathname.replace("/apps/packages/", "/packages/")));
  const f = fixture();
  assert.deepEqual(Object.keys(f.frame(f.delta("x")).body).sort(), schema.$defs.delta.required.sort());
});
