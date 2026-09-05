import { extensionRejected } from "./mcp-errors.mjs";

export const MAX_MCP_RESPONSE_BYTES = 8 << 20;

export const BRIDGE_REJECTION_MARKER = "browser_extension_rejected";

export class StdioJSONRPC {
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

export class StreamingMarkerDetector {
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

export async function waitForExit(child, timeoutMS) {
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
