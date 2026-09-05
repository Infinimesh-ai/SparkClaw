import { getOrCreateAuthToken, rotateAuthToken } from "./token.mjs";

const token = document.querySelector("#token");
const tokenStatus = document.querySelector("#token-status");
const connections = document.querySelector("#connections");

renderToken(getOrCreateAuthToken());
void renderConnections();

document.querySelector("#copy").addEventListener("click", async () => {
  await navigator.clipboard.writeText(token.textContent);
  tokenStatus.textContent = "Token copied.";
});
document.querySelector("#rotate").addEventListener("click", () => {
  renderToken(rotateAuthToken());
  tokenStatus.textContent = "Token rotated. Replace the Browser control credential before the next task.";
});

function renderToken(value) {
  token.textContent = value;
}

async function renderConnections() {
  const response = await chrome.runtime.sendMessage({ type: "getConnectionStatus" });
  if (!response?.success || response.connections.length === 0) {
    connections.textContent = "None";
    return;
  }
  connections.replaceChildren(...response.connections.map((connection) => {
    const row = document.createElement("div");
    row.className = "connection";
    const label = document.createElement("span");
    label.textContent = `${connection.clientName} · ${connection.connectedTabIds.length} task tab(s)`;
    const button = document.createElement("button");
    button.type = "button";
    button.textContent = "Disconnect";
    button.addEventListener("click", async () => {
      await chrome.runtime.sendMessage({ type: "disconnect", connectionId: connection.id });
      await renderConnections();
    });
    row.append(label, button);
    return row;
  }));
}
