import { parseBackendDescriptor } from "./local-backend.mjs";

const PREFIX = "sparkclaw-connect-v1.";
const TOKEN = /^[A-Za-z0-9_-]{32,512}$/u;
const MAX_CREDENTIAL_BYTES = 48 * 1024;

// A connection credential is one secret, copyable value. It carries the
// public pinned backend identity together with one issued Client bearer so the
// target desktop never asks its user to assemble connection metadata.
export function createConnectionCredential(descriptor, token) {
  const backend = publicDescriptor(parseBackendDescriptor(descriptor));
  if (backend.schema_version !== 2) throw new Error("Connection credentials require an HTTPS LAN backend");
  if (typeof token !== "string" || !TOKEN.test(token)) throw new Error("Device credential is invalid");
  const payload = JSON.stringify({ schema_version: 1, backend, token });
  if (Buffer.byteLength(payload) > MAX_CREDENTIAL_BYTES) throw new Error("Connection credential is too large");
  return `${PREFIX}${Buffer.from(payload, "utf8").toString("base64url")}`;
}

export function parseConnectionCredential(value) {
  if (typeof value !== "string" || value !== value.trim() || Buffer.byteLength(value) > MAX_CREDENTIAL_BYTES * 2 ||
      !value.startsWith(PREFIX)) throw new Error("Connection credential is invalid");
  const encoded = value.slice(PREFIX.length);
  if (!encoded || !/^[A-Za-z0-9_-]+$/u.test(encoded)) throw new Error("Connection credential is invalid");
  let raw;
  try {
    raw = Buffer.from(encoded, "base64url");
  } catch {
    throw new Error("Connection credential is invalid");
  }
  if (raw.toString("base64url") !== encoded || raw.byteLength > MAX_CREDENTIAL_BYTES) {
    throw new Error("Connection credential is invalid");
  }
  let payload;
  try { payload = JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(raw)); }
  catch { throw new Error("Connection credential is invalid"); }
  exactKeys(payload, ["schema_version", "backend", "token"]);
  if (payload.schema_version !== 1 || typeof payload.token !== "string" || !TOKEN.test(payload.token)) {
    throw new Error("Connection credential is invalid");
  }
  const backend = publicDescriptor(parseBackendDescriptor(payload.backend));
  if (backend.schema_version !== 2) throw new Error("Connection credential requires an HTTPS LAN backend");
  return Object.freeze({ descriptor: Object.freeze(backend), token: payload.token });
}

function publicDescriptor(descriptor) {
  if (descriptor.schemaVersion !== 2) return Object.freeze({
    schema_version: 1, origin: descriptor.origin, deployment_id: descriptor.deploymentID,
  });
  return Object.freeze({
    schema_version: 2,
    origin: descriptor.origin,
    deployment_id: descriptor.deploymentID,
    owner_id: descriptor.ownerID,
    tls_certificate_sha256: descriptor.certificateSHA256,
    ...(descriptor.ca ? { tls_ca_pem: descriptor.ca } : {}),
  });
}

function exactKeys(value, keys) {
  if (!value || typeof value !== "object" || Array.isArray(value) ||
      Object.keys(value).sort().join(",") !== [...keys].sort().join(",")) {
    throw new Error("Connection credential is invalid");
  }
}
