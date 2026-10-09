import test from 'node:test';
import assert from 'node:assert/strict';
import { mapV2Route, requireOperation } from '../src/main/iscp-routes.mjs';

// These are the actual requests reachable from the qualified desktop surfaces.
// Keep them separate from the generated registry so a UI/registry mismatch fails.
const routes = [
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
 for(const [method,path] of [['GET','/api/integrations/localmind'],['GET','/api/email/compose/capabilities'],['POST','/api/email/replies/polish'],['PUT','/api/email/drafts/draft']])assert.throws(()=>mapV2Route(new URL(path,'https://iscp.invalid'),method));
});
test('events, browser channel and speech clients use fixed forward operations only',()=>{
 const operations=['capabilities.get','operations.receipt','events.snapshot','events.pull','events.ack','browser.host.grant','browser.host.register','browser.host.poll','browser.host.reply','browser.host.heartbeat','browser.host.close','browser.receipt','browser.reconcile','speech.transcribe','speech.cancel','speech.session.open','speech.session.frame','speech.session.events','speech.session.finish','speech.session.cancel','transfer.open','transfer.chunk','transfer.status','transfer.commit','object.describe','object.read','object.release'];
 for(const operation of operations) assert.equal(requireOperation(operation).direction,'forward');
 assert.throws(()=>requireOperation('browser.command'));
});
