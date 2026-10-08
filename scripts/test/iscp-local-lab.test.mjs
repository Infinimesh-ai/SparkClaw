import assert from "node:assert/strict";
import test from "node:test";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { validateLabInput, verifyEnrolledPair, prepareLab, verifyLab, smokeLab } from "../iscp-local-lab.mjs";
import { command, containerState, labLabel, stopLab, waitRelay } from "../lib/iscp-docker-lab.mjs";

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
  for (const reconnect of [false, true]) {
    await smokeLab(lab, reconnect);
    const evidence = JSON.parse(await fs.readFile(path.join(lab, "evidence/smoke.json"), "utf8"));
    assert.equal(evidence.event, "iscp_desktop_smoke_complete");
    assert.equal(evidence.state, "delivered"); assert.equal(evidence.submit_count, 1); assert.equal(evidence.direct_gateway_http_calls, 0);
    assert.equal(evidence.helper_generations, reconnect ? 2 : 1); assert.ok(evidence.ack_count >= 1);
    const control = JSON.parse(await fs.readFile(path.join(lab, "state/gateway-state.json.execution/control.json"), "utf8"));
    const admitted = Object.values(control.fences).filter(record => record.request_id === evidence.request_id);
    assert.equal(admitted.length, 1); assert.equal(admitted[0].state, "delivered");
    assert.equal(admitted[0].input_digest, evidence.input_digest); assert.equal(admitted[0].result_digest, evidence.result_digest);
  }
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
