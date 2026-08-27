// @vitest-environment jsdom

import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { api } from "../api/client";
import type { IntegrationStatus, PublicConfig } from "../api/types";
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
  tool_policy: { risk_counts: {}, definition_count: 2, definition_approval_required_tools: [], configured_approval_required_tools: [], denied_tools: [] },
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

describe("Integration credential settings", () => {
  afterEach(() => vi.restoreAllMocks());

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

describe("Settings directory navigation", () => {
  afterEach(() => vi.restoreAllMocks());

  it("drills into categories and keeps integration details out of the directory", async () => {
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
    expect(container.querySelectorAll(".settingsDirectoryRow")).toHaveLength(4);
    expect(container.textContent).not.toContain(dictionaries.en.settings.licenseId);

    const info = findButton(container, dictionaries.en.settings.info);
    await act(async () => info.click());
    expect(container.textContent).toContain(dictionaries.en.settings.licenseId);
    expect(container.textContent).toContain("Family account");

    const back = container.querySelector(".settingsBack") as HTMLButtonElement;
    await act(async () => back.click());
    const account = findButton(container, dictionaries.en.settings.account);
    await act(async () => account.click());
    expect(container.textContent).toContain(dictionaries.en.settings.ownerProfile);
    expect(container.querySelectorAll(".settingsDirectoryRow")).toHaveLength(2);
    await act(async () => root.unmount());
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
