#!/usr/bin/env node
import crypto from "node:crypto";
import fs from "node:fs/promises";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { spawn } from "node:child_process";
import { assertNoSymlinkPath, assertPrivateDirectory, readPrivateJSON, writePrivateJSON } from "./lib/private-workbench.mjs";
import { buildRelay, command, containerState, dockerArch, labLabel, relayContainerURL, relayContainerWS, startRelay, stopLab, waitRelay } from "./lib/iscp-docker-lab.mjs";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const gatewayRoot = path.join(root, "services/gateway");

export function validateLabInput(input) {
  if (!input || Object.keys(input).sort().join(",") !== "client_id,deployment_id,owner_id,relay_profile,schema_version" ||
      input.schema_version !== 1 || input.relay_profile !== "local-lab" || input.owner_id !== "owner" ||
      !/^[A-Za-z0-9][A-Za-z0-9_.:-]{0,159}$/u.test(input.deployment_id || "") ||
      !/^[A-Za-z0-9][A-Za-z0-9_.:-]{0,159}$/u.test(input.client_id || "")) {
    throw new Error("Lab input requires schema 1, explicit local-lab Relay, deployment/client IDs and test Owner 'owner'");
  }
  return input;
}

function localRelayURL(value, websocket = false) {
  try {
    const url = new URL(value);
    return url.protocol === (websocket ? "ws:" : "http:") && ["127.0.0.1", "[::1]", "localhost", "iscp-relay"].includes(url.hostname) &&
      !url.username && !url.password && !url.search && !url.hash && url.pathname === (websocket ? "/v2/relay/connect" : "/");
  } catch { return false; }
}

export function verifyEnrolledPair(desktop, gateway, now = Date.now()) {
  for (const { identity, enrollment } of [desktop, gateway]) {
    if (identity?.type !== "iscp.device.identity.v2" || identity.public_key?.kty !== "Ed25519" ||
        enrollment?.type !== "sparkclaw.bridge.enrollment.v1" || enrollment.mode !== "workbench-local-lab" ||
        enrollment.device_id !== identity.device_id || enrollment.domain_id !== identity.domain_id || !enrollment.relay_id ||
        !localRelayURL(enrollment.relay_base_url) || !localRelayURL(enrollment.relay_websocket_url, true) ||
        !(Date.parse(enrollment.expires_at) > now) || enrollment.relay_signer_identity?.domain_id !== identity.domain_id ||
        !enrollment.relay_signer_identity?.public_key?.kid) throw new Error("Local Relay device binding/signer pin is invalid or expired");
    for (const credential of [enrollment.access, enrollment.refresh]) {
      if (credential?.device_id !== identity.device_id || credential.domain_id !== identity.domain_id ||
          typeof credential.token !== "string" || !credential.token || !Number.isFinite(Date.parse(credential.expires_at))) throw new Error("Local Relay device credentials are missing or invalid");
    }
    if (!(Date.parse(enrollment.refresh.expires_at) > now)) throw new Error("Local Relay refresh credential has expired");
  }
  if (desktop.identity.domain_id !== gateway.identity.domain_id || desktop.identity.device_id === gateway.identity.device_id ||
      desktop.enrollment.relay_id !== gateway.enrollment.relay_id ||
      JSON.stringify(desktop.enrollment.relay_signer_identity) !== JSON.stringify(gateway.enrollment.relay_signer_identity)) {
    throw new Error("Both devices must share one local Domain/Relay and pinned descriptor signer");
  }
}

async function enrolledPeer(name, directory) {
  const identityDirectory = path.join(directory, name);
  await assertPrivateDirectory(identityDirectory);
  const identity = await readPrivateJSON(path.join(identityDirectory, "device.identity.json"));
  const enrollment = await readPrivateJSON(path.join(identityDirectory, "enrollment.json"));
  const keyPath = path.join(identityDirectory, "device.identity.key");
  await assertNoSymlinkPath(keyPath);
  const info = await fs.lstat(keyPath);
  if (!info.isFile() || info.uid !== process.getuid() || (info.mode & 0o777) !== 0o600 || info.size > 1024) throw new Error("Local runner requires private file-backed device keys");
  return { identity, enrollment };
}

async function write(filename, value) { await writePrivateJSON(filename, value, { replace: false }); }

export async function prepareLab(inputFile, directory) {
  if (!path.isAbsolute(directory) || directory === root || directory.startsWith(root + path.sep)) throw new Error("Use a new absolute private test directory outside the repository");
  const input = validateLabInput(await readPrivateJSON(inputFile));
  await assertNoSymlinkPath(directory);
  await fs.mkdir(directory, { mode: 0o700 });
  await assertPrivateDirectory(directory);
  for (const name of ["desktop", "gateway", "bin", "userdata", "smoke-userdata", "state", "evidence"]) await fs.mkdir(path.join(directory, name), { mode: 0o700 });
  const labID = crypto.randomUUID();
  const metadata = { schema_version: 1, relay_profile: "local-lab", lab_id: labID,
    deployment_id: input.deployment_id, owner_id: input.owner_id, client_id: input.client_id,
    domain_id: `local-${labID}`, relay_id: `relay-${labID}`, desktop_device_id: `sparkx-${labID}`, gateway_device_id: `sparkclaw-${labID}`,
    network: `sparkclaw-iscp-${labID}`, ingress_network: `sparkclaw-iscp-ingress-${labID}`,
    relay_container: `sparkclaw-iscp-relay-${labID}`, gateway_container: `sparkclaw-iscp-gateway-${labID}`, created_at: new Date().toISOString() };
  try {
    process.stdout.write("Building the locked upstream ISCP reference Relay source for Docker...\n");
    metadata.source = await buildRelay(root, directory, dockerArch());
    await startRelay(metadata);
    const enrollBinary = path.join(directory, "bin/iscp-local-enroll");
    command("go", ["build", "-trimpath", "-o", enrollBinary, "./cmd/iscp-local-enroll"], { cwd: gatewayRoot });
    for (const name of ["desktop", "gateway"]) {
      const args = ["-relay-url", metadata.relay_url, "-relay-id", metadata.relay_id, "-domain-id", metadata.domain_id,
        "-device-id", metadata[`${name}_device_id`], "-identity-dir", path.join(directory, name), "-enrollment-file", path.join(directory, name, "enrollment.json")];
      if (name === "gateway") args.push("-runtime-relay-url", relayContainerURL, "-runtime-websocket-url", relayContainerWS);
      command(enrollBinary, args);
    }
    const desktop = await enrolledPeer("desktop", directory);
    const gateway = await enrolledPeer("gateway", directory);
    verifyEnrolledPair(desktop, gateway);
    const issuerBinary = path.join(directory, "bin/iscp-local-issuer");
    command("go", ["build", "-trimpath", "-o", issuerBinary, "./cmd/iscp-local-issuer"], { cwd: gatewayRoot });
    command(issuerBinary, ["-init", "-directory", path.join(directory, "issuer"), "-subject-identity", path.join(directory, "desktop/device.identity.json"), "-audience-identity", path.join(directory, "gateway/device.identity.json"), "-relay-id", metadata.relay_id]);
    await issueGrant(directory);
    await writeProfiles(directory, metadata, desktop.identity, gateway.identity);
    await write(path.join(directory, "run.json"), metadata);
    process.stdout.write(`Local reference Relay ready at ${metadata.relay_url}; both devices registered with signed PoP.\n`);
    return metadata;
  } catch (error) {
    await stopLab(metadata, directory);
    throw error;
  }
}

async function writeProfiles(directory, metadata, desktopIdentity, gatewayIdentity) {
  const binding = { deployment_id: metadata.deployment_id, owner_id: metadata.owner_id, client_id: metadata.client_id };
  for (const [role, name, other] of [["initiator", "desktop", "gateway"], ["responder", "gateway", "desktop"]]) {
    const profile = { schema_version: 1, mode: "local-test", relay_profile: "local-lab", role,
      identity_directory: path.join(directory, name), identity_key_backend: "file",
      enrollment_file: path.join(directory, name, "enrollment.json"), peer_identity_file: path.join(directory, other, "device.identity.json"),
      issuer_identity_file: path.join(directory, "issuer/issuer.identity.json"), grant_file: path.join(directory, "grant.json"), permission: "sparkclaw.workbench.v1", binding };
    await write(path.join(directory, `${name}-helper.json`), profile);
    if (name === "gateway") {
      const containerProfile = Object.fromEntries(Object.entries(profile).map(([key, value]) => [key, typeof value === "string" && value.startsWith(directory + path.sep) ? "/lab" + value.slice(directory.length) : value]));
      await write(path.join(directory, "gateway-container-helper.json"), containerProfile);
    }
  }
  await write(path.join(directory, "desktop-profile.json"), {
    schema_version: 1, transport: "iscp", test_mode: true, helper_config: path.join(directory, "desktop-helper.json"),
    backend: { schema_version: 3, transport: "iscp", origin: "https://iscp.invalid", ...binding, relay_profile: "local-lab", domain_id: desktopIdentity.domain_id,
      initiator_device_id: desktopIdentity.device_id, responder_device_id: gatewayIdentity.device_id,
      responder_key_thumbprint: gatewayIdentity.public_key.kid, relay_url: metadata.relay_url, test_mode: true },
  });
  await write(path.join(directory, "backend-client.json"), { schema_version: 1, ...binding, actor_id: metadata.owner_id,
    client_name: "SparkX ISCP Local Test", token: crypto.randomBytes(32).toString("base64url") });
  const config = JSON.parse(await fs.readFile(path.join(root, "configs/sparkclaw.default.json"), "utf8"));
  config.gateway.bind = "127.0.0.1";
  config.tools.browserAutomation.enabled = false;
  config.tools.web.search.enabled = false;
  config.model.capacity_catalog = "/lab/model.profiles.json";
  await write(path.join(directory, "model.profiles.json"), JSON.parse(await fs.readFile(path.join(root, "configs/model.profiles.json"), "utf8")));
  config.workspaces = { default_root: "/lab/state/workspaces", allowlist: ["/lab/state/workspaces"] };
  config.state.path = "/lab/state/gateway-state.json";
  config.state.credential_key_file = "/lab/state/gateway-credentials.key";
  config.storage = { ...config.storage, trace_dir: "/lab/state/traces", log_dir: "/lab/state/logs", artifact_dir: "/lab/state/artifacts" };
  await write(path.join(directory, "gateway-config.json"), config);
}

async function issueGrant(directory) {
  const child = spawn(path.join(directory, "bin/iscp-local-issuer"), ["-config", path.join(directory, "issuer/issuer.json")], { stdio: ["ignore", "pipe", "ignore"] });
  const exited = new Promise(resolve => { child.once("exit", resolve); child.once("error", resolve); });
  try {
    const issuerURL = await new Promise((resolve, reject) => {
      let data = "";
      const timer = setTimeout(() => reject(new Error("Local issuer startup timed out")), 5000);
      child.once("error", () => { clearTimeout(timer); reject(new Error("Local issuer failed")); });
      child.once("exit", () => { clearTimeout(timer); reject(new Error("Local issuer exited")); });
      child.stdout.on("data", chunk => {
        data += chunk.toString();
        if (data.length > 4096) { clearTimeout(timer); reject(new Error("Invalid issuer startup frame")); }
        else if (data.includes("\n")) {
          clearTimeout(timer);
          try { const url = new URL(JSON.parse(data.split("\n")[0]).issuer_url); if (url.protocol !== "http:" || url.hostname !== "127.0.0.1") throw new Error(); resolve(url.origin); }
          catch { reject(new Error("Invalid issuer startup address")); }
        }
      });
    });
    const token = (await fs.readFile(path.join(directory, "issuer/management.token"), "utf8")).trim();
    const response = await fetch(`${issuerURL}/v1/grants`, { method: "POST", headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" }, body: "{}", signal: AbortSignal.timeout(5000), redirect: "error" });
    if (!response.ok) throw new Error("Local grant issuance failed");
    await writePrivateJSON(path.join(directory, "grant.json"), await response.json());
  } finally {
    child.kill("SIGTERM");
    const timer = setTimeout(() => child.kill("SIGKILL"), 4000);
    await exited; clearTimeout(timer);
  }
}

export async function verifyLab(directory) {
  await assertPrivateDirectory(directory);
  verifyEnrolledPair(await enrolledPeer("desktop", directory), await enrolledPeer("gateway", directory));
  const helper = path.join(root, "apps/desktop/bin/iscp-workbench");
  for (const peer of ["desktop", "gateway"]) command(helper, ["-check", "-config", path.join(directory, `${peer}-helper.json`)]);
  process.stdout.write("Both private local-lab profiles and signed session grants verified.\n");
}

export async function upLab(directory) {
  await assertPrivateDirectory(directory);
  await assertDesktopStopped(directory);
  const metadata = await readPrivateJSON(path.join(directory, "run.json"));
  const relay = containerState(metadata.relay_container, metadata.lab_id);
  if (!relay?.State.Running || (metadata.relay_started_at && relay.State.StartedAt !== metadata.relay_started_at)) throw new Error("Reference Relay stopped or restarted; prepare a fresh lab to pin its new signer and enroll devices");
  await waitRelay(metadata.relay_url);
  await issueGrant(directory);
  await verifyLab(directory);
  command("go", ["build", "-trimpath", "-o", path.join(directory, "bin/gateway-linux"), "./cmd/sparkclaw"], { cwd: gatewayRoot, env: { ...process.env, GOOS: "linux", GOARCH: metadata.source.goarch, CGO_ENABLED: "0" } });
  metadata.gateway_binary_sha256 = crypto.createHash("sha256").update(await fs.readFile(path.join(directory, "bin/gateway-linux"))).digest("hex");
  metadata.helper_binary_sha256 = crypto.createHash("sha256").update(await fs.readFile(path.join(root, "apps/desktop/bin/iscp-workbench"))).digest("hex");
  metadata.gateway_runtime_image_id = command("docker", ["image", "inspect", "sparkclaw-gateway:latest", "--format", "{{.Id}}"]);
  await writePrivateJSON(path.join(directory, "run.json"), metadata);
  if (containerState(metadata.gateway_container, metadata.lab_id)) command("docker", ["rm", "--force", metadata.gateway_container]);
  command("docker", ["run", "--detach", "--name", metadata.gateway_container, "--label", `${labLabel}=${metadata.lab_id}`, "--network", metadata.network,
    "--user", String(process.getuid()), "--mount", `type=bind,source=${directory},target=/lab`, "--entrypoint", "/lab/bin/gateway-linux",
    "--env", `SPARKCLAW_DEPLOYMENT_ID=${metadata.deployment_id}`, "--env", "SPARKCLAW_DESKTOP_CLIENT_FILE=/lab/backend-client.json",
    "--env", "SPARKCLAW_WORKBENCH_ISCP_LOCAL_TEST=1", "--env", "SPARKCLAW_WORKBENCH_ISCP_CONFIG=/lab/gateway-container-helper.json",
    "--env", "SPARKCLAW_MODEL_CAPACITY_CATALOG=/lab/model.profiles.json", "--env", "SPARKCLAW_BROWSER_CHROMIUM_EXECUTABLE=", "--env", "SPARKCLAW_BROWSER_PROFILE_DIR=",
    "--log-opt", "max-size=10m", "--log-opt", "max-file=2", "sparkclaw-gateway:latest", "-config", "/lab/gateway-config.json"]);
  // No Gateway HTTP ports are published: all business calls use encrypted ISCP.
  await new Promise(resolve => setTimeout(resolve, 500));
  if (!containerState(metadata.gateway_container, metadata.lab_id)?.State.Running) throw new Error("Local Gateway startup failed; inspect its Docker log");
  process.stdout.write(`Local Gateway started on isolated Docker network ${metadata.network}.\n`);
  return metadata;
}

export async function runLab(directory) {
  await upLab(directory);
  const leaseFile = path.join(directory, "desktop-launcher.json");
  await write(leaseFile, { schema_version: 1, launcher_pid: process.pid });
  const desktop = spawn(path.join(root, "node_modules/.bin/electron"), ["apps/desktop/src/main/main.mjs"], { cwd: root, stdio: "ignore", env: { ...process.env,
    SPARKCLAW_DESKTOP_ISCP_LOCAL_TEST: "1", SPARKCLAW_DESKTOP_ISCP_CONFIG: path.join(directory, "desktop-profile.json"), SPARKCLAW_DESKTOP_USER_DATA_DIR: path.join(directory, "userdata") } });
  const exited = new Promise(resolve => { desktop.once("exit", resolve); desktop.once("error", resolve); });
  try {
    process.stdout.write("SparkX opened with its private ISCP test profile. Quit (Cmd-Q) or Ctrl-C to close; use down to remove this lab's Docker services.\n");
    await new Promise((resolve, reject) => {
      desktop.once("error", () => reject(new Error("SparkX launch failed")));
      desktop.once("exit", code => code === 0 ? resolve() : reject(new Error("SparkX exited unsuccessfully")));
      process.once("SIGINT", resolve); process.once("SIGTERM", resolve);
    });
  } finally {
    desktop.kill("SIGTERM");
    const timer = setTimeout(() => desktop.kill("SIGKILL"), 4000);
    await exited; clearTimeout(timer);
    await fs.rm(leaseFile);
  }
}

async function assertDesktopStopped(directory) {
  const filename = path.join(directory, "desktop-launcher.json");
  const lease = await readPrivateJSON(filename, { optional: true });
  if (!lease) return;
  if (lease.schema_version !== 1 || !Number.isSafeInteger(lease.launcher_pid) || lease.launcher_pid < 1) throw new Error("Invalid desktop launcher lease");
  try { process.kill(lease.launcher_pid, 0); }
  catch (error) {
    if (error.code === "ESRCH") { await fs.rm(filename); return; }
    throw error;
  }
  throw new Error("Quit this lab's SparkX before up/smoke/down; its normal ClientStore is used exclusively");
}

export async function smokeLab(directory, reconnect = false) {
  await upLab(directory);
  const args = [path.join(root, "scripts/iscp-desktop-smoke.mjs"), "--profile", path.join(directory, "desktop-profile.json"), "--user-data", path.join(directory, "smoke-userdata"),
    "--client-store-directory", path.join(directory, "userdata/workbench")];
  if (reconnect) args.push("--reconnect");
  let output;
  try { output = command(process.execPath, args, { cwd: root }); }
  catch (error) {
    await fs.writeFile(path.join(directory, "evidence/smoke-failed.ndjson"), `${error.stdout || ""}${error.stderr || ""}`, { mode: 0o600 });
    process.stdout.write(error.stdout || "");
    // This entry point emits a deliberately redacted, structured failure frame.
    const failure = (error.stderr || "").trim().split("\n").findLast(line => line.startsWith('{"event":"iscp_desktop_smoke_failed"'));
    if (failure) throw new Error(`Desktop smoke failed: ${JSON.parse(failure).error}`);
    throw error;
  }
  const events = output.split("\n").filter(Boolean).map(line => JSON.parse(line));
  const receipt = events.findLast(event => event.event === "iscp_desktop_smoke_complete");
  if (!receipt) throw new Error("Desktop smoke returned no completion receipt");
  await fs.writeFile(path.join(directory, "evidence/smoke.ndjson"), `${output}\n`, { mode: 0o600 });
  await writePrivateJSON(path.join(directory, "evidence/smoke.json"), receipt);
  await writePrivateJSON(path.join(directory, `evidence/smoke-${reconnect ? "reconnect" : "normal"}.json`), receipt);
  process.stdout.write(`${output}\n`);
}

async function main() {
  if (!/^v26\./u.test(process.version)) throw new Error("Use the repository's locked Node.js 26 runtime");
  const [action, ...args] = process.argv.slice(2);
  const options = {};
  for (let index = 0; index < args.length; index++) {
    if (args[index] === "--reconnect") { options.reconnect = true; continue; }
    if (!["--input", "--directory"].includes(args[index]) || !args[index + 1]) throw new Error("Use prepare --input <private JSON> --directory <new absolute directory>, or verify/up/smoke/run/down --directory <prepared directory>");
    options[args[index].slice(2)] = args[++index];
  }
  if (action === "prepare" && options.input && options.directory) await prepareLab(options.input, options.directory);
  else if (action === "verify" && options.directory) await verifyLab(options.directory);
  else if (action === "up" && options.directory) await upLab(options.directory);
  else if (action === "smoke" && options.directory) await smokeLab(options.directory, options.reconnect);
  else if (action === "run" && options.directory) await runLab(options.directory);
  else if (action === "down" && options.directory) {
    await assertPrivateDirectory(options.directory);
    await assertDesktopStopped(options.directory);
    await stopLab(await readPrivateJSON(path.join(options.directory, "run.json")), options.directory);
    process.stdout.write("Removed this lab's Docker services; private profiles/state/evidence retained. Prepare a new lab before starting a new reference Relay.\n");
  } else throw new Error("Choose prepare, verify, up, smoke, run or down with the required paths");
}

if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) {
  main().catch(error => { process.stderr.write(`${error.message}\n`); process.exitCode = 1; });
}
