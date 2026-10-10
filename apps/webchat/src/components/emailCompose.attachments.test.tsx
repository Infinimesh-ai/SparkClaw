// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import { api, APIError } from "../api/client";
import type { EmailDraft } from "../api/email";
import { MailWorkspaceContext, type MailWorkspace } from "../desktop/MailWorkspaceContext";
import { dictionaries } from "../i18n";
import { EmailCompose } from "./emailCompose";

const text = dictionaries.en;
const attachment = { local_file_id: "12345678-1234-4123-8123-123456789abc", name: "summary.pdf", size_bytes: 8123, sha256: "b".repeat(64) };
const draft: EmailDraft = { id: "draft", version: 4, mailbox_id: "box", mode: "compose", to: ["recipient@example.com"], cc: [], subject: "Reviewed subject", body: "Reviewed content", state: "draft" };
const mailboxes = [{ id: "box", version: 1, provider: "outlook" as const, address: "owner@example.com", intake_enabled: false, active_binding: true, state: "ready" }];
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); });

async function fixture(value = draft, newDraft = false) {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  vi.spyOn(api, "emailComposeCapabilities").mockResolvedValue({ compose: true, reply: true, reply_all: true, cc: true, max_to: 100, workspace_attachments: true });
  const read = vi.spyOn(api, "emailDraft").mockResolvedValue(value);
  const save = vi.spyOn(api, "saveEmailDraft").mockResolvedValue({ ...value, version: 5 });
  const workspace: MailWorkspace = { identity: 1, enabled: true, sendEnabled: true, attachmentsEnabled: true, conversationID: "conversation", onFileSaved: vi.fn(async () => {}), download: vi.fn(async () => {}), listFiles: vi.fn(async () => [{ id: attachment.local_file_id, name: attachment.name, size: attachment.size_bytes, sha256: attachment.sha256, created_at: "today" }]) };
  const target = { mode: "compose" as const, draftId: newDraft ? undefined : "draft" };
  let closeGuard: (() => Promise<boolean>) | null = null;
  const register = (handler: (() => Promise<boolean>) | null) => { closeGuard = handler; };
  const host = document.createElement("div"); const root = createRoot(host);
  const render = async (patch: Partial<MailWorkspace> = {}) => { Object.assign(workspace, patch); await act(async () => root.render(<MailWorkspaceContext.Provider value={{ ...workspace }}><EmailCompose target={target} mailboxes={mailboxes} text={text} language="en" onClose={() => {}} onBeforeClose={register}/></MailWorkspaceContext.Provider>)); };
  await render();
  const button = (label: string) => { const found = [...host.querySelectorAll("button")].find(item => item.textContent === label); expect(found, label).toBeTruthy(); return found!; };
  const click = async (label: string) => { await act(async () => button(label).click()); };
  const input = async (label: string, value: string) => {
    const field = [...host.querySelectorAll("label")].find(item => item.firstChild?.textContent === label)?.querySelector("input,textarea") as HTMLInputElement | HTMLTextAreaElement;
    expect(field, label).toBeTruthy();
    await act(async () => { Object.getOwnPropertyDescriptor(field.tagName === "TEXTAREA" ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype, "value")!.set!.call(field, value); field.dispatchEvent(new Event("input", { bubbles: true })); });
  };
  const selectFile = async () => { await act(async () => { const select = host.querySelector<HTMLSelectElement>('[aria-label="Local workspace file"]')!; select.value = attachment.local_file_id; select.dispatchEvent(new Event("change", { bubbles: true })); }); };
  const removeFile = async (id = attachment.local_file_id) => { await act(async () => host.querySelector<HTMLButtonElement>(`[aria-label="Remove attachment ${id}"]`)!.click()); };
  return { host, root, render, button, click, input, selectFile, removeFile, save, read, close: async () => closeGuard!() };
}

it("keeps the original Send control and confirms only the exact saved local attachment version", async () => {
  const send = vi.spyOn(api, "sendEmailDraft").mockResolvedValue({ ...draft, version: 6, state: "sent", attachments: [attachment] });
  const f = await fixture();
  f.save.mockResolvedValue({ ...draft, version: 5, attachments: [attachment] });
  try {
    expect(f.host.querySelector(".emailComposer")).not.toBeNull();
    expect(f.host.querySelector('input[type="file"]')).toBeNull();
    expect(f.host.querySelector('input[aria-label="Workspace relative file path"]')).toBeNull();
    await f.selectFile(); expect(f.button(text.email.saveDraft).disabled).toBe(true); expect(f.button(text.email.send).disabled).toBe(true);
    let allowed = true; await act(async () => { allowed = await f.close(); }); expect(allowed).toBe(false);
    await f.click("Add attachment"); await f.click(text.email.send);
    expect(f.save).toHaveBeenCalledWith(expect.objectContaining({ id: "draft", expected_version: 4, attachments: [{ local_file_id: attachment.local_file_id }] }));
    const review = f.host.querySelector('[aria-label="Send confirmation"]')!;
    expect(review.textContent).toContain("Confirm sending version 5"); expect(review.textContent).toContain("8,123 B"); expect(review.textContent).toContain(attachment.sha256);
    expect(f.host.querySelector("fieldset")!.disabled).toBe(true); expect(send).not.toHaveBeenCalled();
    const confirm = f.button("Confirm sending this version"); await act(async () => { confirm.click(); confirm.click(); });
    expect(f.save).toHaveBeenCalledOnce(); expect(send).toHaveBeenCalledOnce(); expect(send).toHaveBeenCalledWith("draft", 5, expect.any(String));
    expect(f.host.textContent).toContain(text.email.sendSucceeded);
  } finally { await act(async () => f.root.unmount()); }
});

it("edits and removes attachments in the same composer, then reviews a newly saved empty manifest", async () => {
  const send = vi.spyOn(api, "sendEmailDraft").mockResolvedValue({ ...draft, version: 7, state: "sent" });
  const f = await fixture({ ...draft, attachments: [attachment] });
  try {
    expect(f.host.querySelector("fieldset")!.disabled).toBe(false);
    await f.click(text.email.send); await f.click("Edit message");
    await f.removeFile(); await f.input(text.email.subject, "Revised subject");
    f.save.mockResolvedValue({ ...draft, subject: "Revised subject", version: 6, attachments: [] });
    await f.click(text.email.send);
    expect(f.save).toHaveBeenLastCalledWith(expect.objectContaining({ expected_version: 5, subject: "Revised subject", attachments: [] }));
    expect(f.host.querySelector('[aria-label="Send confirmation"]')!.textContent).toContain("No attachments"); expect(send).not.toHaveBeenCalled();
    await f.click("Confirm sending this version"); expect(send).toHaveBeenCalledWith("draft", 6, expect.any(String));
  } finally { await act(async () => f.root.unmount()); }
});

it.each(["email_conflict", "email_attachment_changed", "email_attachment_invalid"])("requires explicit reload and fresh saved review after pre-send rejection %s", async code => {
  const send = vi.spyOn(api, "sendEmailDraft").mockRejectedValueOnce(new APIError(409, "Changed", code)).mockResolvedValue({ ...draft, version: 8, state: "sent", attachments: [attachment] });
  const f = await fixture({ ...draft, attachments: [attachment] });
  try {
    await f.click(text.email.send); await f.click("Confirm sending this version");
    expect(f.host.textContent).toContain("this attempt was not sent"); expect(f.host.textContent).not.toContain(text.email.sendUnknown);
    expect(f.button(text.email.send).disabled).toBe(true); expect(f.read).toHaveBeenCalledOnce();
    f.read.mockResolvedValue({ ...draft, version: 6, attachments: [attachment] });
    f.save.mockResolvedValue({ ...draft, version: 7, attachments: [{ ...attachment, sha256: "c".repeat(64) }] });
    await f.click("Reload draft"); await f.click(text.email.send);
    expect(send).toHaveBeenCalledOnce(); expect(f.host.querySelector('[aria-label="Send confirmation"]')!.textContent).toContain("c".repeat(64));
    await f.click("Confirm sending this version"); expect(send).toHaveBeenLastCalledWith("draft", 7, expect.any(String));
  } finally { await act(async () => f.root.unmount()); }
});

it("preserves local edits on save conflict until an explicit reload replaces them", async () => {
  const f = await fixture(); f.save.mockRejectedValue(new APIError(409, "Changed", "email_conflict"));
  try {
    await f.input(text.email.body, "Unsaved local correction"); await f.click(text.email.saveDraft);
    expect(f.host.querySelector("textarea")!.value).toBe("Unsaved local correction"); expect(f.read).toHaveBeenCalledOnce(); expect(f.button(text.email.saveDraft).disabled).toBe(true);
    f.read.mockResolvedValue({ ...draft, version: 6, body: "Server correction" }); await f.click("Reload draft");
    expect(f.host.querySelector("textarea")!.value).toBe("Server correction"); expect(f.button(text.email.saveDraft).disabled).toBe(false);
  } finally { await act(async () => f.root.unmount()); }
});

it("never unlocks an uncertain send from a stale draft read, and reconciles without resending", async () => {
  const send = vi.spyOn(api, "sendEmailDraft").mockRejectedValue(new Error("Reply lost"));
  const reconcile = vi.spyOn(api, "reconcileEmailDraft").mockResolvedValue({ ...draft, state: "unknown", attachments: [attachment] });
  const f = await fixture({ ...draft, attachments: [attachment] });
  try {
    await f.click(text.email.send); await f.click("Confirm sending this version");
    expect(f.host.textContent).toContain(text.email.sendUnknown); expect(f.read).toHaveBeenCalledOnce(); expect(f.button(text.email.send).disabled).toBe(true);
    await f.click(text.email.send); expect(send).toHaveBeenCalledOnce();
    await f.render({ sendEnabled: false }); expect(f.button(text.email.reconcileSend).disabled).toBe(true);
    await f.render({ sendEnabled: true }); await f.click(text.email.reconcileSend);
    expect(reconcile).toHaveBeenCalledWith("draft"); expect(send).toHaveBeenCalledOnce(); expect(f.host.textContent).toContain(attachment.sha256);
  } finally { await act(async () => f.root.unmount()); }
});

it("keeps a newly saved text-only draft locked when its first send response is lost", async () => {
  const send = vi.spyOn(api, "sendEmailDraft").mockRejectedValue(new Error("Reply lost"));
  const f = await fixture(draft, true);
  try {
    await f.input(text.email.to, draft.to[0]); await f.input(text.email.subject, draft.subject); await f.input(text.email.body, draft.body);
    await f.click(text.email.send);
    expect(send).toHaveBeenCalledOnce(); expect(f.read).not.toHaveBeenCalled();
    expect(f.host.textContent).toContain(text.email.sendUnknown); expect(f.button(text.email.send).disabled).toBe(true);
    await f.click(text.email.send); expect(send).toHaveBeenCalledOnce();
  } finally { await act(async () => f.root.unmount()); }
});

it("preserves unsaved text and the saved review through connection and attachment capability loss", async () => {
  const send = vi.spyOn(api, "sendEmailDraft").mockResolvedValue({ ...draft, state: "sent", version: 6, attachments: [attachment] });
  const f = await fixture({ ...draft, attachments: [attachment] });
  try {
    await f.input(text.email.body, "Retain me"); await f.render({ enabled: false });
    expect(f.host.querySelector("textarea")!.value).toBe("Retain me"); expect(f.button(text.email.saveDraft).disabled).toBe(true); expect(f.read).toHaveBeenCalledOnce();
    await f.render({ enabled: true }); expect(f.host.querySelector("textarea")!.value).toBe("Retain me");
    f.save.mockResolvedValue({ ...draft, version: 5, body: "Retain me", attachments: [attachment] }); await f.click(text.email.send);
    await f.render({ attachmentsEnabled: false }); expect(f.button("Confirm sending this version").disabled).toBe(true); expect(f.host.querySelector('[aria-label="Send confirmation"]')!.textContent).toContain("Retain me");
    await f.click("Confirm sending this version"); expect(send).not.toHaveBeenCalled();
    await f.render({ attachmentsEnabled: true }); expect(send).not.toHaveBeenCalled(); await f.click("Confirm sending this version"); expect(send).toHaveBeenCalledWith("draft", 5, expect.any(String));
  } finally { await act(async () => f.root.unmount()); }
});

it("allows explicit removal of obsolete sources without silently dropping their saved manifest", async () => {
  const f = await fixture({ ...draft, attachments: [{ ...attachment, local_file_id: undefined, path: "old/report.pdf" }] });
  const send = vi.spyOn(api, "sendEmailDraft");
  try {
    expect(f.host.textContent).toContain("old attachment source is unavailable"); expect(f.button(text.email.send).disabled).toBe(true); expect(f.button(text.email.saveDraft).disabled).toBe(true);
    await f.removeFile("legacy:old/report.pdf"); f.save.mockResolvedValue({ ...draft, version: 5, attachments: [] }); await f.click(text.email.send);
    expect(f.save).toHaveBeenCalledWith(expect.objectContaining({ attachments: [] })); expect(f.host.querySelector('[aria-label="Send confirmation"]')!.textContent).toContain("No attachments"); expect(send).not.toHaveBeenCalled();
  } finally { await act(async () => f.root.unmount()); }
});
