// @vitest-environment jsdom
import { act, useState } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../api/client";
import type { EmailSyncStatus, EmailSyncWarning } from "../api/email";
import type { EmailProviderStatus } from "../api/types";
import { dictionaries } from "../i18n";
import { emailErrorLabel } from "./emailCommon";
import { EmailSync } from "./emailSync";

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
    vi.spyOn(api, "cleanupEmailSource");
  });
  afterEach(() => {
    act(() => root.unmount()); container.remove(); vi.restoreAllMocks(); vi.clearAllMocks(); vi.useRealTimers();
  });
  // Match EmailPopup's error callback + safe user-facing label contract without
  // bringing unrelated conversation, body, and provider polling into this test.
  function Harness({ value }: { value: EmailSyncStatus }) {
    const [reason, setReason] = useState<unknown>(null);
    return <><EmailSync status={value} providers={providers} mailboxId="mailbox-gmail"
      text={text} language="en" onRefresh={refresh}
      onError={(failure) => { error(failure); setReason(failure); }} />
      {reason ? <div role="alert" data-testid="action-error">{emailErrorLabel(reason, text)}</div> : null}</>;
  }
  async function render(value = status()) { await act(async () => root.render(<Harness value={value} />)); }
  function buttons(label: string) {
    return [...container.querySelectorAll<HTMLButtonElement>("button")].filter((button) => button.textContent === label);
  }
  async function click(label: string, index = 0) {
    const button = buttons(label)[index]; expect(button).toBeDefined();
    await act(async () => button.click());
  }
  async function history(index = 0) {
    await click(text.email.receivingSettings); await click(text.email.viewWarnings, index);
  }
  function noReceiveSideEffects() {
    expect(api.updateEmailIntake).not.toHaveBeenCalled();
    expect(api.cleanupEmailSource).not.toHaveBeenCalled();
  }

  it("shows aggregate unacknowledged warnings while collapsed without fetching details", async () => {
    await render();
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
    expect(container.querySelector('[role="alert"]')?.textContent).toContain(`${text.email.syncWarnings}: 3`);
    expect(api.acknowledgeEmailSyncWarning).not.toHaveBeenCalled(); noReceiveSideEffects();
  });

  it("guards rapid clicks and stays pending after HTTP success until the matching round completes", async () => {
    await render();
    await act(async () => { buttons(text.email.sync)[0].click(); buttons(text.email.sync)[0].click(); });
    expect(api.syncEmail).toHaveBeenCalledOnce();
    expect(buttons(text.email.sync)[0].disabled).toBe(true);
    expect(buttons(text.email.sync)[0].querySelector(".spin")).not.toBeNull();
    await render(status()); // stale false must not release the new request
    expect(buttons(text.email.sync)[0].disabled).toBe(true);
    const done = status(); Object.assign(done.mailboxes[1], { refresh_pending: false, refresh_request_id: "refresh-1" });
    await render(done);
    expect(buttons(text.email.sync)[0].disabled).toBe(false);
    expect(container.textContent).not.toContain(text.email.syncScheduled);
  });

  it("submits on LAN HTTP contexts without crypto.randomUUID", async () => {
    const original = Object.getOwnPropertyDescriptor(crypto, "randomUUID");
    Object.defineProperty(crypto, "randomUUID", { configurable: true, value: undefined });
    try {
      await render(); await click(text.email.sync);
      expect(api.syncEmail).toHaveBeenCalledOnce();
      expect(buttons(text.email.sync)[0].disabled).toBe(true);
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
    expect(buttons(text.email.sync)[0].disabled).toBe(true);
  });

  it("restores the pending guard after remount and observes another tab's mailbox request", async () => {
    await render(); await click(text.email.sync);
    await act(async () => { root.unmount(); }); root = createRoot(container);
    await render();
    expect(buttons(text.email.sync)[0].disabled).toBe(true);
    await act(async () => {
      window.localStorage.setItem("sparkclaw.email.refresh.v1", JSON.stringify({ "mailbox-gmail": { baseline: "", request: "other-tab" } }));
      window.dispatchEvent(new StorageEvent("storage", { key: "sparkclaw.email.refresh.v1" }));
    });
    await click(text.email.sync);
    expect(api.syncEmail).toHaveBeenCalledOnce();
    const done = status(); Object.assign(done.mailboxes[1], { refresh_pending: false, refresh_request_id: "other-tab" });
    await render(done);
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

  it.each(["newer-request", ""])("releases a settled request when a fresh GET reports completed replacement %s", async (requestId) => {
    vi.useFakeTimers();
    await render(); await click(text.email.sync);
    expect(buttons(text.email.sync)[0].disabled).toBe(true);
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

  it("does not let a GET started before POST settlement clear the pending request", async () => {
    let finish!: (value: Awaited<ReturnType<typeof api.syncEmail>>) => void;
    let oldStatus!: (value: EmailSyncStatus) => void;
    vi.mocked(api.syncEmail).mockImplementationOnce(() => new Promise((resolve) => { finish = resolve; }));
    vi.mocked(api.emailSyncStatus).mockImplementationOnce(() => new Promise((resolve) => { oldStatus = resolve; }));
    await render(); await click(text.email.sync);
    await act(async () => finish({ scheduled: true, refresh_requests: [{ mailbox_id: "mailbox-gmail", refresh_request_id: "refresh-1" }] }));
    await act(async () => oldStatus(status()));
    expect(buttons(text.email.sync)[0].disabled).toBe(true);
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
    expect(container.querySelector('.emailCapacityWarning[role="alert"]')?.textContent).toContain(`${text.email.syncWarnings}: 3`);
    expect(buttons(text.email.sync)[0].disabled).toBe(false);
    expect(refresh).not.toHaveBeenCalled(); noReceiveSideEffects();
    if (operation !== "sync") expect(api.syncEmail).not.toHaveBeenCalled();
  });
});
