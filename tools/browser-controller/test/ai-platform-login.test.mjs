import test from 'node:test';
import assert from 'node:assert/strict';
import {AI_PLATFORM_URLS, aiPlatformURL, classifyAILogin, inspectAILoginPage} from '../src/ai-platform-login.mjs';

test('four fixed login origins; no caller URLs or prototype keys',()=>{
 assert.deepEqual(Object.keys(AI_PLATFORM_URLS),['chatgpt','claude','gemini','grok']);
 for(const p of ['constructor','__proto__','https://evil.test']) assert.throws(()=>aiPlatformURL(p));
});
test('login requires both account and composer evidence; no export-script dependency',()=>{
 for(const provider of Object.keys(AI_PLATFORM_URLS)){
  const evidence={origin:new URL(aiPlatformURL(provider)).origin,path:'/',profile:true,composer:true,login:false,challenge:false};
  assert.equal(classifyAILogin(provider,evidence),'signed_in');
  assert.equal(classifyAILogin(provider,{...evidence,profile:false}),'unconfirmed');
  assert.equal(classifyAILogin(provider,{...evidence,composer:false}),'unconfirmed');
  assert.equal(classifyAILogin(provider,{...evidence,login:true}),'signed_out');
  assert.equal(classifyAILogin(provider,{...evidence,path:'/login'}),'signed_out');
  assert.equal(classifyAILogin(provider,{...evidence,challenge:true}),'user_action_required');
  assert.equal(classifyAILogin(provider,{...evidence,origin:'https://evil.test'}),'unconfirmed');
 }
 assert.equal(classifyAILogin('gemini',{origin:'https://accounts.google.com',path:'/signin'}),'user_action_required');
 assert.doesNotMatch(inspectAILoginPage.toString(),/export-json|Tampermonkey|cookie|localStorage|sessionStorage/);
});
