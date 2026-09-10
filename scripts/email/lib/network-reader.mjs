// Calls the actually installed userscript; it never injects replacement source.
// Unknown/absent network capabilities fall back to the qualified native flow.
export async function prepareNetworkOriginal(tab, provider, target) {
  if (typeof tab.runReadCode !== 'function') return null;
  const result=await tab.runReadCode(`async page=>page.evaluate(async ({provider,target})=>{
    const reader=window.SparkClawMailReader;
    if(reader?.provider!==provider||reader.version!=='0.1.0')return null;
    try {return await reader.prepareOriginal(target);}
    catch(error){if(['email_network_original_unqualified','email_network_target_unobserved'].includes(error.code))return null;return {error:error.code||'email_network_read_failed'};}
  },${JSON.stringify({provider,target})})`);
  if(result?.error)throw Object.assign(new Error(result.error),{code:result.error});
  return result;
}

export async function armNetworkOriginal(tab, provider, target) {
  if (typeof tab.runReadCode !== 'function') return;
  const result=await tab.runReadCode(`async page=>page.evaluate(({provider,target})=>{
    const reader=window.SparkClawMailReader;
    try{if(reader?.provider===provider&&reader.version==='0.1.0')reader.armOriginal(target);}catch(error){return {error:error.code||'email_network_read_failed'};}
    return true;
  },${JSON.stringify({provider,target})})`);
  if(result?.error)throw Object.assign(new Error(result.error),{code:result.error});
}

export async function networkListPage(tab, provider, options, nativeListed) {
  if (!['gmail','outlook'].includes(provider) || options?.lane !== 'recent_inbound' || typeof tab.runReadCode !== 'function') return null;
  const {createHash} = await import('node:crypto');
  const scope = createHash('sha256').update(JSON.stringify([provider,options.account_address.toLowerCase(),options.interval_start,options.interval_end])).digest('hex');
  const hash=value=>createHash('sha256').update(JSON.stringify(value)).digest('hex');
  let position={s:scope,p:0,o:0,d:'',h:scope};
  if(options.continuation?.startsWith('n1:')) {
    try {
      const parsed=JSON.parse(Buffer.from(options.continuation.slice(3),'base64url').toString('utf8'));
      if(Object.keys(parsed).sort().join(',')!=='d,h,o,p,s'||!/^([a-f0-9]{64})?$/.test(parsed.d)||!/^([a-f0-9]{64})$/.test(parsed.h)||parsed.s!==scope||!Number.isInteger(parsed.p)||parsed.p<0||parsed.p>(provider==='outlook'?409599:10000)||!Number.isInteger(parsed.o)||parsed.o<0||parsed.o>50000)throw new Error();
      position=parsed;
    } catch {throw Object.assign(new Error('email_cursor_invalid'),{code:'email_cursor_invalid'});}
  }
  const requestPage=async index=>{
    const request={account_address:options.account_address,interval_start:options.interval_start,interval_end:options.interval_end,page:index};
    const value=await tab.runReadCode(`async page=>page.evaluate(async request=>{
      const reader=window.SparkClawMailReader;
      if(reader?.provider!==${JSON.stringify(provider)}||reader.version!=='0.1.0'||!reader.listPage)return null;
      try{return await reader.listPage(request);}catch(error){if(error.code==='email_network_list_unqualified')return null;return {error:error.code||'email_network_read_failed'};}
    },${JSON.stringify(request)})`);
    if(value?.error)throw Object.assign(new Error(value.error),{code:value.error});
    if(value?.has_next && value.next_page!==undefined && (!Number.isInteger(value.next_page)||value.next_page<=index||value.next_page>409599))throw Object.assign(new Error('email_network_list_unqualified'),{code:'email_network_list_unqualified'});
    if(value && provider==='outlook' && !['inbound_received','inbox_loaded'].includes(value.scope))throw Object.assign(new Error('email_network_list_unqualified'),{code:'email_network_list_unqualified'});
    if(value && (value.provider!==provider||value.account_address!==options.account_address.toLowerCase()||value.page!==index||!Array.isArray(value.rows)||typeof value.has_next!=='boolean'||!Number.isInteger(value.unsupported_rows)||value.unsupported_rows<0))throw Object.assign(new Error('email_network_list_unqualified'),{code:'email_network_list_unqualified'});
    return value;
  };
  // Continuations are durable metadata, while Gmail's opaque pagination tokens
  // remain solely in the document. Rebuild that token chain after reopening.
  if(provider==='gmail')for(let index=0;index<position.p;index++)if(!await requestPage(index))return null;
  const page=await requestPage(position.p);
  if(!page)return null;
  // Read state changes during capture must not invalidate a receipt-time page.
  const fingerprint=value=>hash([value.rows.map(row=>[row.provider_message_id,row.provider_thread_id,row.received_at]),value.unsupported_rows,value.has_next,value.next_page,value.folder_scope_id]);
  const digest=fingerprint(page);
  if(position.d && position.d!==digest)position={...position,o:0,d:''};
  const candidate=member=>({account_address:page.account_address,provider_message_id:member.provider_message_id,provider_selection_id:member.provider_thread_id,provider_thread_id:member.provider_thread_id,folder:member.folder??(member.inbox?'inbox':'all')});
  const selected=[];let candidateBytes=0;
  for(const member of page.rows.slice(position.o,position.o+options.limit)){
    const bytes=Buffer.byteLength(JSON.stringify(candidate(member)));
    if(candidateBytes+bytes>40<<10)break;
    candidateBytes+=bytes;selected.push(member);
  }
  const more=position.o+selected.length<page.rows.length;
  let next=more?{...position,o:position.o+selected.length,d:digest}:page.has_next?{...position,p:page.next_page??position.p+1,o:0,d:'',h:hash([position.h,digest])}:null;
  const unsupported=page.unsupported_rows;
  if (unsupported && !more) next={...position,o:0}; // Never step past an unresolved page.
  // Offset pagination can shift when a user moves/deletes mail. Before issuing
  // a boundary certificate, re-observe the scanned prefix and verify its chain.
  let changed=false;
  if(!next && unsupported===0 && position.p>0) {
    let chain=scope;
    let index=0,steps=0;
    while(index<position.p && steps++<10000) {
      const prior=await requestPage(index);
      if(!prior || !prior.has_next){changed=true;break;}
      chain=hash([chain,fingerprint(prior)]);
      index=prior.next_page??index+1;
    }
    changed ||= index!==position.p;
    changed ||= chain!==position.h;
    if(changed)next={s:scope,p:0,o:0,d:'',h:scope};
  }
  const incompleteScope=provider==='outlook' && page.scope==='inbox_loaded';
  if(!next && incompleteScope)next={s:scope,p:0,o:0,d:'',h:scope};
  const complete=!next&&unsupported===0;
  const groups=new Map();
  for(const member of page.rows){const group=groups.get(member.provider_thread_id)||[];group.push(member);groups.set(member.provider_thread_id,group);}
  const rows=[...groups].map(([thread,members])=>({provider_thread_id:thread,provider_selection_id:provider==='outlook'?thread:members.at(-1).provider_message_id,provider_message_id:members.at(-1).provider_message_id,
    members:members.map(member=>({...member,local:true})),inventory_complete:false,network_member_proven:provider==='outlook',unread:members.some(m=>m.unread)}));
  const candidates=selected.map(candidate);
  return {listed:{...nativeListed,account_address:page.account_address,rows,network_reader:true},discovery:{schema_version:1,provider,status:complete?(candidates.length?'listed':'empty'):'partial',account_address:page.account_address,candidates,threads:[],
    coverage:{scope:'inbound_received',lane:'recent_inbound',scan_complete:complete,boundary_qualified:complete,scanned_rows:page.rows.length+unsupported,unsupported_rows:unsupported,limited:!complete,
      ...(next?{continuation:'n1:'+Buffer.from(JSON.stringify(next)).toString('base64url')}:{}),...(complete?{}:{reason:unsupported?'network_rows_unqualified':changed?'network_page_changed':incompleteScope?'folder_scope_and_pagination_unqualified':'network_page_continues'})},observed_at:new Date().toISOString()}};
}

export async function warmOutlookNetwork(tab, account_address) {
  if(typeof tab.runReadCode!=='function')return;
  const needed=await tab.runReadCode(`async page=>page.evaluate(account=>{
    const reader=window.SparkClawMailReader;
    if(reader?.provider!=='outlook'||!reader.diagnostics)return false;
    try {reader.verifyTarget({account_address:account,provider_message_id:'check',provider_selection_id:'check'});}
    catch(error){return {error:error.code||'email_network_read_failed'};}
    const d=reader.diagnostics();return d.inbox===true&&d.transport?.itemTemplate===false;
  },${JSON.stringify(account_address)})`);
  if(needed?.error)throw Object.assign(new Error(needed.error),{code:needed.error});
  if(needed!==true)return;
  // The site's observed Drafts view seeds its ItemRows operation. Only folder
  // metadata is opened; no draft or message body is selected or modified.
  await tab.runReadCode(`async page=>{
    const drafts=page.locator('[role="treeitem"][data-folder-name="草稿"]:visible, [role="treeitem"][data-folder-name="Drafts"]:visible');
    const inbox=page.locator('[role="treeitem"][data-folder-name="收件箱"]:visible, [role="treeitem"][data-folder-name="Inbox"]:visible');
    if(!await drafts.count()||!await inbox.count())return false;
    try {
      await drafts.first().evaluate(node=>node.click());
      await page.waitForFunction(()=>window.SparkClawMailReader?.diagnostics?.().transport?.itemTemplate===true,undefined,{timeout:5000}).catch(()=>{});
    } finally {await inbox.first().evaluate(node=>node.click());}
    return true;
  }`);
}

export async function prepareNetworkFolder(tab,target) {
  const result=await tab.runReadCode(`async page=>page.evaluate(target=>{
    const reader=window.SparkClawMailReader;if(reader?.provider!=='outlook')return null;
    try{return reader.prepareFolder(target);}catch(error){return {error:error.code||'email_network_read_failed'};}
  },${JSON.stringify(target)})`);
  if(result?.error)throw Object.assign(new Error(result.error),{code:result.error});
  if(result?.selector)await tab.click(result.selector);
}

export async function recoverOutlookTarget(tab,options,listed) {
  if(!options.account_address||typeof tab.runReadCode!=='function')return null;
  await warmOutlookNetwork(tab,options.account_address);
  const target={account_address:options.account_address,folder:options.folder??'inbox'};
  const first=await tab.runReadCode(`async page=>page.evaluate(target=>{try{return window.SparkClawMailReader?.folderPage(target)??null;}catch{return null;}},${JSON.stringify(target)})`);
  if(!Number.isInteger(first))return null;
  let page=first;
  for(let attempt=0;attempt<256;attempt++) {
    const request={...target,interval_start:'1900-01-01T00:00:00Z',interval_end:new Date().toISOString(),page};
    const value=await tab.runReadCode(`async page=>page.evaluate(async request=>{try{return await window.SparkClawMailReader.listPage(request);}catch(error){return {error:error.code||'email_network_read_failed'};}},${JSON.stringify(request)})`);
    if(value?.error)throw Object.assign(new Error(value.error),{code:value.error});
    const member=value?.rows?.find(row=>row.provider_message_id===options.pinned_message_id && row.provider_selection_id===options.pinned_selection_id);
    if(member)return {...listed,account_address:value.account_address,network_reader:true,rows:[{...member,members:[{...member,local:true}],network_member_proven:true,individual_message_proven:true,inventory_complete:false}]};
    if(!value?.has_next||Math.floor(value.next_page/4096)!==Math.floor(first/4096))break;
    page=value.next_page;
  }
  return null;
}
