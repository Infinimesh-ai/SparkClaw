#!/usr/bin/env node

import path from "node:path";

import { PlaywrightCLIClientFactory } from "./cli-client.mjs";
import { BrowserController } from "./controller.mjs";
import { startUnixServer } from "./http-server.mjs";
import { PlaywrightMCPClientFactory } from "./mcp-client.mjs";

const profileID = process.env.SPARKCLAW_BROWSER_PROFILE_ID?.trim() || "default";
const socketPath = requiredEnv("SPARKCLAW_BROWSER_CONTROLLER_SOCKET");
const runtimeDirectory = path.dirname(socketPath);

const clientFactory = new PlaywrightMCPClientFactory({
  browserChannel: process.env.SPARKCLAW_BROWSER_CHANNEL?.trim() || "chromium",
  executablePath: process.env.SPARKCLAW_BROWSER_EXECUTABLE?.trim() || "",
  userDataDir: process.env.SPARKCLAW_BROWSER_USER_DATA_DIR?.trim() || "",
  outputRoot: process.env.SPARKCLAW_BROWSER_OUTPUT_DIR?.trim() || path.join(runtimeDirectory, "mcp-output"),
  connectTimeoutMS: boundedEnv("SPARKCLAW_BROWSER_CONNECT_TIMEOUT_MS", 15_000, 1_000, 120_000),
  actionTimeoutMS: boundedEnv("SPARKCLAW_BROWSER_ACTION_TIMEOUT_MS", 10_000, 500, 120_000),
  navigationTimeoutMS: boundedEnv("SPARKCLAW_BROWSER_NAVIGATION_TIMEOUT_MS", 30_000, 1_000, 120_000),
});
const scriptFactory = new PlaywrightCLIClientFactory({
  browserChannel: process.env.SPARKCLAW_BROWSER_CHANNEL?.trim() || "chromium",
  executablePath: process.env.SPARKCLAW_BROWSER_EXECUTABLE?.trim() || "",
  userDataDir: process.env.SPARKCLAW_BROWSER_USER_DATA_DIR?.trim() || "",
  runtimeRoot: process.env.SPARKCLAW_BROWSER_CLI_RUNTIME_DIR?.trim() || path.join(runtimeDirectory, "cli-runtime"),
  connectTimeoutMS: boundedEnv("SPARKCLAW_BROWSER_CONNECT_TIMEOUT_MS", 15_000, 1_000, 120_000),
  actionTimeoutMS: boundedEnv("SPARKCLAW_BROWSER_ACTION_TIMEOUT_MS", 10_000, 500, 120_000),
  navigationTimeoutMS: boundedEnv("SPARKCLAW_BROWSER_NAVIGATION_TIMEOUT_MS", 30_000, 1_000, 120_000),
  diagnostic: (event) => process.stderr.write(`${JSON.stringify(event)}\n`),
});
await Promise.all([clientFactory.prepare(), scriptFactory.prepare()]);
const controller = new BrowserController({ profileID, clientFactory, scriptFactory });
const runtime = await startUnixServer({ socketPath, controller });

process.stdout.write(`${JSON.stringify({
  event: "browser_controller_started",
  socket_path: socketPath,
  profile_id: profileID,
  controller_generation: controller.controllerGeneration,
})}\n`);

let closing = false;
async function shutdown(signal) {
  if (closing) return;
  closing = true;
  try {
    await runtime.close();
    process.stdout.write(`${JSON.stringify({ event: "browser_controller_stopped", signal })}\n`);
    process.exitCode = 0;
  } catch {
    process.exitCode = 1;
  }
}

process.on("SIGINT", () => void shutdown("SIGINT"));
process.on("SIGTERM", () => void shutdown("SIGTERM"));

function boundedEnv(name, fallback, minimum, maximum) {
  const raw = process.env[name]?.trim();
  if (!raw) return fallback;
  const parsed = Number(raw);
  if (!Number.isSafeInteger(parsed) || parsed < minimum || parsed > maximum) {
    throw new Error(`${name} is invalid`);
  }
  return parsed;
}

function requiredEnv(name) {
  const value = process.env[name]?.trim();
  if (!value) throw new Error(`${name} is required`);
  return value;
}
