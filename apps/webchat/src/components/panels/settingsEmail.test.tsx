// @vitest-environment jsdom

import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { api, APIError } from "../../api/client";
import type { EmailProviderStatus } from "../../api/types";
import { dictionaries } from "../../i18n";
import { BrowserEmailSettings } from "./settingsEmail";

const gmail: EmailProviderStatus = {
  provider: "gmail", display_name: "Gmail", enabled: true, default: true, account: "default",
  account_hint: "a***@gmail.com", state: "ready", version: 3
};
const outlook: EmailProviderStatus = {
  provider: "outlook", display_name: "Outlook", enabled: true, default: false, account: "default",
  state: "login_required", version: 1
};
const qq: EmailProviderStatus = {
  provider: "qq_mail", display_name: "QQ Mail", enabled: false, default: false, account: "default",
  state: "not_configured", version: 0
};

describe("Browser email settings", () => {
  afterEach(() => vi.restoreAllMocks());

  it("loads providers and exposes separate login and login-check actions", async () => {
    let resolveProviders: (value: { providers: EmailProviderStatus[] }) => void = () => {};
    vi.spyOn(api, "emailProviders").mockReturnValue(new Promise((resolve) => { resolveProviders = resolve; }));
    const openLogin = vi.spyOn(api, "openEmailLoginBrowser").mockResolvedValue({ ...outlook, version: 2 });
    const check = vi.spyOn(api, "checkEmailProvider").mockResolvedValue({ ...outlook, state: "ready", account_hint: "o***@outlook.com", version: 3 });
    const { container, root } = await renderEmailSettings();

    expect(container.querySelectorAll(".emailProviderRow")).toHaveLength(3);
    expect(container.textContent).toContain(dictionaries.en.settings.browserEmailLoading);
    await act(async () => resolveProviders({ providers: [gmail, outlook, qq] }));
    expect(container.textContent).toContain("a***@gmail.com");

    // Login is gated on the owner's enable toggle, like the login check.
    expect(loginButton(providerRow(container, "QQ Mail")).disabled).toBe(true);
    const row = providerRow(container, "Outlook");
    await act(async () => loginButton(row).click());
    expect(openLogin).toHaveBeenCalledWith("outlook");
    expect(container.textContent).toContain(dictionaries.en.settings.browserEmailLoginOpenedDetail);

    await act(async () => checkButton(row).click());
    expect(check).toHaveBeenCalledWith("outlook");
    expect(container.textContent).toContain("o***@outlook.com");
    await act(async () => root.unmount());
  });

  it("explains a pending browser cleanup instead of a generic provider outage", async () => {
    vi.spyOn(api, "emailProviders").mockResolvedValue({ providers: [
      { ...gmail, state: "temporarily_unavailable", error_code: "email_browser_cleanup_pending" }, outlook, qq
    ] });
    vi.spyOn(api, "checkEmailProvider").mockRejectedValue(new APIError(503,
      "SparkClaw browser has not yet confirmed that an earlier email task page closed", "email_browser_cleanup_pending", true));
    const { container, root } = await renderEmailSettings();
    await flushEffects();

    const row = providerRow(container, "Gmail");
    expect(row.textContent).toContain(dictionaries.en.settings.integrationTemporarilyUnavailable);
    await act(async () => checkButton(row).click());
    expect(container.textContent).toContain(dictionaries.en.settings.browserEmailActionFailed);
    expect(container.textContent).toContain(dictionaries.en.settings.browserEmailCleanupPending);
    // Other failures keep the Gateway's message.
    vi.spyOn(api, "checkEmailProvider").mockRejectedValue(new APIError(503, "Email provider is unavailable", "email_provider_unavailable", true));
    await act(async () => checkButton(row).click());
    expect(container.textContent).toContain("Email provider is unavailable");
    expect(container.textContent).not.toContain(dictionaries.en.settings.browserEmailCleanupPending);
    await act(async () => root.unmount());
  });

  it("uses versioned writes for provider enablement and the single default", async () => {
    vi.spyOn(api, "emailProviders")
      .mockResolvedValueOnce({ providers: [gmail, outlook, qq] })
      .mockResolvedValueOnce({ providers: [
        { ...gmail, default: false, version: 4 },
        { ...outlook, default: true, version: 2 },
        qq
      ] });
    const update = vi.spyOn(api, "updateEmailProvider").mockImplementation(async (provider, version, changes) => {
      if (provider === "qq_mail") return { ...qq, enabled: true, version: version + 1 };
      return { ...outlook, default: Boolean(changes.default), version: version + 1 };
    });
    const { container, root } = await renderEmailSettings();
    await flushEffects();

    const qqToggle = providerRow(container, "QQ Mail").querySelector('input[type="checkbox"]') as HTMLInputElement;
    await act(async () => qqToggle.click());
    expect(update).toHaveBeenCalledWith("qq_mail", 0, { enabled: true });
    expect(qqToggle.checked).toBe(true);

    const outlookRow = providerRow(container, "Outlook");
    const defaultButton = outlookRow.querySelector(`button[title="${dictionaries.en.settings.browserEmailSetDefault}"]`) as HTMLButtonElement;
    await act(async () => defaultButton.click());
    expect(update).toHaveBeenNthCalledWith(2, "outlook", 1, { default: true });
    expect(update).toHaveBeenCalledTimes(2);
    expect(api.emailProviders).toHaveBeenCalledTimes(2);
    expect(outlookRow.classList.contains("selected")).toBe(true);
    await act(async () => root.unmount());
  });

  it.each(["en", "zh"] as const)("shows sync health only after checking and preserves provider controls in %s", async language => {
    const copy = dictionaries[language];
    vi.spyOn(api, "emailProviders").mockResolvedValue({ providers: [gmail, outlook, qq] });
    const check = vi.spyOn(api, "checkEmailProvider");
    const container = document.createElement("div"), root = createRoot(container);
    await act(async () => root.render(<BrowserEmailSettings text={copy} />));
    const controls = container.querySelector(".emailProviderList")!.innerHTML;
    expect(container.querySelector(".credentialValidationFeedback")).toBeNull();
    const cases = [
      ["stalled", "email_sync_stalled", copy.settings.browserEmailSyncStalled, "error"],
      ["paused", undefined, copy.settings.browserEmailSyncPaused, "success"],
      ["not_enabled", undefined, copy.settings.browserEmailSyncNotEnabled, "success"],
      ["starting", undefined, copy.settings.browserEmailSyncStarting, "success"],
      ["needs_attention", "email_interval_overflow", copy.settings.browserEmailSyncOverflow, "error"],
      ["needs_attention", "email_incremental_unqualified", copy.settings.browserEmailSyncUnqualified, "error"],
      ["unavailable", undefined, copy.settings.browserEmailSyncUnavailable, "error"],
      ["healthy", undefined, copy.settings.browserEmailSyncHealthy, "success"],
    ] as const;
    for (const [state, code, message, tone] of cases) {
      check.mockResolvedValue({ ...gmail, sync_health: { state, code, checked_at: "2026-10-21T12:00:00Z", stall_after_seconds: 900 } });
      await act(async () => providerRow(container, "Gmail").querySelector<HTMLButtonElement>(`button[title="${copy.settings.checkConnection}"]`)!.click());
      expect(container.querySelector(`.credentialValidationFeedback.${tone}`)?.textContent).toContain(message);
      expect(container.querySelector(".emailProviderList")!.innerHTML).toBe(controls);
    }
    // A later failed provider probe takes priority over the previous health.
    check.mockRejectedValue(new APIError(409, "Please sign in again", "email_login_required", false));
    await act(async () => providerRow(container, "Gmail").querySelector<HTMLButtonElement>(`button[title="${copy.settings.checkConnection}"]`)!.click());
    expect(container.textContent).toContain("Please sign in again");
    expect(container.textContent).not.toContain(copy.settings.browserEmailSyncHealthy);
    await act(async () => root.unmount());
  });

  it("refreshes persisted provider state after an action failure", async () => {
    const failed: EmailProviderStatus = { ...gmail, state: "needs_attention", error_code: "email_page_contract_changed", version: 4 };
    vi.spyOn(api, "emailProviders")
      .mockResolvedValueOnce({ providers: [gmail, outlook, qq] })
      .mockResolvedValueOnce({ providers: [failed, outlook, qq] });
    vi.spyOn(api, "checkEmailProvider").mockRejectedValue(new Error("provider page changed"));
    const { container, root } = await renderEmailSettings();
    await flushEffects();

    await act(async () => checkButton(providerRow(container, "Gmail")).click());
    expect(api.emailProviders).toHaveBeenCalledTimes(2);
    expect(container.textContent).toContain("provider page changed");
    expect(providerRow(container, "Gmail").textContent).toContain(dictionaries.en.settings.integrationNeedsAttention);
    await act(async () => root.unmount());
  });

  it("keeps provider controls visible after status loading fails and retries", async () => {
    vi.spyOn(api, "emailProviders")
      .mockRejectedValueOnce(new Error("offline"))
      .mockResolvedValueOnce({ providers: [gmail, outlook, qq] });
    const { container, root } = await renderEmailSettings();
    await flushEffects();
    expect(container.querySelectorAll(".emailProviderRow")).toHaveLength(3);
    expect(loginButton(providerRow(container, "Gmail")).disabled).toBe(true);
    expect(container.textContent).toContain("offline");
    const retry = container.querySelector(".integrationStatusBar button") as HTMLButtonElement;
    await act(async () => retry.click());
    expect(api.emailProviders).toHaveBeenCalledTimes(2);
    expect(loginButton(providerRow(container, "Gmail")).disabled).toBe(false);
    await act(async () => root.unmount());
  });
});

async function renderEmailSettings() {
  const container = document.createElement("div");
  const root = createRoot(container);
  await act(async () => root.render(<BrowserEmailSettings text={dictionaries.en} />));
  return { container, root };
}

async function flushEffects() {
  await act(async () => { await Promise.resolve(); });
}

function providerRow(container: HTMLElement, name: string) {
  const row = Array.from(container.querySelectorAll(".emailProviderRow")).find((item) => item.textContent?.includes(name));
  if (!row) throw new Error(`provider row not found: ${name}`);
  return row as HTMLElement;
}

function loginButton(row: HTMLElement) {
  return row.querySelector(`button[title="${dictionaries.en.settings.browserEmailOpenLogin}"]`) as HTMLButtonElement;
}

function checkButton(row: HTMLElement) {
  return row.querySelector(`button[title="${dictionaries.en.settings.checkConnection}"]`) as HTMLButtonElement;
}
