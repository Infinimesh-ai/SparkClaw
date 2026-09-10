// Qualified against QQ's current full-screen reader: both visible detail hosts
// carry a React DOM fiber whose direct parent receives the active mail record.
// Never search global caches, ancestor stores or arbitrary nested matching IDs.
export function qqMailDetailIdentity(document, expected) {
  if(!expected||typeof expected.provider_message_id!=='string'||!/^[A-Za-z0-9_+=:.\/~\-]{1,1024}$/u.test(expected.provider_message_id)||typeof expected.subject!=='string')return null;
  const visible=node=>{
    if(!node||!node.getClientRects().length)return null;
    const style=document.defaultView?.getComputedStyle?.(node);
    return !style||style.display!=='none'&&style.visibility!=='hidden';
  };
  const subjects=Array.from(document.querySelectorAll('.mail-detail-subject')).filter(visible);
  const bodies=Array.from(document.querySelectorAll('.mail-detail-content')).filter(visible);
  if(subjects.length!==1||bodies.length!==1)return null;
  const subject=subjects[0],body=bodies[0];
  if((subject.innerText??subject.textContent??'').trim()!==expected.subject)return null;
  const field=(record,name)=>{
    const keys=Object.keys(record).filter(key=>key.toLowerCase()===name);
    return keys.length===1?record[keys[0]]:undefined;
  };
  const identity=node=>{
    let keys;try{keys=Object.getOwnPropertyNames(node).filter(key=>/^__(?:reactInternalInstance|reactFiber)\$/u.test(key));}catch{return null;}
    if(keys.length!==1)return null;
    try {
      const fiber=node[keys[0]],parent=fiber?.return,mail=parent?.memoizedProps?.mail;
      if(!mail||typeof mail!=='object'||mail.id!==expected.provider_message_id)return null;
      // Refuse a pending transition to another message instead of accepting
      // stale rendered props while the detail reader is being replaced.
      const pending=parent.pendingProps?.mail;
      if(pending&&(typeof pending!=='object'||pending.id!==expected.provider_message_id))return null;
      if(field(mail,'isdetail')!==true||field(mail,'isfake')!==false||field(mail,'isdraft')!==false||field(mail,'issessionmail')!==false)return null;
      const unread=field(mail,'isunread');
      if(typeof unread!=='boolean')return null;
      // The UI's opaque messageId is not an RFC Message-ID. Only its actual
      // provider mail.id proves selection; native EML supplies RFC headers.
      return {unread};
    }catch{return null;}
  };
  const subjectMail=identity(subject),bodyMail=identity(body);
  if(!subjectMail||!bodyMail||subjectMail.unread!==bodyMail.unread)return null;
  const readState=subjectMail.unread?'unread':'read';
  if(expected.required_read_state&&expected.required_read_state!==readState)return null;
  return {provider_message_id:expected.provider_message_id,read_state:readState};
}

// Failure-only gate diagnostics: fixed counts and booleans, never mail values.
export function qqMailDetailDiagnostics(document,expected) {
  const visible=node=>{
    if(!node||!node.getClientRects().length)return false;
    const style=document.defaultView?.getComputedStyle?.(node);
    return !style||style.display!=='none'&&style.visibility!=='hidden';
  };
  const subjects=Array.from(document.querySelectorAll('.mail-detail-subject')).filter(visible);
  const bodies=Array.from(document.querySelectorAll('.mail-detail-content')).filter(visible);
  const result={subject_count:Math.min(subjects.length,100),body_count:Math.min(bodies.length,100),subject_matches:subjects.length===1&&(subjects[0].innerText??subjects[0].textContent??'').trim()===expected?.subject,roots:[],native_field_equal:false,unread_equal:false};
  const values=[];
  for(const [root,nodes] of [['subject',subjects],['body',bodies]]){
    const out={root,unique:nodes.length===1,marker_count:0,mail_present:false,id_matches:false,pending_present:false,pending_matches:false,detail_true:false,fake_false:false,draft_false:false,session_false:false,unread_boolean:false,unread_true:false,native_field_string:false,native_field_bounded:false,required_read_matches:false,access_error:false};
    result.roots.push(out);if(nodes.length!==1){values.push(null);continue;}
    try{
      const keys=Object.getOwnPropertyNames(nodes[0]).filter(key=>/^__(?:reactInternalInstance|reactFiber)\$/u.test(key));out.marker_count=Math.min(keys.length,100);
      if(keys.length!==1){values.push(null);continue;}
      const parent=nodes[0][keys[0]]?.return,mail=parent?.memoizedProps?.mail,pending=parent?.pendingProps?.mail;
      out.mail_present=Boolean(mail&&typeof mail==='object');out.pending_present=Boolean(pending);out.pending_matches=!pending||typeof pending==='object'&&pending.id===expected?.provider_message_id;
      if(!out.mail_present){values.push(null);continue;}
      const field=name=>{const names=Object.keys(mail).filter(key=>key.toLowerCase()===name);return names.length===1?mail[names[0]]:undefined;};
      out.id_matches=mail.id===expected?.provider_message_id;out.detail_true=field('isdetail')===true;out.fake_false=field('isfake')===false;out.draft_false=field('isdraft')===false;out.session_false=field('issessionmail')===false;
      const unread=field('isunread'),rfc=field('messageid');out.unread_boolean=typeof unread==='boolean';out.unread_true=unread===true;out.native_field_string=typeof rfc==='string';out.native_field_bounded=out.native_field_string&&rfc.length>0&&rfc.length<=998;

      out.required_read_matches=!expected?.required_read_state||out.unread_boolean&&expected.required_read_state===(unread?'unread':'read');values.push({rfc,unread});
    }catch{out.access_error=true;values.push(null);}
  }
  if(values.length===2&&values.every(Boolean)){result.native_field_equal=typeof values[0].rfc==='string'&&values[0].rfc===values[1].rfc;result.unread_equal=typeof values[0].unread==='boolean'&&values[0].unread===values[1].unread;}
  return result;
}
