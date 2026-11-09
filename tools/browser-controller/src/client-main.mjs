#!/usr/bin/env node
import path from "node:path";
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
const clientFactory = new PlaywrightMCPClientFactory({
  executablePath: launcher,
  outputRoot: path.join(path.dirname(socketPath), "mcp-output"),
  electronAdapter,
  adoptRetainedPage: true,
  connectTimeoutMS: 15000,
  actionTimeoutMS: 10000,
  navigationTimeoutMS: 20000,
});
await clientFactory.prepare();
const controller = new BrowserController({ profileID: "client_embedded", clientFactory, defaultSessionTTLMS: 600000 });
const runtime = await startClientUnixServer({ socketPath, controller });
process.stdout.write(`${JSON.stringify({ event: "client_controller_ready", controller_generation: controller.controllerGeneration,
  node_version: process.versions.node, electron_version: process.versions.electron ?? "", versions: clientFactory.info() })}\n`);
let closing;
const close = () => closing ??= runtime.close().then(() => { process.exitCode = 0; }, () => { process.exitCode = 1; });
process.on("SIGTERM", () => void close());
process.on("SIGINT", () => void close());
