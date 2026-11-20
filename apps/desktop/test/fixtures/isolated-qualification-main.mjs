import crypto from "node:crypto";
import { getLinuxQualificationAdapter } from "../../src/main/main.mjs";

// The real forwarder prepares an association in the Electron main process.
// This parent/child channel supplies the same one-use tasks to the native
// qualification clients. It exists only in this disposable test entry point.
const incarnation = crypto.randomUUID();
let activeTaskID;
process.on("message", async (message) => {
  try {
    const adapter = getLinuxQualificationAdapter();
    if (message.action === "release") {
      if (!activeTaskID || message.taskID !== activeTaskID) throw new Error("Unexpected qualification task release");
      await adapter.revokeControllerTask(activeTaskID, "normal_release");
      activeTaskID = undefined;
      process.send({ released: true });
      return;
    }
    if (message.action !== "prepare" || activeTaskID) throw new Error("Unexpected qualification task request");
    if (!adapter.controllerIncarnation) {
      adapter.controllerIncarnation = incarnation;
      adapter.browserSession.protocol.handle("https", (request) => {
        if (request.url !== "https://popup.fixture.test/popup") return new Response("", { status: 404 });
        return new Response("<!doctype html><title>Popup</title><h1>Task popup</h1>", {
          headers: { "content-type": "text/html; charset=utf-8" },
        });
      });
    }
    const binding = {
      owner_id: "qualification-owner", client_id: "qualification-client", installation_id: "qualification-installation",
      local_conversation_id: crypto.randomUUID(), local_task_id: crypto.randomUUID(), connection_epoch: incarnation,
    };
    const task = adapter.prepareControllerTask(binding, incarnation);
    activeTaskID = task.taskID;
    process.send(task);
  } catch (error) {
    process.send({ error: error.message });
  }
});
