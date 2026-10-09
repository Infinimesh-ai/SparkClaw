// Actual application renderer/main/preload qualification against an isolated
// Linux Gateway. The mail provider is a controlled loopback sink, not a vendor.
import { app, BrowserWindow, net, netLog, safeStorage } from 'electron';
import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import fs from 'node:fs/promises';
import path from 'node:path';

const lab = process.env.SPARKCLAW_MAIL_ISCP_FIXTURE_ROOT;
if (!path.isAbsolute(lab || '')) throw new Error('An absolute private fixture lab is required');
const metadata = JSON.parse(await fs.readFile(path.join(lab, 'run.json'), 'utf8'));
const fixture = path.join(lab, 'native-mail');
const ready = JSON.parse(await fs.readFile(path.join(fixture, 'ready.json'), 'utf8'));
process.env.SPARKCLAW_DESKTOP_ISCP_LOCAL_TEST = '1';
process.env.SPARKCLAW_DESKTOP_ISCP_CONFIG = path.join(lab, 'desktop-profile.json');
process.env.SPARKCLAW_DESKTOP_USER_DATA_DIR = path.join(fixture, 'userdata');
const evidence = { schema_version: 1, source: metadata.source, electron: process.versions.electron, chromium: process.versions.chrome,
  topology: 'Native SparkX renderer -> encrypted ISCP -> Linux Docker Gateway/emailmanagement -> controlled loopback mail sink',
  provider_qualification: 'controlled loopback only; no third-party account and no external email', checks: [], direct_business_http_ws_attempts: 0 };
const originalFetch = net.fetch.bind(net);
net.fetch = (...args) => {
  if (/^https?:/u.test(String(args[0]?.url || args[0]))) { evidence.direct_business_http_ws_attempts++; throw new Error('Direct business HTTP blocked'); }
  return originalFetch(...args);
};
globalThis.fetch = () => { evidence.direct_business_http_ws_attempts++; throw new Error('Direct Node business HTTP blocked'); };
const logReady = app.whenReady().then(() => netLog.startLogging(path.join(fixture, 'native-network.json')));
await import('../src/main/main.mjs');
const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
let window;
const evaluate = source => window.webContents.executeJavaScript(source, true);
async function until(predicate, label) {
  const deadline = Date.now() + 30000;
  while (Date.now() < deadline) { if (await predicate()) return; await pause(100); }
  throw new Error(`Native mail qualification timed out: ${label}`);
}
async function click(labels) {
  assert.equal(await evaluate(`(()=>{const button=[...document.querySelectorAll('button')].find(item=>${JSON.stringify(labels)}.includes(item.textContent.trim()));if(!button||button.disabled)return false;button.click();return true;})()`), true, `Enabled button ${labels}`);
}
async function input(labels, value) {
  assert.equal(await evaluate(`(()=>{const element=[...document.querySelectorAll('input,textarea')].find(item=>${JSON.stringify(labels)}.includes(item.getAttribute('aria-label')));if(!element||element.disabled)return false;const prototype=element.tagName==='TEXTAREA'?HTMLTextAreaElement.prototype:HTMLInputElement.prototype;Object.getOwnPropertyDescriptor(prototype,'value').set.call(element,${JSON.stringify(value)});element.dispatchEvent(new Event('input',{bubbles:true}));return true;})()`), true, `Editable input ${labels}`);
}
const text = () => evaluate("document.querySelector('.emailComposer')?.textContent || ''");
const readSink = async () => JSON.parse(await fs.readFile(path.join(fixture, 'sink-evidence.json'), 'utf8'));
const attachmentPath = 'reports/native-attachment.txt';
const updated = Buffer.from('Reviewed native workspace attachment after the file changed.\n');
const digest = 'sha256:' + crypto.createHash('sha256').update(updated).digest('hex');
const deadline = setTimeout(() => { console.error('Native mail deadline exceeded'); app.exit(1); }, 180000);
void app.whenReady().then(async () => {
  try {
    await logReady;
    assert.equal(safeStorage.isEncryptionAvailable(), true);
    await until(async () => { window = BrowserWindow.getAllWindows()[0]; return Boolean(window); }, 'window');
    window.webContents.session.webRequest.onBeforeRequest({ urls: ['http://*/*', 'https://*/*', 'ws://*/*', 'wss://*/*'] }, (_details, callback) => { evidence.direct_business_http_ws_attempts++; callback({ cancel: true }); });
    await until(() => evaluate("Boolean(window.sparkclawDesktop && document.querySelector('.localWorkbench'))"), 'real workbench renderer');
    await until(async () => (await evaluate('window.sparkclawDesktop.localConnection()')).state === 'connected', 'ISCP connection');
    const connection = await evaluate('window.sparkclawDesktop.localConnection()');
    evidence.capabilities = connection.capabilities;
    assert.equal(connection.capabilities.surfaces.mail_send_attachments.enabled, true);
    evidence.checks.push('fresh_qualified_mail_attachment_capability_in_native_renderer');
    await click(['Mail', '邮箱']);
    await until(() => evaluate("Boolean(document.querySelector('.mailCachePanel'))"), 'mail cache panel');
    await click(['Refresh mailboxes', '更新邮箱列表']);
    await until(() => evaluate("Boolean(document.querySelector('.emailComposer'))"), 'mail draft editor');
    assert.equal(await evaluate("Boolean(document.querySelector('.emailComposer input[type=file]'))"), false);
    await input(['To', '收件人'], 'sink@example.test');
    await input(['Subject', '主题'], 'Native workspace attachment');
    await input(['Body', '正文'], 'Only the reviewed workspace file should be delivered.');
    await input(['Workspace relative file path', '工作区相对文件路径'], attachmentPath);
    await click(['Add attachment', '添加附件']);
    await click(['Review and send', '检查并发送']);
    await until(() => evaluate("Boolean(document.querySelector('.emailComposer [role=region]'))"), 'saved manifest review');
    assert.ok((await text()).includes(ready.initial_sha256));
    await fs.writeFile(path.join(fixture, 'workspace', attachmentPath), updated, { mode: 0o600 });
    await click(['Confirm sending this version', '确认发送此版本']);
    await until(async () => /this attempt was not sent|此次未发送/u.test(await text()), 'changed file rejected before provider send');
    assert.equal((await readSink()).sends.length, 0);
    evidence.checks.push('workspace_file_change_invalidates_old_confirmation_before_any_provider_effect');
    await click(['Reload draft', '重新读取草稿']);
    await until(() => evaluate("![...document.querySelectorAll('.emailComposer button')].find(b=>/^(Review and send|检查并发送)$/.test(b.textContent))?.disabled"), 'draft reload');
    await click(['Review and send', '检查并发送']);
    await until(async () => (await text()).includes(digest), 'fresh attachment hash review');
    assert.ok((await text()).includes(attachmentPath));
    await evaluate("(()=>{const review=document.querySelector('.emailComposer [role=region]');review.querySelector('details').open=true;review.scrollIntoView({block:'center'});})()");
    await evaluate('new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)))');
    await fs.writeFile(path.join(fixture, 'native-mail-review.png'), (await window.webContents.capturePage()).toPNG(), { mode: 0o600 });
    await click(['Confirm sending this version', '确认发送此版本']);
    await until(async () => /The provider confirmed sending|服务方已确认发送/u.test(await text()), 'positive provider receipt');
    let sink = await readSink();
    assert.equal(sink.sends.length, 1); assert.equal(sink.sends[0].attachments[0].sha256, digest);
    assert.equal(sink.sends[0].attachments[0].bytes_base64, updated.toString('base64'));
    evidence.checks.push('renderer_saved_and_confirmed_manifest_delivers_identical_bytes_to_loopback_sink');
    await click(['New draft', '新草稿']);
    await input(['To', '收件人'], 'sink@example.test');
    await input(['Subject', '主题'], 'Lost receipt native attachment');
    await input(['Body', '正文'], 'The provider accepts this once, then drops its response.');
    await input(['Workspace relative file path', '工作区相对文件路径'], attachmentPath);
    await click(['Add attachment', '添加附件']); await click(['Review and send', '检查并发送']);
    await until(async () => (await text()).includes(digest), 'second immutable review');
    await click(['Confirm sending this version', '确认发送此版本']);
    await until(async () => /Send outcome is unknown|发送结果不确定/u.test(await text()), 'missing receipt unknown state');
    await until(async () => (await readSink()).sends.length === 2 && await evaluate("Boolean([...document.querySelectorAll('.emailComposer button')].find(button=>/^(Reconcile send outcome|核对发送结果)$/.test(button.textContent)&&!button.disabled))"), 'provider effect and completed unknown response');
    assert.equal(await evaluate("Boolean(document.querySelector('.emailComposer fieldset').disabled)"), true);
    assert.equal(await evaluate("[...document.querySelectorAll('.emailComposer button')].some(button=>/^(Confirm sending this version|确认发送此版本|Review and send|检查并发送)$/.test(button.textContent))"), false);
    sink = await readSink(); assert.equal(sink.sends.length, 2);
    await click(['Reconcile send outcome', '核对发送结果']);
    await until(async () => /The provider confirmed sending|服务方已确认发送/u.test(await text()), 'original receipt reconciled');
    sink = await readSink(); assert.equal(sink.sends.length, 2); assert.equal(sink.reconciles, 1);
    assert.equal(sink.sends[1].attachments[0].sha256, digest);
    assert.equal(sink.sends[1].attachments[0].bytes_base64, updated.toString('base64'));
    evidence.checks.push('lost_provider_receipt_remains_unknown_until_reconciliation_and_never_resends');
    await evaluate("[...document.querySelectorAll('.emailComposer [role=status]')].find(item=>/The provider confirmed sending|服务方已确认发送/.test(item.textContent)).scrollIntoView({block:'center'})");
    await evaluate('new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)))');
    await fs.writeFile(path.join(fixture, 'native-mail-reconciled.png'), (await window.webContents.capturePage()).toPNG(), { mode: 0o600 });
    evidence.sink = sink;
    assert.equal(evidence.direct_business_http_ws_attempts, 0);
    evidence.passed = true;
  } catch (error) {
    evidence.error = error.message;
    if (window && !window.isDestroyed()) evidence.last_mail_UI = await text().catch(() => 'Renderer unavailable');
    console.error(error.stack);
  }
  finally {
    clearTimeout(deadline); await netLog.stopLogging();
    await fs.chmod(path.join(fixture, 'native-network.json'), 0o600);
    await fs.writeFile(path.join(fixture, 'evidence.json'), JSON.stringify(evidence, null, 2), { mode: 0o600 });
    console.log(JSON.stringify(evidence)); app.exit(evidence.passed ? 0 : 1);
  }
});
