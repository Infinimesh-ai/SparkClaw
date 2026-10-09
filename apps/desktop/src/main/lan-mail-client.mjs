import crypto from "node:crypto";
import { ISCPMutationJournal } from "./iscp-mutation-journal.mjs";
import { LocalMailAttachmentError, localAttachmentErrorResponse, prepareMailAttachments } from "./iscp-mail-attachments.mjs";

const ID = /^[A-Za-z0-9][A-Za-z0-9_.:-]{0,159}$/u;
const unknown = () => new LocalMailAttachmentError("The previous mail change has an unknown outcome. Reload the saved draft or reconcile its send receipt before continuing; it will not be repeated.", "email_send_unknown");

// Main-process only. The renderer can select ClientStore IDs but cannot call
// the binary upload endpoint. The HTTP authority remains the backend service.
export class LANMailClient {
  constructor({ root, scope, readLocalFile, generation, ready }) {
    Object.assign(this, { scope, readLocalFile, currentGeneration: generation, ready });
    this.mutations = new ISCPMutationJournal(root, scope);
    this.pending = new Set();
  }

  async fetch(raw, init, next) {
    const url = new URL(raw), method = (init.method || "GET").toUpperCase();
    if (url.pathname.includes("%")) return localAttachmentErrorResponse(new LocalMailAttachmentError("Invalid mail path."));
    const match = /^\/api\/email\/drafts(?:\/([^/]+)(?:\/(send|reconcile))?)?$/u.exec(url.pathname);
    if (!match) return next(raw, init);
    let body;
    try { body = init.body === undefined ? undefined : JSON.parse(typeof init.body === "string" ? init.body : Buffer.from(init.body).toString("utf8")); }
    catch { return localAttachmentErrorResponse(new LocalMailAttachmentError("Invalid mail draft request.")); }
    const draftID = match[1] || url.searchParams.get("draft") || body?.id;
    if (!draftID || !ID.test(draftID) || method !== "GET" && url.search) {
      return method === "GET" && !draftID ? next(raw, init) : localAttachmentErrorResponse(new LocalMailAttachmentError("Invalid mail draft identity."));
    }
    const resource = `mail-draft:${draftID}`;
    const save = (method === "POST" && !match[1] || method === "PUT" && match[1] && !match[2]) && !url.search;
    const send = method === "POST" && match[2] === "send" && !url.search;
    const reconcile = method === "POST" && match[2] === "reconcile" && !url.search;
    if (!save && !send) {
      const response = await next(raw, init);
      // Reload is the explicit recovery path for a save whose receipt was
      // lost. Sending is released only by the separate receipt-only reconcile.
      const prior = this.mutations.read(resource);
      if (response.ok && prior && !this.pending.has(resource) && (method === "GET" && prior.operation === "mail.drafts.save" || reconcile)) {
        const value = await boundedJSON(response.clone());
        if (value.id === draftID && (prior.operation === "mail.drafts.save" || !["unknown", "sending"].includes(value.state))) this.mutations.complete(resource, prior.operation_id);
      }
      return response;
    }
    if (this.pending.has(resource) || this.mutations.read(resource)) return localAttachmentErrorResponse(unknown());
    this.pending.add(resource);
    try {
      const generation = this.currentGeneration();
      const fence = () => { init.signal?.throwIfAborted(); if (generation !== this.currentGeneration() || !this.ready()) throw new Error("Attachment authentication changed"); };
      const internal = async (pathname, options = {}) => {
        fence();
        const response = await next(`${url.origin}${pathname}`, { ...init, ...options, headers: new Headers(options.headers || { Accept: "application/json" }) });
        fence();
        const value = await boundedJSON(response);
        if (!response.ok) throw new LocalMailAttachmentError(value.error || "Mail attachment request failed.", value.code || "email_attachment_invalid");
        return value;
      };
      const capabilities = await internal("/api/email/compose-capabilities", { method: "GET", body: undefined });
      const client = this;
      const transport = {
        get generation() { return client.currentGeneration(); },
        objectScopeKey: JSON.stringify(this.scope),
        get state() { return client.ready() ? "transport_ready" : "disconnected"; },
        expectedBinding: this.scope, readLocalFile: this.readLocalFile,
        canSendMailAttachments: () => capabilities.workspace_attachments === true,
        invoke: async (_operation, _body, options) => {
          const draft = await internal(`/api/email/drafts/${encodeURIComponent(options.params.draft)}`, { method: "GET", body: undefined });
          if (["unknown", "sending"].includes(draft.state)) throw unknown();
          return draft;
        },
        objects: { upload: async (bytes, manifest) => internal("/api/v1/mail/attachments", {
          method: "POST", body: bytes,
          headers: { "Content-Type": "application/octet-stream", "X-SparkClaw-Transfer": crypto.randomUUID(), "X-SparkClaw-File-Name": encodeURIComponent(manifest.name), "X-SparkClaw-Digest": crypto.createHash("sha256").update(bytes).digest("hex") },
        }) },
      };
      const request = { operation: send ? "mail.drafts.send" : "mail.drafts.save", params: { draft: draftID }, body };
      const prepared = await prepareMailAttachments(transport, request, init.signal);
      fence();
      const record = this.mutations.begin(resource, request);
      const response = await next(raw, { ...init, body: JSON.stringify(prepared.body) });
      fence();
      // A proxy 5xx is not proof that the business handler did not run.
      if (response.status < 500) {
        const result = await boundedJSON(response.clone());
        if (![result.code, result.error_code, response.headers.get("x-sparkclaw-error-code")].includes("operation_outcome_unknown")) this.mutations.complete(resource, record.operation_id);
      }
      return response;
    } catch (error) {
      if (error instanceof LocalMailAttachmentError) return localAttachmentErrorResponse(error);
      throw error;
    } finally { this.pending.delete(resource); }
  }
}

async function boundedJSON(response) {
  const reader = response.body?.getReader();
  if (!reader) throw new Error("Mail response is empty");
  const chunks = []; let bytes = 0;
  try {
    for (;;) {
      const part = await reader.read();
      if (part.done) return JSON.parse(Buffer.concat(chunks).toString("utf8"));
      bytes += part.value.length;
      if (bytes > 2 << 20) throw new Error("Mail response is oversized");
      chunks.push(Buffer.from(part.value));
    }
  } catch (error) { await reader.cancel().catch(() => {}); throw error; }
}
