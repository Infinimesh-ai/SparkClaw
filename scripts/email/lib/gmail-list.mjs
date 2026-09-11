import crypto from 'node:crypto';

// Observed Gmail /sync/u/<account>/i/bv response contract. Unknown shapes
// supply no evidence. Only message identity/read labels leave the page.
export function parseGmailList(value, includeThreads = false) {
  const batches = value?.[19];
  if (!Array.isArray(batches)) return [];
  const result = [];
  for (const batch of batches) {
    if (!Array.isArray(batch?.[1])) continue;
    for (const container of batch[1]) {
      const thread = container?.[0];
      if (!Array.isArray(thread) || !/^(?:thread-f:\d+|thread-a:r-?\d+)$/u.test(thread[3]) || !Array.isArray(thread[4])) continue;
      const messages = thread[4];
      if ((!includeThreads && messages.length !== 1) || messages.length < 1 || messages.length > 1000) continue;
      const members=[];
      for (const message of messages) {
      const id = message?.[55], labels = message?.[10];
      if (!/^(?:msg-f:\d+|msg-a:r-?\d+)$/u.test(message?.[0]) || !/^[a-f0-9]{1,32}$/u.test(id) ||
          !Array.isArray(labels) || !labels.every(label => typeof label === 'string')) continue;
      if (thread[3].startsWith('thread-f:')) {
        if (!message[0].startsWith('msg-f:') || BigInt(`0x${id}`).toString() !== message[0].slice(6) || !includeThreads && thread[3].slice(9) !== message[0].slice(6)) continue;
      } else if (!message[0].startsWith('msg-a:')) continue;
      members.push({ provider_message_id: id, provider_thread_id: thread[3],
        unread: labels.includes('^u'), inbox: labels.includes('^i'), draft: labels.includes('^r'),
        ...(includeThreads ? {sent:labels.includes('^f'),observed_message_count:messages.length,
          // Observed internal receipt timestamp: the pinned original's Received
          // trace agrees at the second, and RFC Date is independently earlier.
          ...(Number.isSafeInteger(message[6]) && message[6]>=946684800000 && message[6]<=Date.now()+300000 ? {received_at:new Date(message[6]).toISOString()} : {})} : {}) });
      }
      if(members.length===messages.length && new Set(members.map(member=>member.provider_message_id)).size===members.length) result.push(...members);
    }
  }
  return result;
}

// Received-only network scans must not discard inbound siblings merely because
// a sent reply in the same native thread uses Gmail's alternate msg-a identity.
// Each admitted inbound member still passes the original identity/receipt parser.
export function parseGmailReceivedList(value) {
  const rows=[];
  for(const batch of value?.[19]??[])for(const container of batch?.[1]??[]) {
    const thread=container?.[0];
    if(!Array.isArray(thread?.[4]))continue;
    for(const message of thread[4]) {
      const labels=message?.[10];
      if(Array.isArray(labels)&&labels.every(label=>typeof label==='string')&&(labels.includes('^r')||labels.includes('^f')))continue;
      const individual=[...thread];individual[4]=[message];
      const source=[];source[19]=[[null,[[individual]]]];
      for(const row of parseGmailList(source,true))rows.push({...row,native_message_id:message[0]});
    }
  }
  return rows;
}

// Observe the UI's own request; this does not replay private provider APIs or
// transport original email bytes. Always restore the task document's XHR hook.
export async function gmailUnreadEvidence(tab, selectUnread, options = {}) {
  const key = `__sparkclaw_mail_list_${crypto.randomUUID().replaceAll('-', '')}`;
  if (typeof tab.navigate !== "function") throw Object.assign(new Error("browser_runtime_unavailable"), { code: "browser_runtime_unavailable" });
  try {
    await tab.runReadCode(`async page => {
    const install=()=>{
    if(window.top!==window || location.origin!=='https://mail.google.com')return;
    const key=${JSON.stringify(key)}, parse=value=>(${parseGmailList.toString()})(value,true);
    const original=XMLHttpRequest.prototype.open;
    const state={records:[],received:false,active:true};
    const hooked=function(method,value,...args) {
      const url=new URL(value,location.href);
      if(url.origin===location.origin && /^\\/sync\\/u\\/\\d+\\/i\\/bv$/u.test(url.pathname)) this.addEventListener('load',()=>{
        if(!state.active || this.status!==200 || this.responseType && this.responseType!=='text') return;
        try {
          if(this.responseText.length>(2<<20)) return;
          state.records=parse(JSON.parse(this.responseText));state.received=true;
        } catch {}
      },{once:true});
      return original.call(this,method,value,...args);
    };
    state.restore=()=>{state.active=false;if(XMLHttpRequest.prototype.open===hooked)XMLHttpRequest.prototype.open=original;};
    globalThis[key]=state;XMLHttpRequest.prototype.open=hooked;
    };
    await page.addInitScript(install);return true;
    }`);
    await tab.navigate("https://mail.google.com/mail/u/0/#inbox");
    if (typeof options.beforeSelect === "function") await options.beforeSelect();
    const listed = await selectUnread();
    if (listed.empty) return listed;
    const evidence = await tab.runReadCode(`async page => page.evaluate(async () => {
      const state=globalThis[${JSON.stringify(key)}], deadline=Date.now()+5000;
      while(!state.received && Date.now()<deadline)await new Promise(resolve=>setTimeout(resolve,100));
      return state.records;
    })`);
    return { ...listed, rows: listed.rows.map(row => {
      const members=evidence.filter(item=>item.provider_thread_id===row.provider_thread_id);
      const proven=Boolean(row.single_message_row && evidence.filter(item =>
        item.provider_message_id === row.provider_message_id && item.provider_thread_id === row.provider_thread_id &&
        item.unread===row.unread && (options.folder==='sent'?item.sent:options.folder==='all'?true:item.inbox) && !item.draft).length === 1);
      return {...row,single_message_proven:proven,single_unread_proven:proven&&row.unread,members,
        inventory_complete:members.length>0 && members.every(member=>member.observed_message_count===members.length)};
    }) };
  } finally {
    await tab.runReadCode(`async page=>page.evaluate(()=>{const key=${JSON.stringify(key)};globalThis[key]?.restore();delete globalThis[key];return true;})`).catch(()=>{});
  }
}
