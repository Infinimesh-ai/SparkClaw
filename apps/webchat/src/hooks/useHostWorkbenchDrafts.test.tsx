// @vitest-environment jsdom
import { act, StrictMode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, APIError, saveAPIToken } from "../api/client";
import type { WorkbenchDraft } from "../lib/workbenchDraft";
import { useHostWorkbenchDrafts } from "./useHostWorkbenchDrafts";

let latest: ReturnType<typeof useHostWorkbenchDrafts>;
const roots: Root[] = [];
const error = vi.fn();
const records = new Map<string, WorkbenchDraft>();
function Harness({ epoch = 0 }: { epoch?: number }) { latest = useHostWorkbenchDrafts(epoch, error); return null; }
async function mount() { const root = createRoot(document.createElement("div")); roots.push(root); await act(async () => root.render(<Harness />)); return root; }

beforeEach(() => {
  const values = new Map<string, string>();
  vi.stubGlobal("localStorage", { getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => values.set(key, value), removeItem: (key: string) => values.delete(key) });
  saveAPIToken("owner-one-token"); error.mockReset(); records.clear();
  vi.spyOn(api, "workbenchDraft").mockImplementation(async (id) => structuredClone(records.get(id) ?? { content: "", attachment_ids: [], revision: 0 }));
  vi.spyOn(api, "saveWorkbenchDraft").mockImplementation(async (id, value) => {
    const previous = records.get(id)?.revision ?? 0;
    if (value.revision !== previous) throw new APIError(409, "draft changed");
    const saved = { ...value, revision: previous + 1 }; records.set(id, saved); return structuredClone(saved);
  });
});
afterEach(async () => { for (const root of roots.splice(0)) await act(async () => root.unmount()); vi.restoreAllMocks(); vi.unstubAllGlobals(); });

describe("host adapter using the common draft queue", () => {
  it("keeps the selected controller active under StrictMode double initialization", async () => {
    const root = createRoot(document.createElement("div")); roots.push(root);
    await act(async () => root.render(<StrictMode><Harness /></StrictMode>));
    await act(async () => { await latest.repository.load(""); latest.setDraftsBySession({ "": "strict mode draft" }); await latest.repository.flush(""); });
    expect(latest.content[""]).toBe("strict mode draft"); expect(records.get("")?.content).toBe("strict mode draft");
  });
  it("persists text and selected file references across a presentation remount without submitting", async () => {
    const submit = vi.spyOn(api, "sendMessageStream");
    const root = await mount();
    await act(async () => { await latest.repository.load("session"); });
    await act(async () => {
      latest.setDraftsBySession({ session: "  preserved draft  " });
      latest.setAttachmentsBySession({ session: [{ name: "notes.txt", rel_path: "uploads/notes.txt" }] });
      await latest.repository.flush("session");
    });
    await act(async () => root.unmount()); roots.splice(roots.indexOf(root), 1);
    await mount();
    await act(async () => { await latest.repository.load("session"); });
    expect(latest.content.session).toBe("  preserved draft  ");
    expect(latest.files.session).toEqual([{ name: "notes.txt", rel_path: "uploads/notes.txt" }]);
    expect(submit).not.toHaveBeenCalled();
  });

  it("keeps text on failed commit and retries that same revision without execution", async () => {
    await mount(); await act(async () => { await latest.repository.load("session"); });
    vi.mocked(api.saveWorkbenchDraft).mockRejectedValueOnce(new Error("disk full"));
    await act(async () => {
      latest.setDraftsBySession({ session: "keep this draft" });
      await expect(latest.repository.flush("session")).rejects.toThrow("disk full");
    });
    expect(latest.content.session).toBe("keep this draft"); expect(latest.status.session).toBe("error");
    await act(async () => { await latest.repository.flush("session"); });
    expect(records.get("session")?.content).toBe("keep this draft"); expect(latest.status.session).toBe("saved");
  });

  it("rejects old welcome autosaves after the authenticated account changes", async () => {
    const root = await mount(); await act(async () => { await latest.repository.load(""); });
    const old = latest.repository;
    const oldSetContent = latest.setDraftsBySession;
    const oldSetFiles = latest.setAttachmentsBySession;
    await act(async () => {
      latest.setDraftsBySession({ "": "owner one private draft" });
      saveAPIToken("owner-two-token"); root.render(<Harness epoch={1} />);
    });
    await expect(old.flush("")).rejects.toThrow(/login changed/);
    expect(api.saveWorkbenchDraft).not.toHaveBeenCalled();
    expect(latest.content).toEqual({});
    await act(async () => {
      oldSetContent({ "": "old account voice callback" });
      oldSetFiles({ "": [{ name: "private.txt", rel_path: "private.txt" }] });
    });
    expect(latest.content).toEqual({}); expect(latest.files).toEqual({});
  });

  it("surfaces initial load failure and can retry without enabling editing prematurely", async () => {
    await mount(); vi.mocked(api.workbenchDraft).mockRejectedValueOnce(new Error("service offline"));
    await act(async () => { await expect(latest.repository.load("")).rejects.toThrow("service offline"); });
    expect(latest.status[""]).toBe("error"); expect(latest.loaded[""]).not.toBe(true);
    await act(async () => { await latest.repository.load(""); });
    expect(latest.loaded[""]).toBe(true); expect(latest.status[""]).toBe("saved");
  });

  it("advances the clear fence so a stale tab cannot restore sent text", async () => {
    records.set("session", { content: "submitted", attachment_ids: [], revision: 4 });
    await mount(); await act(async () => { await latest.repository.load("session"); await latest.repository.clear("session", 4); });
    expect(latest.content.session).toBe("");
    await expect(api.saveWorkbenchDraft("session", { content: "late save", attachment_ids: [], revision: 4 })).rejects.toMatchObject({ status: 409 });
    expect(records.get("session")).toEqual({ content: "", attachment_ids: [], revision: 5 });
  });
});
