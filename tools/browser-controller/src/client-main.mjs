#!/usr/bin/env node
import path from "node:path";
import fs from "node:fs/promises";
import { spawn } from "node:child_process";
import { BrowserController } from "./controller-core.mjs";
import { PlaywrightMCPClientFactory } from "./mcp-client-core.mjs";
import { electronAdapterConfig } from "./electron-adapter-client.mjs";
import { startClientUnixServer } from "./client-http-server.mjs";

// Main supplies only private, fixed process configuration; no remote input is an entry path.
const socketPath = process.env.SPARKCLAW_BROWSER_CONTROLLER_SOCKET;
const launcher = process.env.SPARKCLAW_CLIENT_BROWSER_LAUNCHER;
if (!path.isAbsolute(socketPath ?? "") || path.basename(socketPath) !== "controller.sock" ||
    !path.isAbsolute(launcher ?? "")) throw new Error("Client Controller configuration is invalid");
const electronAdapter = electronAdapterConfig();
if (!electronAdapter) throw new Error("Client Controller requires the embedded Electron adapter");
const directory = path.dirname(socketPath);
const directoryIdentity = await fs.lstat(directory);
if (!directoryIdentity.isDirectory() || directoryIdentity.isSymbolicLink() || directoryIdentity.mode & 0o077 ||
    directoryIdentity.uid !== process.getuid() || path.dirname(launcher) !== directory ||
    path.dirname(electronAdapter.socketPath) !== directory || path.dirname(electronAdapter.secretPath) !== directory) {
  throw new Error("Client Controller runtime directory is invalid");
}
const children = new Set();
let parentLost = false;
let closing;
const clientFactory = new PlaywrightMCPClientFactory({
  executablePath: launcher,
  outputRoot: path.join(path.dirname(socketPath), "mcp-output"),
  electronAdapter,
  adoptRetainedPage: true,
  connectTimeoutMS: 15000,
  actionTimeoutMS: 10000,
  navigationTimeoutMS: 20000,
  // The shared Gateway adapter owns ready-state, quiet-period and route checks.
  // A fixed MCP delay on every read otherwise multiplies that polling window.
  // Backend/mail factories retain their existing 500 ms default.
  settleTimeoutMS: 0,
  spawn(...args) {
    if (closing || parentLost) throw new Error("Client Controller is stopping");
    const child = spawn(...args);
    children.add(child);
    child.once("exit", () => children.delete(child));
    child.once("error", () => children.delete(child));
    return child;
  },
});
await clientFactory.prepare();
const controller = new BrowserController({ profileID: "client_embedded", clientFactory, defaultSessionTTLMS: 600000 });
const runtime = await startClientUnixServer({ socketPath, controller });
process.stdout.write(`${JSON.stringify({ event: "client_controller_ready", controller_generation: controller.controllerGeneration,
  node_version: process.versions.node, electron_version: process.versions.electron ?? "", versions: clientFactory.info() })}\n`);
let retirement;
const retireChildren = () => retirement ??= Promise.all([...children].map(async child => {
  const exited = new Promise(resolve => child.once("exit", resolve));
  child.kill("SIGTERM");
  const kill = setTimeout(() => child.kill("SIGKILL"), 1000);
  try { await exited; } finally { clearTimeout(kill); }
}));
const close = () => closing ??= (async () => {
  const force = setTimeout(() => {
    // Main launches this entry as a dedicated process-group leader. This also
    // bounds cleanup after main itself crashed and cannot supervise us.
    try { process.kill(-process.pid, "SIGKILL"); } finally { process.exit(1); }
  }, 5000);
  try {
    // When main dies there are no live Electron targets to close. Retire MCP
    // first, otherwise its page-cleanup RPC waits on the vanished adapter until
    // the group watchdog kills us before we can remove the private files.
    if (parentLost) await retireChildren();
    try { await runtime.close(); }
    finally {
      if (parentLost) {
        const current = await fs.lstat(directory);
        if (current.isSymbolicLink() || current.dev !== directoryIdentity.dev || current.ino !== directoryIdentity.ino) {
          throw new Error("Client Controller runtime directory changed");
        }
        await fs.rm(directory, { recursive: true, force: true });
      }
    }
    process.exitCode = 0;
  }
  catch { process.exitCode = 1; }
  finally { clearTimeout(force); process.stdin.destroy(); }
})();
process.on("SIGTERM", () => void close());
process.on("SIGINT", () => void close());
const lostParent = () => {
  parentLost = true;
  // EOF can arrive after SIGTERM already began graceful shutdown. Retire the
  // now-orphaned MCP even when close() is already awaiting its page RPC.
  void retireChildren().catch(() => {});
  void close();
};
process.stdin.on("end", lostParent);
process.stdin.on("error", lostParent);
process.stdin.resume();
