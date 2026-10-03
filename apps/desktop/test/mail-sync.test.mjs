import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import crypto from 'node:crypto';
import { ClientStore } from '../src/main/client-store.mjs';
import { MailSyncStore } from '../src/main/mail-sync-store.mjs';
import { MailSyncClient } from '../src/main/mail-sync-client.mjs';
import { MailSyncCapability } from '../src/main/mail-sync-capability.mjs';
const scope={deployment_id:'deployment',owner_id:'owner',client_id:'client'};
const epoch='a'.repeat(32);
const mail=(id,subject=id)=>({id,mailbox_id:'box',version:1,subject,from:'sender@example.com',to:[],cc:[],receiving_address:'owner@example.com',direction:'inbound',sent_at:'',arrived_at:'',summary:'summary',body_text:'body',body_truncated:false,viewed:false,processing_state:'ready',original_available:false,attachments:[]});
const event=(id,sequence,deleted=false)=>({id,sequence,deleted,...(deleted?{}:{mail:mail(id)})});
const response=(mode,sequence,base,events,cursor,more=false)=>({schema_version:1,mailbox_id:'box',epoch,mode,base_sequence:base,sequence,events,cursor,more});
function fixture(t){const root=fs.mkdtempSync(path.join(os.tmpdir(),'sparkclaw-mail-sync-'));t.after(()=>fs.rmSync(root,{recursive:true,force:true}));return root;}
function deferred(){let resolve;const promise=new Promise((done)=>{resolve=done;});return {promise,resolve};}
test('mail cache scopes, durable snapshots and tombstones replace together after restart',(t)=>{
 const root=fixture(t);let store=new MailSyncStore(root);
 store.apply(scope,'box',response('snapshot',1,1,[event('old',1)],'old-cursor'),'');
 store.reset(scope,'box');store.apply(scope,'box',response('snapshot',2,2,[event('a',2)],'page-one',true),'');
 assert.equal(store.read(scope,'box').messages[0].id,'old');store.close();store=new MailSyncStore(root);
 assert.equal(store.cursor(scope,'box'),'page-one');store.apply(scope,'box',response('snapshot',2,2,[event('b',2)],'fresh-cursor'),'page-one');
 assert.deepEqual(store.read(scope,'box').messages.map(m=>m.id),['a','b']);
 assert.deepEqual(store.read({...scope,owner_id:'other'},'box').messages,[]);
 assert.deepEqual(store.read({...scope,client_id:'other'},'box').messages,[]);
 assert.deepEqual(store.read({...scope,deployment_id:'other'},'box').messages,[]);
 store.apply(scope,'box',response('delta',3,2,[event('a',3,true)],'deleted'),'fresh-cursor');assert.deepEqual(store.read(scope,'box').messages.map(m=>m.id),['b']);
 assert.throws(()=>store.apply(scope,'box',response('delta',3,2,[event('a',3,true)],'deleted'),'fresh-cursor'),/stale/);
 store.close();
});
test('malformed, gapped, secret-bearing, oversized and disk-failed pages cannot advance cache',(t)=>{
 const store=new MailSyncStore(fixture(t));store.apply(scope,'box',response('snapshot',1,1,[event('a',1)],'first'),'');
 const gap=response('delta',3,1,[event('b',3)],'bad');assert.throws(()=>store.apply(scope,'box',gap,'first'),/gap/);
 const secret=response('delta',2,1,[{...event('b',2),mail:{...mail('b'),credential:'canary'}}],'bad');assert.throws(()=>store.apply(scope,'box',secret,'first'),/fields/);
 const over=response('delta',2,1,[{...event('b',2),mail:{...mail('b'),body_text:'x'.repeat(256*1024)}}],'bad');assert.throws(()=>store.apply(scope,'box',over,'first'),/capacity/);
 store.db.exec("CREATE TRIGGER disk_failure BEFORE INSERT ON mail BEGIN SELECT RAISE(ABORT,'disk full'); END;");assert.throws(()=>store.apply(scope,'box',response('delta',2,1,[event('b',2)],'second'),'first'),/disk full/);
 assert.equal(store.cursor(scope,'box'),'first');assert.deepEqual(store.read(scope,'box').messages.map(m=>m.id),['a']);
 store.db.exec('DROP TRIGGER disk_failure');store.apply(scope,'box',response('delta',2,1,[event('b',2)],'second'),'first');
 assert.equal(store.read(scope,'box').sequence,2);store.close();
});
test('network sync binds main-owned installation, resumes snapshot and reads offline cache',(t)=>{
 const store=new MailSyncStore(fixture(t));const connection={origin:'https://backend.invalid',authorization:'Bearer synthetic',deploymentID:'deployment',ownerID:'owner',clientID:'client'};
 const calls=[];let online=true;
 const fetcher=async(url,init)=>{calls.push({url,init});if(!online)throw new Error('offline');const cursor=JSON.parse(init.body).cursor;
 if(!cursor)return new Response(JSON.stringify(response('snapshot',2,2,[event('a',2)],'page',true)),{status:200});
 return new Response(JSON.stringify(response('snapshot',2,2,[event('b',2)],'complete')),{status:200});};
 const client=new MailSyncClient({store,getConnection:()=>connection,getFetch:()=>fetcher,installationID:'installation',ensureInstallation:async()=>{}});
 return client.sync('box').then(async result=>{assert.equal(result.messages.length,2);assert.equal(calls[0].init.headers['X-SparkClaw-Installation'],'installation');assert.equal(calls[0].init.headers.Authorization,connection.authorization);assert.equal(calls[0].init.method,'POST');assert.deepEqual(JSON.parse(calls[1].init.body),{cursor:'page',limit:100});online=false;await assert.rejects(client.sync('box'),/offline/);assert.equal(client.read('box').messages.length,2);store.close();});
});
test('epoch reset clears only staging and last full mail cache survives failed resnapshot',(t)=>{
 const store=new MailSyncStore(fixture(t));store.apply(scope,'box',response('snapshot',1,1,[event('a',1)],'old'),'');
 const connection={origin:'https://backend.invalid',authorization:'Bearer synthetic',deploymentID:'deployment',ownerID:'owner',clientID:'client'};
 let calls=0;const fetcher=async()=>{if(calls++===0)return new Response('{}',{status:409});throw new Error('offline');};
 const client=new MailSyncClient({store,getConnection:()=>connection,getFetch:()=>fetcher,installationID:'installation'});
 return assert.rejects(client.sync('box'),/offline/).then(()=>{assert.equal(store.read(scope,'box').messages[0].id,'a');assert.equal(store.cursor(scope,'box'),'');store.close();});
});
test('mail IPC trusts only the workbench main frame and rejects renderer identity or mutation fields',async()=>{
 const mainFrame={url:'sparkclaw-app://workbench/index.html'};const contents={mainFrame};const client={read:(id)=>({id}),catalog:()=>[]};const cap=new MailSyncCapability({window:{webContents:contents},client});const event={sender:contents,senderFrame:mainFrame};
 assert.deepEqual(await cap.dispatch(event,{schema_version:1,operation:'read',mailbox_id:'box'}),{id:'box'});
 await assert.rejects(cap.dispatch({...event,senderFrame:{url:mainFrame.url}},{schema_version:1,operation:'catalog'}),/trusted/);
 await assert.rejects(cap.dispatch(event,{schema_version:1,operation:'read',mailbox_id:'box',owner_id:'other'}),/fields/);
 await assert.rejects(cap.dispatch(event,{schema_version:1,operation:'delete'}),/unavailable/);
});

test('verified mail attachment copies become local files; backend loss, tamper and disk failure preserve ownership',async(t)=>{
 const cache=new MailSyncStore(fixture(t));const local=new ClientStore(fixture(t));const conversation=local.create(scope,'Attachment copies');
 const bytes=Buffer.alloc(2*1024*1024,65);const hash=`sha256:${crypto.createHash('sha256').update(bytes).digest('hex')}`;
 const attachment={id:'part-1',name:'report.bin',size:bytes.length,available:true,sha256:hash};
 const projection=mail('a');projection.attachments=[attachment];cache.apply(scope,'box',response('snapshot',1,1,[{...event('a',1),mail:projection}],'first'),'');
 const connection={origin:'https://backend.invalid',authorization:'Bearer synthetic',deploymentID:'deployment',ownerID:'owner',clientID:'client'};
 let original=bytes;let calls=0;const fetcher=async(url,init)=>{calls++;assert.equal(new URL(url).pathname,'/api/r3/mail/box/messages/a/attachments/part-1');assert.equal(init.headers['X-SparkClaw-Installation'],'installation');return original?new Response(original):new Response(null,{status:404});};
 const client=new MailSyncClient({store:cache,localStore:local,getConnection:()=>connection,getFetch:()=>{throw new Error('ordinary proxy must not transfer attachments');},getFileFetch:()=>fetcher,installationID:'installation'});
 const saved=await client.saveAttachment('box','a','part-1',conversation.id);assert.equal(saved.size,bytes.length);assert.deepEqual(local.file(scope,saved.id).content,bytes);
 original=undefined;await assert.rejects(client.saveAttachment('box','a','part-1',conversation.id),/unavailable/);assert.deepEqual(local.file(scope,saved.id).content,bytes);
 original=Buffer.alloc(bytes.length,66);await assert.rejects(client.saveAttachment('box','a','part-1',conversation.id),/integrity/);assert.equal(local.read(scope,conversation.id).files.length,1);
 original=bytes;local.db.exec("CREATE TRIGGER mail_copy_disk_full BEFORE INSERT ON files BEGIN SELECT RAISE(ABORT,'disk full'); END;");await assert.rejects(client.saveAttachment('box','a','part-1',conversation.id),/disk full/);assert.equal(local.read(scope,conversation.id).files.length,1);assert.equal(original,bytes);
 const before=calls;await assert.rejects(client.saveAttachment('box','a','missing',conversation.id),/available attachment/);assert.equal(calls,before);
 cache.close();local.close();
});

test('suspend aborts a slow sync, fences its late page and preserves a restarted same-mailbox sync',async(t)=>{
 const store=new MailSyncStore(fixture(t));store.apply(scope,'box',response('snapshot',1,1,[event('a',1)],'first'),'');
 const connection={origin:'https://backend.invalid',authorization:'Bearer synthetic',deploymentID:'deployment',ownerID:'owner',clientID:'client'};
 const old=deferred(),fresh=deferred(),oldEntered=deferred(),freshEntered=deferred();let oldSignal;let calls=0;
 const fetcher=async(_url,init)=>{if(++calls===1){oldSignal=init.signal;oldEntered.resolve();return old.promise;}freshEntered.resolve();return fresh.promise;};
 const client=new MailSyncClient({store,getConnection:()=>connection,getFetch:()=>fetcher,installationID:'installation'});
 const pending=client.sync('box');const oldRejected=assert.rejects(pending,/paused/);await oldEntered.promise;client.close();assert.equal(oldSignal.aborted,true);
 assert.equal(client.read('box').messages[0].id,'a');await assert.rejects(client.sync('box'),/paused/);
 client.start();const resumed=client.sync('box');await freshEntered.promise;
 old.resolve(new Response(JSON.stringify(response('delta',2,1,[event('stale',2)],'stale'))));await oldRejected;
 assert.equal(store.cursor(scope,'box'),'first');assert.equal(client.sync('box'),resumed);
 fresh.resolve(new Response(JSON.stringify(response('delta',2,1,[event('fresh',2)],'resumed'))));await resumed;
 assert.equal(store.cursor(scope,'box'),'resumed');assert.deepEqual(client.read('box').messages.map(value=>value.id),['a','fresh']);client.close();store.close();
});

test('a suspended late cursor-reset response cannot clear committed mail or snapshot cursor',async(t)=>{
 const store=new MailSyncStore(fixture(t));store.apply(scope,'box',response('snapshot',1,1,[event('a',1)],'first'),'');
 const connection={origin:'https://backend.invalid',authorization:'Bearer synthetic',deploymentID:'deployment',ownerID:'owner',clientID:'client'};
 const entered=deferred(),late=deferred();const fetcher=async()=>{entered.resolve();return late.promise;};
 const client=new MailSyncClient({store,getConnection:()=>connection,getFetch:()=>fetcher,installationID:'installation'});
 const pending=client.sync('box');const rejected=assert.rejects(pending,/paused/);await entered.promise;client.close();client.start();late.resolve(new Response('{}',{status:409}));await rejected;
 assert.equal(store.cursor(scope,'box'),'first');assert.equal(client.read('box').messages[0].id,'a');client.close();store.close();
});

test('suspend during a slow catalog body keeps its abort signal active and leaves offline catalog readable',async(t)=>{
 const store=new MailSyncStore(fixture(t));store.catalog(scope,[{id:'box',address:'cached@example.com',provider:'gmail'}]);
 const connection={origin:'https://backend.invalid',authorization:'Bearer synthetic',deploymentID:'deployment',ownerID:'owner',clientID:'client'};
 const entered=deferred(),release=deferred();let signal;
 const fetcher=async(_url,init)=>{signal=init.signal;return new Response(new ReadableStream({async pull(controller){entered.resolve();await release.promise;controller.enqueue(new TextEncoder().encode(JSON.stringify({mailboxes:[{id:'box',address:'late@example.com',provider:'gmail'}]})));controller.close();}}));};
 const client=new MailSyncClient({store,getConnection:()=>connection,getFetch:()=>fetcher,installationID:'installation'});
 const pending=client.refreshCatalog();const rejected=assert.rejects(pending,/paused/);await entered.promise;client.close();assert.equal(signal.aborted,true);assert.equal(client.catalog()[0].address,'cached@example.com');client.start();release.resolve();await rejected;
 assert.equal(client.catalog()[0].address,'cached@example.com');client.close();store.close();
});

test('suspended attachment response cannot save a late copy after restart',async(t)=>{
 const store=new MailSyncStore(fixture(t));const local=new ClientStore(fixture(t));const conversation=local.create(scope,'Attachment lifecycle');
 const bytes=Buffer.from('synthetic attachment');const part={id:'part-1',name:'report.txt',size:bytes.length,available:true,sha256:`sha256:${crypto.createHash('sha256').update(bytes).digest('hex')}`};
 const projected=mail('a');projected.attachments=[part];store.apply(scope,'box',response('snapshot',1,1,[{...event('a',1),mail:projected}],'first'),'');
 const connection={origin:'https://backend.invalid',authorization:'Bearer synthetic',deploymentID:'deployment',ownerID:'owner',clientID:'client'};
 const entered=deferred(),late=deferred();let signal;let calls=0;
 const fetcher=async(_url,init)=>{if(++calls===1){signal=init.signal;entered.resolve();return late.promise;}return new Response(bytes);};
 const client=new MailSyncClient({store,localStore:local,getConnection:()=>connection,getFetch:()=>fetcher,getFileFetch:()=>fetcher,installationID:'installation'});
 const pending=client.saveAttachment('box','a','part-1',conversation.id);const rejected=assert.rejects(pending,/paused/);await entered.promise;client.close();assert.equal(signal.aborted,true);assert.equal(local.read(scope,conversation.id).files.length,0);
 client.start();await client.saveAttachment('box','a','part-1',conversation.id);late.resolve(new Response(bytes));await rejected;assert.equal(local.read(scope,conversation.id).files.length,1);assert.equal(store.cursor(scope,'box'),'first');client.close();local.close();store.close();
});
