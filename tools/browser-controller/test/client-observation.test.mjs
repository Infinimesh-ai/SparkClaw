import assert from "node:assert/strict";
import fs from "node:fs/promises";
import net from "node:net";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { PlaywrightMCPClientFactory } from "../src/mcp-client.mjs";

async function fixture(t) {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), "sx-client-observe-"));
  const socketPath = path.join(root, "electron-adapter.sock"), secretPath = path.join(root, "adapter-secret"), logPath = path.join(root, "mcp.log");
  await fs.writeFile(secretPath, "s".repeat(43), { mode: 0o600 });
  const state = { version: "a".repeat(64), digest: "b".repeat(64), requests: [], onCapture: () => {} };
  const server = net.createServer(socket => {
    let input = ""; socket.setEncoding("utf8");
    socket.on("data", chunk => {
      input += chunk; if (!input.includes("\n")) return;
      const request = JSON.parse(input); state.requests.push(request.operation);
      assert.equal(request.runtime_secret, "s".repeat(43));
      assert.equal(request.binding.task_id, "task-test");
      let response;
      if (request.operation === "registerConnection") response = { schema_version: 1, state: "registered", runtime_kind: "electron", protocol_version: 1, connection_credential: "c".repeat(43), runtime_generation: "d".repeat(32) };
      else {
        const capture = request.operation === "capturePage";
        if (capture) state.onCapture();
        response = request.options.document_version && request.options.document_version !== state.version ? { schema_version: 1, state: "rejected" } : {
          schema_version: 1, status: capture ? "captured" : "stable", reason_code: "browser_target_settled", page_ref: "page_" + "e".repeat(32), document_version: state.version,
          url: "https://example.test/", ready_state: "complete", title: "Test", state_digest: state.digest, frame_count: 0,
        };
      }
      socket.end(JSON.stringify(response) + "\n");
    });
  });
  await new Promise(resolve => server.listen(socketPath, resolve));
  let client;
  t.after(async () => { await client?.close(); await new Promise(resolve => server.close(resolve)); await fs.rm(root, { recursive: true, force: true }); });
  const factory = new PlaywrightMCPClientFactory({ entryPoint: fileURLToPath(new URL("./fixtures/fake-mcp.mjs", import.meta.url)), cwd: root,
    outputRoot: path.join(root, "mcp-output"), electronAdapter: { socketPath, secretPath }, extraEnv: { FAKE_MCP_LOG: logPath } });
  client = await factory.open({ token: "private-test-token", taskID: "task-test", sessionID: "session_" + "f".repeat(32), controllerGeneration: 1 });
  await client.createTaskPage();
  await client.execute("page.navigate", { url: "https://example.test/" });
  const observe = () => client.execute("page.observe", { timeout_ms: 1000, quiet_period_ms: 100, poll_interval_ms: 25, signals: ["url", "ready_state", "rendered_content", "frames"] });
  const logs = async () => (await fs.readFile(logPath, "utf8")).trim().split("\n").map(JSON.parse).filter(row => row.event === "tool");
  return { client, state, observe, logs };
}

test("client returns compact local observation then one document-bound snapshot with content", async t => {
  const f = await fixture(t), before = (await f.logs()).length;
  const observed = (await f.observe()).observation;
  assert.equal(observed.status, "stable");
  assert.equal(f.state.requests.filter(op => op === "observePage").length, 1);
  assert.equal((await f.logs()).slice(before).some(row => ["browser_evaluate", "browser_snapshot"].includes(row.name)), false);
  const result = await f.client.execute("page.snapshot", { document_version: observed.document_version });
  assert.equal(result.page.document_version, observed.document_version);
  assert.equal(result.page.text, "Rendered task page text");
  assert.equal(Object.hasOwn(result.page, "html"), false);
  assert.ok(result.refs.length > 0);
  f.state.version = "0".repeat(64);
  const logCount = (await f.logs()).length;
  await assert.rejects(f.client.execute("page.click", { ref: result.refs[0] }), { code: "browser_page_stale" });
  await assert.rejects(f.client.execute("page.snapshot", { document_version: observed.document_version }), { code: "browser_page_stale" });
  assert.equal((await f.logs()).slice(logCount).some(row => ["browser_click", "browser_evaluate", "browser_snapshot"].includes(row.name)), false);
});

test("navigation during snapshot and content read rejects the completed snapshot", async t => {
  const f = await fixture(t); await f.observe(); let captures = 0;
  f.state.onCapture = () => { if (++captures === 2) f.state.version = "0".repeat(64); };
  await assert.rejects(f.client.execute("page.snapshot", { document_version: "a".repeat(64) }), { code: "browser_page_stale" });
  assert.equal(captures, 2);
  await assert.rejects(f.client.execute("page.click", { ref: "e1" }), { code: "browser_page_stale" });
});

test("closing the current Electron task tab confirms the surviving owned observation target", async t => {
  const f = await fixture(t);
  await f.client.execute("tabs.new", { url: "https://background.test/" });
  await f.client.execute("tabs.new", { url: "https://popup.test/" });
  await f.client.execute("tabs.close", { page_id: "page_2" });
  const result = await f.client.execute("tabs.close", { page_id: "page_3" });
  assert.equal(result.pages.length, 1);
  assert.equal(result.pages[0].page_id, "page_1");
  assert.equal(result.pages[0].selected, true);
  assert.equal((await f.client.execute("page.snapshot", {})).page.url, "https://example.test/");
  await f.client.execute("tabs.close", { page_id: "page_1" });
  const changes = (await f.logs()).filter(row => row.name === "browser_tabs" && ["close", "select"].includes(row.arguments.action));
  // Closing a background page needs no selection. Closing the last owned
  // page must never bring the surviving owner tab (index zero) to the front.
  assert.deepEqual(changes.map(row => [row.arguments.action, row.arguments.index]),
    [["close", 2], ["close", 2], ["select", 1], ["close", 1]]);
});
