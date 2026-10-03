import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { spawn, execFile } from "node:child_process";
import { promisify } from "node:util";
import { fileURLToPath } from "node:url";
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../../..");
if (!["linux", "darwin"].includes(process.platform)) throw new Error("Native R3 qualification requires Linux or macOS");
// macOS's /var temporary directory is a symlink. Use its canonical parent
// without weakening the Broker's private-path checks.
const temporary = await fs.mkdtemp(path.join(await fs.realpath(os.tmpdir()), "sparkclaw-r3-native-"));
let xvfb, broker;
const run = promisify(execFile);
try {
  await run("openssl", ["req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1", "-subj", "/CN=localhost", "-addext", "subjectAltName=DNS:localhost,IP:127.0.0.1", "-keyout", path.join(temporary, "key.pem"), "-out", path.join(temporary, "cert.pem")]);
  const binary = path.join(temporary, "broker-fixture");
  await run("go", ["build", "-o", binary, "./internal/r3browser/testdata/broker_fixture.go"], { cwd: path.join(root, "services/gateway") });
  const origin = await new Promise((resolve, reject) => {
    broker = spawn(binary, [path.join(temporary, "backend-control"), path.join(temporary, "cert.pem"), path.join(temporary, "key.pem")], { stdio: ["ignore", "pipe", "pipe"] });
    let output = ""; broker.stdout.on("data", (data) => { output += data; if (output.includes("\n")) { try { resolve(JSON.parse(output.split("\n")[0]).origin); } catch (error) { reject(error); } } });
    broker.stderr.on("data", (data) => process.stderr.write(data)); broker.once("error", reject); broker.once("exit", (code) => reject(new Error(`broker exited ${code}`)));
  });
  const display = process.platform === "linux" ? await new Promise((resolve, reject) => {
    xvfb = spawn("Xvfb", ["-displayfd", "1", "-screen", "0", "1600x1000x24", "-nolisten", "tcp"], { stdio: ["ignore", "pipe", "pipe"] });
    xvfb.stdout.once("data", (data) => resolve(`:${String(data).trim()}`));xvfb.once("error", reject);
  }) : undefined;
  const electron = process.env.SPARKCLAW_R3_TEST_ELECTRON || (await import("electron")).default;
  const flags = process.platform === "linux" ? ["--no-sandbox", "--disable-gpu"] : [];
  const result = await run(electron, [path.join(root, "apps/desktop/test/r3-host-native-fixture.mjs"), ...flags], { cwd: root, timeout: 60000, maxBuffer: 1 << 20, env: { ...process.env, ...(display ? { DISPLAY: display } : {}), SPARKCLAW_R3_QUALIFICATION_ROOT: temporary, SPARKCLAW_R3_QUALIFICATION_ORIGIN: origin } });
  process.stdout.write(result.stdout);
} finally { broker?.kill("SIGTERM");xvfb?.kill("SIGTERM");await fs.rm(temporary, { recursive: true, force: true }); }
