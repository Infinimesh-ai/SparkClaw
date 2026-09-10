// ==UserScript==
// @name         SparkClaw QQ 邮件原件直读与测速
// @namespace    sparkclaw.local
// @version      0.2.0
// @description  复用 QQ 当前登录会话直接读取列表与 EML；手动运行，邮件只保留在本页内存。
// @match        https://wx.mail.qq.com/*
// @run-at       document-idle
// @grant        none
// @noframes
// ==/UserScript==

(() => {
  'use strict';
  if (location.origin !== 'https://wx.mail.qq.com' || window.top !== window) return;
  window.SparkClawQQReadTrial?.dispose();
  const MAX_BYTES = 25 * 1024 * 1024;
  const fetchOriginal = window.fetch.bind(window);
  const mailPattern = /^[A-Za-z0-9_+=:.\/~\-]{1,1024}$/;
  let busy = false, disposed = false, abort, panel, output, report = null;
  let originals = [];
  const round = n => Math.round(n * 10) / 10;
  function session() {
    // Use an observed same-origin list request, not a guessed cookie or saved SID.
    const entries = performance.getEntriesByType('resource');
    for (let i = entries.length - 1; i >= 0; i--) {
      const url = new URL(entries[i].name);
      if (url.origin === location.origin && url.pathname === '/list/maillist' && url.searchParams.get('sid')) {
        if (url.searchParams.get('func') !== '1') continue;
        const dir = url.searchParams.get('dirid');
        if (!/^\d+$/.test(dir || '')) continue;
        return {sid: url.searchParams.get('sid'), dir};
      }
    }
    throw new Error('请先登录 QQ 邮箱并打开一个普通邮件文件夹，再运行。');
  }
  async function request(path, params, binding) {
    if (disposed) throw new Error('脚本已停止');
    if (!['/list/maillist', '/read/readmail'].includes(path)) throw new Error('接口不允许');
    const url = new URL(path, location.origin);
    for (const [key, value] of Object.entries(params)) url.searchParams.set(key, String(value));
    url.searchParams.set('sid', binding.sid);
    const response = await fetchOriginal(url.href, {method:'GET', credentials:'same-origin', redirect:'error', cache:'no-store', signal:AbortSignal.any([abort.signal,AbortSignal.timeout(15000)])});
    if (!response.ok || new URL(response.url).origin !== location.origin) throw new Error(`读取失败 HTTP ${response.status}`);
    if (Number(response.headers.get('content-length')) > MAX_BYTES) {await response.body?.cancel();throw new Error('邮件超过 25 MiB 单封限制');}
    const reader = response.body.getReader(); const chunks = []; let length = 0;
    try {
      for (;;) {
        const {done,value} = await reader.read(); if (done) break;
        length += value.length;
        if (length > MAX_BYTES) throw new Error('响应超过 25 MiB 限制');
        chunks.push(value);
      }
    } catch (e) {await reader.cancel().catch(()=>{});throw e;} finally {reader.releaseLock();}
    const result = new Uint8Array(length); let offset = 0;
    for (const chunk of chunks) {result.set(chunk,offset);offset += chunk.length;}
    return result;
  }
  async function list(binding) {
    const start = performance.now();
    // Scope is a single normal folder page. Pinned mails have a separate endpoint.
    const bytes = await request('/list/maillist',{dir:binding.dir,dirid:binding.dir,func:1,sort_type:1,sort_direction:1,page_now:0,page_size:50,enable_topmail:true},binding);
    let json;try {json=JSON.parse(new TextDecoder().decode(bytes));}catch{throw new Error('列表不是有效 JSON，请检查登录状态');}
    if (json?.head?.ret !== 0 || !Array.isArray(json.body?.list)) throw new Error('邮箱未返回成功列表，请重新登录后重试');
    const mails = json.body.list.filter(m=>typeof m.emailid === 'string' && mailPattern.test(m.emailid) && Number.isSafeInteger(m.size) && m.size > 0);
    return {mails,total:json.body.total_num,ms:round(performance.now()-start),bytes:bytes.length};
  }
  async function original(mail,binding) {
    const start=performance.now();
    const bytes=await request('/read/readmail',{func:5,mailid:mail.emailid},binding);
    const header=new TextDecoder().decode(bytes.subarray(0,Math.min(bytes.length,65536)));
    if (!/^From:/im.test(header) || !/^Message-ID:/im.test(header) || !/\r?\n\r?\n/.test(header)) throw new Error('响应不是可核验的 EML 原件');
    const sha=Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256',bytes)),n=>n.toString(16).padStart(2,'0')).join('');
    return {bytes,sha,ms:round(performance.now()-start),listedSize:mail.size,sizeMatches:bytes.length===mail.size};
  }
  async function run({count=3,rounds=1,maxMailBytes=1024*1024}={}) {
    if (busy || disposed) throw new Error('已有测试正在运行或脚本已停止');
    if (!Number.isInteger(count)||count<1||count>10||!Number.isInteger(rounds)||rounds<1||rounds>5||!Number.isSafeInteger(maxMailBytes)||maxMailBytes<1||maxMailBytes>MAX_BYTES) throw new Error('测试范围无效');
    const binding=session();busy=true;abort=new AbortController(); originals=[];report=null;
    const started=performance.now();const samples=[];
    try {
      let selected;
      for(let r=0;r<rounds;r++) {
        const current=session();if(current.sid!==binding.sid || current.dir!==binding.dir)throw new Error('邮箱会话或文件夹已变化，请重新测试');
        status(`正在获取列表，第 ${r+1}/${rounds} 轮…`);
        const listing=await list(binding);
        if(!selected)selected=listing.mails.filter(m=>m.size<=maxMailBytes && !m.emailid.startsWith('C') && !m.emailid.startsWith('@')).slice(0,count);
        if(!selected.length)throw new Error('当前列表没有满足大小限制的普通单封邮件');
        const captured=[];
        for(let i=0;i<selected.length;i++) {
          if(session().sid!==binding.sid)throw new Error('邮箱会话已变化');
          status(`第 ${r+1}/${rounds} 轮，读取原件 ${i+1}/${selected.length}…`);
          const value=await original(selected[i],binding);
          if(r>0 && value.sha!==originals[i].sha)throw new Error('同一邮件原件在重复读取时发生变化');
          if(r===0)originals.push(value);
          captured.push({index:i+1,bytes:value.bytes.length,sha256:value.sha,elapsedMs:value.ms,listedSize:value.listedSize,sizeMatches:value.sizeMatches});
        }
        samples.push({round:r+1,listMs:listing.ms,listBytes:listing.bytes,listCount:listing.mails.length,totalReported:listing.total,originals:captured});
      }
      report={schemaVersion:1,scriptVersion:'0.2.0',transport:'qq_session_http',scope:'current_normal_folder_first_page_excluding_pinned_and_conversation_rows',cache:'no-store',concurrency:1,requestedCount:count,actualCount:originals.length,rounds,totalMs:round(performance.now()-started),samples};
      status(`完成：${originals.length} 封原件，${rounds} 轮，总耗时 ${report.totalMs} ms。`);
      if(output)output.textContent=JSON.stringify(report,null,2);
      return structuredClone(report);
    } catch(e){originals=[];status(e.name==='AbortError'?'测试已停止':String(e.message));throw e;} finally{busy=false;}
  }
  function status(text){panel?.querySelector('[data-status]')?.replaceChildren(document.createTextNode(text));}
  function download(bytes,name,type) {
    const url=URL.createObjectURL(new Blob([bytes],{type}));const a=document.createElement('a');a.href=url;a.download=name;a.click();setTimeout(()=>URL.revokeObjectURL(url),1000);
  }
  function exportReport(){if(!report)throw new Error('请先完成测试');download(JSON.stringify(report,null,2),'qq-speed-report.json','application/json');}
  function exportEML(index=0){if(!originals[index])throw new Error('原件不存在');download(originals[index].bytes,`qq-mail-${index+1}.eml`,'message/rfc822');}
  function dispose(){disposed=true;abort?.abort();originals=[];report=null;panel?.remove();delete window.SparkClawQQReadTrial;}
  function mount(){
    if(disposed||!document.body)return;
    panel=document.createElement('section');panel.style.cssText='position:fixed;right:16px;bottom:16px;z-index:2147483647;width:360px;max-height:75vh;overflow:auto;background:white;color:#17212c;border:1px solid #bcc6cf;border-radius:10px;padding:14px;font:13px sans-serif;box-shadow:0 4px 20px #0002';
    const title=document.createElement('strong');title.textContent='SparkClaw QQ 原件直读';panel.append(title);
    const state=document.createElement('p');state.dataset.status='';state.textContent='手动获取当前文件夹第一页的前三封小于 1 MiB 的普通邮件。原件仅保存在本页内存。';panel.append(state);
    const buttons=[['获取 3 封原件',()=>run()],['测速 3 轮',()=>run({rounds:3})],['下载测速报告',exportReport],['下载第 1 封 EML',()=>exportEML(0)],['下载第 2 封 EML',()=>exportEML(1)],['下载第 3 封 EML',()=>exportEML(2)],['停止并清除',dispose]];
    for(const [label,handler]of buttons){const button=document.createElement('button');button.textContent=label;button.style.cssText='margin:3px;padding:5px';button.onclick=()=>Promise.resolve().then(handler).catch(e=>status(e.name==='AbortError'?'测试已停止':e.message));panel.append(button);}
    output=document.createElement('pre');output.style.cssText='white-space:pre-wrap;font-size:11px;max-height:220px;overflow:auto';panel.append(output);document.body.append(panel);
  }
  Object.defineProperty(window,'SparkClawQQReadTrial',{configurable:true,value:Object.freeze({version:'0.2.0',run,exportReport,exportEML,dispose})});
  if(document.readyState==='loading')document.addEventListener('DOMContentLoaded',mount,{once:true});else mount();
})();
