import assert from "node:assert/strict";
import test from "node:test";
import { EventEmitter } from "node:events";
import { PassThrough, Writable } from "node:stream";
import { ISCPTransport, ISCP_OPERATIONS, ISCP_PROFILE, ISCPRequestNotSentError, mapISCPRequest, iscpHelperExecutable } from "../src/main/iscp-transport.mjs";

const origin = "https://iscp.invalid";
const requestID = "12345678-1234-4123-8123-123456789abc";
const hello = { ipc_version: 1, type: "hello", operations: ISCP_OPERATIONS, max_request_bytes: 65536, max_response_bytes: 65536 };
function fixture(t, handler = (call, child) => child.send({ ipc_version: 1, type: "response", id: call.id, response: { type: "task.result", profile: ISCP_PROFILE, id: call.request.id, status: 200, body: { ok: true } } }), options = {}) {
  const children = []; const spawns = []; const calls = [];
  const spawnProcess = (executable, args, spawnOptions) => {
    spawns.push({ executable, args, spawnOptions });
    const child = new EventEmitter(); child.stdout = new PassThrough(); child.stderr = new PassThrough(); child.exitCode = null;
    child.send = (frame) => child.stdout.write(`${JSON.stringify(frame)}\n`);
    child.kill = () => { child.exitCode = 0; child.emit("exit", 0); };
    child.stdin = new Writable({ write(chunk, _encoding, done) { const call = JSON.parse(chunk); calls.push(call); handler(call, child); done(); } });
    children.push(child);
    queueMicrotask(() => { child.send(options.hello || hello); child.send({ ipc_version: 1, type: "state", state: "transport_ready" }); });
    return child;
  };
  const transport = new ISCPTransport({ configPath: "/private/test/config.json", origin, spawnProcess, timeoutMS: 100, ...options });
  t.after(() => transport.close());
  return { transport, children, spawns, calls };
}

test("fixed helper process, compatible hello and manifest precede JSON RPC; exact original JSON survives IPC", async (t) => {
  const f = fixture(t);
  await f.transport.start();
  const raw = '{ "request_id" : "12345678-1234-4123-8123-123456789abc",\n "messages" : [] }';
  assert.deepEqual(await (await f.transport.fetch(`${origin}/api/v1/executions`, { method: "POST", body: raw })).json(), { ok: true });
  assert.equal(f.calls[0].request.operation, "execution.submit");
  assert.equal(f.calls[0].request.body, undefined);
  assert.equal(Buffer.from(f.calls[0].body_base64, "base64").toString("utf8"), raw);
  assert.equal(f.spawns[0].executable, iscpHelperExecutable());
  assert.deepEqual(f.spawns[0].args, ["-config", "/private/test/config.json"]);
  assert.equal(iscpHelperExecutable({ packaged: true, resourcesPath: "/package/resources" }), "/package/resources/iscp-workbench");
  assert.deepEqual(f.spawns[0].spawnOptions.stdio, ["pipe", "pipe", "pipe"]);
});

test("only the fixed workbench operations are reachable and input limits fail before a pipe write", async (t) => {
  const f = fixture(t); await f.transport.start();
  for (const [route, method] of [["/api/email", "GET"], ["/api/owner", "PUT"], ["/api/config?x=1", "GET"], [`/api/v1/executions/${requestID}/files/output`, "GET"], ["/api/browser/extension", "POST"]]) {
    await assert.rejects(f.transport.fetch(`${origin}${route}`, { method }), /unavailable|invalid/u);
  }
  await assert.rejects(f.transport.fetch("https://another.invalid/api/config"), /invalid/u);
  await assert.rejects(f.transport.fetch(`${origin}/api/v1/executions`, { method: "POST", body: JSON.stringify({ input_files: [{}] }) }), /File/u);
  await assert.rejects(f.transport.fetch(`${origin}/api/v1/executions`, { method: "POST", body: JSON.stringify({ content: "x".repeat(65536) }) }), /limit/u);
  assert.equal(f.calls.length, 0);
  assert.equal(mapISCPRequest(`${origin}/api/v1/executions/${requestID}/ack`, { method: "POST", body: "{}" }, origin).request_id, requestID);
});

test("incompatible hello, malformed frames, oversized frames and unknown states close the process", async (t) => {
  const incompatible = fixture(t, undefined, { hello: { ...hello, ipc_version: 2 } });
  await assert.rejects(incompatible.transport.start(), /unavailable/u);
  for (const malformed of ["{\n", `${"x".repeat(74000)}`, `${JSON.stringify({ ipc_version: 1, type: "state", state: "connected" })}\n`]) {
    const f = fixture(t); await f.transport.start();
    f.children[0].stdout.write(malformed);
    assert.equal(f.transport.state, "disconnected"); assert.equal(f.children[0].exitCode, 0);
  }
});

test("four outstanding calls are bounded; abort, deadline and exit reject while old process frames cannot recover readiness", async (t) => {
  const f = fixture(t, () => {}, { timeoutMS: 40 }); await f.transport.start();
  const controller = new AbortController();
  const pending = [f.transport.fetch(`${origin}/api/config`, { signal: controller.signal }), ...Array.from({ length: 3 }, () => f.transport.fetch(`${origin}/api/config`))];
  const observed = pending.map((promise) => promise.catch((error) => error.message));
  await assert.rejects(f.transport.fetch(`${origin}/api/config`), /concurrency/u);
  controller.abort();
  assert.match(await observed[0], /canceled/u);
  const timed = await Promise.all(observed.slice(1)); assert.ok(timed.every((error) => /deadline/u.test(error)));
  const exited = f.transport.fetch(`${origin}/api/config`); const rejection = assert.rejects(exited, /unavailable/u);
  f.children[0].emit("exit", 1); await rejection;
  assert.equal(f.transport.state, "disconnected");
  await f.transport.start();
  f.children[0].send({ ipc_version: 1, type: "state", state: "authorization_expired" });
  assert.equal(f.transport.state, "transport_ready");
});

test("only rejection before a pipe write proves an ISCP request was not sent", async (t) => {
  const f = fixture(t, () => {}, { timeoutMS: 30 });
  await assert.rejects(f.transport.fetch(`${origin}/api/config`), ISCPRequestNotSentError);
  await f.transport.start();
  const controller = new AbortController(); controller.abort();
  await assert.rejects(f.transport.fetch(`${origin}/api/config`, { signal: controller.signal }), ISCPRequestNotSentError);
  await assert.rejects(f.transport.fetch(`${origin}/api/email`), ISCPRequestNotSentError);
  assert.equal(f.calls.length, 0);
  const sent = f.transport.fetch(`${origin}/api/config`);
  await assert.rejects(sent, (error) => !(error instanceof ISCPRequestNotSentError) && /deadline/u.test(error.message));
  assert.equal(f.calls.length, 1);
  const abort = new AbortController();
  const pending = f.transport.fetch(`${origin}/api/config`, { signal: abort.signal });
  abort.abort();
  await assert.rejects(pending, (error) => !(error instanceof ISCPRequestNotSentError) && /canceled/u.test(error.message));
  assert.equal(f.calls.length, 2);
});

test("response correlation and profile are validated, including body and status bounds", async (t) => {
  for (const patch of [{ id: "different" }, { profile: "other" }, { status: 700 }, { body: { content: "x".repeat(65536) } }]) {
    const f = fixture(t, (call, child) => child.send({ ipc_version: 1, type: "response", id: call.id, response: { type: "task.result", profile: ISCP_PROFILE, id: call.request.id, status: 200, ...patch } }));
    await f.transport.start();
    await assert.rejects(f.transport.fetch(`${origin}/api/config`), /unavailable/u);
    assert.equal(f.transport.state, "disconnected");
  }
});


test("helper verified public peer identity must exactly match the selected profile", async (t) => {
  const expectedIdentity = { domain_id: "domain", initiator_device_id: "desktop", responder_device_id: "gateway", responder_key_thumbprint: "thumbprint", relay_url: "https://relay.example.test" };
  for (const identity of [undefined, { ...expectedIdentity, responder_device_id: "other" }]) {
    const f = fixture(t, undefined, { expectedIdentity, hello: { ...hello, identity } });
    await assert.rejects(f.transport.start(), /unavailable/u); assert.equal(f.transport.state, "identity_conflict");
  }
  const valid = fixture(t, undefined, { expectedIdentity, hello: { ...hello, identity: expectedIdentity } });
  await valid.transport.start(); assert.equal(valid.transport.state, "transport_ready");
});

test('v2 negotiation routes only registered operations, uses CAS and unwraps metadata without exposing credentials', async t=>{
 const fs=await import('node:fs');const os=await import('node:os');const path=await import('node:path');
 const root=fs.mkdtempSync(path.join(os.tmpdir(),'iscp-v2-'));t.after(()=>fs.rmSync(root,{recursive:true,force:true}));
 const capabilities={schema_version:2,profile:'sparkclaw.workbench.transport.v2',session_id:'session',revision:'2',authorization_revision:1,expires_at:new Date(Date.now()+60000).toISOString(),binding:{deployment_id:'d',owner_id:'o',client_id:'c'},operations:['settings.owner.get','settings.owner.patch','operations.receipt']};
 let revision='a'.repeat(64);
 const f=fixture(t,(call,child)=>{const body=call.body_base64?JSON.parse(Buffer.from(call.body_base64,'base64')):undefined;
  if(call.request.operation==='settings.owner.patch'){assert.equal(call.request.expected_revision,revision);assert.match(call.request.operation_id,/^[a-f0-9-]{36}$/);assert.equal(body.display_name,'updated');revision='b'.repeat(64);}
  child.send({ipc_version:1,type:'response',id:call.id,response:{type:'task.result',profile:capabilities.profile,id:call.id,status:200,body:{revision,value:{display_name:call.request.operation.endsWith('patch')?'updated':'original'}}}});
 },{journalRoot:root});await f.transport.start();f.children[0].send({ipc_version:1,type:'capabilities',capabilities});
 assert.equal((await(await f.transport.fetch(`${origin}/api/owner`)).json()).display_name,'original');
 assert.equal((await(await f.transport.fetch(`${origin}/api/owner`,{method:'POST',body:JSON.stringify({display_name:'updated'})})).json()).display_name,'updated');
 await assert.rejects(f.transport.fetch(`${origin}/api/owner?url=https://elsewhere.test`),/invalid|unavailable/);
 assert.equal(f.calls.length,2);
});

test('authorization deletion uses isolated control helper and verifies durable receipt identity after revoked session',async t=>{
 const operationID='12345678-1234-4123-8123-123456789abc';let args;
 const transport=new ISCPTransport({configPath:'/private/config.json',origin,expectedIdentity:{domain_id:'domain'},timeoutMS:100,
 spawnProcess:(_executable,argv)=>{args=argv;const child=new EventEmitter();child.stdout=new PassThrough();child.stderr=new PassThrough();child.kill=()=>{};child.stdin=new Writable({write(chunk,_encoding,done){const request=JSON.parse(chunk);queueMicrotask(()=>{child.stdout.write(JSON.stringify({ipc_version:1,type:'hello',control_only:true,identity:{domain_id:'domain'}})+'\n');child.stdout.write(JSON.stringify({ipc_version:1,type:'authorization_receipt',id:request.id,receipt:{operation_id:request.operation_id,expected_revision:request.expected_revision,authorization_revision:request.expected_revision+1,state:'revoked',deleted_at:new Date().toISOString()}})+'\n');});done();}});return child;}});
 const receipt=await transport.control('authorization_delete_receipt',operationID,5);assert.equal(receipt.state,'revoked');assert.deepEqual(args,['-config','/private/config.json','-control-only']);assert.equal(transport.state,'closed');
});

test('bodyless typed errors retain their code and trusted installation across internal calls',async t=>{
 const f=fixture(t,(call,child)=>{assert.equal(call.request.installation_id,requestID);child.send({ipc_version:1,type:'response',id:call.id,response:{type:'task.result',profile:'sparkclaw.workbench.transport.v2',id:call.id,status:409,error:'Requires reconciliation',error_code:'operation_outcome_unknown',retryable:false}});},{installationID:requestID});
 await f.transport.start();f.children[0].send({ipc_version:1,type:'capabilities',capabilities:{schema_version:2,profile:'sparkclaw.workbench.transport.v2',authorization_revision:1,expires_at:new Date(Date.now()+60000).toISOString(),operations:['operations.receipt'],binding:{deployment_id:'d',owner_id:'o',client_id:'c'}}});
 await assert.rejects(f.transport.invoke('operations.receipt',undefined,{params:{operation_id:requestID}}),error=>error.status===409&&error.code==='operation_outcome_unknown'&&error.retryable===false);
});
