import assert from "node:assert/strict";
import http from "node:http";
import test from "node:test";
import { BrowserController } from "../src/controller-core.mjs";
import { createClientRequestHandler } from "../src/client-http-server.mjs";

async function fixture(t) {
  let dispatches = 0;
  const controller = {
    health: () => ({ state: "ready" }),
    acquire: async () => ({ state: "acquired" }),
    execute: async input => { dispatches++; return { state: "completed", result: input.arguments }; },
    release: async () => ({ state: "released" }),
  };
  const server = http.createServer(createClientRequestHandler(controller));
  await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
  t.after(() => new Promise(resolve => { server.close(resolve); server.closeAllConnections(); }));
  return { dispatches: () => dispatches, call: (route, body) => fetch(`http://127.0.0.1:${server.address().port}${route}`, {
    method: body === undefined ? "GET" : "POST", headers: { "content-type": "application/json" }, body: body === undefined ? undefined : JSON.stringify(body),
  }) };
}

test("client Controller constructs without optional mail/AI assembly", async () => {
  const controller = new BrowserController({ clientFactory: { open() { throw new Error("must not open"); } } });
  assert.equal(controller.mailObserverFeed, null);
  assert.equal(controller.health().state, "ready");
  await controller.shutdown();
});

test("client local server excludes backend routes and unsafe generic operations before dispatch", async t => {
  const f = await fixture(t);
  for (const route of ["validate-token", "run-script", "open-provider-login", "application-host", "application-lifecycle", "mail-observers/events"]) {
    assert.equal((await f.call(`/v1/${route}`, {})).status, 404);
  }
  for (const operation of ["ai_chat.export", "ai_platform.check", "tabs.handoff", "page.evaluate", "__proto__"]) {
    const response = await f.call("/v1/execute", { operation, arguments: {} });
    assert.equal(response.status, 400);
    assert.equal((await response.json()).code, "browser_operation_unavailable");
  }
  assert.equal(f.dispatches(), 0);
});

test("client operations preserve typed navigation but reject unknown keys and over-budget waits", async t => {
  const f = await fixture(t);
  assert.equal((await f.call("/v1/execute", { operation: "page.navigate", arguments: { url: "https://example.test/" } })).status, 200);
  for (const [operation, args] of [["page.read", { script: "secret" }], ["page.wait", { duration_ms: 15001 }], ["page.wait", { duration_ms: -1 }]]) {
    assert.equal((await f.call("/v1/execute", { operation, arguments: args })).status, 400);
  }
  assert.equal((await f.call("/v1/execute", { operation: "page.fill", arguments: { ref: "e1", text: "x".repeat(33 << 10) } })).status, 400);
  assert.equal(f.dispatches(), 1);
});
