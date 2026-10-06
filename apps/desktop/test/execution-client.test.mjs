import assert from "node:assert/strict";
import crypto from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import http from "node:http";
import test from "node:test";
import { ClientStore } from "../src/main/client-store.mjs";
import { ExecutionClient } from "../src/main/execution-client.mjs";
import { DesktopAuth } from "../src/main/desktop-auth.mjs";

const scope = { deployment_id: "deployment", owner_id: "owner", client_id: "client" };
const hash = (value) => crypto.createHash("sha256").update(value).digest("hex");
function fixture(t, fetcher) {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), "sparkclaw-r3-client-"));
  let store = new ClientStore(directory);
  let identity = scope;
  const auth = { status: { state: "connected" }, descriptor: { origin: "http://127.0.0.1:18790" }, authorizedR3Fetch: fetcher };
  let client;
  const createClient = () => new ExecutionClient({ auth, store, getIdentity: () => identity });
  client = createClient();
  const conversation = store.create(scope, "test");
  const task = store.enqueue(scope, conversation.id, "test input");
  t.after(() => { client.close(); store.close(); fs.rmSync(directory, { recursive: true, force: true }); });
  return { directory, auth, conversation, task, get store() { return store; }, get client() { return client; },
    setIdentity: (value) => { identity = value; }, restart: () => { client.close(); store.close(); store = new ClientStore(directory); client = createClient(); } };
}
function event(f, state, result) {
  return { schema_version: 1, request_id: f.task.request_id, input_digest: f.store.request(scope, f.task.request_id).input_digest,
    state, ...(result ? { result } : {}) };
}
function result(content = "answer", files = []) {
  const payload = JSON.stringify({ content, files });
  return { sequence: 1, digest: hash(payload), payload };
}

test("real HTTP chain preserves explicit immutable submission, verified files and atomic durable ACK", async (t) => {
  const calls = [];
  const output = Buffer.from("verified generated file");
  let accepted;
  const server = http.createServer(async (request, response) => {
    const chunks = []; for await (const chunk of request) chunks.push(chunk);
    const body = Buffer.concat(chunks);
    calls.push({ method: request.method, path: request.url });
    assert.equal(request.headers.authorization, "Bearer synthetic-issued-client-token");
    if (request.url === "/api/r3/executions" && request.method === "POST") {
      const envelope = JSON.parse(body);
      assert.equal(hash(body), request.headers["x-r3-digest"]);
      assert.equal(envelope.installation_id, request.headers["x-sparkclaw-installation"]);
      assert.equal(envelope.messages.at(-1).content, "test input");
      accepted = event(f, "completed", result("Complete", [{ id: "output1", name: "report.txt", size: output.length, sha256: hash(output) }]));
      response.end(JSON.stringify(accepted));
    } else if (request.url.endsWith("/files/output1")) response.end(output);
    else if (request.url.endsWith("/ack")) {
      const receipt = JSON.parse(body);
      assert.equal(receipt.durable, true);
      assert.equal(f.store.read(scope, f.conversation.id).messages.at(-1).content, "Complete");
      const file = f.store.read(scope, f.conversation.id).files.at(-1);
      assert.deepEqual(f.store.file(scope, file.id).content, output);
      response.end("{}");
    } else { response.writeHead(404); response.end(); }
  });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  t.after(() => new Promise((resolve) => server.close(resolve)));
  const f = fixture(t, undefined);
  const auth = new DesktopAuth({ qualification: true, fetcher: fetch });
  auth.descriptor = { schemaVersion: 1, origin: `http://127.0.0.1:${server.address().port}` };
  auth.connection = { authorization: "Bearer synthetic-issued-client-token" };
  auth.status = { state: "connected" };
  f.auth.descriptor = auth.descriptor;
  f.auth.authorizedR3Fetch = auth.authorizedR3Fetch.bind(auth);
  await f.client.reconcilePending();
  assert.deepEqual(calls, [], "saved input never replays automatically");
  const completed = await f.client.submit(scope, f.task.request_id);
  assert.equal(completed.status, "delivered");
  assert.equal(calls.filter((call) => call.method === "POST" && call.path === "/api/r3/executions").length, 1);
  f.restart();
  await f.client.reconcilePending();
  assert.equal(f.store.read(scope, f.conversation.id).files.length, 1);
  assert.equal(f.store.read(scope, f.conversation.id).messages.length, 2);
});

test("lost admission response reconciles same ID and unknown fence never triggers resend", async (t) => {
  let posts = 0; let gets = 0;
  const f = fixture(t, async (_url, init) => {
    if (init.method === "POST") { posts++; throw new Error("connection lost after admission"); }
    gets++; return new Response(JSON.stringify(event(f, "unknown")));
  });
  await assert.rejects(f.client.submit(scope, f.task.request_id), /connection lost/);
  assert.equal(f.store.request(scope, f.task.request_id).status, "unknown");
  f.restart();
  await f.client.reconcilePending();
  await f.client.submit(scope, f.task.request_id);
  assert.equal(posts, 1);
  assert.equal(gets, 2);
});

test("lost admission followed by 404 never permits same-ID POST on explicit or background reconciliation", async (t) => {
  const submittedBodies = [];
  let available = false;
  const f = fixture(t, async (_url, init) => {
    if (init.method === "POST") {
      submittedBodies.push(init.body);
      if (!available) throw new Error("offline before admission");
      return new Response(JSON.stringify(event(f, "accepted")));
    }
    return new Response(null, { status: 404 });
  });
  await assert.rejects(f.client.submit(scope, f.task.request_id), /offline/);
  f.restart(); available = true;
  await f.client.reconcilePending();
  assert.equal(submittedBodies.length, 1);
  assert.equal((await f.client.submit(scope, f.task.request_id)).status, "submission_pending");
  assert.equal(submittedBodies.length, 1, "a missing remote fence is not permission to replay a write");
});

test("lost ACK survives client restart, deduplicates output and does not resubmit work", async (t) => {
  let posts = 0; let acks = 0;
  let loseACK = true;
  const f = fixture(t, async (url, init) => {
    if (url.endsWith("/ack")) { acks++; if (loseACK) throw new Error("ACK lost"); return new Response("{}"); }
    if (init.method === "POST") posts++;
    return new Response(JSON.stringify(event(f, "completed", result())));
  });
  await assert.rejects(f.client.submit(scope, f.task.request_id), /ACK lost/);
  assert.equal(f.store.request(scope, f.task.request_id).status, "saved");
  assert.equal(f.store.read(scope, f.conversation.id).messages.length, 2);
  f.restart(); loseACK = false;
  await f.client.reconcilePending();
  assert.equal(f.store.request(scope, f.task.request_id).status, "delivered");
  assert.equal(f.store.read(scope, f.conversation.id).messages.length, 2);
  assert.equal(posts, 1); assert.ok(acks >= 2);
});

test("disk/database failure rolls back output text, files and receipt and never ACKs", async (t) => {
  const output = Buffer.from("one file"); let acks = 0;
  const f = fixture(t, async (url) => {
    if (url.endsWith("/ack")) { acks++; return new Response("{}"); }
    if (url.endsWith("/files/output")) return new Response(output);
    return new Response(JSON.stringify(event(f, "completed", result("answer", [{ id: "output", name: "out.txt", size: output.length, sha256: hash(output) }]))));
  });
  f.store.db.exec("CREATE TRIGGER fail_file BEFORE INSERT ON files BEGIN SELECT RAISE(ABORT,'disk full'); END;");
  await assert.rejects(f.client.submit(scope, f.task.request_id), /disk full/);
  assert.equal(acks, 0);
  assert.equal(f.store.read(scope, f.conversation.id).messages.length, 1);
  assert.equal(f.store.read(scope, f.conversation.id).files.length, 0);
  assert.equal(f.store.receipt(scope, f.task.request_id), null);
  assert.deepEqual(fs.readdirSync(path.join(f.directory, "files")), []);
  f.store.db.exec("DROP TRIGGER fail_file");
  await f.client.reconcile(scope, f.task.request_id);
  assert.equal(acks, 1);
});

test("file hash drift and changed raw payload digest cannot create a delivery or ACK", async (t) => {
  const bytes = Buffer.from("expected"); let acks = 0; let badPayload = false;
  const f = fixture(t, async (url) => {
    if (url.endsWith("/ack")) { acks++; return new Response("{}"); }
    if (url.endsWith("/files/output")) return new Response("tampered");
    const output = result("answer", [{ id: "output", name: "out.txt", size: bytes.length, sha256: hash(bytes) }]);
    if (badPayload) output.digest = "0".repeat(64);
    return new Response(JSON.stringify(event(f, "completed", output)));
  });
  await assert.rejects(f.client.submit(scope, f.task.request_id), /verification/);
  badPayload = true;
  await assert.rejects(f.client.reconcile(scope, f.task.request_id), /digest/);
  assert.equal(acks, 0); assert.equal(f.store.read(scope, f.conversation.id).messages.length, 1);
});

test("scope changes while a result is in flight fence local persistence and ACK", async (t) => {
  let release; const responseReady = new Promise((resolve) => { release = resolve; }); let acks = 0;
  const f = fixture(t, async (url) => {
    if (url.endsWith("/ack")) { acks++; return new Response("{}"); }
    await responseReady;
    return new Response(JSON.stringify(event(f, "completed", result())));
  });
  const submission = f.client.submit(scope, f.task.request_id);
  await new Promise((resolve) => setImmediate(resolve));
  f.setIdentity({ ...scope, client_id: "different" }); release();
  await assert.rejects(submission, /authentication changed/);
  assert.equal(acks, 0); assert.equal(f.store.read(scope, f.conversation.id).messages.length, 1);
});

test("logout and same-identity relogin generation fence an old result", async (t) => {
  const f = fixture(t, async () => {
    f.auth.generation++;
    return new Response(JSON.stringify(event(f, "completed", result())));
  });
  f.auth.generation = 1;
  await assert.rejects(f.client.submit(scope, f.task.request_id), /authentication changed/);
  assert.equal(f.store.read(scope, f.conversation.id).messages.length, 1);
  assert.equal(f.store.receipt(scope, f.task.request_id), null);
});

test("suspending the client aborts outstanding result work without persisting or ACKing it", async (t) => {
  let signal; let release;
  const wait = new Promise((resolve) => { release = resolve; });
  const f = fixture(t, async (_url, init) => {
    signal = init.signal; await wait;
    return new Response(JSON.stringify(event(f, "completed", result())));
  });
  const submitted = f.client.submit(scope, f.task.request_id);
  await new Promise((resolve) => setImmediate(resolve));
  f.client.close();
  assert.equal(signal.aborted, true);
  release();
  await assert.rejects(submitted, /authentication changed/);
  assert.equal(f.store.read(scope, f.conversation.id).messages.length, 1);
});

test("cancel and expired delivery preserve distinct terminal states without creating outputs", async (t) => {
  let state = "accepted"; let cancel = false;
  const f = fixture(t, async (url) => {
    if (url.endsWith("/cancel")) { cancel = true; state = "canceled"; }
    return new Response(JSON.stringify(event(f, state)));
  });
  await f.client.submit(scope, f.task.request_id);
  assert.equal((await f.client.cancel(scope, f.task.request_id)).status, "canceled");
  assert.equal(cancel, true);
  state = "delivery_expired";
  assert.equal((await f.client.reconcile(scope, f.task.request_id)).status, "delivery_expired");
  assert.equal(f.store.read(scope, f.conversation.id).messages.length, 1);
});

test("explicit selected files are frozen locally, uploaded before POST and cannot be changed on retry", async (t) => {
  const calls = []; let target;
  const f = fixture(t, async (url, init) => {
    calls.push({ url, method: init.method, body: init.body, headers: new Headers(init.headers) });
    if (init.method === "PUT") return new Response("{}");
    return new Response(JSON.stringify({ schema_version: 1, request_id: target.request_id,
      input_digest: f.store.request(scope, target.request_id).input_digest, state: "accepted" }));
  });
  const file = f.store.saveFile(scope, f.conversation.id, "selected.txt", Buffer.from("explicit input"));
  target = f.store.enqueue(scope, f.conversation.id, "read selected file", [file.id]);
  await f.client.submit(scope, target.request_id);
  assert.equal(calls[0].method, "PUT"); assert.equal(calls[1].method, "POST");
  assert.equal(hash(calls[0].body), calls[0].headers.get("x-r3-digest"));
  const envelope = JSON.parse(calls[1].body);
  assert.deepEqual(envelope.input_files, [{ id: file.id, name: file.name, size: file.size, sha256: file.sha256 }]);
  assert.ok(calls.every((call) => call.headers.get("x-sparkclaw-installation") === f.store.installationID));
  assert.equal(f.store.request(scope, f.task.request_id).status, "awaiting_runtime", "other saved input is not replayed");
});

test("corrupted durable files block ACK retry after restart and preserve receipt for recovery", async (t) => {
  const output = Buffer.from("durable file"); let acks = 0;
  const f = fixture(t, async (url) => {
    if (url.endsWith("/ack")) { acks++; throw new Error("ACK response lost"); }
    if (url.endsWith("/files/output")) return new Response(output);
    return new Response(JSON.stringify(event(f, "completed", result("answer", [{ id: "output", name: "out.txt", size: output.length, sha256: hash(output) }]))));
  });
  await assert.rejects(f.client.submit(scope, f.task.request_id), /ACK response lost/);
  const file = f.store.read(scope, f.conversation.id).files[0];
  fs.writeFileSync(path.join(f.directory, "files", file.id), "corruption");
  f.restart();
  const previous = acks;
  await assert.rejects(f.client.reconcile(scope, f.task.request_id), /verification/);
  assert.equal(acks, previous);
  assert.equal(f.store.request(scope, f.task.request_id).status, "saved");
});

test("close/start fences an in-flight response even when authentication generation is unchanged", async (t) => {
  let release; let sent = false;
  const pending = new Promise((resolve) => { release = resolve; });
  const f = fixture(t, async (_url, init) => {
    if (init.method === "POST") { sent = true; await pending; }
    return new Response(JSON.stringify(event(f, "completed", result())));
  });
  f.auth.generation = 1;
  const submission = f.client.submit(scope, f.task.request_id);
  await new Promise((resolve) => setImmediate(resolve)); assert.equal(sent, true);
  f.client.close();
  // Restarting the client object must not make the old response authoritative.
  // Disable its independent reconciler for this stale-response-only test.
  f.client.reconcilePending = async () => {};
  f.client.start(); release();
  await assert.rejects(submission, /authentication changed/);
  assert.equal(f.store.receipt(scope, f.task.request_id), null);
});
