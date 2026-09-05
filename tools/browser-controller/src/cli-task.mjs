import crypto from "node:crypto";

import { ControllerError } from "./errors.mjs";
import { BACKGROUND_CLICK_FUNCTION } from "./dom-actions.mjs";
import {
  MAX_CLI_OUTPUT_BYTES,
  clientContractError,
  clientTimeoutError,
  clientUnavailableError,
  pageStale,
  runProcess,
  scrubPlaywrightEnvironment,
} from "./cli-runtime.mjs";

const EXTENSION_ID = "mmlmfjhmonkocbjadbfplnigmagldckm";
const EXTENSION_CONNECT_URL = "sparkclaw-internal://extension-connect";
const RELAY_PATH_PATTERN = /^\/extension\/[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/u;
const TRANSIENT_EVALUATION_ATTEMPTS = 4;
const TRANSIENT_EVALUATION_DELAY_MS = 250;
const CLI_COMMANDS = new Set([
  "attach",
  "click",
  "close",
  "detach",
  "eval",
  "fill",
  "goto",
  "press",
  "tab-close",
  "tab-list",
  "tab-select",
]);

export class PlaywrightCLITask {
  constructor(options) {
    Object.assign(this, options);
    this.sessionName = `sc-cli-${crypto.createHash("sha256")
      .update(this.state.sessionID)
      .digest("hex")
      .slice(0, 20)}`;
    this.deadline = Date.now() + this.registration.timeoutMS;
    this.ownerTabs = null;
    this.taskIndex = -1;
    this.taskReady = false;
    this.attached = false;
    this.effectAttempted = false;
  }

  async attach() {
    const output = await this.#run(
      ["--json", `-s=${this.sessionName}`, "attach", `--extension=${this.browserChannel}`],
      this.connectTimeoutMS,
      (raw) => sanitizeAttachOutput(raw, this.sessionName, this.browserChannel),
    );
    const parsed = parseJSON(output);
    if (
      parsed.session !== this.sessionName ||
      !Number.isSafeInteger(parsed.pid) ||
      parsed.pid <= 1 ||
      parsed.endpoint !== this.browserChannel
    ) {
      throw clientContractError();
    }
    await this.state.writeMetadata(parsed.pid, this.sessionName);
    this.attached = true;
    const tabs = await this.#tabs();
    const taskIndexes = tabs.flatMap((tab, index) =>
      tab.url === EXTENSION_CONNECT_URL ? [index] : []);
    if (
      taskIndexes.length !== 1 ||
      !tabs[taskIndexes[0]].current ||
      tabs[taskIndexes[0]].crashed
    ) {
      throw clientContractError();
    }
    this.taskIndex = taskIndexes[0];
    this.ownerTabs = tabs.filter((_, index) => index !== this.taskIndex);
  }

  async createTaskPage() {
    if (!this.attached || this.taskIndex < 0 || this.taskReady) {
      throw clientContractError();
    }
    const tabs = await this.#tabs();
    this.#assertTopology(tabs);
    if (tabs[this.taskIndex]?.url !== EXTENSION_CONNECT_URL) {
      throw pageStale("task_page_missing");
    }
    this.taskReady = true;
  }

  async navigate(url) {
    if (!this.taskReady || url !== this.registration.loginURL) throw clientContractError();
    await this.#withTaskSelected(async () => {
      await this.#run(
        ["--raw", `-s=${this.sessionName}`, "goto", url],
        this.navigationTimeoutMS,
      );
    });
    this.#assertProviderURL(await this.#assertAllowedOrigin());
  }

  async closeTaskPage() {
    if (!this.attached || this.taskIndex < 0) return;
    const tabs = await this.#tabs();
    this.#assertTopology(tabs);
    try {
      await this.#run([
        "--raw",
        `-s=${this.sessionName}`,
        "tab-close",
        String(this.taskIndex),
      ]);
    } catch (error) {
      if (!isExpectedTaskPageClosure(error)) throw error;
    }
    this.taskIndex = -1;
    this.taskReady = false;
  }

  async stop() {
    if (!this.attached) return;
    this.attached = false;
    const parsed = parseJSON(await this.#run(
      ["--json", `-s=${this.sessionName}`, "close"],
      this.connectTimeoutMS,
    ));
    if (
      parsed.session !== this.sessionName ||
      parsed.status !== "closed" && parsed.status !== "not-open"
    ) {
      throw clientContractError();
    }
  }

  qqTask() {
    return {
      onTab: async (commands) => {
        const results = [];
        for (const command of commands) {
          results.push({ success: true, result: await this.#agentAction(command) });
        }
        return results;
      },
    };
  }

  outlookTab() {
    return {
      inspect: async (expression) => {
        this.#assertProviderURL(await this.currentURL());
        const result = await this.evaluate(expression);
        const origin = await this.currentURL();
        this.#assertProviderURL(origin);
        return { result, origin };
      },
      act: async (command) => {
        await this.#assertAllowedOrigin();
        return await this.#agentAction(command);
      },
    };
  }

  gmailTab() {
    return {
      open: async () => {},
      getUrl: async (expectedOrigin) => await this.currentURL(expectedOrigin),
      getCount: async (selector, expectedOrigin) => await this.count(selector, expectedOrigin),
      getAttribute: async (selector, attribute, expectedOrigin) =>
        await this.attribute(selector, attribute, expectedOrigin),
      getValue: async (selector, expectedOrigin) => await this.value(selector, expectedOrigin),
      getText: async (selector, expectedOrigin) => await this.text(selector, expectedOrigin),
      waitFor: async (selector, expectedOrigin) => await this.waitFor(selector, expectedOrigin),
      click: async (selector, expectedOrigin) => await this.click(selector, expectedOrigin),
      fill: async (selector, value, expectedOrigin) =>
        await this.fill(selector, value, expectedOrigin),
      focus: async (selector, expectedOrigin) => await this.focus(selector, expectedOrigin),
      press: async (key, expectedOrigin) => await this.press(key, expectedOrigin),
      closeOwnedTab: async () => {},
      dispose: async () => {},
    };
  }

  async currentURL(expectedOrigin) {
    let value;
    for (let attempt = 0; attempt < TRANSIENT_EVALUATION_ATTEMPTS; attempt += 1) {
      try {
        value = await this.#evalJSON("() => location.href");
        break;
      } catch (error) {
        if (!isContextDestroyed(error) || attempt === TRANSIENT_EVALUATION_ATTEMPTS - 1) {
          throw error;
        }
        await abortableDelay(TRANSIENT_EVALUATION_DELAY_MS, this.signal);
      }
    }
    if (typeof value !== "string") throw clientContractError();
    assertExpectedOrigin(value, expectedOrigin, this.registration.origins);
    return value;
  }

  async count(selector, expectedOrigin) {
    await this.currentURL(expectedOrigin);
    const value = await this.#evalJSON(
      `() => document.querySelectorAll(${JSON.stringify(selector)}).length`,
    );
    if (!Number.isSafeInteger(value) || value < 0 || value > 10_000) {
      throw clientContractError();
    }
    return value;
  }

  async attribute(selector, attribute, expectedOrigin) {
    await this.currentURL(expectedOrigin);
    const value = await this.#evalJSON(
      `() => document.querySelector(${JSON.stringify(selector)})?.getAttribute(${JSON.stringify(attribute)}) ?? ""`,
    );
    if (typeof value !== "string") throw clientContractError();
    return value;
  }

  async value(selector, expectedOrigin) {
    await this.currentURL(expectedOrigin);
    const value = await this.#evalJSON(
      `() => document.querySelector(${JSON.stringify(selector)})?.value ?? ""`,
    );
    if (typeof value !== "string") throw clientContractError();
    return value;
  }

  async text(selector, expectedOrigin) {
    await this.currentURL(expectedOrigin);
    const value = await this.#evalJSON(
      `() => { const element = document.querySelector(${JSON.stringify(selector)}); return element ? (element.innerText ?? element.textContent ?? "") : ""; }`,
    );
    if (typeof value !== "string") throw clientContractError();
    return value;
  }

  async visible(selector, expectedOrigin) {
    await this.currentURL(expectedOrigin);
    const value = await this.#evalJSON(
      `() => { const element = document.querySelector(${JSON.stringify(selector)}); if (!element || !element.isConnected) return false; const style = getComputedStyle(element); const rect = element.getBoundingClientRect(); return rect.width > 0 && rect.height > 0 && style.display !== "none" && style.visibility !== "hidden" && Number.parseFloat(style.opacity || "1") > 0; }`,
    );
    if (typeof value !== "boolean") throw clientContractError();
    return value;
  }

  async enabled(selector, expectedOrigin) {
    await this.currentURL(expectedOrigin);
    const value = await this.#evalJSON(
      `() => { const element = document.querySelector(${JSON.stringify(selector)}); return Boolean(element) && !element.disabled && element.getAttribute("aria-disabled") !== "true"; }`,
    );
    if (typeof value !== "boolean") throw clientContractError();
    return value;
  }

  async evaluate(expression) {
    if (typeof expression !== "string" || Buffer.byteLength(expression, "utf8") > 32 << 10) {
      throw clientContractError();
    }
    for (let attempt = 0; attempt < TRANSIENT_EVALUATION_ATTEMPTS; attempt += 1) {
      try {
        return await this.#evalJSON(expression);
      } catch (error) {
        if (!isContextDestroyed(error) || attempt === TRANSIENT_EVALUATION_ATTEMPTS - 1) {
          throw error;
        }
        await abortableDelay(TRANSIENT_EVALUATION_DELAY_MS, this.signal);
        this.#assertProviderURL(await this.currentURL());
      }
    }
    throw clientContractError();
  }

  async click(selector, expectedOrigin) {
    await this.currentURL(expectedOrigin);
    if (selector === this.registration.effectSelector) this.effectAttempted = true;
    const clicked = await this.#evalJSON(BACKGROUND_CLICK_FUNCTION, selector);
    if (clicked !== true) throw clientContractError();
    await this.currentURL(expectedOrigin);
  }

  async fill(selector, value, expectedOrigin) {
    await this.currentURL(expectedOrigin);
    const secret = this.state.secretName(value);
    await this.#withTaskSelected(async () => {
      await this.#run(["--raw", `-s=${this.sessionName}`, "fill", selector, secret]);
    });
    await this.currentURL(expectedOrigin);
  }

  async focus(selector, expectedOrigin) {
    await this.currentURL(expectedOrigin);
    const focused = await this.#evalJSON(
      "(element) => { element.focus(); return document.activeElement === element; }",
      selector,
    );
    if (focused !== true) throw clientContractError();
  }

  async press(key, expectedOrigin) {
    await this.currentURL(expectedOrigin);
    if (typeof key !== "string" || !/^[A-Za-z0-9+_-]{1,32}$/u.test(key)) {
      throw clientContractError();
    }
    await this.#withTaskSelected(async () => {
      await this.#run(["--raw", `-s=${this.sessionName}`, "press", key]);
    });
    await this.currentURL(expectedOrigin);
  }

  async waitFor(selector, expectedOrigin, timeoutMS = 10_000) {
    await this.currentURL(expectedOrigin);
    const bounded = Math.min(Math.max(Number(timeoutMS) || 10_000, 25), 30_000);
    const expression = `async () => { const selector = ${JSON.stringify(selector)}; const deadline = Date.now() + ${bounded}; while (Date.now() < deadline) { if (document.querySelector(selector)) return true; await new Promise(resolve => setTimeout(resolve, 100)); } return false; }`;
    if (await this.#evalJSON(expression) !== true) throw clientContractError();
  }

  async waitMilliseconds(value) {
    const milliseconds = Number(value);
    if (!Number.isSafeInteger(milliseconds) || milliseconds < 1 || milliseconds > 30_000) {
      throw clientContractError();
    }
    await abortableDelay(milliseconds, this.signal);
    await this.#assertAllowedOrigin();
  }

  async #agentAction(command) {
    if (!Array.isArray(command) || command.some((value) => typeof value !== "string")) {
      throw clientContractError();
    }
    const [name, subtype, selector, extra] = command;
    switch (name) {
      case "get":
        if (subtype === "url") return { url: await this.currentURL() };
        if (subtype === "count") return { count: await this.count(selector), selector };
        if (subtype === "text") {
          return { text: await this.text(selector), origin: await this.currentURL() };
        }
        if (subtype === "value") {
          return { value: await this.value(selector), origin: await this.currentURL() };
        }
        if (subtype === "attr") {
          return {
            value: await this.attribute(selector, extra),
            origin: await this.currentURL(),
          };
        }
        break;
      case "is":
        if (subtype === "visible") {
          return { visible: await this.visible(selector), origin: await this.currentURL() };
        }
        if (subtype === "enabled") {
          return { enabled: await this.enabled(selector), origin: await this.currentURL() };
        }
        break;
      case "click":
        await this.click(subtype);
        return { clicked: subtype };
      case "fill":
        await this.fill(subtype, selector);
        return { filled: subtype };
      case "focus":
        await this.focus(subtype);
        return { focused: subtype };
      case "press":
        await this.press(subtype);
        return { pressed: subtype };
      case "wait": {
        if (/^[0-9]+$/u.test(subtype)) await this.waitMilliseconds(subtype);
        else {
          const timeoutIndex = command.indexOf("--timeout");
          await this.waitFor(
            subtype,
            undefined,
            timeoutIndex >= 0 ? Number(command[timeoutIndex + 1]) : undefined,
          );
        }
        return { waited: subtype };
      }
      case "eval": {
        const encoded = command.includes("-b") ? command[command.indexOf("-b") + 1] : "";
        if (!encoded) break;
        const expression = Buffer.from(encoded, "base64").toString("utf8");
        return { result: await this.evaluate(expression), origin: await this.currentURL() };
      }
    }
    throw clientContractError();
  }

  async #evalJSON(expression, target) {
    const output = await this.#withTaskSelected(async () => await this.#run([
      "--raw",
      `-s=${this.sessionName}`,
      "eval",
      expression,
      ...(target ? [target] : []),
    ]));
    return parseJSON(output);
  }

  async #assertAllowedOrigin() {
    const url = await this.currentURL();
    assertExpectedOrigin(url, undefined, this.registration.origins);
    return url;
  }

  #assertProviderURL(url) {
    if (typeof this.registration.signedOutURL !== "function") return;
    if (!this.registration.signedOutURL(url)) return;
    throw Object.assign(new Error("email_login_required"), {
      code: "email_login_required",
    });
  }

  async #withTaskSelected(callback) {
    const tabs = await this.#tabs();
    this.#assertTopology(tabs);
    if (!tabs[this.taskIndex]?.current) {
      await this.#run([
        "--raw",
        `-s=${this.sessionName}`,
        "tab-select",
        String(this.taskIndex),
      ]);
      const selected = await this.#tabs();
      this.#assertTopology(selected);
      if (!selected[this.taskIndex]?.current) throw pageStale("page_topology_changed");
    }
    return await callback();
  }

  async #tabs() {
    return parseTabs(await this.#run(
      ["--raw", `-s=${this.sessionName}`, "tab-list"],
      this.actionTimeoutMS,
      (raw) => sanitizeTabListOutput(raw, this.token),
    ));
  }

  #assertTopology(tabs) {
    if (
      this.taskIndex < 0 ||
      tabs.length !== this.ownerTabs.length + 1 ||
      this.taskIndex >= tabs.length
    ) {
      throw pageStale("page_topology_changed");
    }
    const owners = tabs.filter((_, index) => index !== this.taskIndex).map(tabFingerprint);
    if (!sameFingerprintList(owners, this.ownerTabs.map(tabFingerprint))) {
      throw pageStale("page_topology_changed");
    }
  }

  async #run(args, requestedTimeoutMS = this.actionTimeoutMS, stdoutTransform) {
    if (this.signal?.aborted) throw clientUnavailableError();
    const remaining = this.deadline - Date.now();
    if (remaining <= 0) throw clientTimeoutError();
    const timeoutMS = Math.max(1, Math.min(requestedTimeoutMS, remaining));
    const env = scrubPlaywrightEnvironment({ ...process.env, ...this.extraEnv });
    Object.assign(env, this.state.environment, {
      PLAYWRIGHT_MCP_EXTENSION_TOKEN: this.token,
      PLAYWRIGHT_MCP_CODEGEN: "none",
      PLAYWRIGHT_MCP_IMAGE_RESPONSES: "omit",
      PLAYWRIGHT_MCP_OUTPUT_DIR: this.state.outputDir,
      PLAYWRIGHT_MCP_OUTPUT_MAX_SIZE: String(MAX_CLI_OUTPUT_BYTES),
      PLAYWRIGHT_MCP_SNAPSHOT_MODE: "none",
      PLAYWRIGHT_MCP_TIMEOUT_ACTION: String(this.actionTimeoutMS),
      PLAYWRIGHT_MCP_TIMEOUT_NAVIGATION: String(this.navigationTimeoutMS),
      PLAYWRIGHT_MCP_TIMEOUT_SETTLE: "500",
      NO_COLOR: "1",
      NO_UPDATE_NOTIFIER: "1",
    });
    if (this.executablePath) env.PLAYWRIGHT_MCP_EXECUTABLE_PATH = this.executablePath;
    if (this.userDataDir) env.PLAYWRIGHT_MCP_USER_DATA_DIR = this.userDataDir;
    if (this.state.secretsPath) env.PLAYWRIGHT_MCP_SECRETS_FILE = this.state.secretsPath;
    try {
      return await runProcess(this.spawn, process.execPath, [this.entryPoint, ...args], {
        cwd: this.state.outputDir,
        env,
        timeoutMS,
        secrets: [...this.state.secretValues, this.token],
        forbiddenOutputValues: args.includes("eval")
          ? [this.token]
          : [...this.state.secretValues, this.token],
        signal: this.signal,
        stdoutTransform,
      });
    } catch (error) {
      const command = args.find((value) => !value.startsWith("-"));
      if (error instanceof ControllerError && CLI_COMMANDS.has(command)) {
        Object.defineProperty(error, "diagnosticCommand", {
          value: command,
          enumerable: false,
        });
      }
      throw error;
    }
  }
}

function isContextDestroyed(error) {
  return error instanceof ControllerError &&
    error.diagnosticReason === "process_exit_context_destroyed";
}

function isExpectedTaskPageClosure(error) {
  return error instanceof ControllerError &&
    error.diagnosticCommand === "tab-close" &&
    error.diagnosticReason === "process_exit_page_closed";
}

async function abortableDelay(milliseconds, signal) {
  if (signal?.aborted) throw clientUnavailableError();
  await new Promise((resolve, reject) => {
    const timer = setTimeout(() => finish(resolve), milliseconds);
    const abort = () => finish(() => reject(clientUnavailableError()));
    const finish = (callback) => {
      clearTimeout(timer);
      signal?.removeEventListener("abort", abort);
      callback();
    };
    signal?.addEventListener("abort", abort, { once: true });
    timer.unref?.();
  });
}

export function createProviderRuntime(client, registration) {
  return {
    timeoutMs: registration.timeoutMS,
    withTaskTab: async (operation, callback) => {
      if (operation !== registration.operation) throw clientContractError();
      return await callback(
        registration.provider === "qq_mail" ? client.qqTask() : client.outlookTab(),
      );
    },
    createOwnedTab: async () => client.gmailTab(),
  };
}

export function parseTabs(raw) {
  if (raw === "No open tabs. Navigate to a URL to create one.") return [];
  const tabs = [];
  for (const line of raw.split("\n")) {
    if (!line.trim()) continue;
    tabs.push(parseTabLine(line, tabs.length));
  }
  if (tabs.length === 0) throw clientContractError();
  return tabs;
}

function parseJSON(raw) {
  try {
    return JSON.parse(raw);
  } catch {
    throw clientContractError();
  }
}

function sanitizeAttachOutput(raw, expectedSession, expectedEndpoint) {
  const parsed = parseJSON(raw);
  const keys = parsed && typeof parsed === "object" && !Array.isArray(parsed)
    ? Object.keys(parsed).sort()
    : [];
  if (
    keys.length !== 4 ||
    keys[0] !== "endpoint" ||
    keys[1] !== "pid" ||
    keys[2] !== "result" ||
    keys[3] !== "session" ||
    parsed.session !== expectedSession ||
    !Number.isSafeInteger(parsed.pid) ||
    parsed.pid <= 1 ||
    parsed.endpoint !== expectedEndpoint
  ) {
    throw clientContractError();
  }
  return JSON.stringify({
    session: parsed.session,
    pid: parsed.pid,
    endpoint: parsed.endpoint,
  });
}

function sanitizeTabListOutput(raw, token) {
  const normalized = raw.trim();
  if (normalized === "No open tabs. Navigate to a URL to create one.") return normalized;
  let connectPages = 0;
  const sanitized = [];
  for (const line of normalized.split("\n")) {
    if (!line.trim()) continue;
    const tab = parseTabLine(line, sanitized.length);
    if (isExtensionConnectURL(tab.url, token)) {
      tab.url = EXTENSION_CONNECT_URL;
      connectPages += 1;
    }
    sanitized.push(renderTabLine(tab));
  }
  if (connectPages > 1) throw clientContractError();
  return sanitized.join("\n");
}

function isExtensionConnectURL(rawURL, token) {
  let parsed;
  try {
    parsed = new URL(rawURL);
  } catch {
    return false;
  }
  if (
    parsed.protocol !== "chrome-extension:" ||
    parsed.hostname !== EXTENSION_ID ||
    parsed.pathname !== "/connect.html" ||
    parsed.username ||
    parsed.password ||
    parsed.hash ||
    parsed.searchParams.size !== 4 ||
    parsed.searchParams.getAll("mcpRelayUrl").length !== 1 ||
    parsed.searchParams.getAll("client").length !== 1 ||
    parsed.searchParams.getAll("protocolVersion").length !== 1 ||
    parsed.searchParams.getAll("token").length !== 1 ||
    parsed.searchParams.get("protocolVersion") !== "2" ||
    parsed.searchParams.get("token") !== token
  ) {
    return false;
  }
  let client;
  let relay;
  try {
    client = JSON.parse(parsed.searchParams.get("client"));
    relay = new URL(parsed.searchParams.get("mcpRelayUrl"));
  } catch {
    return false;
  }
  return Boolean(
    client &&
    typeof client === "object" &&
    !Array.isArray(client) &&
    Object.keys(client).length === 1 &&
    client.name === "playwright-cli" &&
    relay.protocol === "ws:" &&
    ["127.0.0.1", "[::1]"].includes(relay.hostname) &&
    /^[1-9][0-9]{0,4}$/u.test(relay.port) &&
    Number(relay.port) <= 65535 &&
    !relay.username &&
    !relay.password &&
    !relay.search &&
    !relay.hash &&
    RELAY_PATH_PATTERN.test(relay.pathname)
  );
}

function parseTabLine(line, expectedIndex) {
  const match = /^- ([0-9]+):( \(current\))? \[(.*)\]\((.*)\)( \[crashed\])?$/u.exec(line);
  if (!match || Number(match[1]) !== expectedIndex) throw clientContractError();
  return {
    index: Number(match[1]),
    current: Boolean(match[2]),
    title: match[3],
    url: match[4],
    crashed: Boolean(match[5]),
  };
}

function renderTabLine(tab) {
  const current = tab.current ? " (current)" : "";
  const crashed = tab.crashed ? " [crashed]" : "";
  return `- ${tab.index}:${current} [${tab.title}](${tab.url})${crashed}`;
}

function assertExpectedOrigin(rawURL, expectedOrigin, allowedOrigins) {
  let parsed;
  try {
    parsed = new URL(rawURL);
  } catch {
    throw pageStale("page_invalid_url");
  }
  if (
    parsed.username ||
    parsed.password
  ) {
    throw pageStale("page_url_credentials");
  }
  if (parsed.protocol !== "https:") {
    throw pageStale(
      parsed.protocol === "chrome-extension:"
        ? "page_extension_origin"
        : "page_non_https_origin",
    );
  }
  if (!allowedOrigins.includes(parsed.origin)) {
    throw pageStale(unregisteredOriginReason(parsed.hostname));
  }
  if (expectedOrigin && parsed.origin !== expectedOrigin) {
    throw pageStale("page_origin_mismatch");
  }
  return parsed;
}

function unregisteredOriginReason(hostname) {
  const normalized = hostname.toLowerCase();
  if (normalized === "workspace.google.com") return "page_google_workspace_origin";
  if (normalized === "www.google.com") return "page_google_www_origin";
  if (normalized === "myaccount.google.com") return "page_google_myaccount_origin";
  if (normalized === "google.com" || normalized.endsWith(".google.com")) {
    return "page_google_other_origin";
  }
  if (
    normalized === "microsoft.com" ||
    normalized.endsWith(".microsoft.com") ||
    normalized === "microsoftonline.com" ||
    normalized.endsWith(".microsoftonline.com") ||
    normalized === "live.com" ||
    normalized.endsWith(".live.com") ||
    normalized === "office.com" ||
    normalized.endsWith(".office.com") ||
    normalized === "office365.com" ||
    normalized.endsWith(".office365.com")
  ) {
    return "page_unregistered_microsoft_origin";
  }
  if (normalized === "qq.com" || normalized.endsWith(".qq.com")) {
    return "page_unregistered_qq_origin";
  }
  return "page_unregistered_other_origin";
}

function tabFingerprint(tab) {
  return `${tab.title}\0${tab.url}\0${tab.crashed ? "1" : "0"}`;
}

function sameFingerprintList(left, right) {
  return left.length === right.length && left.every((value, index) => value === right[index]);
}
