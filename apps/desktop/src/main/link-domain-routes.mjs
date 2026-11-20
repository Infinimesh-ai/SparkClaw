// The HTTP routes of the original screens and the typed domain method each
// one reaches through domain.call (docs/workbench-retirement-migration.md
// M4). The route only names the method and turns the request into the
// method's arguments; the backend never sees a path.

// A record a route names: never a path.
const ID = /^[A-Za-z0-9][A-Za-z0-9_.:-]{0,159}$/u;

// The presentation reads of the original screens.
export const PRESENTATION_ROUTES = Object.freeze({ "GET /api/config": "presentation.config", "GET /api/owner": "presentation.owner", "GET /readyz": "presentation.ready" });

// Domains on typed link methods (docs/workbench-retirement-migration.md M4).
// Each route of the original screens names its method, how the request
// becomes the method's arguments, and the HTTP answer each failure reason
// had, so the screens keep their behavior while the link carries typed
// arguments and link error codes.
const body = ({ body }) => body ?? {};
// The body's fields and the records the route names.
const recordArgs = ({ params, body }) => ({ ...(body ?? {}), ...params });
// A mail list's filters, typed: a page size is a number and the
// unassigned filter a boolean.
const LIST_QUERY = Object.freeze(["mailbox_id", "q", "cursor", "limit", "entry", "unassigned_only"]);
function listArgs(query) {
  const args = { ...query };
  if (query.limit !== undefined) {
    if (!/^[1-9][0-9]{0,2}$/u.test(query.limit)) throw new Error("Query is invalid");
    args.limit = Number(query.limit);
  }
  if (query.unassigned_only !== undefined) {
    if (!["true", "false"].includes(query.unassigned_only)) throw new Error("Query is invalid");
    args.unassigned_only = query.unassigned_only === "true";
  }
  return args;
}
export const TYPED_ROUTES = Object.freeze([
  // medicine
  { method: "POST", path: "/api/medicine/sync", op: "medicine.sync", args: body },
  { method: "GET", path: "/api/medicine/operations/{operation}", op: "medicine.operations.get", args: ({ params }) => ({ operation_id: params.operation }) },
  { method: "POST", path: "/api/medicine/items", op: "medicine.items.create", mutation: true, args: body },
  { method: "POST", path: "/api/medicine/items/batch", op: "medicine.items.batch", mutation: true, args: body },
  { method: "PUT", path: "/api/medicine/items/{item}", op: "medicine.items.update", mutation: true, args: ({ params, body }) => ({ ...body, item_id: params.item }) },
  { method: "DELETE", path: "/api/medicine/items/{item}", op: "medicine.items.delete", mutation: true, args: ({ params, body }) => ({ ...body, item_id: params.item }) },
  { method: "POST", path: "/api/medicine/import", op: "medicine.import", mutation: true, args: body },
  { method: "POST", path: "/api/medicine/recognize", op: "medicine.recognize", args: body },
  { method: "POST", path: "/api/medicine/parse", op: "medicine.parse", args: body },
  // notifications
  { method: "GET", path: "/api/notifications", op: "notifications.list", query: ["limit", "after"],
    args: ({ query }) => ({ ...(query.limit ? { limit: Number(query.limit) } : {}), ...(query.after ? { after: query.after } : {}) }) },
  { method: "POST", path: "/api/notifications/read-all", op: "notifications.read_all", mutation: true, args: body },
  { method: "POST", path: "/api/notifications/{id}/read", op: "notifications.read", mutation: true, args: ({ params }) => ({ id: params.id }) },
  // settings: answered with the domain result {status, body} the settings
  // screens read; the record a route names becomes an argument.
  ...[
    ["GET", "/api/owner", "settings.owner.get"], ["POST", "/api/owner", "settings.owner.patch", true],
    ["GET", "/api/connectors", "settings.connectors.list"], ["PATCH", "/api/connectors/{channel}", "settings.connectors.patch", true],
    ["GET", "/api/integrations", "settings.integrations.list"],
    ["POST", "/api/integrations/{integration_id}/credentials", "settings.credentials.add", true],
    ["PUT", "/api/integrations/{integration_id}/active-credential", "settings.credentials.activate", true],
    ["POST", "/api/integrations/{integration_id}/credentials/{credential_id}/check", "settings.credentials.check", true],
    ["DELETE", "/api/integrations/{integration_id}/credentials/{credential_id}", "settings.credentials.delete", true],
    ["POST", "/api/notification-bindings/{channel}/start", "settings.bindings.start", true],
    ["GET", "/api/notification-bindings/{binding_id}", "settings.bindings.get"],
    ["POST", "/api/notification-bindings/{binding_id}/poll", "settings.bindings.poll"],
    ["DELETE", "/api/notification-bindings/{binding_id}", "settings.bindings.revoke", true],
    ["GET", "/api/notification-bindings/{binding_id}/login-url", "settings.bindings.login"],
    ["GET", "/api/browser/extension", "settings.browser.get"], ["PUT", "/api/browser/extension/token", "settings.browser.save", true],
    ["POST", "/api/browser/extension/check", "settings.browser.check", true], ["DELETE", "/api/browser/extension/token", "settings.browser.delete", true],
    ["GET", "/api/mcp-access/catalog", "settings.mcp.catalog"], ["PATCH", "/api/mcp-access/transports", "settings.mcp.transports", true],
    ["GET", "/api/mcp-access/tickets", "settings.mcp.tickets.list"], ["POST", "/api/mcp-access/tickets", "settings.mcp.tickets.issue", true],
    ["POST", "/api/mcp-access/tickets/{ticket_id}/revoke", "settings.mcp.tickets.revoke", true],
    ["DELETE", "/api/mcp-access/tickets/{ticket_id}", "settings.mcp.tickets.delete", true],
    ["GET", "/api/mcp-access/bindings", "settings.mcp.bindings.list"],
    ["POST", "/api/mcp-access/bindings/{binding_id}/revoke", "settings.mcp.bindings.revoke", true],
    ["DELETE", "/api/mcp-access/bindings/{binding_id}", "settings.mcp.bindings.delete", true],
    ["DELETE", "/api/mcp-access/records", "settings.mcp.records.delete", true],
    ["GET", "/api/iscp-pairing/status", "settings.mcp.pairing.status"], ["GET", "/api/iscp-pairing/onboardings", "settings.mcp.pairing.onboardings"],
    ["POST", "/api/iscp-pairing/start", "settings.mcp.pairing.start", true],
  ].map(([method, path, op, mutation]) => ({ method, path, op, reply: true, ...(mutation ? { mutation: true } : {}), args: recordArgs })),
  { method: "GET", path: "/api/notification-bindings", op: "settings.bindings.list", reply: true, query: ["channel", "status"], args: ({ query }) => ({ ...query }) },
  // mail: the mail window and the composer, answered with the domain result.
  ...[
    ["GET", "/api/email/providers", "mail.providers.list"],
    ["PATCH", "/api/email/providers/{provider}", ({ body }) => body && Object.hasOwn(body, "intake_enabled") ? "mail.intake.update" : "mail.providers.update", true],
    ["POST", "/api/email/providers/{provider}/check", "mail.providers.check", true],
    ["POST", "/api/email/providers/{provider}/login-browser", "mail.providers.login", true],
    ["GET", "/api/v1/mail/mailboxes", "mail.mailboxes"], ["POST", "/api/v1/mail/{mailbox}/sync", "mail.sync"],
    ["GET", "/api/v1/mail/{mailbox}/messages/{mail}/attachments/{part}", "mail.attachment"],
    ["POST", "/api/email/messages/viewed", "mail.viewed", true],
    ["GET", "/api/email/messages/{mail}", "mail.message"], ["GET", "/api/email/messages/{mail}/verification", "mail.verification"],
    ["GET", "/api/email/messages/{mail}/render-preview", "mail.render_preview"],
    ["POST", "/api/email/messages/{mail}/classification", "mail.classification", true], ["POST", "/api/email/messages/{mail}/assignment", "mail.assignment", true],
    ["POST", "/api/email/messages/{mail}/reanalyze", "mail.reanalyze", true],
    ["GET", "/api/email/conversations/{conversation}", "mail.conversations.get"],
    ["POST", "/api/email/conversations/{conversation}/rename", "mail.conversations.rename", true],
    ["DELETE", "/api/email/conversations/{conversation}", "mail.conversations.delete", true],
    ["POST", "/api/email/source/cleanup", "mail.source.cleanup", true], ["POST", "/api/email/sender-rules/{rule}", "mail.sender_rules.update", true],
    ["POST", "/api/email/presentations/ensure", "mail.presentations.ensure", true], ["POST", "/api/email/replies/polish", "mail.replies.polish", true],
    ["GET", "/api/email/compose-capabilities", "mail.compose.capabilities"], ["GET", "/api/email/sync-status", "mail.sync.status"],
    ["POST", "/api/email/sync-warnings/{warning}/acknowledge", "mail.sync.acknowledge", true], ["POST", "/api/email/sync", "mail.sync.request", true],
    ["POST", "/api/email/drafts", "mail.drafts.save", true], ["POST", "/api/email/drafts/{draft}/send", "mail.drafts.send", true],
    ["POST", "/api/email/drafts/{draft}/reconcile", "mail.drafts.reconcile", true], ["POST", "/api/email/drafts/{draft}/resolve", "mail.drafts.resolve", true],
    ["GET", "/api/email/drafts/{draft}", "mail.drafts.list"],
  ].map(([method, path, op, mutation]) => ({ method, path, op, reply: true, ...(mutation ? { mutation: true } : {}), args: recordArgs })),
  // The original composer saves a draft with PUT on the draft itself.
  { method: "PUT", path: "/api/email/drafts/{draft}", op: "mail.drafts.save", reply: true, mutation: true, args: ({ params, body }) => {
    if (!body || typeof body !== "object" || body.id !== undefined && body.id !== params.draft) throw new Error("Draft request is invalid");
    return { ...body, draft: params.draft };
  } },
  ...[
    ["/api/email/conversations", "mail.conversations.list"], ["/api/email/pending", "mail.pending"], ["/api/email/interaction-mails", "mail.interaction"],
    ["/api/email/sent-sources", "mail.sent_sources"], ["/api/email/sender-rules", "mail.sender_rules.list"],
  ].map(([path, op]) => ({ method: "GET", path, op, reply: true, query: LIST_QUERY, args: ({ query }) => listArgs(query) })),
  { method: "GET", path: "/api/email/notifications", op: "mail.notifications", reply: true, query: [...LIST_QUERY, "subtype", "validity"], args: ({ query }) => listArgs(query) },
  { method: "GET", path: "/api/email/conversations/{conversation}/messages", op: "mail.conversations.messages", reply: true, query: LIST_QUERY,
    args: ({ params, query }) => ({ ...listArgs(query), conversation: params.conversation }) },
  { method: "GET", path: "/api/email/drafts", op: "mail.drafts.list", reply: true, query: ["cursor", "mailbox_id", "q", "limit", "draft"], args: ({ query }) => listArgs(query) },
  { method: "GET", path: "/api/email/sync-warnings", op: "mail.sync.warnings", reply: true, query: ["mailbox_id", "cursor", "limit"], args: ({ query }) => listArgs(query) },
  { method: "GET", path: "/api/email/messages/{mail}/file", op: "mail.file", reply: true, query: ["part_id"], args: ({ params, query }) => ({ mail: params.mail, ...query }) },
  // A page of the mail window asks for up to 100 presentations at once.
  { method: "GET", path: "/api/email/presentations", op: "mail.presentations.get", reply: true, query: ["target_kind", "language"], multi: { target_id: 100 },
    args: ({ query, multi }) => ({ ...query, ...(multi.target_id ? { target_ids: multi.target_id } : {}) }) },
].map(Object.freeze));

// Typed methods that clients call by name rather than by route.
const NAMED_METHODS = Object.freeze([
  // speech: frames and event reads are idempotent by sequence and cursor.
  ["speech.status", { mutation: false }], ["speech.transcribe", { mutation: true }], ["speech.cancel", { mutation: true }],
  ["speech.session.open", { mutation: true }], ["speech.session.frame", { mutation: false }], ["speech.session.finish", { mutation: true }],
  ["speech.session.cancel", { mutation: true }], ["speech.session.events", { mutation: false }],
  // browser write fences, and McDonald's sessions, which recover from their
  // original request ids.
  ["browser.receipt", { mutation: false }], ["browser.reconcile", { mutation: true }],
  ["mcd.session.provision", { mutation: false }], ["mcd.session.revoke", { mutation: false }], ["mcd.session.status", { mutation: false }],
  // mail: sending a saved draft, a receiving change, and settling a
  // receiving change whose reply was lost.
  ["mail.send", { mutation: true, reply: true }], ["mail.providers.update", { mutation: true, reply: true }], ["mail.intake.update", { mutation: true, reply: true }], ["mail.intake.reconcile", { mutation: false }],
]);

// The HTTP status each domain failure reason had in the original screens.
export const REASON_STATUS = Object.freeze({
  medicine_invalid: 400, medicine_conflict: 409, medicine_not_found: 404, medicine_capacity: 507, medicine_sync_reset_required: 409,
  medicine_unknown_outcome: 503, medicine_unavailable: 503, medicine_photo_invalid: 400, medicine_text_invalid: 400,
  medicine_recognition_unavailable: 503, medicine_parse_unavailable: 503,
  invalid_input: 400, cursor_expired: 409, invalid_watermark: 400, watermark_not_owned: 403, watermark_expired: 409,
  notification_not_found: 404, notifications_unavailable: 503, notifications_outcome_unknown: 503,
  speech_unavailable: 503, speech_disabled: 503, speech_stream_unavailable: 503, speech_receipt_unavailable: 503, speech_session_not_found: 404,
  speech_session_closed: 409, speech_frame_conflict: 409, speech_stream_overrun: 409, speech_request_in_progress: 409, speech_audio_unavailable: 409,
  speech_session_capacity: 429, speech_frame_too_large: 413, speech_audio_too_long: 413, speech_session_outcome_unknown: 409, invalid_language: 400,
  invalid_audio_object: 400, speech_invalid_frame: 400,
  browser_outcome_unknown: 409, browser_fenced: 409, browser_unavailable: 503,
  mcd_session_invalid: 400, mcd_session_scope: 403, mcd_session_conflict: 409, mcd_session_unavailable: 503, mcd_selection_unavailable: 503,
});

export const TYPED_METHODS = Object.freeze(new Map([...TYPED_ROUTES.filter(route => typeof route.op === "string").map(route => [route.op, { mutation: route.mutation === true, reply: route.reply === true }]), ...NAMED_METHODS]));

// The typed route of a request, or undefined.
export function mapTypedRoute(url, method, requestBody) {
  if (url.hash || url.username || url.password) return undefined;
  const segments = url.pathname.split("/");
  for (const route of TYPED_ROUTES) {
    if (route.method !== method) continue;
    const template = route.path.split("/");
    if (template.length !== segments.length) continue;
    const params = {};
    let matched = true;
    for (let i = 0; i < template.length && matched; i++) {
      if (template[i].startsWith("{") && template[i].endsWith("}")) {
        let value;
        try { value = decodeURIComponent(segments[i]); } catch { matched = false; break; }
        if (!ID.test(value)) { matched = false; break; }
        params[template[i].slice(1, -1)] = value;
      } else if (template[i] !== segments[i]) matched = false;
    }
    if (!matched) continue;
    if (requestBody !== undefined && (typeof requestBody !== "object" || requestBody === null || Array.isArray(requestBody))) throw new Error("Request body is invalid");
    const query = {}, multi = {};
    for (const [key, value] of url.searchParams) {
      if (Buffer.byteLength(value) > 1024 || /[\x00-\x1f]/u.test(value)) throw new Error("Query is invalid");
      if (route.multi?.[key]) {
        const values = multi[key] ??= [];
        if (!ID.test(value) || values.includes(value) || values.length >= route.multi[key]) throw new Error("Query is invalid");
        values.push(value);
        continue;
      }
      if (!(route.query || []).includes(key) || Object.hasOwn(query, key)) throw new Error("Query is invalid");
      query[key] = value;
    }
    const operation = typeof route.op === "function" ? route.op({ body: requestBody }) : route.op;
    const typed = TYPED_METHODS.get(operation);
    return { operation, params: { ...params, ...query }, ...(requestBody !== undefined ? { body: requestBody } : {}), args: route.args({ params, body: requestBody, query, multi }),
      typed: true, ...(typed?.reply ? { reply: true } : {}) };
  }
  return undefined;
}

// The domain methods reachable for the domains the backend reported in
// device.describe.
export function linkDomainOperations(domains = []) {
  const allowed = new Set(domains);
  return [...TYPED_METHODS.keys()].filter((name) => allowed.has(name.split(".")[0]));
}

// A method's recovery rule: whether it changes state (and so carries an
// idempotency key) and whether it answers with the domain result.
export function operationSpec(name) {
  const typed = TYPED_METHODS.get(name);
  if (!typed) throw new Error("Operation is unavailable");
  return { name, mutation: typed.mutation, typed: true, reply: typed.reply === true };
}

// The route of a request of the original screens, or an error naming why
// it has none.
export function mapRoute(url, method, body) {
  if (url.hash || url.username || url.password) throw new Error("Backend path is invalid");
  const mapped = mapTypedRoute(url, method, body);
  if (!mapped) throw new Error("This capability is unavailable");
  return mapped;
}

// Devices join and leave on the console: the original device screens'
// routes are answered as such.
export function consoleRoute(url) {
  return /^\/api\/clients(?:\/[^/]+(?:\/revoke)?)?$/u.test(url.pathname);
}
