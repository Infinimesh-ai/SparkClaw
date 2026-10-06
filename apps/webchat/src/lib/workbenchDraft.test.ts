import { afterEach, describe, expect, it, vi } from "vitest";
import { WorkbenchDrafts, type WorkbenchDraft } from "./workbenchDraft";
const empty: WorkbenchDraft = { content: "", attachment_ids: [], revision: 0 };
afterEach(() => vi.useRealTimers());

describe("workbench draft queue", () => {
  it("debounces edits and serializes a newer edit behind the in-flight CAS save", async () => {
    vi.useFakeTimers();
    let release: (value: WorkbenchDraft) => void = () => {};
    const first = new Promise<WorkbenchDraft>((resolve) => { release = resolve; });
    const save = vi.fn().mockReturnValueOnce(first).mockImplementation(async (_id: string, value: WorkbenchDraft) => ({ ...value, revision: value.revision + 1 }));
    const drafts = new WorkbenchDrafts({ load: async () => empty, save });
    await drafts.load("one");
    drafts.edit("one", { content: "first", attachment_ids: [] });
    await vi.advanceTimersByTimeAsync(250); expect(save).toHaveBeenCalledTimes(1);
    drafts.edit("one", { content: "newer", attachment_ids: ["file"] });
    const flushed = drafts.flush("one"); release({ content: "first", attachment_ids: [], revision: 1 });
    expect(await flushed).toEqual({ content: "newer", attachment_ids: ["file"], revision: 2 });
    expect(save.mock.calls[1]).toEqual(["one", { content: "newer", attachment_ids: ["file"], revision: 1 }]);
    drafts.dispose();
  });

  it("a disk or CAS failure leaves visible edits unsaved and explicit flush can retry", async () => {
    const changed = vi.fn();
    const save = vi.fn().mockRejectedValueOnce(new Error("disk full")).mockImplementation(async (_id: string, value: WorkbenchDraft) => ({ ...value, revision: value.revision + 1 }));
    const drafts = new WorkbenchDrafts({ load: async () => empty, save }, { onChange: changed });
    await drafts.load("one"); drafts.edit("one", { content: "keep me", attachment_ids: [] });
    await expect(drafts.flush("one")).rejects.toThrow("disk full");
    expect(changed.mock.calls.at(-1)?.[2]).toBe("error");
    expect((await drafts.load("one")).content).toBe("keep me");
    expect((await drafts.flush("one")).revision).toBe(1);
    drafts.dispose();
  });

  it("accepting the atomically consumed revision fences a late old save receipt", async () => {
    let release: (value: WorkbenchDraft) => void = () => {};
    const drafts = new WorkbenchDrafts({ load: async () => empty,
      save: () => new Promise((resolve) => { release = resolve; }) });
    await drafts.load("one"); drafts.edit("one", { content: "sent", attachment_ids: [] });
    const pending = drafts.flush("one");
    drafts.accept("one", { ...empty, revision: 2 });
    release({ content: "sent", attachment_ids: [], revision: 1 });
    expect(await pending).toEqual({ ...empty, revision: 2 });
    expect(await drafts.load("one")).toEqual({ ...empty, revision: 2 });
    drafts.dispose();
  });

  it("keeps independent conversation drafts and loads each key once", async () => {
    const load = vi.fn(async (id: string) => ({ ...empty, content: id }));
    const drafts = new WorkbenchDrafts({ load, save: async (_id, value) => ({ ...value, revision: value.revision + 1 }) });
    await Promise.all([drafts.load("one"), drafts.load("one")]);
    drafts.edit("one", { content: "one changed", attachment_ids: [] }); await drafts.flush("one");
    expect((await drafts.load("two")).content).toBe("two");
    expect((await drafts.load("one")).content).toBe("one changed");
    expect(load).toHaveBeenCalledTimes(2); drafts.dispose();
  });
});
