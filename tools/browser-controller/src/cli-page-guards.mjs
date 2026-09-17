// Pure page identity guards shared by the task runtime and short-lived batch transport.
import { BRIDGE_EXTENSION_ID } from "./bridge-native-protocol.mjs";
import { parseTabsMarkdown, renderTabLine } from "./playwright-output.mjs";
import { clientContractError, pageStale } from "./cli-runtime.mjs";

export const EXTENSION_CONNECT_URL = "sparkclaw-internal://extension-connect";
const RELAY_PATH_PATTERN = /^\/extension\/[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/u;

export function parseTabs(raw) {
  const tabs = parseTabsMarkdown(raw);
  if (tabs === undefined) throw clientContractError();
  return tabs;
}

export function sanitizeTabListOutput(raw, token) {
  const tabs = parseTabsMarkdown(raw);
  if (tabs === undefined) throw clientContractError();
  if (tabs.length === 0) return raw.trim();
  let connectPages = 0;
  const sanitized = [];
  for (const tab of tabs) {
    if (isExtensionConnectURL(tab.url, token)) {
      tab.url = EXTENSION_CONNECT_URL;
      connectPages += 1;
    }
    sanitized.push(renderTabLine(tab));
  }
  if (connectPages > 1) throw clientContractError();
  return sanitized.join("\n");
}

function isExtensionConnectURL(rawURL, token) {
  let parsed;
  try {
    parsed = new URL(rawURL);
  } catch {
    return false;
  }
  if (
    parsed.protocol !== "chrome-extension:" ||
    parsed.hostname !== BRIDGE_EXTENSION_ID ||
    parsed.pathname !== "/connect.html" ||
    parsed.username ||
    parsed.password ||
    parsed.hash ||
    parsed.searchParams.size !== 4 ||
    parsed.searchParams.getAll("mcpRelayUrl").length !== 1 ||
    parsed.searchParams.getAll("client").length !== 1 ||
    parsed.searchParams.getAll("protocolVersion").length !== 1 ||
    parsed.searchParams.getAll("token").length !== 1 ||
    parsed.searchParams.get("protocolVersion") !== "2" ||
    parsed.searchParams.get("token") !== token
  ) {
    return false;
  }
  let client;
  let relay;
  try {
    client = JSON.parse(parsed.searchParams.get("client"));
    relay = new URL(parsed.searchParams.get("mcpRelayUrl"));
  } catch {
    return false;
  }
  return Boolean(
    client &&
    typeof client === "object" &&
    !Array.isArray(client) &&
    Object.keys(client).length === 1 &&
    client.name === "playwright-cli" &&
    relay.protocol === "ws:" &&
    ["127.0.0.1", "[::1]"].includes(relay.hostname) &&
    /^[1-9][0-9]{0,4}$/u.test(relay.port) &&
    Number(relay.port) <= 65535 &&
    !relay.username &&
    !relay.password &&
    !relay.search &&
    !relay.hash &&
    RELAY_PATH_PATTERN.test(relay.pathname)
  );
}

export function assertExpectedOrigin(rawURL, expectedOrigin, allowedOrigins) {
  let parsed;
  try {
    parsed = new URL(rawURL);
  } catch {
    throw pageStale("page_invalid_url");
  }
  if (
    parsed.username ||
    parsed.password
  ) {
    throw pageStale("page_url_credentials");
  }
  if (parsed.protocol !== "https:") {
    throw pageStale(
      parsed.protocol === "chrome-extension:"
        ? "page_extension_origin"
        : "page_non_https_origin",
    );
  }
  if (!allowedOrigins.includes(parsed.origin)) {
    throw pageStale(unregisteredOriginReason(parsed.hostname));
  }
  if (expectedOrigin && parsed.origin !== expectedOrigin) {
    throw pageStale("page_origin_mismatch");
  }
  return parsed;
}

function unregisteredOriginReason(hostname) {
  const normalized = hostname.toLowerCase();
  if (normalized === "workspace.google.com") return "page_google_workspace_origin";
  if (normalized === "www.google.com") return "page_google_www_origin";
  if (normalized === "myaccount.google.com") return "page_google_myaccount_origin";
  if (normalized === "google.com" || normalized.endsWith(".google.com")) {
    return "page_google_other_origin";
  }
  if (
    normalized === "microsoft.com" ||
    normalized.endsWith(".microsoft.com") ||
    normalized === "microsoftonline.com" ||
    normalized.endsWith(".microsoftonline.com") ||
    normalized === "live.com" ||
    normalized.endsWith(".live.com") ||
    normalized === "office.com" ||
    normalized.endsWith(".office.com") ||
    normalized === "office365.com" ||
    normalized.endsWith(".office365.com")
  ) {
    return "page_unregistered_microsoft_origin";
  }
  if (normalized === "qq.com" || normalized.endsWith(".qq.com")) {
    return "page_unregistered_qq_origin";
  }
  return "page_unregistered_other_origin";
}

function tabFingerprint(tab) {
  return `${tab.title}\0${tab.url}\0${tab.crashed ? "1" : "0"}`;
}

export function assertTaskTopology(tabs,taskIndex,ownerTabs) {
  if(taskIndex<0||tabs.length!==ownerTabs.length+1||taskIndex>=tabs.length)throw pageStale('page_topology_changed');
  const owners=tabs.filter((_,index)=>index!==taskIndex).map(tabFingerprint);
  if(!sameFingerprintList(owners,ownerTabs.map(tabFingerprint)))throw pageStale('page_topology_changed');
}

function sameFingerprintList(left, right) {
  return left.length === right.length && left.every((value, index) => value === right[index]);
}
