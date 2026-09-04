import { readFile } from "node:fs/promises";
import { spawn } from "node:child_process";
import { createInterface } from "node:readline";

const timeoutMs = 30_000;
const endpointPath =
  process.env.SPARKCLAW_BROWSER_CDP_ENDPOINT_FILE ||
  "/run/sparkclaw/browserd/cdp-endpoint";
const browserCommand =
  process.env.SPARKCLAW_BROWSER_AUTOMATION_COMMAND ||
  "/app/node_modules/.bin/agent-browser";
const endpoint = JSON.parse(await readFile(endpointPath, "utf8"));

const cdpURL =
  process.env.SPARKCLAW_BROWSER_SMOKE_USE_HOST_ENDPOINT === "true"
    ? endpoint.hostWebSocketURL
    : endpoint.webSocketURL;
if (typeof cdpURL !== "string" || !cdpURL) {
  throw new Error("Host-CDP endpoint omitted the required WebSocket URL");
}

const environment = { ...process.env };
for (const name of Object.keys(environment)) {
  if (name.startsWith("AGENT_BROWSER_")) delete environment[name];
}
environment.AGENT_BROWSER_CDP = cdpURL;
environment.AGENT_BROWSER_NAMESPACE = `sparkclaw-smoke-${process.pid}`;
environment.AGENT_BROWSER_SESSION = `host-cdp-smoke-${process.pid}`;

const child = spawn(browserCommand, ["mcp", "--tools", "core,tabs"], {
  env: environment,
  stdio: ["pipe", "pipe", "pipe"],
});
const pending = new Map();
let nextID = 1;
let stderr = "";

child.stderr.setEncoding("utf8");
child.stderr.on("data", (chunk) => {
  stderr = (stderr + chunk).slice(-8192);
});

const lines = createInterface({ input: child.stdout });
lines.on("line", (line) => {
  let response;
  try {
    response = JSON.parse(line);
  } catch (error) {
    for (const entry of pending.values()) entry.reject(error);
    pending.clear();
    return;
  }
  const entry = pending.get(response.id);
  if (!entry) return;
  pending.delete(response.id);
  clearTimeout(entry.timer);
  if (response.error) {
    entry.reject(new Error(response.error.message || "agent-browser MCP error"));
  } else {
    entry.resolve(response.result);
  }
});

child.on("exit", (code, signal) => {
  const detail = stderr.trim();
  const error = new Error(
    `agent-browser MCP exited before smoke completion (${code ?? signal})${
      detail ? `: ${detail}` : ""
    }`,
  );
  for (const entry of pending.values()) entry.reject(error);
  pending.clear();
});

function send(value) {
  child.stdin.write(`${JSON.stringify(value)}\n`);
}

function request(method, params = {}) {
  const id = nextID++;
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      pending.delete(id);
      reject(new Error(`agent-browser MCP ${method} timed out`));
    }, timeoutMs);
    pending.set(id, { resolve, reject, timer });
    send({ jsonrpc: "2.0", id, method, params });
  });
}

async function callTool(name, args = {}) {
  const result = await request("tools/call", {
    name,
    arguments: args,
  });
  if (result?.isError) {
    throw new Error(`agent-browser ${name} smoke returned an error`);
  }
  return result;
}

async function stopChild() {
  if (child.exitCode !== null || child.signalCode !== null) return;
  child.kill("SIGTERM");
  await Promise.race([
    new Promise((resolve) => child.once("exit", resolve)),
    new Promise((resolve) => setTimeout(resolve, 5000)),
  ]);
  if (child.exitCode === null && child.signalCode === null) {
    child.kill("SIGKILL");
    await new Promise((resolve) => child.once("exit", resolve));
  }
}

const smokeTab = `sparkclaw-smoke-${process.pid}-${Date.now()}`;
let smokeTabMayExist = false;
let daemonMayExist = false;
try {
  const initialized = await request("initialize", {
    protocolVersion: "2025-11-25",
    capabilities: {},
    clientInfo: { name: "sparkclaw-deployment-smoke", version: "0.1.0" },
  });
  if (initialized?.serverInfo?.name !== "agent-browser") {
    throw new Error("unexpected Host-CDP MCP server");
  }
  send({ jsonrpc: "2.0", method: "notifications/initialized", params: {} });
  const tools = await request("tools/list");
  const names = new Set((tools?.tools || []).map((tool) => tool.name));
  for (const required of [
    "agent_browser_tab_new",
    "agent_browser_tab_switch",
    "agent_browser_snapshot",
    "agent_browser_tab_close",
    "agent_browser_close",
  ]) {
    if (!names.has(required)) throw new Error(`agent-browser omitted ${required}`);
  }
  daemonMayExist = true;
  smokeTabMayExist = true;
  await callTool("agent_browser_tab_new", {
    label: smokeTab,
    url: "about:blank",
  });
  await callTool("agent_browser_tab_switch", { tab: smokeTab });
  const snapshot = await callTool("agent_browser_snapshot");
  if (!Array.isArray(snapshot?.content) && snapshot?.structuredContent == null) {
    throw new Error("agent-browser snapshot smoke returned no content");
  }
  await callTool("agent_browser_tab_close", { tab: smokeTab });
  smokeTabMayExist = false;
  await callTool("agent_browser_close");
  daemonMayExist = false;
} finally {
  if (smokeTabMayExist) {
    try {
      await callTool("agent_browser_tab_close", { tab: smokeTab });
    } catch {
      // The MCP process is still detached below; cleanup is best effort only.
    }
  }
  if (daemonMayExist) {
    try {
      await callTool("agent_browser_close");
    } catch {
      // Preserve the original smoke failure; the daemon also has an idle timeout.
    }
  }
  await stopChild();
}

console.log(
  JSON.stringify({
    ok: true,
    browserPID: endpoint.browserPID,
    generation: endpoint.generation,
    presentation: endpoint.presentation,
  }),
);
