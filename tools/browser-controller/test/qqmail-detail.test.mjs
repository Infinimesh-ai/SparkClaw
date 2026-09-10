import assert from 'node:assert/strict';
import test from 'node:test';
import {qqMailDetailIdentity,qqMailDetailDiagnostics} from '../../../scripts/email/lib/qqmail-detail.mjs';
const expected={provider_message_id:'message~123',subject:'same subject'};
const host=(id=expected.provider_message_id)=>({getClientRects:()=>[{}],innerText:expected.subject,'__reactInternalInstance$fixture':{return:{memoizedProps:{mail:{id,isUnread:false,isDetail:true,isFake:false,isDraft:false,isSessionMail:false,messageId:'<fixture@example.test>'}}}}});
const fixture=()=>{
 const subject=host(),body=host();
 const f={subjects:[subject],bodies:[body],subject,body};
 f.document={querySelectorAll:selector=>selector.includes('subject')?f.subjects:f.bodies,defaultView:{getComputedStyle:()=>({display:'block',visibility:'visible'})}};
 return f;
};
test('QQ full-screen detail requires matching active mail props on both rendered hosts',()=>{
 const f=fixture();assert.deepEqual(qqMailDetailIdentity(f.document,expected),{provider_message_id:expected.provider_message_id,read_state:'read'});
 f.body.__reactInternalInstance$fixture.return.memoizedProps.mail.id='other';assert.equal(qqMailDetailIdentity(f.document,expected),null);
 f.body.__reactInternalInstance$fixture.return.memoizedProps.mail.id=expected.provider_message_id;
 f.subject.__reactInternalInstance$fixture.return.memoizedProps.mail.id='other';assert.equal(qqMailDetailIdentity(f.document,expected),null);
});
test('QQ identity rejects absent framework, conflicting markers and pending message transitions',()=>{
 for(const mode of ['missing','ambiguous','pending','cache only']){
  const f=fixture();
  if(mode==='missing')delete f.body.__reactInternalInstance$fixture;
  if(mode==='ambiguous')f.body.__reactFiber$other=f.body.__reactInternalInstance$fixture;
  if(mode==='pending')f.body.__reactInternalInstance$fixture.return.pendingProps={mail:{id:'next-message'}};
  if(mode==='cache only')f.body.__reactInternalInstance$fixture.return.memoizedProps={listMails:[{id:expected.provider_message_id}]};
  assert.equal(qqMailDetailIdentity(f.document,expected),null,mode);
 }
});
test('QQ identity accepts only unique visible subject/body and exact subject',()=>{
 const f=fixture();const hidden=host('other');hidden.getClientRects=()=>[];
 f.subjects.push(hidden);f.bodies.push(hidden);assert.deepEqual(qqMailDetailIdentity(f.document,expected),{provider_message_id:expected.provider_message_id,read_state:'read'});
 f.bodies.push(host());assert.equal(qqMailDetailIdentity(f.document,expected),null);
 f.bodies.pop();f.subjects.push(host());assert.equal(qqMailDetailIdentity(f.document,expected),null);
 f.subjects.pop();f.subject.innerText='different subject';assert.equal(qqMailDetailIdentity(f.document,expected),null);
});
test('QQ identity fails closed on inaccessible props and never consults DOM ancestry',()=>{
 const f=fixture();f.body.parentElement=host();Object.defineProperty(f.body,'__reactInternalInstance$fixture',{get(){throw new Error('unavailable');}});
 assert.equal(qqMailDetailIdentity(f.document,expected),null);
});

test('QQ read state requires two matching individual detail records',()=>{
 for(const mode of ['unread mismatch','draft','fake','list shell','session','nonboolean','duplicate key']){
  const f=fixture(),mail=f.body.__reactInternalInstance$fixture.return.memoizedProps.mail;
  if(mode==='unread mismatch')mail.isUnread=true;
  if(mode==='draft')mail.isDraft=true;
  if(mode==='fake')mail.isFake=true;
  if(mode==='list shell')mail.isDetail=false;
  if(mode==='session')mail.isSessionMail=true;
  if(mode==='nonboolean')mail.isUnread=0;
  if(mode==='duplicate key')mail.isunread=false;
  assert.equal(qqMailDetailIdentity(f.document,expected),null,mode);
 }
 const f=fixture();for(const node of [f.subject,f.body])node.__reactInternalInstance$fixture.return.memoizedProps.mail.isUnread=true;
 assert.equal(qqMailDetailIdentity(f.document,expected).read_state,'unread');
 assert.equal(qqMailDetailIdentity(f.document,{...expected,required_read_state:'read'}),null);
 assert.equal(qqMailDetailIdentity(f.document,{...expected,required_read_state:'unread'}).read_state,'unread');
});

test('QQ failure diagnostics treat native fields as opaque without exposing values',()=>{
 const f=fixture();const good=qqMailDetailDiagnostics(f.document,expected);
 assert.equal(good.native_field_equal,true);assert.equal(good.unread_equal,true);assert.ok(good.roots.every(r=>r.native_field_string&&r.pending_matches&&r.id_matches));
 for(const node of [f.subject,f.body])node.__reactInternalInstance$fixture.return.memoizedProps.mail.messageId='sensitive-opaque-native-field';
 f.body.__reactInternalInstance$fixture.return.pendingProps={mail:{id:'other'}};
 const result=qqMailDetailDiagnostics(f.document,expected);assert.ok(result.roots.every(r=>r.native_field_bounded));assert.equal(result.roots[1].pending_matches,false);
 for(const value of ['sensitive-opaque',expected.provider_message_id,expected.subject,'rfc'])assert.equal(JSON.stringify(result).includes(value),false);
});
test('QQ opaque or absent native messageId does not masquerade as an RFC header',()=>{
 for(const mode of ['opaque','absent','different']){
  const f=fixture(),subject=f.subject.__reactInternalInstance$fixture.return.memoizedProps.mail,body=f.body.__reactInternalInstance$fixture.return.memoizedProps.mail;
  if(mode==='opaque'){subject.messageId=body.messageId='opaque-native-key';}
  if(mode==='absent'){delete subject.messageId;delete body.messageId;}
  if(mode==='different'){subject.messageId='opaque-a';body.messageId='opaque-b';}
  assert.deepEqual(qqMailDetailIdentity(f.document,expected),{provider_message_id:expected.provider_message_id,read_state:'read'});
 }
});
