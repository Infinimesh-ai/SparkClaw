// Bundled into the three managed userscripts. No credentials or raw provider
// payloads are exposed by the public API; the Gateway owns durable checkpoints.
export function installReader(config) {
  'use strict';
  if (window.top !== window || !config.origins.includes(location.origin)) return;
  window.SparkClawMailReader?.dispose();
  const originalFetch = window.fetch, originalOpen = XMLHttpRequest.prototype.open;
  const originalSend = XMLHttpRequest.prototype.send, originalWindowOpen = window.open, originalSetHeader = XMLHttpRequest.prototype.setRequestHeader;
  const records = new Map(), pageTokens = new Map();
  let listRequest = null, inbox = null, transport = null, folderNode = null, objectURL = null, originalAbort = null;
  let originalState = 'unlearned', responseOrigin = '';
  let active = true, account = '', template = null, binding = null, anchor = null, armed = null, pending = null;
  const idPattern = /^[A-Za-z0-9_+=:.\/~\-]{1,1024}$/;
  const failure = code => { throw Object.assign(new Error(code), {code}); };
  function checkedAccount(expected) {
    const current = config.account();
    if (!current) failure('email_account_identity_unavailable');
    if (account && account !== current.toLowerCase()) {
      records.clear(); pageTokens.clear(); template = null; binding = null; armed = null; pending = null; listRequest = null; inbox = null; transport?.dispose();folderNode?.removeAttribute('data-sparkclaw-mail-folder');
      failure('email_account_identity_mismatch');
    }
    account = current.toLowerCase();
    if (expected && expected.toLowerCase() !== account) failure('email_account_identity_mismatch');
    return account;
  }
  function observe(url, text) {
    if (!active || typeof text !== 'string' || text.length > 10 << 20) return;
    try {
      const u = new URL(url, location.href);
      if (u.origin !== location.origin || !config.listURL(u)) return;
      const value = JSON.parse(text);
      if(config.provider==='outlook' && u.pathname.endsWith('/startupdata.ashx')) {
        const owner=value.owaUserConfig?.SessionSettings?.UserEmailAddress, id=value.findConversation?.Body?.FolderId?.Id;
        if(typeof owner==='string' && typeof id==='string')inbox=config.parseFolders?.(value)||{account:owner.toLowerCase(),id,qualified:false};
      }
      const rows = config.parse(value);
      if (!rows) return;
      checkedAccount();
      for (const row of rows) {
        if (!idPattern.test(row.provider_message_id || '')) continue;
        records.set(row.provider_message_id, row);
      }
      while (records.size > 2000) records.delete(records.keys().next().value);
      if (config.provider === 'qq_mail' && u.searchParams.get('sid')) binding = u;
    } catch { /* Unqualified responses never become admitted source evidence. */ }
  }
  function originalURL(value, exportProof = null) {
    if (!active) return;
    try {
      const u = new URL(value, location.href);
      if (!armed || Date.now() - armed.at > 60000 || u.protocol !== 'https:' || u.username || u.password) return;
      const attachment = config.provider==='outlook' && location.origin==='https://outlook.live.com' && u.origin==='https://attachment.outlook.live.net' &&
        (/^\/owa\/MSA:[^/]+\/service\.svc\/s\/DownloadMessage$/i).test(decodeURIComponent(u.pathname)) &&
        exportProof?.account_address===armed.account && exportProof.provider_message_id===armed.id;
      if(u.origin!==location.origin && !attachment)return;
      checkedAccount(armed.account);
      const row = records.get(armed.id);
      const matches = [...u.searchParams].filter(([,value]) => value === armed.id || row?.native_message_id && value === row.native_message_id);
      if (matches.length === 1) pending = {url:u, key:matches[0][0], native:matches[0][1] !== armed.id, id:armed.id};
    } catch { /* Only an observed, qualified native download can seed a template. */ }
  }
  const open = function(method, url, ...args) {
    this.__sparkclawMailURL = url;
    this.__sparkclawMailRequest = {method, url, headers:{}};
    return originalOpen.call(this, method, url, ...args);
  };
  const setHeader = function(key, value) {
    if (this.__sparkclawMailRequest) this.__sparkclawMailRequest.headers[key.toLowerCase()] = value;
    return originalSetHeader.call(this,key,value);
  };
  const send = function(...args) {
    if (config.provider === 'gmail') {
      try {
        const request = this.__sparkclawMailRequest, url = new URL(request.url, location.href), body = JSON.parse(args[0]);
        if (request.method === 'POST' && url.origin === location.origin && config.listURL(url) && body?.[0]?.[0] === 123 && body[0][1] === 50 && Number.isInteger(body[0][9]) && typeof body[0][3] === 'string') {
          if(listRequest && listRequest.body[0][3]!==body[0][3])pageTokens.clear();
          listRequest = {...request,url,body};
        }
      } catch {}
    }
    const url = this.__sparkclawMailURL;
    this.addEventListener('load', () => {
      if (this.status === 200 && (!this.responseType || this.responseType === 'text')) observe(url, this.responseText);
    }, {once:true});
    return originalSend.apply(this, args);
  };
  const fetch = async function(...args) {
    const response = await originalFetch.apply(this, args);
    try {
      const u = new URL(response.url);
      if (active && response.ok && u.origin === location.origin && config.listURL(u) && Number(response.headers.get('content-length')) <= 10 << 20) {
        void (async () => {
          const copy = response.clone(), reader = copy.body.getReader(), chunks = []; let size = 0;
          try {
            for (;;) { const {done,value} = await reader.read(); if (done) break;
              size += value.length; if (size > 10 << 20) { void reader.cancel(); return; } chunks.push(value); }
            const bytes = new Uint8Array(size); let offset = 0;
            for (const chunk of chunks) { bytes.set(chunk, offset); offset += chunk.length; }
            observe(response.url, new TextDecoder().decode(bytes));
          } finally { reader.releaseLock(); }
        })().catch(()=>{});
      }
    } catch {}
    return response;
  };
  const opened = function(url, ...args) { originalURL(url); return originalWindowOpen.call(this, url, ...args); };
  const clicked = event => { if(event.target===anchor){event.stopImmediatePropagation();return;} const a = event.target.closest?.('a[href]'); if (a) originalURL(a.href); };
  const mutations = new MutationObserver(changes => {
    for (const change of changes) {
      for (const node of change.addedNodes) if (node.tagName === 'IFRAME') originalURL(node.src);
      if (change.target.tagName === 'IFRAME') originalURL(change.target.src);
    }
  });
  XMLHttpRequest.prototype.open = open; XMLHttpRequest.prototype.send = send; XMLHttpRequest.prototype.setRequestHeader = setHeader;
  window.fetch = fetch; window.open = opened;
  document.addEventListener('click', clicked, true);
  mutations.observe(document, {childList:true,subtree:true,attributes:true,attributeFilter:['src']});
  function snapshot({account_address, interval_start, interval_end} = {}) {
    checkedAccount(account_address);
    const start = Date.parse(interval_start), end = Date.parse(interval_end);
    if (!Number.isFinite(start) || !Number.isFinite(end) || start >= end) failure('invalid_request');
    let unsupported = 0;
    const rows = [];
    for (const row of records.values()) {
      if (row.draft || row.sent) continue;
      if (row.grouped) { unsupported++; continue; }
      const received = Date.parse(row.received_at);
      if (!Number.isFinite(received)) { unsupported++; continue; }
      if (received >= start && received < end) rows.push({...row});
    }
    return {provider:config.provider, account_address:account, rows, unsupported_rows:unsupported,
      scan_complete:false, reason:'folder_scope_and_pagination_unqualified'};
  }
  function prepareOriginal({account_address, provider_message_id} = {}) {
    checkedAccount(account_address);
    if (!idPattern.test(provider_message_id || '')) failure('invalid_request');
    const row = records.get(provider_message_id);
    if (!row || row.draft || row.grouped) failure('email_network_target_unobserved');
    let url = config.download?.({id:provider_message_id, row, binding});
    if (!url && template && (!template.native || row.native_message_id)) {
      url = new URL(template.url.href);
      url.searchParams.set(template.key, template.native ? row.native_message_id : provider_message_id);
    }
    if (!url) failure('email_network_original_unqualified');
    if(originalAbort)failure('email_network_original_unqualified');
    originalState='fetching';originalAbort=new AbortController();
    return (async()=>{
      try {
        const response=await originalFetch.call(window,url.href,{credentials:url.origin===location.origin?'same-origin':'omit',redirect:config.provider==='gmail'?'follow':'error',cache:'no-store',signal:AbortSignal.any([originalAbort.signal,AbortSignal.timeout(20000)])});
        originalState='http_'+response.status;responseOrigin=response.url?new URL(response.url).origin:'';if(response.url&&responseOrigin!==url.origin&&!(config.provider==='gmail'&&responseOrigin==='https://mail-attachment.googleusercontent.com'))failure('email_network_original_unqualified');if(!response.ok)failure('email_network_original_unqualified');
        const reader=response.body.getReader(),chunks=[];let length=0;
        try {
          for(;;){const {done,value}=await reader.read();if(done)break;length+=value.length;if(length>110<<20){void reader.cancel();failure('email_capture_limit');}chunks.push(value);}
        }finally{reader.releaseLock();}
        checkedAccount(account_address);
        if(!active)failure('email_network_original_unqualified');
        const bytes=new Uint8Array(length);let offset=0;for(const chunk of chunks){bytes.set(chunk,offset);offset+=chunk.length;}
        const header=new TextDecoder().decode(bytes.subarray(0,Math.min(bytes.length,65536)));
        if(!/^From:/im.test(header)||!/\r?\n\r?\n/.test(header))failure('email_network_original_unqualified');
        anchor?.remove();if(objectURL)URL.revokeObjectURL(objectURL);
        objectURL=URL.createObjectURL(new Blob([bytes],{type:'message/rfc822'}));
        anchor=document.createElement('a');anchor.id='sparkclaw-mail-original';anchor.href=objectURL;
        anchor.download='message.eml';anchor.textContent='SparkClaw EML';anchor.hidden=true;document.body.append(anchor);
        originalState='ready';return {selector:'#sparkclaw-mail-original',account_address:account,provider_message_id};
      }catch(error){
        if(['email_account_identity_mismatch','email_account_identity_unavailable','email_capture_limit'].includes(error.code))throw error;
        if(originalState==='fetching')originalState='fetch_failed';template=null;failure('email_network_original_unqualified');
      }finally{originalAbort=null;}
    })();
  }

  async function listPage({account_address,interval_start,interval_end,page=0}) {
    checkedAccount(account_address);
    if(config.provider==='outlook' && transport?.listPage)return transport.listPage({account_address,interval_start,interval_end,page});
    if (config.provider !== 'gmail' || !listRequest) failure('email_network_list_unqualified');
    const start=Date.parse(interval_start),end=Date.parse(interval_end);
    if (!Number.isFinite(start)||!Number.isFinite(end)||start>=end||!Number.isInteger(page)||page<0||page>10000) failure('invalid_request');
    const query=`-in:trash -in:spam -in:drafts after:${Math.floor(start/1000)-1} before:${Math.ceil(end/1000)}`;
    if (listRequest.body[0][3] !== query || listRequest.url.origin !== location.origin) failure('email_network_list_unqualified');
    const body=structuredClone(listRequest.body);
    if(page>0 && !pageTokens.has(page))failure('email_network_list_unqualified');
    if(!Array.isArray(body[0][15]))failure('email_network_list_unqualified');
    body[0][15][13]=page===0?null:pageTokens.get(page);
    body[0][9]=page; body[0][7]=2000; body[2][0]=0; // Observed native pagination and full-response request.
    const response=await originalFetch.call(window,listRequest.url.href,{method:'POST',credentials:'same-origin',redirect:'error',headers:listRequest.headers,body:JSON.stringify(body),signal:AbortSignal.timeout(20000)});
    if ([401,403].includes(response.status)) failure('email_login_required');
    if (!response.ok) failure('email_network_read_failed');
    const reader=response.body.getReader(),chunks=[];let length=0;
    try {
      for (;;) {const {done,value}=await reader.read();if(done)break;length+=value.length;if(length>10<<20){void reader.cancel();failure('email_capture_limit');}chunks.push(value);}
    } finally {reader.releaseLock();}
    const bytes=new Uint8Array(length);let offset=0;for(const chunk of chunks){bytes.set(chunk,offset);offset+=chunk.length;}
    let value;try{value=JSON.parse(new TextDecoder().decode(bytes));}catch{failure('email_network_list_unqualified');}
    checkedAccount(account_address);
    if (!Array.isArray(value) || value?.[0] !== 0 || !Array.isArray(value?.[19]) || ![0,1].includes(value[3])) failure('email_network_list_unqualified');
    if(value[3]===1) {
      const token=value?.[13]?.[8];
      if(typeof token!=='string'||!token||token.length>8192||token!==value?.[13]?.[9])failure('email_network_list_unqualified');
      pageTokens.set(page+1,token);
    }
    const rows=config.parse(value), received=[];let unsupported=0;
    let rawCount=0;
    for(const batch of value[19]) {
      // Gmail's observed terminal empty batch is a four-field envelope with
      // a null thread list. Require the matching native empty-query UI too;
      // missing data in an ordinary response must never certify an interval.
      if(value[3]===0&&value[19].length===1&&Array.isArray(batch)&&batch.length===4&&batch[1]===null&&
        location.hash.startsWith('#search/')&&decodeURIComponent(location.hash.slice(8)).replace(/\+/gu,' ').trim()===query&&
        ![...document.querySelectorAll('tr.zA')].some(node=>node.getClientRects().length)&&
        [...document.querySelectorAll('.TC, .ae4')].some(node=>node.getClientRects().length&&/No messages matched your search\.|No conversations found|没有找到|未找到/u.test(node.textContent||'')))continue;
      if(!Array.isArray(batch?.[1]))failure('email_network_list_unqualified');
      for(const container of batch[1]) {
        if(!Array.isArray(container?.[0]?.[4]) || !container[0][4].length)failure('email_network_list_unqualified');
        rawCount+=container[0][4].filter(message=>{const labels=message?.[10];return !(Array.isArray(labels)&&labels.every(label=>typeof label==='string')&&(labels.includes('^r')||labels.includes('^f')));}).length;
      }
    }
    unsupported+=Math.max(0,rawCount-rows.length);
    for(const row of rows) {
      records.set(row.provider_message_id,row);
      if(row.draft||row.sent)continue;
      const at=Date.parse(row.received_at);
      if(!Number.isFinite(at)){unsupported++;continue;}
      if(at>=start&&at<end)received.push({...row});
    }
    while(records.size>2000)records.delete(records.keys().next().value);
    return {provider:config.provider,account_address:account,rows:received,unsupported_rows:unsupported,has_next:value[3]===1,page};
  }
  function dispose() {
    active = false;originalAbort?.abort();if(objectURL)URL.revokeObjectURL(objectURL);objectURL=null; records.clear(); pageTokens.clear(); template = null; binding = null; armed = null; pending = null; listRequest = null; inbox = null; transport?.dispose();folderNode?.removeAttribute('data-sparkclaw-mail-folder'); anchor?.remove();
    mutations.disconnect(); document.removeEventListener('click', clicked, true);
    if (XMLHttpRequest.prototype.open === open) XMLHttpRequest.prototype.open = originalOpen;
    if (XMLHttpRequest.prototype.send === send) XMLHttpRequest.prototype.send = originalSend;
    if (XMLHttpRequest.prototype.setRequestHeader === setHeader) XMLHttpRequest.prototype.setRequestHeader = originalSetHeader;
    if (window.fetch === fetch) window.fetch = originalFetch;
    if (window.open === opened) window.open = originalWindowOpen;
    delete window.SparkClawMailReader;
  }
  transport=config.installTransport?.({account:checkedAccount,getInbox:()=>inbox,originalURL,receiveRows(rows){
    checkedAccount();for(const row of rows)if(idPattern.test(row.provider_message_id||''))records.set(row.provider_message_id,row);
    while(records.size>2000)records.delete(records.keys().next().value);
  }});
  Object.defineProperty(window, 'SparkClawMailReader', {configurable:true, value:Object.freeze({
    version:'0.1.0', provider:config.provider, diagnostics:()=>({original:{template:Boolean(template),armed:Boolean(armed),pending:Boolean(pending),state:originalState,responseOrigin},inbox:Boolean(inbox),records:records.size,transport:transport?.diagnostics?.()}), snapshot, listPage, prepareOriginal, dispose, observeNativeOriginalURL:originalURL,
    folderPage({account_address,folder}) {
      checkedAccount(account_address);
      const index=inbox?.folders?.findIndex(item=>folder==='inbox'?item.inbox:folder==='outlook:'+item.id);
      if(!Number.isInteger(index)||index<0)failure('email_network_list_unqualified');
      return index*4096;
    },
    prepareFolder({account_address,folder}) {
      checkedAccount(account_address);
      const item=inbox?.folders?.find(item=>folder==='inbox'?item.inbox:folder==='outlook:'+item.id);
      if(!item||inbox.folders.filter(f=>f.name===item.name).length!==1)failure('email_network_list_unqualified');
      const nodes=[...document.querySelectorAll('[role="treeitem"][data-folder-name]')].filter(node=>node.getAttribute('data-folder-name')===item.name&&node.getClientRects().length);
      if(!nodes.length)failure('email_network_list_unqualified');
      folderNode?.removeAttribute('data-sparkclaw-mail-folder');folderNode=nodes[0];folderNode.setAttribute('data-sparkclaw-mail-folder','selected');
      return {selector:'[data-sparkclaw-mail-folder="selected"]',account_address:account};
    },
    verifyTarget({account_address,provider_message_id,provider_selection_id,folder}) {
      checkedAccount(account_address);
      const row=records.get(provider_message_id);
      return Boolean(row && !row.draft && !row.sent && row.provider_selection_id===provider_selection_id && (!folder||folder==='all'||row.folder===folder) && Number.isFinite(Date.parse(row.received_at)));
    },
    armOriginal({account_address, provider_message_id}) {
      checkedAccount(account_address);
      if (!idPattern.test(provider_message_id || '')) failure('invalid_request');
      armed = {account, id:provider_message_id, at:Date.now()}; pending = null;
    },
    // Only the trusted Controller calls this after it has downloaded and parsed
    // the native EML, checked identity, and retained the immutable original.
    confirmOriginal({account_address, provider_message_id}) {
      checkedAccount(account_address);
      if (armed?.id === provider_message_id && pending?.id === provider_message_id) {template = pending;originalState='learned';}
      armed = null; pending = null;
    },
  })});
}
