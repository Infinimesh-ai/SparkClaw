import assert from 'node:assert/strict';
import test from 'node:test';
import {AppCLIClientFactory} from '../src/app-cli-client.mjs';
import {MailObserverFeed} from '../src/mail-observer-feed.mjs';

test('App-CLI private cache identity does not suppress product events or authorize retired credentials', async () => {
  const factory = new AppCLIClientFactory();
  const callbacks = [];
  const slot = {identity: 'private-release-cache-identity', state: 'watching', epoch: 'watch-task', task: {status: 'running'}};
  factory.client = {
    describe: () => ({spec: {script_id: 'fixture.watch', revision: 1}}),
    pollWatch: async () => null,
    stopWatch: async () => {},
    execute: async request => {callbacks.push(request.onEvent); request.onEvent(slot, 'state'); return {state: 'completed'};},
  };
  const controller = {profileID: 'default', scriptFactory: factory, validateToken: async () => {},
    runScript: request => factory.runScript({provider: request.provider, operation: request.operation,
      token: request.token, credentialGeneration: request.credential_generation, input: request.input})};
  const feed = new MailObserverFeed(controller);
  try {
    const input = token => ({profile_id: 'default', token, credential_generation: 1,
      bindings: [{provider: 'gmail', owner_scope: 'a'.repeat(64), mailbox_id: 'mailbox', account_address: 'Owner@example.test', binding_generation: 1}]});
    await feed.reconcile(input('first-valid-token'));
    await feed.work.get('gmail')?.promise;
    assert.equal(feed.bindings.get('gmail').state, 'watching');
    callbacks[0](slot, 'mailbox_changed', 'gmail_topic_invalidation');
    assert.equal(feed.events.at(-1).kind, 'mailbox_changed');
    await feed.reconcile(input('second-valid-token'));
    await feed.work.get('gmail')?.promise;
    const sequence = feed.sequence;
    callbacks[0](slot, 'mailbox_changed', 'gmail_topic_invalidation');
    assert.equal(feed.sequence, sequence, 'retired credential callback must be ignored');
    callbacks.at(-1)(slot, 'mailbox_changed', 'gmail_topic_invalidation');
    assert.equal(feed.sequence, sequence + 1);
  } finally {await feed.close();}
});
