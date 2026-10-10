// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import { api } from "../api/client";
import { dictionaries, LANGUAGE_STORAGE_KEY } from "../i18n";
import { LocalWorkbench } from "./LocalWorkbench";
import type { DesktopConnectionStatus, SparkClawDesktop } from "./types";

const file = { id: "12345678-1234-4123-8123-123456789abc", name: "local-private.txt", size: 10, sha256: "a".repeat(64), created_at: "today" };
const initial: DesktopConnectionStatus = {
  schema_version: 1, state: "connected", owner_id: "owner", client_id: "client", authorization_revision: 2,
  backend: { schema_version: 3, transport: "iscp", origin: "https://iscp.invalid", deployment_id: "deployment" },
  capabilities: { operations: [], files: true, mail: true, browser: false, speech: false, approvals: true, settings: false,
    surfaces: Object.fromEntries(["files", "mail_popup", "mail_read", "mail_send", "mail_send_attachments"].map(name => [name, { enabled: true, reason: "" }])) },
};
afterEach(() => {
  delete window.sparkclawClientStore; delete window.sparkclawDesktop; delete window.sparkclawMailSync;
  vi.restoreAllMocks(); vi.unstubAllGlobals();
});

async function fixture(withMessage = false) {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  Object.defineProperty(window, "localStorage", { configurable: true, value: { getItem: (key: string) => key === LANGUAGE_STORAGE_KEY ? "en" : null, setItem: vi.fn() } });
  vi.spyOn(api, "config").mockResolvedValue({ speech: { default_language: "auto" } } as Awaited<ReturnType<typeof api.config>>);
  vi.spyOn(api, "ready").mockResolvedValue({} as Awaited<ReturnType<typeof api.ready>>);
  vi.spyOn(api, "owner").mockResolvedValue({ id: "owner", display_name: "Owner", created_at: "", updated_at: "" });
  vi.spyOn(api, "emailDrafts").mockResolvedValue({ items: [] });
  const save = vi.spyOn(api, "saveEmailDraft").mockImplementation(async value => ({
    id: value.id!, version: 1, mailbox_id: value.mailbox_id, mode: "compose", to: value.to, cc: value.cc,
    subject: value.subject, body: value.body, state: "draft", attachments: [{ local_file_id: file.id, name: file.name, size_bytes: file.size, sha256: file.sha256 }],
  }));
  const send = vi.spyOn(api, "sendEmailDraft");
  vi.spyOn(api, "emailSyncStatus").mockResolvedValue({ version: 1, backlog: 0, pending_count: 0, mailboxes: [{ id: "box", version: 1, provider: "qq_mail", address: "owner@qq.test", active_binding: true, intake_enabled: true, state: "active" }] });
  vi.spyOn(api, "emailProviders").mockResolvedValue({ providers: [] });
  vi.spyOn(api, "emailConversations").mockResolvedValue({ version: 1, conversations: [] });
  vi.spyOn(api, "emailInteractionMails").mockResolvedValue({ version: 1, messages: withMessage ? [{ id: "fixture-mail", mailbox_id: "box", version: 1, receiving_address: "owner@qq.test", direction: "inbound", from: "owner@qq.test", to: [], cc: [], subject: "Fixture download", arrived_at: "2026-10-10T00:00:00Z", viewed: true, original_available: true, attachments: [{ id: "part", name: file.name, size: file.size, available: true }] }] : [] });
  vi.spyOn(api, "emailMessage").mockImplementation(async () => (await api.emailInteractionMails({})).messages![0]!);
  vi.spyOn(api, "emailComposeCapabilities").mockResolvedValue({ compose: true, reply: true, reply_all: true, cc: true, max_to: 100, workspace_attachments: true });
  window.sparkclawClientStore = { schemaVersion: 1, remove: vi.fn(), list: vi.fn(async () => withMessage ? [{ id: "local", title: "Local fixture", created_at: "", updated_at: "" }] : []), create: vi.fn(),
    draft: vi.fn(async () => ({ scope_key: "scope", content: "", local_file_ids: [], revision: 0 })), saveDraft: vi.fn(), moveWelcomeDraft: vi.fn(), enqueueDraft: vi.fn(),
    listFiles: vi.fn(async () => [file]), read: vi.fn(async () => ({ messages: [], tasks: [], files: [] })), enqueue: vi.fn(), saveFile: vi.fn(), exportFile: vi.fn(async () => ({ saved: true })),
    submit: vi.fn(), reconcile: vi.fn(), cancel: vi.fn(), decideApproval: vi.fn(), scheduleCreate: vi.fn(), scheduleCheck: vi.fn(), scheduleCancel: vi.fn(), scheduleRunNow: vi.fn() };
  window.sparkclawMailSync = { catalog: vi.fn(async () => [{ id: "box", address: "owner@qq.test", provider: "qq_mail" }]),
    refreshCatalog: vi.fn(), sync: vi.fn(), saveAttachment: vi.fn(async () => file), read: vi.fn(async () => ({ mailbox_id: "box", sequence: 1, synced_at: "", messages: [] })) };
  let current = initial;
  let changed = (_status: DesktopConnectionStatus) => {};
  window.sparkclawDesktop = { runtimeKind: "electron", capabilityVersion: 1, localConnection: vi.fn(async () => current),
    onLocalConnection: (listener: typeof changed) => { changed = listener; return () => {}; } } as unknown as SparkClawDesktop;
  const mount = document.createElement("div"); document.body.append(mount);
  const host = document.body, root = createRoot(mount);
  const button = (label: string) => [...host.querySelectorAll<HTMLButtonElement>("button")].find(item => item.textContent === label || item.getAttribute("aria-label") === label)!;
  const click = async (label: string) => { expect(button(label)).toBeTruthy(); await act(async () => button(label).click()); };
  const field = (label: string) => host.querySelector<HTMLInputElement | HTMLTextAreaElement>(`[aria-label="${label}"]`) ?? [...host.querySelectorAll(".emailComposer label")].find(item => item.firstChild?.textContent === label)?.querySelector<HTMLInputElement | HTMLTextAreaElement>("input,textarea,select")!;
  const input = async (label: string, text: string) => {
    const element = field(label);
    await act(async () => {
      Object.getOwnPropertyDescriptor(element instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype, "value")!.set!.call(element, text);
      element.dispatchEvent(new Event("input", { bubbles: true }));
    });
  };
  await act(async () => root.render(<LocalWorkbench />));
  if (withMessage) await act(async () => host.querySelector<HTMLButtonElement>(".sessionSelect")!.click());
  await click(dictionaries.en.email.title);
  await click(dictionaries.en.email.compose);
  await input("To", "recipient@example.test"); await input("CC", "copy@example.test");
  await input("Subject", "Unsaved private subject"); await input(dictionaries.en.email.body, "Unsaved private body");
  await act(async () => { const select = field("Local workspace file"); select.value = file.id; select.dispatchEvent(new Event("change", { bubbles: true })); });
  await click("Add attachment");
  return { host, root, mount, field, button, click, save, send, change: async (...statuses: DesktopConnectionStatus[]) => {
    await act(async () => { for (const status of statuses) { current = status; changed(status); } });
  } };
}

it.each(["capability refresh", "send permission loss", "disconnect"])("preserves unsaved fields and attachment IDs through %s without retaining permission", async transition => {
  const f = await fixture();
  try {
    const subject = f.field("Subject");
    const unavailable: DesktopConnectionStatus = transition === "disconnect" ? { ...initial, state: "reconnecting" } : transition === "send permission loss" ? {
      ...initial, capabilities: { ...initial.capabilities!, surfaces: { ...initial.capabilities!.surfaces, mail_send: { enabled: false, reason: "permission_missing" } } },
    } : { ...initial, capabilities: undefined };
    await f.change(unavailable);
    if (transition === "disconnect") await f.change({ ...initial, state: "service_unavailable", capabilities: undefined });
    expect(f.field("Subject")).toBe(subject);
    expect(f.field("To").value).toBe("recipient@example.test"); expect(f.field("CC").value).toBe("copy@example.test");
    expect(subject.value).toBe("Unsaved private subject"); expect(f.field(dictionaries.en.email.body).value).toBe("Unsaved private body");
    expect(f.host.querySelector(`[aria-label="Remove attachment ${file.id}"]`)).not.toBeNull();
    expect(f.button("Save draft").disabled).toBe(true); expect(f.button(dictionaries.en.email.send).disabled).toBe(true);
    await f.click("Save draft"); await f.click(dictionaries.en.email.send);
    if (transition !== "send permission loss") {
      expect(f.button(dictionaries.en.email.sync).matches(":disabled")).toBe(true);
      await f.click(dictionaries.en.email.sync);
    }
    expect(f.save).not.toHaveBeenCalled(); expect(f.send).not.toHaveBeenCalled();
    expect(window.sparkclawMailSync!.sync).not.toHaveBeenCalled(); expect(window.sparkclawMailSync!.refreshCatalog).not.toHaveBeenCalled();
    await f.change({ ...initial, authorization_revision: 3 });
    expect(f.field("Subject")).toBe(subject); expect(f.button("Save draft").disabled).toBe(false);
    expect(f.save).not.toHaveBeenCalled();
    await f.click("Save draft");
    expect(f.save).toHaveBeenCalledOnce(); expect(f.save).toHaveBeenCalledWith(expect.objectContaining({
      to: ["recipient@example.test"], cc: ["copy@example.test"], subject: "Unsaved private subject", body: "Unsaved private body", attachments: [{ local_file_id: file.id }],
    }));
    expect(f.send).not.toHaveBeenCalled();
  } finally { await act(async () => f.root.unmount()); f.mount.remove(); }
});

it.each(["revoked", "batched revoke", "locked", "owner", "client", "deployment"])("discards the previous identity's unsaved mail on %s", async transition => {
  const f = await fixture();
  try {
    const status: DesktopConnectionStatus = transition.includes("revoke") ? { ...initial, state: "invalid_authentication" } : transition === "locked" ? { ...initial, state: "locked" } :
      transition === "owner" ? { ...initial, owner_id: "another-owner" } : transition === "client" ? { ...initial, client_id: "another-client" } : { ...initial, backend: { ...initial.backend!, deployment_id: "another-deployment" } };
    await f.change(status, ...(transition === "batched revoke" ? [initial] : []));
    expect(f.field("Subject") ?? null).toBeNull();
    await f.change(initial);
    await f.click(dictionaries.en.email.title);
    await f.click(dictionaries.en.email.compose);
    expect(f.field("Subject").value).toBe(""); expect(f.field(dictionaries.en.email.body).value).toBe(""); expect(f.field("To").value).toBe("");
    expect(f.host.querySelector(`[aria-label="Remove attachment ${file.id}"]`)).toBeNull();
    expect(f.save).not.toHaveBeenCalled(); expect(f.send).not.toHaveBeenCalled();
  } finally { await act(async () => f.root.unmount()); f.mount.remove(); }
});


it("downloads through the current popup capability without requiring the retired cache operation", async () => {
  const f = await fixture(true);
  try {
    await act(async () => f.host.querySelector<HTMLButtonElement>(".emailComposer header button")!.click());
    await act(async () => f.host.querySelector<HTMLButtonElement>(".emailConversationRow")!.click());
    await f.click(dictionaries.en.email.originalDownload);
    await act(async () => [...f.host.querySelectorAll<HTMLButtonElement>(".emailMessageActions button")].find(button => button.textContent?.includes(file.name))!.click());
    expect(window.sparkclawMailSync!.saveAttachment).toHaveBeenNthCalledWith(1, "box", "fixture-mail", "", "local");
    expect(window.sparkclawMailSync!.saveAttachment).toHaveBeenNthCalledWith(2, "box", "fixture-mail", "part", "local");
    expect(window.sparkclawClientStore!.exportFile).toHaveBeenCalledWith(file.id);
    expect(f.send).not.toHaveBeenCalled();
  } finally { await act(async () => f.root.unmount()); f.mount.remove(); }
});
