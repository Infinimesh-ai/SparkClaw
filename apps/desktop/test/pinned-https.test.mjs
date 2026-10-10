import assert from "node:assert/strict";
import test from "node:test";
import https from "node:https";
import fs from "node:fs/promises";
import path from "node:path";
import os from "node:os";
import crypto from "node:crypto";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { pinnedHTTPSFetch } from "../src/main/pinned-https.mjs";
import { DesktopAuth } from "../src/main/desktop-auth.mjs";
import { createConnectionCredential } from "../src/main/connection-credential.mjs";

test("HTTPS validates CA, hostname and leaf pin before transmitting any credential", async (t) => {
  const directory = await fs.mkdtemp(path.join(os.tmpdir(), "sparkclaw-tls-"));
  t.after(() => fs.rm(directory, { recursive: true, force: true }));
  await promisify(execFile)("openssl", ["req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1",
    "-subj", "/CN=localhost", "-addext", "subjectAltName=DNS:localhost,IP:127.0.0.1",
    "-keyout", path.join(directory, "key.pem"), "-out", path.join(directory, "cert.pem")]);
  const [key, cert] = await Promise.all([fs.readFile(path.join(directory, "key.pem")), fs.readFile(path.join(directory, "cert.pem"))]);
  let requests = 0;
  let authorization = "Bearer synthetic-test-token";
  let body = { connected: true };
  let mailHandler;
  const server = https.createServer({ key, cert }, async (request, response) => {
    requests++;
    assert.equal(request.headers.authorization, authorization);
    if (mailHandler && (request.url.startsWith("/api/email/") || request.url === "/api/v1/mail/attachments")) return mailHandler(request, response);
    if (request.method === "DELETE") {
      const chunks = [];
      for await (const chunk of request) chunks.push(chunk);
      const received = Buffer.concat(chunks);
      response.end(JSON.stringify({ input: JSON.parse(received), length: request.headers["content-length"], transferEncoding: request.headers["transfer-encoding"] || null }));
      return;
    }
    response.end(JSON.stringify(body));
  });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  t.after(() => new Promise((resolve) => server.close(resolve)));
  const origin = `https://127.0.0.1:${server.address().port}`;
  const digest = crypto.createHash("sha256").update(new crypto.X509Certificate(cert).raw).digest("hex");
  const init = { headers: { authorization: "Bearer synthetic-test-token" } };
  const base = { origin, certificateSHA256: digest, ca: cert.toString("utf8") };
  await assert.rejects(pinnedHTTPSFetch({ ...base, certificateSHA256: "0".repeat(64) })(`${origin}/identity`, init), /fingerprint differs/);
  assert.equal(requests, 0, "wrong pin must reject before HTTP headers");
  await assert.rejects(pinnedHTTPSFetch({ ...base, ca: undefined })(`${origin}/identity`, init), /self-signed/);
  assert.equal(requests, 0, "pin alone never bypasses trusted chain validation");
  assert.deepEqual(await (await pinnedHTTPSFetch(base)(`${origin}/identity`, init)).json(), { connected: true });
  assert.equal(requests, 1);
  const deletion = JSON.stringify({ command_key: "删除验收", expected_version: 3 });
  const deleted = await pinnedHTTPSFetch(base)(`${origin}/mail`, {
    method: "DELETE", headers: { ...init.headers, "Content-Type": "application/json", "Content-Length": "1", "Transfer-Encoding": "chunked" }, body: deletion,
  });
  assert.deepEqual(await deleted.json(), { input: JSON.parse(deletion), length: String(Buffer.byteLength(deletion)), transferEncoding: null });
  await assert.rejects(pinnedHTTPSFetch(base)("https://elsewhere.example/identity", init), /origin differs/);
  let saved;
  const installationID = "12345678-1234-4123-8123-123456789abc";
  const auth = new DesktopAuth({ installationID, vault: { available: () => true, clear: async () => { saved = undefined; }, save: async (value) => { saved = value; } }, fetcher: () => { throw new Error("LAN must use pinned transport"); } });
  auth.descriptor = { schemaVersion: 2, ...base, deploymentID: "deployment", ownerID: "expected-owner" };
  const token = "synthetic-issued-credential-" + "x".repeat(32);
  authorization = `Bearer ${token}`;
  body = { schema_version: 1, installation_id: installationID, deployment_id: "deployment", owner_id: "wrong-owner", client_id: "device" };
  assert.equal((await auth.login(token)).state, "identity_conflict");
  assert.equal(saved, undefined, "wrong Owner cannot unlock or persist a credential");
  body.owner_id = "expected-owner";
  assert.equal((await auth.login(token)).state, "connected");
  assert.equal(saved.clientID, "device");
  assert.equal(saved.ownerID, "expected-owner");

  const enrolledDescriptor = { schema_version: 2, origin, deployment_id: "deployment", owner_id: "expected-owner",
    tls_certificate_sha256: digest, tls_ca_pem: cert.toString("utf8").trim() };
  const enrolled = new DesktopAuth({ installationID, descriptorPath: path.join(directory, "backend.json"), requireLAN: true,
    vault: { available: () => true, clear: async () => { saved = undefined; }, save: async (value) => { saved = value; } } });
  assert.equal((await enrolled.enroll(createConnectionCredential(enrolledDescriptor, token))).state, "connected");
  assert.deepEqual(JSON.parse(await fs.readFile(path.join(directory, "backend.json"), "utf8")), enrolledDescriptor);
  assert.equal(enrolled.connectionCredential(token), createConnectionCredential(enrolledDescriptor, token));

  // Exercise the installed auth adapter, pinned HTTPS and exact binary size
  // together. Caller-supplied installation headers cannot replace main's scope.
  const mailBytes = Buffer.alloc(10 * 1024 * 1024, 97);
  const mailHash = crypto.createHash("sha256").update(mailBytes).digest("hex");
  const fileID = crypto.randomUUID();
  const object = { object_id: crypto.randomUUID(), version: 1, size: mailBytes.length, sha256: mailHash, purpose: "mail_send_attachment", name: "local.bin", expires_at: new Date(Date.now() + 86400000).toISOString() };
  let uploads = 0, saves = 0, sends = 0;
  let draft = { id: "tls-draft", version: 1, state: "draft", attachments: [{ local_file_id: fileID, object, name: object.name, size_bytes: object.size, sha256: `sha256:${mailHash}` }] };
  enrolled.readLocalFile = (scope, id) => {
    assert.deepEqual(scope, { deployment_id: "deployment", owner_id: "expected-owner", client_id: "device" });
    assert.equal(id, fileID);
    return { content: Buffer.from(mailBytes), name: object.name, size: mailBytes.length, sha256: mailHash };
  };
  mailHandler = async (request, response) => {
    assert.equal(request.headers["x-sparkclaw-installation"], installationID);
    if (request.url.endsWith("compose-capabilities")) return response.end(JSON.stringify({ compose: true, workspace_attachments: true }));
    const chunks = []; for await (const chunk of request) chunks.push(chunk);
    const bytes = Buffer.concat(chunks);
    if (request.url === "/api/v1/mail/attachments") {
      uploads++; assert.deepEqual(bytes, mailBytes); assert.equal(request.headers["x-sparkclaw-digest"], mailHash);
      return response.end(JSON.stringify(object));
    }
    if (request.method === "GET") return response.end(JSON.stringify(draft));
    if (request.url.endsWith("/send")) { sends++; draft = { ...draft, state: "sent" }; }
    else { saves++; assert.deepEqual(JSON.parse(bytes).attachments, [{ local_file_id: fileID, object }]); }
    response.end(JSON.stringify(draft));
  };
  const mailSave = await enrolled.authorizedFetch(`${origin}/api/email/drafts`, { method: "POST", headers: { "x-sparkclaw-installation": "forged-renderer-installation" }, body: JSON.stringify({ id: draft.id, attachments: [{ local_file_id: fileID }] }) });
  assert.equal(mailSave.status, 200); assert.equal((await mailSave.json()).id, draft.id);
  const mailSend = await enrolled.authorizedFetch(`${origin}/api/email/drafts/${draft.id}/send`, { method: "POST", body: JSON.stringify({ expected_version: 1, idempotency_key: "reviewed" }) });
  assert.equal((await mailSend.json()).state, "sent");
  assert.deepEqual([uploads, saves, sends], [1, 1, 1]);
  mailHandler = undefined;
  const budgets = [];
  const timeout = AbortSignal.timeout.bind(AbortSignal);
  t.mock.method(AbortSignal, "timeout", milliseconds => { budgets.push(milliseconds); return timeout(milliseconds); });
  for (const [route, method] of [
    ["/api/email/providers/qq_mail/check", "POST"], ["/api/email/providers/outlook/login-browser", "POST"],
    ["/api/v1/mail/attachments", "POST"], ["/api/owner", "POST"], ["/api/email/providers/other/check", "POST"],
    ["/api/email/drafts/tls-draft/reconcile", "POST"], ["/api/email/drafts/tls-draft/reconcile", "GET"],
    ["/api/email/drafts/tls-draft/reconcile", "PUT"], ["/api/email/drafts/tls-draft/reconcile?unexpected=true", "POST"],
    ["/api/email/drafts/tls-draft/reconcile/extra", "POST"], ["/api/email/drafts/tls-draft", "GET"],
  ]) {
    await (await pinnedHTTPSFetch(base)(origin + route, { ...init, headers: { authorization }, method })).arrayBuffer();
  }
  assert.deepEqual(budgets, [180000, 180000, 180000, 30000, 30000, 180000, 30000, 30000, 30000, 30000, 30000]);

  // The longer reconciliation allowance must not mask an earlier caller abort.
  // Wait for real TLS admission so this covers an in-flight request, not just a
  // signal rejected before the pinned connection opens.
  let observedReconcile;
  const observed = new Promise(resolve => { observedReconcile = resolve; });
  mailHandler = (request) => {
    assert.equal(request.url, "/api/email/drafts/tls-draft/reconcile");
    request.resume();
    observedReconcile();
  };
  const cancellation = new AbortController();
  const reason = new Error("caller cancelled receipt lookup");
  const reconcile = pinnedHTTPSFetch(base)(`${origin}/api/email/drafts/tls-draft/reconcile`, {
    method: "POST", headers: { authorization }, signal: cancellation.signal,
  });
  await observed;
  cancellation.abort(reason);
  await assert.rejects(reconcile, error => error.code === "ABORT_ERR" && error.cause === reason);
  assert.equal(budgets.at(-1), 180000);
});
