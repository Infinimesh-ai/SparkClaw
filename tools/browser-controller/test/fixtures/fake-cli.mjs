#!/usr/bin/env node

import crypto from "node:crypto";
import fs from "node:fs/promises";
import path from "node:path";

const args = process.argv.slice(2);
const commandIndex = args.findIndex((value) => !value.startsWith("-"));
const command = args[commandIndex] ?? "";
const commandArgs = args.slice(commandIndex + 1);
const sessionName = args.find((value) => value.startsWith("-s="))?.slice(3) ?? "default";
const statePath = path.join(process.env.XDG_CACHE_HOME, "fake-cli-state.json");
const logPath = process.env.FAKE_CLI_LOG;

let state = await loadState();
state.commandCounts ??= {};
state.commandCounts[command] = (state.commandCounts[command] ?? 0) + 1;
await saveState();
if (process.env.FAKE_CLI_MUTATE_OWNER_ON === command && !state.ownerMutated) {
  const owner = state.tabs.find((tab) => !tab.task);
  if (owner) owner.title = `${owner.title}-changed`;
  state.ownerMutated = true;
  await saveState();
}

const secrets = await readSecrets();
await appendLog({
  event: "command",
  command,
  argv: args,
  session: sessionName,
  extension_token_present: Boolean(process.env.PLAYWRIGHT_MCP_EXTENSION_TOKEN),
  executable_path: process.env.PLAYWRIGHT_MCP_EXECUTABLE_PATH ?? "",
  user_data_dir: process.env.PLAYWRIGHT_MCP_USER_DATA_DIR ?? "",
  secret_names: Object.keys(secrets).sort(),
  inherited_forbidden_env_present: Boolean(
    process.env.PLAYWRIGHT_MCP_HEADLESS || process.env.PLAYWRIGHT_CLI_SESSION,
  ),
});

if (process.env.FAKE_CLI_HANG_COMMAND === command) {
  await new Promise(() => { setInterval(() => {}, 1_000); });
}
if (process.env.FAKE_CLI_STDERR_TOKEN_COMMAND === command) {
  process.stderr.write(process.env.PLAYWRIGHT_MCP_EXTENSION_TOKEN);
}
if (process.env.FAKE_CLI_PAGE_CLOSED_COMMAND === command) {
  process.stderr.write("Target page, context or browser has been closed");
  process.exit(2);
}
if (
  process.env.FAKE_CLI_CONTEXT_DESTROYED_COMMAND === command &&
  state.commandCounts[command] === Number(process.env.FAKE_CLI_CONTEXT_DESTROYED_ON_COUNT)
) {
  if (process.env.FAKE_CLI_CONTEXT_DESTROYED_URL) {
    currentTab().url = process.env.FAKE_CLI_CONTEXT_DESTROYED_URL;
    await saveState();
  }
  process.stdout.write("Execution context was destroyed");
  process.exit(2);
}
if (process.env.FAKE_CLI_FAIL_COMMAND === command) {
  const failAfter = Number(process.env.FAKE_CLI_FAIL_COMMAND_AFTER ?? "0");
  if (!Number.isSafeInteger(failAfter) || failAfter < 0 || state.commandCounts[command] > failAfter) {
    process.exit(2);
  }
}

switch (command) {
  case "attach":
    await saveState();
    writeJSON({
      session: sessionName,
      pid: Number(process.env.FAKE_CLI_DAEMON_PID ?? process.pid),
      endpoint: "chromium",
      result: process.env.PLAYWRIGHT_MCP_EXTENSION_TOKEN,
    });
    break;
  case "tab-list":
    writeText(renderTabs(state.tabs));
    break;
  case "tab-new":
    state.tabs.forEach((tab) => { tab.current = false; });
    state.tabs.push({ title: "", url: "about:blank", current: true, crashed: false, task: true });
    await saveState();
    break;
  case "tab-select": {
    const index = Number(commandArgs[0]);
    state.tabs.forEach((tab, tabIndex) => { tab.current = tabIndex === index; });
    await saveState();
    break;
  }
  case "tab-close": {
    const index = Number(commandArgs[0]);
    state.tabs.splice(index, 1);
    if (state.tabs.length > 0 && !state.tabs.some((tab) => tab.current)) {
      state.tabs[Math.min(index, state.tabs.length - 1)].current = true;
    }
    await saveState();
    break;
  }
  case "goto":
    currentTab().url = commandArgs[0];
    currentTab().title = "Provider";
    await saveState();
    break;
  case "fill": {
    const [selector, secretName] = commandArgs;
    const value = Object.hasOwn(secrets, secretName) ? secrets[secretName] : secretName;
    state.fields[selector] = value;
    await appendLog({
      event: "fill",
      selector,
      argument: secretName,
      value_sha256: crypto.createHash("sha256").update(value).digest("hex"),
      value_bytes: Buffer.byteLength(value, "utf8"),
    });
    await saveState();
    break;
  }
  case "click":
    if (
      process.env.FAKE_CLI_FAIL_AFTER_EFFECT === "1" &&
      commandArgs[0] === process.env.FAKE_CLI_EFFECT_SELECTOR
    ) {
      process.exit(2);
    }
    break;
  case "eval":
    if (
      process.env.FAKE_CLI_FAIL_AFTER_EFFECT === "1" &&
      commandArgs[0]?.includes("element.click") &&
      commandArgs[1] === process.env.FAKE_CLI_EFFECT_SELECTOR
    ) {
      process.exit(2);
    }
    writeJSON(evaluate(commandArgs[0] ?? ""));
    break;
  case "press":
    break;
  case "close":
    writeJSON({ session: sessionName, status: "closed" });
    break;
  default:
    process.exit(2);
}

async function loadState() {
  try {
    return JSON.parse(await fs.readFile(statePath, "utf8"));
  } catch (error) {
    if (error?.code !== "ENOENT") throw error;
    const tabs = process.env.FAKE_CLI_OWNER_TABS
      ? JSON.parse(process.env.FAKE_CLI_OWNER_TABS)
      : [{
        title: "Welcome",
        url: extensionConnectURL(),
        current: true,
        crashed: false,
        task: true,
      }];
    return { tabs, fields: {}, ownerMutated: false, commandCounts: {} };
  }
}

function extensionConnectURL() {
  const url = new URL(
    "chrome-extension://mmlmfjhmonkocbjadbfplnigmagldckm/connect.html",
  );
  url.searchParams.set(
    "mcpRelayUrl",
    "ws://127.0.0.1:45678/extension/12345678-1234-4123-8123-123456789abc",
  );
  url.searchParams.set("client", JSON.stringify({ name: "playwright-cli" }));
  url.searchParams.set("protocolVersion", "2");
  url.searchParams.set("token", process.env.PLAYWRIGHT_MCP_EXTENSION_TOKEN);
  return url.toString();
}

async function saveState() {
  await fs.mkdir(path.dirname(statePath), { recursive: true });
  await fs.writeFile(statePath, `${JSON.stringify(state)}\n`);
}

function currentTab() {
  const tab = state.tabs.find((candidate) => candidate.current);
  if (!tab) throw new Error("no current tab");
  return tab;
}

function evaluate(expression) {
  if (expression.includes("location.href")) return currentTab().url;
  if (expression.includes("document.activeElement === element")) return true;
  if (expression.includes("getBoundingClientRect")) return true;
  if (expression.includes("aria-disabled")) return true;
  if (expression.includes("document.querySelectorAll")) return 1;
  if (expression.includes("while (Date.now() < deadline)")) return true;
  const selectorMatch = /document\.querySelector\(("(?:\\.|[^"])*")\)/u.exec(expression);
  const selector = selectorMatch ? JSON.parse(selectorMatch[1]) : "";
  if (expression.includes("getAttribute")) return state.fields[selector] ?? "";
  if (expression.includes("?.value")) return state.fields[selector] ?? "";
  if (expression.includes("innerText")) return state.fields[selector] ?? "";
  return null;
}

async function readSecrets() {
  const secretsPath = process.env.PLAYWRIGHT_MCP_SECRETS_FILE;
  if (!secretsPath) return {};
  const result = {};
  for (const line of (await fs.readFile(secretsPath, "utf8")).split("\n")) {
    if (!line) continue;
    const separator = line.indexOf("=");
    result[line.slice(0, separator)] = JSON.parse(line.slice(separator + 1));
  }
  return result;
}

function renderTabs(tabs) {
  if (tabs.length === 0) return "No open tabs. Navigate to a URL to create one.";
  return tabs.map((tab, index) => {
    const current = tab.current ? " (current)" : "";
    const crashed = tab.crashed ? " [crashed]" : "";
    return `- ${index}:${current} [${tab.title}](${tab.url})${crashed}`;
  }).join("\n");
}

async function appendLog(record) {
  if (!logPath) return;
  await fs.appendFile(logPath, `${JSON.stringify(record)}\n`);
}

function writeJSON(value) {
  process.stdout.write(`${JSON.stringify(value)}\n`);
}

function writeText(value) {
  process.stdout.write(`${value}\n`);
}
