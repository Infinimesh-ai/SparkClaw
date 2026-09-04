#!/usr/bin/env node

import fs from "node:fs";
import readline from "node:readline";

const logPath = process.env.FAKE_MCP_LOG;
const calls = [];

writeLog({
  event: "started",
  argv: process.argv.slice(2),
  extension_token_present: Boolean(process.env.PLAYWRIGHT_MCP_EXTENSION_TOKEN),
  inherited_forbidden_env_present: Boolean(process.env.PLAYWRIGHT_MCP_HEADLESS),
});

const input = readline.createInterface({ input: process.stdin });
input.on("line", (line) => {
  const message = JSON.parse(line);
  if (!Object.hasOwn(message, "id")) return;
  if (message.method === "initialize") {
    respond(message.id, {
      protocolVersion: "2025-06-18",
      capabilities: { tools: {} },
      serverInfo: { name: "fake-playwright-mcp", version: "1" },
    });
    return;
  }
  if (message.method === "tools/call") {
    calls.push(message.params);
    writeLog({ event: "tool", name: message.params.name, arguments: message.params.arguments });
    respond(message.id, { content: [{ type: "text", text: "ok" }] });
    return;
  }
  respond(message.id, null);
});
input.on("close", () => process.exit(0));

function respond(id, result) {
  process.stdout.write(`${JSON.stringify({ jsonrpc: "2.0", id, result })}\n`);
}

function writeLog(record) {
  if (!logPath) return;
  fs.appendFileSync(logPath, `${JSON.stringify(record)}\n`, { encoding: "utf8", mode: 0o600 });
}
