import crypto from 'node:crypto';
import fs from 'node:fs/promises';
import path from 'node:path';
import { invalidRequest } from './errors.mjs';

export const AI_CHAT_MAX_BYTES = 4 << 20;
const providers = {
  chatgpt: ['chatgpt.com', /^\/c\/[A-Za-z0-9-]+\/?$/u],
  claude: ['claude.ai', /^\/chat\/[A-Za-z0-9-]+\/?$/u],
  gemini: ['gemini.google.com', /^\/app\/[A-Za-z0-9_-]+\/?$/u],
  grok: ['grok.com', /^\/c\/[A-Za-z0-9-]+\/?$/u],
};
export function validateAIChatURL(provider, value) {
  let u;
  try { u = new URL(value); } catch { throw invalidRequest('ai_chat_url_invalid'); }
  const rule = providers[provider];
  if (!rule || u.protocol !== 'https:' || u.host !== rule[0] || u.username || u.password || u.search || u.hash || !rule[1].test(u.pathname)) throw invalidRequest('ai_chat_url_invalid');
  return u.href.replace(/\/$/u, '');
}

// Fixed bundled operation: no caller-provided selectors, code, or filesystem paths.
export async function captureAIChat({provider, url, outputDir, runCode}) {
  const source = validateAIChatURL(provider, url);
  const file = path.join(outputDir, `ai-chat-${crypto.randomUUID()}.json`);
  const code = `async page => {
    const expected = ${JSON.stringify(source)};
    const matches = () => page.url().replace(/\\/$/, '') === expected;
    if (!matches()) throw new Error('ai_chat_source_changed');
    const button = page.locator('#export-controls-container #export-json-btn');
    await button.waitFor({state:'visible', timeout:15000});
    // Refuse a filtered/manual subset; never silently change the owner's choices.
    const scope = await page.evaluate(() => {
      const select = document.querySelector('#outline-select-all');
      const search = document.querySelector('#outline-search-input');
      const boxes = [...document.querySelectorAll('#export-outline-container input[type="checkbox"]')];
      const busy = document.querySelector('[data-testid="stop-button"], button[aria-label="Stop streaming"], button[aria-label="Stop response"]');
      return !busy && select?.checked === true && !select.indeterminate && search?.value === '' && boxes.every(b => b.checked);
    });
    if (!scope) throw new Error('ai_chat_selection_or_loading_unverified');
    const pending = page.waitForEvent('download', {timeout:25000});
    pending.catch(() => {});
    let download;
    try {
      await button.evaluate(node => node.click());
      download = await pending;
      const allowed = await page.evaluate(({value, expected}) => {
        const u = new URL(value);
        return ['blob:', 'https:'].includes(u.protocol) && u.origin === new URL(expected).origin && !u.username && !u.password;
      }, {value:download.url(), expected});
      if (!matches() || !allowed) throw new Error('ai_chat_download_source_invalid');
      await download.saveAs(${JSON.stringify(file)});
      if (await download.failure()) throw new Error('ai_chat_download_failed');
      return {status:'downloaded'};
    } finally {
      if(download) await download.delete().catch(() => {});
      else void pending.then(async d => { await d.cancel(); await d.delete(); }).catch(() => {});
    }
  }`;
  try {
    await runCode(code);
    const stat = await fs.lstat(file);
    if (!stat.isFile() || stat.isSymbolicLink() || stat.uid !== process.getuid() || stat.size < 1 || stat.size > AI_CHAT_MAX_BYTES) throw invalidRequest('ai_chat_file_invalid_or_too_large');
    const bytes = await fs.readFile(file);
    const doc = JSON.parse(bytes.toString('utf8'));
    if (validateAIChatURL(provider, doc.url) !== source || !Array.isArray(doc.messages) || !doc.messages.length || doc.author !== provider || !doc.exporter) throw invalidRequest('ai_chat_export_invalid');
    for(const message of doc.messages) if (!['user','ai'].includes(message.author) || typeof message.content !== 'string') throw invalidRequest('ai_chat_messages_invalid');
    return {content_base64:bytes.toString('base64'), sha256:crypto.createHash('sha256').update(bytes).digest('hex'), bytes:bytes.length};
  } finally { await fs.rm(file, {force:true}); }
}
