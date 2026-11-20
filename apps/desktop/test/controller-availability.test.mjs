import assert from "node:assert/strict";
import test from "node:test";
import { EventEmitter } from "node:events";
import { LinkAuth } from "../src/main/link-auth.mjs";
import { LinkDomainClient } from "../src/main/link-domain-client.mjs";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { ClientControllerRuntime } from "../src/browser/controller-runtime.mjs";
import { BrowserControllerForwarder } from "../src/browser/controller-forwarder.mjs";
import { syntheticControllerBundle } from "./controller-bundle-fixture.mjs";

const ALL_DOMAINS = ["mail", "settings", "medicine", "speech", "notifications", "browser", "mcd", "presentation"];

// A LinkAuth over a fake link, with a connected domain client when asked.
async function linkAuth(t, options = {}, connected = false) {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), "sx-link-auth-"));
  t.after(() => fs.rm(root, { recursive: true, force: true }));
  const link = Object.assign(new EventEmitter(), { online: connected, opened: true, status: async () => ({ joined: false }), open: async () => {}, close() {}, request: async () => ({}) });
  const auth = new LinkAuth({ stateDirectory: path.join(root, "link"), userDataDirectory: root, linkFactory: () => link, ...options });
  auth.descriptor = Object.freeze({ transport: "link", origin: "https://backend.sparkclaw-link.invalid", deploymentID: "d", clientID: "link-desk", deviceID: "desk" });
  if (connected) {
    auth.transport = new LinkDomainClient({ link, origin: auth.descriptor.origin, binding: { deployment_id: "d", owner_id: "o", client_id: "link-desk" }, installationID: "11111111-1111-4111-8111-111111111111", domains: ALL_DOMAINS });
    await auth.transport.start();
  }
  return auth;
}

test("local availability notifications do not re-enter transport suspension", async t => {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), "sx-availability-"));
  t.after(() => fs.rm(root, { recursive: true, force: true }));
  await syntheticControllerBundle(root);
  const runtime = new ClientControllerRuntime({ bundleRoot: root, adapterFactory: () => assert.fail("suspension started an adapter") });
  await runtime.prepare();
  let connectionUpdates = 0;
  const capabilityUpdates = [], pending = [];
  const auth = await linkAuth(t, {
    browserRuntimeAvailable: false,
    onChange: () => {
      connectionUpdates++;
      // Bound the old feedback loop so this regression fails without exhausting memory.
      if (connectionUpdates < 20) pending.push(forwarder.suspend());
    },
    onCapabilitiesChanged: status => capabilityUpdates.push(status),
  });
  t.after(() => auth.close());
  auth.status = Object.freeze({ schema_version: 1, state: "service_unavailable" });
  const forwarder = new BrowserControllerForwarder({ auth, runtime, userDataDir: root,
    registry: { closeHostPages() {}, beginTaskEnd() { return []; }, async completeTaskEnd() {} },
    onChange: () => auth.setBrowserRuntimeAvailable(runtime.snapshot().available),
  });
  await forwarder.suspend();
  for (let index = 0; index < pending.length; index++) await pending[index];
  assert.equal(connectionUpdates, 0, "local capability projection must not restart connection lifecycle handlers");
  assert.equal(forwarder.generation, 1);
  assert.equal(capabilityUpdates.length, 1, "publish settled availability without a transient stopping/restored cycle");
  assert.ok(capabilityUpdates.every(status => status.state === "service_unavailable"));
  assert.equal(runtime.state, "stopped");
});

test("local Controller availability reprojects the existing connection once per change", async t => {
  const updates = [];
  const auth = await linkAuth(t, { browserRuntimeAvailable: true, onChange: status => updates.push(status) }, true);
  t.after(() => auth.close());
  auth.status = Object.freeze({ schema_version: 1, state: "connected" });
  auth.setBrowserRuntimeAvailable(false);
  assert.equal(auth.status.state, "connected");
  assert.equal(auth.status.capabilities.browser, false);
  assert.equal(auth.status.capabilities.files, true);
  assert.equal(auth.status.capabilities.mail, true);
  assert.equal(auth.status.capabilities.settings, true);
  assert.equal(updates.length, 1);
  auth.setBrowserRuntimeAvailable(false); assert.equal(updates.length, 1);
  auth.setBrowserRuntimeAvailable(true); assert.equal(auth.status.capabilities.browser, true);
  assert.equal(updates.length, 2);
  // Once the connection is gone, availability cannot reopen the browser.
  auth.transport.close();
  auth.status = Object.freeze({ schema_version: 1, state: "service_unavailable" });
  auth.setBrowserRuntimeAvailable(false); auth.setBrowserRuntimeAvailable(true);
  assert.equal(auth.status.capabilities.browser, false);
  assert.equal(auth.status.capabilities.surfaces.browser.reason, "connection_required");
});

test("runtime cleanup republishes settled availability without restoring a closed connection", async t => {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), "sx-availability-"));
  t.after(() => fs.rm(root, { recursive: true, force: true }));
  await syntheticControllerBundle(root);
  const runtime = new ClientControllerRuntime({ bundleRoot: root, adapterFactory: () => assert.fail("cleanup started an adapter") });
  await runtime.prepare();
  const auth = await linkAuth(t, { browserRuntimeAvailable: false }, true);
  t.after(() => auth.close());
  auth.status = Object.freeze({ schema_version: 1, state: "connected" });
  new BrowserControllerForwarder({ auth, runtime, userDataDir: root, registry: { closeHostPages() {}, beginTaskEnd() { return []; }, async completeTaskEnd() {} },
    onChange: () => auth.setBrowserRuntimeAvailable(runtime.snapshot().available) });
  auth.setBrowserRuntimeAvailable(runtime.snapshot().available);
  assert.equal(auth.status.capabilities.browser, true);
  await runtime.stop("task_canceled");
  assert.equal(runtime.state, "stopped");
  assert.equal(auth.status.capabilities.browser, true, "a reusable stopped runtime must not retain its transient stopping projection");
  auth.transport.close();
  auth.status = Object.freeze({ schema_version: 1, state: "service_unavailable" });
  auth.setBrowserRuntimeAvailable(false);
  await runtime.stop("authority_changed");
  assert.equal(auth.status.capabilities.browser, false);
  assert.equal(auth.status.capabilities.surfaces.browser.reason, "connection_required");
});
