// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import { api } from "../api/client";
import type { EmailDraft } from "../api/email";
import { ISCPMailDraftPanel } from "./ISCPMailDraftPanel";

const draft: EmailDraft = { id: "draft", version: 4, mailbox_id: "box", mode: "compose", to: ["recipient@example.com"], cc: [], subject: "Reviewed subject", body: "Reviewed content", state: "draft" };
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); });
async function fixture() {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  vi.spyOn(api, "emailDrafts").mockResolvedValue({ items: [draft] });
  vi.spyOn(api, "emailDraftSnapshot").mockResolvedValue(draft);
  vi.spyOn(api, "saveEmailDraftSnapshot").mockResolvedValue({ ...draft, version: 5 });
  const host = document.createElement("div"); const root = createRoot(host);
  await act(async () => root.render(<ISCPMailDraftPanel language="en" mailboxID="box" address="owner@example.com"/>));
  await act(async () => { const select = host.querySelector("select")!; select.value = "draft"; select.dispatchEvent(new Event("change", { bubbles: true })); });
  const click = async (label: string) => { const button = [...host.querySelectorAll("button")].find(button => button.textContent === label); expect(button).toBeTruthy(); await act(async () => button!.click()); };
  return { host, root, click };
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
