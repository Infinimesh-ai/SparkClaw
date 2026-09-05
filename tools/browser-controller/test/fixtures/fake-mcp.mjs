#!/usr/bin/env node

import fs from "node:fs";
import readline from "node:readline";

const logPath = process.env.FAKE_MCP_LOG;
const calls = [];
const tools = [
  "browser_click",
  "browser_evaluate",
  "browser_navigate",
  "browser_select_option",
  "browser_snapshot",
  "browser_tabs",
  "browser_take_screenshot",
  "browser_type",
  "browser_wait_for",
];
const tabs = [
  {
    title: "Owner page",
    url: process.env.FAKE_MCP_INITIAL_URL || "https://owner.invalid/",
    current: true,
    crashed: false,
  },
];

writeLog({
  event: "started",
  argv: process.argv.slice(2),
  extension_token_present: Boolean(process.env.PLAYWRIGHT_MCP_EXTENSION_TOKEN),
  debug_namespace: process.env.DEBUG ?? "",
  initial_url: tabs[0].url,
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
  if (message.method === "tools/list") {
    respond(message.id, { tools: tools.map((name) => ({ name, inputSchema: { type: "object" } })) });
    return;
  }
  if (message.method === "tools/call") {
    calls.push(message.params);
    writeLog({ event: "tool", name: message.params.name, arguments: message.params.arguments });
    respond(message.id, callTool(message.params.name, message.params.arguments));
    return;
  }
  respond(message.id, null);
});
input.on("close", () => process.exit(0));

function respond(id, result) {
  process.stdout.write(`${JSON.stringify({ jsonrpc: "2.0", id, result })}\n`);
}

function callTool(name, args) {
  switch (name) {
    case "browser_tabs":
      return callTabs(args);
    case "browser_navigate": {
      const tab = currentTab();
      tab.url = args.url;
      tab.title = "Navigated page";
      return jsonResult({ page: pageMarkdown(tab), snapshot: snapshot() });
    }
    case "browser_evaluate": {
      const tab = currentTab();
      const value = args.function === "() => \"sparkclaw-browser-bridge-handoff-v1\"" ?
        "sparkclaw-browser-bridge-handoff-v1" : args.target && args.function.includes("element.click") ? true : args.function.includes("outerHTML") ? {
        url: tab.url,
        title: tab.title,
        ready_state: "complete",
        lang: "en",
        text: "Rendered task page text",
        html: "<html lang=\"en\"><body>Rendered task page text</body></html>",
        scroll_height: 900,
      } : {
        url: tab.url,
        title: tab.title,
        ready_state: "complete",
      };
      return jsonResult({ result: JSON.stringify(value, null, 2), page: pageMarkdown(tab) });
    }
    case "browser_snapshot":
      return jsonResult({ page: pageMarkdown(currentTab()), snapshot: snapshot() });
    case "browser_click":
    case "browser_type":
    case "browser_select_option":
    case "browser_wait_for":
      return jsonResult({ page: pageMarkdown(currentTab()), snapshot: snapshot() });
    case "browser_take_screenshot":
      return jsonResult(
        { page: pageMarkdown(currentTab()), result: "screenshot captured" },
        [{ type: "image", mimeType: `image/${args.type || "png"}`, data: "c2NyZWVuc2hvdA==" }],
      );
    default:
      return { content: [{ type: "text", text: JSON.stringify({ isError: true, error: "unknown tool" }) }], isError: true };
  }
}

function callTabs(args) {
  switch (args.action) {
    case "list":
      break;
    case "new":
      for (const tab of tabs) tab.current = false;
      tabs.push({ title: "", url: args.url || "about:blank", current: true, crashed: false });
      break;
    case "close": {
      const index = args.index ?? tabs.findIndex((tab) => tab.current);
      tabs.splice(index, 1);
      if (tabs.length > 0 && !tabs.some((tab) => tab.current)) tabs[Math.min(index, tabs.length - 1)].current = true;
      break;
    }
    case "select":
      for (const [index, tab] of tabs.entries()) tab.current = index === args.index;
      break;
    default:
      return { content: [{ type: "text", text: JSON.stringify({ isError: true, error: "bad tab action" }) }], isError: true };
  }
  return jsonResult({ result: tabsMarkdown(), page: tabs.length ? pageMarkdown(currentTab()) : undefined });
}

function jsonResult(payload, extra = []) {
  return { content: [{ type: "text", text: JSON.stringify(payload) }, ...extra] };
}

function tabsMarkdown() {
  if (tabs.length === 0) return "No open tabs. Navigate to a URL to create one.";
  return tabs.map((tab, index) => `- ${index}:${tab.current ? " (current)" : ""} [${tab.title}](${tab.url})${tab.crashed ? " [crashed]" : ""}`).join("\n");
}

function pageMarkdown(tab) {
  return [`- Page URL: ${tab.url}`, ...(tab.title ? [`- Page Title: ${tab.title}`] : [])].join("\n");
}

function currentTab() {
  return tabs.find((tab) => tab.current) || tabs[0];
}

function snapshot() {
  return [
    { role: "button", name: "Submit", ref: "e7" },
    { role: "textbox", name: "Email", ref: "e8" },
  ];
}

function writeLog(record) {
  if (!logPath) return;
  fs.appendFileSync(logPath, `${JSON.stringify(record)}\n`, { encoding: "utf8", mode: 0o600 });
}
