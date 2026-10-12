import { isIP } from "node:net";
import { X509Certificate, createHash } from "node:crypto";
import { parseStrictJSON } from "./strict-json.mjs";

export const ISCP_CONNECTION_PREFIX = "sparkclaw-iscp-connect-v1.";
export const ISCP_CONNECTION_BYTES = 48 * 1024;
const FIELDS = ["schema_version", "type", "origin", "tls_certificate_pem", "domain_id", "relay_id", "relay_signer_identity", "issuer_identity", "invitation_signer_identity", "issuer_route", "responder_identity", "deployment_id", "owner_id", "client_id", "invitation_id", "issued_at", "expires_at", "scopes", "bootstrap_secret", "signature"];

// This preflight protects main's storage/pipe boundary. The helper independently
// verifies the signed credential, role/scope policy and the sealed enrollment.
export function parseISCPConnectionCredential(value, { now = Date.now(), allowExpired = false } = {}) {
  try {
    if (typeof value !== "string" || value !== value.trim() || !value.startsWith(ISCP_CONNECTION_PREFIX) ||
        Buffer.byteLength(value) > Math.ceil(ISCP_CONNECTION_BYTES * 4 / 3) + ISCP_CONNECTION_PREFIX.length) throw new Error();
    const raw = base64url(value.slice(ISCP_CONNECTION_PREFIX.length));
    if (!raw.length || raw.length > ISCP_CONNECTION_BYTES) throw new Error();
    const payload = parseStrictJSON(new TextDecoder("utf-8", { fatal: true }).decode(raw));
    exactKeys(payload, FIELDS);
    if (payload.schema_version !== 1 || payload.type !== "sparkclaw.iscp.connection.v1") throw new Error();
    ipHTTPSOrigin(payload.origin);
    for (const field of ["domain_id", "relay_id", "issuer_route", "deployment_id", "owner_id", "client_id", "invitation_id"]) {
      identifier(payload[field], ["deployment_id", "owner_id", "client_id"].includes(field) ? 160 : 200);
    }
    if (!/^[A-Za-z0-9][A-Za-z0-9_.:-]{0,199}$/u.test(payload.issuer_route) || payload.issuer_route.includes("://")) throw new Error();
    for (const field of ["relay_signer_identity", "issuer_identity", "invitation_signer_identity", "responder_identity"]) identity(payload[field], payload.domain_id);
    if (payload.signature?.alg !== "Ed25519" || payload.signature.kid !== payload.invitation_signer_identity.public_key.kid) throw new Error();
    exactKeys(payload.signature, ["alg", "kid", "value"]); base64url(payload.signature.value, 64);
    base64url(payload.bootstrap_secret, 32);
    const issued = timestamp(payload.issued_at), expires = timestamp(payload.expires_at);
    if (issued > now + 60000 || !allowExpired && expires <= now || expires <= issued || expires - issued > 30 * 60000) throw new Error();
    if (!Array.isArray(payload.scopes) || !payload.scopes.length || payload.scopes.length > 32 || new Set(payload.scopes).size !== payload.scopes.length) throw new Error();
    for (const scope of payload.scopes) if (typeof scope !== "string" || !/^[a-z][a-z0-9_.:-]{0,127}$/u.test(scope)) throw new Error();
    if (typeof payload.tls_certificate_pem !== "string" || Buffer.byteLength(payload.tls_certificate_pem) > 16384 ||
        !/^-----BEGIN CERTIFICATE-----\r?\n[A-Za-z0-9+/=\r\n]+\r?\n-----END CERTIFICATE-----\r?\n?$/u.test(payload.tls_certificate_pem)) throw new Error();
    const certificate = new X509Certificate(payload.tls_certificate_pem);
    const hostname = new URL(payload.origin).hostname.replace(/^\[|\]$/gu, "");
    if (!certificate.checkIP(hostname) || Date.parse(certificate.validFrom) > now || Date.parse(certificate.validTo) <= now) throw new Error();
    return Object.freeze({ kind: "iscp", payload: Object.freeze(payload) });
  } catch { throw new Error("ISCP connection credential is invalid or expired"); }
}

export function ipHTTPSOrigin(value) {
  const url = new URL(value);
  if (typeof value !== "string" || value !== url.origin || url.protocol !== "https:" || url.username || url.password ||
      !isIP(url.hostname.replace(/^\[|\]$/gu, ""))) throw new Error("ISCP requires a canonical HTTPS IP origin");
  return value;
}

function identity(value, domain) {
  exactKeys(value, ["type", "domain_id", "device_id", "public_key", "created_at"], ["metadata"]);
  if (value.type !== "iscp.device.identity.v2" || value.domain_id !== domain) throw new Error();
  identifier(value.device_id, 200); timestamp(value.created_at);
  exactKeys(value.public_key, ["kid", "kty", "public", "use"]);
  if (value.public_key.kty !== "Ed25519" || value.public_key.use !== "identity-signature") throw new Error();
  base64url(value.public_key.kid, 32);
  const publicBytes = base64url(value.public_key.public, 32);
  if (value.public_key.kid !== createHash("sha256").update("iscp/v2/thumbprint/Ed25519\0").update(publicBytes).digest("base64url")) throw new Error();
  if (value.metadata !== undefined && (!value.metadata || typeof value.metadata !== "object" || Array.isArray(value.metadata) ||
      Object.entries(value.metadata).some(([key, item]) => typeof item !== "string" || key.length > 200 || item.length > 2000))) throw new Error();
}
function identifier(value, limit) {
  if (typeof value !== "string" || !value.length || value.length > limit || value !== value.trim() || /[\s\x00-\x1f\x7f]/u.test(value)) throw new Error();
}
function timestamp(value) {
  if (typeof value !== "string" || !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?(?:Z|[+-]\d{2}:\d{2})$/u.test(value) || !Number.isFinite(Date.parse(value))) throw new Error();
  return Date.parse(value);
}
function base64url(value, length) {
  if (typeof value !== "string" || !/^[A-Za-z0-9_-]+$/u.test(value)) throw new Error();
  const bytes = Buffer.from(value, "base64url");
  if (bytes.toString("base64url") !== value || length !== undefined && bytes.length !== length) throw new Error();
  return bytes;
}
function exactKeys(value, keys, optional = []) {
  if (!value || typeof value !== "object" || Array.isArray(value) || keys.some(key => !Object.hasOwn(value, key)) ||
      Object.keys(value).some(key => !keys.includes(key) && !optional.includes(key))) throw new Error();
}
