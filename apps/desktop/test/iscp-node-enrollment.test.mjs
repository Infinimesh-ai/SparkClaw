import assert from "node:assert/strict";
import test from "node:test";
import fs from "node:fs/promises";
import path from "node:path";
import os from "node:os";
import crypto from "node:crypto";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { EventEmitter } from "node:events";
import { PassThrough, Writable } from "node:stream";
import { DesktopAuth } from "../src/main/desktop-auth.mjs";
import { parseBackendDescriptor } from "../src/main/local-backend.mjs";
import { ISCP_CONNECTION_PREFIX, parseISCPConnectionCredential } from "../src/main/iscp-connection-credential.mjs";
import { enrollNodeISCP } from "../src/main/iscp-node-enrollment.mjs";
import { loadISCPProfile } from "../src/main/iscp-profile.mjs";
import { resolveISCPLaunchProfile } from "../src/main/iscp-launch-profile.mjs";

const installationID = "12345678-1234-4123-8123-123456789abc";
let certificate;
test.before(async () => {
  const directory = await fs.mkdtemp(path.join(await fs.realpath(os.tmpdir()), "sparkx-node-cert-"));
  try {
    await promisify(execFile)("openssl", ["req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1", "-subj", "/CN=127.0.0.1", "-addext", "subjectAltName=IP:127.0.0.1", "-keyout", path.join(directory, "key.pem"), "-out", path.join(directory, "cert.pem")], { timeout: 15000 });
    certificate = await fs.readFile(path.join(directory, "cert.pem"), "utf8");
  } finally { await fs.rm(directory, { recursive: true, force: true }); }
});

function identity(device) {
  const publicBytes = crypto.randomBytes(32);
  return { type: "iscp.device.identity.v2", domain_id: "domain", device_id: device, created_at: new Date().toISOString(),
    public_key: { kty: "Ed25519", use: "identity-signature", public: publicBytes.toString("base64url"), kid: crypto.createHash("sha256").update("iscp/v2/thumbprint/Ed25519\0").update(publicBytes).digest("base64url") } };
}
function credentialFixture() {
  const payload = { schema_version: 1, type: "sparkclaw.iscp.connection.v1", origin: "https://127.0.0.1", tls_certificate_pem: certificate,
    domain_id: "domain", relay_id: "relay", relay_signer_identity: identity("relay-signer"), issuer_identity: identity("issuer"), invitation_signer_identity: identity("inviter"),
    issuer_route: "work2-issuer", responder_identity: identity("responder"), deployment_id: "deployment", owner_id: "owner", client_id: "client", invitation_id: crypto.randomUUID(),
    issued_at: new Date(Date.now() - 1000).toISOString(), expires_at: new Date(Date.now() + 15 * 60000).toISOString(), scopes: ["executions.read", "executions.submit"], bootstrap_secret: crypto.randomBytes(32).toString("base64url") };
  payload.signature = { alg: "Ed25519", kid: payload.invitation_signer_identity.public_key.kid, value: crypto.randomBytes(64).toString("base64url") };
  const encode = (value = payload) => ISCP_CONNECTION_PREFIX + Buffer.from(JSON.stringify(value)).toString("base64url");
  return { payload, encode, credential: encode() };
}

async function fixture(t) {
  const directory = await fs.mkdtemp(path.join(await fs.realpath(os.tmpdir()), "sparkx-node-enrollment-"));
  await fs.chmod(directory, 0o700);
  t.after(() => fs.rm(directory, { recursive: true, force: true }));
  const c = credentialFixture(), scope = { deployment_id: "deployment", owner_id: "owner", client_id: "client" };
  const descriptor = { schema_version: 4, mode: "node-managed", transport: "iscp", test_mode: false, origin: "https://iscp.invalid", ...scope, domain_id: "domain", initiator_device_id: "desktop", responder_device_id: "responder", responder_key_thumbprint: c.payload.responder_identity.public_key.kid, relay_url: "https://127.0.0.1", relay_profile: "production" };
  const oldDescriptor = { schema_version: 2, origin: "https://old.example", deployment_id: "old", owner_id: "old-owner", tls_certificate_sha256: "a".repeat(64) };
  const oldRecord = { schema_version: 1, origin: oldDescriptor.origin, deploymentID: "old", ownerID: "old-owner", clientID: "old-client", authorization: "Bearer " + "x".repeat(32), binding: "old-binding" };
  let record = oldRecord, oldClosed = 0;
  const filename = path.join(directory, "iscp-launch-profile.json"), oldSelection = '{"old":"explicit selection"}';
  await fs.writeFile(filename, oldSelection, { mode: 0o600 });
  const vault = { available: () => true, load: async () => record, save: async value => { record = value; }, clear: async () => { record = undefined; } };
  const calls = [], transports = [], manifests = [];
  const nodeEnroller = async ({ directory: privateDir, credential, signal }) => {
    calls.push({ credential, signal });
    assert.equal(auth.status.state, "connected", "previous connection stays active during enrollment");
    await fs.mkdir(privateDir, { recursive: true, mode: 0o700 });
    const helper = path.join(privateDir, "workbench.json"), profile = path.join(privateDir, "connection.profile.json");
    const control = path.join(privateDir, "issuer.control.json"), inviter = path.join(privateDir, "invitation.identity.json");
    await fs.writeFile(control, JSON.stringify({ origin: c.payload.origin, tls_certificate_pem: c.payload.tls_certificate_pem, issuer_route: c.payload.issuer_route,
      binding: scope, issuer_identity: c.payload.issuer_identity, relay_signer_identity: c.payload.relay_signer_identity }), { mode: 0o600 });
    await fs.writeFile(inviter, JSON.stringify(c.payload.invitation_signer_identity), { mode: 0o600 });
    await fs.writeFile(helper, JSON.stringify({ schema_version: 2, mode: "node-managed", role: "initiator", binding: scope, relay_profile: "production", issuer_control_file: control, invitation_identity_file: inviter }), { mode: 0o600 });
    await fs.writeFile(profile, JSON.stringify({ schema_version: 2, transport: "iscp", mode: "node-managed", helper_config: helper, backend: descriptor }), { mode: 0o600 });
    return { ipc_version: 1, type: "enrollment", profile_path: profile, descriptor };
  };
  const transportFactory = options => {
    const manifest = { schema_version: 2, profile: "sparkclaw.workbench.transport.v2", session_id: "session", authorization_revision: 1, expires_at: new Date(Date.now() + 60000).toISOString(), binding: scope, operations: [] };
    manifests.push(manifest);
    const transport = { capabilities: manifest, closed: false, options, async start() {}, close() { this.closed = true; },
      async fetch(url, init) { return new Response(JSON.stringify({ schema_version: 1, ...scope, ...(url.endsWith("/installations") ? { installation_id: JSON.parse(init.body).installation_id } : {}) })); },
      async invoke() { return { schema_version: 2, profile: manifest.profile, session_id: manifest.session_id, authorization_revision: manifest.authorization_revision, expires_at: manifest.expires_at, capabilities: [] }; } };
    transports.push(transport); return transport;
  };
  const options = { vault, descriptorPath: path.join(directory, "backend.json"), installationID, nodeEnroller, transportFactory, requireLAN: true };
  const auth = new DesktopAuth(options);
  auth.descriptor = parseBackendDescriptor(oldDescriptor); auth.connection = oldRecord; auth.status = Object.freeze({ state: "connected" });
  auth.transport = { close() { oldClosed++; } };
  t.after(() => auth.close());
  return { ...c, directory, filename, oldSelection, descriptor, scope, oldDescriptor, oldRecord, vault, options, auth, calls, transports, manifests, get record() { return record; }, get oldClosed() { return oldClosed; } };
}

test("strict node credential rejects expired, duplicate, extended, wrong-role-domain and arbitrary origins before helper use", () => {
  const c = credentialFixture();
  assert.equal(parseISCPConnectionCredential(c.credential).payload.origin, "https://127.0.0.1");
  for (const patch of [{ origin: "https://example.com" }, { origin: "https://127.0.0.1/" }, { origin: "https://127.0.0.1/path" }, { origin: "http://127.0.0.1" }, { origin: "https://user@127.0.0.1" },
    { expires_at: new Date(Date.now() - 1).toISOString() }, { issued_at: new Date(Date.now() + 120000).toISOString() }, { extra: true }, { bootstrap_secret: "short" }, { scopes: ["executions.read", "executions.read"] }, { issuer_route: "https://work2.example" },
    { issuer_identity: { ...c.payload.issuer_identity, domain_id: "other" } }]) assert.throws(() => parseISCPConnectionCredential(c.encode({ ...c.payload, ...patch })), /invalid or expired/);
  for (const raw of [c.credential + "=", " " + c.credential, ISCP_CONNECTION_PREFIX + Buffer.from(JSON.stringify(c.payload).replace('"schema_version":1', '"schema_version":0,"schema_version":1')).toString("base64url"), ISCP_CONNECTION_PREFIX + "x".repeat(70000)]) assert.throws(() => parseISCPConnectionCredential(raw), /invalid or expired/);
});

test("successful enrollment activates only verified node identity and capabilities, preserving userData and private/public boundaries", async t => {
  const f = await fixture(t);
  assert.equal((await f.auth.enroll(f.credential)).state, "connected");
  assert.equal(f.auth.descriptor.mode, "node-managed"); assert.equal(f.auth.status.test_mode, false); assert.equal(f.record.testMode, false); assert.equal(f.oldClosed, 1);
  assert.equal(f.auth.status.client_id, "client"); assert.equal(f.calls.length, 1);
  for (const secret of [f.credential, f.payload.bootstrap_secret, f.auth.iscpProfilePath, "helper_config", "private_key"]) assert.ok(!JSON.stringify(f.auth.status).includes(secret));
  const selection = resolveISCPLaunchProfile({ defaultUserData: f.directory, env: {} });
  assert.equal(selection.configuredUserData, f.directory); assert.equal(selection.allowLocalISCPTest, false); assert.equal(selection.allowNodeManagedISCP, true);
  assert.equal((await loadISCPProfile(selection.iscpProfilePath)).descriptor.testMode, false);
  const restarted = new DesktopAuth({ ...f.options, iscpProfilePath: selection.iscpProfilePath, allowNodeManagedISCP: true });
  t.after(() => restarted.close());
  assert.equal((await restarted.initialize()).state, "connected");
  await restarted.logout();
  assert.equal((await new DesktopAuth({ ...f.options, iscpProfilePath: selection.iscpProfilePath, allowNodeManagedISCP: true }).initialize()).state, "locked", "signout cannot auto-login from private files");
});

test("storage and launch-selection failures retain the prior vault, active connection and selection", async t => {
  for (const failure of ["save", "selection"]) {
    const f = await fixture(t);
    if (failure === "save") { const save = f.vault.save; let fail = true; f.vault.save = async record => { if (fail) { fail = false; throw new Error("OS lock"); } return save(record); }; }
    else f.auth.selectionWriter = () => { throw new Error("disk failure"); };
    await assert.rejects(f.auth.enroll(f.credential), /existing connection is retained/);
    assert.equal(f.record, f.oldRecord); assert.equal(f.auth.connection, f.oldRecord); assert.equal(f.auth.descriptor.origin, f.oldDescriptor.origin); assert.equal(f.auth.status.state, "connected");
    assert.equal(await fs.readFile(f.filename, "utf8"), f.oldSelection); assert.equal(f.oldClosed, 0); assert.equal(f.transports[0].closed, true);
  }
});

test("helper, identity and capability failures cannot publish or persist the staged profile", async t => {
  for (const failure of ["helper", "identity", "capability", "descriptor"]) {
    const f = await fixture(t), originalFactory = f.auth.transportFactory;
    if (failure === "helper") f.auth.nodeEnroller = async () => { throw new Error(f.payload.bootstrap_secret); };
    if (failure === "descriptor") f.descriptor.responder_device_id = "other";
    f.auth.transportFactory = options => {
      const transport = originalFactory(options);
      if (failure === "identity") transport.fetch = async () => new Response(JSON.stringify({ ...f.scope, owner_id: "another-owner" }));
      if (failure === "capability") transport.invoke = async () => ({ session_id: "wrong" });
      return transport;
    };
    const error = await f.auth.enroll(f.credential).then(() => undefined, error => error);
    assert.match(error.message, /existing connection is retained/); assert.ok(!error.message.includes(f.payload.bootstrap_secret));
    assert.equal(f.record, f.oldRecord); assert.equal(f.auth.connection, f.oldRecord); assert.equal(await fs.readFile(f.filename, "utf8"), f.oldSelection);
  }
});

test("logout cancels enrollment and late helper completion cannot restore a connection or overwrite the selection", async t => {
  const f = await fixture(t), original = f.auth.nodeEnroller;
  let release, entered;
  const started = new Promise(resolve => { entered = resolve; }), wait = new Promise(resolve => { release = resolve; });
  f.auth.nodeEnroller = async options => { const result = await original(options); entered(); await wait; return result; };
  const enrollment = f.auth.enroll(f.credential); await started;
  await assert.rejects(f.auth.enroll(f.credential), /already in progress/);
  await f.auth.logout(); assert.equal(f.calls[0].signal.aborted, true); release();
  await assert.rejects(enrollment, /existing connection is retained/);
  assert.equal(f.auth.status.state, "locked"); assert.equal(f.auth.connection, undefined); assert.equal(f.record, undefined); assert.equal(await fs.readFile(f.filename, "utf8"), f.oldSelection);
});

test("native enrollment sends secret only on stdin and rejects nonzero, multiple, oversized or secret-bearing output", async t => {
  const f = await fixture(t);
  const result = await f.options.nodeEnroller({ credential: f.credential, directory: path.join(f.directory, "fixture-profile"), signal: new AbortController().signal });
  for (const failure of [undefined, "nonzero", "multiple", "oversize", "extra", "outside"]) {
    let received, argv;
    const spawnProcess = (_exe, args, options) => {
      argv = args; assert.equal(options.shell, undefined); assert.deepEqual(options.stdio, ["pipe", "pipe", "pipe"]);
      const child = new EventEmitter(); child.stdout = new PassThrough(); child.stderr = new PassThrough(); child.exitCode = null; child.kill = () => { child.exitCode = 1; };
      child.stdin = new Writable({ write(bytes, _encoding, done) { received = bytes.toString("utf8"); done(); }, final(done) {
        queueMicrotask(() => {
          child.stderr.write(f.payload.bootstrap_secret);
          const frame = failure === "extra" ? { ...result, secret: f.payload.bootstrap_secret } : failure === "outside" ? { ...result, profile_path: "/tmp/outside.json" } : result;
          child.stdout.write(failure === "oversize" ? "x".repeat(65537) : JSON.stringify(frame) + "\n" + (failure === "multiple" ? "{}\n" : ""));
          child.exitCode = failure === "nonzero" ? 1 : 0; child.emit("close", child.exitCode);
        }); done();
      } }); return child;
    };
    const promise = enrollNodeISCP({ credential: f.credential, directory: path.dirname(result.profile_path), installationID, spawnProcess });
    if (failure) await assert.rejects(promise, error => !error.message.includes(f.payload.bootstrap_secret)); else assert.deepEqual(await promise, result);
    assert.equal(received, f.credential + "\n"); assert.ok(!argv.join(" ").includes(f.credential)); assert.ok(!argv.join(" ").includes(f.payload.bootstrap_secret)); assert.ok(argv.includes("keyring")); assert.ok(argv.includes(installationID));
  }
});

test("original device-copy adapter preserves the issuer credential and rejects foreign Owner, route and signing pins", async t => {
  const f = await fixture(t); await f.auth.enroll(f.credential);
  const next = { ...f.payload, invitation_id: crypto.randomUUID(), client_id: "second-client", bootstrap_secret: crypto.randomBytes(32).toString("base64url") };
  assert.equal(await f.auth.connectionCredential(f.encode(next)), f.encode(next));
  for (const patch of [{ deployment_id: "other" }, { owner_id: "other" }, { issuer_route: "other-issuer" }, { issuer_identity: identity("other-issuer") },
    { relay_signer_identity: identity("other-relay-signer") }, { responder_identity: identity("other-responder") }, { invitation_signer_identity: identity("other-inviter"), signature: { alg: "Ed25519", kid: "a".repeat(43), value: "b".repeat(86) } }]) {
    await assert.rejects(f.auth.connectionCredential(f.encode({ ...next, ...patch })), /differs|invalid/);
  }
  await assert.rejects(f.auth.connectionCredential("ordinary-bearer-" + "x".repeat(32)), /invalid/);
  assert.equal(f.auth.status.state, "connected");
  await f.auth.logout(); await assert.rejects(f.auth.connectionCredential(f.encode(next)), /Reconnect/);
});
