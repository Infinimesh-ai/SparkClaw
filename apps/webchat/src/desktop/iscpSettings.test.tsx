// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { api } from "../api/client";
import { dictionaries } from "../i18n";
import type { DesktopConnectionStatus } from "./types";
import type { PublicConfig } from "../api/types";
import { SettingsPanel, type WorkspaceSettingsSection } from "../components/panels/settings";
import { ISCPDeviceAuthorization, desktopSettingsAccess } from "./iscpSettings";

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
function qualifiedConnection(transport: "lan" | "iscp"): DesktopConnectionStatus {
  const status = connection(true);
  if (transport === "lan") {
    status.backend = { schema_version: 2, origin: "https://backend.invalid", deployment_id: "deployment" };
  } else {
    for (const surface of ["settings_connectors", "settings_bindings", "settings_browser_control", "settings_mcp_access", "settings_credentials"]) {
      status.capabilities!.surfaces![surface] = { enabled: true, reason: "" };
    }
  }
  return status;
}

it("enables the original device panel only after qualified node-managed capability and keeps its exact markup", async () => {
  const qualified = connection(true);
  qualified.capabilities!.surfaces!.settings_devices = { enabled: true, reason: "" };
  expect(desktopSettingsAccess(connection(false), "en").unavailable.clients).toBeDefined();
  expect(desktopSettingsAccess(qualified, "en").unavailable.clients).toBeUndefined();
  expect(desktopSettingsAccess(qualified, "en").clientDeletionAvailable).toBe(false);
  qualified.capabilities!.surfaces!.settings_devices_delete = { enabled: true, reason: "" };
  expect(desktopSettingsAccess(qualified, "en").clientDeletionAvailable).toBe(true);
  const host = document.createElement("div"), root = createRoot(host);
  try {
    await act(async () => root.render(<SettingsPanel {...props} section="devices" text={dictionaries.en} language="en" onLanguageChange={vi.fn()} />));
    const original = host.innerHTML;
    await act(async () => root.render(<SettingsPanel {...props} section="devices" access={desktopSettingsAccess(qualified,"en")} text={dictionaries.en} language="en" onLanguageChange={vi.fn()} />));
    expect(host.innerHTML).toBe(original);
    expect(host.textContent).not.toContain("ISCP:");
  } finally { await act(async () => root.unmount()); }
});

it.each(["en", "zh"] as const)("uses the same original general and appearance markup for both transports in %s", async language => {
  const integrations = vi.spyOn(api, "integrations");
  const host = document.createElement("div"), root = createRoot(host);
  try {
    for (const section of ["general", "appearance"] as const) {
      await act(async () => root.render(<SettingsPanel key={`direct-${section}`} {...props} section={section} text={dictionaries[language]} language={language} onLanguageChange={vi.fn()} />));
      const original = host.innerHTML;
      await act(async () => root.render(<SettingsPanel key={`iscp-${section}`} {...props} section={section} access={desktopSettingsAccess(connection(false), language)} text={dictionaries[language]} language={language} onLanguageChange={vi.fn()} />));
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
  const render = async (enabled: boolean, section: WorkspaceSettingsSection = "connections") => act(async () => root.render(<SettingsPanel key={section} {...props} access={desktopSettingsAccess(connection(enabled), "en")} section={section} text={dictionaries.en} language="en" />));
  try {
    await render(false);
    expect(host.querySelectorAll(".settingsConnectionRow")).toHaveLength(8);
    expect(list).not.toHaveBeenCalled(); expect(integrations).not.toHaveBeenCalled();
    await act(async () => button(host, "Email").click());
    expect(host.querySelector(".settingsBack")).not.toBeNull();
    expect(list).not.toHaveBeenCalled();
    await render(true);
    expect(list).toHaveBeenCalledOnce();
    expect(host.textContent).toContain("Complete sign-in in the backend.");
    expect(login).not.toHaveBeenCalled(); expect(check).not.toHaveBeenCalled();
    const outlook = [...host.querySelectorAll(".emailProviderRow")].find(row => row.textContent?.includes("Outlook"))!;
    const loginButton = outlook.querySelector<HTMLButtonElement>(`button[aria-label="${dictionaries.en.settings.browserEmailOpenLogin}"]`)!;
    expect(loginButton.disabled).toBe(true);
    await act(async () => loginButton.click());
    expect(login).not.toHaveBeenCalled();
    await act(async () => outlook.querySelector<HTMLButtonElement>(`button[aria-label="${dictionaries.en.settings.checkConnection}"]`)!.click());
    expect(check).toHaveBeenCalledWith("outlook"); expect(send).not.toHaveBeenCalled();
    await render(false); expect(host.querySelector(".emailProviderList")).toBeNull();
    await act(async () => host.querySelector<HTMLButtonElement>(".settingsBack")!.click());
    await act(async () => button(host, dictionaries.en.settings.browserControl).click());
    expect(browser).not.toHaveBeenCalled();
  } finally { await act(async () => root.unmount()); }
});

it.each(["lan", "iscp"] as const)("keeps all eight entries and prevents website sign-in over %s", async transport => {
  const providers = [{ provider: "outlook" as const, display_name: "Outlook", enabled: true, default: false,
    account: "default" as const, state: "ready" as const, version: 3 }];
  const list = vi.spyOn(api, "emailProviders").mockResolvedValue({ providers });
  const emailLogin = vi.spyOn(api, "openEmailLoginBrowser");
  const check = vi.spyOn(api, "checkEmailProvider").mockResolvedValue(providers[0]);
  const aiList = vi.spyOn(api, "aiPlatformLogins").mockResolvedValue({ browser_state: "ready", profile_id: "default", providers: [] });
  const aiLogin = vi.spyOn(api, "openAIPlatformLogin");
  const integrations = vi.spyOn(api, "integrations").mockResolvedValue({ integrations: [] });
  const host = document.createElement("div"), root = createRoot(host);
  try {
    await act(async () => root.render(<SettingsPanel {...props} section="connections" access={desktopSettingsAccess(qualifiedConnection(transport), "en")} text={dictionaries.en} language="en" />));
    expect(host.querySelectorAll(".settingsConnectionRow")).toHaveLength(8);
    await act(async () => button(host, "Email").click());
    expect(list).toHaveBeenCalledOnce();
    expect(host.querySelectorAll(".emailProviderRow")).toHaveLength(3);
    for (const login of host.querySelectorAll<HTMLButtonElement>(`button[aria-label="${dictionaries.en.settings.browserEmailOpenLogin}"]`)) {
      expect(login.disabled).toBe(true);
      await act(async () => login.click());
    }
    const outlook = [...host.querySelectorAll(".emailProviderRow")].find(row => row.textContent?.includes("Outlook"))!;
    await act(async () => outlook.querySelector<HTMLButtonElement>(`button[aria-label="${dictionaries.en.settings.checkConnection}"]`)!.click());
    expect(check).toHaveBeenCalledWith("outlook");
    await act(async () => host.querySelector<HTMLButtonElement>(".settingsBack")!.click());
    await act(async () => button(host, dictionaries.en.settings.aiPlatformLogin).click());
    expect(host.querySelectorAll("article")).toHaveLength(4);
    expect(host.textContent).toContain("Complete sign-in in the backend.");
    for (const login of [...host.querySelectorAll<HTMLButtonElement>("button")].filter(item => item.textContent === dictionaries.en.settings.aiPlatformOpenLogin)) {
      expect(login.disabled).toBe(true);
      await act(async () => login.click());
    }
    expect(emailLogin).not.toHaveBeenCalled(); expect(aiLogin).not.toHaveBeenCalled();
    expect(aiList).toHaveBeenCalledTimes(transport === "lan" ? 1 : 0);
    expect(button(host, dictionaries.en.settings.aiPlatformRefresh).disabled).toBe(transport === "iscp");
    await act(async () => host.querySelector<HTMLButtonElement>(".settingsBack")!.click());
    expect(host.querySelectorAll(".settingsConnectionRow")).toHaveLength(8);
    expect(integrations).toHaveBeenCalledOnce();
  } finally { await act(async () => root.unmount()); }
});

it.each(["lan", "iscp"] as const)("enables supported binding surfaces in the original components over %s", async transport => {
  const connector = { channel: "weixin", provider: "openclaw-weixin-qr", setup_kind: "qr", available: true, enabled: true,
    running: true, state: "active", binding_status: "active", binding_startable: true, supports_multiple_bindings: true, version: 5 } as const;
  const mcp = { ...connector, channel: "mcp", provider: "iscp-mcp", setup_kind: "external", binding_startable: false,
    binding_status: "managed_externally", iscp_enabled: true, lan_access_enabled: true } as const;
  const start = vi.fn().mockResolvedValue(undefined);
  const browser = vi.spyOn(api, "browserExtension").mockResolvedValue({ configured: false, state: "not_configured", profile_id: "default", credential_generation: 0, versions: {} });
  const save = vi.spyOn(api, "saveBrowserExtensionToken").mockResolvedValue({ configured: true, state: "ready", profile_id: "default", credential_generation: 1, versions: {} });
  vi.spyOn(api, "integrations").mockResolvedValue({ integrations: [] });
  vi.spyOn(api, "iscpPairingStatus").mockResolvedValue({ enabled: true, ready: true, state: "ready", expected_ticket_type: "iscp.pairing_ticket.v2" });
  vi.spyOn(api, "iscpOnboardings").mockResolvedValue({ onboardings: [] });
  const catalog = vi.spyOn(api, "mcpAccessCatalog").mockResolvedValue({ scope: "conversation", business_tool: "sparkclaw.conversation.send", domain_id: "domain", iscp_enabled: true, lan_access_enabled: true, transport_version: 5, endpoint_path: "/mcp" });
  vi.spyOn(api, "mcpAccessTickets").mockResolvedValue({ tickets: [] });
  vi.spyOn(api, "mcpBindings").mockResolvedValue({ bindings: [] });
  const update = vi.spyOn(api, "updateMCPTransports").mockResolvedValue({ ...mcp, iscp_enabled: false, version: 6 });
  const host = document.createElement("div"), root = createRoot(host);
  try {
    const access = desktopSettingsAccess(qualifiedConnection(transport), "en");
    expect(access.bindingsUnavailable).toBeUndefined();
    expect(access.unavailable.info).toBeUndefined(); expect(access.unavailable.localmind).toBeUndefined();
    await act(async () => root.render(<SettingsPanel {...props} connectors={[connector, mcp]} onStartNotificationBinding={start}
      section="connections" access={access} text={dictionaries.en} language="en" />));
    await act(async () => button(host, "Weixin").click());
    const bind = host.querySelector<HTMLButtonElement>(".bindingEmpty button")!;
    expect(bind.disabled).toBe(false);
    await act(async () => bind.click());
    expect(start).toHaveBeenCalledWith("weixin", "");
    await act(async () => host.querySelector<HTMLButtonElement>(".settingsBack")!.click());
    await act(async () => button(host, dictionaries.en.settings.browserControl).click());
    expect(browser).toHaveBeenCalledOnce();
    const input = host.querySelector<HTMLInputElement>('input[type="password"]')!;
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, "desktop-extension-token");
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await act(async () => host.querySelector<HTMLFormElement>("form")!.requestSubmit());
    expect(save).toHaveBeenCalledWith("desktop-extension-token"); expect(input.value).toBe("");
    await act(async () => host.querySelector<HTMLButtonElement>(".settingsBack")!.click());
    await act(async () => button(host, dictionaries.en.settings.externalMCP).click());
    expect(catalog).toHaveBeenCalledOnce();
    expect(host.textContent).toContain(transport === "lan"
      ? "https://backend.invalid/mcp" : "Use the backend’s LAN HTTPS address with the /mcp path.");
    expect(host.textContent).not.toContain("iscp.invalid/mcp");
    const toggle = host.querySelector<HTMLInputElement>(`input[aria-label="${dictionaries.en.settings.useISCP}"]`)!;
    expect(toggle.disabled).toBe(false);
    await act(async () => toggle.click());
    expect(update).toHaveBeenCalledWith(false, true, 5);
  } finally { await act(async () => root.unmount()); }
});

it("does not load MCP or browser credentials when the connection has not qualified those surfaces", async () => {
  const browser = vi.spyOn(api, "browserExtension");
  const mcp = vi.spyOn(api, "mcpAccessCatalog");
  const host = document.createElement("div"), root = createRoot(host);
  try {
    await act(async () => root.render(<SettingsPanel {...props} section="connections" access={desktopSettingsAccess(connection(false), "en")} text={dictionaries.en} language="en" />));
    for (const title of [dictionaries.en.settings.browserControl, dictionaries.en.settings.externalMCP]) {
      await act(async () => button(host, title).click());
      expect(host.querySelector(".settingsBack")).not.toBeNull();
      expect(host.textContent).toContain("This action is unavailable through the current connection.");
      await act(async () => host.querySelector<HTMLButtonElement>(".settingsBack")!.click());
      expect(host.querySelectorAll(".settingsConnectionRow")).toHaveLength(8);
    }
    expect(browser).not.toHaveBeenCalled(); expect(mcp).not.toHaveBeenCalled();
  } finally { await act(async () => root.unmount()); }
});

it("loads an integration by the supported collection API", async () => {
  const status = { id: "localmind", credentials: [] } as unknown as Awaited<ReturnType<typeof api.integration>>;
  const list = vi.spyOn(api, "integrations").mockResolvedValue({ integrations: [status] });
  const item = vi.spyOn(api, "integration");
  expect(await desktopSettingsAccess(connection(true), "en").loadIntegrationStatus!("localmind")).toBe(status);
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
      access={desktopSettingsAccess(connection(false), "en")} text={dictionaries.en} language="en" />));
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
      section="connections" access={desktopSettingsAccess(status, "en")} text={dictionaries.en} language="en" />));
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
