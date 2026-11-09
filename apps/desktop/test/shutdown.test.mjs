import assert from "node:assert/strict";
import test from "node:test";
import { EventEmitter } from "node:events";
import { bindDesktopShutdown } from "../src/main/shutdown.mjs";

test("ordinary Quit waits for owned runtime cleanup and coalesces repeated requests", async () => {
  const app = new EventEmitter(); let exits = 0, stops = 0, finish;
  app.quit = () => { let prevented = false; app.emit("before-quit", { preventDefault() { prevented = true; } }); if (!prevented) exits++; };
  const request = bindDesktopShutdown(app, async () => { stops++; await new Promise(resolve => { finish = resolve; }); });
  app.quit(); app.quit(); await Promise.resolve();
  assert.equal(stops, 1); assert.equal(exits, 0);
  const pending = request(); finish(); await pending;
  assert.equal(exits, 1); assert.equal(stops, 1);
});

test("failed cleanup cannot authorize quit and an explicit retry runs cleanup again", async () => {
  const app = new EventEmitter(); let stops = 0, exits = 0;
  app.quit = () => { exits++; };
  const request = bindDesktopShutdown(app, async () => { if (++stops === 1) throw new Error("cleanup unproved"); });
  await assert.rejects(request(), /unproved/); assert.equal(exits, 0);
  await request(); assert.equal(stops, 2); assert.equal(exits, 1);
});
