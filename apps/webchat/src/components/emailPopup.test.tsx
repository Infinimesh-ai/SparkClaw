// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../api/client";
import type { EmailConversation, EmailDraft, EmailMessage, EmailSyncStatus } from "../api/email";
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
    vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
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
    vi.spyOn(api, "emailComposeCapabilities").mockResolvedValue({ compose: true, reply: true, reply_all: true, cc: true, max_to: 100 });
    vi.spyOn(api, "markEmailViewed").mockImplementation(async (ids) => ({ version: 1, mail_ids: ids }));
    vi.spyOn(api, "reanalyzeEmail").mockResolvedValue({ scheduled: true });
    vi.spyOn(api, "deleteEmailConversation").mockResolvedValue({ conversation_id: "purchase", deleted_mails: 1, freed_bytes: 0 });
    vi.spyOn(api, "updateEmailIntake").mockResolvedValue({ mailbox: { ...status.mailboxes[0], version: 4, intake_enabled: false } });
    container = document.createElement("div"); document.body.append(container); root = createRoot(container);
  });
  afterEach(() => { act(() => root.unmount()); container.remove(); vi.restoreAllMocks(); vi.useRealTimers(); vi.unstubAllGlobals(); });
  function button(label: string) {
    const match = [...document.body.querySelectorAll<HTMLButtonElement>("button")].find((element) => element.getAttribute("aria-label") === label || element.textContent?.trim() === label);
    if (!match) throw new Error(`Missing button ${label}`);
    return match;
  }
  async function open() {
    await act(async () => root.render(<EmailPopupEntry text={text} language="en" />));
    const trigger = button(text.email.title); trigger.focus();
    await act(async () => trigger.click());
    return trigger;
  }
  async function edit(element: HTMLInputElement | HTMLTextAreaElement, value: string) {
    const prototype = element instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
    await act(async () => {
      Object.getOwnPropertyDescriptor(prototype, "value")!.set!.call(element, value);
      element.dispatchEvent(new Event("input", { bubbles: true }));
    });
  }
  async function chooseAddress(value: string) {
    await act(async () => {
      const select = document.body.querySelector<HTMLSelectElement>(".emailAddressFilter select")!;
      select.value = value; select.dispatchEvent(new Event("change", { bubbles: true }));
    });
  }

  it("toggles an empty composer closed and open again without closing the mail popup", async () => {
    const save = vi.spyOn(api, "saveEmailDraft").mockRejectedValue(new Error("An empty composer must not save"));
    const send = vi.spyOn(api, "sendEmailDraft").mockRejectedValue(new Error("Toggling must not send mail"));
    await open();
    const popup = document.body.querySelector("dialog[open]")!;
    const compose = button(text.email.compose);
    expect(compose.getAttribute("aria-expanded")).toBe("false");

    await act(async () => compose.click());
    expect(compose.getAttribute("aria-expanded")).toBe("true");
    expect(document.body.querySelector(".emailComposer")).not.toBeNull();
    await act(async () => compose.click());
    expect(compose.getAttribute("aria-expanded")).toBe("false");
    expect(document.body.querySelector(".emailComposer")).toBeNull();
    expect(document.body.querySelector("dialog[open]")).toBe(popup);

    await act(async () => compose.click());
    expect(compose.getAttribute("aria-expanded")).toBe("true");
    expect(document.body.querySelector<HTMLTextAreaElement>(".emailComposer textarea")?.value).toBe("");
    expect(document.body.querySelector("dialog[open]")).toBe(popup);
    expect(save).not.toHaveBeenCalled();
    expect(send).not.toHaveBeenCalled();
  });

  it("waits for the edited composer to save before toggling closed and ignores repeated clicks while saving", async () => {
    const savedDraft: EmailDraft = { id: "toggle-saved-draft", version: 1, mailbox_id: "account-1", mode: "compose", to: [], cc: [], subject: "", body: "Keep this draft before I leave", state: "draft" };
    let finishSave!: (draft: EmailDraft) => void;
    const save = vi.spyOn(api, "saveEmailDraft").mockImplementation(() => new Promise(resolve => { finishSave = resolve; }));
    const send = vi.spyOn(api, "sendEmailDraft").mockRejectedValue(new Error("Toggling must not send mail"));
    await open();
    const popup = document.body.querySelector("dialog[open]")!;
    const compose = button(text.email.compose);
    await act(async () => compose.click());
    const composer = document.body.querySelector(".emailComposer")!;
    await edit(composer.querySelector("textarea")!, savedDraft.body);

    await act(async () => compose.click());
    expect(save).toHaveBeenCalledOnce();
    expect(save).toHaveBeenCalledWith(expect.objectContaining({ id: expect.any(String), expected_version: 0, mailbox_id: "account-1", mode: "compose", body: savedDraft.body }));
    expect(compose.getAttribute("aria-expanded")).toBe("true");
    expect(document.body.querySelector(".emailComposer")).toBe(composer);
    expect(composer.querySelector<HTMLTextAreaElement>("textarea")?.value).toBe(savedDraft.body);
    expect(compose.closest("fieldset")?.disabled).toBe(true);
    await act(async () => compose.dispatchEvent(new MouseEvent("click", { bubbles: true })));
    expect(save).toHaveBeenCalledOnce();

    await act(async () => finishSave(savedDraft));
    expect(document.body.querySelector(".emailComposer")).toBeNull();
    expect(compose.getAttribute("aria-expanded")).toBe("false");
    expect(compose.closest("fieldset")?.disabled).toBe(false);
    expect(document.body.querySelector("dialog[open]")).toBe(popup);
    expect(send).not.toHaveBeenCalled();
  });

  it("keeps an edited composer open after a failed save and closes it only after a successful retry", async () => {
    const savedDraft: EmailDraft = { id: "toggle-retried-draft", version: 1, mailbox_id: "account-1", mode: "compose", to: [], cc: [], subject: "", body: "Do not lose this unsaved draft", state: "draft" };
    const save = vi.spyOn(api, "saveEmailDraft").mockRejectedValueOnce(new Error("Offline")).mockResolvedValueOnce(savedDraft);
    const send = vi.spyOn(api, "sendEmailDraft").mockRejectedValue(new Error("Toggling must not send mail"));
    await open();
    const popup = document.body.querySelector("dialog[open]")!;
    const compose = button(text.email.compose);
    await act(async () => compose.click());
    const composer = document.body.querySelector(".emailComposer")!;
    await edit(composer.querySelector("textarea")!, savedDraft.body);

    await act(async () => compose.click());
    expect(save).toHaveBeenCalledOnce();
    expect(document.body.querySelector(".emailComposer")).toBe(composer);
    expect(composer.querySelector<HTMLTextAreaElement>("textarea")?.value).toBe(savedDraft.body);
    expect(compose.getAttribute("aria-expanded")).toBe("true");
    expect(compose.closest("fieldset")?.disabled).toBe(false);
    expect(composer.querySelector('[role="alert"]')?.textContent).toContain(text.email.actionFailed);
    expect(document.body.querySelector("dialog[open]")).toBe(popup);

    await act(async () => compose.click());
    expect(save).toHaveBeenCalledTimes(2);
    expect(save.mock.calls[1][0]).toEqual(save.mock.calls[0][0]);
    expect(document.body.querySelector(".emailComposer")).toBeNull();
    expect(compose.getAttribute("aria-expanded")).toBe("false");
    expect(document.body.querySelector("dialog[open]")).toBe(popup);
    expect(send).not.toHaveBeenCalled();
  });

  it("retains reply intent across conversations, category switches and closing the popup", async () => {
    vi.mocked(api.emailMessages).mockImplementation(async id => ({ version: 1, messages: [mail(`intent-${id}`)] }));
    await open();
    const row = (index: number) => document.body.querySelectorAll<HTMLButtonElement>(".emailConversationRow")[index];
    await act(async () => row(0).click());
    await edit(document.body.querySelector(".emailReplyComposer textarea")!, "Keep this proposal until I return");
    await act(async () => row(1).click());
    await act(async () => row(0).click());
    expect(document.body.querySelector<HTMLTextAreaElement>(".emailReplyComposer textarea")?.value).toBe("Keep this proposal until I return");
    await act(async () => button(text.email.information).click());
    await act(async () => button(text.email.interactions).click());
    expect(document.body.querySelector<HTMLTextAreaElement>(".emailReplyComposer textarea")?.value).toBe("Keep this proposal until I return");
    await act(async () => button(text.common.close).click());
    await act(async () => button(text.email.title).click());
    expect(document.body.querySelector<HTMLTextAreaElement>(".emailReplyComposer textarea")?.value).toBe("Keep this proposal until I return");
    await edit(document.body.querySelector(".emailReplyComposer textarea")!, "");
  });

  it("blocks every view transition when the current reply cannot be saved", async () => {
    vi.mocked(api.emailMessages).mockImplementation(async id => ({ version: 1, messages: [mail(`blocked-${id}`)] }));
    vi.mocked(api.emailSyncStatus).mockResolvedValue({ ...status, mailboxes: [...status.mailboxes, { ...status.mailboxes[0], id: "account-2", address: "second@example.com" }] });
    const draft: EmailDraft = { id: "blocked-draft", version: 1, mailbox_id: "account-1", mode: "reply", reply_mail_id: "blocked-purchase", to: ["vendor@example.com"], cc: [], subject: "Reply", body: "Generated body", state: "draft" };
    vi.spyOn(api, "polishEmailReply").mockResolvedValue(draft);
    const save = vi.spyOn(api, "saveEmailDraft").mockRejectedValue(new Error("Offline"));
    const read = vi.spyOn(api, "emailDraft");
    await open();
    await act(async () => document.body.querySelectorAll<HTMLButtonElement>(".emailConversationRow")[0].click());
    await edit(document.body.querySelector(".emailReplyComposer textarea")!, "Generate the reply");
    await act(async () => button(text.email.polishReply).click());
    await edit(document.body.querySelector(".emailReplyComposer textarea")!, "Unsaved local correction");
    await act(async () => document.body.querySelectorAll<HTMLButtonElement>(".emailConversationRow")[1].click());
    await act(async () => button(text.email.information).click());
    await chooseAddress("account-2");
    await edit(document.body.querySelector(".emailSearch input")!, "different topic");
    await act(async () => document.body.querySelector(".emailFilters")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })));
    await act(async () => button(text.common.close).click());
    expect(document.body.querySelector("dialog")).not.toBeNull();
    expect(document.body.querySelector(".emailDetailHeader h2")?.textContent).toBe("Procurement");
    expect(document.body.querySelector<HTMLSelectElement>(".emailAddressFilter select")?.value).toBe("");
    expect(document.body.querySelector<HTMLTextAreaElement>(".emailReplyComposer textarea")?.value).toBe("Unsaved local correction");
    expect(save).toHaveBeenCalledTimes(5); expect(read).not.toHaveBeenCalled();
    expect(api.emailMessages).not.toHaveBeenCalledWith("payment", expect.anything(), expect.anything());
    expect(api.emailConversations).toHaveBeenLastCalledWith(expect.objectContaining({ mailbox_id: "", q: "", entry: "interaction" }), expect.any(AbortSignal));
  });

  it("clears stale details on address or search changes and restores the complete scope on reopen", async () => {
    vi.mocked(api.emailSyncStatus).mockResolvedValue({ ...status, mailboxes: [...status.mailboxes, { ...status.mailboxes[0], id: "account-2", address: "second@example.com" }] });
    await open();
    await act(async () => document.body.querySelectorAll<HTMLButtonElement>(".emailConversationRow")[0].click());
    await chooseAddress("account-2");
    expect(document.body.querySelector(".emailDetailHeader h2")).toBeNull();
    expect(document.body.querySelector(".emailReplyComposer")).toBeNull();
    expect(api.emailConversations).toHaveBeenLastCalledWith(expect.objectContaining({ mailbox_id: "account-2" }), expect.any(AbortSignal));
    await act(async () => document.body.querySelectorAll<HTMLButtonElement>(".emailConversationRow")[1].click());
    await edit(document.body.querySelector(".emailSearch input")!, "budget");
    await act(async () => document.body.querySelector(".emailFilters")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })));
    expect(document.body.querySelector(".emailDetailHeader h2")).toBeNull();
    expect(api.emailConversations).toHaveBeenLastCalledWith(expect.objectContaining({ mailbox_id: "account-2", q: "budget" }), expect.any(AbortSignal));
    await act(async () => document.body.querySelectorAll<HTMLButtonElement>(".emailConversationRow")[1].click());
    await act(async () => button(text.common.close).click());
    await act(async () => button(text.email.title).click());
    expect(document.body.querySelector<HTMLSelectElement>(".emailAddressFilter select")?.value).toBe("account-2");
    expect(document.body.querySelector<HTMLInputElement>(".emailSearch input")?.value).toBe("budget");
    expect(document.body.querySelector(".emailDetailHeader h2")?.textContent).toBe("Payment");
    expect(api.emailConversations).toHaveBeenLastCalledWith(expect.objectContaining({ mailbox_id: "account-2", q: "budget" }), expect.any(AbortSignal));
  });

  it("finishes saving before navigation and ignores a competing transition while saving", async () => {
    vi.mocked(api.emailMessages).mockImplementation(async id => ({ version: 1, messages: [mail(`saving-${id}`)] }));
    const draft: EmailDraft = { id: "save-before-navigation", version: 1, mailbox_id: "account-1", mode: "reply", reply_mail_id: "saving-purchase", to: ["vendor@example.com"], cc: [], subject: "Reply", body: "Generated body", state: "draft" };
    vi.spyOn(api, "polishEmailReply").mockResolvedValue(draft);
    let finishSave!: (draft: EmailDraft) => void;
    const save = vi.spyOn(api, "saveEmailDraft").mockImplementation(() => new Promise(resolve => { finishSave = resolve; }));
    const savedDraft = { ...draft, version: 2, body: "The saved revision" };
    vi.spyOn(api, "emailDraft").mockResolvedValue(savedDraft);
    await open();
    await act(async () => document.body.querySelectorAll<HTMLButtonElement>(".emailConversationRow")[0].click());
    await edit(document.body.querySelector(".emailReplyComposer textarea")!, "Generate my reply");
    await act(async () => button(text.email.polishReply).click());
    await edit(document.body.querySelector(".emailReplyComposer textarea")!, savedDraft.body);
    await act(async () => document.body.querySelectorAll<HTMLButtonElement>(".emailConversationRow")[1].click());
    expect(document.body.querySelector(".emailDetailHeader h2")?.textContent).toBe("Procurement");
    await act(async () => button(text.email.information).dispatchEvent(new MouseEvent("click", { bubbles: true })));
    expect(save).toHaveBeenCalledOnce();
    expect(save).toHaveBeenCalledWith(expect.objectContaining({ id: draft.id, body: savedDraft.body, expected_version: 1 }));
    await act(async () => finishSave(savedDraft));
    expect(document.body.querySelector(".emailDetailHeader h2")?.textContent).toBe("Payment");
    expect(api.emailConversations).toHaveBeenLastCalledWith(expect.objectContaining({ entry: "interaction" }), expect.any(AbortSignal));
    await act(async () => document.body.querySelectorAll<HTMLButtonElement>(".emailConversationRow")[0].click());
    expect(document.body.querySelector<HTMLTextAreaElement>(".emailReplyComposer textarea")?.value).toBe(savedDraft.body);
  });

  it("keeps the reply target fixed while writing when newer incoming mail arrives", async () => {
    const original = { ...mail("stable-original"), from: "original@example.com" };
    vi.mocked(api.emailMessages).mockResolvedValue({ version: 1, messages: [original] });
    await open();
    await act(async () => document.body.querySelectorAll<HTMLButtonElement>(".emailConversationRow")[0].click());
    await edit(document.body.querySelector(".emailReplyComposer textarea")!, "Do not change the recipient while I write");
    vi.mocked(api.emailMessages).mockResolvedValue({ version: 2, messages: [original, { ...mail("newer-arrival"), from: "new@example.com", arrived_at: "2026-09-10T08:00:00Z" }] });
    await act(async () => { await vi.advanceTimersByTimeAsync(5000); });
    expect(document.body.querySelector<HTMLTextAreaElement>(".emailReplyComposer textarea")?.value).toBe("Do not change the recipient while I write");
    expect(document.body.querySelector(".emailReplyComposer .emailReplyRecipient")?.textContent).toContain("original@example.com");
    expect(document.body.querySelector(".emailReplyComposer .emailReplyRecipient")?.textContent).not.toContain("new@example.com");
    await edit(document.body.querySelector(".emailReplyComposer textarea")!, "");
  });

  it.each(["recipient@example.test", undefined])("uses a verified Outlook filter label while keeping mailbox-ID requests (%s)", async deliveryAddress => {
    const outlook = { ...status.mailboxes[0], provider: "outlook" as const, address: "outlook_internal@outlook.com", delivery_address: deliveryAddress, account_aliases: deliveryAddress ? [deliveryAddress] : undefined };
    vi.mocked(api.emailSyncStatus).mockResolvedValue({ ...status, mailboxes: [outlook,
      { ...status.mailboxes[0], id: "legacy-qq", provider: "qq_mail", address: "legacy@qq.com" },
      { ...status.mailboxes[0], id: "legacy-gmail", address: "legacy@gmail.com" }] });
    vi.mocked(api.emailMessages).mockResolvedValue({ version: 1, messages: [{ ...mail("source"), receiving_address: outlook.address }] });
    vi.mocked(api.emailPending).mockResolvedValue({ version: 1, messages: [{ ...mail("pending-source"), receiving_address: outlook.address }] });
    await open();
    const select = document.body.querySelector<HTMLSelectElement>(".emailAddressFilter select")!;
    expect([...select.options].map(option => [option.value, option.textContent])).toEqual([
      ["", text.email.allAddresses], ["account-1", deliveryAddress || "Outlook"], ["legacy-qq", "legacy@qq.com"], ["legacy-gmail", "legacy@gmail.com"],
    ]);
    expect(document.body.textContent).not.toContain(outlook.address);
    await act(async () => { select.value = "account-1"; select.dispatchEvent(new Event("change", { bubbles: true })); });
    expect(api.emailConversations).toHaveBeenLastCalledWith(expect.objectContaining({ mailbox_id: "account-1" }), expect.any(AbortSignal));
    expect(select.value).toBe("account-1");
    await act(async () => (document.body.querySelector(".emailConversationRow") as HTMLElement).click());
    expect(document.body.querySelector(".emailConversationAddresses > div:first-child dd")?.textContent).toBe(deliveryAddress || "Outlook");
    await act(async () => [...document.body.querySelectorAll<HTMLButtonElement>(".emailCategoryTabs button")][2].click());
    const receivingRow = [...document.body.querySelectorAll(".emailAddresses dt")].find(row => row.textContent === text.email.receivingAddress)!;
    expect(receivingRow.nextElementSibling?.textContent).toBe(deliveryAddress || "Outlook");
    expect(document.body.textContent).not.toContain(outlook.address);
    expect(api.markEmailViewed).not.toHaveBeenCalled();
  });

  it("keeps popup search and keyboard events outside the chat composer form", async () => {
    const submit = vi.fn((event: React.FormEvent) => event.preventDefault());
    const keyDown = vi.fn();
    await act(async () => root.render(<form onSubmit={submit} onKeyDown={keyDown}><EmailPopupEntry text={text} language="en" /></form>));
    await act(async () => button(text.email.title).click());
    const popup = document.body.querySelector("dialog")!;
    expect(popup.closest("form")).toBeNull();
    await act(async () => {
      popup.querySelector(".emailFilters")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
      popup.querySelector("input")!.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
    });
    expect(submit).not.toHaveBeenCalled();
    expect(keyDown).not.toHaveBeenCalled();
  });

  it("suspends an open mail window for approval and restores its unsaved input afterward", async () => {
    await open();
    const popup = document.body.querySelector("dialog")!;
    const input = popup.querySelector<HTMLInputElement>("input")!;
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, "unsaved search");
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await act(async () => root.render(<EmailPopupEntry text={text} language="en" enabled={false} suspended />));
    expect(document.body.querySelector("dialog")).toBe(popup);
    expect(popup.hidden).toBe(true);
    expect(popup.hasAttribute("inert")).toBe(true);
    expect(popup.querySelector("fieldset")!.disabled).toBe(true);
    expect(input.value).toBe("unsaved search");
    await act(async () => root.render(<EmailPopupEntry text={text} language="en" />));
    expect(popup.hidden).toBe(false);
    expect(popup.querySelector("fieldset")!.disabled).toBe(false);
    expect(document.body.querySelector("dialog input")).toBe(input);
    expect(input.value).toBe("unsaved search");
  });

  it("lets Escape dismiss a sender-rule native picker before closing the mail dialog", async () => {
    vi.spyOn(api, "emailSenderRules").mockResolvedValue({ rules: [{ id: "rule", address: "vendor@example.com", entry: "interaction", revision: 1, enabled: true }] });
    const supports = vi.fn((condition: string) => condition === "selector(select:open)");
    vi.stubGlobal("CSS", { supports });
    const trigger = await open();
    await act(async () => button(text.email.senderRules).click());
    const popup = document.body.querySelector("dialog")!;
    const select = popup.querySelector<HTMLSelectElement>(".emailRules select")!;
    expect(select).not.toBeNull();
    const nativeMatches = select.matches.bind(select);
    let pickerOpen = true;
    vi.spyOn(select, "matches").mockImplementation((selector) => selector === ":open" ? pickerOpen : nativeMatches(selector));
    select.focus();

    const dismissPicker = new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true });
    await act(async () => select.dispatchEvent(dismissPicker));
    expect(supports).toHaveBeenCalledWith("selector(select:open)");
    expect(document.body.querySelector("dialog")).toBe(popup);
    expect(dismissPicker.defaultPrevented).toBe(false);

    pickerOpen = false;
    const dismissMail = new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true });
    await act(async () => select.dispatchEvent(dismissMail));
    expect(document.body.querySelector("dialog")).toBeNull();
    expect(dismissMail.defaultPrevented).toBe(true);
    expect(document.activeElement).toBe(trigger);
  });

  it("shows localized message summaries without a conversation overview and keeps source details safe", async () => {
    const trigger = await open();
    expect(document.body.querySelector("dialog[open]")).not.toBeNull();
    expect(document.body.querySelectorAll(".emailConversationRow")).toHaveLength(2);
    await act(async () => (document.body.querySelector(".emailConversationRow") as HTMLElement).click());
    expect(document.body.textContent).toContain(text.email.suspectedDuplicate);
    expect(document.body.textContent).not.toContain("AI OVERVIEW");
    expect(document.body.textContent).not.toContain("A procurement topic");
    expect(api.ensureEmailPresentations).not.toHaveBeenCalled();
    expect(api.emailPresentations).toHaveBeenCalledWith("mail", ["102"], "en", expect.any(AbortSignal));
    expect(document.body.querySelector(".emailRenderPreview")).not.toBeNull();
    expect(document.body.querySelector(".emailRenderPreview iframe")).toBeNull();
    const addressRows = document.body.querySelectorAll(".emailConversationAddresses > div");
    expect(addressRows).toHaveLength(2);
    expect(addressRows[0]?.querySelector("dt")?.textContent).toBe(text.email.conversationReceivingEmail);
    expect(addressRows[0]?.querySelector("dd")?.textContent).toBe("owner@example.com");
    expect(addressRows[1]?.querySelector("dt")?.textContent).toBe(text.email.conversationSenderEmail);
    expect(addressRows[1]?.querySelector("dd")?.textContent).toBe("vendor@example.com");
    expect(document.body.querySelector(".emailConversationSummary")?.textContent).toBe("Localized summary");
    expect(document.body.textContent).not.toContain("Individual summary");
    expect(document.body.textContent).not.toContain("<script>alert('untrusted')</script>");
    expect(document.body.querySelector("script")).toBeNull();
    expect(api.markEmailViewed).not.toHaveBeenCalled();
    await act(async () => button(`${text.email.relatedConversation} 1`).click());
    expect(document.body.querySelector(".emailDetailHeader h2")?.textContent).toBe("Payment");
    await act(async () => button(text.email.reanalyze).click());
    expect(api.reanalyzeEmail).toHaveBeenCalledWith("201");
    expect(document.body.querySelectorAll(".emailConversationRow")).toHaveLength(2);
    expect(document.body.querySelector("[data-email-mail-id='201']")).not.toBeNull();
    await act(async () => document.body.querySelector("dialog")!.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })));
    expect(document.body.querySelector("dialog")).toBeNull();
    expect(document.activeElement).toBe(trigger);
    await act(async () => trigger.click());
    expect(document.body.querySelector(".emailDetailHeader h2")?.textContent).toBe("Payment");
  });

  it("loads pending sources independently and sends versioned intake toggles without replacing send settings", async () => {
    await open();
    const pending = [...document.body.querySelectorAll<HTMLButtonElement>(".emailCategoryTabs button")][2];
    await act(async () => pending.click());
    expect(document.body.querySelector("[data-email-mail-id='pending-1']")).not.toBeNull();
    expect(document.body.textContent).toContain(text.email.pendingHelp);
    expect(api.markEmailViewed).not.toHaveBeenCalled();
    await act(async () => button(text.email.receivingSettings).click());
    const toggle = document.body.querySelector<HTMLInputElement>("input[type=checkbox]")!;
    expect(toggle.checked).toBe(true);
    await act(async () => toggle.click());
    expect(api.updateEmailIntake).toHaveBeenCalledWith("gmail", false, 3);
  });

  it("supports arrow-key list navigation without acknowledging a conversation preview", async () => {
    await open();
    const rows = document.body.querySelectorAll<HTMLButtonElement>(".emailConversationRow");
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
    const toggle = document.body.querySelector<HTMLInputElement>("input[type=checkbox]")!;
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
    await act(async () => (Array.from(document.body.querySelectorAll<HTMLButtonElement>(".emailConversationRow")).find((row) => row.textContent?.includes("Subject uncertain"))!).click());
    expect(document.body.textContent).toContain(text.email.classificationFailed);
    expect(document.body.textContent).not.toContain(text.email.classificationUncertain);
    await act(async () => button(text.email.moveToInformation).click());
    expect(document.body.textContent).toContain(text.email.rememberSender);
    await act(async () => button(text.email.saveClassification).click());
    expect(api.classifyEmail).toHaveBeenCalledWith("uncertain", expect.objectContaining({ entry: "notification", expected_version: 2, expected_rule_version: 4, remember_sender: true }));
    await act(async () => button(text.email.information).click());
    expect(api.emailNotifications).toHaveBeenCalledWith(expect.objectContaining({ unassigned_only: true }), expect.any(AbortSignal));
    expect(api.emailConversations).toHaveBeenCalledWith(expect.objectContaining({ entry: "notification" }), expect.any(AbortSignal));
    expect(document.body.querySelectorAll(".emailConversationRow")).toHaveLength(3);
    expect(document.body.textContent).toContain("Subject notice");
    expect(api.markEmailViewed).not.toHaveBeenCalled();
  });

  it("keeps event names independent while switching historical mail summaries with the display language", async () => {
    vi.mocked(api.emailPresentations).mockImplementation(async (kind, ids, language) => ({ items: ids.map((id) => ({ target_kind: kind, target_id: id, presentation_language: language, state: "ready" as const, revision: 1, analysis_revision: "1", title: language === "zh" ? "采购事项" : "Procurement", summary: language === "zh" ? "采购摘要" : "Summary" })) }));
    await open();
    await act(async () => (document.body.querySelector(".emailConversationRow") as HTMLElement).click());
    await act(async () => root.render(<EmailPopupEntry text={dictionaries.zh} language="zh" />));
    expect(document.body.querySelector(".emailDetailHeader h2")?.textContent).toBe("Procurement");
    expect(document.body.querySelector(".emailConversationSummary")?.textContent).toBe("采购摘要");
    expect(api.emailPresentations).toHaveBeenCalledWith("mail", ["102"], "zh", expect.any(AbortSignal));
    vi.mocked(api.reanalyzeEmail).mockRejectedValue(new Error("Internal English error /private/path"));
    await act(async () => button(dictionaries.zh.email.reanalyze).click());
    expect(document.body.textContent).toContain(dictionaries.zh.email.actionFailed);
    expect(document.body.textContent).not.toContain("Internal English error");
  });

  it("shows an incomplete history without blocking source reading or reply", async () => {
    vi.mocked(api.emailMessages).mockResolvedValue({ version: 1, messages: [{ ...mail("102"), history_state: "partial", history_reason: "/private/diagnostic" }, { ...mail("103"), history_state: "partial" }] });
    await open();
    await act(async () => (document.body.querySelector(".emailConversationRow") as HTMLElement).click());
    expect(document.body.textContent).toContain(text.email.historyMissing);
    expect(document.body.textContent).not.toContain("/private/diagnostic");
    expect(document.body.querySelectorAll(".emailDetailHeader .emailWarning")).toHaveLength(1);
    expect(document.body.querySelectorAll(".emailMessage > .emailWarning")).toHaveLength(0);
    expect(document.body.querySelector(".emailRenderPreview")).not.toBeNull();
    expect(document.body.querySelector(".emailRenderPreview iframe")).toBeNull();
    expect(document.body.querySelector(`textarea[aria-label="${text.email.replyIntent}"]`)).not.toBeNull();
    expect(button(text.email.polishReply).disabled).toBe(true);
    expect(api.ensureEmailPresentations).not.toHaveBeenCalled();
  });

  it("deletes the selected conversation rather than an individual mail", async () => {
    await open();
    await act(async () => (document.body.querySelector(".emailConversationRow") as HTMLElement).click());
    expect(document.body.querySelector("[data-email-mail-id='102']")).not.toBeNull();
    expect(document.body.textContent).not.toContain("Clean up original");
    await act(async () => button(text.email.deleteConversation).click());
    expect(document.body.textContent).toContain(text.email.deleteConversationConfirm);
    await act(async () => button(text.email.deleteConversation).click());
    expect(api.deleteEmailConversation).toHaveBeenCalledWith("purchase", expect.objectContaining({ expected_version: 1, command_key: expect.any(String) }));
    expect(document.body.querySelector(".emailDetailHeader h2")).toBeNull();
  });

  it("shows failed history separately from active backfill", async () => {
    vi.mocked(api.emailMessages).mockResolvedValue({ version: 1, messages: [{ ...mail("102"), history_state: "failed", history_reason: "capture_failed" }] });
    await open();
    await act(async () => (document.body.querySelector(".emailConversationRow") as HTMLElement).click());
    expect(document.body.textContent).toContain(text.email.historyFailed);
    expect(document.body.textContent).not.toContain(text.email.historyPending);
    expect(document.body.textContent).not.toContain("capture_failed");
    expect(document.body.querySelector(".emailRenderPreview")).not.toBeNull();
    expect(document.body.querySelector(".emailRenderPreview iframe")).toBeNull();
    expect(document.body.querySelector(`textarea[aria-label="${text.email.replyIntent}"]`)).not.toBeNull();
    expect(button(text.email.polishReply).disabled).toBe(true);
  });

  it("labels source fallback names and shows original classification evidence with a localized summary", async () => {
    const fallback = { ...conversation("purchase"), title: "Delivery update", title_state: "source_fallback" as const };
    vi.mocked(api.emailConversations).mockResolvedValue({ version: 1, conversations: [fallback, { ...conversation("payment"), title: "", title_state: "pending" }] });
    vi.mocked(api.emailConversation).mockResolvedValue({ version: 1, conversation: fallback });
    vi.mocked(api.emailMessages).mockResolvedValue({ version: 1, messages: [{ ...mail("102"), classification: { category: "interaction", effective_entry: "interaction", source: "model", state: "ready", revision: 1, reason_code: "body_evidence", evidence: [{ ref: "representation:102:body", text: "Please confirm the delivery date." }] } }] });
    await open();
    expect(document.body.textContent).toContain(text.email.eventAwaitingName);
    await act(async () => (document.body.querySelector(".emailConversationRow") as HTMLElement).click());
    expect(document.body.querySelector(".emailDetailHeader h2")?.textContent).toBe(`${text.email.originalSubject}: Delivery update`);
    expect(document.body.textContent).toContain(text.email.classifiedByBody);
    expect(document.body.textContent).toContain(text.email.sourceEvidence);
    expect(document.body.querySelector(".emailClassification blockquote")?.textContent).toBe("Please confirm the delivery date.");
    expect(api.emailPresentations).toHaveBeenCalledWith("mail", ["102"], "en", expect.any(AbortSignal));
    expect(api.ensureEmailPresentations).not.toHaveBeenCalled();
  });

  it("queues a selected-language summary for already-synced mail", async () => {
    vi.mocked(api.emailPresentations).mockImplementation(async (kind, ids, language) => ({ items: ids.map((id) => ({ target_kind: kind, target_id: id, presentation_language: language, state: "missing" as const, revision: 0, analysis_revision: "historical-source" })) }));
    vi.mocked(api.ensureEmailPresentations).mockImplementation(async (kind, ids, language) => ({ items: ids.map((id) => ({ target_kind: kind, target_id: id, presentation_language: language, state: "ready" as const, revision: 1, analysis_revision: "historical-source", summary: language === "zh" ? "历史邮件摘要" : "Historical mail summary" })) }));
    await act(async () => root.render(<EmailPopupEntry text={dictionaries.zh} language="zh" />));
    await act(async () => button(dictionaries.zh.email.title).click());
    await act(async () => (document.body.querySelector(".emailConversationRow") as HTMLElement).click());
    expect(api.ensureEmailPresentations).toHaveBeenCalledWith("mail", ["102"], "zh", false, expect.any(AbortSignal));
    expect(document.body.querySelector(".emailConversationSummary")?.textContent).toBe("历史邮件摘要");
  });

  it("distinguishes a failed classification job from a semantic uncertainty", async () => {
    vi.mocked(api.emailMessages).mockResolvedValue({ version: 1, messages: [{ ...mail("102"), classification_state: "failed", classification: { category: "unknown", effective_entry: "interaction", source: "fallback", state: "ready", revision: 1, reason_code: "classification_uncertain" } }] });
    await open();
    await act(async () => (document.body.querySelector(".emailConversationRow") as HTMLElement).click());
    expect(document.body.textContent).toContain(text.email.classificationFailed);
    expect(document.body.textContent).not.toContain(text.email.classificationUncertain);
  });

  it("shows event-scope counts for notifications", async () => {
    vi.mocked(api.emailConversations).mockResolvedValue({ version: 1, conversations: [], counts: { total: 205, unseen: 104 } });
    await open();
    await act(async () => button(text.email.information).click());
    expect(document.body.textContent).toContain(`${text.email.matchingMessages}: 205`);
    expect(api.emailConversations).toHaveBeenLastCalledWith(expect.objectContaining({ entry: "notification", cursor: "" }), expect.any(AbortSignal));
  });

});
