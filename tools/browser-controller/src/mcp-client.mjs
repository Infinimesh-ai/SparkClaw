import { spawn } from "node:child_process";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

import { ControllerError, invalidRequest } from "./errors.mjs";
import { collectSnapshotRefs, comparePlaywrightRefs } from "./playwright-output.mjs";
import { BACKGROUND_CLICK_FUNCTION } from "./dom-actions.mjs";
import { clientError, pageStale, clientContractError } from "./mcp-errors.mjs";
import { MAX_MCP_RESPONSE_BYTES, StdioJSONRPC, waitForExit } from "./mcp-stdio-rpc.mjs";
import {
  createSessionOutputDir,
  validateOutputRoot,
  prepareOutputRoot,
  removeSessionOutputDir,
} from "./mcp-session-output.mjs";
import {
  exactArgs,
  requiredPageID,
  optionalPageID,
  requiredRef,
  requiredText,
  optionalText,
  optionalBoolean,
  optionalInteger,
  optionalMaximum,
  optionalEnum,
  optionalStringArray,
  requiredURL,
  optionalURL,
  selectValues,
  normalizePageInfo,
  normalizePageRead,
  parseJSONResult,
} from "./mcp-arguments.mjs";
import {
  tabsFromPayload,
  tabFingerprint,
  isBridgeConnectionPage,
  sameFingerprintList,
  findCurrentInsertion,
  sameTabsAfterRemoval,
  currentOwnedPageID,
} from "./mcp-tabs.mjs";

const PACKAGE_ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const DEFAULT_MCP_ENTRY = path.join(PACKAGE_ROOT, "node_modules", "@playwright", "mcp", "cli.js");
const MCP_PROTOCOL_VERSION = "2025-06-18";
const PROCESS_EXIT_GRACE_MS = 1500;
const MAX_MCP_OUTPUT_BYTES = 8 << 20;
const MAX_TASK_PAGES = 16;
const RELAY_DEBUG_NAMESPACE = "pw:mcp:relay";

// Every upstream tool the client calls; clicks go through browser_evaluate so
// the background click never focuses the task tab.
const REQUIRED_TOOLS = new Set([
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

  async #snapshot(candidate, args) {
    const page = await this.#selectPage(candidate);
    const depth = optionalInteger(args.depth, "depth", 1, 64);
    const boxes = optionalBoolean(args.boxes, "boxes");
    const result = await this.#callJSONTool("browser_snapshot", {
      ...(depth === undefined ? {} : { depth }),
      ...(boxes === undefined ? {} : { boxes }),
    });
    // Under `_meta.json` the pinned MCP returns only `{ snapshot }`; page URL and
    // title are observed separately through page.info / page.read.
    const snapshot = result.payload.snapshot;
    if (!Array.isArray(snapshot)) throw clientContractError();
    const refs = collectSnapshotRefs(snapshot);
    page.refs = refs;
    return {
      page: { page_id: page.pageID },
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
      page: { page_id: page.pageID },
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

function scrubPlaywrightEnvironment(env) {
  for (const key of Object.keys(env)) {
    if (key.startsWith("PLAYWRIGHT_MCP_")) delete env[key];
  }
  return env;
}
