import assert from "node:assert/strict";
import fs from "node:fs/promises";
import http from "node:http";
import os from "node:os";
import path from "node:path";
import { spawn, spawnSync } from "node:child_process";
import { test } from "node:test";
import { claimCredential, parseCredentialArguments, issuedClientID, socketRequest } from "../lib/credentials.mjs";
import { parseConnectionCredential } from "../../apps/desktop/src/main/connection-credential.mjs";

const root = path.resolve(import.meta.dirname, "../..");
const secret = "fake_credential_only_for_isolated_tests_0000000000";

async function fixture(t) {
  const runtime = await fs.mkdtemp(path.join(os.tmpdir(), "sparkclaw-credentials-"));
  await fs.chmod(runtime, 0o700);
  const result = spawnSync(process.execPath, [path.join(root, "scripts/provision-local-workbench.mjs"), "--runtime-dir", runtime, "--origin", "http://127.0.0.1:18790", "--deployment-id", "fake-deployment"], { encoding: "utf8" });
  assert.equal(result.status, 0, result.stderr);
  const admin = JSON.parse(await fs.readFile(path.join(runtime, "local-management.json"), "utf8"));
  await fs.writeFile(path.join(runtime, "client-backend.json"), JSON.stringify({
    schema_version: 2,
    origin: "https://sparkclaw.test:18790",
    deployment_id: admin.deployment_id,
    owner_id: admin.owner_id,
    tls_certificate_sha256: "a".repeat(64),
  }), { mode: 0o600 });
  const directory = path.join(runtime, "management");
  const socketPath = path.join(directory, "credentials.sock");
  const server = http.createServer((_, response) => { response.end("{}"); });
  await new Promise(resolve => server.listen(socketPath, resolve));
  await fs.chmod(socketPath, 0o600);
  t.after(async () => { await new Promise(resolve => server.close(resolve)); await fs.rm(runtime, { recursive: true, force: true }); });
  let printed = "";
  const output = { isTTY: true, write: value => { printed += value; } };
  const options = parseCredentialArguments(["initial", "--runtime-dir", runtime, "--name", "My test Mac"]);
  const calls = [];
  const request = async call => {
    calls.push(call);
    if (call.route.endsWith("identity")) return { status: 200, body: { deployment_id: admin.deployment_id, owner_id: admin.owner_id, client_id: admin.client_id } };
    if (call.route.endsWith("revoke")) return { status: 200, body: { id: options.revokeID, revoked_at: new Date().toISOString() } };
    const files = await fs.readdir(directory);
    const journals = await Promise.all(files.filter(name => /^(initial|recovery-).*\.json$/u.test(name)).map(async filename => JSON.parse(await fs.readFile(path.join(directory, filename), "utf8"))));
    const journal = journals.find(value => value.request_key === call.key);
    assert.ok(journal, "request key must be durable before issuance");
    assert.equal(journal.state, "pending_issue");
    assert.equal(call.body.client_name, journal.name);
    return { status: 201, body: { token: secret, client: { id: issuedClientID(admin.owner_id, call.key), name: call.body.client_name, owner_id: admin.owner_id } } };
  };
  const deps = { input: { isTTY: true }, output, errorOutput: { isTTY: true }, request };
  return { runtime, directory, socketPath, server, admin, calls, options, deps, request, printed: () => printed };
}

async function assertNoSecretInRecords(f) {
  for (const filename of await fs.readdir(f.directory)) {
    const info = await fs.lstat(path.join(f.directory, filename));
    if (!info.isFile()) continue;
    const raw = await fs.readFile(path.join(f.directory, filename), "utf8");
    assert.ok(!raw.includes(secret));
    assert.ok(!raw.includes(f.admin.token));
    assert.equal(info.mode & 0o777, 0o600);
  }
}

test("initial persists key before issue, displays once, and completed repeat makes no issue", async t => {
  const f = await fixture(t);
  await claimCredential(f.options, f.deps);
  const encoded = f.printed().match(/sparkclaw-connect-v1\.[A-Za-z0-9_-]+/u)?.[0];
  assert.ok(encoded);
  assert.equal(parseConnectionCredential(encoded).token, secret);
  assert.equal(f.printed().includes(secret), false, "the raw token is never printed separately");
  const before = f.calls.length;
  await claimCredential({ ...f.options, name: "Another name" }, f.deps);
  assert.equal(f.calls.length, before);
  assert.equal(f.printed().match(/sparkclaw-connect-v1\./gu)?.length, 1);
  await assertNoSecretInRecords(f);
});

test("response loss retries original key and name without issuing another device", async t => {
  const f = await fixture(t);
  let lost = true;
  const request = async call => {
    const result = await f.request(call);
    if (call.route === "/api/clients" && lost) { lost = false; throw new Error(`ignored server detail ${secret}`); }
    return result;
  };
  await assert.rejects(claimCredential(f.options, { ...f.deps, request }), /outcome is unknown/u);
  assert.equal(f.printed(), "");
  const pending = JSON.parse(await fs.readFile(path.join(f.directory, "initial.json"), "utf8"));
  await assert.rejects(claimCredential({ ...f.options, name: "Changed" }, f.deps), /original device name/u);
  await claimCredential(f.options, { ...f.deps, request });
  assert.deepEqual(f.calls.filter(call => call.route === "/api/clients").map(call => call.key), [pending.request_key, pending.request_key]);
  await assertNoSecretInRecords(f);
});

test("service restart or plaintext expiry guides explicit revoke/reissue while preserving key", async t => {
  const f = await fixture(t);
  const request = async call => call.route === "/api/clients" ? { status: 409, body: { code: "CLIENT_CREDENTIAL_UNRECOVERABLE", client_id: issuedClientID(f.admin.owner_id, call.key) } } : f.request(call);
  await assert.rejects(claimCredential(f.options, { ...f.deps, request }), /credentials:recover.*--revoke-id client_web_/u);
  const first = JSON.parse(await fs.readFile(path.join(f.directory, "initial.json"), "utf8"));
  await assert.rejects(claimCredential(f.options, { ...f.deps, request }), /cannot be recovered/u);
  const second = JSON.parse(await fs.readFile(path.join(f.directory, "initial.json"), "utf8"));
  assert.equal(first.request_key, second.request_key);
  assert.equal(f.printed(), "");
  await assertNoSecretInRecords(f);
});

test("recovery revokes specified lost device first and keeps replacement request key on retry", async t => {
  const f = await fixture(t);
  f.options = parseCredentialArguments(["recover", "--runtime-dir", f.runtime, "--revoke-id", "lost-device", "--name", "Replacement"]);
  const request = async call => {
    if (call.route.endsWith("revoke")) {
      f.calls.push(call);
      const records = await fs.readdir(f.directory);
      const record = JSON.parse(await fs.readFile(path.join(f.directory, records.find(name => name.startsWith("recovery-"))), "utf8"));
      assert.equal(record.state, "pending_revoke");
      return { status: 200, body: { id: "lost-device", revoked_at: new Date().toISOString() } };
    }
    return f.request(call);
  };
  await claimCredential(f.options, { ...f.deps, request });
  assert.deepEqual(f.calls.map(call => call.route), ["/api/workbench/identity", "/api/clients/lost-device/revoke", "/api/clients"]);
  await assertNoSecretInRecords(f);
});

test("failed recovery revocation never signs a replacement", async t => {
  const f = await fixture(t);
  const options = parseCredentialArguments(["recover", "--runtime-dir", f.runtime, "--revoke-id", "lost-device", "--name", "Replacement"]);
  const request = async call => call.route.endsWith("revoke") ? { status: 404, body: {} } : f.request(call);
  await assert.rejects(claimCredential(options, { ...f.deps, request }), /no replacement/u);
  assert.equal(f.calls.filter(call => call.route === "/api/clients").length, 0);
});

test("all redirected streams reject before any file access or request", async () => {
  for (const stream of ["input", "output", "errorOutput"]) {
    const deps = { input: { isTTY: true }, output: { isTTY: true }, errorOutput: { isTTY: true }, request: () => assert.fail("must not request") };
    deps[stream] = { isTTY: false };
    await assert.rejects(claimCredential({ runtimeDirectory: "/does-not-exist" }, deps), /interactive terminal/u);
  }
  const child = spawnSync(process.execPath, [path.join(root, "scripts/credentials.mjs"), "initial", "--name", "test"], { encoding: "utf8" });
  assert.notEqual(child.status, 0);
  assert.match(child.stderr, /interactive terminal/u);
  assert.equal(child.stdout, "");
});

test("non-private descriptor, management credential, client backend, directory and socket fail before issuance", async t => {
  for (const name of ["local-workbench.json", "local-management.json", "client-backend.json", "management", "management/credentials.sock"]) {
    const f = await fixture(t);
    const filename = path.join(f.runtime, name);
    await fs.chmod(filename, name === "management" ? 0o755 : 0o644);
    await assert.rejects(claimCredential(f.options, f.deps), /mode 0[67]00/u);
    assert.equal(f.calls.length, 0);
  }
});

test("symlink runtime, credential, descriptors, journal and socket fail closed", async t => {
  for (const name of ["local-workbench.json", "local-management.json", "client-backend.json", "management/credentials.sock", "management/initial.json"]) {
    const f = await fixture(t);
    const filename = path.join(f.runtime, name);
    if (name.endsWith("initial.json")) await fs.writeFile(filename, "{}", { mode: 0o600 });
    await fs.rename(filename, `${filename}.real`);
    await fs.symlink(`${filename}.real`, filename);
    await assert.rejects(claimCredential(f.options, f.deps), /symbolic links/u);
    assert.equal(f.calls.length, 0);
  }
  const f = await fixture(t);
  await fs.symlink(f.runtime, `${f.runtime}-link`);
  t.after(() => fs.unlink(`${f.runtime}-link`));
  await assert.rejects(claimCredential({ ...f.options, runtimeDirectory: `${f.runtime}-link` }, f.deps), /symbolic links/u);
});

test("wrong deployment identity or user client identity prevents issuance", async t => {
  const f = await fixture(t);
  for (const mismatch of ["deployment_id", "owner_id", "client_id"]) {
    await assert.rejects(claimCredential(f.options, { ...f.deps, request: async () => ({ status: 200, body: { deployment_id: f.admin.deployment_id, owner_id: f.admin.owner_id, client_id: f.admin.client_id, [mismatch]: "wrong" } }) }), /identity verification/u);
  }
  assert.equal(f.printed(), "");
});

test("a mismatched public client backend cannot redirect an issued token", async t => {
  for (const field of ["deployment_id", "owner_id"]) {
    const f = await fixture(t);
    const filename = path.join(f.runtime, "client-backend.json");
    const backend = JSON.parse(await fs.readFile(filename, "utf8"));
    await fs.writeFile(filename, JSON.stringify({ ...backend, [field]: "other" }), { mode: 0o600 });
    await assert.rejects(claimCredential(f.options, f.deps), /identity does not match/u);
    assert.equal(f.calls.length, 0);
  }
});

test("actual Unix HTTP transport uses saved management secret and no network requests", async t => {
  const f = await fixture(t);
  f.server.removeAllListeners("request");
  f.server.on("request", async (req, response) => {
    assert.equal(req.headers.authorization, `Bearer ${f.admin.token}`);
    let raw = "";
    for await (const chunk of req) raw += chunk;
    response.setHeader("Content-Type", "application/json");
    if (req.url.endsWith("identity")) response.end(JSON.stringify({ deployment_id: f.admin.deployment_id, owner_id: f.admin.owner_id, client_id: f.admin.client_id }));
    else {
      const name = JSON.parse(raw).client_name;
      const journal = JSON.parse(await fs.readFile(path.join(f.directory, "initial.json"), "utf8"));
      assert.equal(req.headers["idempotency-key"], journal.request_key);
      response.writeHead(201).end(JSON.stringify({ token: secret, client: { id: journal.client_id, name, owner_id: f.admin.owner_id } }));
    }
  });
  const { request: _, ...deps } = f.deps;
  await claimCredential(f.options, deps);
  assert.match(f.printed(), /shown once/u);
  assert.ok(f.printed().includes(`Owner: ${f.admin.owner_id}`));
  await assertNoSecretInRecords(f);
});

test("invalid private JSON never echoes its contents into errors", async t => {
  const f = await fixture(t);
  await fs.writeFile(path.join(f.runtime, "local-management.json"), `{ ${secret}`);
  await assert.rejects(claimCredential(f.options, f.deps), error => /invalid JSON/u.test(error.message) && !error.message.includes(secret));
});

test("provisioner refuses symlink or public runtime instead of changing its permissions", async t => {
  const f = await fixture(t);
  await fs.chmod(f.runtime, 0o755);
  const child = spawnSync(process.execPath, [path.join(root, "scripts/provision-local-workbench.mjs"), "--runtime-dir", f.runtime, "--origin", "http://127.0.0.1:18790"], { encoding: "utf8" });
  assert.notEqual(child.status, 0);
  assert.equal((await fs.stat(f.runtime)).mode & 0o777, 0o755);
  assert.ok(!child.stdout.includes(f.admin.token));
});

test("provisioner derives and checks the public pinned backend descriptor", async t => {
  const runtime = await fs.mkdtemp(path.join(os.tmpdir(), "sparkclaw-client-backend-"));
  await fs.chmod(runtime, 0o700);
  t.after(() => fs.rm(runtime, { recursive: true, force: true }));
  const key = path.join(runtime, "server.key");
  const cert = path.join(runtime, "server.crt");
  const generated = spawnSync("openssl", ["req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1", "-subj", "/CN=sparkclaw.test", "-keyout", key, "-out", cert], { encoding: "utf8" });
  assert.equal(generated.status, 0, generated.stderr);
  const args = [path.join(root, "scripts/provision-local-workbench.mjs"), "--runtime-dir", runtime, "--origin", "http://127.0.0.1:18790", "--deployment-id", "deployment", "--client-origin", "https://sparkclaw.test:18790", "--client-tls-cert", cert, "--client-tls-ca", cert];
  const provisioned = spawnSync(process.execPath, args, { encoding: "utf8" });
  assert.equal(provisioned.status, 0, provisioned.stderr);
  const backend = JSON.parse(await fs.readFile(path.join(runtime, "client-backend.json"), "utf8"));
  assert.equal(backend.origin, "https://sparkclaw.test:18790");
  assert.match(backend.tls_certificate_sha256, /^[a-f0-9]{64}$/u);
  assert.match(backend.tls_ca_pem, /^-----BEGIN CERTIFICATE-----/u);
  const checked = spawnSync(process.execPath, [...args, "--check"], { encoding: "utf8" });
  assert.equal(checked.status, 0, checked.stderr);
});

test("management HTTP deadline is absolute even when responses keep dripping", async t => {
  const f = await fixture(t);
  f.server.removeAllListeners("request");
  f.server.on("request", (_, response) => {
    response.writeHead(200);
    const interval = setInterval(() => response.write(" "), 5);
    response.on("close", () => clearInterval(interval));
  });
  const start = Date.now();
  await assert.rejects(socketRequest({ socketPath: f.socketPath, origin: "http://127.0.0.1:18790", token: f.admin.token, method: "GET", route: "/api/workbench/identity", timeoutMS: 50 }), /service is unavailable|response failed/u);
  assert.ok(Date.now() - start < 500);
});

test("actual interactive CLI displays once through a pseudo-terminal", async t => {
  const f = await fixture(t);
  f.server.removeAllListeners("request");
  let issues = 0;
  f.server.on("request", async (req, response) => {
    assert.equal(req.headers.authorization, `Bearer ${f.admin.token}`);
    let raw = "";
    for await (const chunk of req) raw += chunk;
    response.setHeader("Content-Type", "application/json");
    if (req.url.endsWith("identity")) response.end(JSON.stringify({ deployment_id: f.admin.deployment_id, owner_id: f.admin.owner_id, client_id: f.admin.client_id }));
    else {
      issues++;
      const journal = JSON.parse(await fs.readFile(path.join(f.directory, "initial.json"), "utf8"));
      assert.equal(req.headers["idempotency-key"], journal.request_key);
      response.writeHead(201).end(JSON.stringify({ token: secret, client: { id: journal.client_id, name: JSON.parse(raw).client_name, owner_id: f.admin.owner_id } }));
    }
  });
  const python = `import os, pty, subprocess, sys
master, slave = pty.openpty()
child = subprocess.Popen(sys.argv[1:], stdin=slave, stdout=slave, stderr=slave)
os.close(slave)
try:
 while True:
  chunk = os.read(master, 65536)
  if not chunk: break
  sys.stdout.buffer.write(chunk)
except OSError: pass
finally: os.close(master)
sys.exit(child.wait())`;
  const run = () => new Promise((resolve, reject) => {
    const child = spawn("python3", ["-c", python, process.execPath, path.join(root, "scripts/credentials.mjs"), "initial", "--runtime-dir", f.runtime, "--name", "PTY device"], { stdio: ["ignore", "pipe", "pipe"], timeout: 5000 });
    let text = "";
    child.stdout.on("data", chunk => { text += chunk; });
    child.on("error", reject);
    child.on("close", code => { if (code !== 0) reject(new Error(`CLI failed: ${text}`)); else resolve(text); });
  });
  const first = await run();
  const second = await run();
  const delivered = first.match(/sparkclaw-connect-v1\.[A-Za-z0-9_-]+/u)?.[0];
  assert.ok(delivered);
  assert.equal(parseConnectionCredential(delivered).token, secret);
  assert.ok(!first.includes(secret));
  assert.ok(!second.includes("sparkclaw-connect-v1."));
  assert.equal(issues, 1);
  await assertNoSecretInRecords(f);
});
