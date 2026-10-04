// @vitest-environment jsdom

import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, APIError } from "../api/client";
import type { Client, IssuedClientCredential } from "../api/types";
import { dictionaries, type Language } from "../i18n";
import { InspectorColumn, type PanelTab } from "./inspector";
import { PairedClientsSettings } from "./panels/settingsClients";
import { WorkspaceSettingsSidebar } from "./settingsSidebar";

const current: Client = { id: "current-device", name: "Linux", created_at: "2026-09-30T02:00:00Z", last_seen_at: "2026-09-30T03:00:00Z" };
const issuedClient: Client = { id: "new-device", name: "My Mac", created_at: "2026-09-30T04:00:00Z" };
const issued: IssuedClientCredential = { client: issuedClient, token: "synthetic-one-time-token" };
const refreshGlobal = vi.fn(async () => { throw new Error("global refresh must not delay credential delivery"); });
const endLogin = vi.fn(async () => {});

function SettingsRoute({ language = "en" }: { language?: Language }) {
  const [tab, setTab] = useState<PanelTab>("settings");
  const text = dictionaries[language];
  return <>
    <WorkspaceSettingsSidebar text={text} language={language} tab={tab} pendingApprovalCount={0} pendingCandidateCount={0} onTabChange={setTab} onBack={() => {}} />
    <InspectorColumn settingsPage showTabs={false} tab={tab} onTabChange={setTab} text={text} language={language}
      pendingApprovalCount={0} pendingCandidateCount={0} toolCalls={[]} approvals={[]} candidates={[]} memories={[]}
      traceRun={null} traceList={[]} traceLoading={false} ready={null} modelCalls={[]} auditEvents={[]} artifacts={[]} episodes={[]}
      evalRuns={[]} runtimeConfig={null} ownerProfile={null} clients={[]} connectors={[]} notificationBindings={[]}
      onOpenTrace={() => {}} setError={() => {}} surfaceError={() => {}} refreshGlobal={refreshGlobal}
      refreshActiveSession={async () => {}} setEvalRuns={() => {}} setNotificationBindings={() => {}} setConnectors={() => {}}
      setRuntimeConfig={() => {}} setOwnerProfile={() => {}} onLanguageChange={() => {}} onOpenSchedules={() => {}}
      onCurrentClientRevoked={endLogin} onLogout={endLogin} />
  </>;
}

function deferred<T>() {
  let resolve!: (result: T) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<T>((done, fail) => { resolve = done; reject = fail; });
  return { promise, resolve, reject };
}

function button(host: HTMLElement, label: string) {
  const found = [...host.querySelectorAll<HTMLButtonElement>("button")].find((element) => element.textContent?.trim() === label);
  if (!found) throw new Error(`Missing button: ${label}`);
  return found;
}

async function inputValue(input: HTMLInputElement, value: string) {
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")?.set?.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

const roots: Root[] = [];
async function render(element: React.ReactNode) {
  const host = document.createElement("div");
  const root = createRoot(host);
  roots.push(root);
  await act(async () => root.render(element));
  return { host, root };
}

async function visitDevices(host: HTMLElement, language: Language = "en") {
  const label = language === "zh" ? "设备与凭据" : "Devices & credentials";
  await inputValue(host.querySelector('input[type="search"]') as HTMLInputElement, language === "zh" ? "凭据" : "credentials");
  expect(host.querySelectorAll(".settingsPageNavigation button")).toHaveLength(1);
  await act(async () => button(host, label).click());
}

beforeEach(() => {
  delete window.sparkclawDesktop;
  const storage = new Map<string, string>();
  vi.stubGlobal("localStorage", {
    getItem: (key: string) => storage.get(key) ?? null,
    setItem: (key: string, value: string) => storage.set(key, value),
    removeItem: (key: string) => storage.delete(key),
    get length() { return storage.size; }
  });
  refreshGlobal.mockClear();
  endLogin.mockClear();
  vi.spyOn(api, "integrations").mockResolvedValue({ integrations: [] });
  vi.spyOn(api, "workbenchIdentity").mockResolvedValue({ deployment_id: "deployment", owner_id: "owner", client_id: current.id });
  vi.spyOn(api, "clients").mockResolvedValue({ clients: [current] });
  vi.spyOn(api, "issueClient").mockResolvedValue(issued);
  vi.spyOn(api, "revokeClient").mockResolvedValue({ ...current, revoked_at: "2026-09-30T05:00:00Z" });
});

afterEach(async () => {
  delete window.sparkclawDesktop;
  for (const root of roots.splice(0)) await act(async () => root.unmount());
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("device credentials through the production settings route", () => {
  it.each(["en", "zh"] as const)("finds the section by search and loads confirmed device metadata in %s", async (language) => {
    const { host } = await render(<SettingsRoute language={language} />);
    await visitDevices(host, language);
    const text = dictionaries[language];
    expect(host.querySelector('[aria-current="page"]')?.textContent).toContain(text.settings.clients);
    expect(host.textContent).toContain(text.settings.currentClient);
    expect(host.textContent).toContain(current.id);
    expect(host.textContent).toContain(text.settings.clientCreated);
    expect(host.textContent).toContain(text.settings.seen);
    expect(api.clients).toHaveBeenCalledOnce();
    expect(api.workbenchIdentity).toHaveBeenCalledOnce();
    expect(host.querySelector(".clientIssuance")).not.toBeNull();
  });

  it("delivers a credential despite refresh failure, supports manual/click copy, and hides it explicitly", async () => {
    const { host } = await render(<SettingsRoute />);
    await visitDevices(host);
    vi.mocked(api.clients).mockRejectedValueOnce(new Error("sensitive diagnostic must stay private"));
    await inputValue(host.querySelector(".clientIssuance input") as HTMLInputElement, issuedClient.name);
    await act(async () => button(host, dictionaries.en.settings.issueClient).click());
    expect(host.textContent).toContain(issued.token);
    expect(host.textContent).toContain(dictionaries.en.settings.clientsLoadFailed);
    expect(host.textContent).not.toContain("sensitive diagnostic");
    expect(host.textContent).not.toContain(dictionaries.en.settings.noClients);
    expect(refreshGlobal).not.toHaveBeenCalled();
    expect(window.localStorage.length).toBe(0);

    const writeText = vi.fn().mockRejectedValueOnce(new Error("clipboard denied")).mockResolvedValueOnce(undefined);
    Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
    await act(async () => button(host, dictionaries.en.common.copy).click());
    expect(host.textContent).toContain(dictionaries.en.settings.clientTokenCopyFailed);
    expect(host.querySelector(".issuedClientCredential code")?.getAttribute("tabindex")).toBe("0");
    await act(async () => button(host, dictionaries.en.common.copy).click());
    expect(writeText).toHaveBeenNthCalledWith(2, issued.token);
    expect(host.textContent).toContain(dictionaries.en.settings.clientTokenCopied);
    await act(async () => button(host, dictionaries.en.settings.hideClientToken).click());
    expect(host.textContent).not.toContain(issued.token);
    expect(host.querySelector(".issuedClientCredential")).toBeNull();
  });

  it("packages a newly issued token as one desktop connection credential", async () => {
    const connectionCredential = vi.fn(async (token: string) => `sparkclaw-connect-v1.${token}-bundle`);
    window.sparkclawDesktop = { runtimeKind: "electron", capabilityVersion: 1, connectionCredential,
      loginStartup: async () => ({ supported: false, enabled: false }) } as unknown as NonNullable<typeof window.sparkclawDesktop>;
    const { host } = await render(<SettingsRoute />);
    await visitDevices(host);
    await act(async () => button(host, dictionaries.en.settings.issueClient).click());
    const bundled = `sparkclaw-connect-v1.${issued.token}-bundle`;
    expect(connectionCredential).toHaveBeenCalledWith(issued.token);
    expect(host.querySelector(".issuedClientCredential code")?.textContent).toBe(bundled);
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
    await act(async () => button(host, dictionaries.en.common.copy).click());
    expect(writeText).toHaveBeenCalledWith(bundled);
  });

  it("preserves one request key and name after a lost response and blocks concurrent issuance", async () => {
    const first = deferred<IssuedClientCredential>();
    vi.mocked(api.issueClient).mockReturnValueOnce(first.promise).mockResolvedValueOnce(issued);
    const { host } = await render(<SettingsRoute />);
    await visitDevices(host);
    const input = host.querySelector(".clientIssuance input") as HTMLInputElement;
    await inputValue(input, "  My Mac  ");
    await act(async () => {
      const issue = button(host, dictionaries.en.settings.issueClient);
      issue.click(); issue.click();
    });
    expect(api.issueClient).toHaveBeenCalledOnce();
    expect(input.disabled).toBe(true);
    // A transport failure may occur after the backend accepted this request.
    const fail = vi.mocked(api.issueClient).mock.calls[0];
    await act(async () => first.reject(new TypeError("connection lost")));
    expect(host.textContent).toContain(dictionaries.en.settings.clientIssuanceUnknown);
    expect(input.disabled).toBe(true);
    await act(async () => button(host, dictionaries.en.settings.retryClientIssuance).click());
    expect(api.issueClient).toHaveBeenNthCalledWith(2, fail[0], fail[1]);
    expect(fail[0]).toBe("My Mac");
    expect(host.textContent).toContain(issued.token);
  });

  it("does not describe failed or pending list loads as empty and retries safely", async () => {
    const load = deferred<{ clients: Client[] }>();
    vi.mocked(api.clients).mockReturnValueOnce(load.promise).mockResolvedValueOnce({ clients: [] });
    const { host } = await render(<SettingsRoute />);
    await visitDevices(host);
    expect(host.textContent).toContain(dictionaries.en.settings.loadingClients);
    expect(host.textContent).not.toContain(dictionaries.en.settings.noClients);
    await act(async () => load.reject(new Error("offline")));
    expect(host.textContent).toContain(dictionaries.en.settings.clientsLoadFailed);
    expect(host.textContent).not.toContain(dictionaries.en.settings.noClients);
    await act(async () => button(host, dictionaries.en.settings.retryClients).click());
    expect(host.textContent).toContain(dictionaries.en.settings.noClients);
    expect(host.textContent).not.toContain(dictionaries.en.settings.clientsLoadFailed);
  });

  it("leaves the one-time credential behind when navigating away and back", async () => {
    const { host } = await render(<SettingsRoute />);
    await visitDevices(host);
    await act(async () => button(host, dictionaries.en.settings.issueClient).click());
    expect(host.textContent).toContain(issued.token);
    const search = host.querySelector('input[type="search"]') as HTMLInputElement;
    await inputValue(search, "appearance");
    await act(async () => button(host, "Appearance").click());
    expect(host.textContent).not.toContain(issued.token);
    await visitDevices(host);
    expect(host.textContent).not.toContain(issued.token);
    expect(api.issueClient).toHaveBeenCalledOnce();
    expect(window.localStorage.length).toBe(0);
  });

  it("requires revoking the exact unrecoverable device before issuing with a new key", async () => {
    const unrelated = { ...issuedClient, id: "other-same-name-device" };
    vi.mocked(api.clients).mockResolvedValue({ clients: [current, unrelated, issuedClient] });
    vi.mocked(api.issueClient).mockRejectedValueOnce(new APIError(409, "private internal conflict", "CLIENT_CREDENTIAL_UNRECOVERABLE", false, { client_id: issuedClient.id }));
    const { host } = await render(<SettingsRoute />);
    await visitDevices(host);
    await inputValue(host.querySelector(".clientIssuance input") as HTMLInputElement, issuedClient.name);
    await act(async () => button(host, dictionaries.en.settings.issueClient).click());
    expect(host.textContent).toContain(dictionaries.en.settings.clientCredentialUnrecoverable);
    expect(host.textContent).not.toContain("private internal conflict");
    const originalKey = vi.mocked(api.issueClient).mock.calls[0][1];
    expect(button(host, dictionaries.en.settings.retryClientIssuance).disabled).toBe(true);
    const row = (id: string) => [...host.querySelectorAll<HTMLElement>(".clientItem")].find((item) => item.textContent?.includes(id))!;
    await act(async () => row(unrelated.id).querySelector<HTMLButtonElement>("button")!.click());
    expect(button(host, dictionaries.en.settings.retryClientIssuance).disabled).toBe(true);
    vi.mocked(api.clients).mockResolvedValue({ clients: [current, { ...issuedClient, revoked_at: "2026-09-30T05:00:00Z" }] });
    await act(async () => row(issuedClient.id).querySelector<HTMLButtonElement>("button")!.click());
    await act(async () => button(host, dictionaries.en.settings.issueClient).click());
    expect(api.issueClient).toHaveBeenCalledTimes(2);
    expect(vi.mocked(api.issueClient).mock.calls[1][1]).not.toBe(originalKey);
    expect(vi.mocked(api.issueClient).mock.calls[1][0]).toBe(issuedClient.name);
  });

  it("confirms current-device revocation, ends login before any refresh, and keeps failures visible", async () => {
    const confirm = vi.spyOn(window, "confirm").mockReturnValueOnce(false).mockReturnValue(true);
    const { host } = await render(<SettingsRoute />);
    await visitDevices(host);
    const revoke = host.querySelector<HTMLButtonElement>('.clientItem button')!;
    await act(async () => revoke.click());
    expect(api.revokeClient).not.toHaveBeenCalled();
    expect(confirm).toHaveBeenCalledWith(dictionaries.en.settings.revokeCurrentClientConfirm);
    vi.mocked(api.revokeClient).mockRejectedValueOnce(new Error("private revoke diagnostic"));
    await act(async () => revoke.click());
    expect(host.textContent).toContain(dictionaries.en.settings.clientRevokeFailed);
    expect(host.textContent).not.toContain("private revoke diagnostic");
    expect(endLogin).not.toHaveBeenCalled();
    const order: string[] = [];
    vi.mocked(api.revokeClient).mockImplementationOnce(async () => { order.push("revoke"); return current; });
    endLogin.mockImplementationOnce(async () => { order.push("stop-and-logout"); });
    await act(async () => revoke.click());
    expect(order).toEqual(["revoke", "stop-and-logout"]);
    expect(api.clients).toHaveBeenCalledOnce();
    expect(refreshGlobal).not.toHaveBeenCalled();
  });

  it("exposes an explicit sign-out action using the same protected-channel callback", async () => {
    const { host } = await render(<SettingsRoute />);
    await visitDevices(host);
    await act(async () => button(host, dictionaries.en.settings.logoutClient).click());
    expect(endLogin).toHaveBeenCalledOnce();
    expect(api.revokeClient).not.toHaveBeenCalled();
  });

  it("filters unavailable desktop tabs while preserving direct device navigation", async () => {
    const change = vi.fn();
    const { host } = await render(<WorkspaceSettingsSidebar text={dictionaries.en} language="en" tab="devices"
      availableTabs={["devices", "appearance"]} pendingApprovalCount={0} pendingCandidateCount={0} onTabChange={change} onBack={() => {}} />);
    expect(host.querySelectorAll(".settingsPageNavigation button")).toHaveLength(2);
    expect(host.textContent).not.toContain("General");
    await act(async () => button(host, "Devices & credentials").click());
    expect(change).toHaveBeenCalledWith("devices");
  });

  it("ignores a late response after leaving the view and never persists the token", async () => {
    const response = deferred<IssuedClientCredential>();
    const issue = vi.fn(() => response.promise);
    const { host, root } = await render(<PairedClientsSettings clients={[]} text={dictionaries.en} language="en" onIssueClient={issue} onRevokeClient={async () => {}} />);
    await act(async () => button(host, dictionaries.en.settings.issueClient).click());
    await act(async () => root.render(<div>Other section</div>));
    await act(async () => response.resolve(issued));
    expect(host.textContent).toBe("Other section");
    expect(window.localStorage.length).toBe(0);
  });

  it("refuses revocation while current-device identity is unknown", async () => {
    const revoke = vi.fn(async () => {});
    const { host } = await render(<PairedClientsSettings clients={[current]} text={dictionaries.en} language="en" onRevokeClient={revoke} />);
    expect(host.textContent).toContain(dictionaries.en.settings.clientsIdentityUnavailable);
    expect(host.querySelector<HTMLButtonElement>(".clientItem button")!.disabled).toBe(true);
    await act(async () => host.querySelector<HTMLButtonElement>(".clientItem button")!.click());
    expect(revoke).not.toHaveBeenCalled();
  });

  it("does not revive a revoked issuance request and uses a new key for the replacement", async () => {
    vi.mocked(api.issueClient).mockRejectedValueOnce(new APIError(409, "revoked", "CLIENT_REVOKED"));
    const { host } = await render(<SettingsRoute />);
    await visitDevices(host);
    await act(async () => button(host, dictionaries.en.settings.issueClient).click());
    expect(host.textContent).toContain(dictionaries.en.settings.clientAlreadyRevoked);
    const originalKey = vi.mocked(api.issueClient).mock.calls[0][1];
    await act(async () => button(host, dictionaries.en.settings.issueClient).click());
    expect(vi.mocked(api.issueClient).mock.calls[1][1]).not.toBe(originalKey);
    expect(host.textContent).toContain(issued.token);
  });
});
