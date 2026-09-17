import fs from 'node:fs/promises';
import path from 'node:path';
import crypto from 'node:crypto';
import { SPARKCLAW_BATCH_PROVIDERS, discoverBatchHistory, runTimelineBatch } from '../../tools/browser-userscripts/timeline-core.mjs';

// SparkClaw can supply its authorized BrowserContext. Only this new page is
// navigated/closed; no global download settings, browser restart or email state.
export async function exportTimeline({ context, provider, workspaceRoot, accountScope, signal, timeoutMS = 1_800_000, bridgeID = 'sparkclaw-ai-export-bridge' }) {
  const config = SPARKCLAW_BATCH_PROVIDERS[provider];
  if (!config || typeof accountScope !== 'string' || !accountScope.trim() || !Number.isFinite(timeoutMS) || timeoutMS <= 0 || !['sparkclaw-ai-export-bridge', 'sparkclaw-live-ai-export-bridge'].includes(bridgeID)) throw new Error('batch_arguments_invalid');
  const root = await fs.realpath(workspaceRoot);
  async function subdirectory(parent, name) {
    const result = path.join(parent, name);
    await fs.mkdir(result, { recursive: true });
    if ((await fs.lstat(result)).isSymbolicLink() || await fs.realpath(result) !== result) throw new Error('batch_workspace_escape');
    return result;
  }
  const hash = value => crypto.createHash('sha256').update(value).digest('hex');
  const parent = await subdirectory(root, 'ai-chat-exports');
  const directory = await subdirectory(parent, provider + '-' + hash(accountScope).slice(0, 24));
  const lockPath = path.join(directory, '.batch.lock');
  const lock = await fs.open(lockPath, 'wx', 0o600).catch(() => { throw new Error('batch_already_running_or_stale_lock'); });
  const ledgerPath = path.join(directory, 'ledger.json');
  const manifestPath = path.join(directory, 'batch-' + crypto.randomUUID() + '.json');
  const deadline = Date.now() + timeoutMS;
  let page, timer;
  const check = () => { signal?.throwIfAborted(); if (Date.now() >= deadline) throw new Error('batch_timeout'); };
  const abort = () => { void page?.close().catch(() => {}); };
  const write = async (target, text) => {
    const temporary = target + '.' + crypto.randomUUID() + '.tmp';
    try { await fs.writeFile(temporary, text, { flag: 'wx', mode: 0o600 }); await fs.rename(temporary, target); }
    finally { await fs.rm(temporary, { force: true }); }
  };
  let ledger = {}, timeline, result;
  try {
    try { ledger = JSON.parse(await fs.readFile(ledgerPath, 'utf8')); } catch (error) { if (error.code !== 'ENOENT') throw error; }
    check(); page = await context.newPage();
    signal?.addEventListener('abort', abort, { once: true });
    timer = setTimeout(abort, timeoutMS);
    async function command(name) {
      check();
      const bridge = page.locator('#' + bridgeID);
      await bridge.waitFor({ state: 'attached', timeout: Math.min(30000, deadline - Date.now()) });
      await bridge.evaluate((node, operation) => {
        node.dataset.command = operation;
        node.dataset.state = 'working';
        node.textContent = '';
        node.dispatchEvent(new Event('sparkclaw-ai-export-command'));
      }, name);
      await page.waitForFunction(id => ['ready', 'failed'].includes(document.getElementById(id)?.dataset.state), bridgeID, { polling: 250, timeout: deadline - Date.now() });
      if (await bridge.getAttribute('data-state') !== 'ready') throw new Error(await bridge.innerText());
      return bridge.textContent();
    }
    timeline = await discoverBatchHistory(provider, 'https://' + config.host + config.history, async url => {
      await page.goto(url, { timeout: Math.min(30000, deadline - Date.now()) });
      return JSON.parse(await command('timeline.scan'));
    }, check);
    const verify = async record => {
      try {
        if (!/^[\w.-]+\.json$/.test(record.path)) return false;
        const target = path.join(directory, record.path), stat = await fs.lstat(target);
        return stat.isFile() && !stat.isSymbolicLink() && hash(await fs.readFile(target)) === record.sha256;
      } catch { return false; }
    };
    result = await runTimelineBatch({ provider, timeline, ledger, check, verify,
      capture: async item => {
        await page.goto(item.url, { timeout: Math.min(30000, deadline - Date.now()) });
        const text = await command('conversation.capture');
        if (new URL(page.url()).host !== config.host) throw new Error('batch_origin_changed');
        return text;
      },
      save: async (item, text) => {
        const sha256 = hash(text), name = item.id + '-' + sha256 + '.json';
        await write(path.join(directory, name), text);
        return { path: name, sha256, bytes: Buffer.byteLength(text), coverage: 'unknown' };
      },
      commit: async (id, row) => { await write(ledgerPath, JSON.stringify({ ...ledger, [id]: row }, null, 2)); },
      progress: async () => {}
    });
    // UI history exhaustion is not evidence of complete account history.
    const receipt = { status: result.failed.length || timeline.errors.length ? 'partial' : 'saved_visible_history', provider, timeline, ...result, directory, manifest_path: manifestPath, coverage: 'unknown' };
    await write(manifestPath, JSON.stringify(receipt, null, 2));
    return receipt;
  } catch (error) {
    await write(manifestPath, JSON.stringify({ status: 'partial', provider, error: error.message, timeline, result, ledger_path: ledgerPath }, null, 2));
    throw Object.assign(error, { manifest_path: manifestPath });
  } finally {
    clearTimeout(timer); signal?.removeEventListener('abort', abort);
    await page?.close().catch(() => {});
    await lock.close(); await fs.rm(lockPath, { force: true });
  }
}
