import fs from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import crypto from 'node:crypto';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { PlaywrightCLIClientFactory } from '../../../tools/browser-controller/src/cli-client.mjs';
import { ProviderScriptRegistry } from '../../../tools/browser-controller/src/provider-scripts.mjs';
import { collectUnread, READ_PROVIDERS } from '../../../scripts/email/read.mjs';

const root = process.env.SPARKCLAW_NATIVE_QUAL_ROOT;
const output = path.join(root, 'data/qualifications/native-download-20260908');
await fs.chmod(output, 0o700);
const chunks = []; for await (const chunk of process.stdin) chunks.push(chunk);
const payload = JSON.parse(Buffer.concat(chunks).toString()); for (const chunk of chunks) chunk.fill(0);
const provider = process.env.SPARKCLAW_NATIVE_QUAL_PROVIDER ?? 'fixture';
const owner = crypto.createHash('sha256').update('owner').digest('hex');
const { simpleParser } = createRequire(path.join(root, 'tools/browser-controller/package.json'))('mailparser');
const settings = READ_PROVIDERS[provider === 'fixture' ? 'qq_mail' : provider];
const origins = settings.origins;
const downloadOrigins = [...origins, ...(provider === 'gmail' ? ['https://mail-attachment.googleusercontent.com'] : [])];
let loginURL = settings.url, journal;
if (provider !== 'fixture') {
  const invocation = { qq_mail: 'email_live_read_source_qq_20260907', gmail: 'email_live_read_be736d3d22a5684d', outlook: 'email_live_read_source_outlook_20260907' }[provider];
  const name = crypto.createHash('sha256').update(provider + '\0' + invocation).digest('hex') + '.json';
  journal = JSON.parse(await fs.readFile(path.join(root, 'data/workspaces/email', owner, 'invocations', name), 'utf8'));
  if (provider === 'gmail') loginURL = 'https://mail.google.com/mail/u/0/#all/' + encodeURIComponent(journal.identity.provider_message_id);
}
const started = Date.now();
const digest = bytes => crypto.createHash('sha256').update(bytes).digest('hex');
const fixtureAttachment = Buffer.from(Array.from({ length: (1 << 20) + 37 }, (_, i) => i % 251));
const fixtureMime = ['From: sender@example.test', 'To: receiver@example.test', 'Date: Tue, 8 Sep 2026 10:00:00 +0800', 'Message-ID: <native-fixture@example.test>', 'Subject: Native download fixture', 'MIME-Version: 1.0', 'Content-Type: multipart/mixed; boundary="fixture"', '', '--fixture', 'Content-Type: text/plain; charset=utf-8', '', 'Fixture body', '--fixture', 'Content-Type: application/octet-stream', 'Content-Disposition: attachment; filename="sample.bin"', 'Content-Transfer-Encoding: base64', '', fixtureAttachment.toString('base64').match(/.{1,76}/g).join('\r\n'), '--fixture--', ''].join('\r\n');
const handler = async (_input, runtime) => runtime.withReadTab(async tab => {
  if (provider === 'fixture') {
    console.log(JSON.stringify({stage:'fixture_setup'}));
    const results = [];
    // Route the owned page only; exercise Chromium's native response/Blob download path.
    const small = 'native-download-fixture\r\n';
    const url = origins[0] + '/__sparkclaw_native_' + crypto.randomBytes(12).toString('hex');
    await tab.runReadCode(`async page => {
      await page.context().route(${JSON.stringify(url)} + '**', route => {
        if (route.request().url().endsWith('/redirect')) return route.fulfill({ status:302, headers:{location:${JSON.stringify(url + '/file')}} });
        return route.fulfill({ status:200, contentType:'message/rfc822', headers:{'content-disposition':'attachment; filename="fixture.eml"'}, body:${JSON.stringify(small)} });
      });
      await page.setContent('<a id="direct" href="${url}/file">Direct</a><a id="popup" target="_blank" rel="opener" href="${url}/file">Popup</a><button id="blob">Blob</button>');
      await page.evaluate(value => document.querySelector('#blob').onclick = () => { const a=document.createElement('a'); a.href=URL.createObjectURL(new Blob([value])); a.download='fixture.eml'; a.click(); }, ${JSON.stringify(small)});
      return true;
    }`);
    const fixtureMode=process.env.SPARKCLAW_NATIVE_QUAL_MODE ?? 'direct';
    for (const mode of fixtureMode==='mime_attachment'?[]:[fixtureMode]) {
      console.log(JSON.stringify({stage:'fixture_download',mode}));
      if (mode === 'popup' && process.env.SPARKCLAW_NATIVE_QUAL_DEBUG === '1') {
        const info = await tab.runReadCode(`async page => {
          const events=[], children=[];
          const onPage=async p => { if(await p.opener()===page) {children.push(p);events.push('popup');p.on('download',()=>events.push('popup_download'));} };
          page.context().on('page',onPage);
          const pending=page.waitForEvent('download',{timeout:5000}).then(()=>events.push('page_download')).catch(()=>events.push('timeout'));
          await page.locator('#popup').click();await pending;
          page.context().off('page',onPage);
          const pages=page.context().pages().length;
          for(const p of children) await p.close().catch(()=>{});
          return {events,pages};
        }`);
        console.log(JSON.stringify(info)); return {debug:true};
      }
      const target = path.join(output, 'fixture-' + mode + '.eml');
      await fs.rm(target, { force:true });
      const result = await tab.download('#' + mode, target, 2 << 20);
      const bytes = await fs.readFile(target);
      assert.equal(bytes.toString(), small);
      results.push({ mode, bytes:result.bytes, sha256:digest(bytes), exact:true });
    }
    if(fixtureMode==='mime_attachment') {
    await tab.runReadCode(`async page => {
      await page.evaluate(({prefix, suffix, length}) => {
        const bytes = new Uint8Array(length); for(let i=0;i<length;i++) bytes[i]=i%251;
        let binary=''; for(let i=0;i<length;i+=8192) binary+=String.fromCharCode(...bytes.subarray(i,i+8192));
        const eml=prefix + btoa(binary).match(/.{1,76}/g).join('\\r\\n') + suffix;
        document.querySelector('#blob').onclick = () => { const a=document.createElement('a'); a.href=URL.createObjectURL(new Blob([eml])); a.download='fixture.eml'; a.click(); };
      }, ${JSON.stringify({prefix:fixtureMime.split(fixtureAttachment.toString('base64').slice(0,76))[0], suffix:'\r\n--fixture--\r\n',length:fixtureAttachment.length})});
      return true;
    }`);
    const mimeTarget=path.join(output,'fixture-attachment.eml');
    await fs.rm(mimeTarget,{force:true});
    await tab.download('#blob',mimeTarget,2<<20);
    const mimeBytes=await fs.readFile(mimeTarget), parsed=await simpleParser(mimeBytes);
    assert.equal(mimeBytes.toString(),fixtureMime);
    assert.equal(parsed.attachments.length,1);
    assert.deepEqual(parsed.attachments[0].content,fixtureAttachment);
    results.push({mode:'mime_attachment',bytes:mimeBytes.length,sha256:digest(mimeBytes),attachment_bytes:fixtureAttachment.length,attachment_exact:true});
    }
    const receipt={provider,status:'verified',results};
    await fs.writeFile(path.join(output,'fixture-'+fixtureMode+'-receipt.json'),JSON.stringify(receipt,null,2),{mode:0o600});
    return receipt;
  }
  const message = await collectUnread(tab, provider, {
    pinned_message_id: journal.identity.provider_message_id,
    pinned_selection_id: journal.selection.provider_selection_id,
    onSelected: async value => assert.equal(value.provider_message_id, journal.identity.provider_message_id),
  });
  assert.equal(message.account_address.toLowerCase(), journal.identity.account_address.toLowerCase());
  const target = path.join(output, provider + '.eml');
  await fs.rm(target, { force:true });
  const result = await tab.download(message.original.selector, target, 10 << 20);
  const bytes = await fs.readFile(target), parsed = await simpleParser(bytes);
  if(message.subject !== undefined) assert.equal(parsed.subject, message.subject);
  assert.ok(parsed.messageId && parsed.from?.value?.length && parsed.date);
  let reference;
  if (provider !== 'outlook') reference = await fs.readFile(path.join(root, 'data/qualifications/email-eml-20260908', provider + '.eml'));
  else reference=await fs.readFile(path.join(root,'data/workspaces',path.dirname(journal.receipt.capture.manifest_path),'message.eml'));
  const receipt = { provider, status:'verified', acquisition:'playwright_download_saveAs', bytes:result.bytes,
    sha256:digest(bytes), reference_exact:reference ? bytes.equals(reference) : null,
    subject_matches:message.subject === undefined?null:true, attachment_count:parsed.attachments.length, elapsed_ms:Date.now()-started };
  await fs.writeFile(path.join(output, provider + '-receipt.json'), JSON.stringify(receipt,null,2), {mode:0o600});
  return receipt;
});
const entryProvider = provider === 'fixture' ? 'qq_mail' : provider;
const entry = { provider:entryProvider, operation:'read', scriptID:entryProvider+'.native_qualification', revision:1,
  loginURL, origins, downloadOrigins, timeoutMS:180000, handler, sourceFiles:['data/qualifications/native-download-20260908/verify.mjs'], validate:() => {} };
const factory = new PlaywrightCLIClientFactory({ registry:new ProviderScriptRegistry([entry]), runtimeRoot:path.join(output,'cli-runtime'),
  executablePath:path.join(os.homedir(),'.local/share/sparkclaw/browser-controller/bin/browser-bridge-launcher'),
  userDataDir:path.join(os.homedir(),'.local/share/sparkclaw/browser/default/user-data'), diagnostic:value=>console.error(JSON.stringify(value)) });
try {
  await factory.prepare();
  const result = await factory.runScript({token:payload.token, sessionID:'session_'+crypto.randomBytes(16).toString('hex'), provider:entryProvider, operation:'read', scriptID:entry.scriptID, revision:1,
    input:{schema_version:1, operation:'read', provider:entryProvider, account:'default', owner_scope:owner, invocation_id:'native-'+crypto.randomUUID()} });
  if (result.state !== 'completed') throw new Error('qualification_not_completed');
  console.log(JSON.stringify(result.result));
} catch (error) {
  console.error(JSON.stringify({code:error.code ?? 'qualification_failed', reason:error.diagnosticReason ?? 'unclassified'}));
  process.exitCode=1;
} finally { payload.token=''; }
