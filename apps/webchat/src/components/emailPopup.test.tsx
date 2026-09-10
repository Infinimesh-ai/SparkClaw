// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../api/client";
import type { EmailConversation, EmailMessage, EmailSyncStatus } from "../api/email";
import { dictionaries } from "../i18n";
import { EmailPopupEntry } from "./emailPopup";

const text = dictionaries.en;
const conversation = (id: string): EmailConversation => ({ id, version: 1, title: id === "purchase" ? "Procurement" : "Payment", participants: ["vendor@example.com", "finance@example.com"], summary: "A procurement topic", summary_state: "ready", unseen_count: 1, concerns: [{ id: `concern-${id}`, version: 1, kind: "suspected_duplicate", evidence: "Shared purchase evidence", related_conversation_ids: [id === "purchase" ? "payment" : "purchase"] }] });
const mail = (id: string): EmailMessage => ({ id, version: 1, mailbox_id: "account-1", receiving_address: "owner@example.com", direction: "inbound", from: "vendor@example.com", to: ["owner@example.com"], cc: [], subject: `Subject ${id}`, arrived_at: "2026-09-08T08:00:00Z", summary: "Individual summary", body_text: "<script>alert('untrusted')</script>\nActual text", viewed: false, original_available: true, attachments: [] });
const status: EmailSyncStatus = { version: 1, backlog: 1, pending_count: 1, mailboxes: [{ id: "account-1", version: 3, provider: "gmail", address: "owner@example.com", active_binding: true, intake_enabled: true, state: "active" }] };

describe("email popup owner flow", () => {
  let root: ReturnType<typeof createRoot>;
  let container: HTMLElement;
  beforeEach(() => {
    vi.useFakeTimers();
    const storage = new Map<string, string>();
    Object.defineProperty(window, "localStorage", { configurable: true, value: { getItem: (k: string) => storage.get(k) ?? null, setItem: (k: string, v: string) => storage.set(k, v), removeItem: (k: string) => storage.delete(k) } });
    vi.spyOn(api, "emailInteractionMails").mockResolvedValue({ version: 1, messages: [] });
    vi.spyOn(api, "emailNotifications").mockResolvedValue({ version: 1, messages: [] });
    vi.spyOn(api, "emailPresentations").mockImplementation(async (kind, ids, language) => ({ items: ids.map((id) => ({ target_kind: kind, target_id: id, presentation_language: language, state: "ready" as const, revision: 1, analysis_revision: "1", title: id === "purchase" ? "Procurement" : "Payment", summary: "Localized summary", concern_explanations: {} })) }));
    vi.spyOn(api, "ensureEmailPresentations").mockResolvedValue({ items: [] });
    vi.spyOn(api, "emailProviders").mockResolvedValue({ providers: [{ provider: "gmail", display_name: "Gmail", enabled: true, default: true, account: "default", state: "ready", version: 1 }] });
    vi.spyOn(api, "emailConversations").mockResolvedValue({ version: 1, conversations: [conversation("purchase"), conversation("payment")] });
    vi.spyOn(api, "emailConversation").mockImplementation(async (id) => ({ version: 1, conversation: conversation(id) }));
    vi.spyOn(api, "emailMessages").mockImplementation(async (id) => ({ version: 1, messages: [mail(id === "purchase" ? "102" : "201")] }));
    vi.spyOn(api, "emailPending").mockResolvedValue({ version: 1, messages: [mail("pending-1")] });
    vi.spyOn(api, "emailSyncStatus").mockResolvedValue(status);
    vi.spyOn(api, "markEmailViewed").mockImplementation(async (ids) => ({ version: 1, mail_ids: ids }));
    vi.spyOn(api, "reanalyzeEmail").mockResolvedValue({ scheduled: true });
    vi.spyOn(api, "updateEmailIntake").mockResolvedValue({ mailbox: { ...status.mailboxes[0], version: 4, intake_enabled: false } });
    container = document.createElement("div"); document.body.append(container); root = createRoot(container);
  });
  afterEach(() => { act(() => root.unmount()); container.remove(); vi.restoreAllMocks(); vi.useRealTimers(); });
  function button(label: string) {
    const match = [...container.querySelectorAll<HTMLButtonElement>("button")].find((element) => element.getAttribute("aria-label") === label || element.textContent?.trim() === label);
    if (!match) throw new Error(`Missing button ${label}`);
    return match;
  }
  async function open() {
    await act(async () => root.render(<EmailPopupEntry text={text} language="en" />));
    const trigger = button(text.email.title); trigger.focus();
    await act(async () => trigger.click());
    return trigger;
  }

  it("shows both suspected duplicate conversations, safe source text and stable selection after reopening", async () => {
    const trigger = await open();
    expect(container.querySelector("dialog[open]")).not.toBeNull();
    expect(container.querySelectorAll(".emailConversationRow")).toHaveLength(2);
    await act(async () => (container.querySelector(".emailConversationRow") as HTMLElement).click());
    expect(container.textContent).toContain(text.email.suspectedDuplicate);
    expect(container.textContent).toContain(text.email.conversationSummary);
    expect(container.textContent).toContain(text.email.messageSummary);
    expect(container.textContent).toContain("<script>alert('untrusted')</script>");
    expect(container.querySelector("script")).toBeNull();
    expect(api.markEmailViewed).not.toHaveBeenCalled();
    await act(async () => button(`${text.email.relatedConversation} 1`).click());
    expect(container.querySelector(".emailDetailHeader h2")?.textContent).toBe("Payment");
    await act(async () => button(text.email.reanalyze).click());
    expect(api.reanalyzeEmail).toHaveBeenCalledWith("201");
    expect(container.querySelectorAll(".emailConversationRow")).toHaveLength(2);
    expect(container.querySelector("[data-email-mail-id='201']")).not.toBeNull();
    await act(async () => container.querySelector("dialog")!.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })));
    expect(container.querySelector("dialog")).toBeNull();
    expect(document.activeElement).toBe(trigger);
    await act(async () => trigger.click());
    expect(container.querySelector(".emailDetailHeader h2")?.textContent).toBe("Payment");
  });

  it("loads pending sources independently and sends versioned intake toggles without replacing send settings", async () => {
    await open();
    const pending = [...container.querySelectorAll<HTMLButtonElement>(".emailViewTabs button")][1];
    await act(async () => pending.click());
    expect(container.querySelector("[data-email-mail-id='pending-1']")).not.toBeNull();
    expect(container.textContent).toContain(text.email.pendingHelp);
    expect(api.markEmailViewed).not.toHaveBeenCalled();
    await act(async () => button(text.email.receivingSettings).click());
    const toggle = container.querySelector<HTMLInputElement>("input[type=checkbox]")!;
    expect(toggle.checked).toBe(true);
    await act(async () => toggle.click());
    expect(api.updateEmailIntake).toHaveBeenCalledWith("gmail", false, 3);
  });

  it("supports arrow-key list navigation without acknowledging a conversation preview", async () => {
    await open();
    const rows = container.querySelectorAll<HTMLButtonElement>(".emailConversationRow");
    rows[0].focus();
    act(() => rows[0].dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true })));
    expect(document.activeElement).toBe(rows[1]);
    expect(api.emailMessages).not.toHaveBeenCalled();
    expect(api.markEmailViewed).not.toHaveBeenCalled();
  });

  it("re-enables a paused account with its latest binding version", async () => {
    vi.mocked(api.emailSyncStatus).mockResolvedValue({ ...status, mailboxes: [
      { ...status.mailboxes[0], id: "old-account", version: 2, active_binding: false, intake_enabled: false },
      { ...status.mailboxes[0], version: 7, active_binding: false, intake_enabled: true, state: "needs_attention" }
    ] });
    await open();
    await act(async () => button(text.email.receivingSettings).click());
    const toggle = container.querySelector<HTMLInputElement>("input[type=checkbox]")!;
    expect(toggle.checked).toBe(false);
    await act(async () => toggle.click());
    expect(api.updateEmailIntake).toHaveBeenCalledWith("gmail", true, 7);
  });
  it("routes notifications and uncertain interaction mail independently, and remembers a sender change", async () => {
    const uncertain = { ...mail("uncertain"), current_sender_address: "service@example.com", current_sender_rule_revision: 4, classification: { category: "unknown" as const, effective_entry: "interaction" as const, source: "fallback" as const, state: "failed", revision: 2, sender_address: "service@example.com" } };
    const notice = { ...mail("notice"), classification: { ...uncertain.classification, category: "notification" as const, effective_entry: "notification" as const, source: "model" as const } };
    vi.mocked(api.emailInteractionMails).mockResolvedValue({ version: 1, messages: [uncertain] });
    vi.mocked(api.emailNotifications).mockResolvedValue({ version: 1, messages: [notice] });
    vi.spyOn(api, "emailMessage").mockResolvedValue(uncertain);
    vi.spyOn(api, "emailSenderRules").mockResolvedValue({ rules: [{ id: "rule", address: "service@example.com", entry: "interaction", revision: 4, enabled: true }] });
    vi.spyOn(api, "classifyEmail").mockResolvedValue({ classification: { ...uncertain.classification, effective_entry: "notification", source: "manual", revision: 3 } });
    await open();
    await act(async () => (Array.from(container.querySelectorAll<HTMLButtonElement>(".emailConversationRow")).find((row) => row.textContent?.includes("Subject uncertain"))!).click());
    expect(container.textContent).toContain(text.email.classificationUncertain);
    await act(async () => button(text.email.moveToInformation).click());
    expect(container.textContent).toContain(text.email.rememberSender);
    await act(async () => button(text.email.saveClassification).click());
    expect(api.classifyEmail).toHaveBeenCalledWith("uncertain", expect.objectContaining({ entry: "notification", expected_version: 2, expected_rule_version: 4, remember_sender: true }));
    await act(async () => button(text.email.information).click());
    expect(api.emailNotifications).toHaveBeenCalledWith(expect.objectContaining({ subtype: "" }), expect.any(AbortSignal));
    expect(container.querySelectorAll(".emailConversationRow")).toHaveLength(1);
    expect(container.querySelector(".emailConversationRow")?.textContent).toContain("Subject notice");
    expect(api.markEmailViewed).not.toHaveBeenCalled();
  });

  it("switches generated content with settings and does not render raw backend errors", async () => {
    vi.mocked(api.emailPresentations).mockImplementation(async (kind, ids, language) => ({ items: ids.map((id) => ({ target_kind: kind, target_id: id, presentation_language: language, state: "ready" as const, revision: 1, analysis_revision: "1", title: language === "zh" ? "采购事项" : "Procurement", summary: language === "zh" ? "采购摘要" : "Summary" })) }));
    await open();
    await act(async () => (container.querySelector(".emailConversationRow") as HTMLElement).click());
    await act(async () => root.render(<EmailPopupEntry text={dictionaries.zh} language="zh" />));
    expect(container.querySelector(".emailDetailHeader h2")?.textContent).toBe("采购事项");
    expect(container.textContent).not.toContain("A procurement topic");
    vi.mocked(api.reanalyzeEmail).mockRejectedValue(new Error("Internal English error /private/path"));
    await act(async () => button(dictionaries.zh.email.reanalyze).click());
    expect(container.textContent).toContain(dictionaries.zh.email.actionFailed);
    expect(container.textContent).not.toContain("Internal English error");
  });

  it("filters verification validity server-side and shows complete-scope counts", async () => {
    vi.mocked(api.emailNotifications).mockResolvedValue({ version: 1, messages: [], counts: { total: 205, unseen: 104 } });
    await open();
    await act(async () => button(text.email.information).click());
    expect(container.textContent).toContain(`${text.email.matchingMessages}: 205`);
    const subtype = container.querySelector<HTMLSelectElement>(".emailSubtype")!;
    await act(async () => { subtype.value = "verification"; subtype.dispatchEvent(new Event("change", { bubbles: true })); });
    const validity = container.querySelector<HTMLSelectElement>(`select[aria-label="${text.email.validityFilter}"]`)!;
    await act(async () => { validity.value = "validity_unknown"; validity.dispatchEvent(new Event("change", { bubbles: true })); });
    expect(api.emailNotifications).toHaveBeenLastCalledWith(expect.objectContaining({ subtype: "verification", validity: "validity_unknown", cursor: "" }), expect.any(AbortSignal));
  });

});
