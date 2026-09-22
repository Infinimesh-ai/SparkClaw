import assert from "node:assert/strict";
import crypto from "node:crypto";
import fs from "node:fs/promises";
import http from "node:http";
import os from "node:os";
import path from "node:path";
import { spawn } from "node:child_process";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const DESKTOP_TOKEN = "package-proof-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx";
const output = path.join(root, "apps", "desktop", "dist");
const manifest = JSON.parse(await fs.readFile(path.join(output, "release-manifest.json"), "utf8"));
assert.equal(manifest.architecture, "arm64");
assert.equal(manifest.artifacts.length, 2);

const temporary = await fs.mkdtemp(path.join(os.tmpdir(), "sparkclaw-desktop-artifacts-"));
let xvfb;
let gatewayServer;
try {
  const display = await startXvfb();
  const gateway = await startProbeServer();
  gatewayServer = gateway.server;
  const runtimeDirectory = path.join(temporary, "runtime");
  await fs.mkdir(runtimeDirectory, { mode: 0o700 });
  const descriptorPath = path.join(runtimeDirectory, "local-workbench.json");
  const credentialPath = path.join(runtimeDirectory, "desktop-client.json");
  await fs.writeFile(descriptorPath, JSON.stringify({
    schema_version: 1, origin: gateway.origin, deployment_id: "qualification-deployment",
  }), { mode: 0o600 });
  await fs.writeFile(credentialPath, JSON.stringify({
    schema_version: 1, deployment_id: "qualification-deployment", client_id: "qualification-desktop",
    owner_id: "owner", client_name: "SparkClaw Desktop Qualification", token: DESKTOP_TOKEN,
  }), { mode: 0o600 });
  const evidence = [];
  for (const artifact of manifest.artifacts) {
    const filename = path.join(output, artifact.name);
    const content = await fs.readFile(filename);
    assert.equal(crypto.createHash("sha256").update(content).digest("hex"), artifact.sha256);
    let executable = filename;
    const env = { ...process.env, DISPLAY: display };
    if (artifact.name.endsWith(".deb")) {
      const field = await run("dpkg-deb", ["--field", filename, "Architecture"]);
      assert.equal(field.stdout.trim(), "arm64");
      const extracted = path.join(temporary, "deb-root");
      await fs.mkdir(extracted);
      await run("dpkg-deb", ["--extract", filename, extracted]);
      executable = path.join(extracted, "opt", "SparkClaw", "sparkclaw");
    } else {
      const extracted = path.join(temporary, "appimage-root");
      await fs.mkdir(extracted);
      await run(filename, ["--appimage-extract"], { cwd: extracted });
      executable = path.join(extracted, "squashfs-root", "AppRun");
    }
    const userData = path.join(temporary, artifact.name.replaceAll(/[^a-zA-Z0-9]/gu, "-"));
    const adapterDirectory = path.join(userData, "adapter");
    const ready = await launchAndStop(executable, ["--qualification-workbench", "--disable-gpu"], {
      ...env,
      SPARKCLAW_DESKTOP_USER_DATA_DIR: userData,
      SPARKCLAW_ELECTRON_ADAPTER_SOCKET: path.join(adapterDirectory, "electron-adapter.sock"),
      SPARKCLAW_ELECTRON_ADAPTER_SECRET_FILE: path.join(adapterDirectory, "adapter-secret"),
      SPARKCLAW_DESKTOP_CONNECTION_FILE: descriptorPath,
      SPARKCLAW_DESKTOP_CREDENTIAL_FILE: credentialPath,
    });
    assert.equal(ready.architecture, "arm64");
    assert.equal(ready.runtime_kind, "electron");
    assert.equal(ready.workbench_loaded, true);
    assert.equal(ready.desktop_browser_panel, true);
    assert.equal(ready.desktop_ipc_bounded, true);
    assert.equal(ready.gateway_http_proxy, true);
    assert.equal(ready.gateway_sse_proxy, true);
    assert.equal(ready.speech_websocket_proxy, true);
    evidence.push({ artifact: artifact.name, launched: true, electron: ready.electron_version, chromium: ready.chromium_version });
  }
  process.stdout.write(`${JSON.stringify({ event: "sparkclaw_desktop_artifacts_qualified", isolated_display: true, evidence })}\n`);
} finally {
  await closeServer(gatewayServer);
  await stopChild(xvfb);
  await fs.rm(temporary, { recursive: true, force: true });
}

async function startProbeServer() {
  let origin = "";
  const server = http.createServer((request, response) => {
    if (request.url === "/api/workbench/identity") {
      if (request.headers.authorization !== `Bearer ${DESKTOP_TOKEN}`) return response.writeHead(401).end();
      response.writeHead(200, { "content-type": "application/json", "cache-control": "no-store" });
      response.end(JSON.stringify({ deployment_id: "qualification-deployment", owner_id: "owner", client_id: "qualification-desktop" }));
      return;
    }
    if (request.url === "/desktop-sse") {
      response.writeHead(200, { "content-type": "text/event-stream", "cache-control": "no-cache" });
      response.end("data: stream-ok\n\n");
      return;
    }
    if (request.url !== "/desktop-probe") return response.writeHead(404).end();
    const chunks = [];
    request.on("data", (chunk) => chunks.push(chunk));
    request.on("end", () => {
      const authorization = request.headers.authorization === `Bearer ${DESKTOP_TOKEN}` ? "package-proof" : "missing";
      response.end(`gateway:${authorization}:${Buffer.concat(chunks).toString("utf8")}`);
    });
  });
  trackServerSockets(server);
  server.on("upgrade", (request, socket) => {
    if (request.url !== "/desktop-speech" || request.headers.origin !== origin) {
      socket.end("HTTP/1.1 403 Forbidden\r\n\r\n");
      return;
    }
    const key = request.headers["sec-websocket-key"];
    const accept = crypto.createHash("sha1").update(`${key}258EAFA5-E914-47DA-95CA-C5AB0DC85B11`).digest("base64");
    socket.write(`HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: ${accept}\r\n\r\n`);
    const payload = Buffer.from("speech-ok");
    socket.write(Buffer.concat([Buffer.from([0x81, payload.length]), payload]));
    setTimeout(() => socket.end(), 100);
  });
  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });
  origin = `http://127.0.0.1:${server.address().port}`;
  return { server, origin };
}

async function closeServer(server) {
  if (!server) return;
  for (const socket of server.sparkclawSockets ?? []) socket.destroy();
  server.closeAllConnections?.();
  await new Promise((resolve) => server.close(resolve));
}

function trackServerSockets(server) {
  server.sparkclawSockets = new Set();
  server.on("connection", (socket) => {
    server.sparkclawSockets.add(socket);
    socket.once("close", () => server.sparkclawSockets.delete(socket));
  });
}

async function startXvfb() {
  const child = spawn("Xvfb", ["-displayfd", "3", "-screen", "0", "1440x900x24", "-nolisten", "tcp", "-noreset"], {
    stdio: ["ignore", "ignore", "pipe", "pipe"],
  });
  xvfb = child;
  const display = await readLine(child.stdio[3], child, 5000);
  assert.match(display, /^[0-9]+$/u);
  return `:${display}`;
}

async function launchAndStop(executable, args, env) {
  const child = spawn(executable, args, { cwd: temporary, env, stdio: ["ignore", "pipe", "pipe"] });
  const ready = await readMatchingJSON(child.stdout, child.stderr, child, "sparkclaw_electron_ready", 25_000);
  await stopChild(child);
  return ready;
}

async function run(executable, args, { cwd } = {}) {
  return await new Promise((resolve, reject) => {
    const child = spawn(executable, args, { cwd, stdio: ["ignore", "pipe", "pipe"] });
    const stdout = [];
    const stderr = [];
    child.stdout.on("data", (chunk) => stdout.push(chunk));
    child.stderr.on("data", (chunk) => stderr.push(chunk));
    child.once("error", reject);
    child.once("close", (code) => code === 0
      ? resolve({ stdout: Buffer.concat(stdout).toString("utf8") })
      : reject(new Error(Buffer.concat(stderr).toString("utf8"))));
  });
}

async function readLine(stream, child, timeoutMS) {
  return await new Promise((resolve, reject) => {
    let input = "";
    const timer = setTimeout(() => reject(new Error("Packaged desktop did not become ready")), timeoutMS);
    stream.on("data", (chunk) => {
      input += chunk;
      const newline = input.indexOf("\n");
      if (newline < 0) return;
      clearTimeout(timer);
      resolve(input.slice(0, newline).trim());
    });
    child.once("exit", (code) => reject(new Error(`Packaged desktop exited before ready (${code})`)));
  });
}

async function readMatchingJSON(stream, errorStream, child, event, timeoutMS) {
  return await new Promise((resolve, reject) => {
    let input = "";
    let errors = "";
    const timer = setTimeout(() => reject(new Error("Packaged desktop did not become ready")), timeoutMS);
    const onData = (chunk) => {
      input += chunk;
      for (;;) {
        const newline = input.indexOf("\n");
        if (newline < 0) return;
        const line = input.slice(0, newline).trim();
        input = input.slice(newline + 1);
        try {
          const value = JSON.parse(line);
          if (value.event === event) {
            clearTimeout(timer);
            stream.removeListener("data", onData);
            resolve(value);
            return;
          }
        } catch {
          // AppImage extraction may print a temporary icon path before the app starts.
        }
      }
    };
    errorStream.on("data", (chunk) => { errors = `${errors}${chunk}`.slice(-4000); });
    stream.on("data", onData);
    child.once("error", reject);
    child.once("exit", (code) => reject(new Error(`Packaged desktop exited before ready (${code}): ${errors}`)));
  });
}

async function stopChild(child) {
  if (!child || child.exitCode !== null) return;
  child.kill("SIGTERM");
  await Promise.race([new Promise((resolve) => child.once("exit", resolve)), new Promise((resolve) => setTimeout(resolve, 3000))]);
  if (child.exitCode === null) child.kill("SIGKILL");
}
