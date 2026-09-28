// @vitest-environment jsdom

import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { api } from "../api/client";
import type { ConnectorStatus, IntegrationStatus, PublicConfig } from "../api/types";
import { dictionaries } from "../i18n";
import { SettingsPanel } from "./panels";
import { IntegrationCredentialSettings } from "./panels/settingsIntegrations";

const infoStatus: IntegrationStatus = {
  id: "infinimesh-info",
  category: "data_provider",
  configured: true,
  source: "operator",
  state: "ready",
  editable: true,
  checkable: true,
  operator_available: true,
  credentials: [
    { id: "info-a", label: "Family account", validated_at: "2026-08-27T02:00:00Z", state: "ready", active: false },
    { id: "info-b", label: "备用家庭研究账号凭据名称用于窄屏布局测试", validated_at: "2026-08-27T03:00:00Z", state: "ready", active: false }
  ]
};

const localMindStatus: IntegrationStatus = {
  id: "localmind",
  category: "outbound_mcp",
  configured: false,
  source: "none",
  state: "not_configured",
  editable: true,
  checkable: true,
  operator_available: false,
  credentials: []
};

const runtimeConfig = {
  tool_policy: { risk_counts: {}, definition_count: 2, definition_approval_required_tools: [], configured_approval_required_tools: [], denied_tools: [], operator_controls: { web_access: true, workspace_files: true, shell_commands: true, file_changes: "default", external_actions: "current" } },
  iscp_pairing: { enabled: false, ready: false, state: "disabled", expected_ticket_type: "iscp.pairing_ticket.v2" },
  model: {
    capacity_profile: "mock",
    mock: true,
    fast: { name: "fast", model: "fast", base_url: "", capacity_physical_model: "mock-chat", context_tokens: 8192, output_budgets: {} },
    deep: { name: "deep", model: "deep", base_url: "", capacity_physical_model: "mock-chat", context_tokens: 8192, output_budgets: {} },
    embedding: { name: "embedding", model: "embedding", base_url: "", capacity_physical_model: "mock-embedding", context_tokens: 8192, output_budgets: {} },
    guard: { name: "guard", model: "guard", base_url: "", capacity_physical_model: "mock-guard", context_tokens: 8192, output_budgets: {} }
  },
  gateway: { bind: "127.0.0.1", port: 18789, remote_access: "disabled", rate_limit: { enabled: false, requests_per_minute: 0, burst: 0 } },
  workspaces: { default_root: "/tmp" }, sandbox: { enabled: false }, state: { backend: "memory" },
  storage: { artifact_backend: "filesystem" }, memory: { enabled: false }, tools: { notifications: { channels: {} }, reminders: { enabled: false, default_channel: "web" } }
} as unknown as PublicConfig;

const messageConnectors: ConnectorStatus[] = [
  {
    channel: "weixin", provider: "openclaw-weixin-qr", setup_kind: "qr", available: true, enabled: true,
    running: true, state: "active", binding_status: "active", binding_startable: true,
    supports_multiple_bindings: true, version: 2
  },
  {
    channel: "telegram", provider: "telegram-bot-api", setup_kind: "secret", available: true, enabled: false,
    running: false, state: "disabled", binding_status: "", binding_startable: false,
    supports_multiple_bindings: true, version: 1
  }
];

describe("Integration credential settings", () => {
  afterEach(() => vi.restoreAllMocks());

  it("keeps the credential form visible when status cannot be loaded", async () => {
    vi.spyOn(api, "integration").mockRejectedValue(new Error("offline"));
    const container = document.createElement("div");
    const root = createRoot(container);
    await act(async () => root.render(<IntegrationCredentialSettings id="localmind" status={null} text={dictionaries.zh} language="zh" onStatus={() => {}} />));
    expect(container.querySelector('input[type="url"]')).not.toBeNull();
    expect(container.querySelector('input[type="password"]')).not.toBeNull();
    expect(container.querySelector(".integrationStatusBar button")).not.toBeNull();
    await act(async () => root.unmount());
  });

  it("renders only redacted summaries and clears secrets after failed validation", async () => {
    const add = vi.spyOn(api, "addInfoCredential").mockRejectedValue(new Error("credentials were rejected"));
    const container = document.createElement("div");
    const root = createRoot(container);
    await act(async () => {
      root.render(<IntegrationCredentialSettings id="infinimesh-info" status={infoStatus} text={dictionaries.zh} language="zh" onStatus={() => {}} />);
    });

    expect(container.textContent).toContain("Family account");
    expect(container.textContent).toContain("备用家庭研究账号凭据名称用于窄屏布局测试");
    expect(container.innerHTML).not.toContain("ilk_v1");
    expect(container.innerHTML).not.toContain("secret-token");

    const inputs = Array.from(container.querySelectorAll("input"));
    await changeInput(inputs[0], "Rejected account");
    await changeInput(inputs[1], "lic_rejected");
    await changeInput(inputs[2], "ilk_v1.lic_rejected.secret-token");
    const form = container.querySelector("form") as HTMLFormElement;
    await act(async () => { form.requestSubmit(); });

    expect(add).toHaveBeenCalledWith("Rejected account", "lic_rejected", "ilk_v1.lic_rejected.secret-token");
    expect(container.textContent).toContain(dictionaries.zh.settings.validationFailed);
    expect(container.textContent).toContain("credentials were rejected");
    expect(container.textContent).not.toContain("Rejected account");
    expect(Array.from(container.querySelectorAll("input")).every((input) => input.value === "")).toBe(true);
    expect(container.querySelectorAll(".credentialRow")).toHaveLength(3);
    await act(async () => root.unmount());
  });

  it("announces validation progress and a successful save", async () => {
    let resolveAdd: (status: IntegrationStatus) => void = () => {};
    vi.spyOn(api, "addInfoCredential").mockReturnValue(new Promise((resolve) => { resolveAdd = resolve; }));
    const onStatus = vi.fn();
    const container = document.createElement("div");
    const root = createRoot(container);
    await act(async () => {
      root.render(<IntegrationCredentialSettings id="infinimesh-info" status={infoStatus} text={dictionaries.zh} language="zh" onStatus={onStatus} />);
    });

    const inputs = Array.from(container.querySelectorAll("input"));
    await changeInput(inputs[0], "家庭主账号");
    await changeInput(inputs[1], "lic_family");
    await changeInput(inputs[2], "ilk_v1.lic_family.secret-token");
    const form = container.querySelector("form") as HTMLFormElement;
    await act(async () => {
      form.requestSubmit();
      await Promise.resolve();
    });

    const submit = form.querySelector('button[type="submit"]') as HTMLButtonElement;
    expect(submit.disabled).toBe(true);
    expect(submit.textContent).toContain(dictionaries.zh.settings.validatingAndSaving);
    expect(container.querySelector(".integrationDetail")?.getAttribute("aria-busy")).toBe("true");
    expect(container.querySelector('[role="status"]')?.textContent).toContain(dictionaries.zh.settings.validationInProgress);
    expect(container.querySelector(".integrationState")?.textContent).toBe(dictionaries.zh.settings.integrationChecking);

    await act(async () => { resolveAdd(infoStatus); });

    expect(onStatus).toHaveBeenCalledWith(infoStatus);
    expect(submit.disabled).toBe(false);
    expect(container.querySelector('[role="status"]')?.textContent).toContain(dictionaries.zh.settings.validationSucceeded);
    expect(container.textContent).toContain(dictionaries.zh.settings.credentialSaved);
    expect(Array.from(container.querySelectorAll("input")).every((input) => input.value === "")).toBe(true);
    await act(async () => root.unmount());
  });

  it("refreshes persisted integration status after a failed connection check", async () => {
    const failedStatus: IntegrationStatus = {
      ...infoStatus,
      state: "needs_attention",
      error_code: "credential_auth_failed",
      credentials: infoStatus.credentials.map((item) => item.id === "info-a"
        ? { ...item, state: "needs_attention", error_code: "credential_auth_failed" }
        : item)
    };
    const check = vi.spyOn(api, "checkIntegrationCredential").mockRejectedValue(new Error("credentials were rejected"));
    const refresh = vi.spyOn(api, "integration").mockResolvedValue(failedStatus);
    const onStatus = vi.fn();
    const container = document.createElement("div");
    const root = createRoot(container);
    await act(async () => {
      root.render(<IntegrationCredentialSettings id="infinimesh-info" status={infoStatus} text={dictionaries.en} language="en" onStatus={onStatus} />);
    });

    const row = Array.from(container.querySelectorAll(".credentialRow")).find((item) => item.textContent?.includes("Family account"));
    const checkButton = row?.querySelector(`button[title="${dictionaries.en.settings.checkConnection}"]`) as HTMLButtonElement;
    await act(async () => {
      checkButton.click();
      await Promise.resolve();
    });

    expect(check).toHaveBeenCalledWith("infinimesh-info", "info-a");
    expect(refresh).toHaveBeenCalledWith("infinimesh-info");
    expect(onStatus).toHaveBeenCalledWith(failedStatus);
    expect(container.textContent).toContain("credentials were rejected");
    await act(async () => root.unmount());
  });

  it("requires confirmation before selecting another effective credential", async () => {
    const activate = vi.spyOn(api, "activateIntegrationCredential").mockResolvedValue({
      ...infoStatus,
      source: "household",
      active_credential_id: "info-a",
      credentials: infoStatus.credentials.map((item) => ({ ...item, active: item.id === "info-a" }))
    });
    const confirm = vi.spyOn(window, "confirm").mockReturnValueOnce(false).mockReturnValueOnce(true);
    const container = document.createElement("div");
    const root = createRoot(container);
    await act(async () => {
      root.render(<IntegrationCredentialSettings id="infinimesh-info" status={infoStatus} text={dictionaries.en} language="en" onStatus={() => {}} />);
    });
    const row = Array.from(container.querySelectorAll(".credentialRow")).find((item) => item.textContent?.includes("Family account"));
    const use = row?.querySelector(`button[title="${dictionaries.en.settings.useCredential}"]`) as HTMLButtonElement;
    await act(async () => use.click());
    expect(activate).not.toHaveBeenCalled();
    await act(async () => use.click());
    expect(confirm).toHaveBeenCalledTimes(2);
    expect(activate).toHaveBeenCalledWith("infinimesh-info", "info-a");
    await act(async () => root.unmount());
  });
});

describe("Connection directory navigation", () => {
  afterEach(() => vi.restoreAllMocks());

  it("moves every connection into the connections page and keeps details out of its directory", async () => {
    vi.spyOn(api, "integrations").mockResolvedValue({ integrations: [infoStatus, localMindStatus] });
    const container = document.createElement("div");
    const root = createRoot(container);
    await act(async () => {
      root.render(
        <SettingsPanel
          connectionsOnly
          runtimeConfig={runtimeConfig} ownerProfile={null} clients={[]} connectors={[]} notificationBindings={[]}
          text={dictionaries.en} language="en" onUpdateOwner={async () => {}} onRevokeClient={async () => {}}
          onStartNotificationBinding={async () => {}} onRefreshNotificationBinding={async () => ({}) as never}
          onOpenNotificationBindingBrowser={async () => {}} onRevokeNotificationBinding={async () => {}}
          onUpdateConnector={async () => ({}) as never} onUpdatePolicy={async () => {}}
        />
      );
    });
    expect(container.querySelectorAll(".settingsDirectoryRow")).toHaveLength(7);
    expect(container.textContent).toContain(dictionaries.en.settings.messaging);
    expect(container.textContent).toContain(dictionaries.en.settings.aiPlatformLogin);
    expect(container.textContent).toContain(dictionaries.en.settings.browserControl);
    expect(container.textContent).toContain(dictionaries.en.settings.browserEmail);
    expect(container.textContent).toContain(dictionaries.en.settings.info);
    expect(container.textContent).toContain(dictionaries.en.settings.localMind);
    expect(container.textContent).toContain(dictionaries.en.settings.externalMCP);
    expect(container.textContent).not.toContain(dictionaries.en.settings.licenseId);

    const info = findButton(container, dictionaries.en.settings.info);
    await act(async () => info.click());
    expect(container.textContent).toContain(dictionaries.en.settings.licenseId);
    expect(container.textContent).toContain("Family account");

    const back = container.querySelector(".settingsBack") as HTMLButtonElement;
    await act(async () => back.click());
    expect(container.querySelectorAll(".settingsDirectoryRow")).toHaveLength(7);
    await act(async () => root.unmount());
  });

  it("keeps every connection available from the workspace settings connection category", async () => {
    vi.spyOn(api, "integrations").mockResolvedValue({ integrations: [infoStatus, localMindStatus] });
    const container = document.createElement("div");
    const root = createRoot(container);
    await act(async () => {
      root.render(
        <SettingsPanel
          runtimeConfig={runtimeConfig} ownerProfile={null} clients={[]} connectors={[]} notificationBindings={[]}
          text={dictionaries.en} language="en" onUpdateOwner={async () => {}} onRevokeClient={async () => {}}
          onStartNotificationBinding={async () => {}} onRefreshNotificationBinding={async () => ({}) as never}
          onOpenNotificationBindingBrowser={async () => {}} onRevokeNotificationBinding={async () => {}}
          onUpdateConnector={async () => ({}) as never} onUpdatePolicy={async () => {}}
        />
      );
    });
    expect(container.querySelectorAll(".settingsCategoryTabs button")).toHaveLength(4);
    expect(container.querySelectorAll(".settingsDirectoryRow")).toHaveLength(2);
    const connections = findButton(container, dictionaries.en.settings.connections);
    await act(async () => connections.click());
    expect(container.querySelectorAll(".settingsDirectoryRow")).toHaveLength(7);
    expect(container.textContent).toContain(dictionaries.en.settings.messaging);
    expect(container.textContent).toContain(dictionaries.en.settings.browserControl);
    expect(container.textContent).toContain(dictionaries.en.settings.externalMCP);
    await act(async () => root.unmount());
  });

  it("renders each backend messaging channel as a connection row in the aligned settings surface", async () => {
    vi.spyOn(api, "integrations").mockResolvedValue({ integrations: [infoStatus, localMindStatus] });
    const container = document.createElement("div");
    const root = createRoot(container);
    await act(async () => {
      root.render(
        <SettingsPanel
          section="connections"
          runtimeConfig={runtimeConfig} ownerProfile={null} clients={[]} connectors={messageConnectors} notificationBindings={[]}
          text={dictionaries.zh} language="zh" onUpdateOwner={async () => {}} onRevokeClient={async () => {}}
          onStartNotificationBinding={async () => {}} onRefreshNotificationBinding={async () => ({}) as never}
          onOpenNotificationBindingBrowser={async () => {}} onRevokeNotificationBinding={async () => {}}
          onUpdateConnector={async () => ({}) as never} onUpdatePolicy={async () => {}}
        />
      );
    });

    expect(container.textContent).toContain("消息渠道");
    expect(container.textContent).toContain(dictionaries.zh.settings.weixinBinding);
    expect(container.textContent).toContain(dictionaries.zh.settings.telegramBinding);
    expect(container.querySelectorAll(".settingsConnectionRow")).toHaveLength(8);

    const telegram = findButton(container, dictionaries.zh.settings.telegramBinding);
    await act(async () => telegram.click());
    expect(container.textContent).toContain(dictionaries.zh.settings.telegramToken);
    expect(container.textContent).not.toContain(dictionaries.zh.settings.weixinBinding);
    await act(async () => root.unmount());
  });

  it("keeps unsupported and explicitly removed settings out of the aligned pages", async () => {
    vi.spyOn(api, "integrations").mockResolvedValue({ integrations: [] });
    const container = document.createElement("div");
    const root = createRoot(container);
    await act(async () => {
      root.render(
        <SettingsPanel
          section="general"
          runtimeConfig={runtimeConfig} ownerProfile={null} clients={[]} connectors={[]} notificationBindings={[]}
          text={dictionaries.zh} language="zh" onLanguageChange={() => {}} onUpdateOwner={async () => {}} onRevokeClient={async () => {}}
          onStartNotificationBinding={async () => {}} onRefreshNotificationBinding={async () => ({}) as never}
          onOpenNotificationBindingBrowser={async () => {}} onRevokeNotificationBinding={async () => {}}
          onUpdateConnector={async () => ({}) as never} onUpdatePolicy={async () => {}}
        />
      );
    });

    expect(container.textContent).toContain("界面语言");
    expect(container.textContent).not.toMatch(/时区|启动页面|恢复未完成任务|界面密度|动态效果|默认模型|推理强度/);
    await act(async () => root.unmount());
  });

  it("updates independent tool and approval controls through the gateway policy", async () => {
    vi.spyOn(api, "integrations").mockResolvedValue({ integrations: [] });
    const onUpdatePolicy = vi.fn(async () => {});
    const container = document.createElement("div");
    const root = createRoot(container);
    const props = {
      runtimeConfig, ownerProfile: null, clients: [], connectors: [], notificationBindings: [],
      text: dictionaries.en, language: "en" as const, onUpdateOwner: async () => {}, onRevokeClient: async () => {},
      onStartNotificationBinding: async () => {}, onRefreshNotificationBinding: async () => ({}) as never,
      onOpenNotificationBindingBrowser: async () => {}, onRevokeNotificationBinding: async () => {},
      onUpdateConnector: async () => ({}) as never, onUpdatePolicy
    };
    try {
      await act(async () => root.render(<SettingsPanel {...props} section="models-tools" />));
      expect(container.textContent).not.toContain("Model profiles");
      const webSwitch = container.querySelector('button[role="switch"][aria-label="Web access"]') as HTMLButtonElement;
      await act(async () => webSwitch.click());
      expect(onUpdatePolicy).toHaveBeenCalledWith([], [], expect.objectContaining({ web_access: false, external_actions: "current" }));

      await act(async () => root.render(<SettingsPanel {...props} section="permissions" />));
      const externalSelect = container.querySelector('select[aria-label="External actions"]') as HTMLSelectElement;
      await act(async () => {
        externalSelect.value = "block";
        externalSelect.dispatchEvent(new Event("change", { bubbles: true }));
      });
      expect(onUpdatePolicy).toHaveBeenCalledWith([], [], expect.objectContaining({ external_actions: "block", web_access: true }));
    } finally {
      await act(async () => root.unmount());
    }
  });

  it("keeps agent configuration visible while status is unavailable and offers a retry", async () => {
    vi.spyOn(api, "integrations").mockRejectedValue(new Error("offline"));
    const onCheckStatus = vi.fn(async () => {});
    const container = document.createElement("div");
    const root = createRoot(container);
    const props = {
      runtimeConfig: null, ownerProfile: null, clients: [], connectors: [], notificationBindings: [],
      text: dictionaries.zh, language: "zh" as const, onUpdateOwner: async () => {}, onRevokeClient: async () => {},
      onStartNotificationBinding: async () => {}, onRefreshNotificationBinding: async () => ({}) as never,
      onOpenNotificationBindingBrowser: async () => {}, onRevokeNotificationBinding: async () => {},
      onUpdateConnector: async () => ({}) as never, onUpdatePolicy: async () => {}, onCheckStatus
    };
    try {
      await act(async () => root.render(<SettingsPanel {...props} section="models-tools" />));
      expect(container.textContent).toContain("网络访问");
      expect(container.textContent).toContain("工作区文件");
      expect(container.textContent).toContain("终端命令");
      expect(container.querySelector('button[aria-label="网络访问"]')?.hasAttribute("disabled")).toBe(true);
      await act(async () => findButton(container, "检查状态").click());
      expect(onCheckStatus).toHaveBeenCalledWith("models-tools");

      await act(async () => root.render(<SettingsPanel {...props} section="permissions" />));
      expect(container.querySelector('select[aria-label="文件变更"]')).not.toBeNull();
      expect(container.querySelector('select[aria-label="外部操作"]')?.hasAttribute("disabled")).toBe(true);

      await act(async () => root.render(<SettingsPanel {...props} section="connections" />));
      expect(container.textContent).toContain(dictionaries.zh.settings.weixinBinding);
      expect(container.textContent).toContain(dictionaries.zh.settings.telegramBinding);
      await act(async () => findButton(container, dictionaries.zh.settings.telegramBinding).click());
      expect(container.querySelector('input[type="password"]')).not.toBeNull();
      expect(container.textContent).not.toContain("配置不可用");
    } finally {
      await act(async () => root.unmount());
    }
  });
});

async function changeInput(input: HTMLInputElement, value: string) {
  await act(async () => {
    const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")?.set;
    setter?.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

function findButton(container: HTMLElement, label: string) {
  const button = Array.from(container.querySelectorAll("button")).find((item) => item.textContent?.includes(label));
  if (!button) throw new Error(`button not found: ${label}`);
  return button;
}
