import {
  boundedClientName,
  rejectRelayConnection,
  SUPPORTED_PROTOCOL_VERSION,
  parseRelayURL,
} from "./protocol.mjs";
import { getOrCreateAuthToken } from "./token.mjs";

const status = document.querySelector("#status");
const params = new URLSearchParams(location.search);
const clientName = parseClientName(params.get("client"));

void connect();
const keepalive = setInterval(() => {
  void chrome.runtime.sendMessage({ type: "keepalive" }).catch(() => {});
}, 20_000);
addEventListener("pagehide", () => clearInterval(keepalive), { once: true });

async function connect() {
  let connected = false;
  try {
    const requestedVersion = Number.parseInt(params.get("protocolVersion") ?? "", 10);
    if (requestedVersion !== SUPPORTED_PROTOCOL_VERSION) throw new Error("Unsupported client protocol");
    const relayURL = parseRelayURL(params.get("mcpRelayUrl"));
    const token = params.get("token");
    if (!token || token !== getOrCreateAuthToken()) {
      await rejectRelayConnection(relayURL);
      throw new Error("Browser control token rejected");
    }
    const requested = await chrome.runtime.sendMessage({ type: "connectionRequested", mcpRelayUrl: relayURL });
    if (!requested?.success) throw new Error(requested?.error || "Connection rejected");
    const response = await chrome.runtime.sendMessage({ type: "connectToTask", clientName });
    if (!response?.success) throw new Error(response?.error || "Connection rejected");
    connected = true;
    status.textContent = `${clientName} connected to an isolated task tab.`;
  } catch (error) {
    status.textContent = error instanceof Error ? error.message : "Connection rejected";
    status.className = "error";
  } finally {
    if (!connected) {
      await chrome.runtime.sendMessage({ type: "discardConnectionPage" }).catch(() => {});
    }
  }
}

function parseClientName(raw) {
  try {
    return boundedClientName(JSON.parse(raw || "{}").name);
  } catch {
    return "unknown";
  }
}
