// @vitest-environment jsdom
import { act, StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { api } from "../api/client";
import type { EmailDraft } from "../api/email";
import { dictionaries } from "../i18n";
import { EmailCompose, EmailDraftList } from "./emailCompose";

const text = dictionaries.zh;
const draft: EmailDraft = { id: "d", version: 1, mailbox_id: "box", mode: "compose", to: ["to@example.com"], cc: [], subject: "Subject", body: "Text", state: "draft" };
const mailboxes = [{ id: "box", version: 1, provider: "gmail" as const, address: "owner@example.com", intake_enabled: false, active_binding: true, state: "ready" }];
afterEach(() => vi.restoreAllMocks());
describe("email compose safety", () => {
  it("polishes a reply intent, keeps the result editable, and sends the edited native reply directly", async () => {
    vi.spyOn(api, "emailComposeCapabilities").mockResolvedValue({ compose: true, reply: true, reply_all: true, cc: true, max_to: 100 });
    const polished: EmailDraft = { ...draft, mode: "reply", reply_mail_id: "incoming", body: "周二下午可以参加，请发会议链接。" };
    const polish = vi.spyOn(api, "polishEmailReply").mockResolvedValue(polished);
    const save = vi.spyOn(api, "saveEmailDraft").mockImplementation(async (value) => ({ ...polished, ...value, id: "d", version: 2 }));
    const send = vi.spyOn(api, "sendEmailDraft").mockResolvedValue({ ...polished, version: 3, state: "sent", conversation_id: "topic" });
    const onSent = vi.fn();
    const host = document.createElement("div"); const root = createRoot(host);
    try {
      await act(async () => root.render(<EmailCompose variant="conversation" target={{ mode: "reply", mailId: "incoming", mailboxId: "box" }} mailboxes={mailboxes} text={text} language="zh" onClose={() => {}} onBeforeClose={() => {}} onSent={onSent} />));
      const intent = host.querySelector<HTMLTextAreaElement>(`textarea[aria-label="${text.email.replyIntent}"]`)!;
      expect(intent).not.toBeNull();
      await act(async () => { Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(intent, "确认能参加，并索要会议链接"); intent.dispatchEvent(new Event("input", { bubbles: true })); });
      await act(async () => [...host.querySelectorAll("button")].find((button) => button.textContent === text.email.polishReply)!.click());
      expect(polish).toHaveBeenCalledWith(expect.objectContaining({ mail_id: "incoming", instruction: "确认能参加，并索要会议链接", language: "zh" }));
      const body = host.querySelector<HTMLTextAreaElement>(`.emailReplyBody textarea`)!;
      expect(body.value).toContain("会议链接");
      await act(async () => { Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(body, "周二下午可以参加，请提前发送会议链接。"); body.dispatchEvent(new Event("input", { bubbles: true })); });
      expect(send).not.toHaveBeenCalled();
      await act(async () => [...host.querySelectorAll("button")].find((button) => button.textContent === text.email.sendReply)!.click());
      expect(save).toHaveBeenCalledWith(expect.objectContaining({ mode: "reply", reply_mail_id: "incoming", body: "周二下午可以参加，请提前发送会议链接。" }));
      expect(send).toHaveBeenCalledTimes(1);
      expect(onSent).toHaveBeenCalledWith(expect.objectContaining({ state: "sent" }));
    } finally { act(() => root.unmount()); }
  });

  it("keeps an unknown sending result locked after a repeated click", async () => {
    vi.spyOn(api, "emailComposeCapabilities").mockResolvedValue({ compose: true, reply: false, reply_all: false, cc: false, max_to: 1 });
    vi.spyOn(api, "emailDraft").mockResolvedValue(draft);
    vi.spyOn(api, "saveEmailDraft").mockResolvedValue({ ...draft, version: 2 });
    let finish!: (value: EmailDraft) => void;
    const send = vi.spyOn(api, "sendEmailDraft").mockImplementation(() => new Promise((resolve) => { finish = resolve; }));
    const host = document.createElement("div"); const root = createRoot(host);
    try {
      await act(async () => root.render(<EmailCompose target={{ mode: "compose", draftId: "d" }} mailboxes={mailboxes} text={text} language="zh" onClose={() => {}} onBeforeClose={() => {}} />));
      const button = [...host.querySelectorAll("button")].find((b) => b.textContent === text.email.send)!;
      await act(async () => { button.click(); button.click(); });
      expect(send).toHaveBeenCalledTimes(1);
      await act(async () => finish({ ...draft, version: 3, state: "unknown" }));
      expect(button.disabled).toBe(true); expect(host.textContent).toContain(text.email.sendUnknown);
      await act(async () => button.click()); expect(send).toHaveBeenCalledTimes(1);
    } finally { act(() => root.unmount()); }
  });
  it("saves a reply draft but does not enable unsupported native delivery", async () => {
    vi.spyOn(api, "emailComposeCapabilities").mockResolvedValue({ compose: true, reply: false, reply_all: false, cc: false, max_to: 1 });
    const save = vi.spyOn(api, "saveEmailDraft").mockResolvedValue({ ...draft, mode: "reply", reply_mail_id: "m" });
    const send = vi.spyOn(api, "sendEmailDraft");
    const host = document.createElement("div"); const root = createRoot(host);
    try {
      await act(async () => root.render(<StrictMode><EmailCompose target={{ mode: "reply", mailId: "m", mailboxId: "box" }} mailboxes={mailboxes} text={text} language="zh" onClose={() => {}} onBeforeClose={() => {}} /></StrictMode>));
      expect(host.textContent).toContain(text.email.sendUnsupported);
      expect([...host.querySelectorAll("button")].find((b) => b.textContent === text.email.send)?.disabled).toBe(true);
      expect(save).toHaveBeenCalled();
      const ids = save.mock.calls.map(([value]) => value.id);
      expect(new Set(ids).size).toBe(1);
      expect(send).not.toHaveBeenCalled();
    } finally { act(() => root.unmount()); }
  });
  it("persists an editable draft before allowing its window to close", async () => {
    vi.spyOn(api, "emailComposeCapabilities").mockResolvedValue({ compose: true, reply: false, reply_all: false, cc: false, max_to: 1 });
    vi.spyOn(api, "emailDraft").mockResolvedValue(draft);
    const save = vi.spyOn(api, "saveEmailDraft").mockResolvedValue({ ...draft, version: 2 });
    let closeGuard: (() => Promise<boolean>) | null = null;
    const register = (handler: (() => Promise<boolean>) | null) => { closeGuard = handler; };
    const host = document.createElement("div"); const root = createRoot(host);
    try {
      await act(async () => root.render(<EmailCompose target={{ mode: "compose", draftId: "d" }} mailboxes={mailboxes} text={text} language="zh" onClose={() => {}} onBeforeClose={register} />));
      let allowed = false;
      await act(async () => { allowed = await closeGuard!(); });
      expect(allowed).toBe(true);
      expect(save).toHaveBeenCalledWith(expect.objectContaining({ id: "d", expected_version: 1, body: "Text" }));
    } finally { act(() => root.unmount()); }
  });

  it("sends a native reply-all draft with separate To and CC and opens the resulting conversation", async () => {
    vi.spyOn(api, "emailComposeCapabilities").mockResolvedValue({ compose: true, reply: true, reply_all: true, cc: true, max_to: 100 });
    const reply: EmailDraft = { ...draft, mode: "reply_all", reply_mail_id: "incoming", to: ["a@example.com", "b@example.com"], cc: ["c@example.com"] };
    vi.spyOn(api, "emailDraft").mockResolvedValue(reply);
    const save = vi.spyOn(api, "saveEmailDraft").mockResolvedValue({ ...reply, version: 2 });
    vi.spyOn(api, "sendEmailDraft").mockResolvedValue({ ...reply, version: 3, state: "sent", conversation_id: "topic" });
    const onSent = vi.fn();
    const host = document.createElement("div"); const root = createRoot(host);
    try {
      await act(async () => root.render(<EmailCompose target={{ mode: "reply_all", draftId: "d" }} mailboxes={mailboxes} text={text} language="zh" onClose={() => {}} onBeforeClose={() => {}} onSent={onSent} />));
      const send = [...host.querySelectorAll("button")].find((b) => b.textContent === text.email.send)!;
      expect(send.disabled).toBe(false);
      await act(async () => send.click());
      expect(save).toHaveBeenCalledWith(expect.objectContaining({ mode: "reply_all", reply_mail_id: "incoming", to: ["a@example.com", "b@example.com"], cc: ["c@example.com"] }));
      expect(onSent).toHaveBeenCalledWith(expect.objectContaining({ state: "sent", conversation_id: "topic" }));
    } finally { act(() => root.unmount()); }
  });

  it("checks an uncertain send without executing Send again", async () => {
    vi.spyOn(api, "emailComposeCapabilities").mockResolvedValue({ compose: true, reply: true, reply_all: true, cc: true, max_to: 100 });
    vi.spyOn(api, "emailDraft").mockResolvedValue({ ...draft, state: "unknown" });
    const reconcile = vi.spyOn(api, "reconcileEmailDraft").mockResolvedValue({ ...draft, state: "sent", sent_mail_id: "source", conversation_id: "topic" });
    const send = vi.spyOn(api, "sendEmailDraft");
    const host = document.createElement("div"); const root = createRoot(host);
    try {
      await act(async () => root.render(<EmailCompose target={{ mode: "compose", draftId: "d" }} mailboxes={mailboxes} text={text} language="zh" onClose={() => {}} onBeforeClose={() => {}} />));
      await act(async () => [...host.querySelectorAll("button")].find((b) => b.textContent === text.email.reconcileSend)!.click());
      expect(reconcile).toHaveBeenCalledWith("d"); expect(send).not.toHaveBeenCalled();
      expect(host.textContent).toContain(text.email.sendEvidenceLinked);
    } finally { act(() => root.unmount()); }
  });

  it("requires explicit original selection and confirmation to reconcile an uncertain send", async () => {
    vi.spyOn(api, "emailComposeCapabilities").mockResolvedValue({ compose: true, reply: true, reply_all: true, cc: true, max_to: 100 });
    vi.spyOn(api, "emailDraft").mockResolvedValue({ ...draft, state: "unknown" });
    const sources = vi.spyOn(api, "emailSentSources").mockResolvedValue({ version: 1, messages: [{ id: "original", version: 1, mailbox_id: "box", receiving_address: "owner@example.com", direction: "outbound", from: "owner@example.com", to: draft.to, cc: [], subject: "Captured original", body_text: "Actual sent body", arrived_at: "2026-09-09T00:00:00Z", viewed: true, attachments: [], original_available: true }] });
    const reconcile = vi.spyOn(api, "reconcileEmailDraft").mockResolvedValue({ ...draft, state: "sent", sent_mail_id: "original", confirmation_source: "owner_confirmed_capture" });
    const send = vi.spyOn(api, "sendEmailDraft");
    const host = document.createElement("div"); const root = createRoot(host);
    try {
      await act(async () => root.render(<EmailCompose target={{ mode: "compose", draftId: "d" }} mailboxes={mailboxes} text={text} language="zh" onClose={() => {}} onBeforeClose={() => {}} />));
      await act(async () => [...host.querySelectorAll("button")].find((b) => b.textContent === text.email.selectSentSource)!.click());
      expect(sources).toHaveBeenCalledWith(expect.objectContaining({ mailbox_id: "box" }), expect.any(AbortSignal));
      expect(reconcile).not.toHaveBeenCalled();
      await act(async () => [...host.querySelectorAll("button")].find((b) => b.textContent?.startsWith("Captured original"))!.click());
      expect(host.textContent).toContain("Actual sent body"); expect(reconcile).not.toHaveBeenCalled();
      await act(async () => [...host.querySelectorAll("button")].find((b) => b.textContent === text.email.confirmSentSource)!.click());
      expect(reconcile).toHaveBeenCalledWith("d", "original"); expect(send).not.toHaveBeenCalled();
      expect(host.textContent).toContain(text.email.sendOwnerConfirmed);
    } finally { act(() => root.unmount()); }
  });

  it("opens a draft beyond the first page", async () => {
    const list = vi.spyOn(api, "emailDrafts").mockResolvedValueOnce({ items: [draft], next_cursor: "page2" }).mockResolvedValueOnce({ items: [{ ...draft, id: "later", subject: "Later draft" }] });
    const select = vi.fn(); const host = document.createElement("div"); const root = createRoot(host);
    try {
      await act(async () => root.render(<EmailDraftList text={text} onSelect={select} />));
      await act(async () => [...host.querySelectorAll("button")].find((b) => b.textContent === text.email.moreDrafts)!.click());
      expect(list).toHaveBeenLastCalledWith({ cursor: "page2" });
      await act(async () => [...host.querySelectorAll("button")].find((b) => b.textContent?.startsWith("Later draft"))!.click());
      expect(select).toHaveBeenCalledWith({ mode: "compose", draftId: "later" });
      expect(host.textContent).not.toContain(text.email.moreDrafts);
    } finally { act(() => root.unmount()); }
  });

  it("allows a blank-subject draft to be saved but requires a subject for sending", async () => {
    vi.spyOn(api, "emailComposeCapabilities").mockResolvedValue({ compose: true, reply: true, reply_all: true, cc: true, max_to: 100 });
    vi.spyOn(api, "emailDraft").mockResolvedValue({ ...draft, subject: "" });
    const save = vi.spyOn(api, "saveEmailDraft").mockResolvedValue({ ...draft, subject: "", version: 2 });
    const send = vi.spyOn(api, "sendEmailDraft");
    const host = document.createElement("div"); const root = createRoot(host);
    try {
      await act(async () => root.render(<EmailCompose target={{ mode: "compose", draftId: "d" }} mailboxes={mailboxes} text={text} language="zh" onClose={() => {}} onBeforeClose={() => {}} />));
      expect([...host.querySelectorAll("button")].find((b) => b.textContent === text.email.send)?.disabled).toBe(true);
      await act(async () => [...host.querySelectorAll("button")].find((b) => b.textContent === text.email.saveDraft)!.click());
      expect(save).toHaveBeenCalledWith(expect.objectContaining({ subject: "" })); expect(send).not.toHaveBeenCalled();
    } finally { act(() => root.unmount()); }
  });

});
