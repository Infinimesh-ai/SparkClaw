import { describe, expect, it } from "vitest";
import type { ConnectorStatus, NotificationBinding } from "../api/types";
import { bindingsForConnector, connectorBindingStartDisabled, pendingBindingPollKey } from "./connectors";

function connector(overrides: Partial<ConnectorStatus> = {}): ConnectorStatus {
  return {
    channel: "telegram",
    provider: "telegram-bot-api",
    setup_kind: "secret",
    available: true,
    enabled: false,
    running: false,
    state: "disabled",
    binding_status: "unbound",
    binding_startable: false,
    supports_multiple_bindings: true,
    version: 0,
    ...overrides
  };
}

function binding(id: string, channel: string, status: string, updatedAt: string): NotificationBinding {
  return {
    id,
    owner_id: "owner",
    channel,
    provider: channel + "-provider",
    status,
    default_for_channel: false,
    scopes: [],
    created_at: updatedAt,
    updated_at: updatedAt
  };
}

describe("connector controls", () => {
  it("never starts binding setup while the channel is disabled", () => {
    expect(connectorBindingStartDisabled(connector(), false, true)).toBe(true);
    expect(connectorBindingStartDisabled(connector({ enabled: true, binding_startable: true }), false, false)).toBe(true);
    expect(connectorBindingStartDisabled(connector({ enabled: true, binding_startable: true }), false, true)).toBe(false);
    expect(connectorBindingStartDisabled(connector({ channel: "weixin", setup_kind: "qr", enabled: true, binding_startable: true }), false, false)).toBe(false);
  });

  it("shows only visible bindings for the selected connector", () => {
    const records = [
      binding("old", "telegram", "active", "2026-01-01T00:00:00Z"),
      binding("new", "telegram", "waiting_scan", "2026-01-02T00:00:00Z"),
      binding("revoked", "telegram", "revoked", "2026-01-03T00:00:00Z"),
      binding("weixin", "weixin", "active", "2026-01-04T00:00:00Z")
    ];
    expect(bindingsForConnector(records, "telegram").map((record) => record.id)).toEqual(["new", "old"]);
  });

  it("keeps the polling identity stable across binding refreshes", () => {
    const waiting = binding("weixin", "weixin", "waiting_scan", "2026-01-01T00:00:00Z");
    const refreshed = { ...waiting, updated_at: "2026-01-01T00:00:30Z", qr_code_url: "https://example.com/qr" };

    expect(pendingBindingPollKey([waiting])).toBe(pendingBindingPollKey([refreshed]));
    expect(pendingBindingPollKey([binding("telegram", "telegram", "waiting_scan", "2026-01-01T00:00:00Z"), waiting]))
      .toBe(pendingBindingPollKey([waiting, binding("telegram", "telegram", "waiting_scan", "2026-01-02T00:00:00Z")]));
    expect(pendingBindingPollKey([{ ...refreshed, status: "expired" }])).toBe("[]");
  });
});
