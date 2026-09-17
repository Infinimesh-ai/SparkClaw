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
    const bridge = page.locator('#sparkclaw-ai-export-bridge');
    await bridge.waitFor({state:'attached', timeout:15000});
    const scope = await page.evaluate(() => !document.querySelector('[data-testid="stop-button"], button[aria-label="Stop streaming"], button[aria-label="Stop response"]'));
    if (!scope) throw new Error('ai_chat_loading_unverified');
    const pending = page.waitForEvent('download', {timeout:25000});
    pending.catch(() => {});
    let download;
    try {
      await bridge.evaluate(node => {
        node.dataset.command = 'conversation.export-json';
        node.dataset.state = 'working';
        node.textContent = '';
        node.dispatchEvent(new Event('sparkclaw-ai-export-command'));
      });
      await page.waitForFunction(() => ['ready', 'failed'].includes(document.querySelector('#sparkclaw-ai-export-bridge')?.dataset.state), null, {polling:100, timeout:25000});
      if (await bridge.getAttribute('data-state') !== 'ready') throw new Error(await bridge.innerText());
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
