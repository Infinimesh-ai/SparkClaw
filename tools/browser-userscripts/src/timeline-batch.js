// SparkClaw additions to RevivalStack (MIT).
const SPARKCLAW_BATCH_PROVIDERS = {
  chatgpt: { host: 'chatgpt.com', history: '/', path: /^\/(?:g\/[^/]+\/)?c\/([\w-]+)\/?$/ },
  claude: { host: 'claude.ai', history: '/recents', path: /^\/chat\/([\w-]+)\/?$/ },
  gemini: { host: 'gemini.google.com', history: '/app', path: /^\/(?:u\/\d+\/)?app\/([\w-]+)\/?$/ },
  grok: { host: 'grok.com', history: '/history', path: /^\/c\/([\w-]+)\/?$/ }
};
function batchConversation(provider, href) {
  try {
    const config = SPARKCLAW_BATCH_PROVIDERS[provider], url = new URL(href, 'https://' + config.host);
    const match = config.path.exec(url.pathname);
    if (url.protocol !== 'https:' || url.host !== config.host || url.username || url.password || !match) return null;
    return { id: match[1], url: url.origin + url.pathname.replace(/\/$/, '') };
  } catch { return null; }
}
async function scanBatchTimeline(provider, io) {
  const rows = new Map();
  let stable = 0;
  for (let step = 0; step < 2000; step++) {
    io.check();
    const snapshot = await io.snapshot();
    if (snapshot.blocked) throw new Error('timeline_login_or_loading_blocked');
    let added = 0;
    for (const item of snapshot.items) {
      const ref = batchConversation(provider, item.url);
      if (!ref) continue;
      const updated = Number.isFinite(Date.parse(item.updated)) ? new Date(item.updated).toISOString() : null;
      if (!rows.has(ref.id)) { rows.set(ref.id, { ...ref, title: item.title || '', updated, rank: rows.size }); added++; }
      else if (updated && (!rows.get(ref.id).updated || updated > rows.get(ref.id).updated)) rows.get(ref.id).updated = updated;
    }
    if (snapshot.end && !added && !snapshot.busy) stable++; else stable = 0;
    if (stable >= 5) {
      if (!rows.size && !snapshot.empty) throw new Error('timeline_not_found');
      return { schema: 'sparkclaw.timeline.v1', provider, coverage: 'visible-history', complete: false,
        warning: 'UI exhaustion cannot prove account-wide coverage; archived/project/hidden conversations may be absent.',
        conversations: [...rows.values()].sort((a, b) => a.updated && b.updated ? a.updated.localeCompare(b.updated) || a.id.localeCompare(b.id) : a.updated ? -1 : b.updated ? 1 : b.rank - a.rank) };
    }
    await io.advance();
  }
  throw new Error('timeline_scan_limit');
}
// Reads only rendered UI. No private website API, token, or cookie dependencies.
function batchTimelineSnapshot(provider, doc = document) {
  const roots = [...doc.querySelectorAll('nav, [role="navigation"], aside, side-navigation'), /\/recents|\/history/.test(doc.location.pathname) ? doc.querySelector('main') : null].filter(Boolean).filter((root, index, all) => !all.some((other, i) => i !== index && other.contains(root)));
  const items = [];
  for (const root of roots) {
    for (const node of root.querySelectorAll('a[href], [data-conversation-id]')) {
      const id = node.getAttribute('data-conversation-id');
      const url = node.getAttribute('href') || (provider === 'gemini' && id ? '/app/' + id : '');
      if (!url) continue;
      const row = node.closest('li, [role="listitem"]') || node;
      items.push({ url, title: node.textContent.trim(), updated: row.querySelector('time[datetime]')?.getAttribute('datetime') || row.getAttribute('data-update-time') });
    }
  }
  const scrollers = roots.flatMap(root => [root, ...root.querySelectorAll('*')]).filter(node => node.scrollHeight > node.clientHeight + 4 && node.clientHeight > 0 && /auto|scroll/.test(doc.defaultView.getComputedStyle(node).overflowY));
  const more = roots.flatMap(root => [...root.querySelectorAll('button')]).find(node => /^(load more|show more|加载更多|显示更多)$/i.test(node.textContent.trim()) && !node.disabled);
  return { items, end: !more && scrollers.every(node => node.scrollTop + node.clientHeight >= node.scrollHeight - 4),
    busy: roots.some(root => root.querySelector('[aria-busy="true"], [role="progressbar"]')),
    empty: roots.some(root => /^(no conversations|no chats|暂无对话|暂无聊天)$/i.test(root.textContent.trim())),
    blocked: !!doc.querySelector('input[type="password"]'), scrollers, more };
}
async function runTimelineBatch({ provider, timeline, ledger, capture, save, verify, commit, check, progress = () => {} }) {
  const result = { exported: [], skipped: [], failed: [] };
  if (timeline.provider !== provider || !Array.isArray(timeline.conversations)) throw new Error('timeline_invalid');
  for (const item of timeline.conversations) {
    check();
    const ref = batchConversation(provider, item.url);
    if (!ref || ref.id !== item.id) throw new Error('timeline_identity_invalid');
    const previous = ledger[item.id];
    if (previous && item.updated && previous.updated === item.updated && await verify(previous)) { result.skipped.push(item.id); continue; }
    try {
      const text = await capture(item);
      check();
      const doc = JSON.parse(text);
      if (doc.author !== provider || batchConversation(provider, doc.url)?.id !== item.id || !doc.exporter || !Array.isArray(doc.messages) || !doc.messages.length || doc.messages.some(m => !['user', 'ai'].includes(m.author) || typeof m.content !== 'string')) throw new Error('conversation_export_invalid');
      const contentBytes = new TextEncoder().encode(JSON.stringify({ title: doc.title, messages: doc.messages }));
      const contentHash = [...new Uint8Array(await crypto.subtle.digest('SHA-256', contentBytes))].map(x => x.toString(16).padStart(2, '0')).join('');
      if (previous?.contentHash === contentHash && await verify(previous)) {
        const record = { ...previous, updated: item.updated };
        await commit(item.id, record); ledger[item.id] = record;
        result.skipped.push(item.id); continue;
      }
      const receipt = await save(item, text);
      // File persistence, read-back verification, then checkpoint; failures retry.
      if (!await verify(receipt)) throw new Error('saved_file_verification_failed');
      check();
      const record = { ...receipt, contentHash, updated: item.updated, url: item.url };
      await commit(item.id, record); ledger[item.id] = record;
      result.exported.push(item.id);
    } catch (error) { result.failed.push({ id: item.id, error: error.message }); }
    progress(result);
  }
  return result;
}
function installTimelineBatch(parent, captureCurrent) {
  const provider = Object.keys(SPARKCLAW_BATCH_PROVIDERS).find(p => SPARKCLAW_BATCH_PROVIDERS[p].host === location.hostname);
  if (!provider) return;
  const container = document.createElement('div'); container.id = 'sparkclaw-batch-controls';
  Object.assign(container.style, { display: 'flex', flexWrap: 'wrap', maxWidth: 'min(520px, 90vw)', alignItems: 'center' });
  parent.appendChild(container);
  let canceled = false, busy = false;
  const status = document.createElement('output'); status.id = 'sparkclaw-batch-status'; status.textContent = '批量导出：就绪';
  const output = document.createElement('textarea'); output.id = 'sparkclaw-batch-output'; output.hidden = true;
  const check = () => { if (canceled) throw new Error('batch_canceled'); };
  const hash = async text => [...new Uint8Array(await crypto.subtle.digest('SHA-256', new TextEncoder().encode(text)))].map(x => x.toString(16).padStart(2, '0')).join('');
  const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
  const scan = async () => {
    for (const node of batchTimelineSnapshot(provider).scrollers) node.scrollTop = 0;
    await pause(1000);
    return scanBatchTimeline(provider, { check, snapshot: () => batchTimelineSnapshot(provider), advance: async () => {
    const view = batchTimelineSnapshot(provider); if (view.more) view.more.click();
    for (const node of view.scrollers) node.scrollTop += Math.max(100, node.clientHeight * 0.8);
    await pause(1000);
  } }); };
  const add = (id, title, action) => {
    const button = document.createElement('button'); button.type = 'button'; button.id = id; button.textContent = title; button.style.margin = '4px';
    button.onclick = async () => {
      if (busy) return; busy = true; canceled = false; output.value = ''; status.dataset.state = 'working';
      try { await action(); status.dataset.state = 'ready'; }
      catch (error) { status.dataset.state = 'failed'; status.textContent = error.message; }
      finally { busy = false; }
    }; container.appendChild(button);
  };
  add('sparkclaw-batch-scan', '检索时间线', async () => { output.value = JSON.stringify(await scan()); status.textContent = '时间线已检索（当前可见历史）'; });
  add('sparkclaw-batch-capture', '采集本页全部消息', async () => {
    // Wait for the loaded transcript to stabilize. This is not proof that the
    // platform exposed every historical message; coverage stays unknown.
    let last = '', stable = 0, text;
    for (let n = 0; n < 90; n++) {
      check();
      if (document.querySelector('[data-testid="stop-button"], button[aria-label="Stop streaming"], button[aria-label="Stop response"]')) throw new Error('conversation_generating');
      text = captureCurrent();
      if (!text) { stable = 0; await pause(1000); continue; }
      const data = JSON.parse(text);
      const body = JSON.stringify(data.messages);
      if (data.messages?.length && body === last) stable++; else stable = 0;
      if (stable >= 3) { output.value = text; status.textContent = '采集就绪；正文覆盖未知'; return; }
      last = body;
      for (const node of document.querySelectorAll('main, main *')) if (node.clientHeight > 0 && node.scrollHeight > node.clientHeight + 4 && /auto|scroll/.test(getComputedStyle(node).overflowY)) node.scrollTop = 0;
      await pause(1000);
    }
    throw new Error('conversation_not_ready');
  });
  add('sparkclaw-batch-export', '批量导出到文件夹', async () => {
    // Ask for the directory inside this explicit user gesture. A directory is
    // an account/workspace scope; never share it between platform accounts.
    const accountScope = window.prompt('请输入当前登录账号/工作区标识（仅用于区分本地导出记录；切换账号后请使用不同标识）');
    if (!accountScope?.trim()) throw new Error('account_scope_required');
    const popup = window.open('about:blank', '_blank', 'popup');
    if (!popup) throw new Error('batch_popup_blocked');
    let directory;
    try { directory = await window.showDirectoryPicker({ mode: 'readwrite' }); }
    catch (error) { popup.close(); throw error; }
    const read = async name => (await (await directory.getFileHandle(name)).getFile()).text();
    const write = async (name, text) => { const handle = await directory.getFileHandle(name, { create: true }); const writer = await handle.createWritable(); try { await writer.write(text); await writer.close(); } catch (e) { await writer.abort().catch(() => {}); throw e; } };
    let ledger = {};
    const ledgerName = provider + '-' + (await hash(accountScope)).slice(0, 24) + '-export-ledger.json';
    try {
      await navigator.locks.request('sparkclaw-batch-' + provider, { ifAvailable: true }, async lock => {
        if (!lock) throw new Error('batch_already_running');
        try { ledger = JSON.parse(await read(ledgerName)); } catch (error) { if (error.name !== 'NotFoundError') throw error; }
        const timeline = await scan();
        const result = await runTimelineBatch({ provider, timeline, ledger, check,
          verify: async row => { try { return /^[\w.-]+\.json$/.test(row.path) && await hash(await read(row.path)) === row.sha256; } catch { return false; } },
          save: async (item, text) => { const sha256 = await hash(text), name = provider + '-' + item.id + '-' + sha256 + '.json'; await write(name, text); return { path: name, sha256 }; },
          commit: async (id, row) => { await write(ledgerName, JSON.stringify({ ...ledger, [id]: row }, null, 2)); },
          capture: async item => {
            const previousDocument = popup.document;
            popup.location.href = item.url;
            for (let n = 0; n < 120; n++) {
              check(); if (popup.closed) throw new Error('batch_window_closed');
              let doc; try { doc = popup.document; } catch { throw new Error('login_or_origin_changed'); }
              const button = doc.querySelector('#sparkclaw-batch-capture');
              if (doc !== previousDocument && doc.readyState !== 'loading' && doc.location.href.replace(/\/$/, '') === item.url && button) {
                button.click();
                for (let attempt = 0; attempt < 120; attempt++) {
                  await pause(1000); check();
                  const state = doc.querySelector('#sparkclaw-batch-status');
                  if (state?.dataset.state === 'failed') throw new Error(state.textContent);
                  if (state?.dataset.state === 'ready') return doc.querySelector('#sparkclaw-batch-output').value;
                }
                throw new Error('capture_timeout');
              }
              await pause(1000);
            }
            throw new Error('userscript_not_ready');
          }, progress: result => { status.textContent = `已导出 ${result.exported.length} 条，失败 ${result.failed.length} 条`; }
        });
        await write(provider + '-batch-' + crypto.randomUUID() + '.json', JSON.stringify({ ...result, timeline, coverage: 'unknown' }, null, 2));
        status.textContent = `完成可见历史：导出 ${result.exported.length}，跳过 ${result.skipped.length}，失败 ${result.failed.length}；全部历史覆盖未知`;
      });
    } finally { popup.close(); }
  });
  const stop = document.createElement('button'); stop.textContent = '停止批量'; stop.id = 'sparkclaw-batch-cancel'; stop.onclick = () => { canceled = true; };
  container.append(stop, status, output);
}
