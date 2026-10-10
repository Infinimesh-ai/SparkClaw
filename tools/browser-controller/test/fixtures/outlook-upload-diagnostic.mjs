import {prepareEmptyOutlookComposer} from './outlook-menu-diagnostic.mjs';

// Fixed approved desktop-source manifest. No caller-supplied file or message.
export const OUTLOOK_UPLOAD_MANIFEST = Object.freeze({name:'SparkX-work2-acceptance.txt',size_bytes:158,
  sha256:'sha256:39803ed9f9698d9676bf92ca10819e4f242248ca35dbe9c885cf6b09ffc070b2'});

// Only this newly proven-empty composer is read. No other mailbox DOM/text.
export function outlookUploadFacts(expected) {
  const s=globalThis.__sparkclawManagedMail;
  if(!s?.ownershipChecked||s.provider!=='outlook'||!s.root?.isConnected||!s.root.contains(s.body)||!s.root.contains(s.send)||s.sendAttempted)return {error:'owned_composer_lost'};
  const visible=n=>{if(!n?.isConnected||!s.root.contains(n)||getComputedStyle(n).visibility!=='visible')return false;const r=n.getBoundingClientRect();if(r.width<=0||r.height<=0)return false;for(let p=n;p;p=p.parentElement){const cs=getComputedStyle(p);if(p.hidden||p.inert||p.getAttribute('aria-hidden')==='true'||cs.display==='none'||Number(cs.opacity)<=0)return false;if(p===s.root)return true;}return false;};
  const redact=text=>String(text??'').split(expected.name).join('[approved-file]').replace(/[A-Za-z0-9.!#$%&'*+/=?^_`{|}~-]+@[A-Za-z0-9.-]+/gu,'[address]').replace(/https?:\/\/\S+/gu,'[url]').slice(0,200);
  const attrs=n=>({tag:n.tagName.toLowerCase(),classes:[...n.classList].filter(v=>/^[a-zA-Z0-9_-]{1,64}$/u.test(v)).slice(0,8),
    role:['alert','status','button','progressbar','listitem','list','group'].includes(n.getAttribute('role'))?n.getAttribute('role'):null,
    visible:visible(n),disabled:n.disabled===true||n.getAttribute('aria-disabled')==='true',busy:n.getAttribute('aria-busy')==='true',
    title:redact(n.getAttribute('title')),label:redact(n.getAttribute('aria-label')),attachment_id:n.hasAttribute('data-attachment-id'),
    aria_labelledby:n.hasAttribute('aria-labelledby'),aria_describedby:n.hasAttribute('aria-describedby'),expected_name:[n.getAttribute('title'),n.getAttribute('aria-label'),n.textContent?.trim()].includes(expected.name)});
  const names=[...s.root.querySelectorAll('span,div,[title],[aria-label]')].slice(0,4096).filter(n=>n.children.length<3&&[n.getAttribute('title'),n.getAttribute('aria-label'),n.textContent?.trim()].includes(expected.name)).slice(0,4);
  const contexts=names.map(n=>{const chain=[];for(let p=n,i=0;p&&p!==s.root&&i<7;p=p.parentElement,i++)chain.push({node:attrs(p),children:[...p.children].slice(0,10).map(attrs),controls:[...p.querySelectorAll('button,[role="button"]')].slice(0,8).map(attrs)});return chain;});
  const alerts=[...s.root.querySelectorAll('[role="alert"],.attachment-error,.upload-error')].slice(0,8).map(n=>({...attrs(n),text:redact(n.innerText),children:[...n.children].slice(0,8).map(attrs)}));
  const actual=s.attachmentVerifiedManifest;
  return {owned:true,send_attempted:false,body_empty:!s.body.innerText.trim(),context_valid:s.attachmentContextValid?.()===true,
    input_valid:s.attachmentInputValid?.()===true,byte_proof_matches:JSON.stringify(actual)===JSON.stringify([expected]),
    input_file_count:s.attachmentInput?.files?.length??null,known_rows:s.root.querySelectorAll('[data-attachment-id]').length,
    pending:[...s.root.querySelectorAll('[role="progressbar"],[aria-busy="true"],.attachment-uploading,.uploading')].filter(visible).length,
    alerts,contexts};
}

export async function diagnoseFixedOutlookUpload(tab,modules,account,workspace,relative) {
  await prepareEmptyOutlookComposer(tab,modules,account);
  const manifest=[{...OUTLOOK_UPLOAD_MANIFEST,path:relative}];
  let effects=0,result={no_send:true,approved_manifest:OUTLOOK_UPLOAD_MANIFEST};
  try {
    tab.checkpoint?.('fixed_approved_upload');
    try {await modules.uploadManagedAttachments(tab,'outlook',workspace,manifest,()=>{if(++effects!==1)throw new Error('duplicate_upload_effect');});result.upload_returned=true;await modules.verifyManagedAttachments(tab,'outlook',manifest);result.final_verified=true;}
    catch(error){result.upload_returned=false;result.error=/^email_[a-z_]+$/u.test(error.code??'')?error.code:'host_failure';}
    result.effects=effects;
    result.immediate=(await tab.inspect(`()=>(${outlookUploadFacts.toString()})(${JSON.stringify(OUTLOOK_UPLOAD_MANIFEST)})`))?.result;
    result.settled=await tab.runReadCode(`/* app-cli:awaited-code:v1 */\nasync page=>page.evaluate(async()=>{await new Promise(r=>setTimeout(r,5000));return (${outlookUploadFacts.toString()})(${JSON.stringify(OUTLOOK_UPLOAD_MANIFEST)});})`);
    return result;
  } finally {
    tab.checkpoint?.('discard_own_diagnostic_composer');
    result.discard=await modules.discardManagedDraft(tab,'outlook').then(value=>value?.discarded===true,()=>false);
  }
}
