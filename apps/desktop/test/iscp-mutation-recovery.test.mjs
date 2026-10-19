import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { EventEmitter } from 'node:events';
import { PassThrough, Writable } from 'node:stream';
import { ISCPTransport, ISCP_OPERATIONS } from '../src/main/iscp-transport.mjs';
import { mutationProtector } from './fixtures/mutation-protector.mjs';
const origin = 'https://iscp.invalid';
const input = { method: 'PUT', body: JSON.stringify({ expected_version: 2, subject: 'Private original', attachments: [] }) };
const route = `${origin}/api/email/drafts/draft`;
const tick = () => new Promise(resolve => setImmediate(resolve));
async function eventually(check) { for (let i = 0; i < 100; i++) { if (check()) return; await new Promise(resolve => setTimeout(resolve, 5)); } assert.fail('recovery did not settle'); }
function fixture(t) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'mutation-ready-'));
  const f = { root, calls: [], effects: 0, lost: true, missing: false, children: [] };
  const capabilities = { schema_version: 2, profile: 'sparkclaw.workbench.transport.v2', session_id: 'session', authorization_revision: 1,
    expires_at: new Date(Date.now()+60000).toISOString(), operations: ['mail.drafts.save','operations.receipt','settings.owner.get','settings.owner.patch'],
    binding: { deployment_id: 'd', owner_id: 'o', client_id: 'c' } };
  const spawnProcess = () => {
    const child = new EventEmitter(); child.stdout = new PassThrough(); child.stderr = new PassThrough(); child.exitCode = null;
    child.send = frame => child.stdout.write(JSON.stringify({ ipc_version: 1, ...frame })+'\n');
    child.kill = () => { child.exitCode = 0; };
    child.stdin = new Writable({ write(chunk, _, done) {
      const call = JSON.parse(chunk); f.calls.push(call);
      let body, status = 200;
      if (call.request.operation === 'mail.drafts.save') {
        assert.equal(f.transport.mutations.read('mail-draft:draft').phase, 'attempted', 'attempt must survive process loss before pipe write');
        f.effects++; f.operationID = call.request.operation_id;
        if (f.lost) { done(); return; }
        body = { id: 'draft', version: 3 };
      } else if (call.request.operation === 'operations.receipt') {
        assert.equal(call.request.params.operation_id, f.operationID);
        status = f.missing ? 404 : 200;
        body = f.missing ? { code: 'operation_not_found' } : { state: 'completed', operation_id: f.operationID, response: { status: 200, body: { id: 'draft', version: 3 } } };
      } else if (call.request.operation === 'settings.owner.patch') {
        f.effects++; f.operationID = call.request.operation_id;
        f.owner = JSON.parse(Buffer.from(call.body_base64, 'base64')); f.revision = `revision-${f.effects}`;
        body = { value: f.owner, revision: f.revision };
      } else body = f.owner ? { value: f.owner, revision: f.revision } : { display_name: 'current' };
      const frame = { type: 'response', id: call.id, response: { type: 'task.result', profile: capabilities.profile, id: call.id, status, body } };
      if (f.holdReceipt && call.request.operation === 'operations.receipt') f.held = () => child.send(frame); else child.send(frame);
      done();
    } });
    f.children.push(child);
    queueMicrotask(() => { child.send({ type: 'hello', operations: ISCP_OPERATIONS, max_request_bytes: 65536, max_response_bytes: 65536 });
      child.send({ type: 'capabilities', capabilities }); child.send({ type: 'state', state: 'transport_ready' }); });
    return child;
  };
  f.transport = new ISCPTransport({ origin, configPath: '/private/test.json', spawnProcess, timeoutMS: 50, journalRoot: root, mutationProtector });
  f.start = () => f.transport.start();
  f.authority = revision => { capabilities.authorization_revision = revision; f.children.at(-1).send({ type: 'capabilities', capabilities }); };
  f.restart = async revision => { f.transport.close(); capabilities.authorization_revision = revision ?? capabilities.authorization_revision; await f.start(); };
  t.after(() => { f.transport.close(); fs.rmSync(root, { recursive: true, force: true }); });
  return f;
}
test('business READY automatically looks up an attempted original ID and preserves terminal result through IPC loss', async t => {
  const f = fixture(t); await f.start();
  await assert.rejects(f.transport.fetch(route,input), /deadline/);
  const id = f.operationID;
  await f.restart();
  await eventually(() => f.transport.mutations.read('mail-draft:draft')?.phase === 'terminal');
  assert.equal(f.effects,1);
  assert.equal(f.calls.filter(call=>call.request.operation==='operations.receipt').length,1);
  assert.equal((await(await f.transport.fetch(route,input)).json()).version,3);
  await f.restart();
  assert.equal((await(await f.transport.fetch(route,input)).json()).version,3);
  assert.equal(f.transport.mutations.read('mail-draft:draft').operation_id,id);
  assert.equal(f.effects,1);
});
test('a missing receipt after reconnect keeps uncertainty and never admits another effect, including a new auth revision', async t => {
  const f = fixture(t); await f.start(); await assert.rejects(f.transport.fetch(route,input), /deadline/);
  f.missing = true; await f.restart(2); await tick(); await tick();
  await assert.rejects(f.transport.fetch(route,input));
  assert.equal(f.transport.mutations.read('mail-draft:draft').phase,'attempted'); assert.equal(f.effects,1);
});
test('a late old-session lookup cannot install a terminal result after replacement READY', async t => {
  const f = fixture(t); await f.start(); await assert.rejects(f.transport.fetch(route,input), /deadline/);
  f.holdReceipt = true; await f.restart(); await eventually(()=>Boolean(f.held));
  const oldReply = f.held; f.holdReceipt = false; f.missing = true;
  await f.restart(); oldReply(); await tick(); await tick();
  assert.equal(f.transport.mutations.read('mail-draft:draft').phase,'attempted'); assert.equal(f.effects,1);
});
test('only persisted never-attempted intent may resume at READY, under its original deadline and authority', async t => {
  const f = fixture(t); f.lost = false; await f.start();
  const req = { type: 'task.invoke', profile: 'sparkclaw.workbench.transport.v2', operation: 'mail.drafts.save', params: { draft: 'draft' }, body: { id: 'draft', expected_version: 2, attachments: [] } };
  const row = f.transport.mutations.prepare('mail-draft:draft',req,{ rawBody: JSON.stringify(req.body), deadline: Date.now()+60000, authorizationRevision: 1 });
  await f.restart(); await eventually(()=>f.transport.mutations.read('mail-draft:draft')?.phase==='terminal');
  assert.equal(f.operationID,row.operation_id); assert.equal(f.effects,1);
  const expired = f.transport.mutations.prepare('expired', req, { deadline: Date.now()-1, authorizationRevision: 1 });
  const narrowed = f.transport.mutations.prepare('narrowed', req, { deadline: Date.now()+60000, authorizationRevision: 1 });
  await f.restart(2); await eventually(()=>f.transport.mutations.read('narrowed')?.phase==='terminal');
  assert.equal(f.transport.mutations.read('expired').operation_id,expired.operation_id);
  assert.equal(f.transport.mutations.read('narrowed').operation_id,narrowed.operation_id);
  assert.equal(f.effects,1);
});
test('failure to persist the attempt writes no call and leaves the shared helper usable', async t => {
  const f = fixture(t); await f.start();
  f.transport.mutations.markAttempted = () => { throw new Error('synthetic disk failure'); };
  await assert.rejects(f.transport.fetch(route,input), /disk failure/);
  assert.equal(f.effects,0); assert.equal(f.transport.state,'transport_ready');
  assert.equal((await(await f.transport.fetch(`${origin}/api/owner`)).json()).display_name,'current');
});

test('READY does not renew an expired intent; a later explicit action can succeed its proved non-submission', async t => {
  const f = fixture(t); await f.start(); f.transport.timeoutMS = 0;
  assert.equal((await f.transport.fetch(route, input)).status, 409);
  const previous = f.transport.mutations.read('mail-draft:draft');
  assert.equal(previous.response.body.code, 'operation_not_sent'); assert.equal(f.effects, 0);
  f.transport.timeoutMS = 50; await f.restart(); await tick(); assert.equal(f.effects, 0);
  f.lost = false; assert.equal((await f.transport.fetch(route, input)).status, 200);
  assert.notEqual(f.operationID, previous.operation_id); assert.equal(f.effects, 1);
});

test('an independently read newer revision permits the same owner input as a new action', async t => {
  const f = fixture(t); await f.start();
  const options = { method: 'POST', body: JSON.stringify({ display_name: 'X' }) };
  await f.transport.fetch(`${origin}/api/owner`, options);
  const first = f.operationID;
  f.owner = { display_name: 'Y' }; f.revision = 'other-client-revision';
  await f.transport.fetch(`${origin}/api/owner`);
  await f.transport.fetch(`${origin}/api/owner`, options);
  assert.equal(f.owner.display_name, 'X'); assert.equal(f.effects, 2); assert.notEqual(first, f.operationID);
});

test('explicit action IDs distinguish identical completed inputs and preserve uncertain originals', async t => {
  const f = fixture(t); await f.start(); f.lost = false;
  const first = { ...input, headers: { 'x-sparkclaw-action-id': '00000000-0000-4000-8000-000000000001' } };
  const next = { ...input, headers: { 'x-sparkclaw-action-id': '00000000-0000-4000-8000-000000000002' } };
  await f.transport.fetch(route, first); const original = f.operationID;
  await f.transport.fetch(route, first); assert.equal(f.effects, 1);
  await f.transport.fetch(route, next); assert.equal(f.effects, 2); assert.notEqual(f.operationID, original);
  f.lost = true;
  const unknown = { ...input, headers: { 'x-sparkclaw-action-id': '00000000-0000-4000-8000-000000000003' } };
  await assert.rejects(f.transport.fetch(route, unknown), /deadline/); const unresolved = f.operationID;
  f.missing = true;
  await assert.rejects(f.transport.fetch(route, first));
  assert.equal(f.effects, 3); assert.equal(f.transport.mutations.read('mail-draft:draft').operation_id, unresolved);
});

test('authority changing during an awaited body upload prevents the original mutation write', async t => {
  const f = fixture(t); await f.start();
  f.transport.objects.upload = async () => {
    f.authority(2);
    return { object_id: 'synthetic', version: 1 };
  };
  const large = { method: 'PUT', body: JSON.stringify({ expected_version: 2, subject: 'X'.repeat(70000), attachments: [] }) };
  await assert.rejects(f.transport.fetch(route, large), /authentication changed/);
  assert.equal(f.effects, 0);
  assert.notEqual(f.transport.mutations.read('mail-draft:draft').phase, 'attempted');
});
