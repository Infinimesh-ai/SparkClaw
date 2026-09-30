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
  const server = https.createServer({ key, cert }, (request, response) => {
    requests++;
    assert.equal(request.headers.authorization, authorization);
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
  await assert.rejects(pinnedHTTPSFetch(base)("https://elsewhere.example/identity", init), /origin differs/);
  let saved;
  const auth = new DesktopAuth({ vault: { available: () => true, clear: async () => { saved = undefined; }, save: async (value) => { saved = value; } }, fetcher: () => { throw new Error("LAN must use pinned transport"); } });
  auth.descriptor = { schemaVersion: 2, ...base, deploymentID: "deployment", ownerID: "expected-owner" };
  const token = "synthetic-issued-credential-" + "x".repeat(32);
  authorization = `Bearer ${token}`;
  body = { deployment_id: "deployment", owner_id: "wrong-owner", client_id: "device" };
  assert.equal((await auth.login(token)).state, "identity_conflict");
  assert.equal(saved, undefined, "wrong Owner cannot unlock or persist a credential");
  body.owner_id = "expected-owner";
  assert.equal((await auth.login(token)).state, "connected");
  assert.equal(saved.clientID, "device");
  assert.equal(saved.ownerID, "expected-owner");
});
