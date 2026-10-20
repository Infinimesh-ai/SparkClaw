import test from 'node:test';
import assert from 'node:assert/strict';
import { mapV2Route, requireOperation } from '../src/main/iscp-routes.mjs';

test('progress uses only its negotiated v2 route with bounded closed read inputs', () => {
 const id='11111111-1111-4111-8111-111111111111', body={input_digest:'a'.repeat(64),after_revision:0,wait_ms:10000};
 const map=(suffix='',value=body)=>mapV2Route(new URL(`/api/v1/executions/${id}/progress${suffix}`,'https://iscp.invalid'),'POST',value);
 assert.deepEqual(map(),{operation:'execution.progress',params:{request_id:id},body});
 for(const value of [{...body,after_revision:-1},{...body,after_revision:Number.MAX_SAFE_INTEGER+1},{...body,wait_ms:10001},{...body,wait_ms:-1},{...body,input_digest:'invalid'},{...body,owner_id:'another'}])assert.throws(()=>map('',value),/invalid/);
 assert.throws(()=>map('?owner_id=another'),/invalid/);
});

test('original device-management routes accept only fixed operations and closed inputs', () => {
 assert.equal(mapV2Route(new URL('/api/clients','https://iscp.invalid'),'GET').operation,'clients.list');
 assert.deepEqual(mapV2Route(new URL('/api/clients/client','https://iscp.invalid'),'DELETE'),{operation:'clients.delete',params:{id:'client'}});
 for (const [path,body] of [['/api/clients/client?owner_id=other'],['/api/clients/client',{owner_id:'other'}],['/api/clients/client%2fother']]) assert.throws(()=>mapV2Route(new URL(path,'https://iscp.invalid'),'DELETE',body));
 assert.equal(mapV2Route(new URL('/api/clients','https://iscp.invalid'),'POST',{client_name:'second desktop'}).operation,'clients.issue');
 assert.deepEqual(mapV2Route(new URL('/api/clients/client/revoke','https://iscp.invalid'),'POST',{}).params,{id:'client'});
 for(const [path,method,body] of [['/api/clients?owner_id=other','GET'],['/api/clients','GET',{}],['/api/clients','POST',{client_name:'name',scopes:['admin']}],['/api/clients','POST',{client_name:'name',url:'https://other'}],['/api/clients','POST',{}],['/api/clients/client/revoke?url=https://other','POST',{}],['/api/clients/client/revoke','POST',{revision:2}],['/api/clients/client%2frevoke/revoke','POST',{}]])assert.throws(()=>mapV2Route(new URL(path,'https://iscp.invalid'),method,body),/invalid|unavailable/);
});

// These are the actual requests reachable from the qualified desktop surfaces.
// Keep them separate from the generated registry so a UI/registry mismatch fails.
const routes = [
 ['GET','/api/notification-bindings?channel=weixin&status=waiting_scan','settings.bindings.list'],
 ['POST','/api/notification-bindings/weixin/start','settings.bindings.start'],
 ['POST','/api/notification-bindings/telegram/start','settings.bindings.start'],
 ['GET','/api/notification-bindings/binding','settings.bindings.get'],
 ['POST','/api/notification-bindings/binding/poll','settings.bindings.poll'],
 ['GET','/api/notification-bindings/binding/login-url','settings.bindings.login'],
 ['DELETE','/api/notification-bindings/binding','settings.bindings.revoke'],
 ['GET','/api/browser/extension','settings.browser.get'],
 ['PUT','/api/browser/extension/token','settings.browser.save'],
 ['POST','/api/browser/extension/check','settings.browser.check'],
 ['DELETE','/api/browser/extension/token','settings.browser.delete'],
 ['GET','/api/mcp-access/catalog','settings.mcp.catalog'],
 ['PATCH','/api/mcp-access/transports','settings.mcp.transports'],
 ['GET','/api/mcp-access/tickets','settings.mcp.tickets.list'],
 ['POST','/api/mcp-access/tickets','settings.mcp.tickets.issue'],
 ['POST','/api/mcp-access/tickets/ticket/revoke','settings.mcp.tickets.revoke'],
 ['DELETE','/api/mcp-access/tickets/ticket','settings.mcp.tickets.delete'],
 ['GET','/api/mcp-access/bindings','settings.mcp.bindings.list'],
 ['POST','/api/mcp-access/bindings/binding/revoke','settings.mcp.bindings.revoke'],
 ['DELETE','/api/mcp-access/bindings/binding','settings.mcp.bindings.delete'],
 ['DELETE','/api/mcp-access/records','settings.mcp.records.delete'],
 ['GET','/api/iscp-pairing/status','settings.mcp.pairing.status'],
 ['GET','/api/iscp-pairing/onboardings','settings.mcp.pairing.onboardings'],
 ['POST','/api/iscp-pairing/start','settings.mcp.pairing.start'],
 ['GET','/api/owner','settings.owner.get'], ['POST','/api/owner','settings.owner.patch'],
 ['GET','/api/connectors','settings.connectors.list'], ['PATCH','/api/connectors/wechat','settings.connectors.patch'],
 ['GET','/api/integrations','settings.integrations.list'],
 ['POST','/api/integrations/infinimesh-info/credentials','settings.credentials.add'],
 ['POST','/api/integrations/localmind/credentials','settings.credentials.add'],
 ['PUT','/api/integrations/localmind/active-credential','settings.credentials.activate'],
 ['POST','/api/integrations/localmind/credentials/credential/check','settings.credentials.check'],
 ['DELETE','/api/integrations/localmind/credentials/credential','settings.credentials.delete'],
 ['GET','/api/notifications?limit=50&after=notification','notifications.list'],
 ['POST','/api/notifications/notification/read','notifications.read'], ['POST','/api/notifications/read-all','notifications.read_all'],
 ['GET','/api/v1/mail/mailboxes','mail.mailboxes'], ['POST','/api/v1/mail/mailbox/sync','mail.sync'],
 ['GET','/api/v1/mail/mailbox/messages/mail/attachments/part','mail.attachment'],
 ['GET','/api/email/drafts?mailbox_id=box&cursor=opaque&limit=50','mail.drafts.list'],
 ['GET','/api/email/drafts?draft=draft','mail.drafts.list'], ['POST','/api/email/drafts','mail.drafts.save'],
 ['POST','/api/email/drafts/draft/send','mail.drafts.send'], ['POST','/api/email/drafts/draft/reconcile','mail.drafts.reconcile'],
 ['POST','/api/v1/executions/task/approvals/approval','execution.approval'],
 ['PUT','/api/v1/inputs/task/files/file','execution.input.put'], ['GET','/api/v1/executions/task/files/file','execution.file.get'],
];
test('qualified presentation, mail, approval and file requests map to canonical routes',()=>{
 for(const [method,path,operation] of routes) assert.equal(mapV2Route(new URL(path,'https://iscp.invalid'),method).operation,operation,path);
 for(const [method,path] of [['GET','/api/integrations/localmind'],['GET','/api/email/compose/capabilities'],['POST','/api/email/provider/arbitrary'],['PUT','/api/email/drafts/draft']])assert.throws(()=>mapV2Route(new URL(path,'https://iscp.invalid'),method));
});

const popupRoutes = [
 ['GET','/api/email/providers','mail.providers.list'], ['GET','/api/email/conversations?mailbox_id=box&q=search&cursor=opaque&limit=20&entry=interaction&unassigned_only=true','mail.conversations.list'],
 ['GET','/api/email/conversations/conversation','mail.conversations.get'], ['GET','/api/email/conversations/conversation/messages?cursor=next&limit=20','mail.conversations.messages'],
 ['GET','/api/email/pending?mailbox_id=box','mail.pending'], ['GET','/api/email/messages/mail','mail.message'], ['GET','/api/email/messages/mail/verification','mail.verification'],
 ['GET','/api/email/messages/mail/render-preview','mail.render_preview'], ['GET','/api/email/messages/mail/file?part_id=part','mail.file'], ['GET','/api/email/messages/mail/file','mail.file'],
 ['POST','/api/email/source/cleanup','mail.source.cleanup'], ['GET','/api/email/notifications?subtype=verification&validity=active','mail.notifications'], ['GET','/api/email/interaction-mails','mail.interaction'],
 ['GET','/api/email/sender-rules?cursor=next&limit=100','mail.sender_rules.list'], ['POST','/api/email/messages/mail/classification','mail.classification'], ['POST','/api/email/messages/mail/assignment','mail.assignment'],
 ['POST','/api/email/conversations/conversation/rename','mail.conversations.rename'], ['DELETE','/api/email/conversations/conversation','mail.conversations.delete'], ['POST','/api/email/sender-rules/rule','mail.sender_rules.update'],
 ['GET','/api/email/presentations?target_kind=mail&language=zh&target_id=one&target_id=two','mail.presentations.get'], ['POST','/api/email/presentations/ensure','mail.presentations.ensure'],
 ['GET','/api/email/compose-capabilities','mail.compose.capabilities'], ['GET','/api/email/drafts/draft','mail.drafts.list'], ['PUT','/api/email/drafts/draft','mail.drafts.save'],
 ['POST','/api/email/replies/polish','mail.replies.polish'], ['GET','/api/email/sent-sources?mailbox_id=box','mail.sent_sources'], ['GET','/api/email/sync-status','mail.sync.status'],
 ['GET','/api/email/sync-warnings?mailbox_id=box&cursor=next','mail.sync.warnings'], ['POST','/api/email/sync-warnings/warning/acknowledge','mail.sync.acknowledge'],
 ['POST','/api/email/sync','mail.sync.request'], ['POST','/api/email/messages/viewed','mail.viewed'], ['POST','/api/email/messages/mail/reanalyze','mail.reanalyze'],
 ['PATCH','/api/email/providers/outlook','mail.providers.update'], ['POST','/api/email/providers/outlook/check','mail.providers.check'],
];
test('every original popup route uses a fixed typed operation with bounded multi-target presentations', () => {
 for (const [method,path,operation] of popupRoutes) assert.equal(mapV2Route(new URL(path,'https://iscp.invalid'),method, method === 'GET' ? undefined : {}).operation,operation,path);
 const map = (path, method = 'GET', body) => mapV2Route(new URL(path,'https://iscp.invalid'), method, body);
 assert.deepEqual(map('/api/email/presentations?target_kind=mail&language=zh&target_id=one&target_id=two').params,{target_kind:'mail',language:'zh',target_ids:'["one","two"]'});
 const pageIDs = Array.from({length:100},(_,i)=>i.toString(16).padStart(64,'0'));
 assert.deepEqual(JSON.parse(map(`/api/email/presentations?${pageIDs.map(id=>`target_id=${id}`).join('&')}`).params.target_ids),pageIDs);
 assert.throws(()=>map(`/api/email/conversations?q=${'字'.repeat(342)}`));
 assert.deepEqual(map('/api/email/drafts/draft','PUT',{subject:'unchanged'}),{operation:'mail.drafts.save',params:{draft:'draft'},body:{subject:'unchanged',id:'draft'}});
 assert.equal(map('/api/email/providers/outlook','PATCH',{intake_enabled:false,expected_mailbox_version:1}).operation,'mail.intake.update');
 for (const path of ['/api/email/presentations?target_id=one&target_id=one','/api/email/presentations?target_id=../secret','/api/email/presentations?target_ids=%5B%22one%22%5D','/api/email/presentations?target_kind=mail&target_kind=conversation','/api/email/messages/mail/file?part_id=one&part_id=two','/api/email/messages/mail?mail=other','/api/email/messages/mail/file?path=/etc/passwd','/api/email/messages/%2fetc/file','/api/email/messages/mail/file#fragment',`/api/email/presentations?${Array.from({length:101},(_,i)=>`target_id=id-${i}`).join('&')}`]) assert.throws(()=>map(path),undefined,path);
 assert.throws(()=>map('/api/email/drafts/draft','PUT',{id:'other'}));
 assert.throws(()=>map('/api/email/drafts/draft?draft=other'));
 assert.throws(()=>map('/api/email/messages/mail/file','DELETE'));
 // Desktop mail settings check sign-in state only; the sign-in browser stays backend-only.
 assert.throws(()=>map('/api/email/providers/outlook/login-browser','POST',{}));
});
test('events, browser channel and speech clients use fixed forward operations only',()=>{
 const operations=['capabilities.get','operations.receipt','events.snapshot','events.pull','events.ack','browser.host.grant','browser.host.register','browser.host.poll','browser.host.reply','browser.host.heartbeat','browser.host.close','browser.receipt','browser.reconcile','speech.transcribe','speech.cancel','speech.session.open','speech.session.frame','speech.session.events','speech.session.finish','speech.session.cancel','transfer.open','transfer.chunk','transfer.status','transfer.commit','object.describe','object.read','object.release'];
 for(const operation of operations) assert.equal(requireOperation(operation).direction,'forward');
 assert.throws(()=>requireOperation('browser.command'));
});
