import { execFile } from "node:child_process";
import { createHash, randomUUID } from "node:crypto";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";

const execFileAsync = promisify(execFile);
const SCRIPT_DIR = dirname(fileURLToPath(import.meta.url));
const REPOSITORY_ROOT = resolve(SCRIPT_DIR, "../..");
const DEFAULT_AGENT_BROWSER = join(REPOSITORY_ROOT, "node_modules", ".bin", "agent-browser");

export const GMAIL_REGISTRATION_URL = "https://mail.google.com/mail/u/0/";
export const GMAIL_ORIGIN = "https://mail.google.com";
export const GOOGLE_ACCOUNTS_ORIGIN = "https://accounts.google.com";

const ALLOWED_HTTPS_ORIGINS = new Set([GMAIL_ORIGIN, GOOGLE_ACCOUNTS_ORIGIN]);
const INPUT_LIMIT_BYTES = 128 * 1024;
const OUTPUT_LIMIT_BYTES = 64 * 1024;
const DEFAULT_COMMAND_TIMEOUT_MS = 5000;

const FORBIDDEN_AGENT_BROWSER_ENVIRONMENT = [
  /^AGENT_BROWSER_PROFILE$/,
  /^AGENT_BROWSER_STATE(?:_|$)/,
  /^AGENT_BROWSER_RESTORE(?:_|$)/,
  /^AGENT_BROWSER_AUTO_CONNECT$/,
  /^AGENT_BROWSER_CONFIG$/,
  /^AGENT_BROWSER_ARGS$/,
  /^AGENT_BROWSER_EXECUTABLE_PATH$/,
  /^AGENT_BROWSER_EXTENSIONS$/,
  /^AGENT_BROWSER_INIT_SCRIPTS$/,
  /^AGENT_BROWSER_ENABLE$/,
  /^AGENT_BROWSER_PROVIDER$/,
  /^AGENT_BROWSER_ENGINE$/,
  /^AGENT_BROWSER_SESSION_NAME$/,
  /^AGENT_BROWSER_NAMESPACE$/
];

const LOGIN_SELECTORS = Object.freeze({
  accountControl: '[aria-label^="Google Account:"]',
  composeControl: '[role="button"][gh="cm"]',
  mainMenu: '[aria-label="Main menu"]',
  accountChoice: "[data-identifier]",
  useAnotherAccount: '[jsname="rwl3qc"]',
  identifierInput: "input#identifierId",
  identifierNext: "#identifierNext"
});

export class GmailCliError extends Error {
  constructor(code) {
    super(code);
    this.code = code;
  }
}

export async function readStrictJson(errorCode) {
  const chunks = [];
  let totalBytes = 0;

  for await (const chunk of process.stdin) {
    const bytes = Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk);
    totalBytes += bytes.length;
    if (totalBytes > INPUT_LIMIT_BYTES) {
      throw new GmailCliError(errorCode);
    }
    chunks.push(bytes);
  }

  if (totalBytes === 0) {
    throw new GmailCliError(errorCode);
  }

  let input;
  try {
    input = new TextDecoder("utf-8", { fatal: true }).decode(Buffer.concat(chunks));
  } catch {
    throw new GmailCliError(errorCode);
  }

  try {
    return JSON.parse(input);
  } catch {
    throw new GmailCliError(errorCode);
  }
}

export function requireExactObject(value, requiredKeys, optionalKeys, errorCode) {
  if (value === null || Array.isArray(value) || typeof value !== "object") {
    throw new GmailCliError(errorCode);
  }

  const required = new Set(requiredKeys);
  const allowed = new Set([...requiredKeys, ...optionalKeys]);
  const actualKeys = Object.keys(value);
  if (actualKeys.some((key) => !allowed.has(key)) ||
      [...required].some((key) => !Object.hasOwn(value, key))) {
    throw new GmailCliError(errorCode);
  }
  return value;
}

export function requireInvocationId(value, errorCode) {
  if (typeof value !== "string" || !/^[\x21-\x7e]{1,128}$/.test(value)) {
    throw new GmailCliError(errorCode);
  }
  return value;
}

export function recipientDigest(value) {
  return `sha256:${createHash("sha256").update(value, "utf8").digest("hex")}`;
}

export function requireSafeHostCdpEnvironment(configurationErrorCode) {
  for (const key of Object.keys(process.env)) {
    if (FORBIDDEN_AGENT_BROWSER_ENVIRONMENT.some((pattern) => pattern.test(key))) {
      throw new GmailCliError(configurationErrorCode);
    }
  }

  const cdpEndpoint = process.env.AGENT_BROWSER_CDP;
  if (typeof cdpEndpoint !== "string" || cdpEndpoint.length === 0 ||
      cdpEndpoint.length > 2048 || /[\x00-\x20\x7f]/.test(cdpEndpoint)) {
    throw new GmailCliError(configurationErrorCode);
  }
  return cdpEndpoint;
}

export async function createOwnedGmailTab({
  kind,
  cdpEndpoint,
  configurationErrorCode,
  timeoutErrorCode,
  invalidOutputErrorCode,
  browserErrorCode,
  originErrorCode,
  originConflictErrorCode
}) {
  const agentBrowser = resolveAgentBrowser(configurationErrorCode);
  const commandTimeoutMs = resolveCommandTimeout(configurationErrorCode);
  const temporaryDirectory = await mkdtemp(join(tmpdir(), `gmail-${kind}-`));
  const configPath = join(temporaryDirectory, "agent-browser.json");
  await writeFile(configPath, "{}\n", { encoding: "utf8", mode: 0o600 });

  const token = randomUUID().replaceAll("-", "");
  const secondToken = randomUUID().replaceAll("-", "");
  const session = `gmail-${kind}-${token}`;
  const label = `gmail-${kind}-${secondToken}`;
  const environment = buildChildEnvironment(cdpEndpoint, commandTimeoutMs);

  let creationAttempted = false;
  let createdTabId;
  let daemonMayExist = false;
  let disposed = false;

  return {
    async open() {
      creationAttempted = true;
      const data = await runAgentBrowser([
        "tab",
        "new",
        "--label",
        label,
        "about:blank"
      ]);
      if (!isTabId(data.tabId) || data.label !== label || data.url !== "about:blank") {
        throw new GmailCliError(invalidOutputErrorCode);
      }
      createdTabId = data.tabId;
      await navigateOwnedTab();
    },

    async getUrl(expectedOrigin) {
      const data = await runOnOwnedTab(["get", "url"], expectedOrigin);
      if (typeof data.url !== "string" || data.url.length === 0 || data.url.length > 8192) {
        throw new GmailCliError(invalidOutputErrorCode);
      }
      requireAllowedOrigin(data.url, originErrorCode);
      return data.url;
    },

    async getCount(selector, expectedOrigin) {
      const data = await runOnOwnedTab(["get", "count", selector], expectedOrigin);
      validateReportedOrigin(data.origin, expectedOrigin);
      if (!Number.isSafeInteger(data.count) || data.count < 0 || data.count > 1000) {
        throw new GmailCliError(invalidOutputErrorCode);
      }
      return data.count;
    },

    async getAttribute(selector, attribute, expectedOrigin) {
      const data = await runOnOwnedTab(["get", "attr", selector, attribute], expectedOrigin);
      validateReportedOrigin(data.origin, expectedOrigin);
      if (typeof data.value !== "string" || data.value.length > 4096) {
        throw new GmailCliError(invalidOutputErrorCode);
      }
      return data.value;
    },

    async getValue(selector, expectedOrigin) {
      const data = await runOnOwnedTab(["get", "value", selector], expectedOrigin);
      validateReportedOrigin(data.origin, expectedOrigin);
      if (typeof data.value !== "string" || Buffer.byteLength(data.value, "utf8") > 128 * 1024) {
        throw new GmailCliError(invalidOutputErrorCode);
      }
      return data.value;
    },

    async getText(selector, expectedOrigin) {
      const data = await runOnOwnedTab(["get", "text", selector], expectedOrigin);
      validateReportedOrigin(data.origin, expectedOrigin);
      if (typeof data.text !== "string" || Buffer.byteLength(data.text, "utf8") > 128 * 1024) {
        throw new GmailCliError(invalidOutputErrorCode);
      }
      return data.text;
    },

    async waitFor(selector, expectedOrigin) {
      await runOnOwnedTab(["wait", selector], expectedOrigin);
    },

    async click(selector, expectedOrigin) {
      const data = await runOnOwnedTab(["click", selector], expectedOrigin);
      if (data.clicked !== selector) {
        throw new GmailCliError(invalidOutputErrorCode);
      }
    },

    async fill(selector, value, expectedOrigin) {
      const data = await runOnOwnedTab(["fill", selector, value], expectedOrigin);
      if (data.filled !== selector) {
        throw new GmailCliError(invalidOutputErrorCode);
      }
    },

    async focus(selector, expectedOrigin) {
      const data = await runOnOwnedTab(["focus", selector], expectedOrigin);
      if (data.focused !== selector) {
        throw new GmailCliError(invalidOutputErrorCode);
      }
    },

    async press(key, expectedOrigin) {
      const data = await runOnOwnedTab(["press", key], expectedOrigin);
      if (data.pressed !== key) {
        throw new GmailCliError(invalidOutputErrorCode);
      }
    },

    async closeOwnedTab() {
      if (!creationAttempted) {
        return;
      }
      const data = await runAgentBrowser(["tab", "close", label]);
      if (data.closed !== true || data.label !== label ||
          (createdTabId !== undefined && data.tabId !== createdTabId)) {
        throw new GmailCliError(invalidOutputErrorCode);
      }
      creationAttempted = false;
    },

    async dispose() {
      if (!disposed) {
        disposed = true;
        if (daemonMayExist) {
          try {
            const data = await runAgentBrowser(["close"]);
            if (data.closed !== true) {
              throw new GmailCliError(invalidOutputErrorCode);
            }
            daemonMayExist = false;
          } catch {
            // The bounded agent-browser idle timeout remains the cleanup fallback.
          }
        }
        await rm(temporaryDirectory, { recursive: true, force: true }).catch(() => {});
      }
    }
  };

  async function runOnOwnedTab(commandArguments, expectedOrigin) {
    if (createdTabId === undefined) {
      throw new GmailCliError(browserErrorCode);
    }

    const switched = await runAgentBrowser(["tab", label]);
    if (switched.tabId !== createdTabId || switched.label !== label ||
        typeof switched.url !== "string") {
      throw new GmailCliError(invalidOutputErrorCode);
    }
    const activeOrigin = requireAllowedOrigin(switched.url, originErrorCode);
    if (expectedOrigin !== undefined && activeOrigin !== expectedOrigin) {
      throw new GmailCliError(originConflictErrorCode);
    }

    const data = await runAgentBrowser(commandArguments);
    validateReportedOrigin(data.origin, expectedOrigin ?? activeOrigin);
    return data;
  }

  async function navigateOwnedTab() {
    const switched = await runAgentBrowser(["tab", label]);
    if (switched.tabId !== createdTabId || switched.label !== label ||
        switched.url !== "about:blank") {
      throw new GmailCliError(invalidOutputErrorCode);
    }

    await runAgentBrowser(["open", GMAIL_REGISTRATION_URL]);
    await runOnOwnedTab(["wait", "3000"]);
    const final = await runOnOwnedTab(["get", "url"]);
    if (typeof final.url !== "string" || final.url.length === 0 || final.url.length > 8192) {
      throw new GmailCliError(invalidOutputErrorCode);
    }
    requireAllowedOrigin(final.url, originErrorCode);
  }

  async function runAgentBrowser(commandArguments) {
    daemonMayExist = true;
    const args = [
      "--session",
      session,
      "--config",
      configPath,
      "--json",
      ...commandArguments
    ];
    let result;

    try {
      result = await execFileAsync(agentBrowser, args, {
        cwd: REPOSITORY_ROOT,
        env: environment,
        encoding: "utf8",
        timeout: commandTimeoutMs,
        killSignal: "SIGKILL",
        maxBuffer: OUTPUT_LIMIT_BYTES,
        windowsHide: true
      });
    } catch (caught) {
      if (caught?.code === "ENOENT" || caught?.code === "EACCES") {
        throw new GmailCliError(configurationErrorCode);
      }
      if (caught?.code === "ERR_CHILD_PROCESS_STDIO_MAXBUFFER") {
        throw new GmailCliError(invalidOutputErrorCode);
      }
      if (caught?.killed === true && caught?.signal === "SIGKILL") {
        throw new GmailCliError(timeoutErrorCode);
      }
      throw new GmailCliError(browserErrorCode);
    }

    if (typeof result.stdout !== "string" || typeof result.stderr !== "string" ||
        result.stderr.trim() !== "") {
      throw new GmailCliError(browserErrorCode);
    }

    let response;
    try {
      response = JSON.parse(result.stdout);
    } catch {
      throw new GmailCliError(invalidOutputErrorCode);
    }

    if (response === null || Array.isArray(response) || typeof response !== "object") {
      throw new GmailCliError(invalidOutputErrorCode);
    }
    if (response.success === false) {
      throw new GmailCliError(browserErrorCode);
    }
    if (response.success !== true || response.data === null ||
        Array.isArray(response.data) || typeof response.data !== "object") {
      throw new GmailCliError(invalidOutputErrorCode);
    }
    return response.data;
  }

  function validateReportedOrigin(reportedOrigin, expectedOrigin) {
    if (reportedOrigin === undefined) {
      return;
    }
    if (typeof reportedOrigin !== "string") {
      throw new GmailCliError(originErrorCode);
    }
    const origin = requireAllowedOrigin(reportedOrigin, originErrorCode);
    if (expectedOrigin !== undefined && origin !== expectedOrigin) {
      throw new GmailCliError(originConflictErrorCode);
    }
  }
}

export async function inspectGmailLogin(tab, { includeAccountHint = false } = {}) {
  const initialUrl = await tab.getUrl();
  const initialOrigin = requireAllowedOrigin(initialUrl, "email_provider_origin_invalid");
  const evidence = {};
  for (const [name, selector] of Object.entries(LOGIN_SELECTORS)) {
    evidence[name] = await tab.getCount(selector, initialOrigin);
  }

  let accountHint;
  if (includeAccountHint && initialOrigin === GMAIL_ORIGIN && evidenceIsReady(evidence)) {
    const label = await tab.getAttribute(
      LOGIN_SELECTORS.accountControl,
      "aria-label",
      initialOrigin
    );
    accountHint = extractAccountHint(label);
  }

  const finalUrl = await tab.getUrl(initialOrigin);
  const finalOrigin = requireAllowedOrigin(finalUrl, "email_provider_origin_invalid");
  if (finalOrigin !== initialOrigin) {
    throw new GmailCliError("email_login_evidence_conflict");
  }

  const result = classifyEvidence(initialOrigin, evidence);
  if (result !== "ready") {
    throw new GmailCliError(result);
  }
  return { accountHint };
}

export function requireAllowedOrigin(rawUrl, errorCode) {
  let parsed;
  try {
    parsed = new URL(rawUrl);
  } catch {
    throw new GmailCliError(errorCode);
  }
  if (!ALLOWED_HTTPS_ORIGINS.has(parsed.origin)) {
    throw new GmailCliError(errorCode);
  }
  return parsed.origin;
}

export function writeSuccess(payload) {
  process.stdout.write(`${JSON.stringify(payload)}\n`);
}

export function writeFailure(code) {
  process.stderr.write(`${JSON.stringify({
    schema_version: 1,
    status: "error",
    provider: "gmail",
    code
  })}\n`);
}

function resolveAgentBrowser(configurationErrorCode) {
  const override = process.env.SPARKCLAW_AGENT_BROWSER ??
    process.env.GMAIL_CLI_AGENT_BROWSER_BIN;
  if (override === undefined) {
    return DEFAULT_AGENT_BROWSER;
  }
  if (override.length === 0 || override.length > 4096 || /[\x00\r\n]/.test(override)) {
    throw new GmailCliError(configurationErrorCode);
  }
  return resolve(override);
}

function resolveCommandTimeout(configurationErrorCode) {
  const raw = process.env.GMAIL_CLI_COMMAND_TIMEOUT_MS;
  if (raw === undefined) {
    return DEFAULT_COMMAND_TIMEOUT_MS;
  }
  if (!/^\d+$/.test(raw)) {
    throw new GmailCliError(configurationErrorCode);
  }
  const value = Number(raw);
  if (!Number.isSafeInteger(value) || value < 25 || value > 30000) {
    throw new GmailCliError(configurationErrorCode);
  }
  return value;
}

function buildChildEnvironment(cdpEndpoint, commandTimeoutMs) {
  const environment = {};
  for (const key of [
    "HOME",
    "LANG",
    "LC_ALL",
    "LOGNAME",
    "PATH",
    "TEMP",
    "TMP",
    "TMPDIR",
    "USER",
    "XDG_CACHE_HOME",
    "XDG_CONFIG_HOME",
    "XDG_RUNTIME_DIR"
  ]) {
    if (process.env[key] !== undefined) {
      environment[key] = process.env[key];
    }
  }

  environment.AGENT_BROWSER_CDP = cdpEndpoint;
  environment.AGENT_BROWSER_IDLE_TIMEOUT_MS = String(commandTimeoutMs + 10_000);
  environment.AGENT_BROWSER_JSON = "1";
  environment.NO_COLOR = "1";

  if (process.env.GMAIL_CLI_AGENT_BROWSER_BIN !== undefined) {
    for (const key of [
      "GMAIL_CLI_TEST_LOG",
      "GMAIL_CLI_TEST_SCENARIO",
      "GMAIL_CLI_TEST_STATE"
    ]) {
      if (process.env[key] !== undefined) {
        environment[key] = process.env[key];
      }
    }
  }
  return environment;
}

function isTabId(value) {
  return typeof value === "string" && /^t[1-9]\d*$/.test(value);
}

function evidenceIsReady(evidence) {
  return evidence.accountControl === 1 &&
    evidence.composeControl === 1 &&
    evidence.mainMenu === 1 &&
    negativeEvidenceCount(evidence) === 0;
}

function classifyEvidence(origin, evidence) {
  const positivePresent = evidence.accountControl > 0 ||
    evidence.composeControl > 0 ||
    evidence.mainMenu > 0;
  const negativePresent = negativeEvidenceCount(evidence) > 0;

  if (positivePresent && negativePresent) {
    return "email_login_evidence_conflict";
  }
  if (origin === GMAIL_ORIGIN) {
    if (negativePresent) {
      return "email_login_evidence_conflict";
    }
    return evidenceIsReady(evidence) ? "ready" : "email_page_contract_changed";
  }
  if (origin === GOOGLE_ACCOUNTS_ORIGIN) {
    if (positivePresent) {
      return "email_login_evidence_conflict";
    }
    const chooserReady = evidence.accountChoice >= 1 && evidence.useAnotherAccount === 1;
    const identifierReady = evidence.identifierInput === 1 && evidence.identifierNext === 1;
    const chooserEvidenceCount = evidence.accountChoice + evidence.useAnotherAccount;
    const identifierEvidenceCount = evidence.identifierInput + evidence.identifierNext;
    if ((chooserReady && identifierEvidenceCount === 0) ||
        (identifierReady && chooserEvidenceCount === 0)) {
      return "email_login_required";
    }
    return "email_page_contract_changed";
  }
  return "email_provider_origin_invalid";
}

function negativeEvidenceCount(evidence) {
  return evidence.accountChoice +
    evidence.useAnotherAccount +
    evidence.identifierInput +
    evidence.identifierNext;
}

function extractAccountHint(label) {
  if (!label.startsWith("Google Account:")) {
    return undefined;
  }
  const accountMarker = label.match(/\(([^()\r\n]{1,128})\)\s*$/);
  if (accountMarker === null) {
    return undefined;
  }
  const candidate = accountMarker[1];
  const separator = candidate.indexOf("@");
  if (separator <= 0 || separator !== candidate.lastIndexOf("@") ||
      separator === candidate.length - 1) {
    return undefined;
  }

  const localPart = candidate.slice(0, separator);
  const rawDomain = candidate.slice(separator + 1);
  const localPattern = /^[\p{L}\p{N}\p{M}.!#$%&'*+/=?^_`{|}~-]+$/u;
  const domainPattern = /^[A-Z0-9](?:[A-Z0-9-]{0,61}[A-Z0-9])?(?:\.[A-Z0-9](?:[A-Z0-9-]{0,61}[A-Z0-9])?)+$/i;
  if (!localPattern.test(localPart) || localPart.startsWith(".") ||
      localPart.endsWith(".") || localPart.includes("..") ||
      !domainPattern.test(rawDomain)) {
    return undefined;
  }

  const prefix = Array.from(localPart).slice(0, 2).join("");
  const hint = `${prefix}***@${rawDomain.toLowerCase()}`;
  return Array.from(hint).length <= 64 ? hint : undefined;
}
