import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import {captureAIChat,validateAIChatURL} from '../src/ai-chat-export.mjs';

test('only canonical provider conversation URLs are accepted',()=>{
 for(const [p,u] of [['chatgpt','https://chatgpt.com/c/abc'],['claude','https://claude.ai/chat/abc'],['gemini','https://gemini.google.com/app/abc'],['grok','https://grok.com/c/abc']]) assert.equal(validateAIChatURL(p,u),u);
 for(const u of ['https://chatgpt.com.evil/c/a','http://chatgpt.com/c/a','https://chatgpt.com/c/a?token=x','https://chatgpt.com/','https://user@chatgpt.com/c/a']) assert.throws(()=>validateAIChatURL('chatgpt',u));
});
test('listener precedes bridge command, original download is returned, staging is removed',async()=>{
 const dir=await fs.mkdtemp(path.join(os.tmpdir(),'ai-chat-test-'));
 const url='https://chatgpt.com/c/abc';
 const raw=Buffer.from(JSON.stringify({url,author:'chatgpt',exporter:'3.1.0',messages:[{author:'user',content:'hello'},{author:'ai',content:'OK'}]}));
 let listening=false,deleted=false;
 const download={url:()=>`blob:https://chatgpt.com/test`,saveAs:p=>fs.writeFile(p,raw),failure:async()=>null,delete:async()=>{deleted=true}};
 const page={url:()=>url,locator:()=>({waitFor:async()=>{},evaluate:async()=>assert.ok(listening,'must listen before command'),getAttribute:async()=>'ready',innerText:async()=>''}),evaluate:async()=>true,waitForFunction:async()=>{},waitForEvent:()=>{listening=true;return Promise.resolve(download)}};
 try{
  const out=await captureAIChat({provider:'chatgpt',url,outputDir:dir,runCode:code=>new Function(`return (${code})`)()(page)});
  assert.deepEqual(Buffer.from(out.content_base64,'base64'),raw);assert.equal(deleted,true);assert.deepEqual(await fs.readdir(dir),[]);
 }finally{await fs.rm(dir,{recursive:true,force:true})}
});
test('streaming response fails before commanding or downloading',async()=>{
 const dir=await fs.mkdtemp(path.join(os.tmpdir(),'ai-chat-test-'));
 const url='https://chatgpt.com/c/abc';let clicked=false;
 const page={url:()=>url,locator:()=>({waitFor:async()=>{},evaluate:async()=>{clicked=true}}),evaluate:async()=>false};
 try{await assert.rejects(captureAIChat({provider:'chatgpt',url,outputDir:dir,runCode:code=>new Function(`return (${code})`)()(page)}),/loading_unverified/);assert.equal(clicked,false)}finally{await fs.rm(dir,{recursive:true,force:true})}
});

test('native Chromium download plumbing (synthetic page, not platform qualification)', {skip:!process.env.SPARKCLAW_TEST_CHROMIUM}, async()=>{
 const {chromium}=await import('playwright');
 const vm=await import('node:vm');
 const dir=await fs.mkdtemp(path.join(os.tmpdir(),'ai-chat-native-'));
 const browser=await chromium.launch({headless:true,executablePath:process.env.SPARKCLAW_TEST_CHROMIUM});
 try{
  const context=await browser.newContext({acceptDownloads:true});
  const page=await context.newPage();
  const url='https://chatgpt.com/c/fixture';
  const doc={url,author:'chatgpt',exporter:'3.1.0',messages:[{author:'user',content:'测试'},{author:'ai',content:'answer\n\ncode'}]};
  await page.route('**/*',r=>r.fulfill({contentType:'text/html',body:'<output id="sparkclaw-ai-export-bridge" hidden data-state="ready"></output>'}));
  await page.goto(url);
  await page.evaluate(doc=>{const bridge=document.querySelector('#sparkclaw-ai-export-bridge');bridge.addEventListener('sparkclaw-ai-export-command',()=>{const a=document.createElement('a');a.href=URL.createObjectURL(new Blob([JSON.stringify(doc)],{type:'application/json'}));a.download='conversation.json';a.click();bridge.dataset.state='ready';});},doc);
  assert.equal(await page.locator('#export-controls-container, #export-outline-container, #sparkclaw-batch-controls').count(),0);
  for(let i=0;i<3;i++){
   const output=await captureAIChat({provider:'chatgpt',url,outputDir:dir,runCode:code=>vm.runInNewContext(`(${code})(page)`,{page})});
   assert.deepEqual(JSON.parse(Buffer.from(output.content_base64,'base64').toString()),doc);
   assert.deepEqual(await fs.readdir(dir),[]);
  }
 }finally{await browser.close();await fs.rm(dir,{recursive:true,force:true})}
});
