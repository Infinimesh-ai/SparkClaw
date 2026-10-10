// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { api } from "../api/client";
import { dictionaries } from "../i18n";
import type { DesktopConnectionStatus } from "./types";
import type { PublicConfig } from "../api/types";
import { SettingsPanel, type WorkspaceSettingsSection } from "../components/panels/settings";
import { ISCPDeviceAuthorization, iscpSettingsAccess } from "./iscpSettings";

beforeEach(() => vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true));
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); delete window.sparkclawDesktop; });
const connection = (enabled: boolean): DesktopConnectionStatus => ({
  schema_version: 1, state: "connected", client_id: "client",
  backend: { schema_version: 3, transport: "iscp", origin: "https://iscp.invalid", deployment_id: "deployment" },
  capabilities: { operations: [], files: false, mail: false, browser: false, speech: false, approvals: false, settings: enabled,
    surfaces: { mail_settings: { enabled, reason: enabled ? "" : "qualification_required" } } },
});
const props = {
  runtimeConfig: null, ownerProfile: null, clients: [], connectors: [], notificationBindings: [],
  onUpdateOwner: vi.fn(), onRevokeClient: vi.fn(), onStartNotificationBinding: vi.fn(), onRefreshNotificationBinding: vi.fn(),
  onOpenNotificationBindingBrowser: vi.fn(), onRevokeNotificationBinding: vi.fn(), onUpdateConnector: vi.fn(), onUpdatePolicy: vi.fn(),
};
const button = (host: HTMLElement, label: string) => [...host.querySelectorAll<HTMLButtonElement>("button")].find(item => item.textContent?.includes(label))!;

it.each(["en", "zh"] as const)("uses the same original general and appearance markup for both transports in %s", async language => {
  const integrations = vi.spyOn(api, "integrations");
  const host = document.createElement("div"), root = createRoot(host);
  try {
    for (const section of ["general", "appearance"] as const) {
      await act(async () => root.render(<SettingsPanel key={`direct-${section}`} {...props} section={section} text={dictionaries[language]} language={language} onLanguageChange={vi.fn()} />));
      const original = host.innerHTML;
      await act(async () => root.render(<SettingsPanel key={`iscp-${section}`} {...props} section={section} access={iscpSettingsAccess(connection(false), language)} text={dictionaries[language]} language={language} onLanguageChange={vi.fn()} />));
      expect(host.innerHTML).toBe(original);
    }
    expect(integrations).not.toHaveBeenCalled();
  } finally { await act(async () => root.unmount()); }
});

it("keeps the original connections directory and mounts only qualified provider controls after opening Email", async () => {
  const providers = [
    { provider: "outlook" as const, display_name: "Outlook", enabled: true, default: false, account: "default" as const, state: "ready" as const, version: 3 },
    { provider: "qq_mail" as const, display_name: "QQ Mail", enabled: true, default: false, account: "default" as const, state: "login_required" as const, version: 2 },
  ];
  const list = vi.spyOn(api, "emailProviders").mockResolvedValue({ providers });
  const integrations = vi.spyOn(api, "integrations");
  const login = vi.spyOn(api, "openEmailLoginBrowser").mockResolvedValue({ ...providers[0], state: "login_required" });
  const check = vi.spyOn(api, "checkEmailProvider").mockResolvedValue(providers[0]);
  const send = vi.spyOn(api, "sendEmailDraft");
  const browser = vi.spyOn(api, "browserExtension");
  const host = document.createElement("div"), root = createRoot(host);
  const render = async (enabled: boolean, section: WorkspaceSettingsSection = "connections") => act(async () => root.render(<SettingsPanel key={section} {...props} access={iscpSettingsAccess(connection(enabled), "en")} section={section} text={dictionaries.en} language="en" />));
  try {
    await render(false);
    expect(host.querySelectorAll(".settingsConnectionRow")).toHaveLength(8);
    expect(list).not.toHaveBeenCalled(); expect(integrations).not.toHaveBeenCalled();
    await act(async () => button(host, "Email").click());
    expect(host.querySelector(".settingsBack")).not.toBeNull();
    expect(list).not.toHaveBeenCalled();
    await render(true);
    expect(list).toHaveBeenCalledOnce();
    expect(host.textContent).toContain("backend’s dedicated browser");
    expect(login).not.toHaveBeenCalled(); expect(check).not.toHaveBeenCalled();
    const outlook = [...host.querySelectorAll(".emailProviderRow")].find(row => row.textContent?.includes("Outlook"))!;
    await act(async () => outlook.querySelector<HTMLButtonElement>(`button[aria-label="${dictionaries.en.settings.browserEmailOpenLogin}"]`)!.click());
    expect(login).toHaveBeenCalledWith("outlook"); expect(login).toHaveBeenCalledOnce();
    await act(async () => outlook.querySelector<HTMLButtonElement>(`button[aria-label="${dictionaries.en.settings.checkConnection}"]`)!.click());
    expect(check).toHaveBeenCalledWith("outlook"); expect(send).not.toHaveBeenCalled();
    await render(false); expect(host.querySelector(".emailProviderList")).toBeNull();
    await act(async () => host.querySelector<HTMLButtonElement>(".settingsBack")!.click());
    await act(async () => button(host, dictionaries.en.settings.browserControl).click());
    expect(browser).not.toHaveBeenCalled();
  } finally { await act(async () => root.unmount()); }
});

it("loads an integration by the supported collection API", async () => {
  const status = { id: "localmind", credentials: [] } as unknown as Awaited<ReturnType<typeof api.integration>>;
  const list = vi.spyOn(api, "integrations").mockResolvedValue({ integrations: [status] });
  const item = vi.spyOn(api, "integration");
  expect(await iscpSettingsAccess(connection(true), "en").loadIntegrationStatus!("localmind")).toBe(status);
  expect(list).toHaveBeenCalledOnce(); expect(item).not.toHaveBeenCalled();
});

it.each(["models-tools", "permissions"] as const)("preserves actual policy values while disabling unsupported changes in %s", async section => {
  const config = { tool_policy: { risk_counts: {}, definition_count: 0, denied_tools: [], configured_approval_required_tools: [],
    definition_approval_required_tools: [], operator_controls: { web_access: true, workspace_files: true, shell_commands: false,
      file_changes: "ask", external_actions: "block" } } } as unknown as PublicConfig;
  const update = vi.fn(), check = vi.fn();
  const host = document.createElement("div"), root = createRoot(host);
  try {
    await act(async () => root.render(<SettingsPanel {...props} runtimeConfig={config} section={section} onUpdatePolicy={update} onCheckStatus={check}
      access={iscpSettingsAccess(connection(false), "en")} text={dictionaries.en} language="en" />));
    if (section === "models-tools") {
      expect([...host.querySelectorAll('[role="switch"]')].map(item => item.getAttribute('aria-checked'))).toEqual(["true", "true", "false"]);
      for (const item of host.querySelectorAll<HTMLButtonElement>('[role="switch"]')) expect(item.disabled).toBe(true);
    } else {
      expect([...host.querySelectorAll<HTMLSelectElement>('select')].map(item => item.value)).toEqual(["ask", "block"]);
      for (const item of host.querySelectorAll<HTMLSelectElement>('select')) expect(item.disabled).toBe(true);
      expect(host.querySelector<HTMLButtonElement>('button.edit')!.disabled).toBe(true);
    }
    await act(async () => button(host, "Check status").click());
    expect(check).toHaveBeenCalledWith(section); expect(update).not.toHaveBeenCalled();
  } finally { await act(async () => root.unmount()); }
});

it("keeps qualified connector toggles in the original detail while disabling unsupported binding actions", async () => {
  const status = connection(false);
  status.capabilities!.surfaces!.settings_connectors = { enabled: true, reason: "" };
  const connector = { channel: "weixin", provider: "openclaw-weixin-qr", setup_kind: "qr", available: true, enabled: true,
    running: true, state: "active", binding_status: "active", binding_startable: true, supports_multiple_bindings: true, version: 5 } as const;
  const update = vi.fn().mockResolvedValue({ ...connector, enabled: false, version: 6 });
  const start = vi.fn();
  const host = document.createElement("div"), root = createRoot(host);
  try {
    await act(async () => root.render(<SettingsPanel {...props} connectors={[connector]} onUpdateConnector={update} onStartNotificationBinding={start}
      section="connections" access={iscpSettingsAccess(status, "en")} text={dictionaries.en} language="en" />));
    await act(async () => button(host, "Weixin").click());
    const toggle = host.querySelector<HTMLInputElement>('.connectorToggle input')!;
    expect(toggle.disabled).toBe(false);
    expect(host.querySelector<HTMLButtonElement>('.bindingEmpty button')!.disabled).toBe(true);
    await act(async () => toggle.click());
    expect(update).toHaveBeenCalledWith("weixin", false, 5); expect(start).not.toHaveBeenCalled();
  } finally { await act(async () => root.unmount()); }
});

it("retains explicit device authorization review and cancellation within the original settings surface", async () => {
  const remove = vi.fn().mockRejectedValue(new Error("Connection unavailable"));
  window.sparkclawDesktop = { deleteAuthorization: remove } as unknown as NonNullable<Window["sparkclawDesktop"]>;
  const host = document.createElement("div"), root = createRoot(host);
  try {
    await act(async () => root.render(<ISCPDeviceAuthorization connection={connection(true)} language="en" />));
    await act(async () => button(host, "Delete this device authorization").click());
    expect(remove).not.toHaveBeenCalled();
    await act(async () => button(host, "Cancel").click());
    expect(host.querySelector('[role="region"]')).toBeNull(); expect(remove).not.toHaveBeenCalled();
    await act(async () => button(host, "Delete this device authorization").click());
    await act(async () => button(host, "Confirm deletion").click());
    expect(remove).toHaveBeenCalledOnce(); expect(host.querySelector('[role="alert"]')?.textContent).toBe("Connection unavailable");
  } finally { await act(async () => root.unmount()); }
});
