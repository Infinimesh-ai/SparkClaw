// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License, Version 2.0.

export const BRIDGE_EXTENSION_ID = "mmlmfjhmonkocbjadbfplnigmagldckm";
export const BRIDGE_VERSION = "1.0.18";
export const SUPPORTED_PROTOCOL_VERSION = 2;
export const HANDOFF_MARKER = "sparkclaw-browser-bridge-handoff-v1";
export const HANDOFF_EVALUATE_FUNCTION = `() => "${HANDOFF_MARKER}"`;
export const NATIVE_HOST_NAME = "com.sparkclaw.browser_bridge";
export const REJECTION_MARKER = "browser_extension_rejected";
export const TOKEN_STORAGE_KEY = "sparkclaw-browser-bridge-token-v1";

const RELAY_PATH = /^\/extension\/[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/u;

export function parseRelayURL(raw) {
  if (typeof raw !== "string" || raw.length > 2048) throw new Error("Invalid relay URL");
  const url = new URL(raw);
  if (url.protocol !== "ws:" || !["127.0.0.1", "[::1]"].includes(url.hostname)) {
    throw new Error("Relay URL must use loopback WebSocket transport");
  }
  if (!RELAY_PATH.test(url.pathname) || url.username || url.password || url.search || url.hash) {
    throw new Error("Invalid relay URL");
  }
  return url.toString();
}

export function parseConnectionPageURL(raw) {
  if (typeof raw !== "string" || raw.length > 16384) throw new Error("Invalid connection URL");
  const url = new URL(raw);
  if (url.protocol !== "chrome-extension:" || url.hostname !== BRIDGE_EXTENSION_ID ||
      url.pathname !== "/connect.html" || url.username || url.password || url.hash) {
    throw new Error("Invalid connection URL");
  }
  const keys = [...url.searchParams.keys()].sort();
  if (keys.join("\n") !== ["client", "mcpRelayUrl", "protocolVersion", "token"].sort().join("\n") ||
      keys.some((key, index) => index > 0 && key === keys[index - 1])) {
    throw new Error("Invalid connection URL");
  }
  if (url.searchParams.get("protocolVersion") !== String(SUPPORTED_PROTOCOL_VERSION)) {
    throw new Error("Invalid connection URL");
  }
  parseRelayURL(url.searchParams.get("mcpRelayUrl"));
  const token = url.searchParams.get("token");
  if (!token || token.length > 4096 || /[\u0000-\u001f\u007f]/u.test(token)) {
    throw new Error("Invalid connection URL");
  }
  return url.toString();
}

export function exactKeys(value, required, optional = []) {
  if (!value || typeof value !== "object" || Array.isArray(value)) return false;
  const keys = Object.keys(value).sort();
  const allowed = new Set([...required, ...optional]);
  return required.every((key) => Object.hasOwn(value, key)) && keys.every((key) => allowed.has(key));
}

export function boundedClientName(value) {
  if (typeof value !== "string") return "unknown";
  const normalized = value.replace(/[\u0000-\u001f\u007f]/gu, "").trim();
  return normalized.slice(0, 64) || "unknown";
}

export async function rejectRelayConnection(rawRelayURL, WebSocketClass = WebSocket) {
  const relayURL = parseRelayURL(rawRelayURL);
  await new Promise((resolve) => {
    const socket = new WebSocketClass(relayURL);
    let timer = setTimeout(() => finish(), 5000);
    const finish = () => {
      if (timer === null) return;
      clearTimeout(timer);
      timer = null;
      resolve();
    };
    socket.onopen = () => {
      try {
        socket.send(JSON.stringify({ id: -1, error: REJECTION_MARKER }));
        socket.close(4001, REJECTION_MARKER);
      } catch {
        finish();
      }
    };
    socket.onclose = finish;
    socket.onerror = finish;
  });
}
