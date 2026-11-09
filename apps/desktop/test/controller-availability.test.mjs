import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { DesktopAuth } from "../src/main/desktop-auth.mjs";
import { ISCP_SURFACES } from "../src/main/iscp-capabilities.mjs";

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
