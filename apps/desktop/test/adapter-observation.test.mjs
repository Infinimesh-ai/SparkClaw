import assert from "node:assert/strict";
import fs from "node:fs/promises";
import net from "node:net";
import os from "node:os";
import path from "node:path";
import { EventEmitter } from "node:events";
import test from "node:test";
import { ElectronAdapterServer } from "../src/browser/adapter-server.mjs";
import { OBSERVATION_SIGNALS } from "../src/browser/page-observation.mjs";

async function fixture(t) {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), "sx-observe-"));
  const binding = { task_id: "task-a", session_id: "session_" + "b".repeat(32), controller_generation: 1, session_generation: 1, page_generation: 1 };
  const frame = { frameTreeNodeId: 1, processId: 10, routingId: 20, url: "https://example.test/",
    executeJavaScript: async () => ({ title: "Example", text: "Ready", ready_state: "complete", controls: [] }) };
  frame.framesInSubtree = [frame];
  const record = { tabID: 7, pageRef: "page_" + "c".repeat(32), documentRevision: 1,
    webContents: { mainFrame: frame, getURL: () => frame.url, isDestroyed: () => false, isLoading: () => false } };
  const connection = { id: "connection", binding, selectedTabID: 7 };
  const registry = { connections: new Map([[connection.id, connection]]), closeConnection() {},
    get(id, tab) { if (id !== connection.id || tab !== record.tabID) throw new Error("wrong page"); return record; } };
  const server = new ElectronAdapterServer({ socketPath: path.join(root, "electron-adapter.sock"), secretPath: path.join(root, "adapter-secret"),
    downloadRoot: path.join(root, "downloads"), runtimeGeneration: "a".repeat(32), registry, browserSession: new EventEmitter(), controllerIncarnation: "incarnation" });
  await server.start();
  const association = { taskID: binding.task_id, state: "connected", incarnation: "incarnation", connectionID: connection.id };
  server.controllerTasks.set(binding.task_id, association);
  t.after(async () => { await server.close(); await fs.rm(root, { recursive: true, force: true }); });
  const request = (options = {}, overrides = {}) => new Promise((resolve, reject) => {
    const socket = net.createConnection(server.socketPath); let input = "";
    socket.setEncoding("utf8"); socket.on("error", reject);
    socket.on("connect", () => socket.write(JSON.stringify({ schema_version: 1, operation: "capturePage", runtime_secret: server.runtimeSecret, binding, options, ...overrides }) + "\n"));
    socket.on("data", chunk => { input += chunk; if (input.includes("\n")) { socket.end(); resolve(JSON.parse(input)); } });
  });
  return { server, request, record, frame, connection, binding, association };
}

test("private observations require the live task binding and capture rejects same URL replacement", async t => {
  const f = await fixture(t), first = await f.request();
  assert.equal(first.status, "captured");
  for (const overrides of [{ runtime_secret: "x".repeat(43) }, { binding: { ...f.binding, session_generation: 2 } },
    { binding: { ...f.binding, task_id: "task-other" } }, { binding: { ...f.binding, script: "unexpected" } }]) {
    assert.equal((await f.request({}, overrides)).state, "rejected");
  }
  assert.equal((await f.request({ page_ref: first.page_ref, document_version: first.document_version })).status, "captured");
  f.record.documentRevision++;
  assert.equal((await f.request({ document_version: first.document_version })).state, "rejected");
  f.connection.retiring = true;
  assert.equal((await f.request()).state, "rejected");
});

test("release during local observation cannot return successful evidence", async t => {
  const f = await fixture(t), original = f.frame.executeJavaScript;
  f.frame.executeJavaScript = async () => { const result = await original(); f.association.state = "revoked"; return result; };
  const result = await f.request({ timeout_ms: 500, quiet_period_ms: 100, poll_interval_ms: 25, signals: OBSERVATION_SIGNALS }, { operation: "observePage" });
  assert.equal(result.state, "rejected");
  assert.equal(result.status, undefined);
});
