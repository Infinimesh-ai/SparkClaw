// @vitest-environment jsdom

import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { api } from "../api/client";
import type { ConnectorStatus } from "../api/types";
import { dictionaries } from "../i18n";
import { ExternalMCPSettings } from "./externalMCPSettings";

describe("ExternalMCPSettings transports", () => {
  afterEach(() => vi.restoreAllMocks());

  it("updates ISCP and LAN access with the current connector version", async () => {
    const connector: ConnectorStatus = {
      channel: "mcp",
      provider: "iscp-mcp",
      setup_kind: "external",
      available: true,
      enabled: true,
      running: false,
      state: "active",
      binding_status: "managed_externally",
      binding_startable: false,
      supports_multiple_bindings: false,
      iscp_enabled: false,
      lan_access_enabled: false,
      version: 1
    };
    vi.spyOn(api, "iscpPairingStatus").mockResolvedValue({ enabled: true, ready: true, state: "ready", expected_ticket_type: "iscp.pairing_ticket.v2" });
    vi.spyOn(api, "iscpOnboardings").mockResolvedValue({ onboardings: [] });
    vi.spyOn(api, "mcpAccessCatalog").mockResolvedValue({
      scope: "conversation",
      business_tool: "sparkclaw.conversation.send",
      iscp_enabled: false,
      lan_access_enabled: false,
      transport_version: 1,
      domain_id: "sparkclaw-local",
      endpoint_path: "/mcp"
    });
    vi.spyOn(api, "mcpAccessTickets").mockResolvedValue({ tickets: [] });
    vi.spyOn(api, "mcpBindings").mockResolvedValue({ bindings: [] });
    const update = vi.spyOn(api, "updateMCPTransports")
      .mockResolvedValueOnce({ ...connector, iscp_enabled: true, version: 2 })
      .mockResolvedValueOnce({ ...connector, iscp_enabled: true, lan_access_enabled: true, version: 3 });

    const container = document.createElement("div");
    const root = createRoot(container);
    await act(async () => {
      root.render(
        <ExternalMCPSettings
          connector={connector}
          text={dictionaries.en}
          language="en"
          onUpdateConnector={async () => connector}
        />
      );
    });

    const iscp = container.querySelector(`input[aria-label="${dictionaries.en.settings.useISCP}"]`) as HTMLInputElement;
    await act(async () => iscp.click());
    expect(update).toHaveBeenNthCalledWith(1, true, false, 1);
    expect(iscp.checked).toBe(true);

    const lan = container.querySelector(`input[aria-label="${dictionaries.en.settings.allowLANAccess}"]`) as HTMLInputElement;
    expect(container.textContent).toContain(`${window.location.origin}/mcp`);
    await act(async () => lan.click());
    expect(update).toHaveBeenNthCalledWith(2, true, true, 2);
    expect(lan.checked).toBe(true);

    await act(async () => root.unmount());
  });
});
