import { createHash, randomBytes } from "node:crypto";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { spawn } from "node:child_process";

export const OUTLOOK_REGISTRATION_URL = "https://outlook.live.com/mail/";
export const OUTLOOK_ORIGINS = new Set([
  "https://outlook.live.com",
  "https://outlook.office.com",
  "https://outlook.office365.com",
]);
export const MICROSOFT_LOGIN_ORIGINS = new Set([
  "https://login.live.com",
  "https://login.microsoftonline.com",
]);
const MICROSOFT_OUTLOOK_LANDING_ORIGIN = "https://www.microsoft.com";
const MICROSOFT_OUTLOOK_LANDING_PATH =
  /^\/[a-z]{2}-[a-z]{2}\/microsoft-365\/outlook\/email-and-calendar-software-microsoft-outlook\/?$/;

const MAX_STDIN_BYTES = 256 * 1_024;
const MAX_AGENT_STDOUT_BYTES = 512 * 1_024;
const MAX_AGENT_STDERR_BYTES = 32 * 1_024;
const DEFAULT_TIMEOUT_MS = 15_000;
const FORBIDDEN_STARTUP_ENV = new Set([
  "AGENT_BROWSER_ARGS",
  "AGENT_BROWSER_AUTO_CONNECT",
  "AGENT_BROWSER_CONFIG",
  "AGENT_BROWSER_ENABLE",
  "AGENT_BROWSER_EXECUTABLE_PATH",
  "AGENT_BROWSER_EXTENSIONS",
  "AGENT_BROWSER_HEADED",
  "AGENT_BROWSER_INIT_SCRIPTS",
  "AGENT_BROWSER_PROFILE",
  "AGENT_BROWSER_PROVIDER",
  "AGENT_BROWSER_RESTORE",
  "AGENT_BROWSER_SESSION_NAME",
  "AGENT_BROWSER_STATE",
  "AGENT_BROWSER_STORAGE_STATE",
]);

const OUTLOOK_ORIGIN_GUARD = `(() => {
  const allowedOrigins = ${JSON.stringify([...OUTLOOK_ORIGINS])};
  if (!allowedOrigins.includes(window.location.origin)) {
    throw new Error("outlook_origin_not_allowed");
  }
  return { contract_version: 1, url: window.location.href };
})()`;

export const PROBE_EXPRESSION = String.raw`(async () => {
  const isVisible = (element) => {
    if (!element || !element.isConnected) return false;
    const style = window.getComputedStyle(element);
    const rect = element.getBoundingClientRect();
    return rect.width > 0 && rect.height > 0 &&
      style.display !== "none" && style.visibility !== "hidden" &&
      Number.parseFloat(style.opacity || "1") > 0;
  };
  const anyVisible = (selector) =>
    Array.from(document.querySelectorAll(selector)).some(isVisible);
  const collect = () => {
    const titleBar = document.querySelector("#OwaTitleBar");
    const appLauncher = document.querySelector(
      "#OwaTitleBar button#owaAppLauncherBtn_container",
    );
    const accountControl = Array.from(document.querySelectorAll([
      "#OwaTitleBar button#O365_MainLink_MePhoto",
      "#OwaTitleBar button#O365_MeFlexPane_ButtonID",
      '#OwaTitleBar button[data-testid="mectrl_headerPicture"]',
      '#OwaTitleBar button[aria-label^="Account manager for "]',
      '#OwaTitleBar button[aria-label^="Account manager"]',
      '#OwaTitleBar button[aria-label^="\u5e10\u6237\u7ba1\u7406"]',
      '#OwaTitleBar button[aria-label^="\u8d26\u6237\u7ba1\u7406"]',
    ].join(", "))).find(isVisible) || null;
    return {
      contract_version: 1,
      url: window.location.href,
      positive: {
        app_shell: isVisible(titleBar) && isVisible(appLauncher),
        compose_command: anyVisible([
          'button[aria-label="New mail"]',
          'button[aria-label="New message"]',
          'button[aria-label="\u65b0\u90ae\u4ef6"]',
          'button[aria-label="\u65b0\u5efa\u90ae\u4ef6"]',
        ].join(", ")),
        mail_navigation: anyVisible([
          '[role="navigation"] button[aria-label="Mail"]',
          '[role="navigation"] button[aria-label="\u90ae\u4ef6"]',
        ].join(", ")),
      },
      negative: {
        credential_entry: anyVisible([
          'input[name="loginfmt"]',
          "input#i0116",
          'input[name="passwd"]',
          "input#i0118",
        ].join(", ")),
        account_chooser: anyVisible([
          "#tilesHolder",
          '[data-testid="tile-list"]',
          '[role="listbox"][aria-label="Pick an account"]',
          '[role="listbox"][aria-label="\u9009\u62e9\u5e10\u6237"]',
          '[role="listbox"][aria-label="\u9009\u62e9\u8d26\u6237"]',
        ].join(", ")),
        sign_in_action: anyVisible([
          "#idSIButton9",
          'button[data-testid="primaryButton"]',
        ].join(", ")),
      },
      account_marker: accountControl ? {
        source: "account_control",
        label: accountControl.getAttribute("aria-label") ||
          accountControl.getAttribute("title") || "",
      } : null,
    };
  };

  const deadline = Date.now() + 8000;
  let evidence = collect();
  while (
    Date.now() < deadline &&
    !Object.values(evidence.positive).some(Boolean) &&
    !Object.values(evidence.negative).some(Boolean)
  ) {
    await new Promise((resolve) => setTimeout(resolve, 100));
    evidence = collect();
  }
  return evidence;
})()`;

export class OutlookCliError extends Error {
  constructor(code) {
    super(code);
    this.name = "OutlookCliError";
    this.code = code;
  }
}

export async function readJsonStdin() {
  let body = "";
  let byteLength = 0;
  process.stdin.setEncoding("utf8");
  for await (const chunk of process.stdin) {
    byteLength += Buffer.byteLength(chunk);
    if (byteLength > MAX_STDIN_BYTES) throw new OutlookCliError("invalid_request");
    body += chunk;
  }
  try {
    return JSON.parse(body);
  } catch {
    throw new OutlookCliError("invalid_request");
  }
}

export function validateInvocationId(value) {
  if (typeof value !== "string" || !/^[\x21-\x7e]{1,128}$/.test(value)) {
    throw new OutlookCliError("invalid_request");
  }
  return value;
}

export function recipientDigest(value) {
  return `sha256:${createHash("sha256").update(value, "utf8").digest("hex")}`;
}

export function readCommandTimeout(environmentName) {
  const raw = process.env[environmentName];
  if (raw === undefined) return DEFAULT_TIMEOUT_MS;
  if (!/^\d+$/.test(raw)) throw new OutlookCliError("browser_configuration_invalid");
  const timeout = Number(raw);
  if (timeout < 25 || timeout > 60_000) {
    throw new OutlookCliError("browser_configuration_invalid");
  }
  return timeout;
}

export function validateHostCdpEnvironment() {
  for (const name of Object.keys(process.env)) {
    if (
      FORBIDDEN_STARTUP_ENV.has(name) ||
      name.startsWith("AGENT_BROWSER_RESTORE_") ||
      name === "AGENT_BROWSER_AUTOSAVE_INTERVAL_MS"
    ) {
      throw new OutlookCliError("browser_configuration_invalid");
    }
  }

  const cdp = process.env.AGENT_BROWSER_CDP;
  if (typeof cdp !== "string" || cdp.length === 0 || cdp.length > 2_048) {
    throw new OutlookCliError("browser_configuration_invalid");
  }
  if (/^[1-9]\d{0,4}$/.test(cdp)) {
    if (Number(cdp) <= 65_535) return cdp;
    throw new OutlookCliError("browser_configuration_invalid");
  }
  let parsed;
  try {
    parsed = new URL(cdp);
  } catch {
    throw new OutlookCliError("browser_configuration_invalid");
  }
  if (
    !new Set(["http:", "https:", "ws:", "wss:"]).has(parsed.protocol) ||
    parsed.hostname === "" || parsed.username !== "" || parsed.password !== ""
  ) {
    throw new OutlookCliError("browser_configuration_invalid");
  }
  return cdp;
}

export async function withOwnedOutlookTab(options, operation) {
  const cdp = validateHostCdpEnvironment();
  const timeoutMs = options.timeoutMs;
  const nonce = randomBytes(12).toString("hex");
  const session = `outlook-${options.operation}-${nonce}`;
  const label = `outlook-owned-${nonce}`;
  let configDirectory;
  try {
    configDirectory = await mkdtemp(path.join(tmpdir(), "outlook-host-cdp-"));
    await writeFile(path.join(configDirectory, "agent-browser.json"), "{}\n", {
      mode: 0o600,
    });
  } catch {
    throw new OutlookCliError("host_setup_failed");
  }

  const runner = new AgentBrowserRunner({
    cdp,
    session,
    configPath: path.join(configDirectory, "agent-browser.json"),
    timeoutMs,
  });
  let tabMayExist = false;
  let tabCreated = false;
  let result;
  let operationError;
  let cleanupError;

  try {
    tabMayExist = true;
    const created = await runner.runSingle([
      "tab",
      "new",
      "--label",
      label,
      "about:blank",
    ]);
    if (
      !isRecord(created) || created.label !== label ||
      typeof created.tabId !== "string" || created.url !== "about:blank"
    ) {
      throw new OutlookCliError("agent_browser_invalid_output");
    }
    tabCreated = true;
    const tab = new OwnedOutlookTab(runner, label);
    await tab.navigate();
    result = await operation(tab);
  } catch (error) {
    operationError = asOutlookError(error);
  } finally {
    if (tabMayExist) {
      try {
        const closed = await runner.runSingle(["tab", "close", label]);
        if (!isRecord(closed) || closed.label !== label || closed.closed !== true) {
          throw new OutlookCliError("agent_browser_invalid_output");
        }
      } catch (error) {
        cleanupError = new OutlookCliError("tab_cleanup_failed");
      }
    }
    try {
      await runner.closeSession();
    } catch {
      // The bounded agent-browser idle timeout remains the cleanup fallback.
    }
    await rm(configDirectory, { recursive: true, force: true }).catch(() => {});
  }

  if (cleanupError && tabCreated) throw cleanupError;
  if (operationError) throw operationError;
  if (cleanupError) throw cleanupError;
  return result;
}

export function parseProbeEvidence(evalData) {
  if (!isRecord(evalData)) throw new OutlookCliError("agent_browser_invalid_output");
  const { result, origin } = evalData;
  if (
    !isRecord(result) || result.contract_version !== 1 ||
    typeof result.url !== "string" || typeof origin !== "string" ||
    result.url !== origin ||
    !hasBooleanKeys(result.positive, [
      "app_shell",
      "compose_command",
      "mail_navigation",
    ]) ||
    !hasBooleanKeys(result.negative, [
      "account_chooser",
      "credential_entry",
      "sign_in_action",
    ]) ||
    !isAccountMarker(result.account_marker)
  ) {
    throw new OutlookCliError("agent_browser_invalid_output");
  }
  return result;
}

export function classifyProbeEvidence(evidence) {
  const url = parseHttpsUrl(evidence.url, "agent_browser_invalid_output");
  const isOutlookOrigin = OUTLOOK_ORIGINS.has(url.origin);
  const isLoginOrigin = MICROSOFT_LOGIN_ORIGINS.has(url.origin);
  if (!isOutlookOrigin && !isLoginOrigin) {
    throw new OutlookCliError("outlook_origin_not_allowed");
  }

  const positiveCount = Object.values(evidence.positive).filter(Boolean).length;
  const negativeCount = Object.values(evidence.negative).filter(Boolean).length;
  if (positiveCount > 0 && negativeCount > 0) {
    throw new OutlookCliError("outlook_evidence_conflict");
  }
  if (negativeCount > 0) throw new OutlookCliError("email_login_required");
  if (isLoginOrigin) throw new OutlookCliError("outlook_page_contract_changed");
  if (
    !evidence.positive.app_shell ||
    (!evidence.positive.compose_command && !evidence.positive.mail_navigation)
  ) {
    throw new OutlookCliError("outlook_page_contract_changed");
  }

  const result = {
    schema_version: 1,
    status: "ready",
    provider: "outlook",
  };
  const accountHint = deriveMaskedAccountHint(evidence.account_marker);
  if (accountHint !== null) result.account_hint = accountHint;
  return result;
}

export function hasExactKeys(value, keys) {
  if (!isRecord(value)) return false;
  const actual = Object.keys(value).sort();
  const expected = [...keys].sort();
  return actual.length === expected.length &&
    actual.every((key, index) => key === expected[index]);
}

export function writeSuccess(value) {
  process.stdout.write(`${JSON.stringify(value)}\n`);
}

export function writeFailure(error, fallbackCode = "outlook_internal_error") {
  const code = error instanceof OutlookCliError ? error.code : fallbackCode;
  process.stderr.write(`${JSON.stringify({
    schema_version: 1,
    status: "error",
    provider: "outlook",
    code,
  })}\n`);
  process.exitCode = 1;
}

class OwnedOutlookTab {
  constructor(runner, label) {
    this.runner = runner;
    this.label = label;
  }

  async navigate() {
    const commands = [
      ["tab", this.label],
      ["open", OUTLOOK_REGISTRATION_URL],
      ["wait", "3000"],
      ["get", "url"],
    ];
    const results = await this.runner.runBatch(commands);
    validateBoundSwitch(results[0], this.label, "about:blank");
    if (!isRecord(results[3]) || typeof results[3].url !== "string") {
      throw new OutlookCliError("agent_browser_invalid_output");
    }
    const final = parseHttpsUrl(results[3].url, "agent_browser_invalid_output");
    if (isMicrosoftOutlookLanding(final)) {
      throw new OutlookCliError("email_login_required");
    }
    if (!OUTLOOK_ORIGINS.has(final.origin) && !MICROSOFT_LOGIN_ORIGINS.has(final.origin)) {
      throw new OutlookCliError("outlook_origin_not_allowed");
    }
  }

  async inspect(expression) {
    const commands = [
      ["tab", this.label],
      ["eval", "-b", Buffer.from(expression, "utf8").toString("base64")],
    ];
    const results = await this.runner.runBatch(commands);
    validateBoundSwitch(results[0], this.label);
    return results[1];
  }

  async act(command) {
    const commands = [
      ["tab", this.label],
      ["eval", "-b", Buffer.from(OUTLOOK_ORIGIN_GUARD, "utf8").toString("base64")],
      command,
    ];
    const results = await this.runner.runBatch(commands);
    validateBoundSwitch(results[0], this.label);
    validateOriginGuard(results[1]);
    return results[2];
  }
}

class AgentBrowserRunner {
  constructor(options) {
    this.cdp = options.cdp;
    this.session = options.session;
    this.configPath = options.configPath;
    this.timeoutMs = options.timeoutMs;
  }

  async runSingle(command) {
    const raw = await this.run(command, null);
    let response;
    try {
      response = JSON.parse(raw);
    } catch {
      throw new OutlookCliError("agent_browser_invalid_output");
    }
    if (
      !isRecord(response) || response.success !== true ||
      !isRecord(response.data) ||
      (response.error !== null && response.error !== undefined) ||
      (response.warning !== null && response.warning !== undefined)
    ) {
      throw new OutlookCliError("agent_browser_invalid_output");
    }
    return response.data;
  }

  async runBatch(commands) {
    const raw = await this.run(["batch", "--bail"], `${JSON.stringify(commands)}\n`);
    let results;
    try {
      results = JSON.parse(raw);
    } catch {
      throw new OutlookCliError("agent_browser_invalid_output");
    }
    if (!Array.isArray(results) || results.length !== commands.length) {
      throw new OutlookCliError("agent_browser_invalid_output");
    }
    return results.map((entry, index) => {
      if (
        !isRecord(entry) || entry.success !== true ||
        JSON.stringify(entry.command) !== JSON.stringify(commands[index]) ||
        !isRecord(entry.result) ||
        (entry.error !== null && entry.error !== undefined)
      ) {
        throw new OutlookCliError("agent_browser_invalid_output");
      }
      return entry.result;
    });
  }

  async closeSession() {
    const result = await this.runSingle(["close"]);
    if (result.closed !== true) {
      throw new OutlookCliError("agent_browser_invalid_output");
    }
  }

  run(command, stdin) {
    const args = [
      "--config",
      this.configPath,
      "--session",
      this.session,
      "--json",
      ...command,
    ];
    const env = {
      ...process.env,
      AGENT_BROWSER_CDP: this.cdp,
      AGENT_BROWSER_IDLE_TIMEOUT_MS: String(this.timeoutMs + 10_000),
    };
    for (const name of Object.keys(env)) {
      if (
        FORBIDDEN_STARTUP_ENV.has(name) ||
        name.startsWith("AGENT_BROWSER_RESTORE_") ||
        name === "AGENT_BROWSER_AUTOSAVE_INTERVAL_MS" ||
        name === "AGENT_BROWSER_SESSION"
      ) {
        delete env[name];
      }
    }

    return new Promise((resolve, reject) => {
      let stdout = Buffer.alloc(0);
      let stderr = Buffer.alloc(0);
      let timedOut = false;
      let outputTooLarge = false;
      let settled = false;
      const child = spawn("agent-browser", args, {
        env,
        stdio: ["pipe", "pipe", "pipe"],
        windowsHide: true,
      });
      const timer = setTimeout(() => {
        timedOut = true;
        child.kill("SIGKILL");
      }, this.timeoutMs);
      timer.unref();

      child.stdout.on("data", (chunk) => {
        stdout = Buffer.concat([stdout, chunk]);
        if (stdout.length > MAX_AGENT_STDOUT_BYTES) {
          outputTooLarge = true;
          child.kill("SIGKILL");
        }
      });
      child.stderr.on("data", (chunk) => {
        stderr = Buffer.concat([stderr, chunk]);
        if (stderr.length > MAX_AGENT_STDERR_BYTES) {
          outputTooLarge = true;
          child.kill("SIGKILL");
        }
      });
      child.once("error", () => {
        if (settled) return;
        settled = true;
        clearTimeout(timer);
        reject(new OutlookCliError("agent_browser_failed"));
      });
      child.once("close", (code, signal) => {
        if (settled) return;
        settled = true;
        clearTimeout(timer);
        if (timedOut) return reject(new OutlookCliError("agent_browser_timeout"));
        if (outputTooLarge) {
          return reject(new OutlookCliError("agent_browser_invalid_output"));
        }
        if (code !== 0 || signal !== null || stderr.toString("utf8").trim() !== "") {
          return reject(new OutlookCliError("agent_browser_failed"));
        }
        resolve(stdout.toString("utf8"));
      });
      child.stdin.once("error", () => {});
      child.stdin.end(stdin ?? "");
    });
  }
}

function validateBoundSwitch(result, label, expectedUrl) {
  if (!isRecord(result) || result.label !== label || typeof result.url !== "string") {
    throw new OutlookCliError("agent_browser_invalid_output");
  }
  if (expectedUrl !== undefined && result.url !== expectedUrl) {
    throw new OutlookCliError("agent_browser_invalid_output");
  }
}

function validateOriginGuard(result) {
  if (!isRecord(result) || !isRecord(result.result) || result.result.contract_version !== 1) {
    throw new OutlookCliError("agent_browser_invalid_output");
  }
  if (typeof result.result.url !== "string" || result.result.url !== result.origin) {
    throw new OutlookCliError("agent_browser_invalid_output");
  }
  const url = parseHttpsUrl(result.result.url, "outlook_origin_not_allowed");
  if (!OUTLOOK_ORIGINS.has(url.origin)) {
    throw new OutlookCliError("outlook_origin_not_allowed");
  }
}

function parseHttpsUrl(value, invalidCode) {
  let url;
  try {
    url = new URL(value);
  } catch {
    throw new OutlookCliError(invalidCode);
  }
  if (url.protocol !== "https:" || url.username !== "" || url.password !== "") {
    throw new OutlookCliError(invalidCode);
  }
  return url;
}

function isMicrosoftOutlookLanding(url) {
  return url.origin === MICROSOFT_OUTLOOK_LANDING_ORIGIN &&
    MICROSOFT_OUTLOOK_LANDING_PATH.test(url.pathname.toLowerCase()) &&
    url.searchParams.get("deeplink") === "/mail/";
}

function deriveMaskedAccountHint(marker) {
  if (marker === null) return null;
  const label = marker.label.normalize("NFKC");
  const emailPattern = /[\p{L}\p{M}\p{N}.!#$%&'*+/=?^_`{|}~-]+@[A-Z0-9](?:[A-Z0-9-]{0,61}[A-Z0-9])?(?:\.[A-Z0-9](?:[A-Z0-9-]{0,61}[A-Z0-9])?)+/giu;
  const candidates = Array.from(label.matchAll(emailPattern), (match) => match[0])
    .filter((email) => {
      const separator = email.lastIndexOf("@");
      const localPart = email.slice(0, separator);
      const domain = email.slice(separator + 1);
      return localPart !== "" && !localPart.startsWith(".") &&
        !localPart.endsWith(".") && !localPart.includes("..") &&
        domain.length <= 253;
    });
  if (candidates.length !== 1) return null;
  const email = candidates[0];
  const separator = email.lastIndexOf("@");
  const localPrefix = Array.from(email.slice(0, separator)).slice(0, 2).join("");
  const domain = email.slice(separator + 1).toLowerCase();
  const hint = `${localPrefix}***@${domain}`;
  return Array.from(hint).length <= 64 ? hint : null;
}

function hasBooleanKeys(value, keys) {
  return hasExactKeys(value, keys) && keys.every((key) => typeof value[key] === "boolean");
}

function isAccountMarker(value) {
  return value === null || (
    hasExactKeys(value, ["label", "source"]) &&
    value.source === "account_control" &&
    typeof value.label === "string" && value.label.length <= 512
  );
}

function asOutlookError(error) {
  return error instanceof OutlookCliError ? error : new OutlookCliError("outlook_internal_error");
}

function isRecord(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}
