import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import type { Approval, ConnectorStatus, NotificationBinding, PublicConfig, ReadyStatus } from "../api/types";
import { dictionaries } from "../i18n";
import { ApprovalPanel, SettingsPanel } from "./panels";
import { StatusPanel } from "./panels/status";
import { ConnectorBindingSettings } from "./panels/settingsBindings";

const settingsConfig = {
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

describe("StatusPanel resident services", () => {
  it("renders all Gateway-projected lanes with readiness and latest call state", () => {
    const ready = {
      ok: true,
      gateway_binding: "127.0.0.1:18789",
      model_mode: "external",
      workspace_root: "/workspace",
      trace_dir: "/traces",
      state_backend: "file",
      state_path: "/state.json",
      speech: { enabled: true, ready: true, state: "ready", backend: "openai-http", model: "asr" },
      resident_services: [
        { lane: "fast", backend: "openai-http", model: "fast-model", readiness: "configured", last_call_status: "completed" },
        { lane: "embedding", backend: "openai-http", model: "embedding-model", readiness: "configured" },
        { lane: "guard", backend: "openai-http", model: "guard-model", readiness: "configured", last_call_status: "failed" },
        { lane: "asr", backend: "openai-http", model: "asr-model", readiness: "ready", last_call_status: "completed" },
        { lane: "ocr", backend: "openai-http", model: "ocr-model", readiness: "disabled" }
      ]
    } as ReadyStatus;
    const markup = renderToStaticMarkup(
      <StatusPanel ready={ready} modelCalls={[]} auditEvents={[]} text={dictionaries.en} />
    );
    expect(markup).toContain(dictionaries.en.status.residentServices);
    for (const value of ["fast-model", "embedding-model", "guard-model", "asr-model", "ocr-model"]) {
      expect(markup).toContain(value);
    }
    expect(markup).toContain(`${dictionaries.en.status.lastCall}: completed`);
    expect(markup).toContain(dictionaries.en.status.noServiceCalls);
  });
});

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

describe("ApprovalPanel context-bound approvals", () => {
  it("keeps approve and reject actions but omits argument editing", () => {
    const approval: Approval = {
      ...happyApproval("available"),
      id: "ap-workspace",
      source: "tool",
      external_context: undefined,
      tool: "workspace.data.access",
      risk: "read",
      summary: "Approve workspace.data.access",
      arguments: { locators: [{ path: "report.txt" }] },
      policy_context: { principal_class: "external_mcp_ai", contract_digest: "frozen" },
      presentation: {
        kind: "external_mcp_workspace_data_access",
        session_id: "session-mcp",
        requester: "AI · device-a",
        locators: [{ path: "report.txt", caption: "Quarterly report" }],
        locator_status: "unverified",
        access_class: "workspace_source_read",
        output_class: "document_content",
        return_route: { mode: "source", source_endpoint_id: "mcp:binding-a" },
        scope: "single_operation"
      }
    };
    const markup = renderToStaticMarkup(
      <ApprovalPanel approvals={[approval]} text={dictionaries.en} onResolve={() => {}} onModify={() => {}} onModifyPlan={() => {}} />
    );
    expect(markup).toContain(dictionaries.en.common.approve);
    expect(markup).toContain(dictionaries.en.common.reject);
    expect(markup).toContain(dictionaries.en.approval.workspaceDataTitle);
    expect(markup).toContain("AI · device-a");
    expect(markup).toContain("Quarterly report");
    expect(markup).toContain("report.txt");
    expect(markup).toContain(dictionaries.en.approval.unverified);
    expect(markup).toContain(dictionaries.en.approval.originalMCPConversation);
    expect(markup).not.toContain(dictionaries.en.approval.editArguments);
  });

  it("disables both decisions while the approval request is resolving", () => {
    const approval: Approval = {
      ...happyApproval("available"),
      id: "ap-resolving",
      source: "tool",
      external_context: undefined,
      tool: "workspace.data.access",
      policy_context: { principal_class: "external_mcp_ai" }
    };
    const markup = renderToStaticMarkup(
      <ApprovalPanel approvals={[approval]} text={dictionaries.en} resolvingId={approval.id} onResolve={() => {}} onModify={() => {}} onModifyPlan={() => {}} />
    );
    expect(markup.match(/disabled/g)?.length).toBe(2);
    expect(markup).toContain('class="lucide lucide-refresh-cw spin"');
  });
});

describe("SettingsPanel External MCP", () => {
  it("renders MCP through its dedicated management surface instead of a message binding card", () => {
    const connector: ConnectorStatus = {
      channel: "mcp", provider: "iscp-mcp", setup_kind: "external", available: true, enabled: false,
      running: false, state: "disabled", binding_status: "", binding_startable: false,
      supports_multiple_bindings: false, version: 0
    };
    const markup = renderToStaticMarkup(
      <SettingsPanel
        runtimeConfig={settingsConfig} ownerProfile={null} clients={[]} connectors={[connector]} notificationBindings={[]}
        text={dictionaries.en} language="en" onUpdateOwner={async () => {}} onRevokeClient={async () => {}}
        onStartNotificationBinding={async () => {}} onRefreshNotificationBinding={async () => ({}) as never}
        onOpenNotificationBindingBrowser={async () => {}}
        onRevokeNotificationBinding={async () => {}} onUpdateConnector={async () => connector} onUpdatePolicy={async () => {}}
      />
    );
    expect(markup).toContain(dictionaries.en.settings.externalMCP);
	  expect(markup).toContain(dictionaries.en.settings.connections);
	  expect(markup).toContain(dictionaries.en.settings.messaging);
    expect(markup).not.toContain(dictionaries.en.settings.addWeixinBinding);
  });
});

describe("SettingsPanel Weixin login", () => {
  it("opens provider login through managed Chromium instead of a default-browser link", () => {
    const connector: ConnectorStatus = {
      channel: "weixin", provider: "openclaw-weixin-qr", setup_kind: "qr", available: true, enabled: true,
      running: true, state: "setup_required", binding_status: "waiting_scan", binding_startable: false,
      supports_multiple_bindings: true, version: 1
    };
    const binding: NotificationBinding = {
      id: "bind-weixin", owner_id: "owner", channel: "weixin", provider: "openclaw-weixin-qr",
      status: "waiting_scan", qr_code_url: "https://liteapp.weixin.qq.com/q/provider-ticket",
      default_for_channel: false, scopes: [], created_at: "2026-08-13T00:00:00Z", updated_at: "2026-08-13T00:00:00Z"
    };
    const markup = renderToStaticMarkup(
      <ConnectorBindingSettings
		connectors={[connector]}
		notificationBindings={[binding]}
		text={dictionaries.en}
		language="en"
		onStartNotificationBinding={async () => {}}
		onRefreshNotificationBinding={async () => binding}
		onOpenNotificationBindingBrowser={async () => {}}
		onRevokeNotificationBinding={async () => {}}
		onUpdateConnector={async () => connector}
      />
    );
    expect(markup).toContain(dictionaries.en.settings.openWeixinLogin);
    expect(markup).not.toContain(`href="${binding.qr_code_url}"`);
    expect(markup).not.toContain('target="_blank"');
  });
});
