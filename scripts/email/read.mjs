import { prepareNetworkOriginal, armNetworkOriginal, networkListPage, warmOutlookNetwork, prepareNetworkFolder, recoverOutlookTarget } from './lib/network-reader.mjs';
import { qqMailDetailIdentity, qqMailDetailDiagnostics } from './lib/qqmail-detail.mjs';
import crypto from 'node:crypto';
import { capturePage, captureUnread, markCapturedRead, validateCaptureInput, validateMailTarget } from "./lib/read-capture.mjs";
import { gmailUnreadEvidence } from "./lib/gmail-list.mjs";
import { prepareOutlookList, outlookListEvidence } from "./lib/outlook-list.mjs";
import { prepareQQMailList, qqMailListEvidence, qqDiscoveryContinuation, qqDiscoveryPosition } from './lib/qqmail-list.mjs';

export const READ_PROVIDERS = Object.freeze({
  qq_mail: { url: "https://wx.mail.qq.com/home/index#/list/1", origins: ["https://wx.mail.qq.com", "https://mail.qq.com"], rows: ".mail-list-page-item[data-mailid]" },
  outlook: { url: "https://outlook.live.com/mail/0/inbox", origins: ["https://outlook.live.com", "https://outlook.office.com", "https://outlook.office365.com"], rows: '[role="option"][data-convid]' },
  gmail: { url: "https://mail.google.com/mail/u/0/#inbox", origins: ["https://mail.google.com"], rows: "tr.zA" },
});
export const QQ_MAIL_MORE_BUTTON_LABELS = Object.freeze(["More options", "更多", "更多选项", "更多操作"]);
function fail(code = "email_page_contract_changed") { return Object.assign(new Error(code), { code }); }

// Serialized into the Controller's guarded, owned-page inspection.
export function providerDOM(provider, phase, expected = {}, qqIdentity = null) {
  const visible = node => {
    if (!node?.isConnected) return false;
    const rect = node.getBoundingClientRect(), style = getComputedStyle(node);
    return rect.width > 0 && rect.height > 0 && style.display !== "none" && style.visibility !== "hidden";
  };
  const all = (selector, root = document) => Array.from(root.querySelectorAll(selector)).filter(visible);
  const text = node => (node?.innerText ?? node?.textContent ?? "").trim();
  const address = value => String(value ?? "").match(/[A-Za-z0-9.!#$%&'*+/=?^_`{|}~-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}/u)?.[0] ?? "";
  const result = value => ({ url: location.href, ...value });
  if (provider === 'qq_mail' && all('.login-page').length) return result({error:'email_login_required'});
  let account = "";
  if (provider === "gmail") account = address(document.querySelector('[aria-label^="Google Account:"]')?.getAttribute("aria-label"));
  if (provider === "qq_mail") account = address(text(document.querySelector('.frame-header .profile-user-info .user-email')));
  if (provider === "outlook") {
    const roots=all('[role="tree"] [role="treeitem"][aria-level="1"][data-folder-name]');
    const accounts=roots.map(node=>{
      const title=address(node.getAttribute('title'));
      const labels=all(':scope > span',node).map(child=>address(text(child))).filter(Boolean);
      return title && labels.length===1 && title.toLowerCase()===labels[0].toLowerCase() ? title : '';
    }).filter(Boolean);
    account=accounts.length===1 ? accounts[0] : address(text(document.querySelector('#mectrl_currentAccount_secondary'))) ||
      address(document.querySelector('#O365_MainLink_MePhoto, #O365_MeFlexPane_ButtonID, [data-testid="mectrl_headerPicture"]')?.getAttribute('aria-label'));
  }
  if (phase === "account") return expected.required && !account ? null : result({ account_address: account });
  if (phase === 'filter' && provider === 'outlook') {
    if(all('button[aria-label="Unread"], button[aria-label="未读"]').length)return result({filtered:true});
    if(all('button[aria-label="Filter"], button[aria-label="筛选器"]').length)return result({filtered:false});
    return null;
  }
  if (phase === "menu") {
    const commands=all('[role="menu"], [role="menuitem"], [role="menuitemradio"], .xmail-ui-panel-item')
      .flatMap(node => [text(node), ...Array.from(node.querySelectorAll('*')).filter(child => !child.children.length).map(text)]);
    if(expected.required && !commands.some(command=>command.includes(expected.required))) return null;
    if(expected.download_command && !commands.some(command=>/Download|下载/u.test(command))) return null;
    if(expected.read_command && !commands.some(command=>/^(?:Mark as (?:un)?read|标记为(?:未|已)读)$/u.test(command))) return null;
    if(expected.original_eml && !commands.some(command=>/\beml\b/iu.test(command)&&!/[\r\n]|\bmsg\b/iu.test(command))) return null;
    return result({commands});
  }
  if (phase === "list") {
    if (expected.required_account && provider==='qq_mail' && !account) return null;
    if (provider === "gmail") {
      const rows = all('tr.zA');
      if ((expected.filtered||expected.query) && (!location.hash.startsWith('#search/') || decodeURIComponent(location.hash.slice(8)).replace(/\+/gu,' ').trim() !== (expected.query??'in:inbox is:unread'))) return null;
      const empty = rows.length === 0 && (all('.TC').some(node => /^No messages matched your search\.(?:\s|$)/u.test(text(node))) ||
        all('.TC, .ae4').some(node => /No conversations found|没有找到|未找到/u.test(text(node))));
      if (empty && all('[role="main"][aria-busy="true"], [role="main"] [role="progressbar"]').length) return null;
      if (expected.filtered && ((!rows.length && !empty) || rows.some(row=>!row.classList.contains('zE')))) return null;
      return result({ account_address: account, empty, rows: rows.map(row => {
        const marker = row.querySelector('[data-legacy-last-message-id]');
        const id = marker?.getAttribute('data-legacy-last-message-id') ?? '';
        const threadID = marker?.getAttribute('data-thread-id')?.replace(/^#/u,'') ?? '';
        const legacyThreadID = marker?.getAttribute('data-legacy-thread-id') ?? '';
        const senders = row.querySelector('.yW');
        return { provider_message_id: id,
          provider_selection_id: marker?.getAttribute('data-legacy-last-message-id') ?? "",
          provider_thread_id: threadID,
          single_message_row: Boolean(id && /^[a-f0-9]{1,32}$/u.test(legacyThreadID) &&
            (legacyThreadID===id || /^thread-a:r-?\d+$/u.test(threadID)) && marker.getAttribute('data-legacy-last-non-draft-message-id')===id &&
            senders && all('.zF, .yP',senders).length===1 && all('.bx0',senders).length===0 && text(senders)===text(senders.querySelector('.zF, .yP'))),
          unread: row.classList.contains('zE'), subject: text(row.querySelector('.bog')) };
      }) });
    }
    if (provider === "outlook") {
      const rows = all('[role="option"][data-convid]');
      const filtered = all('button[aria-label="Unread"], button[aria-label="未读"]').length > 0;
      if (expected.filtered && !filtered) return null;
      const empty = rows.length === 0 && all('.ksePc').some(node => /No unread messages|没有未读邮件|无未读邮件/u.test(text(node)));
      if(!rows.length&&!empty)return null;
      if (expected.filtered && ((!rows.length && !empty) || rows.some(row=>!/\bUnread\b|未读/u.test(row.getAttribute('aria-label') ?? '')))) return null;
      return result({ account_address: account, empty, rows: rows.map(row => ({
        provider_selection_id: row.getAttribute('data-convid'),
        unread: /\bUnread\b|未读/u.test(row.getAttribute('aria-label') ?? ""),
      })) });
    }
    const rows = all('.mail-list-page-item[data-mailid]');
    const total = /^(\d+) mails?$/u.exec(text(document.querySelector('.mail-total-btn')))?.[1];
    const values = rows.map(row => ({
      provider_message_id: row.getAttribute('data-mailid'), provider_selection_id: row.getAttribute('data-mailid'),
      unread: /unread/u.test(row.className) || Number.parseInt(getComputedStyle(row.querySelector('.mail-subject') ?? row).fontWeight, 10) >= 600,
      subject: text(row.querySelector('.mail-subject')),
    }));
    if (expected.after_ids && !values.some(row=>!expected.after_ids.includes(row.provider_message_id))) return null;
    // Prove exhaustion against the mailbox total, never just a virtual page.
    return result({ account_address: account, total_count: total === undefined ? null : Number(total), empty: total !== undefined && Number(total) === rows.length && values.every(row => !row.unread), rows: values });
  }
  if (phase === "thread_members" && provider === "gmail") {
    const messages = all('.adn[data-legacy-message-id]').filter(node=>all('.a3s',node).length===1);
    const ids = messages.map(node=>node.getAttribute('data-legacy-message-id'));
    if (!ids.includes(expected.provider_message_id)) return null;
    if (!account || !ids.length || ids.length>1000 || ids.some(id=>!/^[a-f0-9]{1,32}$/u.test(id)) || new Set(ids).size!==ids.length) return result({error:"email_message_identity_ambiguous"});
    return result({account_address:account,ids});
  }
  if (phase === "detail") {
    if (provider === "gmail") {
      const allMessages = all('.adn[data-legacy-message-id]');
      const messages = allMessages.filter(node=>node.getAttribute('data-legacy-message-id')===expected.provider_message_id);
      if (!messages.length) return allMessages.length ? result({error:"email_message_identity_mismatch"}) : null;
      if (messages.length !== 1) return result({error:"email_message_identity_ambiguous"});
      const message = messages[0];
      const body = all('.a3s', message)[0];
      if (!body) return null;
      return result({ provider_message_id: expected.provider_message_id, account_address: account,
        subject: text(document.querySelector('h2.hP')), sender: message.querySelector('[email]')?.getAttribute('email') ?? "",
        body_text: text(body), body_html: body.innerHTML, attachments: [], inventory_complete: false });
    }
    if (provider === "outlook") {
      const records=globalThis[expected.evidence_key]?.records;
      const proof=records?.filter(row=>row.provider_selection_id===expected.provider_selection_id &&
        (row.provider_message_id===expected.provider_message_id || expected.individual_message_proven && row.inventory_complete && row.members?.some(member=>member.provider_message_id===expected.provider_message_id && !member.draft)));
      let networkProof=false;
      if(expected.network_member_proven && window.SparkClawMailReader?.provider==='outlook') {
        try{networkProof=window.SparkClawMailReader.verifyTarget({...expected,account_address:account});}catch{}
      }
      if(proof?.length!==1 && !networkProof)return result({error:'email_message_identity_ambiguous'});
      const selected = all('[role="option"][data-convid][aria-selected="true"]');
      if (selected.length!==1 || selected[0].getAttribute('data-convid') !== expected.provider_selection_id) return null;
      const match = (expected.network_member_proven ? /^\/mail\/\d+\/[^/]+\/id\/([^/]+)\/?$/u : /^\/mail\/\d+\/(?:inbox|sentitems)\/id\/([^/]+)\/?$/u).exec(location.pathname);
      if (!match) return null;
      if (expected.individual_message_proven) {
        const host = document.getElementById(expected.provider_message_id);
        const items = host ? all('[role="listitem"]',host) : [];
        if (items.length!==1 || all('[role="checkbox"][aria-checked="true"]',items[0]).length!==1) return null;
        const focused=all('#focused');
        const bodies=focused.length===1 ? all('[id^="UniqueMessageBody"]',focused[0]) : [];
        if(bodies.length!==1 || all('button[aria-label="More items"]',focused[0]).length!==1) return null;
        if(decodeURIComponent(match[1])!==expected.provider_selection_id)return result({error:'email_message_identity_mismatch'});
        return result({provider_message_id:expected.provider_message_id,provider_selection_id:expected.provider_selection_id,
          individual_message_proven:true,verify_body_text:true,account_address:account,body_text:text(bodies[0]),body_html:bodies[0].innerHTML,attachments:[],inventory_complete:false});
      }
      const bodies = all('[id^="UniqueMessageBody"]');
      const messageBodies=bodies.length ? bodies : all('[role="document"][aria-label="Message body"]');
      if (messageBodies.length > 1) return result({error:"email_message_identity_ambiguous"});
      if (!messageBodies.length) return null;
      const id = decodeURIComponent(match[1]);
      if (expected.provider_selection_id !== id) return result({error:"email_message_identity_mismatch"});
      return result({ provider_message_id: expected.provider_message_id, provider_selection_id: expected.provider_selection_id,
        account_address: account, attachments: [], inventory_complete: false });
    }
    const identity = qqIdentity?.(document, expected);
    if (!identity) return null;
    const subject = all('.mail-detail-subject')[0], bodyHost = all('.mail-detail-content')[0];
    if (!subject || !bodyHost) return null;
    const body = bodyHost.shadowRoot?.querySelector('body') ?? bodyHost;
    if(expected.required_read_state && identity.read_state!==expected.required_read_state) return null;
    return result({ ...identity, account_address: account,
      subject: text(subject), body_text: text(body), body_html: body.innerHTML, attachments: [], inventory_complete: false });
  }
  return result({error:'email_page_contract_changed'});
}

// Failure-only diagnostics expose fixed counts/booleans, never page text or IDs.
export function inspectionDiagnostics(provider, phase, expected) {
  const visible = node => {
    if (!node?.isConnected) return false;
    const rect=node.getBoundingClientRect(),style=getComputedStyle(node);
    return rect.width>0&&rect.height>0&&style.display!=='none'&&style.visibility!=='hidden';
  };
  const all=selector=>Array.from(document.querySelectorAll(selector)).filter(visible);
  const text=node=>(node?.innerText??node?.textContent??'').trim();
  if(provider==='qq_mail'&&phase==='detail'){
    const selected=all('.mail-list-page-item.mail-item-selected[data-mailid]');
    const subject=document.querySelector('.mail-detail-subject'),body=document.querySelector('.mail-detail-content');
    const domSelected=Array.from(document.querySelectorAll('.mail-list-page-item.mail-item-selected[data-mailid]'));
    const target=Array.from(document.querySelectorAll('.mail-list-page-item[data-mailid]')).filter(node=>node.getAttribute('data-mailid')===expected.provider_message_id);
    const attrMatches=Array.from(document.querySelectorAll('[data-mailid], [mailid], [data-id], [id]')).filter(node=>Array.from(node.attributes??[]).some(attr=>attr.value===expected.provider_message_id));
    const route=new URL(location.href);
    const routeValues=[...route.searchParams.values(),...route.hash.split(/[/?&=]/u)];
    let decodedMatches=false;for(const value of routeValues){try{if(decodeURIComponent(decodeURIComponent(value))===expected.provider_message_id)decodedMatches=true;}catch{}}
    return {detail_identity_attr_count:attrMatches.length,decoded_route_target_matches:decodedMatches,selected_dom_count:domSelected.length,hidden_selection_matches:domSelected.length===1&&domSelected[0].getAttribute('data-mailid')===expected.provider_message_id,target_dom_count:target.length,target_visible:target.some(visible),route_target_matches:decodeURIComponent(location.hash).split(/[/?&=]/u).includes(expected.provider_message_id),selected_count:selected.length,selection_matches:selected.length===1&&selected[0].getAttribute('data-mailid')===expected.provider_message_id,
      subject_present:Boolean(subject),subject_matches:Boolean(subject)&&text(subject)===expected.subject,body_present:Boolean(body),body_visible:visible(body)};
  }
  if(provider==='gmail'&&phase==='detail'){
    const nodes=Array.from(document.querySelectorAll('[data-legacy-message-id]'));
    const target=nodes.filter(node=>node.getAttribute('data-legacy-message-id')===expected.provider_message_id);
    return {message_dom_count:nodes.length,target_dom_count:target.length,target_visible_count:target.filter(visible).length,
      collapsed_count:target.filter(node=>node.classList.contains('kv')).length,expanded_count:target.filter(node=>node.classList.contains('adn')).length,
      target_body_count:target.reduce((n,node)=>n+node.querySelectorAll('.a3s').length,0)};
  }
  if(provider==='gmail'&&phase==='list'){
    const rows=all('tr.zA');
    let query_matches=false;
    try{query_matches=location.hash.startsWith('#search/')&&decodeURIComponent(location.hash.slice(8)).replace(/\+/gu,' ').trim()===(expected.query??'in:inbox is:unread');}catch{}
    const tc=Array.from(document.querySelectorAll('.TC'));
    const emptyTexts=rows.length===0?all('.TC, .ae4').map(text):[];
    const labels={
      empty_en_conversations_found:/^No conversations found[.!]?$/u,
      empty_en_messages_match:/^No messages matched your search\.(?:\s|$)/u,
      empty_en_conversations_match:/^No conversations match your search[.!]?$/u,
      empty_zh_matching_mail:/^没有符合搜索条件的邮件[。.]?$/u,
      empty_zh_matching_conversations:/^没有符合搜索条件的会话[。.]?$/u,
      empty_zh_no_mail_found:/^未找到符合搜索条件的邮件[。.]?$/u,
      empty_zh_no_conversations_found:/^没有找到任何会话[。.]?$/u,
    };
    const matchingLeaves=rows.length===0?all('*').filter(node=>!node.children.length).slice(0,10000).filter(node=>Object.values(labels).some(pattern=>pattern.test(text(node)))):[];
    const generic=Object.fromEntries(Object.entries(labels).map(([key,pattern])=>[key,[...emptyTexts,...matchingLeaves.map(text)].some(value=>pattern.test(value))]));
    return {rows:rows.length,unread_rows:rows.filter(row=>row.classList.contains('zE')).length,query_matches,
      document_complete:document.readyState==='complete',document_interactive:document.readyState==='interactive',
      main_count:all('[role="main"]').length,tc_count:tc.length,tc_visible:tc.filter(visible).length,
      empty_label_outside_tc:matchingLeaves.some(node=>!node.closest('.TC, .ae4')),empty_label_in_main:matchingLeaves.some(node=>Boolean(node.closest('[role="main"]'))),
      input_query_matches:text({textContent:document.querySelector('input[name="q"]')?.value??''})===(expected.query??'in:inbox is:unread'),
      visible_progress:all('[role="progressbar"], progress').length>0,visible_busy:all('[aria-busy="true"]').length>0,
      empty_marker:emptyTexts.some(value=>/No conversations found|没有找到|未找到/u.test(value)),...generic};
  }
  if(phase==='menu'){
    const nodes=all('[role="menu"], [role="menuitem"], [role="menuitemradio"], .xmail-ui-panel-item');
    const commands=nodes.flatMap(node=>[text(node),...Array.from(node.querySelectorAll('*')).filter(child=>!child.children.length).map(text)]);
    return {menu_count:nodes.length,required_command_present:commands.some(command=>expected.required?command.includes(expected.required):expected.download_command?/Download|下载/u.test(command):expected.read_command?/^(?:Mark as (?:un)?read|标记为(?:未|已)读)$/u.test(command):expected.original_eml?/\beml\b/iu.test(command)&&!/[\r\n]|\bmsg\b/iu.test(command):true)};
  }
  if(phase==='list')return {rows:all(provider==='qq_mail'?'.mail-list-page-item[data-mailid]':'[role="option"][data-convid]').length,
    account_marker_present:provider==='qq_mail'?Boolean(text(document.querySelector('.frame-header .profile-user-info .user-email'))):undefined};
  return {};
}

export function warnInspectionFailure(provider, phase, code, observed = {}) {
  const record={event:'email_inspection_failed',provider:READ_PROVIDERS[provider]?provider:'unknown',
    phase:['list','detail','menu','account','filter'].includes(phase)?phase:'unknown',
    error_code:['email_page_contract_changed','email_provider_origin_invalid','email_message_identity_ambiguous','email_message_identity_mismatch','email_account_identity_mismatch','email_account_identity_unavailable'].includes(code)?code:'email_page_contract_changed'};
  for(const key of ['message_dom_count','target_visible_count','collapsed_count','expanded_count','target_body_count','detail_identity_attr_count','selected_count','selected_dom_count','target_dom_count','rows','unread_rows','menu_count','main_count','tc_count','tc_visible'])if(Number.isInteger(observed?.[key])&&observed[key]>=0&&observed[key]<=10000)record[key]=observed[key];
  for(const key of ['decoded_route_target_matches','hidden_selection_matches','target_visible','route_target_matches','selection_matches','subject_present','subject_matches','body_present','body_visible','query_matches','empty_marker','required_command_present','account_marker_present','document_complete','document_interactive','input_query_matches','visible_progress','visible_busy','empty_label_outside_tc','empty_label_in_main','empty_en_conversations_found','empty_en_messages_match','empty_en_conversations_match','empty_zh_matching_mail','empty_zh_matching_conversations','empty_zh_no_mail_found','empty_zh_no_conversations_found'])if(typeof observed?.[key]==='boolean')record[key]=observed[key];
  console.warn(JSON.stringify(record));
}

export async function inspect(tab, provider, phase, expected = {}) {
  const config = READ_PROVIDERS[provider];
  if(phase==='list')expected={...expected,required_account:true};
  const evidence = await tab.inspect(`async () => {
    if (!${JSON.stringify(config.origins)}.includes(location.origin)) return {url:location.href,error:'email_provider_origin_invalid'};
    const deadline=Date.now()+5000;let transitioning;
    do {
      const value=(${providerDOM.toString()})(${JSON.stringify(provider)},${JSON.stringify(phase)},${JSON.stringify(expected)}${provider==='qq_mail'&&phase==='detail'?`,(${qqMailDetailIdentity.toString()})`:''});
      if(value?.error==='email_message_identity_mismatch' && Date.now()<deadline){transitioning=value;await new Promise(resolve=>setTimeout(resolve,100));continue;}
      if(value) return value.error ? {...value,diagnostics:(${inspectionDiagnostics.toString()})(${JSON.stringify(provider)},${JSON.stringify(phase)},${JSON.stringify(expected)})} : value;
      await new Promise(resolve=>setTimeout(resolve,100));
    }while(Date.now()<deadline);
    return {url:location.href,error:transitioning?.error||'email_page_contract_changed',diagnostics:(${inspectionDiagnostics.toString()})(${JSON.stringify(provider)},${JSON.stringify(phase)},${JSON.stringify(expected)})};
  }`);
  if (!evidence?.result || evidence.result.url !== evidence.origin) throw fail("email_provider_origin_invalid");
  const parsed = new URL(evidence.origin);
  if (parsed.username || parsed.password || !config.origins.includes(parsed.origin)) throw fail("email_provider_origin_invalid");
  if (evidence.result.error) {
    warnInspectionFailure(provider,phase,evidence.result.error,evidence.result.diagnostics);
    if(provider==='qq_mail' && phase==='detail' && typeof tab.runReadCode==='function') {
      try {
        const detail=await tab.runReadCode(`async page=>page.evaluate(()=>(${qqMailDetailDiagnostics.toString()})(document,${JSON.stringify(expected)}))`);
        console.warn(JSON.stringify({event:'email_qq_detail_identity_failed',detail}));
      } catch {}
    }
    throw fail(evidence.result.error);
  }
  return evidence.result;
}

export async function openMessageMenu(tab, provider, id, individual = false) {
  if (provider === 'gmail') {
    await tab.runReadCode(`async page => {
      await page.locator(${JSON.stringify(`[data-legacy-message-id="${id}"] button[aria-label="More message options"]`)}).evaluate(node=>node.click());
      return true;
    }`);
  } else if (provider === 'outlook') await tab.click(`${individual?'#focused ':''}button[aria-label="More items"]`);
  else await tab.runReadCode(`async page => {
    const labels = ${JSON.stringify(QQ_MAIL_MORE_BUTTON_LABELS)};
    await page.locator('.mail-detail-basic-action-bar .ui-btn-text').evaluateAll((nodes, expected) => {
      const visible = node => {
        const rect = node.getBoundingClientRect();
        const style = getComputedStyle(node);
        return rect.width > 0 && rect.height > 0 && style.display !== 'none' && style.visibility !== 'hidden';
      };
      const button = nodes.find(node => expected.includes((node.innerText ?? node.textContent ?? '').trim()) && visible(node));
      if (!button) throw new Error('email_more_menu_button_not_found');
      (button.parentElement ?? button).click();
    }, labels);
    return true;
  }`);
}

export async function collectUnread(tab, provider, options = {}) {
  if (!READ_PROVIDERS[provider] || typeof options.onSelected !== 'function') throw fail("invalid_request");
  const pinned = options.pinned_message_id || options.pinned_selection_id;
  const recent = options.discovery_options?.lane === 'recent_inbound';
  const folder = options.folder ?? (provider==='gmail' && recent ? 'all' : 'inbox');
  const folderURL = folder !== 'inbox' ? provider === 'qq_mail' && (folder === 'sent' || /^qq:[1-9][0-9]{3,9}$/u.test(folder)) ? READ_PROVIDERS.qq_mail.url.replace('/list/1',`/list/${folder==='sent'?3:folder.slice(3)}`) :
    provider === 'outlook' && folder === 'sent' ? READ_PROVIDERS.outlook.url.replace('/inbox','/sentitems') :
    provider === 'outlook' && (folder === 'all' || folder.startsWith('outlook:')) ? READ_PROVIDERS.outlook.url :
    provider === 'gmail' ? READ_PROVIDERS.gmail.url.replace('#inbox',folder === 'sent' ? '#sent' : '#all') : null : null;
  const folderNeedsNavigation = Boolean(folderURL && folderURL !== READ_PROVIDERS[provider].url);
  let qqKey;
  if (folder !== 'inbox') {
    if (!folderURL) throw fail('email_pinned_message_unavailable');
    if (provider !== 'gmail' && folderNeedsNavigation) await tab.runReadCode(`async page=>{await page.goto(${JSON.stringify(folderURL)});return true;}`);
  }
  let outlookKey;
  if(provider==='qq_mail' && options.discovery_options) {
    qqKey=await prepareQQMailList(tab);
    if (folderNeedsNavigation) await tab.runReadCode(`async page=>{await page.goto(${JSON.stringify(folderURL)});return true;}`);
  }
  if(provider==='outlook') {
    if((pinned || recent) && (await inspect(tab,provider,'filter')).filtered)await tab.click('button:has-text("Clear filter")');
    outlookKey=await prepareOutlookList(tab);
    if (folderNeedsNavigation) await tab.runReadCode(`async page=>{await page.goto(${JSON.stringify(folderURL)});return true;}`);
  }
  let listed;
  if (provider === 'gmail') {
    listed = await gmailUnreadEvidence(tab, async () => {
      const scope=folder === 'sent' ? 'in:sent' : folder === 'all' ? '-in:trash -in:spam -in:drafts' : 'in:inbox';
      const query=recent ? `${scope} after:${Math.floor(Date.parse(options.discovery_options.interval_start)/1000)-1} before:${Math.ceil(Date.parse(options.discovery_options.interval_end)/1000)}` : pinned ? scope : `${scope} is:unread`;
      await tab.fill('input[name="q"]',query);
      await tab.press('Enter');
      return inspect(tab,provider,'list',{filtered:!pinned && !recent,query});
    },{folder,beforeSelect: folderNeedsNavigation && provider === 'gmail' ? async () => {
      await tab.runReadCode(`async page=>{await page.goto(${JSON.stringify(folderURL)});return true;}`);
    } : undefined});
  } else if (provider === 'outlook' && !pinned && !recent) {
    if(!(await inspect(tab,provider,'filter')).filtered){
      await tab.click('button[aria-label="Filter"], button[aria-label="筛选器"]');
      await tab.click('[role="menuitemradio"][title="Unread"], [role="menuitemradio"][title="未读"]');
    }
    listed = await inspect(tab,provider,'list',{filtered:true});
  } else listed = await inspect(tab,provider,'list');
  if(provider==='outlook')listed=await outlookListEvidence(tab,outlookKey,listed);
  if(qqKey)listed=await qqMailListEvidence(tab,qqKey,listed,options.discovery_options,()=>inspect(tab,provider,'list'));
  if(provider==='outlook' && options.pinned_message_id && options.account_address && options.folder!=='sent'){const recovered=await recoverOutlookTarget(tab,options,listed);if(recovered)listed=recovered;}
  if (options.inventory) return listed;
  if (options.discovery) return discoveryResult(tab, provider, listed, options.discovery_options);
  return collectListed(tab, provider, options, listed);
}

async function collectListed(tab, provider, options, listed) {
  const pinned = options.pinned_message_id || options.pinned_selection_id;
  const folder = options.folder ?? 'inbox';
  if (listed.empty && !pinned) return {status:'empty'};
  if (!pinned && provider !== 'qq_mail' && !listed.rows.some(row=>row.single_unread_proven)) throw fail('email_message_identity_ambiguous');
  const matches = row => pinned ?
    (!options.pinned_message_id || row.provider_message_id === options.pinned_message_id || provider==='gmail' && row.members?.some(member=>member.provider_message_id===options.pinned_message_id && !member.draft) || provider==='outlook' && (row.inventory_complete || row.network_member_proven) &&
      row.members?.some(member=>member.provider_message_id===options.pinned_message_id && !member.draft && (member.local || folder==='all'))) &&
      (!options.pinned_selection_id || row.provider_selection_id === options.pinned_selection_id || provider==='gmail' && row.provider_thread_id===options.pinned_selection_id) : row.unread && (provider==='qq_mail' || row.single_unread_proven);
  let selected = listed.rows.find(matches);
  if (provider==='outlook' && selected && options.pinned_message_id && !selected.single_message_proven && (selected.inventory_complete || selected.network_member_proven)) {
    selected={...selected,provider_message_id:options.pinned_message_id,individual_message_proven:true};
  }
  if (provider==='gmail' && selected && options.pinned_message_id && selected.members?.some(member=>member.provider_message_id===options.pinned_message_id && !member.draft)) {
    selected={...selected,provider_message_id:options.pinned_message_id,provider_selection_id:options.pinned_selection_id,individual_message_proven:true};
  }
  if (provider === 'qq_mail' && !selected && !listed.empty) {
    const seen = new Set(listed.rows.map(row=>row.provider_message_id));
    for(let page=0;page<10 && !selected;page++) {
      const moved=await tab.runReadCode(`async page => page.evaluate(() => {
        const list=document.querySelector('.mail-list-body .ui-float-scroll-body');
        if(!list) return false;
        const before=list.scrollTop;
        list.scrollTop+=list.clientHeight;
        return list.scrollTop>before;
      })`);
      if(!moved) break;
      const next=await inspect(tab,provider,'list',{after_ids:[...seen]});
      if(next.account_address!==listed.account_address) throw fail("email_account_identity_mismatch");
      listed=next;
      for(const row of listed.rows) seen.add(row.provider_message_id);
      selected=listed.rows.find(matches);
      if(!pinned && !selected && Number.isInteger(listed.total_count) && seen.size===listed.total_count) return {status:'empty'};
    }
  }
  if (!selected && provider === 'gmail' && options.pinned_message_id && options.account_address && /^[a-f0-9]{1,32}$/u.test(options.pinned_message_id)) {
    // The route and individual message marker are already used by native
    // capture. A missing list row must not hide an explicitly indexed source.
    await tab.runReadCode(`async page=>{await page.goto(${JSON.stringify(`${READ_PROVIDERS.gmail.url.split('#')[0]}#all/${encodeURIComponent(options.pinned_message_id)}`)});return true;}`);
    const detail = await inspect(tab,provider,'detail',{provider_message_id:options.pinned_message_id});
    if (detail.account_address?.toLowerCase() !== options.account_address.toLowerCase()) throw fail('email_account_identity_mismatch');
    selected = {provider_message_id:options.pinned_message_id,provider_selection_id:options.pinned_selection_id,
      individual_message_proven:true};
    listed = {...listed,account_address:detail.account_address};
  }
  if (!selected) {
    if (pinned) throw fail("email_pinned_message_unavailable");
    throw fail("email_page_contract_changed");
  }
  let account = listed.account_address;
  if (!account && provider === 'outlook') {
    await tab.click('#O365_MainLink_MePhoto, #O365_MeFlexPane_ButtonID, [data-testid="mectrl_headerPicture"]');
    account = (await inspect(tab,provider,'account')).account_address;
    await tab.press('Escape');
  }
  if (!account) throw fail("email_account_identity_unavailable");
  if (options.account_address && account.toLowerCase() !== options.account_address.toLowerCase()) throw fail('email_account_identity_mismatch');
  selected = {...selected,account_address:account,...(options.folder ? {folder} : {})};
  if(provider==='gmail'&&!(selected.single_message_proven || selected.individual_message_proven))throw fail('email_message_identity_ambiguous');
  if(provider==='outlook' && (!(selected.single_message_proven || selected.individual_message_proven) || !selected.provider_message_id))throw fail('email_message_identity_ambiguous');
  if(provider==='outlook' && options.pinned_message_id && selected.provider_message_id!==options.pinned_message_id)throw fail('email_message_identity_mismatch');
  for (const id of [selected.provider_message_id,selected.provider_selection_id].filter(Boolean)) {
    if (typeof id !== 'string' || !/^[A-Za-z0-9_+=:.\/~\-]{1,1024}$/u.test(id)) throw fail("email_message_identity_invalid");
  }
  await options.onSelected(selected);
  if (options.capture_required !== false) {
    const target = {account_address:account,provider_message_id:selected.provider_message_id};
    const original = options.force_native ? null : await prepareNetworkOriginal(tab,provider,target);
    if (original?.selector && original.provider_message_id===target.provider_message_id && original.account_address===account.toLowerCase()) {
      return {...selected,original,network_original:true,read_state:'unknown'};
    }
  }
  if (provider === 'gmail') {
    if (options.pinned_message_id) {
      await tab.runReadCode(`async page => { await page.goto(${JSON.stringify(`${READ_PROVIDERS.gmail.url.split('#')[0]}#all/${encodeURIComponent(selected.provider_message_id)}`)}); return true; }`);
    } else await tab.click(`tr.zA:has([data-legacy-last-message-id="${selected.provider_message_id}"]):visible`);
  } else if (provider === 'outlook') {
    if(selected.network_member_proven)await prepareNetworkFolder(tab,{account_address:account,folder});
    await tab.click(`[role="option"][data-convid="${selected.provider_selection_id}"]:visible`);
    if (selected.individual_message_proven) {
      await tab.runReadCode(`async page=>{await page.locator(${JSON.stringify(`[role="option"][data-convid="${selected.provider_selection_id}"] button[aria-expanded][aria-label="展开对话"], [role="option"][data-convid="${selected.provider_selection_id}"] button[aria-expanded][aria-label="Expand conversation"]`)}).evaluate(node=>{if(node.getAttribute('aria-expanded')!=='true')node.click();});return true;}`);
      await tab.click(`[id="${selected.provider_message_id}"] [role="listitem"]:visible`);
    }
  }
  else await tab.click(`.mail-list-page-item[data-mailid="${selected.provider_message_id}"]:visible`);
  if(provider==='gmail' && options.pinned_message_id) {
    // Native threads may initially omit older folded members entirely. Expand
    // only the observed thread control, then still require the exact member ID.
    const folded=await tab.runReadCode(`async page=>page.evaluate(id=>{
      const visible=node=>Boolean(node.getClientRects().length);
      const found=[...document.querySelectorAll('[data-legacy-message-id]')].some(node=>node.getAttribute('data-legacy-message-id')===id);
      const controls=[...document.querySelectorAll('[aria-label="Expand all"], [data-tooltip="Expand all"], [aria-label="全部展开"], [data-tooltip="全部展开"]')].filter(visible);
      return !found && controls.length===1;
    },${JSON.stringify(selected.provider_message_id)})`);
    if(folded===true)await tab.click('[aria-label="Expand all"]:visible, [data-tooltip="Expand all"]:visible, [aria-label="全部展开"]:visible, [data-tooltip="全部展开"]:visible');
  }
  const detail = await inspect(tab,provider,'detail',selected);
  if (detail.account_address && detail.account_address.toLowerCase() !== account.toLowerCase()) throw fail("email_account_identity_mismatch");
  const message = {...selected,...detail,account_address:account};
  if (provider === 'outlook') await options.onSelected(message);
  if (options.capture_required === false) return message;
  await openMessageMenu(tab,provider,message.provider_message_id,message.individual_message_proven);
  const commands = (await inspect(tab,provider,'menu',provider==='outlook'?{download_command:true}:{required:provider==='gmail'?'Download message':'Export as eml file'})).commands;
  if (commands.some(command=>/^(?:Mark as unread|标记为未读)$/u.test(command))) message.read_state='read';
  else if (commands.some(command=>/^(?:Mark as read|标记为已读)$/u.test(command))) message.read_state='unread';
  if (provider === 'gmail' && commands.some(command=>command==='Download message')) message.original = {selector:'[role="menu"] :text-is("Download message"):visible'};
  else if (provider === 'outlook' && commands.some(command=>/Download|下载/u.test(command))) {
    const label=commands.some(command=>/下载/u.test(command))?'下载':'Download';
    await tab.runReadCode(`async page=>{await page.locator('[role="menu"] [role="button"]:has-text(${JSON.stringify(label)}):visible').evaluate(node=>node.click());return true;}`);
    const formats=(await inspect(tab,provider,'menu',{original_eml:true})).commands;
    const eml=[...new Set(formats.filter(command=>/\beml\b/iu.test(command)&&!/[\r\n]|\bmsg\b/iu.test(command)))];
    if(eml.length!==1) throw fail('email_original_download_unavailable');
    message.original={selector:`[role="menu"] :text-is(${JSON.stringify(eml[0])}):visible`};
  }
  else if (provider === 'qq_mail' && commands.some(command=>command==='Export as eml file')) message.original = {selector:'.xmail-ui-panel-item:has-text("Export as eml file"):visible'};
  else throw fail("email_original_download_unavailable");
  // Arm after native navigation and menus, immediately before the download.
  // A slow UI or a replaced document must not expire/lose the learning window.
  await armNetworkOriginal(tab,provider,{account_address:account,provider_message_id:message.provider_message_id});
  return message;
}

export async function markRead(tab, provider, message) {
  if (message.network_original) return 'unknown';
  const identity = { provider_message_id:message.provider_message_id, provider_selection_id:message.provider_selection_id, subject:message.subject, evidence_key:message.evidence_key,network_member_proven:message.network_member_proven,individual_message_proven:message.individual_message_proven };
  const detail = await inspect(tab,provider,'detail',identity);
  if (detail.provider_message_id !== message.provider_message_id) throw fail("email_message_identity_mismatch");
  if (detail.account_address && detail.account_address.toLowerCase()!==message.account_address.toLowerCase()) throw fail("email_account_identity_mismatch");
  if (provider === 'qq_mail' && detail.read_state==='read') return 'read';
  await openMessageMenu(tab,provider,message.provider_message_id,message.individual_message_proven);
  let commands = (await inspect(tab,provider,'menu',{read_command:true})).commands;
  if (commands.some(command=>/Mark as unread|标记为未读/u.test(command))) return 'read';
  if (!commands.some(command=>/Mark as read|标记为已读/u.test(command))) return 'unknown';
  const label=commands.some(command=>/标记为已读/u.test(command))?'标记为已读':'Mark as read';
  const selector=provider==='gmail' ? '[role="menu"] :text-is("Mark as read"):visible' : provider==='qq_mail' ? '.xmail-ui-panel-item:has-text("Mark as read"):visible' : `[role="menuitem"]:has-text(${JSON.stringify(label)}):visible`;
  await tab.runReadCode(`async page=>{await page.locator(${JSON.stringify(selector)}).evaluate(node=>{
    for(const type of ['mousedown','mouseup','click']) node.dispatchEvent(new MouseEvent(type,{bubbles:true,cancelable:true,view:window,button:0,buttons:type==='mousedown'?1:0}));
  });return true;}`);
  if(provider==='qq_mail') return (await inspect(tab,provider,'detail',{...identity,required_read_state:'read'})).read_state;
  await openMessageMenu(tab,provider,message.provider_message_id,message.individual_message_proven);
  commands = (await inspect(tab,provider,'menu',{read_command:true})).commands;
  return commands.some(command=>/Mark as unread|标记为未读/u.test(command)) ? 'read' : 'unknown';
}

export const readEmail = (input,runtime,provider) => captureUnread(input,runtime,provider,{collectUnread,markRead});
export const readQQMail = (input,runtime) => readEmail(input,runtime,'qq_mail');
export const readOutlook = (input,runtime) => readEmail(input,runtime,'outlook');
export const readGmail = (input,runtime) => readEmail(input,runtime,'gmail');
export const markEmailRead = (input,runtime,provider) => markCapturedRead(input,runtime,provider,{collectUnread,markRead});

// Bounded discovery never opens a message or changes its read state. Coverage
// deliberately describes loaded rows, not whole-mailbox synchronization.
async function discoveryResult(tab, provider, listed, options, includeMembers = false) {
  let account = listed.account_address;
  if (!account && provider === 'outlook') {
    await tab.click('#O365_MainLink_MePhoto, #O365_MeFlexPane_ButtonID, [data-testid="mectrl_headerPicture"]');
    account = (await inspect(tab, provider, 'account')).account_address;
    await tab.press('Escape');
  }
  if (!account) throw fail('email_account_identity_unavailable');
  validateMailTarget({account_address:account,provider_message_id:'check',provider_selection_id:'check'});
  if (options && account.toLowerCase() !== options.account_address.toLowerCase()) throw fail('email_account_identity_mismatch');
  const candidates = [], seen = new Set();
  let unsupported = 0, candidateBytes = 0;
  const recent = options?.lane === 'recent_inbound';
  const receivedAt = row => provider==='outlook'?row.last_delivery_time:provider==='qq_mail' && listed.receipt_evidence?row.received_at:provider==='gmail'?row.members?.filter(member=>!member.sent&&!member.draft&&member.received_at).map(member=>member.received_at).sort().at(-1):null;
  const withinDate = value=>value && Date.parse(value) >= Date.parse(options.interval_start) && Date.parse(value) < Date.parse(options.interval_end);
  const withinInterval = row => provider==='gmail'?row.members?.some(member=>!member.sent&&!member.draft&&withinDate(member.received_at)):withinDate(receivedAt(row));
  for (const row of listed.rows) {
    if (recent ? !withinInterval(row) : !row.unread) continue;
    const members = includeMembers && (provider === 'gmail' || provider === 'outlook' && row.inventory_complete) && row.members?.length ? row.members.filter(member=>!member.draft) : null;
    if (!members && provider !== 'qq_mail' && !(recent ? row.single_message_proven : row.single_unread_proven)) { unsupported++; continue; }
    for (const member of members ?? [null]) {
    // A recent thread does not make its historical members recent. Require
    // individual receipt evidence before downloading any grouped original.
    if (recent && member) {
      const receipt = member.received_at ?? (members.length === 1 ? receivedAt(row) : null);
      if (!receipt || !Number.isFinite(Date.parse(receipt))) { unsupported++; continue; }
      if (!withinDate(receipt)) continue;
    }
    const target = {account_address:account.toLowerCase(), provider_message_id:member?.provider_message_id ?? row.provider_message_id,
      provider_selection_id:options && provider==='gmail' ? row.provider_thread_id : row.provider_selection_id ?? row.provider_message_id,
      ...(options ? {folder:member ? (provider==='gmail' ? member.inbox?'inbox':member.sent?'sent':'all' : member.local ? row.folder ?? listed.folder_scope ?? 'inbox' : 'all') : row.folder ?? listed.folder_scope ?? (provider==='gmail' && recent?'all':'inbox'),provider_thread_id:row.provider_thread_id ?? row.provider_selection_id ?? row.provider_message_id} : {})};
    try { validateMailTarget(target); } catch { unsupported++; continue; }
    if (seen.has(target.provider_message_id)) throw fail('email_message_identity_ambiguous');
    seen.add(target.provider_message_id);
    const size = Buffer.byteLength(JSON.stringify(target));
    if (options || candidates.length < 100 && candidateBytes + size <= 48 << 10) { candidates.push(target); candidateBytes += size; }
    }
  }
  if (options) {
    const threads = listed.rows.filter(row=>(recent ? withinInterval(row) : row.unread) && row.provider_selection_id).map(row=>({
      account_address:account.toLowerCase(),provider_thread_id:row.provider_thread_id ?? row.provider_selection_id,
      provider_selection_id:provider==='gmail' ? row.provider_thread_id : row.provider_selection_id,folder:row.folder ?? listed.folder_scope ?? (provider==='gmail' && recent?'all':'inbox')})).filter(thread=>{
        try {validateMailTarget({...thread,provider_message_id:thread.provider_selection_id});return true;}catch{return false;}
      });
    // The observed list contracts do not yet prove receipt-time ordering or
    // folder-wide continuation. In particular RFC Date must never substitute
    // for a received boundary and admit old read mail at activation.
    if (recent && provider === 'qq_mail' && !listed.receipt_evidence) return {schema_version:1,provider,status:'partial',account_address:account.toLowerCase(),candidates:[],
      coverage:{scope:'inbox_loaded',lane:options.lane,scan_complete:false,scanned_rows:includeMembers?Math.max(listed.rows.length,candidates.length+unsupported):listed.rows.length,
        unsupported_rows:listed.rows.length,limited:true,reason:'receipt_order_and_folder_scope_unqualified'},observed_at:new Date().toISOString()};
    const digest = crypto.createHash('sha256').update(JSON.stringify([provider,account.toLowerCase(),options.lane,
      options.interval_start,options.interval_end,candidates.map(row=>row.provider_message_id),threads.map(row=>row.provider_thread_id)])).digest('hex');
    const [prior,offsetText] = listed.discovery_position ? [listed.discovery_position.s,listed.discovery_position.o] : options.continuation.split(':');
    const offset = prior === digest ? Number(offsetText) || 0 : 0;
    let size=0,end=offset;
    for(;end<Math.max(candidates.length,threads.length)&&end<offset+options.limit;end++){
      const itemBytes=Buffer.byteLength(JSON.stringify([candidates[end],threads[end]]));
      if(size+itemBytes>40<<10)break;size+=itemBytes;
    }
    const page = candidates.slice(offset,end);
    const threadPage = threads.slice(offset,end);
    const nextOffset=offset+Math.max(page.length,threadPage.length);
    const more=nextOffset < Math.max(candidates.length,threads.length);
    const continuation = listed.discovery_position ? qqDiscoveryContinuation(listed.discovery_position,digest,nextOffset,more,listed.discovery_folders) : more ? `${digest}:${nextOffset}` : '';
    const complete = !recent && listed.empty===true && !listed.discovery_position;
    const oldest = Math.min(...listed.rows.map(row=>Date.parse(receivedAt(row))).filter(Number.isFinite));
    return {schema_version:1,provider,status:complete ? 'empty' : 'partial',account_address:account.toLowerCase(),candidates:page,threads:threadPage,
      coverage:{scope:recent?(provider==='gmail'?'inbound_received':'inbox_loaded'):'inbox_unread',lane:options.lane,scan_complete:complete,scanned_rows:includeMembers?Math.max(listed.rows.length,candidates.length+unsupported):listed.rows.length,
        unsupported_rows:unsupported,limited:!complete,...(continuation ? {continuation} : {}),
        ...(recent && Number.isFinite(oldest)?{oldest_observed_at:new Date(oldest).toISOString(),ordering:provider==='qq_mail'?'qq_totime':provider==='gmail'?'gmail_internal_received':'outlook_last_delivery'}:{}),
        ...(complete ? {} : {reason:listed.discovery_position?'native_folder_scan_partial':recent?'folder_scope_and_pagination_unqualified':'loaded_rows_only'})},observed_at:new Date().toISOString()};
  }
  return {schema_version:1, provider, status:listed.empty ? 'empty' : 'listed', account_address:account.toLowerCase(), candidates,
    coverage:{scope:'inbox_unread', scan_complete:listed.empty === true,
      scanned_rows:includeMembers?Math.max(listed.rows.length,candidates.length+unsupported):listed.rows.length, unsupported_rows:unsupported, limited:listed.empty !== true},
    observed_at:new Date().toISOString()};
}

export async function discoverEmail(input, runtime, provider) {
  validateCaptureInput(input, provider);
  if (input.operation !== 'discover') throw fail('invalid_request');
  return runtime.withReadTab(async tab => {
    const listed=await collectUnread(tab,provider,{inventory:true,discovery_options:input.discovery,onSelected:async()=>{throw fail('invalid_request');}});
    if(provider==='outlook'&&input.discovery?.lane==='recent_inbound')await warmOutlookNetwork(tab,input.discovery.account_address);
    const network=await networkListPage(tab,provider,input.discovery,listed);
    return network?.discovery??discoveryResult(tab,provider,listed,input.discovery);
  });
}

export async function enumerateThread(input,runtime,provider) {
  validateCaptureInput(input,provider);
  if (input.operation !== 'enumerate_thread') throw fail('invalid_request');
  return runtime.withReadTab(async tab => {
    const listed = await collectUnread(tab,provider,{inventory:true,folder:input.thread.folder,
      pinned_selection_id:input.thread.provider_selection_id,onSelected:async()=>{throw fail('invalid_request');}});
    if (listed.account_address?.toLowerCase() !== input.thread.account_address.toLowerCase()) throw fail('email_account_identity_mismatch');
    const rows = listed.rows.filter(row=>row.provider_selection_id===input.thread.provider_selection_id || provider==='gmail' && row.provider_thread_id===input.thread.provider_selection_id);
    const members = [];
    let directInventory = false;
    if (provider === 'gmail' && rows.length === 0 && /^[a-f0-9]{1,32}$/u.test(input.thread.provider_selection_id)) {
      await tab.runReadCode(`async page=>{await page.goto(${JSON.stringify(`${READ_PROVIDERS.gmail.url.split('#')[0]}#all/${encodeURIComponent(input.thread.provider_selection_id)}`)});return true;}`);
      const detail = await inspect(tab,provider,'thread_members',{provider_message_id:input.thread.provider_selection_id});
      if (detail.account_address?.toLowerCase() !== input.thread.account_address.toLowerCase()) throw fail('email_account_identity_mismatch');
      for (const id of detail.ids) members.push({target:{account_address:input.thread.account_address,provider_message_id:id,
        provider_selection_id:input.thread.provider_selection_id,provider_thread_id:input.thread.provider_thread_id,folder:'all'},
        direction:'unknown',draft:false,read_state:'unknown'});
      directInventory = true;
    }
    if (provider === 'gmail' && rows.length === 1 && rows[0].members?.length) {
      for (const member of rows[0].members) {
        const folder=member.inbox?'inbox':member.sent?'sent':'all';
        members.push({target:{account_address:input.thread.account_address,provider_message_id:member.provider_message_id,
          provider_selection_id:input.thread.provider_selection_id,provider_thread_id:input.thread.provider_thread_id,folder},
          direction:member.inbox?'inbound':member.sent?'outbound':'unknown',draft:member.draft,read_state:member.unread?'unread':'read',...(member.received_at?{received_at:member.received_at}:{})});
      }
    } else if (provider === 'outlook' && rows.length === 1 && rows[0].inventory_complete) {
      for (const member of rows[0].members) {
        const folder = member.local ? input.thread.folder : 'all';
        members.push({target:{account_address:input.thread.account_address,provider_message_id:member.provider_message_id,
          provider_selection_id:input.thread.provider_selection_id,provider_thread_id:input.thread.provider_thread_id,folder},
          draft:member.draft,direction:member.local ? folder==='sent'?'outbound':'inbound' : 'unknown',
          read_state:rows[0].global_unread_count===0?'read':'unknown',...(rows[0].members.length===1&&rows[0].last_delivery_time?{received_at:rows[0].last_delivery_time}:{})});
      }
    } else if (rows.length === 1 && (provider === 'qq_mail' || rows[0].single_message_proven)) {
      const row = rows[0];
      const target = {account_address:input.thread.account_address,provider_message_id:row.provider_message_id,
        provider_selection_id:input.thread.provider_selection_id,provider_thread_id:input.thread.provider_thread_id,folder:input.thread.folder};
      validateMailTarget(target);
      members.push({target,direction:input.thread.folder==='sent'?'outbound':'inbound',draft:false,read_state:row.unread?'unread':'read',...((row.received_at||row.last_delivery_time)?{received_at:row.received_at||row.last_delivery_time}:{})});
    }
    const digest = crypto.createHash('sha256').update(JSON.stringify([provider,input.thread,members.map(member=>member.target.provider_message_id)])).digest('hex');
    const [prior,offsetText] = input.continuation.split(':');
    const offset = prior === digest ? Number(offsetText) || 0 : 0;
    const page = members.slice(offset,offset+input.limit);
    const continuation = offset+page.length < members.length ? `${digest}:${offset+page.length}` : '';
    const complete = !directInventory && provider !== 'qq_mail' && members.length>0 && !continuation && (rows[0].single_message_proven || rows[0].inventory_complete);
    return {schema_version:1,provider,status:complete?'complete_for_observation':'partial',thread:input.thread,members:page,
      coverage:{scope:'thread',scan_complete:complete,scanned_rows:Math.max(rows.length,members.length),unsupported_rows:members.length?0:rows.length,
        limited:!complete,...(continuation?{continuation}:{}),...(complete?{}:{reason:continuation?'batch_limit':directInventory?'rendered_thread_inventory_partial':provider==='qq_mail'?'reply_relationships_unqualified':'individual_thread_members_unqualified'})},observed_at:new Date().toISOString()};
  });
}


export const collectEmailPage = (input, runtime, provider) => capturePage(input, runtime, provider, {
  discover: async (tab, discoveryOptions) => {
    const listed = await collectUnread(tab, provider, {inventory:true,discovery_options:discoveryOptions,onSelected:async()=>{throw fail('invalid_request');}});
    if(provider==='outlook'&&discoveryOptions.lane==='recent_inbound')await warmOutlookNetwork(tab,discoveryOptions.account_address);
    const network = await networkListPage(tab,provider,discoveryOptions,listed);
    if (network) return network;
    const discovery = await discoveryResult(tab,provider,listed,discoveryOptions,true);
    // Freeze this bounded observation until every acknowledged sub-page drains;
    // opening one conversation can remove all of its members from unread search.
    const selected = {...listed,account_address:discovery.account_address};
    if (provider === 'qq_mail' && discovery.candidates.length) {
      await tab.runReadCode(`async page=>page.evaluate(()=>{const list=document.querySelector('.mail-list-body .ui-float-scroll-body');if(list)list.scrollTop=0;return true;})`);
    }
    return {discovery,listed:selected};
  },
  continuePage: async (listed, options, prior) => {
    const continuation = prior.coverage?.continuation;
    if (!continuation) return null;
    if (provider === 'qq_mail') {
      const position = qqDiscoveryPosition({...options,continuation});
      const initial = listed.discovery_position;
      if (!initial || position.b !== initial.b || position.f !== initial.f || position.d !== initial.d || !position.s || position.o < 1) return null;
      listed = {...listed,discovery_position:position};
    } else if (!/^[a-f0-9]{64}:[1-9][0-9]{0,3}$/u.test(continuation)) return null;
    const discovery_options = {...options,continuation};
    const discovery = await discoveryResult(null,provider,listed,discovery_options,true);
    if (!discovery.candidates.length) return null;
    return {listed,discovery,discovery_options};
  },
  collect: async (tab, sameProvider, options, listed, recovering) => {
    // Crash recovery may need to locate a saved target in a new page. The live
    // path only rereads current row identity; it never initializes/reloads the list.
    if (recovering) return collectUnread(tab,sameProvider,options);
    if (listed.network_reader && ['gmail','outlook'].includes(sameProvider)) return collectListed(tab,sameProvider,options,listed);
    const visible = await inspect(tab,sameProvider,'list');
    if (visible.account_address && visible.account_address.toLowerCase() !== options.account_address?.toLowerCase()) throw fail('email_account_identity_mismatch');
    const rows = visible.rows.map(row => {
      const prior = listed.rows.find(saved => (saved.provider_selection_id ?? saved.provider_message_id) === (row.provider_selection_id ?? row.provider_message_id));
      return {...prior,...row,
        ...(prior?.members ? {members:prior.members,inventory_complete:prior.inventory_complete} : {}),
        ...(prior?.single_message_proven ? {single_message_proven:true} : {}),
        ...(prior?.provider_message_id ? {provider_message_id:prior.provider_message_id} : {})};
    });
    if (sameProvider === 'gmail') {
      for (const saved of listed.rows) if (!rows.some(row=>row.provider_thread_id === saved.provider_thread_id)) rows.push(saved);
    }
    return collectListed(tab,sameProvider,options,{...visible,rows});
  },
  restore: async (tab, sameProvider, listed) => {
    const url = new URL(listed.url);
    if (!READ_PROVIDERS[sameProvider].origins.includes(url.origin) || url.username || url.password) throw fail('email_provider_origin_invalid');
    await tab.press('Escape');
    if(listed.network_reader)return;
    const restored = await tab.runReadCode(`async page=>{const expected=${JSON.stringify(listed.url)};if(page.url()!==expected)await page.goBack({waitUntil:'domcontentloaded'});return page.url()===expected;}`);
    if (!restored) throw fail('email_page_contract_changed');
  },
  markRead,
});
