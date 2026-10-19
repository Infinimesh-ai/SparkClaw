import { expect, it, vi } from "vitest";
import { WorkbenchDrafts, type WorkbenchDraft } from "./workbenchDraft";
import { VoiceDraftPersistence } from "./voiceDraftPersistence";

it("retries the original draft save without repeating an already visible transcript", async () => {
  let visible = "original ";
  const save = vi.fn().mockRejectedValueOnce(new Error("disk full"))
    .mockImplementation(async (_id: string, value: WorkbenchDraft) => ({ ...value, revision: value.revision + 1 }));
  const drafts = new WorkbenchDrafts({ load: async () => ({ content: visible, attachment_ids: [], revision: 0 }), save },
    { onChange: (_id, value) => { visible = value.content; } });
  await drafts.load("original-session");
  const persistence = new VoiceDraftPersistence();
  const insert = vi.fn(() => { drafts.edit("original-session", { content: visible + "transcript", attachment_ids: [] }); return true; });
  await expect(persistence.apply("original-recording", insert, () => drafts.flush("original-session"))).rejects.toThrow("disk full");
  expect(visible).toBe("original transcript");
  // Subsequent typing or a selected-session change must not redirect the save
  // or replay insertion. The original callback owns that draft until ACK.
  drafts.edit("original-session", { content: visible + " and edited", attachment_ids: [] });
  const wrongSession = vi.fn(async () => { throw new Error("wrong session"); });
  await expect(persistence.apply("original-recording", insert, wrongSession)).resolves.toBe(true);
  expect(visible).toBe("original transcript and edited");
  expect(insert).toHaveBeenCalledOnce(); expect(wrongSession).not.toHaveBeenCalled();
  expect(save.mock.calls[1][0]).toBe("original-session");
  expect((await drafts.load("original-session")).revision).toBe(1);
  drafts.dispose();
});
