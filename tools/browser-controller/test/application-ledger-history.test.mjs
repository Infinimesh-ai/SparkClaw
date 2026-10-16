import {test} from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import crypto from 'node:crypto';

const runtime = new URL('../', import.meta.resolve('@infinimesh/app-cli-runtime/release'));
const {Ledger} = await import(new URL('src/ledger.mjs', runtime));

function fixture(t, options) {
  const root = fs.mkdtempSync(path.join(fs.realpathSync(os.tmpdir()), 'app-ledger-history-'));
  const state = {root, ledger: new Ledger(root, options)};
  t.after(() => {state.ledger.close(); fs.rmSync(root, {recursive: true, force: true});});
  return state;
}
// Lifecycle 3.0 identities in the first generation and request epoch.
const key = name => `op3.1.1.${crypto.createHash('sha256').update(name).digest('hex').slice(0, 32)}`;
function admit(ledger, name = 'original', intent = 'intent') {
  return ledger.prepare({app: 'fixture', command: 'read', authorization_ref: 'fixture-grant'},
    {principal: 'fixture', owner: 'fixture', request_key: key(name), revision: 1,
      execution_expires_ms: Date.now() + 60000, side_effect: 'read_only'}, intent, 'resource', 'pure_read');
}

test('installed release accepts task 10001 after restart and preserves original history', t => {
  const state = fixture(t), first = admit(state.ledger);
  state.ledger.transaction(() => {
    state.ledger.update(first.row.id, {status: 'completed', data: '{"value":42}'});
    state.ledger.db.prepare(`WITH RECURSIVE history(n) AS (VALUES(1) UNION ALL SELECT n+1 FROM history WHERE n<9999)
      INSERT INTO tasks(id,principal,owner,request_key,intent,app,command,request,grant_ref,resource_digest,grant_revision,
        expires,side_effect,status,reason,data,effect,sequence,epoch) SELECT 'history-'||n,principal,owner,'history-'||n,intent,app,command,request,grant_ref,
        resource_digest,grant_revision,expires,side_effect,status,reason,data,effect,sequence,epoch
      FROM tasks,history WHERE id=?`).run(first.row.id);
  });
  const original = state.ledger.get(first.row.id), events = state.ledger.events(original, 0), epoch = state.ledger.epoch;
  state.ledger.close(); state.ledger = new Ledger(state.root);
  assert.ok(state.ledger.epoch > epoch);
  assert.equal(admit(state.ledger, 'after-full-history').fresh, true);
  assert.equal(state.ledger.db.prepare('SELECT COUNT(*) AS n FROM tasks').get().n, 10001);
  assert.deepEqual(admit(state.ledger).row, original);
  assert.equal(admit(state.ledger).fresh, false);
  assert.deepEqual(state.ledger.events(original, 0), events);
  assert.throws(() => admit(state.ledger, 'original', 'changed'), {code: 'REQUEST_KEY_CONFLICT'});
});

test('installed release keeps explicit task caps and the independent byte admission limit', t => {
  const bounded = fixture(t, {maxTasks: 1}), original = admit(bounded.ledger);
  assert.throws(() => admit(bounded.ledger, 'second'), {code: 'LEDGER_CAPACITY_EXCEEDED'});
  assert.equal(admit(bounded.ledger).row.id, original.row.id);
  const bytes = fixture(t, {maxTasks: 0, maxBytes: 1});
  assert.throws(() => admit(bytes.ledger), {code: 'LEDGER_CAPACITY_EXCEEDED'});
  assert.equal(bytes.ledger.db.prepare('SELECT COUNT(*) AS n FROM tasks').get().n, 0);
});
