import crypto from "node:crypto";
import fs from "node:fs/promises";
import path from "node:path";
import net from "node:net";
import { spawn, execFile } from "node:child_process";
import { promisify } from "node:util";
import { fileURLToPath } from "node:url";

const repository = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../../..");
const samples = Number(process.argv[2] || "1");
const mode = (process.argv[3] || "--controlled").slice(2);
const repetitions = ["long-text", "auto-follow", "joint-ui"].includes(mode) ? 10 : 1;
if (process.platform !== "darwin" || !/^v26\./u.test(process.version) || !Number.isSafeInteger(samples) || samples < 1 || samples > 20 || process.argv.length > 4 || !["controlled", "long-text", "auto-follow", "joint-ui", "recovery", "real-model"].includes(mode) || (["recovery", "real-model"].includes(mode) && samples !== 1)) throw new Error("Use Node 26 on macOS: run-stream-native-qualification.mjs [1..20] [--long-text|--auto-follow|--joint-ui|--recovery|--real-model]");
if (mode === "real-model" && !path.isAbsolute(process.env.SPARKCLAW_STREAM_FIXTURE_MODEL_CONFIG || "")) throw new Error("Explicit private SPARKCLAW_STREAM_FIXTURE_MODEL_CONFIG required");
if (mode === "real-model" && !process.env.SPARKCLAW_STREAM_FIXTURE_LINUX_IMAGE) throw new Error("Explicit SPARKCLAW_STREAM_FIXTURE_LINUX_IMAGE with the repository Linux test dependencies required");
const exec = promisify(execFile);
const run = (command, args, options = {}) => exec(command, args, { cwd: repository, timeout: 180000, maxBuffer: 2 << 20, ...options });
const root = await fs.mkdtemp("/private/tmp/sparkclaw-stream-v3-");
await fs.chmod(root, 0o700);
await fs.mkdir(path.join(root, "bin"), { mode: 0o700 });
await fs.mkdir(path.join(root, "profile"), { mode: 0o700 });
const id = crypto.randomUUID();
const container = `sparkclaw-stream-v3-${id}`;
const gatewayContainer = `sparkclaw-stream-gateway-${id}`;
const linuxFixture = mode === "real-model";
const password = crypto.randomBytes(24).toString("hex");
const token = crypto.randomBytes(24).toString("hex");
let fixture;
const evidence = { schema_version: 1, root, samples, mode, ...(mode !== "real-model" ? {chars_per_sample: 600 * repetitions} : {}), production_data: false, postgres_container: container, source_revision: (await run("git", ["rev-parse", "HEAD"])).stdout.trim() };
if (process.env.SPARKCLAW_STREAM_FIXTURE_RELAY_BINARY) {
  evidence.independent_relay = {};
  for (const [kind, key] of [['relay', 'SPARKCLAW_STREAM_FIXTURE_RELAY_BINARY'], ['maintenance', 'SPARKCLAW_STREAM_FIXTURE_MAINTENANCE_BINARY']]) {
    const binary = process.env[key];
    if (!path.isAbsolute(binary || '')) throw new Error('Absolute independent Relay fixture binaries required');
    evidence.independent_relay[kind] = { path: binary, sha256: crypto.createHash('sha256').update(await fs.readFile(binary)).digest('hex') };
  }
}
console.log(JSON.stringify({ event: "stream_native_lab", root, samples }));
try {
  await run("docker", ["run", "--detach", "--name", container, "--label", `io.sparkclaw.stream.fixture=${id}`, "--publish", "127.0.0.1::5432", "--tmpfs", "/var/lib/postgresql:rw", "--env", "POSTGRES_PASSWORD", "--env", "POSTGRES_DB=stream_fixture", "postgres:18"], {env: {...process.env, POSTGRES_PASSWORD: password}});
  const port = (await run("docker", ["inspect", container, "--format", '{{(index (index .NetworkSettings.Ports "5432/tcp") 0).HostPort}}'])).stdout.trim();
  for (let attempt = 0; ; attempt++) {
    try { await run("docker", ["exec", container, "pg_isready", "-U", "postgres", "-d", "stream_fixture"], { timeout: 5000 }); break; }
    catch { if (attempt >= 40) throw new Error("Isolated PostgreSQL startup timed out"); await new Promise(resolve => setTimeout(resolve, 250)); }
  }
  await run("go", ["test", "-c", "-o", path.join(root, "bin/gateway-fixture"), "./internal/gateway"], { cwd: path.join(repository, "services/gateway"), ...(linuxFixture ? {env: {...process.env, GOOS: "linux", GOARCH: process.arch === "arm64" ? "arm64" : "amd64", CGO_ENABLED: "0"}} : {}) });
  await fs.mkdir(path.join(repository, "apps/desktop/bin"), { recursive: true });
  await run("go", ["build", "-o", path.join(repository, "apps/desktop/bin/iscp-workbench"), "./cmd/iscp-workbench"], { cwd: path.join(repository, "services/gateway") });
  await run("npm", ["run", "build:webchat"]);
  const fingerprint = async file => ({ path: path.relative(repository, file), sha256: crypto.createHash("sha256").update(await fs.readFile(file)).digest("hex") });
  const assets = await fs.readdir(path.join(repository, "apps/webchat/dist/assets"));
  evidence.build_fingerprints = await Promise.all([
    path.join(root, "bin/gateway-fixture"),
    path.join(repository, "apps/desktop/bin/iscp-workbench"),
    path.join(repository, "apps/desktop/src/main/main.mjs"),
    path.join(repository, "apps/desktop/src/main/execution-client.mjs"),
    path.join(repository, "apps/desktop/src/main/execution-progress.mjs"),
    path.join(repository, "apps/desktop/src/main/execution-stream-projection.mjs"),
    path.join(repository, "apps/desktop/src/main/client-store.mjs"),
    path.join(repository, "apps/desktop/src/main/client-store-capability.mjs"),
    path.join(repository, "apps/desktop/src/main/desktop-auth.mjs"),
    path.join(repository, "apps/desktop/src/main/iscp-transport.mjs"),
    path.join(repository, "apps/desktop/test/stream-native-fixture.mjs"),
    path.join(repository, "apps/desktop/test/run-stream-native-qualification.mjs"),
    ...assets.filter(file => /\.(?:js|css)$/u.test(file)).map(file => path.join(repository, "apps/webchat/dist/assets", file)),
  ].map(fingerprint));
  evidence.tracked_diff_sha256 = crypto.createHash("sha256").update((await run("git", ["diff", "HEAD", "--binary"])).stdout).digest("hex");
  const env = { ...process.env, TMPDIR: "/private/tmp", SPARKCLAW_STREAM_FIXTURE_ROOT: root, SPARKCLAW_STREAM_FIXTURE_DSN: `postgres://postgres:${password}@127.0.0.1:${port}/stream_fixture?sslmode=disable`, SPARKCLAW_STREAM_FIXTURE_TOKEN: token, SPARKCLAW_STREAM_FIXTURE_SAMPLES: String(samples), SPARKCLAW_STREAM_FIXTURE_REPETITIONS: String(repetitions), SPARKCLAW_STREAM_FIXTURE_MODE: mode };
  let fixtureCommand = path.join(root, "bin/gateway-fixture");
  let fixtureArgs = ["-test.run", "^TestExecutionStreamNativeQualificationFixture$", "-test.timeout", "8m"];
  let fixtureEnv = env;
  if (linuxFixture) {
    const unusedPort = () => new Promise((resolve, reject) => { const server = net.createServer(); server.once("error", reject); server.listen(0, "127.0.0.1", () => { const value = server.address().port; server.close(() => resolve(value)); }); });
    const relayPort = await unusedPort(), issuerPort = await unusedPort();
    fixtureEnv = {...env, SPARKCLAW_STREAM_FIXTURE_DSN: env.SPARKCLAW_STREAM_FIXTURE_DSN.replace("@127.0.0.1:", "@host.docker.internal:"), SPARKCLAW_STREAM_FIXTURE_LISTEN: `0.0.0.0:${relayPort}`, SPARKCLAW_STREAM_FIXTURE_ISSUER_LISTEN: `0.0.0.0:${issuerPort}`};
    const image = process.env.SPARKCLAW_STREAM_FIXTURE_LINUX_IMAGE;
    evidence.gateway_runtime = {platform: "linux", image_id: (await run("docker", ["image", "inspect", image, "--format", "{{.Id}}"])).stdout.trim(), architecture: process.arch};
    const privateConfigDirectory = path.dirname(env.SPARKCLAW_STREAM_FIXTURE_MODEL_CONFIG);
    fixtureCommand = "docker";
    fixtureArgs = ["run", "--rm", "--name", gatewayContainer, "--label", `io.sparkclaw.stream.fixture=${id}`, "--user", `${process.getuid()}:${process.getgid()}`, "--shm-size", "256m", "--publish", `127.0.0.1:${relayPort}:${relayPort}`, "--publish", `127.0.0.1:${issuerPort}:${issuerPort}`, "--volume", `${repository}:/repo:ro`, "--volume", `${root}:${root}`, "--volume", `${privateConfigDirectory}:${privateConfigDirectory}:ro`, "--workdir", "/repo", ...Object.keys(fixtureEnv).filter(key => key.startsWith("SPARKCLAW_STREAM_FIXTURE_") || key === "OPENAI_API_KEY").flatMap(key => ["--env", key]), "--entrypoint", path.join(root, "bin/gateway-fixture"), image, ...fixtureArgs];
  }
  const origin = await new Promise((resolve, reject) => {
    const timeout = setTimeout(() => reject(new Error("Continuous Relay fixture startup timed out")), linuxFixture ? 90000 : 45000);
    fixture = spawn(fixtureCommand, fixtureArgs, { cwd: repository, env: fixtureEnv, stdio: ["ignore", "pipe", "pipe"] });
    let stdout = "";
    fixture.stdout.on("data", chunk => {
      stdout += chunk.toString();
      for (const line of stdout.split("\n")) {
        if (!line.startsWith('{"fixture_origin":')) continue;
        try { const value = JSON.parse(line); clearTimeout(timeout); resolve(value.fixture_origin); } catch { /* incomplete startup line */ }
      }
    });
    fixture.stderr.on("data", chunk => process.stderr.write(chunk));
    fixture.once("error", error => { clearTimeout(timeout); reject(error); });
    fixture.once("exit", code => { clearTimeout(timeout); reject(new Error(`Fixture exited ${code}: ${stdout}`)); });
  });
  const electron = (await import("electron")).default;
  const childEnv = { ...env, SPARKCLAW_STREAM_FIXTURE_ORIGIN: origin };
  delete childEnv.ELECTRON_RUN_AS_NODE;
  // Credentials remain in the private helper pipes/profile. The fixture HTTP
  // endpoint exposes only controlled timing evidence, never business methods.
  const result = await run(electron, [path.join(repository, "apps/desktop/test/stream-native-fixture.mjs")], { env: childEnv, timeout: (mode === "real-model" ? 300 : samples * 13 + 60) * 1000, maxBuffer: 2 << 20 });
  process.stdout.write(result.stdout);
  const native = JSON.parse(await fs.readFile(path.join(root, "native-evidence.json"), "utf8"));
  Object.assign(evidence, { passed: native.passed === true, summary: native.summary });
  if (!evidence.passed) throw new Error(native.error || "Native stream qualification failed");
} catch (error) {
  evidence.passed = false; evidence.error = error.message;
  if (error.stdout) process.stdout.write(error.stdout);
  if (error.stderr) process.stderr.write(error.stderr);
  throw error;
} finally {
  if (fixture && fixture.exitCode === null) {
    fixture.kill("SIGTERM");
    await Promise.race([new Promise(resolve => fixture.once("exit", resolve)), new Promise(resolve => setTimeout(resolve, 5000))]);
    if (fixture.exitCode === null) fixture.kill("SIGKILL");
  }
  for (const ownedContainer of [gatewayContainer, container]) {
    try {
      const label = (await run("docker", ["inspect", ownedContainer, "--format", '{{index .Config.Labels "io.sparkclaw.stream.fixture"}}'])).stdout.trim();
      if (label === id) await run("docker", ["rm", "--force", ownedContainer]);
    } catch { /* Never operate on an unverified container. */ }
  }
  await fs.writeFile(path.join(root, "receipt.json"), JSON.stringify(evidence, null, 2), { mode: 0o600 });
  console.log(JSON.stringify({ event: "stream_native_receipt", root, passed: evidence.passed, evidence_file: path.join(root, "receipt.json") }));
}
