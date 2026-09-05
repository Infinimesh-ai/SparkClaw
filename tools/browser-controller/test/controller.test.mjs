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

test("execute binds operations to the active session generations", async () => {
  const factory = new FakeFactory();
  const controller = new BrowserController({
    profileID: "default",
    clientFactory: factory,
    controllerGeneration: 404,
  });
  const lease = await controller.acquire(acquireInput({ credential_generation: 9 }));

  const listed = await controller.execute(executeInput(lease, "tabs.list", {}));
  assert.equal(listed.state, "completed");
  assert.equal(listed.credential_generation, 9);
  assert.deepEqual(listed.result, { operation: "tabs.list", arguments: {} });

  const created = await controller.execute(executeInput(lease, "tabs.new", { url: "https://example.test" }));
  assert.equal(created.page_generation, lease.page_generation + 1);
  await assert.rejects(
    controller.execute(executeInput(lease, "page.info", {})),
    (error) => error.code === "browser_page_stale",
  );

  const current = { ...lease, page_generation: created.page_generation };
  const selected = await controller.execute(executeInput(current, "tabs.select", { page_id: "page_1" }));
  assert.equal(selected.page_generation, current.page_generation + 1);
  const latest = { ...current, page_generation: selected.page_generation };
  const handedOff = await controller.execute(executeInput(latest, "tabs.handoff", { page_id: "page_1" }));
  assert.equal(handedOff.page_generation, latest.page_generation + 1);
  const handedOffLease = { ...latest, page_generation: handedOff.page_generation };
  await assert.rejects(
    controller.execute(executeInput(handedOffLease, "page.evaluate", {})),
    (error) => error.code === "browser_operation_unavailable",
  );
  await controller.release({
    session_id: handedOffLease.session_id,
    controller_generation: handedOffLease.controller_generation,
    session_generation: handedOffLease.session_generation,
  });
});

test("execute advances page generation for every ref-invalidating operation", async () => {
  const controller = new BrowserController({
    profileID: "default",
    clientFactory: new FakeFactory(),
    controllerGeneration: 405,
  });
  let lease = await controller.acquire(acquireInput());

  for (const operation of [
    "page.info",
    "page.read",
    "page.screenshot",
    "page.snapshot",
    "page.wait",
    "tabs.list",
  ]) {
    const result = await controller.execute(executeInput(lease, operation, {}));
    assert.equal(result.page_generation, lease.page_generation, operation);
  }

  for (const operation of [
    "page.click",
    "page.fill",
    "page.navigate",
    "page.reload",
    "page.select",
    "page.type",
    "tabs.close",
    "tabs.handoff",
    "tabs.new",
    "tabs.select",
  ]) {
    const result = await controller.execute(executeInput(lease, operation, {}));
    assert.equal(result.page_generation, lease.page_generation + 1, operation);
    lease = { ...lease, page_generation: result.page_generation };
  }

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

test("runScript uses the shared profile reservation and returns fixed script identity", async () => {
  const scriptFactory = new FakeScriptFactory();
  const controller = new BrowserController({
    profileID: "default",
    clientFactory: new FakeFactory(),
    scriptFactory,
    controllerGeneration: 505,
  });
  const input = runScriptInput();

  const result = await controller.runScript(input);

  assert.equal(result.state, "completed");
  assert.equal(result.lane, "cli");
  assert.equal(result.provider, "gmail");
  assert.equal(result.operation, "probe");
  assert.equal(result.script_id, "gmail.login_probe");
  assert.equal(result.revision, 1);
  assert.equal(result.source_checksum, `sha256:${"a".repeat(64)}`);
  assert.equal(result.credential_generation, 7);
  assert.equal(result.controller_generation, 505);
  assert.equal(result.session_generation, 1);
  assert.deepEqual(result.result, { schema_version: 1, status: "authenticated" });
  assert.match(scriptFactory.calls[0].sessionID, /^session_[0-9a-f]{32}$/u);
  assert.equal(input.token, "");
  assert.equal(controller.health().state, "ready");
  assert.deepEqual(controller.health().versions, {
    client: "fake",
    client_version: "1",
    cli: "fake-cli",
    cli_version: "1",
  });
});

test("runScript fails closed while MCP owns the profile and clears sensitive input", async () => {
  const scriptFactory = new FakeScriptFactory();
  const controller = new BrowserController({
    profileID: "default",
    clientFactory: new FakeFactory(),
    scriptFactory,
  });
  const lease = await controller.acquire(acquireInput());
  const input = runScriptInput({
    operation: "send",
    script_id: "gmail.send",
    input: {
      schema_version: 1,
      operation: "send",
      invocation_id: "send-1",
      provider: "gmail",
      account: "default",
      message: {
        recipient: "person@example.test",
        subject: "subject",
        body: { format: "text", content: "body" },
      },
    },
  });

  await assert.rejects(
    controller.runScript(input),
    (error) => error.code === "browser_busy" && error.status === 409,
  );
  assert.equal(scriptFactory.calls.length, 0);
  assert.equal(input.token, "");
  assert.equal(input.input.message.recipient, "");
  assert.equal(input.input.message.subject, "");
  assert.equal(input.input.message.body.content, "");
  await controller.release({
    session_id: lease.session_id,
    controller_generation: lease.controller_generation,
    session_generation: lease.session_generation,
  });
});

test("runScript clears secrets even when strict outer validation fails", async () => {
  const controller = new BrowserController({
    profileID: "default",
    clientFactory: new FakeFactory(),
    scriptFactory: new FakeScriptFactory(),
  });
  const input = runScriptInput({ unknown: true });

  await assert.rejects(
    controller.runScript(input),
    (error) => error.code === "invalid_request" && error.status === 400,
  );
  assert.equal(input.token, "");
});

test("openProviderLogin shares the profile reservation and exposes no URL", async () => {
  const scriptFactory = new FakeScriptFactory();
  const controller = new BrowserController({
    profileID: "default",
    clientFactory: new FakeFactory(),
    scriptFactory,
    controllerGeneration: 606,
  });

  const result = await controller.openProviderLogin({
    profile_id: "default",
    task_id: "login-task",
    provider: "outlook",
  });

  assert.deepEqual(result, {
    schema_version: 1,
    state: "opened",
    profile_id: "default",
    provider: "outlook",
    controller_generation: 606,
    session_generation: 1,
  });
  assert.deepEqual(scriptFactory.loginProviders, ["outlook"]);
  assert.equal(controller.health().state, "ready");
});

test("shutdown aborts an active CLI reservation and waits for cleanup", async () => {
  const scriptFactory = new BlockingScriptFactory();
  const controller = new BrowserController({
    profileID: "default",
    clientFactory: new FakeFactory(),
    scriptFactory,
  });
  const running = controller.runScript(runScriptInput());
  await waitFor(() => controller.health().active_session, 500);

  await controller.shutdown();
  await assert.rejects(
    running,
    (error) => error.code === "browser_extension_unavailable" && error.status === 503,
  );
  assert.equal(scriptFactory.aborted, true);
  assert.equal(controller.health().active_session, false);
  assert.equal(controller.health().state, "stopping");
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

function executeInput(lease, operation, args) {
  return {
    session_id: lease.session_id,
    controller_generation: lease.controller_generation,
    session_generation: lease.session_generation,
    page_generation: lease.page_generation,
    operation,
    arguments: args,
  };
}

function runScriptInput(overrides = {}) {
  return {
    profile_id: "default",
    task_id: "script-task",
    credential_generation: 7,
    token,
    provider: "gmail",
    operation: "probe",
    script_id: "gmail.login_probe",
    revision: 1,
    input: {
      schema_version: 1,
      operation: "probe",
      invocation_id: "probe-1",
      provider: "gmail",
      account: "default",
    },
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

  async execute(operation, args) {
    this.events.push(`execute:${operation}`);
    return { operation, arguments: args };
  }

  async close() {
    if (this.events.at(-1) !== "client:close") this.events.push("client:close");
    this.closedSignal.resolve({ code: 0, signal: null });
  }
}

class FakeScriptFactory {
  constructor() {
    this.calls = [];
    this.loginProviders = [];
  }

  info() {
    return { cli: "fake-cli", cli_version: "1" };
  }

  async runScript(options) {
    this.calls.push(options);
    return {
      state: "completed",
      sourceChecksum: `sha256:${"a".repeat(64)}`,
      result: { schema_version: 1, status: "authenticated" },
    };
  }

  async openProviderLogin(provider) {
    this.loginProviders.push(provider);
  }
}

class BlockingScriptFactory extends FakeScriptFactory {
  async runScript({ signal }) {
    await new Promise((_, reject) => {
      signal.addEventListener("abort", () => {
        this.aborted = true;
        reject(new Error("aborted"));
      }, { once: true });
    });
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
