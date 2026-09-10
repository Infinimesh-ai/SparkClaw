import fs from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import crypto from 'node:crypto';
import { createRequire } from 'node:module';
import { PlaywrightCLIClientFactory } from '../../../tools/browser-controller/src/cli-client.mjs';
import { ProviderScriptRegistry } from '../../../tools/browser-controller/src/provider-scripts.mjs';
import { collectUnread, READ_PROVIDERS } from '../../../scripts/email/read.mjs';

const root = process.env.SPARKCLAW_EML_QUAL_ROOT;
const output = path.join(root, 'data/qualifications/email-eml-20260908');
await fs.chmod(output, 0o700);
const provider = process.env.SPARKCLAW_EML_QUAL_PROVIDER;
const mode = process.env.SPARKCLAW_EML_QUAL_MODE ?? 'export';
if (!['qq_mail', 'gmail'].includes(provider)) throw new Error('qualification_provider_required');
const chunks = []; for await (const chunk of process.stdin) chunks.push(chunk);
const payload = JSON.parse(Buffer.concat(chunks).toString());
for (const chunk of chunks) chunk.fill(0);
const owner = crypto.createHash('sha256').update('owner').digest('hex');
const invocation = provider === 'gmail' ? 'email_live_read_be736d3d22a5684d' : 'email_live_read_source_qq_20260907';
const journalName = crypto.createHash('sha256').update(provider + '\0' + invocation).digest('hex') + '.json';
const journal = JSON.parse(await fs.readFile(path.join(root, 'data/workspaces/email', owner, 'invocations', journalName), 'utf8'));
const pinned = journal.identity;
if (!pinned?.provider_message_id) throw new Error('qualification_pin_missing');
const origins = READ_PROVIDERS[provider].origins;
const downloadOrigins = [...origins, ...(provider === 'gmail' ? ['https://mail-attachment.googleusercontent.com'] : [])];
const loginURL = provider === 'gmail' ? 'https://mail.google.com/mail/u/0/#all/' + encodeURIComponent(pinned.provider_message_id) : READ_PROVIDERS[provider].url;
const { simpleParser } = createRequire(path.join(root, 'tools/browser-controller/package.json'))('mailparser');
const started = Date.now();
const key = '__qualification_eml_' + crypto.randomBytes(8).toString('hex');

function install({ key, origins }) {
  const originalOpen = window.open, originalClick = HTMLAnchorElement.prototype.click;
  const state = { status: 'waiting', events: [], buffer: null };
  const acquire = value => {
    let url; try { url = new URL(value, location.href); } catch { return false; }
    if (!origins.includes(url.origin) || !['https:', 'blob:'].includes(url.protocol)) {
      state.events.push({ allowed: false, origin: url.origin }); return false;
    }
    state.events.push({ allowed: true, origin: url.origin, protocol: url.protocol });
    if (state.status !== 'waiting') return true;
    state.status = 'fetching';
    void (async () => {
      try {
        const response = await fetch(url.href, { credentials: 'include', signal: AbortSignal.timeout(20000) });
        state.response = { status: response.status, type: response.headers.get('content-type'), redirected: response.redirected, origin: new URL(response.url).origin };
        if (!response.ok || !origins.includes(new URL(response.url).origin)) throw new Error('export_response_rejected');
        const reader = response.body.getReader(), chunks = []; let total = 0;
        for (;;) {
          const { value, done } = await reader.read(); if (done) break;
          total += value.byteLength;
          if (total > 10 << 20) { await reader.cancel(); throw new Error('export_sample_limit'); }
          chunks.push(value);
        }
        state.buffer = new Uint8Array(total); let offset = 0;
        for (const chunk of chunks) { state.buffer.set(chunk, offset); offset += chunk.length; }
        state.status = 'captured';
      } catch (error) { state.status = 'failed'; state.error = error.name; }
    })();
    return true;
  };
  window.open = function(url) { acquire(url); return { closed: false, focus() {}, close() {} }; };
  HTMLAnchorElement.prototype.click = function() { if (!acquire(this.href)) return originalClick.call(this); };
  const listener = event => { const anchor = event.target.closest?.('a'); if (anchor && (anchor.download || anchor.href.startsWith('blob:')) && acquire(anchor.href)) event.preventDefault(); };
  document.addEventListener('click', listener, true);
  state.restore = () => { window.open = originalOpen; HTMLAnchorElement.prototype.click = originalClick; document.removeEventListener('click', listener, true); };
  globalThis[key] = state;
}

const handler = async (input, runtime) => runtime.withReadTab(async tab => {
  const message = await collectUnread(tab, provider, {
    pinned_message_id: pinned.provider_message_id,
    pinned_selection_id: journal.selection.provider_selection_id,
    onSelected: async selected => { if (selected.provider_message_id !== pinned.provider_message_id) throw new Error('qualification_identity_changed'); },
  });
  if (message.account_address.toLowerCase() !== pinned.account_address.toLowerCase()) throw new Error('qualification_account_changed');
  if (mode === 'headers' || mode === 'details') {
    await tab.press('Escape');
    if (mode === 'details' && provider === 'qq_mail') {
      await tab.runReadCode(`async page => { await page.locator('.xmail-ui-hyperlink:text-is("Details")').evaluate(node=>node.click()); return true; }`);
    }
    const headerObservation = await tab.runReadCode(`async page => page.evaluate(provider => {
      const visible = node => { const r=node.getBoundingClientRect(); return r.width>0&&r.height>0&&getComputedStyle(node).visibility!=='hidden'; };
      if (provider === 'gmail') return {browser_timezone:Intl.DateTimeFormat().resolvedOptions().timeZone, dates:Array.from(document.querySelectorAll('.g3')).filter(visible).map(n=>({text:n.innerText,title:n.getAttribute('title')}))};
      const subject=document.querySelector('.mail-detail-subject'), body=document.querySelector('.mail-detail-content');
      let scope=subject;while(scope.parentElement&&!scope.contains(body))scope=scope.parentElement;
      return {browser_timezone:Intl.DateTimeFormat().resolvedOptions().timeZone,
        text:Array.from(scope.querySelectorAll('*')).filter(n=>visible(n)&&!n.children.length&&!body.contains(n)).map(n=>({tag:n.tagName,class:n.className,text:n.innerText,title:n.getAttribute('title'),label:n.getAttribute('aria-label'),parent_class:n.parentElement.className,parent_text:n.parentElement.innerText})),
        controls:Array.from(scope.querySelectorAll('button,[role="button"],[title]')).filter(n=>visible(n)&&!body.contains(n)).map(n=>({tag:n.tagName,class:n.className,text:n.innerText,title:n.getAttribute('title'),label:n.getAttribute('aria-label')}))};
    }, ${JSON.stringify(provider)})`);
    await fs.writeFile(path.join(output, provider + (mode==='details' ? '-expanded-headers-observation.json' : '-headers-observation.json')), JSON.stringify(headerObservation,null,2), {mode:0o600});
    return {provider,status:'headers_observed',browser_timezone:headerObservation.browser_timezone,leaf_classes:headerObservation.text?.map(n=>n.class),controls:headerObservation.controls?.map(n=>({tag:n.tag,class:n.class,title:n.title,label:n.label})),dates:headerObservation.dates?.map(n=>({text_shape:n.text.replace(/[0-9]/g,'#'),title_shape:n.title?.replace(/[0-9]/g,'#')}))};
  }
  await fs.writeFile(path.join(output, provider + '-current-observation.json'), JSON.stringify(message, null, 2), { mode: 0o600 });
  const result = { provider, account_matches: true, identity_matches: true, acquisition: 'provider_export_response', elapsed_ms: null };
  try {
    const capture = await tab.runReadCode(`async page => {
      await page.evaluate(${install.toString()}, ${JSON.stringify({ key, origins: downloadOrigins })});
      await page.locator(${JSON.stringify(message.original.selector)}).evaluate(node => node.click());
      for (let attempt = 0; attempt < 200; attempt++) {
        const value = await page.evaluate(key => { const s=globalThis[key]; return { status:s.status,events:s.events,response:s.response,error:s.error,bytes:s.buffer?.byteLength }; }, ${JSON.stringify(key)});
        if (['captured','failed'].includes(value.status)) return value;
        await page.waitForTimeout(100);
      }
      return await page.evaluate(key => { const s=globalThis[key]; return {status:s.status,events:s.events,response:s.response,error:s.error}; }, ${JSON.stringify(key)});
    }`);
    Object.assign(result, capture);
    if (capture.status === 'captured') {
      const buffers = [];
      for (let offset = 0; offset < capture.bytes; offset += 131072) {
        const encoded = await tab.runReadCode(`async page => page.evaluate(key => { const b=globalThis[key].buffer.subarray(${offset},${offset + 131072});let s='';for(let i=0;i<b.length;i+=8192)s+=String.fromCharCode(...b.subarray(i,i+8192));return btoa(s); }, ${JSON.stringify(key)})`);
        buffers.push(Buffer.from(encoded, 'base64'));
      }
      const bytes = Buffer.concat(buffers);
      const parsed = await simpleParser(bytes);
      if (bytes.length !== capture.bytes || !parsed.messageId || !parsed.from?.value?.length || !parsed.date || parsed.subject !== message.subject) {
        result.status = 'invalid_eml';
        result.mime_headers_present = { message_id: !!parsed.messageId, from: !!parsed.from?.value?.length, date: !!parsed.date, subject_matches: parsed.subject === message.subject };
      } else {
        const destination = path.join(output, provider + '.eml');
        await fs.writeFile(destination, bytes, { mode: 0o600, flag: 'wx' });
        result.sha256 = crypto.createHash('sha256').update(bytes).digest('hex');
        result.attachment_count = parsed.attachments.length;
        result.subject_matches = true;
      }
    }
  } finally {
    await tab.runReadCode(`async page => { await page.evaluate(key => { globalThis[key]?.restore(); delete globalThis[key]; }, ${JSON.stringify(key)}); return true; }`).catch(() => {});
  }
  result.elapsed_ms = Date.now() - started;
  await fs.writeFile(path.join(output, provider + '-acquisition.json'), JSON.stringify(result, null, 2), { mode: 0o600 });
  return result;
});
const entry = { provider, operation: 'read', scriptID: provider + '.eml_qualification', revision: 1, loginURL, origins, downloadOrigins, timeoutMS: 180000, handler, sourceFiles: ['data/qualifications/email-eml-20260908/acquire.mjs'], validate: value => { if (value.operation !== 'read') throw new Error('qualification_input_invalid'); } };
const factory = new PlaywrightCLIClientFactory({ registry: new ProviderScriptRegistry([entry]), runtimeRoot: path.join(output, 'cli-runtime'), executablePath: path.join(os.homedir(), '.local/share/sparkclaw/browser-controller/bin/browser-bridge-launcher'), userDataDir: path.join(os.homedir(), '.local/share/sparkclaw/browser/default/user-data'), diagnostic: event => console.error(JSON.stringify(event)) });
try {
  await factory.prepare();
  const result = await factory.runScript({ token: payload.token, sessionID: 'session_' + crypto.randomBytes(16).toString('hex'), provider, operation: 'read', scriptID: entry.scriptID, revision: 1, input: { schema_version: 1, operation: 'read', provider, account: 'default', owner_scope: owner, invocation_id: 'eml-qualification-' + crypto.randomUUID() } });
  if (result.state !== 'completed') { console.error(JSON.stringify({ state: result.state, code: result.result?.code })); process.exitCode = 1; }
  else console.log(JSON.stringify(result.result));
} catch (error) { console.error(JSON.stringify({ code: error.code ?? 'qualification_failed', reason: error.diagnosticReason ?? 'unclassified' })); process.exitCode = 1; }
finally { payload.token = ''; }
