import { spawn } from "node:child_process";
import { randomBytes } from "node:crypto";
import { access } from "node:fs/promises";
import { constants } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

import {
  QQMAIL_URL,
  QQMailScriptError,
  ROOT,
  boundedTimeoutMS,
} from "./qqmail-browser.mjs";

const DEFAULT_AGENT_BROWSER = path.join(ROOT, "node_modules", ".bin", "agent-browser");
const SAFE_CONFIG = path.join(path.dirname(fileURLToPath(import.meta.url)), "agent-browser-host-cdp.json");
const MAX_BROWSER_OUTPUT_BYTES = 2 * 1024 * 1024;

export const QQMAIL_ALLOWED_ORIGINS = Object.freeze([
  "https://mail.qq.com",
  "https://wx.mail.qq.com",
]);

function validateEnvironment(environment) {
  const cdp = String(environment.AGENT_BROWSER_CDP ?? "").trim();
  if (!cdp) {
    throw new QQMailScriptError(
      "host_cdp_required",
      "AGENT_BROWSER_CDP must identify an existing host browser",
    );
  }

  const allowedAgentBrowser = new Set(["AGENT_BROWSER_CDP", "AGENT_BROWSER_SESSION"]);
  const forbiddenAgentBrowser = Object.keys(environment).filter(
    (name) => name.startsWith("AGENT_BROWSER_") && !allowedAgentBrowser.has(name),
  );
  const forbiddenQQMail = Object.keys(environment).filter((name) =>
    /^SPARKCLAW_QQMAIL_(?:PROFILE|STATE|RESTORE|AUTO_CONNECT|SESSION)/u.test(name),
  );
  if (forbiddenAgentBrowser.length > 0 || forbiddenQQMail.length > 0) {
    throw new QQMailScriptError(
      "forbidden_browser_environment",
      "Host-CDP QQ Mail scripts reject browser profile, state, restore, and launch settings",
    );
  }
  return cdp;
}

async function ensureExecutable(executable) {
  try {
    await access(executable, constants.X_OK);
    await access(SAFE_CONFIG, constants.R_OK);
  } catch {
    throw new QQMailScriptError(
      "agent_browser_unavailable",
      "agent-browser or its Host-CDP configuration is unavailable",
    );
  }
}

function taskToken() {
  return randomBytes(8).toString("hex");
}

function childEnvironment(options) {
  const environment = { ...process.env };
  for (const name of Object.keys(environment)) {
    if (name.startsWith("AGENT_BROWSER_")) delete environment[name];
  }
  environment.AGENT_BROWSER_CDP = options.cdp;
  environment.AGENT_BROWSER_IDLE_TIMEOUT_MS = String(options.timeoutMS + 10_000);
  environment.AGENT_BROWSER_NAMESPACE = options.namespace;
  environment.AGENT_BROWSER_SESSION = options.session;
  return environment;
}

async function runBatch(commands, options) {
  const payload = JSON.stringify(commands);
  return await new Promise((resolve, reject) => {
    const child = spawn(
      options.executable,
      ["--config", SAFE_CONFIG, "batch", "--json", "--bail"],
      {
        cwd: ROOT,
        env: childEnvironment(options),
        stdio: ["pipe", "pipe", "pipe"],
      },
    );
    let stdout = "";
    let stderrBytes = 0;
    let settled = false;

    const finish = (error, value) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      if (error) reject(error);
      else resolve(value);
    };
    const timer = setTimeout(() => {
      child.kill("SIGKILL");
      finish(new QQMailScriptError(`${options.phase}_timeout`, `${options.phase} timed out`));
    }, options.timeoutMS);

    child.on("error", () => {
      finish(
        new QQMailScriptError(
          `${options.phase}_start_failed`,
          `${options.phase} could not start agent-browser`,
        ),
      );
    });
    child.stdout.setEncoding("utf8");
    child.stdout.on("data", (chunk) => {
      stdout += chunk;
      if (Buffer.byteLength(stdout, "utf8") > MAX_BROWSER_OUTPUT_BYTES) {
        child.kill("SIGKILL");
        finish(
          new QQMailScriptError(
            `${options.phase}_output_too_large`,
            `${options.phase} returned too much output`,
          ),
        );
      }
    });
    child.stderr.on("data", (chunk) => {
      stderrBytes += chunk.length;
      if (stderrBytes > MAX_BROWSER_OUTPUT_BYTES) {
        child.kill("SIGKILL");
        finish(
          new QQMailScriptError(
            `${options.phase}_output_too_large`,
            `${options.phase} returned too much error output`,
          ),
        );
      }
    });
    child.on("close", (code) => {
      if (settled) return;
      if (code !== 0) {
        finish(new QQMailScriptError(`${options.phase}_failed`, `${options.phase} failed`));
        return;
      }
      try {
        const parsed = JSON.parse(stdout);
        if (
          !Array.isArray(parsed) ||
          parsed.length !== commands.length ||
          parsed.some(
            (entry) =>
              entry?.success !== true ||
              !entry.result ||
              typeof entry.result !== "object" ||
              Array.isArray(entry.result),
          )
        ) {
          throw new Error("unexpected batch result");
        }
        finish(null, parsed);
      } catch {
        finish(
          new QQMailScriptError(
            `${options.phase}_invalid_output`,
            `${options.phase} returned invalid JSON`,
          ),
        );
      }
    });
    child.stdin.on("error", () => {});
    child.stdin.end(payload);
  });
}

export function parseQQMailURL(value, invalidCode = "browser_output_invalid") {
  let parsed;
  try {
    parsed = new URL(String(value ?? ""));
  } catch {
    throw new QQMailScriptError(invalidCode, "agent-browser returned an invalid URL");
  }
  if (parsed.username || parsed.password || !QQMAIL_ALLOWED_ORIGINS.includes(parsed.origin)) {
    throw new QQMailScriptError(
      "provider_origin_mismatch",
      "the task tab is not on an allowed QQ Mail origin",
    );
  }
  return parsed;
}

function actionResults(results) {
  const actions = [];
  for (let index = 1; index < results.length; index += 2) {
    actions.push(results[index]);
  }
  return actions;
}

async function createTaskRuntime(operation, runtime) {
  const cdp = validateEnvironment(process.env);
  const executable = runtime.executable || process.env.SPARKCLAW_AGENT_BROWSER || DEFAULT_AGENT_BROWSER;
  const timeoutMS = boundedTimeoutMS(runtime.timeoutMS ?? process.env.SPARKCLAW_QQMAIL_TIMEOUT_MS);
  if (!runtime.runBatch) await ensureExecutable(executable);

  const token = taskToken();
  const operationCode = { probe: "p", read: "r", send: "s" }[operation];
  if (!operationCode) {
    throw new QQMailScriptError("invalid_operation", "unsupported QQ Mail browser operation");
  }
  const options = {
    cdp,
    executable,
    label: `qqmail-${operation}-${token}`,
    namespace: `scq-${token}`,
    session: `scq-${operationCode}-${token}`,
    timeoutMS,
  };
  const batch = runtime.runBatch || runBatch;

  return {
    label: options.label,
    async raw(commands, phase) {
      return await batch(commands, { ...options, phase });
    },
    async onTab(commands, phase) {
      const bound = [];
      for (const command of commands) {
        bound.push(["tab", options.label], command);
      }
      return actionResults(await batch(bound, { ...options, phase }));
    },
  };
}

export async function withQQMailTaskTab(operation, runtime, callback) {
  const task = await createTaskRuntime(operation, runtime);
  let creationAttempted = false;
  let operationError;
  let cleanupError;
  let value;

  try {
    creationAttempted = true;
    await task.raw(
      [["tab", "new", "--label", task.label, "about:blank"]],
      `${operation}_task_tab_open`,
    );
    const opened = await task.onTab(
      [["open", QQMAIL_URL], ["wait", "3000"], ["get", "url"]],
      `${operation}_task_tab_navigate`,
    );
    parseQQMailURL(opened[2]?.result?.url, `${operation}_browser_output_invalid`);
    value = await callback(task);
  } catch (error) {
    operationError = error;
  }

  if (creationAttempted) {
    try {
      await task.raw([["tab", "close", task.label]], `${operation}_task_tab_cleanup`);
    } catch (error) {
      cleanupError = error;
    }
    try {
      await task.raw([["close"]], `${operation}_session_cleanup`);
    } catch {
      // The bounded agent-browser idle timeout remains the cleanup fallback.
    }
  }

  if (operationError) throw operationError;
  if (cleanupError) {
    throw new QQMailScriptError(
      "task_tab_cleanup_failed",
      "the task-owned QQ Mail tab could not be closed",
    );
  }
  return value;
}
