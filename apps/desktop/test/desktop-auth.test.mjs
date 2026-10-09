import assert from "node:assert/strict";
import test from "node:test";
import fs from "node:fs/promises";
import path from "node:path";
import os from "node:os";
import crypto from "node:crypto";
import { DesktopAuth, authorizeWorkbenchSender } from "../src/main/desktop-auth.mjs";
import { SecureCredentialStore } from "../src/main/secure-credential-store.mjs";
import { parseBackendDescriptor } from "../src/main/local-backend.mjs";

const token = "synthetic-test-credential-" + "x".repeat(32);
const descriptor = { schema_version: 1, origin: "http://127.0.0.1:18790", deployment_id: "test-deployment" };
const installationID = "12345678-1234-4123-8123-123456789abc";
const identity = { schema_version: 1, installation_id: installationID, deployment_id: "test-deployment", owner_id: "test-owner", client_id: "test-device" };
const encryptionKey = crypto.randomBytes(32);
const secureStorage = {
  isEncryptionAvailable: () => true,
  getSelectedStorageBackend: () => "gnome_libsecret",
  encryptString: (text) => {
    const iv = crypto.randomBytes(12);
    const cipher = crypto.createCipheriv("aes-256-gcm", encryptionKey, iv);
    const content = Buffer.concat([cipher.update(text, "utf8"), cipher.final()]);
    return Buffer.concat([iv, cipher.getAuthTag(), content]);
  },
  decryptString: (buffer) => {
    const cipher = crypto.createDecipheriv("aes-256-gcm", encryptionKey, buffer.subarray(0, 12));
    cipher.setAuthTag(buffer.subarray(12, 28));
    return Buffer.concat([cipher.update(buffer.subarray(28)), cipher.final()]).toString("utf8");
  },
};

async function fixture(t, storage = secureStorage) {
  const directory = await fs.mkdtemp(path.join(await fs.realpath(os.tmpdir()), "sparkclaw-auth-"));
  t.after(() => fs.rm(directory, { recursive: true, force: true }));
  const descriptorPath = path.join(directory, "backend.json");
  await fs.writeFile(descriptorPath, JSON.stringify(descriptor), { mode: 0o600 });
  const vault = new SecureCredentialStore({ directory: path.join(directory, "authentication"), safeStorage: storage, platform: "linux" });
  const options = { descriptorPath, vault, installationID, fetcher: async () => new Response(JSON.stringify(identity)) };
  return { options, directory, vault, auth: new DesktopAuth(options) };
}

test("production first launch requires user credential; encrypted restart binds exact device and preserves credential during outage", async (t) => {
  const { auth, options, vault } = await fixture(t);
  assert.equal((await auth.initialize()).state, "locked");
  assert.equal((await auth.login(token)).state, "connected");
  await assert.rejects(auth.enroll("not-a-connection-credential"), /invalid/u);
  assert.equal(auth.status.state, "connected", "malformed enrollment cannot replace an active connection");
  const ciphertext = await fs.readFile(vault.filename);
  assert.ok(!ciphertext.includes(Buffer.from(token)));
  assert.ok(!JSON.stringify(auth.status).includes(token));
  const restarted = new DesktopAuth(options);
  assert.equal((await restarted.initialize()).client_id, "test-device");
  restarted.fetcher = async () => { throw new Error("offline"); };
  assert.equal((await restarted.retry()).state, "service_unavailable");
  assert.ok(await vault.load());
  restarted.fetcher = options.fetcher;
  assert.equal((await restarted.retry()).state, "connected");
  restarted.fetcher = async () => new Response(JSON.stringify({ ...identity, client_id: "another-device" }));
  assert.equal((await restarted.retry()).state, "identity_conflict");
  assert.equal(await vault.load(), undefined);
});

test("bad credential stays locked; Linux basic_text fails closed before transmitting user token", async (t) => {
  const { auth, vault } = await fixture(t);
  await auth.initialize();
  auth.fetcher = async () => new Response(null, { status: 401 });
  assert.equal((await auth.login(token)).state, "invalid_authentication");
  assert.equal(await vault.load(), undefined);
  const unavailable = await fixture(t, { ...secureStorage, getSelectedStorageBackend: () => "basic_text" });
  unavailable.auth.fetcher = () => { throw new Error("must not send"); };
  assert.equal((await unavailable.auth.initialize()).state, "secure_storage_unavailable");
  assert.equal((await unavailable.auth.login(token)).state, "secure_storage_unavailable");
  await assert.rejects(unavailable.vault.load(), /unavailable/);
});

test("temporary key-store decryption failure preserves ciphertext and cannot open protected channels", async (t) => {
  const { auth, options, vault } = await fixture(t);
  await auth.initialize();
  await auth.login(token);
  const ciphertext = await fs.readFile(vault.filename);
  const unavailableVault = new SecureCredentialStore({ directory: vault.directory, platform: "linux", safeStorage: {
    ...secureStorage, decryptString() { throw new Error("OS key-store unavailable"); },
  } });
  const blocked = new DesktopAuth({ ...options, vault: unavailableVault, fetcher() { throw new Error("must not transmit"); } });
  assert.equal((await blocked.initialize()).state, "locked");
  assert.equal(blocked.connection, undefined);
  assert.equal((await blocked.authorizedFetch(`${descriptor.origin}/api/clients`)).status, 401);
  assert.deepEqual(await fs.readFile(vault.filename), ciphertext);
  assert.equal((await new DesktopAuth(options).initialize()).state, "connected");
  assert.deepEqual(await fs.readFile(vault.filename), ciphertext);
});

test("401 and explicit logout clear the secret, abort protected channels, and call lock cleanup", async (t) => {
  const { auth, vault } = await fixture(t);
  await auth.initialize(); await auth.login(token);
  let stopped = 0;
  auth.onLock = () => { stopped++; };
  let protectedSignal;
  auth.fetcher = async (_url, init) => {
    protectedSignal = init.signal;
    return new Response(new ReadableStream({ start(controller) { controller.enqueue(new TextEncoder().encode("event: ready\n\n")); } }));
  };
  const stream = await auth.authorizedFetch(`${descriptor.origin}/api/stream`);
  assert.equal(stream.status, 200);
  await auth.logout();
  assert.equal(stopped, 1);
  assert.equal(protectedSignal.aborted, true);
  assert.equal(await vault.load(), undefined);
  assert.equal(auth.connection, undefined);
  auth.fetcher = async () => new Response(JSON.stringify(identity)); await auth.login(token);
  auth.fetcher = async () => new Response(null, { status: 401 });
  assert.equal((await auth.authorizedFetch(`${descriptor.origin}/api/clients`)).status, 401);
  assert.equal(auth.status.state, "invalid_authentication");
  assert.equal(await vault.load(), undefined);
});

test("normal startup never reads injected fixture paths, including when its descriptor is missing or invalid", async (t) => {
  const { options, directory } = await fixture(t);
  let reads = 0;
  const qualificationPaths = {
    get descriptorPath() { reads++; throw new Error("fixture descriptor must not be read"); },
    get credentialPath() { reads++; throw new Error("fixture credential must not be read"); },
  };
  assert.equal((await new DesktopAuth({ ...options, qualificationPaths }).initialize()).state, "locked");
  await fs.unlink(options.descriptorPath);
  assert.equal((await new DesktopAuth({ ...options, qualificationPaths }).initialize()).state, "incomplete_setup");
  await fs.writeFile(options.descriptorPath, "invalid", { mode: 0o600 });
  assert.equal((await new DesktopAuth({ ...options, qualificationPaths }).initialize()).state, "incomplete_setup");
  assert.equal(reads, 0);

  const oldDescriptor = path.join(directory, "local-workbench.json");
  await fs.writeFile(oldDescriptor, JSON.stringify(descriptor), { mode: 0o600 });
  const oldOption = { descriptorPath: oldDescriptor, credentialPath: path.join(directory, "desktop-client.json") };
  assert.equal((await new DesktopAuth({ ...options, legacyPaths: oldOption }).initialize()).state, "incomplete_setup",
    "the removed legacy option cannot recover an invalid selected descriptor");
});

test("qualification explicitly selects its injected loopback fixture and token", async (t) => {
  const { options, directory } = await fixture(t);
  const credentialPath = path.join(directory, "desktop-client.json");
  await fs.writeFile(credentialPath, JSON.stringify({ schema_version: 1, deployment_id: identity.deployment_id,
    owner_id: identity.owner_id, client_id: identity.client_id, client_name: "fixture", token }), { mode: 0o600 });
  const qualificationPaths = { descriptorPath: options.descriptorPath, credentialPath };
  const auth = new DesktopAuth({ ...options, descriptorPath: path.join(directory, "missing.json"), qualificationPaths, qualification: true,
    vault: { available() { throw new Error("fixture must not use the vault"); } } });
  assert.equal((await auth.initialize()).state, "connected");
  assert.equal(auth.connection.authorization, `Bearer ${token}`);

  await fs.writeFile(qualificationPaths.descriptorPath, "invalid", { mode: 0o600 });
  const chosenDescriptorPath = path.join(directory, "chosen.json");
  await fs.writeFile(chosenDescriptorPath, JSON.stringify(descriptor), { mode: 0o600 });
  assert.equal((await new DesktopAuth({ ...options, descriptorPath: chosenDescriptorPath, qualificationPaths, qualification: true }).initialize()).state,
    "incomplete_setup", "an invalid qualification fixture does not fall back to another descriptor");
});

test("descriptor grammar never loosens legacy loopback or accepts embedded secrets", () => {
  assert.throws(() => parseBackendDescriptor({ ...descriptor, origin: "http://192.168.1.2:18790" }), /loopback/);
  const lan = { schema_version: 2, origin: "https://sparkclaw.example:9443", deployment_id: "deployment", owner_id: "owner", tls_certificate_sha256: "a".repeat(64) };
  assert.equal(parseBackendDescriptor(lan).schemaVersion, 2);
  assert.throws(() => parseBackendDescriptor({ ...lan, token }), /unsupported/);
  assert.throws(() => parseBackendDescriptor({ ...lan, origin: "http://sparkclaw.example:9443" }), /HTTPS/);
  assert.throws(() => parseBackendDescriptor({ ...lan, origin: "https://sparkclaw.example:9443/path" }), /canonical/);
});

test("LAN-required startup and configuration reject v1 before credential use while keeping Linux v1 supported", async (t) => {
  const { options, directory } = await fixture(t);
  let requests = 0;
  const auth = new DesktopAuth({ ...options, requireLAN: true, fetcher: async () => { requests++; return new Response(JSON.stringify(identity)); } });
  assert.equal((await auth.initialize()).state, "incomplete_setup");
  assert.equal(auth.descriptor, undefined);
  assert.equal((await auth.login(token)).state, "incomplete_setup");
  assert.equal(requests, 0);
  await assert.rejects(auth.configure(descriptor), /version 2 HTTPS LAN/);
  assert.equal(JSON.parse(await fs.readFile(options.descriptorPath)).schema_version, 1);

  const lan = { schema_version: 2, origin: "https://sparkclaw.example:9443", deployment_id: identity.deployment_id,
    owner_id: identity.owner_id, tls_certificate_sha256: "a".repeat(64) };
  assert.equal((await auth.configure(lan)).state, "locked");
  const configured = await fs.readFile(options.descriptorPath, "utf8");
  await assert.rejects(auth.configure(descriptor), /version 2 HTTPS LAN/);
  assert.equal(await fs.readFile(options.descriptorPath, "utf8"), configured, "rejected v1 cannot replace the LAN description");
  assert.equal((await new DesktopAuth({ ...options, requireLAN: true }).initialize()).state, "locked");

  const fixtureDescriptorPath = path.join(directory, "fixture-workbench.json");
  await fs.writeFile(fixtureDescriptorPath, JSON.stringify(descriptor), { mode: 0o600 });
  const qualified = new DesktopAuth({ ...options, requireLAN: true, qualification: true, descriptorPath: path.join(directory, "missing.json"),
    qualificationPaths: { descriptorPath: fixtureDescriptorPath, credentialPath: path.join(directory, "desktop-client.json") } });
  assert.equal((await qualified.initialize()).state, "incomplete_setup", "qualification cannot bypass the LAN requirement");

  const linux = new DesktopAuth({ ...options, requireLAN: false });
  assert.equal((await linux.configure(descriptor)).state, "locked");
  assert.equal((await linux.login(token)).state, "connected");
});

test("auth IPC permits only the workbench main frame", () => {
  const mainFrame = { url: "sparkclaw-app://workbench/index.html" };
  const window = { webContents: { mainFrame } };
  authorizeWorkbenchSender({ sender: window.webContents, senderFrame: mainFrame }, window);
  assert.throws(() => authorizeWorkbenchSender({ sender: window.webContents, senderFrame: { ...mainFrame } }, window), /not trusted/);
  assert.throws(() => authorizeWorkbenchSender({ sender: {}, senderFrame: mainFrame }, window), /not trusted/);
  mainFrame.url = "https://malicious.example";
  assert.throws(() => authorizeWorkbenchSender({ sender: window.webContents, senderFrame: mainFrame }, window), /not trusted/);
});

test("production login binds the installation before saving a credential and fails closed on drift", async (t) => {
  const { auth, vault } = await fixture(t);
  await auth.initialize();
  const calls = [];
  auth.fetcher = async (url, init) => {
    calls.push(url);
    if (url.endsWith("/installations")) {
      assert.equal(init.method, "POST");
      assert.equal(new Headers(init.headers).get("authorization"), `Bearer ${token}`);
      assert.deepEqual(JSON.parse(init.body), { schema_version: 1, installation_id: installationID });
      assert.equal(await vault.load(), undefined, "binding precedes credential persistence");
    }
    return new Response(JSON.stringify(identity));
  };
  assert.equal((await auth.login(token)).state, "connected");
  assert.deepEqual(calls, ["http://127.0.0.1:18790/api/workbench/identity", "http://127.0.0.1:18790/api/v1/installations"]);
  await auth.logout();
  auth.fetcher = async (url) => new Response(JSON.stringify(url.endsWith("/installations") ? { ...identity, installation_id: crypto.randomUUID() } : identity));
  assert.equal((await auth.login(token)).state, "identity_conflict");
  assert.equal(await vault.load(), undefined);
  const noInstallation = new DesktopAuth({ ...auth, vault, installationID: undefined });
  noInstallation.descriptor = auth.descriptor;
  assert.equal((await noInstallation.login(token)).state, "incomplete_setup");
});

test("workbench response expansion remains bounded and inaccessible to ordinary renderer paths", async (t) => {
  const { auth } = await fixture(t);
  await auth.initialize(); await auth.login(token);
  auth.fetcher = async () => new Response("x".repeat(2 * 1024 * 1024));
  const normal = await auth.authorizedFetch(`${descriptor.origin}/api/clients`);
  await assert.rejects(normal.arrayBuffer(), /allowed size/);
  const execution = await auth.authorizedExecutionFetch(`${descriptor.origin}/api/v1/executions/test`);
  assert.equal((await execution.arrayBuffer()).byteLength, 2 * 1024 * 1024);
  await assert.rejects(auth.authorizedExecutionFetch(`${descriptor.origin}/api/sessions`), /path/);
  await assert.rejects(auth.authorizedExecutionFetch(`${descriptor.origin}/api/v1/executions/test?token=x`), /path/);
  auth.fetcher = async () => new Response("x".repeat(8 * 1024 * 1024 + 1));
  const tooLarge = await auth.authorizedExecutionFetch(`${descriptor.origin}/api/v1/executions/test`);
  await assert.rejects(tooLarge.arrayBuffer(), /allowed size/);
});

test("logout during slow encrypted save fences stale login without deleting a newer device credential", async (t) => {
  const { auth, vault } = await fixture(t);
  await auth.initialize();
  const save = vault.save.bind(vault);
  let releaseSave;
  let startedSave;
  const saveStarted = new Promise((resolve) => { startedSave = resolve; });
  const blockedSave = new Promise((resolve) => { releaseSave = resolve; });
  let saves = 0;
  vault.save = async (record) => {
    if (++saves === 1) { startedSave(); await blockedSave; }
    return save(record);
  };
  const staleLogin = auth.login(token);
  await saveStarted;
  const logout = auth.logout();
  auth.fetcher = async () => new Response(JSON.stringify({ ...identity, client_id: "replacement-device" }));
  const replacement = auth.login(`${token}-replacement`);
  releaseSave();
  await Promise.all([staleLogin, logout, replacement]);
  assert.equal(auth.status.state, "connected");
  assert.equal(auth.connection.clientID, "replacement-device");
  assert.equal((await vault.load()).clientID, "replacement-device");
  assert.equal((await vault.load()).authorization, `Bearer ${token}-replacement`);
});

test("only bounded mail mutations and supported login operations receive longer deadlines; caller cancellation applies", async t => {
  const budgets = [];
  t.mock.method(AbortSignal, "timeout", milliseconds => { budgets.push(milliseconds); return new AbortController().signal; });
  const auth = new DesktopAuth({});
  auth.status = { state: "connected" }; auth.connection = {};
  auth.descriptor = { transport: "iscp", origin: "https://iscp.invalid" };
  auth.transport = { capabilities: {}, async fetch(_url, init) { assert.equal(init.signal.aborted, false); return new Response(null, { status: 204 }); } };
  for (const [route, method] of [["/api/email/drafts", "POST"], ["/api/email/drafts/draft/send", "POST"], ["/api/email/drafts", "GET"], ["/api/email/drafts/draft/reconcile", "POST"], ["/api/owner", "POST"], ["/api/email/providers/qq_mail/check", "POST"], ["/api/email/providers/outlook/login-browser", "POST"], ["/api/email/providers/other/check", "POST"]]) {
    assert.equal((await auth.authorizedFetch(`https://iscp.invalid${route}`, { method })).status, 204);
  }
  assert.deepEqual(budgets, [180000, 180000, 30000, 30000, 30000, 180000, 180000, 30000]);
  const canceled = new AbortController(); canceled.abort();
  auth.transport.fetch = async (_url, init) => { assert.equal(init.signal.aborted, true); return new Response(null, { status: 204 }); };
  await auth.authorizedFetch("https://iscp.invalid/api/email/drafts", { method: "POST", signal: canceled.signal });
});
