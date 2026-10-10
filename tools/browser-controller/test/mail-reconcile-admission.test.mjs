import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import os from 'node:os';
import crypto from 'node:crypto';
import test from 'node:test';
import {pathToFileURL} from 'node:url';
import {setTimeout as delay} from 'node:timers/promises';

const runtime = process.env.APP_CLI_RECOVERY_RUNTIME_ROOT
  ? pathToFileURL(path.resolve(process.env.APP_CLI_RECOVERY_RUNTIME_ROOT) + path.sep)
  : new URL('../', import.meta.resolve('@infinimesh/app-cli-runtime/release'));
const python = process.env.APP_CLI_RECOVERY_PYTHON;
const {Executor} = await import(new URL('src/executor.mjs', runtime));
const {Ledger} = await import(new URL('src/ledger.mjs', runtime));
const {serve} = await import(new URL('src/socket.mjs', runtime));
const {MailboxClient} = await import(new URL('applications/mail/client.mjs', runtime));
const {digest} = await import(new URL('src/protocol.mjs', runtime));
const {mailHandlers} = await import(new URL('applications/mail/handlers.mjs', runtime));
const {validateManagedSend} = await import(new URL('applications/mail/lib/managed-send.mjs', runtime));
const {openSendJournal} = await import(new URL('applications/mail/lib/send-journal.mjs', runtime));
const {createNotSentReceipt} = await import(new URL('applications/mail/lib/send-recovery.mjs', runtime));

async function fixture(t) {
  // Keep the Unix path short on macOS. All mail content and effects are synthetic.
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'sc-r-')); fs.chmodSync(root, 0o700);
  const state = path.join(root, 'state'); fs.mkdirSync(state, {mode: 0o700});
  const binding = JSON.parse(fs.readFileSync(new URL('bindings/mail-outlook.json', runtime)));
  const bindingFile = path.join(root, 'binding.json'); fs.writeFileSync(bindingFile, JSON.stringify(binding), {mode: 0o600});
  const config = {node: process.execPath, runtime_directory: new URL('.', runtime).pathname,
    owner_id: 'fixture-owner', profile_id: 'default', workspace_root: root, host_runtime_root: root,
    state_directory: state, socket: path.join(state, 'e.sock'), grants_directory: path.join(root, 'grants'),
    issuer_key_file: path.join(root, 'issuer'), assembly_module: path.join(root, 'fixture-assembly.mjs'),
    bindings: [{path: bindingFile, digest: digest(binding)}]};
  fs.writeFileSync(config.issuer_key_file, crypto.randomBytes(32), {mode: 0o600});
  const configFile = path.join(root, 'config.json'); fs.writeFileSync(configFile, JSON.stringify(config), {mode: 0o600});
  const mailbox = new MailboxClient({configFile, python});
  let ledger = new Ledger(state);
  const handlers = await mailHandlers(), unblockers = new Set();
  const name = binding.commands.send.handler, reconcile = handlers[name].reconcile;
  let effects = 0, reads = 0, executor, server;
  const originalContexts = [];
  handlers[name] = {
    async run(input, context) {
      context.beforeEffect(); effects++;
      const request = validateManagedSend(input, 'outlook');
      const receipt = createNotSentReceipt(request, context, 'EMAIL_ATTACHMENT_UPLOAD_FAILED', 'pre_dispatch_failure');
      const journal = await openSendJournal(root, request);
      assert.equal(await journal.write('not_sent', receipt), true);
      throw Object.assign(new Error('synthetic preparation failure'), {code: 'EMAIL_ATTACHMENT_UPLOAD_FAILED'});
    },
    async reconcile(input, context) {
      reads++; originalContexts.push(context.reconciliation);
      return reconcile(input, context);
    },
  };
  const options = {authorization: mailbox.client.authorization, bindings: [binding], handlers,
    host: {async acquire() {return {async release() {}};}}};
  executor = new Executor({...options, ledger});
  const acknowledgements = [];
  let loseReconcileReply = false;
  server = await serve(config.socket, async request => {
    const response = await executor.control(request);
    if (request.operation === 'reconcile') {
      acknowledgements.push(structuredClone(response));
      if (loseReconcileReply) {loseReconcileReply = false; throw Object.assign(new Error('lost reply fixture'), {code: 'BACKEND_UNAVAILABLE'});}
    }
    return response;
  });
  t.after(async () => {for (const release of unblockers) release(); await server.close(); await executor.close(); ledger.close(); fs.rmSync(root, {recursive: true, force: true});});
  const input = {schema_version: 1, operation: 'send', provider: 'outlook', account: 'default',
    account_address: 'owner@example.test', invocation_id: 'original-invocation', mode: 'compose',
    message: {to: ['sink@example.test'], cc: [], subject: 'Synthetic', body: {format: 'text', content: 'Fixture only'}}};
  const spec = binding.commands.send;
  const request = {provider: 'outlook', operation: 'send', taskID: input.invocation_id,
    credentialGeneration: 1, token: 'synthetic-token', scriptID: spec.script_id, revision: spec.revision, input};
  const original = await mailbox.execute(request);
  assert.equal(original.state, 'failed'); assert.equal(original.result.code, 'send_outcome_unknown');
  const admission = mailbox.client.restore({principal: 'product-owner', owner: config.owner_id,
    request_key: digest({taskID: request.taskID, app: binding.manifest.id, command: 'send'})});
  const lookup = await mailbox.client.control(admission, 'lookup'), taskID = lookup.task.id;
  assert.equal(lookup.task.status, 'uncertain');
  return {mailbox, get ledger() {return ledger;}, get executor() {return executor;}, admission, taskID, acknowledgements, originalContexts,
    request: {...request, input: {...input, mode: 'reconcile'}},
    counts: () => ({effects, reads}),
    loseNextReconcileReply() {loseReconcileReply = true;},
    latestAdmission() {return mailbox.client.restore({principal: 'product-owner', owner: config.owner_id,
      request_key: admission.request.request_key});},
    crashQueuedExecutor() {
      // The old process cannot execute its queued callbacks after a crash.
      // Abandon its unresolved lane, close SQLite, then use the production
      // Ledger startup recovery and a fresh Executor against the same files.
      assert.equal(ledger.get(taskID).status, 'pending');
      unblockers.clear(); ledger.close(); ledger = new Ledger(state);
      executor = new Executor({...options, ledger});
    },
    blockLane() {
      let release; const pending = new Promise(resolve => {release = resolve;});
      executor.lanes.set(`${config.owner_id}\0${spec.resource ?? binding.manifest.id}`, pending);
      unblockers.add(release);
      return release;
    }};
}

test('first reconciliation observes admission and returns the original proof over Python and Unix socket', {skip: !python}, async t => {
  const f = await fixture(t), before = f.ledger.get(f.taskID);
  const result = await f.mailbox.execute(f.request);
  assert.equal(f.acknowledgements.length, 1);
  assert.equal(f.acknowledgements[0].task.status, 'pending', 'accepted reconciliation must not return the old uncertain outcome');
  assert.equal(result.state, 'completed'); assert.equal(result.result.status, 'not_sent');
  assert.equal(result.result.not_sent.task_id, f.taskID);
  assert.equal(result.result.not_sent.invocation_id, f.request.taskID);
  assert.equal(result.result.not_sent.intent_digest, before.intent);
  assert.equal(result.result.not_sent.resource_digest, before.resource_digest);
  assert.equal(result.result.not_sent.ledger_epoch, before.epoch);
  assert.deepEqual(f.originalContexts, [{status: 'uncertain', reason: 'EMAIL_ATTACHMENT_UPLOAD_FAILED', effect: 1}]);
  assert.equal(f.ledger.get(f.taskID).effect, 1);
  assert.deepEqual(await f.mailbox.execute(f.request), result, 'repeated lookup must reuse the completed original receipt');
  assert.deepEqual(f.counts(), {effects: 1, reads: 1});
});

async function waitForAdmission(f) {
  const deadline = Date.now() + 15000;
  while (!f.acknowledgements.length && Date.now() < deadline) await delay(10);
  assert.equal(f.acknowledgements.length, 1, 'reconciliation did not reach the executor');
}

test('queued reconciliation is visible, rejects a second execution and survives concurrent lookup', {skip: !python}, async t => {
  const f = await fixture(t), release = f.blockLane();
  const first = f.mailbox.execute(f.request);
  await waitForAdmission(f);
  assert.equal(f.ledger.get(f.taskID).status, 'pending');
  assert.deepEqual(f.counts(), {effects: 1, reads: 0});
  await assert.rejects(f.mailbox.client.control(f.latestAdmission(), 'reconcile', f.taskID), {code: 'INVALID_TASK_STATE'});
  const second = f.mailbox.execute(f.request);
  release();
  const results = await Promise.all([first, second]);
  assert.equal(results[0].result.status, 'not_sent'); assert.deepEqual(results[1], results[0]);
  await assert.rejects(f.mailbox.client.control(f.latestAdmission(), 'reconcile', f.taskID), {code: 'INVALID_TASK_STATE'});
  assert.equal(f.ledger.get(f.taskID).status, 'completed');
  assert.deepEqual(f.counts(), {effects: 1, reads: 1});
});

test('cancelling queued recovery retains the original effect as uncertain', {skip: !python}, async t => {
  const f = await fixture(t), release = f.blockLane();
  const pending = f.mailbox.execute(f.request); await waitForAdmission(f);
  await f.mailbox.client.control(f.latestAdmission(), 'cancel', f.taskID);
  assert.equal(f.ledger.get(f.taskID).status, 'uncertain');
  assert.equal(f.ledger.get(f.taskID).reason, 'CANCEL_REQUESTED');
  release(); await f.executor.active.get(f.taskID)?.promise;
  assert.equal((await pending).result.code, 'send_outcome_unknown');
  assert.equal(f.ledger.get(f.taskID).effect, 1);
  assert.deepEqual(f.counts(), {effects: 1, reads: 0});
  assert.equal((await f.mailbox.execute(f.request)).result.status, 'not_sent');
  assert.deepEqual(f.counts(), {effects: 1, reads: 1});
});

test('SQLite restart fences queued recovery without replaying the original send handler', {skip: !python}, async t => {
  const f = await fixture(t); f.blockLane();
  const pending = f.mailbox.execute(f.request); await waitForAdmission(f);
  f.crashQueuedExecutor();
  assert.equal(f.ledger.epoch, 2);
  assert.equal(f.ledger.get(f.taskID).status, 'uncertain');
  assert.equal(f.ledger.get(f.taskID).reason, 'EXECUTOR_RESTARTED');
  assert.equal((await pending).result.code, 'send_outcome_unknown');
  assert.deepEqual(f.counts(), {effects: 1, reads: 0});
  const result = await f.mailbox.execute(f.request);
  assert.equal(result.result.status, 'not_sent');
  assert.equal(result.result.not_sent.ledger_epoch, 1, 'recovery must retain the original proof epoch');
  assert.equal(result.result.not_sent.task_id, f.taskID);
  assert.equal(f.ledger.get(f.taskID).effect, 1);
  assert.deepEqual(f.counts(), {effects: 1, reads: 1});
});

test('failed admission and a lost accepted reply cannot reissue or erase the original outcome', {skip: !python}, async t => {
  const f = await fixture(t), before = f.ledger.get(f.taskID);
  const badRef = f.mailbox.client.authorization.issue({...f.admission.grant, owner: 'different-owner'}, f.admission.resource);
  const badAdmission = {...f.admission, request: {...f.admission.request, authorization_ref: badRef}};
  await assert.rejects(f.mailbox.client.control(badAdmission, 'reconcile', f.taskID), {code: 'TASK_ACCESS_DENIED'});
  assert.equal(f.ledger.get(f.taskID).sequence, before.sequence);
  assert.deepEqual(f.counts(), {effects: 1, reads: 0});
  f.loseNextReconcileReply();
  await assert.rejects(f.mailbox.execute(f.request), {code: 'BACKEND_UNAVAILABLE'});
  await f.executor.active.get(f.taskID)?.promise;
  assert.equal(f.ledger.get(f.taskID).status, 'completed');
  const result = await f.mailbox.execute(f.request);
  assert.equal(result.result.status, 'not_sent');
  assert.equal(result.result.not_sent.task_id, f.taskID);
  assert.deepEqual(f.counts(), {effects: 1, reads: 1});
});
