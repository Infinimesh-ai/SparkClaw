import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { chmod, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

export const GMAIL_REGISTRATION_URL = "https://mail.google.com/mail/u/0/";

export const LOGIN_SELECTORS = Object.freeze({
  accountControl: '[aria-label^="Google Account:"]',
  composeControl: '[role="button"][gh="cm"]',
  mainMenu: '[aria-label="Main menu"]',
  accountChoice: "[data-identifier]",
  useAnotherAccount: '[jsname="rwl3qc"]',
  identifierInput: "input#identifierId",
  identifierNext: "#identifierNext"
});

export const SEND_SELECTORS = Object.freeze({
  compose: '[role="button"][gh="cm"]',
  subject: 'input[name="subjectbox"]',
  recipientInput: [
    '[role="dialog"] textarea[name="to"]',
    '[role="dialog"] input[peoplekit-id]',
    '[role="dialog"] input[role="combobox"][aria-label^="To"]'
  ].join(", "),
  recipientChip: '[role="dialog"] [email]',
  body: '[role="dialog"] [role="textbox"][contenteditable="true"]',
  send: [
    '[role="dialog"] [role="button"][data-tooltip^="Send"]',
    '[role="dialog"] [role="button"][aria-label^="Send"]',
    '[role="dialog"] [role="button"][data-tooltip^="发送"]',
    '[role="dialog"] [role="button"][aria-label^="发送"]'
  ].join(", "),
  sentStatus: ".bAq"
});

export async function createFixture(t, scenario = {}) {
  const directory = await mkdtemp(join(tmpdir(), "gmail-cli-test-"));
  const executable = join(directory, "fake-agent-browser.mjs");
  const logPath = join(directory, "commands.jsonl");
  const statePath = join(directory, "state.json");
  await writeFile(executable, FAKE_AGENT_BROWSER, "utf8");
  await chmod(executable, 0o755);
  t.after(() => rm(directory, { recursive: true, force: true }));

  return {
    executable,
    logPath,
    statePath,
    scenario,
    async readLog() {
      return readJsonLines(logPath);
    },
    async readState() {
      try {
        return JSON.parse(await readFile(statePath, "utf8"));
      } catch (caught) {
        if (caught?.code === "ENOENT") {
          return {};
        }
        throw caught;
      }
    }
  };
}

export async function runCli(scriptPath, fixture, request, options = {}) {
  const environment = { ...process.env };
  for (const key of Object.keys(environment)) {
    if (key.startsWith("AGENT_BROWSER_")) {
      delete environment[key];
    }
  }
  if (options.omitCdp !== true) {
    environment.AGENT_BROWSER_CDP = "http://127.0.0.1:9222";
  }
  environment.GMAIL_CLI_AGENT_BROWSER_BIN = fixture.executable;
  environment.GMAIL_CLI_COMMAND_TIMEOUT_MS = String(options.commandTimeoutMs ?? 1000);
  environment.GMAIL_CLI_TEST_LOG = fixture.logPath;
  environment.GMAIL_CLI_TEST_STATE = fixture.statePath;
  environment.GMAIL_CLI_TEST_SCENARIO = JSON.stringify(fixture.scenario);
  Object.assign(environment, options.extraEnvironment ?? {});

  const child = spawn(process.execPath, [scriptPath], {
    env: environment,
    stdio: ["pipe", "pipe", "pipe"]
  });

  let stdout = "";
  let stderr = "";
  child.stdout.setEncoding("utf8");
  child.stderr.setEncoding("utf8");
  child.stdout.on("data", (chunk) => { stdout += chunk; });
  child.stderr.on("data", (chunk) => { stderr += chunk; });
  child.stdin.end(`${JSON.stringify(request)}\n`);

  const code = await new Promise((resolvePromise, rejectPromise) => {
    child.once("error", rejectPromise);
    child.once("close", resolvePromise);
  });
  return { code, stdout, stderr };
}

export function parseOnlyJson(output) {
  assert.match(output, /^\{[^\n]*\}\n$/);
  return JSON.parse(output);
}

export function assertError(stderr, expectedCode) {
  assert.deepEqual(parseOnlyJson(stderr), {
    schema_version: 1,
    status: "error",
    provider: "gmail",
    code: expectedCode
  });
}

export function assertOwnedTabLifecycle(log, expectedKind) {
  assert.ok(log.length >= 2);
  const sessions = new Set(log.map((entry) => entry.session));
  assert.equal(sessions.size, 1);
  const session = [...sessions][0];
  assert.match(session, new RegExp(`^gmail-${expectedKind}-[a-f0-9]{32}$`));

  const first = log[0];
  assert.deepEqual(first.command.slice(0, 3), ["tab", "new", "--label"]);
  assert.equal(first.command[4], "about:blank");
  const label = first.command[3];
  assert.match(label, new RegExp(`^gmail-${expectedKind}-[a-f0-9]{32}$`));

  const tabClose = log.at(-2);
  assert.deepEqual(tabClose.command, ["tab", "close", label]);
  const sessionClose = log.at(-1);
  assert.deepEqual(sessionClose.command, ["close"]);

  const middle = log.slice(1, -2);
  assert.equal(middle.length % 2, 0);
  for (let index = 0; index < middle.length; index += 2) {
    assert.deepEqual(middle[index].command, ["tab", label]);
    assert.notEqual(middle[index + 1].command[0], "tab");
  }
  assert.deepEqual(middle[1].command, ["open", GMAIL_REGISTRATION_URL]);
  assert.ok(middle.some((entry) =>
    entry.command[0] === "wait" && entry.command[1] === "3000"
  ));
  assert.ok(middle.some((entry) =>
    entry.command[0] === "get" && entry.command[1] === "url"
  ));

  for (const entry of log) {
    assert.equal(entry.hasCdp, true);
    assert.deepEqual(entry.agentBrowserEnvironmentKeys, [
      "AGENT_BROWSER_CDP",
      "AGENT_BROWSER_IDLE_TIMEOUT_MS",
      "AGENT_BROWSER_JSON"
    ]);
    assert.equal(entry.configContents, "{}\n");
    assert.ok(entry.arguments.includes("--json"));
    assert.ok(entry.arguments.includes("--config"));
    assert.ok(entry.arguments.includes("--session"));
    assert.equal(entry.arguments.includes("--profile"), false);
    assert.equal(entry.arguments.includes("--state"), false);
    assert.equal(entry.arguments.includes("--restore"), false);
    assert.equal(entry.arguments.includes("--auto-connect"), false);
    assert.equal(["read", "snapshot", "eval", "cookies", "state", "profiles"]
      .includes(entry.command[0]), false);
    const commandText = entry.command.join(" ");
    for (const forbidden of [
      "tr.zA",
      "tr.zE",
      ".a3s",
      ".aQH",
      ".aZo",
      "download_url",
      "attachment"
    ]) {
      assert.equal(commandText.includes(forbidden), false);
    }
  }

  assert.equal(log.filter((entry) => entry.command[0] === "close").length, 1);

  return { session, label };
}

async function readJsonLines(path) {
  try {
    const text = await readFile(path, "utf8");
    return text.trim() === ""
      ? []
      : text.trim().split("\n").map((line) => JSON.parse(line));
  } catch (caught) {
    if (caught?.code === "ENOENT") {
      return [];
    }
    throw caught;
  }
}

const FAKE_AGENT_BROWSER = `#!/usr/bin/env node
import { appendFileSync, readFileSync, writeFileSync } from "node:fs";

const scenario = JSON.parse(process.env.GMAIL_CLI_TEST_SCENARIO || "{}");
const args = process.argv.slice(2);
let cursor = 0;
let session = "";
let configPath = "";
while (cursor < args.length) {
  if (args[cursor] === "--session") {
    session = args[cursor + 1] || "";
    cursor += 2;
  } else if (args[cursor] === "--config") {
    configPath = args[cursor + 1] || "";
    cursor += 2;
  } else if (args[cursor] === "--json") {
    cursor += 1;
  } else {
    break;
  }
}
const command = args.slice(cursor);
const state = readState();
state.callCount = (state.callCount || 0) + 1;
state.nextTabId = state.nextTabId || 90;
state.tabs = state.tabs || {};
state.sendClicks = state.sendClicks || 0;
state.sessionCloses = state.sessionCloses || 0;

let configContents = null;
try { configContents = readFileSync(configPath, "utf8"); } catch {}
appendFileSync(process.env.GMAIL_CLI_TEST_LOG, JSON.stringify({
  arguments: args,
  command,
  session,
  hasCdp: Boolean(process.env.AGENT_BROWSER_CDP),
  agentBrowserEnvironmentKeys: Object.keys(process.env)
    .filter((key) => key.startsWith("AGENT_BROWSER_"))
    .sort(),
  configContents
}) + "\\n");

if (scenario.timeoutAt === state.callCount) {
  writeState();
  setTimeout(() => {}, 60_000);
} else if (scenario.invalidOutputAt === state.callCount) {
  writeState();
  process.stdout.write("not-json\\n");
} else {
  handleCommand();
}

function handleCommand() {
  const action = command[0];
  if (action === "close") {
    state.sessionCloses += 1;
    writeState();
    respond({ closed: true });
    return;
  }

  if (action === "tab" && command[1] === "new") {
    const labelIndex = command.indexOf("--label");
    const label = command[labelIndex + 1];
    const requestedUrl = command[labelIndex + 2];
    const tabId = "t" + state.nextTabId++;
    state.tabs[label] = {
      tabId,
      url: "about:blank"
    };
    state.activeLabel = label;
    writeState();
    respond({ tabId, label, url: requestedUrl, total: 2 });
    return;
  }

  if (action === "tab" && command[1] === "close") {
    const label = command[2];
    const tab = state.tabs[label];
    if (!tab) return fail();
    delete state.tabs[label];
    writeState();
    respond({ tabId: tab.tabId, label, closed: true });
    return;
  }

  if (action === "tab") {
    const label = command[1];
    const tab = state.tabs[label];
    if (!tab) return fail();
    state.activeLabel = label;
    writeState();
    respond({ tabId: tab.tabId, label, url: tab.url, title: "Gmail" });
    return;
  }

  const tab = state.tabs[state.activeLabel];
  if (!tab) return fail();
  const pageUrl = tab.url;

  if (action === "open") {
    if (command[1] !== ${JSON.stringify(GMAIL_REGISTRATION_URL)}) return fail();
    tab.url = scenario.pageUrl || "https://mail.google.com/mail/u/0/#inbox";
    writeState();
    respond({ url: tab.url });
    return;
  }

  if (action === "wait") {
    if (/^\\d+$/.test(command[1] || "")) {
      writeState();
      respond({ waited: "time", ms: Number(command[1]) });
      return;
    }
    if (command[1] === "--load") {
      writeState();
      respond({ waited: "load", state: command[2] });
      return;
    }
    const selector = command[1];
    if (selector === ${JSON.stringify(SEND_SELECTORS.sentStatus)} && scenario.sendOutcome === "unknown") {
      writeState();
      fail();
      return;
    }
    if (count(selector) < 1) {
      writeState();
      fail();
      return;
    }
    writeState();
    respond({ waited: "selector", selector });
    return;
  }

  if (action === "get" && command[1] === "url") {
    writeState();
    respond({ url: pageUrl });
    return;
  }

  if (action === "get" && command[1] === "count") {
    writeState();
    respond({ count: count(command[2]), selector: command[2] });
    return;
  }

  if (action === "get" && command[1] === "attr") {
    const selector = command[2];
    let value = "";
    if (selector === ${JSON.stringify(LOGIN_SELECTORS.accountControl)}) {
      value = scenario.accountLabel || "Google Account: Example (user@example.com)";
    } else if (selector === ${JSON.stringify(SEND_SELECTORS.recipientChip)}) {
      value = state.recipientChip || "";
    }
    writeState();
    respond({ value, origin: pageUrl });
    return;
  }

  if (action === "get" && command[1] === "value") {
    const value = command[2] === ${JSON.stringify(SEND_SELECTORS.subject)}
      ? (state.subject || "")
      : "";
    writeState();
    respond({ value, origin: pageUrl });
    return;
  }

  if (action === "get" && command[1] === "text") {
    let text = "";
    if (command[2] === ${JSON.stringify(SEND_SELECTORS.body)}) {
      text = Object.hasOwn(scenario, "bodyReadback")
        ? scenario.bodyReadback
        : (state.body || "");
    } else if (command[2] === ${JSON.stringify(SEND_SELECTORS.sentStatus)}) {
      text = state.sentStatus || "";
    }
    writeState();
    respond({ text, origin: pageUrl });
    return;
  }

  if (action === "click") {
    const selector = command[1];
    if (selector === ${JSON.stringify(SEND_SELECTORS.compose)}) {
      state.composeOpen = true;
    } else if (selector === ${JSON.stringify(SEND_SELECTORS.send)}) {
      state.sendClicks += 1;
      if (scenario.sendOutcome === "success") {
        state.composeOpen = false;
        state.sentStatus = "Message sent";
      }
    }
    writeState();
    respond({ clicked: selector });
    return;
  }

  if (action === "fill") {
    const selector = command[1];
    const value = command[2] || "";
    if (selector === ${JSON.stringify(SEND_SELECTORS.recipientInput)}) state.recipientInput = value;
    if (selector === ${JSON.stringify(SEND_SELECTORS.subject)}) state.subject = value;
    if (selector === ${JSON.stringify(SEND_SELECTORS.body)}) state.body = value;
    writeState();
    respond({ filled: selector });
    return;
  }

  if (action === "focus") {
    writeState();
    respond({ focused: command[1] });
    return;
  }

  if (action === "press") {
    if (command[1] === "Enter") {
      state.recipientChip = state.recipientInput || "";
    }
    writeState();
    respond({ pressed: command[1] });
    return;
  }

  writeState();
  fail();
}

function count(selector) {
  if (scenario.loginCounts && Object.hasOwn(scenario.loginCounts, selector)) {
    return scenario.loginCounts[selector];
  }
  if (selector === ${JSON.stringify(SEND_SELECTORS.subject)} ||
      selector === ${JSON.stringify(SEND_SELECTORS.recipientInput)} ||
      selector === ${JSON.stringify(SEND_SELECTORS.body)} ||
      selector === ${JSON.stringify(SEND_SELECTORS.send)}) {
    return state.composeOpen ? 1 : 0;
  }
  if (selector === ${JSON.stringify(SEND_SELECTORS.recipientChip)}) {
    return state.recipientChip ? 1 : 0;
  }
  if (selector === ${JSON.stringify(SEND_SELECTORS.sentStatus)}) {
    return state.sentStatus ? 1 : 0;
  }
  return 0;
}

function readState() {
  try { return JSON.parse(readFileSync(process.env.GMAIL_CLI_TEST_STATE, "utf8")); }
  catch { return {}; }
}

function writeState() {
  writeFileSync(process.env.GMAIL_CLI_TEST_STATE, JSON.stringify(state));
}

function respond(data) {
  process.stdout.write(JSON.stringify({ success: true, data }) + "\\n");
}

function fail() {
  process.stdout.write(JSON.stringify({ success: false, error: "fake failure" }) + "\\n");
  process.exitCode = 1;
}
`;
