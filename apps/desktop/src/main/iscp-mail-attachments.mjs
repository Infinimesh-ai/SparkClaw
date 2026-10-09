const UUID = /^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/u;
const MAX_COUNT = 5;
const MAX_BYTES = 10 * 1024 * 1024;
export class LocalMailAttachmentError extends Error {
  constructor(message, code = "email_attachment_invalid") { super(message); this.code = code; }
}
const invalid = () => new LocalMailAttachmentError("Choose an existing file from this SparkX workbench's local data.");
const changed = () => new LocalMailAttachmentError("A local attachment changed or is unavailable. Reload, select the current file, save and review again. This attempt was not sent.", "email_attachment_changed");

// Called only after the durable operation journal has ruled out an unknown
// mutation. Renderer paths, hashes and object references are never trusted.
export async function prepareMailAttachments(transport, request, signal) {
  if (!["mail.drafts.save", "mail.drafts.send"].includes(request.operation)) return request;
  const generation = transport.generation, scopeKey = transport.objectScopeKey;
  const binding = transport.expectedBinding || transport.capabilities.binding;
  const scope = { deployment_id: binding.deployment_id, owner_id: binding.owner_id, client_id: binding.client_id };
  const fence = () => {
    signal?.throwIfAborted();
    if (generation !== transport.generation || scopeKey !== transport.objectScopeKey || transport.state !== "transport_ready") throw new Error("ISCP authentication changed");
  };
  const read = id => {
    fence();
    if (transport.canSendMailAttachments?.() !== true) throw new LocalMailAttachmentError("Local attachment access is unavailable for this authorization.");
    if (!UUID.test(id) || !transport.readLocalFile) throw invalid();
    return transport.readLocalFile(scope, id, MAX_BYTES);
  };
  if (request.operation === "mail.drafts.send") {
    const draft = await transport.invoke("mail.drafts.list", undefined, { params: { draft: request.params.draft }, signal });
    fence();
    if (draft.id !== request.params.draft || draft.version !== request.body?.expected_version) throw new LocalMailAttachmentError("The saved draft changed. Reload and review this version before sending.", "email_conflict");
    // Unknown/sent drafts belong to receipt reconciliation, never source checks.
    if (!["draft", "failed"].includes(draft.state)) return request;
    for (const attachment of draft.attachments || []) {
      let file;
      try {
        file = read(attachment.local_file_id);
        if (file.name !== attachment.name || file.size !== attachment.size_bytes || `sha256:${file.sha256}` !== attachment.sha256 || attachment.object?.purpose !== "mail_send_attachment" || attachment.object?.sha256 !== file.sha256 || attachment.object?.size !== file.size) throw changed();
      } catch { throw changed(); }
      finally { file?.content.fill(0); }
    }
    fence();
    return request;
  }
  const inputs = request.body?.attachments;
  if (inputs === undefined) return request;
  if (!Array.isArray(inputs) || inputs.length > MAX_COUNT) throw invalid();
  const seen = new Set(), files = [];
  let size = 0;
  try {
    for (const input of inputs) {
      if (!input || Object.keys(input).join(",") !== "local_file_id" || !UUID.test(input.local_file_id) || seen.has(input.local_file_id)) throw invalid();
      seen.add(input.local_file_id);
    }
    for (const input of inputs) {
      let file;
      try { file = read(input.local_file_id); } catch { throw changed(); }
      files.push({ id: input.local_file_id, file });
      size += file.size;
      if (size > MAX_BYTES) throw new LocalMailAttachmentError("Attachments exceed the 10 MiB total limit.");
    }
    if (files.length && !transport.objects) throw invalid();
    const attachments = [];
    for (const { id, file } of files) {
      fence();
      const object = await transport.objects.upload(file.content, { purpose: "mail_send_attachment", name: file.name }, signal);
      fence();
      if (object.purpose !== "mail_send_attachment" || object.size !== file.size || object.sha256 !== file.sha256 || object.name !== file.name) throw new Error("Attachment transfer manifest differs");
      attachments.push({ local_file_id: id, object });
    }
    return { ...request, body: { ...request.body, attachments } };
  } finally { for (const { file } of files) file.content.fill(0); }
}

export function localAttachmentErrorResponse(error) {
  return new Response(JSON.stringify({ error: error.message, code: error.code, error_code: error.code, retryable: false }), {
    status: error.code === "email_attachment_invalid" ? 400 : 409,
    headers: { "content-type": "application/json", "x-sparkclaw-error-code": error.code },
  });
}
