import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import fs from 'node:fs/promises';
import { createRequire, Module } from 'node:module';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import { patchedBundle } from '../src/install-playwright-downloads.mjs';

const bundlePath = fileURLToPath(new URL('../node_modules/playwright-core/lib/coreBundle.js', import.meta.url));
const require = createRequire(bundlePath);
const { wsReceiver, wsSender } = require('./utilsBundle.js');
const source = await fs.readFile(bundlePath, 'utf8');

// Compile the pinned implementation in memory to exercise its actual relay,
// ExtensionConnection and BrowserModel await chain. The installed file is untouched.
function relayFrom(bundle) {
  const loaded = new Module(bundlePath);
  loaded.filename = bundlePath;
  loaded.paths = Module._nodeModulePaths(path.dirname(bundlePath));
  loaded._compile(`${bundle}\ninit_cdpRelay(); module.exports.orderingTestRelay = CDPRelayServer;`, bundlePath);
  return new loaded.exports.orderingTestRelay('chromium');
}

async function coalescedInitialization(bundle) {
  const relay = relayFrom(bundle);
  const receiver = new wsReceiver({
    allowSynchronousEvents: relay._wsServer._delegate.allowSynchronousEvents ?? true,
    isServer: false,
  });
  const extension = new EventEmitter();
  extension.readyState = 1;
  let resolveCommand;
  const command = new Promise(resolve => { resolveCommand = resolve; });
  extension.send = value => resolveCommand(JSON.parse(value));
  receiver.on('message', data => extension.emit('message', data));
  relay._handleExtensionConnection(extension);
  relay._handler._model._tabSessions.set(1001, {
    tabId: 1001, sessionId: 'pw-tab-1', childSessions: new Set(),
  });
  const messages = [];
  let frameRegistered = false;
  let mainContextReceived = false;
  relay._cdpConnection = { send(value) {
    const message = JSON.parse(value);
    messages.push(message.method ?? 'Page.getFrameTree response');
    if (message.id === 7) frameRegistered = true;
    if (message.method === 'Runtime.executionContextCreated' && frameRegistered) mainContextReceived = true;
  } };
  relay._handler.connectOverCDP(message => relay._sendToCDPClient(message));
  const handled = relay._handlePlaywrightMessage({
    id: 7, sessionId: 'pw-tab-1', method: 'Page.getFrameTree', params: {},
  });
  const outgoing = await command;
  assert.equal(outgoing.params[1], 'Page.getFrameTree');
  const wire = [
    { id: outgoing.id, result: { frameTree: { frame: { id: 'popup-frame' } } } },
    { method: 'chrome.debugger.onEvent', params: [
      { tabId: 1001 }, 'Runtime.executionContextCreated',
      { context: { id: 1, auxData: { isDefault: true, frameId: 'popup-frame' } } },
    ] },
    { method: 'chrome.debugger.onEvent', params: [{ tabId: 1001 }, 'Runtime.executionContextsCleared', {}] },
  ];
  const frames = wire.flatMap(message => wsSender.frame(Buffer.from(JSON.stringify(message)), {
    fin: true, opcode: 1, mask: false, readOnly: true, rsv1: false,
  }));
  try {
    // One TCP-read equivalent deterministically exercises ws's batch delivery;
    // no timers, network packet timing or browser scheduling are involved.
    await new Promise((resolve, reject) => receiver.write(Buffer.concat(frames), error => error ? reject(error) : resolve()));
    await handled;
    await new Promise(setImmediate);
    return { messages, mainContextReceived };
  } finally {
    receiver.destroy();
  }
}

test('pinned MCP synchronous batch delivery loses a popup default context', async () => {
  const legacy = source.replace('allowSynchronousEvents: false, // SparkClaw MCP reply/event ordering\n          ', '');
  const result = await coalescedInitialization(legacy);
  assert.deepEqual(result.messages, ['Runtime.executionContextCreated', 'Runtime.executionContextsCleared', 'Page.getFrameTree response']);
  assert.equal(result.mainContextReceived, false);
});

test('MCP delivery preserves the frame reply and all subsequent context events', async () => {
  const result = await coalescedInitialization(patchedBundle(source));
  assert.deepEqual(result.messages, ['Page.getFrameTree response', 'Runtime.executionContextCreated', 'Runtime.executionContextsCleared']);
  assert.equal(result.mainContextReceived, true);
});

test('only the MCP websocket server opts out of synchronous frame batches', async () => {
  const relay = relayFrom(patchedBundle(source));
  const mcpServer = relay._wsServer;
  const ordinaryServer = new mcpServer.constructor({});
  try {
    await mcpServer.listen(0, '127.0.0.1', '');
    await ordinaryServer.listen(0, '127.0.0.1', '');
    assert.equal(mcpServer._wsServer.options.allowSynchronousEvents, false);
    assert.equal(ordinaryServer._wsServer.options.allowSynchronousEvents, true);
  } finally {
    await mcpServer.close();
    await ordinaryServer.close();
  }
});
