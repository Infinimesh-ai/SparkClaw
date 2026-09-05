import { spawn } from "node:child_process";
import crypto from "node:crypto";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

import { ControllerError, invalidRequest } from "./errors.mjs";
import { BACKGROUND_CLICK_FUNCTION } from "./dom-actions.mjs";
import { parseID, requireExactObject } from "./protocol.mjs";

const PACKAGE_ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const DEFAULT_MCP_ENTRY = path.join(PACKAGE_ROOT, "node_modules", "@playwright", "mcp", "cli.js");
const MCP_PROTOCOL_VERSION = "2025-06-18";
const PROCESS_EXIT_GRACE_MS = 1500;
const MAX_MCP_RESPONSE_BYTES = 8 << 20;
const MAX_MCP_OUTPUT_BYTES = 8 << 20;
const MAX_PAGE_TEXT_CHARS = 120_000;
const MAX_INPUT_TEXT_BYTES = 24 << 10;
const MAX_TASK_PAGES = 16;
const PLAYWRIGHT_REF_PATTERN = /^e[1-9][0-9]*$/u;
const SESSION_OUTPUT_PATTERN = /^session-[0-9a-f]{24}$/u;
const BRIDGE_REJECTION_MARKER = "browser_extension_rejected";
const BRIDGE_CONNECT_URL_PREFIX = "chrome-extension://mmlmfjhmonkocbjadbfplnigmagldckm/connect.html?";
const RELAY_DEBUG_NAMESPACE = "pw:mcp:relay";
const REQUIRED_TOOLS = new Set([
  "browser_click",
  "browser_evaluate",
  "browser_navigate",
  "browser_select_option",
  "browser_snapshot",
  "browser_tabs",
  "browser_take_screenshot",
  "browser_type",
  "browser_wait_for",
]);
const PAGE_INFO_FUNCTION = "() => ({ url: location.href, title: document.title, ready_state: document.readyState })";
const PAGE_READ_FUNCTION = "() => ({ url: location.href, title: document.title, ready_state: document.readyState, lang: document.documentElement?.lang || '', text: (document.body?.innerText || document.documentElement?.innerText || '').slice(0, 120000), html: (document.documentElement?.outerHTML || '').slice(0, 120000), scroll_height: document.documentElement?.scrollHeight || 0 })";
const BRIDGE_HANDOFF_FUNCTION = "() => \"sparkclaw-browser-bridge-handoff-v1\"";

export class PlaywrightMCPClientFactory {
  constructor(options = {}) {
    this.entryPoint = options.entryPoint ?? DEFAULT_MCP_ENTRY;
    this.cwd = options.cwd ?? PACKAGE_ROOT;
    this.browserChannel = options.browserChannel ?? "chromium";
    this.executablePath = options.executablePath ?? "";
    this.userDataDir = options.userDataDir ?? "";
    this.actionTimeoutMS = options.actionTimeoutMS ?? 10_000;
    this.navigationTimeoutMS = options.navigationTimeoutMS ?? 30_000;
    this.connectTimeoutMS = options.connectTimeoutMS ?? 15_000;
    this.spawn = options.spawn ?? spawn;
    this.extraEnv = options.extraEnv ?? {};
    this.outputRoot = options.outputRoot ?? path.join(os.tmpdir(), "sparkclaw-browser-controller", "mcp-output");
    validateOutputRoot(this.outputRoot);
    this.outputReady = prepareOutputRoot(this.outputRoot);
  }

  info() {
    return {
      client: "playwright-mcp",
      client_version: "0.0.80",
      playwright_version: "1.63.0-alpha-2026-08-31",
      browser_channel: this.browserChannel,
    };
  }

  async prepare() {
    await this.outputReady;
  }

  async open({ token, sessionID }) {
    await this.outputReady;
    const outputDir = await createSessionOutputDir(this.outputRoot, sessionID);
    const args = [
      this.entryPoint,
      "--extension",
      "--browser",
      this.browserChannel,
      "--codegen",
      "none",
      "--image-responses",
      "allow",
      "--snapshot-mode",
      "none",
      "--output-dir",
      outputDir,
      "--output-max-size",
      String(MAX_MCP_OUTPUT_BYTES),
      "--timeout-action",
      String(this.actionTimeoutMS),
      "--timeout-navigation",
      String(this.navigationTimeoutMS),
      "--timeout-settle",
      "500",
    ];
    if (this.executablePath) args.push("--executable-path", this.executablePath);
    if (this.userDataDir) args.push("--user-data-dir", this.userDataDir);

    const env = scrubPlaywrightEnvironment({ ...process.env, ...this.extraEnv });
    env.DEBUG = RELAY_DEBUG_NAMESPACE;
    env.PLAYWRIGHT_MCP_EXTENSION_TOKEN = token;

    let child;
    try {
      child = this.spawn(process.execPath, args, {
        cwd: this.cwd,
        env,
        stdio: ["pipe", "pipe", "pipe"],
        windowsHide: true,
      });
    } catch (error) {
      await removeSessionOutputDir(outputDir);
      throw clientError(error);
    }

    const rpc = new StdioJSONRPC(child, this.connectTimeoutMS);
    const client = new PlaywrightMCPClient(child, rpc, outputDir);
    try {
      await rpc.request("initialize", {
        protocolVersion: MCP_PROTOCOL_VERSION,
        capabilities: {},
        clientInfo: {
          name: "sparkclaw-browser-controller",
          version: "0.1.0",
        },
      });
      rpc.notify("notifications/initialized", {});
      await client.verifyToolCatalog();
      return client;
    } catch (error) {
      await client.close();
      throw clientError(error, sessionID);
    }
  }
}

export class PlaywrightMCPClient {
  constructor(child, rpc, outputDir) {
    this.child = child;
    this.rpc = rpc;
    this.outputDir = outputDir;
    this.ownerTabs = null;
    this.pages = new Map();
    this.currentPageID = "";
    this.nextPageID = 1;
    this.bridgeConnectionPage = false;
    this.closePromise = null;
  }

  get closed() {
    return this.rpc.closed;
  }

  async verifyToolCatalog() {
    const result = await this.rpc.request("tools/list", {});
    const names = new Set(Array.isArray(result?.tools) ? result.tools.map((tool) => tool?.name) : []);
    for (const name of REQUIRED_TOOLS) {
      if (!names.has(name)) {
        throw new ControllerError("browser_extension_unavailable", "browser extension is unavailable", {
          status: 503,
          retryable: true,
        });
      }
    }
  }

  async createTaskPage() {
    if (this.pages.size > 0) {
      throw new ControllerError("browser_session_invalid", "browser session is invalid", {
        status: 409,
      });
    }
    await this.#newTaskPage("about:blank");
  }

  async closeTaskPage() {
    const pages = [...this.pages.values()].sort((left, right) => right.index - left.index);
    for (const page of pages) {
      try {
        await this.#closePage(page.pageID);
      } catch {
        // Process teardown still detaches the MCP client if a page changed underneath cleanup.
      }
    }
    this.pages.clear();
    this.currentPageID = "";
    await this.#closeBridgeConnectionPage();
  }

  async execute(operation, args) {
    switch (operation) {
      case "tabs.list":
        exactArgs(args, []);
        return this.#listOwnedPages();
      case "tabs.new":
        exactArgs(args, [], ["url"]);
        return this.#newTaskPage(optionalURL(args.url, { allowBlank: true }));
      case "tabs.select":
        exactArgs(args, ["page_id"]);
        await this.#selectPage(requiredPageID(args.page_id));
        return this.#pageInfo(args.page_id);
      case "tabs.handoff":
        exactArgs(args, ["page_id"]);
        return this.#handoff(requiredPageID(args.page_id));
      case "tabs.close":
        exactArgs(args, [], ["page_id"]);
        return this.#closePage(optionalPageID(args.page_id) || this.currentPageID);
      case "page.info":
        exactArgs(args, [], ["page_id"]);
        return this.#pageInfo(optionalPageID(args.page_id));
      case "page.read":
        exactArgs(args, [], ["page_id", "max_chars"]);
        return this.#readPage(optionalPageID(args.page_id), optionalMaximum(args.max_chars));
      case "page.navigate":
        exactArgs(args, ["url"], ["page_id"]);
        return this.#navigate(optionalPageID(args.page_id), requiredURL(args.url));
      case "page.reload":
        exactArgs(args, [], ["page_id"]);
        return this.#reload(optionalPageID(args.page_id));
      case "page.snapshot":
        exactArgs(args, [], ["page_id", "depth", "boxes"]);
        return this.#snapshot(optionalPageID(args.page_id), args);
      case "page.click":
        exactArgs(args, ["ref"], ["page_id", "double_click", "button", "modifiers"]);
        return this.#click(optionalPageID(args.page_id), args);
      case "page.fill":
        exactArgs(args, ["ref", "text"], ["page_id", "submit"]);
        return this.#type(optionalPageID(args.page_id), args, false);
      case "page.type":
        exactArgs(args, ["text"], ["page_id", "ref", "focused", "submit"]);
        return this.#type(optionalPageID(args.page_id), args, true);
      case "page.select":
        exactArgs(args, ["ref"], ["page_id", "value", "values"]);
        return this.#selectOption(optionalPageID(args.page_id), args);
      case "page.wait":
        exactArgs(args, [], ["page_id", "duration_ms", "text", "text_gone"]);
        return this.#wait(optionalPageID(args.page_id), args);
      case "page.screenshot":
        exactArgs(args, [], ["page_id", "full_page", "type"]);
        return this.#screenshot(optionalPageID(args.page_id), args);
      default:
        throw new ControllerError("browser_operation_unavailable", "browser operation is unavailable", {
          status: 400,
        });
    }
  }

  async close() {
    if (this.closePromise) return this.closePromise;
    this.closePromise = this.#close();
    return this.closePromise;
  }

  async #newTaskPage(url = "") {
    if (this.pages.size >= MAX_TASK_PAGES) throw invalidRequest("too many task pages");
    const before = await this.#providerTabs();
    if (this.ownerTabs === null) {
      this.ownerTabs = before.map(tabFingerprint);
      this.bridgeConnectionPage = before.length === 1 && isBridgeConnectionPage(before[0]);
    }
    else this.#assertTopology(before);

    const result = await this.#callJSONTool("browser_tabs", {
      action: "new",
      ...(url ? { url } : {}),
    });
    const after = tabsFromPayload(result.payload);
    const inserted = findCurrentInsertion(before, after);
    if (inserted < 0) throw pageStale("task page creation was ambiguous");
    for (const page of this.pages.values()) {
      if (page.index >= inserted) page.index++;
    }
    const pageID = `page_${this.nextPageID++}`;
    this.pages.set(pageID, { pageID, index: inserted, refs: null });
    this.currentPageID = pageID;
    this.#invalidateSnapshots();
    return this.#ownedPages(after);
  }

  async #closePage(candidate) {
    const pageID = requiredPageID(candidate);
    const page = this.#requirePage(pageID);
    const before = await this.#providerTabs();
    this.#assertTopology(before);
    const result = await this.#callJSONTool("browser_tabs", { action: "close", index: page.index });
    const after = tabsFromPayload(result.payload);
    if (!sameTabsAfterRemoval(before, after, page.index)) {
      throw pageStale("task page closure was ambiguous");
    }
    this.pages.delete(pageID);
    for (const other of this.pages.values()) {
      if (other.index > page.index) other.index--;
    }
    this.currentPageID = currentOwnedPageID(this.pages, after);
    this.#invalidateSnapshots();
    return this.#ownedPages(after);
  }

  async #listOwnedPages() {
    const tabs = await this.#providerTabs();
    this.#assertTopology(tabs);
    this.currentPageID = currentOwnedPageID(this.pages, tabs);
    return this.#ownedPages(tabs);
  }

  async #closeBridgeConnectionPage() {
    if (!this.bridgeConnectionPage) return;
    const tabs = await this.#providerTabs();
    if (tabs.length !== 1 || !isBridgeConnectionPage(tabs[0])) return;
    this.bridgeConnectionPage = false;
    await this.#callJSONTool("browser_tabs", { action: "close", index: 0 });
  }

  async #selectPage(candidate) {
    const pageID = candidate || this.currentPageID;
    const page = this.#requirePage(requiredPageID(pageID));
    const tabs = await this.#providerTabs();
    this.#assertTopology(tabs);
    if (!tabs[page.index]?.current) {
      const result = await this.#callJSONTool("browser_tabs", { action: "select", index: page.index });
      const selected = tabsFromPayload(result.payload);
      this.#assertTopology(selected);
      if (!selected[page.index]?.current) throw pageStale("task page selection was not confirmed");
      this.#invalidateSnapshots();
    }
    this.currentPageID = page.pageID;
    return page;
  }

  async #pageInfo(candidate) {
    const page = await this.#selectPage(candidate);
    const result = await this.#evaluate(PAGE_INFO_FUNCTION);
    return { page: { page_id: page.pageID, ...normalizePageInfo(result) } };
  }

  async #handoff(candidate) {
    try {
      const page = await this.#selectPage(candidate);
      const marker = await this.#callJSONTool("browser_evaluate", { function: BRIDGE_HANDOFF_FUNCTION });
      if (parseJSONResult(marker.payload.result) !== "sparkclaw-browser-bridge-handoff-v1") {
        throw clientContractError();
      }
      const result = await this.#callJSONTool("browser_tabs", { action: "select", index: page.index });
      const selected = tabsFromPayload(result.payload);
      this.#assertTopology(selected);
      if (!selected[page.index]?.current) throw pageStale("task page handoff was not confirmed");
      this.currentPageID = page.pageID;
      this.#invalidateSnapshots();
      return await this.#pageInfo(page.pageID);
    } catch (error) {
      throw error;
    }
  }

  async #readPage(candidate, maximum) {
    const page = await this.#selectPage(candidate);
    const result = normalizePageRead(await this.#evaluate(PAGE_READ_FUNCTION), maximum);
    return { page: { page_id: page.pageID, ...result } };
  }

  async #navigate(candidate, url) {
    const page = await this.#selectPage(candidate);
    await this.#callJSONTool("browser_navigate", { url });
    this.#invalidateSnapshots();
    return this.#pageInfo(page.pageID);
  }

  async #reload(candidate) {
    const page = await this.#selectPage(candidate);
    const info = normalizePageInfo(await this.#evaluate(PAGE_INFO_FUNCTION));
    const url = observedURL(info.url);
    await this.#callJSONTool("browser_navigate", { url });
    this.#invalidateSnapshots();
    return this.#pageInfo(page.pageID);
  }

  async #snapshot(candidate, args) {
    const page = await this.#selectPage(candidate);
    const depth = optionalInteger(args.depth, "depth", 1, 64);
    const boxes = optionalBoolean(args.boxes, "boxes");
    const result = await this.#callJSONTool("browser_snapshot", {
      ...(depth === undefined ? {} : { depth }),
      ...(boxes === undefined ? {} : { boxes }),
    });
    const snapshot = result.payload.snapshot;
    if (snapshot === undefined) throw clientContractError();
    const refs = collectSnapshotRefs(snapshot);
    page.refs = refs;
    return {
      page: { page_id: page.pageID, ...pageInfoFromPayload(result.payload) },
      snapshot,
      refs: [...refs].sort(comparePlaywrightRefs),
    };
  }

  async #click(candidate, args) {
    const page = await this.#requireFreshRef(candidate, args.ref);
    if (optionalBoolean(args.double_click, "double_click") ||
        optionalEnum(args.button, "button", ["left", "right", "middle"]) ||
        optionalStringArray(args.modifiers, "modifiers", ["Alt", "Control", "ControlOrMeta", "Meta", "Shift"])) {
      throw invalidRequest("background click supports only an unmodified left click");
    }
    const result = await this.#callJSONTool("browser_evaluate", {
      target: requiredRef(args.ref),
      function: BACKGROUND_CLICK_FUNCTION,
    });
    if (parseJSONResult(result.payload.result) !== true) throw clientContractError();
    this.#invalidateSnapshots();
    return this.#pageInfo(page.pageID);
  }

  async #type(candidate, args, slowly) {
    const focused = optionalBoolean(args.focused, "focused") === true;
    if (focused && args.ref !== undefined) throw invalidRequest("focused typing cannot include ref");
    if (!focused && args.ref === undefined) throw invalidRequest("page.type requires ref or focused mode");
    const page = focused ? await this.#selectPage(candidate) : await this.#requireFreshRef(candidate, args.ref);
    await this.#callJSONTool("browser_type", {
      target: focused ? ":focus" : requiredRef(args.ref),
      text: requiredText(args.text),
      ...(optionalBoolean(args.submit, "submit") ? { submit: true } : {}),
      ...(slowly ? { slowly: true } : {}),
    });
    this.#invalidateSnapshots();
    return this.#pageInfo(page.pageID);
  }

  async #selectOption(candidate, args) {
    const page = await this.#requireFreshRef(candidate, args.ref);
    const values = selectValues(args);
    await this.#callJSONTool("browser_select_option", { target: requiredRef(args.ref), values });
    this.#invalidateSnapshots();
    return this.#pageInfo(page.pageID);
  }

  async #wait(candidate, args) {
    const page = await this.#selectPage(candidate);
    const durationMS = optionalInteger(args.duration_ms, "duration_ms", 1, 30_000);
    const text = optionalText(args.text, "text");
    const textGone = optionalText(args.text_gone, "text_gone");
    if (durationMS === undefined && !text && !textGone) {
      throw invalidRequest("page.wait requires duration_ms, text, or text_gone");
    }
    await this.#callJSONTool("browser_wait_for", {
      ...(durationMS === undefined ? {} : { time: durationMS / 1000 }),
      ...(text ? { text } : {}),
      ...(textGone ? { textGone } : {}),
    });
    return this.#pageInfo(page.pageID);
  }

  async #screenshot(candidate, args) {
    const page = await this.#selectPage(candidate);
    const type = optionalEnum(args.type, "type", ["png", "jpeg", "webp"]) || "png";
    const result = await this.#callJSONTool("browser_take_screenshot", {
      type,
      scale: "css",
      ...(optionalBoolean(args.full_page, "full_page") ? { fullPage: true } : {}),
    });
    const image = result.images[0];
    if (!image || image.mimeType !== `image/${type}`) throw clientContractError();
    return {
      page: { page_id: page.pageID, ...pageInfoFromPayload(result.payload) },
      screenshot: { mime_type: image.mimeType, data_base64: image.data },
    };
  }

  async #requireFreshRef(candidate, ref) {
    const page = await this.#selectPage(candidate);
    const rawRef = requiredRef(ref);
    if (!page.refs?.has(rawRef)) throw pageStale("snapshot reference is stale or unknown");
    return page;
  }

  async #evaluate(fn) {
    const result = await this.#callJSONTool("browser_evaluate", { function: fn });
    if (typeof result.payload.result !== "string") throw clientContractError();
    try {
      return JSON.parse(result.payload.result);
    } catch {
      throw clientContractError();
    }
  }

  async #providerTabs() {
    const result = await this.#callJSONTool("browser_tabs", { action: "list" });
    return tabsFromPayload(result.payload);
  }

  #ownedPages(tabs) {
    this.#assertTopology(tabs);
    const pages = [...this.pages.values()]
      .sort((left, right) => left.pageID.localeCompare(right.pageID, "en", { numeric: true }))
      .map((page) => {
        const tab = tabs[page.index];
        return {
          page_id: page.pageID,
          url: tab.url,
          title: tab.title,
          selected: tab.current,
          crashed: tab.crashed,
        };
      });
    return { pages };
  }

  #assertTopology(tabs) {
    if (this.ownerTabs === null) return;
    if (tabs.length !== this.ownerTabs.length + this.pages.size) {
      throw pageStale("browser tab topology changed outside the active task");
    }
    const taskIndices = new Set();
    for (const page of this.pages.values()) {
      if (!Number.isSafeInteger(page.index) || page.index < 0 || page.index >= tabs.length || taskIndices.has(page.index)) {
        throw pageStale("task page ownership is invalid");
      }
      taskIndices.add(page.index);
    }
    const owners = tabs.filter((_, index) => !taskIndices.has(index)).map(tabFingerprint);
    if (!sameFingerprintList(owners, this.ownerTabs)) {
      throw pageStale("owner browser tabs changed during the active task");
    }
  }

  #requirePage(pageID) {
    const page = this.pages.get(pageID);
    if (!page) throw pageStale("task page was not found");
    return page;
  }

  #invalidateSnapshots() {
    for (const page of this.pages.values()) page.refs = null;
  }

  async #callJSONTool(name, args) {
    const result = await this.#callTool(name, { ...args, _meta: { json: true } });
    const text = result?.content?.find((item) => item?.type === "text")?.text;
    if (typeof text !== "string" || Buffer.byteLength(text, "utf8") > MAX_MCP_RESPONSE_BYTES) {
      throw clientContractError();
    }
    let payload;
    try {
      payload = JSON.parse(text);
    } catch {
      throw clientContractError();
    }
    if (!payload || typeof payload !== "object" || Array.isArray(payload)) throw clientContractError();
    const images = result.content
      .filter((item) => item?.type === "image" && typeof item.data === "string" && typeof item.mimeType === "string")
      .map((item) => ({ data: item.data, mimeType: item.mimeType }));
    return { payload, images };
  }

  async #callTool(name, args) {
    const result = await this.rpc.request("tools/call", { name, arguments: args });
    if (result?.isError) {
      throw new ControllerError("browser_extension_unavailable", "browser extension is unavailable", {
        status: 503,
        retryable: true,
      });
    }
    return result;
  }

  async #close() {
    try {
      try {
        await this.closeTaskPage();
      } catch {
        // Cleanup continues by terminating and reaping the MCP subprocess.
      }
      this.rpc.closeInput();
      if (await waitForExit(this.child, PROCESS_EXIT_GRACE_MS)) return;
      this.child.kill("SIGTERM");
      if (await waitForExit(this.child, PROCESS_EXIT_GRACE_MS)) return;
      this.child.kill("SIGKILL");
      await waitForExit(this.child, PROCESS_EXIT_GRACE_MS);
    } finally {
      await removeSessionOutputDir(this.outputDir);
    }
  }
}

function parseJSONResult(value) {
  if (typeof value !== "string") throw clientContractError();
  try {
    return JSON.parse(value);
  } catch {
    throw clientContractError();
  }
}

class StdioJSONRPC {
  constructor(child, requestTimeoutMS) {
    this.child = child;
    this.requestTimeoutMS = requestTimeoutMS;
    this.nextID = 1;
    this.pending = new Map();
    this.buffer = "";
    this.failure = null;
    this.closed = new Promise((resolve) => {
      child.once("exit", (code, signal) => {
        this.#failAll(new Error(`MCP process exited: ${code ?? signal ?? "unknown"}`));
        resolve({ code, signal });
      });
    });
    child.once("error", (error) => this.#failAll(error));
    child.stdout.setEncoding("utf8");
    child.stdout.on("data", (chunk) => this.#onData(chunk));
    const rejectionDetector = new StreamingMarkerDetector(BRIDGE_REJECTION_MARKER);
    child.stderr.on("data", (chunk) => {
      if (rejectionDetector.push(chunk)) this.#failAll(extensionRejected());
    });
  }

  request(method, params) {
    if (this.failure) return Promise.reject(this.failure);
    const id = this.nextID++;
    return new Promise((resolve, reject) => {
      const timeout = setTimeout(() => {
        this.pending.delete(id);
        reject(new Error(`MCP request timed out: ${method}`));
      }, this.requestTimeoutMS);
      timeout.unref?.();
      this.pending.set(id, { resolve, reject, timeout });
      this.#write({ jsonrpc: "2.0", id, method, params });
    });
  }

  notify(method, params) {
    if (this.failure) return;
    this.#write({ jsonrpc: "2.0", method, params });
  }

  closeInput() {
    if (!this.child.stdin.destroyed) this.child.stdin.end();
  }

  #write(message) {
    try {
      this.child.stdin.write(`${JSON.stringify(message)}\n`);
    } catch (error) {
      this.#failAll(error);
    }
  }

  #onData(chunk) {
    this.buffer += chunk;
    if (Buffer.byteLength(this.buffer, "utf8") > MAX_MCP_RESPONSE_BYTES) {
      this.#failAll(new Error("MCP response exceeded the size limit"));
      return;
    }
    let newline = this.buffer.indexOf("\n");
    while (newline >= 0) {
      const line = this.buffer.slice(0, newline).trim();
      this.buffer = this.buffer.slice(newline + 1);
      if (line) this.#onLine(line);
      newline = this.buffer.indexOf("\n");
    }
  }

  #onLine(line) {
    let message;
    try {
      message = JSON.parse(line);
    } catch {
      this.#failAll(new Error("MCP stdout was not valid JSON"));
      return;
    }
    if (!Number.isSafeInteger(message.id)) return;
    const pending = this.pending.get(message.id);
    if (!pending) return;
    this.pending.delete(message.id);
    clearTimeout(pending.timeout);
    if (message.error) pending.reject(new Error("MCP request failed"));
    else pending.resolve(message.result);
  }

  #failAll(error) {
    if (this.failure) return;
    this.failure = error;
    for (const pending of this.pending.values()) {
      clearTimeout(pending.timeout);
      pending.reject(error);
    }
    this.pending.clear();
  }
}

function scrubPlaywrightEnvironment(env) {
  for (const key of Object.keys(env)) {
    if (key.startsWith("PLAYWRIGHT_MCP_")) delete env[key];
  }
  return env;
}

async function createSessionOutputDir(outputRoot, sessionID) {
  const normalized = parseID(sessionID, "session_id");
  const digest = crypto.createHash("sha256").update(normalized).digest("hex").slice(0, 24);
  await fs.mkdir(outputRoot, { recursive: true, mode: 0o700 });
  await fs.chmod(outputRoot, 0o700);
  const outputDir = path.join(outputRoot, `session-${digest}`);
  await fs.mkdir(outputDir, { mode: 0o700 });
  return outputDir;
}

function validateOutputRoot(outputRoot) {
  if (
    !path.isAbsolute(outputRoot) ||
    path.basename(outputRoot) !== "mcp-output" ||
    path.dirname(outputRoot) === path.parse(outputRoot).root
  ) {
    throw new TypeError("outputRoot must be an absolute mcp-output directory below a private runtime directory");
  }
}

async function prepareOutputRoot(outputRoot) {
  await fs.mkdir(outputRoot, { recursive: true, mode: 0o700 });
  const stat = await fs.lstat(outputRoot);
  if (!stat.isDirectory() || stat.isSymbolicLink()) {
    throw new TypeError("outputRoot must be a real directory");
  }
  await fs.chmod(outputRoot, 0o700);
  for (const entry of await fs.readdir(outputRoot, { withFileTypes: true })) {
    if (entry.isDirectory() && SESSION_OUTPUT_PATTERN.test(entry.name)) {
      await fs.rm(path.join(outputRoot, entry.name), { recursive: true, force: true });
    }
  }
}

async function removeSessionOutputDir(outputDir) {
  if (!outputDir) return;
  await fs.rm(outputDir, { recursive: true, force: true }).catch(() => {});
}

function clientError(error) {
  if (error instanceof ControllerError) return error;
  return new ControllerError("browser_extension_unavailable", "browser extension is unavailable", {
    status: 503,
    retryable: true,
    cause: error,
  });
}

function extensionRejected() {
  return new ControllerError("browser_extension_rejected", "browser extension rejected the credential", {
    status: 401,
    retryable: false,
  });
}

class StreamingMarkerDetector {
  constructor(marker) {
    this.marker = Buffer.from(marker, "ascii");
    this.offset = 0;
    this.found = false;
  }

  push(chunk) {
    if (this.found) return true;
    const bytes = Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk);
    for (const byte of bytes) {
      if (byte === this.marker[this.offset]) {
        this.offset++;
        if (this.offset === this.marker.length) {
          this.found = true;
          return true;
        }
      } else {
        this.offset = byte === this.marker[0] ? 1 : 0;
      }
    }
    return false;
  }
}

function exactArgs(args, required, optional = []) {
  requireExactObject(args, required, optional);
}

function requiredPageID(value) {
  return parseID(value, "page_id");
}

function optionalPageID(value) {
  return value === undefined ? "" : requiredPageID(value);
}

function requiredRef(value) {
  if (typeof value !== "string" || !PLAYWRIGHT_REF_PATTERN.test(value)) {
    throw invalidRequest("ref is invalid");
  }
  return value;
}

function requiredText(value) {
  if (typeof value !== "string" || Buffer.byteLength(value, "utf8") > MAX_INPUT_TEXT_BYTES) {
    throw invalidRequest("text is invalid");
  }
  return value;
}

function optionalText(value, field) {
  if (value === undefined) return "";
  if (typeof value !== "string" || value.length === 0 || Buffer.byteLength(value, "utf8") > MAX_INPUT_TEXT_BYTES) {
    throw invalidRequest(`${field} is invalid`);
  }
  return value;
}

function optionalBoolean(value, field) {
  if (value === undefined) return undefined;
  if (typeof value !== "boolean") throw invalidRequest(`${field} is invalid`);
  return value;
}

function optionalInteger(value, field, minimum, maximum) {
  if (value === undefined) return undefined;
  if (!Number.isSafeInteger(value) || value < minimum || value > maximum) {
    throw invalidRequest(`${field} is invalid`);
  }
  return value;
}

function optionalMaximum(value) {
  return optionalInteger(value, "max_chars", 1, MAX_PAGE_TEXT_CHARS) ?? MAX_PAGE_TEXT_CHARS;
}

function optionalEnum(value, field, allowed) {
  if (value === undefined) return "";
  if (typeof value !== "string" || !allowed.includes(value)) throw invalidRequest(`${field} is invalid`);
  return value;
}

function optionalStringArray(value, field, allowed) {
  if (value === undefined) return undefined;
  if (!Array.isArray(value) || value.length > allowed.length || value.some((item) => !allowed.includes(item))) {
    throw invalidRequest(`${field} is invalid`);
  }
  return [...new Set(value)];
}

function requiredURL(value) {
  return optionalURL(value, { required: true });
}

function optionalURL(value, { required = false, allowBlank = false } = {}) {
  if (value === undefined && !required) return "";
  if (typeof value !== "string" || value.length === 0 || Buffer.byteLength(value, "utf8") > 4096) {
    throw invalidRequest("url is invalid");
  }
  if (allowBlank && value === "about:blank") return value;
  let parsed;
  try {
    parsed = new URL(value);
  } catch {
    throw invalidRequest("url is invalid");
  }
  if (parsed.protocol !== "http:" && parsed.protocol !== "https:") throw invalidRequest("url is invalid");
  return parsed.href;
}

function selectValues(args) {
  if (args.value !== undefined && args.values !== undefined) throw invalidRequest("select values are invalid");
  const values = args.values ?? (args.value === undefined ? [] : [args.value]);
  if (!Array.isArray(values) || values.length === 0 || values.length > 32) {
    throw invalidRequest("select values are invalid");
  }
  return values.map(requiredText);
}

function tabsFromPayload(payload) {
  const markdown = typeof payload.result === "string" ? payload.result : payload.tabs;
  if (typeof markdown !== "string") throw clientContractError();
  if (markdown.trim() === "No open tabs. Navigate to a URL to create one.") return [];
  const tabs = [];
  for (const line of markdown.split("\n")) {
    if (!line.trim()) continue;
    const match = /^- ([0-9]+):( \(current\))? \[(.*)\]\((.*)\)( \[crashed\])?$/u.exec(line);
    if (!match || Number(match[1]) !== tabs.length) throw clientContractError();
    tabs.push({
      index: Number(match[1]),
      current: Boolean(match[2]),
      title: match[3],
      url: match[4],
      crashed: Boolean(match[5]),
    });
  }
  if (tabs.length === 0) throw clientContractError();
  return tabs;
}

function pageInfoFromPayload(payload) {
  if (typeof payload.page !== "string") return {};
  const info = {};
  for (const line of payload.page.split("\n")) {
    if (line.startsWith("- Page URL: ")) info.url = line.slice(12);
    if (line.startsWith("- Page Title: ")) info.title = line.slice(14);
    if (line === "- Page status: crashed") info.crashed = true;
  }
  return info;
}

function normalizePageInfo(value) {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw clientContractError();
  const url = observedURL(value.url);
  if (typeof value.title !== "string" || typeof value.ready_state !== "string") throw clientContractError();
  return { url, title: value.title, ready_state: value.ready_state };
}

function observedURL(value) {
  if (value === "about:blank") return value;
  return requiredURL(value);
}

function normalizePageRead(value, maximum) {
  const info = normalizePageInfo(value);
  if (typeof value.text !== "string" || typeof value.html !== "string" || typeof value.lang !== "string" || !Number.isSafeInteger(value.scroll_height) || value.scroll_height < 0) {
    throw clientContractError();
  }
  const originalLength = [...value.text].length;
  return {
    ...info,
    lang: value.lang,
    text: [...value.text].slice(0, maximum).join(""),
    html: [...value.html].slice(0, maximum).join(""),
    text_length: originalLength,
    text_truncated: originalLength > maximum,
    scroll_height: value.scroll_height,
  };
}

function collectSnapshotRefs(value, refs = new Set()) {
  if (Array.isArray(value)) {
    for (const item of value) collectSnapshotRefs(item, refs);
  } else if (value && typeof value === "object") {
    if (typeof value.ref === "string" && PLAYWRIGHT_REF_PATTERN.test(value.ref)) refs.add(value.ref);
    for (const item of Object.values(value)) collectSnapshotRefs(item, refs);
  }
  return refs;
}

function comparePlaywrightRefs(left, right) {
  return Number(left.slice(1)) - Number(right.slice(1));
}

function tabFingerprint(tab) {
  return `${tab.title}\u0000${tab.url}\u0000${tab.crashed ? "1" : "0"}`;
}

function isBridgeConnectionPage(tab) {
  return typeof tab?.url === "string" && tab.url.startsWith(BRIDGE_CONNECT_URL_PREFIX);
}

function sameFingerprintList(left, right) {
  return left.length === right.length && left.every((value, index) => value === right[index]);
}

function findCurrentInsertion(before, after) {
  const current = after.findIndex((tab) => tab.current);
  if (current < 0 || after.length !== before.length + 1) return -1;
  const reduced = after.filter((_, index) => index !== current).map(tabFingerprint);
  return sameFingerprintList(reduced, before.map(tabFingerprint)) ? current : -1;
}

function sameTabsAfterRemoval(before, after, removed) {
  if (after.length !== before.length - 1) return false;
  const reduced = before.filter((_, index) => index !== removed).map(tabFingerprint);
  return sameFingerprintList(reduced, after.map(tabFingerprint));
}

function currentOwnedPageID(pages, tabs) {
  for (const page of pages.values()) {
    if (tabs[page.index]?.current) return page.pageID;
  }
  return "";
}

function pageStale(detail) {
  return new ControllerError("browser_page_stale", "browser page generation is stale", {
    status: 409,
    cause: new Error(detail),
  });
}

function clientContractError() {
  return new ControllerError("browser_extension_unavailable", "browser extension is unavailable", {
    status: 503,
    retryable: true,
  });
}

async function waitForExit(child, timeoutMS) {
  if (child.exitCode !== null || child.signalCode !== null) return true;
  let timer;
  const timeout = new Promise((resolve) => {
    timer = setTimeout(() => resolve(false), timeoutMS);
    timer.unref?.();
  });
  const exited = child.once ? new Promise((resolve) => child.once("exit", () => resolve(true))) : Promise.resolve(true);
  const result = await Promise.race([exited, timeout]);
  clearTimeout(timer);
  return result;
}
