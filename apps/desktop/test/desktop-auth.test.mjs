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
const identity = { deployment_id: "test-deployment", owner_id: "test-owner", client_id: "test-device" };
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
  const directory = await fs.mkdtemp(path.join(os.tmpdir(), "sparkclaw-auth-"));
  t.after(() => fs.rm(directory, { recursive: true, force: true }));
  const descriptorPath = path.join(directory, "backend.json");
  await fs.writeFile(descriptorPath, JSON.stringify(descriptor), { mode: 0o600 });
  const vault = new SecureCredentialStore({ directory: path.join(directory, "authentication"), safeStorage: storage, platform: "linux" });
  const options = { descriptorPath, vault, fetcher: async () => new Response(JSON.stringify(identity)) };
  return { options, directory, vault, auth: new DesktopAuth(options) };
}

test("production first launch requires user credential; encrypted restart binds exact device and preserves credential during outage", async (t) => {
  const { auth, options, vault } = await fixture(t);
  assert.equal((await auth.initialize()).state, "locked");
  assert.equal((await auth.login(token)).state, "connected");
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

test("provisioned legacy desktop token is never read in production but qualified loopback fixture still works", async (t) => {
  const { options, directory } = await fixture(t);
  const credentialPath = path.join(directory, "desktop-client.json");
  await fs.writeFile(credentialPath, JSON.stringify({ schema_version: 1, deployment_id: identity.deployment_id,
    owner_id: identity.owner_id, client_id: identity.client_id, client_name: "fixture", token }), { mode: 0o600 });
  const legacyPaths = { descriptorPath: options.descriptorPath, credentialPath };
  assert.equal((await new DesktopAuth({ ...options, legacyPaths }).initialize()).state, "locked");
  assert.equal((await new DesktopAuth({ ...options, legacyPaths, qualification: true }).initialize()).state, "connected");
});

test("descriptor grammar never loosens legacy loopback or accepts embedded secrets", () => {
  assert.throws(() => parseBackendDescriptor({ ...descriptor, origin: "http://192.168.1.2:18790" }), /loopback/);
  const lan = { schema_version: 2, origin: "https://sparkclaw.example:9443", deployment_id: "deployment", owner_id: "owner", tls_certificate_sha256: "a".repeat(64) };
  assert.equal(parseBackendDescriptor(lan).schemaVersion, 2);
  assert.throws(() => parseBackendDescriptor({ ...lan, token }), /unsupported/);
  assert.throws(() => parseBackendDescriptor({ ...lan, origin: "http://sparkclaw.example:9443" }), /HTTPS/);
  assert.throws(() => parseBackendDescriptor({ ...lan, origin: "https://sparkclaw.example:9443/path" }), /canonical/);
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
