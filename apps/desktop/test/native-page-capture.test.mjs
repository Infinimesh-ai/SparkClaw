import assert from "node:assert/strict";
import test from "node:test";
import { nativePageCommand } from "../src/browser/native-page-commands.mjs";

const binding = { page_id: "page_fixture" };
const capture = (record, fence = () => {}) => nativePageCommand(record, "screenshot", {}, binding, fence);
function fixture({ native, cdp, attached = false } = {}) {
  const calls = [];
  const debuggerSession = {
    isAttached: () => attached,
    attach(version) { calls.push(["attach", version]); attached = true; },
    detach() { calls.push(["detach"]); attached = false; },
    sendCommand(method, params) { calls.push([method, params]); return cdp(); },
  };
  return { calls, view: { getBounds: () => ({width:640,height:720}) }, webContents: { capturePage: native || (() => Promise.reject(new Error("no display surface"))), debugger: debuggerSession, isDestroyed: () => false } };
}

test("native capture normalizes the bounded output without opening a debugger", async () => {
  const bytes = Buffer.from("fixture PNG");
  const record = fixture({ native: async () => ({ isEmpty: () => false, resize: value => { assert.deepEqual(value, { width: 480 }); return { toPNG: () => bytes }; } }) });
  assert.deepEqual(await capture(record), { page_id: binding.page_id, mimeType: "image/png", data: bytes.toString("base64") });
  assert.deepEqual(record.calls, []);
});

test("never displaces an existing debugger session when a native surface is missing", async () => {
  const record = fixture({ attached: true });
  await assert.rejects(capture(record), /no display surface/);
  assert.deepEqual(record.calls, []);
  assert.equal(record.webContents.debugger.isAttached(), true);
});

test("hidden capture uses only the fixed task viewport and detaches after failure", async () => {
  const record = fixture({ cdp: async () => { throw new Error("capture failure"); } });
  await assert.rejects(capture(record), /capture failure/);
  assert.deepEqual(record.calls, [["attach", "1.3"], ["Page.captureScreenshot", {
    format: "png", fromSurface: true, captureBeyondViewport: true,
    clip: { x: 0, y: 0, width: 640, height: 720, scale: 0.75 },
  }], ["detach"]]);
});

test("a revoked lease fences the hidden image and still releases the debugger", async () => {
  let revoked = false;
  const record = fixture({ cdp: async () => { revoked = true; return { data: "ignored after revocation" }; } });
  await assert.rejects(capture(record, () => { if (revoked) throw new Error("lease fenced"); }), /lease fenced/);
  assert.deepEqual(record.calls.at(-1), ["detach"]);
});

test("a stalled hidden capture is bounded and releases its debugger", async t => {
  t.mock.timers.enable({ apis: ["setTimeout"] });
  const record = fixture({ cdp: () => new Promise(() => {}) });
  const result = capture(record);
  const rejected = assert.rejects(result, /screenshot timed out/);
  for (let index = 0; index < 10; index++) await Promise.resolve();
  assert.equal(record.webContents.debugger.isAttached(), true);
  t.mock.timers.tick(3000);
  await rejected;
  assert.deepEqual(record.calls.at(-1), ["detach"]);
});

test("capture output still obeys the host reply budget", async () => {
  const record = fixture({ native: async () => ({ isEmpty: () => false, resize: () => ({ toPNG: () => Buffer.alloc((64 << 10) + 1) }) }) });
  await assert.rejects(capture(record), /bounded output budget/);
  assert.deepEqual(record.calls, []);
});
