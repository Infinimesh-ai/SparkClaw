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

test('v2 bulk and audio windows leave a reserved control slot while excess bulk fails before send',async t=>{
 const operations=['object.read','speech.session.frame','execution.cancel'];
 const f=fixture(t,(call,child)=>{if(call.request.operation==='execution.cancel')child.send({ipc_version:1,type:'response',id:call.id,response:{type:'task.result',profile:'sparkclaw.workbench.transport.v2',id:call.id,status:200,body:{state:'canceled'}}});});
 await f.transport.start();f.children[0].send({ipc_version:1,type:'capabilities',capabilities:{schema_version:2,profile:'sparkclaw.workbench.transport.v2',session_id:'session',authorization_revision:1,expires_at:new Date(Date.now()+60000).toISOString(),operations,binding:{deployment_id:'d',owner_id:'o',client_id:'c'}}});
 const pending=[...Array.from({length:2},()=>f.transport.invoke('object.read',{}).catch(()=>{})),...Array.from({length:2},()=>f.transport.invoke('speech.session.frame',{}).catch(()=>{}))];
 await assert.rejects(f.transport.invoke('object.read',{}),ISCPRequestNotSentError);
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
  }, { journalRoot: root, timeoutMS: 20, readLocalFile() { assert.fail('receipt recovery read a local file'); } });
  await f.transport.start();
  f.children[0].send({ ipc_version: 1, type: 'capabilities', capabilities: { schema_version: 2, profile: 'sparkclaw.workbench.transport.v2', session_id: 'session', authorization_revision: 1, expires_at: new Date(Date.now() + 60000).toISOString(), operations: ['mail.drafts.list', 'mail.drafts.send', 'mail.drafts.reconcile', 'operations.receipt'], binding: { deployment_id: 'd', owner_id: 'o', client_id: 'c' } } });
  const request = { method: 'POST', body: JSON.stringify({ expected_version: 4, idempotency_key: requestID }) };
  await assert.rejects(f.transport.fetch(`${origin}/api/email/drafts/draft/send`, request), /deadline/);
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
    child.send({ ipc_version: 1, type: 'response', id: call.id, response: { type: 'task.result', profile: 'sparkclaw.workbench.transport.v2', id: call.id, status: 200, body: { state: 'completed', response: { status: 200, body: { id: 'draft', version: 1 } } } } });
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
     child.send({ipc_version:1,type:'response',id:call.id,response:{type:'task.result',profile:'sparkclaw.workbench.transport.v2',id:call.id,status:200,body:reply?{state:'completed',response:result}:{state:'unknown'}}});
     return;
   }
   assert.ok(operation_id);
   mutations.set(operation,(mutations.get(operation)||0)+1);
   receipts.set(operation_id,{status:200,body:{operation,version:3}});
 },{journalRoot:root,timeoutMS:15});
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
 assert.equal(active.length,3);assert.equal(f.transport.mailReadQueue.size,7);
 const writing=f.transport.fetch(`${origin}/api/email/messages/mail/classification`,{method:'POST',body:JSON.stringify({expected_version:1,entry:'interaction',command_key:requestID})});
 for(let i=0;i<5;i++)await Promise.resolve();
 assert.equal(active.length,4,'one business slot remains available to an explicit mutation');
 assert.equal(active[3][0].request.operation,'mail.classification');
 assert.equal((await(await f.transport.fetch(`${origin}/api/v1/executions/${requestID}/cancel`,{method:'POST',body:'{}'})).json()).operation,'execution.cancel');
 reply(...active.splice(3,1)[0]);await writing;
 while(active.length){reply(...active.shift());for(let i=0;i<12;i++)await Promise.resolve();assert.ok(active.length<=3);}
 const results=await Promise.all(reads);
 assert.equal(results.length,routes.length);assert.equal(f.transport.mailReadQueue.size,0);
 assert.equal(f.calls.length,routes.length+2,'every original read is dispatched only once');
 assert.equal(f.transport.mutations.read('mail-draft:draft').operation_id,prior.operation_id,'snapshot reads cannot clear an unknown send');
});

test('queued popup reads are bounded, abort before send, and drain on disconnect without crossing generations', async t => {
 const f=fixture(t,()=>{}, {timeoutMS:1000});await f.transport.start();
 const capabilities={schema_version:2,profile:'sparkclaw.workbench.transport.v2',session_id:'session',authorization_revision:1,expires_at:new Date(Date.now()+60000).toISOString(),operations:['mail.sync.status'],binding:{deployment_id:'d',owner_id:'o',client_id:'c'}};
 f.children[0].send({ipc_version:1,type:'capabilities',capabilities});
 const abort=new AbortController();
 const pending=Array.from({length:27},(_,index)=>f.transport.fetch(`${origin}/api/email/sync-status`,{...(index===3?{signal:abort.signal}:{})}).catch(error=>error));
 assert.equal(f.calls.length,3);assert.equal(f.transport.mailReadQueue.size,24);
 await assert.rejects(f.transport.fetch(`${origin}/api/email/sync-status`),error=>error instanceof ISCPRequestNotSentError&&error.reason==='capacity');
 abort.abort();assert.equal(f.transport.mailReadQueue.size,23);
 assert.ok(await pending[3] instanceof ISCPRequestNotSentError);
 f.transport.close();const results=await Promise.all(pending);
 assert.ok(results.slice(3).every(error=>error instanceof ISCPRequestNotSentError));assert.equal(f.transport.mailReadQueue.size,0);assert.equal(f.calls.length,3);
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
 assert.equal(f.transport.mailReadQueue.size,0);
});
