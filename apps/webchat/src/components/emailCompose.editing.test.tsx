// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import { api, APIError } from "../api/client";
import type { EmailDraft } from "../api/email";
import { MailWorkspaceContext, type MailWorkspace } from "../desktop/MailWorkspaceContext";
import { dictionaries } from "../i18n";
import { EmailCompose, type EmailComposeTarget } from "./emailCompose";

const text = dictionaries.en;
const capabilities = { compose: true, reply: true, reply_all: true, cc: true, max_to: 100 };
const mailboxes = [
  { id: "box-a", version: 1, provider: "gmail" as const, address: "first@example.com", intake_enabled: true, active_binding: true, state: "ready" },
  { id: "box-b", version: 1, provider: "gmail" as const, address: "second@example.com", intake_enabled: true, active_binding: true, state: "ready" },
];
const draft: EmailDraft = { id: "editor-draft", version: 2, mailbox_id: "box-b", mode: "reply", reply_mail_id: "original", to: ["sender@example.com"], cc: [], subject: "Re: Meeting", body: "Server body", state: "draft" };
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); });

async function edit(field: HTMLTextAreaElement | HTMLInputElement, value: string) {
  await act(async () => {
    Object.getOwnPropertyDescriptor(field.tagName === "TEXTAREA" ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype, "value")!.set!.call(field, value);
    field.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

it("retains an unpolished intent when closing and reopening, without sharing it with another mailbox", async () => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  vi.spyOn(api, "emailComposeCapabilities").mockResolvedValue(capabilities);
  const save = vi.spyOn(api, "saveEmailDraft");
  const polish = vi.spyOn(api, "polishEmailReply");
  const host = document.createElement("div"); const root = createRoot(host);
  let guard: (() => Promise<boolean>) | null = null;
  const register = (value: (() => Promise<boolean>) | null) => { guard = value; };
  const editing = vi.fn();
  const render = async (mailboxId = "box-a") => act(async () => root.render(<EmailCompose key={mailboxId} variant="conversation" target={{ mode: "reply", mailId: "intent-retention", mailboxId, replyRecipient: "sender@example.com" }} mailboxes={mailboxes} text={text} language="en" onClose={() => {}} onBeforeClose={register} onEditingChange={editing} />));
  try {
    await render(); expect(editing).toHaveBeenLastCalledWith(false);
    await edit(host.querySelector("textarea")!, "Ask for another time before agreeing");
    expect(editing).toHaveBeenLastCalledWith(true);
    let allowed = false; await act(async () => { allowed = await guard!(); });
    expect(allowed).toBe(true); expect(save).not.toHaveBeenCalled(); expect(polish).not.toHaveBeenCalled();
    await act(async () => root.render(null));
    await render("box-b"); expect(host.querySelector("textarea")!.value).toBe("");
    await act(async () => root.render(null));
    await render(); expect(host.querySelector("textarea")!.value).toBe("Ask for another time before agreeing");
    expect(editing).toHaveBeenLastCalledWith(true);
    await edit(host.querySelector("textarea")!, "");
  } finally { await act(async () => root.unmount()); }
});

it("blocks leaving after a failed first save and never replaces local text with an older server draft", async () => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  vi.spyOn(api, "emailComposeCapabilities").mockResolvedValue(capabilities);
  const save = vi.spyOn(api, "saveEmailDraft").mockRejectedValue(new Error("Connection lost"));
  const read = vi.spyOn(api, "emailDraft").mockResolvedValue(draft);
  const host = document.createElement("div"); const root = createRoot(host);
  let guard: (() => Promise<boolean>) | null = null;
  const editing = vi.fn();
  try {
    await act(async () => root.render(<EmailCompose target={{ mode: "compose", mailboxId: "box-a" }} mailboxes={mailboxes} text={text} language="en" onClose={() => {}} onBeforeClose={value => { guard = value; }} onEditingChange={editing} />));
    await edit(host.querySelector("textarea")!, "This correction must survive a failed save");
    let allowed = true; await act(async () => { allowed = await guard!(); });
    expect(allowed).toBe(false); expect(save).toHaveBeenCalledOnce(); expect(read).not.toHaveBeenCalled();
    expect(host.querySelector("textarea")!.value).toBe("This correction must survive a failed save");
    expect(editing).toHaveBeenLastCalledWith(true);
    const unload = new Event("beforeunload", { cancelable: true }); window.dispatchEvent(unload); expect(unload.defaultPrevented).toBe(true);
  } finally { await act(async () => root.unmount()); }
});

it("does not reload or overwrite an edited draft when the parent supplies an equivalent target object", async () => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  vi.spyOn(api, "emailComposeCapabilities").mockResolvedValue(capabilities);
  const read = vi.spyOn(api, "emailDraft").mockResolvedValue(draft);
  const host = document.createElement("div"); const root = createRoot(host);
  const render = async () => act(async () => root.render(<EmailCompose target={{ mode: "reply", draftId: draft.id }} mailboxes={mailboxes} text={text} language="en" onClose={() => {}} onBeforeClose={() => {}} />));
  try {
    await render(); await edit(host.querySelector("textarea")!, "New local text"); await render();
    expect(read).toHaveBeenCalledOnce(); expect(host.querySelector("textarea")!.value).toBe("New local text");
    expect(host.querySelector("select")!.disabled).toBe(true);
    expect(host.querySelector("select")!.value).toBe("box-b");
  } finally { await act(async () => root.unmount()); }
});

it("reopens the saved reply draft and rejects cached work after the draft was deleted", async () => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  vi.spyOn(api, "emailComposeCapabilities").mockResolvedValue(capabilities);
  vi.spyOn(api, "polishEmailReply").mockResolvedValue({ ...draft, reply_mail_id: "resume-draft" });
  const save = vi.spyOn(api, "saveEmailDraft").mockImplementation(async value => ({ ...draft, body: value.body, version: 3 }));
  const read = vi.spyOn(api, "emailDraft").mockResolvedValue({ ...draft, body: "Saved correction" });
  const target: EmailComposeTarget = { mode: "reply", mailId: "resume-draft", mailboxId: "box-b", replyRecipient: "sender@example.com" };
  const host = document.createElement("div"); const root = createRoot(host);
  let guard: (() => Promise<boolean>) | null = null;
  const register = (value: (() => Promise<boolean>) | null) => { guard = value; };
  const render = async () => act(async () => root.render(<EmailCompose variant="conversation" target={target} mailboxes={mailboxes} text={text} language="en" onClose={() => {}} onBeforeClose={register} />));
  try {
    await render(); await edit(host.querySelector("textarea")!, "Suggest a new date");
    await act(async () => [...host.querySelectorAll("button")].find(button => button.textContent === text.email.polishReply)!.click());
    await edit(host.querySelector("textarea")!, "Saved correction");
    let allowed = false; await act(async () => { allowed = await guard!(); }); expect(allowed).toBe(true);
    expect(save).toHaveBeenCalledWith(expect.objectContaining({ id: draft.id, body: "Saved correction" }));
    await act(async () => root.render(null)); await render();
    expect(read).toHaveBeenCalledWith(draft.id); expect(host.querySelector("textarea")!.value).toBe("Saved correction");
    await act(async () => root.render(null)); read.mockRejectedValue(new APIError(404, "Draft deleted", "email_not_found")); await render();
    expect(host.querySelector("textarea")!.value).toBe("");
    await act(async () => root.render(null)); read.mockClear(); await render(); expect(read).not.toHaveBeenCalled();
  } finally { await act(async () => root.unmount()); }
});

it("starts a clean reply after its saved version was successfully sent", async () => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  vi.spyOn(api, "emailComposeCapabilities").mockResolvedValue(capabilities);
  const reply = { ...draft, reply_mail_id: "clear-after-send" };
  vi.spyOn(api, "polishEmailReply").mockResolvedValue(reply);
  vi.spyOn(api, "saveEmailDraft").mockResolvedValue({ ...reply, version: 3 });
  vi.spyOn(api, "sendEmailDraft").mockResolvedValue({ ...reply, version: 4, state: "sent" });
  const read = vi.spyOn(api, "emailDraft");
  const target: EmailComposeTarget = { mode: "reply", mailId: "clear-after-send", mailboxId: "box-b", replyRecipient: "sender@example.com" };
  const host = document.createElement("div"); const root = createRoot(host);
  const editing = vi.fn();
  const render = async () => act(async () => root.render(<EmailCompose variant="conversation" target={target} mailboxes={mailboxes} text={text} language="en" onClose={() => {}} onBeforeClose={() => {}} onEditingChange={editing} />));
  try {
    await render(); await edit(host.querySelector("textarea")!, "Confirm the date");
    await act(async () => [...host.querySelectorAll("button")].find(button => button.textContent === text.email.polishReply)!.click());
    expect(host.textContent).toContain(text.email.draftSaved); expect(editing).toHaveBeenLastCalledWith(true);
    await act(async () => [...host.querySelectorAll("button")].find(button => button.textContent === text.email.sendReply)!.click());
    expect(host.textContent).toContain(text.email.sendSucceeded); expect(editing).toHaveBeenLastCalledWith(false);
    await act(async () => root.render(null)); await render();
    expect(read).not.toHaveBeenCalled(); expect(host.querySelector("textarea")!.value).toBe("");
    expect(host.textContent).toContain(text.email.tellHowToReply);
  } finally { await act(async () => root.unmount()); }
});

it("does not restore a previous workspace identity's intent into the new identity", async () => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  vi.spyOn(api, "emailComposeCapabilities").mockResolvedValue(capabilities);
  const host = document.createElement("div"); const root = createRoot(host);
  const workspace: MailWorkspace = { identity: 700, enabled: true, sendEnabled: true, attachmentsEnabled: false, conversationID: "conversation", onFileSaved: async () => {}, download: async () => {}, listFiles: async () => [] };
  const render = async (identity: number) => act(async () => root.render(<MailWorkspaceContext.Provider value={{ ...workspace, identity }}><EmailCompose key={identity} variant="conversation" target={{ mode: "reply", mailId: "identity-isolation", mailboxId: "box-a" }} mailboxes={mailboxes} text={text} language="en" onClose={() => {}} onBeforeClose={() => {}} /></MailWorkspaceContext.Provider>));
  try {
    await render(700); await edit(host.querySelector("textarea")!, "Private to the first owner");
    await render(701); expect(host.querySelector("textarea")!.value).toBe("");
    await edit(host.querySelector("textarea")!, "Private to the next owner");
    await render(700); expect(host.querySelector("textarea")!.value).toBe("Private to the first owner");
    await edit(host.querySelector("textarea")!, ""); await render(701); expect(host.querySelector("textarea")!.value).toBe("Private to the next owner");
    await edit(host.querySelector("textarea")!, "");
  } finally { await act(async () => root.unmount()); }
});
