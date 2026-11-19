import assert from "node:assert/strict";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { LINK_ORIGIN, LinkAuth, linkCapabilities, linkInstallationID } from "../src/main/link-auth.mjs";
import { linkDomainOperations, linkManifest } from "../src/main/link-domain-wire.mjs";
import { LinkTransport } from "../src/main/link-transport.mjs";
import { fakeBackend, fakeHelper, linkError, until } from "./link-fakes.mjs";

const vault = { available: () => true, sealMutationRecord: (r) => Buffer.from(JSON.stringify(r)), openMutationRecord: (b) => JSON.parse(Buffer.from(b).toString()) };

async function joined({ domain = () => ({ status: 200, body: {} }), describe } = {}) {
  const userData = await fs.mkdtemp(path.join(os.tmpdir(), "link-auth-"));
  const backend = fakeBackend();
  const domainCalls = [];
  backend.handlers["domain.call"] = async (params) => { domainCalls.push(params); return { result: await domain(params) }; };
  if (describe) backend.handlers["device.describe"] = describe;
  const helper = fakeHelper({
    status: async () => ({ joined: false }),
    join: async (m) => { if (!m.credential.startsWith("sparkclaw-link-v1.")) throw linkError("invalid_request"); return { joined: true, device_id: "desktop-1", domain_id: "dom_1", domain_name: "Home", host_device_id: "sparkclaw-host", host_name: "SparkClaw work2", device_name: "Mac" }; },
    open: async () => { queueMicrotask(() => helper.state("iscp")); return { ok: true }; },
    leave: async () => ({ ok: true }),
    request: (op, params) => backend.request(op, params),
  });
  const statuses = [], identities = [];
  let joinedInstallation;
  const auth = new LinkAuth({ stateDirectory: path.join(userData, "link"), userDataDirectory: userData, vault, hostName: "Mac", appVersion: "9.9.9",
    linkFactory: (options) => new LinkTransport({ ...options, spawnProcess: helper.spawnProcess }),
    onChange: (s) => statuses.push(s.state), onIdentity: (identity) => identities.push(identity), onJoined: (id) => { joinedInstallation = id; } });
  await auth.initialize();
  assert.equal(auth.status.state, "incomplete_setup");
  await auth.enroll("sparkclaw-link-v1.credential");
  await until(() => auth.status.state === "connected");
  return { auth, backend, helper, domainCalls, statuses, identities, joinedInstallation, userData };
}

test("a SparkClaw access credential joins, opens the link with the desktop's conditions and verifies the principal", async () => {
  const f = await joined();
  const open = f.helper.sent.find(m => m.method === "open");
  assert.deepEqual(open.describe, { class: "desktop", name: "Mac", app: "SparkX 9.9.9", conditions: { browser_surface: true, attachments: true, microphone: true, screen: "large" } });
  assert.ok(open.reverse.includes("browser.controller.execute") && open.reverse.includes("mcd.checkout.commit"));
  assert.deepEqual(f.auth.identity(), { deployment_id: "sparkclaw-host", owner_id: "owner", client_id: "link-desktop-1" });
  assert.equal(f.joinedInstallation, linkInstallationID("desktop-1"));
  assert.equal(f.auth.status.backend.transport, "link");
  assert.equal(f.auth.status.backend.host_name, "SparkClaw work2");
  assert.equal(f.auth.status.capabilities.mail, true);
  assert.equal(f.auth.status.capabilities.approvals, true, "approvals are served by core link operations");
  assert.equal(f.auth.status.capabilities.surfaces.settings_devices.enabled, false, "devices are managed on the console");
  await assert.rejects(f.auth.enroll("not-a-credential"), /SparkClaw access credential/u);
  f.auth.close();
});

test("the installation identity matches the Gateway's derivation", () => {
  // services/gateway/internal/gateway/link_domain_bridge.go linkInstallationID
  const id = linkInstallationID("desktop-7ig7atsm3qwxlreya4iy2oco");
  assert.match(id, /^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/u);
  assert.equal(id, linkInstallationID("desktop-7ig7atsm3qwxlreya4iy2oco"));
});

test("desktop-gateway requests become domain.call with the original v2 envelope", async () => {
  const f = await joined({ domain: (p) => p.method === "settings.owner.get" ? { status: 200, body: { revision: "r1", value: { id: "owner", display_name: "Owner" } } }
    : p.method === "mail.providers.list" ? { status: 200, body: { providers: [{ provider: "qq_mail" }] } }
    : p.method === "settings.owner.patch" ? { status: 200, body: { revision: "r2", value: { id: "owner", display_name: "New" } } }
    : { status: 404, error_code: "not_found", error: "missing" } });
  const read = await f.auth.authorizedFetch(`${LINK_ORIGIN}/api/email/providers`);
  assert.equal(read.status, 200);
  assert.deepEqual(await read.json(), { providers: [{ provider: "qq_mail" }] });
  const call = f.domainCalls.find(c => c.method === "mail.providers.list");
  assert.equal(call.domain, "mail");
  assert.equal(call.idempotency_key, undefined, "reads carry no idempotency key");
  // The original settings revision handling still unwraps the value.
  const owner = await f.auth.authorizedFetch(`${LINK_ORIGIN}/api/owner`);
  assert.deepEqual(await owner.json(), { id: "owner", display_name: "Owner" });
  const patched = await f.auth.authorizedFetch(`${LINK_ORIGIN}/api/owner`, { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify({ display_name: "New" }) });
  assert.equal(patched.status, 200);
  const mutation = f.domainCalls.find(c => c.method === "settings.owner.patch");
  assert.match(mutation.idempotency_key, /^[a-f0-9-]{36}$/u, "mutations carry the operation id as idempotency key");
  assert.deepEqual(mutation.args.body, { display_name: "New" });
  assert.equal(mutation.args.expected_revision, "r1");
  const missing = await f.auth.authorizedFetch(`${LINK_ORIGIN}/api/medicine/operations/op1`);
  assert.equal(missing.status, 404);
  assert.equal(f.domainCalls.find(c => c.method === "medicine.operations.get").args.params.operation, "op1");
  f.auth.close();
});

test("readiness falls back to the speech domain while the backend lacks the first-version read", async () => {
  const f = await joined({ domain: (p) => {
    if (p.domain === "presentation") throw linkError("unsupported_capability", "not available");
    if (p.method === "speech.status") return { status: 200, body: { enabled: true, ready: true } };
    return { status: 404 };
  } });
  const ready = await f.auth.authorizedFetch(`${LINK_ORIGIN}/readyz`);
  assert.deepEqual(await ready.json(), { ok: true, speech: { enabled: true, ready: true } });
  const config = await f.auth.authorizedFetch(`${LINK_ORIGIN}/api/config`);
  assert.equal(config.status, 501);
  f.auth.close();
});

test("going offline fails business requests at once and reconnecting restores them", async () => {
  const f = await joined({ domain: () => ({ status: 200, body: { notifications: [] } }) });
  f.helper.state("");
  await until(() => f.auth.status.state === "service_unavailable");
  const offline = await f.auth.authorizedFetch(`${LINK_ORIGIN}/api/notifications`);
  assert.equal(offline.status, 503);
  assert.deepEqual(f.auth.identity(), { deployment_id: "sparkclaw-host", owner_id: "owner", client_id: "link-desktop-1" }, "cached data stays readable");
  f.helper.state("lan");
  await until(() => f.auth.status.state === "connected");
  assert.equal((await f.auth.authorizedFetch(`${LINK_ORIGIN}/api/notifications`)).status, 200);
  f.auth.close();
});

test("a revoked device locks and asks to join again", async () => {
  const f = await joined();
  f.helper.event("device", { state: "revoked" });
  await until(() => f.auth.status.state === "invalid_authentication");
  assert.equal(f.auth.status.transport_stage, "authorization_revoked");
  f.auth.close();
});

test("reverse calls reach the original handlers with the v2 request shape", async () => {
  const f = await joined();
  let received;
  f.auth.onReverse = async (request, session) => { received = { request, session }; return { status: 200, body: { available: true } }; };
  const replies = [];
  f.helper.child.stdin.write; // eslint-disable-line no-unused-expressions
  const original = f.helper.sent.length;
  f.helper.reverse("s9", "browser.controller.status", { body: { schema_version: 1 } });
  await until(() => f.helper.sent.slice(original).some(m => m.method === "reverse_response"));
  const reply = f.helper.sent.slice(original).find(m => m.method === "reverse_response");
  replies.push(reply);
  assert.equal(received.request.operation, "browser.controller.status");
  assert.equal(received.request.profile, "sparkclaw.workbench.transport.v2");
  assert.equal(received.request.installation_id, linkInstallationID("desktop-1"));
  assert.deepEqual(received.request.body, { schema_version: 1 });
  assert.equal(received.session.authorization_revision, 1);
  assert.deepEqual(reply.result, { status: 200, body: { available: true } });
  f.auth.close();
});

test("the manifest lists reachable domain operations and the capability projection follows it", () => {
  const ops = linkDomainOperations(["mail", "settings", "clients", "execution"]);
  assert.ok(ops.includes("mail.providers.list") && ops.includes("settings.owner.get"));
  assert.ok(!ops.some(op => op.startsWith("clients.") || op.startsWith("execution.")), "refused groups are not offered");
  const manifest = linkManifest({ sessionID: "link:x", binding: { deployment_id: "d", owner_id: "o", client_id: "c" }, domains: ["mail", "events", "object", "transfer", "approvals"] });
  const caps = linkCapabilities(manifest, true);
  assert.equal(caps.mail, true);
  assert.equal(caps.surfaces.settings_owner.enabled, false, "a backend without the settings domain keeps the entry disabled");
  assert.equal(linkCapabilities(undefined).mail, false);
});
