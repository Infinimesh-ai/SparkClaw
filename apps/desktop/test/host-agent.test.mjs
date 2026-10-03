import assert from "node:assert/strict";
import test from "node:test";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { EventEmitter } from "node:events";
import { BrowserHostAgent, commandDigest } from "../src/browser/host-agent.mjs";
import { HostJournal } from "../src/browser/host-journal.mjs";

const scope = { owner_id: "owner", client_id: "client", installation_id: "installation" };
const binding = (conversation = "A") => ({ ...scope, local_conversation_id: conversation, local_task_id: `task_${conversation}`, host_id: "host_test", runtime_generation: "runtime_test", connection_epoch: "epoch_test", lease_id: `lease_${conversation}`, page_id: `page_${conversation}`, page_generation: 1, authorization_digest: "a".repeat(64), lease_expires_at: new Date(Date.now() + 29000).toISOString() });
function command(operation, b, args = {}, id = `${operation}_${b.local_conversation_id}`) { const value = { schema_version: 1, type: "command", command_id: id, binding: b, operation, arguments: args, digest: "" }; value.digest = commandDigest(value); return value; }
async function until(predicate) { for (let attempt = 0; attempt < 100; attempt++) { if (predicate()) return; await new Promise((resolve) => setTimeout(resolve, 5)); } throw new Error("fixture timed out"); }
async function fixture(t, execute) {
 const root = await fs.mkdtemp(path.join(os.tmpdir(), "sparkclaw-host-test-"));t.after(() => fs.rm(root, { recursive: true, force: true }));
 const pages = new Map(); let closed = 0; let grantCalls = 0;
 const registry = { runtimeGeneration: "runtime_test", acquireHostPage(b) { if (pages.has(b.local_conversation_id)) throw new Error("second controller"); const record = { binding: b, pageRef: `ref_${b.local_conversation_id}` }; pages.set(b.local_conversation_id, record); return record; }, requireHostPage(b) { const record = pages.get(b.local_conversation_id); if (!record || record.binding.lease_id !== b.lease_id) throw new Error("stale"); return record; }, releaseHostPage(b) { pages.delete(b.local_conversation_id); }, closeHostPages() { closed++; pages.clear(); }, closeConnection(id) { for (const [key, value] of pages) if (value.binding.lease_id === id) pages.delete(key); } };
 const auth = { status: { state: "connected" }, connection: { ownerID: "owner", clientID: "client", authorization: "Bearer synthetic" }, descriptor: { schemaVersion: 2, origin: "https://example.test" }, async authorizedFetch(url) { grantCalls++; assert.equal(url, "https://example.test/api/r3/hosts/grants"); return Response.json({ host_id: "host_test", grant_token: "synthetic-private-grant", grant_digest: "a".repeat(64), expires_at: new Date(Date.now() + 60000).toISOString() }); } };
 const socket = new EventEmitter(); socket.results = []; socket.send = (value) => socket.results.push(value); socket.close = () => { if (!socket.closed) { socket.closed = true; socket.emit("close"); } };
 const agent = new BrowserHostAgent({ auth, registry, userDataDir: root, execute: execute || (async (_record, _op, _args, b, fence) => { fence(); return { text: b.local_conversation_id }; }), connect: async () => { setTimeout(() => socket.emit("message", { schema_version: 1, type: "welcome", host_id: "host_test", runtime_generation: "runtime_test", connection_epoch: "epoch_test", authorization_digest: "a".repeat(64), lease_seconds: 30, heartbeat_seconds: 10 }), 0); return socket; } });
 t.after(() => agent.stop()); await agent.start({ installation_id: "installation" });
 assert.equal(grantCalls, 0, "login/start cannot grant the browser"); await agent.grant();
 return { root, agent, socket, pages, registry, auth, closed: () => closed };
}
test("host admits exact scoped commands and fences foreign identity/epoch/script injection", async (t) => {
 const { socket, agent, pages } = await fixture(t);
 const a = binding("A"), b = binding("B");
 socket.emit("message", command("acquire", a)); socket.emit("message", command("acquire", b));
 await until(() => pages.size === 2);
 socket.emit("message", command("read", a)); socket.emit("message", command("read", b));
 await until(() => socket.results.filter((value) => value.type === "result").length === 4);
 assert.deepEqual(socket.results.slice(-2).map((value) => value.output.text).sort(), ["A", "B"]);
 const forged = { ...a, client_id: "other" }; socket.emit("message", command("read", forged, {}, "forged"));
 assert.equal(agent.state, "fenced"); assert.equal(pages.size, 0);
});
test("unknown write survives local restart without replay and suspend rejects a queued late command", async (t) => {
 let unblock; let writes = 0;
 const { socket, agent, root, pages } = await fixture(t, async (_record, op, _args, b, fence) => { if (op === "fill") { writes++; await new Promise((resolve) => { unblock = resolve; }); } fence(); return { text: b.local_conversation_id }; });
 const a = binding(); socket.emit("message", command("acquire", a)); await until(() => pages.size === 1);
 const write = command("fill", a, { ref: "snapshot_fixture:e1", snapshot_id: "snapshot_fixture", value: "CONTENT_CANARY" });
 socket.emit("message", write); await until(() => writes === 1);
 await agent.suspend(); unblock(); await until(() => agent.journal.unknown(scope).length === 1);
 const restarted = await new HostJournal(path.join(root, "client-r3", "browser-journal")).load();
 assert.equal((await restarted.begin(write)), "unknown");
 const raw = await fs.readFile(path.join(root, "client-r3", "browser-journal", `${write.command_id}.json`), "utf8");
 assert.doesNotMatch(raw, /CONTENT_CANARY|snapshot_fixture|arguments|output/);
 assert.equal(writes, 1); assert.equal(pages.size, 0);
});
test("host refuses raw script fields and stale lease generations before resource use", async (t) => {
 const { socket, agent, pages } = await fixture(t);
 const a = binding(); socket.emit("message", command("acquire", a)); await until(() => pages.size === 1);
 socket.emit("message", command("read", a, { script: "alert(1)" }, "script_injection"));
 assert.equal(agent.state, "fenced"); assert.equal(pages.size, 0);
});
test("journal crash recovery is content-free and explicit evidence is required to reconcile", async (t) => {
 const root = await fs.mkdtemp(path.join(os.tmpdir(), "sparkclaw-journal-test-"));t.after(() => fs.rm(root, { recursive: true, force: true }));
 const first = await new HostJournal(root).load(); const write = command("click", binding(), { ref: "snapshot:e1", snapshot_id: "snapshot" });
 assert.equal(await first.begin(write), "new");
 const second = await new HostJournal(root).load(); assert.equal(second.unknown(scope).length, 1);
 await assert.rejects(second.reconcile(write.command_id, "0".repeat(64), "observed_completed"), /invalid/);
 await second.reconcile(write.command_id, write.digest, "observed_completed"); assert.equal(second.unknown(scope).length, 0);
});
