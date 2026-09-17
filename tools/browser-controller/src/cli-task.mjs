import crypto from "node:crypto";
import fs from 'node:fs/promises';
import path from "node:path";
import {fileURLToPath} from 'node:url';

import { ControllerError } from "./errors.mjs";
import { BACKGROUND_INPUT_EVALUATE_FUNCTION, BACKGROUND_INPUT_MARKER } from "../../browser-bridge/src/protocol.mjs";
import { BACKGROUND_FOCUS_FUNCTION, BATCH_READ_FUNCTION, EDITOR_LINES_FUNCTION } from "./dom-actions.mjs";
import { EXTENSION_CONNECT_URL, parseTabs, sanitizeTabListOutput, assertExpectedOrigin, assertTaskTopology } from "./cli-page-guards.mjs";
import { downloadFromPage } from "./cli-download.mjs";
import {installOutlookEarlyBridge} from '../../../scripts/email/userscripts/lib/outlook-early-bridge.mjs';
import awaitedMailRead from './awaited-mail-read.cjs';
import {
  MAX_CLI_OUTPUT_BYTES,
  clientContractError,
  clientTimeoutError,
  clientUnavailableError,
  pageStale,
  runProcess,
  scrubPlaywrightEnvironment,
} from "./cli-runtime.mjs";

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
    this.backgroundPrepared = false;
    this.mailDocumentNonce = crypto.randomUUID();
  }

  renewReadInvocation(registration, signal) {
    if (registration.operation !== 'collect_page' || this.registration.provider !== registration.provider) throw clientContractError();
    this.registration = registration;
    this.signal = signal;
    this.deadline = Date.now() + registration.timeoutMS;
    this.cleanupDeadline = undefined;
  }

  async prepareMailRound(account, reused = false) {
    const result = await this.runReadCode(`async page=>page.evaluate(async options=>{
      if(options.reused&&window.__sparkclawMailDocument!==options.nonce)return {stale:true};
      const deadline=Date.now()+5000;
      for(;;){
        const reader=window.SparkClawMailReader;
        if(!reader&&Date.now()<deadline){await new Promise(resolve=>setTimeout(resolve,100));continue;}
        if(reader?.provider!==options.provider||reader.version!=='0.2.0'||typeof reader.resetRound!=='function')return {stale:true};
        try{
          const result=reader.resetRound({account_address:options.account});
          window.__sparkclawMailDocument=options.nonce;
          return result;
        }catch(error){
          if(error.code==='email_account_identity_unavailable'&&Date.now()<deadline){await new Promise(resolve=>setTimeout(resolve,100));continue;}
          return {error:error.code||'email_network_read_failed'};
        }
      }
    },${JSON.stringify({provider:this.registration.provider,account,nonce:this.mailDocumentNonce,reused})})`);
    if (result?.stale) throw pageStale('task_page_missing');
    if (result?.error) throw Object.assign(new Error(result.error),{code:result.error});
    if(result?.provider!==this.registration.provider||result?.account_address!==account.toLowerCase()) throw clientContractError();
  }

  async parkMailRound(account) {
    // Local-only reset releases original blobs/records before the page is idle.
    // Retain topology/origin/document/account guards; never refresh a mailbox.
    await this.prepareMailRound(account,true);
    this.signal = undefined;
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
    if (['read','discover','capture','enumerate_thread','mark_read','collect_page'].includes(this.registration.operation)) {
      // The fixed Bridge marker enables target-scoped CDP focus emulation,
      // which survives navigation. Do this before loading a busy provider page.
      // No provider code or owner tab is eligible for this blank-page exception.
      const output = await this.#withTaskSelected(async tab => {
        if (tab.url !== EXTENSION_CONNECT_URL) throw pageStale('task_page_missing');
        return this.#run(['--raw', `-s=${this.sessionName}`, 'eval', BACKGROUND_INPUT_EVALUATE_FUNCTION]);
      });
      if (parseJSON(output) !== BACKGROUND_INPUT_MARKER) throw clientContractError();
      await this.#withTaskSelected(tab => {
        if (tab.url !== EXTENSION_CONNECT_URL) throw pageStale('task_page_missing');
      });
      this.backgroundPrepared = true;
    }
    if (this.registration.provider === 'outlook' && ['read','discover','capture','enumerate_thread','mark_read','collect_page'].includes(this.registration.operation)) {
      // Fixed observer in the owned blank page, before Outlook binds its native
      // transport. This avoids a second navigation or startup-data replay.
      await this.#withTaskSelected(() => this.#run([
        '--raw', `-s=${this.sessionName}`, 'run-code',
        `async page=>{await page.addInitScript(${installOutlookEarlyBridge.toString()});return true}`,
      ]));
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
    if (!["send", "read", "discover", "capture", "enumerate_thread", "mark_read", "collect_page"].includes(this.registration.operation)) throw clientContractError();
    await this.#assertAllowedOrigin();
    if (this.backgroundPrepared) return;
    if (await this.#evalJSON(BACKGROUND_INPUT_EVALUATE_FUNCTION) !== BACKGROUND_INPUT_MARKER) throw clientContractError();
    await this.#assertAllowedOrigin();
  }

  qqTask() {
    return {
      onTab: async (commands) => {
        if (this.registration.operation === "probe" && commands.length > 0 &&
            commands.every(isProbeRead)) {
          return await this.#probeReads(commands);
        }
        const results = [];
        for (let index = 0; index < commands.length;) {
          if (this.registration.operation === "send" && isBatchRead(commands[index])) {
            const start = index;
            while (index < commands.length && index - start < 32 && isBatchRead(commands[index])) index += 1;
            const batch = await this.readMany(commands.slice(start, index));
            results.push(...batch.map(result => ({ success: true, result })));
          } else {
            results.push({ success: true, result: await this.#agentAction(commands[index++]) });
          }
        }
        return results;
      },
    };
  }

  async #probeReads(commands) {
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

  outlookTab() {
    return {
      inspect: async (expression) => await this.#inspect(expression),
      readMany: async commands => await this.readMany(commands),
      act: async (command) => {
        // DOM operations validate origin themselves; raw evaluation and delays do not.
        if (command?.[0] === "eval" || command?.[0] === "wait" && /^[0-9]+$/u.test(command[1])) {
          await this.#assertAllowedOrigin();
        }
        return await this.#agentAction(command);
      },
    };
  }

  gmailTab() {
    return {
      open: async () => {},
      inspect: async (expression) => await this.#inspect(expression),
      readMany: async (commands, expectedOrigin) => await this.readMany(commands, expectedOrigin),
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
    if (this.registration.operation !== "send") return (await this.#inspectProbe(expression, expectedOrigin)).result;
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
    if (selector === this.registration.effectSelector || this.registration.effectSelectors?.includes(selector)) this.effectAttempted = true;
    await this.#withTaskSelected(async () => {
      await this.#run(["--raw", `-s=${this.sessionName}`, "click", selector]);
    });
    await this.currentURL(expectedOrigin);
  }

  async runReadCode(code, timeoutMS = 30_000) {
    if (!["send", "read", "discover", "capture", "enumerate_thread", "mark_read", "collect_page"].includes(this.registration.operation) || typeof code !== "string" || Buffer.byteLength(code) > 64 << 10 ||
        !Number.isSafeInteger(timeoutMS) || timeoutMS < 1 || timeoutMS > 60_000) throw clientContractError();
    const readOnly = ['read', 'discover', 'capture', 'collect_page'].includes(this.registration.operation);
    // Bind the result to the actual page URLs in the same awaited call. Keep
    // the independent post-call tab topology check, without a second renderer
    // evaluation merely to retrieve location.href.
    const guardedCode = readOnly ? `${awaitedMailRead.MARKER}\nasync page => {
      const initial_url = page.url();
      if (!${JSON.stringify(this.registration.origins)}.some(origin => initial_url === origin ||
          ['/', '?', '#'].some(separator => initial_url.startsWith(origin + separator))))
        return {initial_url, final_url:initial_url, result:null};
      const result = await (${code})(page);
      return {initial_url, final_url:page.url(), result};
    }` : code;
    if (process.platform==='linux' && this.batchReadCommands !== false && readOnly && ['qq_mail','gmail'].includes(this.registration.provider) &&
        typeof this.registration.signedOutURL!=='function' &&
        path.resolve(this.entryPoint) === fileURLToPath(new URL('../node_modules/@playwright/cli/playwright-cli.js', import.meta.url))) {
      const requestPath=path.join(this.state.directory,`read-batch-${crypto.randomUUID()}.json`);
      try {
        await fs.writeFile(requestPath,JSON.stringify({sessionName:this.sessionName,taskIndex:this.taskIndex,ownerTabs:this.ownerTabs,
          origins:this.registration.origins,code:guardedCode,actionTimeoutMS:this.actionTimeoutMS,readTimeoutMS:Math.max(this.actionTimeoutMS,timeoutMS)}),{flag:'wx',mode:0o600});
        const output=await this.#run(['--batch-request',requestPath],Math.max(this.actionTimeoutMS,timeoutMS)+6*this.actionTimeoutMS,
          raw=>{
            const envelope=parseJSON(raw);
            if(envelope?.error){
              if(!['browser_page_stale','browser_extension_unavailable','browser_script_timeout'].includes(envelope.error.code))throw clientContractError();
              throw new ControllerError(envelope.error.code,'browser guarded read failed',{status:envelope.error.code==='browser_page_stale'?409:envelope.error.code==='browser_script_timeout'?504:503,retryable:envelope.error.code!=='browser_page_stale',diagnosticReason:envelope.error.reason});
            }
            if(typeof envelope?.output!=='string')throw clientContractError();
            return envelope.output;
          },fileURLToPath(new URL('./cli-read-batch.mjs',import.meta.url)));
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

  async download(selector, destination, maxBytes) {
    if (!["read", "capture", "collect_page"].includes(this.registration.operation) || typeof selector !== "string" || !selector ||
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
    if (typeof this.registration.signedOutURL !== "function") return;
    if (!this.registration.signedOutURL(url)) return;
    throw Object.assign(new Error("email_login_required"), {
      code: "email_login_required",
    });
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

  async #run(args, requestedTimeoutMS = this.actionTimeoutMS, stdoutTransform, entryPoint=this.entryPoint) {
    const signal = this.cleanupDeadline === undefined ? this.signal : undefined;
    if (signal?.aborted) throw clientUnavailableError();
    const remaining = (this.cleanupDeadline ?? this.deadline) - Date.now();
    if (remaining <= 0) throw clientTimeoutError();
    const timeoutMS = Math.max(1, Math.min(requestedTimeoutMS, remaining));
    const env = scrubPlaywrightEnvironment({ ...process.env, ...this.extraEnv });
    // Outlook range selection is still driven by the native search UI. Its
    // fast-completion trial timed out intermittently, so keep the qualified UI
    // completion policy; only QQ/Gmail direct readers use the fast path.
    const awaitedNetworkRead = ['qq_mail', 'gmail'].includes(this.registration.provider) &&
      ['read', 'discover', 'capture', 'collect_page'].includes(this.registration.operation);
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
      SPARKCLAW_AWAITED_MAIL_READ: awaitedNetworkRead ? '1' : '0',
      NO_COLOR: "1",
      NO_UPDATE_NOTIFIER: "1",
    });
    if (this.executablePath) env.PLAYWRIGHT_MCP_EXECUTABLE_PATH = this.executablePath;
    if (this.userDataDir) env.PLAYWRIGHT_MCP_USER_DATA_DIR = this.userDataDir;
    if (this.state.secretsPath) env.PLAYWRIGHT_MCP_CONFIG = this.state.secretsPath;
    try {
      return await runProcess(this.spawn, process.execPath, [entryPoint, ...args], {
        cwd: this.state.outputDir,
        env,
        timeoutMS,
        secrets: [...this.state.secretValues, this.token],
        forbiddenOutputValues: args.includes("eval")
          ? [this.token]
          : [...this.state.secretValues, this.token],
        signal,
        stdoutTransform,
      });
    } catch (error) {
      const command = entryPoint!==this.entryPoint?'run-code':args.find((value) => !value.startsWith("-"));
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
    prepareSendPage: async () => {
      if(registration.operation!=="send")throw clientContractError();
      await client.prepareBackgroundPage();
    },
    signal: client.signal,
    withTaskTab: async (operation, callback) => {
      if (operation !== registration.operation) throw clientContractError();
      return await callback(
        registration.provider === "qq_mail" ? client.qqTask() : client.outlookTab(),
      );
    },
    createOwnedTab: async () => client.gmailTab(),
    withSendTab: async callback => {
      if(registration.operation!=="send")throw clientContractError();
      return callback({
        inspect:expression=>client.gmailTab().inspect(expression),
        click:selector=>client.click(selector),
        fill:(selector,value)=>{
          // Reply lookup uses only these fixed provider-owned folder queries;
          // message fields always go through the private secret slots.
          if(selector==='input[name="q"]'&&['in:inbox','in:sent','-in:trash -in:spam -in:drafts'].includes(value))return client.runReadCode(`async page=>{await page.locator('input[name="q"]').fill(${JSON.stringify(value)});return true}`);
          return client.fill(selector,value);
        },
        focus:selector=>client.focus(selector),
        press:key=>client.press(key),
        // Reply lookup shares the read-side list observers. Send registrations
        // use a run-code navigation because their fixed login URL can differ
        // from the mailbox route needed to verify the reply target.
        navigate:url=>client.runReadCode(`async page=>{await page.goto(${JSON.stringify(url)});return true}`),
        readMany:commands=>client.readMany(commands),
        runReadCode:code=>client.runReadCode(code),
      });
    },
    withReadTab: async callback => {
      if (!["read", "discover", "capture", "enumerate_thread", "mark_read", "collect_page"].includes(registration.operation)) throw clientContractError();
      return await callback({
        inspect: expression => client.gmailTab().inspect(expression),
        click: selector => client.click(selector),
        fill: (selector, value) => client.runReadCode(`async page => { await page.locator(${JSON.stringify(selector)}).fill(${JSON.stringify(value)}); return true; }`),
        press: key => client.press(key),
        navigate: url => client.navigate(url),
        runReadCode: code => client.runReadCode(code),
        download: (selector, destination, maxBytes) => client.download(selector, destination, maxBytes),
      });
    },
    emailWorkspaceRoot: client.emailWorkspaceRoot,
    captureTimingDiagnostic: client.captureTimingDiagnostic,
  };
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
