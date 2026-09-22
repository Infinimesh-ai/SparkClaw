import assert from "node:assert/strict";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import { loadLocalBackendConnection, loadLocalBackendDescriptor, verifyLocalBackend } from "../src/main/local-backend.mjs";

async function fixture(t, descriptor = {}, credential = {}) {
  const directory = await fs.mkdtemp(path.join(os.tmpdir(), "sparkclaw-local-backend-"));
  t.after(() => fs.rm(directory, { recursive: true, force: true }));
  await fs.chmod(directory, 0o700);
  const descriptorPath = path.join(directory, "local-workbench.json");
  const credentialPath = path.join(directory, "desktop-client.json");
  await fs.writeFile(descriptorPath, JSON.stringify({
    schema_version: 1, origin: "http://127.0.0.1:18790", deployment_id: "deployment-test", ...descriptor,
  }), { mode: 0o600 });
  await fs.writeFile(credentialPath, JSON.stringify({
    schema_version: 1, deployment_id: "deployment-test", client_id: "client-desktop", owner_id: "owner",
    client_name: "SparkClaw Desktop", token: "x".repeat(48), ...credential,
  }), { mode: 0o600 });
  return { descriptorPath, credentialPath };
}

test("loads one canonical loopback backend and keeps the bearer private", async (t) => {
  const paths = await fixture(t);
  const connection = await loadLocalBackendConnection(paths);
  assert.equal(connection.origin, "http://127.0.0.1:18790");
  assert.equal(connection.authorization, `Bearer ${"x".repeat(48)}`);
  const status = await verifyLocalBackend(connection, async (_url, init) => {
    assert.equal(init.headers.Authorization, connection.authorization);
    return new Response(JSON.stringify({ deployment_id: "deployment-test", owner_id: "owner", client_id: "client-desktop" }));
  });
  assert.deepEqual(status, { schema_version: 1, state: "connected" });
});

test("loads a custom-port descriptor before a credential is available", async (t) => {
	const paths = await fixture(t, { origin: "http://127.0.0.1:28443" });
	await fs.rm(paths.credentialPath);
	const descriptor = await loadLocalBackendDescriptor(paths);
	assert.deepEqual(descriptor, { origin: "http://127.0.0.1:28443", deploymentID: "deployment-test" });
	await assert.rejects(loadLocalBackendConnection(paths));
});

test("rejects non-loopback origins, mismatched deployments, and loose credentials", async (t) => {
  const remote = await fixture(t, { origin: "http://192.168.1.20:18790" });
  await assert.rejects(loadLocalBackendConnection(remote), /loopback origin/);
  const mismatch = await fixture(t, {}, { deployment_id: "another-installation" });
  await assert.rejects(loadLocalBackendConnection(mismatch), /do not identify the same deployment/);
  const loose = await fixture(t);
  await fs.chmod(loose.credentialPath, 0o644);
  await assert.rejects(loadLocalBackendConnection(loose), /mode 0600/);
});

test("fails closed on redirects and identity mismatch", async (t) => {
  const paths = await fixture(t);
  const connection = await loadLocalBackendConnection(paths);
  assert.equal((await verifyLocalBackend(connection, async () => new Response("", { status: 302 }))).state, "identity_conflict");
  assert.equal((await verifyLocalBackend(connection, async () => new Response(JSON.stringify({
    deployment_id: "wrong", owner_id: "owner", client_id: "client-desktop",
  })))).state, "identity_conflict");
});
