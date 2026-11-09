import crypto from "node:crypto";
import operations from "./controller-operations.json" with { type: "json" };

export const CONTROLLER_OPERATIONS = Object.freeze(["status", "acquire", "execute", "release", "chunk"].map(name => `browser.controller.${name}`));
export const BINDING_KEYS = Object.freeze(["owner_id", "client_id", "installation_id", "local_conversation_id", "local_task_id", "host_id", "runtime_generation", "connection_epoch", "lease_id", "page_id", "page_generation", "authorization_digest", "lease_expires_at"]);
export const ID = /^[A-Za-z0-9][A-Za-z0-9_.:-]{0,159}$/u;
export const HASH = /^[a-f0-9]{64}$/u;
const LEASE_KEYS = ["schema_version", "state", "profile_id", "lane", "session_id", "credential_generation", "controller_generation", "session_generation", "page_generation", "expires_at"];
const sha256 = value => crypto.createHash("sha256").update(value).digest("hex");

export function iscpHostID(scope, sessionID) { return `iscp_${sha256(`sparkclaw.browser.iscp.host.v1\0${scope.owner_id}\0${scope.client_id}\0${scope.installation_id}\0${sessionID}`).slice(0, 32)}`; }
export function iscpAuthorizationDigest(sessionID, revision) { return sha256(`sparkclaw.browser.iscp.authorization.v1\0${sessionID}\0${revision}`); }

export function canonicalControllerJSON(value) {
  if (Array.isArray(value)) return `[${value.map(canonicalControllerJSON).join(",")}]`;
  if (value && typeof value === "object") return `{${Object.keys(value).sort((a, b) => Buffer.compare(Buffer.from(a), Buffer.from(b))).map(key => `${canonicalControllerJSON(key)}:${canonicalControllerJSON(value[key])}`).join(",")}}`;
  if (typeof value === "number" && !Number.isSafeInteger(value) || typeof value === "string" && !value.isWellFormed() || value === undefined) throw new Error("Controller digest input is invalid");
  return JSON.stringify(value).replaceAll("<", "\\u003c").replaceAll(">", "\\u003e").replaceAll("&", "\\u0026").replaceAll("\u2028", "\\u2028").replaceAll("\u2029", "\\u2029");
}
export function controllerDigest(operation, body) { return sha256(canonicalControllerJSON({ operation, body: { ...body, digest: "" } })); }
export function wireBytes(value) { return Buffer.byteLength(JSON.stringify(value).replace(/[<>&\u2028\u2029]/gu, "\\u0000")); }
export function exactObject(value, required, optional = []) {
  if (!value || typeof value !== "object" || Array.isArray(value) || required.some(key => !Object.hasOwn(value, key)) ||
      Object.keys(value).some(key => !required.includes(key) && !optional.includes(key))) throw new Error("Controller request is invalid");
}
export function positive(value, maximum = Number.MAX_SAFE_INTEGER, minimum = 1) {
  if (!Number.isSafeInteger(value) || value < minimum || value > maximum) throw new Error("Controller integer is invalid");
}
export function validateControllerBinding(binding, scope, session) {
  exactObject(binding, BINDING_KEYS);
  positive(binding.page_generation);
  if (BINDING_KEYS.filter(key => key.endsWith("_id") || ["runtime_generation", "connection_epoch"].includes(key)).some(key => !ID.test(binding[key])) ||
      ["owner_id", "client_id", "installation_id"].some(key => binding[key] !== scope?.[key]) ||
      binding.connection_epoch !== session?.session_id || binding.runtime_generation !== `iscp_${session?.session_id}` ||
      binding.host_id !== iscpHostID(scope, session.session_id) || binding.authorization_digest !== iscpAuthorizationDigest(session.session_id, session.authorization_revision) ||
      binding.lease_expires_at !== "0001-01-01T00:00:00Z") throw new Error("Controller binding is stale");
}
export function sameControllerBinding(left, right) { return BINDING_KEYS.every(key => left?.[key] === right?.[key]); }
export function validateControllerLease(lease) {
  exactObject(lease, LEASE_KEYS);
  if (lease.schema_version !== 1 || lease.state !== "acquired" || lease.profile_id !== "client_embedded" || lease.lane !== "mcp" ||
      !/^session_[a-f0-9]{32}$/u.test(lease.session_id) || !Number.isFinite(Date.parse(lease.expires_at))) throw new Error("Controller lease is invalid");
  for (const key of ["credential_generation", "controller_generation", "session_generation", "page_generation"]) positive(lease[key]);
}
export function sameControllerLease(left, right) {
  return LEASE_KEYS.every(key => key === "expires_at" ? Date.parse(left?.[key]) === Date.parse(right?.[key]) : left?.[key] === right?.[key]);
}
export function validateControllerOperation(operation, args) {
  const spec = Object.hasOwn(operations, operation) && operations[operation];
  if (!spec?.client) throw new Error("Controller operation is unavailable");
  exactObject(args, spec.required, spec.optional);
  if (wireBytes(args) > 32 << 10) throw new Error("Controller arguments are too large");
  if (operation === "page.wait" && args.duration_ms !== undefined) positive(args.duration_ms, 15000, 0);
  return spec.mutation;
}
export function validateControllerRequest(operation, body) {
  if (!CONTROLLER_OPERATIONS.includes(operation)) throw new Error("Controller operation is unavailable");
  if (operation.endsWith(".status")) { exactObject(body, ["schema_version"]); if (body.schema_version !== 1) throw new Error("Controller schema is invalid"); return false; }
  if (operation.endsWith(".chunk")) {
    exactObject(body, ["schema_version", "binding", "command_id", "result_id", "offset", "length"]);
    if (body.schema_version !== 1 || !ID.test(body.command_id) || !ID.test(body.result_id)) throw new Error("Controller chunk is invalid");
    positive(body.offset, 10 << 20, 0); positive(body.length, 32 << 10);
    return false;
  }
  const additional = operation.endsWith(".acquire") ? ["wait_timeout_ms", "session_ttl_ms"] : operation.endsWith(".execute") ? ["lease", "operation", "arguments"] : ["lease"];
  exactObject(body, ["schema_version", "command_id", "digest", "binding", "timeout_ms", ...additional]);
  if (body.schema_version !== 1 || !ID.test(body.command_id) || !HASH.test(body.digest)) throw new Error("Controller identity is invalid");
  positive(body.timeout_ms, 25000);
  if (operation.endsWith(".acquire")) { positive(body.wait_timeout_ms, 30000, 0); positive(body.session_ttl_ms, 600000); }
  else validateControllerLease(body.lease);
  const mutation = operation.endsWith(".execute") ? validateControllerOperation(body.operation, body.arguments) : true;
  if (body.digest !== controllerDigest(operation, body)) throw new Error("Controller digest differs");
  return mutation;
}
