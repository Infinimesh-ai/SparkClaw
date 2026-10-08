import crypto from "node:crypto";
import https from "node:https";
import tls from "node:tls";
import { EventEmitter } from "node:events";

// Outbound only, no listener, redirect, raw CDP or legacy relay. Validate PKI
// and the configured leaf pin before transmitting Authorization/grant headers.
export function connectPinnedHostSocket(descriptor, headers, { signal } = {}) {
  if (descriptor.schemaVersion !== 2) return Promise.reject(new Error("Browser host requires pinned HTTPS LAN configuration"));
  return new Promise((resolve, reject) => {
    const url = new URL("/api/v1/browser/hosts/connect", descriptor.origin);
    if (url.protocol !== "https:" || url.origin !== descriptor.origin) return reject(new Error("Browser host origin is invalid"));
    const key = crypto.randomBytes(16).toString("base64");
    const request = https.request(url, {
      method: "GET", agent: false, ca: descriptor.ca,
      signal: AbortSignal.any([AbortSignal.timeout(10000), ...(signal ? [signal] : [])]),
      headers: { ...headers, Connection: "Upgrade", Upgrade: "websocket", "Sec-WebSocket-Key": key, "Sec-WebSocket-Version": "13" },
      checkServerIdentity(hostname, certificate) {
        const error = tls.checkServerIdentity(hostname, certificate);
        if (error) return error;
        if (crypto.createHash("sha256").update(certificate.raw).digest("hex") !== descriptor.certificateSHA256) {
          const mismatch = new Error("Backend certificate fingerprint differs");
          mismatch.code = "SPARKCLAW_TLS_IDENTITY_CONFLICT";
          return mismatch;
        }
      },
    });
    request.on("upgrade", (response, socket, head) => {
      const expected = crypto.createHash("sha1").update(`${key}258EAFA5-E914-47DA-95CA-C5AB0DC85B11`).digest("base64");
      if (response.headers["sec-websocket-accept"] !== expected || response.headers["sec-websocket-protocol"] || response.headers["sec-websocket-extensions"] || response.headers.upgrade?.toLowerCase() !== "websocket") {
        socket.destroy(); reject(new Error("Browser host handshake is invalid")); return;
      }
      const transport = new HostSocket(socket);
      resolve(transport);
      // Install consumer listeners before parsing coalesced welcome bytes.
      queueMicrotask(() => transport.receive(head));
    });
    request.on("response", (response) => { response.resume(); reject(Object.assign(new Error("Browser host access rejected"), { status: response.statusCode })); });
    request.on("error", reject);
    request.end();
  });
}

export class HostSocket extends EventEmitter {
  constructor(socket) {
    super(); this.socket = socket; this.buffer = Buffer.alloc(0); this.fragments = []; this.fragmentBytes = 0; this.fragmentOpcode = 0; this.closed = false;
    socket.on("data", (bytes) => this.receive(bytes));
    socket.on("error", () => this.close());
    socket.on("close", () => { if (!this.closed) { this.closed = true; this.emit("close"); } });
    socket.setTimeout(35000, () => this.close());
  }
  send(value) {
    if (this.closed) throw new Error("Browser host socket is closed");
    const payload = Buffer.from(JSON.stringify(value));
    if (payload.length > 128 << 10) throw new Error("Browser host message exceeds limit");
    this.#frame(1, payload);
  }
  receive(chunk) {
    if (this.closed || !chunk.length) return;
    this.buffer = Buffer.concat([this.buffer, chunk]);
    if (this.buffer.length > 256 << 10) { this.close(); return; }
    while (this.buffer.length >= 2) {
      const first = this.buffer[0], second = this.buffer[1], opcode = first & 15;
      const final = Boolean(first & 128);
      if (first & 112 || second & 128 || ![0, 1, 8, 9, 10].includes(opcode)) { this.close(); return; }
      let size = second & 127, offset = 2;
      if (size === 126) { if (this.buffer.length < 4) return; size = this.buffer.readUInt16BE(2); offset = 4; }
      else if (size === 127) { if (this.buffer.length < 10) return; const large = this.buffer.readBigUInt64BE(2); if (large > 131072n) { this.close(); return; } size = Number(large); offset = 10; }
      if (size > 128 << 10 || opcode >= 8 && (!final || size > 125)) { this.close(); return; }
      if (this.buffer.length < offset + size) return;
      const payload = this.buffer.subarray(offset, offset + size);
      this.buffer = this.buffer.subarray(offset + size);
      if (opcode === 8) { this.close(); return; }
      if (opcode === 9) { this.#frame(10, payload); continue; }
      if (opcode === 10) continue;
      if (opcode === 1) {
        if (this.fragmentOpcode) { this.close(); return; }
        this.fragmentOpcode = 1;
      } else if (!this.fragmentOpcode) { this.close(); return; }
      this.fragmentBytes += size;
      if (this.fragmentBytes > 128 << 10) { this.close(); return; }
      this.fragments.push(payload);
      if (!final) continue;
      const bytes = Buffer.concat(this.fragments);
      this.fragments = []; this.fragmentBytes = 0; this.fragmentOpcode = 0;
      try {
        const text = new TextDecoder("utf-8", { fatal: true }).decode(bytes);
        this.emit("message", JSON.parse(text));
      } catch { this.close(); return; }
    }
  }
  close() {
    if (this.closed) return;
    this.closed = true; this.socket.destroy(); this.emit("close");
  }
  #frame(opcode, payload) {
    const length = payload.length;
    const offset = length < 126 ? 2 : length <= 65535 ? 4 : 10;
    const frame = Buffer.alloc(offset + 4 + length);
    frame[0] = 128 | opcode;
    frame[1] = 128 | (offset === 2 ? length : offset === 4 ? 126 : 127);
    if (offset === 4) frame.writeUInt16BE(length, 2);
    if (offset === 10) frame.writeBigUInt64BE(BigInt(length), 2);
    const mask = crypto.randomBytes(4); mask.copy(frame, offset);
    for (let index = 0; index < length; index++) frame[offset + 4 + index] = payload[index] ^ mask[index % 4];
    this.socket.write(frame);
  }
}
