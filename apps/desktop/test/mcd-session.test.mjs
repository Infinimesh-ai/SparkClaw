import test from 'node:test';
import assert from 'node:assert/strict';
import { MCDSession } from '../src/main/mcd/session.mjs';

function harness() {
  const context = { deployment_id: 'deployment', owner_id: 'owner', client_id: 'client', installation_id: 'installation', operations: ['mcd.session.provision', 'mcd.session.revoke', 'mcd.session.status'] };
  let saved, fail = false, issues = 0;
  const requests = [];
  const options = {
    vault: { loadMCDConnection: async () => structuredClone(saved), saveMCDConnection: async (_, row) => { saved = structuredClone(row); } },
    context: () => context,
    selectSource: async () => ({ root: '/private/selected', alias: 'personal' }),
    issue: async params => { issues++; return { identity: { ...params.binding, member_digest: 'a'.repeat(64) }, connection_id: params.connection_id, provision_request_id: params.request_id,
      credential_revision: 1, grant_revision: params.previous.grant_revision + 1, material_digest: 'b'.repeat(64), envelope_digest: 'c'.repeat(64), material: { secret: 'SYNTHETIC-PRIVATE' }, session_not_after_ms: Date.now() + 60000 }; },
    invoke: async (op, body) => {
      requests.push({ op, body: structuredClone(body) });
      if (fail) throw new Error('lost response');
      if (op === 'mcd.session.provision') return { request_id: body.provision_request_id, digest: body.envelope_digest, connection_id: body.connection_id, disposition: 'accepted' };
      if (op === 'mcd.session.revoke') return { request_id: body.request_id, connection_id: body.connection_id, disposition: 'revoked' };
      return { connections: saved ? [{ connection_id: saved.connection_id, state: saved.state === 'revoked' ? 'revoked' : 'ready' }] : [] };
    },
  };
  return { options, requests, context, saved: () => saved, issues: () => issues, fail: value => { fail = value; } };
}

test('native restart retries exact lost provision without renewing its lease', async () => {
  const h = harness(); h.fail(true);
  await assert.rejects(new MCDSession(h.options).connect());
  assert.equal(h.saved().state, 'pending');
  const original = structuredClone(h.saved().pending);
  h.fail(false);
  const result = await new MCDSession(h.options).connect();
  assert.equal(result.state, 'ready');
  assert.equal(h.issues(), 1);
  assert.deepEqual(h.requests[1].body, original);
  assert.ok(!JSON.stringify(result).includes('PRIVATE'));
});

test('lost revocation fences renewal and retains original id through restart', async () => {
  const h = harness(), session = new MCDSession(h.options);
  await session.connect(); const original = h.saved().connection_id;
  h.fail(true); await assert.rejects(session.disconnect());
  const request = h.saved().revoke_id;
  await assert.rejects(new MCDSession(h.options).connect(), /Finish disconnecting/);
  h.fail(false); await new MCDSession(h.options).disconnect();
  assert.equal(h.saved().revocation.request_id, request);
  assert.equal(h.saved().envelope, undefined);
  assert.equal(h.saved().pending, undefined);
  assert.equal(h.saved().connection_id, original);
  await new MCDSession(h.options).connect();
  assert.notEqual(h.saved().connection_id, original);
});

test('scope replacement rejects saved account before issuer or transport', async () => {
  const h = harness(); await new MCDSession(h.options).connect();
  h.context.owner_id = 'different';
  await assert.rejects(new MCDSession(h.options).connect(), /needs recovery/);
  assert.equal(h.issues(), 1);
});

test('offline disconnect clears local secrets and reconnect revokes before renewal', async () => {
 const h=harness(),session=new MCDSession(h.options);await session.connect();
 const operations=h.context.operations;h.context.operations=[];
 assert.equal((await session.status()).disconnect_available,true);
 assert.equal((await session.disconnect()).state,'revoking');
 assert.equal(h.saved().envelope,undefined);assert.equal(h.saved().pending,undefined);
 const key=h.saved().revoke_id,issues=h.issues();
 h.context.operations=operations;
 await session.maintain();
 assert.equal(h.saved().state,'revoked');assert.equal(h.saved().revocation.request_id,key);assert.equal(h.issues(),issues);
});

test('expired pending provisioning advances its original grant instead of retrying expired access forever', async () => {
 const h=harness();h.fail(true);await assert.rejects(new MCDSession(h.options).connect());
 const pending=structuredClone(h.saved().pending);h.fail(false);
 await new MCDSession({...h.options,now:()=>pending.session_not_after_ms+1}).connect();
 assert.equal(h.saved().envelope.grant_revision,pending.grant_revision+1);
 assert.equal(h.saved().expired_provision.request_id,pending.provision_request_id);
 assert.equal(h.saved().connection_id,pending.connection_id);
});

test('same-account reconnect retains its ledger anchor and never provisions the full native account', async () => {
  const h = harness();
  const issued = [];
  const issue = h.options.issue;
  h.options.issue = async params => { issued.push(structuredClone(params)); return issue(params); };
  h.options.capture = async () => ({ material_digest: 'b'.repeat(64), account_json: 'NATIVE-ONLY-WALLET-RUNTIME' });
  const session = new MCDSession(h.options);
  await session.connect();
  const { resource } = session.localScope();
  await h.options.vault.saveMCDConnection(resource, { ...h.saved(), host_ledger: 'ready' });
  const original = structuredClone(h.saved());
  await session.disconnect();
  assert.equal(h.saved().host_account, undefined);
  await new MCDSession(h.options).connect();
  assert.notEqual(h.saved().connection_id, original.connection_id);
  assert.equal(h.saved().account_ref, original.account_ref);
  assert.deepEqual(h.saved().identity, original.identity);
  assert.equal(h.saved().host_ledger, 'ready');
  assert.equal(issued[1].previous.material_digest, original.material_digest);
  assert.equal(issued[1].previous.credential_revision, original.credential_revision);
  assert.equal(issued[1].previous.grant_revision, 0);
  assert.ok(!JSON.stringify(h.requests).includes('NATIVE-ONLY-WALLET-RUNTIME'));
});
