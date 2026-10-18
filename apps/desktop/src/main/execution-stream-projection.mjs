import crypto from "node:crypto";

const UUID = /^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/u;
const SHA = /^[a-f0-9]{64}$/u;
const COMMON = ["kind", "request_id", "input_digest", "epoch", "generation", "revision"];
const FIELDS = { delta: ["from_offset", "to_offset", "text", "content_digest"],
  snapshot_begin: ["snapshot_id", "total_bytes", "content_digest"], snapshot_chunk: ["snapshot_id", "offset", "text"],
  snapshot_end: ["snapshot_id", "total_bytes", "content_digest"], state: ["state"], invalidate: ["reason"] };
const STATES = new Set(["accepted", "running", "approval_pending", "browser_login_blocked", "completed", "failed", "unknown", "delivery_expired", "delivered"]);
const INVALIDATIONS = new Set(["reset", "failed", "unknown", "delivered", "delivery_expired", "revoked", "closed", "preview_unavailable"]);
export const STREAM_REASONS = new Set(["terminal", "renew", "canceled", "reset", "auth_expired", "gap", "preview_unavailable"]);
export const streamDigest = value => crypto.createHash("sha256").update(value).digest("hex");
const integer = (value, minimum = 0) => Number.isSafeInteger(value) && value >= minimum;
const exact = (value, fields) => value && typeof value === "object" && !Array.isArray(value) &&
  Object.keys(value).sort().join(",") === [...fields].sort().join(",");
const validText = value => typeof value === "string" && value.isWellFormed() && !value.includes("\0");
const failure = message => { throw new Error(`Invalid execution stream: ${message}`); };

// Only verified text lives here. A snapshot candidate and out-of-order frames
// have no publication path until their entire preceding sequence is verified.
// Nothing in this module writes ClientStore or creates/acknowledges a task.
export class ExecutionStreamProjection {
  constructor({ requestID, inputDigest, maxBytes = 8 * 1024 * 1024, now = () => performance.now() }) {
    Object.assign(this, { requestID, inputDigest, maxBytes, now });
    this.closed = false;
  }

  open(subscriptionID) {
    if (this.closed || !UUID.test(subscriptionID)) failure("closed or invalid subscription");
    this.subscriptionID = subscriptionID;
    this.sequence = 0; this.pending = new Map(); this.seen = new Map(); this.pendingBytes = 0;
    this.source = undefined; this.candidate = undefined; this.completion = undefined; this.gapAt = undefined;
  }

  read() { return this.closed ? undefined : this.visible; }
  cursor() {
    if (!this.visible) return undefined;
    const { epoch, generation, revision, offset, content_digest } = this.visible;
    return { epoch, generation, revision, offset, content_digest };
  }
  discard() { this.visible = undefined; this.candidate = undefined; }
  disconnect() { this.candidate = undefined; this.pending?.clear(); this.pendingBytes = 0; this.gapAt = undefined; }
  close() { this.closed = true; this.discard(); this.disconnect(); }

  event(frame) {
    if (this.closed) return false;
    const before = this.visible;
    if (frame?.type === "iscp.application.stream.control.v1") return this.#control(frame);
    if (!exact(frame, ["type", "schema_version", "subscription_id", "stream_seq", "body"]) ||
        frame.type !== "iscp.application.stream.v1" || frame.schema_version !== 1 ||
        frame.subscription_id !== this.subscriptionID || !integer(frame.stream_seq, 1) ||
        Buffer.byteLength(JSON.stringify(frame)) > 65536) failure("frame");
    this.#validateBody(frame.body);
    const fingerprint = streamDigest(JSON.stringify(frame.body)), seq = frame.stream_seq, now = this.now();
    for (const [number, record] of this.seen) if (record.at < now - 30000) this.seen.delete(number);
    if (seq <= this.sequence) {
      if (this.seen.get(seq)?.fingerprint !== fingerprint) failure("unproven or conflicting duplicate");
      return false;
    }
    if (this.completion && seq > this.completion.last_stream_seq) failure("frame after terminal boundary");
    if (this.pending.has(seq)) {
      if (this.pending.get(seq).fingerprint !== fingerprint) failure("conflicting pending duplicate");
      return false;
    }
    const bytes = Buffer.byteLength(JSON.stringify(frame));
    if (this.pendingBytes + bytes > this.maxBytes) failure("pending result budget");
    this.pending.set(seq, { body: frame.body, fingerprint, bytes }); this.pendingBytes += bytes;
    let changed = false;
    while (this.pending.has(this.sequence + 1)) {
      const next = this.pending.get(this.sequence + 1);
      changed = this.#apply(next.body) || changed;
      this.pending.delete(++this.sequence); this.pendingBytes -= next.bytes;
      this.seen.set(this.sequence, { fingerprint: next.fingerprint, at: now });
    }
    const missing = this.pending.size > 0 || this.completion && this.sequence < this.completion.last_stream_seq;
    this.gapAt = missing ? this.gapAt ?? now : undefined;
    return changed || before !== this.visible;
  }

  complete(value) {
    if (this.closed) return;
    if (!exact(value, ["schema_version", "last_stream_seq", "reason"]) || value.schema_version !== 1 ||
        !integer(value.last_stream_seq) || !STREAM_REASONS.has(value.reason) || value.last_stream_seq < this.sequence ||
        [...this.pending.keys()].some(seq => seq > value.last_stream_seq)) failure("terminal boundary");
    this.completion = value;
    if (["canceled", "reset", "auth_expired"].includes(value.reason)) { this.discard(); this.disconnect(); return; }
    if (this.sequence < value.last_stream_seq) this.gapAt ??= this.now();
    else if (this.candidate) failure("incomplete terminal snapshot");
  }

  get settled() {
    return this.completion && (["canceled", "reset", "auth_expired"].includes(this.completion.reason) ||
      this.sequence === this.completion.last_stream_seq && !this.candidate);
  }
  get gapExpired() { return this.gapAt !== undefined && this.now() - this.gapAt >= 2000; }

  #validateBody(body, control = false) {
    if (!FIELDS[body?.kind] || !exact(body, [...COMMON, ...FIELDS[body.kind]]) ||
        (body.kind === "invalidate") !== control || body.request_id !== this.requestID || body.input_digest !== this.inputDigest ||
        !UUID.test(body.epoch) || !UUID.test(body.generation) || !integer(body.revision, 1)) failure("binding or schema");
    if (body.kind === "state" && !STATES.has(body.state)) failure("state");
    if (control && !INVALIDATIONS.has(body.reason)) failure("invalidation reason");
    if (body.kind.startsWith("snapshot_") && !UUID.test(body.snapshot_id)) failure("snapshot identity");
    for (const key of ["from_offset", "to_offset", "offset", "total_bytes"]) {
      if (Object.hasOwn(body, key) && (!integer(body[key]) || body[key] > this.maxBytes)) failure("byte offset");
    }
    if (Object.hasOwn(body, "text") && !validText(body.text)) failure("UTF-8 text");
    if (Object.hasOwn(body, "content_digest") && !SHA.test(body.content_digest)) failure("digest");
  }

  #control(frame) {
    // A late invalidate is never permission to clear another subscription or
    // source version; reject its effect before inspecting its body further.
    if (frame.subscription_id !== this.subscriptionID || frame.schema_version !== 1 ||
        frame.body?.request_id !== this.requestID || frame.body?.input_digest !== this.inputDigest) return false;
    if (!exact(frame, ["type", "schema_version", "subscription_id", "action", "body"]) ||
        frame.schema_version !== 1 || frame.action !== "invalidate") failure("control");
    this.#validateBody(frame.body, true);
    const source = this.source || this.visible;
    if (!source || frame.body.epoch !== source.epoch || frame.body.generation !== source.generation ||
        frame.body.revision < Math.max(this.visible?.revision ?? 0, this.candidate?.revision ?? 0)) return false;
    const changed = Boolean(this.visible); this.discard();
    this.completion = { schema_version: 1, last_stream_seq: this.sequence, reason: "reset" };
    this.disconnect(); return changed;
  }

  #apply(body) {
    if (this.source && (body.epoch !== this.source.epoch || body.generation !== this.source.generation)) failure("source drift");
    this.source ??= { epoch: body.epoch, generation: body.generation };
    if (this.visible && (body.epoch !== this.visible.epoch || body.generation !== this.visible.generation)) this.discard();
    const previous = this.visible;
    if (previous && body.revision < previous.revision) failure("revision rollback");
    if (body.kind === "snapshot_begin") {
      if (this.candidate) failure("overlapping snapshot");
      this.candidate = { ...body, content: "", offset: 0 }; return false;
    }
    if (body.kind === "snapshot_chunk" || body.kind === "snapshot_end") {
      const candidate = this.candidate;
      if (!candidate || body.snapshot_id !== candidate.snapshot_id || body.revision !== candidate.revision) failure("snapshot binding");
      if (body.kind === "snapshot_chunk") {
        const length = Buffer.byteLength(body.text);
        if (!length || body.offset !== candidate.offset || candidate.offset + length > candidate.total_bytes) failure("snapshot offset");
        candidate.content += body.text; candidate.offset += length; return false;
      }
      if (body.total_bytes !== candidate.total_bytes || body.content_digest !== candidate.content_digest ||
          candidate.offset !== candidate.total_bytes || streamDigest(candidate.content) !== candidate.content_digest) failure("snapshot digest");
      this.#publish(body, candidate.content); this.candidate = undefined; return true;
    }
    if (this.candidate) failure("event interleaved with snapshot");
    if (body.kind === "delta") {
      const content = (previous?.content ?? "") + body.text;
      if (!body.text || body.from_offset !== (previous?.offset ?? 0) || body.to_offset !== Buffer.byteLength(content) ||
          body.to_offset <= body.from_offset || previous && body.revision <= previous.revision ||
          streamDigest(content) !== body.content_digest) failure("delta continuity");
      this.#publish(body, content); return true;
    }
    if (["failed", "canceled", "unknown", "delivery_expired"].includes(body.state)) {
      this.discard(); return Boolean(previous);
    }
    if (previous) this.visible = { ...previous, revision: body.revision, state: body.state };
    return false;
  }

  #publish(body, content) {
    const offset = Buffer.byteLength(content), content_digest = streamDigest(content);
    if (this.visible?.revision === body.revision && (this.visible.offset !== offset || this.visible.content_digest !== content_digest)) failure("revision conflict");
    this.visible = Object.freeze({ request_id: this.requestID, input_digest: this.inputDigest, ...this.source,
      revision: body.revision, offset, content_digest, content, state: this.visible?.state ?? "running" });
  }
}
