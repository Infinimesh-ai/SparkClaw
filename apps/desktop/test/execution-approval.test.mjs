import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import http from "node:http";
import test from "node:test";
import { ClientStore } from "../src/main/client-store.mjs";
import { ExecutionClient } from "../src/main/execution-client.mjs";
import { DesktopAuth } from "../src/main/desktop-auth.mjs";
import { ClientStoreCapability } from "../src/main/client-store-capability.mjs";

const scope = { deployment_id: "deployment", owner_id: "owner", client_id: "client" };
const approval = { approval_id: "approval_test", digest: "a".repeat(64), tool: "browser.type", summary: "Type the supplied text into the selected website field", arguments: { text: "Synthetic input", field_ref: "field_test" } };
function fixture(t) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "sparkclaw-execution-approval-"));
  let store = new ClientStore(root);
  const conversation = store.create(scope, "approval test");
  const task = store.enqueue(scope, conversation.id, "request browser typing");
  store.markSubmitted(scope, task.request_id);
  const deadline = new Date(Date.now() + 600000).toISOString();
  const calls = [];
  let pending = [approval]; let state = "running";
  let identity = scope;
  const auth = { generation: 1, status: { state: "connected" }, descriptor: { origin: "http://127.0.0.1:18790" },
    authorizedExecutionFetch: async (url, init = {}) => {
      calls.push({ url, method: init.method || "GET", body: init.body });
      if (init.method === "POST") { pending = []; return new Response(JSON.stringify({ resolved: true })); }
      return new Response(JSON.stringify(event()));
    } };
  const event = () => ({ schema_version: 1, request_id: task.request_id, input_digest: store.request(scope, task.request_id).input_digest,
    state, ...(pending.length ? { pending_approvals: pending } : {}), execution_expires_at: deadline });
  let client = new ExecutionClient({ auth, store, getIdentity: () => identity });
  t.after(() => { client.close(); store.close(); fs.rmSync(root, { recursive: true, force: true }); });
  return { root, auth, calls, conversation, task, deadline, event, setPending: (rows) => { pending = rows; }, setState: (value) => { state = value; },
    setIdentity: (value) => { identity = value; }, get store() { return store; }, get client() { return client; },
    restart: () => { client.close(); store.close(); store = new ClientStore(root); client = new ExecutionClient({ auth, store, getIdentity: () => identity }); } };
}

test("real slow approval HTTP response serializes duplicate explicit decisions without repeating task submission", async (t) => {
  const f = fixture(t);
  let release; const wait = new Promise((resolve) => { release = resolve; });
  let started; const posted = new Promise((resolve) => { started = resolve; });
  let decisions = 0;
  const server = http.createServer(async (request, response) => {
    assert.equal(request.headers.authorization, "Bearer synthetic-issued-client");
    assert.equal(request.headers["x-sparkclaw-installation"], f.store.installationID);
    if (request.method === "POST") {
      assert.equal(request.url, `/api/v1/executions/${f.task.request_id}/approvals/approval_test`);
      const chunks = []; for await (const chunk of request) chunks.push(chunk);
      assert.deepEqual(JSON.parse(Buffer.concat(chunks)), { digest: approval.digest, decision: "approve" });
      decisions++; started(); await wait;
      f.setPending([]); response.end(JSON.stringify({ resolved: true }));
    } else response.end(JSON.stringify(f.event()));
  });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  t.after(() => new Promise((resolve) => server.close(resolve)));
  const auth = new DesktopAuth({ qualification: true, fetcher: fetch });
  auth.descriptor = { schemaVersion: 1, origin: `http://127.0.0.1:${server.address().port}` };
  auth.connection = { authorization: "Bearer synthetic-issued-client" }; auth.status = { state: "connected" };
  f.auth.descriptor = auth.descriptor; f.auth.authorizedExecutionFetch = auth.authorizedExecutionFetch.bind(auth);
  await f.client.reconcilePending();
  assert.equal(decisions, 0, "status polling never chooses an approval");
  const first = f.client.decideApproval(scope, f.task.request_id, approval.approval_id, approval.digest, "approve");
  await posted;
  const second = f.client.decideApproval(scope, f.task.request_id, approval.approval_id, approval.digest, "approve");
  assert.equal(f.store.approvals(scope, f.task.request_id)[0].state, "decision_pending");
  await new Promise((resolve) => setTimeout(resolve, 40)); release();
  assert.deepEqual(await first, { resolved: true });
  assert.deepEqual(await second, { resolved: true });
  assert.equal(decisions, 1);
  assert.equal(f.store.approvals(scope, f.task.request_id)[0].state, "approved");
  await assert.rejects(f.client.decideApproval(scope, f.task.request_id, approval.approval_id, approval.digest, "reject"), /mismatch/);
  assert.equal(decisions, 1);
});

test("explicit rejection persists its receipt and never becomes an automatic approval", async (t) => {
  const f = fixture(t);
  await f.client.reconcile(scope, f.task.request_id);
  assert.equal(f.client.approvals(scope, f.task.request_id)[0].actionable, true);
  await f.client.decideApproval(scope, f.task.request_id, approval.approval_id, approval.digest, "reject");
  assert.deepEqual(JSON.parse(f.calls.find((call) => call.method === "POST").body), { digest: approval.digest, decision: "reject" });
  f.restart();
  assert.equal(f.store.approvals(scope, f.task.request_id)[0].state, "rejected");
  assert.equal(f.client.approvals(scope, f.task.request_id)[0].actionable, false);
});

test("cached approvals stay read-only after restart/offline until same-identity fresh running lookup", async (t) => {
  const f = fixture(t);
  await f.client.reconcile(scope, f.task.request_id);
  f.restart();
  assert.equal(f.client.approvals(scope, f.task.request_id)[0].actionable, false);
  f.auth.status.state = "service_unavailable";
  await assert.rejects(f.client.decideApproval(scope, f.task.request_id, approval.approval_id, approval.digest, "approve"), /unavailable/);
  assert.equal(f.calls.filter((call) => call.method === "POST").length, 0);
  f.auth.status.state = "connected";
  await f.client.reconcile(scope, f.task.request_id);
  assert.equal(f.client.approvals(scope, f.task.request_id)[0].actionable, true);
});

test("lost approval receipt checks the original request and leaves an uncertain read-only decision", async (t) => {
  const f = fixture(t);
  await f.client.reconcile(scope, f.task.request_id);
  const fetcher = f.auth.authorizedExecutionFetch;
  f.auth.authorizedExecutionFetch = async (url, init) => {
    if (init.method === "POST") { f.calls.push({ url, method: "POST", body: init.body }); f.setPending([]); throw new Error("receipt lost after acceptance"); }
    return fetcher(url, init);
  };
  await assert.rejects(f.client.decideApproval(scope, f.task.request_id, approval.approval_id, approval.digest, "approve"), /receipt lost/);
  assert.equal(f.store.approvals(scope, f.task.request_id)[0].state, "decision_unknown");
  assert.equal(f.client.approvals(scope, f.task.request_id)[0].actionable, false);
  assert.ok(f.calls.filter((call) => call.method === "GET").every((call) => call.url.endsWith(f.task.request_id)));
  assert.equal(f.calls.filter((call) => call.method === "POST").length, 1);
  f.restart(); await f.client.reconcilePending();
  assert.equal(f.calls.filter((call) => call.method === "POST").length, 1);
});

test("lost unaccepted decision may be retried explicitly only with the identical decision and digest", async (t) => {
  const f = fixture(t);
  await f.client.reconcile(scope, f.task.request_id);
  const fetcher = f.auth.authorizedExecutionFetch; let lost = true;
  f.auth.authorizedExecutionFetch = async (url, init) => { if (init.method === "POST" && lost) throw new Error("request not received"); return fetcher(url, init); };
  await assert.rejects(f.client.decideApproval(scope, f.task.request_id, approval.approval_id, approval.digest, "reject"), /not received/);
  assert.equal(f.store.approvals(scope, f.task.request_id)[0].state, "decision_pending");
  await assert.rejects(f.client.decideApproval(scope, f.task.request_id, approval.approval_id, approval.digest, "approve"), /mismatch/);
  lost = false;
  assert.deepEqual(await f.client.decideApproval(scope, f.task.request_id, approval.approval_id, approval.digest, "reject"), { resolved: true });
});

test("expired, changed digest, foreign scope and old credential generation cannot approve", async (t) => {
  const f = fixture(t);
  await f.client.reconcile(scope, f.task.request_id);
  await assert.rejects(f.client.decideApproval({ ...scope, client_id: "other" }, f.task.request_id, approval.approval_id, approval.digest, "approve"), /authentication changed/);
  await assert.rejects(f.client.decideApproval(scope, f.task.request_id, approval.approval_id, "b".repeat(64), "approve"), /mismatch/);
  const fetcher = f.auth.authorizedExecutionFetch;
  f.auth.authorizedExecutionFetch = async (...args) => { const response = await fetcher(...args); f.auth.generation++; return response; };
  await assert.rejects(f.client.decideApproval(scope, f.task.request_id, approval.approval_id, approval.digest, "approve"), /authentication changed/);
  assert.equal(f.calls.filter((call) => call.method === "POST").length, 0);
  f.auth.authorizedExecutionFetch = async () => new Response(JSON.stringify({ ...f.event(), execution_expires_at: new Date(Date.now() - 1).toISOString() }));
  // A changed absolute deadline is an immutable replay violation, also fail-closed.
  await assert.rejects(f.client.decideApproval(scope, f.task.request_id, approval.approval_id, approval.digest, "approve"), /deadline/);
});

test("an approval with an expired or missing active deadline is never actionable", async (t) => {
  const f = fixture(t);
  f.auth.authorizedExecutionFetch = async () => new Response(JSON.stringify({ ...f.event(), execution_expires_at: undefined }));
  await assert.rejects(f.client.reconcile(scope, f.task.request_id), /deadline/);
  assert.deepEqual(f.store.approvals(scope, f.task.request_id), []);
  const expiredDeadline = new Date(Date.now() - 1000).toISOString();
  f.auth.authorizedExecutionFetch = async () => new Response(JSON.stringify({ ...f.event(), execution_expires_at: expiredDeadline }));
  await f.client.reconcile(scope, f.task.request_id);
  assert.equal(f.client.approvals(scope, f.task.request_id)[0].actionable, false);
  await assert.rejects(f.client.decideApproval(scope, f.task.request_id, approval.approval_id, approval.digest, "approve"), /expired/);
  assert.equal(f.calls.filter((call) => call.method === "POST").length, 0);
});

test("a late decision response from an old credential generation cannot confirm the local receipt", async (t) => {
  const f = fixture(t);
  await f.client.reconcile(scope, f.task.request_id);
  const fetcher = f.auth.authorizedExecutionFetch;
  let started; const posted = new Promise((resolve) => { started = resolve; });
  let release; const response = new Promise((resolve) => { release = resolve; });
  f.auth.authorizedExecutionFetch = async (url, init) => {
    if (init.method !== "POST") return fetcher(url, init);
    started(); return response;
  };
  const decision = f.client.decideApproval(scope, f.task.request_id, approval.approval_id, approval.digest, "approve");
  await posted;
  f.auth.generation++;
  release(new Response(JSON.stringify({ resolved: true })));
  await assert.rejects(decision, /authentication changed/);
  assert.equal(f.store.approvals(scope, f.task.request_id)[0].state, "decision_pending");
  assert.equal(f.client.approvals(scope, f.task.request_id)[0].actionable, false);
});

test("approval snapshot/disk failure cannot send a decision; IPC keeps scope and arguments main-owned", async (t) => {
  const f = fixture(t);
  f.store.db.exec("CREATE TRIGGER fail_approval BEFORE INSERT ON execution_approvals BEGIN SELECT RAISE(ABORT,'disk full'); END;");
  await assert.rejects(f.client.reconcile(scope, f.task.request_id), /disk full/);
  assert.deepEqual(f.store.approvals(scope, f.task.request_id), []);
  f.store.db.exec("DROP TRIGGER fail_approval");
  await f.client.reconcile(scope, f.task.request_id);
  f.store.db.exec("CREATE TRIGGER fail_decision BEFORE UPDATE ON execution_approvals BEGIN SELECT RAISE(ABORT,'disk full'); END;");
  await assert.rejects(f.client.decideApproval(scope, f.task.request_id, approval.approval_id, approval.digest, "approve"), /disk full/);
  assert.equal(f.calls.filter((call) => call.method === "POST").length, 0);
  f.store.db.exec("DROP TRIGGER fail_decision");
  const frame = { url: "sparkclaw-app://workbench/index.html" }; const webContents = { mainFrame: frame };
  const capability = new ClientStoreCapability({ window: { webContents }, store: f.store, execution: f.client, getIdentity: () => scope });
  const event = { sender: webContents, senderFrame: frame };
  const request = { schema_version: 1, operation: "decideApproval", request_id: f.task.request_id, approval_id: approval.approval_id, digest: approval.digest, decision: "approve" };
  await assert.rejects(capability.dispatch(event, { ...request, arguments: { text: "injected" } }), /fields/);
  await assert.rejects(capability.dispatch(event, { ...request, owner_id: "other" }), /fields/);
  await assert.rejects(capability.dispatch({ ...event, senderFrame: { ...frame } }, request), /trusted/);
  await assert.rejects(capability.dispatch(event, { ...request, approval_id: "../foreign" }), /Invalid/);
  const content = await capability.dispatch(event, { schema_version: 1, operation: "read", conversation_id: f.conversation.id });
  assert.equal(content.tasks[0].approvals[0].actionable, true);
});
