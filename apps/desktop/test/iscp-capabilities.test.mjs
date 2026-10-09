import test from 'node:test';
import assert from 'node:assert/strict';
import { projectISCPCapabilities } from '../src/main/iscp-capabilities.mjs';
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
