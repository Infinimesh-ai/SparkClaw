import assert from "node:assert/strict";
import fs from "node:fs/promises";
import http from "node:http";
import path from "node:path";
import test from "node:test";
import { controllerRequest, MAX_CONTROLLER_RESPONSE_BYTES } from "../src/browser/controller-runtime.mjs";
import { ControllerResults, RESULT_LIMIT, RESULT_CACHE_LIMIT } from "../src/browser/controller-results.mjs";

// The local transport boundary can receive a malformed/oversized child reply
// even though the pinned MCP has a smaller inner output limit.
test("UDS accepts exactly 10 MiB of UTF-8 JSON and rejects one extra streamed byte without truncation", async t => {
  const root = await fs.mkdtemp(path.join(await fs.realpath("/tmp"), "sx-limit-"));
  const socket = path.join(root, "controller.sock");
  const overhead = Buffer.byteLength(JSON.stringify({ text: "" }));
  const length = MAX_CONTROLLER_RESPONSE_BYTES - overhead;
  const text = "界".repeat(Math.floor(length / 3)) + "x".repeat(length % 3);
  const exact = Buffer.from(JSON.stringify({ text }));
  assert.equal(exact.length, MAX_CONTROLLER_RESPONSE_BYTES);
  let oversized = false;
  const server = http.createServer((request, response) => {
    if (request.url === "/v1/health") return response.end('{"state":"ready"}');
    // No content-length header: the client must count received bytes.
    for (let offset = 0; offset < exact.length; offset += 32768) response.write(exact.subarray(offset, offset + 32768));
    response.end(oversized ? " " : undefined);
  });
  await new Promise(resolve => server.listen(socket, resolve));
  t.after(async () => { server.closeAllConnections(); await new Promise(resolve => server.close(resolve)); await fs.rm(root, { recursive: true, force: true }); });
  const accepted = await controllerRequest(socket, "execute", {});
  assert.equal(Buffer.byteLength(JSON.stringify(accepted.payload)), MAX_CONTROLLER_RESPONSE_BYTES);
  assert.equal(accepted.payload.text.length, text.length);
  assert.equal(accepted.payload.text.slice(-8), text.slice(-8));
  oversized = true;
  await assert.rejects(controllerRequest(socket, "execute", {}), /response is too large/);
  assert.deepEqual(await controllerRequest(socket, "health"), { status: 200, payload: { state: "ready" } });
});

test("full 20 MiB cache retains exact-limit results, rejects overflow and recovers only at fixed expiry", () => {
  let now = 100;
  const cache = new ControllerResults({ now: () => now });
  const command = { command_id: "limit-command", digest: "a".repeat(64), binding: {} };
  const payload = { text: "x".repeat(RESULT_LIMIT - Buffer.byteLength(JSON.stringify({ text: "" }))) };
  const a = cache.put(cache.reserve(), command, payload);
  const b = cache.put(cache.reserve(), { ...command, command_id: "limit-command-b" }, payload);
  assert.equal(a.total_bytes, RESULT_LIMIT); assert.equal(b.total_bytes, RESULT_LIMIT);
  assert.equal(cache.bytes, RESULT_CACHE_LIMIT); assert.equal(cache.reserve(), null);
  const retained = [...cache.entries.values()].map(entry => entry.bytes);
  now = 60099; cache.expire(); assert.equal(cache.bytes, RESULT_CACHE_LIMIT);
  now = 60100; const reservation = cache.reserve(); assert.ok(reservation);
  assert.equal(cache.bytes, 0); assert.ok(retained.every(bytes => bytes.every(byte => byte === 0)));
  assert.throws(() => cache.put(reservation, command, { text: payload.text + "x" }), /too large/);
  assert.equal(cache.entries.size, 0); assert.equal(cache.bytes, 0);
  cache.release(reservation); assert.equal(cache.reservations.size, 0);
});
