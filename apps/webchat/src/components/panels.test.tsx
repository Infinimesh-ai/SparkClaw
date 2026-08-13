import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import type { Approval, ConnectorStatus, PublicConfig } from "../api/types";
import { dictionaries } from "../i18n";
import { ApprovalPanel, SettingsPanel } from "./panels";

function happyApproval(planAvailability: string, plan = "# Plan\n\nInspect, implement, verify."): Approval {
  return {
    id: "ap-happy",
    source: "happy_team_plan",
    external_id: "task-1",
    external_context: {
      provider: "happy-team",
      title: "Implement feature",
      goal_prompt: "Implement the requested feature safely.",
      plan,
      plan_availability: planAvailability
    },
    session_id: "",
    run_id: "",
    tool_call_id: "",
    tool: "happy-team.review_plan",
    risk: "dangerous",
    status: "pending",
    summary: "Review Happy Team plan",
    reason: "Human decision required",
    resources: ["happy-task:task-1"],
    arguments: { taskId: "task-1" },
    created_at: "2026-08-06T00:00:00Z"
  };
}

describe("ApprovalPanel Happy plans", () => {
  it("renders task goal and plan without a raw argument editor", () => {
    const markup = renderToStaticMarkup(
      <ApprovalPanel approvals={[happyApproval("available")]} text={dictionaries.en} onResolve={() => {}} onModify={() => {}} onModifyPlan={() => {}} />
    );
    expect(markup).toContain("Implement feature");
    expect(markup).toContain("Implement the requested feature safely.");
    expect(markup).toContain("Inspect, implement, verify.");
    expect(markup).not.toContain("taskId");
    expect(markup).not.toContain("disabled");
  });

  it("disables approval and plan editing while the member machine is offline", () => {
    const markup = renderToStaticMarkup(
      <ApprovalPanel approvals={[happyApproval("temporarily_unavailable", "")]} text={dictionaries.en} onResolve={() => {}} onModify={() => {}} onModifyPlan={() => {}} />
    );
    expect(markup).toContain(dictionaries.en.approval.planUnavailable);
    expect(markup.match(/disabled/g)?.length).toBe(2);
  });
});

describe("SettingsPanel External MCP", () => {
  it("renders MCP through its dedicated management surface instead of a message binding card", () => {
    const connector: ConnectorStatus = {
      channel: "mcp", provider: "iscp-mcp", setup_kind: "external", available: true, enabled: false,
      running: false, state: "disabled", binding_status: "", binding_startable: false,
      supports_multiple_bindings: false, version: 0
    };
    const config = {
      tool_policy: { risk_counts: {}, definition_count: 0, definition_approval_required_tools: [], configured_approval_required_tools: [], denied_tools: [] },
      iscp_pairing: { enabled: false, ready: false, state: "disabled", expected_ticket_type: "iscp.pairing_ticket.v2" },
      model: {
        mock: true,
        fast: { name: "fast", model: "fast", base_url: "", context_tokens: 8192, mtp: false },
        deep: { name: "deep", model: "deep", base_url: "", context_tokens: 8192, mtp: false },
        embedding: { name: "embedding", model: "embedding", base_url: "", context_tokens: 8192, mtp: false },
        guard: { name: "guard", model: "guard", base_url: "", context_tokens: 8192, mtp: false }
      },
      gateway: { bind: "127.0.0.1", port: 18789, remote_access: "disabled", rate_limit: { enabled: false, requests_per_minute: 0, burst: 0 } },
      workspaces: { default_root: "/tmp" }, sandbox: { enabled: false }, state: { backend: "memory" },
      storage: { artifact_backend: "filesystem" }, memory: { enabled: false }, tools: { notifications: { channels: {} }, reminders: { enabled: false, default_channel: "web" } }
    } as unknown as PublicConfig;
    const markup = renderToStaticMarkup(
      <SettingsPanel
        runtimeConfig={config} ownerProfile={null} clients={[]} connectors={[connector]} notificationBindings={[]}
        text={dictionaries.en} language="en" onUpdateOwner={async () => {}} onRevokeClient={async () => {}}
        onStartNotificationBinding={async () => {}} onRefreshNotificationBinding={async () => ({}) as never}
        onRevokeNotificationBinding={async () => {}} onUpdateConnector={async () => connector} onUpdatePolicy={async () => {}}
      />
    );
    expect(markup).toContain(dictionaries.en.settings.externalMCP);
    expect(markup).toContain(dictionaries.en.settings.iscpPairing);
    expect(markup).not.toContain(dictionaries.en.settings.addWeixinBinding);
  });
});
