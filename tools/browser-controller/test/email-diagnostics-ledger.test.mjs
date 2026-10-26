import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import crypto from 'node:crypto';
import {DatabaseSync} from 'node:sqlite';
import {spawnSync} from 'node:child_process';
import {fileURLToPath, pathToFileURL} from 'node:url';
import {readLedger} from '../scripts/email-diagnostics-ledger.mjs';

const task = '11111111-1111-4111-8111-111111111111';
const key = `op3.1.4.${'a'.repeat(32)}`;
const scope = 'a'.repeat(64);
const legacy = {invocation: 'email_changes_fixture_r1', app: 'mail-gmail', command: 'collect_page'};
// Frozen independently from the unchanged .41 canonical encoder (bcc55506).
const legacyKey = 'c0b2bff4910620bf3e314e0acce032ca135f72bf7e3edb139a1b2581b786d610';
const headName = '14c06e0fb30933db33d66d1e04f299d5ddade8a562a65117593d65934fe0a793.head.json';
const aliasName = 'alias-2b5016559bd021032bd598736ba7b2bce12347768fd09d5414d0f2f4a326ada5.json';
const guarded = {skip: process.platform !== 'darwin' ? 'macOS write guard unavailable; production must return incomplete' : false};
const reader = fileURLToPath(new URL('../scripts/email-diagnostics-ledger.mjs', import.meta.url));
const canonicalReader = fileURLToPath(new URL('../../../services/gateway/internal/emaildiagnostics/ledger_reader.mjs', import.meta.url));
function fixture(t, {wal = false, row = {}, schema = 2, nocasePrincipal = false} = {}) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'email-ledger-read-'));
  fs.chmodSync(root, 0o700);
  const file = path.join(root, 'ledger.sqlite'), index = path.join(root, 'index');
  fs.mkdirSync(index, {mode: 0o700});
  const db = new DatabaseSync(file);
  if (wal) db.exec('PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=0;');
  db.exec(`CREATE TABLE metadata(key TEXT PRIMARY KEY,value INTEGER NOT NULL);
    CREATE TABLE tasks(id TEXT PRIMARY KEY,principal TEXT ${nocasePrincipal ? 'COLLATE NOCASE' : ''} NOT NULL,owner TEXT NOT NULL,request_key TEXT NOT NULL,
      app TEXT NOT NULL,command TEXT NOT NULL,request TEXT NOT NULL,status TEXT NOT NULL,reason TEXT,effect INTEGER NOT NULL,
      attempt INTEGER NOT NULL,dispatch TEXT,terminal_revision INTEGER NOT NULL,result_digest TEXT,ack TEXT,sequence INTEGER NOT NULL,
      UNIQUE(principal,owner,request_key));
    CREATE TABLE events(task TEXT NOT NULL,sequence INTEGER NOT NULL,type TEXT NOT NULL,payload TEXT NOT NULL,PRIMARY KEY(task,sequence));`);
  for (const [k,v] of Object.entries({schema_version: schema, epoch: 4, commit_sequence: 3, generation: 1, open_epoch: 4, sealed_through: 2, authorization_generation: 1})) db.prepare('INSERT INTO metadata VALUES (?,?)').run(k,v);
  const r = {id: task, principal: 'product-owner', owner: 'ledger-owner', request_key: key, app: 'mail-gmail', command: 'send',
    request: JSON.stringify({app: 'mail-gmail', command: 'send', request_key: key, arguments: {owner_scope: scope, body: 'private-body', recipient: 'private@example.test'}}),
    status: 'uncertain', reason: 'EMAIL_NETWORK_READ_FAILED', effect: 1, attempt: 1, dispatch: 'released', terminal_revision: 1, result_digest: 'c'.repeat(64), ack: null, sequence: 2, ...row};
  const keys = Object.keys(r);
  db.prepare(`INSERT INTO tasks (${keys.join(',')}) VALUES (${keys.map(() => '?').join(',')})`).run(...Object.values(r));
  db.prepare('INSERT INTO events VALUES (?,?,?,?)').run(r.id, 1, 'admitted', '{}');
  db.prepare('INSERT INTO events VALUES (?,?,?,?)').run(r.id, 2, 'state', JSON.stringify({status: r.status, reason: r.reason, body: 'private-body'}));
  fs.writeFileSync(path.join(root, 'authority.json'), JSON.stringify({schema_version: 2, epoch: 4, commit_sequence: 3}), {mode: 0o600});
  fs.chmodSync(file, 0o600);
  let closed = false;
  const close = () => {if (!closed) {db.close(); closed = true;}};
  if (!wal) close();
  t.after(() => {close(); fs.rmSync(root, {recursive: true, force: true});});
  const input = {path: file, principal: 'product-owner', owner: 'ledger-owner', owner_scope: scope, index_directory: index,
    operations: [{request_key: r.request_key, app: r.app, command: r.command}], max_rows: 2000, max_record_bytes: 1048576};
  return {root, file, index, db, close, input};
}
function snapshot(directory) {
  const result = {};
  function visit(dir) {
    for (const name of fs.readdirSync(dir).sort()) {
      const file = path.join(dir, name), stat = fs.lstatSync(file), relative = path.relative(directory, file);
      result[relative] = {mode: stat.mode, bytes: stat.size, hash: stat.isFile() ? crypto.createHash('sha256').update(fs.readFileSync(file)).digest('hex') : null};
      if (stat.isDirectory()) visit(file);
    }
  }
  visit(directory); return result;
}
for (const wal of [false, true]) test(`ledger reads preserve unknown and every source byte/mode/inventory (active WAL=${wal})`, guarded, async t => {
  const f = fixture(t, {wal}), before = snapshot(f.root);
  const report = await readLedger(f.input);
  assert.equal(report.tasks.length, 1, JSON.stringify(report));
  assert.equal(report.tasks[0].status, 'uncertain'); assert.equal(report.tasks[0].dispatch, 'released');
  assert.equal(report.tasks[0].ack, 'pending'); assert.equal(report.tasks[0].request_key, key);
  assert.deepEqual(report.tasks[0].events.map(e => e.sequence), [1,2]);
  assert.doesNotMatch(JSON.stringify(report), /private-body|private@example|payload|receiver_id|receipt_id/u);
  assert.deepEqual(snapshot(f.root), before);
  await readLedger(f.input); assert.deepEqual(snapshot(f.root), before);
});
test('ledger exact principal, ledger owner and product owner scope cannot lend each other a task', guarded, async t => {
  const f = fixture(t);
  for (const delta of [{principal: 'different'}, {owner: 'different'}, {owner_scope: 'b'.repeat(64)}]) {
    const report = await readLedger({...f.input, ...delta});
    assert.deepEqual(report.tasks, []); assert.ok(report.gaps.length > 0);
  }
});
test('schema collation cannot weaken exact principal isolation', guarded, t => {
  const f = fixture(t, {nocasePrincipal: true});
  const output = readLedger({...f.input, principal: 'PRODUCT-OWNER'});
  assert.deepEqual(output.tasks, [], JSON.stringify(output));
  assert.ok(output.gaps.includes('ledger_schema_unsupported'));
});
test('result digest is a verified SHA256 or an empty gap, never opaque result data', guarded, t => {
  const f = fixture(t, {wal: true});
  let output = readLedger(f.input);
  assert.equal(output.tasks[0]?.result_digest, 'c'.repeat(64), JSON.stringify(output));
  f.db.prepare('UPDATE tasks SET result_digest=? WHERE id=?').run('private-result-payload', task);
  output = readLedger(f.input);
  assert.equal(output.tasks[0]?.result_digest, '');
  assert.ok(output.gaps.includes('ledger_value_unclassified'));
  assert.doesNotMatch(JSON.stringify(output), /private-result-payload|private-body/u);
  f.db.prepare('UPDATE tasks SET result_digest=NULL WHERE id=?').run(task);
  output = readLedger(f.input); assert.equal(output.tasks[0]?.result_digest, '');
});
test('a retained receipt can bind scope-less .41 send only by exact task, revision and result digest', guarded, t => {
  const request = JSON.stringify({app: 'mail-gmail', command: 'send', request_key: key, arguments: {message: {body: 'private-body'}}});
  const f = fixture(t, {wal: true, row: {request, status: 'failed'}});
  const binding = {task_id: task, terminal_revision: 1, result_digest: 'c'.repeat(64)};
  const operation = {...f.input.operations[0], receipt_binding: binding};
  const input = {...f.input, operations: [operation]};
  let output = readLedger(input);
  assert.equal(output.tasks[0]?.id, task, JSON.stringify(output));
  assert.equal(output.tasks[0]?.owner_binding, 'receipt');
  assert.equal(output.tasks[0]?.owner_scope, scope);
  for (const receipt_binding of [{...binding, task_id: '22222222-2222-4222-8222-222222222222'},
    {...binding, terminal_revision: 2}, {...binding, result_digest: 'd'.repeat(64)}, {...binding, result_digest: 'private-result'}, undefined]) {
    output = readLedger({...input, operations: [{...operation, receipt_binding}]});
    assert.deepEqual(output.tasks, []); assert.ok(output.gaps.length > 0);
  }
  for (const ownerJSON of [`"${'b'.repeat(64)}"`, 'null', `"${scope}","owner_scope":"${scope}"`]) {
    f.db.prepare('UPDATE tasks SET request=? WHERE id=?').run(`{"app":"mail-gmail","command":"send","request_key":"${key}","arguments":{"owner_scope":${ownerJSON}}}`, task);
    output = readLedger(input); assert.deepEqual(output.tasks, []);
    assert.ok(output.gaps.includes('ledger_scope_unverified'));
  }
});
test('scope-less legacy head stays only a candidate until its retained receipt matches the ledger', guarded, t => {
  const request = {app: legacy.app, command: legacy.command, request_key: legacyKey, arguments: {body: 'private-body'}};
  const f = fixture(t, {row: {request_key: legacyKey, app: legacy.app, command: legacy.command, request: JSON.stringify(request), status: 'failed'}});
  const binding = {task_id: task, terminal_revision: 1, result_digest: 'c'.repeat(64)};
  f.input.operations = [{...legacy, receipt_binding: binding}];
  fs.writeFileSync(path.join(f.index, headName), JSON.stringify({schema_version: 1, revision: 1, request, current: 'authorization-reference', previous: null, candidate: null}));
  const before = snapshot(f.root); let output = readLedger(f.input);
  assert.equal(output.tasks[0]?.owner_binding, 'receipt', JSON.stringify(output));
  assert.deepEqual(snapshot(f.root), before);
  output = readLedger({...f.input, operations: [{...legacy, receipt_binding: {...binding, result_digest: 'd'.repeat(64)}}]});
  assert.deepEqual(output.tasks, []); assert.ok(output.gaps.includes('ledger_scope_unverified'));
});
test('legacy canonical, alias and conflicting proof never allocate new identity files', guarded, async t => {
  const request = JSON.stringify({app: legacy.app, command: legacy.command, request_key: legacyKey, arguments: {owner_scope: scope}});
  const f = fixture(t, {row: {request_key: legacyKey, app: legacy.app, command: legacy.command, request}});
  f.input.operations = [legacy];
  fs.writeFileSync(path.join(f.index, headName), JSON.stringify({schema_version: 1, revision: 1, request: JSON.parse(request), current: task, previous: null, candidate: null}), {mode: 0o600});
  const before = snapshot(f.root);
  const report = await readLedger(f.input);
  assert.equal(report.tasks[0]?.request_key, legacyKey, JSON.stringify(report));
  assert.equal(report.tasks[0]?.invocation, legacy.invocation);
  assert.deepEqual(snapshot(f.root), before);
  fs.writeFileSync(path.join(f.index, aliasName), JSON.stringify({request_key: key}), {mode: 0o600});
  const conflicting = await readLedger(f.input);
  assert.deepEqual(conflicting.tasks, []); assert.ok(conflicting.gaps.includes('legacy_identity_mapping_conflict'));
});
test('read failures and unknown schemas return bounded fixed gaps without paths or exception text', guarded, async t => {
  const f = fixture(t, {schema: 999});
  const report = await readLedger(f.input);
  assert.deepEqual(report.tasks, []); assert.ok(report.gaps.includes('ledger_schema_unsupported'));
  const missing = await readLedger({...f.input, path: path.join(f.root, 'private-secret-missing.sqlite')});
  assert.deepEqual(missing.tasks, []); assert.ok(missing.gaps.includes('ledger_source_missing'));
  assert.doesNotMatch(JSON.stringify(missing), /private-secret|email-ledger-read/u);
});
test('no qualified platform write guard yields incomplete without opening the source', async t => {
  if (process.platform === 'darwin') {t.skip('production platform has a qualified guard'); return;}
  const report = await readLedger({path: '/must-not-open/private.sqlite', principal: 'p', owner: 'o', owner_scope: scope, operations: [{request_key: key}]});
  assert.deepEqual(report.tasks, []); assert.ok(report.gaps.includes('ledger_write_guard_unavailable'));
});
test('the OS guard denies real source write/create/chmod calls while SQLite reads active WAL', guarded, t => {
  const f = fixture(t, {wal: true}), before = snapshot(f.root);
  const code = `import fs from 'node:fs'; import {readLedgerUnderGuard} from ${JSON.stringify(pathToFileURL(reader).href)};
    const input=${JSON.stringify(f.input)}, denied=[];
    for (const target of [input.path,input.path+'-wal',input.path+'-shm',${JSON.stringify(path.join(f.root, 'authority.json'))}]) {
      for (const action of [()=>fs.writeFileSync(target,'illegal'),()=>fs.chmodSync(target,0o666)]) {
        try {action();denied.push(false);} catch(error) {denied.push(error.code==='EPERM'||error.code==='EACCES');}
      }
    }
    for (const target of [${JSON.stringify(path.join(f.root, 'new-source-file'))},${JSON.stringify(path.join(f.index, 'new-alias.json'))}]) {
      try {fs.writeFileSync(target,'illegal',{flag:'wx'});denied.push(false);} catch(error) {denied.push(error.code==='EPERM'||error.code==='EACCES');}
    }
    console.log(JSON.stringify({denied,result:readLedgerUnderGuard(input)}));`;
  const run = spawnSync('/usr/bin/sandbox-exec', ['-p', '(version 1) (allow default) (deny file-write*) (deny network*)',
    process.execPath, '--no-warnings', '--input-type=module', '-e', code], {encoding: 'utf8', timeout: 5000});
  assert.equal(run.status, 0, run.stderr);
  const output = JSON.parse(run.stdout);
  assert.equal(output.denied.length, 10); assert.ok(output.denied.every(Boolean));
  assert.equal(output.result.tasks.length, 1, run.stdout); assert.deepEqual(output.result.gaps, []);
  assert.deepEqual(snapshot(f.root), before);
});
test('prepared is a real .41 task state, not an invented missing task', guarded, t => {
  const f = fixture(t, {row: {status: 'prepared', effect: 0, attempt: 0, dispatch: null, terminal_revision: 0}});
  const output = readLedger(f.input);
  assert.equal(output.tasks[0]?.status, 'prepared', JSON.stringify(output));
});
test('ACK is acknowledged only for a valid terminal receipt, and never exports receipt identifiers', guarded, t => {
  const ack = {receiver_id: 'private-receiver', receipt_id: 'private-receipt', terminal_revision: 1, at_ms: 12345};
  const f = fixture(t, {wal: true, row: {status: 'failed', ack: JSON.stringify(ack)}});
  let output = readLedger(f.input);
  assert.equal(output.tasks[0]?.ack, 'acknowledged', JSON.stringify(output));
  assert.doesNotMatch(JSON.stringify(output), /private-receiver|private-receipt|receiver_id|receipt_id/u);
  for (const invalid of [JSON.stringify({...ack, terminal_revision: 2}), JSON.stringify({...ack, receipt_id: ''}),
    JSON.stringify({...ack, token: 'private-token'}), '{"receiver_id":"one","receiver_id":"two"}', 'null', '{}', 'bad-json']) {
    f.db.prepare('UPDATE tasks SET ack=? WHERE id=?').run(invalid, task);
    output = readLedger(f.input); assert.equal(output.tasks[0]?.ack, 'incomplete', invalid);
    assert.ok(output.gaps.includes('ledger_ack_invalid'));
    assert.doesNotMatch(JSON.stringify(output), /private-token|bad-json/u);
  }
});
test('duplicate owner binding, malformed JSON and missing scope cannot cross Gateway owners', guarded, t => {
  const f = fixture(t, {wal: true});
  const prefix = `"app":"mail-gmail","command":"send","request_key":"${key}"`;
  for (const request of ['bad-private-request', `{${prefix},"arguments":{}}`,
    `{${prefix},"arguments":{"owner_scope":"${scope}","owner_scope":"${'b'.repeat(64)}"}}`,
    `{${prefix},"arguments":{"owner_scope":"${scope}"},"arguments":{"owner_scope":"${scope}"}}`]) {
    f.db.prepare('UPDATE tasks SET request=? WHERE id=?').run(request, task);
    const report = readLedger(f.input);
    assert.deepEqual(report.tasks, []); assert.ok(report.gaps.includes('ledger_scope_unverified'));
    assert.doesNotMatch(JSON.stringify(report), /bad-private-request/u);
  }
});
test('unknown and oversized reason/status/type fields are content-free gaps', guarded, t => {
  const f = fixture(t, {wal: true, row: {reason: 'private-secret'.repeat(30)}});
  let output = readLedger(f.input);
  assert.equal(output.tasks[0]?.reason, 'unclassified', JSON.stringify(output));
  assert.ok(output.gaps.includes('ledger_value_unclassified'));
  f.db.prepare('UPDATE tasks SET reason=NULL WHERE id=?').run(task);
  f.db.prepare('UPDATE events SET payload=? WHERE sequence=2').run(JSON.stringify({status: 'private-secret'.repeat(30), reason: 'private-secret'.repeat(30)}));
  output = readLedger(f.input); assert.ok(output.gaps.includes('ledger_value_unclassified'));
  assert.doesNotMatch(JSON.stringify(output), /private-secret/u);
  f.db.prepare('UPDATE events SET type=? WHERE sequence=2').run('private-secret');
  output = readLedger(f.input); assert.ok(output.gaps.includes('ledger_value_unclassified'));
  assert.deepEqual(output.tasks[0].events.map(e => e.type), ['admitted']);
});
test('row and parsed-record budgets return explicit gaps without oversized fields', guarded, t => {
  const f = fixture(t, {wal: true});
  let output = readLedger({...f.input, max_rows: 2});
  assert.equal(output.rows_read, 2); assert.equal(output.tasks[0]?.events.length, 1);
  assert.ok(output.gaps.includes('ledger_row_limit'));
  f.db.prepare('UPDATE events SET payload=? WHERE sequence=2').run(JSON.stringify({body: 'private'.repeat(1000)}));
  output = readLedger({...f.input, max_record_bytes: 1024});
  assert.ok(output.gaps.includes('ledger_record_limit')); assert.ok(output.gaps.includes('ledger_event_gap'));
  f.db.prepare('UPDATE tasks SET request=? WHERE id=?').run(JSON.stringify({body: 'private'.repeat(1000)}), task);
  output = readLedger({...f.input, max_record_bytes: 1024});
  assert.deepEqual(output.tasks, []); assert.ok(output.gaps.includes('ledger_record_limit'));
  assert.doesNotMatch(JSON.stringify(output), /private/u);
});
test('existing alias proves legacy mapping only with the exact scoped task binding', guarded, t => {
  const request = JSON.stringify({app: legacy.app, command: legacy.command, request_key: key, arguments: {owner_scope: scope}});
  const f = fixture(t, {row: {app: legacy.app, command: legacy.command, request}}); f.input.operations = [legacy];
  let output = readLedger(f.input); assert.ok(output.gaps.includes('legacy_identity_mapping_missing'));
  fs.writeFileSync(path.join(f.index, aliasName), JSON.stringify({request_key: key}), {mode: 0o600});
  const before = snapshot(f.root); output = readLedger(f.input);
  assert.equal(output.tasks[0]?.invocation, legacy.invocation); assert.deepEqual(snapshot(f.root), before);
  output = readLedger({...f.input, owner_scope: 'b'.repeat(64)});
  assert.deepEqual(output.tasks, []); assert.ok(output.gaps.includes('ledger_scope_unverified'));
  fs.writeFileSync(path.join(f.index, aliasName), `{"request_key":"${key}","request_key":"${key}"}`);
  output = readLedger(f.input); assert.deepEqual(output.tasks, []); assert.ok(output.gaps.includes('ledger_record_invalid'));
});
test('schema columns, metadata and authority failures are incomplete with no repair', guarded, t => {
  const f = fixture(t, {wal: true});
  fs.writeFileSync(path.join(f.root, 'authority.json'), JSON.stringify({schema_version: 2, epoch: 999, commit_sequence: 3}));
  let before = snapshot(f.root), output = readLedger(f.input);
  assert.deepEqual(output.tasks, []); assert.ok(output.gaps.includes('ledger_authority_invalid')); assert.deepEqual(snapshot(f.root), before);
  fs.unlinkSync(path.join(f.root, 'authority.json')); before = snapshot(f.root); output = readLedger(f.input);
  assert.ok(output.gaps.includes('ledger_authority_missing')); assert.deepEqual(snapshot(f.root), before);
  f.db.exec("DELETE FROM metadata WHERE key='generation'");
  output = readLedger(f.input); assert.ok(output.gaps.includes('ledger_schema_unsupported'));
  f.db.exec('ALTER TABLE tasks DROP COLUMN ack');
  output = readLedger(f.input); assert.ok(output.gaps.includes('ledger_schema_unsupported'));
});
test('corrupt and symlink ledger sources are rejected without changing bytes or permissions', guarded, t => {
  const f = fixture(t); fs.writeFileSync(f.file, 'private-corrupt-ledger');
  let before = snapshot(f.root), output = readLedger(f.input);
  assert.deepEqual(output.tasks, []); assert.ok(output.gaps.length > 0); assert.deepEqual(snapshot(f.root), before);
  assert.doesNotMatch(JSON.stringify(output), /private-corrupt-ledger/u);
  fs.symlinkSync(f.file, path.join(f.root, 'link.sqlite')); before = snapshot(f.root);
  output = readLedger({...f.input, path: path.join(f.root, 'link.sqlite')});
  assert.ok(output.gaps.includes('ledger_source_invalid')); assert.deepEqual(snapshot(f.root), before);
});
test('stdin command emits exactly one bounded JSON result and private worker has no unguarded bypass', t => {
  const f = fixture(t);
  const run = (input, args = []) => spawnSync(process.execPath, ['--no-warnings', reader, ...args], {input, encoding: 'utf8', timeout: 5000});
  for (const input of ['{"private":', 'x'.repeat(1048577), JSON.stringify({...f.input, max_rows: 2001})]) {
    const child = run(input); assert.equal(child.status, 0, child.stderr);
    assert.deepEqual(JSON.parse(child.stdout).gaps, ['ledger_input_invalid']);
    assert.equal(child.stdout.trim().split('\n').length, 1); assert.doesNotMatch(child.stdout, /private/u);
  }
  const child = run(JSON.stringify(f.input), ['--guarded-reader']);
  assert.deepEqual(JSON.parse(child.stdout).gaps, ['ledger_write_guard_unavailable']);
});
test('embedded source executes via eval and the guarded child uses those same source bytes', guarded, t => {
  const f = fixture(t), before = snapshot(f.root);
  const source = fs.readFileSync(canonicalReader, 'utf8');
  const run = spawnSync(process.execPath, ['--no-warnings', '--input-type=module', '--eval', `${source}\nawait runCLI();`],
    {input: JSON.stringify(f.input), encoding: 'utf8', timeout: 5000, cwd: f.root});
  assert.equal(run.status, 0, run.stderr);
  const result = JSON.parse(run.stdout);
  assert.equal(result.tasks[0]?.id, task, run.stdout);
  assert.deepEqual(result.gaps, []); assert.deepEqual(snapshot(f.root), before);
});
test('Linux core reads static and active-WAL fixtures on an OS read-only mount; production has no bypass', {
  skip: !process.env.SPARKCLAW_LEDGER_TEST_DOCKER_IMAGE ? 'optional existing Node 26 image required for isolated Linux read-only-mount qualification' : false,
}, t => {
  for (const wal of [false, true]) {
    const f = fixture(t, {wal}), before = snapshot(f.root);
    const input = {...f.input, path: '/evidence/ledger.sqlite', index_directory: '/evidence/index'};
    const code = `import fs from 'node:fs';
      import {readLedger,readLedgerUnderGuard} from 'file:///reader.mjs';
      const input=${JSON.stringify(input)},denied=[];
      for(const target of ['/evidence/ledger.sqlite','/evidence/ledger.sqlite-wal','/evidence/ledger.sqlite-shm','/evidence/authority.json','/evidence/index/new-alias.json']){
        for(const action of [()=>fs.writeFileSync(target,'illegal'),()=>fs.chmodSync(target,0o666)]) {
          try {action();denied.push(false);} catch(error) {denied.push(['EROFS','EACCES','EPERM','ENOENT'].includes(error.code));}
        }
      }
      console.log(JSON.stringify({denied,production:readLedger(input),core:readLedgerUnderGuard(input)}));`;
    const run = spawnSync('docker', ['run', '--rm', '--network', 'none', '--read-only', '--platform', 'linux/amd64',
      '--mount', `type=bind,source=${f.root},target=/evidence,readonly`,
      '--mount', `type=bind,source=${canonicalReader},target=/reader.mjs,readonly`,
      process.env.SPARKCLAW_LEDGER_TEST_DOCKER_IMAGE, 'node', '--no-warnings', '--input-type=module', '-e', code],
    {encoding: 'utf8', timeout: 20000, maxBuffer: 1048576});
    assert.equal(run.status, 0, run.stderr);
    const output = JSON.parse(run.stdout);
    assert.equal(output.denied.length, 10); assert.ok(output.denied.every(Boolean), run.stdout);
    assert.deepEqual(output.production.gaps, ['ledger_write_guard_unavailable']);
    assert.equal(output.core.tasks[0]?.status, 'uncertain', run.stdout);
    assert.deepEqual(output.core.gaps, []); assert.deepEqual(snapshot(f.root), before);
  }
});
