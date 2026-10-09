// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import { api } from "../api/client";
import { LANGUAGE_STORAGE_KEY } from "../i18n";
import { LocalWorkbench } from "./LocalWorkbench";
import type { DesktopConnectionStatus, SparkClawDesktop } from "./types";

const file = { id: "12345678-1234-4123-8123-123456789abc", name: "local-private.txt", size: 10, sha256: "a".repeat(64), created_at: "today" };
const initial: DesktopConnectionStatus = {
  schema_version: 1, state: "connected", owner_id: "owner", client_id: "client", authorization_revision: 2,
  backend: { schema_version: 3, transport: "iscp", origin: "https://iscp.invalid", deployment_id: "deployment" },
  capabilities: { operations: [], files: true, mail: true, browser: false, speech: false, approvals: true, settings: false,
    surfaces: Object.fromEntries(["files", "mail_read", "mail_send", "mail_send_attachments", "mail_attachments"].map(name => [name, { enabled: true, reason: "" }])) },
};
afterEach(() => {
  delete window.sparkclawClientStore; delete window.sparkclawDesktop; delete window.sparkclawMailSync;
  vi.restoreAllMocks(); vi.unstubAllGlobals();
});

async function fixture() {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  Object.defineProperty(window, "localStorage", { configurable: true, value: { getItem: (key: string) => key === LANGUAGE_STORAGE_KEY ? "en" : null, setItem: vi.fn() } });
  vi.spyOn(api, "config").mockResolvedValue({ speech: { default_language: "auto" } } as Awaited<ReturnType<typeof api.config>>);
  vi.spyOn(api, "ready").mockResolvedValue({} as Awaited<ReturnType<typeof api.ready>>);
  vi.spyOn(api, "owner").mockResolvedValue({ id: "owner", display_name: "Owner", created_at: "", updated_at: "" });
  vi.spyOn(api, "emailDrafts").mockResolvedValue({ items: [] });
  const save = vi.spyOn(api, "saveEmailDraftSnapshot").mockImplementation(async value => ({
    id: value.id!, version: 1, mailbox_id: value.mailbox_id, mode: "compose", to: value.to, cc: value.cc,
    subject: value.subject, body: value.body, state: "draft", attachments: [{ local_file_id: file.id, name: file.name, size_bytes: file.size, sha256: file.sha256 }],
  }));
  const send = vi.spyOn(api, "sendEmailDraft");
  window.sparkclawClientStore = { schemaVersion: 1, list: vi.fn(async () => []), create: vi.fn(),
    draft: vi.fn(async () => ({ scope_key: "scope", content: "", local_file_ids: [], revision: 0 })), saveDraft: vi.fn(), moveWelcomeDraft: vi.fn(), enqueueDraft: vi.fn(),
    listFiles: vi.fn(async () => [file]), read: vi.fn(async () => ({ messages: [], tasks: [], files: [] })), enqueue: vi.fn(), saveFile: vi.fn(), exportFile: vi.fn(),
    submit: vi.fn(), reconcile: vi.fn(), cancel: vi.fn(), decideApproval: vi.fn(), scheduleCreate: vi.fn(), scheduleCheck: vi.fn(), scheduleCancel: vi.fn(), scheduleRunNow: vi.fn() };
  window.sparkclawMailSync = { catalog: vi.fn(async () => [{ id: "box", address: "owner@qq.test", provider: "qq_mail" }]),
    refreshCatalog: vi.fn(), sync: vi.fn(), read: vi.fn(async () => ({ mailbox_id: "box", sequence: 1, synced_at: "", messages: [] })) };
  let current = initial;
  let changed = (_status: DesktopConnectionStatus) => {};
  window.sparkclawDesktop = { runtimeKind: "electron", capabilityVersion: 1, localConnection: vi.fn(async () => current),
    onLocalConnection: (listener: typeof changed) => { changed = listener; return () => {}; } } as unknown as SparkClawDesktop;
  const host = document.createElement("div"), root = createRoot(host);
  const button = (label: string) => [...host.querySelectorAll<HTMLButtonElement>("button")].find(item => item.textContent === label)!;
  const click = async (label: string) => { expect(button(label)).toBeTruthy(); await act(async () => button(label).click()); };
  const field = (label: string) => host.querySelector<HTMLInputElement | HTMLTextAreaElement>(`[aria-label="${label}"]`)!;
  const input = async (label: string, text: string) => {
    const element = field(label);
    await act(async () => {
      Object.getOwnPropertyDescriptor(element instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype, "value")!.set!.call(element, text);
      element.dispatchEvent(new Event("input", { bubbles: true }));
    });
  };
  await act(async () => root.render(<LocalWorkbench />));
  await click("Mail");
  await input("To", "recipient@example.test"); await input("CC", "copy@example.test");
  await input("Subject", "Unsaved private subject"); await input("Body", "Unsaved private body");
  await act(async () => { const select = field("Local workspace file"); select.value = file.id; select.dispatchEvent(new Event("change", { bubbles: true })); });
  await click("Add attachment");
  return { host, root, field, button, click, save, send, change: async (...statuses: DesktopConnectionStatus[]) => {
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
    expect(subject.value).toBe("Unsaved private subject"); expect(f.field("Body").value).toBe("Unsaved private body");
    expect(f.host.querySelector(`[aria-label="Remove attachment ${file.id}"]`)).not.toBeNull();
    expect(f.button("Save draft").disabled).toBe(true); expect(f.button("Review and send").disabled).toBe(true);
    await f.click("Save draft"); await f.click("Review and send");
    if (transition !== "send permission loss") {
      expect(f.button("Sync mail").disabled).toBe(true); expect(f.button("Refresh mailboxes").disabled).toBe(true);
      await f.click("Sync mail"); await f.click("Refresh mailboxes");
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
  } finally { await act(async () => f.root.unmount()); }
});

it.each(["revoked", "batched revoke", "locked", "owner", "client", "deployment"])("discards the previous identity's unsaved mail on %s", async transition => {
  const f = await fixture();
  try {
    const status: DesktopConnectionStatus = transition.includes("revoke") ? { ...initial, state: "invalid_authentication" } : transition === "locked" ? { ...initial, state: "locked" } :
      transition === "owner" ? { ...initial, owner_id: "another-owner" } : transition === "client" ? { ...initial, client_id: "another-client" } : { ...initial, backend: { ...initial.backend!, deployment_id: "another-deployment" } };
    await f.change(status, ...(transition === "batched revoke" ? [initial] : []));
    expect(f.host.querySelector('[aria-label="Subject"]')).toBeNull();
    await f.change(initial);
    await f.click("Mail");
    expect(f.field("Subject").value).toBe(""); expect(f.field("Body").value).toBe(""); expect(f.field("To").value).toBe("");
    expect(f.host.querySelector(`[aria-label="Remove attachment ${file.id}"]`)).toBeNull();
    expect(f.save).not.toHaveBeenCalled(); expect(f.send).not.toHaveBeenCalled();
  } finally { await act(async () => f.root.unmount()); }
});
