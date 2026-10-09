import crypto from "node:crypto";
import fs from "node:fs/promises";
import path from "node:path";
import { spawnSync } from "node:child_process";
import { prepareCapacityRelay } from "./iscp-relay-capacity.mjs";

export const labLabel = "io.sparkclaw.iscp.lab";
export const relayContainerURL = "http://iscp-relay:8080";
export const relayContainerWS = "ws://iscp-relay:8080/v2/relay/connect";
export const issuerContainerURL = "http://iscp-local-issuer:8080";

export function command(executable, args, options = {}) {
  const result = spawnSync(executable, args, { stdio: "pipe", timeout: 120000, maxBuffer: 4 << 20, ...options });
  if (result.error || result.status !== 0) {
    const error = new Error(`${path.basename(executable)} ${args[0] || ""} failed; check local toolchain/Docker prerequisites`);
    error.stdout = result.stdout?.toString() || "";
    error.stderr = result.stderr?.toString() || "";
    throw error;
  }
  return result.stdout?.toString().trim();
}

export function dockerArch() {
  const arch = command("docker", ["info", "--format", "{{.Architecture}}"]);
  const result = { aarch64: "arm64", arm64: "arm64", x86_64: "amd64", amd64: "amd64" }[arch];
  if (!result) throw new Error("Unsupported Docker Linux architecture");
  return result;
}

export async function buildRelay(root, directory, arch, { capacity = false } = {}) {
  const module = JSON.parse(command("go", ["list", "-m", "-json", "github.com/Infinimesh-ai/ISCP"], { cwd: root }));
  if (!module.Dir || !module.Version || !module.Sum || module.Replace) throw new Error("The local Relay requires the checksum-locked, unreplaced ISCP Go module");
  command("go", ["mod", "verify"], { cwd: path.join(root, "services/gateway"), env: { ...process.env, GOWORK: "off" } });
  const context = path.join(directory, "relay-build");
  await fs.mkdir(context, { mode: 0o700 });
  const binary = path.join(context, "relayd");
  const sourceDirectory = capacity ? path.join(directory, "relay-source") : module.Dir;
  const capacityPatch = capacity ? await prepareCapacityRelay(module.Dir, sourceDirectory) : undefined;
  command("go", ["build", "-trimpath", "-ldflags=-s -w", "-o", binary, "./services/relay-reference/cmd/relayd"], {
    cwd: sourceDirectory, env: { ...process.env, GOWORK: "off", CGO_ENABLED: "0", GOOS: "linux", GOARCH: arch },
  });
  const hash = crypto.createHash("sha256").update(await fs.readFile(binary)).digest("hex");
  const image = `sparkclaw-iscp-relay:${module.Version.replace(/^v/u, "")}-${hash.slice(0, 12)}`;
  command("docker", ["build", "--network=none", "--pull=false", "--file", path.join(root, "docker/images/iscp-relay-local.Dockerfile"), "--tag", image, context]);
  const imageID = command("docker", ["image", "inspect", image, "--format", "{{.Id}}"]);
  return { module: module.Path, version: module.Version, module_sum: module.Sum, ...(capacityPatch ? { capacity_patch: capacityPatch } : {}), relay_binary_sha256: hash, image, image_id: imageID, goarch: arch };
}

export function containerState(name, labID) {
  const result = spawnSync("docker", ["container", "inspect", name], { timeout: 15000, stdio: "pipe" });
  if (result.status !== 0) return null;
  const container = JSON.parse(result.stdout.toString())[0];
  if (container.Config.Labels?.[labLabel] !== labID) throw new Error("Refusing to operate on a Docker container belonging to another lab");
  return container;
}

export async function waitRelay(url, timeout = 15000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    try {
      const response = await fetch(`${url}/readyz`, { signal: AbortSignal.timeout(1000), redirect: "error" });
      if (response.ok && (await response.json()).status === "ok") return;
    } catch { /* bounded startup retry */ }
    await new Promise(resolve => setTimeout(resolve, 250));
  }
  throw new Error("Local reference Relay readiness timed out");
}

export async function startRelay(metadata) {
  command("docker", ["network", "create", "--internal", "--label", `${labLabel}=${metadata.lab_id}`, metadata.network]);
  // Docker 29 does not publish ports for an internal-only network. Relay also
  // joins a dedicated ingress bridge; Gateway joins only the internal network.
  command("docker", ["network", "create", "--label", `${labLabel}=${metadata.lab_id}`, metadata.ingress_network]);
  command("docker", ["create", "--name", metadata.relay_container, "--label", `${labLabel}=${metadata.lab_id}`,
    "--network", metadata.ingress_network, "--publish", "127.0.0.1::8080",
    "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--log-opt", "max-size=10m", "--log-opt", "max-file=2",
    "--env", "ISCP_PROFILE=local-lab", "--env", `ISCP_DOMAIN_ID=${metadata.domain_id}`, "--env", `ISCP_RELAY_ID=${metadata.relay_id}`,
    "--env", `ISCP_RELAY_BASE_URL=${relayContainerURL}`, "--env", `ISCP_RELAY_WS_URL=${relayContainerWS}`,
    ...(metadata.source.capacity_patch ? ["--env", "SPARKCLAW_ISCP_CAPACITY=1"] : []), metadata.source.image]);
  command("docker", ["network", "connect", "--alias", "iscp-relay", metadata.network, metadata.relay_container]);
  command("docker", ["start", metadata.relay_container]);
  const container = containerState(metadata.relay_container, metadata.lab_id);
  const binding = container.NetworkSettings.Ports["8080/tcp"]?.[0];
  if (binding?.HostIp !== "127.0.0.1" || !/^\d+$/u.test(binding.HostPort || "")) throw new Error("Relay must publish only a loopback port");
  metadata.relay_url = `http://127.0.0.1:${binding.HostPort}`;
  metadata.relay_started_at = container.State.StartedAt;
  await waitRelay(metadata.relay_url);
}

export function issuerContainerArguments(metadata, directory, uid = process.getuid()) {
  if (!path.isAbsolute(directory) || !metadata.issuer_container || !metadata.source?.image_id) throw new Error("Issuer requires a private prepared lab and pinned runtime image");
  return ["create", "--name", metadata.issuer_container, "--label", `${labLabel}=${metadata.lab_id}`,
    "--network", metadata.ingress_network, "--publish", "127.0.0.1::8080", "--user", String(uid),
    "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--log-opt", "max-size=10m", "--log-opt", "max-file=2",
    "--mount", `type=bind,source=${directory},target=/lab`, "--entrypoint", "/lab/bin/issuer-linux", metadata.source.image_id,
    "-config", "/lab/issuer/issuer-container.json", "-listen", "0.0.0.0:8080", "-allow-container-listen"];
}

export async function startIssuer(metadata, directory) {
  command("docker", issuerContainerArguments(metadata, directory));
  command("docker", ["network", "connect", "--alias", "iscp-local-issuer", metadata.network, metadata.issuer_container]);
  command("docker", ["start", metadata.issuer_container]);
  const container = containerState(metadata.issuer_container, metadata.lab_id);
  const binding = container?.NetworkSettings.Ports["8080/tcp"]?.[0];
  if (!container?.State.Running || binding?.HostIp !== "127.0.0.1" || !/^\d+$/u.test(binding.HostPort || "")) throw new Error("Issuer must run with a loopback-only published port");
  metadata.issuer_url = `http://127.0.0.1:${binding.HostPort}`;
  metadata.issuer_started_at = container.State.StartedAt;
  metadata.issuer_runtime_image_id = container.Image;
}

export async function stopLab(metadata, directory) {
  // Names alone never authorize deletion: check this lab's ownership label.
  for (const name of [metadata.gateway_container, metadata.issuer_container, metadata.relay_container]) {
    if (!name) continue;
    const container = containerState(name, metadata.lab_id);
    if (!container) continue;
    const result = spawnSync("docker", ["logs", name], { timeout: 15000, stdio: "pipe", maxBuffer: 16 << 20 });
    const log = name === metadata.relay_container ? "relay.log" : name === metadata.issuer_container ? "issuer.log" : "gateway.log";
    if (directory) await fs.writeFile(path.join(directory, "evidence", log), Buffer.concat([result.stdout || Buffer.alloc(0), result.stderr || Buffer.alloc(0)]), { mode: 0o600 });
    command("docker", ["rm", "--force", name]);
  }
  for (const name of [metadata.network, metadata.ingress_network]) {
    if (!name) continue;
    const result = spawnSync("docker", ["network", "inspect", name], { timeout: 15000, stdio: "pipe" });
    if (result.status === 0) {
      const network = JSON.parse(result.stdout.toString())[0];
      if (network.Labels?.[labLabel] !== metadata.lab_id) throw new Error("Refusing to remove a Docker network belonging to another lab");
      command("docker", ["network", "rm", name]);
    }
  }
}
