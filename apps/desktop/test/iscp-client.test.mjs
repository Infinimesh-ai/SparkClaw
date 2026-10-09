import assert from "node:assert/strict";
import crypto from "node:crypto";
import fs from "node:fs/promises";
import path from "node:path";
import os from "node:os";
import { EventEmitter } from "node:events";
import { PassThrough, Writable } from "node:stream";
import test from "node:test";
import { DesktopAuth } from "../src/main/desktop-auth.mjs";
import { ISCPTransport, ISCP_OPERATIONS, ISCP_PROFILE } from "../src/main/iscp-transport.mjs";
import { SecureCredentialStore } from "../src/main/secure-credential-store.mjs";
import { ClientStore } from "../src/main/client-store.mjs";
import { ExecutionClient } from "../src/main/execution-client.mjs";
import { loadISCPProfile } from "../src/main/iscp-profile.mjs";
import { parseBackendDescriptor } from "../src/main/local-backend.mjs";
import { DesktopCapability, isDesktopSessionAuthorized } from "../src/main/desktop-capability.mjs";
import { ClientStoreCapability } from "../src/main/client-store-capability.mjs";

const scope = { deployment_id: "deployment", owner_id: "owner", client_id: "client" };
const descriptor = { schema_version: 3, transport: "iscp", origin: "https://iscp.invalid", ...scope, domain_id: "domain", initiator_device_id: "desktop", responder_device_id: "gateway", responder_key_thumbprint: "a".repeat(64), relay_url: "https://relay.example.test", test_mode: true };
const hash = (value) => crypto.createHash("sha256").update(value).digest("hex");

async function fixture(t) {
  const directory = await fs.mkdtemp(path.join(await fs.realpath(os.tmpdir()), "sparkclaw-iscp-client-")); await fs.chmod(directory, 0o700);
  const profilePath = path.join(directory, "profile.json"), helperPath = path.join(directory, "helper.json");
  await fs.writeFile(helperPath, JSON.stringify({ schema_version: 1, mode: "local-test", role: "initiator", binding: scope }), { mode: 0o600 });
  await fs.writeFile(profilePath, JSON.stringify({ schema_version: 1, transport: "iscp", test_mode: true, backend: descriptor, helper_config: helperPath }), { mode: 0o600 });
  const encryptionKey = crypto.randomBytes(32);
  const safeStorage = { isEncryptionAvailable: () => true, getSelectedStorageBackend: () => "gnome_libsecret",
    encryptString: (text) => { const iv = crypto.randomBytes(12), cipher = crypto.createCipheriv("aes-256-gcm", encryptionKey, iv); const bytes = Buffer.concat([cipher.update(text), cipher.final()]); return Buffer.concat([iv, cipher.getAuthTag(), bytes]); },
    decryptString: (bytes) => { const cipher = crypto.createDecipheriv("aes-256-gcm", encryptionKey, bytes.subarray(0, 12)); cipher.setAuthTag(bytes.subarray(12, 28)); return Buffer.concat([cipher.update(bytes.subarray(28)), cipher.final()]).toString("utf8"); } };
  const vault = new SecureCredentialStore({ directory: path.join(directory, "auth"), safeStorage, platform: "linux" });
  const calls = []; const children = []; let directHTTP = 0;
  let store = new ClientStore(path.join(directory, "workbench")); let auth, execution;
  const f = { directory, profilePath, helperPath, vault, calls, children, descriptor, ready: true, identity: scope,
    handler: () => ({ status: 404 }), get store() { return store; }, get auth() { return auth; }, get execution() { return execution; }, get directHTTP() { return directHTTP; } };
  const spawnProcess = () => {
    const child = new EventEmitter(); child.stdout = new PassThrough(); child.stderr = new PassThrough(); child.exitCode = null;
    child.send = (frame) => child.stdout.write(`${JSON.stringify(frame)}\n`);
    child.kill = () => { child.exitCode = 0; child.emit("exit", 0); };
    child.stdin = new Writable({ write(bytes, _encoding, done) {
      const frame = JSON.parse(bytes); const request = frame.request;
      if (frame.body_base64 !== undefined) { request.body = JSON.parse(Buffer.from(frame.body_base64, "base64").toString("utf8")); request.originalBody = Buffer.from(frame.body_base64, "base64").toString("utf8"); }
      calls.push(request);
      let result;
      if (request.operation === "workbench.identity") result = { status: 200, body: { schema_version: 1, ...f.identity } };
      else if (request.operation === "installation.bind") result = { status: 200, body: { schema_version: 1, installation_id: request.body.installation_id, ...f.identity } };
      else result = f.handler(request, child);
      if (result) child.send({ ipc_version: 1, type: "response", id: frame.id, response: { type: "task.result", profile: ISCP_PROFILE, id: request.id, ...result } });
      done();
    } });
    children.push(child);
    queueMicrotask(() => { child.send({ ipc_version: 1, type: "hello", operations: ISCP_OPERATIONS, max_request_bytes: 65536, max_response_bytes: 65536,
      identity: { domain_id: descriptor.domain_id, initiator_device_id: descriptor.initiator_device_id, responder_device_id: descriptor.responder_device_id, responder_key_thumbprint: descriptor.responder_key_thumbprint, relay_url: descriptor.relay_url, relay_profile: descriptor.relay_profile ?? "production" } }); if (f.ready) child.send({ ipc_version: 1, type: "state", state: "transport_ready" }); });
    return child;
  };
  f.newAuth = (overrides = {}) => new DesktopAuth({ vault, descriptorPath: path.join(directory, "backend.json"), installationID: store.installationID, requireLAN: true,
    iscpProfilePath: profilePath, allowLocalISCPTest: true, fetcher: () => { directHTTP++; throw new Error("Direct HTTP is inaccessible"); },
    transportFactory: (options) => new ISCPTransport({ ...options, spawnProcess, timeoutMS: 100 }), ...overrides });
  f.start = async () => { auth = f.newAuth(); await auth.initialize(); execution = new ExecutionClient({ auth, store, getIdentity: () => scope }); };
  f.restart = async () => { execution.close(); auth.close(); store.close(); store = new ClientStore(path.join(directory, "workbench")); await f.start(); };
  t.after(async () => { execution?.close(); auth?.close(); store.close(); await fs.rm(directory, { recursive: true, force: true }); });
  return f;
}

function completed(task) {
  const payload = JSON.stringify({ content: "Persisted through ISCP", files: [] });
  return { schema_version: 1, request_id: task.request_id, input_digest: task.input_digest, state: "completed", result: { sequence: 1, digest: hash(payload), payload } };
}

test("normal ISCP auth requires manifest, exact identity and installation, retaining private profile in the vault", async (t) => {
  const f = await fixture(t); await f.start();
  assert.equal(f.auth.status.state, "connected");
  assert.deepEqual(f.calls.map((call) => call.operation), ["workbench.identity", "installation.bind"]);
  assert.equal(f.auth.status.capabilities.files, false); assert.equal(f.auth.status.capabilities.browser, false);
  assert.ok(!JSON.stringify(f.auth.status).includes(f.profilePath));
  const saved = await f.vault.load(); assert.equal(saved.schema_version, 2); assert.equal(saved.transport, "iscp"); assert.equal(saved.authorization, undefined);
  assert.equal(f.directHTTP, 0);
  await assert.rejects(f.auth.authorizedFetch(`${descriptor.origin}/api/email`), /unavailable/u);
  assert.equal(f.auth.status.state, "connected");
  f.identity = { ...scope, client_id: "wrong" };
  assert.equal((await f.auth.retry()).state, "identity_conflict");
  assert.ok(await f.vault.load());
});

test("optional helper grant renewal stays private and needs no Desktop authentication or profile changes", async (t) => {
  const f = await fixture(t);
  const helper = JSON.parse(await fs.readFile(f.helperPath, "utf8"));
  const renewal = { url: "http://127.0.0.1:19891/v1/grants/current", pending_file: path.join(f.directory, "pending-grant.json"), poll_interval_seconds: 10 };
  await fs.writeFile(f.helperPath, JSON.stringify({ ...helper, grant_renewal: renewal }));
  await f.start();
  assert.equal(f.auth.status.state, "connected");
  const publicStatus = JSON.stringify(f.auth.status), saved = JSON.stringify(await f.vault.load());
  for (const privateValue of [renewal.url, renewal.pending_file, "grant_renewal"]) {
    assert.ok(!publicStatus.includes(privateValue)); assert.ok(!saved.includes(privateValue));
  }
  assert.equal(f.directHTTP, 0);
});

test("authenticated ISCP admits normal workbench IPC without Bearer and still fences browser operations and locked sessions", async (t) => {
  const f = await fixture(t); await f.start();
  assert.equal(f.auth.connection.authorization, undefined);
  assert.equal(f.auth.connection.identityVerified, true);
  let handler;
  const frame = { url: "sparkclaw-app://workbench/index.html" };
  const webContents = { mainFrame: frame };
  const selected = [];
  const capability = new DesktopCapability({
    ipcMain: { handle(_channel, callback) { handler = callback; }, removeHandler() {} },
    window: { webContents }, runtimeGeneration: "a".repeat(32),
    registry: { setChangeListener() {}, desktopSnapshot: () => [], selectConversation: (id) => { selected.push(id); return "page"; },
      createPersonalPage() { throw new Error("Browser implementation must not be invoked"); } },
    presentation: { status: () => ({}) }, browserServices: { snapshot: () => ({}) },
    authorizeSession: () => isDesktopSessionAuthorized(f.auth), getCapabilities: () => f.auth.status.capabilities,
  }).start();
  t.after(() => capability.close());
  const event = { sender: webContents, senderFrame: frame };
  const conversation = f.store.create(scope, "IPC conversation");
  assert.deepEqual(await handler(event, { schema_version: 1, operation: "selectConversation", local_conversation_id: conversation.id }), { page_ref: "page" });
  assert.deepEqual(selected, [conversation.id]);
  assert.equal((await handler(event, { schema_version: 1, operation: "state" })).runtime_kind, "electron");
  const storeCapability = new ClientStoreCapability({ window: { webContents }, store: f.store, execution: f.execution,
    getIdentity: () => isDesktopSessionAuthorized(f.auth) ? scope : null, getCapabilities: () => f.auth.status.capabilities });
  f.handler = (request) => request.operation === "execution.submit" ? { status: 200, body: completed(f.store.request(scope, request.request_id)) } : { status: 200, body: {} };
  const task = await storeCapability.dispatch(event, { schema_version: 1, operation: "enqueue", conversation_id: conversation.id, content: "IPC submission" });
  await storeCapability.dispatch(event, { schema_version: 1, operation: "submit", request_id: task.request_id });
  assert.equal(f.store.request(scope, task.request_id).status, "delivered");
  assert.equal(f.store.read(scope, conversation.id).messages.at(-1).content, "Persisted through ISCP");
  assert.equal(f.calls.filter((request) => request.operation === "execution.submit").length, 1);
  assert.equal(f.directHTTP, 0);
  for (const operation of ["grantBrowserHost", "createPersonal", "navigatePersonal", "showDownload", "observeTask", "setBounds"]) {
    await assert.rejects(handler(event, { schema_version: 1, operation }), /Browser operations are unavailable/u);
  }
  await assert.rejects(handler({ ...event, senderFrame: { ...frame } }, { schema_version: 1, operation: "state" }), /not trusted/u);
  f.auth.suspend();
  await assert.rejects(handler(event, { schema_version: 1, operation: "selectConversation", local_conversation_id: conversation.id }), /session is locked/u);
  await assert.rejects(storeCapability.dispatch(event, { schema_version: 1, operation: "submit", request_id: task.request_id }), /locked/u);
  assert.equal(isDesktopSessionAuthorized({ status: { state: "connected" }, descriptor: { transport: "iscp" }, connection: { transport: "iscp", identityVerified: false } }), false);
  assert.equal(isDesktopSessionAuthorized({ status: { state: "connected" }, descriptor: { transport: "iscp" }, connection: { authorization: "Bearer untrusted" } }), false);
});

test("test profile gates, private paths and initiator binding reject before any business traffic", async (t) => {
  const f = await fixture(t);
  assert.equal((await f.newAuth({ allowLocalISCPTest: false }).initialize()).state, "incomplete_setup");
  assert.equal((await f.newAuth({ qualification: true }).initialize()).state, "incomplete_setup");
  assert.equal(f.calls.length, 0);
  await fs.chmod(f.helperPath, 0o644); await assert.rejects(loadISCPProfile(f.profilePath), /private/u);
  await fs.chmod(f.helperPath, 0o600);
  await fs.writeFile(f.helperPath, JSON.stringify({ schema_version: 1, mode: "local-test", role: "responder", binding: scope }));
  assert.equal((await f.newAuth().initialize()).state, "incomplete_setup");
});

test("transport Ready alone and wrong installation cannot publish connected", async (t) => {
  const f = await fixture(t); f.ready = false;
  const auth = f.newAuth(); t.after(() => auth.close());
  assert.equal((await auth.initialize()).state, "service_unavailable"); assert.equal(f.calls.length, 0);
  assert.equal(auth.status.client_id, undefined); assert.equal((await f.vault.load()).identityVerified, false);
  f.ready = true; await auth.retry(); assert.equal(auth.status.state, "connected");
  const original = auth.transport.fetch.bind(auth.transport);
  auth.transport.fetch = async (url, init) => url.endsWith("/installations") ? new Response(JSON.stringify({ schema_version: 1, installation_id: crypto.randomUUID(), ...scope })) : original(url, init);
  assert.equal((await auth.retry()).state, "identity_conflict");
});

test("lost submit response and lost ACK recover the original immutable task through normal ClientStore after restart", async (t) => {
  const f = await fixture(t); await f.start();
  const conversation = f.store.create(scope, "ISCP conversation"), task = f.store.enqueue(scope, conversation.id, "text task");
  let admitted, submissions = 0, acks = 0, loseACK = true;
  f.handler = (request, child) => {
    if (request.operation === "execution.submit") {
      submissions++; assert.equal(hash(request.originalBody), request.input_digest);
      assert.equal(request.installation_id, f.store.installationID);
      admitted = completed(f.store.request(scope, task.request_id)); child.emit("exit", 1); return undefined;
    }
    if (request.operation === "execution.lookup") { assert.equal(request.request_id, task.request_id); return { status: 200, body: admitted }; }
    if (request.operation === "execution.ack") {
      acks++; assert.equal(f.store.read(scope, conversation.id).messages.at(-1).content, "Persisted through ISCP");
      if (loseACK) { child.emit("exit", 1); return undefined; }
      return { status: 200, body: {} };
    }
    return { status: 404 };
  };
  await assert.rejects(f.execution.submit(scope, task.request_id), /unavailable/u);
  assert.equal(submissions, 1); assert.equal(f.store.request(scope, task.request_id).explicitly_submitted, 1);
  await f.restart(); await f.execution.reconcilePending();
  assert.equal(f.store.read(scope, conversation.id).messages.at(-1).content, "Persisted through ISCP");
  assert.equal(f.store.receipt(scope, task.request_id).acknowledged, 0);
  loseACK = false; await f.restart(); await f.execution.reconcilePending();
  assert.equal(f.store.request(scope, task.request_id).status, "delivered"); assert.equal(submissions, 1); assert.equal(acks, 2);
  assert.equal(f.store.read(scope, conversation.id).messages.length, 2); assert.equal(f.directHTTP, 0);
});

test("oversize and file tasks fail before marking submission, preserving locally saved input", async (t) => {
  const f = await fixture(t); await f.start();
  const conversation = f.store.create(scope, "large input");
  f.store.enqueue(scope, conversation.id, "x".repeat(40000));
  const task = f.store.enqueue(scope, conversation.id, "y".repeat(40000));
  await assert.rejects(f.execution.submit(scope, task.request_id), /limit/u);
  assert.equal(f.store.request(scope, task.request_id).explicitly_submitted, 0);
  const filesConversation = f.store.create(scope, "file input");
  const file = f.store.saveFile(scope, filesConversation.id, "input.txt", Buffer.from("test"));
  const withFile = f.store.enqueue(scope, filesConversation.id, "read file", [file.id]);
  await assert.rejects(f.execution.submit(scope, withFile.request_id), /File/u);
  assert.equal(f.store.request(scope, withFile.request_id).explicitly_submitted, 0);
  assert.equal(f.calls.filter((call) => call.operation === "execution.submit").length, 0);
});

test("sleep and transient Grant expiry preserve private authorization and automatically reverify identity", async (t) => {
  const f = await fixture(t); await f.start(); const previous = f.auth.generation;
  f.auth.suspend(); assert.ok(f.auth.generation > previous); assert.equal(f.auth.status.state, "service_unavailable");
  assert.ok(await f.vault.load()); await f.auth.retry(); assert.equal(f.auth.status.state, "connected");
  const saved = await f.vault.load(), beforeExpiry = f.auth.generation, identities = f.calls.filter((call) => call.operation === "workbench.identity").length;
  f.children.at(-1).send({ ipc_version: 1, type: "state", state: "authorization_expired" });
  assert.equal(f.auth.status.state, "service_unavailable"); assert.ok(f.auth.generation > beforeExpiry);
  assert.ok(f.auth.reconnectTimer); assert.deepEqual(await f.vault.load(), saved);
  for (let tries = 0; f.auth.status.state !== "connected" && tries < 200; tries++) await new Promise((resolve) => setTimeout(resolve, 10));
  assert.equal(f.auth.status.state, "connected");
  assert.equal(f.calls.filter((call) => call.operation === "workbench.identity").length, identities + 1);
  assert.equal(f.calls.filter((call) => call.operation === "installation.bind").length, identities + 1);
  assert.deepEqual(await f.vault.load(), saved); assert.equal(f.directHTTP, 0);
});

test("verified standing-authorization revocation fences in-flight work and cannot automatically or manually reconnect", async (t) => {
  const f = await fixture(t); await f.start();
  const saved = await f.vault.load(), generation = f.auth.generation, spawns = f.children.length;
  f.handler = () => undefined;
  const old = f.auth.authorizedFetch(`${descriptor.origin}/api/config`);
  const rejected = assert.rejects(old, /unavailable/u);
  const child = f.children.at(-1);
  child.send({ ipc_version: 1, type: "state", state: "authorization_revoked" });
  await rejected;
  assert.equal(f.auth.status.state, "invalid_authentication");
  assert.ok(f.auth.generation > generation); assert.equal(f.auth.reconnectTimer, undefined);
  child.send({ ipc_version: 1, type: "state", state: "transport_ready" });
  assert.equal((await f.auth.retry()).state, "invalid_authentication");
  assert.equal(f.children.length, spawns); assert.deepEqual(await f.vault.load(), saved);
  assert.equal(f.directHTTP, 0);
});


test("session renegotiation fences connected operations until fresh identity and installation verification", async (t) => {
  const f = await fixture(t); await f.start(); const generation = f.auth.generation;
  const child = f.children.at(-1);
  child.send({ ipc_version: 1, type: "state", state: "handshaking" });
  assert.equal(f.auth.status.state, "reconnecting"); assert.ok(f.auth.generation > generation);
  child.send({ ipc_version: 1, type: "state", state: "transport_ready" });
  await new Promise((resolve) => setTimeout(resolve, 5));
  assert.equal(f.auth.status.state, "connected");
  assert.equal(f.calls.filter((call) => call.operation === "workbench.identity").length, 2);
  assert.equal(f.calls.filter((call) => call.operation === "installation.bind").length, 2);
});

test("ISCP recovery leaves the four-call budget available for required startup presentation", async (t) => {
  const f = await fixture(t); await f.start();
  const conversation = f.store.create(scope, "pending tasks");
  const tasks = [f.store.enqueue(scope, conversation.id, "first"), f.store.enqueue(scope, conversation.id, "second")];
  for (const task of tasks) f.store.markSubmitted(scope, task.request_id);
  let active = 0, peak = 0;
  f.handler = (request, child) => {
    if (request.operation === "execution.ack") return { status: 200, body: {} };
    active++; peak = Math.max(peak, active);
    const body = request.operation === "execution.lookup" ? completed(f.store.request(scope, request.request_id)) : {};
    setTimeout(() => { active--; child.send({ ipc_version: 1, type: "response", id: request.id, response: { type: "task.result", profile: ISCP_PROFILE, id: request.id, status: 200, body } }); }, 5);
    return undefined;
  };
  await Promise.all([f.execution.reconcilePending(), ...["/api/config", "/api/owner", "/readyz"].map((route) => f.auth.authorizedFetch(`${descriptor.origin}${route}`))]);
  assert.equal(peak, 4); assert.equal(f.auth.status.state, "connected");
  for (const task of tasks) assert.equal(f.store.request(scope, task.request_id).status, "delivered");
});

test("local ISCP capacity rejection preserves unsent input across restart and an explicit retry submits once", async (t) => {
  const f = await fixture(t); await f.start();
  const conversation = f.store.create(scope, "capacity"), task = f.store.enqueue(scope, conversation.id, "still unsent");
  f.handler = () => undefined;
  const busy = Array.from({ length: 4 }, () => f.auth.authorizedFetch(`${descriptor.origin}/api/config`));
  await assert.rejects(f.execution.submit(scope, task.request_id), /concurrency/u);
  assert.equal(f.auth.status.state, "connected");
  assert.equal(f.store.request(scope, task.request_id).explicitly_submitted, 0);
  assert.equal(f.store.request(scope, task.request_id).status, "awaiting_runtime");
  assert.equal(f.calls.filter((call) => call.operation.startsWith("execution.")).length, 0);
  for (const request of f.calls.slice(-4)) f.children.at(-1).send({ ipc_version: 1, type: "response", id: request.id,
    response: { type: "task.result", profile: ISCP_PROFILE, id: request.id, status: 200, body: {} } });
  for (const response of await Promise.all(busy)) await response.json();
  await f.restart(); await f.execution.reconcilePending();
  assert.equal(f.calls.filter((call) => call.operation.startsWith("execution.")).length, 0);
  f.handler = (request) => ({ status: 200, body: request.operation === "execution.submit" ? completed(f.store.request(scope, task.request_id)) : {} });
  assert.equal((await f.execution.submit(scope, task.request_id)).status, "delivered");
  assert.equal(f.calls.filter((call) => call.operation === "execution.submit").length, 1);
  assert.equal(f.directHTTP, 0);
});

test("oversized ISCP lookup becomes a durable delivery terminal without output, ACK or automatic polling", async (t) => {
  const f = await fixture(t); await f.start();
  const conversation = f.store.create(scope, "oversized result"), task = f.store.enqueue(scope, conversation.id, "one request");
  f.handler = (request) => request.operation === "execution.submit"
    ? { status: 200, body: { schema_version: 1, request_id: task.request_id, input_digest: f.store.request(scope, task.request_id).input_digest, state: "accepted" } }
    : { status: 413, error: "arbitrary diagnostic text is not a control signal" };
  await f.execution.submit(scope, task.request_id);
  assert.equal((await f.execution.reconcile(scope, task.request_id)).status, "delivery_too_large");
  assert.equal(f.store.receipt(scope, task.request_id), null);
  assert.equal(f.store.read(scope, conversation.id).messages.length, 1);
  assert.equal(f.store.read(scope, conversation.id).files.length, 0);
  assert.equal(f.store.pending(scope).length, 0);
  const lookups = f.calls.filter((call) => call.operation === "execution.lookup").length;
  await f.restart(); await f.execution.reconcilePending();
  assert.equal(f.store.request(scope, task.request_id).status, "delivery_too_large");
  assert.equal(f.calls.filter((call) => call.operation === "execution.lookup").length, lookups);
  assert.equal((await f.execution.submit(scope, task.request_id)).status, "delivery_too_large");
  assert.equal(f.calls.filter((call) => call.operation === "execution.submit").length, 1);
  assert.equal(f.calls.filter((call) => call.operation === "execution.ack").length, 0);
  assert.equal(f.directHTTP, 0);
});

test("an oversized submit response is reconciled as undeliverable without treating it as an unsent request", async (t) => {
  const f = await fixture(t); await f.start();
  const conversation = f.store.create(scope, "oversized immediate result"), task = f.store.enqueue(scope, conversation.id, "one request");
  f.handler = () => ({ status: 413 });
  await assert.rejects(f.execution.submit(scope, task.request_id), /not accepted/u);
  assert.equal(f.store.request(scope, task.request_id).status, "delivery_too_large");
  assert.equal(f.store.request(scope, task.request_id).explicitly_submitted, 1);
  assert.equal(f.store.receipt(scope, task.request_id), null);
  assert.deepEqual(f.calls.filter((call) => call.operation.startsWith("execution.")).map((call) => call.operation), ["execution.submit", "execution.lookup"]);
});


test("binding rejection fences other responses and preserves reauthorization config without reconnecting automatically", async (t) => {
  const f = await fixture(t); await f.start(); const generation = f.auth.generation;
  f.handler = (request) => request.operation === "presentation.config" ? undefined : { status: 403, body: {} };
  const old = f.auth.authorizedFetch(`${descriptor.origin}/api/config`); const observed = old.catch((error) => error.message);
  const response = await f.auth.authorizedFetch(`${descriptor.origin}/api/owner`);
  assert.equal(response.status, 401); assert.match(await observed, /unavailable/u);
  assert.equal(f.auth.status.state, "invalid_authentication"); assert.ok(f.auth.generation > generation);
  assert.equal(f.auth.reconnectTimer, undefined); assert.ok(await f.vault.load());
});


test("local-lab Relay is explicit, loopback only, and must match the helper's selected profile", async (t) => {
  const local = { ...descriptor, relay_profile: "local-lab", relay_url: "http://127.0.0.1:19888" };
  assert.equal(parseBackendDescriptor(local).relayProfile, "local-lab");
  for (const relay_url of ["http://localhost:19888", "http://[::1]:19888", "ws://127.0.0.1:19888", "http://127.0.0.2:19888"]) assert.equal(parseBackendDescriptor({ ...local, relay_url }).relayProfile, "local-lab");
  for (const relay_url of ["http://192.168.1.5:19888", "http://0.0.0.0:19888", "http://iscp-relay:8080", "http://127.0.0.1.evil:19888", "http://2130706433:19888", "https://relay.example.test", "http://127.0.0.1:19888/path", "ws://127.0.0.1:19888?token=secret"]) assert.throws(() => parseBackendDescriptor({ ...local, relay_url }), /Relay/u);
  assert.throws(() => parseBackendDescriptor({ ...descriptor, relay_url: local.relay_url }), /HTTPS/u);
  assert.equal(parseBackendDescriptor(descriptor).relayProfile, "production");
  const f = await fixture(t);
  await fs.writeFile(f.profilePath, JSON.stringify({ schema_version: 1, transport: "iscp", test_mode: true, backend: local, helper_config: f.helperPath }));
  await assert.rejects(loadISCPProfile(f.profilePath), /configuration/u);
  await fs.writeFile(f.helperPath, JSON.stringify({ schema_version: 1, mode: "local-test", role: "initiator", relay_profile: "local-lab", binding: scope }));
  assert.equal((await loadISCPProfile(f.profilePath)).descriptor.relayProfile, "local-lab");
});
