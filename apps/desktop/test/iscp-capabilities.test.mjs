import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { ISCP_SURFACES, projectISCPCapabilities } from '../src/main/iscp-capabilities.mjs';
const manifest={schema_version:2,session_id:'session',authorization_revision:1,profile:'sparkclaw.workbench.transport.v2',expires_at:new Date(Date.now()+60000).toISOString(),operations:['settings.owner.get','settings.owner.patch']};
const report={...manifest};
const row={id:'settings_owner',qualified:true,supported:true,permitted:true,dependencies_ready:true,enabled:true};
test('UI projection requires negotiated authorization and all qualification gates independently',()=>{
 assert.equal(projectISCPCapabilities(manifest,{...report,capabilities:[row]}).settings,true);
 for(const field of ['qualified','supported','permitted','dependencies_ready','enabled'])assert.equal(projectISCPCapabilities(manifest,{...report,capabilities:[{...row,[field]:false}]}).settings,false);
 assert.equal(projectISCPCapabilities({...manifest,operations:['settings.owner.get']},{...report,capabilities:[row]}).settings,false);
 assert.equal(projectISCPCapabilities(manifest,{...report,capabilities:[row]},Date.now()+120000).settings,false);
 assert.equal(projectISCPCapabilities(undefined).files,false);
});

test('capability reports cannot cross authorization or session boundaries or survive expiry',()=>{
 for(const patch of [{schema_version:1},{profile:'other'},{session_id:'old-session'},{authorization_revision:2},{expires_at:new Date(0).toISOString()}])assert.equal(projectISCPCapabilities(manifest,{...report,...patch,capabilities:[row]}).settings,false);
 assert.equal(projectISCPCapabilities({...manifest,session_id:undefined},{...report,session_id:undefined,capabilities:[row]}).settings,false);
});

test('workspace attachment sending requires its own qualification and the qualified mail send dependencies',()=>{
 const definitions=JSON.parse(readFileSync(new URL('../src/shared/iscp-operations.json',import.meta.url),'utf8'));
 const full={...manifest,operations:definitions.map(operation=>operation.name)};
 const capabilities=ISCP_SURFACES.map(id=>({...row,id}));
 assert.equal(projectISCPCapabilities(full,{...full,capabilities}).surfaces.mail_send_attachments.enabled,true);
 for(const field of ['qualified','supported','permitted','dependencies_ready','enabled']) {
   const result=projectISCPCapabilities(full,{...full,capabilities:capabilities.map(value=>value.id==='mail_send_attachments'?{...value,[field]:false}:value)});
   assert.equal(result.surfaces.mail_send_attachments.enabled,false);
   assert.equal(result.surfaces.mail_send.enabled,true);
 }
 for(const dependency of ['mail_send','mail_read','files','approvals','events']) {
   const result=projectISCPCapabilities(full,{...full,capabilities:capabilities.map(value=>value.id===dependency?{...value,qualified:false}:value)});
   assert.equal(result.surfaces.mail_send_attachments.enabled,false);
 }
 assert.equal(projectISCPCapabilities(full,{...full,capabilities:capabilities.filter(value=>value.id!=='mail_send_attachments')}).surfaces.mail_send_attachments.enabled,false);
});

test('mail settings qualify independently before any mailbox exists and expire with the report', () => {
  const operations = ['mail.providers.list','mail.providers.update','mail.providers.check','mail.providers.login'];
  const full = {...manifest,operations};
  const capabilities = [{...row,id:'mail_settings'}];
  const projection = projectISCPCapabilities(full,{...full,capabilities});
  assert.equal(projection.surfaces.mail_settings.enabled,true);
  assert.equal(projection.settings,true);
  assert.equal(projection.mail,false);
  assert.equal(projectISCPCapabilities(full,{...full,capabilities},Date.now()+120000).surfaces.mail_settings.enabled,false);
  for(const name of operations)assert.equal(projectISCPCapabilities({...full,operations:operations.filter(value=>value!==name)},{...full,capabilities}).surfaces.mail_settings.enabled,false);
});

test('original mail popup has its complete qualification gate independent of provider send readiness', () => {
 const definitions=JSON.parse(readFileSync(new URL('../src/shared/iscp-operations.json',import.meta.url),'utf8'));
 const full={...manifest,operations:definitions.map(operation=>operation.name)};
 const capabilities=ISCP_SURFACES.map(id=>({...row,id}));
 assert.equal(projectISCPCapabilities(full,{...full,capabilities}).mail,true);
 for (const id of ['mail_popup']) assert.equal(projectISCPCapabilities(full,{...full,capabilities:capabilities.map(value=>value.id===id?{...value,qualified:false}:value)}).mail,false);
 for (const operation of ['mail.conversations.list','mail.classification','mail.file','mail.replies.polish','mail.intake.update','object.read']) assert.equal(projectISCPCapabilities({...full,operations:full.operations.filter(value=>value!==operation)},{...full,capabilities}).mail,false);
 assert.equal(projectISCPCapabilities(full,{...full,capabilities:capabilities.filter(value=>value.id!=='mail_popup')}).mail,false,'old cache qualification cannot enable the original popup');
 assert.equal(projectISCPCapabilities(full,{...full,capabilities:capabilities.map(value=>['mail_send','mail_send_attachments','mail_settings'].includes(value.id)?{...value,dependencies_ready:false}:value)}).mail,true,'login and browsing stay available when sending is unavailable');
});
