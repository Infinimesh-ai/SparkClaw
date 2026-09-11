import crypto from 'node:crypto';

// QQ's own /list/maillist response separates receipt time (totime) from
// sender time (fromtime). Only bounded metadata leaves the owned task page.
export function parseQQMailList(value) {
  if(value?.head?.ret!==0 || !Number.isInteger(value.head.time) || !Array.isArray(value.body?.list) || !Number.isInteger(value.body.total_num) || value.body.total_num<0)return null;
  const rows=[];
  for(const item of value.body.list.slice(0,2000)){
    if(typeof item?.emailid!=='string' || !/^[A-Za-z0-9_+=:.\/~\-]{1,1024}$/u.test(item.emailid) || !Number.isInteger(item.dirid) || item.dirid<1 ||
      !Number.isInteger(item.totime) || item.totime<946684800 || item.totime>value.head.time+300 || item.unread!==undefined && item.unread!==0 && item.unread!==1)continue;
    rows.push({provider_message_id:item.emailid,provider_selection_id:item.emailid,provider_thread_id:item.emailid,
      folder:item.dirid===1?'inbox':item.dirid===3?'sent':`qq:${item.dirid}`,unread:item.unread===1,
      received_at:new Date(item.totime*1000).toISOString()});
  }
  return {rows,total_count:value.body.total_num,unsupported_rows:value.body.list.length-rows.length};
}

export function parseQQMailFolders(value){
  if(value?.head?.ret!==0 || !Array.isArray(value.body?.list?.sys_list) || !Array.isArray(value.body.list.personal_list))return null;
  const system=value.body.list.sys_list,custom=value.body.list.personal_list;
  if(!system.some(folder=>folder.dirid===1))return null;
  // Current ordinary personal folders use type 3. Sent, drafts, trash, junk,
  // shared/system aggregates and unknown future types are never inbound scope.
  const folders=[{id:1,folder:'inbox'}];
  let unsupported=0;
  for(const item of custom){
    if(item?.folder_type!==3 || !Number.isInteger(item.dirid) || item.dirid<1000 || item.dirid>2147483647){unsupported++;continue;}
    if(!folders.some(folder=>folder.id===item.dirid))folders.push({id:item.dirid,folder:`qq:${item.dirid}`});
  }
  return {folders:folders.sort((a,b)=>a.id-b.id).slice(0,100),unsupported:unsupported+Math.max(0,folders.length-100)};
}

const binding=options=>crypto.createHash('sha256').update(JSON.stringify([options.account_address.toLowerCase(),options.lane,options.interval_start,options.interval_end])).digest('hex');
export function qqDiscoveryPosition(options){
  const initial={b:binding(options),f:1,d:4,s:'',o:0};
  if(!options.continuation.startsWith('q1:'))return initial;
  try{
    const value=JSON.parse(Buffer.from(options.continuation.slice(3),'base64url').toString('utf8'));
    if(Object.keys(value).sort().join(',')!=='b,d,f,o,s' || value.b!==initial.b || !Number.isInteger(value.f) || value.f<1 || value.f>2147483647 ||
      !Number.isInteger(value.d) || value.d<4 || value.d>128 || !Number.isInteger(value.o) || value.o<0 || value.o>2000 || !/^(?:[a-f0-9]{64})?$/u.test(value.s))return initial;
    return value;
  }catch{return initial;}
}
export function qqDiscoveryContinuation(position,digest,offset,more,folders){
  if(more)return 'q1:'+Buffer.from(JSON.stringify({...position,s:digest,o:offset})).toString('base64url');
  const index=folders.findIndex(folder=>folder.id===position.f),next=folders[(index+1)%folders.length];
  return 'q1:'+Buffer.from(JSON.stringify({...position,f:next.id,d:index+1>=folders.length?(position.d<128?position.d+4:4):position.d,s:'',o:0})).toString('base64url');
}

export async function prepareQQMailList(tab){
 const key=`__sparkclaw_qq_list_${crypto.randomUUID().replaceAll('-','')}`;
 if(typeof tab.navigate!=='function')throw Object.assign(new Error('browser_runtime_unavailable'),{code:'browser_runtime_unavailable'});
 await tab.runReadCode(`async page=>{
  await page.addInitScript(()=>{
   if(window.top!==window || !['https://wx.mail.qq.com','https://mail.qq.com'].includes(location.origin))return;
   const state={rows:[],total_count:null,unsupported_rows:0,received:false,folders:null};globalThis[${JSON.stringify(key)}]=state;
   const parse=${parseQQMailList.toString()},parseFolders=${parseQQMailFolders.toString()};
   const observe=(url,raw)=>{try{const u=new URL(url,location.href);if(u.origin!==location.origin||!['/list/maillist','/home/home'].includes(u.pathname)||raw.length>(2<<20))return;
    const value=JSON.parse(raw);if(u.pathname==='/home/home'){state.folders=parseFolders(value);return;}
    const data=parse(value);if(!data)return;const rows=new Map(state.rows.map(row=>[row.provider_message_id,row]));for(const row of data.rows)rows.set(row.provider_message_id,row);
    state.rows=[...rows.values()].slice(0,2000);state.total_count=data.total_count;state.unsupported_rows+=data.unsupported_rows;state.received=true;
   }catch{}};
   const open=XMLHttpRequest.prototype.open;
   XMLHttpRequest.prototype.open=function(method,url,...rest){this.addEventListener('load',()=>{if(this.status===200&&(!this.responseType||this.responseType==='text'))observe(url,this.responseText);},{once:true});return open.call(this,method,url,...rest);};
   const fetch=globalThis.fetch;globalThis.fetch=async function(...args){const r=await fetch.apply(this,args);if(r.ok&&new URL(r.url).origin===location.origin&&['/list/maillist','/home/home'].includes(new URL(r.url).pathname))void r.clone().text().then(text=>observe(r.url,text)).catch(()=>{});return r;};
  });return true;
 }`);
 await tab.navigate('https://wx.mail.qq.com/home/index#/list/1');
 return key;
}

export async function qqMailListEvidence(tab,key,listed,options,inspectList){
 const read=()=>tab.runReadCode(`async page=>page.evaluate(async()=>{const state=globalThis[${JSON.stringify(key)}],deadline=Date.now()+3000;
   while(state&&(!state.received||!state.folders)&&Date.now()<deadline)await new Promise(resolve=>setTimeout(resolve,100));
   return state?{rows:state.rows,total_count:state.total_count,unsupported_rows:state.unsupported_rows,folders:state.folders,receipt_evidence:state.received}:null;})`);
 let snapshot=await read();
 if(!snapshot?.receipt_evidence)return {...listed,receipt_evidence:false};
 const folders=snapshot.folders?.folders?.length?snapshot.folders.folders:[{id:1,folder:'inbox'}];
 let position=qqDiscoveryPosition(options);
 if(!folders.some(folder=>folder.id===position.f))position={...position,f:1,s:'',o:0};
 if(position.f!==1){
   await tab.runReadCode(`async page=>page.evaluate(()=>{const state=globalThis[${JSON.stringify(key)}];if(state){state.rows=[];state.received=false;state.total_count=null;state.unsupported_rows=0;}return true;})`);
   await tab.runReadCode(`async page=>{await page.goto(${JSON.stringify(`https://wx.mail.qq.com/home/index#/list/${position.f}`)});return true;}`);
   listed=await inspectList();snapshot=await read();
   if(!snapshot?.receipt_evidence)return {...listed,receipt_evidence:false};
 }
 // Reconstruct a bounded native-scroll prefix after restart, then publish it in
 // durable batches. Each folder rotation deepens the prefix. No provider API
 // replay or Date-header guess is used; reaching the safety cap stays partial.
 await tab.runReadCode(`async page=>page.evaluate(async()=>{const state=globalThis[${JSON.stringify(key)}];if(!state)return false;
   const deadline=Date.now()+20000;
   for(let step=0;step<${position.d}&&Date.now()<deadline&&state.rows.length<Math.min(state.total_count??2000,2000);step++){
     const list=document.querySelector('.mail-list-body .ui-float-scroll-body');if(!list)break;
     const before=list.scrollTop,previous=state.rows.length;list.scrollTop+=list.clientHeight;
     if(list.scrollTop===before)break;
     const waitUntil=Math.min(deadline,Date.now()+700);
     while(state.rows.length===previous&&Date.now()<waitUntil)await new Promise(resolve=>setTimeout(resolve,50));
   }return true;})`);
 snapshot=await read();
 const folder=folders.find(folder=>folder.id===position.f).folder;
 const rows=snapshot.rows.filter(row=>row.folder===folder);
 return {...listed,...snapshot,rows,receipt_evidence:true,empty:snapshot.total_count===0,
   discovery_position:position,discovery_folders:folders,folder_scope:folder,
   folder_inventory_qualified:Boolean(snapshot.folders)&&snapshot.folders.unsupported===0};
}
