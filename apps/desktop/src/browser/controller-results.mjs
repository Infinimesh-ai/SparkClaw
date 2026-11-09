import crypto from "node:crypto";
import { sameControllerBinding } from "./controller-wire.mjs";

export const RESULT_LIMIT = 10 << 20;
export const RESULT_CACHE_LIMIT = 20 << 20;
export const RESULT_TTL_MS = 60000;
export const CHUNK_LIMIT = 32 << 10;

// Reserve the worst-case response before executing. Live data is immutable,
// never evicted for another action, and never consumed by reading its last chunk.
export class ControllerResults {
  constructor({ now = Date.now, maximum = RESULT_CACHE_LIMIT } = {}) {
    this.now = now; this.maximum = maximum;
    this.entries = new Map(); this.reservations = new Set(); this.bytes = 0;
  }
  reserve() {
    this.expire();
    if (this.bytes + (this.reservations.size + 1) * RESULT_LIMIT > this.maximum) return null;
    const reservation = Object.freeze({}); this.reservations.add(reservation); return reservation;
  }
  release(reservation) { this.reservations.delete(reservation); }
  put(reservation, command, payload) {
    if (!this.reservations.has(reservation)) throw new Error("Controller result reservation is invalid");
    const bytes = Buffer.from(JSON.stringify(payload));
    if (bytes.length > RESULT_LIMIT) { bytes.fill(0); throw new Error("Controller response is too large"); }
    const id = `result_${crypto.randomBytes(16).toString("hex")}`;
    const expires = this.now() + RESULT_TTL_MS;
    const descriptor = Object.freeze({ result_id: id, total_bytes: bytes.length,
      sha256: crypto.createHash("sha256").update(bytes).digest("hex"), expires_at: new Date(expires).toISOString() });
    this.entries.set(id, { bytes, descriptor, expires, commandID: command.command_id,
      digest: command.digest, binding: Object.freeze({ ...command.binding }) });
    this.bytes += bytes.length;
    this.release(reservation);
    return descriptor;
  }
  read({ binding, command_id: commandID, result_id: id, offset, length }) {
    this.expire();
    const entry = this.entries.get(id);
    if (!entry || entry.commandID !== commandID || !sameControllerBinding(entry.binding, binding)) throw new Error("Controller result is unavailable");
    if (!Number.isSafeInteger(offset) || !Number.isSafeInteger(length) || offset < 0 || length < 1 || length > CHUNK_LIMIT || offset + length > entry.bytes.length) throw new Error("Controller chunk range is invalid");
    return { schema_version: 1, command_id: commandID, result_id: id, offset, total_bytes: entry.bytes.length,
      data_base64: entry.bytes.subarray(offset, offset + length).toString("base64") };
  }
  inline(command, descriptor) {
    this.expire();
    const entry = this.entries.get(descriptor.result_id);
    if (!entry || entry.commandID !== command.command_id || entry.digest !== command.digest || !sameControllerBinding(entry.binding, command.binding)) throw new Error("Controller result is unavailable");
    return JSON.parse(entry.bytes.toString("utf8"));
  }
  expire() {
    for (const [id, entry] of this.entries) if (entry.expires <= this.now()) {
      this.bytes -= entry.bytes.length; entry.bytes.fill(0); this.entries.delete(id);
    }
  }
  clear() {
    for (const entry of this.entries.values()) entry.bytes.fill(0);
    this.entries.clear(); this.reservations.clear(); this.bytes = 0;
  }
}
