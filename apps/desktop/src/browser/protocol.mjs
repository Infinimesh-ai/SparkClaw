import crypto from "node:crypto";
import path from "node:path";

export const ELECTRON_ADAPTER_VERSION = "1.0.0";
export const ELECTRON_ADAPTER_PROTOCOL_VERSION = 1;
export const PLAYWRIGHT_RELAY_PROTOCOL_VERSION = 2;
export const PLAYWRIGHT_EXTENSION_ID = "mmlmfjhmonkocbjadbfplnigmagldckm";
export const ELECTRON_CONNECT_URL = "sparkclaw-internal://extension-connect";
export const MAX_ADAPTER_MESSAGE_BYTES = 24 << 10;

const RELAY_PATH = /^\/extension\/[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/u;
const ID = /^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$/u;
const SESSION_ID = /^session_[0-9a-f]{32}$/u;
const GENERATION = /^[0-9a-f]{32}$/u;
const CREDENTIAL = /^[A-Za-z0-9_-]{32,128}$/u;
const SHA256 = /^[0-9a-f]{64}$/u;

export function adapterSocketPath(env = process.env) {
  const configured = env.SPARKCLAW_ELECTRON_ADAPTER_SOCKET?.trim();
  const runtimeRoot = env.XDG_RUNTIME_DIR?.trim() || `/run/user/${process.getuid()}`;
  const value = configured || path.join(runtimeRoot, "sparkclaw", "desktop", "electron-adapter.sock");
  if (!path.isAbsolute(value) || path.basename(value) !== "electron-adapter.sock") {
    throw new Error("Electron adapter socket path is invalid");
  }
  return value;
}

export function adapterSecretPath(env = process.env) {
  const configured = env.SPARKCLAW_ELECTRON_ADAPTER_SECRET_FILE?.trim();
  const value = configured || path.join(path.dirname(adapterSocketPath(env)), "adapter-secret");
  if (!path.isAbsolute(value) || path.basename(value) !== "adapter-secret") {
    throw new Error("Electron adapter secret path is invalid");
  }
  return value;
}

export function parsePlaywrightConnectionURL(raw) {
  if (typeof raw !== "string" || raw.length > 16_384) {
    throw new Error("Playwright connection URL is invalid");
  }
  const url = new URL(raw);
  if (url.protocol !== "chrome-extension:" || url.hostname !== PLAYWRIGHT_EXTENSION_ID ||
      url.pathname !== "/connect.html" || url.username || url.password || url.hash) {
    throw new Error("Playwright connection URL is invalid");
  }
  const keys = [...url.searchParams.keys()].sort();
  const expected = ["client", "mcpRelayUrl", "protocolVersion", "token"].sort();
  if (keys.length !== expected.length || keys.some((key, index) => key !== expected[index]) ||
      url.searchParams.get("protocolVersion") !== String(PLAYWRIGHT_RELAY_PROTOCOL_VERSION)) {
    throw new Error("Playwright connection URL is invalid");
  }
  const relayURL = new URL(url.searchParams.get("mcpRelayUrl") || "invalid:");
  if (relayURL.protocol !== "ws:" || !["127.0.0.1", "[::1]"].includes(relayURL.hostname) ||
      !RELAY_PATH.test(relayURL.pathname) || relayURL.username || relayURL.password ||
      relayURL.search || relayURL.hash) {
    throw new Error("Playwright connection URL is invalid");
  }
  const token = url.searchParams.get("token");
  if (!token || token.length > 4096 || /[\u0000-\u001f\u007f]/u.test(token)) {
    throw new Error("Playwright connection URL is invalid");
  }
  let client;
  try {
    client = JSON.parse(url.searchParams.get("client") || "null");
  } catch {
    throw new Error("Playwright connection URL is invalid");
  }
  if (!plainObject(client) || typeof client.name !== "string" || client.name.length > 64) {
    throw new Error("Playwright connection URL is invalid");
  }
  return {
    relayURL: relayURL.toString(),
    tokenHash: sha256Hex(token),
    clientName: client.name,
  };
}

export function parseConnectionBinding(value) {
  const keys = [
    "controller_generation",
    "page_generation",
    "session_generation",
    "session_id",
    "task_id",
  ];
  if (!plainObject(value) || Object.keys(value).sort().join("\n") !== keys.sort().join("\n") ||
      !ID.test(value.task_id) || !SESSION_ID.test(value.session_id) ||
      !Number.isSafeInteger(value.controller_generation) || value.controller_generation < 1 ||
      !Number.isSafeInteger(value.session_generation) || value.session_generation < 1 ||
      !Number.isSafeInteger(value.page_generation) || value.page_generation < 1) {
    throw new Error("Electron adapter binding is invalid");
  }
  return Object.freeze({
    task_id: value.task_id,
    session_id: value.session_id,
    controller_generation: value.controller_generation,
    session_generation: value.session_generation,
    page_generation: value.page_generation,
  });
}

export function parseRegisterRequest(value) {
  const expected = [
    "binding",
    "operation",
    "protocol_version",
    "runtime_kind",
    "runtime_secret",
    "schema_version",
    "token_sha256",
  ];
  if (!plainObject(value) || Object.keys(value).sort().join("\n") !== expected.sort().join("\n") ||
      value.schema_version !== 1 || value.operation !== "registerConnection" ||
      value.runtime_kind !== "electron" || value.protocol_version !== ELECTRON_ADAPTER_PROTOCOL_VERSION ||
      typeof value.runtime_secret !== "string" || value.runtime_secret.length > 256 ||
      !SHA256.test(value.token_sha256)) {
    throw new Error("Electron adapter registration is invalid");
  }
  return {
    runtimeSecret: value.runtime_secret,
    tokenHash: value.token_sha256,
    binding: parseConnectionBinding(value.binding),
  };
}

export function parseOpenRequest(value) {
  const expected = [
    "binding",
    "connection_credential",
    "connection_url",
    "operation",
    "protocol_version",
    "runtime_kind",
    "schema_version",
  ];
  if (!plainObject(value) || Object.keys(value).sort().join("\n") !== expected.sort().join("\n") ||
      value.schema_version !== 1 || value.operation !== "openConnection" ||
      value.runtime_kind !== "electron" || value.protocol_version !== ELECTRON_ADAPTER_PROTOCOL_VERSION ||
      !CREDENTIAL.test(value.connection_credential)) {
    throw new Error("Electron adapter open request is invalid");
  }
  return {
    credential: value.connection_credential,
    connectionURL: value.connection_url,
    binding: parseConnectionBinding(value.binding),
  };
}

export function parsePersonalRequest(value, { allowLoopbackHTTP = false } = {}) {
  const expected = ["operation", "runtime_kind", "runtime_secret", "schema_version", "url"];
  if (!plainObject(value) || Object.keys(value).sort().join("\n") !== expected.sort().join("\n") ||
      value.schema_version !== 1 || value.operation !== "openPersonalPage" ||
      value.runtime_kind !== "electron" || typeof value.runtime_secret !== "string" ||
      value.runtime_secret.length > 256) {
    throw new Error("Electron personal-page request is invalid");
  }
  const url = new URL(value.url);
  const loopbackHTTP = allowLoopbackHTTP && url.protocol === "http:" &&
    ["127.0.0.1", "[::1]", "localhost"].includes(url.hostname);
  if (url.protocol !== "https:" && !loopbackHTTP || url.username || url.password || url.hash || value.url.length > 16_384) {
    throw new Error("Electron personal-page request is invalid");
  }
  return { runtimeSecret: value.runtime_secret, url: url.toString() };
}

export function secureEqual(left, right) {
  if (typeof left !== "string" || typeof right !== "string") return false;
  const leftBuffer = Buffer.from(left);
  const rightBuffer = Buffer.from(right);
  return leftBuffer.length === rightBuffer.length && crypto.timingSafeEqual(leftBuffer, rightBuffer);
}

export function sha256Hex(value) {
  return crypto.createHash("sha256").update(value).digest("hex");
}

export function bindingEquals(left, right) {
  return left.task_id === right.task_id && left.session_id === right.session_id &&
    left.controller_generation === right.controller_generation &&
    left.session_generation === right.session_generation &&
    left.page_generation === right.page_generation;
}

export function exactStatusResponse(value) {
  const keys = ["adapter_version", "protocol_version", "runtime_generation", "runtime_kind", "schema_version", "state"];
  return plainObject(value) && Object.keys(value).sort().join("\n") === keys.sort().join("\n") &&
    value.schema_version === 1 && value.state === "ready" && value.runtime_kind === "electron" &&
    value.adapter_version === ELECTRON_ADAPTER_VERSION &&
    value.protocol_version === ELECTRON_ADAPTER_PROTOCOL_VERSION && GENERATION.test(value.runtime_generation);
}

function plainObject(value) {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}
