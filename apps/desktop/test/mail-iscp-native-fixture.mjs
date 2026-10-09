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
const desktopData = process.env.SPARKCLAW_MAIL_LOCAL_DATA;
if (!path.isAbsolute(desktopData || '') || desktopData.startsWith(lab + path.sep)) throw new Error('Desktop data must be outside Gateway mounts');
process.env.SPARKCLAW_DESKTOP_USER_DATA_DIR = desktopData;
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
async function until(predicate, label, budgetMS = 30000) {
  const deadline = Date.now() + budgetMS;
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
const attachmentName = 'native-attachment.txt';
const initial = Buffer.from('Initial desktop-local attachment before explicit review.\n');
const initialDigest = 'sha256:' + crypto.createHash('sha256').update(initial).digest('hex');
const updated = Buffer.from('Reviewed desktop-local attachment after explicit reselection.\n');
const digest = 'sha256:' + crypto.createHash('sha256').update(updated).digest('hex');
const deadline = setTimeout(() => { console.error('Native mail deadline exceeded'); app.exit(1); }, 360000);
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
    const conversation = await evaluate("window.sparkclawClientStore.create('Desktop-only mail files')");
    const saveLocal = bytes => evaluate(`window.sparkclawClientStore.saveFile(${JSON.stringify(conversation.id)},${JSON.stringify(attachmentName)},new Uint8Array(${JSON.stringify([...bytes])}))`);
    const originalFile = await saveLocal(initial);
    const selectFile = async fileID => {
      assert.equal(await evaluate(`(()=>{const element=document.querySelector('select[aria-label="Local workspace file"],select[aria-label="本机工作区文件"]');if(!element||!Array.from(element.options).some(option=>option.value===${JSON.stringify(fileID)}))return false;Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype,'value').set.call(element,${JSON.stringify(fileID)});element.dispatchEvent(new Event('change',{bubbles:true}));return true;})()`),true);
      await click(['Add attachment','添加附件']);
    };
    await click(['Mail', '邮箱']);
    await until(() => evaluate("Boolean(document.querySelector('.mailCachePanel'))"), 'mail cache panel');
    await click(['Refresh mailboxes', '更新邮箱列表']);
    await until(() => evaluate("Boolean(document.querySelector('.emailComposer'))"), 'mail draft editor');
    assert.equal(await evaluate("Boolean(document.querySelector('.emailComposer input[type=file]'))"), false);
    await input(['To', '收件人'], 'sink@example.test');
    await input(['Subject', '主题'], 'Native workspace attachment');
    await input(['Body', '正文'], 'Only the reviewed workspace file should be delivered.');
    await until(() => evaluate(`Boolean(document.querySelector('option[value="${originalFile.id}"]'))`), 'desktop local file inventory');
    await selectFile(originalFile.id);
    await click(['Review and send', '检查并发送']);
    await until(() => evaluate("Boolean(document.querySelector('.emailComposer [role=region]'))"), 'saved manifest review');
    assert.ok((await text()).includes(initialDigest));
    await fs.writeFile(path.join(desktopData, 'workbench/files', originalFile.id), updated, { mode: 0o600 });
    await click(['Confirm sending this version', '确认发送此版本']);
    await until(async () => /this attempt was not sent|此次未发送/u.test(await text()), 'changed file rejected before provider send');
    assert.equal((await readSink()).sends.length, 0);
    evidence.checks.push('desktop_file_change_invalidates_old_confirmation_before_any_provider_effect');
    await fs.writeFile(path.join(desktopData, 'workbench/files', originalFile.id), initial, { mode: 0o600 });
    const updatedFile = await saveLocal(updated);
    await click(['Reload draft', '重新读取草稿']);
    await until(() => evaluate("![...document.querySelectorAll('.emailComposer button')].find(b=>/^(Review and send|检查并发送)$/.test(b.textContent))?.disabled"), 'draft reload');
    assert.equal(await evaluate(`(()=>{const button=document.querySelector('button[aria-label="Remove attachment ${originalFile.id}"],button[aria-label="移除附件 ${originalFile.id}"]');if(!button)return false;button.click();return true;})()`),true);
    await click(['Refresh local files', '刷新本机文件']);
    await until(() => evaluate(`Boolean(document.querySelector('option[value="${updatedFile.id}"]'))`), 'replacement desktop file inventory');
    await selectFile(updatedFile.id);
    await click(['Review and send', '检查并发送']);
    await until(async () => (await text()).includes(digest), 'fresh attachment hash review');
    assert.ok((await text()).includes(attachmentName));
    await evaluate("(()=>{const review=document.querySelector('.emailComposer [role=region]');review.querySelector('details').open=true;review.scrollIntoView({block:'center'});})()");
    await evaluate('new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)))');
    await fs.writeFile(path.join(fixture, 'native-mail-review.png'), (await window.webContents.capturePage()).toPNG(), { mode: 0o600 });
    await click(['Confirm sending this version', '确认发送此版本']);
    await until(async () => /The provider confirmed sending|服务方已确认发送/u.test(await text()), 'positive provider receipt');
    let sink = await readSink();
    assert.equal(sink.sends.length, 1); assert.equal(sink.sends[0].attachments[0].sha256, digest);
    assert.equal(sink.sends[0].attachments[0].bytes_base64, updated.toString('base64'));
    assert.notEqual(sink.sends[0].attachments[0].sha256, ready.gateway_decoy_sha256);
    assert.equal(await fs.readFile(path.join(fixture, 'workspace/reports/native-attachment.txt'), 'utf8'), 'Gateway decoy: these bytes must never be attached by SparkX.\n');
    evidence.checks.push('desktop_owned_file_transferred_without_gateway_source_access_or_decoy_read');
    evidence.checks.push('renderer_saved_and_confirmed_manifest_delivers_identical_bytes_to_loopback_sink');
    await click(['New draft', '新草稿']);
    await input(['To', '收件人'], 'sink@example.test');
    await input(['Subject', '主题'], 'Lost receipt native attachment');
    await input(['Body', '正文'], 'The provider accepts this once, then drops its response.');
    await selectFile(updatedFile.id);
    await click(['Review and send', '检查并发送']);
    await until(async () => (await text()).includes(digest), 'second immutable review');
    await click(['Confirm sending this version', '确认发送此版本']);
    await until(async () => /Send outcome is unknown|发送结果不确定/u.test(await text()), 'missing receipt unknown state');
    await until(async () => (await readSink()).sends.length === 2 && await evaluate("Boolean([...document.querySelectorAll('.emailComposer button')].find(button=>/^(Reconcile send outcome|核对发送结果)$/.test(button.textContent)&&!button.disabled))"), 'provider effect and completed unknown response');
    assert.equal(await evaluate("Boolean(document.querySelector('.emailComposer fieldset').disabled)"), true);
    assert.equal(await evaluate("[...document.querySelectorAll('.emailComposer button')].some(button=>/^(Confirm sending this version|确认发送此版本|Review and send|检查并发送)$/.test(button.textContent))"), false);
    sink = await readSink(); assert.equal(sink.sends.length, 2);
    // Receipt recovery must succeed even after the desktop source is removed.
    await fs.unlink(path.join(desktopData, 'workbench/files', updatedFile.id));
    await click(['Reconcile send outcome', '核对发送结果']);
    await until(async () => /The provider confirmed sending|服务方已确认发送/u.test(await text()), 'original receipt reconciled');
    sink = await readSink(); assert.equal(sink.sends.length, 2); assert.equal(sink.reconciles, 1);
    assert.equal(sink.sends[1].attachments[0].sha256, digest);
    assert.equal(sink.sends[1].attachments[0].bytes_base64, updated.toString('base64'));
    evidence.checks.push('lost_provider_receipt_remains_unknown_until_reconciliation_and_never_resends');
    await evaluate("[...document.querySelectorAll('.emailComposer [role=status]')].find(item=>/The provider confirmed sending|服务方已确认发送/.test(item.textContent)).scrollIntoView({block:'center'})");
    await evaluate('new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)))');
    await fs.writeFile(path.join(fixture, 'native-mail-reconciled.png'), (await window.webContents.capturePage()).toPNG(), { mode: 0o600 });
    // Exercise the advertised object-transfer ceiling through the production
    // main/IPC path; the controlled sink still cannot send external mail.
    const capacityBytes = Buffer.alloc(10 * 1024 * 1024, 0x61);
    const capacityHash = 'sha256:' + crypto.createHash('sha256').update(capacityBytes).digest('hex');
    const capacityFile = await evaluate(`window.sparkclawClientStore.saveFile(${JSON.stringify(conversation.id)},'capacity-10MiB.txt',new Uint8Array(10*1024*1024).fill(0x61))`);
    await click(['New draft', '新草稿']);
    await click(['Refresh local files', '刷新本机文件']);
    await until(() => evaluate(`Boolean(document.querySelector('option[value="${capacityFile.id}"]'))`), 'capacity local file inventory');
    await input(['To', '收件人'], 'sink@example.test');
    await input(['Subject', '主题'], 'Desktop local capacity attachment');
    await input(['Body', '正文'], 'Controlled sink qualification at the advertised file transfer ceiling.');
    await selectFile(capacityFile.id);
    const transferStarted = Date.now();
    await click(['Review and send', '检查并发送']);
    await until(async () => (await text()).includes(capacityHash), '10 MiB desktop object review', 180000);
    const transferMS = Date.now() - transferStarted;
    await click(['Confirm sending this version', '确认发送此版本']);
    await until(async () => /The provider confirmed sending|服务方已确认发送/u.test(await text()), '10 MiB controlled sink receipt', 180000);
    sink = await readSink();
    assert.equal(sink.sends.length, 3);
    assert.equal(sink.sends[2].attachments[0].bytes_base64, capacityBytes.toString('base64'));
    assert.equal(sink.sends[2].attachments[0].sha256, capacityHash);
    evidence.desktop_object_capacity = { bytes: capacityBytes.length, sha256: capacityHash, transfer_review_ms: transferMS, no_external_email: true };
    evidence.checks.push('10MiB_desktop_source_encrypted_transfer_and_exact_sink_bytes');
    evidence.sink = { ...sink, sends: sink.sends.map(send => ({ ...send, attachments: send.attachments.map(({bytes_base64,...manifest}) => manifest) })) };
    evidence.desktop_data_mounted_into_gateway = false;
    evidence.gateway_decoy_sha256 = ready.gateway_decoy_sha256;
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
