import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import {setTimeout as delay} from 'node:timers/promises';
import {LocalMailRuntimeFactory} from '../../src/local-mail-runtime.mjs';

// Real repository bindings, authorization, Host protocol, admission and handlers.
// Only native browser actions are substituted; all accounts and messages are synthetic.
export async function localMailFixture(t, {handlers = {}, root: existingRoot, driver = {}} = {}) {
  const root = existingRoot ?? await fs.realpath(await fs.mkdtemp(path.join(os.tmpdir(), 'lm-t-')));
  if (!existingRoot) t.after(() => fs.rm(root, {recursive: true, force: true}));
  const factory = new LocalMailRuntimeFactory({runtimeRoot: path.join(root, 'cli-runtime'), stateDirectory: path.join(root, 'state'),
    emailWorkspaceRoot: path.join(root, 'workspace'), fixtureAssembly: true, ownerID: 'fixture-owner'});
  await fs.mkdir(factory.emailWorkspaceRoot, {recursive: true, mode: 0o700});
  await factory.prepare();
  const counts = {created: 0, closed: 0};
  Object.assign(factory.driver, {
    async create() {counts.created++; return {activities: new Map()};},
    async beginActivity() {}, async updateLease() {}, async revokeActivity() {},
    async call() {return null;}, async close() {counts.closed++;}, ...driver,
  });
  for (const [key, entry] of factory.commands) if (handlers[entry.spec.handler]) {
    factory.commands.set(key, {...entry, handler: handlers[entry.spec.handler]});
  }
  t.after(() => factory.close());
  return {root, factory, counts, request: (provider, operation, input, overrides = {}) => {
    const spec = factory.describe(provider, operation);
    return {provider, operation, input, taskID: input.invocation_id ?? `fixture-${provider}-${operation}`,
      token: 'synthetic-token', credentialGeneration: 1, scriptID: spec.script_id, revision: spec.revision, ...overrides};
  }};
}

export async function eventually(predicate, message = 'condition did not settle') {
  for (let attempt = 0; attempt < 200; attempt++) {if (await predicate()) return; await delay(10);}
  assert.fail(message);
}
