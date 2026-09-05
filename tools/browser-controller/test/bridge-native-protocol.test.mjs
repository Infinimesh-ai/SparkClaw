import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { once } from "node:events";
import fs from "node:fs/promises";
import net from "node:net";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

import {
  assertExpectedReadyStatus,
  BRIDGE_EXTENSION_ID,
  BRIDGE_PROTOCOL_VERSION,
  BRIDGE_VERSION,
  decodeNativeMessages,
  encodeNativeMessage,
  nativeSocketPath,
  parseNativeClientRequest,
  parseConnectionURL,
  parseLauncherArguments,
  readyStatus,
} from "../src/bridge-native-protocol.mjs";

const URL = "chrome-extension://mmlmfjhmonkocbjadbfplnigmagldckm/connect.html?" + new URLSearchParams({
  mcpRelayUrl: "ws://127.0.0.1:12345/extension/12345678-1234-4234-8234-123456789abc",
  client: JSON.stringify({ name: "test" }),
  protocolVersion: "2",
  token: "secret-value",
});

test("native launcher accepts only one exact Bridge connection URL", () => {
  assert.deepEqual(parseLauncherArguments([
    "--user-data-dir=/tmp/profile",
    "--no-sandbox",
    URL,
  ]), { operation: "openConnection", url: URL });
  assert.deepEqual(parseLauncherArguments(["--check"]), { operation: "status" });
  assert.throws(() => parseLauncherArguments(["https://example.com/"]), /invalid/);
  assert.throws(() => parseLauncherArguments([URL, "--new-window"]), /invalid/);
  assert.throws(() => parseConnectionURL(URL.replace("protocolVersion=2", "protocolVersion=3")), /invalid/);
  assert.throws(() => parseConnectionURL(`${URL}&extra=true`), /invalid/);
});

test("native client protocol exposes only loaded Bridge readiness", () => {
  assert.deepEqual(parseNativeClientRequest({ schema_version: 1, operation: "status" }), { operation: "status" });
  assert.deepEqual(parseNativeClientRequest({ schema_version: 1, operation: "openConnection", url: URL }), {
    operation: "openConnection",
    url: URL,
  });
  assert.throws(() => parseNativeClientRequest({ schema_version: 1, operation: "status", extra: true }), /invalid/);
  assert.deepEqual(readyStatus(null), { schema_version: 1, state: "not_ready" });
  const ready = readyStatus({
    extension_id: BRIDGE_EXTENSION_ID,
    version: BRIDGE_VERSION,
    protocol_version: BRIDGE_PROTOCOL_VERSION,
  });
  assert.doesNotThrow(() => assertExpectedReadyStatus(ready));
  assert.throws(() => assertExpectedReadyStatus({ ...ready, bridge_version: "stale" }), /stale/);
});

test("native message framing is bounded and preserves partial input", () => {
  const first = encodeNativeMessage({ id: 1, success: true });
  const second = encodeNativeMessage({ id: 2, success: false });
  const split = first.length + 3;
  const partial = decodeNativeMessages(Buffer.concat([first, second]).subarray(0, split));
  assert.deepEqual(partial.messages, [{ id: 1, success: true }]);
  const completed = decodeNativeMessages(Buffer.concat([
    partial.remaining,
    Buffer.concat([first, second]).subarray(split),
  ]));
  assert.deepEqual(completed.messages, [{ id: 2, success: false }]);
  assert.equal(completed.remaining.length, 0);
});

test("native socket remains in the owner runtime directory", () => {
  assert.equal(
    nativeSocketPath({ XDG_RUNTIME_DIR: "/run/user/1000" }),
    "/run/user/1000/sparkclaw/browser-controller/bridge-native.sock",
  );
  assert.throws(() => nativeSocketPath({ SPARKCLAW_BROWSER_BRIDGE_NATIVE_SOCKET: "/tmp/other.sock" }), /invalid/);
});

test("native host entrypoint publishes loaded Bridge readiness", async (t) => {
  const directory = await fs.mkdtemp(path.join(os.tmpdir(), "sparkclaw-native-host-"));
  const socketPath = path.join(directory, "bridge-native.sock");
  const host = fileURLToPath(new globalThis.URL("../src/browser-bridge-native-host.mjs", import.meta.url));
  const child = spawn(process.execPath, [host, `chrome-extension://${BRIDGE_EXTENSION_ID}/`], {
    env: { ...process.env, SPARKCLAW_BROWSER_BRIDGE_NATIVE_SOCKET: socketPath },
    stdio: ["pipe", "pipe", "pipe"],
  });
  let stderr = "";
  child.stderr.setEncoding("utf8");
  child.stderr.on("data", (chunk) => { stderr += chunk; });
  t.after(async () => {
    child.kill("SIGTERM");
    await fs.rm(directory, { recursive: true, force: true });
  });

  await waitForSocket(socketPath, child, () => stderr);
  const socketStat = await fs.stat(socketPath);
  assert.equal(socketStat.mode & 0o777, 0o600);

  child.stdin.write(encodeNativeMessage({
    type: "bridgeReady",
    extension_id: BRIDGE_EXTENSION_ID,
    version: BRIDGE_VERSION,
    protocol_version: BRIDGE_PROTOCOL_VERSION,
  }));
  const status = await requestStatus(socketPath);
  assert.doesNotThrow(() => assertExpectedReadyStatus(status));

  child.stdin.end();
  const [code] = await once(child, "exit");
  assert.equal(code, 0, stderr);
});

async function waitForSocket(socketPath, child, stderr) {
  for (let attempt = 0; attempt < 100; attempt += 1) {
    if (child.exitCode !== null) throw new Error(`native host exited early: ${stderr()}`);
    try {
      await fs.stat(socketPath);
      return;
    } catch (error) {
      if (error.code !== "ENOENT") throw error;
    }
    await new Promise((resolve) => setTimeout(resolve, 10));
  }
  throw new Error(`native host socket was not created: ${stderr()}`);
}

function requestStatus(socketPath) {
  return new Promise((resolve, reject) => {
    const socket = net.createConnection(socketPath);
    let response = "";
    socket.setEncoding("utf8");
    socket.on("connect", () => {
      socket.write(`${JSON.stringify({ schema_version: 1, operation: "status" })}\n`);
    });
    socket.on("data", (chunk) => { response += chunk; });
    socket.on("end", () => {
      try { resolve(JSON.parse(response)); } catch (error) { reject(error); }
    });
    socket.on("error", reject);
  });
}
