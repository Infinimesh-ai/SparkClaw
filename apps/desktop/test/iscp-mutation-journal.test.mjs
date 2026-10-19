import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { ISCPMutationJournal } from '../src/main/iscp-mutation-journal.mjs';
import { mutationProtector } from './fixtures/mutation-protector.mjs';
const scope = { deployment_id: 'deployment', owner_id: 'owner', client_id: 'client' };
const request = { operation: 'mail.drafts.send', body: { subject: 'PRIVATE-INTENT-CANARY', expected_version: 7 } };
const response = { status: 200, body: { result: 'PRIVATE-RESULT-CANARY' } };
function fixture(t) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'mutation-journal-'));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const open = () => new ISCPMutationJournal(root, scope, mutationProtector);
  return { root, open, journal: open() };
}
test('restart retains encrypted intent, immutable identity, attempt and terminal/application evidence', t => {
  const f = fixture(t), deadline = Date.now() + 60000;
  const row = f.journal.prepare('draft:1', request, { rawBody: JSON.stringify(request.body), deadline, revision: '7', authorizationRevision: 2 });
  assert.equal(f.open().pending()[0].phase, 'prepared');
  f.open().markAttempted('draft:1', row.operation_id);
  assert.equal(f.open().read('draft:1').phase, 'attempted');
  assert.throws(() => f.open().markAttempted('draft:1', row.operation_id), /already attempted/);
  assert.throws(() => f.open().complete('draft:1', row.operation_id), /unresolved/);
  assert.throws(() => f.open().prepare('draft:1', { ...request, body: {} }, { deadline }), /reconciliation/);
  f.open().saveResult('draft:1', row.operation_id, response);
  assert.equal(f.open().pending().length, 0);
  assert.throws(() => f.open().saveResult('draft:1', row.operation_id, { status: 200, body: {} }), /changed/);
  f.open().complete('draft:1', row.operation_id);
  assert.equal(f.open().prepare('draft:1', request, { deadline }).operation_id, row.operation_id);
  const next = f.open().prepare('draft:1', { ...request, body: { expected_version: 8 } }, { deadline });
  assert.notEqual(next.operation_id, row.operation_id);
  assert.equal(fs.readdirSync(f.root).filter(name => name.endsWith('.done')).length, 1);
  for (const name of fs.readdirSync(f.root)) {
    const bytes = fs.readFileSync(path.join(f.root, name));
    for (const secret of ['PRIVATE-INTENT-CANARY', 'PRIVATE-RESULT-CANARY', row.operation_id]) assert.equal(bytes.includes(Buffer.from(secret)), false);
  }
});
test('unavailable protection, damaged ciphertext, wrong scope and symlink do not create fresh histories', t => {
  const f = fixture(t), deadline = Date.now() + 60000;
  assert.throws(() => new ISCPMutationJournal(f.root, scope).prepare('draft:1', request, { deadline }), /Secure/);
  f.journal.prepare('draft:1', request, { deadline });
  const name = fs.readdirSync(f.root).find(name => name.endsWith('.sealed'));
  const bytes = fs.readFileSync(path.join(f.root, name));
  const other = new ISCPMutationJournal(f.root, { ...scope, client_id: 'other' }, mutationProtector);
  assert.equal(other.pending().length, 0);
  bytes[15] ^= 1; fs.writeFileSync(path.join(f.root, name), bytes);
  assert.throws(() => f.open().read('draft:1'), /unlocked/);
  assert.throws(() => f.open().prepare('draft:1', request, { deadline }), /unlocked/);
  fs.unlinkSync(path.join(f.root, name)); fs.symlinkSync('/dev/null', path.join(f.root, name));
  assert.throws(() => f.open().pending());
});
test('expired never-attempted intent can close locally but attempted work remains a fence', t => {
  const f = fixture(t), row = f.journal.prepare('draft:1', request, { deadline: Date.now() - 1 });
  assert.throws(() => f.journal.markAttempted('draft:1', row.operation_id), /expired/);
  f.journal.cancelPrepared('draft:1', row.operation_id);
  assert.equal(f.open().read('draft:1').response.body.code, 'operation_not_sent');
  const second = f.journal.prepare('draft:2', request, { deadline: Date.now() + 60000 });
  f.journal.markAttempted('draft:2', second.operation_id);
  f.journal.cancelPrepared('draft:2', second.operation_id);
  assert.equal(f.open().read('draft:2').phase, 'attempted');
});
