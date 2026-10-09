// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import { api, APIError } from "../api/client";
import type { EmailDraft } from "../api/email";
import { ISCPMailDraftPanel } from "./ISCPMailDraftPanel";

const draft: EmailDraft = { id: "draft", version: 4, mailbox_id: "box", mode: "compose", to: ["recipient@example.com"], cc: [], subject: "Reviewed subject", body: "Reviewed content", state: "draft" };
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); });
async function fixture(value = draft, attachmentsEnabled = false) {
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
  return { host, root, click, input, save, read };
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

const attachment = { path: "reports/summary.pdf", name: "summary.pdf", size_bytes: 8123, sha256: "b".repeat(64) };

it("adds only relative workspace paths and reviews the server's exact saved attachment manifest", async () => {
  const send = vi.spyOn(api, "sendEmailDraft").mockResolvedValue({ ...draft, attachments: [attachment], version: 6, state: "sent" });
  const f = await fixture(draft, true);
  f.save.mockResolvedValue({ ...draft, attachments: [attachment], version: 5 });
  try {
    expect(f.host.querySelector('input[type="file"]')).toBeNull();
    for (const invalid of ["/tmp/secret", "../outside.txt", "file:///tmp/secret", "reports/../secret", "C:\\secret.txt"]) {
      await f.input("Workspace relative file path", invalid); await f.click("Add attachment");
      expect(f.host.querySelector('[aria-label="Remove attachment ' + invalid + '"]')).toBeNull();
      expect(f.save).not.toHaveBeenCalled();
    }
    await f.input("Workspace relative file path", attachment.path);
    const reviewButton = [...f.host.querySelectorAll("button")].find(button => button.textContent === "Review and send")!;
    expect(reviewButton.disabled).toBe(true);
    await f.click("Add attachment"); await f.click("Review and send");
    expect(f.save).toHaveBeenCalledWith(expect.objectContaining({ attachments: [{ path: attachment.path }] }));
    const review = f.host.querySelector('[aria-label="Send confirmation"]')!;
    expect(review.textContent).toContain(attachment.name);
    expect(review.textContent).toContain(attachment.path);
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
    await act(async () => f.host.querySelector<HTMLButtonElement>(`[aria-label="Remove attachment ${attachment.path}"]`)!.click());
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
    await act(async () => f.host.querySelector<HTMLButtonElement>(`[aria-label="Remove attachment ${attachment.path}"]`)!.click());
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
    await f.input("Workspace relative file path", "exports/too-large.bin"); await f.click("Add attachment");
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
    expect(f.save).toHaveBeenLastCalledWith(expect.objectContaining({ expected_version: 8, attachments: [{ path: attachment.path }] }));
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
