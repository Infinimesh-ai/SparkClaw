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

  it("deletes terminal access records individually or all at once", async () => {
    const connector: ConnectorStatus = {
      channel: "mcp",
      provider: "iscp-mcp",
      setup_kind: "external",
      available: true,
      enabled: true,
      running: true,
      state: "active",
      binding_status: "managed_externally",
      binding_startable: false,
      supports_multiple_bindings: false,
      iscp_enabled: true,
      lan_access_enabled: false,
      version: 1
    };
    vi.spyOn(api, "iscpPairingStatus").mockResolvedValue({ enabled: true, ready: true, state: "ready", expected_ticket_type: "iscp.pairing_ticket.v2" });
    vi.spyOn(api, "iscpOnboardings").mockResolvedValue({ onboardings: [] });
    vi.spyOn(api, "mcpAccessCatalog").mockResolvedValue({
      scope: "conversation",
      business_tool: "sparkclaw.conversation.send",
      iscp_enabled: true,
      lan_access_enabled: false,
      transport_version: 1,
      domain_id: "sparkclaw-local",
      endpoint_path: "/mcp"
    });
    vi.spyOn(api, "mcpAccessTickets").mockResolvedValue({ tickets: [{
      schema_version: 2,
      id: "tick-dead1",
      owner_id: "owner",
      actor_id: "owner",
      domain_id: "domain-a",
      authorization_revision: 1,
      scope: "conversation",
      status: "expired",
      max_uses: 1,
      use_count: 0,
      issued_at: "2026-08-12T00:00:00Z",
      expires_at: "2026-08-13T00:00:00Z"
    }] });
    vi.spyOn(api, "mcpBindings").mockResolvedValue({ bindings: [{
      schema_version: 2,
      id: "bind-dead1",
      owner_id: "owner",
      actor_id: "owner",
      domain_id: "domain-a",
      requester_device_id: "device-a",
      requester_key_thumbprint: "thumb-a",
      authorization_revision: 1,
      scope: "conversation",
      status: "revoked",
      linked_session_id: "session-a",
      created_at: "2026-08-12T00:00:00Z",
      updated_at: "2026-08-13T00:00:00Z"
    }] });
    const deleteTicket = vi.spyOn(api, "deleteMCPAccessTicket").mockResolvedValue({} as never);
    const deleteBinding = vi.spyOn(api, "deleteMCPBinding").mockResolvedValue({} as never);
    const deleteAll = vi.spyOn(api, "deleteAllMCPAccessRecords").mockResolvedValue({ deleted_tickets: 1, deleted_bindings: 1 });
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(true);

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
    const click = async (label: string) => {
      const button = container.querySelector(`button[aria-label="${label}"]`) as HTMLButtonElement | null;
      expect(button).not.toBeNull();
      await act(async () => {
        button?.click();
        await new Promise((resolve) => window.setTimeout(resolve, 0));
      });
    };

    await click(`${dictionaries.en.settings.deleteAccessRecord}: bind-dead1`);
    expect(deleteBinding).toHaveBeenCalledWith("bind-dead1");
    await click(`${dictionaries.en.settings.deleteAccessRecord}: tick-dead1`);
    expect(deleteTicket).toHaveBeenCalledWith("tick-dead1");
    await click(dictionaries.en.settings.deleteAllAccessRecords);
    expect(deleteAll).toHaveBeenCalledTimes(1);
    expect(confirm).toHaveBeenCalledTimes(3);

    await act(async () => root.unmount());
  });

  it("does not let a stale connector prop from the background poll revert transport toggles", async () => {
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
    const render = (value: ConnectorStatus) =>
      act(async () => {
        root.render(
          <ExternalMCPSettings
            connector={value}
            text={dictionaries.en}
            language="en"
            onUpdateConnector={async () => value}
          />
        );
      });
    await render(connector);

    const iscp = container.querySelector(`input[aria-label="${dictionaries.en.settings.useISCP}"]`) as HTMLInputElement;
    await act(async () => iscp.click());
    expect(update).toHaveBeenNthCalledWith(1, true, false, 1);
    expect(iscp.checked).toBe(true);

    // App's 5s poll re-delivers a connector snapshot fetched before the
    // toggle landed; it must not revert the transport state or version.
    await render({ ...connector });
    expect((container.querySelector(`input[aria-label="${dictionaries.en.settings.useISCP}"]`) as HTMLInputElement).checked).toBe(true);

    const lan = container.querySelector(`input[aria-label="${dictionaries.en.settings.allowLANAccess}"]`) as HTMLInputElement;
    await act(async () => lan.click());
    expect(update).toHaveBeenNthCalledWith(2, true, true, 2);
    expect(lan.checked).toBe(true);

    await act(async () => root.unmount());
  });

  it("removes an issued access ticket from the screen once it expires", async () => {
    vi.useFakeTimers();
    const connector: ConnectorStatus = {
      channel: "mcp",
      provider: "iscp-mcp",
      setup_kind: "external",
      available: true,
      enabled: true,
      running: true,
      state: "active",
      binding_status: "managed_externally",
      binding_startable: false,
      supports_multiple_bindings: false,
      iscp_enabled: true,
      lan_access_enabled: false,
      version: 1
    };
    vi.spyOn(api, "iscpPairingStatus").mockResolvedValue({ enabled: true, ready: true, state: "ready", expected_ticket_type: "iscp.pairing_ticket.v2" });
    vi.spyOn(api, "iscpOnboardings").mockResolvedValue({ onboardings: [] });
    vi.spyOn(api, "mcpAccessCatalog").mockResolvedValue({
      scope: "conversation",
      business_tool: "sparkclaw.conversation.send",
      iscp_enabled: true,
      lan_access_enabled: false,
      transport_version: 1,
      domain_id: "sparkclaw-local",
      endpoint_path: "/mcp"
    });
    vi.spyOn(api, "mcpAccessTickets").mockResolvedValue({ tickets: [] });
    vi.spyOn(api, "mcpBindings").mockResolvedValue({ bindings: [] });
    vi.spyOn(api, "issueMCPAccessTicket").mockResolvedValue({
      secret: "one-time-secret",
      ticket: {
        schema_version: 2,
        id: "tick-expiry",
        owner_id: "owner",
        actor_id: "owner",
        domain_id: "sparkclaw-local",
        authorization_revision: 1,
        scope: "conversation",
        status: "pending",
        max_uses: 1,
        use_count: 0,
        issued_at: new Date().toISOString(),
        expires_at: new Date(Date.now() + 60_000).toISOString()
      }
    });

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

    const issue = container.querySelector("button.externalMCPPrimary") as HTMLButtonElement;
    await act(async () => issue.click());
    expect(container.textContent).toContain("one-time-secret");

    act(() => {
      vi.advanceTimersByTime(61_000);
    });
    expect(container.textContent).not.toContain("one-time-secret");

    await act(async () => root.unmount());
    vi.useRealTimers();
  });
});
