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
export async function diagnoseEmptyOutlookMenu(tab,{managedSendDOM,attachmentDOM,verifySendAccount},account) {
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
    const facts=${menuFacts.toString()},category=${actionErrorCategory.toString()};
    await page.locator('[data-sc-mail-attachment-chooser="true"]').click({timeout:5000});
    const end=Date.now()+5000;let menu;do{menu=await page.evaluate(()=>(${attachmentDOM.toString()})('outlook','chooser_menu',[]));if(menu.error||menu.selector)break;await new Promise(r=>setTimeout(r,100))}while(Date.now()<end);
    if(menu?.error||menu?.selector!=='[data-sc-mail-local-file-action="true"]')return {phase:'menu',error:menu?.error||'menu_timeout'};
    const before=await page.evaluate(facts);if(!before.empty||!before.context||before.marker_count!==1||!before.exact_marker||!before.in_menu)return {phase:'guard',before};
    await page.evaluate(()=>{const s=globalThis.__sparkclawManagedMail;s.diagnosticChanges={marker_removed:false,action_detached:false,action_replaced:false};const a=s.attachmentMenuAction;s.diagnosticObserver=new MutationObserver(()=>{s.diagnosticChanges.marker_removed ||= !a.matches('[data-sc-mail-local-file-action="true"]');s.diagnosticChanges.action_detached ||= !a.isConnected;s.diagnosticChanges.action_replaced ||= [...(s.attachmentMenu?.querySelectorAll('[role="menuitem"],button')??[])].some(n=>n!==a&&/^(Browse this computer|Browse computer|浏览此计算机|浏览这台计算机|Upload from this device|从此设备上传)$/u.test(n.getAttribute('aria-label')||n.textContent.trim()))});s.diagnosticObserver.observe(document.documentElement,{childList:true,subtree:true,attributes:true,attributeFilter:['data-sc-mail-local-file-action']});});
    let chooserEvents=0,actionError=null;const observe=()=>chooserEvents++;page.on('filechooser',observe);const pendingChooser=page.waitForEvent('filechooser',{timeout:6500}).then(()=>true,()=>false);
    try{await page.locator('[data-sc-mail-local-file-action="true"]').click({timeout:5000});await pendingChooser;}catch(error){actionError=category(error);}
    finally{page.removeListener('filechooser',observe);}
    const after=await page.evaluate(facts);const changes=await page.evaluate(()=>{const s=globalThis.__sparkclawManagedMail;s.diagnosticObserver.disconnect();return s.diagnosticChanges;});
    return {phase:'native_action',before,after,changes,action_error:actionError,chooser_events:chooserEvents,no_upload:true,no_send:true};
  }`);
}

// Preserve only the fixed observation if the mandatory post-call origin read
// rejects an intentionally unacknowledged empty chooser. This is evidence, never
// an authorization or successful Host result; cleanup and original error remain.
export function sanitizeMenuObservation(raw){
 let v;try{v=JSON.parse(raw)}catch{return null}
 if(v?.phase!=='native_action'||v.no_send!==true||v.no_upload!==true||!Number.isSafeInteger(v.chooser_events)||v.chooser_events<0||v.chooser_events>4)return null;
 const keys=['empty','context','action_connected','menu_connected','exact_marker','in_menu','positive_rect','in_viewport','hit_target_owned','pointer_events','visible','disabled','native_action_seen','native_action_trusted','input_activated','activation_ambiguous'];
 const facts=x=>{if(!x||!keys.every(k=>typeof x[k]==='boolean')||!Number.isSafeInteger(x.marker_count)||x.marker_count<0||x.marker_count>32)return null;return Object.fromEntries([...keys,'marker_count'].map(k=>[k,x[k]]));};
 const before=facts(v.before),after=facts(v.after),changes=['marker_removed','action_detached','action_replaced'];
 if(!before||!after||!changes.every(k=>typeof v.changes?.[k]==='boolean')||![null,'pointer_interception','unstable','not_visible','detached','disabled','locator_missing','timeout_other','other'].includes(v.action_error))return null;
 return {phase:'native_action',before,after,changes:Object.fromEntries(changes.map(k=>[k,v.changes[k]])),action_error:v.action_error,chooser_events:v.chooser_events,no_upload:true,no_send:true};
}
