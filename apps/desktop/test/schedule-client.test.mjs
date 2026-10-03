import assert from "node:assert/strict";
import crypto from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { ClientStore } from "../src/main/client-store.mjs";
import { ScheduleClient } from "../src/main/schedule-client.mjs";

const scope = { deployment_id: "deployment", owner_id: "owner", client_id: "client" };
const hash = (value) => crypto.createHash("sha256").update(value).digest("hex");
function fixture(t) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "sparkclaw-r3-schedule-"));
  let store = new ClientStore(root);
  let now = Date.now();
  let executionState;
  let scheduleID;
  let leaseLost = false;
  const calls = []; const executed = [];
  const auth = { generation: 1, status: { state: "connected" }, descriptor: { origin: "http://127.0.0.1:18790" },
    authorizedR3Fetch: async (url, init = {}) => {
      calls.push({ url, method: init.method || "GET", body: init.body });
      assert.equal(new Headers(init.headers).get("x-sparkclaw-installation"), store.installationID);
      if (url.includes("/executions/")) {
        if (!executionState) return new Response(null, { status: 404 });
        return new Response(JSON.stringify({ schema_version: 1, request_id: scheduleID,
          input_digest: store.request(scope, scheduleID).input_digest, state: executionState }));
      }
      if (url.endsWith("/cancel")) return new Response(JSON.stringify({ schema_version: 1, request_id: scheduleID, state: "canceled" }));
      if (url.endsWith("/renew") && leaseLost) return new Response(null, { status: 404 });
      if (url.endsWith("/lease")) {
        const registration = JSON.parse(init.body);
        const context = JSON.parse(registration.context);
        scheduleID = context.request_id;
        assert.equal(registration.digest, hash(registration.context));
        assert.equal(store.request(scope, scheduleID).context_json, registration.context, "definition and exact input precede registration");
        assert.equal(store.scheduledRequest(scope, scheduleID).state, "registering");
      }
      return new Response(JSON.stringify({ schema_version: 1, request_id: scheduleID, state: "leased", lease_expires_at: new Date(now + 30000).toISOString() }));
    } };
  const execution = { reconcile: async (_scope, id) => { executed.push({ operation: "reconcile", id }); },
    submit: async (_scope, id) => { executed.push({ operation: "submit", id }); } };
  let client = new ScheduleClient({ auth, store, execution, getIdentity: () => scope, now: () => now });
  const conversation = store.create(scope, "scheduled tasks");
  t.after(() => { client.close(); store.close(); fs.rmSync(root, { recursive: true, force: true }); });
  return { calls, executed, auth, conversation, get store() { return store; }, get client() { return client; },
    now: () => now, advance: (ms) => { now += ms; }, state: (value) => { executionState = value; }, loseLease: () => { leaseLost = true; },
    restart: () => { client.close(); store.close(); store = new ClientStore(root); client = new ScheduleClient({ auth, store, execution, getIdentity: () => scope, now: () => now }); } };
}

test("single-run definition and immutable ID persist before registration and renew every connected lease", async (t) => {
  const f = fixture(t);
  const schedule = await f.client.create(scope, f.conversation.id, "scheduled input", new Date(f.now() + 120000).toISOString());
  assert.equal(schedule.state, "leased");
  f.restart(); f.advance(10000);
  await f.client.reconcilePending();
  assert.equal(f.calls.filter((call) => call.url.endsWith("/lease")).length, 1);
  assert.equal(f.calls.filter((call) => call.url.endsWith("/renew")).length, 1);
  assert.equal(f.store.scheduledRequest(scope, schedule.request_id).state, "leased");
  assert.deepEqual(f.executed, [], "lease is not a direct task POST");
});

test("offline and expired leases miss overdue work without catchup or new request IDs", async (t) => {
  const f = fixture(t);
  const schedule = await f.client.create(scope, f.conversation.id, "do once", new Date(f.now() + 60000).toISOString());
  f.auth.status.state = "service_unavailable"; f.advance(70000);
  const before = f.calls.length;
  await f.client.reconcilePending();
  assert.equal(f.calls.length, before);
  f.auth.status.state = "connected";
  await f.client.reconcilePending();
  assert.equal(f.store.scheduledRequest(scope, schedule.request_id).state, "missed");
  assert.equal(f.calls.filter((call) => call.url.endsWith("/lease")).length, 1);
  assert.deepEqual(f.executed, []);
  const tasks = f.store.read(scope, f.conversation.id).tasks;
  assert.equal(tasks.length, 1); assert.equal(tasks[0].request_id, schedule.request_id);
});

test("backend restart re-registers same future context only after execution lookup returns404", async (t) => {
  const f = fixture(t);
  const schedule = await f.client.create(scope, f.conversation.id, "one request", new Date(f.now() + 120000).toISOString());
  f.restart(); f.loseLease(); f.advance(10000);
  await f.client.reconcilePending();
  const leases = f.calls.filter((call) => call.url.endsWith("/lease"));
  assert.equal(leases.length, 2); assert.equal(leases[0].body, leases[1].body);
  assert.equal(f.store.scheduledRequest(scope, schedule.request_id).state, "leased");
  assert.equal(f.calls.at(-2).method, "GET", "re-registration follows lookup after lost lease");
});

test("fired or unknown execution after restart reconciles existing ID and does not register a second lease", async (t) => {
  const f = fixture(t);
  const schedule = await f.client.create(scope, f.conversation.id, "once", new Date(f.now() + 60000).toISOString());
  f.advance(70000); f.state("unknown"); f.restart();
  await f.client.reconcilePending();
  assert.equal(f.store.scheduledRequest(scope, schedule.request_id).state, "unknown");
  assert.equal(f.calls.filter((call) => call.url.endsWith("/lease")).length, 1);
  assert.deepEqual(f.executed, [{ operation: "reconcile", id: schedule.request_id }]);
});

test("explicit Run now uses a new durable request and cannot double-create it", async (t) => {
  const f = fixture(t);
  f.auth.status.state = "service_unavailable";
  const schedule = await f.client.create(scope, f.conversation.id, "offline due", new Date(f.now() + 60000).toISOString());
  assert.deepEqual(f.calls, []);
  f.advance(70000); f.auth.status.state = "connected";
  await f.client.reconcilePending();
  assert.equal(f.store.scheduledRequest(scope, schedule.request_id).state, "missed");
  await f.client.runNow(scope, schedule.request_id);
  const record = f.store.scheduledRequest(scope, schedule.request_id);
  assert.equal(record.state, "run_now");
  assert.notEqual(record.recovery_request_id, schedule.request_id);
  assert.equal(f.store.request(scope, record.recovery_request_id).context_json.includes("offline due"), true);
  assert.deepEqual(f.executed, [{ operation: "submit", id: record.recovery_request_id }]);
  await assert.rejects(f.client.runNow(scope, schedule.request_id), /unexecuted missed/);
  assert.equal(f.store.read(scope, f.conversation.id).tasks.length, 2);
});

test("canceling a local definition prevents registration, server cancel stops active renewal", async (t) => {
  const f = fixture(t);
  f.auth.status.state = "service_unavailable";
  const local = await f.client.create(scope, f.conversation.id, "never register", new Date(f.now() + 60000).toISOString());
  assert.equal((await f.client.cancel(scope, local.request_id)).state, "canceled");
  f.auth.status.state = "connected";
  await f.client.reconcilePending();
  assert.deepEqual(f.calls, []);
  const active = await f.client.create(scope, f.conversation.id, "cancel lease", new Date(f.now() + 60000).toISOString());
  assert.equal((await f.client.cancel(scope, active.request_id)).state, "canceled");
  const before = f.calls.length;
  await f.client.reconcilePending();
  assert.equal(f.calls.length, before);
});

test("registration disk failure makes no network call and same-ID auth generation changes fence stale response", async (t) => {
  const f = fixture(t);
  f.store.db.exec("CREATE TRIGGER fail_schedule BEFORE INSERT ON schedules BEGIN SELECT RAISE(ABORT,'disk full'); END;");
  await assert.rejects(f.client.create(scope, f.conversation.id, "never send", new Date(f.now() + 60000).toISOString()), /disk full/);
  assert.deepEqual(f.calls, []);
  assert.equal(f.store.read(scope, f.conversation.id).messages.length, 0);
  f.store.db.exec("DROP TRIGGER fail_schedule");
  const fetcher = f.auth.authorizedR3Fetch;
  f.auth.authorizedR3Fetch = async (...args) => { const response = await fetcher(...args); f.auth.generation++; return response; };
  await assert.rejects(f.client.create(scope, f.conversation.id, "stale lease", new Date(f.now() + 60000).toISOString()), /authentication changed/);
  assert.equal(f.store.read(scope, f.conversation.id).schedules[0].state, "registering");
});

test("lost registration retains one definition and closes outstanding renewal on suspend", async (t) => {
  const f = fixture(t);
  f.auth.authorizedR3Fetch = async () => { throw new Error("offline after registration"); };
  const schedule = await f.client.create(scope, f.conversation.id, "keep exact ID", new Date(f.now() + 60000).toISOString());
  assert.equal(schedule.state, "registering");
  assert.equal(f.store.read(scope, f.conversation.id).schedules.length, 1);
  let signal; let release;
  const pending = new Promise((resolve) => { release = resolve; });
  f.auth.authorizedR3Fetch = async (_url, init) => { signal = init.signal; await pending; return new Response(null, { status: 404 }); };
  const renewal = f.client.reconcile(scope, schedule.request_id);
  await new Promise((resolve) => setImmediate(resolve));
  f.client.close();
  assert.equal(signal.aborted, true);
  release();
  await assert.rejects(renewal, /authentication changed/);
});
