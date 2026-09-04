import assert from "node:assert/strict";
import test from "node:test";

import { BrowserController } from "../src/controller.mjs";

const token = "qualification-token-value";

test("validateToken opens and closes one ephemeral task page", async () => {
  const factory = new FakeFactory();
  const controller = new BrowserController({
    profileID: "default",
    clientFactory: factory,
    controllerGeneration: 101,
  });

  const input = { profile_id: "default", token };
  const result = await controller.validateToken(input);

  assert.equal(result.state, "ready");
  assert.equal(result.controller_generation, 101);
  assert.equal(result.session_generation, 1);
  assert.equal(result.page_generation, 1);
  assert.equal(input.token, "");
  assert.deepEqual(factory.clients[0].events, ["page:new", "page:close", "client:close"]);
  assert.equal(controller.health().active_session, false);
});

test("acquire serializes one profile and returns stable generations", async () => {
  const factory = new FakeFactory();
  const controller = new BrowserController({
    profileID: "daily",
    clientFactory: factory,
    controllerGeneration: 202,
  });

  const first = await controller.acquire(acquireInput({ profile_id: "daily", task_id: "task-1", credential_generation: 7 }));
  assert.equal(first.controller_generation, 202);
  assert.equal(first.credential_generation, 7);
  assert.equal(first.session_generation, 1);
  assert.equal(first.page_generation, 1);

  await assert.rejects(
    controller.acquire(acquireInput({ profile_id: "daily", task_id: "task-2", credential_generation: 7 })),
    (error) => error.code === "browser_busy" && error.status === 409 && error.retryable,
  );

  await controller.release({
    session_id: first.session_id,
    controller_generation: first.controller_generation,
    session_generation: first.session_generation,
  });
  assert.equal(controller.health().state, "ready");

  const second = await controller.acquire(acquireInput({ profile_id: "daily", task_id: "task-2", credential_generation: 8 }));
  assert.equal(second.session_generation, 2);
  await controller.release({
    session_id: second.session_id,
    controller_generation: second.controller_generation,
    session_generation: second.session_generation,
  });
});

test("release rejects stale generations without closing the active lease", async () => {
  const factory = new FakeFactory();
  const controller = new BrowserController({
    profileID: "default",
    clientFactory: factory,
    controllerGeneration: 303,
  });
  const lease = await controller.acquire(acquireInput());

  await assert.rejects(
    controller.release({
      session_id: lease.session_id,
      controller_generation: 302,
      session_generation: lease.session_generation,
    }),
    (error) => error.code === "browser_controller_stale",
  );
  assert.equal(controller.health().active_session, true);

  await assert.rejects(
    controller.release({
      session_id: lease.session_id,
      controller_generation: lease.controller_generation,
      session_generation: lease.session_generation + 1,
    }),
    (error) => error.code === "browser_session_stale",
  );
  assert.equal(controller.health().active_session, true);

  await controller.release({
    session_id: lease.session_id,
    controller_generation: lease.controller_generation,
    session_generation: lease.session_generation,
  });
});

test("failed client startup releases the reservation", async () => {
  const factory = new FakeFactory({ failOpen: true });
  const controller = new BrowserController({ profileID: "default", clientFactory: factory });

  await assert.rejects(
    controller.acquire(acquireInput()),
    (error) => error.code === "browser_extension_unavailable" && error.retryable,
  );
  assert.equal(controller.health().state, "ready");
});

test("session TTL reaps the client and admits the next task", async () => {
  const factory = new FakeFactory();
  const controller = new BrowserController({
    profileID: "default",
    clientFactory: factory,
    defaultSessionTTLMS: 25,
  });
  await controller.acquire(acquireInput({ session_ttl_ms: 25 }));
  await waitFor(() => controller.health().state === "ready", 500);
  assert.deepEqual(factory.clients[0].events, ["page:new", "page:close", "client:close"]);

  const next = await controller.acquire(acquireInput({ task_id: "task-2", session_ttl_ms: 25 }));
  assert.equal(next.session_generation, 2);
  await controller.shutdown();
});

test("acquire rejects unsupported lanes and strict unknown fields", async () => {
  const controller = new BrowserController({ profileID: "default", clientFactory: new FakeFactory() });
  await assert.rejects(
    controller.acquire(acquireInput({ lane: "cli" })),
    (error) => error.code === "browser_lane_unavailable",
  );
  await assert.rejects(
    controller.validateToken({ profile_id: "default", token, extra: true }),
    (error) => error.code === "invalid_request",
  );
});

function acquireInput(overrides = {}) {
  return {
    profile_id: "default",
    lane: "mcp",
    task_id: "task-1",
    credential_generation: 1,
    token,
    ...overrides,
  };
}

class FakeFactory {
  constructor({ failOpen = false } = {}) {
    this.failOpen = failOpen;
    this.clients = [];
  }

  info() {
    return { client: "fake", client_version: "1" };
  }

  async open({ token: candidate }) {
    assert.equal(candidate, token);
    if (this.failOpen) throw new Error("private startup detail");
    const client = new FakeClient();
    this.clients.push(client);
    return client;
  }
}

class FakeClient {
  constructor() {
    this.events = [];
    this.closedSignal = deferred();
    this.closed = this.closedSignal.promise;
  }

  async createTaskPage() {
    this.events.push("page:new");
  }

  async closeTaskPage() {
    if (this.events.at(-1) !== "page:close") this.events.push("page:close");
  }

  async close() {
    if (this.events.at(-1) !== "client:close") this.events.push("client:close");
    this.closedSignal.resolve({ code: 0, signal: null });
  }
}

function deferred() {
  let resolve;
  const promise = new Promise((done) => { resolve = done; });
  return { promise, resolve };
}

async function waitFor(predicate, timeoutMS) {
  const deadline = Date.now() + timeoutMS;
  while (!predicate()) {
    if (Date.now() >= deadline) throw new Error("condition timed out");
    await new Promise((resolve) => setTimeout(resolve, 5));
  }
}
