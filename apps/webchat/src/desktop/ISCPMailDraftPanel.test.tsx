// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import { api, APIError } from "../api/client";
import type { EmailDraft } from "../api/email";
import { ISCPMailDraftPanel } from "./ISCPMailDraftPanel";

const draft: EmailDraft = { id: "draft", version: 4, mailbox_id: "box", mode: "compose", to: ["recipient@example.com"], cc: [], subject: "Reviewed subject", body: "Reviewed content", state: "draft" };
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); delete window.sparkclawClientStore; });
async function fixture(value = draft, attachmentsEnabled = false) {
  Object.defineProperty(window, "sparkclawClientStore", { configurable: true, value: { listFiles: vi.fn(async () => [{ id: attachment.local_file_id, name: attachment.name, size: attachment.size_bytes, sha256: attachment.sha256, created_at: "today" }]) } });
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  vi.spyOn(api, "emailDrafts").mockResolvedValue({ items: [value] });
  const read = vi.spyOn(api, "emailDraftSnapshot").mockResolvedValue(value);
  const save = vi.spyOn(api, "saveEmailDraftSnapshot").mockResolvedValue({ ...value, version: 5 });
  const host = document.createElement("div"); const root = createRoot(host);
  await act(async () => root.render(<ISCPMailDraftPanel language="en" mailboxID="box" address="owner@example.com" attachmentsEnabled={attachmentsEnabled}/>));
  await act(async () => { const select = host.querySelector("select")!; select.value = "draft"; select.dispatchEvent(new Event("change", { bubbles: true })); });
  const click = async (label: string) => { const button = [...host.querySelectorAll("button")].find(button => button.textContent === label); expect(button).toBeTruthy(); await act(async () => button!.click()); };
  const input = async (label: string, text: string) => {
    const element = host.querySelector<HTMLInputElement>(`input[aria-label="${label}"]`)!;
    expect(element).toBeTruthy();
    await act(async () => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(element, text); element.dispatchEvent(new Event("input", { bubbles: true })); });
  };
  const selectFile = async () => { await act(async () => { const select = host.querySelector<HTMLSelectElement>('[aria-label="Local workspace file"]')!; select.value = attachment.local_file_id; select.dispatchEvent(new Event("change", { bubbles: true })); }); };
  return { host, root, click, input, selectFile, save, read };
}

it("requires confirmation of an immutable saved version and presents the provider receipt", async () => {
  const send = vi.spyOn(api, "sendEmailDraft").mockResolvedValue({ ...draft, version: 6, state: "sent", confirmation_source: "provider_receipt", receipt: { provider: "gmail", status: "sent", recipient_digest: "digest", provider_message_id: "provider-id" } });
  const forbidden = vi.spyOn(api, "emailComposeCapabilities"); const legacyRead = vi.spyOn(api, "emailDraft");
  const f = await fixture();
  try {
    await f.click("Review and send");
    expect(send).not.toHaveBeenCalled();
    expect(f.host.textContent).toContain("Confirm sending version 5");
    expect(f.host.querySelector("fieldset")!.disabled).toBe(true);
    await f.click("Edit message");
    expect(f.host.querySelector("fieldset")!.disabled).toBe(false);
    await f.click("Review and send"); await f.click("Confirm sending this version");
    expect(send).toHaveBeenCalledOnce(); expect(send).toHaveBeenCalledWith("draft", 5, expect.any(String));
    expect(f.host.textContent).toContain("provider-id");
    expect(forbidden).not.toHaveBeenCalled(); expect(legacyRead).not.toHaveBeenCalled();
  } finally { await act(async () => f.root.unmount()); }
});

const attachment = { local_file_id: "12345678-1234-4123-8123-123456789abc", name: "summary.pdf", size_bytes: 8123, sha256: "b".repeat(64) };

it("retains a reviewed version while disabling sends and reconciliation until permission recovers", async () => {
  const send = vi.spyOn(api, "sendEmailDraft").mockRejectedValue(new Error("Reply lost"));
  const reconcile = vi.spyOn(api, "reconcileEmailDraft").mockResolvedValue({ ...draft, state: "unknown" });
  const f = await fixture();
  const enabled = async (value: boolean) => { await act(async () => f.root.render(<ISCPMailDraftPanel language="en" mailboxID="box" address="owner@example.com" enabled={value}/>)); };
  const button = (label: string) => [...f.host.querySelectorAll("button")].find(item => item.textContent === label)!;
  try {
    await f.click("Review and send");
    await enabled(false);
    expect(f.host.querySelector('[aria-label="Send confirmation"]')!.textContent).toContain("Confirm sending version 5");
    expect(button("Confirm sending this version").disabled).toBe(true);
    await f.click("Confirm sending this version"); expect(send).not.toHaveBeenCalled();
    await enabled(true);
    expect(send).not.toHaveBeenCalled();
    await f.click("Confirm sending this version"); expect(send).toHaveBeenCalledOnce();
    await enabled(false);
    expect(button("Reconcile send outcome").disabled).toBe(true);
    await f.click("Reconcile send outcome"); expect(reconcile).not.toHaveBeenCalled();
    await enabled(true); await f.click("Reconcile send outcome");
    expect(reconcile).toHaveBeenCalledWith("draft"); expect(send).toHaveBeenCalledOnce();
  } finally { await act(async () => f.root.unmount()); }
});

it("selects only existing local files and reviews the exact saved attachment manifest", async () => {
  const send = vi.spyOn(api, "sendEmailDraft").mockResolvedValue({ ...draft, attachments: [attachment], version: 6, state: "sent" });
  const f = await fixture(draft, true);
  f.save.mockResolvedValue({ ...draft, attachments: [attachment], version: 5 });
  try {
    expect(f.host.querySelector('input[type="file"]')).toBeNull();
    expect(f.host.querySelector('input[aria-label="Workspace relative file path"]')).toBeNull();
    expect(f.host.querySelector('[aria-label="Local workspace file"]')!.textContent).toContain(attachment.name);
    await f.selectFile();
    const reviewButton = [...f.host.querySelectorAll("button")].find(button => button.textContent === "Review and send")!;
    expect(reviewButton.disabled).toBe(true);
    await f.click("Add attachment"); await f.click("Review and send");
    expect(f.save).toHaveBeenCalledWith(expect.objectContaining({ attachments: [{ local_file_id: attachment.local_file_id }] }));
    const review = f.host.querySelector('[aria-label="Send confirmation"]')!;
    expect(review.textContent).toContain(attachment.name);
    expect(review.textContent).not.toContain("Gateway workspace");
    expect(review.textContent).toContain("8,123 B");
    expect(review.textContent).toContain(attachment.sha256);
    expect(send).not.toHaveBeenCalled();
    await f.click("Confirm sending this version");
    expect(send).toHaveBeenCalledWith("draft", 5, expect.any(String));
  } finally { await act(async () => f.root.unmount()); }
});

it("removing an attachment requires a newly saved version and a new explicit confirmation", async () => {
  const send = vi.spyOn(api, "sendEmailDraft").mockResolvedValue({ ...draft, version: 7, state: "sent" });
  const f = await fixture({ ...draft, attachments: [attachment] }, true);
  try {
    await f.click("Review and send"); await f.click("Edit message");
    await act(async () => f.host.querySelector<HTMLButtonElement>(`[aria-label="Remove attachment ${attachment.local_file_id}"]`)!.click());
    expect(f.host.querySelector('[aria-label="Send confirmation"]')).toBeNull();
    f.save.mockResolvedValue({ ...draft, version: 6, attachments: [] });
    await f.click("Review and send");
    expect(f.save).toHaveBeenLastCalledWith(expect.objectContaining({ expected_version: 5, attachments: [] }));
    expect(f.host.querySelector('[aria-label="Send confirmation"]')!.textContent).toContain("No attachments");
    await f.click("Confirm sending this version");
    expect(send).toHaveBeenCalledWith("draft", 6, expect.any(String));
  } finally { await act(async () => f.root.unmount()); }
});

it.each(["email_conflict", "email_attachment_changed", "email_attachment_invalid"])("requires reload and review after a known pre-send rejection: %s", async code => {
  const send = vi.spyOn(api, "sendEmailDraft").mockRejectedValueOnce(new APIError(409, "Changed before send", code)).mockResolvedValue({ ...draft, version: 8, state: "sent", attachments: [attachment] });
  const f = await fixture({ ...draft, attachments: [attachment] }, true);
  try {
    await f.click("Review and send"); await f.click("Confirm sending this version");
    expect(f.host.textContent).toContain("this attempt was not sent");
    expect(f.host.textContent).not.toContain("Send outcome is unknown");
    expect([...f.host.querySelectorAll("button")].find(button => button.textContent === "Review and send")!.disabled).toBe(true);
    f.read.mockResolvedValue({ ...draft, version: 6, attachments: [attachment] });
    f.save.mockResolvedValue({ ...draft, version: 7, attachments: [{ ...attachment, sha256: "c".repeat(64) }] });
    await f.click("Reload draft"); await f.click("Review and send");
    expect(send).toHaveBeenCalledOnce();
    expect(f.host.querySelector('[aria-label="Send confirmation"]')!.textContent).toContain("c".repeat(64));
    await f.click("Confirm sending this version");
    expect(send).toHaveBeenLastCalledWith("draft", 7, expect.any(String));
  } finally { await act(async () => f.root.unmount()); }
});

it("blocks attachment sending when its individual capability is unavailable and allows explicit removal", async () => {
  const f = await fixture({ ...draft, attachments: [attachment] });
  try {
    expect(f.host.querySelector('[aria-label="Workspace relative file path"]')).toBeNull();
    expect([...f.host.querySelectorAll("button")].find(button => button.textContent === "Review and send")!.disabled).toBe(true);
    await act(async () => f.host.querySelector<HTMLButtonElement>(`[aria-label="Remove attachment ${attachment.local_file_id}"]`)!.click());
    f.save.mockResolvedValue({ ...draft, version: 5, attachments: [] });
    await f.click("Review and send");
    expect(f.save).toHaveBeenLastCalledWith(expect.objectContaining({ attachments: [] }));
    expect(f.host.querySelector('[aria-label="Send confirmation"]')!.textContent).toContain("No attachments");
  } finally { await act(async () => f.root.unmount()); }
});

it("retains the original attachment manifest when a missing send reply is reconciled", async () => {
  const value = { ...draft, attachments: [attachment] };
  const send = vi.spyOn(api, "sendEmailDraft").mockRejectedValue(new Error("Reply lost"));
  vi.spyOn(api, "reconcileEmailDraft").mockResolvedValue({ ...value, state: "unknown" });
  const f = await fixture(value, true);
  try {
    await f.click("Review and send"); await f.click("Confirm sending this version");
    expect(f.host.querySelector('[aria-label="Saved attachments"]')!.textContent).toContain(attachment.sha256);
    await f.click("Reconcile send outcome");
    expect(send).toHaveBeenCalledOnce();
    expect(f.host.querySelector("fieldset")!.disabled).toBe(true);
    expect(f.host.querySelector('[aria-label="Saved attachments"]')!.textContent).toContain(attachment.sha256);
  } finally { await act(async () => f.root.unmount()); }
});

it("keeps an over-limit or inaccessible attachment out of the confirmation when the server rejects saving", async () => {
  const send = vi.spyOn(api, "sendEmailDraft");
  const f = await fixture(draft, true);
  try {
    await f.selectFile(); await f.click("Add attachment");
    f.save.mockRejectedValue(new APIError(400, "Attachment byte limit exceeded", "email_attachment_invalid"));
    await f.click("Review and send");
    expect(f.host.querySelector('[aria-label="Send confirmation"]')).toBeNull();
    expect(f.host.textContent).toContain("Attachment byte limit exceeded");
    expect(send).not.toHaveBeenCalled();
    expect(f.host.querySelector("fieldset")!.disabled).toBe(false);
  } finally { await act(async () => f.root.unmount()); }
});

it("reloads a changed draft version before another save can produce a send confirmation", async () => {
  const f = await fixture(draft, true);
  try {
    f.save.mockRejectedValueOnce(new APIError(409, "Draft version changed", "email_conflict"));
    await f.click("Review and send");
    expect(f.host.querySelector('[aria-label="Send confirmation"]')).toBeNull();
    expect([...f.host.querySelectorAll("button")].find(button => button.textContent === "Review and send")!.disabled).toBe(true);
    f.read.mockResolvedValue({ ...draft, version: 8, attachments: [attachment] });
    f.save.mockResolvedValue({ ...draft, version: 9, attachments: [attachment] });
    await f.click("Reload draft"); await f.click("Review and send");
    expect(f.save).toHaveBeenLastCalledWith(expect.objectContaining({ expected_version: 8, attachments: [{ local_file_id: attachment.local_file_id }] }));
    expect(f.host.querySelector('[aria-label="Send confirmation"]')!.textContent).toContain("version 9");
  } finally { await act(async () => f.root.unmount()); }
});

it("locks an unknown send and only reconciles the original draft without sending twice", async () => {
  const send = vi.spyOn(api, "sendEmailDraft").mockRejectedValue(new Error("Connection lost"));
  const reconcile = vi.spyOn(api, "reconcileEmailDraft").mockResolvedValue({ ...draft, state: "unknown" });
  const f = await fixture();
  try {
    await f.click("Review and send"); await f.click("Confirm sending this version");
    expect(f.host.textContent).toContain("Send outcome is unknown");
    expect([...f.host.querySelectorAll("button")].some(button => button.textContent === "Review and send")).toBe(false);
    await f.click("Reconcile send outcome");
    expect(reconcile).toHaveBeenCalledWith("draft"); expect(send).toHaveBeenCalledOnce();
    expect(f.host.querySelector("fieldset")!.disabled).toBe(true);
  } finally { await act(async () => f.root.unmount()); }
});

it("refreshes the owned local inventory without discarding the mail draft or selecting an external path", async () => {
  const f = await fixture(draft, true);
  try {
    const second = { id: "87654321-4321-4321-8321-123456789abc", name: "new-local.txt", size: 20, sha256: "d".repeat(64), created_at: "today" };
    vi.mocked(window.sparkclawClientStore!.listFiles).mockResolvedValue([second]);
    await f.click("Refresh local files");
    const select = f.host.querySelector<HTMLSelectElement>('[aria-label="Local workspace file"]')!;
    expect(select.textContent).toContain("new-local.txt"); expect(select.textContent).not.toContain(attachment.name);
    expect(f.host.querySelector<HTMLInputElement>('[aria-label="Subject"]')!.value).toBe(draft.subject);
    await act(async () => { select.value = second.id; select.dispatchEvent(new Event("change", { bubbles: true })); });
    await f.click("Add attachment"); await f.click("Save draft");
    expect(f.save).toHaveBeenCalledWith(expect.objectContaining({ attachments: [{ local_file_id: second.id }] }));
  } finally { await act(async () => f.root.unmount()); }
});

it("does not turn legacy Gateway paths into local sources and permits explicit removal", async () => {
  const legacy = { path: "gateway/report.txt", name: "report.txt", size_bytes: 1, sha256: "a".repeat(64) };
  const f = await fixture({ ...draft, attachments: [legacy] }, true);
  try {
    expect(f.host.textContent).toContain("This old attachment source is unavailable");
    expect(f.host.querySelector<HTMLInputElement>('input[aria-label="Workspace relative file path"]')).toBeNull();
    await act(async () => f.host.querySelector<HTMLButtonElement>('[aria-label="Remove attachment legacy:gateway/report.txt"]')!.click());
    f.save.mockResolvedValue({ ...draft, attachments: [], version: 5 });
    await f.click("Review and send");
    expect(f.save).toHaveBeenCalledWith(expect.objectContaining({ attachments: [] }));
  } finally { await act(async () => f.root.unmount()); }
});
