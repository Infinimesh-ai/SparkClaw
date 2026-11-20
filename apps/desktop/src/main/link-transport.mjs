import { spawn } from "node:child_process";
import { EventEmitter } from "node:events";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const MODULE_DIR = path.dirname(fileURLToPath(import.meta.url));
// One NDJSON line carries at most one link frame (600 KiB) plus its envelope.
const LINE_BYTES = 700 * 1024;

export const LINK_REVERSE_OPERATIONS = Object.freeze([
  "browser.controller.status", "browser.controller.acquire", "browser.controller.execute", "browser.controller.release", "browser.controller.chunk",
  "mcd.account.ensure", "mcd.checkout.prepare", "mcd.checkout.commit", "mcd.checkout.abort", "mcd.operation.status", "mcd.orders.query",
]);

// A failed link operation. Codes are the link contract's error codes.
// notSent is the contract's not_sent: the request never reached a handler
// (no path was up, it could not be written, or the backend refused it
// before running it), so it had no effect. Any other failure of a mutation
// leaves its outcome to be learned by retrying with the same key (design
// 3.12: nothing is queued).
// reason is a business domain's own reason for the failure.
export class LinkError extends Error {
  constructor(code, message, retryable = false, notSent = false, reason = "") {
    super(message || code);
    this.name = "LinkError";
    this.code = code;
    this.retryable = retryable === true;
    this.notSent = notSent === true;
    if (typeof reason === "string" && reason) this.reason = reason;
  }
}

// True when a failed request certainly had no effect on the backend.
export function linkNotSent(error) {
  return error instanceof LinkError && error.notSent;
}

export function linkHelperExecutable({ packaged = false, resourcesPath } = {}) {
  return packaged ? path.join(resourcesPath, "link-helper") : path.resolve(MODULE_DIR, "../../bin/link-helper");
}

// LinkTransport owns the link-helper process (services/link/cmd/link-helper):
// device membership, path selection (LAN first, ISCP Relay as fallback) and
// Grant renewal all stay in the helper. This side correlates requests, relays
// pushed events and answers the backend's reverse calls.
export class LinkTransport extends EventEmitter {
  constructor({ stateDirectory, executable, packaged = false, resourcesPath, spawnProcess = spawn, describe, reverse = LINK_REVERSE_OPERATIONS,
    onReverse, requestTimeoutMS = 30000, restartDelayMS = 1000 } = {}) {
    super();
    if (!path.isAbsolute(stateDirectory || "")) throw new Error("Link state directory must be absolute");
    Object.assign(this, { stateDirectory, spawnProcess, describe, reverse: [...reverse], onReverse, requestTimeoutMS, restartDelayMS });
    this.executable = executable || linkHelperExecutable({ packaged, resourcesPath });
    this.pending = new Map();
    this.nextID = 0;
    this.path = "";
    this.hello = null;
    this.opened = false;
    this.closed = false;
    this.generation = 0;
  }

  get online() { return this.path !== ""; }

  // Starts the helper once; resolves after its hello line.
  start() {
    if (this.closed) return Promise.reject(new LinkError("backend_unavailable", "Link transport is closed", false, true));
    if (this.ready) return this.ready;
    fs.mkdirSync(this.stateDirectory, { recursive: true, mode: 0o700 });
    const generation = ++this.generation;
    const child = this.spawnProcess(this.executable, ["-state", this.stateDirectory], { stdio: ["pipe", "pipe", "pipe"], windowsHide: true });
    this.child = child;
    let buffer = "";
    this.ready = new Promise((resolve, reject) => {
      this.helloWaiter = { resolve, reject };
    });
    // The helper's diagnostics may include device identifiers; never echo them.
    child.stderr?.on("data", () => {});
    child.stdout.setEncoding?.("utf8");
    child.stdout.on("data", (chunk) => {
      if (generation !== this.generation) return;
      buffer += typeof chunk === "string" ? chunk : Buffer.from(chunk).toString("utf8");
      for (;;) {
        const newline = buffer.indexOf("\n");
        if (newline < 0) {
          if (buffer.length > LINE_BYTES) this.#exited(generation, new Error("Link helper line exceeds bounds"));
          return;
        }
        const line = buffer.slice(0, newline);
        buffer = buffer.slice(newline + 1);
        if (!line.trim()) continue;
        let message;
        try { message = JSON.parse(line); } catch { continue; }
        this.#receive(message);
      }
    });
    const exited = (error) => this.#exited(generation, error);
    child.on("error", exited);
    child.on("exit", () => exited(new Error("Link helper exited")));
    child.stdin.on("error", exited);
    return this.ready;
  }

  #receive(message) {
    if (message.event === "hello") { this.helperVersion = message.version; this.helloWaiter?.resolve(); this.helloWaiter = undefined; return; }
    if (message.id !== undefined && !message.event) {
      const call = this.pending.get(message.id);
      if (!call) return;
      this.pending.delete(message.id);
      clearTimeout(call.timer);
      call.signal?.removeEventListener("abort", call.abort);
      if (message.error) call.reject(new LinkError(message.error.code || "internal", message.error.message, message.error.retryable, message.error.not_sent, message.error.reason));
      else call.resolve(message.result);
      return;
    }
    if (message.event === "state") {
      const next = typeof message.path === "string" ? message.path : "";
      const previous = this.path;
      this.path = next;
      if (next) this.hello = message.hello || this.hello;
      if (previous !== next) this.emit("state", { path: next, previous, hello: this.hello });
      return;
    }
    if (message.event === "frame") {
      const frame = message.frame;
      if (frame?.type === "event" && typeof frame.topic === "string") this.emit("event", frame);
      return;
    }
    if (message.event === "reverse") void this.#reverse(message);
  }

  async #reverse(message) {
    let reply;
    try {
      if (!this.reverse.includes(message.op) || typeof this.onReverse !== "function") throw new LinkError("unsupported_capability", "This device does not serve the operation");
      reply = { result: await this.onReverse(message.op, message.params ?? null) ?? null };
    } catch (error) {
      reply = { error: { code: error instanceof LinkError ? error.code : "internal", message: error instanceof LinkError ? error.message : "Reverse operation failed" } };
    }
    this.#write({ method: "reverse_response", reverse_id: message.reverse_id, ...reply });
  }

  #write(value) {
    try { this.child?.stdin.write(`${JSON.stringify(value)}\n`); return true; } catch { return false; }
  }

  #exited(generation, error) {
    if (generation !== this.generation) return;
    this.generation++;
    const child = this.child;
    this.child = undefined;
    this.ready = undefined;
    this.helloWaiter?.reject(new LinkError("backend_unavailable", error?.message || "Link helper is unavailable", true, true));
    this.helloWaiter = undefined;
    for (const call of this.pending.values()) {
      clearTimeout(call.timer);
      call.reject(new LinkError("backend_unavailable", "Link helper stopped before the reply", true));
    }
    this.pending.clear();
    try { child?.stdin.destroy(); child?.kill(); } catch { /* already gone */ }
    if (this.path) { const previous = this.path; this.path = ""; this.emit("state", { path: "", previous, hello: this.hello }); }
    this.emit("exit");
    // A crashed helper is restarted and the link reopened; requests made in
    // between fail at once as backend_unavailable.
    if (!this.closed && this.opened) {
      clearTimeout(this.restartTimer);
      this.restartTimer = setTimeout(() => { void this.open().catch(() => {}); }, this.restartDelayMS);
      this.restartTimer.unref?.();
    }
  }

  // call sends one helper method and waits for its reply.
  async call(method, fields = {}, { timeoutMS = this.requestTimeoutMS + 5000, signal } = {}) {
    await this.start();
    if (signal?.aborted) throw new LinkError("cancelled", "Request was canceled");
    const id = `m${++this.nextID}`;
    return new Promise((resolve, reject) => {
      const call = { resolve, reject, signal };
      call.timer = setTimeout(() => { this.pending.delete(id); reject(new LinkError("timeout", "Link helper did not reply in time", true)); }, timeoutMS);
      call.timer.unref?.();
      call.abort = () => { this.pending.delete(id); clearTimeout(call.timer); reject(new LinkError("cancelled", "Request was canceled")); };
      signal?.addEventListener("abort", call.abort, { once: true });
      this.pending.set(id, call);
      if (!this.#write({ id, method, ...fields })) { this.pending.delete(id); clearTimeout(call.timer); reject(new LinkError("backend_unavailable", "Link helper is unavailable", true, true)); }
    });
  }

  status() { return this.call("status"); }

  join(credential, name) { return this.call("join", { credential, name }, { timeoutMS: 90000 }); }

  async open(describe = this.describe) {
    this.describe = describe;
    this.opened = true;
    await this.call("open", { describe, reverse: this.reverse }, { timeoutMS: 60000 });
  }

  // request sends one link operation. When no path is up it fails at once.
  async request(op, params = {}, { timeoutMS = this.requestTimeoutMS, signal } = {}) {
    if (!this.online) throw new LinkError("backend_unavailable", "The backend is unreachable", true, true);
    return this.call("request", { op, params, timeout_ms: timeoutMS }, { timeoutMS: timeoutMS + 5000, signal });
  }

  async closeLink() {
    this.opened = false;
    if (this.child) await this.call("close").catch(() => {});
  }

  async leave() {
    this.opened = false;
    const result = await this.call("leave", {}, { timeoutMS: 60000 });
    this.#offline();
    return result;
  }

  async forget() {
    this.opened = false;
    const result = await this.call("forget");
    this.#offline();
    return result;
  }

  #offline() {
    if (!this.path) return;
    const previous = this.path;
    this.path = "";
    this.emit("state", { path: "", previous, hello: this.hello });
  }

  close() {
    this.closed = true;
    this.opened = false;
    clearTimeout(this.restartTimer);
    const child = this.child;
    this.generation++;
    this.child = undefined;
    this.ready = undefined;
    for (const call of this.pending.values()) { clearTimeout(call.timer); call.reject(new LinkError("backend_unavailable", "Link transport closed", true)); }
    this.pending.clear();
    this.path = "";
    if (child) {
      try { child.stdin.end(); } catch { /* closed */ }
      const timer = setTimeout(() => { try { child.kill(); } catch { /* gone */ } }, 2000);
      timer.unref?.();
    }
  }
}
