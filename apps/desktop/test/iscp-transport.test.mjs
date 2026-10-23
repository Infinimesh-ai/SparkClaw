import assert from "node:assert/strict";
import { mutationProtector } from "./fixtures/mutation-protector.mjs";
import test from "node:test";
import { EventEmitter } from "node:events";
import { PassThrough, Writable } from "node:stream";
import { ISCPTransport, ISCP_OPERATIONS, ISCP_PROFILE, ISCPRequestNotSentError, mapISCPRequest, iscpHelperExecutable } from "../src/main/iscp-transport.mjs";
import crypto from 'node:crypto';
import { SecureCredentialStore } from '../src/main/secure-credential-store.mjs';
import fs, { readFileSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';

const operations = JSON.parse(readFileSync(new URL('../src/shared/iscp-operations.json', import.meta.url), 'utf8'));

const origin = "https://iscp.invalid";
const requestID = "12345678-1234-4123-8123-123456789abc";
const hello = { ipc_version: 1, type: "hello", operations: ISCP_OPERATIONS, max_request_bytes: 65536, max_response_bytes: 65536 };
function fixture(t, handler = (call, child) => child.send({ ipc_version: 1, type: "response", id: call.id, response: { type: "task.result", profile: ISCP_PROFILE, id: call.request.id, status: 200, body: { ok: true } } }), options = {}) {
  const children = []; const spawns = []; const calls = [];
  const journalRoot = options.journalRoot || fs.mkdtempSync(path.join(os.tmpdir(), 'iscp-transport-journal-'));
  if (!options.journalRoot) t.after(() => fs.rmSync(journalRoot, { recursive: true, force: true }));
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
  const transport = new ISCPTransport({ configPath: "/private/test/config.json", origin, spawnProcess, timeoutMS: 100, mutationProtector, journalRoot, ...options });
  t.after(() => transport.close());
  return { transport, children, spawns, calls };
}

test('node-managed device operations replay original encrypted intents and preserve raw original UI responses', async t => {
 const fs=await import('node:fs/promises'),os=await import('node:os'),path=await import('node:path');
 const root=await fs.mkdtemp(path.join(await fs.realpath(os.tmpdir()),'iscp-devices-'));t.after(()=>fs.rm(root,{recursive:true,force:true}));
 const key=crypto.randomBytes(32),safeStorage={isEncryptionAvailable:()=>true,getSelectedStorageBackend:()=> 'gnome_libsecret',
  encryptString(value){const iv=crypto.randomBytes(12),cipher=crypto.createCipheriv('aes-256-gcm',key,iv);const bytes=Buffer.concat([cipher.update(value),cipher.final()]);return Buffer.concat([iv,cipher.getAuthTag(),bytes]);},
  decryptString(value){const cipher=crypto.createDecipheriv('aes-256-gcm',key,value.subarray(0,12));cipher.setAuthTag(value.subarray(12,28));return Buffer.concat([cipher.update(value.subarray(28)),cipher.final()]).toString('utf8');}};
 const vault=new SecureCredentialStore({directory:path.join(root,'secure'),safeStorage,platform:'linux'}).forISCPProfile('a'.repeat(64));
 const capabilities={schema_version:2,profile:'sparkclaw.workbench.transport.v2',session_id:'session',authorization_revision:1,expires_at:new Date(Date.now()+60000).toISOString(),binding:{deployment_id:'d',owner_id:'o',client_id:'c'},operations:['clients.list','clients.issue','clients.revoke','clients.delete','operations.receipt']};
 const credential='sparkclaw-iscp-connect-v1.synthetic-one-use-secret',original={client:{id:'new-client',owner_id:'o',name:'second'},token:credential};
 let drop=true,revoking=true,deleting=true;
 const f=fixture(t,(call,child)=>{
  if(drop){drop=false;return;}
  const status=call.request.operation==='clients.revoke'&&revoking||call.request.operation==='clients.delete'&&deleting?503:200;
  const body=call.request.operation==='clients.list'?{clients:[]}:call.request.operation==='clients.issue'?original:status===503?{code:'client_revocation_requires_reconciliation'}:{id:'new-client',owner_id:'o',revoked_at:new Date().toISOString()};
  child.send({ipc_version:1,type:'response',id:call.id,response:{type:'task.result',profile:capabilities.profile,id:call.id,status,body}});
 },{clientOperationVault:vault,journalRoot:path.join(root,'journal')});
 await f.transport.start();f.children[0].send({ipc_version:1,type:'capabilities',capabilities});
 const input={method:'POST',headers:{'Idempotency-Key':requestID},body:JSON.stringify({client_name:'second'})};
 await assert.rejects(f.transport.fetch(`${origin}/api/clients`,input),/unavailable|timed|deadline/);
 const files=await fs.readdir(vault.directory);assert.equal(files.length,1);
 const cipher=await fs.readFile(path.join(vault.directory,files[0]));assert.ok(!cipher.includes(Buffer.from(requestID)));assert.ok(!cipher.includes(Buffer.from(credential)));
 await assert.rejects(f.transport.fetch(`${origin}/api/clients`,{...input,body:JSON.stringify({client_name:'changed'})}),/reconciled/);
 assert.deepEqual(await (await f.transport.fetch(`${origin}/api/clients`,input)).json(),original);
 assert.equal(f.calls[0].request.operation_id,requestID);assert.equal(f.calls[1].request.operation_id,requestID);
 assert.deepEqual(await fs.readdir(vault.directory),[]);
 const revoke={method:'POST',body:'{}'};
 assert.equal((await f.transport.fetch(`${origin}/api/clients/new-client/revoke`,revoke)).status,503);
 const originalRevoke=f.calls.at(-1).request.operation_id;revoking=false;
 assert.equal((await f.transport.fetch(`${origin}/api/clients/new-client/revoke`,revoke)).status,200);assert.equal(f.calls.at(-1).request.operation_id,originalRevoke);
 const nativeDelete=new Request(`${origin}/api/clients/new-client`,{method:'DELETE',body:'{}'});
 const remove={method:nativeDelete.method,body:await nativeDelete.arrayBuffer()};
 assert.equal((await f.transport.fetch(`${origin}/api/clients/new-client`,remove)).status,503);
 assert.equal(Buffer.from(f.calls.at(-1).body_base64,'base64').toString('utf8'),'{}');
 const originalDelete=f.calls.at(-1).request.operation_id;
 assert.notEqual(originalDelete,originalRevoke);
 deleting=false;
 assert.equal((await f.transport.fetch(`${origin}/api/clients/new-client`,remove)).status,200);
 assert.equal(f.calls.at(-1).request.operation_id,originalDelete);
 assert.deepEqual(await fs.readdir(vault.directory),[]);
 assert.ok(!f.calls.some(call=>call.request.operation==='operations.receipt'));
 await assert.rejects(f.transport.fetch(`${origin}/api/clients`,{...input,headers:{}}),/original operation identity/);
});

test('original device UI keys persist one private wire UUID across concurrency, lost response and restart', async t => {
  const fs = await import('node:fs/promises'), os = await import('node:os'), path = await import('node:path');
  const root = await fs.mkdtemp(path.join(await fs.realpath(os.tmpdir()), 'iscp-original-device-key-'));
  t.after(() => fs.rm(root, { recursive: true, force: true }));
  const key = crypto.randomBytes(32), safeStorage = { isEncryptionAvailable: () => true, getSelectedStorageBackend: () => 'gnome_libsecret',
    encryptString(value) { const iv = crypto.randomBytes(12), cipher = crypto.createCipheriv('aes-256-gcm', key, iv); const bytes = Buffer.concat([cipher.update(value), cipher.final()]); return Buffer.concat([iv, cipher.getAuthTag(), bytes]); },
    decryptString(value) { const cipher = crypto.createDecipheriv('aes-256-gcm', key, value.subarray(0, 12)); cipher.setAuthTag(value.subarray(12, 28)); return Buffer.concat([cipher.update(value.subarray(28)), cipher.final()]).toString('utf8'); } };
  const makeVault = () => new SecureCredentialStore({ directory: path.join(root, 'secure'), safeStorage, platform: 'linux' }).forISCPProfile('a'.repeat(64));
  const capabilities = { schema_version: 2, profile: 'sparkclaw.workbench.transport.v2', session_id: 'session', authorization_revision: 1,
    expires_at: new Date(Date.now() + 60000).toISOString(), binding: { deployment_id: 'd', owner_id: 'o', client_id: 'c' }, operations: ['clients.issue'] };
  // Identical to PairedClientsSettings: 24 random bytes become client-<48hex>.
  const uiKey = () => `client-${Array.from(crypto.randomBytes(24), byte => byte.toString(16).padStart(2, '0')).join('')}`;
  const firstKey = uiKey(), secondKey = uiKey();
  const input = (name, idempotencyKey) => ({ method: 'POST', headers: { 'Idempotency-Key': idempotencyKey }, body: JSON.stringify({ client_name: name }) });
  const firstInput = input('second desktop', firstKey), secondInput = input('third desktop', secondKey);
  const original = { client: { id: 'new-client', owner_id: 'o', name: 'second desktop' }, token: 'sparkclaw-iscp-connect-v1.synthetic-secret' };
  let resolveWritten; const written = new Promise(resolve => { resolveWritten = resolve; });
  const respond = (call, child) => child.send({ ipc_version: 1, type: 'response', id: call.id,
    response: { type: 'task.result', profile: capabilities.profile, id: call.id, status: 200, body: original } });
  const f = fixture(t, (call, child) => {
    const body = JSON.parse(Buffer.from(call.body_base64, 'base64').toString('utf8'));
    if (body.client_name === 'second desktop') resolveWritten(); else respond(call, child);
  }, { clientOperationVault: makeVault(), journalRoot: path.join(root, 'journal') });
  await f.transport.start(); f.children[0].send({ ipc_version: 1, type: 'capabilities', capabilities });
  const lost = assert.rejects(f.transport.fetch(`${origin}/api/clients`, firstInput), /unavailable|timed|deadline/);
  await written;
  const firstWire = f.calls[0].request.operation_id;
  assert.match(firstWire, /^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/u);
  assert.notEqual(firstWire, firstKey);
  await assert.rejects(f.transport.fetch(`${origin}/api/clients`, firstInput), error => error instanceof ISCPRequestNotSentError && error.reason === 'capacity');
  assert.deepEqual(await (await f.transport.fetch(`${origin}/api/clients`, secondInput)).json(), original);
  const secondWire = f.calls[1].request.operation_id;
  assert.notEqual(firstWire, secondWire, 'different original UI keys retain independent identities');
  await lost; f.transport.close();

  const vault = makeVault();
  const g = fixture(t, respond, { clientOperationVault: vault, journalRoot: path.join(root, 'journal') });
  await g.transport.start(); g.children[0].send({ ipc_version: 1, type: 'capabilities', capabilities });
  await assert.rejects(g.transport.fetch(`${origin}/api/clients`, input('changed name', firstKey)), /reconciled/);
  assert.equal(g.calls.length, 0, 'a changed input with the same key cannot reach the helper');
  assert.deepEqual(await (await g.transport.fetch(`${origin}/api/clients`, firstInput)).json(), original);
  assert.deepEqual(await (await g.transport.fetch(`${origin}/api/clients`, firstInput)).json(), original);
  assert.deepEqual(await (await g.transport.fetch(`${origin}/api/clients`, secondInput)).json(), original);
  assert.deepEqual(g.calls.map(call => call.request.operation_id), [firstWire, firstWire, secondWire], 'successful native response replay also retains the wire identity');
  for (const invalid of ['', 'client-short', `client-${'A'.repeat(48)}`, `client-${'a'.repeat(49)}`, `client-${'a'.repeat(4096)}`]) {
    await assert.rejects(g.transport.fetch(`${origin}/api/clients`, input('second desktop', invalid)), /original operation identity/);
  }
  assert.equal(g.calls.length, 3);
  const files = await fs.readdir(vault.directory); assert.equal(files.length, 2);
  for (const filename of files) {
    const encrypted = await fs.readFile(path.join(vault.directory, filename));
    for (const value of [firstKey, secondKey, firstWire, secondWire, original.token]) assert.ok(!encrypted.includes(Buffer.from(value)));
    const row = JSON.parse(safeStorage.decryptString(encrypted));
    assert.deepEqual(Object.keys(row).sort(), ['fingerprint', 'operation', 'operation_id', 'schema_version']);
  }
});

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
  const admissionAbort = new AbortController();
  const queued = f.transport.fetch(`${origin}/api/config`, {signal:admissionAbort.signal});
  const notSent = assert.rejects(queued, error => error instanceof ISCPRequestNotSentError && error.reason === 'canceled');
  assert.equal(f.transport.admissionQueue.size, 1);assert.equal(f.calls.length, 4);
  admissionAbort.abort();await notSent;
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

test("one deadline and a late helper reply preserve the process and concurrent requests", async t => {
  const f = fixture(t, () => {}, { timeoutMS: 40 });
  await f.transport.start();
  const lost = assert.rejects(f.transport.fetch(`${origin}/api/config`), error =>
    !(error instanceof ISCPRequestNotSentError) && /deadline/u.test(error.message));
  // Model a longer-lived concurrent operation on the same helper.
  f.transport.timeoutMS = 1000;
  const concurrent = f.transport.fetch(`${origin}/api/owner`);
  await lost;
  const child = f.children[0];
  const answer = (call, body, status = 200) => child.send({ ipc_version: 1, type: 'response', id: call.id,
    response: { type: 'task.result', profile: ISCP_PROFILE, id: call.request.id, status, body } });
  answer(f.calls[0], { late: true });
  assert.equal(f.transport.pending.size, 1);
  assert.equal(f.transport.state, 'transport_ready');
  assert.equal(child.exitCode, null);
  answer(f.calls[1], { concurrent: true });
  assert.deepEqual(await (await concurrent).json(), { concurrent: true });
  const failed = f.transport.fetch(`${origin}/api/config`);
  answer(f.calls[2], { error: 'ISCP workbench call failed' }, 503);
  assert.equal((await failed).status, 503);
  const next = f.transport.fetch(`${origin}/api/config`);
  answer(f.calls[3], { next: true });
  assert.deepEqual(await (await next).json(), { next: true });
  assert.equal(f.transport.state, 'transport_ready');
  assert.equal(f.children.length, 1);
  assert.equal(f.calls.length, 4, 'no timed-out call was replayed');
  assert.equal(f.transport.pending.size, 0);
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

test('connection snapshots coexist and binding polls read current state without a durable receipt', async t => {
  t.mock.method(globalThis, 'fetch', () => assert.fail('connection settings bypassed ISCP'));
  const names = ['settings.mcp.catalog', 'settings.mcp.tickets.list', 'settings.mcp.bindings.list',
    'settings.mcp.pairing.status', 'settings.mcp.pairing.onboardings', 'settings.bindings.poll'];
  const capabilities = {schema_version:2, profile:'sparkclaw.workbench.transport.v2', session_id:'session', authorization_revision:1,
    expires_at:new Date(Date.now()+60000).toISOString(), binding:{deployment_id:'d',owner_id:'o',client_id:'c'}, operations:names};
  let polls = 0;
  const f = fixture(t, (call, child) => {
    const body = call.request.operation === 'settings.bindings.poll' ? {status: ++polls === 1 ? 'waiting_scan' : 'active'} : {operation:call.request.operation};
    setImmediate(() => child.send({ipc_version:1,type:'response',id:call.id,response:{type:'task.result',profile:capabilities.profile,id:call.id,status:200,body}}));
  });
  await f.transport.start(); f.children[0].send({ipc_version:1,type:'capabilities',capabilities});
  const paths = ['/api/mcp-access/catalog','/api/mcp-access/tickets','/api/mcp-access/bindings','/api/iscp-pairing/status','/api/iscp-pairing/onboardings'];
  const responses = await Promise.all(paths.map(async route => (await f.transport.fetch(`${origin}${route}`)).json()));
  assert.equal(responses.length, 5);
  const poll = {method:'POST',body:'{}'};
  assert.equal((await (await f.transport.fetch(`${origin}/api/notification-bindings/binding/poll`, poll)).json()).status, 'waiting_scan');
  assert.equal(f.transport.mutations?.read('binding:binding') ?? null, null, 'a poll leaves no durable journal record');
  assert.equal(f.calls.find(call => call.request.operation === 'settings.bindings.poll').request.operation_id, undefined, 'a poll requests no receipt');
  assert.equal((await (await f.transport.fetch(`${origin}/api/notification-bindings/binding/poll`, poll)).json()).status, 'active');
  assert.equal(polls, 2, 'each poll queries the provider for the current state');
});

test('revoking a binding is not refused while its provider poll is still waiting', async t => {
  let releasePoll;
  const f=fixture(t,(call,child)=>{
    const reply=()=>child.send({ipc_version:1,type:'response',id:call.id,response:{type:'task.result',profile:'sparkclaw.workbench.transport.v2',id:call.id,status:200,
      body:{status:call.request.operation==='settings.bindings.poll'?'waiting_scan':'revoked'}}});
    if(call.request.operation==='settings.bindings.poll') releasePoll=reply; else setImmediate(reply);
  });
  await f.transport.start();
  f.children[0].send({ipc_version:1,type:'capabilities',capabilities:{
    schema_version:2,profile:'sparkclaw.workbench.transport.v2',session_id:'session',authorization_revision:1,
    expires_at:new Date(Date.now()+60000).toISOString(),binding:{deployment_id:'d',owner_id:'o',client_id:'c'},
    operations:['settings.bindings.poll','settings.bindings.revoke'],
  }});
  const poll=f.transport.fetch(`${origin}/api/notification-bindings/binding/poll`,{method:'POST',body:'{}',actionID:crypto.randomUUID()});
  while(!releasePoll) await new Promise(resolve=>setImmediate(resolve));
  const revoke=await f.transport.fetch(`${origin}/api/notification-bindings/binding`,{method:'DELETE',actionID:crypto.randomUUID()});
  assert.equal((await revoke.json()).status,'revoked');
  releasePoll();
  assert.equal((await (await poll).json()).status,'waiting_scan');
});

test('a bodyless DELETE from the renderer reaches the backend as an empty request', async t => {
  const f=fixture(t,(call,child)=>child.send({ipc_version:1,type:'response',id:call.id,response:{type:'task.result',profile:'sparkclaw.workbench.transport.v2',id:call.id,status:200,body:{status:'revoked'}}}));
  await f.transport.start();
  f.children[0].send({ipc_version:1,type:'capabilities',capabilities:{
    schema_version:2,profile:'sparkclaw.workbench.transport.v2',session_id:'session',authorization_revision:1,
    expires_at:new Date(Date.now()+60000).toISOString(),binding:{deployment_id:'d',owner_id:'o',client_id:'c'},
    operations:['settings.bindings.revoke','settings.mcp.tickets.delete'],
  }});
  for (const [route,body] of [['/api/notification-bindings/binding',new ArrayBuffer(0)],['/api/mcp-access/tickets/ticket',new Uint8Array(0)]]) {
    const response=await f.transport.fetch(`${origin}${route}`,{method:'DELETE',body,actionID:crypto.randomUUID()});
    assert.equal(response.status,200,route);
  }
  assert.deepEqual(f.calls.map(call=>[call.request.operation,call.request.body]),[['settings.bindings.revoke',undefined],['settings.mcp.tickets.delete',undefined]]);
});

test('QR binding operations retain a sixty-second original deadline for the provider long poll', async t => {
  const f=fixture(t, (call,child)=>child.send({ipc_version:1,type:'response',id:call.id,response:{
    type:'task.result',profile:'sparkclaw.workbench.transport.v2',id:call.id,status:200,body:{status:'waiting_scan'},
  }}), {timeoutMS:30000});
  await f.transport.start();
  f.children[0].send({ipc_version:1,type:'capabilities',capabilities:{
    schema_version:2,profile:'sparkclaw.workbench.transport.v2',session_id:'session',authorization_revision:1,
    expires_at:new Date(Date.now()+120000).toISOString(),binding:{deployment_id:'d',owner_id:'o',client_id:'c'},
    operations:['settings.bindings.start','settings.bindings.poll'],
  }});
  for (const [route,resource] of [['weixin/start','binding:weixin']]) {
    await f.transport.fetch(`${origin}/api/notification-bindings/${route}`,{method:'POST',body:'{}'});
    const record=f.transport.mutations.read(resource);
    const budget=record.not_after-record.created_at;
    assert.ok(budget>59000 && budget<=60000, `QR provider budget was ${budget}`);
  }
});

test('a binding poll keeps the sixty-second provider budget without a journal record', async t => {
  const f=fixture(t,(call,child)=>setTimeout(()=>child.send({ipc_version:1,type:'response',id:call.id,response:{
    type:'task.result',profile:'sparkclaw.workbench.transport.v2',id:call.id,status:200,body:{status:'active'},
  }}),45000),{timeoutMS:30000});
  await f.transport.start();
  f.children[0].send({ipc_version:1,type:'capabilities',capabilities:{
    schema_version:2,profile:'sparkclaw.workbench.transport.v2',session_id:'session',authorization_revision:1,
    expires_at:new Date(Date.now()+120000).toISOString(),binding:{deployment_id:'d',owner_id:'o',client_id:'c'},
    operations:['settings.bindings.poll'],
  }});
  t.mock.timers.enable({apis:['setTimeout']});
  const pending=f.transport.fetch(`${origin}/api/notification-bindings/binding/poll`,{method:'POST',body:'{}'});
  for (let i=0;i<5;i++) await Promise.resolve();
  t.mock.timers.tick(45000);
  assert.equal((await (await pending).json()).status,'active');
});

test('a later browser check reconciles the failed receipt before admitting a fresh check', async t => {
  let checks=0, recovered=false;
  const f=fixture(t,(call,child)=>{
    let body,status=200;
    if(call.request.operation==='operations.receipt') body=recovered
      ? {state:'completed',operation_id:call.request.params.operation_id,response:{status:503,body:{code:'controller_unavailable'}}}
      : {state:'unknown',operation_id:call.request.params.operation_id};
    else { checks++; status=checks===1?503:200; body=checks===1?{code:'controller_unavailable'}:{state:'ready'}; }
    child.send({ipc_version:1,type:'response',id:call.id,response:{type:'task.result',profile:'sparkclaw.workbench.transport.v2',id:call.id,status,body}});
  });
  await f.transport.start();
  f.children[0].send({ipc_version:1,type:'capabilities',capabilities:{
    schema_version:2,profile:'sparkclaw.workbench.transport.v2',session_id:'session',authorization_revision:1,
    expires_at:new Date(Date.now()+60000).toISOString(),binding:{deployment_id:'d',owner_id:'o',client_id:'c'},
    operations:['settings.browser.check','operations.receipt'],
  }});
  const check=()=>f.transport.fetch(`${origin}/api/browser/extension/check`,{method:'POST',body:'{}',actionID:crypto.randomUUID()});
  assert.equal((await check()).status,503);
  await assert.rejects(check(),/unknown/); assert.equal(checks,1);
  recovered=true;
  assert.equal((await check()).status,503); assert.equal(checks,1);
  assert.equal((await (await check()).json()).state,'ready'); assert.equal(checks,2);
});

test('lost binding and token replies reconcile the original sealed intent after restart without repeating effects', async t => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'iscp-connections-'));
  t.after(() => fs.rmSync(root,{recursive:true,force:true}));
  t.mock.method(globalThis, 'fetch', () => assert.fail('binding bypassed encrypted ISCP'));
  const effects = new Map(), receipts = new Map();
  let recovered = false;
  const cases = [
    ['POST','/api/notification-bindings/telegram/start',{credential_secret:'private-bot-token'},'settings.bindings.start'],
    ['PUT','/api/browser/extension/token',{token:'private-extension-token'},'settings.browser.save'],
    ['POST','/api/mcp-access/tickets',{display_name:'desktop'},'settings.mcp.tickets.issue'],
    ['POST','/api/iscp-pairing/start',{display_name:'desktop'},'settings.mcp.pairing.start'],
  ];
  const capabilities={schema_version:2,profile:'sparkclaw.workbench.transport.v2',session_id:'session',authorization_revision:1,
    expires_at:new Date(Date.now()+60000).toISOString(),operations:[...cases.map(row=>row[3]),'operations.receipt'],binding:{deployment_id:'d',owner_id:'o',client_id:'c'}};
  const f=fixture(t,(call,child)=>{
    const {operation,operation_id}=call.request;
    if(operation==='operations.receipt') {
      child.send({ipc_version:1,type:'response',id:call.id,response:{type:'task.result',profile:capabilities.profile,id:call.id,status:200,
        body:recovered?{state:'completed',operation_id:call.request.params.operation_id,response:receipts.get(call.request.params.operation_id)}:{state:'unknown'}}});
      return;
    }
    effects.set(operation,(effects.get(operation)||0)+1);
    receipts.set(operation_id,{status:200,body:{operation,secret:'private-issued-ticket'}});
  },{journalRoot:root,timeoutMS:100});
  const start=async()=>{await f.transport.start();f.children.at(-1).send({ipc_version:1,type:'capabilities',capabilities});};
  await start();
  for(const [method,route,body] of cases) await assert.rejects(f.transport.fetch(`${origin}${route}`,{method,body:JSON.stringify(body)}),/deadline/);
  f.transport.close(); await start(); recovered=true;
  for(const [method,route,body,operation] of cases) {
    const result=await(await f.transport.fetch(`${origin}${route}`,{method,body:JSON.stringify(body)})).json();
    assert.equal(result.operation,operation); assert.equal(effects.get(operation),1);
  }
  for(const filename of fs.readdirSync(path.join(root,'mutations'))) {
    const content=fs.readFileSync(path.join(root,'mutations',filename));
    for(const secret of ['private-bot-token','private-extension-token','private-issued-ticket']) assert.ok(!content.includes(Buffer.from(secret)));
  }
});

test('authorization deletion uses isolated control helper and verifies durable receipt identity after revoked session',async t=>{
 const operationID='12345678-1234-4123-8123-123456789abc';let args;let step=1;
 const transport=new ISCPTransport({configPath:'/private/config.json',origin,expectedIdentity:{domain_id:'domain'},timeoutMS:100,
 spawnProcess:(_executable,argv)=>{args=argv;const child=new EventEmitter();child.stdout=new PassThrough();child.stderr=new PassThrough();child.kill=()=>{};child.stdin=new Writable({write(chunk,_encoding,done){const request=JSON.parse(chunk);queueMicrotask(()=>{child.stdout.write(JSON.stringify({ipc_version:1,type:'hello',control_only:true,identity:{domain_id:'domain'}})+'\n');child.stdout.write(JSON.stringify({ipc_version:1,type:'authorization_receipt',id:request.id,receipt:{operation_id:request.operation_id,expected_revision:request.expected_revision,authorization_revision:request.expected_revision+step,state:'revoked',deleted_at:new Date().toISOString()}})+'\n');});done();}});return child;}});
 const receipt=await transport.control('authorization_delete_receipt',operationID,5);assert.equal(receipt.state,'revoked');assert.deepEqual(args,['-config','/private/config.json','-control-only']);assert.equal(transport.state,'closed');step=2;await assert.rejects(transport.control('authorization_delete_receipt',operationID,5),/invalid/);
});

test('bodyless typed errors retain their code and trusted installation across internal calls',async t=>{
 const f=fixture(t,(call,child)=>{assert.equal(call.request.installation_id,requestID);child.send({ipc_version:1,type:'response',id:call.id,response:{type:'task.result',profile:'sparkclaw.workbench.transport.v2',id:call.id,status:409,error:'Requires reconciliation',error_code:'operation_outcome_unknown',retryable:false}});},{installationID:requestID});
 await f.transport.start();f.children[0].send({ipc_version:1,type:'capabilities',capabilities:{schema_version:2,profile:'sparkclaw.workbench.transport.v2',session_id:'session',authorization_revision:1,expires_at:new Date(Date.now()+60000).toISOString(),operations:['operations.receipt'],binding:{deployment_id:'d',owner_id:'o',client_id:'c'}}});
 await assert.rejects(f.transport.invoke('operations.receipt',undefined,{params:{operation_id:requestID}}),error=>error.status===409&&error.code==='operation_outcome_unknown'&&error.retryable===false);
});

test('legacy v2 bulk and audio windows leave control admission while queued bulk can cancel before send',async t=>{
 const operations=['object.read','speech.session.frame','execution.cancel'];
 const f=fixture(t,(call,child)=>{if(call.request.operation==='execution.cancel')child.send({ipc_version:1,type:'response',id:call.id,response:{type:'task.result',profile:'sparkclaw.workbench.transport.v2',id:call.id,status:200,body:{state:'canceled'}}});},{timeoutMS:1000});
 await f.transport.start();f.children[0].send({ipc_version:1,type:'capabilities',capabilities:{schema_version:2,profile:'sparkclaw.workbench.transport.v2',session_id:'session',authorization_revision:1,expires_at:new Date(Date.now()+60000).toISOString(),operations,binding:{deployment_id:'d',owner_id:'o',client_id:'c'}}});
 const pending=[...Array.from({length:2},()=>f.transport.invoke('object.read',{}).catch(()=>{})),...Array.from({length:2},()=>f.transport.invoke('speech.session.frame',{}).catch(()=>{}))];
 const abort=new AbortController();
 const queued=f.transport.invoke('object.read',{}, {signal:abort.signal});
 const rejected=assert.rejects(queued,error=>error instanceof ISCPRequestNotSentError&&error.reason==='canceled');
 assert.equal(f.calls.length,4);assert.equal(f.transport.admissionQueue.size,1);
 abort.abort();await rejected;
 assert.equal((await(await f.transport.fetch(`${origin}/api/v1/executions/${requestID}/cancel`,{method:'POST',body:'{}'})).json()).state,'canceled');
 f.transport.close();await Promise.all(pending);
});

 test('unavailable durable storage closes negotiation without escaping an IPC callback',async t=>{
 const fs=await import('node:fs');const os=await import('node:os');const path=await import('node:path');
 const root=fs.mkdtempSync(path.join(os.tmpdir(),'iscp-journal-fail-'));t.after(()=>fs.rmSync(root,{recursive:true,force:true}));
 fs.writeFileSync(path.join(root,'objects'),'not a directory');
 const f=fixture(t,undefined,{journalRoot:root});await f.transport.start();
 assert.doesNotThrow(()=>f.children[0].send({ipc_version:1,type:'capabilities',capabilities:{schema_version:2,profile:'sparkclaw.workbench.transport.v2',session_id:'session',authorization_revision:1,expires_at:new Date(Date.now()+60000).toISOString(),operations:[],binding:{deployment_id:'d',owner_id:'o',client_id:'c'}}}));
 assert.equal(f.transport.state,'disconnected');assert.equal(f.transport.capabilities,undefined);
 });

test('v2 text-only grants retain their authorized basic owner presentation',async t=>{
 const f=fixture(t,(call,child)=>{assert.equal(call.request.operation,'presentation.owner');child.send({ipc_version:1,type:'response',id:call.id,response:{type:'task.result',profile:'sparkclaw.workbench.transport.v2',id:call.id,status:200,body:{id:'owner'}}});});await f.transport.start();
 f.children[0].send({ipc_version:1,type:'capabilities',capabilities:{schema_version:2,profile:'sparkclaw.workbench.transport.v2',session_id:'session',authorization_revision:1,expires_at:new Date(Date.now()+60000).toISOString(),operations:['presentation.owner'],binding:{deployment_id:'d',owner_id:'o',client_id:'c'}}});
 assert.equal((await(await f.transport.fetch(`${origin}/api/owner`)).json()).id,'owner');
 await assert.rejects(f.transport.fetch(`${origin}/api/owner`,{method:'POST',body:'{}'}),ISCPRequestNotSentError);
 assert.equal(f.calls.length,1);
});

test('lost mail send responses reconcile durable operation receipts before any new mail mutation',async t=>{
 const fs=await import('node:fs');const os=await import('node:os');const path=await import('node:path');const root=fs.mkdtempSync(path.join(os.tmpdir(),'iscp-mail-'));t.after(()=>fs.rmSync(root,{recursive:true,force:true}));
 let sentID;const f=fixture(t,(call,child)=>{if(call.request.operation==='mail.drafts.list'){child.send({ipc_version:1,type:'response',id:call.id,response:{type:'task.result',profile:'sparkclaw.workbench.transport.v2',id:call.id,status:200,body:{id:'draft',version:4,state:'draft',attachments:[]}}});return;}if(call.request.operation==='mail.drafts.send'){sentID=call.request.operation_id;return;}assert.equal(call.request.operation,'operations.receipt');assert.equal(call.request.params.operation_id,sentID);child.send({ipc_version:1,type:'response',id:call.id,response:{type:'task.result',profile:'sparkclaw.workbench.transport.v2',id:call.id,status:200,body:{state:'completed',operation_id:sentID,response:{status:200,body:{id:'draft',state:'sent'}}}}});},{journalRoot:root,timeoutMS:20});await f.transport.start();
 f.children[0].send({ipc_version:1,type:'capabilities',capabilities:{schema_version:2,profile:'sparkclaw.workbench.transport.v2',session_id:'session',authorization_revision:1,expires_at:new Date(Date.now()+60000).toISOString(),operations:['mail.drafts.list','mail.drafts.send','operations.receipt'],binding:{deployment_id:'d',owner_id:'o',client_id:'c'}}});
 const request={method:'POST',body:JSON.stringify({expected_version:4,idempotency_key:requestID})};
 await assert.rejects(f.transport.fetch(`${origin}/api/email/drafts/draft/send`,request),/deadline/);
 assert.equal((await(await f.transport.fetch(`${origin}/api/email/drafts/draft/send`,request)).json()).state,'sent');
 assert.deepEqual(f.calls.map(call=>call.request.operation),['mail.drafts.list','mail.drafts.send','operations.receipt']);
});

test('unknown mail send fence permits explicit reconciliation without touching local files or repeating send', async t => {
  const fs = await import('node:fs'), os = await import('node:os'), path = await import('node:path');
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'iscp-mail-unknown-')); t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const f = fixture(t, (call, child) => {
    let body;
    if (call.request.operation === 'mail.drafts.list') body = { id: 'draft', version: 4, state: 'draft', attachments: [] };
    else if (call.request.operation === 'mail.drafts.send') return;
    else if (call.request.operation === 'operations.receipt') body = { state: 'unknown' };
    else if (call.request.operation === 'mail.drafts.reconcile') body = { id: 'draft', version: 5, state: 'sent' };
    else assert.fail('unexpected operation');
    child.send({ ipc_version: 1, type: 'response', id: call.id, response: { type: 'task.result', profile: 'sparkclaw.workbench.transport.v2', id: call.id, status: 200, body } });
  // The lost reply is the injected fault; encrypted journal fsync must have
  // time to complete before that fault under the parallel desktop suite.
  }, { journalRoot: root, timeoutMS: 200, readLocalFile() { assert.fail('receipt recovery read a local file'); } });
  await f.transport.start();
  f.children[0].send({ ipc_version: 1, type: 'capabilities', capabilities: { schema_version: 2, profile: 'sparkclaw.workbench.transport.v2', session_id: 'session', authorization_revision: 1, expires_at: new Date(Date.now() + 60000).toISOString(), operations: ['mail.drafts.list', 'mail.drafts.send', 'mail.drafts.reconcile', 'operations.receipt'], binding: { deployment_id: 'd', owner_id: 'o', client_id: 'c' } } });
  const request = { method: 'POST', body: JSON.stringify({ expected_version: 4, idempotency_key: requestID }) };
  await assert.rejects(f.transport.fetch(`${origin}/api/email/drafts/draft/send`, request), /deadline/);
  f.transport.timeoutMS = 1000;
  await assert.rejects(f.transport.fetch(`${origin}/api/email/drafts/draft/send`, request), /unknown/);
  const reconciled = await f.transport.fetch(`${origin}/api/email/drafts/draft/reconcile`, { method: 'POST', body: '{}' });
  assert.equal((await reconciled.json()).state, 'sent');
  assert.equal(f.calls.filter(call => call.request.operation === 'mail.drafts.send').length, 1);
  assert.ok(f.transport.mutations.read('mail-draft:draft'));
});

test('lost attachment save reconciles original receipt before consulting changed local files or expired uploads', async t => {
  const fs = await import('node:fs'), os = await import('node:os'), path = await import('node:path'), crypto = await import('node:crypto');
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'iscp-mail-save-')); t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const fileID = crypto.randomUUID(), bytes = Buffer.from('local bytes'), hash = crypto.createHash('sha256').update(bytes).digest('hex');
  const f = fixture(t, (call, child) => {
    if (call.request.operation === 'mail.drafts.save') return;
    assert.equal(call.request.operation, 'operations.receipt');
    child.send({ ipc_version: 1, type: 'response', id: call.id, response: { type: 'task.result', profile: 'sparkclaw.workbench.transport.v2', id: call.id, status: 200, body: { state: 'completed', operation_id: call.request.params.operation_id, response: { status: 200, body: { id: 'draft', version: 1 } } } } });
  }, { journalRoot: root, timeoutMS: 20, canSendMailAttachments: () => true, readLocalFile: () => ({ name: 'local.txt', size: bytes.length, sha256: hash, content: Buffer.from(bytes) }) });
  await f.transport.start();
  f.children[0].send({ ipc_version: 1, type: 'capabilities', capabilities: { schema_version: 2, profile: 'sparkclaw.workbench.transport.v2', session_id: 'session', authorization_revision: 1, expires_at: new Date(Date.now() + 60000).toISOString(), operations: ['mail.drafts.save', 'operations.receipt'], binding: { deployment_id: 'd', owner_id: 'o', client_id: 'c' } } });
  let uploads = 0;
  f.transport.objects.upload = async () => { uploads++; return { object_id: 'one-object', version: 1, size: bytes.length, sha256: hash, purpose: 'mail_send_attachment', name: 'local.txt' }; };
  const request = { method: 'POST', body: JSON.stringify({ id: 'draft', attachments: [{ local_file_id: fileID }] }) };
  await assert.rejects(f.transport.fetch(`${origin}/api/email/drafts`, request), /deadline/);
  f.transport.readLocalFile = () => { assert.fail('unknown save reread deleted source'); };
  f.transport.objects.upload = () => { assert.fail('unknown save uploaded again'); };
  assert.equal((await (await f.transport.fetch(`${origin}/api/email/drafts`, request)).json()).version, 1);
  assert.equal(uploads, 1);
  assert.deepEqual(f.calls.map(call => call.request.operation), ['mail.drafts.save', 'operations.receipt']);
});

test('mail save, receipt reconciliation and provider probes can complete beyond 30 seconds without relaxing reads', async t => {
  const fs = await import('node:fs'), os = await import('node:os'), path = await import('node:path');
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'iscp-mail-budget-')); t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const f = fixture(t, (call, child) => {
    setTimeout(() => child.send({ ipc_version: 1, type: 'response', id: call.id, response: { type: 'task.result', profile: 'sparkclaw.workbench.transport.v2', id: call.id, status: 200, body: { id: 'draft', version: 1 } } }), 45000);
  }, { journalRoot: root, timeoutMS: 30000 });
  await f.transport.start();
  f.children[0].send({ ipc_version: 1, type: 'capabilities', capabilities: { schema_version: 2, profile: 'sparkclaw.workbench.transport.v2', session_id: 'session', authorization_revision: 1, expires_at: new Date(Date.now() + 300000).toISOString(), operations: ['mail.drafts.save', 'mail.drafts.list', 'mail.drafts.reconcile', 'mail.providers.check', 'mail.providers.login'], binding: { deployment_id: 'd', owner_id: 'o', client_id: 'c' } } });
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const saving = f.transport.fetch(`${origin}/api/email/drafts`, { method: 'POST', body: JSON.stringify({ id: 'draft', attachments: [] }) });
  for (let i = 0; i < 5; i++) await Promise.resolve();
  assert.equal(f.calls.length, 1);
  t.mock.timers.tick(45000);
  assert.equal((await (await saving).json()).version, 1);
  for (const action of ['check', 'login-browser']) {
    const pending = f.transport.fetch(`${origin}/api/email/providers/qq_mail/${action}`, {method:'POST',body:'{}'});
    for (let i = 0; i < 5; i++) await Promise.resolve();
    t.mock.timers.tick(45000);
    assert.equal((await (await pending).json()).version, 1);
  }
  const reconciling = f.transport.fetch(`${origin}/api/email/drafts/draft/reconcile`, { method: 'POST', body: '{}' });
  for (let i = 0; i < 5; i++) await Promise.resolve();
  t.mock.timers.tick(45000);
  assert.equal((await (await reconciling).json()).version, 1);
  assert.equal(f.calls.at(-1).request.operation, 'mail.drafts.reconcile');
  f.transport.capabilities = {...f.transport.capabilities,operations:[...f.transport.capabilities.operations,'mail.intake.update','mail.replies.polish','mail.source.cleanup','mail.conversations.delete']};
  for (const [route,method,body] of [['/api/email/providers/outlook','PATCH',{intake_enabled:false,expected_mailbox_version:1}],['/api/email/replies/polish','POST',{}],['/api/email/source/cleanup','POST',{}],['/api/email/conversations/conversation','DELETE',{}]]) {
    const pending = f.transport.fetch(`${origin}${route}`,{method,body:JSON.stringify(body)});
    for (let i = 0; i < 5; i++) await Promise.resolve();
    t.mock.timers.tick(45000);
    assert.equal((await (await pending).json()).version,1);
  }
  const reading = f.transport.fetch(`${origin}/api/email/drafts`);
  const rejected = assert.rejects(reading, /deadline/);
  t.mock.timers.tick(30000); await rejected;
});

test('mail provider login uses a durable receipt after an explicit unknown response and never reopens', async t => {
  const fs = await import('node:fs'), os = await import('node:os'), path = await import('node:path');
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'iscp-provider-login-'));
  t.after(() => fs.rmSync(root, {recursive:true, force:true}));
  const f = fixture(t, (call, child) => {
    const operation = call.request.operation;
    const body = operation === 'operations.receipt' ? {state:'unknown'} : operation === 'mail.providers.login' ? {code:'operation_outcome_unknown', error:'Login outcome is unknown'} : {provider:'outlook', state:'ready'};
    child.send({ipc_version:1,type:'response',id:call.id,response:{type:'task.result',profile:'sparkclaw.workbench.transport.v2',id:call.id,status:operation === 'mail.providers.login' ? 409 : 200,body}});
  }, {journalRoot:root});
  await f.transport.start();
  f.children[0].send({ipc_version:1,type:'capabilities',capabilities:{schema_version:2,profile:'sparkclaw.workbench.transport.v2',session_id:'session',authorization_revision:1,expires_at:new Date(Date.now()+60000).toISOString(),operations:['mail.providers.login','mail.providers.check','operations.receipt'],binding:{deployment_id:'d',owner_id:'o',client_id:'c'}}});
  const request = {method:'POST',body:'{}'};
  assert.equal((await f.transport.fetch(`${origin}/api/email/providers/outlook/login-browser`,request)).status,409);
  assert.ok(f.transport.mutations.read('mail-provider:outlook:login'));
  await assert.rejects(f.transport.fetch(`${origin}/api/email/providers/outlook/login-browser`,request), /unknown/);
  assert.equal((await f.transport.fetch(`${origin}/api/email/providers/outlook/check`,request)).status,200);
  assert.deepEqual(f.calls.map(call => call.request.operation), ['mail.providers.login','operations.receipt','mail.providers.check']);
});

test('original popup mutations retain their original receipt across helper restart without HTTP or repeated effects', async t => {
 const fs=await import('node:fs'), os=await import('node:os'), path=await import('node:path');
 const root=fs.mkdtempSync(path.join(os.tmpdir(),'iscp-popup-mutations-'));t.after(()=>fs.rmSync(root,{recursive:true,force:true}));
 t.mock.method(globalThis,'fetch',()=>assert.fail('popup bypassed encrypted ISCP'));
 const mutations = new Map(), receipts = new Map();
 let reply = false;
 const f=fixture(t,(call,child)=>{
   const {operation,operation_id}=call.request;
   if(operation === 'operations.receipt') {
     const result=receipts.get(call.request.params.operation_id);
     child.send({ipc_version:1,type:'response',id:call.id,response:{type:'task.result',profile:'sparkclaw.workbench.transport.v2',id:call.id,status:200,body:reply?{state:'completed',operation_id:call.request.params.operation_id,response:result}:{state:'unknown'}}});
     return;
   }
   assert.ok(operation_id);
   mutations.set(operation,(mutations.get(operation)||0)+1);
   receipts.set(operation_id,{status:200,body:{operation,version:3}});
 },{journalRoot:root,timeoutMS:100});
 const cases = [
   ['POST','/api/email/messages/mail/classification',{expected_version:2,command_key:'classification'},'mail.classification'],
   ['POST','/api/email/conversations/conversation/rename',{title:'Edited',expected_version:2,command_key:'rename'},'mail.conversations.rename'],
   ['POST','/api/email/presentations/ensure',{target_kind:'mail',target_ids:['mail'],language:'zh'},'mail.presentations.ensure'],
   ['PATCH','/api/email/providers/outlook',{intake_enabled:false,expected_mailbox_version:2},'mail.intake.update'],
   ['PUT','/api/email/drafts/draft',{expected_version:0,attachments:[],subject:'Popup draft'},'mail.drafts.save'],
 ];
 const capabilities={schema_version:2,profile:'sparkclaw.workbench.transport.v2',session_id:'session',authorization_revision:1,expires_at:new Date(Date.now()+60000).toISOString(),operations:[...cases.map(value=>value[3]),'mail.assignment','operations.receipt'],binding:{deployment_id:'d',owner_id:'o',client_id:'c'}};
 const start = async () => { await f.transport.start(); f.children.at(-1).send({ipc_version:1,type:'capabilities',capabilities}); };
 await start();
 for (const [method,route,body] of cases) await assert.rejects(f.transport.fetch(`${origin}${route}`,{method,body:JSON.stringify(body)}),/deadline/);
 const savedCall=f.calls.find(call=>call.request.operation==='mail.drafts.save');
 assert.equal(JSON.parse(Buffer.from(savedCall.body_base64,'base64')).id,'draft','PUT identity must survive serialized IPC');
 f.transport.close(); await start();
 await assert.rejects(f.transport.fetch(`${origin}/api/email/messages/mail/assignment`,{method:'POST',body:JSON.stringify({conversation_id:'other',expected_version:2})}),/unknown/);
 assert.equal(mutations.has('mail.assignment'),false,'unknown classification fences another mutation of that message');
 reply = true;
 for (const [method,route,body,operation] of cases) {
   const value=await(await f.transport.fetch(`${origin}${route}`,{method,body:JSON.stringify(body)})).json();
   assert.equal(value.operation,operation);
   assert.equal(mutations.get(operation),1);
 }
 assert.ok(fs.readdirSync(path.join(root,'mutations')).every(name=>!name.endsWith('.json')));
});

test('original popup files use object verification and main-owned digest headers', async t => {
 const fs=await import('node:fs'), os=await import('node:os'), path=await import('node:path'), crypto=await import('node:crypto');
 const root=fs.mkdtempSync(path.join(os.tmpdir(),'iscp-popup-file-'));t.after(()=>fs.rmSync(root,{recursive:true,force:true}));
 const bytes=Buffer.from('approved synthetic source');
 const object={object_id:'mail-object',version:1,size:bytes.length,sha256:crypto.createHash('sha256').update(bytes).digest('hex'),name:'original.eml',purpose:'mail_attachment',media_type:'application/octet-stream'};
 const f=fixture(t,(call,child)=>{
   assert.equal(call.request.operation,'mail.file');assert.deepEqual(call.request.params,{mail:'mail'});
   child.send({ipc_version:1,type:'response',id:call.id,response:{type:'task.result',profile:'sparkclaw.workbench.transport.v2',id:call.id,status:200,body_object:object}});
 },{journalRoot:root});
 await f.transport.start(); f.children[0].send({ipc_version:1,type:'capabilities',capabilities:{schema_version:2,profile:'sparkclaw.workbench.transport.v2',session_id:'session',authorization_revision:1,expires_at:new Date(Date.now()+60000).toISOString(),operations:['mail.file','object.read'],binding:{deployment_id:'d',owner_id:'o',client_id:'c'}}});
 f.transport.objects.download=async reference=>{assert.deepEqual(reference,object);return bytes;};
 const response=await f.transport.fetch(`${origin}/api/email/messages/mail/file`);
 assert.equal(response.headers.get('x-sparkclaw-digest'),object.sha256);
 assert.equal(response.headers.get('content-length'),String(bytes.length));
 assert.deepEqual(Buffer.from(await response.arrayBuffer()),bytes);
});

test('popup read bursts queue before dispatch while preserving write and control admission', async t => {
 const fs=await import('node:fs'), os=await import('node:os'), path=await import('node:path');
 const root=fs.mkdtempSync(path.join(os.tmpdir(),'iscp-popup-burst-'));t.after(()=>fs.rmSync(root,{recursive:true,force:true}));
 const active=[];
 const reply=(call,child)=>child.send({ipc_version:1,type:'response',id:call.id,response:{type:'task.result',profile:'sparkclaw.workbench.transport.v2',id:call.id,status:200,body:{operation:call.request.operation}}});
 const f=fixture(t,(call,child)=>{if(call.request.operation==='execution.cancel')reply(call,child);else active.push([call,child]);},{journalRoot:root,timeoutMS:1000});
 t.mock.method(globalThis,'fetch',()=>assert.fail('read queue bypassed encrypted ISCP'));
 await f.transport.start();
 const operations=['mail.sync.status','mail.providers.list','mail.conversations.list','mail.interaction','mail.conversations.get','mail.conversations.messages','mail.presentations.get','mail.drafts.list','mail.classification','execution.cancel'];
 f.children[0].send({ipc_version:1,type:'capabilities',capabilities:{schema_version:2,profile:'sparkclaw.workbench.transport.v2',session_id:'session',authorization_revision:1,expires_at:new Date(Date.now()+60000).toISOString(),operations,binding:{deployment_id:'d',owner_id:'o',client_id:'c'}}});
 const routes=['/api/email/sync-status','/api/email/providers','/api/email/conversations','/api/email/interaction-mails','/api/email/conversations/conversation','/api/email/conversations/conversation/messages','/api/email/presentations?target_kind=mail&language=en&target_id=mail','/api/email/providers','/api/email/drafts/draft','/api/email/drafts/draft'];
 const prior=f.transport.mutations.begin('mail-draft:draft',{operation:'mail.drafts.send',body:{expected_version:4}});
 const reads=routes.map(route=>f.transport.fetch(`${origin}${route}`).then(response=>response.json()));
 for(let i=0;i<5;i++)await Promise.resolve();
 assert.equal(active.length,3);assert.equal(f.transport.admissionQueue.size,7);
 const writing=f.transport.fetch(`${origin}/api/email/messages/mail/classification`,{method:'POST',body:JSON.stringify({expected_version:1,entry:'interaction',command_key:requestID})});
 for(let i=0;i<5;i++)await Promise.resolve();
 assert.equal(active.length,4,'one business slot remains available to an explicit mutation');
 assert.equal(active[3][0].request.operation,'mail.classification');
 assert.equal((await(await f.transport.fetch(`${origin}/api/v1/executions/${requestID}/cancel`,{method:'POST',body:'{}'})).json()).operation,'execution.cancel');
 reply(...active.splice(3,1)[0]);await writing;
 while(active.length){reply(...active.shift());for(let i=0;i<12;i++)await Promise.resolve();assert.ok(active.length<=3);}
 const results=await Promise.all(reads);
 assert.equal(results.length,routes.length);assert.equal(f.transport.admissionQueue.size,0);
 assert.equal(f.calls.length,routes.length+2,'every original read is dispatched only once');
 assert.equal(f.transport.mutations.read('mail-draft:draft').operation_id,prior.operation_id,'snapshot reads cannot clear an unknown send');
});

test('queued popup reads are bounded, abort before send, and drain on disconnect without crossing generations', async t => {
 const f=fixture(t,()=>{}, {timeoutMS:1000});await f.transport.start();
 const capabilities={schema_version:2,profile:'sparkclaw.workbench.transport.v2',session_id:'session',authorization_revision:1,expires_at:new Date(Date.now()+60000).toISOString(),operations:['mail.sync.status'],binding:{deployment_id:'d',owner_id:'o',client_id:'c'}};
 f.children[0].send({ipc_version:1,type:'capabilities',capabilities});
 const abort=new AbortController();
 const pending=Array.from({length:27},(_,index)=>f.transport.fetch(`${origin}/api/email/sync-status`,{...(index===3?{signal:abort.signal}:{})}).catch(error=>error));
 assert.equal(f.calls.length,3);assert.equal(f.transport.admissionQueue.size,24);
 await assert.rejects(f.transport.fetch(`${origin}/api/email/sync-status`),error=>error instanceof ISCPRequestNotSentError&&error.reason==='capacity');
 abort.abort();assert.equal(f.transport.admissionQueue.size,23);
 assert.ok(await pending[3] instanceof ISCPRequestNotSentError);
 f.transport.close();const results=await Promise.all(pending);
 assert.ok(results.slice(3).every(error=>error instanceof ISCPRequestNotSentError));assert.equal(f.transport.admissionQueue.size,0);assert.equal(f.calls.length,3);
 await f.transport.start();f.children.at(-1).send({ipc_version:1,type:'capabilities',capabilities:{...capabilities,session_id:'new-session'}});
 for(let i=0;i<5;i++)await Promise.resolve();assert.equal(f.calls.length,3,'old queued reads cannot use a new authenticated helper');
});

test('queued read admission retains its deadline and rechecks permission before writing', async t => {
 const f=fixture(t,()=>{}, {timeoutMS:30});await f.transport.start();
 t.mock.timers.enable({apis:['Date','setTimeout'],now:Date.now()});
 const capabilities={schema_version:2,profile:'sparkclaw.workbench.transport.v2',session_id:'session',authorization_revision:1,expires_at:new Date(Date.now()+60000).toISOString(),operations:['mail.sync.status','mail.conversations.list'],binding:{deployment_id:'d',owner_id:'o',client_id:'c'}};
 f.children[0].send({ipc_version:1,type:'capabilities',capabilities});
 const pending=Array.from({length:3},()=>f.transport.fetch(`${origin}/api/email/sync-status`).catch(error=>error));
 const queued=f.transport.fetch(`${origin}/api/email/conversations`).catch(error=>error);
 f.children[0].send({ipc_version:1,type:'capabilities',capabilities:{...capabilities,operations:['mail.sync.status']}});
 const call=f.calls[0];f.children[0].send({ipc_version:1,type:'response',id:call.id,response:{type:'task.result',profile:'sparkclaw.workbench.transport.v2',id:call.id,status:200,body:{}}});
 assert.ok(await queued instanceof ISCPRequestNotSentError);assert.equal(f.calls.length,3);
 const more=Array.from({length:3},()=>f.transport.fetch(`${origin}/api/email/sync-status`).catch(error=>error));
 t.mock.timers.tick(30);
 const results=await Promise.all([...pending,...more]);
 assert.ok(results.at(-1) instanceof ISCPRequestNotSentError,'an expired queued request retains unsent proof');
 assert.equal(f.calls.length,4,'a queued deadline must not restart a fresh RPC timeout');
 assert.equal(f.transport.admissionQueue.size,0);
});

test('queued execution never crosses cancellation, permission, session or authority changes', async t => {
 for (const transition of ['abort','permission','session','revision','disconnect']) {
  await t.test(transition, async t => {
   const f=fixture(t,()=>{}, {timeoutMS:1000});await f.transport.start();
   const capabilities={schema_version:2,profile:'sparkclaw.workbench.transport.v2',session_id:'session',authorization_revision:1,expires_at:new Date(Date.now()+60000).toISOString(),operations:['presentation.config','execution.submit'],binding:{deployment_id:'d',owner_id:'o',client_id:'c'}};
   f.children[0].send({ipc_version:1,type:'capabilities',capabilities});
   const busy=Array.from({length:4},()=>f.transport.fetch(`${origin}/api/config`).catch(error=>error));
   const abort=new AbortController();
   const sending=f.transport.fetch(`${origin}/api/v1/executions`,{method:'POST',body:JSON.stringify({request_id:requestID}),signal:abort.signal});
   const rejected=assert.rejects(sending,error=>error instanceof ISCPRequestNotSentError);
   assert.equal(f.transport.admissionQueue.size,1);assert.equal(f.calls.length,4);
   if(transition==='abort')abort.abort();
   else if(transition==='disconnect')f.transport.close();
   else f.children[0].send({ipc_version:1,type:'capabilities',capabilities:{...capabilities,
    ...(transition==='permission'?{operations:['presentation.config']}:transition==='session'?{session_id:'new-session'}:{authorization_revision:2})}});
   if(transition!=='disconnect') {
    const call=f.calls[0];f.children[0].send({ipc_version:1,type:'response',id:call.id,response:{type:'task.result',profile:capabilities.profile,id:call.id,status:200,body:{}}});
   }
   await rejected;assert.equal(f.calls.length,4,'canceled old intent must never be sent');assert.equal(f.transport.admissionQueue.size,0);
   f.transport.close();await Promise.all(busy);
  });
 }
});

test('execution admission and sent response share one deadline and retain distinct unsent proof', async t => {
 const f=fixture(t,()=>{}, {timeoutMS:100});await f.transport.start();
 t.mock.timers.enable({apis:['Date','setTimeout'],now:Date.now()});
 const busy=Array.from({length:4},()=>f.transport.fetch(`${origin}/api/config`).catch(error=>error));
 const init={method:'POST',body:JSON.stringify({request_id:requestID})};
 const sending=f.transport.fetch(`${origin}/api/v1/executions`,init).catch(error=>error);
 t.mock.timers.tick(70);
 const call=f.calls[0];f.children[0].send({ipc_version:1,type:'response',id:call.id,response:{type:'task.result',profile:ISCP_PROFILE,id:call.id,status:200,body:{}}});
 for(let i=0;i<5;i++)await Promise.resolve();
 assert.equal(f.calls.length,5);assert.equal(f.transport.admissionQueue.size,0);
 const abort=new AbortController();
 const unsent=f.transport.fetch(`${origin}/api/v1/executions`,{...init,signal:abort.signal}).catch(error=>error);
 abort.abort();
 assert.ok(await unsent instanceof ISCPRequestNotSentError);
 t.mock.timers.tick(30);
 const error=await sending;
 assert.ok(!(error instanceof ISCPRequestNotSentError));assert.match(error.message,/deadline/u);
 f.transport.close();
 await Promise.all(busy);
 assert.equal(f.calls.length,5,'an expired sent request must never be replayed');
});

test('all registered forward operations wait for admission in each negotiated capacity class', async t => {
 const limits={business:4,control:1,bulk:2,events:1,audio:2};
 for(const [capacityClass,limit] of Object.entries(limits)) {
  await t.test(capacityClass,async t=>{
   const rows=operations.filter(row=>row.direction==='forward'&&row.capacity_class===capacityClass);
   assert.ok(rows.length>0);
   const held=[];
   const f=fixture(t,(call,child)=>held.push([call,child]),{timeoutMS:1000});await f.transport.start();
   const profile='sparkclaw.workbench.transport.v2';
   f.children[0].send({ipc_version:1,type:'capabilities',capabilities:{schema_version:2,profile,session_id:'session',authorization_revision:1,expires_at:new Date(Date.now()+60000).toISOString(),operations:operations.map(row=>row.name),binding:{deployment_id:'d',owner_id:'o',client_id:'c'}}});
   const active=[];
   const respond=([call,child])=>child.send({ipc_version:1,type:'response',id:call.id,response:{type:'task.result',profile,id:call.id,status:200,body:{}}});
   const fill=()=>active.push(f.transport.invoke(rows[0].name,{}).catch(error=>error));
   for(let i=0;i<limit;i++)fill();
   for(const row of rows) {
    const count=f.calls.length;
    const waiting=f.transport.invoke(row.name,{});
    const observed=assert.doesNotReject(waiting);
    await new Promise(resolve=>setImmediate(resolve));
    assert.equal(f.calls.length,count,`${row.name} must wait before writing`);
    assert.equal(f.transport.admissionQueue.size,1);
    respond(held.shift());
    await new Promise(resolve=>setImmediate(resolve));
    assert.equal(f.calls.length,count+1);assert.equal(held.at(-1)[0].request.operation,row.name);
    assert.equal(f.transport.pending.size,limit);
    respond(held.pop());await observed;
    fill();
   }
   for(const item of held.splice(0))respond(item);
   await Promise.all(active);assert.equal(f.transport.admissionQueue.size,0);
  });
 }
});

test('a full popup queue leaves room for explicit execution and independent control admission', async t => {
 const held=[];
 const f=fixture(t,(call,child)=>held.push([call,child]),{timeoutMS:1000});await f.transport.start();
 const profile='sparkclaw.workbench.transport.v2';
 f.children[0].send({ipc_version:1,type:'capabilities',capabilities:{schema_version:2,profile,session_id:'session',authorization_revision:1,expires_at:new Date(Date.now()+60000).toISOString(),operations:['mail.sync.status','presentation.config','execution.submit','events.ack','capabilities.get'],binding:{deployment_id:'d',owner_id:'o',client_id:'c'}}});
 const all=[];
 const invoke=name=>{const p=f.transport.invoke(name,{});all.push(p.catch(error=>error));return p;};
 for(let i=0;i<27;i++)invoke('mail.sync.status');
 invoke('presentation.config');invoke('events.ack');
 const count=f.calls.length;
 const sending=invoke('execution.submit'),control=invoke('capabilities.get');
 assert.equal(f.calls.length,count);assert.equal(f.transport.admissionQueue.size,26);
 const respond=operation=>{const index=held.findIndex(([call])=>call.request.operation===operation);assert.ok(index>=0);const [call,child]=held.splice(index,1)[0];child.send({ipc_version:1,type:'response',id:call.id,response:{type:'task.result',profile,id:call.id,status:200,body:{}}});};
 respond('events.ack');await new Promise(resolve=>setImmediate(resolve));
 assert.equal(f.calls.at(-1).request.operation,'capabilities.get','business congestion cannot block the control class');
 respond('capabilities.get');await control;
 respond('presentation.config');await new Promise(resolve=>setImmediate(resolve));
 assert.equal(f.calls.at(-1).request.operation,'execution.submit','explicit execution precedes waiting mailbox snapshots');
 respond('execution.submit');await sending;
 f.transport.close();await Promise.all(all);
 assert.equal(f.transport.admissionQueue.size,0);
});

test('negotiated unbounded RPC removes every per-class, popup and admission-queue quota', async t => {
 const held=[];
 const f=fixture(t,(call,child)=>held.push([call,child]),{timeoutMS:5000});await f.transport.start();
 const profile='sparkclaw.workbench.transport.v2';
 const capabilities={schema_version:2,profile,session_id:'session',authorization_revision:1,expires_at:new Date(Date.now()+60000).toISOString(),operations:operations.map(row=>row.name),binding:{deployment_id:'d',owner_id:'o',client_id:'c'},limits:{concurrent:0}};
 f.children[0].send({ipc_version:1,type:'capabilities',capabilities});
 const active=[];
 for(const name of ['presentation.config','capabilities.get','object.read','events.pull','speech.session.frame','mail.sync.status']) {
  // Select the canonical audio operation from the registry, not a fixture alias.
  const operation=name==='speech.session.frame'?operations.find(row=>row.capacity_class==='audio'&&row.direction==='forward').name:name;
  for(let i=0;i<48;i++)active.push(f.transport.invoke(operation,{}));
 }
 assert.equal(f.calls.length,288);assert.equal(f.transport.admissionQueue.size,0);assert.equal(f.transport.pending.size,288);
 for(const [call,child] of held)child.send({ipc_version:1,type:'response',id:call.id,response:{type:'task.result',profile,id:call.id,status:200,body:{}}});
 await Promise.all(active);assert.equal(f.transport.pending.size,0);
});

test('verified helper admission also removes zero-epoch legacy-v1 quotas and clears on disconnect', async t => {
 const f=fixture(t,()=>{}, {timeoutMS:1000});await f.transport.start();
 f.children[0].send({ipc_version:1,type:'admission',profile:ISCP_PROFILE,session_id:'session',authorization_revision:0,expires_at:new Date(Date.now()+60000).toISOString(),concurrent:0});
 const active=Array.from({length:40},()=>f.transport.fetch(`${origin}/api/config`).catch(error=>error));
 assert.equal(f.calls.length,40);assert.equal(f.transport.admissionQueue.size,0);
 f.transport.close();await Promise.all(active);assert.equal(f.transport.admission,undefined);
});

function streamManifest() {
  return { schema_version: 2, stream_schema_version: 1, profile: "sparkclaw.workbench.transport.v2", session_id: "session", authorization_revision: 1,
    expires_at: new Date(Date.now() + 60000).toISOString(), binding: { deployment_id: "d", owner_id: "o", client_id: "c" },
    operations: ["execution.progress.stream", "presentation.config"], limits: { concurrent: 0 } };
}
function streamResponse(call, body) { return { ipc_version: 1, type: "response", id: call.id,
  response: { type: "task.result", profile: call.request.profile, id: call.id, status: 200, body } }; }
function streamEvent(call) { return { ipc_version: 1, type: "stream_event", id: call.id, authority_valid_for_ms: 300000, authority_deadline_ms: 300000,
  event: { type: "iscp.application.stream.v1", schema_version: 1, subscription_id: call.id, stream_seq: 1, body: { kind: "state" } } }; }

test("stream IPC requires matched helper/remote versions and does not use ordinary RPC deadlines or quotas", async t => {
  const f = fixture(t, () => {}, { hello: { ...hello, stream_schema_version: 1 }, timeoutMS: 10 }); await f.transport.start();
  f.children[0].send({ ipc_version: 1, type: "capabilities", capabilities: streamManifest() });
  const abort = new AbortController(), events = [], opened = [];
  const pending = Array.from({ length: 12 }, () => f.transport.streamCall("execution.progress.stream", { schema_version: 1 },
    { requestID, signal: abort.signal, onOpen: id => opened.push(id), onEvent: event => events.push(event) }));
  const observed = pending.map(promise => promise.catch(error => error));
  assert.equal(f.calls.length, 12); assert.equal(new Set(opened).size, 12);
  assert.ok(f.calls.every(call => call.type === "stream_call" && call.id === call.request.id && call.id !== requestID && call.request.body.schema_version === 1));
  await new Promise(resolve => setTimeout(resolve, 25)); assert.equal(f.transport.pending.size, 12);
  f.children[0].send(streamEvent(f.calls[0])); assert.equal(events.length, 1);
  abort.abort(); await Promise.all(observed);
  assert.equal(f.transport.pending.size, 0); assert.equal(f.transport.state, "transport_ready");
  assert.equal(f.calls.filter(call => call.type === "stream_cancel").length, 12);
  assert.equal(f.calls.filter(call => call.request?.operation === "execution.cancel").length, 0);
});

test("a stream terminal response can precede its tail without canceling other requests", async t => {
  const f = fixture(t, (call, child) => {
    if (call.type === "call") child.send(streamResponse(call, { ordinary: true }));
  }, { hello: { ...hello, stream_schema_version: 1 } }); await f.transport.start();
  f.children[0].send({ ipc_version: 1, type: "capabilities", capabilities: streamManifest() });
  const abort = new AbortController(), events = [];
  const pending = f.transport.streamCall("execution.progress.stream", {}, { requestID, signal: abort.signal, onEvent: event => events.push(event) });
  const call = f.calls[0]; f.children[0].send(streamResponse(call, { schema_version: 1, last_stream_seq: 1, reason: "terminal" }));
  assert.equal((await pending).last_stream_seq, 1);
  f.children[0].send(streamEvent(call)); assert.equal(events.length, 1);
  assert.deepEqual(await (await f.transport.fetch(`${origin}/api/config`)).json(), { ordinary: true });
  abort.abort(); f.children[0].send(streamEvent(call)); assert.equal(events.length, 1);
  assert.equal(f.transport.state, "transport_ready");
});

test("observer exceptions and stream-only cancellation cannot poison the shared helper or ordinary calls", async t => {
  const f = fixture(t, (call, child) => { if (call.type === "call") child.send(streamResponse(call, { ok: true })); },
    { hello: { ...hello, stream_schema_version: 1 } }); await f.transport.start();
  f.children[0].send({ ipc_version: 1, type: "capabilities", capabilities: streamManifest() });
  const failed = f.transport.streamCall("execution.progress.stream", {}, { requestID, onEvent: () => { throw new Error("invalid stream"); } });
  const rejected = assert.rejects(failed); f.children[0].send(streamEvent(f.calls[0])); await rejected;
  assert.equal(f.transport.state, "transport_ready");
  assert.deepEqual(await (await f.transport.fetch(`${origin}/api/config`)).json(), { ok: true });
});

test("unmatched stream schema rejects before write, and authority changes fence in-flight stream events", async t => {
  for (const field of ["helper", "remote"]) {
    const f = fixture(t, () => {}, { hello: { ...hello, stream_schema_version: field === "helper" ? 2 : 1 } }); await f.transport.start();
    f.children[0].send({ ipc_version: 1, type: "capabilities", capabilities: { ...streamManifest(), stream_schema_version: field === "remote" ? 2 : 1 } });
    await assert.rejects(f.transport.streamCall("execution.progress.stream", {}, { requestID, onEvent() {} }), /schema/); assert.equal(f.calls.length, 0);
  }
  const f = fixture(t, () => {}, { hello: { ...hello, stream_schema_version: 1 } }); await f.transport.start();
  f.children[0].send({ ipc_version: 1, type: "capabilities", capabilities: streamManifest() });
  let published = 0;
  const pending = f.transport.streamCall("execution.progress.stream", {}, { requestID, onEvent() { published++; } });
  const rejected = assert.rejects(pending);
  f.children[0].send({ ipc_version: 1, type: "capabilities", capabilities: { ...streamManifest(), authorization_revision: 2 } });
  f.children[0].send(streamEvent(f.calls[0])); await rejected;
  assert.equal(published, 0); assert.equal(f.transport.state, "transport_ready");
});

test("helper authority deadline is mandatory and delivered before the corresponding stream event", async t => {
  const f = fixture(t, () => {}, { hello: { ...hello, stream_schema_version: 1 } }); await f.transport.start();
  f.children[0].send({ ipc_version: 1, type: "capabilities", capabilities: streamManifest() });
  const order = [], abort = new AbortController();
  const pending = f.transport.streamCall("execution.progress.stream", {}, { requestID, signal: abort.signal,
    onAuthority: ms => order.push(["authority", ms]), onEvent: () => order.push(["event"]) });
  const rejected = assert.rejects(pending);
  f.children[0].send({ ...streamEvent(f.calls[0]), authority_valid_for_ms: 10 });
  assert.deepEqual(order, [["authority", 10], ["event"]]); abort.abort(); await rejected;
});

for (const authority of [undefined, null, 0, -1, 300001, 1.5, "10", Number.MAX_SAFE_INTEGER + 1]) {
  test(`invalid helper authority ${String(authority)} closes only its observation before any callback`, async t => {
    const f = fixture(t, () => {}, { hello: { ...hello, stream_schema_version: 1 } }); await f.transport.start();
    f.children[0].send({ ipc_version: 1, type: "capabilities", capabilities: streamManifest() });
    let callbacks = 0;
    const pending = f.transport.streamCall("execution.progress.stream", {}, { requestID,
      onAuthority: () => callbacks++, onEvent: () => callbacks++ });
    const rejected = assert.rejects(pending, error => error.code === "stream_authority_invalid");
    f.children[0].send({ ...streamEvent(f.calls[0]), authority_valid_for_ms: authority }); await rejected;
    assert.equal(callbacks, 0); assert.equal(f.transport.state, "transport_ready");
    assert.equal(f.calls.filter(call => call.type === "stream_cancel").length, 1);
  });
}

test("helper grant-expired error retains authenticated code and status for immediate preview invalidation", async t => {
  const f = fixture(t, () => {}, { hello: { ...hello, stream_schema_version: 1 } }); await f.transport.start();
  f.children[0].send({ ipc_version: 1, type: "capabilities", capabilities: streamManifest() });
  const pending = f.transport.streamCall("execution.progress.stream", {}, { requestID, onEvent() {} });
  const rejected = assert.rejects(pending, error => error.status === 403 && error.code === "grant_expired");
  const response = streamResponse(f.calls[0]); response.response.status = 403; response.response.error_code = "grant_expired";
  f.children[0].send(response); await rejected; assert.equal(f.transport.state, "transport_ready");
});

test("old IPC events cannot restart an elapsed helper authority deadline on arrival", async t => {
  let monotonic = 1000; t.mock.method(performance, "now", () => monotonic);
  const f = fixture(t, () => {}, { hello: { ...hello, stream_schema_version: 1 } }); await f.transport.start();
  f.children[0].send({ ipc_version: 1, type: "capabilities", capabilities: streamManifest() });
  let published = 0;
  const pending = f.transport.streamCall("execution.progress.stream", {}, { requestID,
    onAuthority() { published++; }, onEvent() { published++; } });
  const rejected = assert.rejects(pending, error => error.status === 403 && error.code === "grant_expired");
  monotonic += 20; // A ten-millisecond cached event waited in the helper pipe.
  f.children[0].send({ ...streamEvent(f.calls[0]), authority_valid_for_ms: 10, authority_deadline_ms: 10 });
  await rejected; assert.equal(published, 0); assert.equal(f.transport.state, "transport_ready");
});

test("helper-relative deadlines permit normal refresh beyond five minutes while deducting pipe delay", async t => {
  let monotonic = 1000; t.mock.method(performance, "now", () => monotonic);
  const f = fixture(t, () => {}, { hello: { ...hello, stream_schema_version: 1 } }); await f.transport.start();
  f.children[0].send({ ipc_version: 1, type: "capabilities", capabilities: { ...streamManifest(), expires_at: new Date(Date.now() + 600000).toISOString() } });
  const bounds = [], abort = new AbortController(); let events = 0;
  const pending = f.transport.streamCall("execution.progress.stream", {}, { requestID, signal: abort.signal,
    onAuthority: ms => bounds.push(ms), onEvent() { events++; } });
  const rejected = assert.rejects(pending);
  monotonic += 50;
  f.children[0].send({ ...streamEvent(f.calls[0]), authority_valid_for_ms: 100, authority_deadline_ms: 100 });
  assert.deepEqual(bounds, [50]);
  monotonic += 60000;
  f.children[0].send({ ...streamEvent(f.calls[0]), authority_valid_for_ms: 300000, authority_deadline_ms: 360000 });
  assert.deepEqual(bounds, [50, 299950]); assert.equal(events, 2);
  abort.abort(); await rejected;
});

for (const deadline of [undefined, null, -1, 1.5, "100", Number.MAX_SAFE_INTEGER + 1]) {
  test(`invalid helper-relative deadline ${String(deadline)} cannot authorize a frame`, async t => {
    const f = fixture(t, () => {}, { hello: { ...hello, stream_schema_version: 1 } }); await f.transport.start();
    f.children[0].send({ ipc_version: 1, type: "capabilities", capabilities: streamManifest() });
    let published = 0;
    const pending = f.transport.streamCall("execution.progress.stream", {}, { requestID, onEvent() { published++; } });
    const rejected = assert.rejects(pending, error => error.code === "stream_authority_invalid");
    f.children[0].send({ ...streamEvent(f.calls[0]), authority_deadline_ms: deadline }); await rejected;
    assert.equal(published, 0); assert.equal(f.transport.state, "transport_ready");
  });
}

test('reverse browser.command calls reach the browser handler on this session only', async t => {
  const capabilities = { schema_version: 2, profile: 'sparkclaw.workbench.transport.v2', session_id: 'session-1', authorization_revision: 3, expires_at: new Date(Date.now() + 60000).toISOString(), binding: { deployment_id: 'd', owner_id: 'o', client_id: 'c' }, operations: ['browser.command', 'browser.receipt', 'browser.reconcile'] };
  const seen = []; let release;
  const f = fixture(t, () => {}, { onReverse: async (request, session) => { seen.push({ request, session }); if (request.body.hold) await new Promise(resolve => { release = resolve; }); return { status: 200, body: { schema_version: 1, type: 'result', status: 'completed', echo: request.body.value } }; } });
  await f.transport.start(); f.children[0].send({ ipc_version: 1, type: 'capabilities', capabilities });
  const id = (n) => `5555555${n}-5555-4555-8555-555555555555`;
  const reverse = (n, operation, body) => f.children[0].send({ ipc_version: 1, type: 'reverse_request', id: id(n), request: { type: 'task.invoke', profile: capabilities.profile, id: id(n), operation, body } });
  const replies = () => f.calls.filter(call => call.type === 'reverse_response');
  reverse(1, 'browser.command', { value: 'page' });
  reverse(2, 'settings.owner.get', {});
  await new Promise(resolve => setTimeout(resolve, 20));
  const [command, denied] = [replies().find(call => call.id === id(1)), replies().find(call => call.id === id(2))];
  assert.deepEqual(command.response, { type: 'task.result', profile: capabilities.profile, id: id(1), status: 200, body: { schema_version: 1, type: 'result', status: 'completed', echo: 'page' } });
  assert.equal(denied.response.status, 403); assert.equal(denied.response.error_code, 'permission_denied');
  assert.deepEqual(seen.map(row => row.session), [{ session_id: 'session-1', authorization_revision: 3 }]);
  assert.equal(f.transport.state, 'transport_ready', 'a refused reverse operation does not end the session');
  // A reply for a helper that was replaced is dropped; the Gateway sees unknown.
  reverse(3, 'browser.command', { hold: true });
  await new Promise(resolve => setTimeout(resolve, 10));
  f.transport.close(); release();
  await new Promise(resolve => setTimeout(resolve, 10));
  assert.equal(replies().some(call => call.id === id(3)), false);
});

test('reverse browser.command without a browser handler is refused before executing', async t => {
  const capabilities = { schema_version: 2, profile: 'sparkclaw.workbench.transport.v2', session_id: 'session-1', authorization_revision: 3, expires_at: new Date(Date.now() + 60000).toISOString(), binding: { deployment_id: 'd', owner_id: 'o', client_id: 'c' }, operations: ['browser.command'] };
  const f = fixture(t, () => {});
  await f.transport.start(); f.children[0].send({ ipc_version: 1, type: 'capabilities', capabilities });
  const id = '66666666-6666-4666-8666-666666666666';
  f.children[0].send({ ipc_version: 1, type: 'reverse_request', id, request: { type: 'task.invoke', profile: capabilities.profile, id, operation: 'browser.command', body: {} } });
  await new Promise(resolve => setTimeout(resolve, 10));
  const reply = f.calls.find(call => call.type === 'reverse_response');
  assert.equal(reply.response.status, 503); assert.equal(reply.response.error_code, 'capability_unavailable');
});
