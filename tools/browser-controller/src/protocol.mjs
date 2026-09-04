import { invalidRequest } from "./errors.mjs";

export const MAX_REQUEST_BYTES = 16 << 10;
export const MAX_TOKEN_BYTES = 4096;
export const MIN_TOKEN_BYTES = 16;
export const MAX_WAIT_MS = 30_000;
export const MAX_SESSION_TTL_MS = 10 * 60_000;

const TOKEN_CONTROL_PATTERN = /[\u0000-\u001f\u007f]/u;
const ID_PATTERN = /^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/u;

export function requireExactObject(value, required, optional = []) {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    throw invalidRequest();
  }
  const allowed = new Set([...required, ...optional]);
  for (const field of required) {
    if (!Object.hasOwn(value, field)) throw invalidRequest();
  }
  for (const field of Object.keys(value)) {
    if (!allowed.has(field)) throw invalidRequest();
  }
}

export function parseToken(value) {
  if (typeof value !== "string") throw invalidRequest();
  const bytes = Buffer.byteLength(value, "utf8");
  if (
    bytes < MIN_TOKEN_BYTES ||
    bytes > MAX_TOKEN_BYTES ||
    value.trim() !== value ||
    TOKEN_CONTROL_PATTERN.test(value)
  ) {
    throw invalidRequest();
  }
  return value;
}

export function parseID(value, field) {
  if (typeof value !== "string" || !ID_PATTERN.test(value)) {
    throw invalidRequest(`${field} is invalid`);
  }
  return value;
}

export function parseProfileID(value, expected) {
  const profileID = parseID(value, "profile_id");
  if (profileID !== expected) {
    throw invalidRequest("profile_id does not match this controller");
  }
  return profileID;
}

export function parseGeneration(value, field, { allowZero = false } = {}) {
  const minimum = allowZero ? 0 : 1;
  if (!Number.isSafeInteger(value) || value < minimum) {
    throw invalidRequest(`${field} is invalid`);
  }
  return value;
}

export function parseBoundedMilliseconds(value, field, maximum, fallback) {
  if (value === undefined) return fallback;
  if (!Number.isSafeInteger(value) || value < 0 || value > maximum) {
    throw invalidRequest(`${field} is invalid`);
  }
  return value;
}
