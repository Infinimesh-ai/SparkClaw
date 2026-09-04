import assert from "node:assert/strict";
import { mkdir, mkdtemp, readFile, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { spawn } from "node:child_process";

export const FAKE_CDP = "ws://127.0.0.1:9222/devtools/browser/test";
export const READY_EVIDENCE = {
  positive: { app_shell: true, compose_command: true, mail_navigation: true },
  negative: { credential_entry: false, account_chooser: false, sign_in_action: false },
};

const FAKE_AGENT_BROWSER = String.raw`#!/usr/bin/env node
import { appendFileSync, existsSync, readFileSync, writeFileSync } from "node:fs";

const args = process.argv.slice(2);
const stdin = readFileSync(0, "utf8");
const scenario = JSON.parse(process.env.FAKE_OUTLOOK_SCENARIO || "{}");
const statePath = process.env.FAKE_OUTLOOK_STATE;
const logPath = process.env.FAKE_OUTLOOK_LOG;
const state = existsSync(statePath) ? JSON.parse(readFileSync(statePath, "utf8")) : {
  created: [],
  opened: [],
  closed: [],
  closeAttempts: [],
  sessionCloses: [],
  bound: [],
  fields: {},
  newMailClicks: 0,
  sendClicks: 0,
  presses: [],
  currentUrl: "https://outlook.live.com/mail/",
};
const sessionIndex = args.indexOf("--session");
const session = sessionIndex >= 0 ? args[sessionIndex + 1] : null;
let commandIndex = 0;
while (commandIndex < args.length && args[commandIndex].startsWith("--")) {
  commandIndex += args[commandIndex] === "--json" ? 1 : 2;
}
const command = args.slice(commandIndex);
const agentEnvironment = Object.fromEntries(
  Object.entries(process.env).filter(([name]) => name.startsWith("AGENT_BROWSER_")),
);
appendFileSync(logPath, JSON.stringify({ args, command, session, stdin, env: agentEnvironment }) + "\n");

function save() {
  writeFileSync(statePath, JSON.stringify(state));
}

function finish(value, code = 0) {
  save();
  process.stdout.write(JSON.stringify(value) + "\n");
  process.exitCode = code;
}

function response(data) {
  return { success: true, data, error: null };
}

function batchEntry(item, result) {
  return { command: item, success: true, result, error: null };
}

function failedEntry(item) {
  return { command: item, success: false, error: "fixture_failure" };
}

function currentUrl() {
  return state.currentUrl;
}

function destinationUrl() {
  return scenario.probe && scenario.probe.url
    ? scenario.probe.url
    : "https://outlook.live.com/mail/";
}

function probeResult() {
  const defaults = {
    positive: { app_shell: true, compose_command: true, mail_navigation: true },
    negative: { credential_entry: false, account_chooser: false, sign_in_action: false },
    accountMarker: null,
  };
  const probe = { ...defaults, ...(scenario.probe || {}) };
  return {
    result: {
      contract_version: 1,
      url: currentUrl(),
      positive: probe.positive,
      negative: probe.negative,
      account_marker: probe.accountMarker,
    },
    origin: currentUrl(),
  };
}

function fieldForSelector(selector) {
  if (selector.includes('aria-label="To"')) return "recipient";
  if (selector.includes('aria-label="Add a subject"')) return "subject";
  if (selector.includes('aria-label="Message body"')) return "body";
  return null;
}

function actionResult(action) {
  const name = action[0];
  if (name === "open") {
    if (action[1] !== "https://outlook.live.com/mail/") return { ok: false };
    state.currentUrl = destinationUrl();
    state.opened.push({ session, url: action[1], finalUrl: state.currentUrl });
    return { ok: true, result: { url: state.currentUrl } };
  }
  if (name === "get" && action[1] === "url") {
    return { ok: true, result: { url: currentUrl() } };
  }
  if (name === "eval") {
    const source = Buffer.from(action[2], "base64").toString("utf8");
    if (source.includes("account_marker")) return { ok: true, result: probeResult() };
    if (source.includes("sent_evidence")) {
      if (scenario.sendOutcome === "error") return { ok: false };
      const sent = scenario.sendOutcome !== "unknown";
      return {
        ok: true,
        result: {
          result: {
            contract_version: 1,
            url: currentUrl(),
            sent_evidence: sent,
            compose_open: !sent,
          },
          origin: currentUrl(),
        },
      };
    }
    return {
      ok: true,
      result: {
        result: { contract_version: 1, url: currentUrl() },
        origin: currentUrl(),
      },
    };
  }

  if (name === "wait") return { ok: scenario.failAction !== "wait", result: { waited: "selector" } };
  if (name === "fill") {
    const field = fieldForSelector(action[1]);
    if (scenario.failAction === "fill-" + field) return { ok: false };
    state.fields[field] = action.slice(2).join(" ");
    return { ok: true, result: { filled: action[1] } };
  }
  if (name === "get" && action[1] === "value") {
    const field = fieldForSelector(action[2]);
    const overrideName = field + "ValueOverride";
    const value = Object.hasOwn(scenario, overrideName) ? scenario[overrideName] : state.fields[field];
    return { ok: true, result: { value, origin: currentUrl() } };
  }
  if (name === "get" && action[1] === "text") {
    const value = Object.hasOwn(scenario, "bodyTextOverride")
      ? scenario.bodyTextOverride
      : state.fields.body;
    return { ok: true, result: { text: value, origin: currentUrl() } };
  }
  if (name === "press") {
    state.presses.push(action[1]);
    return { ok: true, result: { pressed: action[1] } };
  }
  if (name === "focus") return { ok: true, result: { focused: action[1] } };
  if (name === "is" && action[1] === "enabled") {
    return { ok: true, result: { enabled: scenario.sendEnabled !== false, origin: currentUrl() } };
  }
  if (name === "click") {
    if (action[1].includes('aria-label="New mail"')) {
      state.newMailClicks += 1;
      return { ok: scenario.failAction !== "new-mail-click", result: { clicked: action[1] } };
    }
    if (action[1].includes('aria-label="Send"')) {
      state.sendClicks += 1;
      return { ok: scenario.sendClickFailure !== true, result: { clicked: action[1] } };
    }
  }
  return { ok: false };
}

if (scenario.hang === true) {
  setInterval(() => {}, 1000);
} else if (scenario.rawOutput !== undefined) {
  process.stdout.write(scenario.rawOutput);
} else if (command[0] === "tab" && command[1] === "new") {
  const labelIndex = command.indexOf("--label");
  const label = command[labelIndex + 1];
  const url = command[labelIndex + 2];
  state.currentUrl = "about:blank";
  state.created.push({ session, label, url });
  finish(response({ tabId: "t-owned", label, url, total: 2 }));
} else if (command[0] === "tab" && command[1] === "close") {
  const label = command[2];
  state.closeAttempts.push({ session, label });
  if (scenario.cleanupFailure === true) {
    finish({ success: false, data: null, error: "fixture_cleanup_failure" }, 1);
  } else {
    state.closed.push({ session, label });
    finish(response({ tabId: "t-owned", label, closed: true }));
  }
} else if (command[0] === "close") {
  state.sessionCloses.push(session);
  finish(response({ closed: true }));
} else if (command[0] === "batch") {
  const commands = JSON.parse(stdin);
  const label = commands[0][1];
  state.bound.push({ session, label, commands });
  const results = [batchEntry(commands[0], {
    tabId: "t-owned",
    label,
    url: currentUrl(),
    title: "Outlook",
  })];
  let failed = false;
  for (let actionIndex = 1; actionIndex < commands.length; actionIndex += 1) {
    const performed = actionResult(commands[actionIndex]);
    if (!performed.ok) {
      results.push(failedEntry(commands[actionIndex]));
      failed = true;
      break;
    }
    results.push(batchEntry(commands[actionIndex], performed.result));
  }
  finish(results, failed ? 1 : 0);
} else {
  finish({ success: false, data: null, error: "unexpected_fixture_command" }, 1);
}
`;

export async function createFixture(scenario = {}) {
  const directory = await mkdtemp(path.join(tmpdir(), "outlook-cli-test-"));
  const binDirectory = path.join(directory, "bin");
  const logPath = path.join(directory, "agent-browser.log");
  const statePath = path.join(directory, "state.json");
  await mkdir(binDirectory);
  await writeFile(path.join(binDirectory, "agent-browser"), FAKE_AGENT_BROWSER, {
    mode: 0o755,
  });
  return { directory, binDirectory, logPath, statePath, scenario };
}

export function runCli(script, input, fixture, extraEnv = {}) {
  return new Promise((resolve, reject) => {
    const child = spawn(process.execPath, [script], {
      env: {
        ...process.env,
        PATH: `${fixture.binDirectory}${path.delimiter}${process.env.PATH ?? ""}`,
        AGENT_BROWSER_CDP: FAKE_CDP,
        FAKE_OUTLOOK_LOG: fixture.logPath,
        FAKE_OUTLOOK_STATE: fixture.statePath,
        FAKE_OUTLOOK_SCENARIO: JSON.stringify(fixture.scenario),
        OUTLOOK_LOGIN_PROBE_TIMEOUT_MS: "500",
        OUTLOOK_SEND_TIMEOUT_MS: "500",
        ...extraEnv,
      },
      stdio: ["pipe", "pipe", "pipe"],
    });
    let stdout = "";
    let stderr = "";
    child.stdout.setEncoding("utf8");
    child.stderr.setEncoding("utf8");
    child.stdout.on("data", (chunk) => { stdout += chunk; });
    child.stderr.on("data", (chunk) => { stderr += chunk; });
    child.once("error", reject);
    child.once("close", (code) => resolve({ code, stdout, stderr }));
    child.stdin.end(typeof input === "string" ? input : `${JSON.stringify(input)}\n`);
  });
}

export async function readInvocations(fixture) {
  try {
    const content = await readFile(fixture.logPath, "utf8");
    return content.trim().split("\n").filter(Boolean).map(JSON.parse);
  } catch (error) {
    if (error.code === "ENOENT") return [];
    throw error;
  }
}

export async function readState(fixture) {
  return JSON.parse(await readFile(fixture.statePath, "utf8"));
}

export function assertErrorCode(result, code) {
  assert.equal(result.code, 1);
  assert.equal(result.stdout, "");
  assert.deepEqual(JSON.parse(result.stderr), {
    schema_version: 1,
    status: "error",
    provider: "outlook",
    code,
  });
}

export function assertOwnedTabDiscipline(invocations, state) {
  const prohibitedCommands = new Set(["auth", "cookies", "profiles", "state", "storage"]);
  assert.ok(state.created.length >= 1);
  assert.ok(state.created.every((item) => item.url === "about:blank"));
  assert.equal(state.opened.length, state.created.length);
  assert.ok(state.opened.every((item) => item.url === "https://outlook.live.com/mail/"));
  assert.deepEqual(state.closed, state.created.map(({ session, label }) => ({ session, label })));
  assert.deepEqual(state.sessionCloses, state.created.map(({ session }) => session));
  for (const bound of state.bound) {
    const owned = state.created.find((item) => item.session === bound.session);
    assert.ok(owned);
    assert.equal(bound.label, owned.label);
    assert.deepEqual(bound.commands[0], ["tab", owned.label]);
    assert.ok(bound.commands.every((command) => !prohibitedCommands.has(command[0])));
  }
  for (const invocation of invocations.filter((item) => item.command[0] === "tab")) {
    if (invocation.command[1] === "close") {
      assert.equal(invocation.command.length, 3);
      assert.match(invocation.command[2], /^outlook-owned-[a-f0-9]{24}$/);
    }
  }
  assert.ok(invocations.every((item) => !prohibitedCommands.has(item.command[0])));
  assert.equal(invocations.filter((item) => item.command[0] === "close").length, state.created.length);
}
