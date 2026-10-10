// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { LocalWorkbench } from "./LocalWorkbench";
import { api } from "../api/client";
import type { SparkClawDesktop, DesktopState } from "./types";
import type { LocalApproval, LocalDraft } from "./clientStore";
import { dictionaries, LANGUAGE_STORAGE_KEY } from "../i18n";

function draftAPI() {
  const rows = new Map<string, LocalDraft>();
  const read = (id: string) => rows.get(id) ?? { scope_key: "test-scope", content: "", local_file_ids: [], revision: 0 };
  return {
    listFiles: vi.fn(async () => []),
    draft: vi.fn(async (id: string) => read(id)),
    saveDraft: vi.fn(async (id: string, content: string, local_file_ids: string[], revision: number) => {
      if (read(id).revision !== revision) throw new Error("Draft revision conflict");
      const value = { scope_key: "test-scope", content, local_file_ids, revision: revision + 1 }; rows.set(id, value); return value;
    }),
    moveWelcomeDraft: vi.fn(async (id: string, revision: number) => {
      const value = read(""); if (value.revision !== revision) throw new Error("Draft revision conflict");
      const source = { scope_key: "test-scope", content: "", local_file_ids: [], revision: revision + 1 }; const draft = { ...value, revision: 1 };
      rows.set("", source); rows.set(id, draft); return { source, draft };
    }),
    enqueueDraft: vi.fn(async (id: string, draftID: string, revision: number) => {
      const value = read(draftID); if (value.revision !== revision) throw new Error("Draft revision conflict");
      const enqueue = window.sparkclawClientStore!.enqueue;
      const task = value.local_file_ids.length ? await enqueue(id, value.content.trim(), value.local_file_ids) : await enqueue(id, value.content.trim());
      const draft = { scope_key: "test-scope", content: "", local_file_ids: [], revision: revision + 1 }; rows.set(draftID, draft); return { task, draft };
    }),
  };
}

beforeEach(() => {
  const values = new Map<string, string>();
  Object.defineProperty(window, "localStorage", { configurable: true, value: {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => values.set(key, value),
    clear: () => values.clear(),
  } });
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
});

afterEach(() => {
  delete window.sparkclawClientStore; delete window.sparkclawDesktop;
  delete window.sparkclawMailSync;
  vi.unstubAllGlobals(); vi.restoreAllMocks(); window.localStorage.clear();
});

describe("workbench local workbench", () => {
  it("LAN mail opens the original popup and enables local attachments after fresh compose capabilities", async () => {
    window.localStorage.setItem(LANGUAGE_STORAGE_KEY, "en");
    vi.stubGlobal("fetch", vi.fn(async () => new Response("{}")));
    vi.spyOn(api, "config").mockResolvedValue({ speech: { default_language: "auto" } } as Awaited<ReturnType<typeof api.config>>);
    vi.spyOn(api, "owner").mockResolvedValue({ id: "owner", display_name: "LAN owner", created_at: "today", updated_at: "today" });
    vi.spyOn(api, "emailDrafts").mockResolvedValue({ items: [] });
    let resolveCapabilities!: (value: Awaited<ReturnType<typeof api.emailComposeCapabilities>>) => void;
    const capability = vi.spyOn(api, "emailComposeCapabilities").mockImplementation(() => new Promise(resolve => { resolveCapabilities = resolve; }));
    window.sparkclawClientStore = { schemaVersion: 1, ...draftAPI(), list: vi.fn(async () => []), create: vi.fn(),
      read: vi.fn(async () => ({ messages: [], tasks: [], files: [] })), enqueue: vi.fn(), saveFile: vi.fn(), exportFile: vi.fn(),
      submit: vi.fn(), reconcile: vi.fn(), cancel: vi.fn(), decideApproval: vi.fn(), scheduleCreate: vi.fn(), scheduleCheck: vi.fn(), scheduleCancel: vi.fn(), scheduleRunNow: vi.fn() };
    vi.spyOn(api, "emailSyncStatus").mockResolvedValue({ version: 1, backlog: 0, pending_count: 0, mailboxes: [{ id: "box", version: 1, provider: "outlook", address: "owner@example.test", active_binding: true, intake_enabled: true, state: "active" }] });
    vi.spyOn(api, "emailProviders").mockResolvedValue({ providers: [] });
    vi.spyOn(api, "emailConversations").mockResolvedValue({ version: 1, conversations: [] });
    vi.spyOn(api, "emailInteractionMails").mockResolvedValue({ version: 1, messages: [] });
    window.sparkclawDesktop = { runtimeKind: "electron", capabilityVersion: 1, localConnection: vi.fn(async () => ({ schema_version: 1, state: "connected", client_id: "client", owner_id: "owner", backend: { schema_version: 2, origin: "https://backend.test", deployment_id: "deployment" } })) } as unknown as SparkClawDesktop;
    const host = document.createElement("div"), root = createRoot(host);
    try {
      await act(async () => root.render(<LocalWorkbench />));
      expect(capability).not.toHaveBeenCalled();
      await act(async () => host.querySelector<HTMLButtonElement>(".composer .emailEntryButton")!.click());
      expect(document.body.querySelector("dialog.emailPopup[open]")).not.toBeNull();
      expect(host.querySelector(".mailCachePanel")).toBeNull();
      expect([...host.querySelectorAll(".topbarActions button")].some(button => button.textContent === "Mail")).toBe(false);
      await act(async () => [...document.body.querySelectorAll<HTMLButtonElement>(".emailToolbarActions button")].find(button => button.textContent === dictionaries.en.email.compose)!.click());
      expect(capability).toHaveBeenCalledTimes(1);
      expect(document.body.querySelector('[aria-label="Local workspace file"]')).toBeNull();
      await act(async () => resolveCapabilities({ compose: true, reply: true, reply_all: true, cc: true, max_to: 100, workspace_attachments: true }));
      expect(document.body.querySelector('[aria-label="Local workspace file"]')).not.toBeNull();
      expect(host.textContent).not.toContain("ISCP connection");
    } finally { await act(async () => root.unmount()); }
  });
  it("ISCP startup reads real config, owner and readiness while gating unsupported services", async () => {
    window.localStorage.setItem(LANGUAGE_STORAGE_KEY, "en");
    const config = vi.spyOn(api, "config").mockResolvedValue({ speech: { default_language: "auto" } } as Awaited<ReturnType<typeof api.config>>);
    vi.spyOn(api, "ready").mockResolvedValue({ ok: true, workspace_root: "/test", trace_dir: "/test/traces", state_backend: "file", state_path: "/test/state", model_mode: "mock", gateway_binding: "local-test",
      speech: { enabled: false, ready: false, state: "disabled", backend: "", model: "", supports_streaming: false, accepted_content_types: [], max_audio_seconds: 0, max_upload_bytes: 0 },
      resident_services: [], credential_vault: { ready: true, state: "ready" } });
    vi.spyOn(api, "owner").mockResolvedValue({ id: "owner", display_name: "ISCP owner", created_at: "2026-10-08T00:00:00Z", updated_at: "2026-10-08T00:00:00Z" });
    const unsupported = [vi.spyOn(api, "clients"), vi.spyOn(api, "connectors"), vi.spyOn(api, "notifications"), vi.spyOn(api, "approvals"), vi.spyOn(api, "artifacts")];
    const fetch = vi.fn(() => { throw new Error("Unsupported request"); }); vi.stubGlobal("fetch", fetch);
    const row = { id: "conversation", title: "New conversation", created_at: "", updated_at: "" };
    const create = vi.fn(async () => row);
    const enqueue = vi.fn(async () => ({ id: "task", request_id: "request", status: "awaiting_runtime", created_at: "" }));
    const submit = vi.fn();
    window.sparkclawClientStore = { schemaVersion: 1, ...draftAPI(), list: vi.fn(async () => create.mock.calls.length ? [row] : []), create,
      read: vi.fn(async () => ({ messages: submit.mock.calls.length ? [{ id: "message", role: "assistant" as const, content: "ISCP response", created_at: "2026-10-08T00:00:00Z" }] : [], tasks: [], files: [] })), enqueue,
      saveFile: vi.fn(), exportFile: vi.fn(), submit, reconcile: vi.fn(), cancel: vi.fn(), decideApproval: vi.fn(),
      scheduleCreate: vi.fn(), scheduleCheck: vi.fn(), scheduleCancel: vi.fn(), scheduleRunNow: vi.fn() };
    const state = vi.fn();
    let stateChanged: (snapshot: DesktopState) => void = () => {};
    const unavailableBrowser: DesktopState = { schema_version: 1, capability_version: 1, runtime_kind: "electron", runtime_generation: "test", revision: 2,
      pages: [], downloads: [], permissions: [], browser_host: { state: "unavailable" }, presentation: { panel_bounds: { x: 0, y: 0, width: 640, height: 720 }, insufficient_space: false, presented_page_ref: "" } };
    window.sparkclawDesktop = { runtimeKind: "electron", capabilityVersion: 1, state,
      onState: (listener: typeof stateChanged) => { stateChanged = listener; return () => {}; },
      selectConversation: vi.fn(async () => { stateChanged(unavailableBrowser); return { page_ref: "" }; }),
      localConnection: vi.fn(async () => ({ schema_version: 1, state: "connected", client_id: "client", test_mode: true,
        backend: { schema_version: 3, transport: "iscp", origin: "https://iscp.invalid", deployment_id: "deployment" } })) } as unknown as SparkClawDesktop;
    const host = document.createElement("div"), root = createRoot(host);
    try {
      await act(async () => root.render(<LocalWorkbench />));
      expect(config).toHaveBeenCalledTimes(1);
      for (const spy of unsupported) expect(spy).not.toHaveBeenCalled();
      expect(fetch).not.toHaveBeenCalled(); expect(state).not.toHaveBeenCalled();
      expect(host.textContent).not.toContain("ISCP connection");
      expect(host.querySelectorAll<HTMLButtonElement>(".composer .uploadButton")).toHaveLength(2);
      for (const button of host.querySelectorAll<HTMLButtonElement>(".composer .uploadButton")) expect(button.disabled).toBe(true);
      expect(host.querySelector(".emailEntryButton")).toBeNull(); expect(host.querySelector(".rightSidebarToggle")).toBeNull();
      let input = host.querySelector<HTMLTextAreaElement>("form.composer textarea")!; expect(input.disabled).toBe(false);
      await act(async () => host.querySelector<HTMLButtonElement>(".sidebarAccountTrigger")!.click());
      const settings = [...host.querySelectorAll<HTMLButtonElement>(".sidebarAccountMenuItem")].find((button) => button.textContent === "Workspace settings")!;
      await act(async () => settings.click());
      expect(host.querySelector(".settingsPageContent")).not.toBeNull();
      expect([...host.querySelectorAll(".settingsPageNavigation button")].map(button => button.textContent?.trim())).toEqual(["Appearance", "Devices & credentials"]);
      for (const spy of unsupported) expect(spy).not.toHaveBeenCalled();
      await act(async () => host.querySelector<HTMLButtonElement>(".settingsPageBack")!.click());
      input = host.querySelector<HTMLTextAreaElement>("form.composer textarea")!;
      await act(async () => {
        Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(input, "First ISCP request");
        input.dispatchEvent(new Event("input", { bubbles: true }));
      });
      await act(async () => host.querySelector("form.composer")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })));
      expect(create).toHaveBeenCalledTimes(1);
      expect(window.sparkclawDesktop!.selectConversation).toHaveBeenCalledWith(row.id);
      expect(enqueue).toHaveBeenCalledWith(row.id, "First ISCP request");
      expect(submit).toHaveBeenCalledWith("request");
      expect(host.textContent).toContain("ISCP response");
      expect(host.querySelector("form.composer")).not.toBeNull();
      expect(fetch).not.toHaveBeenCalled();
    } finally { await act(async () => root.unmount()); }
  });
  it("uses the shared Linux conversation, mailbox and complete settings presentation", async () => {
    window.localStorage.setItem(LANGUAGE_STORAGE_KEY, "en");
    const row = { id: "conversation", title: "Parity conversation", created_at: "", updated_at: "" };
    window.sparkclawClientStore = { schemaVersion: 1, ...draftAPI(), list: vi.fn(async () => [row]), create: vi.fn(async () => row),
      read: vi.fn(async () => ({ messages: [{ id: "message", role: "assistant" as const, content: "Shared renderer", created_at: "2026-10-04T00:00:00Z" }], tasks: [], files: [] })), enqueue: vi.fn(),
      saveFile: vi.fn(), exportFile: vi.fn(), submit: vi.fn(), reconcile: vi.fn(), cancel: vi.fn(), decideApproval: vi.fn(),
      scheduleCreate: vi.fn(), scheduleCheck: vi.fn(), scheduleCancel: vi.fn(), scheduleRunNow: vi.fn() };
    window.sparkclawDesktop = { runtimeKind: "electron", capabilityVersion: 1,
      localConnection: vi.fn(async () => ({ schema_version: 1, state: "service_unavailable", client_id: "client" })) } as unknown as SparkClawDesktop;
    const host = document.createElement("div"); const root = createRoot(host);
    try {
      await act(async () => root.render(<LocalWorkbench />));
      await act(async () => host.querySelector<HTMLButtonElement>(".sessionSelect")!.click());
      expect(host.querySelector(".message.assistant .messageMeta")?.textContent).toContain("SparkX");
      expect(host.querySelectorAll(".composer .uploadButton")).toHaveLength(2);
      expect(host.querySelector(".composer .emailEntryButton")).not.toBeNull();
      expect(host.querySelector(".composer .voiceControl")).not.toBeNull();
      expect(host.querySelector<HTMLTextAreaElement>(".composer textarea")?.placeholder).toBe(dictionaries.en.chat.placeholder);
      await act(async () => host.querySelectorAll<HTMLButtonElement>(".composer .uploadButton")[1]!.click());
      expect(host.querySelector(".documentPickerOverlay")?.textContent).toContain(dictionaries.en.chat.noUploadedFiles);
      await act(async () => host.querySelector<HTMLButtonElement>(".documentPickerHeader .attachmentRemove")!.click());
      await act(async () => host.querySelector<HTMLButtonElement>(".sidebarAccountTrigger")!.click());
      const settings = [...host.querySelectorAll<HTMLButtonElement>(".sidebarAccountMenuItem")].find((button) => button.textContent === "Workspace settings")!;
      await act(async () => settings.click());
      expect([...host.querySelectorAll<HTMLButtonElement>(".settingsPageNavigation button")].map((button) => button.textContent?.trim())).toEqual([
        "General", "Appearance", "Devices & credentials", "Models & tools", "Permissions", "Connections", "Memory", "Approvals", "Timeline"
      ]);
    } finally { await act(async () => root.unmount()); }
  });

  it("uses the shared send surface without loading gateway history and preserves input on disk failure", async () => {
    window.localStorage.setItem(LANGUAGE_STORAGE_KEY, "en");
    const row = { id: "conversation", title: "Local conversation", created_at: "", updated_at: "" };
    const fetch = vi.fn(() => { throw new Error("unexpected network request"); });
    vi.stubGlobal("fetch", fetch);
    const enqueue = vi.fn().mockRejectedValueOnce(new Error("Disk full"))
      .mockResolvedValue({ id: "task", request_id: "request", status: "awaiting_runtime", created_at: "" });
    window.sparkclawClientStore = { schemaVersion: 1, ...draftAPI(), list: vi.fn(async () => [row]), create: vi.fn(async () => row),
      read: vi.fn(async () => ({ messages: [], tasks: [], files: [] })), enqueue,
      saveFile: vi.fn(), exportFile: vi.fn(), submit: vi.fn(), reconcile: vi.fn(), cancel: vi.fn(), decideApproval: vi.fn(), scheduleCreate: vi.fn(), scheduleCheck: vi.fn(), scheduleCancel: vi.fn(), scheduleRunNow: vi.fn() };
    window.sparkclawDesktop = { runtimeKind: "electron", capabilityVersion: 1,
      localConnection: vi.fn(async () => ({ schema_version: 1, state: "connected", client_id: "client" })) } as unknown as SparkClawDesktop;
    const host = document.createElement("div"); const root = createRoot(host);
    try {
      await act(async () => root.render(<LocalWorkbench />));
      const rowButton = [...host.querySelectorAll<HTMLButtonElement>("nav button")].find((button) => button.textContent?.includes(row.title))!;
      await act(async () => rowButton.click());
      const input = host.querySelector<HTMLTextAreaElement>("form.composer textarea")!;
      await act(async () => {
        Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(input, "Keep this local input");
        input.dispatchEvent(new Event("input", { bubbles: true }));
      });
      await act(async () => host.querySelector("form.composer")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })));
      expect(host.textContent).toContain("Disk full");
      expect(input.value).toBe("Keep this local input");
      await act(async () => host.querySelector("form.composer")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })));
      expect(host.textContent).not.toContain("Input saved");
      expect(host.querySelector(".localTaskList")).toBeNull();
      expect(enqueue).toHaveBeenLastCalledWith(row.id, "Keep this local input");
      expect(window.sparkclawClientStore!.submit).toHaveBeenCalledWith("request");
      expect((fetch.mock.calls as unknown[][]).some((call) => String(call[0]).includes("/api/sessions"))).toBe(false);
    } finally { await act(async () => root.unmount()); }
  });

  it("starts from the shared welcome and saves the first input locally without duplicate conversations after a disk failure", async () => {
    window.localStorage.setItem(LANGUAGE_STORAGE_KEY, "en");
    const row = { id: "conversation", title: "New conversation", created_at: "", updated_at: "" };
    const fetch = vi.fn(() => { throw new Error("unexpected network request"); });
    vi.stubGlobal("fetch", fetch);
    const create = vi.fn(async () => row);
    const enqueue = vi.fn().mockRejectedValueOnce(new Error("Disk full")).mockResolvedValue({ id: "task", request_id: "request", status: "awaiting_runtime", created_at: "" });
    const submit = vi.fn();
    window.sparkclawClientStore = { schemaVersion: 1, ...draftAPI(), list: vi.fn(async () => create.mock.calls.length ? [row] : []), create,
      read: vi.fn(async () => ({ messages: [], tasks: [], files: [] })), enqueue, submit,
      saveFile: vi.fn(), exportFile: vi.fn(), reconcile: vi.fn(), cancel: vi.fn(), decideApproval: vi.fn(),
      scheduleCreate: vi.fn(), scheduleCheck: vi.fn(), scheduleCancel: vi.fn(), scheduleRunNow: vi.fn() };
    window.sparkclawDesktop = { runtimeKind: "electron", capabilityVersion: 1,
      localConnection: vi.fn(async () => ({ schema_version: 1, state: "connected", client_id: "client" })) } as unknown as SparkClawDesktop;
    const host = document.createElement("div"); const root = createRoot(host);
    try {
      await act(async () => root.render(<LocalWorkbench />));
      expect(host.querySelector(".workbenchWelcome h1")!.textContent).toBe("What should we do?");
      expect(host.querySelector(".workbenchBrandMark")!.getAttribute("src")).toBe(host.querySelector(".welcomeBrandMark")!.getAttribute("src"));
      expect(create).not.toHaveBeenCalled();
      const input = host.querySelector<HTMLTextAreaElement>("form.composer textarea")!;
      expect(input.disabled).toBe(false);
      await act(async () => {
        Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(input, "Keep my first draft");
        input.dispatchEvent(new Event("input", { bubbles: true }));
      });
      const save = () => host.querySelector("form.composer")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
      await act(async () => { save(); });
      expect(input.value).toBe("Keep my first draft");
      expect(host.textContent).toContain("Disk full");
      await act(async () => { save(); });
      expect(create).toHaveBeenCalledTimes(1);
      expect(enqueue).toHaveBeenLastCalledWith(row.id, "Keep my first draft");
      expect(submit).toHaveBeenCalledWith("request");
      expect((fetch.mock.calls as unknown[][]).some((call) => String(call[0]).includes("/api/sessions"))).toBe(false);
    } finally { await act(async () => root.unmount()); }
  });

  it("restores durable drafts after navigation and remount, while selection flushes without sending", async () => {
    window.localStorage.setItem(LANGUAGE_STORAGE_KEY, "en");
    const rows = ["First", "Second"].map((title) => ({ id: title.toLowerCase(), title, created_at: "", updated_at: "" }));
    const drafts = draftAPI();
    await drafts.saveDraft("first", "restored draft", [], 0);
    const enqueue = vi.fn(); const submit = vi.fn();
    window.sparkclawClientStore = { schemaVersion: 1, ...drafts, list: vi.fn(async () => rows), create: vi.fn(async () => rows[0]),
      read: vi.fn(async () => ({ messages: [], tasks: [], files: [] })), enqueue, submit,
      saveFile: vi.fn(), exportFile: vi.fn(), reconcile: vi.fn(), cancel: vi.fn(), decideApproval: vi.fn(),
      scheduleCreate: vi.fn(), scheduleCheck: vi.fn(), scheduleCancel: vi.fn(), scheduleRunNow: vi.fn() };
    window.sparkclawDesktop = { runtimeKind: "electron", capabilityVersion: 1,
      localConnection: vi.fn(async () => ({ schema_version: 1, state: "connected", client_id: "client" })) } as unknown as SparkClawDesktop;
    const host = document.createElement("div"); let root = createRoot(host);
    const choose = async (title: string) => { await act(async () => [...host.querySelectorAll<HTMLButtonElement>(".sessionSelect")].find((button) => button.textContent?.includes(title))!.click()); };
    try {
      await act(async () => root.render(<LocalWorkbench />)); await choose("First");
      const input = () => host.querySelector<HTMLTextAreaElement>("form.composer textarea")!;
      expect(input().value).toBe("restored draft");
      await act(async () => {
        Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(input(), "edited draft");
        input().dispatchEvent(new Event("input", { bubbles: true }));
      });
      drafts.draft.mockRejectedValueOnce(new Error("Cannot load second draft"));
      await choose("Second");
      expect(input().value).toBe("edited draft"); expect(input().disabled).toBe(false);
      expect(host.textContent).toContain("Cannot load second draft");
      await choose("Second"); expect(input().value).toBe("");
      await choose("First"); expect(input().value).toBe("edited draft");
      await act(async () => root.unmount()); root = createRoot(host);
      await act(async () => root.render(<LocalWorkbench />)); await choose("First");
      expect(input().value).toBe("edited draft");
      expect(host.textContent).toContain("Draft saved on this device");
      expect(enqueue).not.toHaveBeenCalled(); expect(submit).not.toHaveBeenCalled();
    } finally { await act(async () => root.unmount()); }
  });

  it("fences pending welcome autosave and clears prior local content when authenticated owner changes", async () => {
    window.localStorage.setItem(LANGUAGE_STORAGE_KEY, "en");
    let owner = "first-owner";
    let changed: (status: import("./types").DesktopConnectionStatus) => void = () => {};
    const writes: string[] = [];
    const drafts = draftAPI();
    window.sparkclawClientStore = { schemaVersion: 1, ...drafts,
      draft: vi.fn(async () => ({ scope_key: owner, content: owner === "first-owner" ? "" : "new owner's draft", local_file_ids: [], revision: 0 })),
      saveDraft: vi.fn(async (_id, content, local_file_ids, revision, expectedScope) => {
        if (expectedScope !== owner) throw new Error("Draft authentication changed");
        writes.push(content); return { scope_key: owner, content, local_file_ids, revision: revision + 1 };
      }), list: vi.fn(async () => []), create: vi.fn(), read: vi.fn(async () => ({ messages: [], tasks: [], files: [] })),
      enqueue: vi.fn(), submit: vi.fn(), saveFile: vi.fn(), exportFile: vi.fn(), reconcile: vi.fn(), cancel: vi.fn(), decideApproval: vi.fn(),
      scheduleCreate: vi.fn(), scheduleCheck: vi.fn(), scheduleCancel: vi.fn(), scheduleRunNow: vi.fn() };
    window.sparkclawDesktop = { runtimeKind: "electron", capabilityVersion: 1,
      localConnection: vi.fn(async () => ({ schema_version: 1, state: "connected", owner_id: owner, client_id: "client" })),
      onLocalConnection: (listener: typeof changed) => { changed = listener; return () => {}; } } as unknown as SparkClawDesktop;
    const host = document.createElement("div"); const root = createRoot(host);
    try {
      await act(async () => root.render(<LocalWorkbench />));
      const input = host.querySelector<HTMLTextAreaElement>("form.composer textarea")!;
      await act(async () => {
        Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(input, "old private text");
        input.dispatchEvent(new Event("input", { bubbles: true }));
      });
      await act(async () => {
        owner = "second-owner";
        changed({ schema_version: 1, state: "connected", owner_id: owner, client_id: "client" });
      });
      expect(input.value).toBe("new owner's draft");
      expect(writes).toEqual([]);
      expect(window.sparkclawClientStore.submit).not.toHaveBeenCalled();
    } finally { await act(async () => root.unmount()); }
  });

  it("keeps execution records out of the conversation without replaying unresolved work", async () => {
    window.localStorage.setItem(LANGUAGE_STORAGE_KEY, "en");
    const row = { id: "conversation", title: "Local conversation", created_at: "", updated_at: "" };
    let task = { id: "task", request_id: "request", status: "awaiting_runtime", explicitly_submitted: 0, created_at: "" };
    const submit = vi.fn(async () => { task = { ...task, status: "unknown", explicitly_submitted: 1 }; throw new Error("Admission response lost"); });
    const reconcile = vi.fn(async () => task);
    let changed: () => void = () => {};
    const selectConversation = vi.fn(async () => ({ page_ref: "" }));
    window.sparkclawClientStore = { schemaVersion: 1, ...draftAPI(), list: vi.fn(async () => [row]), create: vi.fn(async () => row),
      read: vi.fn(async () => ({ messages: [{ id: "message", role: "user" as const, content: "Keep the conversation clean", created_at: "2026-10-04T00:00:00Z" }], tasks: [task], files: [] })), enqueue: vi.fn(),
      saveFile: vi.fn(), exportFile: vi.fn(), submit, reconcile, cancel: vi.fn(), decideApproval: vi.fn(), scheduleCreate: vi.fn(), scheduleCheck: vi.fn(), scheduleCancel: vi.fn(), scheduleRunNow: vi.fn() };
    window.sparkclawClientStore.onChange = (listener) => { changed = listener; return () => {}; };
    window.sparkclawDesktop = { runtimeKind: "electron", capabilityVersion: 1, selectConversation,
      localConnection: vi.fn(async () => ({ schema_version: 1, state: "connected", client_id: "client" })) } as unknown as SparkClawDesktop;
    const host = document.createElement("div"); const root = createRoot(host);
    try {
      await act(async () => root.render(<LocalWorkbench />));
      await act(async () => host.querySelector<HTMLButtonElement>("nav button")!.click());
      expect(submit).not.toHaveBeenCalled();
      expect(selectConversation).toHaveBeenCalledWith("conversation");
      expect(host.textContent).toContain("Keep the conversation clean");
      expect(host.querySelector(".localTaskList")).toBeNull();
      expect(host.textContent).not.toContain("request");
      expect(host.textContent).not.toContain("Not submitted");
      task = { ...task, status: "delivered" };
      await act(async () => changed());
      expect(host.textContent).not.toContain("Saved and acknowledged");
      expect(submit).not.toHaveBeenCalled();
      expect(reconcile).not.toHaveBeenCalled();
    } finally { await act(async () => root.unmount()); }
  });

  it("keeps local input available offline and provides reachable reconnection", async () => {
    window.localStorage.setItem(LANGUAGE_STORAGE_KEY, "en");
    const row = { id: "conversation", title: "Offline conversation", created_at: "", updated_at: "" };
    const retryLocalConnection = vi.fn(async () => ({ schema_version: 1, state: "connected", client_id: "client" }));
    window.sparkclawClientStore = { schemaVersion: 1, ...draftAPI(), list: vi.fn(async () => [row]), create: vi.fn(async () => row),
      read: vi.fn(async () => ({ messages: [], tasks: [], files: [] })), enqueue: vi.fn(),
      saveFile: vi.fn(), exportFile: vi.fn(), submit: vi.fn(), reconcile: vi.fn(), cancel: vi.fn(), decideApproval: vi.fn(), scheduleCreate: vi.fn(), scheduleCheck: vi.fn(), scheduleCancel: vi.fn(), scheduleRunNow: vi.fn() };
    window.sparkclawDesktop = { runtimeKind: "electron", capabilityVersion: 1, retryLocalConnection,
      localConnection: vi.fn(async () => ({ schema_version: 1, state: "service_unavailable", client_id: "client" })) } as unknown as SparkClawDesktop;
    const host = document.createElement("div"); const root = createRoot(host);
    try {
      await act(async () => root.render(<LocalWorkbench />));
      await act(async () => host.querySelector<HTMLButtonElement>("nav button")!.click());
      expect(host.querySelector<HTMLTextAreaElement>("form.composer textarea")!.disabled).toBe(false);
      expect(host.textContent).toContain("Connection unavailable");
      const reconnect = [...host.querySelectorAll<HTMLButtonElement>("button")].find((button) => button.textContent === "Reconnect")!;
      await act(async () => reconnect.click());
      expect(retryLocalConnection).toHaveBeenCalledTimes(1);
      expect(host.textContent).not.toContain("Connection unavailable");
    } finally { await act(async () => root.unmount()); }
  });

  it.each([0, 3600000])("persists the explicit schedule interval %i and offers new-request recovery only when missed", async (intervalMS) => {
    window.localStorage.setItem(LANGUAGE_STORAGE_KEY, "en");
    const row = { id: "conversation", title: "Scheduled conversation", created_at: "", updated_at: "" };
    let schedules: import("./clientStore").LocalSchedule[] = [];
    const scheduleCreate = vi.fn(async (_id: string, _content: string, dueAt: string) => {
      const schedule = { request_id: "schedule-request", schedule_id: "schedule-request", interval_ms: intervalMS, definition_state: "completed" as const, missed_count: 1, due_at: dueAt, state: "missed" };
      schedules = [schedule]; return schedule;
    });
    const scheduleRunNow = vi.fn(async () => { schedules = [{ ...schedules[0], state: "run_now" }]; return schedules[0]; });
    window.sparkclawClientStore = { schemaVersion: 1, ...draftAPI(), list: vi.fn(async () => [row]), create: vi.fn(async () => row),
      read: vi.fn(async () => ({ messages: [], tasks: [], files: [], schedules })), enqueue: vi.fn(),
      saveFile: vi.fn(), exportFile: vi.fn(), submit: vi.fn(), reconcile: vi.fn(), cancel: vi.fn(), decideApproval: vi.fn(),
      scheduleCreate, scheduleCheck: vi.fn(), scheduleCancel: vi.fn(), scheduleRunNow };
    window.sparkclawDesktop = { runtimeKind: "electron", capabilityVersion: 1,
      localConnection: vi.fn(async () => ({ schema_version: 1, state: "connected", client_id: "client" })) } as unknown as SparkClawDesktop;
    const host = document.createElement("div"); const root = createRoot(host);
    try {
      await act(async () => root.render(<LocalWorkbench />));
      await act(async () => host.querySelector<HTMLButtonElement>("nav button")!.click());
      await act(async () => host.querySelector<HTMLButtonElement>(".sidebarScheduleLink")!.click());
      expect(scheduleCreate).not.toHaveBeenCalled();
      await act(async () => {
        const draft = host.querySelector<HTMLTextAreaElement>("#scheduleDraft")!;
        Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(draft, "Run this once");
        draft.dispatchEvent(new Event("input", { bubbles: true }));
        const date = host.querySelector<HTMLInputElement>("#scheduleDate")!;
        Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(date, "2026-10-03T20:00");
        date.dispatchEvent(new Event("input", { bubbles: true }));
        const interval = host.querySelector<HTMLSelectElement>("#scheduleInterval")!;
        interval.value = String(intervalMS);
        interval.dispatchEvent(new Event("change", { bubbles: true }));
      });
      await act(async () => host.querySelector<HTMLFormElement>(".localSchedules form")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })));
      expect(scheduleCreate).toHaveBeenCalledWith("conversation", "Run this once", new Date("2026-10-03T20:00").toISOString(), intervalMS);
      const recover = [...host.querySelectorAll<HTMLButtonElement>("button")].find((button) => button.textContent === "Run now as a new request")!;
      await act(async () => recover.click());
      expect(scheduleRunNow).toHaveBeenCalledWith("schedule-request");
      expect(host.textContent).toContain("Started as a new request");
      expect([...host.querySelectorAll<HTMLButtonElement>("button")].some((button) => button.textContent === "Run now as a new request")).toBe(false);
    } finally { await act(async () => root.unmount()); }
  });

  it.each(["approve", "reject"] as const)("shows exact parameters and sends only the explicit %s decision while a slow receipt is pending", async (decision) => {
    window.localStorage.setItem(LANGUAGE_STORAGE_KEY, "en");
    const row = { id: "conversation", title: "Approval conversation", created_at: "", updated_at: "" };
    let approval: LocalApproval = { approval_id: "approval", digest: "a".repeat(64), tool: "browser.type", summary: "Type into the selected website field",
      arguments: { field_ref: "field", text: "<script>Synthetic text</script>" }, state: "pending", expires_at: new Date(Date.now() + 60000).toISOString(), actionable: true };
    let release: () => void = () => {};
    const receipt = new Promise<void>((resolve) => { release = resolve; });
    const decideApproval = vi.fn(async () => { await receipt; approval = { ...approval, state: decision === "approve" ? "approved" : "rejected", decision, actionable: false }; return { resolved: true as const }; });
    window.sparkclawClientStore = { schemaVersion: 1, ...draftAPI(), list: vi.fn(async () => [row]), create: vi.fn(async () => row),
      read: vi.fn(async () => ({ messages: [], tasks: [{ id: "task", request_id: "original-request", status: "running", explicitly_submitted: 1, created_at: "", approvals: [approval] }], files: [] })),
      enqueue: vi.fn(), saveFile: vi.fn(), exportFile: vi.fn(), submit: vi.fn(), reconcile: vi.fn(), cancel: vi.fn(), decideApproval,
      scheduleCreate: vi.fn(), scheduleCheck: vi.fn(), scheduleCancel: vi.fn(), scheduleRunNow: vi.fn() };
    window.sparkclawDesktop = { runtimeKind: "electron", capabilityVersion: 1,
      localConnection: vi.fn(async () => ({ schema_version: 1, state: "connected", client_id: "client" })) } as unknown as SparkClawDesktop;
    const host = document.createElement("div"); const root = createRoot(host);
    try {
      await act(async () => root.render(<LocalWorkbench />));
      await act(async () => host.querySelector<HTMLButtonElement>("nav button")!.click());
      expect(host.querySelector(".localTaskList")).toBeNull();
      expect(host.querySelector(".localApproval h3")!.textContent).toBe("browser.type");
      expect(host.querySelector(".localApproval pre")!.textContent).toContain("<script>Synthetic text</script>");
      expect(host.querySelector(".localApproval script")).toBeNull();
      expect(decideApproval).not.toHaveBeenCalled();
      const button = [...host.querySelectorAll<HTMLButtonElement>(".localApproval button")].find((item) => item.textContent === (decision === "approve" ? "Approve this action" : "Reject this action"))!;
      await act(async () => button.click());
      expect(decideApproval).toHaveBeenCalledWith("original-request", "approval", approval.digest, decision);
      expect([...host.querySelectorAll<HTMLButtonElement>(".localApproval button")].every((item) => item.disabled)).toBe(true);
      await act(async () => button.click());
      expect(decideApproval).toHaveBeenCalledTimes(1);
      await act(async () => release());
      expect(host.textContent).toContain(decision === "approve" ? "Approval accepted" : "Rejection accepted");
      expect(host.querySelectorAll(".localApproval button").length).toBe(0);
      expect(window.sparkclawClientStore!.submit).not.toHaveBeenCalled();
    } finally { release(); await act(async () => root.unmount()); }
  });

  it("keeps cached, expired and uncertain approvals read-only, with only the original saved choice available for retry", async () => {
    window.localStorage.setItem(LANGUAGE_STORAGE_KEY, "en");
    const row = { id: "conversation", title: "Cached approvals", created_at: "", updated_at: "" };
    const base: LocalApproval = { approval_id: "cached", digest: "a".repeat(64), tool: "browser.type", summary: "Cached action", arguments: { text: "Synthetic" },
      state: "pending", expires_at: new Date(Date.now() + 60000).toISOString(), actionable: false };
    const approvals: LocalApproval[] = [base, { ...base, approval_id: "expired", expires_at: new Date(Date.now() - 1).toISOString(), actionable: true },
      { ...base, approval_id: "unknown", state: "decision_unknown", decision: "approve", actionable: false },
      { ...base, approval_id: "retry", state: "decision_pending", decision: "reject", actionable: true }];
    const decideApproval = vi.fn();
    window.sparkclawClientStore = { schemaVersion: 1, ...draftAPI(), list: vi.fn(async () => [row]), create: vi.fn(async () => row),
      read: vi.fn(async () => ({ messages: [], tasks: [{ id: "task", request_id: "original-request", status: "running", explicitly_submitted: 1, created_at: "", approvals }], files: [] })),
      enqueue: vi.fn(), saveFile: vi.fn(), exportFile: vi.fn(), submit: vi.fn(), reconcile: vi.fn(), cancel: vi.fn(), decideApproval,
      scheduleCreate: vi.fn(), scheduleCheck: vi.fn(), scheduleCancel: vi.fn(), scheduleRunNow: vi.fn() };
    window.sparkclawDesktop = { runtimeKind: "electron", capabilityVersion: 1,
      localConnection: vi.fn(async () => ({ schema_version: 1, state: "connected", client_id: "client" })) } as unknown as SparkClawDesktop;
    const host = document.createElement("div"); const root = createRoot(host);
    try {
      await act(async () => root.render(<LocalWorkbench />));
      await act(async () => host.querySelector<HTMLButtonElement>("nav button")!.click());
      const sections = [...host.querySelectorAll(".localApproval")];
      expect([...sections[0].querySelectorAll<HTMLButtonElement>("button")].every((button) => button.disabled)).toBe(true);
      expect(sections[0].textContent).toContain("Refresh the current operation");
      expect(sections[1].textContent).toContain("Approval expired");
      expect(sections[1].querySelectorAll("button").length).toBe(0);
      expect(sections[2].textContent).toContain("Decision receipt is uncertain");
      expect(sections[2].querySelectorAll("button").length).toBe(0);
      expect(sections[3].querySelectorAll("button").length).toBe(1);
      const retry = sections[3].querySelector<HTMLButtonElement>("button")!;
      expect(retry.textContent).toBe("Retry original rejection");
      await act(async () => retry.click());
      expect(decideApproval).toHaveBeenCalledWith("original-request", "retry", base.digest, "reject");
      expect(decideApproval).toHaveBeenCalledTimes(1);
    } finally { await act(async () => root.unmount()); }
  });
});
