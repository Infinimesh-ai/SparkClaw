// @vitest-environment jsdom
import { act, useState } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../api/client";
import type { EmailSyncStatus, EmailSyncWarning } from "../api/email";
import type { EmailProviderStatus } from "../api/types";
import { dictionaries } from "../i18n";
import { emailErrorLabel } from "./emailCommon";
import { MailWorkspaceContext } from "../desktop/MailWorkspaceContext";
import { EmailSync } from "./emailSync";

(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const text = dictionaries.en;
const warning: EmailSyncWarning = {
  id: "warning-qq", mailbox_id: "mailbox-qq", warning_ref: "local-warning-1",
  stage: "original", scope: "mail_specific", error_code: "email_source_invalid",
  state: "suppressed", attempt_count: 2,
  first_failed_at: "2026-09-15T01:00:00Z", last_attempt_at: "2026-09-15T02:00:00Z",
};
const providers: EmailProviderStatus[] = [{
  provider: "qq_mail", display_name: "QQ Mail", enabled: true, default: true,
  account: "default", state: "ready", version: 1,
}];
function status(): EmailSyncStatus {
  return { version: 1, backlog: 0, pending_count: 0, mailboxes: [
    { id: "mailbox-qq", provider: "qq_mail", address: "qq@example.com", version: 3,
      active_binding: true, intake_enabled: true, state: "idle",
      suppressed_mail_count: 2, unacknowledged_warning_count: 1 },
    { id: "mailbox-gmail", provider: "gmail", address: "gmail@example.com", version: 2,
      active_binding: true, intake_enabled: false, state: "idle",
      refresh_pending: false,
      coverage_gap_count: 2, unacknowledged_warning_count: 2 },
  ] };
}

describe("email sync persistent warnings and Refresh", () => {
  let container: HTMLElement;
  let root: ReturnType<typeof createRoot>;
  const refresh = vi.fn(async () => {});
  const error = vi.fn();
  beforeEach(() => {
    const stored = new Map<string, string>();
    Object.defineProperty(window, "localStorage", { configurable: true, value: {
      getItem: (key: string) => stored.get(key) ?? null,
      setItem: (key: string, value: string) => stored.set(key, value),
      clear: () => stored.clear(),
    } });
    window.localStorage.clear(); window.dispatchEvent(new StorageEvent("storage", { key: null }));
    container = document.createElement("div"); document.body.append(container);
    root = createRoot(container);
    vi.spyOn(api, "syncEmail").mockResolvedValue({ scheduled: true, refresh_requests: [{ mailbox_id: "mailbox-gmail", refresh_request_id: "refresh-1" }] });
    const serverPending = status(); Object.assign(serverPending.mailboxes[1], { refresh_pending: true, refresh_request_id: "refresh-1" });
    vi.spyOn(api, "emailSyncStatus").mockResolvedValue(serverPending);
    vi.spyOn(api, "emailSyncWarnings").mockResolvedValue({ items: [{ ...warning }] });
    vi.spyOn(api, "acknowledgeEmailSyncWarning").mockResolvedValue({ ...warning, acknowledged_at: "2026-09-16T00:00:00Z" });
    vi.spyOn(api, "updateEmailIntake");
    vi.spyOn(api, "emailAccounts").mockRejectedValue(new Error("Legacy account listing unavailable"));
    vi.spyOn(api, "cleanupEmailSource").mockResolvedValue({ scope: "all", purged: 2, freed_bytes: 1024, partial: false });
  });
  afterEach(() => {
    act(() => root.unmount()); container.remove(); vi.restoreAllMocks(); vi.clearAllMocks(); vi.useRealTimers();
  });
  // Match EmailPopup's error callback + safe user-facing label contract without
  // bringing unrelated conversation, body, and provider polling into this test.
  function Harness({ value, mailboxId = "mailbox-gmail" }: { value: EmailSyncStatus; mailboxId?: string }) {
    const [reason, setReason] = useState<unknown>(null);
    return <><EmailSync status={value} providers={providers} mailboxId={mailboxId}
      text={text} language="en" onRefresh={refresh}
      onError={(failure) => { error(failure); setReason(failure); }} />
      {reason ? <div role="alert" data-testid="action-error">{emailErrorLabel(reason, text)}</div> : null}</>;
  }
  async function render(value = status(), mailboxId = "mailbox-gmail") { await act(async () => root.render(<Harness value={value} mailboxId={mailboxId} />)); }
  function buttons(label: string) {
    return [...container.querySelectorAll<HTMLButtonElement>("button")].filter((button) => button.textContent === label);
  }
  async function click(label: string, index = 0) {
    const button = buttons(label)[index]; expect(button).toBeDefined();
    await act(async () => button.click());
  }
  it.each(["recipient@example.test", undefined])("keeps intake and cleanup identity/version while projecting Outlook labels (%s)", async deliveryAddress => {
    const mailbox = { ...status().mailboxes[0], provider: "outlook" as const, address: "outlook_internal@outlook.com", delivery_address: deliveryAddress,
      account_aliases: deliveryAddress ? [deliveryAddress] : undefined, state: "login_required" };
    const value = { ...status(), mailboxes: [mailbox] };
    const outlook = { ...providers[0], provider: "outlook" as const, display_name: "Outlook" };
    vi.mocked(api.updateEmailIntake).mockResolvedValue({ mailbox });
    vi.mocked(api.cleanupEmailSource).mockResolvedValue({ scope: "mailbox", purged: 0, freed_bytes: 0, partial: false });
    await act(async () => root.render(<EmailSync status={value} providers={[outlook]} mailboxId={mailbox.id} text={text} language="en" onRefresh={refresh} onError={error} />));
    expect(container.querySelector(".emailLoginRecovery strong")?.textContent).toBe(deliveryAddress || "Outlook");
    await click(text.email.receivingSettings);
    expect(container.querySelector(".emailAccountRow strong")?.textContent).toBe(deliveryAddress || "Outlook");
    expect(container.querySelector(".emailCoverage strong")?.textContent).toBe(deliveryAddress || "Outlook");
    expect(container.textContent).not.toContain(mailbox.address);
    await act(async () => container.querySelector<HTMLInputElement>("input[type=checkbox]")!.click());
    expect(api.updateEmailIntake).toHaveBeenCalledWith("outlook", false, 3);
    await click(text.email.cleanupMailbox);
    await click(text.email.cleanupConfirmAction);
    expect(api.cleanupEmailSource).toHaveBeenCalledWith(expect.objectContaining({ scope: "mailbox", mailbox_id: mailbox.id }));
  });

  it("offers no sign-in button without the desktop login capability", async () => {
    const value = status(); value.mailboxes[0].state = "login_required";
    const login = vi.spyOn(api, "openEmailLoginBrowser");
    const check = vi.spyOn(api, "checkEmailProvider");
    await act(async () => root.render(<MailWorkspaceContext.Provider value={{ identity: 1, enabled: true, sendEnabled: true, loginEnabled: false, attachmentsEnabled: true, listFiles: vi.fn(), conversationID: "local", onFileSaved: vi.fn(), download: vi.fn() }}><Harness value={value} /></MailWorkspaceContext.Provider>));
    expect(buttons(text.email.signInAgain)).toHaveLength(0);
    expect(buttons(text.email.resumeAfterLogin)[0].disabled).toBe(true);
    await click(text.email.resumeAfterLogin);
    expect(login).not.toHaveBeenCalled(); expect(check).not.toHaveBeenCalled();
  });
  it("resumes an account signed in on the backend without enabling desktop sign-in", async () => {
    const value = status(); value.mailboxes[0].state = "login_required";
    const login = vi.spyOn(api, "openEmailLoginBrowser");
    const check = vi.spyOn(api, "checkEmailProvider").mockResolvedValue(providers[0]);
    vi.mocked(api.updateEmailIntake).mockResolvedValue({ mailbox: value.mailboxes[0] });
    const loginNotice = "Complete sign-in in the backend.";
    await act(async () => root.render(<MailWorkspaceContext.Provider value={{ identity: 1, enabled: true, sendEnabled: true,
      loginEnabled: false, resumeEnabled: true, loginNotice, attachmentsEnabled: true, listFiles: vi.fn(), conversationID: "local",
      onFileSaved: vi.fn(), download: vi.fn() }}><Harness value={value} /></MailWorkspaceContext.Provider>));
    expect(buttons(text.email.signInAgain)).toHaveLength(0);
    expect(buttons(text.email.resumeAfterLogin)[0].disabled).toBe(false);
    expect(container.textContent).toContain(loginNotice);
    await click(text.email.resumeAfterLogin);
    expect(check).toHaveBeenCalledExactlyOnceWith("qq_mail");
    expect(api.updateEmailIntake).toHaveBeenCalledExactlyOnceWith("qq_mail", true, 3);
    expect(api.syncEmail).toHaveBeenCalledExactlyOnceWith("mailbox-qq");
    expect(login).not.toHaveBeenCalled();
  });
  async function history(index = 0) {
    await click(text.email.receivingSettings); await click(text.email.viewWarnings, index);
  }
  function noReceiveSideEffects() {
    expect(api.updateEmailIntake).not.toHaveBeenCalled();
    expect(api.cleanupEmailSource).not.toHaveBeenCalled();
  }

  it("shows aggregate unacknowledged warnings while collapsed without fetching details", async () => {
    await render(status(), "");
    expect(container.querySelector('[role="alert"]')?.textContent).toContain(`${text.email.syncWarnings}: 3`);
    expect(container.querySelector('[role="alert"]')?.textContent).toContain(text.email.suppressedWarning);
    expect(api.emailSyncWarnings).not.toHaveBeenCalled();
    expect(api.syncEmail).not.toHaveBeenCalled(); noReceiveSideEffects();
  });

  it("loads history for the chosen mailbox, not the current sync filter", async () => {
    await render(); await history();
    expect(api.emailSyncWarnings).toHaveBeenCalledExactlyOnceWith("mailbox-qq");
    expect(container.textContent).toContain(warning.warning_ref);
    expect(container.textContent).toContain(warning.error_code);
    await click(text.email.viewWarnings, 1);
    expect(api.emailSyncWarnings).toHaveBeenLastCalledWith("mailbox-gmail");
    expect(api.syncEmail).not.toHaveBeenCalled(); noReceiveSideEffects();
  });

  it("acknowledges the warning without syncing or changing intake, and retains history", async () => {
    await render(); await history(); await click(text.email.acknowledgeWarning);
    expect(api.acknowledgeEmailSyncWarning).toHaveBeenCalledExactlyOnceWith("mailbox-qq", "warning-qq");
    expect(refresh).toHaveBeenCalledOnce();
    expect(container.textContent).toContain(text.email.warningAcknowledged);
    expect(container.textContent).toContain(warning.warning_ref);
    expect(buttons(text.email.acknowledgeWarning)).toHaveLength(0);
    expect(api.syncEmail).not.toHaveBeenCalled(); noReceiveSideEffects();
    const updated = status(); updated.mailboxes.forEach((mailbox) => { mailbox.unacknowledged_warning_count = 0; });
    await render(updated);
    expect(container.querySelector('[role="alert"]')).toBeNull();
    expect(container.textContent).toContain(warning.warning_ref);
    expect(buttons(text.email.viewWarnings)).toHaveLength(2);
  });

  it("Refresh calls the shared sync endpoint for the selected mailbox and retains warnings", async () => {
    await render(); await click(text.email.sync);
    expect(api.syncEmail).toHaveBeenCalledExactlyOnceWith("mailbox-gmail");
    expect(refresh).toHaveBeenCalledOnce();
    expect(container.textContent).toContain(text.email.syncScheduled);
    expect(container.querySelector('[role="alert"]')?.textContent).toContain(`${text.email.syncWarnings}: 2`);
    expect(api.acknowledgeEmailSyncWarning).not.toHaveBeenCalled(); noReceiveSideEffects();
  });

  it("guards rapid clicks only until HTTP acceptance, then permits another request", async () => {
    await render();
    await act(async () => { buttons(text.email.sync)[0].click(); buttons(text.email.sync)[0].click(); });
    expect(api.syncEmail).toHaveBeenCalledOnce();
    expect(buttons(text.email.sync)[0].disabled).toBe(false);
    await render(status());
    expect(buttons(text.email.sync)[0].disabled).toBe(false);
    await click(text.email.sync);
    expect(api.syncEmail).toHaveBeenCalledTimes(2);
    const done = status(); Object.assign(done.mailboxes[1], { refresh_pending: false, refresh_request_id: "refresh-1" });
    await render(done);
    expect(buttons(text.email.sync)[0].disabled).toBe(false);
  });

  it("submits on LAN HTTP contexts without crypto.randomUUID", async () => {
    const original = Object.getOwnPropertyDescriptor(crypto, "randomUUID");
    Object.defineProperty(crypto, "randomUUID", { configurable: true, value: undefined });
    try {
      await render(); await click(text.email.sync);
      expect(api.syncEmail).toHaveBeenCalledOnce();
      expect(buttons(text.email.sync)[0].disabled).toBe(false);
    } finally {
      if (original) Object.defineProperty(crypto, "randomUUID", original);
      else Reflect.deleteProperty(crypto, "randomUUID");
    }
  });

  it("allows one follow-up during automatic synchronization regardless of model backlog", async () => {
    const value = status(); value.backlog = 99; value.mailboxes[1].state = "syncing";
    await render(value);
    expect(buttons(text.email.sync)[0].disabled).toBe(false);
    await click(text.email.sync);
    expect(api.syncEmail).toHaveBeenCalledOnce();
    expect(buttons(text.email.sync)[0].disabled).toBe(false);
    await click(text.email.sync);
    expect(api.syncEmail).toHaveBeenCalledTimes(2);
  });

  it("does not lock a confirmed request across remount; retains an uncertain other-tab guard", async () => {
    await render(); await click(text.email.sync);
    await act(async () => { root.unmount(); }); root = createRoot(container);
    await render();
    expect(buttons(text.email.sync)[0].disabled).toBe(false);
    await act(async () => {
      window.localStorage.setItem("sparkclaw.email.refresh.v1", JSON.stringify({ "mailbox-gmail": { baseline: "", request: "other-tab" } }));
      window.dispatchEvent(new StorageEvent("storage", { key: "sparkclaw.email.refresh.v1" }));
    });
    expect(buttons(text.email.sync)[0].disabled).toBe(true);
    expect(api.syncEmail).toHaveBeenCalledOnce();
    const done = status(); Object.assign(done.mailboxes[1], { refresh_pending: false, refresh_request_id: "other-tab" });
    vi.mocked(api.emailSyncStatus).mockResolvedValue(done);
    await render(done);
    expect(JSON.parse(window.localStorage.getItem("sparkclaw.email.refresh.v1") || "{}")).toEqual({});
    expect(buttons(text.email.sync)[0].disabled).toBe(false);
  });

  it("checks uncertain submission without resending and keeps a confirmed pending request locked", async () => {
    const failure = new Error("network lost");
    vi.mocked(api.syncEmail).mockRejectedValueOnce(failure);
    const pending = status(); Object.assign(pending.mailboxes[1], { refresh_pending: true, refresh_request_id: "unknown-response" });
    vi.mocked(api.emailSyncStatus).mockResolvedValue(pending);
    await render(); await click(text.email.sync);
    expect(api.emailSyncStatus).toHaveBeenCalled();
    expect(api.syncEmail).toHaveBeenCalledOnce();
    expect(buttons(text.email.sync)[0].disabled).toBe(true);
  });

  it.each(["newer-request", ""])("keeps a confirmed request enabled after fresh GET replacement %s", async (requestId) => {
    vi.useFakeTimers();
    await render(); await click(text.email.sync);
    expect(buttons(text.email.sync)[0].disabled).toBe(false);
    const done = status(); Object.assign(done.mailboxes[1], { refresh_pending: false, refresh_request_id: requestId });
    vi.mocked(api.emailSyncStatus).mockResolvedValue(done);
    await act(async () => { await vi.advanceTimersByTimeAsync(5000); });
    expect(buttons(text.email.sync)[0].disabled).toBe(false);
    expect(api.syncEmail).toHaveBeenCalledOnce();
  });

  it("does not let a late A POST overwrite another tab's newer B guard", async () => {
    let finish!: (value: Awaited<ReturnType<typeof api.syncEmail>>) => void;
    vi.mocked(api.syncEmail).mockImplementationOnce(() => new Promise((resolve) => { finish = resolve; }));
    await render(); await click(text.email.sync);
    const server = status(); Object.assign(server.mailboxes[1], { refresh_pending: true, refresh_request_id: "request-B" });
    vi.mocked(api.emailSyncStatus).mockResolvedValue(server);
    await act(async () => {
      window.localStorage.setItem("sparkclaw.email.refresh.v1", JSON.stringify({ "mailbox-gmail": { baseline: "request-A", request: "request-B", generation: "generation-B" } }));
      window.dispatchEvent(new StorageEvent("storage", { key: "sparkclaw.email.refresh.v1" }));
      finish({ scheduled: true, refresh_requests: [{ mailbox_id: "mailbox-gmail", refresh_request_id: "request-A" }] });
    });
    expect(JSON.parse(window.localStorage.getItem("sparkclaw.email.refresh.v1")!)["mailbox-gmail"].request).toBe("request-B");
    expect(buttons(text.email.sync)[0].disabled).toBe(true);
    const done = status(); Object.assign(done.mailboxes[1], { refresh_pending: false, refresh_request_id: "request-B" });
    await render(done); expect(buttons(text.email.sync)[0].disabled).toBe(false);
  });

  it("allows a new request after POST confirmation despite a stale GET", async () => {
    let finish!: (value: Awaited<ReturnType<typeof api.syncEmail>>) => void;
    let oldStatus!: (value: EmailSyncStatus) => void;
    vi.mocked(api.syncEmail).mockImplementationOnce(() => new Promise((resolve) => { finish = resolve; }));
    vi.mocked(api.emailSyncStatus).mockImplementationOnce(() => new Promise((resolve) => { oldStatus = resolve; }));
    await render(); await click(text.email.sync);
    await act(async () => finish({ scheduled: true, refresh_requests: [{ mailbox_id: "mailbox-gmail", refresh_request_id: "refresh-1" }] }));
    await act(async () => oldStatus(status()));
    expect(buttons(text.email.sync)[0].disabled).toBe(false);
    expect(api.syncEmail).toHaveBeenCalledOnce();
  });

  it.each(["acknowledge", "history", "sync"] as const)("keeps warnings and exposes a safe user error when %s fails", async (operation) => {
    const failure = new Error("private transport detail must not be rendered");
    await render(); await history();
    if (operation === "acknowledge") {
      vi.mocked(api.acknowledgeEmailSyncWarning).mockRejectedValueOnce(failure);
      await click(text.email.acknowledgeWarning);
      expect(buttons(text.email.acknowledgeWarning)).toHaveLength(1);
      expect(container.textContent).not.toContain(text.email.warningAcknowledged);
    } else if (operation === "history") {
      vi.mocked(api.emailSyncWarnings).mockRejectedValueOnce(failure);
      await click(text.email.viewWarnings);
    } else {
      vi.mocked(api.syncEmail).mockRejectedValueOnce(failure);
      vi.mocked(api.emailSyncStatus).mockResolvedValue(status());
      await click(text.email.sync);
      expect(container.textContent).not.toContain(text.email.syncScheduled);
    }
    expect(error).toHaveBeenCalledExactlyOnceWith(failure);
    expect(container.querySelector('[data-testid="action-error"]')?.textContent).toBe(emailErrorLabel(failure, text));
    expect(container.textContent).not.toContain(failure.message);
    expect(container.textContent).toContain(warning.warning_ref);
    expect(container.querySelector('.emailCapacityWarning[role="alert"]')?.textContent).toContain(`${text.email.syncWarnings}: 2`);
    expect(buttons(text.email.sync)[0].disabled).toBe(false);
    expect(refresh).not.toHaveBeenCalled(); noReceiveSideEffects();
    if (operation !== "sync") expect(api.syncEmail).not.toHaveBeenCalled();
  });
  it("preserves the original intake toggle and uses the fenced version without a mailbox", async () => {
    const value: EmailSyncStatus = { version: 2, backlog: 0, pending_count: 0, mailboxes: [], provider_versions: { qq_mail: 1 } };
    vi.mocked(api.updateEmailIntake).mockRejectedValueOnce({ status: 409, code: "email_intake_not_applied" });
    await render(value); await click(text.email.receivingSettings);
    const toggle = container.querySelector<HTMLInputElement>('.emailAccountGroup[aria-label="QQ Mail"] input[aria-label$=": ' + text.email.accountReceiveLabel + '"]')!;
    expect(toggle).not.toBeNull(); expect(toggle.checked).toBe(false); expect(toggle.disabled).toBe(false);
    await act(async () => toggle.click());
    expect(api.updateEmailIntake).toHaveBeenCalledExactlyOnceWith("qq_mail", true, 1);
    expect(container.querySelector('[data-testid="action-error"]')?.textContent).toBe(text.email.intakeNotApplied);
    expect(refresh).toHaveBeenCalled(); expect(toggle.checked).toBe(false); expect(toggle.disabled).toBe(false);
    expect(buttons(text.email.receivingSettings)).toHaveLength(1);
    expect(container.querySelector('.emailSyncBar')).not.toBeNull();
  });


  it("keeps connection state, backlog, timestamp and warnings within the selected mailbox", async () => {
    const value = status(); value.backlog = 93;
    Object.assign(value.mailboxes[0], { backlog: 90, last_sync_at: "2026-09-20T11:00:00Z" });
    Object.assign(value.mailboxes[1], { backlog: 3, last_sync_at: "2026-09-19T09:00:00Z" });
    await render(value);
    expect(container.querySelector(".emailSyncState small")?.textContent).toBe(text.email.syncPaused);
    expect(container.querySelector(".emailSyncQueue strong")?.textContent).toBe("3");
    expect(container.querySelector(".emailSyncLast")?.textContent).toContain("Sep 19");
    expect(container.querySelector('.emailCapacityWarning[role="alert"]')?.textContent).toContain(`${text.email.syncWarnings}: 2`);
    expect(buttons(text.email.sync)[0].title).toContain("gmail@example.com");
    await render(value, "mailbox-qq");
    expect(container.querySelector(".emailSyncState small")?.textContent).toBe(text.email.syncConnected);
    expect(container.querySelector(".emailSyncQueue strong")?.textContent).toBe("90");
    expect(container.querySelector(".emailSyncLast")?.textContent).toContain("Sep 20");
    expect(container.querySelector('.emailCapacityWarning[role="alert"]')?.textContent).toContain(`${text.email.syncWarnings}: 1`);
    await render(value, "");
    expect(container.querySelector(".emailSyncQueue strong")?.textContent).toBe("93");
    expect(container.querySelector('.emailCapacityWarning[role="alert"]')?.textContent).toContain(`${text.email.syncWarnings}: 3`);
  });

  it("does not display a previous mailbox's scheduled state after switching filters", async () => {
    await render(); await click(text.email.sync);
    expect(container.textContent).toContain(text.email.syncScheduled);
    await render(status(), "mailbox-qq");
    expect(container.textContent).not.toContain(text.email.syncScheduled);
  });

  it("confirms capacity cleanup in the email layer and focuses cancellation first", async () => {
    const value = status(); value.capacity = { state: "critical", total_bytes: 2048, free_bytes: 16, used_percent: 99 };
    await render(value);
    const trigger = buttons(text.email.cleanupAll)[0];
    await act(async () => trigger.click());
    const confirmation = container.querySelector('[role="alertdialog"]');
    expect(confirmation).not.toBeNull();
    expect(confirmation?.textContent).toContain(text.email.cleanupAllScope);
    expect(confirmation?.textContent).toContain(text.email.cleanupConsequences);
    expect(document.activeElement?.textContent).toBe(text.common.cancel);
    expect(api.cleanupEmailSource).not.toHaveBeenCalled();
    await click(text.common.cancel);
    expect(container.querySelector('[role="alertdialog"]')).toBeNull();
    expect(document.activeElement).toBe(trigger);
    expect(api.cleanupEmailSource).not.toHaveBeenCalled();
  });

  it("Escape cancels cleanup without reaching the enclosing email dialog", async () => {
    await render(); await click(text.email.receivingSettings); await click(text.email.cleanupMailbox);
    const bubble = vi.fn(); container.addEventListener("keydown", bubble);
    const event = new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true });
    await act(async () => document.activeElement?.dispatchEvent(event));
    expect(event.defaultPrevented).toBe(true);
    expect(bubble).not.toHaveBeenCalled();
    expect(container.querySelector('[role="alertdialog"]')).toBeNull();
    expect(api.cleanupEmailSource).not.toHaveBeenCalled();
  });

  it("snapshots the mailbox address and ID, and submits only once after confirmation", async () => {
    let finish!: (result: Awaited<ReturnType<typeof api.cleanupEmailSource>>) => void;
    vi.mocked(api.cleanupEmailSource).mockImplementationOnce(() => new Promise(resolve => { finish = resolve; }));
    await render(); await click(text.email.receivingSettings); await click(text.email.cleanupMailbox);
    expect(container.querySelector('[role="alertdialog"]')?.textContent).toContain("qq@example.com");
    // A filter switch cannot silently change an already reviewed cleanup scope.
    await render(status(), "mailbox-gmail");
    await act(async () => {
      buttons(text.email.cleanupConfirmAction)[0].click();
      buttons(text.email.cleanupConfirmAction)[0].click();
    });
    expect(api.cleanupEmailSource).toHaveBeenCalledExactlyOnceWith({ scope: "mailbox", mailbox_id: "mailbox-qq", command_key: expect.any(String) });
    expect(buttons(text.email.cleanupConfirmAction)[0].disabled).toBe(true);
    await act(async () => finish({ scope: "mailbox", purged: 1, freed_bytes: 256, partial: false }));
    expect(container.querySelector('[role="alertdialog"]')).toBeNull();
    expect(refresh).toHaveBeenCalledOnce();
  });

  it("states date cleanup affects every mailbox and retains the reviewed date", async () => {
    await render(); await click(text.email.receivingSettings);
    const date = container.querySelector<HTMLInputElement>('input[type="date"]')!;
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(date, "2026-09-19");
      date.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await click(text.email.cleanupDate);
    expect(container.querySelector('[role="alertdialog"]')?.textContent).toContain(`${text.email.cleanupDateScope} 2026-09-19`);
    expect(api.cleanupEmailSource).not.toHaveBeenCalled();
    await click(text.email.cleanupConfirmAction);
    expect(api.cleanupEmailSource).toHaveBeenCalledExactlyOnceWith({ scope: "date", date: "2026-09-19", command_key: expect.any(String) });
  });

  it("confirms full cleanup from expanded settings and reports safe cleanup errors", async () => {
    const failure = new Error("private cleanup failure");
    vi.mocked(api.cleanupEmailSource).mockRejectedValueOnce(failure);
    await render(); await click(text.email.receivingSettings); await click(text.email.cleanupAll);
    expect(container.querySelector('[role="alertdialog"]')?.textContent).toContain(text.email.cleanupAllScope);
    await click(text.email.cleanupConfirmAction);
    expect(api.cleanupEmailSource).toHaveBeenCalledExactlyOnceWith({ scope: "all", command_key: expect.any(String) });
    expect(container.querySelector('[role="alertdialog"]')).toBeNull();
    expect(container.querySelector('[data-testid="action-error"]')?.textContent).toBe(emailErrorLabel(failure, text));
    expect(container.textContent).not.toContain("private cleanup failure");
  });
});
