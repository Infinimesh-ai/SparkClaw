import { expect, it } from "vitest";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { WorkbenchDrafts } from "./workbenchDraft";
import { VoiceDraftPersistence } from "./voiceDraftPersistence";
import { ClientStore } from "../../../desktop/src/main/client-store.mjs";

it("recovers an applied recording after draft commit and lost ACK without inserting again", async () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "voice-draft-ack-"));
  const scope = { deployment_id: "test", owner_id: "owner", client_id: "client" };
  const requestID = "00000000-0000-4000-8000-000000000001";
  let store = new ClientStore(root);
  try {
    const conversation = store.create(scope, "Original");
    let insertions = 0;
    const restoreRenderer = async () => {
      const view = (value) => ({ content: value.content, attachment_ids: value.local_file_ids, revision: value.revision });
      const drafts = new WorkbenchDrafts({ load: async id => view(store.draft(scope, id)),
        save: async (id, value) => view(store.saveDraft(scope, id, value.content, value.attachment_ids, value.revision, value.recording_request_id)) });
      const restored = await drafts.load(conversation.id);
      const persistence = new VoiceDraftPersistence();
      return { drafts, apply: () => persistence.apply(requestID, () => {
        insertions++;
        drafts.edit(conversation.id, { content: restored.content + "transcript", attachment_ids: [] });
        drafts.markRecording(conversation.id, requestID);
        return true;
      }, () => drafts.flush(conversation.id), async () => store.recordingApplied(scope, conversation.id, requestID)) };
    };
    let renderer = await restoreRenderer();
    expect(await renderer.apply()).toBe(true);
    // The renderer dies before acknowledgeRecording reaches main. Reopen the
    // actual SQLite store and reconstruct both draft and insertion adapters.
    renderer.drafts.dispose(); store.close(); store = new ClientStore(root);
    renderer = await restoreRenderer();
    expect(await renderer.apply()).toBe(true);
    expect(insertions).toBe(1);
    expect(store.draft(scope, conversation.id).content).toBe("transcript");
    renderer.drafts.dispose();
    // Sending/clearing the draft cannot erase consumption of the old result.
    store.saveDraft(scope, conversation.id, "", [], 1);
    store.close(); store = new ClientStore(root);
    renderer = await restoreRenderer(); expect(await renderer.apply()).toBe(true);
    expect(insertions).toBe(1); expect(store.draft(scope, conversation.id).content).toBe("");
    renderer.drafts.dispose();
  } finally { store.close(); fs.rmSync(root, { recursive: true, force: true }); }
});
