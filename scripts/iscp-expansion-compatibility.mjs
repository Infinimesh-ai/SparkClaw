import assert from "node:assert/strict";
import crypto from "node:crypto";
import fs from "node:fs/promises";
import path from "node:path";
import { ISCPTransport, ISCP_OPERATIONS } from "../apps/desktop/src/main/iscp-transport.mjs";
import { ISCPObjectClient } from "../apps/desktop/src/main/iscp-object-client.mjs";
import { assertPrivateDirectory, readPrivateJSON, writePrivateJSON } from "./lib/private-workbench.mjs";
import { containerState } from "./lib/iscp-docker-lab.mjs";

const directory = process.argv[2];
if (process.argv.length !== 3 || !path.isAbsolute(directory || "")) throw new Error("Usage: node scripts/iscp-expansion-compatibility.mjs <absolute private expansion lab>");
await assertPrivateDirectory(directory);
const metadata = await readPrivateJSON(path.join(directory, "run.json"));
assert.ok(metadata.qualified_operations, "An expansion lab is required");
const installationFile = path.join(directory, "evidence/compatibility-installation.json");
let installation;
try { installation = await readPrivateJSON(installationFile); }
catch (error) {
  if (error.code !== "ENOENT") throw error;
  installation = { installation_id: crypto.randomUUID() };
  await writePrivateJSON(installationFile, installation, { replace: false });
}
const installationID = installation.installation_id;
const profile = path.join(directory, "desktop-helper.json");
const legacyProfile = path.join(directory, "desktop-compatibility-v1.json");
const config = await readPrivateJSON(profile);
delete config.application_profiles;
delete config.qualified_capabilities;
await writePrivateJSON(legacyProfile, config);
const gateway = containerState(metadata.gateway_container, metadata.lab_id);
assert.ok(gateway?.State.Running && Object.keys(gateway.HostConfig.PortBindings || {}).length === 0, "Gateway business ports must not be published");
const report = { schema_version: 1, source: metadata.source, gateway_binary_sha256: metadata.gateway_binary_sha256, helper_binary_sha256: metadata.helper_binary_sha256, gateway_business_ports_published: false, checks: [] };
let transport;
const start = async (configPath) => {
  transport = new ISCPTransport({ configPath, installationID, origin: "https://iscp.invalid", timeoutMS: 30000 });
  await transport.start();
};
const call = (operation, body, options = {}) => transport.invoke(operation, body, { ...options, installationID });
try {
  await start(profile);
  assert.equal(transport.capabilities.profile, "sparkclaw.workbench.transport.v2");
  await call("installation.bind", { schema_version: 1, installation_id: installationID });
  const owner = await call("settings.owner.get");
  const operationID = crypto.randomUUID();
  const updated = await call("settings.owner.patch", { display_name: "ISCP compatibility" }, { operationID, expectedRevision: owner.revision });
  assert.equal(updated.value.display_name, "ISCP compatibility");
  const receipt = await call("operations.receipt", undefined, { params: { operation_id: operationID } });
  assert.equal(receipt.state, "completed");
  report.checks.push("v2_settings_write_and_receipt");
  const objects = new ISCPObjectClient({ root: path.join(directory, "compatibility-objects"), scope: { installationID, authorization_revision: transport.capabilities.authorization_revision }, call });
  const input = crypto.randomBytes(16 * 1024);
  const object = await objects.upload(input, { purpose: "file", name: "compatibility.bin" });
  assert.deepEqual(await objects.download(object), input);
  await call("object.release", { object_id: object.object_id, version: object.version });
  report.checks.push("v2_standard_envelope_chunk_roundtrip");
  transport.close();
  await start(profile);
  assert.equal((await call("settings.owner.get")).value.display_name, "ISCP compatibility");
  report.checks.push("v2_reconnect_preserves_business_state");
  transport.close();
  await start(legacyProfile);
  assert.equal(transport.capabilities, undefined, "Legacy readiness must not expose v2 capabilities");
  assert.equal(ISCP_OPERATIONS.length, 9);
  const identity = await (await transport.fetch("https://iscp.invalid/api/workbench/identity")).json();
  assert.equal(identity.deployment_id, metadata.deployment_id);
  report.checks.push("v1_exact_nine_operations_and_identity");
  report.passed = true;
  console.log(JSON.stringify(report));
} catch (error) {
  report.error = error.message;
  process.exitCode = 1;
  console.error(error);
} finally {
  transport?.close();
  await writePrivateJSON(path.join(directory, "evidence/expansion-compatibility.json"), report);
}
