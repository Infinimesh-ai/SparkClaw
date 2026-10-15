import assert from 'node:assert/strict';
import test from 'node:test';
import {MailObserverFeed} from '../src/mail-observer-feed.mjs';
import {localMailFixture, eventually} from './fixtures/local-mail-runtime.mjs';

for (const change of ['token', 'generation', 'account', 'scope']) {
  test(`local mail watch preserves product events and rejects retired ${change} callbacks`, async t => {
    const contexts = [];
    const {factory} = await localMailFixture(t, {handlers: {'gmail.watch': {async run(input, context) {
      contexts.push(context);
      context.emit('watch_ready', {provider: 'gmail', resync_required: true});
      await new Promise(resolve => context.signal.addEventListener('abort', resolve, {once: true}));
      context.check();
    }}}});
    const controller = {profileID: 'default', scriptFactory: factory, validateToken: async () => {},
      runScript: (request, options) => factory.runScript({provider: request.provider, operation: request.operation,
        token: request.token, credentialGeneration: request.credential_generation, taskID: request.task_id,
        scriptID: request.script_id, revision: request.revision, input: request.input, signal: options?.signal})};
    const feed = new MailObserverFeed(controller);
    t.after(() => feed.close());
    const input = overrides => ({profile_id: 'default', token: overrides.token ?? 'first-valid-token',
      credential_generation: overrides.generation ?? 1,
      bindings: [{provider: 'gmail', owner_scope: overrides.scope ?? 'a'.repeat(64), mailbox_id: 'mailbox',
        account_address: overrides.account ?? 'Owner@example.test', binding_generation: 1}]});
    await feed.reconcile(input({}));
    await feed.work.get('gmail')?.promise;
    await eventually(() => contexts.length === 1);
    const prior = await factory.pollWatch('gmail');
    assert.equal(feed.bindings.get('gmail').state, 'watching');
    assert.notEqual(prior.identity, feed.bindings.get('gmail').identity, 'private cache and public subscription identities are distinct');
    prior.onEvent(prior, 'mailbox_changed', 'gmail_topic_invalidation');
    assert.equal(feed.events.at(-1).kind, 'mailbox_changed');
    const changed = {token: 'second-valid-token', generation: 2, account: 'other@example.test', scope: 'b'.repeat(64)};
    await feed.reconcile(input({[change]: changed[change]}));
    await feed.work.get('gmail')?.promise;
    await eventually(() => contexts.length === 2);
    const current = await factory.pollWatch('gmail');
    assert.equal(contexts[0].signal.aborted, true);
    const sequence = feed.sequence;
    prior.onEvent(prior, 'mailbox_changed', 'gmail_topic_invalidation');
    assert.equal(feed.sequence, sequence, 'retired watch callback must be ignored');
    current.onEvent(current, 'mailbox_changed', 'gmail_topic_invalidation');
    assert.equal(feed.sequence, sequence + 1);
  });
}
