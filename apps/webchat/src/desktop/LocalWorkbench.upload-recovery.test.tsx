// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { LocalWorkbench } from "./LocalWorkbench";
import { api } from "../api/client";
import { LANGUAGE_STORAGE_KEY } from "../i18n";
import type { ClientStoreAPI, LocalDraft, LocalFile } from "./clientStore";
import type { SparkClawDesktop } from "./types";

beforeEach(() => {
  const values = new Map([[LANGUAGE_STORAGE_KEY, "en"]]);
  Object.defineProperty(window, "localStorage", { configurable: true, value: {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => values.set(key, value),
  } });
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  vi.stubGlobal("ResizeObserver", class { observe() {} disconnect() {} });
  vi.spyOn(api, "config").mockResolvedValue({ speech: { default_language: "auto" } } as Awaited<ReturnType<typeof api.config>>);
  vi.spyOn(api, "ready").mockResolvedValue({ ok: true } as Awaited<ReturnType<typeof api.ready>>);
  vi.spyOn(api, "owner").mockResolvedValue({ id: "owner", display_name: "Test", created_at: "", updated_at: "" });
});
afterEach(() => {
  delete window.sparkclawClientStore; delete window.sparkclawDesktop;
  document.body.replaceChildren(); vi.unstubAllGlobals(); vi.restoreAllMocks();
});

async function fixture() {
  const rows = ["existing", "other"].map(id => ({ id, title: id, created_at: "", updated_at: "" }));
  const drafts = new Map<string, LocalDraft>();
  const emptyDraft = (): LocalDraft => ({ scope_key: "scope", content: "", local_file_ids: [], revision: 0 });
  const goodFile = { id: "good", name: "good.txt", size: 4, sha256: "", created_at: "" };
  const files = new Map<string, LocalFile[]>([["existing", [goodFile]]]);
  drafts.set("existing", { ...emptyDraft(), local_file_ids: ["good"] });
  const failures = { save: false, refreshAfterSave: false, read: false };
  const listeners = new Set<() => void>();
  const create = vi.fn(async (title: string) => {
    const row = { id: "created", title, created_at: "", updated_at: "" }; rows.push(row); return row;
  });
  const saveFile = vi.fn(async (id: string, name: string, bytes: Uint8Array) => {
    if (failures.save) throw new Error("Disk full");
    const file = { id: `file-${saveFile.mock.calls.length}`, name, size: bytes.length, sha256: "", created_at: "" };
    files.set(id, [...(files.get(id) ?? []), file]);
    if (failures.refreshAfterSave) failures.read = true;
    return file;
  });
  const store = {
    schemaVersion: 1, create, saveFile,
    list: vi.fn(async () => [...rows]),
    read: vi.fn(async (id: string) => {
      if (failures.read) throw new Error("Refresh failed");
      return { messages: [], tasks: [], files: [...(files.get(id) ?? [])] };
    }),
    draft: vi.fn(async (id: string) => drafts.get(id) ?? emptyDraft()),
    saveDraft: vi.fn(async (id: string, content: string, local_file_ids: string[], revision: number) => {
      expect((drafts.get(id) ?? emptyDraft()).revision).toBe(revision);
      const draft = { scope_key: "scope", content, local_file_ids, revision: revision + 1 };
      drafts.set(id, draft); return draft;
    }),
    moveWelcomeDraft: vi.fn(async (id: string, revision: number) => {
      const value = drafts.get("") ?? emptyDraft(); expect(value.revision).toBe(revision);
      const source = { ...emptyDraft(), revision: revision + 1 }, draft = { ...value, revision: 1 };
      drafts.set("", source); drafts.set(id, draft); return { source, draft };
    }),
    onChange: (listener: () => void) => { listeners.add(listener); return () => { listeners.delete(listener); }; },
  } satisfies Partial<ClientStoreAPI>;
  window.sparkclawClientStore = store as unknown as ClientStoreAPI;
  window.sparkclawDesktop = { runtimeKind: "electron", capabilityVersion: 1,
    localConnection: vi.fn(async () => ({ schema_version: 1, state: "connected", client_id: "client", owner_id: "owner",
      backend: { schema_version: 3, transport: "iscp", deployment_id: "test", origin: "https://fixture.invalid" },
      capabilities: { surfaces: { files: { enabled: true } } } })) } as unknown as SparkClawDesktop;
  const host = document.createElement("div"); document.body.append(host); const root = createRoot(host);
  await act(async () => root.render(<LocalWorkbench />));
  const select = async (id: string) => act(async () => {
    const title = rows.find(row => row.id === id)!.title;
    [...host.querySelectorAll<HTMLButtonElement>(".sessionSelect")].find(button => button.textContent?.includes(title))!.click();
  });
  const type = async (value: string) => act(async () => {
    const input = host.querySelector("textarea")!;
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
  const upload = async (name: string) => act(async () => {
    const file = new File(["upload"], name);
    Object.defineProperty(file, "arrayBuffer", { value: async () => new Uint8Array([1, 2, 3]).buffer });
    const input = host.querySelector<HTMLInputElement>('input[type="file"]')!;
    Object.defineProperty(input, "files", { configurable: true, value: [file] });
    input.dispatchEvent(new Event("change", { bubbles: true }));
  });
  return { host, store, drafts, failures, select, type, upload, cleanup: () => act(async () => root.unmount()) };
}

it("keeps a saved attachment after a read failure without offering to upload it twice", async () => {
  const f = await fixture();
  try {
    await f.select("existing"); await f.type("Keep this draft");
    f.failures.refreshAfterSave = true;
    await f.upload("added.txt");
    expect(f.store.saveFile).toHaveBeenCalledTimes(1);
    expect(f.host.textContent).toContain("Refresh failed");
    expect(f.host.querySelector(".composerUploadFailure")).toBeNull();
    expect(f.host.querySelectorAll(".attachmentChip")).toHaveLength(2);
    expect(f.host.querySelector<HTMLTextAreaElement>("textarea")!.value).toBe("Keep this draft");
    f.failures.read = false;
    await f.select("other"); await f.select("existing");
    expect(f.store.saveFile).toHaveBeenCalledTimes(1);
    expect(f.host.querySelectorAll(".attachmentChip")).toHaveLength(2);
    expect(f.host.querySelector(".attachmentTray")?.textContent).toContain("good.txt");
    expect(f.host.querySelector(".attachmentTray")?.textContent).toContain("added.txt");
    expect(f.drafts.get("existing")).toMatchObject({ content: "Keep this draft", local_file_ids: ["good", "file-1"] });
  } finally { await f.cleanup(); }
});

it("binds a failed welcome upload to its created conversation and retries the same file with the draft intact", async () => {
  const f = await fixture();
  try {
    await f.type("Keep my welcome draft"); f.failures.save = true;
    await f.upload("failed.txt");
    expect(f.store.create).toHaveBeenCalledTimes(1);
    expect(f.store.saveFile).toHaveBeenLastCalledWith("created", "failed.txt", new Uint8Array([1, 2, 3]));
    expect(f.host.querySelector(".composerUploadFailure")?.textContent).toContain("failed.txt: Disk full");
    expect(f.host.querySelector<HTMLTextAreaElement>("textarea")!.value).toBe("Keep my welcome draft");
    await f.select("other"); expect(f.host.querySelector(".composerUploadFailure")).toBeNull();
    await f.select("created");
    expect(f.host.querySelector(".composerUploadFailure")?.textContent).toContain("failed.txt: Disk full");
    f.failures.save = false;
    await act(async () => f.host.querySelector<HTMLButtonElement>(".composerUploadFailure button")!.click());
    expect(f.store.create).toHaveBeenCalledTimes(1);
    expect(f.store.saveFile).toHaveBeenCalledTimes(2);
    expect(f.store.saveFile).toHaveBeenLastCalledWith("created", "failed.txt", new Uint8Array([1, 2, 3]));
    expect(f.host.querySelector(".composerUploadFailure")).toBeNull();
    expect(f.host.querySelectorAll(".attachmentChip")).toHaveLength(1);
    expect(f.host.querySelector(".attachmentTray")?.textContent).toContain("failed.txt");
    expect(f.host.querySelector<HTMLTextAreaElement>("textarea")!.value).toBe("Keep my welcome draft");
    await f.select("other");
    expect(f.drafts.get("created")).toMatchObject({ content: "Keep my welcome draft", local_file_ids: ["file-2"] });
    expect(f.drafts.get("")?.content).toBe("");
  } finally { await f.cleanup(); }
});
