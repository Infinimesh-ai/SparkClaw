import assert from "node:assert/strict";
import test from "node:test";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { validateLabInput, verifyEnrolledPair, prepareLab, verifyLab, upLab, smokeLab, issuerContainerConfig, helperRenewalConfig, validateIssuerRenewalCapability } from "../iscp-local-lab.mjs";
import { command, containerState, issuerContainerArguments, labLabel, stopLab, waitRelay } from "../lib/iscp-docker-lab.mjs";

const input = () => ({ schema_version: 1, relay_profile: "local-lab", deployment_id: "lab", owner_id: "owner", client_id: "desktop-client" });
const pair = (id) => ({
  identity: { type: "iscp.device.identity.v2", domain_id: "local", device_id: id, public_key: { kty: "Ed25519" } },
  enrollment: { type: "sparkclaw.bridge.enrollment.v1", mode: "workbench-local-lab", domain_id: "local", device_id: id, relay_id: "relay",
    relay_base_url: id === "desktop" ? "http://127.0.0.1:18080" : "http://iscp-relay:8080",
    relay_websocket_url: id === "desktop" ? "ws://127.0.0.1:18080/v2/relay/connect" : "ws://iscp-relay:8080/v2/relay/connect",
    expires_at: "2030-01-01T00:00:00Z", relay_signer_identity: { domain_id: "local", device_id: "relay-signer", public_key: { kid: "pinned-key" } },
    access: { domain_id: "local", device_id: id, token: "test-access", expires_at: "2030-01-01T00:00:00Z" },
    refresh: { domain_id: "local", device_id: id, token: "test-refresh", expires_at: "2030-01-01T00:00:00Z" } },
});

test("lab explicitly selects local Relay and rejects cloud credential input", () => {
  assert.equal(validateLabInput(input()).client_id, "desktop-client");
  for (const change of [value => delete value.relay_profile, value => value.relay_profile = "production", value => value.owner_id = "another", value => value.desktop = { enrollment_file: "/private/cloud.json" }]) {
    const value = input(); change(value); assert.throws(() => validateLabInput(value));
  }
});

test("local enrollment pair accepts host/container aliases and refreshable access, rejects remote/plain mixed routing and changed signer", () => {
  verifyEnrolledPair(pair("desktop"), pair("backend"), Date.UTC(2026, 9, 8));
  const refreshable = pair("backend"); refreshable.enrollment.access.expires_at = "2020-01-01T00:00:00Z";
  verifyEnrolledPair(pair("desktop"), refreshable, Date.UTC(2026, 9, 8));
  for (const change of [value => value.enrollment.access.token = "", value => value.enrollment.mode = "managed",
    value => value.enrollment.access.expires_at = "invalid", value => value.enrollment.refresh.expires_at = "2020-01-01T00:00:00Z",
    value => value.enrollment.relay_base_url = "http://iscp.infinimesh.cloud", value => value.enrollment.relay_base_url = "https://iscp.infinimesh.cloud",
    value => value.enrollment.relay_websocket_url = "wss://iscp.infinimesh.cloud/v2/relay/connect", value => value.enrollment.refresh.device_id = "wrong",
    value => value.identity.domain_id = "wrong", value => value.identity.device_id = "desktop", value => value.enrollment.relay_signer_identity.public_key.kid = "changed"] ) {
    const backend = pair("backend"); change(backend);
    assert.throws(() => verifyEnrolledPair(pair("desktop"), backend, Date.UTC(2026, 9, 8)));
  }
});

test("issuer runtime is pinned, lab-owned, isolated and publishes only loopback while helpers receive no management credential", () => {
  const directory = "/private/lab";
  const metadata = { lab_id: "lab", issuer_container: "lab-issuer", network: "internal", ingress_network: "loopback-ingress", source: { image_id: "sha256:pinned-runtime" } };
  const args = issuerContainerArguments(metadata, directory, 501);
  assert.equal(args[args.indexOf("--publish") + 1], "127.0.0.1::8080");
  assert.equal(args[args.indexOf("--label") + 1], `${labLabel}=lab`);
  assert.equal(args[args.indexOf("--entrypoint") + 1], "/lab/bin/issuer-linux");
  assert.equal(args[args.indexOf("--user") + 1], "501");
  assert.ok(args.includes("--read-only")); assert.ok(args.includes("no-new-privileges")); assert.ok(args.includes("sha256:pinned-runtime"));
  assert.ok(args.includes("-allow-container-listen")); assert.ok(!args.some(arg => arg.includes("token")));
  const config = issuerContainerConfig({ schema_version: 1, mode: "local-test", directory: `${directory}/issuer`, subject_identity_file: `${directory}/desktop/device.identity.json`, audience_identity_file: `${directory}/gateway/device.identity.json`, relay_id: "relay" }, directory);
  assert.equal(config.directory, "/lab/issuer"); assert.equal(config.subject_identity_file, "/lab/desktop/device.identity.json");
  assert.throws(() => issuerContainerConfig({ ...config, directory: "/private/another-lab" }, directory), /inside/u);
  const desktop = helperRenewalConfig(directory, "desktop", "http://127.0.0.1:19091"), gateway = helperRenewalConfig(directory, "gateway", "http://iscp-local-issuer:8080");
  assert.notEqual(desktop.pending_file, gateway.pending_file); assert.equal(desktop.poll_interval_seconds, 10);
  assert.deepEqual(Object.keys(desktop).sort(), ["pending_file", "poll_interval_seconds", "url"]);
  for (const url of ["http://remote.example", "http://0.0.0.0:8080", "http://127.0.0.1:19091/v1/current", "http://user:password@127.0.0.1:19091"]) assert.throws(() => helperRenewalConfig(directory, "desktop", url));
});

test("issuer startup requires the SDK descriptor envelope, fixed pair and bounded authorization metadata", () => {
  const now = Date.UTC(2026, 9, 8), metadata = { domain_id: "domain", relay_id: "relay", desktop_device_id: "desktop", gateway_device_id: "gateway" }, issuer = { device_id: "issuer" };
  const capability = () => ({ type: "iscp.signed_descriptor.v2", descriptor_type: "iscp.trust_root.descriptor.v2", descriptor: {
    type: "iscp.trust_root.descriptor.v2", trust_root_id: "issuer", domain_id: "domain", issued_at: new Date(now).toISOString(), expires_at: new Date(now + 300000).toISOString(),
    metadata: { purpose: "sparkclaw-local-grant-renewal", grant_renewal: "true", issuer_device_id: "issuer", relay_id: "relay", subject_device_id: "desktop", audience_device_id: "gateway", permission: "sparkclaw.workbench.v1", authorization_expires_at: new Date(now + 86400000).toISOString() } } });
  assert.doesNotThrow(() => validateIssuerRenewalCapability(capability(), metadata, issuer, now));
  for (const change of [value => value.type = "ad-hoc-capability", value => value.descriptor_type = "iscp.device.identity.v2", value => value.descriptor.domain_id = "another",
    value => value.descriptor.metadata.grant_renewal = "false", value => value.descriptor.metadata.subject_device_id = "another", value => value.descriptor.metadata.permission = "unrestricted",
    value => value.descriptor.expires_at = new Date(now + 360000).toISOString(), value => value.descriptor.metadata.authorization_expires_at = new Date(now + 1000).toISOString()]) {
    const value = capability(); change(value); assert.throws(() => validateIssuerRenewalCapability(value, metadata, issuer, now));
  }
});

test("up refuses an old lab without managed renewal instead of silently reauthorizing its Grant", async (t) => {
  const directory = await fs.realpath(await fs.mkdtemp(path.join(os.tmpdir(), "iscp-legacy-lab-")));
  await fs.chmod(directory, 0o700); t.after(() => fs.rm(directory, { recursive: true, force: true }));
  await fs.writeFile(path.join(directory, "run.json"), JSON.stringify({ schema_version: 1, lab_id: "legacy", relay_profile: "local-lab" }), { mode: 0o600 });
  await assert.rejects(upLab(directory), /prepare a fresh lab/u);
  await assert.rejects(fs.stat(path.join(directory, "issuer")), { code: "ENOENT" });
});

test("real upstream Docker Relay supports PoP enrollment and normal Desktop durable execution/reconnect", { skip: process.env.SPARKCLAW_TEST_DOCKER_RELAY !== "1", timeout: 300000 }, async (t) => {
  const directory = await fs.realpath(await fs.mkdtemp(path.join(os.tmpdir(), "iscp-docker-acceptance-")));
  await fs.chmod(directory, 0o700);
  const inputFile = path.join(directory, "input.json");
  await fs.writeFile(inputFile, JSON.stringify(input()), { mode: 0o600 });
  const lab = path.join(directory, "lab");
  let metadata, completed = false;
  t.after(async () => {
    if (metadata) await stopLab(metadata, lab);
    if (completed) await fs.rm(directory, { recursive: true, force: true });
    else process.stdout.write(`Failed local lab evidence retained privately at ${directory}\n`);
  });
  metadata = await prepareLab(inputFile, lab);
  const deviceResponse = await fetch(`${metadata.relay_url}/v2/relay/admin/devices`, { signal: AbortSignal.timeout(5000), redirect: "error" });
  assert.equal(deviceResponse.status, 200);
  assert.deepEqual(Object.keys(await deviceResponse.json()).sort(), [metadata.desktop_device_id, metadata.gateway_device_id].sort());
  const publicIdentity = JSON.parse(await fs.readFile(path.join(lab, "desktop/device.identity.json"), "utf8"));
  const rejected = await fetch(`${metadata.relay_url}/v2/relay/devices/bind-self`, { method: "POST", headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ identity: publicIdentity, proof: {} }), signal: AbortSignal.timeout(5000), redirect: "error" });
  assert.equal(rejected.status, 401, "Real Relay must reject registration without a signed possession proof");
  await verifyLab(lab);
  const network = JSON.parse(command("docker", ["network", "inspect", metadata.network]))[0];
  assert.equal(network.Internal, true); assert.equal(network.Labels[labLabel], metadata.lab_id);
  assert.equal(containerState(metadata.relay_container, metadata.lab_id).NetworkSettings.Ports["8080/tcp"][0].HostIp, "127.0.0.1");
  const issuerContainer = containerState(metadata.issuer_container, metadata.lab_id);
  assert.equal(issuerContainer.NetworkSettings.Ports["8080/tcp"][0].HostIp, "127.0.0.1");
  assert.equal(issuerContainer.Image, metadata.source.image_id);
  assert.deepEqual(Object.keys(issuerContainer.NetworkSettings.Networks).sort(), [metadata.ingress_network, metadata.network].sort());
  const desktopHelper = JSON.parse(await fs.readFile(path.join(lab, "desktop-helper.json"), "utf8"));
  const gatewayHelper = JSON.parse(await fs.readFile(path.join(lab, "gateway-container-helper.json"), "utf8"));
  assert.equal(desktopHelper.grant_renewal.url, metadata.issuer_url); assert.equal(gatewayHelper.grant_renewal.url, "http://iscp-local-issuer:8080");
  assert.equal(gatewayHelper.grant_renewal.pending_file, "/lab/gateway/pending-grant.json");
  assert.notEqual(desktopHelper.grant_renewal.pending_file, gatewayHelper.grant_renewal.pending_file);
  const initialGrant = await fs.readFile(path.join(lab, "grant.json"));
  for (const reconnect of [false, true]) {
    await smokeLab(lab, reconnect);
    const evidence = JSON.parse(await fs.readFile(path.join(lab, "evidence/smoke.json"), "utf8"));
    assert.equal(evidence.event, "iscp_desktop_smoke_complete");
    assert.equal(evidence.state, "delivered"); assert.equal(evidence.submit_count, 1); assert.equal(evidence.direct_gateway_http_calls, 0);
    assert.equal(evidence.successful_mock_answer, true);
    assert.equal(evidence.helper_generations, reconnect ? 2 : 1); assert.ok(evidence.ack_count >= 1);
    const control = JSON.parse(await fs.readFile(path.join(lab, "state/gateway-state.json.execution/control.json"), "utf8"));
    const admitted = Object.values(control.fences).filter(record => record.request_id === evidence.request_id);
    assert.equal(admitted.length, 1); assert.equal(admitted[0].state, "delivered");
    assert.equal(admitted[0].input_digest, evidence.input_digest); assert.equal(admitted[0].result_digest, evidence.result_digest);
  }
  assert.deepEqual(await fs.readFile(path.join(lab, "grant.json")), initialGrant, "up must not mint a new Grant or reset fixed-pair renewal authorization");
  assert.equal(containerState(metadata.issuer_container, metadata.lab_id).State.StartedAt, metadata.issuer_started_at);
  const gatewayContainer = containerState(metadata.gateway_container, metadata.lab_id);
  assert.ok(Object.values(gatewayContainer.NetworkSettings.Ports || {}).every(binding => !binding || binding.length === 0));
  assert.equal(Object.keys(gatewayContainer.HostConfig.PortBindings || {}).length, 0);
  assert.deepEqual(Object.keys(gatewayContainer.NetworkSettings.Networks), [metadata.network]);
  assert.equal((await fs.stat(path.join(lab, "desktop/enrollment.json"))).mode & 0o777, 0o600);
  assert.equal((await fs.stat(path.join(lab, "gateway/enrollment.json"))).mode & 0o777, 0o600);
  await assert.rejects(() => prepareLab(inputFile, lab));
  // A newly generated reference signer must not silently replace the old pin.
  const originalEnrollment = await fs.readFile(path.join(lab, "desktop/enrollment.json"));
  command("docker", ["restart", metadata.relay_container]);
  // Docker can allocate a new random published port when restarting.
  const rebound = containerState(metadata.relay_container, metadata.lab_id).NetworkSettings.Ports["8080/tcp"][0];
  const restartedURL = `http://127.0.0.1:${rebound.HostPort}`;
  await waitRelay(restartedURL);
  assert.throws(() => command(path.join(lab, "bin/iscp-local-enroll"), ["-relay-url", restartedURL, "-relay-id", metadata.relay_id,
    "-domain-id", metadata.domain_id, "-device-id", metadata.desktop_device_id, "-identity-dir", path.join(lab, "desktop"), "-enrollment-file", path.join(lab, "desktop/enrollment.json")]),
  error => error.stderr.includes("local Relay signer changed"));
  assert.deepEqual(await fs.readFile(path.join(lab, "desktop/enrollment.json")), originalEnrollment);
  completed = true;
});
