// @vitest-environment jsdom

import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../api/client";
import type { ConnectorStatus, NotificationBinding, PublicConfig } from "../api/types";
import { dictionaries } from "../i18n";
import { SettingsPanel } from "./panels";

const config = {
  tool_policy: { risk_counts: {}, definition_count: 0, definition_approval_required_tools: [], configured_approval_required_tools: [], denied_tools: [] },
  iscp_pairing: { enabled: false, ready: false, state: "disabled", expected_ticket_type: "iscp.pairing_ticket.v2" },
  model: {
    capacity_profile: "mock",
    mock: true,
    fast: { name: "fast", model: "fast", base_url: "", capacity_physical_model: "mock-chat", context_tokens: 8192, output_budgets: {}, mtp: false },
    deep: { name: "deep", model: "deep", base_url: "", capacity_physical_model: "mock-chat", context_tokens: 8192, output_budgets: {}, mtp: false },
    embedding: { name: "embedding", model: "embedding", base_url: "", capacity_physical_model: "mock-embedding", context_tokens: 8192, output_budgets: {}, mtp: false },
    guard: { name: "guard", model: "guard", base_url: "", capacity_physical_model: "mock-guard", context_tokens: 8192, output_budgets: {}, mtp: false }
  },
  gateway: { bind: "127.0.0.1", port: 18789, remote_access: "disabled", rate_limit: { enabled: false, requests_per_minute: 0, burst: 0 } },
  workspaces: { default_root: "/tmp" },
  sandbox: { enabled: false },
  state: { backend: "memory" },
  storage: { artifact_backend: "filesystem" },
  memory: { enabled: false },
  tools: { notifications: { channels: {} }, reminders: { enabled: false, default_channel: "web" } }
} as unknown as PublicConfig;

const connector: ConnectorStatus = {
  channel: "weixin",
  provider: "openclaw-weixin-qr",
  setup_kind: "qr",
  available: true,
  enabled: true,
  running: true,
  state: "setup_required",
  binding_status: "waiting_scan",
  binding_startable: false,
  supports_multiple_bindings: true,
  version: 1
};

function waitingBinding(updatedAt: string): NotificationBinding {
  return {
    id: "binding-weixin",
    owner_id: "owner",
    channel: "weixin",
    provider: "openclaw-weixin-qr",
    status: "waiting_scan",
    default_for_channel: false,
    scopes: [],
    created_at: "2026-08-13T00:00:00Z",
    updated_at: updatedAt
  };
}

describe("SettingsPanel binding polling", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.spyOn(api, "iscpPairingStatus").mockResolvedValue(config.iscp_pairing);
    vi.spyOn(api, "iscpOnboardings").mockResolvedValue({ onboardings: [] });
    vi.spyOn(api, "mcpAccessCatalog").mockResolvedValue({
      scope: "conversation",
      business_tool: "sparkclaw.conversation.send",
      iscp_enabled: false,
      lan_access_enabled: false,
      transport_version: 0,
      endpoint_path: "/mcp"
    });
    vi.spyOn(api, "mcpAccessTickets").mockResolvedValue({ tickets: [] });
    vi.spyOn(api, "mcpBindings").mockResolvedValue({ bindings: [] });
    vi.spyOn(api, "updateMCPTransports").mockResolvedValue(connector);
	vi.spyOn(api, "integrations").mockResolvedValue({ integrations: [] });
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("keeps one long poll across parent rerenders and aborts it on cleanup", async () => {
    const container = document.createElement("div");
    const root = createRoot(container);
    const signals: AbortSignal[] = [];
    const refresh = vi.fn((_id: string, signal?: AbortSignal) => new Promise<NotificationBinding>((_resolve, reject) => {
      if (!signal) return;
      signals.push(signal);
      signal.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")), { once: true });
    }));
    const render = (binding: NotificationBinding) => root.render(
      <SettingsPanel
        runtimeConfig={config}
        ownerProfile={null}
        clients={[]}
        connectors={[connector]}
        notificationBindings={[binding]}
        text={dictionaries.en}
        language="en"
        onUpdateOwner={async () => {}}
        onRevokeClient={async () => {}}
        onStartNotificationBinding={async () => {}}
        onRefreshNotificationBinding={(id, signal) => refresh(id, signal)}
        onOpenNotificationBindingBrowser={async () => {}}
        onRevokeNotificationBinding={async () => {}}
        onUpdateConnector={async () => connector}
        onUpdatePolicy={async () => {}}
      />
    );

    await act(async () => render(waitingBinding("2026-08-13T00:00:00Z")));
	const messaging = Array.from(container.querySelectorAll("button")).find((button) => button.textContent?.includes(dictionaries.en.settings.messaging));
	expect(messaging).toBeTruthy();
	await act(async () => messaging?.click());
    await act(async () => vi.advanceTimersByTimeAsync(1000));
    expect(refresh).toHaveBeenCalledTimes(1);

    await act(async () => render(waitingBinding("2026-08-13T00:00:05Z")));
    await act(async () => vi.advanceTimersByTimeAsync(6000));
    expect(refresh).toHaveBeenCalledTimes(1);
    expect(signals[0]?.aborted).toBe(false);

    await act(async () => root.unmount());
    expect(signals[0]?.aborted).toBe(true);
  });
});
