#!/usr/bin/env node

const marker = "browser_extension_rejected";
const mode = process.env.FAKE_MCP_STDERR_MODE;
const canary = process.env.FAKE_MCP_STDERR_CANARY ?? "";

process.stdin.resume();
process.stderr.write(`${canary} ignored diagnostic browser_extension_`);
setTimeout(() => {
  process.stderr.write(mode === "rejected" ? marker.slice("browser_extension_".length) : "rejecteX");
}, 5);
setTimeout(() => process.exit(2), 20);
