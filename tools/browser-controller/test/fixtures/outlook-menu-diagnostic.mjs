// Fixed qualification code; never accepts a selector, expression, message or file.
// The caller owns a new task page and holds the production exclusive reservation.
export function menuFacts() {
  const s=globalThis.__sparkclawManagedMail,a=s?.attachmentMenuAction,m=s?.attachmentMenu;
  const r=a?.getBoundingClientRect(),hit=r&&document.elementFromPoint(r.x+r.width/2,r.y+r.height/2);
  const empty=document.querySelectorAll('[contenteditable="true"][aria-label="Message body"],[contenteditable="true"][aria-label="Email body"],[contenteditable="true"][aria-label="邮件正文"],[contenteditable="true"][aria-label="消息正文"]').length===1&&!!s?.root?.isConnected&&s.root.contains(s.body)&&s.root.contains(s.send)&&
    !s.body.innerText.trim()&&[...s.root.querySelectorAll('input:not([type="file"]),textarea')].every(n=>!n.value.trim())&&
    !s.root.querySelector('[draggable="true"],[data-attachment-id]');
  return {empty,context:s?.attachmentContextValid?.()===true,action_connected:!!a?.isConnected,menu_connected:!!m?.isConnected,
    marker_count:document.querySelectorAll('[data-sc-mail-local-file-action="true"]').length,
    exact_marker:a?.matches('[data-sc-mail-local-file-action="true"]')===true,in_menu:!!m?.contains(a),
    positive_rect:!!r&&r.width>0&&r.height>0,in_viewport:!!r&&r.x>=0&&r.y>=0&&r.right<=innerWidth&&r.bottom<=innerHeight,
    hit_target_owned:!!hit&&(hit===a||a?.contains(hit)),pointer_events:a?getComputedStyle(a).pointerEvents!=='none':false,
    visible:a?getComputedStyle(a).visibility==='visible':false,disabled:!!a?.disabled||a?.getAttribute('aria-disabled')==='true',
    native_action_seen:s?.attachmentNativeActionSeen===true,native_action_trusted:s?.attachmentNativeActionTrusted===true,
    input_activated:!!s?.attachmentActivatedInput,activation_ambiguous:s?.attachmentActivationAmbiguous===true};
}
// Fixed, bounded actionability diagnostics. No HTML, names, selectors or text leave the page.
export function menuSettleFacts(){
 const s=globalThis.__sparkclawManagedMail,visible=n=>n?.isConnected&&n.getBoundingClientRect().width>0&&n.getBoundingClientRect().height>0&&getComputedStyle(n).visibility!=='hidden';
 const menus=[...document.querySelectorAll('[role="menu"]')].filter(n=>visible(n)&&!s?.attachmentPriorMenus?.has(n));
 const actions=menus.length===1?[...menus[0].querySelectorAll('[role="menuitem"],button')].filter(n=>visible(n)&&/^(Browse this computer|Browse computer|浏览此计算机|浏览这台计算机|Upload from this device|从此设备上传)$/u.test(n.getAttribute('aria-label')||n.getAttribute('data-tooltip')||n.textContent.trim())):[];
 const a=actions.length===1?actions[0]:null,r=a?.getBoundingClientRect(),hit=r&&document.elementFromPoint(r.x+r.width/2,r.y+r.height/2),c=s?.attachmentMenuCandidate;
 const context=s?.attachmentContextValid?.()===true,expanded=s?.attachmentButton?.getAttribute('aria-expanded')==='true',enabled=!!a&&!a.disabled&&a.getAttribute('aria-disabled')!=='true',pointer=!!a&&getComputedStyle(a).pointerEvents!=='none',viewport=!!r&&r.x>=0&&r.y>=0&&r.right<=innerWidth&&r.bottom<=innerHeight,owned=!!hit&&(hit===a||a?.contains(hit)),boxSame=!!c&&!!r&&[r.x,r.y,r.width,r.height].every((v,i)=>Math.abs(v-c.box[i])<=0.5);
 const reason=!context?'context_invalid':!expanded?'not_expanded':menus.length===0?'no_visible_menu':menus.length!==1?'menu_count':actions.length===0?'no_visible_action':actions.length!==1?'action_count':!enabled?'disabled':!pointer?'pointer_disabled':!viewport?'outside_viewport':!owned?'hit_target':!c?'no_candidate':c.menu!==menus[0]?'menu_identity':c.action!==a?'action_identity':!boxSame?'rect_changed':Date.now()-c.since<150?'stabilizing':'stable';
 return {context:s?.attachmentContextValid?.()===true,expanded:s?.attachmentButton?.getAttribute('aria-expanded')==='true',menu_count:Math.min(menus.length,8),action_count:Math.min(actions.length,8),
  action_enabled:!!a&&!a.disabled&&a.getAttribute('aria-disabled')!=='true',pointer_events:!!a&&getComputedStyle(a).pointerEvents!=='none',in_viewport:!!r&&r.x>=0&&r.y>=0&&r.right<=innerWidth&&r.bottom<=innerHeight,
  hit_target_owned:!!hit&&(hit===a||a?.contains(hit)),hit_target:!hit?'none':hit===a||a?.contains(hit)?'action':menus.length===1&&menus[0].contains(hit)?'menu':hit===document.body?'body':'other',
  condition:reason,rect_positive:!!r&&r.width>0&&r.height>0,candidate_menu_same:!!c&&c.menu===menus[0],candidate_action_same:!!c&&c.action===a,candidate_box_same:boxSame,stable_age_ms:c?Math.min(10000,Math.max(0,Date.now()-c.since)):0,same_candidate:!!c&&c.menu===menus[0]&&c.action===a,stability:!c?'none':Date.now()-c.since<150?'pending':'elapsed'};
}
export async function paintProgressDOM(){
 const before={visibility:document.visibilityState,focused:document.hasFocus()};let frames=0,request;
 const tick=()=>{frames++;if(frames<3)request=requestAnimationFrame(tick);};request=requestAnimationFrame(tick);
 await new Promise(resolve=>setTimeout(resolve,1000));cancelAnimationFrame(request);
 return {before,after:{visibility:document.visibilityState,focused:document.hasFocus()},frames};
}
export function menuHoverDOM(phase){
 const s=globalThis.__sparkclawManagedMail,visible=n=>n?.isConnected&&n.getBoundingClientRect().width>0&&n.getBoundingClientRect().height>0&&getComputedStyle(n).visibility!=='hidden';
 const menus=[...document.querySelectorAll('[role="menu"]')].filter(n=>visible(n)&&!s?.attachmentPriorMenus?.has(n));
 const actions=menus.length===1?[...menus[0].querySelectorAll('[role="menuitem"],button')].filter(n=>visible(n)&&/^(Browse this computer|Browse computer|浏览此计算机|浏览这台计算机|Upload from this device|从此设备上传)$/u.test(n.getAttribute('aria-label')||n.getAttribute('data-tooltip')||n.textContent.trim())):[];
 const menu=menus.length===1?menus[0]:null,action=actions.length===1?actions[0]:null,rect=action?.getBoundingClientRect(),hit=rect&&document.elementFromPoint(rect.x+rect.width/2,rect.y+rect.height/2);
 const attrs=n=>n?{tag:/^[a-z]{1,16}$/u.test(n.tagName.toLowerCase())?n.tagName.toLowerCase():'other',role:['menu','menuitem','button','presentation','none','tooltip'].includes(n.getAttribute('role'))?n.getAttribute('role'):null,classes:[...n.classList].filter(v=>/^[a-zA-Z_-][a-zA-Z0-9_-]{0,47}$/u.test(v)).slice(0,4),pointer_events:getComputedStyle(n).pointerEvents==='none'?'none':'enabled',display:['none','block','flex','grid','contents','inline','inline-block','inline-flex'].includes(getComputedStyle(n).display)?getComputedStyle(n).display:'other',visibility:getComputedStyle(n).visibility,opacity:Math.max(0,Math.min(1,Number(getComputedStyle(n).opacity))),hidden:n.hidden===true,aria_hidden:n.getAttribute('aria-hidden')==='true',inert:n.inert===true,connected:n.isConnected}:null;
 const structure={menu:attrs(menu),action:attrs(action),hit:attrs(hit),menu_contains_action:!!menu?.contains(action),menu_contains_hit:!!menu?.contains(hit),action_contains_hit:!!action?.contains(hit),hit_contains_menu:!!hit?.contains(menu),hit_contains_action:!!hit?.contains(action),action_parent:attrs(action?.parentElement),hit_parent:attrs(hit?.parentElement)};
 const ancestryVisible=n=>{let depth=0;for(let current=n;current;current=current.parentElement){if(++depth>32)return false;const style=getComputedStyle(current);if(current.hidden||current.inert||current.getAttribute('aria-hidden')==='true'||style.display==='none'||Number(style.opacity)<=0)return false;}return !!n&&getComputedStyle(n).visibility==='visible';};
 structure.menu_ancestry_visible=ancestryVisible(menu);structure.action_ancestry_visible=ancestryVisible(action);
 if(phase==='prepare'){const chain=n=>{const nodes=[];for(let p=n?.parentElement;p&&nodes.length<6;p=p.parentElement)nodes.push(attrs(p));return nodes;};structure.menu_ancestors=chain(menu);structure.action_ancestors=chain(action);structure.hit_ancestors=chain(hit);}
 const ready=s?.attachmentContextValid?.()===true&&s.attachmentButton?.getAttribute('aria-expanded')==='true'&&menus.length===1&&actions.length===1&&structure.menu_ancestry_visible&&structure.action_ancestry_visible;
 if(phase==='prepare'){
  if(!ready||document.querySelector('[data-sc-diagnostic-outlook-menu]'))return {ready:false,structure};
  s.diagnosticHover={menu,action};menu.setAttribute('data-sc-diagnostic-outlook-menu','true');return {ready:true,structure};
 }
 const prior=s?.diagnosticHover;
 return {ready:ready&&prior?.menu===menu&&prior.menu.isConnected,action_replaced:!!prior&&prior.action!==action,original_action_connected:!!prior?.action?.isConnected,structure};
}
export function outlookRibbonFacts(){
 const visible=n=>n?.isConnected&&n.getBoundingClientRect().width>0&&n.getBoundingClientRect().height>0&&getComputedStyle(n).visibility==='visible';
 const all=[...document.querySelectorAll('button[data-automation-type="RibbonFlyoutAnchor"][aria-haspopup="true"]')];
 const attach=all.filter(n=>/^(Attach files|Attach file|附加文件)$/u.test(n.getAttribute('aria-label')||n.textContent.trim()));
 const s=globalThis.__sparkclawManagedMail;
 return {document_focused:document.hasFocus(),visibility:document.visibilityState,body_focused:!!s?.body&&document.activeElement===s.body,
  ribbon_total:all.length,exact_attach_total:attach.length,exact_attach_visible:attach.filter(visible).length,
  exact_attach_enabled:attach.filter(n=>visible(n)&&!n.disabled&&n.getAttribute('aria-disabled')!=='true').length,
  exact_attach_parent:attach.filter(n=>n.closest('[data-automation-type="RibbonBottomBarContainer"]')).length};
}
export function actionErrorCategory(error) {
  const m=String(error?.message??'');
  if(/intercepts pointer events|subtree intercepts/u.test(m))return 'pointer_interception';
  if(/not stable/u.test(m))return 'unstable';
  if(/not visible/u.test(m))return 'not_visible';
  if(/detached|not attached/u.test(m))return 'detached';
  if(/not enabled|disabled/u.test(m))return 'disabled';
  if(/waiting for locator/u.test(m))return 'locator_missing';
  if(/Timeout|timed out/u.test(m))return 'timeout_other';
  return 'other';
}
export async function diagnoseEmptyOutlookMenu(tab,{managedSendDOM,attachmentDOM,verifySendAccount,ATTACHMENT_MENU_WAIT_MS=5000},account,{hoverMenu=false,waitVisible=false}={}) {
  tab.checkpoint?.('verify_account');
  await verifySendAccount(tab,'outlook',account);
  tab.checkpoint?.('preexisting_composer_guard');
  const clean=(await tab.inspect(`()=>({clean:!document.querySelector('[contenteditable="true"][aria-label="Message body"],[contenteditable="true"][aria-label="Email body"],[contenteditable="true"][aria-label="邮件正文"],[contenteditable="true"][aria-label="消息正文"]')})`))?.result;
  if(!clean?.clean)throw new Error('email_existing_draft');
  tab.checkpoint?.('open_contract');
  const opened=(await tab.inspect(`()=>{const r=(${managedSendDOM.toString()})('outlook','open',{mode:'compose'});return {opened:r.opened===true,error:r.error??null,selector:r.action_selector??null}}`))?.result;
  if(opened?.error==='email_reply_control_unavailable'){
    // Outlook may omit New mail from the compact Ribbon. This fixed same-origin
    // deep link creates a fresh blank compose; a restored/hidden old composer
    // rejects before navigation. No URL/selector is accepted from the caller.
    tab.checkpoint?.('fixed_compose_deeplink');
    const fresh=await tab.runReadCode(`/* app-cli:awaited-code:v1 */\nasync page=>{
      const clean=await page.evaluate(()=>location.origin==='https://outlook.live.com'&&!document.querySelector('[contenteditable="true"][aria-label="Message body"],[contenteditable="true"][aria-label="Email body"],[contenteditable="true"][aria-label="邮件正文"],[contenteditable="true"][aria-label="消息正文"]'));
      if(!clean)return {fresh:false};
      await page.goto('https://outlook.live.com/mail/0/deeplink/compose');
      await page.evaluate(()=>{if(location.origin!=='https://outlook.live.com')throw new Error('provider_origin_changed');globalThis.__sparkclawManagedMail={provider:'outlook',mode:'compose',target:'',selection:'',diagnosticNewPage:true};});return {fresh:true};
    }`);
    if(!fresh?.fresh)throw new Error('new_composer_unavailable');
    tab.checkpoint?.('verify_new_composer_account');
    await verifySendAccount(tab,'outlook',account);
  }else{
    if(!opened?.opened||opened.selector!=='[data-sc-mail-action="prepare"]')throw new Error(opened?.error||'new_composer_unavailable');
    tab.checkpoint?.('new_mail_action');
    await tab.click('[data-sc-mail-action="prepare"]');
  }
  tab.checkpoint?.('empty_editor');
  const editor=(await tab.inspect(`async()=>{const dom=${managedSendDOM.toString()};let phase='editor';try{const end=Date.now()+3500;do{const r=dom('outlook','editor',{mode:'compose'});if(r.ready){phase='readback';const rb=dom('outlook','readback',{});phase='empty_guard';const empty=!rb.error&&rb.to.length===0&&rb.cc.length===0&&!rb.subject&&!rb.body.trim()&&(${menuFacts.toString()})().empty;return {ready:true,empty}}if(r.error&&!['email_reply_editor_unverified','email_recipient_editor_unverified'].includes(r.error))return {error:r.error};await new Promise(r=>setTimeout(r,100))}while(Date.now()<end);return {ready:false,state_present:!!globalThis.__sparkclawManagedMail,body_count:document.querySelectorAll('[contenteditable="true"][aria-label="Message body"],[contenteditable="true"][aria-label="邮件正文"]').length}}catch(error){return {ready:false,failed_phase:phase,exception_type:['TypeError','ReferenceError','SyntaxError'].includes(error.name)?error.name:'other',error_category:/Cannot read properties/u.test(error.message)?'null_property':/is not a function/u.test(error.message)?'not_function':/is not defined/u.test(error.message)?'undefined_identifier':'other'}}}`))?.result;
  tab.record?.('editor',editor);
  if(!editor?.ready||!editor.empty)throw new Error('new_composer_not_proven_empty');
  tab.checkpoint?.('attachment_prepare');
  tab.record?.('ribbon',(await tab.inspect(`()=>(${outlookRibbonFacts.toString()})()`))?.result);
  const prepared=(await tab.inspect(`()=>(${attachmentDOM.toString()})('outlook','prepare',[])`))?.result;
  tab.record?.('prepare',{error:prepared?.error??null,chooser:prepared?.chooser===true,shared:prepared?.shared===true,target:prepared?.selector==='[data-sc-mail-attachment-chooser="true"]'?'chooser':prepared?.selector==='[data-sc-mail-attachment-input="true"]'?'input':'none'});
  if(!prepared?.shared){tab.record?.('prepare_structure',(await tab.inspect(`()=>(${attachmentDOM.toString()})('outlook','diagnostic',[])`))?.result);}
  if(!prepared?.shared||prepared.selector!=='[data-sc-mail-attachment-chooser="true"]')throw new Error('shared_ribbon_not_unique');
  // Await within the real pinned CLI wrapper. No owned-chooser ACK: an empty
  // chooser is closed only by normal cleanup of this diagnostic task page.
  tab.checkpoint?.('native_menu_action');
  return tab.runReadCode(`/* app-cli:awaited-code:v1 */\n/* sparkclaw:empty-outlook-menu-diagnostic:v1 */\nasync page=>{
    const facts=${menuFacts.toString()},category=${actionErrorCategory.toString()};const paintBefore=${hoverMenu===true||waitVisible===true}?await page.evaluate(${paintProgressDOM.toString()}):null;
    await page.locator('[data-sc-mail-attachment-chooser="true"]').click({timeout:5000});
    let hover=null;if(${hoverMenu===true||waitVisible===true}){const paintAfter=await page.evaluate(${paintProgressDOM.toString()});const beforeHover=await page.evaluate(async()=>{const read=${menuHoverDOM.toString()};if(!${waitVisible===true})return read('prepare');const started=Date.now(),trace=[];let frames=0,request;const tick=()=>{frames++;if(frames<1000)request=requestAnimationFrame(tick);};request=requestAnimationFrame(tick);let last,key;do{last=read('prepare');const state={ready:last.ready,menu_visible:last.structure.menu_ancestry_visible,action_visible:last.structure.action_ancestry_visible,menu_opacity:last.structure.menu?.opacity??null,menu_pointer:last.structure.menu?.pointer_events??null};const next=JSON.stringify(state);if(next!==key&&trace.length<16)trace.push({elapsed_ms:Date.now()-started,frames,...state});key=next;if(last.ready)break;await new Promise(r=>setTimeout(r,200));}while(Date.now()-started<15000);cancelAnimationFrame(request);return {...last,visibility_trace:trace,wait_elapsed_ms:Date.now()-started,frames};});hover={before:beforeHover,error:null,performed:${hoverMenu===true},paint_before:paintBefore,paint_after:paintAfter};if(!beforeHover.ready)return {phase:'hover_guard',hover};try{if(${hoverMenu===true})await page.locator('[data-sc-diagnostic-outlook-menu="true"]').hover({timeout:3000});}catch(error){hover.error=category(error);}hover.after=await page.evaluate(()=>(${menuHoverDOM.toString()})('after'));if(hover.error||!hover.after.ready)return {phase:'hover',hover};}
    const menu=await page.evaluate(async()=>{const end=Date.now()+${ATTACHMENT_MENU_WAIT_MS},facts=${menuSettleFacts.toString()},history=[];let last,key,observations=0;do{const before=facts(),value=(${attachmentDOM.toString()})('outlook','chooser_menu',[]);last={...facts(),before_condition:before.condition};observations++;const next=JSON.stringify({...last,stable_age_ms:0});if(history.length<24&&key!==next)history.push(last);key=next;if(value.error||value.selector)return {...value,settle:last,settle_history:history,observations};await new Promise(r=>setTimeout(r,50))}while(Date.now()<end);return {error:'email_attachment_control_unavailable',settle:last,settle_history:history,observations}});
    if(menu?.error||menu?.selector!=='[data-sc-mail-local-file-action="true"]')return {phase:'menu',hover,error:menu?.error||'menu_timeout',settle:menu?.settle,settle_history:menu?.settle_history,observations:menu?.observations};
    const before=await page.evaluate(facts);if(!before.empty||!before.context||before.marker_count!==1||!before.exact_marker||!before.in_menu)return {phase:'guard',before};
    await page.evaluate(()=>{const s=globalThis.__sparkclawManagedMail;s.diagnosticChanges={marker_removed:false,action_detached:false,action_replaced:false};const a=s.attachmentMenuAction;s.diagnosticObserver=new MutationObserver(()=>{s.diagnosticChanges.marker_removed ||= !a.matches('[data-sc-mail-local-file-action="true"]');s.diagnosticChanges.action_detached ||= !a.isConnected;s.diagnosticChanges.action_replaced ||= [...(s.attachmentMenu?.querySelectorAll('[role="menuitem"],button')??[])].some(n=>n!==a&&/^(Browse this computer|Browse computer|浏览此计算机|浏览这台计算机|Upload from this device|从此设备上传)$/u.test(n.getAttribute('aria-label')||n.textContent.trim()))});s.diagnosticObserver.observe(document.documentElement,{childList:true,subtree:true,attributes:true,attributeFilter:['data-sc-mail-local-file-action']});});
    let chooserEvents=0,actionError=null;const observe=()=>chooserEvents++;page.on('filechooser',observe);const pendingChooser=page.waitForEvent('filechooser',{timeout:6500}).then(value=>value,()=>null);let chooser=null;
    try{await page.locator('[data-sc-mail-local-file-action="true"]').click({timeout:5000});chooser=await pendingChooser;}catch(error){actionError=category(error);}
    finally{page.removeListener('filechooser',observe);}
    const chooserBindingValid=!!chooser&&await page.evaluate(node=>{const s=globalThis.__sparkclawManagedMail;return s?.attachmentContextValid?.()===true&&node===s.attachmentActivatedInput&&s.attachmentPriorInputs?.has(node)===true&&s.attachmentPriorInputs.get(node)===node.parentElement&&node.isConnected&&node.ownerDocument===document&&node.type==='file'&&!node.disabled&&!node.webkitdirectory&&node.accept===''&&!s.attachmentActivationAmbiguous;},chooser.element());
    const after=await page.evaluate(facts);const changes=await page.evaluate(()=>{const s=globalThis.__sparkclawManagedMail;s.diagnosticObserver.disconnect();return s.diagnosticChanges;});
    return {phase:'native_action',hover,before,after,changes,action_error:actionError,chooser_events:chooserEvents,chooser_binding_valid:chooserBindingValid,no_upload:true,no_send:true};
  }`);
}

// Preserve only the fixed observation if the mandatory post-call origin read
// rejects an intentionally unacknowledged empty chooser. This is evidence, never
// an authorization or successful Host result; cleanup and original error remain.
export function sanitizeMenuObservation(raw){
 let v;try{v=JSON.parse(raw)}catch{return null}
 if(v?.phase!=='native_action'||v.no_send!==true||v.no_upload!==true||typeof v.chooser_binding_valid!=='boolean'||!Number.isSafeInteger(v.chooser_events)||v.chooser_events<0||v.chooser_events>4)return null;
 const keys=['empty','context','action_connected','menu_connected','exact_marker','in_menu','positive_rect','in_viewport','hit_target_owned','pointer_events','visible','disabled','native_action_seen','native_action_trusted','input_activated','activation_ambiguous'];
 const facts=x=>{if(!x||!keys.every(k=>typeof x[k]==='boolean')||!Number.isSafeInteger(x.marker_count)||x.marker_count<0||x.marker_count>32)return null;return Object.fromEntries([...keys,'marker_count'].map(k=>[k,x[k]]));};
 const before=facts(v.before),after=facts(v.after),changes=['marker_removed','action_detached','action_replaced'];
 const attrs=x=>{if(x===null)return null;if(!x||!['menu','menuitem','button','presentation','none','tooltip',null].includes(x.role)||!['none','enabled'].includes(x.pointer_events)||typeof x.connected!=='boolean'||!['none','block','flex','grid','contents','inline','inline-block','inline-flex','other'].includes(x.display)||!['visible','hidden','collapse'].includes(x.visibility)||typeof x.opacity!=='number'||x.opacity<0||x.opacity>1||!['hidden','aria_hidden','inert'].every(k=>typeof x[k]==='boolean')||!/^[a-z]{1,16}$/u.test(x.tag)||!Array.isArray(x.classes)||x.classes.length>4||!x.classes.every(c=>/^[a-zA-Z_-][a-zA-Z0-9_-]{0,47}$/u.test(c)))throw new Error('invalid');return Object.fromEntries(['tag','role','classes','pointer_events','display','visibility','opacity','hidden','aria_hidden','inert','connected'].map(k=>[k,x[k]]));};
 const hoverRead=x=>{if(typeof x?.ready!=='boolean')throw new Error('invalid');const result={ready:x.ready,structure:{}};for(const k of ['menu','action','hit','action_parent','hit_parent'])result.structure[k]=attrs(x.structure?.[k]);for(const k of ['menu_contains_action','menu_contains_hit','action_contains_hit','hit_contains_menu','hit_contains_action','menu_ancestry_visible','action_ancestry_visible']){if(typeof x.structure?.[k]!=='boolean')throw new Error('invalid');result.structure[k]=x.structure[k];}for(const k of ['menu_ancestors','action_ancestors','hit_ancestors'])if(k in x.structure){if(!Array.isArray(x.structure[k])||x.structure[k].length>6)throw new Error('invalid');result.structure[k]=x.structure[k].map(attrs);}for(const k of ['action_replaced','original_action_connected'])if(k in x){if(typeof x[k]!=='boolean')throw new Error('invalid');result[k]=x[k];}if('visibility_trace' in x){if(!Array.isArray(x.visibility_trace)||x.visibility_trace.length>16||!Number.isSafeInteger(x.frames)||x.frames<0||x.frames>1000||!Number.isSafeInteger(x.wait_elapsed_ms)||x.wait_elapsed_ms<0||x.wait_elapsed_ms>20000)throw new Error('invalid');result.frames=x.frames;result.wait_elapsed_ms=x.wait_elapsed_ms;result.visibility_trace=x.visibility_trace.map(v=>{if(!Number.isSafeInteger(v.elapsed_ms)||v.elapsed_ms<0||v.elapsed_ms>20000||!Number.isSafeInteger(v.frames)||v.frames<0||v.frames>1000||!['ready','menu_visible','action_visible'].every(k=>typeof v[k]==='boolean')||![null,'none','enabled'].includes(v.menu_pointer)||!(v.menu_opacity===null||typeof v.menu_opacity==='number'&&v.menu_opacity>=0&&v.menu_opacity<=1))throw new Error('invalid');return Object.fromEntries(['elapsed_ms','frames','ready','menu_visible','action_visible','menu_opacity','menu_pointer'].map(k=>[k,v[k]]));});}return result;};
 let hover=null;try{if(v.hover){if(v.hover.error!==null||typeof v.hover.performed!=='boolean')throw new Error('invalid');hover={before:hoverRead(v.hover.before),after:hoverRead(v.hover.after),error:null,performed:v.hover.performed};for(const k of ['paint_before','paint_after']){const x=v.hover[k];if(!Number.isSafeInteger(x?.frames)||x.frames<0||x.frames>3||!['before','after'].every(p=>['hidden','visible'].includes(x[p]?.visibility)&&typeof x[p]?.focused==='boolean'))throw new Error('invalid');hover[k]={frames:x.frames,before:{visibility:x.before.visibility,focused:x.before.focused},after:{visibility:x.after.visibility,focused:x.after.focused}};}}}catch{return null;}

 if(!before||!after||!changes.every(k=>typeof v.changes?.[k]==='boolean')||![null,'pointer_interception','unstable','not_visible','detached','disabled','locator_missing','timeout_other','other'].includes(v.action_error))return null;
 return {phase:'native_action',hover,before,after,changes:Object.fromEntries(changes.map(k=>[k,v.changes[k]])),action_error:v.action_error,chooser_events:v.chooser_events,chooser_binding_valid:v.chooser_binding_valid,no_upload:true,no_send:true};
}
