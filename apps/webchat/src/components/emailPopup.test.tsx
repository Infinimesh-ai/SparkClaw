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
    expect(container.textContent).not.toContain("Conversation summary");
    expect(container.textContent).not.toContain("Email summary");
    expect(api.ensureEmailPresentations).not.toHaveBeenCalled();
    expect(api.emailPresentations).not.toHaveBeenCalled();
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
    const pending = [...container.querySelectorAll<HTMLButtonElement>(".emailCategoryTabs button")][2];
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
    expect(container.textContent).toContain(text.email.classificationFailed);
    expect(container.textContent).not.toContain(text.email.classificationUncertain);
    await act(async () => button(text.email.moveToInformation).click());
    expect(container.textContent).toContain(text.email.rememberSender);
    await act(async () => button(text.email.saveClassification).click());
    expect(api.classifyEmail).toHaveBeenCalledWith("uncertain", expect.objectContaining({ entry: "notification", expected_version: 2, expected_rule_version: 4, remember_sender: true }));
    await act(async () => button(text.email.information).click());
    expect(api.emailNotifications).toHaveBeenCalledWith(expect.objectContaining({ unassigned_only: true }), expect.any(AbortSignal));
    expect(api.emailConversations).toHaveBeenCalledWith(expect.objectContaining({ entry: "notification" }), expect.any(AbortSignal));
    expect(container.querySelectorAll(".emailConversationRow")).toHaveLength(3);
    expect(container.textContent).toContain("Subject notice");
    expect(api.markEmailViewed).not.toHaveBeenCalled();
  });

  it("keeps event names independent of display language and does not render raw backend errors", async () => {
    vi.mocked(api.emailPresentations).mockImplementation(async (kind, ids, language) => ({ items: ids.map((id) => ({ target_kind: kind, target_id: id, presentation_language: language, state: "ready" as const, revision: 1, analysis_revision: "1", title: language === "zh" ? "采购事项" : "Procurement", summary: language === "zh" ? "采购摘要" : "Summary" })) }));
    await open();
    await act(async () => (container.querySelector(".emailConversationRow") as HTMLElement).click());
    await act(async () => root.render(<EmailPopupEntry text={dictionaries.zh} language="zh" />));
    expect(container.querySelector(".emailDetailHeader h2")?.textContent).toBe("Procurement");
    expect(container.textContent).not.toContain("A procurement topic");
    vi.mocked(api.reanalyzeEmail).mockRejectedValue(new Error("Internal English error /private/path"));
    await act(async () => button(dictionaries.zh.email.reanalyze).click());
    expect(container.textContent).toContain(dictionaries.zh.email.actionFailed);
    expect(container.textContent).not.toContain("Internal English error");
  });

  it("shows an incomplete history without blocking source reading or reply", async () => {
    vi.mocked(api.emailMessages).mockResolvedValue({ version: 1, messages: [{ ...mail("102"), history_state: "partial", history_reason: "/private/diagnostic" }] });
    await open();
    await act(async () => (container.querySelector(".emailConversationRow") as HTMLElement).click());
    expect(container.textContent).toContain(text.email.historyMissing);
    expect(container.textContent).not.toContain("/private/diagnostic");
    expect(container.querySelector(".emailBody[open]")).not.toBeNull();
    expect(button(text.email.reply).disabled).toBe(false);
    expect(api.ensureEmailPresentations).not.toHaveBeenCalled();
  });

  it("shows failed history separately from active backfill", async () => {
    vi.mocked(api.emailMessages).mockResolvedValue({ version: 1, messages: [{ ...mail("102"), history_state: "failed", history_reason: "capture_failed" }] });
    await open();
    await act(async () => (container.querySelector(".emailConversationRow") as HTMLElement).click());
    expect(container.textContent).toContain(text.email.historyFailed);
    expect(container.textContent).not.toContain(text.email.historyPending);
    expect(container.textContent).not.toContain("capture_failed");
    expect(container.querySelector(".emailBody[open]")).not.toBeNull();
    expect(button(text.email.reply).disabled).toBe(false);
  });

  it("labels source fallback names and shows original classification evidence without presentations", async () => {
    const fallback = { ...conversation("purchase"), title: "Delivery update", title_state: "source_fallback" as const };
    vi.mocked(api.emailConversations).mockResolvedValue({ version: 1, conversations: [fallback, { ...conversation("payment"), title: "", title_state: "pending" }] });
    vi.mocked(api.emailConversation).mockResolvedValue({ version: 1, conversation: fallback });
    vi.mocked(api.emailMessages).mockResolvedValue({ version: 1, messages: [{ ...mail("102"), classification: { category: "interaction", effective_entry: "interaction", source: "model", state: "ready", revision: 1, reason_code: "body_evidence", evidence: [{ ref: "representation:102:body", text: "Please confirm the delivery date." }] } }] });
    await open();
    expect(container.textContent).toContain(text.email.eventAwaitingName);
    await act(async () => (container.querySelector(".emailConversationRow") as HTMLElement).click());
    expect(container.querySelector(".emailDetailHeader h2")?.textContent).toBe(`${text.email.originalSubject}: Delivery update`);
    expect(container.textContent).toContain(text.email.classifiedByBody);
    expect(container.textContent).toContain(text.email.sourceEvidence);
    expect(container.querySelector(".emailClassification blockquote")?.textContent).toBe("Please confirm the delivery date.");
    expect(api.emailPresentations).not.toHaveBeenCalled();
    expect(api.ensureEmailPresentations).not.toHaveBeenCalled();
  });

  it("distinguishes a failed classification job from a semantic uncertainty", async () => {
    vi.mocked(api.emailMessages).mockResolvedValue({ version: 1, messages: [{ ...mail("102"), classification_state: "failed", classification: { category: "unknown", effective_entry: "interaction", source: "fallback", state: "ready", revision: 1, reason_code: "classification_uncertain" } }] });
    await open();
    await act(async () => (container.querySelector(".emailConversationRow") as HTMLElement).click());
    expect(container.textContent).toContain(text.email.classificationFailed);
    expect(container.textContent).not.toContain(text.email.classificationUncertain);
  });

  it("shows event-scope counts for notifications", async () => {
    vi.mocked(api.emailConversations).mockResolvedValue({ version: 1, conversations: [], counts: { total: 205, unseen: 104 } });
    await open();
    await act(async () => button(text.email.information).click());
    expect(container.textContent).toContain(`${text.email.matchingMessages}: 205`);
    expect(api.emailConversations).toHaveBeenLastCalledWith(expect.objectContaining({ entry: "notification", cursor: "" }), expect.any(AbortSignal));
  });

});
