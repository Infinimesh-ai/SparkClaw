import assert from "node:assert/strict";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { LINK_ORIGIN, LinkAuth, linkCapabilities, linkInstallationID } from "../src/main/link-auth.mjs";
import { linkDomainOperations } from "../src/main/link-domain-routes.mjs";
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
  const id = linkInstallationID("desktop-7ig7atsm3qwxlreya4iy2oco");
  assert.match(id, /^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/u);
  assert.equal(id, "73878d51-19c8-453a-ba9d-3d287f7cffff");
});

test("desktop requests map to domain.call with typed arguments and mutation keys", async () => {
  const f = await joined({ domain: (p) => p.method === "settings.owner.get" ? { status: 200, body: { revision: "r1", value: { id: "owner", display_name: "Owner" } } }
    : p.method === "mail.providers.list" ? { status: 200, body: { providers: [{ provider: "qq_mail" }] } }
    : p.method === "settings.owner.patch" ? { status: 200, body: { revision: "r2", value: { id: "owner", display_name: "New" } } }
    : p.method === "medicine.operations.get" ? (() => { throw linkError("not_found", "medicine_not_found", false, "medicine_not_found"); })()
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
  assert.deepEqual(mutation.args, { display_name: "New", expected_revision: "r1" }, "settings take typed arguments");
  assert.equal(mutation.args.expected_revision, "r1");
  const missing = await f.auth.authorizedFetch(`${LINK_ORIGIN}/api/medicine/operations/op1`);
  assert.equal(missing.status, 404);
  assert.equal((await missing.json()).code, "medicine_not_found", "a typed failure keeps its domain reason");
  assert.deepEqual(f.domainCalls.find(c => c.method === "medicine.operations.get").args, { operation_id: "op1" }, "medicine takes typed arguments");
  f.auth.close();
});

test("readiness falls back to the speech domain while the backend lacks the first-version read", async () => {
  const f = await joined({ domain: (p) => {
    if (p.domain === "presentation") throw linkError("unsupported_capability", "not available");
    if (p.method === "speech.status") return { enabled: true, ready: true };
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

test("the reachable domain operations follow the domains the backend reported", async () => {
  const ops = linkDomainOperations(["mail", "settings", "clients", "execution", "events", "transfer"]);
  assert.ok(ops.includes("mail.providers.list") && ops.includes("settings.owner.get"));
  assert.ok(!ops.some(op => /^(clients|execution|events|transfer)\./u.test(op)), "refused groups are not offered");
  const f = await joined({ describe: () => ({ principal: { owner_id: "owner", device_id: "desktop-1", class: "desktop" }, features: [], domains: ["mail"] }) });
  const caps = f.auth.status.capabilities;
  assert.equal(caps.mail, true);
  assert.equal(caps.files, true, "files are core link operations");
  assert.equal(caps.surfaces.settings_owner.enabled, false, "a backend without the settings domain keeps the entry disabled");
  assert.equal(linkCapabilities(undefined).mail, false);
  f.auth.close();
});

test("reverse calls outside the browser and McDonald's operations are refused, and oversized replies are bounded", async () => {
  const f = await joined();
  f.auth.onReverse = async () => ({ status: 200, body: { blob: "x".repeat(600 * 1024) } });
  const before = f.helper.sent.length;
  f.helper.reverse("s10", "browser.controller.execute", { body: {} });
  f.helper.reverse("s11", "settings.owner.get", { body: {} });
  await until(() => f.helper.sent.slice(before).filter(m => m.method === "reverse_response").length === 2);
  const replies = Object.fromEntries(f.helper.sent.slice(before).filter(m => m.method === "reverse_response").map(m => [m.reverse_id, m]));
  assert.equal(replies.s10.result.status, 413);
  assert.equal(replies.s11.error.code, "unsupported_capability");
  f.auth.close();
});
