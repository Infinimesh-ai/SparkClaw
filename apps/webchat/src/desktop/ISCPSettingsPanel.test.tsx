// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import { api } from "../api/client";
import { dictionaries } from "../i18n";
import type { DesktopConnectionStatus } from "./types";
import { ISCPSettingsPanel } from "./ISCPSettingsPanel";

afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); });
const connection = (enabled: boolean): DesktopConnectionStatus => ({
  schema_version: 1, state: "connected", client_id: "client",
  backend: {schema_version:3, transport:"iscp", origin:"https://iscp.invalid", deployment_id:"deployment"},
  capabilities: {operations:[],files:false,mail:false,browser:false,speech:false,approvals:false,settings:enabled,
    surfaces: {mail_settings:{enabled,reason:enabled ? "" : "qualification_required"}}},
});

it("mounts the qualified provider controls without enabling unrelated connectors or mail sending", async () => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  const providers = [
    {provider:"outlook" as const,display_name:"Outlook",enabled:true,default:false,account:"default" as const,state:"ready" as const,version:3},
    {provider:"qq_mail" as const,display_name:"QQ Mail",enabled:true,default:false,account:"default" as const,state:"login_required" as const,version:2},
  ];
  const list = vi.spyOn(api,"emailProviders").mockResolvedValue({providers});
  const connectors = vi.spyOn(api,"connectors");
  const login = vi.spyOn(api,"openEmailLoginBrowser").mockResolvedValue({...providers[0],state:"login_required"});
  const check = vi.spyOn(api,"checkEmailProvider").mockResolvedValue(providers[0]);
  const send = vi.spyOn(api,"sendEmailDraft");
  const host = document.createElement("div"); const root = createRoot(host);
  const render = async (enabled: boolean) => act(async () => root.render(<ISCPSettingsPanel connection={connection(enabled)} tab="connections" text={dictionaries.en} language="en"/>));
  try {
    await render(false); expect(list).not.toHaveBeenCalled();
    await render(true); expect(list).toHaveBeenCalledOnce(); expect(connectors).not.toHaveBeenCalled();
    expect(host.textContent).toContain("backend’s dedicated browser");
    expect(login).not.toHaveBeenCalled(); expect(check).not.toHaveBeenCalled();
    const outlook = [...host.querySelectorAll(".emailProviderRow")].find(row=>row.textContent?.includes("Outlook"))!;
    await act(async()=>outlook.querySelector<HTMLButtonElement>(`button[aria-label="${dictionaries.en.settings.browserEmailOpenLogin}"]`)!.click());
    expect(login).toHaveBeenCalledWith("outlook"); expect(login).toHaveBeenCalledOnce();
    await act(async()=>outlook.querySelector<HTMLButtonElement>(`button[aria-label="${dictionaries.en.settings.checkConnection}"]`)!.click());
    expect(check).toHaveBeenCalledWith("outlook"); expect(send).not.toHaveBeenCalled();
    await render(false); expect(host.querySelector(".emailProviderList")).toBeNull();
  } finally { await act(async()=>root.unmount()); }
});
