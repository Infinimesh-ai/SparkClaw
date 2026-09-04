import { spawn } from "node:child_process";
import path from "node:path";
import { fileURLToPath } from "node:url";

import { ControllerError } from "./errors.mjs";

const PACKAGE_ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const DEFAULT_MCP_ENTRY = path.join(PACKAGE_ROOT, "node_modules", "@playwright", "mcp", "cli.js");
const MCP_PROTOCOL_VERSION = "2025-06-18";
const PROCESS_EXIT_GRACE_MS = 1500;

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
  }

  info() {
    return {
      client: "playwright-mcp",
      client_version: "0.0.80",
      playwright_version: "1.63.0-alpha-2026-08-31",
      browser_channel: this.browserChannel,
    };
  }

  async open({ token, sessionID }) {
    const args = [
      this.entryPoint,
      "--extension",
      "--browser",
      this.browserChannel,
      "--codegen",
      "none",
      "--image-responses",
      "omit",
      "--snapshot-mode",
      "none",
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
      throw clientError(error);
    }

    const rpc = new StdioJSONRPC(child, this.connectTimeoutMS);
    const client = new PlaywrightMCPClient(child, rpc);
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
      return client;
    } catch (error) {
      await client.close();
      throw clientError(error, sessionID);
    }
  }
}

export class PlaywrightMCPClient {
  constructor(child, rpc) {
    this.child = child;
    this.rpc = rpc;
    this.taskPageOpen = false;
    this.closePromise = null;
  }

  get closed() {
    return this.rpc.closed;
  }

  async createTaskPage() {
    if (this.taskPageOpen) {
      throw new ControllerError("browser_session_invalid", "browser session is invalid", {
        status: 409,
      });
    }
    await this.#callTool("browser_tabs", { action: "new", url: "about:blank" });
    this.taskPageOpen = true;
  }

  async closeTaskPage() {
    if (!this.taskPageOpen) return;
    try {
      await this.#callTool("browser_tabs", { action: "close" });
    } finally {
      this.taskPageOpen = false;
    }
  }

  async close() {
    if (this.closePromise) return this.closePromise;
    this.closePromise = this.#close();
    return this.closePromise;
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
    child.stderr.resume();
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

function clientError(error) {
  if (error instanceof ControllerError) return error;
  return new ControllerError("browser_extension_unavailable", "browser extension is unavailable", {
    status: 503,
    retryable: true,
    cause: error,
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
