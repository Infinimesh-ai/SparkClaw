import crypto from "node:crypto";
import fs from 'node:fs/promises';
import path from "node:path";
import {fileURLToPath} from 'node:url';

import { ControllerError } from "./errors.mjs";
import { BACKGROUND_INPUT_EVALUATE_FUNCTION, BACKGROUND_INPUT_MARKER } from "../../browser-bridge/src/protocol.mjs";
import { BACKGROUND_FOCUS_FUNCTION, BATCH_READ_FUNCTION, EDITOR_LINES_FUNCTION } from "./dom-actions.mjs";
import { EXTENSION_CONNECT_URL, parseTabs, sanitizeTabListOutput, assertExpectedOrigin, assertTaskTopology } from "./cli-page-guards.mjs";
import { downloadFromPage } from "./cli-download.mjs";

const AWAITED_CODE_MARKER = '/* app-cli:awaited-code:v1 */';
import {
  MAX_CLI_OUTPUT_BYTES,
  assertArgumentsWithoutSecrets,
  clientContractError,
  clientTimeoutError,
  clientUnavailableError,
  pageStale,
  runProcess,
  scrubPlaywrightEnvironment,
  settleProcessOutput,
} from "./cli-runtime.mjs";
import { electronConnectionEnvironment } from "./electron-adapter-client.mjs";
import { TRANSPORT_COMMANDS } from "./cli-page-transport.mjs";
import { MAX_CODE_REQUEST_ARGUMENT_BYTES, MAX_CODE_REQUEST_CODE_BYTES } from "./cli-read-batch.mjs";
import { PageTransport, PageTransportUnavailable } from "./page-transport.mjs";

const PINNED_CLI_ENTRY = fileURLToPath(new URL('../node_modules/@playwright/cli/playwright-cli.js', import.meta.url));

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
  "run-code",
  "tab-close",
  "tab-list",
  "tab-select",
]);

function isProbeRead(command) {
  if (!Array.isArray(command) || command.some((value) => typeof value !== "string")) return false;
  const [name, subtype] = command;
  return (name === "get" && subtype === "url" && command.length === 2) ||
    (command.length === 3 && ((name === "get" && subtype === "text") ||
      (name === "is" && subtype === "visible")));
}

function isBatchRead(command) {
  if (!Array.isArray(command) || command.some(value => typeof value !== "string")) return false;
  const [name, subtype] = command;
  return name === "get" && (subtype === "url" && command.length === 2 ||
    ["count", "value", "text", "lines"].includes(subtype) && command.length === 3 ||
    subtype === "attr" && command.length === 4) ||
    name === "is" && ["visible", "enabled"].includes(subtype) && command.length === 3;
}

export class HostPage {
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
    this.backgroundPrepared = false;
    // Process accounting for the page lifetime (reported at stop).
    this.processStats = { cli: 0, transportCommands: 0, transportFallbacks: 0, started: Date.now() };
  }

  async attach() {
    await this.state.writeAttachIntent?.(this.sessionName,fileURLToPath(new URL('../node_modules/playwright-core/lib/entry/cliDaemon.js',import.meta.url)));
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
      throw clientContractError('attach_response_invalid');
    }
    await this.state.writeMetadata(parsed.pid, this.sessionName);
    this.attached = true;
    const tabs = await this.#tabs();
    // The authenticated Electron adapter returns a connection-owned blank
    // page, whereas the extension retains its connect page. Accept the blank
    // form only in that task-bound adapter and with exactly one visible tab.
    this.bootstrapURL = this.electronConnection && tabs.length === 1 &&
      ['about:blank', EXTENSION_CONNECT_URL + '/'].includes(tabs[0].url) ? tabs[0].url : EXTENSION_CONNECT_URL;
    const taskIndexes = tabs.flatMap((tab, index) =>
      tab.url === this.bootstrapURL ? [index] : []);
    if (
      taskIndexes.length !== 1 ||
      !tabs[taskIndexes[0]].current ||
      tabs[taskIndexes[0]].crashed
    ) {
      this.diagnostic?.({event: 'application_bootstrap_topology', count: tabs.length,
        pages: tabs.map(tab => ({current: tab.current, crashed: tab.crashed, blank: tab.url === 'about:blank',
          connect: tab.url === EXTENSION_CONNECT_URL, scheme: tab.url.split(':', 1)[0]}))});
      throw clientContractError('attach_task_topology');
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
    if (tabs[this.taskIndex]?.url !== this.bootstrapURL) {
      throw pageStale("task_page_missing");
    }
    this.taskReady = true;
    // A request file is used once; one left by an interrupted process is never run.
    for (const name of await fs.readdir(this.state.directory).catch(() => [])) {
      if (/^(?:read-batch|code-request)-[0-9a-f-]{36}\.json$/.test(name)) await fs.rm(path.join(this.state.directory, name), {force: true});
    }
    await this.#withTaskSelected(() => this.#run(['--raw', `-s=${this.sessionName}`, 'run-code',
      '/* app-cli:guard:v1 */ async page => true']));
    if (this.registration.hook) {
      await this.#withTaskSelected(() => this.#run(['--raw', `-s=${this.sessionName}`, 'run-code',
        '/* app-cli:hook:install:v1 */ async page => true']));
    }
    if (this.registration.backgroundBeforeNavigation) {
      const output = await this.#withTaskSelected(async tab => {
        if (tab.url !== this.bootstrapURL) throw pageStale('task_page_missing');
        return this.#run(['--raw', `-s=${this.sessionName}`, 'eval', BACKGROUND_INPUT_EVALUATE_FUNCTION]);
      });
      if (parseJSON(output) !== BACKGROUND_INPUT_MARKER) throw clientContractError();
      this.backgroundPrepared = true;
    }
    for (const source of this.registration.beforeNavigationSources ?? []) {
      await this.#withTaskSelected(() => this.#run(['--raw', `-s=${this.sessionName}`, 'run-code',
        `async page=>{await page.addInitScript({content:${JSON.stringify(source)}});return true}`]));
    }
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
    this.cleanupDeadline ??= Date.now() + 20_000;
    const tabs = await this.#tabs();
    // The current Bridge exposes only its task allowlist. In that topology,
    // popups are task-owned too and must be closed even after a handler fails.
    // Preserve strict fingerprints for legacy contexts containing owner pages.
    if (this.ownerTabs.length) this.#assertTopology(tabs);
    const indexes = this.ownerTabs.length ? [this.taskIndex] : tabs.map((_, index)=>index).reverse();
    for (const index of indexes) {
      try {
        await this.#run(["--raw", `-s=${this.sessionName}`, "tab-close", String(index)]);
      } catch (error) {
        if (!isExpectedTaskPageClosure(error) || index !== indexes.at(-1)) throw error;
      }
    }
    this.taskIndex = -1;
    this.taskReady = false;
    this.backgroundPrepared = false;
  }

  async stop() {
    await this.#closeTransport();
    if (!this.attached) return;
    this.cleanupDeadline ??= Date.now() + 20_000;
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

  async prepareBackgroundPage() {
    if (!this.registration.codeEnabled) throw clientContractError();
    await this.#assertAllowedOrigin();
    if (this.backgroundPrepared) return;
    if (await this.#evalJSON(BACKGROUND_INPUT_EVALUATE_FUNCTION) !== BACKGROUND_INPUT_MARKER) throw clientContractError();
    await this.#assertAllowedOrigin();
  }

  async probeReads(commands) {
    if (!Array.isArray(commands) || !commands.length || commands.length > 32 || !commands.every(isProbeRead)) throw clientContractError();
    // Collect a read-only evidence round in one document turn. Each evaluation
    // still selects and validates the owned task tab through #evalJSON.
    const output = await this.evaluate(`() => {
      const url = location.href;
      const parsed = new URL(url);
      if (parsed.username || parsed.password ||
          !${JSON.stringify(this.registration.origins)}.includes(parsed.origin)) {
        return { url, results: null };
      }
      const results = ${JSON.stringify(commands)}.map(([name, subtype, selector]) => {
        if (subtype === "url") return { url };
        const element = document.querySelector(selector);
        if (subtype === "text") {
          return { text: element ? (element.innerText ?? element.textContent ?? "") : "", origin: url };
        }
        if (!element || !element.isConnected) return { visible: false, origin: url };
        const style = getComputedStyle(element);
        const rect = element.getBoundingClientRect();
        return { visible: rect.width > 0 && rect.height > 0 &&
          style.display !== "none" && style.visibility !== "hidden" &&
          Number.parseFloat(style.opacity || "1") > 0, origin: url };
      });
      return { url, results };
    }`);
    if (!output || typeof output.url !== "string") throw clientContractError();
    assertExpectedOrigin(output.url, undefined, this.registration.origins);
    if (!Array.isArray(output.results) || output.results.length !== commands.length) {
      throw clientContractError();
    }
    return output.results.map((result, index) => {
      const subtype = commands[index][1];
      if (!result ||
          (subtype === "text" && typeof result.text !== "string") ||
          (subtype === "visible" && typeof result.visible !== "boolean") ||
          (subtype === "url" && result.url !== output.url)) {
        throw clientContractError();
      }
      return { success: true, result };
    });
  }

  async inspect(expression) { return this.#inspect(expression); }
  async act(command) { return this.#agentAction(command); }
  async reloadSecrets() {
    await this.#withTaskSelected(() => this.#run(['--raw', `-s=${this.sessionName}`, 'run-code',
      '/* app-cli:secrets:reload:v1 */ async page => true']));
  }
  async hookActivate() {
    await this.#assertAllowedOrigin();
    await this.#withTaskSelected(() => this.#run(['--raw', `-s=${this.sessionName}`, 'run-code',
      '/* app-cli:hook:activate:v1 */ async page => true']));
  }
  async #inspect(expression) {
    return await this.#inspectProbe(expression);
  }

  async #inspectProbe(expression, expectedOrigin, timeoutMS) {
    if (typeof expression !== "string") throw clientContractError();
    // Keep the URL guard and result in the same browser call. Tab ownership is
    // still checked before evaluation, including after a context-loss retry.
    const output = await this.evaluate(`async () => {
      const inspectionURL = window.location.href;
      const parsed = new URL(inspectionURL);
      if (parsed.username || parsed.password ||
          !${JSON.stringify(this.registration.origins)}.includes(parsed.origin) ||
          (${JSON.stringify(expectedOrigin ?? null)} !== null && parsed.origin !== ${JSON.stringify(expectedOrigin ?? null)})) {
        return { initial_url: inspectionURL, origin: inspectionURL, result: null };
      }
      const value = (${expression});
      const result = await (typeof value === "function" ? value() : value);
      return { initial_url: inspectionURL, origin: window.location.href, result };
    }`, true, timeoutMS);
    if (!output || typeof output.initial_url !== "string" || typeof output.origin !== "string" ||
        !Object.hasOwn(output, "result")) throw clientContractError();
    assertExpectedOrigin(output.initial_url, expectedOrigin, this.registration.origins);
    assertExpectedOrigin(output.origin, expectedOrigin, this.registration.origins);
    this.#assertProviderURL(output.initial_url);
    this.#assertProviderURL(output.origin);
    return { result: output.result, origin: output.origin };
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
    const { result: value } = await this.#inspectProbe(
      `() => document.querySelectorAll(${JSON.stringify(selector)}).length`,
      expectedOrigin,
    );
    if (!Number.isSafeInteger(value) || value < 0 || value > 10_000) {
      throw clientContractError();
    }
    return value;
  }

  async attribute(selector, attribute, expectedOrigin) {
    const value = await this.#readString(
      `() => document.querySelector(${JSON.stringify(selector)})?.getAttribute(${JSON.stringify(attribute)}) ?? ""`,
      expectedOrigin,
    );
    if (typeof value !== "string") throw clientContractError();
    return value;
  }

  async value(selector, expectedOrigin) {
    const value = await this.#readString(
      `() => { const element = document.querySelector(${JSON.stringify(selector)}); return element?.value ?? (element?.isContentEditable ? element.textContent : "") ?? ""; }`,
      expectedOrigin,
    );
    if (typeof value !== "string") throw clientContractError();
    return value;
  }

  async text(selector, expectedOrigin) {
    const value = await this.#readString(
      `() => { const element = document.querySelector(${JSON.stringify(selector)}); return element ? (element.innerText ?? element.textContent ?? "") : ""; }`,
      expectedOrigin,
    );
    if (typeof value !== "string") throw clientContractError();
    return value;
  }

  async lines(selector) {
    return await this.#readString(`() => (${EDITOR_LINES_FUNCTION})(document.querySelector(${JSON.stringify(selector)}))`);
  }

  async #readString(expression, expectedOrigin) {
    if (!this.registration.secretReadback) return (await this.#inspectProbe(expression, expectedOrigin)).result;
    // Playwright redacts secret values even in eval results. Only recover a
    // known input when the browser's digest proves the actual field matches.
    const { result } = await this.#inspectProbe(`async () => {
      const text = (${expression})();
      const bytes = new TextEncoder().encode(text);
      const digest = Array.from(new Uint8Array(await crypto.subtle.digest("SHA-256", bytes)),
        byte => byte.toString(16).padStart(2, "0")).join("");
      return { text, digest };
    }`, expectedOrigin);
    return this.#restoreString(result);
  }

  #restoreString(result) {
    if (!result || typeof result.text !== "string" || !/^[a-f0-9]{64}$/u.test(result.digest)) {
      throw clientContractError();
    }
    const candidates = [result.text, ...this.state.secretValues.flatMap(value =>
      [value, value.replace(/\r\n?/gu, "\n"), value.toLowerCase()])];
    const match = candidates.find(value =>
      crypto.createHash("sha256").update(value, "utf8").digest("hex") === result.digest);
    if (match === undefined) throw clientContractError();
    return match;
  }

  async readMany(commands, expectedOrigin) {
    if (!Array.isArray(commands) || commands.length === 0 || commands.length > 32 || !commands.every(isBatchRead)) {
      throw clientContractError();
    }
    const { result, origin } = await this.#inspectProbe(
      `() => (${BATCH_READ_FUNCTION})(${JSON.stringify(commands)}, ${EDITOR_LINES_FUNCTION})`, expectedOrigin,
    );
    if (!Array.isArray(result) || result.length !== commands.length) throw clientContractError();
    return result.map((entry, index) => {
      const [, subtype, selector] = commands[index];
      if (!entry || !Object.hasOwn(entry, "value")) throw clientContractError();
      let value = entry.value;
      if (subtype === "url") {
        if (value !== origin) throw clientContractError();
      } else if (subtype === "count") {
        if (!Number.isSafeInteger(value) || value < 0 || value > 10_000) throw clientContractError();
      } else if (["visible", "enabled"].includes(subtype)) {
        if (typeof value !== "boolean") throw clientContractError();
      } else {
        value = this.#restoreString({ text: value, digest: entry.digest });
      }
      const field = subtype === "lines" ? "text" : subtype === "attr" ? "value" : subtype;
      return { [field]: value, origin, ...(subtype === "count" ? { selector } : {}) };
    });
  }

  async visible(selector, expectedOrigin) {
    const { result: value } = await this.#inspectProbe(
      `() => { const element = document.querySelector(${JSON.stringify(selector)}); if (!element || !element.isConnected) return false; const style = getComputedStyle(element); const rect = element.getBoundingClientRect(); return rect.width > 0 && rect.height > 0 && style.display !== "none" && style.visibility !== "hidden" && Number.parseFloat(style.opacity || "1") > 0; }`,
      expectedOrigin,
    );
    if (typeof value !== "boolean") throw clientContractError();
    return value;
  }

  async enabled(selector, expectedOrigin) {
    const { result: value } = await this.#inspectProbe(
      `() => { const element = document.querySelector(${JSON.stringify(selector)}); return Boolean(element) && !element.disabled && element.getAttribute("aria-disabled") !== "true"; }`,
      expectedOrigin,
    );
    if (typeof value !== "boolean") throw clientContractError();
    return value;
  }

  async evaluate(expression, validateProvider = false, timeoutMS) {
    if (typeof expression !== "string" || Buffer.byteLength(expression, "utf8") > 32 << 10) {
      throw clientContractError();
    }
    for (let attempt = 0; attempt < TRANSIENT_EVALUATION_ATTEMPTS; attempt += 1) {
      try {
        return await this.#evalJSON(expression, undefined, validateProvider, timeoutMS);
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
    if (this.registration.effectSelectors?.includes(selector)) { this.beforeEffect?.(); this.effectAttempted = true; }
    await this.#withTaskSelected(async () => {
      await this.#run(["--raw", `-s=${this.sessionName}`, "click", selector]);
    });
    await this.currentURL(expectedOrigin);
  }

  async runReadCode(code, timeoutMS = 30_000) {
    if (!this.registration.codeEnabled || typeof code !== "string" || Buffer.byteLength(code) > 64 << 10 ||
        !Number.isSafeInteger(timeoutMS) || timeoutMS < 1 || timeoutMS > 60_000) throw clientContractError();
    const readOnly = this.registration.readOnlyCode === true;
    // Bind the result to the actual page URLs in the same awaited call. Keep
    // the independent post-call tab topology check, without a second renderer
    // evaluation merely to retrieve location.href.
    const guardedCode = readOnly ? `${AWAITED_CODE_MARKER}\nasync page => {
      const initial_url = page.url();
      if (!${JSON.stringify(this.registration.origins)}.some(origin => initial_url === origin ||
          ['/', '?', '#'].some(separator => initial_url.startsWith(origin + separator))))
        return {initial_url, final_url:initial_url, result:null};
      const result = await (${code})(page);
      return {initial_url, final_url:page.url(), result};
    }` : code;
    if (process.platform==='linux' && this.batchReadCommands !== false && readOnly && this.registration.awaitedRead &&
        !this.registration.deniedURLs?.length &&
        path.resolve(this.entryPoint) === fileURLToPath(new URL('../node_modules/@playwright/cli/playwright-cli.js', import.meta.url))) {
      const requestPath=path.join(this.state.directory,`read-batch-${crypto.randomUUID()}.json`);
      try {
        const request={sessionName:this.sessionName,taskIndex:this.taskIndex,ownerTabs:this.ownerTabs,
          origins:this.registration.origins,code:guardedCode,actionTimeoutMS:this.actionTimeoutMS,readTimeoutMS:Math.max(this.actionTimeoutMS,timeoutMS)};
        const unwrap=raw=>{
          const envelope=parseJSON(raw);
          if(envelope?.error){
            if(!['browser_page_stale','browser_extension_unavailable','browser_script_timeout'].includes(envelope.error.code))throw clientContractError();
            throw new ControllerError(envelope.error.code,'browser guarded read failed',{status:envelope.error.code==='browser_page_stale'?409:envelope.error.code==='browser_script_timeout'?504:503,retryable:envelope.error.code!=='browser_page_stale',diagnosticReason:envelope.error.reason});
          }
          if(typeof envelope?.output!=='string')throw clientContractError();
          return envelope.output;
        };
        const batchTimeoutMS=Math.max(this.actionTimeoutMS,timeoutMS)+6*this.actionTimeoutMS;
        let output=await this.#transportBatch(request,batchTimeoutMS,unwrap);
        if(output===undefined){
          await fs.writeFile(requestPath,JSON.stringify(request),{flag:'wx',mode:0o600});
          output=await this.#run(['--batch-request',requestPath],batchTimeoutMS,unwrap,fileURLToPath(new URL('./cli-read-batch.mjs',import.meta.url)));
        }
        const value=parseJSON(output);
        if(!value||!Object.hasOwn(value,'result'))throw clientContractError();
        for(const url of [value.initial_url,value.final_url])assertExpectedOrigin(url,undefined,this.registration.origins);
        return value.result;
      } finally {await fs.rm(requestPath,{force:true});}
    }
    const output = await this.#withTaskSelected(async tab => {
      assertExpectedOrigin(tab.url, undefined, this.registration.origins);
      this.#assertProviderURL(tab.url);
      return this.#run(["--raw", `-s=${this.sessionName}`, "run-code", guardedCode], Math.max(this.actionTimeoutMS, timeoutMS));
    });
    if (readOnly) {
      await this.#withTaskSelected(tab => {
        assertExpectedOrigin(tab.url, undefined, this.registration.origins);
        this.#assertProviderURL(tab.url);
      });
      const value = parseJSON(output);
      if (!value || !Object.hasOwn(value, 'result')) throw clientContractError();
      for (const url of [value.initial_url, value.final_url]) {
        assertExpectedOrigin(url, undefined, this.registration.origins);
        this.#assertProviderURL(url);
      }
      return value.result;
    }
    await this.#assertAllowedOrigin();
    return parseJSON(output);
  }

  // Guarded code request for mutation pages. `code` is a small fixed page
  // function `(page, argument) => ...`; `argument` (message content) is joined
  // with it only inside the guarded process and reaches the browser through the
  // daemon's Session socket, never through an argument list. The request goes
  // over the resident transport's stdin or a one-use 0600 file. Topology and
  // origin checks stay; there is no read-only wrapper.
  async runCodeRequest(code, argument, timeoutMS = 30_000) {
    if (!this.registration.codeEnabled || this.registration.readOnlyCode === true || typeof code !== "string" ||
        Buffer.byteLength(code) > MAX_CODE_REQUEST_CODE_BYTES || !Number.isSafeInteger(timeoutMS) || timeoutMS < 1 || timeoutMS > 60_000) throw clientContractError();
    let value;
    try { value = JSON.parse(JSON.stringify(argument ?? null)); } catch { throw clientContractError(); }
    if (Buffer.byteLength(JSON.stringify(value)) > MAX_CODE_REQUEST_ARGUMENT_BYTES) throw clientContractError();
    // Daemon ownership is proved through /proc; elsewhere no private path exists.
    if (process.platform !== 'linux' || this.batchReadCommands === false || this.electronConnection ||
        path.resolve(this.entryPoint) !== PINNED_CLI_ENTRY) throw clientContractError('code_request_unsupported');
    const origins = JSON.stringify(this.registration.origins), denied = JSON.stringify(this.registration.deniedURLs ?? []);
    const guardedCode = `${AWAITED_CODE_MARKER}\nasync (page, argument) => {
      const initial_url = page.url();
      let allowed = false;
      try {
        const url = new URL(initial_url);
        allowed = ${origins}.some(origin => initial_url === origin || ['/', '?', '#'].some(separator => initial_url.startsWith(origin + separator))) &&
          !${denied}.some(rule => url.origin === rule.origin && new RegExp(rule.path).test(url.pathname.toLowerCase()) &&
            Object.entries(rule.query ?? {}).every(([key, expected]) => url.searchParams.get(key) === expected));
      } catch {}
      if (!allowed) return {initial_url, final_url: initial_url, result: null};
      const result = await (${code})(page, argument);
      return {initial_url, final_url: page.url(), result};
    }`;
    const requestPath = path.join(this.state.directory, `code-request-${crypto.randomUUID()}.json`);
    try {
      const request = {sessionName: this.sessionName, taskIndex: this.taskIndex, ownerTabs: this.ownerTabs, origins: this.registration.origins,
        code: guardedCode, argument: value, actionTimeoutMS: this.actionTimeoutMS, readTimeoutMS: Math.max(this.actionTimeoutMS, timeoutMS)};
      const unwrap = raw => {
        const envelope = parseJSON(raw);
        if (envelope?.error) {
          if (!['browser_page_stale', 'browser_extension_unavailable', 'browser_script_timeout'].includes(envelope.error.code)) throw clientContractError();
          throw new ControllerError(envelope.error.code, 'browser guarded code request failed', {status: envelope.error.code === 'browser_page_stale' ? 409 :
            envelope.error.code === 'browser_script_timeout' ? 504 : 503, retryable: false, diagnosticReason: envelope.error.reason});
        }
        if (typeof envelope?.output !== 'string') throw clientContractError();
        return envelope.output;
      };
      const requestTimeoutMS = Math.max(this.actionTimeoutMS, timeoutMS) + 6 * this.actionTimeoutMS;
      let output = await this.#transportBatch(request, requestTimeoutMS, unwrap, 'code_request');
      if (output === undefined) {
        await fs.writeFile(requestPath, JSON.stringify(request), {flag: 'wx', mode: 0o600});
        output = await this.#run(['--code-request', requestPath], requestTimeoutMS, unwrap, fileURLToPath(new URL('./cli-read-batch.mjs', import.meta.url)));
      }
      const result = parseJSON(output);
      if (!result || !Object.hasOwn(result, 'result')) throw clientContractError();
      for (const url of [result.initial_url, result.final_url]) {
        assertExpectedOrigin(url, undefined, this.registration.origins);
        this.#assertProviderURL(url);
      }
      return result.result;
    } finally { await fs.rm(requestPath, {force: true}); }
  }

  async download(selector, destination, maxBytes) {
    if (!this.registration.downloadEnabled || typeof selector !== "string" || !selector ||
        !path.isAbsolute(destination) || !Number.isSafeInteger(maxBytes) || maxBytes <= 0 || maxBytes > 110 << 20) throw clientContractError();
    return downloadFromPage(this, selector, destination, maxBytes);
  }

  async fill(selector, value, expectedOrigin) {
    const secret = this.state.secretName(value);
    await this.#withTaskSelected(async tab => {
      assertExpectedOrigin(tab.url, expectedOrigin, this.registration.origins);
      this.#assertProviderURL(tab.url);
      await this.#run(["--raw", `-s=${this.sessionName}`, "fill", selector, secret]);
    });
    await this.currentURL(expectedOrigin);
  }

  async focus(selector, expectedOrigin) {
    await this.currentURL(expectedOrigin);
    const focused = await this.#evalJSON(BACKGROUND_FOCUS_FUNCTION, selector);
    if (focused !== true) throw clientContractError();
  }

  async press(key, expectedOrigin) {
    if (typeof key !== "string" || !/^[A-Za-z0-9+_-]{1,32}$/u.test(key)) {
      throw clientContractError();
    }
    await this.#withTaskSelected(async tab => {
      assertExpectedOrigin(tab.url, expectedOrigin, this.registration.origins);
      this.#assertProviderURL(tab.url);
      await this.#run(["--raw", `-s=${this.sessionName}`, "press", key]);
    });
    await this.currentURL(expectedOrigin);
  }

  async waitFor(selector, expectedOrigin, timeoutMS = 10_000) {
    const bounded = Math.min(Math.max(Number(timeoutMS) || 10_000, 25), 30_000);
    const expression = `async () => { const selector = ${JSON.stringify(selector)}; const deadline = Date.now() + ${bounded}; while (Date.now() < deadline) {
      const element = document.querySelector(selector);
      if (element && element.isConnected) {
        const rect = element.getBoundingClientRect();
        const style = getComputedStyle(element);
        if (rect.width > 0 && rect.height > 0 && style.display !== "none" && style.visibility !== "hidden" &&
            Number.parseFloat(style.opacity || "1") > 0) return true;
      }
      await new Promise(resolve => setTimeout(resolve, 100));
    } return false; }`;
    if ((await this.#inspectProbe(expression, expectedOrigin, bounded + 1000)).result !== true) throw clientContractError();
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
        if (subtype === "lines") {
          return { text: await this.lines(selector), origin: await this.currentURL() };
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
        return await this.#inspect(expression);
      }
    }
    throw clientContractError();
  }

  async #evalJSON(expression, target, validateProvider = false, timeoutMS) {
    const output = await this.#withTaskSelected(async (tab) => {
      if (validateProvider) {
        assertExpectedOrigin(tab.url, undefined, this.registration.origins);
        this.#assertProviderURL(tab.url);
      }
      return await this.#run([
        "--raw", `-s=${this.sessionName}`, "eval", expression,
        ...(target ? [target] : []),
      ], timeoutMS);
    });
    return parseJSON(output);
  }

  async #assertAllowedOrigin() {
    const url = await this.currentURL();
    assertExpectedOrigin(url, undefined, this.registration.origins);
    return url;
  }

  #assertProviderURL(url) {
    if (this.registration.deniedURLs?.some(rule => {
      const value = new URL(url);
      return value.origin === rule.origin && new RegExp(rule.path).test(value.pathname.toLowerCase()) &&
        Object.entries(rule.query ?? {}).every(([key, expected]) => value.searchParams.get(key) === expected);
    })) throw Object.assign(new Error('Application sign-in required'), {code: 'application_login_required'});

  }

  async #withTaskSelected(callback) {
    let tabs = await this.#tabs();
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
      tabs = selected;
    }
    return await callback(tabs[this.taskIndex]);
  }

  async #tabs() {
    return parseTabs(await this.#run(
      ["--raw", `-s=${this.sessionName}`, "tab-list"],
      this.actionTimeoutMS,
      (raw) => sanitizeTabListOutput(raw, this.token),
    ));
  }

  #assertTopology(tabs) {
    assertTaskTopology(tabs,this.taskIndex,this.ownerTabs);
  }

  #runContext(requestedTimeoutMS) {
    const signal = this.cleanupDeadline === undefined ? this.signal : undefined;
    if (signal?.aborted) throw clientUnavailableError();
    const remaining = (this.cleanupDeadline ?? this.deadline) - Date.now();
    if (remaining <= 0) throw clientTimeoutError();
    return { signal, timeoutMS: Math.max(1, Math.min(requestedTimeoutMS, remaining)) };
  }

  #environment() {
    const env = scrubPlaywrightEnvironment({ ...process.env, ...this.extraEnv });
    const awaitedNetworkRead = this.registration.awaitedRead === true;
    Object.assign(env, this.state.environment, {
      PLAYWRIGHT_MCP_EXTENSION_TOKEN: this.token,
      PLAYWRIGHT_MCP_CODEGEN: "none",
      PLAYWRIGHT_MCP_IMAGE_RESPONSES: "omit",
      PLAYWRIGHT_MCP_OUTPUT_DIR: this.state.outputDir,
      PLAYWRIGHT_MCP_OUTPUT_MAX_SIZE: String(MAX_CLI_OUTPUT_BYTES),
      PLAYWRIGHT_MCP_SNAPSHOT_MODE: "none",
      PLAYWRIGHT_MCP_TIMEOUT_ACTION: String(this.actionTimeoutMS),
      PLAYWRIGHT_MCP_TIMEOUT_NAVIGATION: String(this.navigationTimeoutMS),
      // Managed network readers await their own response and readiness proof.
      // Sending and legacy/UI operations retain their existing settle policy.
      PLAYWRIGHT_MCP_TIMEOUT_SETTLE: awaitedNetworkRead ? "0" : "500",
      APP_CLI_AWAITED_CODE: awaitedNetworkRead ? '1' : '0',
      NO_COLOR: "1",
      NO_UPDATE_NOTIFIER: "1",
    });
    Object.assign(env, electronConnectionEnvironment(this.electronConnection));
    if (this.hookConfig) env.APP_CLI_HOOK_CONFIG = this.hookConfig;
    if (this.leaseFile) env.APP_CLI_LEASE_FILE = this.leaseFile;
    if (this.executablePath) env.PLAYWRIGHT_MCP_EXECUTABLE_PATH = this.executablePath;
    if (this.userDataDir) env.PLAYWRIGHT_MCP_USER_DATA_DIR = this.userDataDir;
    if (this.state.secretsPath) env.PLAYWRIGHT_MCP_CONFIG = this.state.secretsPath;
    return env;
  }

  // The resident transport serves the same pinned CLI commands as the
  // per-command processes, against the same attached daemon. It is used only
  // for the production entry point on Linux (daemon ownership is proved through
  // /proc, as for guarded read batches), after attach recorded the daemon.
  #transportReady() {
    return this.pageTransport === true && this.attached && process.platform === 'linux' && !this.electronConnection &&
      path.resolve(this.entryPoint) === PINNED_CLI_ENTRY && this.transportDisabled !== true;
  }

  #transport() {
    this.transport ??= new PageTransport({ spawn: this.spawn, cwd: this.state.outputDir, env: this.#environment(),
      sessionName: this.sessionName, idleMS: this.pageTransportIdleMS });
    return this.transport;
  }

  // Returns undefined when the request was never sent (the caller then uses
  // its ordinary process); any failure after sending is final.
  async #transportRequest(message, timeoutMS, signal) {
    const transport = this.#transport();
    if (transport.busy) return undefined;
    try {
      const response = await transport.request(message, { timeoutMS, signal });
      this.processStats.transportCommands += 1;
      return response;
    } catch (error) {
      if (!(error instanceof PageTransportUnavailable)) throw error;
      this.processStats.transportFallbacks += 1;
      if (error.reason !== 'busy') {
        // A helper that cannot prove the daemon is not retried for this page.
        this.transportDisabled = true;
        this.diagnostic?.({ event: 'application_page_transport_unavailable', reason: error.reason });
      }
      return undefined;
    }
  }

  async #transportBatch(request, requestedTimeoutMS, unwrap, kind = 'batch') {
    if (!this.#transportReady()) return undefined;
    try {
      const { signal, timeoutMS } = this.#runContext(requestedTimeoutMS);
      const response = await this.#transportRequest({ [kind]: request }, timeoutMS, signal);
      if (response === undefined) return undefined;
      if (!response.envelope) throw clientContractError('transport_protocol');
      const raw = JSON.stringify(response.envelope);
      if (raw.includes(this.token)) throw clientContractError('forbidden_output');
      return unwrap(raw);
    } catch (error) {
      // Same attribution as the one-shot batch process.
      if (error instanceof ControllerError) Object.defineProperty(error, "diagnosticCommand", { value: 'run-code', enumerable: false });
      throw error;
    }
  }

  async #closeTransport() {
    const transport = this.transport;
    this.transport = null;
    if (!transport && !this.processStats.cli) return;
    try { await transport?.close(); }
    finally {
      if (!this.processStatsReported) {
        this.processStatsReported = true;
        this.diagnostic?.({ event: 'application_page_processes', cli_processes: this.processStats.cli,
          transport_processes: transport?.spawns ?? 0, transport_commands: this.processStats.transportCommands,
          transport_fallbacks: this.processStats.transportFallbacks, lifetime_ms: Date.now() - this.processStats.started });
      }
    }
  }

  async #run(args, requestedTimeoutMS = this.actionTimeoutMS, stdoutTransform, entryPoint=this.entryPoint) {
    const { signal, timeoutMS } = this.#runContext(requestedTimeoutMS);
    const options = {
      cwd: this.state.outputDir,
      env: this.#environment(),
      timeoutMS,
      secrets: [...this.state.secretValues, this.token],
      forbiddenOutputValues: args.includes("eval")
        ? [this.token]
        : [...this.state.secretValues, this.token],
      signal,
      stdoutTransform,
    };
    const command = entryPoint!==this.entryPoint?'run-code':args.find((value) => !value.startsWith("-"));
    try {
      if (entryPoint === this.entryPoint && args[0] === '--raw' && TRANSPORT_COMMANDS.has(command) && this.#transportReady()) {
        assertArgumentsWithoutSecrets(args, options.secrets);
        const response = await this.#transportRequest({ args }, timeoutMS, signal);
        if (response !== undefined) {
          if (response.overflow) throw clientContractError("output_overflow");
          if (response.error) throw clientContractError(response.error.reason);
          if (![0, 1].includes(response.code) || typeof response.stdout !== 'string' || typeof response.stderr !== 'string' ||
              Buffer.byteLength(response.stdout) > MAX_CLI_OUTPUT_BYTES || Buffer.byteLength(response.stderr) > MAX_CLI_OUTPUT_BYTES) {
            throw clientContractError('transport_protocol');
          }
          return settleProcessOutput(response.code, null, response.stdout, response.stderr, options);
        }
        // A helper that never started must not extend the page deadline.
        Object.assign(options, this.#runContext(requestedTimeoutMS));
      }
      this.processStats.cli += 1;
      return await runProcess(this.spawn, process.execPath, [entryPoint, ...args], options);
    } catch (error) {
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
