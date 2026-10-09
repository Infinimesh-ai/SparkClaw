import { app, BrowserWindow, net, netLog } from 'electron';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { containerState } from '../../../scripts/lib/iscp-docker-lab.mjs';

const lab = process.env.SPARKCLAW_AUTH_ISCP_FIXTURE_ROOT;
const phase = process.env.SPARKCLAW_AUTH_ISCP_FIXTURE_PHASE;
if (!path.isAbsolute(lab || '') || !['delete', 'restart'].includes(phase)) throw new Error('A private authorization lab and explicit phase are required');
const metadata = JSON.parse(await fs.readFile(path.join(lab, 'run.json'), 'utf8'));
assert.equal(metadata.authorization_lifetime, 'until_revoked');
process.env.SPARKCLAW_DESKTOP_ISCP_LOCAL_TEST = '1';
process.env.SPARKCLAW_DESKTOP_ISCP_CONFIG = path.join(lab, 'desktop-profile.json');
process.env.SPARKCLAW_DESKTOP_USER_DATA_DIR = path.join(lab, 'userdata');
const evidence = { schema_version: 1, phase, source: metadata.source, electron: process.versions.electron, chromium: process.versions.chrome, checks: [], direct_business_http_ws_attempts: 0 };
const originalFetch = net.fetch.bind(net);
net.fetch = (...args) => {
  if (/^https?:/u.test(String(args[0]?.url || args[0]))) { evidence.direct_business_http_ws_attempts++; throw new Error('Direct native business HTTP blocked'); }
  return originalFetch(...args);
};
globalThis.fetch = () => { evidence.direct_business_http_ws_attempts++; throw new Error('Direct Node HTTP blocked'); };
const logging = app.whenReady().then(() => netLog.startLogging(path.join(lab, `evidence/authorization-${phase}-network.json`)));
await import('../src/main/main.mjs');
const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
let window;
const evaluate = script => window.webContents.executeJavaScript(script, true);
async function until(predicate, label) {
  const deadline = Date.now() + 30000;
  while (Date.now() < deadline) { if (await predicate()) return; await pause(100); }
  throw new Error(`Native authorization timeout: ${label}`);
}
const status = () => evaluate('window.sparkclawDesktop.localConnection()');
async function click(labels) {
  assert.equal(await evaluate(`(()=>{const element=[...document.querySelectorAll('button')].find(item=>${JSON.stringify(labels)}.includes(item.textContent.trim()));if(!element||element.disabled)return false;element.click();return true})()`), true, `Enabled button ${labels}`);
}
const timer = setTimeout(() => app.exit(1), 90000);
void app.whenReady().then(async () => {
  try {
    await logging;
    await until(async () => { window = BrowserWindow.getAllWindows()[0]; return Boolean(window); }, 'window');
    window.webContents.session.webRequest.onBeforeRequest({ urls: ['http://*/*', 'https://*/*', 'ws://*/*', 'wss://*/*'] }, (_details, callback) => { evidence.direct_business_http_ws_attempts++; callback({ cancel: true }); });
    await until(() => evaluate('Boolean(window.sparkclawDesktop)'), 'real preload');
    if (phase === 'delete') {
      await until(async () => (await status()).state === 'connected', 'real ISCP connection');
      const before = await status();
      evidence.initial_authorization_revision = before.authorization_revision;
      const owner = await evaluate("fetch(window.sparkclawDesktop.gatewayBase+'/api/owner').then(response=>({ok:response.ok,status:response.status}))");
      assert.equal(owner.ok, true);
      await evaluate("document.querySelector('.sidebarAccountTrigger').click()");
      await evaluate("[...document.querySelectorAll('.sidebarAccountMenuItem')].find(button=>/settings|设置/i.test(button.textContent)).click()");
      await until(() => evaluate("Boolean(document.querySelector('.settingsPageNavigation'))"), 'settings');
      await evaluate("[...document.querySelectorAll('.settingsPageNavigation button')].find(button=>/devices|设备/i.test(button.textContent)).click()");
      await click(['Delete this device authorization', '删除此设备授权']);
      assert.ok(containerState(metadata.relay_container, metadata.lab_id)?.State.Running);
      await click(['Confirm deletion', '确认删除授权']);
      await until(async () => (await status()).authorization_deletion?.state === 'revoked', 'signed deletion receipt');
      execFileSync('docker', ['stop', metadata.relay_container], { timeout: 20000, stdio: 'pipe' });
      evidence.checks.push('real_settings_confirmation_deletes_permanent_authorization_and_retains_its_signed_receipt');
    } else {
      assert.equal(containerState(metadata.relay_container, metadata.lab_id)?.State.Running, false);
      await until(async () => (await status()).authorization_deletion?.state === 'revoked', 'persisted deletion after native and issuer restart');
      const before = await status();
      const reconciled = await evaluate('window.sparkclawDesktop.deleteAuthorization()');
      assert.deepEqual(reconciled.authorization_deletion, before.authorization_deletion);
      evidence.checks.push('native_and_issuer_restart_retain_deletion_and_recover_original_signed_receipt_without_Relay');
    }
    const after = await status();
    assert.equal(after.state, 'invalid_authentication');
    assert.equal(after.authorization_deletion.state, 'revoked');
    const intent = JSON.parse(await fs.readFile(path.join(lab, 'userdata/authorization-deletion.json'), 'utf8'));
    assert.equal(intent.operation_id, after.authorization_deletion.operation_id);
    assert.equal(intent.receipt.authorization_revision, intent.expected_revision + 1);
    evidence.authorization_deletion = { state: intent.state, operation_id: intent.operation_id, expected_revision: intent.expected_revision, receipt: intent.receipt };
    const blocked = await evaluate("fetch(window.sparkclawDesktop.gatewayBase+'/api/owner').then(response=>({ok:response.ok,status:response.status}),()=>({ok:false}))");
    assert.equal(blocked.ok, false);
    await until(() => evaluate("/This device authorization was deleted|此设备授权已删除/.test(document.body.textContent)"), 'revoked UI');
    assert.equal(evidence.direct_business_http_ws_attempts, 0);
    evidence.checks.push('revoked_native_UI_and_business_calls_remain_fenced_without_HTTP_fallback');
    await fs.writeFile(path.join(lab, `evidence/authorization-${phase}.png`), (await window.webContents.capturePage()).toPNG(), { mode: 0o600 });
    evidence.passed = true;
  } catch (error) { evidence.error = error.message; console.error(error.stack); }
  finally {
    clearTimeout(timer); await netLog.stopLogging();
    await fs.chmod(path.join(lab, `evidence/authorization-${phase}-network.json`), 0o600);
    await fs.writeFile(path.join(lab, `evidence/authorization-${phase}.json`), JSON.stringify(evidence, null, 2), { mode: 0o600 });
    console.log(JSON.stringify(evidence)); app.exit(evidence.passed ? 0 : 1);
  }
});
