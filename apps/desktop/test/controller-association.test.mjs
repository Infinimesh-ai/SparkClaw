import assert from "node:assert/strict";
import fs from "node:fs/promises";
import net from "node:net";
import path from "node:path";
import test from "node:test";
import { EventEmitter } from "node:events";
import { ElectronAdapterServer } from "../src/browser/adapter-server.mjs";
import { registerElectronConnection } from "../../../tools/browser-controller/src/electron-adapter-client.mjs";

test("production adapter consumes only main-issued task associations and revokes unused credentials", async t => {
  const root = await fs.mkdtemp(path.join(await fs.realpath("/tmp"), "sx-association-"));
  const adapter = await new ElectronAdapterServer({ socketPath: path.join(root, "electron-adapter.sock"), secretPath: path.join(root, "adapter-secret"),
    downloadRoot: path.join(root, "downloads"), runtimeGeneration: "a".repeat(32), controllerIncarnation: "b".repeat(32), registry: {}, browserSession: new EventEmitter() }).start();
  t.after(async () => { await adapter.close(); await fs.rm(root, { recursive: true, force: true }); });
  const binding = { local_conversation_id: "A", local_task_id: "task-A" };
  const local = { task_id: "caller_chosen", session_id: "session_" + "1".repeat(32), controller_generation: 1, session_generation: 1, page_generation: 1 };
  const register = (token, taskID) => registerElectronConnection({ adapter, token, binding: { ...local, task_id: taskID } });
  await assert.rejects(register("caller_secret", local.task_id), /registration failed/);
  assert.throws(() => adapter.prepareControllerTask(binding, "stale"), /unavailable/);
  const issued = adapter.prepareControllerTask(binding, "b".repeat(32));
  assert.throws(() => adapter.prepareControllerTask(binding, "b".repeat(32)), /unavailable/);
  await assert.rejects(register("wrong_token", issued.taskID), /registration failed/);
  const credential = await register(issued.token, issued.taskID);
  assert.ok(credential.credential);
  await assert.rejects(register(issued.token, issued.taskID), /registration failed/);
  adapter.revokeControllerTask(issued.taskID);
  assert.equal(adapter.registrations.size, 0);
  await assert.rejects(register(issued.token, issued.taskID), /registration failed/);
  assert.equal((await fs.stat(adapter.socketPath)).mode & 0o777, 0o600);
  // An idle local socket must not make shutdown wait for the read deadline.
  const idle = net.createConnection(adapter.socketPath);
  await new Promise(resolve => idle.once("connect", resolve));
  await adapter.close();
});
