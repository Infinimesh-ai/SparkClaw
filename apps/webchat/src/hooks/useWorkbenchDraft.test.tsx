// @vitest-environment jsdom
import { act, StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { expect, it } from "vitest";
import type { WorkbenchDraft } from "../lib/workbenchDraft";
import { useWorkbenchDraft } from "./useWorkbenchDraft";

it("keeps edits and receipts on the committed controller in StrictMode", async () => {
  let saved: WorkbenchDraft = { content: "saved draft", attachment_ids: [], revision: 1 };
  const adapter = { load: async () => saved, save: async (_id: string, value: WorkbenchDraft) => (saved = { ...value, revision: value.revision + 1 }) };
  let latest!: ReturnType<typeof useWorkbenchDraft>;
  function Harness() { latest = useWorkbenchDraft(adapter, (error) => { throw error; }); return null; }
  const root = createRoot(document.createElement("div"));
  try {
    await act(async () => root.render(<StrictMode><Harness /></StrictMode>));
    expect(latest.ready).toBe(true);
    await act(async () => { latest.content("new draft"); await latest.flush(); });
    expect(latest.draft.content).toBe("new draft"); expect(latest.status).toBe("saved");
    expect(saved.content).toBe("new draft");
  } finally { await act(async () => root.unmount()); }
});
