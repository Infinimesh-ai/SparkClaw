import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { fileURLToPath } from 'node:url';
import { assertPrivateDirectory, readPrivateJSON, writePrivateJSON } from '../../../scripts/lib/private-workbench.mjs';
import { containerState } from '../../../scripts/lib/iscp-docker-lab.mjs';

// Requires its own fresh expansion lab with Gateway up. It intentionally
// revokes that disposable device and leaves its Relay stopped for cleanup.
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../..');
const lab = process.argv[2];
if (process.platform !== 'darwin' || process.argv.length !== 3 || !path.isAbsolute(lab || '')) throw new Error('Usage: Node 26 on macOS run-authorization-iscp-native-qualification.mjs <fresh private running expansion lab>');
await assertPrivateDirectory(lab);
const metadata = await readPrivateJSON(path.join(lab, 'run.json'));
assert.ok(containerState(metadata.gateway_container, metadata.lab_id)?.State.Running);
assert.ok(containerState(metadata.issuer_container, metadata.lab_id)?.State.Running);
const gateway = containerState(metadata.gateway_container, metadata.lab_id);
assert.equal(Object.keys(gateway.HostConfig.PortBindings || {}).length, 0);
const run = promisify(execFile);
const electron = (await import('electron')).default;
const summary = { schema_version: 1, gateway_binary_sha256: metadata.gateway_binary_sha256, helper_binary_sha256: metadata.helper_binary_sha256, checks: [], gateway_business_ports_published: false, phases: [] };
try {
  for (const phase of ['delete', 'restart']) {
    if (phase === 'restart') {
      await run('docker', ['restart', metadata.issuer_container], { timeout: 20000 });
      // Docker reallocates an ephemeral published port on container restart.
      // Update only this private endpoint; the issuer identity pin and original
      // device/Grant/deletion evidence remain byte-for-byte unchanged.
      const issuer = containerState(metadata.issuer_container, metadata.lab_id);
      const port = issuer.NetworkSettings.Ports['8080/tcp'][0];
      assert.equal(port.HostIp, '127.0.0.1');
      const helperPath = path.join(lab, 'desktop-helper.json');
      const helper = await readPrivateJSON(helperPath);
      helper.grant_renewal.url = `http://127.0.0.1:${port.HostPort}`;
      await writePrivateJSON(helperPath, helper);
      summary.checks.push('issuer_identity_and_durable_state_retained_while_private_ephemeral_Docker_endpoint_is_refreshed');
    }
    const result = await run(electron, [path.join(root, 'apps/desktop/test/authorization-iscp-native-fixture.mjs')], { cwd: root, timeout: 100000, maxBuffer: 1 << 20, env: { ...process.env, SPARKCLAW_AUTH_ISCP_FIXTURE_ROOT: lab, SPARKCLAW_AUTH_ISCP_FIXTURE_PHASE: phase } });
    process.stdout.write(result.stdout);
    const evidence = await readPrivateJSON(path.join(lab, `evidence/authorization-${phase}.json`));
    assert.equal(evidence.passed, true, evidence.error);
    const netlog = JSON.parse(await fs.readFile(path.join(lab, `evidence/authorization-${phase}-network.json`), 'utf8'));
    assert.equal((netlog.events || []).filter(event => /^https?:|^wss?:/u.test(event.params?.url || '')).length, 0);
    summary.phases.push(evidence);
  }
  assert.deepEqual(summary.phases[0].authorization_deletion, summary.phases[1].authorization_deletion);
  summary.passed = true;
} catch (error) {
  if (error.stdout) process.stdout.write(error.stdout);
  if (error.stderr) process.stderr.write(error.stderr);
  summary.error = error.message; process.exitCode = 1;
} finally {
  await writePrivateJSON(path.join(lab, 'evidence/native-authorization.json'), summary);
  console.log(JSON.stringify({ passed: summary.passed === true, evidence_file: path.join(lab, 'evidence/native-authorization.json') }));
}
