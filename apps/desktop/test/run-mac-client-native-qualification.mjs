import assert from "node:assert/strict";
import crypto from "node:crypto";
import fs from "node:fs/promises";
import https from "node:https";
import os from "node:os";
import path from "node:path";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { fileURLToPath } from "node:url";

if (process.platform !== "darwin") throw new Error("Mac client qualification requires a logged-in macOS session");
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../../..");
const temporary = await fs.mkdtemp(path.join(await fs.realpath(os.tmpdir()), "sparkclaw-mac-client-"));
const run = promisify(execFile);
const token = crypto.randomBytes(32).toString("hex");
const identity = { deployment_id: "qualification-deployment", owner_id: "qualification-owner", client_id: "qualification-client" };
const sockets = new Set();
let server, installation, revoked = false;
let identityRequests = 0, installationRequests = 0, streamRequests = 0;
let executionPosts = 0, scheduleLeaseRequests = 0, businessWrites = 0;
try {
  await run("openssl", ["req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1", "-subj", "/CN=localhost", "-addext", "subjectAltName=DNS:localhost,IP:127.0.0.1", "-keyout", path.join(temporary, "key.pem"), "-out", path.join(temporary, "cert.pem")], { timeout: 15000, maxBuffer: 1 << 20 });
  const [key, cert] = await Promise.all([fs.readFile(path.join(temporary, "key.pem")), fs.readFile(path.join(temporary, "cert.pem"))]);
  server = https.createServer({ key, cert }, async (request, response) => {
    if (request.method === "POST" && request.url === "/api/v1/executions") executionPosts++;
    if (request.url.startsWith("/api/v1/schedules/")) scheduleLeaseRequests++;
    if (["POST", "PUT", "PATCH"].includes(request.method) && request.url !== "/api/v1/installations") businessWrites++;
    if (revoked || request.headers.authorization !== `Bearer ${token}`) { response.writeHead(401).end(); return; }
    if (request.method === "GET" && request.url === "/api/workbench/identity") {
      identityRequests++; response.end(JSON.stringify(identity)); return;
    }
    if (request.method === "POST" && request.url === "/api/v1/installations") {
      let input = "";
      for await (const chunk of request) { input += chunk; if (input.length > 4096) { response.writeHead(413).end(); return; } }
      const value = JSON.parse(input);
      assert.equal(Object.keys(value).sort().join(","), "installation_id,schema_version");
      assert.equal(value.schema_version, 1);
      assert.match(value.installation_id, /^[a-f0-9-]{36}$/u);
      if (installation && installation !== value.installation_id) { response.writeHead(409).end(); return; }
      installation = value.installation_id;
      installationRequests++;
      response.end(JSON.stringify({ schema_version: 1, installation_id: installation, ...identity })); return;
    }
    if (request.method === "GET" && request.url === "/api/fixture-stream") {
      streamRequests++; response.writeHead(200, { "Content-Type": "text/event-stream" }); response.write("event: ready\ndata: synthetic\n\n"); return;
    }
    response.writeHead(404).end();
  });
  server.on("connection", (socket) => { sockets.add(socket); socket.once("close", () => sockets.delete(socket)); });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  const descriptor = { schema_version: 2, origin: `https://127.0.0.1:${server.address().port}`, ...identity,
    tls_ca_pem: cert.toString(), tls_certificate_sha256: crypto.createHash("sha256").update(new crypto.X509Certificate(cert).raw).digest("hex") };
  delete descriptor.client_id;
  await fs.writeFile(path.join(temporary, "connection.json"), JSON.stringify(descriptor), { mode: 0o600 });
  const electron = (await import("electron")).default;
  const environment = { ...process.env, SPARKCLAW_MAC_QUALIFICATION_ROOT: temporary, SPARKCLAW_MAC_QUALIFICATION_TOKEN: token };
  delete environment.ELECTRON_RUN_AS_NODE;
  for (const phase of ["prepare", "restore", "revoke"]) {
    revoked = phase === "revoke";
    if (phase === "restore") {
      const expected = JSON.parse(await fs.readFile(path.join(temporary, "expected.json"), "utf8"));
      const untilDue = Date.parse(expected.due_at) - Date.now() + 25;
      assert.ok(Number.isFinite(untilDue) && untilDue <= 5000);
      // No scheduler process runs during this short synthetic offline interval.
      if (untilDue > 0) await new Promise((resolve) => setTimeout(resolve, untilDue));
    }
    const result = await run(electron, [path.join(root, "apps/desktop/test/mac-client-native-fixture.mjs"), phase], { cwd: root, timeout: 60000, maxBuffer: 1 << 20, env: environment });
    process.stdout.write(result.stdout);
  }
  assert.equal(installationRequests, 3);
  assert.equal(identityRequests, 3);
  assert.equal(streamRequests, 1);
  assert.equal(executionPosts, 0, "restart/revocation must not POST an overdue execution");
  assert.equal(scheduleLeaseRequests, 0, "future work must never be registered with the backend");
  assert.equal(businessWrites, 0, "local drafts and recovery do not upload business content");
  console.log(JSON.stringify({ event: "sparkclaw_mac_client_native_qualification", passed: true, platform: process.platform, architecture: process.arch,
    fresh_electron_processes: 3, real_safe_storage: true, encrypted_credential_restart: true, local_data_retained_on_logout_and_revoke: true,
    drafts_and_selected_file_refs_restart: true, recurring_schedule_restart: true, offline_occurrence_missed: true,
    future_occurrence_retained_after_revoke: true, execution_posts: executionPosts, schedule_lease_requests: scheduleLeaseRequests,
    backend: "synthetic_loopback_https_identity_only", business_content_uploaded: false, production_data: false }));
} finally {
  for (const socket of sockets) socket.destroy();
  if (server?.listening) await new Promise((resolve) => server.close(resolve));
  await fs.rm(temporary, { recursive: true, force: true });
}
