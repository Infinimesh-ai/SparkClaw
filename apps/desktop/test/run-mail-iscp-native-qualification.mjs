import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import fs from 'node:fs/promises';
import path from 'node:path';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { fileURLToPath } from 'node:url';
import { assertPrivateDirectory, readPrivateJSON, writePrivateJSON } from '../../../scripts/lib/private-workbench.mjs';
import { containerState, labLabel } from '../../../scripts/lib/iscp-docker-lab.mjs';

// Prepare a fresh expansion lab first. This runner owns only the additional
// labelled Linux fixture container; iscp-local-lab down cleans the Relay/issuer.
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../..');
const lab = process.argv[2];
if (process.platform !== 'darwin' || process.argv.length !== 3 || !path.isAbsolute(lab || '')) throw new Error('Usage: Node 26 on macOS run-mail-iscp-native-qualification.mjs <fresh private expansion lab>');
await assertPrivateDirectory(lab);
const metadata = await readPrivateJSON(path.join(lab, 'run.json'));
assert.ok(metadata.qualified_operations?.includes('mail.drafts.send'));
assert.ok(metadata.authorization_scopes?.includes('files.read'), 'The private candidate must explicitly authorize reading workspace attachments');
assert.ok(containerState(metadata.relay_container, metadata.lab_id)?.State.Running);
assert.ok(!containerState(metadata.gateway_container, metadata.lab_id)?.State.Running, 'The normal lab Gateway must be stopped before using the fixture identity');
const fixture = path.join(lab, 'native-mail');
await fs.mkdir(fixture, { mode: 0o700 });
// Keep the desktop source outside every Gateway bind mount. A shared fixture
// directory would hide an accidental return to Gateway-local source paths.
const desktopData = await fs.mkdtemp('/private/tmp/sparkx-mail-local-');
const run = promisify(execFile);
const container = `${metadata.gateway_container}-mail-fixture`;
let started = false;
try {
  await run('go', ['test', '-c', '-o', path.join(lab, 'bin/native-mail-fixture'), './internal/gateway'], { cwd: path.join(root, 'services/gateway'), timeout: 120000, maxBuffer: 2 << 20, env: { ...process.env, GOOS: 'linux', GOARCH: metadata.source.goarch, CGO_ENABLED: '0' } });
  await run(process.execPath, [path.join(root, 'scripts/build-iscp-helper.mjs')], { cwd: root, timeout: 120000, maxBuffer: 1 << 20 });
  await run('npm', ['run', 'build:webchat'], { cwd: root, timeout: 120000, maxBuffer: 1 << 20 });
  const binary = await fs.readFile(path.join(lab, 'bin/native-mail-fixture'));
  const binarySHA256 = crypto.createHash('sha256').update(binary).digest('hex');
  await run('docker', ['run', '--detach', '--name', container, '--label', `${labLabel}=${metadata.lab_id}`, '--network', metadata.network,
    '--user', String(process.getuid()), '--mount', `type=bind,source=${lab},target=/lab`, '--entrypoint', '/lab/bin/native-mail-fixture',
    '--env', 'SPARKCLAW_MAIL_ISCP_FIXTURE_ROOT=/lab', '--env', 'SPARKCLAW_MODEL_CAPACITY_CATALOG=', '--env', 'SPARKCLAW_BROWSER_PROFILE_DIR=', '--env', 'SPARKCLAW_BROWSER_CHROMIUM_EXECUTABLE=',
    '--log-opt', 'max-size=10m', '--log-opt', 'max-file=1', 'sparkclaw-gateway:latest', '-test.run', '^TestWorkbenchISCPNativeMailQualificationFixture$', '-test.timeout', '420s'], { timeout: 30000 });
  started = true;
  const deadline = Date.now() + 15000;
  let ready;
  while (Date.now() < deadline) {
    ready = await readPrivateJSON(path.join(fixture, 'ready.json'), { optional: true });
    if (ready) break;
    if (!containerState(container, metadata.lab_id)?.State.Running) throw new Error('Linux mail fixture exited during startup');
    await new Promise(resolve => setTimeout(resolve, 100));
  }
  assert.ok(ready, 'Linux mail fixture is ready');
  const state = containerState(container, metadata.lab_id);
  assert.equal(Object.keys(state.HostConfig.PortBindings || {}).length, 0, 'Gateway business ports are not published');
  assert.ok(state.Mounts.every(mount => desktopData !== mount.Source && !desktopData.startsWith(mount.Source + path.sep)), 'Desktop data is not mounted into Gateway');
  assert.equal(ready.gateway_business_listener, false);
  const electron = (await import('electron')).default;
  const result = await run(electron, [path.join(root, 'apps/desktop/test/mail-iscp-native-fixture.mjs')], { cwd: root, timeout: 370000, maxBuffer: 2 << 20, env: { ...process.env, SPARKCLAW_MAIL_ISCP_FIXTURE_ROOT: lab, SPARKCLAW_MAIL_LOCAL_DATA: desktopData } });
  process.stdout.write(result.stdout);
  const evidence = await readPrivateJSON(path.join(fixture, 'evidence.json'));
  assert.equal(evidence.passed, true, evidence.error);
  const operations = await readPrivateJSON(path.join(fixture, 'operations.json'));
  for (const name of ['transfer.open', 'transfer.chunk', 'transfer.commit', 'mail.mailboxes', 'mail.drafts.list', 'mail.drafts.save', 'mail.drafts.send', 'mail.drafts.reconcile']) assert.ok(operations[name] > 0, `Actual ISCP operation ${name}`);
  const network = JSON.parse(await fs.readFile(path.join(fixture, 'native-network.json'), 'utf8'));
  const direct = (network.events || []).filter(event => /^https?:|^wss?:/u.test(event.params?.url || ''));
  assert.equal(direct.length, 0, 'Native Chromium HTTP/WS URL events');
  evidence.gateway_fixture_sha256 = binarySHA256;
  evidence.gateway_business_ports_published = false;
  evidence.desktop_data_mounted_into_gateway = false;
  evidence.iscp_operations = operations;
  evidence.chromium_http_ws_url_events = direct.length;
  await writePrivateJSON(path.join(fixture, 'evidence.json'), evidence);
  console.log(JSON.stringify({ passed: true, evidence_file: path.join(fixture, 'evidence.json'), screenshot_file: path.join(fixture, 'native-mail-review.png') }));
} catch (error) {
  if (error.stdout) process.stdout.write(error.stdout);
  if (error.stderr) process.stderr.write(error.stderr);
  if (started) {
    const logs = await run('docker', ['logs', '--tail', '100', container], { timeout: 10000 }).catch(() => null);
    if (logs) process.stderr.write(logs.stdout + logs.stderr);
  }
  throw error;
} finally {
  if (started && containerState(container, metadata.lab_id)) await run('docker', ['rm', '--force', container], { timeout: 30000 });
  await fs.rm(desktopData, { recursive: true, force: true });
}
