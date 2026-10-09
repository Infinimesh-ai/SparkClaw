import crypto from "node:crypto";
import fs from "node:fs/promises";
import path from "node:path";
import { spawn, execFile } from "node:child_process";
import { promisify } from "node:util";
import { fileURLToPath } from "node:url";
import { ClientStore } from "../src/main/client-store.mjs";
import { assertPrivateDirectory, readPrivateJSON, writePrivateJSON } from "../../../scripts/lib/private-workbench.mjs";

// Prepare an independent disposable lab with iscp-local-lab prepare-expansion
// first. This runner starts its own real Gateway fixture, not the lab Gateway.
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../../..");
const lab = process.argv[2];
if (!["linux", "darwin"].includes(process.platform) || process.argv.length !== 3 || !path.isAbsolute(lab || "")) throw new Error("Usage: Node 26 run-host-iscp-native-qualification.mjs <prepared private expansion lab>");
await assertPrivateDirectory(lab);
const metadata = await readPrivateJSON(path.join(lab, "run.json"));
if (!metadata.qualified_operations?.includes("browser.host.poll")) throw new Error("An explicitly qualified disposable expansion lab is required");
const exec = promisify(execFile);
const run = (command,args,options={}) => exec(command,args,{timeout:180000,killSignal:"SIGKILL",...options});
try {
  const { stdout } = await run("docker", ["inspect", metadata.gateway_container, "--format", "{{.State.Running}}"]);
  if (stdout.trim() === "true") throw new Error("Stop this lab's Gateway before using its device identity for the fixture");
} catch (error) { if (!/no such/iu.test(error.stderr || "")) throw error; }
const temporary = await fs.mkdtemp(path.join(lab, "native-host-"));
await fs.chmod(temporary, 0o700);
await fs.mkdir(path.join(temporary, "bin"), { mode: 0o700 });
await fs.mkdir(path.join(temporary, "profile"), { mode: 0o700 });
const store = new ClientStore(path.join(temporary, "profile", "workbench"));
const installationID = store.installationID;
store.close();
const token = crypto.randomBytes(32).toString("base64url");
const sourceFiles = ["apps/desktop/src/main/main.mjs", "apps/desktop/src/main/presentation.mjs", "apps/desktop/src/browser/page-registry.mjs", "apps/desktop/src/browser/host-agent.mjs", "apps/desktop/src/browser/native-page-commands.mjs"];
const desktopSource = { revision: (await run("git",["rev-parse","HEAD"],{cwd:root})).stdout.trim(), files:{} };
for (const name of sourceFiles) desktopSource.files[name] = crypto.createHash("sha256").update(await fs.readFile(path.join(root,name))).digest("hex");
let broker, xvfb;
try {
  const enrollment = await readPrivateJSON(path.join(lab, "gateway/enrollment.json"));
  enrollment.relay_base_url = metadata.relay_url;
  enrollment.relay_websocket_url = new URL("/v2/relay/connect", metadata.relay_url).href.replace(/^http/u, "ws");
  await writePrivateJSON(path.join(temporary, "gateway-enrollment.json"), enrollment);
  for (const peer of ["desktop", "gateway"]) {
    const profile = await readPrivateJSON(path.join(lab, `${peer}-helper.json`));
    profile.grant_renewal.pending_file = path.join(temporary, `${peer}-pending.json`);
    if (peer === "gateway") profile.enrollment_file = path.join(temporary, "gateway-enrollment.json");
    await writePrivateJSON(path.join(temporary, `${peer}-helper.json`), profile);
  }
  const desktopProfile = await readPrivateJSON(path.join(lab, "desktop-profile.json"));
  desktopProfile.helper_config = path.join(temporary, "desktop-helper.json");
  await writePrivateJSON(path.join(temporary, "desktop-profile.json"), desktopProfile);
  await run("openssl", ["req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1", "-subj", "/CN=localhost", "-addext", "subjectAltName=DNS:localhost,IP:127.0.0.1", "-keyout", path.join(temporary, "key.pem"), "-out", path.join(temporary, "cert.pem")]);
  await fs.chmod(path.join(temporary, "key.pem"), 0o600);
  await fs.mkdir(path.join(root, "apps/desktop/bin"), {recursive:true});
  await run("go", ["build", "-o", path.join(root, "apps/desktop/bin/iscp-workbench"), "./cmd/iscp-workbench"], { cwd: path.join(root, "services/gateway") });
  const binary = path.join(temporary, "bin/native-broker-fixture");
  await run("go", ["test", "-c", "-o", binary, "./internal/gateway"], { cwd: path.join(root, "services/gateway") });
  const env = { ...process.env, SPARKCLAW_HOST_ISCP_FIXTURE_ROOT: temporary, SPARKCLAW_HOST_ISCP_FIXTURE_TOKEN: token, SPARKCLAW_HOST_ISCP_INSTALLATION: installationID };
  const origin = await new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error("Native ISCP fixture startup timed out")), 20000);
    broker = spawn(binary, ["-test.run", "^TestWorkbenchISCPNativeHostQualificationFixture$", "-test.timeout", "120s"], { env, stdio: ["ignore", "pipe", "pipe"] });
    let output = "";
    broker.stdout.on("data", chunk => {
      output += chunk;
      if (output.length > 64 << 10) { clearTimeout(timer); reject(new Error("Fixture startup output exceeds bounds")); return; }
      for (const line of output.split("\n").filter(line => line.startsWith('{"fixture_origin":'))) {
        try { const parsed = JSON.parse(line); clearTimeout(timer); resolve(parsed.fixture_origin); } catch { /* Wait for a complete startup frame. */ }
      }
    });
    broker.stderr.on("data", chunk => process.stderr.write(chunk));
    broker.once("error", error => { clearTimeout(timer); reject(error); });
    broker.once("exit", code => { clearTimeout(timer); reject(new Error(`Native ISCP fixture exited ${code}: ${output}`)); });
  });
  const display = process.platform === "linux" ? await new Promise((resolve, reject) => {
    xvfb = spawn("Xvfb", ["-displayfd", "1", "-screen", "0", "1600x1000x24", "-nolisten", "tcp"], { stdio: ["ignore", "pipe", "pipe"] });
    xvfb.stdout.once("data", data => resolve(`:${String(data).trim()}`));
    xvfb.once("error", reject);
  }) : undefined;
  const electron = process.env.SPARKCLAW_HOST_TEST_ELECTRON || (await import("electron")).default;
  const flags = process.platform === "linux" ? ["--no-sandbox", "--disable-gpu"] : [];
  const result = await run(electron, [path.join(root, "apps/desktop/test/host-iscp-native-fixture.mjs"), ...flags], { cwd: root, timeout: 100000, killSignal: "SIGKILL", maxBuffer: 1 << 20, env: { ...env, ...(display ? { DISPLAY: display } : {}), SPARKCLAW_HOST_ISCP_FIXTURE_ORIGIN: origin } });
  process.stdout.write(result.stdout);
  const evidence = await readPrivateJSON(path.join(temporary, "evidence.json"));
  if (evidence.passed !== true) throw new Error(`Native evidence failed: ${evidence.error || "missing successful receipt"}`);
  console.log(JSON.stringify({ screenshot_file: path.join(temporary, "native-iscp-host.png") }));
} catch (error) {
  if (error.stdout) process.stdout.write(error.stdout);
  if (error.stderr) process.stderr.write(error.stderr);
  throw error;
} finally {
  broker?.kill("SIGTERM");
  xvfb?.kill("SIGTERM");
  const evidenceFile = path.join(temporary, "evidence.json");
  const retained = await readPrivateJSON(evidenceFile, { optional: true });
  if (retained) {
    retained.source = metadata.source;
    retained.desktop_source = desktopSource;
    await writePrivateJSON(evidenceFile, retained);
    console.log(JSON.stringify({ evidence_file: evidenceFile, passed: retained.passed === true }));
  }
  // Retain only this private lab's evidence and journals for review. The lab
  // owner uses iscp-local-lab down to remove its labelled Relay/issuer services.
}
