import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { ClientStore } from "../src/main/client-store.mjs";
import { ExecutionClient } from "../src/main/execution-client.mjs";
import { ScheduleClient } from "../src/main/schedule-client.mjs";
import { ISCPRequestNotSentError } from "../src/main/iscp-transport.mjs";

const scope = { deployment_id: "deployment", owner_id: "owner", client_id: "client" };
const settle = () => new Promise((resolve) => setImmediate(resolve));
function fixture(t) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "sparkclaw-workbench-schedule-"));
  let store = new ClientStore(root);
  let now = Date.now();
  let identity = scope;
  const calls = []; const states = new Map();
  const auth = { generation: 1, status: { state: "connected" }, descriptor: { origin: "http://127.0.0.1:18790" },
    authorizedExecutionFetch: async (url, init = {}) => {
      calls.push({ url, method: init.method || "GET", body: init.body });
      assert.equal(new Headers(init.headers).get("x-sparkclaw-installation"), store.installationID);
      assert.ok(url.includes("/executions"), "scheduling uses only the ordinary execution API");
      let id = url.split("/executions/")[1]?.split("/")[0];
      if (url.endsWith("/executions")) {
        const context = JSON.parse(init.body); id = context.request_id;
        assert.equal(store.request(scope, id).context_json, init.body, "context precedes network send");
        assert.equal(store.request(scope, id).explicitly_submitted, 1, "intent precedes network send");
        assert.equal(store.request(scope, id).submission_claim, null, "one-shot claim consumed before POST");
        states.set(id, "accepted");
      } else if (url.endsWith("/cancel")) states.set(id, "canceled");
      if (!states.has(id)) return new Response(null, { status: 404 });
      return new Response(JSON.stringify({ schema_version: 1, request_id: id,
        input_digest: store.request(scope, id).input_digest, state: states.get(id) }));
    } };
  let execution; let client;
  const createClients = () => {
    execution = new ExecutionClient({ auth, store, getIdentity: () => identity });
    client = new ScheduleClient({ auth, store, execution, getIdentity: () => identity, now: () => now });
  };
  createClients(); client.start();
  const conversation = store.create(scope, "scheduled tasks");
  t.after(() => { client.close(); execution.close(); store.close(); fs.rmSync(root, { recursive: true, force: true }); });
  return { calls, states, auth, conversation, root,
    get store() { return store; }, get client() { return client; }, get execution() { return execution; },
    now: () => now, advance: (ms) => { now += ms; }, identity: (value) => { identity = value; },
    due: (ms = 60000) => new Date(now + ms).toISOString(),
    posts: () => calls.filter((call) => call.method === "POST" && call.url.endsWith("/executions")),
    rows: () => store.read(scope, conversation.id).schedules,
    restart: () => { client.close(); execution.close(); store.close(); store = new ClientStore(root); createClients(); client.start(); } };
}

test("future definitions persist locally without remote registration and ordinary restart preserves identity", async (t) => {
  const f = fixture(t);
  const schedule = await f.client.create(scope, f.conversation.id, "scheduled input", f.due());
  assert.equal(schedule.state, "saved");
  assert.deepEqual(f.calls, []);
  f.restart(); await settle();
  assert.equal(f.rows()[0].request_id, schedule.request_id);
  assert.equal(f.rows()[0].state, "saved");
  assert.deepEqual(f.calls, []);
});

test("a continuously online delayed timer claims once and uses the ordinary execution path", async (t) => {
  const f = fixture(t);
  const schedule = await f.client.create(scope, f.conversation.id, "do once", f.due());
  f.advance(90000); // Normal dispatch jitter is not a restart/reconnect window.
  await Promise.all([f.client.reconcile(scope, schedule.request_id), f.client.reconcile(scope, schedule.request_id), f.client.reconcilePending()]);
  assert.equal(f.posts().length, 1);
  assert.equal(f.rows()[0].state, "accepted");
  assert.equal(f.store.request(scope, schedule.request_id).explicitly_submitted, 1);
});

test("offline-at-due stays permanently missed after reconnect; offline creation remains available", async (t) => {
  const f = fixture(t);
  f.auth.status.state = "service_unavailable"; f.client.close();
  const schedule = await f.client.create(scope, f.conversation.id, "offline input", f.due());
  f.advance(70000);
  f.auth.status.state = "connected"; f.client.start(); await settle();
  assert.equal(f.store.scheduledRequest(scope, schedule.request_id).state, "missed");
  await f.client.reconcile(scope, schedule.request_id);
  assert.equal(f.posts().length, 0);
  assert.equal(f.rows()[0].missed_count, 1);
});

test("startup never dispatches an overdue occurrence, even while authentication is already connected", async (t) => {
  const f = fixture(t);
  await f.client.create(scope, f.conversation.id, "no startup catchup", f.due());
  f.advance(70000); f.restart(); await settle();
  assert.equal(f.rows()[0].state, "missed");
  assert.equal(f.posts().length, 0);
});

test("sleep/wake breaks continuity while future occurrences can still execute", async (t) => {
  const f = fixture(t);
  await f.client.create(scope, f.conversation.id, "miss during sleep", f.due());
  await f.client.create(scope, f.conversation.id, "future after wake", f.due(180000));
  f.client.close(); f.execution.close(); f.advance(90000);
  f.execution.start(); f.client.start(); await settle();
  assert.equal(f.rows()[0].state, "missed");
  assert.equal(f.rows()[1].state, "saved");
  f.advance(100000); await f.client.reconcilePending();
  assert.equal(f.rows()[1].state, "accepted");
  assert.equal(f.posts().length, 1);
});

test("same-identity authentication generation changes reset the due-time horizon", async (t) => {
  const f = fixture(t);
  await f.client.create(scope, f.conversation.id, "miss across authentication", f.due());
  f.advance(70000); f.auth.generation++;
  await f.client.reconcilePending();
  assert.equal(f.rows()[0].state, "missed");
  assert.equal(f.posts().length, 0);
});

test("repeated connected notifications do not lose a continuously online dispatch window", async (t) => {
  const f = fixture(t);
  await f.client.create(scope, f.conversation.id, "online through status refresh", f.due());
  f.advance(70000); f.client.start(); await settle();
  assert.equal(f.posts().length, 1);
});

test("recurrence skips an offline range without backlog and executes a distinct future occurrence", async (t) => {
  const f = fixture(t);
  const first = await f.client.create(scope, f.conversation.id, "repeat input", f.due(), 60000);
  f.client.close(); f.advance(6 * 60000 + 5000); f.client.start(); await settle();
  const [missed, future] = f.rows();
  assert.equal(missed.state, "missed"); assert.equal(missed.missed_count, 6);
  assert.equal(missed.missed_until, new Date(Date.parse(first.due_at) + 5 * 60000).toISOString());
  assert.equal(future.state, "saved"); assert.equal(future.schedule_id, first.schedule_id);
  assert.notEqual(future.request_id, first.request_id);
  assert.ok(Date.parse(future.due_at) > f.now()); assert.equal(f.posts().length, 0);
  f.advance(60000); await f.client.reconcilePending();
  assert.equal(f.posts().length, 1);
  assert.equal(JSON.parse(f.posts()[0].body).request_id, future.request_id);
  assert.equal(f.rows()[0].state, "missed"); assert.equal(f.rows().at(-1).state, "saved");
});

test("online recurrence gives each due occurrence a unique durable claim despite dispatch jitter", async (t) => {
  const f = fixture(t);
  await f.client.create(scope, f.conversation.id, "online repetitions", f.due(), 60000);
  f.advance(125000);
  await f.client.reconcilePending(); await f.client.reconcilePending();
  assert.equal(f.posts().length, 2);
  assert.equal(new Set(f.posts().map((call) => JSON.parse(call.body).request_id)).size, 2);
  assert.deepEqual(f.rows().map((row) => row.state), ["accepted", "accepted", "saved"]);
});

test("exclusive SQLite claims prevent two schedulers from submitting the same occurrence", async (t) => {
  const f = fixture(t);
  const secondStore = new ClientStore(f.root);
  const second = new ScheduleClient({ auth: f.auth, store: secondStore, execution: f.execution, getIdentity: () => scope, now: f.now }).start();
  t.after(() => { second.close(); secondStore.close(); });
  const row = await f.client.create(scope, f.conversation.id, "one claim", f.due());
  f.advance(60000);
  await Promise.all([f.client.reconcile(scope, row.request_id), second.reconcile(scope, row.request_id)]);
  assert.equal(f.posts().length, 1);
});

test("a crash between durable claim and POST only looks up the original ID, even after 404", async (t) => {
  const f = fixture(t);
  const row = await f.client.create(scope, f.conversation.id, "claim then crash", f.due());
  f.advance(60000);
  assert.ok(f.store.claimSchedule(scope, row.request_id, f.now()));
  f.restart(); await settle();
  await f.execution.submit(scope, row.request_id);
  assert.equal(f.posts().length, 0);
  assert.ok(f.calls.length > 0);
  assert.ok(f.calls.every((call) => call.method === "GET" && call.url.endsWith(row.request_id)));
  assert.equal(f.store.request(scope, row.request_id).explicitly_submitted, 1);
});

test("lost due-time submit and 404 never resubmit on restart, scheduler scan or explicit submit", async (t) => {
  const f = fixture(t);
  const row = await f.client.create(scope, f.conversation.id, "uncertain once", f.due());
  let posts = 0; let gets = 0;
  f.auth.authorizedExecutionFetch = async (_url, init) => {
    if (init.method === "POST") { posts++; throw new Error("response lost"); }
    gets++; return new Response(null, { status: 404 });
  };
  f.advance(60000); await f.client.reconcilePending();
  f.restart(); await settle();
  await f.client.reconcilePending(); await f.execution.submit(scope, row.request_id);
  assert.equal(posts, 1); assert.ok(gets >= 2);
});

test("canceling a definition offline prevents all future occurrences and admitted work uses normal cancellation", async (t) => {
  const f = fixture(t);
  const local = await f.client.create(scope, f.conversation.id, "cancel future", f.due(), 60000);
  f.auth.status.state = "service_unavailable"; f.client.close();
  assert.equal((await f.client.cancel(scope, local.request_id)).state, "canceled");
  f.auth.status.state = "connected"; f.client.start(); await settle();
  const active = await f.client.create(scope, f.conversation.id, "cancel admitted", f.due(), 60000);
  f.advance(60000); await f.client.reconcilePending();
  assert.equal((await f.client.cancel(scope, active.request_id)).state, "canceled");
  assert.equal(f.rows().at(-1).state, "canceled");
  assert.ok(f.calls.some((call) => call.url.endsWith(`/executions/${active.request_id}/cancel`)));
  f.advance(60000); await f.client.reconcilePending(); assert.equal(f.posts().length, 1);
});

test("explicit Run now is a new durable request and does not revive a missed occurrence", async (t) => {
  const f = fixture(t);
  const row = await f.client.create(scope, f.conversation.id, "run explicitly", f.due());
  f.client.close(); f.advance(70000); f.client.start(); await settle();
  await f.client.runNow(scope, row.request_id);
  const record = f.store.scheduledRequest(scope, row.request_id);
  assert.equal(record.state, "run_now"); assert.notEqual(record.recovery_request_id, row.request_id);
  assert.equal(JSON.parse(f.posts()[0].body).request_id, record.recovery_request_id);
  await assert.rejects(f.client.runNow(scope, row.request_id), /unexecuted missed/);
});

test("definition and claim disk failures cannot cause a send or partial future recurrence", async (t) => {
  const f = fixture(t);
  f.store.db.exec("CREATE TRIGGER fail_schedule BEFORE INSERT ON schedules BEGIN SELECT RAISE(ABORT,'disk full'); END;");
  await assert.rejects(f.client.create(scope, f.conversation.id, "never send", f.due()), /disk full/);
  assert.equal(f.store.read(scope, f.conversation.id).messages.length, 0); assert.deepEqual(f.calls, []);
  f.store.db.exec("DROP TRIGGER fail_schedule");
  const row = await f.client.create(scope, f.conversation.id, "recurrence transaction", f.due(), 60000);
  f.store.db.exec("CREATE TRIGGER fail_next BEFORE INSERT ON schedules BEGIN SELECT RAISE(ABORT,'disk full'); END;");
  f.advance(60000);
  await assert.rejects(f.client.reconcile(scope, row.request_id), /disk full/);
  assert.equal(f.rows().length, 1); assert.equal(f.rows()[0].state, "saved");
  assert.equal(f.store.request(scope, row.request_id).explicitly_submitted, 0); assert.deepEqual(f.calls, []);
});

test("auth change after POST fences stale response; a future occurrence retains its identity", async (t) => {
  const f = fixture(t);
  const row = await f.client.create(scope, f.conversation.id, "stale generation", f.due(), 60000);
  const fetcher = f.auth.authorizedExecutionFetch;
  f.auth.authorizedExecutionFetch = async (...args) => { const response = await fetcher(...args); f.auth.generation++; return response; };
  f.advance(60000);
  await assert.rejects(f.client.reconcile(scope, row.request_id), /authentication changed/);
  assert.equal(f.rows()[0].state, "claimed"); assert.equal(f.rows()[1].state, "saved"); assert.equal(f.posts().length, 1);
});

test("an observed availability gap skips due work even without a lifecycle notification", async (t) => {
  const f = fixture(t);
  await f.client.create(scope, f.conversation.id, "network gap", f.due());
  f.auth.status.state = "service_unavailable"; await f.client.reconcilePending();
  f.advance(60000); f.auth.status.state = "connected";
  await f.client.reconcilePending(); assert.equal(f.rows()[0].state, "missed"); assert.equal(f.posts().length, 0);
});

test("disk unavailable at due time cannot turn later recovery into automatic execution", async (t) => {
  const f = fixture(t);
  const row = await f.client.create(scope, f.conversation.id, "skip disk outage", f.due());
  f.store.db.exec("CREATE TRIGGER fail_claim BEFORE UPDATE ON tasks BEGIN SELECT RAISE(ABORT,'disk full'); END;");
  f.advance(60000); await assert.rejects(f.client.reconcile(scope, row.request_id), /disk full/);
  f.store.db.exec("DROP TRIGGER fail_claim");
  f.advance(1); await f.client.reconcilePending();
  assert.equal(f.rows()[0].state, "missed"); assert.equal(f.posts().length, 0);
});

test("canceling a claimed but unsent occurrence revokes its one-shot submission capability", async (t) => {
  const f = fixture(t);
  const row = await f.client.create(scope, f.conversation.id, "cancel claim", f.due());
  f.advance(60000); const claim = f.store.claimSchedule(scope, row.request_id, f.now());
  assert.equal((await f.client.cancel(scope, row.request_id)).state, "canceled");
  await f.execution.submit(scope, row.request_id, { scheduleClaim: claim });
  assert.equal(f.posts().length, 0); assert.equal(f.rows()[0].state, "canceled");
});

test("a generation change between claim and dispatch permanently misses the unsent occurrence", async (t) => {
  const f = fixture(t);
  const row = await f.client.create(scope, f.conversation.id, "fenced before send", f.due());
  const claim = f.store.claimSchedule.bind(f.store);
  f.store.claimSchedule = (...args) => { const token = claim(...args); f.auth.generation++; return token; };
  f.advance(60000);
  await assert.rejects(f.client.reconcile(scope, row.request_id), /authentication changed/);
  assert.equal(f.rows()[0].state, "missed"); assert.equal(f.posts().length, 0);
});

test("a proven local capacity rejection permanently misses the occurrence without replaying it", async (t) => {
  const f = fixture(t);
  const row = await f.client.create(scope, f.conversation.id, "busy transport", f.due());
  const fetcher = f.auth.authorizedExecutionFetch;
  f.auth.authorizedExecutionFetch = async () => { throw new ISCPRequestNotSentError("ISCP request concurrency limit reached", "capacity"); };
  f.advance(60000);
  await assert.rejects(f.client.reconcile(scope, row.request_id), /concurrency/u);
  assert.equal(f.rows()[0].state, "missed");
  assert.equal(f.store.request(scope, row.request_id).explicitly_submitted, 0);
  assert.equal(f.store.request(scope, row.request_id).submission_claim, null);
  f.auth.authorizedExecutionFetch = fetcher;
  f.restart(); await settle(); await f.client.reconcilePending();
  assert.equal(f.rows()[0].state, "missed"); assert.equal(f.posts().length, 0);
});
