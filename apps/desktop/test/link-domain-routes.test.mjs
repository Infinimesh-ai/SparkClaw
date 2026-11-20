import test from 'node:test';
import assert from 'node:assert/strict';
import { consoleRoute, mapRoute, mapTypedRoute, operationSpec, linkDomainOperations } from '../src/main/link-domain-routes.mjs';

const mapV2Route = mapRoute;

test('the original device screens are answered by the console, never sent', () => {
 for (const path of ['/api/clients','/api/clients/client','/api/clients/client/revoke']) assert.equal(consoleRoute(new URL(path,'https://backend.invalid')),true,path);
 assert.equal(consoleRoute(new URL('/api/clients/a/b/c','https://backend.invalid')),false);
 for (const path of ['/api/v1/executions/11111111-1111-4111-8111-111111111111/progress','/api/v1/inputs/task/files/file']) assert.throws(()=>mapRoute(new URL(path,'https://backend.invalid'),'POST',{}),/unavailable/u,path);
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
];
test('the original settings, notification and mail requests map to their typed methods',()=>{
 for(const [method,path,operation] of routes) assert.equal(mapV2Route(new URL(path,'https://backend.invalid'),method,method === 'GET' || method === 'DELETE' ? undefined : {}).operation,operation,path);
 for(const [method,path] of [['GET','/api/integrations/localmind'],['GET','/api/email/compose/capabilities'],['POST','/api/email/provider/arbitrary'],['PUT','/api/email/drafts/draft']])assert.throws(()=>mapV2Route(new URL(path,'https://backend.invalid'),method));
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
 ['PATCH','/api/email/providers/outlook','mail.providers.update'], ['POST','/api/email/providers/outlook/login-browser','mail.providers.login'], ['POST','/api/email/providers/outlook/check','mail.providers.check'],
];
test('every original popup route uses a fixed typed operation with bounded multi-target presentations', () => {
 for (const [method,path,operation] of popupRoutes) assert.equal(mapV2Route(new URL(path,'https://backend.invalid'),method, method === 'GET' ? undefined : {}).operation,operation,path);
 const map = (path, method = 'GET', body) => mapV2Route(new URL(path,'https://backend.invalid'), method, body);
 assert.deepEqual(map('/api/email/presentations?target_kind=mail&language=zh&target_id=one&target_id=two').args,{target_kind:'mail',language:'zh',target_ids:['one','two']});
 const pageIDs = Array.from({length:100},(_,i)=>i.toString(16).padStart(64,'0'));
 assert.deepEqual(map(`/api/email/presentations?${pageIDs.map(id=>`target_id=${id}`).join('&')}`).args.target_ids,pageIDs);
 assert.deepEqual(map('/api/email/conversations?mailbox_id=box&limit=20&unassigned_only=true').args,{mailbox_id:'box',limit:20,unassigned_only:true},'typed list filters');
 for (const path of ['/api/email/conversations?limit=0','/api/email/conversations?limit=x','/api/email/conversations?unassigned_only=yes']) assert.throws(()=>map(path),/Query/u,path);
 assert.throws(()=>map(`/api/email/conversations?q=${'字'.repeat(342)}`));
 assert.deepEqual(map('/api/email/drafts/draft','PUT',{subject:'unchanged'}).args,{subject:'unchanged',draft:'draft'});
 assert.equal(map('/api/email/providers/outlook','PATCH',{intake_enabled:false,expected_mailbox_version:1}).operation,'mail.intake.update');
 for (const path of ['/api/email/presentations?target_id=one&target_id=one','/api/email/presentations?target_id=../secret','/api/email/presentations?target_ids=%5B%22one%22%5D','/api/email/presentations?target_kind=mail&target_kind=conversation','/api/email/messages/mail/file?part_id=one&part_id=two','/api/email/messages/mail?mail=other','/api/email/messages/mail/file?path=/etc/passwd','/api/email/messages/%2fetc/file','/api/email/messages/mail/file#fragment',`/api/email/presentations?${Array.from({length:101},(_,i)=>`target_id=id-${i}`).join('&')}`]) assert.throws(()=>map(path),undefined,path);
 assert.throws(()=>map('/api/email/drafts/draft','PUT',{id:'other'}));
 assert.throws(()=>map('/api/email/drafts/draft?draft=other'));
 assert.throws(()=>map('/api/email/messages/mail/file','DELETE'));
});
test('browser fences and speech clients call fixed typed methods only',()=>{
 for(const operation of ['browser.receipt','browser.reconcile','speech.transcribe','speech.cancel','speech.session.open','speech.session.frame','speech.session.events','speech.session.finish','speech.session.cancel','mcd.session.status']) assert.equal(operationSpec(operation).typed,true,operation);
 // The link layer's own operations replaced these; the embedded browser has
 // no forward host lifecycle left to call.
 const reachable = linkDomainOperations(['browser','speech','mcd','capabilities','operations','events','transfer','object']);
 for(const operation of ['browser.command','capabilities.get','operations.receipt','events.pull','transfer.open','object.read','browser.host.grant','browser.host.poll']) assert.ok(!reachable.includes(operation),operation);
});

test('settings routes take typed arguments: the records a route names and the body fields', () => {
 const map = (path, method = 'GET', body) => mapTypedRoute(new URL(path, 'https://backend.invalid'), method, body);
 assert.deepEqual(map('/api/notification-bindings?channel=weixin&status=waiting_scan'), { operation: 'settings.bindings.list', params: { channel: 'weixin', status: 'waiting_scan' }, args: { channel: 'weixin', status: 'waiting_scan' }, typed: true, reply: true });
 assert.deepEqual(map('/api/integrations/localmind/credentials/credential/check', 'POST', {}).args, { integration_id: 'localmind', credential_id: 'credential' });
 assert.deepEqual(map('/api/connectors/wechat', 'PATCH', { enabled: true, expected_version: 2 }).args, { enabled: true, expected_version: 2, channel: 'wechat' });
 // A record the route names wins over a body field of the same name.
 assert.equal(map('/api/mcp-access/tickets/ticket/revoke', 'POST', { ticket_id: 'other' }).args.ticket_id, 'ticket');
 for (const path of ['/api/notification-bindings?owner_id=other', '/api/notification-bindings?channel=a&channel=b', '/api/owner?x=1']) assert.throws(() => map(path), /Query/u, path);
 assert.equal(map('/api/notification-bindings/a%2fb'), undefined, 'a record id is never a path');
});

test('a link connection reaches the reported domains without the groups the link layer replaced', () => {
 const operations = linkDomainOperations(['mail', 'settings', 'medicine', 'events', 'object', 'transfer', 'operations', 'execution', 'presentation']);
 assert.ok(operations.includes('mail.drafts.send') && operations.includes('settings.owner.get') && operations.includes('medicine.sync'));
 for (const op of operations) assert.ok(!/^(events|object|transfer|operations|execution|approvals|clients|tools)\./u.test(op), op);
 assert.deepEqual(linkDomainOperations([]), []);
});
