import path from "node:path";

export const BRIDGE_EXTENSION_ID = "mmlmfjhmonkocbjadbfplnigmagldckm";
export const BRIDGE_VERSION = "1.0.18";
export const BRIDGE_NATIVE_HOST = "com.sparkclaw.browser_bridge";
export const MAX_NATIVE_MESSAGE_BYTES = 20 << 10;
export const BRIDGE_PROTOCOL_VERSION = 2;

const RELAY_PATH = /^\/extension\/[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/u;

export function nativeSocketPath(env = process.env) {
  const configured = env.SPARKCLAW_BROWSER_BRIDGE_NATIVE_SOCKET?.trim();
  const runtimeRoot = env.XDG_RUNTIME_DIR?.trim() || `/run/user/${process.getuid()}`;
  const value = configured || path.join(runtimeRoot, "sparkclaw", "browser-controller", "bridge-native.sock");
  if (!path.isAbsolute(value) || path.basename(value) !== "bridge-native.sock") {
    throw new Error("Browser Bridge native socket path is invalid");
  }
  return value;
}

export function parseLauncherArguments(args) {
  if (args.length === 1 && args[0] === "--check") return { operation: "status" };
  let connectionURL = "";
  for (const argument of args) {
    if (argument === "--no-sandbox" || argument.startsWith("--user-data-dir=") ||
        argument.startsWith("--profile-directory=")) {
      continue;
    }
    if (connectionURL) throw new Error("Browser Bridge launcher arguments are invalid");
    connectionURL = parseConnectionURL(argument);
  }
  if (!connectionURL) throw new Error("Browser Bridge connection URL is missing");
  return { operation: "openConnection", url: connectionURL };
}

export function parseNativeClientRequest(value) {
  if (!value || typeof value !== "object" || Array.isArray(value) || value.schema_version !== 1) {
    throw new Error("Browser Bridge native request is invalid");
  }
  if (value.operation === "status" && Object.keys(value).sort().join("\n") === "operation\nschema_version") {
    return { operation: "status" };
  }
  if (value.operation === "openConnection" &&
      Object.keys(value).sort().join("\n") === "operation\nschema_version\nurl") {
    return { operation: "openConnection", url: parseConnectionURL(value.url) };
  }
  throw new Error("Browser Bridge native request is invalid");
}

export function readyStatus(ready) {
  if (!ready) return { schema_version: 1, state: "not_ready" };
  return {
    schema_version: 1,
    state: "ready",
    extension_id: ready.extension_id,
    bridge_version: ready.version,
    protocol_version: ready.protocol_version,
  };
}

export function assertExpectedReadyStatus(value) {
  const expectedKeys = ["bridge_version", "extension_id", "protocol_version", "schema_version", "state"];
  if (!value || typeof value !== "object" || Array.isArray(value) ||
      Object.keys(value).sort().join("\n") !== expectedKeys.join("\n") ||
      value.schema_version !== 1 || value.state !== "ready" ||
      value.extension_id !== BRIDGE_EXTENSION_ID || value.bridge_version !== BRIDGE_VERSION ||
      value.protocol_version !== BRIDGE_PROTOCOL_VERSION) {
    throw new Error("loaded Browser Bridge version is unavailable or stale");
  }
}

export function parseConnectionURL(raw) {
  if (typeof raw !== "string" || raw.length > 16384) throw new Error("Browser Bridge connection URL is invalid");
  const url = new URL(raw);
  if (url.protocol !== "chrome-extension:" || url.hostname !== BRIDGE_EXTENSION_ID ||
      url.pathname !== "/connect.html" || url.username || url.password || url.hash) {
    throw new Error("Browser Bridge connection URL is invalid");
  }
  const keys = [...url.searchParams.keys()].sort();
  const expected = ["client", "mcpRelayUrl", "protocolVersion", "token"].sort();
  if (keys.length !== expected.length || keys.some((key, index) => key !== expected[index])) {
    throw new Error("Browser Bridge connection URL is invalid");
  }
  const relay = new URL(url.searchParams.get("mcpRelayUrl") || "invalid:");
  if (relay.protocol !== "ws:" || !["127.0.0.1", "[::1]"].includes(relay.hostname) ||
      !RELAY_PATH.test(relay.pathname) || relay.username || relay.password || relay.search || relay.hash ||
      url.searchParams.get("protocolVersion") !== String(BRIDGE_PROTOCOL_VERSION)) {
    throw new Error("Browser Bridge connection URL is invalid");
  }
  const token = url.searchParams.get("token");
  if (!token || token.length > 4096 || /[\u0000-\u001f\u007f]/u.test(token)) {
    throw new Error("Browser Bridge connection URL is invalid");
  }
  return url.toString();
}

export function encodeNativeMessage(value) {
  const payload = Buffer.from(JSON.stringify(value), "utf8");
  if (payload.length === 0 || payload.length > MAX_NATIVE_MESSAGE_BYTES) {
    throw new Error("Native message is invalid");
  }
  const header = Buffer.alloc(4);
  header.writeUInt32LE(payload.length);
  return Buffer.concat([header, payload]);
}

export function decodeNativeMessages(buffer) {
  const messages = [];
  let offset = 0;
  while (buffer.length - offset >= 4) {
    const length = buffer.readUInt32LE(offset);
    if (length === 0 || length > MAX_NATIVE_MESSAGE_BYTES) throw new Error("Native message is invalid");
    if (buffer.length - offset - 4 < length) break;
    const payload = buffer.subarray(offset + 4, offset + 4 + length);
    const value = JSON.parse(payload.toString("utf8"));
    if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error("Native message is invalid");
    messages.push(value);
    offset += 4 + length;
  }
  return { messages, remaining: buffer.subarray(offset) };
}
