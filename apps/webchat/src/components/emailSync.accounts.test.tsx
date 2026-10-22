// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../api/client";
import type { EmailMailbox, EmailSyncStatus } from "../api/email";
import type { EmailProviderStatus } from "../api/types";
import { dictionaries } from "../i18n";
import { EmailSync } from "./emailSync";
import { MailWorkspaceContext } from "../desktop/MailWorkspaceContext";

(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
const text = dictionaries.en;
const account = (id: string, overrides: Partial<EmailProviderStatus> = {}): EmailProviderStatus => ({
  provider: "gmail", display_name: "Gmail", account: id, account_id: id,
  enabled: true, default: id === "default", state: "ready", version: 4,
  multi_account_available: true, ...overrides
});
const mailbox = (id: string, overrides: Partial<EmailMailbox> = {}): EmailMailbox => ({
  id: `mailbox-${id}`, account_id: id, provider: "gmail", address: `${id}@example.com`,
  version: id === "default" ? 2 : 19, active_binding: true, intake_enabled: true, state: "idle", ...overrides
});

describe("independent receiving accounts", () => {
  let container: HTMLElement;
  let root: ReturnType<typeof createRoot>;
  let value: EmailSyncStatus;
  const refresh = vi.fn(async () => {});
  const error = vi.fn();
  beforeEach(() => {
    container = document.createElement("div"); document.body.append(container); root = createRoot(container);
    value = { version: 1, backlog: 0, pending_count: 0, mailboxes: [mailbox("default"), mailbox("email_account_two")] };
    vi.spyOn(api, "emailAccounts").mockResolvedValue({ accounts: [account("default"), account("email_account_two")] });
    vi.spyOn(api, "updateEmailIntake").mockResolvedValue({ mailbox: mailbox("default", { intake_enabled: false }) });
    vi.spyOn(api, "updateEmailAccountIntake").mockResolvedValue({ mailbox: mailbox("email_account_two", { intake_enabled: false }) });
    vi.spyOn(api, "openEmailLoginBrowser").mockResolvedValue(account("default"));
    vi.spyOn(api, "checkEmailProvider").mockResolvedValue(account("default"));
    vi.spyOn(api, "openEmailAccountLogin").mockResolvedValue(account("email_account_two"));
    vi.spyOn(api, "checkEmailAccount").mockResolvedValue(account("email_account_two"));
    vi.spyOn(api, "syncEmail").mockResolvedValue({ scheduled: false });
    vi.spyOn(api, "emailSyncStatus").mockResolvedValue(value);
    vi.spyOn(api, "addEmailAccount");
    vi.spyOn(api, "updateEmailAccount");
  });
  afterEach(() => { act(() => root.unmount()); container.remove(); vi.restoreAllMocks(); vi.clearAllMocks(); });
  async function render(providers = [account("default")]) {
    await act(async () => root.render(<EmailSync status={value} providers={providers} mailboxId="" text={text} language="en" onRefresh={refresh} onError={error} />));
  }
  function button(label: string, parent: ParentNode = container) {
    const target = [...parent.querySelectorAll<HTMLButtonElement>("button")].find((item) => item.textContent === label);
    expect(target, label).toBeDefined(); return target!;
  }
  async function click(label: string, parent: ParentNode = container) { await act(async () => button(label, parent).click()); }
  function row(address: string) {
    const target = [...container.querySelectorAll<HTMLElement>(".emailAccountRow")].find((item) => item.querySelector("strong")?.textContent === address);
    expect(target, address).toBeDefined(); return target!;
  }
  async function open() { await render(); await click(text.email.receivingSettings); }

  it("preserves default receiving but disables extra-account controls when the connection lacks account management", async () => {
    await act(async () => root.render(<MailWorkspaceContext.Provider value={{ identity: 4, enabled: true, loginEnabled: true, accountsEnabled: false, sendEnabled: true, attachmentsEnabled: false, conversationID: "local", listFiles: async () => [], onFileSaved: async () => {}, download: async () => {} }}><EmailSync status={value} providers={[account("default")]} mailboxId="" text={text} language="en" onRefresh={refresh} onError={error}/></MailWorkspaceContext.Provider>));
    await click(text.email.receivingSettings);
    expect(button(text.email.addAccount).disabled).toBe(true);
    expect(container.textContent).toContain(text.email.accountManagementUnavailable);
    const other = row("email_account_two@example.com");
    expect(button(text.email.accountOpenLogin, other).disabled).toBe(true);
    expect(button(text.email.accountCheck, other).disabled).toBe(true);
    expect(other.querySelector<HTMLInputElement>('input[type="checkbox"]')?.disabled).toBe(true);
    await act(async () => row("default@example.com").querySelector<HTMLInputElement>('input[type="checkbox"]')!.click());
    expect(api.updateEmailIntake).toHaveBeenCalledExactlyOnceWith("gmail", false, 2);
    expect(api.updateEmailAccountIntake).not.toHaveBeenCalled();
  });

  it("matches simultaneous Gmail mailboxes by account and changes only the chosen receiving switch", async () => {
    await open();
    expect(container.querySelectorAll(".emailAccountGroup")).toHaveLength(1);
    expect(container.querySelectorAll(".emailAccountRow")).toHaveLength(2);
    expect(container.textContent).toContain(text.email.multipleAccountsHelp);
    expect(container.textContent).not.toContain(text.email.oneAccount);
    expect(row("default@example.com").querySelector<HTMLInputElement>('input[type="checkbox"]')?.checked).toBe(true);
    await act(async () => row("email_account_two@example.com").querySelector<HTMLInputElement>('input[type="checkbox"]')!.click());
    expect(api.updateEmailAccountIntake).toHaveBeenCalledExactlyOnceWith("email_account_two", false, 19);
    expect(api.updateEmailIntake).not.toHaveBeenCalled();
    value = { ...value, mailboxes: [mailbox("default"), mailbox("email_account_two", { intake_enabled: false })] };
    await render();
    expect(row("default@example.com").querySelector<HTMLInputElement>('input[type="checkbox"]')?.checked).toBe(true);
    expect(row("email_account_two@example.com").querySelector<HTMLInputElement>('input[type="checkbox"]')?.checked).toBe(false);
    await act(async () => row("default@example.com").querySelector<HTMLInputElement>('input[type="checkbox"]')!.click());
    // The higher-version second account must not supply the default's version.
    expect(api.updateEmailIntake).toHaveBeenCalledExactlyOnceWith("gmail", false, 2);
  });

  it("adds an unsigned account once, then enables sign-in and waits for an identity check before receiving", async () => {
    const created = account("email_account_three", { enabled: false, state: "not_configured", version: 1 });
    const enabled = { ...created, enabled: true, version: 2 };
    const login = { ...enabled, state: "login_required" as const, version: 3 };
    const checked = { ...login, state: "ready" as const, account_hint: "third@example.com", version: 4 };
    vi.mocked(api.addEmailAccount).mockResolvedValue(created);
    vi.mocked(api.updateEmailAccount).mockResolvedValue(enabled);
    vi.mocked(api.openEmailAccountLogin).mockResolvedValue(login);
    vi.mocked(api.checkEmailAccount).mockResolvedValue(checked);
    await open();
    await act(async () => { button(text.email.addAccount).click(); button(text.email.addAccount).click(); });
    expect(api.addEmailAccount).toHaveBeenCalledExactlyOnceWith("gmail");
    let added = row("Gmail 3");
    expect(added.textContent).toContain(text.email.accountNotSignedIn);
    expect(added.querySelector<HTMLInputElement>('input[type="checkbox"]')?.disabled).toBe(true);
    expect(button(text.email.accountCheck, added).disabled).toBe(true);
    expect(api.openEmailAccountLogin).not.toHaveBeenCalled();
    expect(api.updateEmailAccountIntake).not.toHaveBeenCalled();
    await click(text.email.accountOpenLogin, added);
    expect(api.updateEmailAccount).toHaveBeenCalledExactlyOnceWith("email_account_three", 1, { enabled: true });
    expect(api.openEmailAccountLogin).toHaveBeenCalledExactlyOnceWith("email_account_three");
    expect(api.openEmailLoginBrowser).not.toHaveBeenCalled();
    added = row("Gmail 3");
    expect(added.querySelector<HTMLInputElement>('input[type="checkbox"]')?.disabled).toBe(true);
    await click(text.email.accountCheck, added);
    expect(api.checkEmailAccount).toHaveBeenCalledExactlyOnceWith("email_account_three");
    const receiving = row("third@example.com").querySelector<HTMLInputElement>('input[type="checkbox"]')!;
    expect(receiving.disabled).toBe(false);
    await act(async () => receiving.click());
    expect(api.updateEmailAccountIntake).toHaveBeenCalledExactlyOnceWith("email_account_three", true, 0);
    expect(api.updateEmailIntake).not.toHaveBeenCalled();
  });

  it("keeps legacy provider sign-in and default receiving available if account discovery is unsupported", async () => {
    vi.mocked(api.emailAccounts).mockRejectedValue(new Error("unsupported"));
    value.mailboxes[0] = { ...mailbox("default"), account_id: undefined };
    await render([account("default", { account_id: undefined, multi_account_available: false })]);
    await click(text.email.receivingSettings);
    expect(button(text.email.addAccount).disabled).toBe(true);
    expect(container.textContent).toContain(text.email.oneAccount);
    const defaultRow = row("default@example.com");
    await click(text.email.accountOpenLogin, defaultRow);
    await click(text.email.accountCheck, defaultRow);
    expect(api.openEmailLoginBrowser).toHaveBeenCalledExactlyOnceWith("gmail");
    expect(api.checkEmailProvider).toHaveBeenCalledExactlyOnceWith("gmail");
    await act(async () => defaultRow.querySelector<HTMLInputElement>('input[type="checkbox"]')!.click());
    expect(api.updateEmailIntake).toHaveBeenCalledExactlyOnceWith("gmail", false, 2);
    expect(api.updateEmailAccountIntake).not.toHaveBeenCalled();
    expect(error).not.toHaveBeenCalled();
  });

  it("does not enable creation or another account's login when isolated sessions are unavailable", async () => {
    vi.mocked(api.emailAccounts).mockResolvedValue({ accounts: [account("default", { multi_account_available: false }), account("email_account_two", { multi_account_available: false })] });
    await open();
    expect(button(text.email.addAccount).disabled).toBe(true);
    const other = row("email_account_two@example.com");
    expect(button(text.email.accountOpenLogin, other).disabled).toBe(true);
    expect(button(text.email.accountCheck, other).disabled).toBe(true);
    // Disabling an already active account remains possible without opening a session.
    await act(async () => other.querySelector<HTMLInputElement>('input[type="checkbox"]')!.click());
    expect(api.updateEmailAccountIntake).toHaveBeenCalledExactlyOnceWith("email_account_two", false, 19);
  });

  it("recovers and refreshes an expired additional account without checking or replacing the default", async () => {
    value.mailboxes[1] = mailbox("email_account_two", { state: "login_required" });
    await open(); await click(text.email.resumeAfterLogin);
    expect(api.checkEmailAccount).toHaveBeenCalledExactlyOnceWith("email_account_two");
    expect(api.updateEmailAccountIntake).toHaveBeenCalledExactlyOnceWith("email_account_two", true, 19);
    expect(api.syncEmail).toHaveBeenCalledExactlyOnceWith("mailbox-email_account_two");
    expect(api.checkEmailProvider).not.toHaveBeenCalled();
    expect(api.updateEmailIntake).not.toHaveBeenCalled();
    expect(error).not.toHaveBeenCalled();
  });
});
