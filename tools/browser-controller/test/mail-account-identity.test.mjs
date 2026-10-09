import test from 'node:test';
import assert from 'node:assert/strict';
import vm from 'node:vm';
const runtime = new URL('../', import.meta.resolve('@infinimesh/app-cli-runtime/release'));
const {installReader} = await import(new URL('applications/mail/userscripts/lib/reader-core.mjs', runtime));
const {parseOutlookFolders} = await import(new URL('applications/mail/userscripts/lib/outlook-folders.mjs', runtime));
const interval = {interval_start:'2026-10-09T00:00:00Z', interval_end:'2026-10-09T01:00:00Z'};
const mailbox = 'mailbox@example.test';
function startup(address = mailbox) {
  return {owaUserConfig:{SessionSettings:{UserEmailAddress:address}}, findConversation:{Body:{FolderId:{Id:'inbox-id'}}},
    findFolders:{Body:{ResponseMessages:{Items:[{RootFolder:{IncludesLastItemInRange:true,TotalItemsInView:1,
      ParentFolder:{FolderId:{Id:'root-id'}}, Folders:[{FolderId:{Id:'inbox-id'},ParentFolderId:{Id:'root-id'},
        FolderClass:'IPF.Note',DistinguishedFolderId:'inbox',DisplayName:'Inbox',
        ExtendedProperty:[{ExtendedFieldURI:{PropertyTag:'0x10f4',PropertyType:'Boolean'},Value:'false'}]}]}}]}}}};
}
function fixture(provider = 'outlook') {
  let address = '', transport, requests = 0;
  const window = {fetch:async()=>{requests++;throw new Error('No provider request is needed for identity');}};
  window.top = window;
  class XMLHttpRequest {open() {} send() {} setRequestHeader() {}}
  const config = {provider,origins:['https://outlook.live.com'],account:()=>address,parseFolders:parseOutlookFolders,
    installTransport: callbacks => {transport = callbacks;return {dispose(){},resetRound(){}};}};
  vm.runInNewContext(`(${installReader.toString()})(config)`, {window,config,location:{origin:'https://outlook.live.com'},
    XMLHttpRequest,URL,Date,performance,TextEncoder,TextDecoder,AbortSignal});
  return {reader:window.SparkClawMailReader,dom:value=>{address=value;},observe:value=>transport.receiveStartup(value),requests:()=>requests};
}
const code = expected => error => error.code === expected;

test('collapsed Outlook navigation uses the observed mailbox scope for bootstrap and watch identity', () => {
  const f=fixture();
  assert.throws(()=>f.reader.snapshot(interval),code('email_account_identity_unavailable'));
  f.observe(startup());
  const value=f.reader.snapshot(interval);
  assert.equal(value.account_address,mailbox);assert.equal(value.rows.length,0);assert.equal(value.scan_complete,false);
  assert.equal(f.reader.checkAccount({account_address:mailbox}),true);
  assert.equal(f.reader.resetRound({account_address:mailbox}).account_address,mailbox);
  assert.equal(f.requests(),0);
});
test('DOM and observed startup evidence must agree even before an account is pinned', () => {
  const f=fixture();f.observe(startup());f.dom('another@example.test');
  assert.throws(()=>f.reader.snapshot(interval),code('email_account_identity_mismatch'));
  f.dom(mailbox.toUpperCase());assert.equal(f.reader.snapshot(interval).account_address,mailbox);
});
test('startup and DOM identity changes cannot reuse a pinned mailbox or an expected owner', () => {
  const f=fixture();f.observe(startup());f.reader.snapshot(interval);
  assert.throws(()=>f.reader.checkAccount({account_address:'another@example.test'}),code('email_account_identity_mismatch'));
  f.observe(startup('another@example.test'));
  assert.throws(()=>f.reader.snapshot(interval),code('email_account_identity_mismatch'));
  f.observe(startup('another@example.test'));f.dom(mailbox);
  assert.throws(()=>f.reader.checkAccount({account_address:mailbox}),code('email_account_identity_mismatch'));
});
test('invalid or unobserved startup scope never supplies an identity', () => {
  for(const value of [{},startup('invalid'),startup('bad@example.test\n'),startup('<bad@example.test>')]) {
    const f=fixture();f.observe(value);
    assert.throws(()=>f.reader.snapshot(interval),code('email_account_identity_unavailable'));
  }
  const f=fixture();const value=startup();value.findConversation.Body.FolderId.Id='!not-a-folder';f.observe(value);
  assert.throws(()=>f.reader.snapshot(interval),code('email_account_identity_unavailable'));
});
test('other provider readers still require their own DOM identity', () => {
  const f=fixture('qq_mail');f.observe(startup());
  assert.throws(()=>f.reader.snapshot(interval),code('email_account_identity_unavailable'));
  f.dom(mailbox);assert.equal(f.reader.snapshot(interval).account_address,mailbox);
  assert.throws(()=>f.reader.checkAccount({}),code('email_account_identity_mismatch'));
});
