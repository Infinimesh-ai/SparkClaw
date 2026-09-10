import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import vm from 'node:vm';
import {webcrypto} from 'node:crypto';
const source = await fs.readFile(new URL('../../../scripts/email/userscripts/qq-internal-reader.user.js', import.meta.url), 'utf8');
const eml='From: sender@example.com\r\nMessage-ID: <test@example.com>\r\nMIME-Version: 1.0\r\nContent-Type: text/plain\r\n\r\noriginal body';
function fixture({resources=true,ret=0,original=eml}={}) {
 const calls=[]; const window={}; window.top=window;
 window.fetch=async(url,options)=>{calls.push({url,options});const u=new URL(url);const body=u.pathname==='/list/maillist'?JSON.stringify({head:{ret},body:{total_num:1,list:[{emailid:'ZTEST',size:Buffer.byteLength(eml)}]}}):original;const response=new Response(body,{status:200});Object.defineProperty(response,'url',{value:url});return response;};
 const context=vm.createContext({window,URL,TextDecoder,Uint8Array,crypto:webcrypto,performance:{now:()=>performance.now(),getEntriesByType:()=>resources?[{name:'https://wx.mail.qq.com/list/maillist?func=1&dirid=1&sid=PRIVATE_TOKEN'}]:[]},AbortSignal,AbortController,structuredClone,location:{origin:'https://wx.mail.qq.com'},document:{readyState:'loading',addEventListener(){}},Blob,setTimeout});
 vm.runInContext(source,context);return {window,calls};
}
test('same-session GET reads fixed list and EML endpoints, report has no token or body',async()=>{
 const f=fixture();const report=await f.window.SparkClawQQReadTrial.run({count:1,rounds:2});
 assert.equal(report.actualCount,1);assert.equal(report.samples.length,2);assert.ok(report.samples.every(s=>s.originals[0].sizeMatches));
 assert.equal(f.calls.length,4);
 for(const c of f.calls){assert.equal(c.options.method,'GET');assert.equal(c.options.redirect,'error');assert.equal(c.options.credentials,'same-origin');assert.equal(c.options.cache,'no-store');const u=new URL(c.url);assert.equal(u.origin,'https://wx.mail.qq.com');assert.ok(['/list/maillist','/read/readmail'].includes(u.pathname));if(u.pathname==='/read/readmail')assert.equal(u.searchParams.get('func'),'5');}
 for(const privateValue of ['PRIVATE_TOKEN','original body','sender@example.com','ZTEST'])assert.ok(!JSON.stringify(report).includes(privateValue));
});
test('missing authenticated list does not issue a guessed request',async()=>{
 const f=fixture({resources:false});await assert.rejects(f.window.SparkClawQQReadTrial.run(),/登录/);assert.equal(f.calls.length,0);
});
test('login/error envelope cannot be treated as mails',async()=>{
 const f=fixture({ret:-1});await assert.rejects(f.window.SparkClawQQReadTrial.run(),/成功列表/);assert.equal(f.calls.length,1);
});
test('HTML response cannot be exported as a successful original',async()=>{
 const f=fixture({original:'<html>login</html>'});await assert.rejects(f.window.SparkClawQQReadTrial.run(),/EML/);assert.throws(()=>f.window.SparkClawQQReadTrial.exportEML(),/不存在/);
});
test('bounds calls and clears API on dispose',async()=>{
 const f=fixture();await assert.rejects(f.window.SparkClawQQReadTrial.run({count:100}),/范围/);assert.equal(f.calls.length,0);f.window.SparkClawQQReadTrial.dispose();assert.equal(f.window.SparkClawQQReadTrial,undefined);
});
