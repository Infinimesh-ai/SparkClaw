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
 const auth = { status: { state: "connected" }, connection: { ownerID: "owner", clientID: "client", authorization: "Bearer synthetic" }, descriptor: { schemaVersion: 2, origin: "https://example.test" }, async authorizedFetch(url, init) { if (url.endsWith("/fences")) { assert.equal(init.headers["X-SparkClaw-Installation"], "installation"); return Response.json({ fences: [] }); } grantCalls++; assert.equal(url, "https://example.test/api/v1/browser/hosts/grants"); assert.equal(init.headers["X-SparkClaw-Installation"], "installation"); assert.deepEqual(JSON.parse(init.body), {}); return Response.json({ host_id: "host_test", grant_token: "synthetic-private-grant", grant_digest: "a".repeat(64), expires_at: new Date(Date.now() + 60000).toISOString() }); } };
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
 const restarted = await new HostJournal(path.join(root, "workbench", "browser-journal")).load();
 assert.equal((await restarted.begin(write)), "unknown");
 const raw = await fs.readFile(path.join(root, "workbench", "browser-journal", `${write.command_id}.json`), "utf8");
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

test("lost reply after durable local completion merges backend unknown by exact identity/hash and requires explicit evidence", async (t) => {
 const { socket, agent, auth, pages } = await fixture(t);
 const a = binding();socket.emit("message", command("acquire", a));await until(() => pages.size === 1);
 const write = command("fill", a, { ref: "snapshot:e1", snapshot_id: "snapshot", value: "local draft" }, "lost_result_write");
 socket.emit("message", write);await until(() => agent.journal.rows.get(write.command_id)?.state === "completed");
 const fence = { command_id: write.command_id, scope: { ...scope, local_conversation_id: "A", local_task_id: "task_A" }, host_id: a.host_id,
   runtime_generation: a.runtime_generation, connection_epoch: a.connection_epoch, lease_id: a.lease_id, page_id: a.page_id, page_generation: 1,
   authorization_digest: a.authorization_digest, digest: write.digest, write: true, state: "unknown", updated_at: new Date().toISOString() };
 let reconcileCalls = 0;
 auth.authorizedFetch = async (url, init) => {
   assert.equal(init.headers["X-SparkClaw-Installation"], "installation");
   if (url.endsWith("/fences")) return Response.json({ fences: [fence] });
   assert.ok(url.endsWith("/reconcile"));reconcileCalls++;
   assert.deepEqual(JSON.parse(init.body), { command_id: write.command_id, digest: write.digest, outcome: "observed_completed" });
   return Response.json({ reconciled: true });
 };
 await agent.refreshFences();assert.equal(agent.snapshot().unknown_writes.length, 1);assert.equal(reconcileCalls, 0, "a local completed journal cannot automatically resolve remote unknown");
 await assert.rejects(agent.reconcile(write.command_id, "0".repeat(64), "observed_completed"), /invalid/);assert.equal(reconcileCalls, 0);
 await agent.reconcile(write.command_id, write.digest, "observed_completed");assert.equal(agent.snapshot().unknown_writes.length, 0);assert.equal(agent.journal.rows.get(write.command_id).state, "observed_completed");assert.equal(reconcileCalls, 1);
 // Another Owner/client/installation's fence must never enter this UI scope.
 auth.authorizedFetch = async () => Response.json({ fences: [{ ...fence, scope: { ...fence.scope, client_id: "other-client" } }] });
 await assert.rejects(agent.refreshFences(), /identity/);
 auth.authorizedFetch = async () => Response.json({ fences: [{ ...fence, digest: "0".repeat(64) }] });
 await assert.rejects(agent.refreshFences(), /digest conflicts/);assert.equal(agent.state, "reconciliation_conflict");assert.equal(pages.size, 0);
});

test("ISCP connection grants the device browser automatically and retries after failure, LAN does not", async (t) => {
 const root = await fs.mkdtemp(path.join(os.tmpdir(), "sparkclaw-host-auto-"));t.after(() => fs.rm(root, { recursive: true, force: true }));
 const registry = { runtimeGeneration: "runtime_test", closeHostPages() {} };
 const auth = { status: { state: "connected", capabilities: { browser: true } }, connection: { ownerID: "owner", clientID: "client" }, descriptor: { transport: "iscp" } };
 const agent = new BrowserHostAgent({ auth, registry, userDataDir: root });
 t.after(() => agent.stop());
 await agent.start({ installation_id: "installation" });
 let grants = 0; let fail = true;
 agent.grant = async () => { grants++; if (fail) { fail = false; throw new Error("unavailable"); } agent.state = "connected"; };
 agent.autoGrantISCP();
 await until(() => grants === 1 && !agent.autoGranting);
 assert.ok(agent.autoGrantTimer, "a failed automatic grant schedules a retry");
 agent.autoGrantDelay = 0; agent.autoGrantISCP();
 await until(() => agent.state === "connected");
 assert.equal(grants, 2);
 agent.autoGrantISCP(); await new Promise((resolve) => setTimeout(resolve, 10));
 assert.equal(grants, 2, "an already connected host is not granted again");

 agent.state = "fenced"; agent.autoGrantISCP(); await new Promise((resolve) => setTimeout(resolve, 10));
 assert.equal(grants, 2, "a fenced host waits for the user");
 agent.state = "ungranted"; auth.status.capabilities.browser = false; agent.autoGrantISCP(); await new Promise((resolve) => setTimeout(resolve, 10));
 assert.equal(grants, 2, "no grant without the backend browser capability");
 auth.status.capabilities.browser = true; auth.descriptor = { schemaVersion: 2, origin: "https://example.test" };
 agent.autoGrantISCP(); await new Promise((resolve) => setTimeout(resolve, 10));
 assert.equal(grants, 2, "LAN keeps the explicit user grant");
});

async function grantConcurrencyFixture(t) {
 const root=await fs.mkdtemp(path.join(os.tmpdir(),'sparkclaw-host-flight-'));t.after(()=>fs.rm(root,{recursive:true,force:true}));
 const grants=[],registers=[],closes=[];let polls=0;
 const auth={generation:1,status:{state:'connected',capabilities:{browser:true}},connection:{ownerID:'owner',clientID:'client'},descriptor:{transport:'iscp'},async invokeISCP(operation,body){
  if(operation==='browser.host.grant')return new Promise(resolve=>grants.push(resolve));
  if(operation==='browser.host.register'){registers.push(body);return {connection_epoch:`epoch_${registers.length}`};}
  if(operation==='browser.host.close'){closes.push(body);return {closed:true};}
  if(operation==='browser.host.poll'){polls++;return {messages:[{sequence:1,body:{schema_version:1,type:'welcome',host_id:body.host_id,runtime_generation:'runtime_test',connection_epoch:body.connection_epoch,authorization_digest:'a'.repeat(64),lease_seconds:30,heartbeat_seconds:10}}]};}
  if(operation==='browser.receipt')return {fences:[]};
  throw new Error(`unexpected ${operation}`);
 }};
 const registry={runtimeGeneration:'runtime_test',closeHostPages(){}};
 const agent=new BrowserHostAgent({auth,registry,userDataDir:root});t.after(()=>agent.stop());await agent.start({installation_id:'installation'});
 const finish=(index)=>grants[index]({host_id:`host_${index}`,grant_token:'private-test-grant',grant_digest:'a'.repeat(64),expires_at:new Date(Date.now()+60000).toISOString()});
 return {agent,auth,grants,registers,closes,finish,polls:()=>polls};
}

test('automatic and manual browser grants share one live owner; connected activation is idempotent',async t=>{
 const f=await grantConcurrencyFixture(t);f.agent.autoGrantISCP();await until(()=>f.grants.length===1);
 const manual=f.agent.grant();const another=f.agent.grant();f.finish(0);await Promise.all([manual,another]);await until(()=>!f.agent.autoGranting);
 assert.equal(f.agent.state,'connected');assert.equal(f.grants.length,1);assert.equal(f.registers.length,1);assert.equal(f.closes.length,0);
 const epoch=f.agent.epoch,socket=f.agent.socket,generation=f.agent.generation;
 await f.agent.grant();assert.equal(f.grants.length,1);assert.equal(f.registers.length,1);assert.equal(f.closes.length,0);assert.equal(f.agent.epoch,epoch);assert.equal(f.agent.socket,socket);assert.equal(f.agent.generation,generation);
});

test('stopped or replaced scope cannot be revived by a late grant result',async t=>{
 const f=await grantConcurrencyFixture(t);const stopped=f.agent.grant();const rejected=assert.rejects(stopped,/fenced|invalid/);await until(()=>f.grants.length===1);await f.agent.stop();f.finish(0);await rejected;
 assert.equal(f.agent.state,'ungranted');assert.equal(f.agent.scope,undefined);assert.equal(f.registers.length,0);
 await f.agent.start({installation_id:'installation'});const old=f.agent.grant();const oldRejected=assert.rejects(old,/fenced|invalid/);await until(()=>f.grants.length===2);
 f.auth.generation++;f.auth.connection={ownerID:'next-owner',clientID:'next-client'};await f.agent.start({installation_id:'next-installation'});
 const fresh=f.agent.grant();await until(()=>f.grants.length===3);f.finish(2);await fresh;const socket=f.agent.socket,epoch=f.agent.epoch;
 f.finish(1);await oldRejected;assert.equal(f.agent.state,'connected');assert.equal(f.agent.scope.owner_id,'next-owner');assert.equal(f.agent.socket,socket);assert.equal(f.agent.epoch,epoch);assert.equal(f.registers.length,1);assert.equal(f.closes.length,0);
});
