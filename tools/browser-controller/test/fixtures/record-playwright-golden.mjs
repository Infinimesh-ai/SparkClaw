#!/usr/bin/env node
// Re-records test/fixtures/playwright-golden.json from the pinned @playwright/mcp
// and @playwright/cli packages in node_modules. The Response serializer that
// renders tab lists, snapshots, and errors is shared by the extension transport
// and the local headless transport, so a local headless Chrome run captures the
// same wire format the Browser Bridge produces. Run after bumping the Playwright
// pins, then review the diff:
//
//   node test/fixtures/record-playwright-golden.mjs
//
// SPARKCLAW_GOLDEN_BROWSER selects the Playwright browser channel (default
// "chrome", the locally installed Google Chrome).

import { spawn, spawnSync } from "node:child_process";
import fs from "node:fs";
import http from "node:http";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const fixturesDir = path.dirname(fileURLToPath(import.meta.url));
const packageRoot = path.resolve(fixturesDir, "..", "..");
const goldenPath = path.join(fixturesDir, "playwright-golden.json");
const mcpEntry = path.join(packageRoot, "node_modules", "@playwright", "mcp", "cli.js");
const cliEntry = path.join(packageRoot, "node_modules", "@playwright", "cli", "playwright-cli.js");
const browserChannel = process.env.SPARKCLAW_GOLDEN_BROWSER || "chrome";
const fixturePort = 48765;
const fixtureURL = `http://127.0.0.1:${fixturePort}/fixture`;
const fixtureHTML = fs.readFileSync(path.join(fixturesDir, "adapter-live.html"), "utf8");
const workDir = fs.mkdtempSync(path.join(os.tmpdir(), "sparkclaw-playwright-golden-"));
const sessionName = "sparkclaw-golden";

const versions = {
  playwright_mcp: readVersion("@playwright/mcp"),
  playwright_cli: readVersion("@playwright/cli"),
  playwright_core: readVersion("playwright-core"),
};

const server = http.createServer((_, res) => {
  res.setHeader("content-type", "text/html; charset=utf-8");
  res.end(fixtureHTML);
});
await new Promise((resolve, reject) => {
  server.once("error", reject);
  server.listen(fixturePort, "127.0.0.1", resolve);
});

try {
  const golden = {
    recorded: {
      ...versions,
      browser_channel: browserChannel,
      fixture_url: fixtureURL,
      note: "Recorded by record-playwright-golden.mjs against a local headless browser; screenshot bytes are elided.",
    },
    mcp: await recordMCP(),
    cli: recordCLI(),
  };
  fs.writeFileSync(goldenPath, `${JSON.stringify(golden, null, 2)}\n`);
  console.log(`recorded ${path.relative(packageRoot, goldenPath)}`);
} finally {
  server.close();
  fs.rmSync(workDir, { recursive: true, force: true });
}

async function recordMCP() {
  const outputDir = path.join(workDir, "mcp-output");
  fs.mkdirSync(outputDir);
  const child = spawn(process.execPath, [
    mcpEntry,
    "--headless",
    "--isolated",
    "--browser",
    browserChannel,
    "--codegen",
    "none",
    "--image-responses",
    "allow",
    "--snapshot-mode",
    "none",
    "--output-dir",
    outputDir,
    "--timeout-action",
    "10000",
    "--timeout-navigation",
    "30000",
    "--timeout-settle",
    "500",
  ], { stdio: ["pipe", "pipe", "inherit"] });
  const pending = new Map();
  let buffer = "";
  let nextID = 0;
  child.stdout.setEncoding("utf8");
  child.stdout.on("data", (chunk) => {
    buffer += chunk;
    let newline;
    while ((newline = buffer.indexOf("\n")) >= 0) {
      const line = buffer.slice(0, newline);
      buffer = buffer.slice(newline + 1);
      if (!line.trim()) continue;
      const message = JSON.parse(line);
      pending.get(message.id)?.(message);
      pending.delete(message.id);
    }
  });
  const request = (method, params) => new Promise((resolve, reject) => {
    const id = ++nextID;
    pending.set(id, (message) => (message.error ? reject(new Error(JSON.stringify(message.error))) : resolve(message.result)));
    child.stdin.write(`${JSON.stringify({ jsonrpc: "2.0", id, method, params })}\n`);
  });
  const call = (name, args = {}, meta = { json: true }) =>
    request("tools/call", { name, arguments: { ...args, _meta: meta } });

  try {
    await request("initialize", {
      protocolVersion: "2025-06-18",
      capabilities: {},
      clientInfo: { name: "sparkclaw-golden-recorder", version: "0" },
    });
    child.stdin.write(`${JSON.stringify({ jsonrpc: "2.0", method: "notifications/initialized", params: {} })}\n`);
    const record = {};
    record.tools = (await request("tools/list", {})).tools.map((tool) => tool.name);
    record.tabs_list_initial = await call("browser_tabs", { action: "list" });
    record.snapshot_about_blank = await call("browser_snapshot");
    record.navigate = await call("browser_navigate", { url: fixtureURL });
    record.tabs_list_one = await call("browser_tabs", { action: "list" });
    record.snapshot = await call("browser_snapshot");
    record.snapshot_depth1 = await call("browser_snapshot", { depth: 1 });
    record.snapshot_text_mode = await call("browser_snapshot", {}, {});
    record.tabs_new = await call("browser_tabs", { action: "new" });
    record.tabs_new_url = await call("browser_tabs", { action: "new", url: fixtureURL });
    record.tabs_list_three = await call("browser_tabs", { action: "list" });
    record.tabs_select = await call("browser_tabs", { action: "select", index: 0 });
    record.evaluate = await call("browser_evaluate", {
      function: "() => ({ url: location.href, title: document.title, ready_state: document.readyState })",
    });
    record.type_focus_without_focus = await call("browser_type", { target: ":focus", text: "golden" });
    record.wait_for = await call("browser_wait_for", { time: 0.1 });
    record.screenshot = elideImages(normalizeOutputPaths(await call("browser_take_screenshot", { type: "png", scale: "css" })));
    record.error_unknown_ref = await call("browser_type", { target: "e999", text: "x" });
    record.tabs_close_index = await call("browser_tabs", { action: "close", index: 2 });
    record.tabs_close_current = await call("browser_tabs", { action: "close" });
    record.tabs_close_last = await call("browser_tabs", { action: "close" });
    return record;
  } finally {
    child.stdin.end();
    await new Promise((resolve) => {
      child.once("exit", resolve);
      setTimeout(() => child.kill("SIGKILL"), 5000).unref();
    });
  }
}

function recordCLI() {
  const cliWorkDir = path.join(workDir, "cli");
  fs.mkdirSync(cliWorkDir);
  const env = {
    ...process.env,
    NO_UPDATE_NOTIFIER: "1",
    NO_COLOR: "1",
    PLAYWRIGHT_MCP_CODEGEN: "none",
    PLAYWRIGHT_MCP_IMAGE_RESPONSES: "omit",
    PLAYWRIGHT_MCP_SNAPSHOT_MODE: "none",
    PLAYWRIGHT_MCP_TIMEOUT_ACTION: "10000",
    PLAYWRIGHT_MCP_TIMEOUT_NAVIGATION: "30000",
    PLAYWRIGHT_MCP_TIMEOUT_SETTLE: "500",
  };
  const run = (...args) => {
    const result = spawnSync(process.execPath, [cliEntry, ...args], { cwd: cliWorkDir, env, encoding: "utf8" });
    return {
      args,
      exit_code: result.status,
      stdout: normalizeCLIOutput(result.stdout),
      stderr: normalizeCLIOutput(result.stderr),
    };
  };
  const session = `-s=${sessionName}`;
  const record = {};
  record.tab_list_not_open_raw = run("--raw", session, "tab-list");
  record.tab_list_not_open_json = run("--json", session, "tab-list");
  record.open_json = redactPID(run("--json", session, "open", "--browser", browserChannel));
  record.tab_list_raw = run("--raw", session, "tab-list");
  record.tab_list_json = run("--json", session, "tab-list");
  record.tab_list_default = run(session, "tab-list");
  record.tab_new_raw = run("--raw", session, "tab-new");
  record.tab_new_url_raw = run("--raw", session, "tab-new", "data:text/html,<title>Second tab</title>second");
  record.tab_select_json = run("--json", session, "tab-select", "0");
  record.tab_close_missing_raw = run("--raw", session, "tab-close", "99");
  record.tab_close_missing_json = run("--json", session, "tab-close", "99");
  record.eval_raw = run("--raw", session, "eval", "() => location.href");
  record.eval_json = run("--json", session, "eval", "() => location.href");
  record.click_unknown_ref_json = run("--json", session, "click", "e999");
  record.tab_close_raw = run("--raw", session, "tab-close", "2");
  run("--raw", session, "tab-close", "1");
  record.tab_close_last_raw = run("--raw", session, "tab-close", "0");
  record.unknown_option_raw = run("--raw", session, "tab-list", "--bogus");
  record.too_many_arguments_raw = run("--raw", session, "tab-list", "extra");
  record.close_json = run("--json", session, "close");
  record.close_not_open_json = run("--json", session, "close");
  return record;
}

function normalizeOutputPaths(result) {
  return {
    ...result,
    content: result.content.map((item) => (item.type === "text"
      ? { ...item, text: item.text.replace(/\([^)]*\/page-[^)]*\.png\)/u, "(<output-dir>/page-<timestamp>.png)") }
      : item)),
  };
}

function elideImages(result) {
  return {
    ...result,
    content: result.content.map((item) =>
      (item.type === "image" ? { ...item, data: `<${Buffer.from(item.data, "base64").length} bytes elided>` } : item)),
  };
}

function redactPID(result) {
  return { ...result, stdout: result.stdout.replace(/"pid": [0-9]+/u, "\"pid\": 0") };
}

function normalizeCLIOutput(text) {
  return text.replace(/page-[0-9TZ-]+\.(yml|png)/gu, "page-<timestamp>.$1");
}

function readVersion(name) {
  return JSON.parse(fs.readFileSync(path.join(packageRoot, "node_modules", name, "package.json"), "utf8")).version;
}
