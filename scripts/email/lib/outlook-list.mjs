import crypto from 'node:crypto';
import {installOutlookEarlyBridge} from '../userscripts/lib/outlook-early-bridge.mjs';

// A conversation is usable only when the provider explicitly reports exactly
// one global item. A route/conversation ID is never promoted to a message ID.
export function parseOutlookList(value, includeInventory = false) {
  const rows=[];
  let visited=0;
  const visit=(node,depth)=>{
    if(!node || typeof node!=='object' || depth>20 || ++visited>20000) return;
    if(Array.isArray(node.Conversations)) for(const item of node.Conversations.slice(0,100)) {
      const selection=item?.ConversationId?.Id, id=item?.ItemIds?.[0]?.Id;
      if(typeof selection!=='string' || !selection)continue;
      let inventory = includeInventory ? {last_delivery_time:typeof item.LastDeliveryTime==='string' && Number.isFinite(Date.parse(item.LastDeliveryTime)) ? item.LastDeliveryTime : null} : {};
      if (includeInventory && Number.isInteger(item.GlobalMessageCount) && item.GlobalMessageCount >= 1 && item.GlobalMessageCount <= 1000 &&
          Array.isArray(item.GlobalItemIds) && item.GlobalItemIds.length === item.GlobalMessageCount && Array.isArray(item.ItemIds) &&
          item.ItemIds.length === item.MessageCount && Array.isArray(item.DraftItemIds)) {
        const globalIDs = item.GlobalItemIds.map(value=>value?.Id), localIDs = item.ItemIds.map(value=>value?.Id), drafts = item.DraftItemIds.map(value=>value?.Id);
        if (globalIDs.every(value=>typeof value==='string' && /^[A-Za-z0-9_+=:.\/~\-]{1,1024}$/u.test(value)) && new Set(globalIDs).size === globalIDs.length &&
            localIDs.every(value=>globalIDs.includes(value)) && drafts.every(value=>globalIDs.includes(value)) && new Set(drafts).size === drafts.length) {
          inventory = {members:globalIDs.map(value=>({provider_message_id:value,local:localIDs.includes(value),draft:drafts.includes(value)})),
            inventory_complete:true,global_unread_count:item.GlobalUnreadCount,
            last_delivery_time: typeof item.LastDeliveryTime==='string' && Number.isFinite(Date.parse(item.LastDeliveryTime)) ? item.LastDeliveryTime : null};
        }
      }
      if(typeof id!=='string' || !id || selection===id ||
          item.MessageCount!==1 || item.GlobalMessageCount!==1 || item.ItemIds?.length!==1 ||
          item.GlobalItemIds?.length!==1 || item.GlobalItemIds[0]?.Id!==id ||
          ![0,1].includes(item.UnreadCount) || item.GlobalUnreadCount!==item.UnreadCount) {
        rows.push({provider_selection_id:selection,provider_message_id:null,unread:null,...inventory});continue;
      }
      rows.push({provider_selection_id:selection,provider_message_id:id,unread:item.UnreadCount===1,...inventory});
    }
    for(const child of Object.values(node))if(child&&typeof child==='object')visit(child,depth+1);
  };
  visit(value,0);return rows;
}

export async function prepareOutlookList(tab) {
  const key=`__sparkclaw_mail_list_${crypto.randomUUID().replaceAll('-','')}`;
  // Startup data can arrive before the first DOM check; install only on the
  // owned task page and reload that page to observe it from the beginning.
  await tab.runReadCode(`async page=>{
    await page.addInitScript(()=>{
      (${installOutlookEarlyBridge.toString()})();
      const key=${JSON.stringify(key)},parse=value=>(${parseOutlookList.toString()})(value,true);
      const state={records:[],received:false};globalThis[key]=state;
      const allowed=value=>{try{const url=new URL(value,location.href);return url.origin===location.origin&&/^\\/owa\\/\\d+\\/(?:startupdata\\.ashx|service\\.svc)$/u.test(url.pathname);}catch{return false;}};
      const observe=(value,text)=>{
        try {
          const url=new URL(value,location.href);
          if(url.origin!==location.origin || !/^\\/owa\\/\\d+\\/(?:startupdata\\.ashx|service\\.svc)$/u.test(url.pathname) || text.length>(10<<20))return;
          const records=parse(JSON.parse(text));
          if(records.length){const rows=new Map(state.records.map(row=>[row.provider_selection_id,row]));for(const row of records)rows.set(row.provider_selection_id,row);state.records=[...rows.values()].slice(-100);state.received=true;}
        }catch{}
      };
      const open=XMLHttpRequest.prototype.open;
      XMLHttpRequest.prototype.open=function(method,url,...args){
        if(allowed(url))this.addEventListener('load',()=>{if(this.status===200&&(!this.responseType||this.responseType==='text'))observe(url,this.responseText);},{once:true});
        return open.call(this,method,url,...args);
      };
      const fetch=globalThis.fetch;
      globalThis.fetch=async function(...args){const response=await fetch.apply(this,args);
        if(response.ok&&allowed(response.url))void response.clone().text().then(text=>observe(response.url,text)).catch(()=>{});
        return response;
      };
    });
    await page.reload();return true;
  }`);
  return key;
}

export async function outlookListEvidence(tab,key,listed) {
  if(listed.empty)return listed;
  const records=await tab.runReadCode(`async page=>page.evaluate(async()=>{
    const state=globalThis[${JSON.stringify(key)}],deadline=Date.now()+5000;
    while(state&&!state.received&&Date.now()<deadline)await new Promise(resolve=>setTimeout(resolve,100));
    return state?.records??[];
  })`);
  return {...listed,rows:listed.rows.map(row=>{
    const matches=records.filter(record=>record.provider_selection_id===row.provider_selection_id);
    if(matches.length!==1)return row;
    if(!matches[0].provider_message_id)return {...row,members:matches[0].members,inventory_complete:matches[0].inventory_complete,
      global_unread_count:matches[0].global_unread_count,last_delivery_time:matches[0].last_delivery_time,evidence_key:key};
    return {...row,provider_message_id:matches[0].provider_message_id,evidence_key:key,
      single_unread_proven:row.unread&&matches[0].unread,single_message_proven:true,
      last_delivery_time:matches[0].last_delivery_time,
      ...(matches[0].members ? {members:matches[0].members,inventory_complete:matches[0].inventory_complete,global_unread_count:matches[0].global_unread_count,last_delivery_time:matches[0].last_delivery_time} : {})};
  })};
}
