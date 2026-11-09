import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { DesktopAuth } from "../src/main/desktop-auth.mjs";
import { ISCP_SURFACES } from "../src/main/iscp-capabilities.mjs";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { ClientControllerRuntime } from "../src/browser/controller-runtime.mjs";
import { BrowserControllerForwarder } from "../src/browser/controller-forwarder.mjs";
import { syntheticControllerBundle } from "./controller-bundle-fixture.mjs";

test("local availability notifications do not re-enter transport suspension", async t => {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), "sx-availability-"));
  t.after(() => fs.rm(root, { recursive: true, force: true }));
  await syntheticControllerBundle(root);
  const runtime = new ClientControllerRuntime({ bundleRoot: root, adapterFactory: () => assert.fail("suspension started an adapter") });
  await runtime.prepare();
  let connectionUpdates = 0;
  const capabilityUpdates = [], pending = [];
  const auth = new DesktopAuth({
    browserRuntimeAvailable: false,
    onChange: () => {
      connectionUpdates++;
      // Bound the old feedback loop so this regression fails without exhausting memory.
      if (connectionUpdates < 20) pending.push(forwarder.suspend());
    },
    onCapabilitiesChanged: status => capabilityUpdates.push(status),
  });
  t.after(() => auth.close());
  auth.descriptor = { transport: "iscp" };
  auth.status = Object.freeze({ schema_version: 1, state: "service_unavailable" });
  const forwarder = new BrowserControllerForwarder({ auth, runtime, userDataDir: root,
    registry: { closeHostPages() {} },
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

test("local Controller availability reprojects the existing connection once per change and cannot restore expired authority", t => {
  const updates = [];
  const auth = new DesktopAuth({ onChange: status => updates.push(status) });
  t.after(() => auth.close());
  const manifest = { schema_version: 2, profile: "sparkclaw.workbench.transport.v2", session_id: "session", authorization_revision: 1,
    expires_at: new Date(Date.now() + 60000).toISOString(), operations: JSON.parse(readFileSync(new URL("../src/shared/iscp-operations.json", import.meta.url))).map(row => row.name) };
  auth.descriptor = { transport: "iscp" };
  auth.transport = { capabilities: manifest, close() {} };
  auth.capabilityReport = { ...manifest, capabilities: ISCP_SURFACES.map(id => ({ id, qualified: true, supported: true, permitted: true, dependencies_ready: true, enabled: true })) };
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
  auth.capabilityReport = { ...auth.capabilityReport, expires_at: new Date(0).toISOString() };
  auth.setBrowserRuntimeAvailable(false); auth.setBrowserRuntimeAvailable(true);
  assert.equal(auth.status.capabilities.browser, false);
  assert.equal(auth.status.capabilities.surfaces.browser.reason, "negotiation_required");
});

test("runtime cleanup republishes settled availability without restoring expired authority", async t => {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), "sx-availability-"));
  t.after(() => fs.rm(root, { recursive: true, force: true }));
  await syntheticControllerBundle(root);
  const runtime = new ClientControllerRuntime({ bundleRoot: root, adapterFactory: () => assert.fail("cleanup started an adapter") });
  await runtime.prepare();
  const auth = new DesktopAuth({ browserRuntimeAvailable: false });
  t.after(() => auth.close());
  const manifest = { schema_version: 2, profile: "sparkclaw.workbench.transport.v2", session_id: "session", authorization_revision: 1,
    expires_at: new Date(Date.now() + 60000).toISOString(), operations: JSON.parse(readFileSync(new URL("../src/shared/iscp-operations.json", import.meta.url))).map(row => row.name) };
  auth.descriptor = { transport: "iscp" };
  auth.transport = { capabilities: manifest, close() {} };
  auth.capabilityReport = { ...manifest, capabilities: ISCP_SURFACES.map(id => ({ id, qualified: true, supported: true, permitted: true, dependencies_ready: true, enabled: true })) };
  auth.status = Object.freeze({ schema_version: 1, state: "connected" });
  new BrowserControllerForwarder({ auth, runtime, userDataDir: root, registry: { closeHostPages() {} },
    onChange: () => auth.setBrowserRuntimeAvailable(runtime.snapshot().available) });
  auth.setBrowserRuntimeAvailable(runtime.snapshot().available);
  assert.equal(auth.status.capabilities.browser, true);
  await runtime.stop("task_canceled");
  assert.equal(runtime.state, "stopped");
  assert.equal(auth.status.capabilities.browser, true, "a reusable stopped runtime must not retain its transient stopping projection");
  auth.capabilityReport = { ...auth.capabilityReport, expires_at: new Date(0).toISOString() };
  auth.setBrowserRuntimeAvailable(false);
  await runtime.stop("authority_changed");
  assert.equal(auth.status.capabilities.browser, false);
  assert.equal(auth.status.capabilities.surfaces.browser.reason, "negotiation_required");
});
