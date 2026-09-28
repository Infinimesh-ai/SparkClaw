#!/usr/bin/env node

import fs from "node:fs/promises";
import { execFileSync, spawn } from "node:child_process";
import net from "node:net";
import path from "node:path";
import { fileURLToPath } from "node:url";

import {
  BRIDGE_EXTENSION_ID,
  BRIDGE_PROTOCOL_VERSION,
  BRIDGE_VERSION,
  decodeNativeMessages,
  encodeNativeMessage,
  nativeSocketPath,
  parseNativeClientRequest,
  readyStatus,
} from "./bridge-native-protocol.mjs";
import { listenOwnerOnlyUnixSocket } from "./unix-socket.mjs";

const expectedOrigin = `chrome-extension://${BRIDGE_EXTENSION_ID}/`;
if (process.argv[2] !== expectedOrigin) process.exit(1);

const socketPath = nativeSocketPath();
const focusHelper = fileURLToPath(new URL("./browser-bridge-focus.py", import.meta.url));
const pending = new Map();
let nextID = 0;
let nativeInput = Buffer.alloc(0);
let bridgeReady = null;

await prepareSocket(socketPath);
const server = net.createServer((client) => handleClient(client));
server.on("error", () => shutdown(1));
await listenOwnerOnlyUnixSocket(server, socketPath).catch(() => shutdown(1));

process.stdin.on("data", (chunk) => {
  try {
    nativeInput = Buffer.concat([nativeInput, Buffer.from(chunk)]);
    const decoded = decodeNativeMessages(nativeInput);
    nativeInput = decoded.remaining;
    for (const message of decoded.messages) handleNativeMessage(message);
  } catch {
    shutdown(1);
  }
});
process.stdin.on("end", () => shutdown(0));
process.on("SIGTERM", () => shutdown(0));
process.on("SIGINT", () => shutdown(0));

function handleClient(client) {
  client.setEncoding("utf8");
  let input = "";
  const timer = setTimeout(() => client.destroy(), 6000);
  timer.unref?.();
  client.on("data", (chunk) => {
    input += chunk;
    if (input.length > 20 << 10) return client.destroy();
    const newline = input.indexOf("\n");
    if (newline < 0) return;
    try {
      const request = parseNativeClientRequest(JSON.parse(input.slice(0, newline)));
      if (request.operation === "status") {
        clearTimeout(timer);
        client.end(`${JSON.stringify(readyStatus(bridgeReady))}\n`);
        return;
      }
      if (!bridgeReady) throw new Error("bridge is not ready");
      const id = ++nextID;
      pending.set(id, { client, timer, operation: request.operation,
        windowSnapshot: request.operation === "openConnection" ? snapshotBrowserWindows() : null });
      process.stdout.write(encodeNativeMessage(request.operation === "taskStatus"
        ? { type: "taskStatus", id } : { type: "openConnection", id, url: request.url }));
    } catch {
      clearTimeout(timer);
      client.end(`${JSON.stringify({ schema_version: 1, state: "rejected" })}\n`);
    }
  });
  client.on("close", () => {
    for (const [id, entry] of pending) {
      if (entry.client === client) {
        clearTimeout(entry.timer);
        pending.delete(id);
      }
    }
  });
}

function handleNativeMessage(message) {
  if (message?.type === "taskStatusResult" && Number.isSafeInteger(message.id) &&
      Object.keys(message).sort().join("\n") === "id\ntype\nwindows") {
    const entry = pending.get(message.id);
    if (entry?.operation !== "taskStatus") return;
    const windows = message.windows;
    if (!Array.isArray(windows) || windows.length > 100 || windows.some(window =>
      !window || Object.keys(window).sort().join(",") !== "focused,other_tabs,stale_task_tabs,task_tabs,window_id" ||
      typeof window.focused !== "boolean" ||
      [window.window_id, window.task_tabs, window.stale_task_tabs, window.other_tabs]
        .some(value => !Number.isSafeInteger(value) || value < 0))) return;
    pending.delete(message.id);
    clearTimeout(entry.timer);
    entry.client.end(`${JSON.stringify({ schema_version: 1, state: "ready", windows })}\n`);
    return;
  }
  if (message?.type === "bridgeReady" &&
      Object.keys(message).sort().join("\n") === "extension_id\nprotocol_version\ntype\nversion" &&
      message.extension_id === BRIDGE_EXTENSION_ID && message.version === BRIDGE_VERSION &&
      message.protocol_version === BRIDGE_PROTOCOL_VERSION) {
    bridgeReady = message;
    return;
  }
  if (message?.type === "focusHandoff" && Object.keys(message).length === 1) {
    focusBrowserWindow();
    return;
  }
  if (message?.type !== "openConnectionResult" || !Number.isSafeInteger(message.id) ||
      typeof message.success !== "boolean" || Object.keys(message).sort().join("\n") !== "id\nsuccess\ntype") {
    return;
  }
  const entry = pending.get(message.id);
  if (entry?.operation !== "openConnection") return;
  pending.delete(message.id);
  clearTimeout(entry.timer);
  if (message.success) lowerNewBrowserWindow(entry.windowSnapshot);
  entry.client.end(`${JSON.stringify({
    schema_version: 1,
    state: message.success ? "opened" : "rejected",
  })}\n`);
}

let focusProcess = null;
function snapshotBrowserWindows() {
  if (!process.env.DISPLAY) return null;
  try {
    return execFileSync("python3", [focusHelper, String(process.ppid), "snapshot"], {
      encoding: "utf8",
      env: focusEnvironment(),
      timeout: 1000,
      maxBuffer: 8192,
      stdio: ["ignore", "pipe", "ignore"],
    }).trim();
  } catch {
    return null;
  }
}

function lowerNewBrowserWindow(snapshot) {
  if (!snapshot || !process.env.DISPLAY) return;
  const child = spawn("python3", [focusHelper, String(process.ppid), "lower-new", snapshot], {
    env: focusEnvironment(),
    stdio: "ignore",
  });
  const timer = setTimeout(() => child.kill("SIGKILL"), 2000);
  timer.unref?.();
  child.once("error", () => clearTimeout(timer));
  child.once("exit", () => clearTimeout(timer));
}

function focusEnvironment() {
  return {
    DISPLAY: process.env.DISPLAY,
    XAUTHORITY: process.env.XAUTHORITY || "",
    PATH: process.env.PATH || "/usr/bin:/bin",
  };
}

function focusBrowserWindow() {
  if (focusProcess || !process.env.DISPLAY) return;
  const child = spawn("python3", [focusHelper, String(process.ppid)], {
    env: focusEnvironment(),
    stdio: "ignore",
  });
  focusProcess = child;
  const timer = setTimeout(() => child.kill("SIGKILL"), 2000);
  timer.unref?.();
  child.once("error", () => {
    clearTimeout(timer);
    if (focusProcess === child) focusProcess = null;
  });
  child.once("exit", () => {
    clearTimeout(timer);
    if (focusProcess === child) focusProcess = null;
  });
}

async function prepareSocket(target) {
  await fs.mkdir(path.dirname(target), { recursive: true, mode: 0o700 });
  const stat = await fs.lstat(path.dirname(target));
  if (!stat.isDirectory() || stat.isSymbolicLink() || stat.uid !== process.getuid()) process.exit(1);
  const stale = await fs.lstat(target).catch(() => null);
  if (stale && (!stale.isSocket() || stale.uid !== process.getuid())) process.exit(1);
  if (stale) await fs.unlink(target);
}

let closing = false;
function shutdown(code) {
  if (closing) return;
  closing = true;
  for (const entry of pending.values()) {
    clearTimeout(entry.timer);
    entry.client.destroy();
  }
  pending.clear();
  focusProcess?.kill("SIGKILL");
  server.close();
  void fs.unlink(socketPath).catch(() => {}).finally(() => process.exit(code));
}
