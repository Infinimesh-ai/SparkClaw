import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import fs from 'node:fs/promises';
import http from 'node:http';
import os from 'node:os';
import path from 'node:path';
import {pathToFileURL} from 'node:url';
import vm from 'node:vm';
import test from 'node:test';
import {chromium} from 'playwright';
const runtime = process.env.APP_CLI_RECOVERY_RUNTIME_ROOT
  ? pathToFileURL(path.resolve(process.env.APP_CLI_RECOVERY_RUNTIME_ROOT) + path.sep)
  : new URL('../',import.meta.resolve('@infinimesh/app-cli-runtime/release'));
const {uploadManagedAttachments,verifyManagedAttachments,attachmentDOM}=await import(new URL('applications/mail/lib/workspace-attachments.mjs',runtime));
const enabled=process.env.SPARKCLAW_MAIL_BROWSER_TEST==='1';
const sha=bytes=>'sha256:'+crypto.createHash('sha256').update(bytes).digest('hex');

async function fixture(t,mode='normal') {
  const root=await fs.realpath(await fs.mkdtemp(path.join(os.tmpdir(),'mail-native-ribbon-')));
  t.after(()=>fs.rm(root,{recursive:true,force:true}));
  const server=http.createServer((req,res)=>{res.setHeader('content-type','text/html; charset=utf-8');res.end(`<!doctype html>
    <div data-automation-type="RibbonBottomBarContainer"><button data-automation-type="RibbonFlyoutAnchor" aria-haspopup="true" aria-expanded="false" aria-label="附加文件">Attach</button></div>
    <div id="global-inputs"><input id="image" type="file" accept="image/*" data-testid="local-computer-filein" multiple hidden><input id="decoy1" type="file" data-testid="local-computer-filein" multiple hidden><input id="actual" type="file" data-testid="local-computer-filein" multiple hidden><input id="decoy2" type="file" data-testid="local-computer-filein" multiple hidden></div>
    <div id="composer"><div contenteditable="true" aria-label="邮件正文">Synthetic body</div><button id="send">发送</button><div id="rows"></div></div><div id="other"></div>`);});
  await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));t.after(()=>new Promise(resolve=>server.close(resolve)));
  const browser=await chromium.launch({headless:true});t.after(()=>browser.close());
  const page=await browser.newPage();await page.goto(`http://127.0.0.1:${server.address().port}/`);
  await page.evaluate(mode=>{
    const root=document.getElementById('composer'),body=root.querySelector('[contenteditable]'),button=document.querySelector('[data-automation-type="RibbonFlyoutAnchor"]');
    window.__sparkclawManagedMail={provider:'outlook',ownershipChecked:true,root,body,send:document.getElementById('send')};
    window.uploads=[];window.sendClicks=0;window.__sparkclawManagedMail.send.onclick=()=>window.sendClicks++;
    button.onclick=()=>{
      button.setAttribute('aria-expanded','true');
      const menu=document.createElement('div');menu.setAttribute('role','menu');
      const action=document.createElement('button');action.setAttribute('role','menuitem');action.textContent='浏览此计算机';menu.append(action);
      if(mode==='ambiguous-menu')menu.append(action.cloneNode(true));
      action.onclick=()=>{
        const input=document.getElementById(mode==='image'?'image':'actual');
        if(mode==='unrelated-chooser')setTimeout(()=>input.click(),30);else input.click();
      };
      document.body.append(menu);
    };
    for(const input of document.querySelectorAll('input[type="file"]'))input.addEventListener('change',async()=>{
      for(const file of input.files){
        uploads.push({id:input.id,name:file.name,bytes:Array.from(new Uint8Array(await file.arrayBuffer()))});
        const row=document.createElement('div');row.setAttribute('data-attachment-id','synthetic-native-row');
        const name=document.createElement('span');name.setAttribute('title',file.name);name.textContent=file.name;
        const remove=document.createElement('button');remove.setAttribute('aria-label','Remove attachment');remove.textContent='Remove';row.append(name,remove);document.getElementById('rows').append(row);
      }
    });
    if(mode==='owned-chooser') {
      const own=document.createElement('button');own.setAttribute('aria-label','Attach files');own.textContent='Attach';root.append(own);
      own.onclick=()=>{const input=document.getElementById('actual');root.append(input);input.click();};
    }
    if(mode==='ambiguous-composer')document.getElementById('other').innerHTML='<div contenteditable="true" aria-label="邮件正文">Decoy</div>';
    if(mode==='ambiguous-ribbon')button.parentElement.append(button.cloneNode(true));
  },mode);
  const bytes=Buffer.from('Synthetic approved workspace attachment\n');
  const relative=`.sparkclaw-mail-send-${'b'.repeat(32)}/00/native.txt`, filename=path.join(root,relative);
  await fs.mkdir(path.dirname(filename),{recursive:true,mode:0o700});await fs.writeFile(filename,bytes,{mode:0o600});
  const manifest=[{path:relative,name:'native.txt',size_bytes:bytes.length,sha256:sha(bytes)}];
  let effects=0;
  const tab={attachmentSecretSlots:100,setAttachmentSecrets:async()=>{},inspect:async code=>({result:await page.evaluate('('+code+')()')}),
    runReadCode:async code=>{assert.ok(Buffer.byteLength(code)<=65536);return vm.runInNewContext('('+code+')',{page})(page);}};
  return {page,tab,root,bytes,manifest,before:()=>effects++,effects:()=>effects};
}

test('shared Outlook Ribbon binds native chooser to the owned composer, never global input order',{skip:!enabled},async t=>{
  const f=await fixture(t);await uploadManagedAttachments(f.tab,'outlook',f.root,f.manifest,f.before);await verifyManagedAttachments(f.tab,'outlook',f.manifest);
  assert.deepEqual(await f.page.evaluate(()=>uploads),[{id:'actual',name:'native.txt',bytes:[...f.bytes]}]);
  assert.equal(f.effects(),1);assert.equal(await f.page.evaluate(()=>sendClicks),0);
});
test('owned native chooser is checked in the main document, not its isolated utility world',{skip:!enabled},async t=>{
  const f=await fixture(t,'owned-chooser');await uploadManagedAttachments(f.tab,'outlook',f.root,f.manifest,f.before);await verifyManagedAttachments(f.tab,'outlook',f.manifest);
  assert.deepEqual(await f.page.evaluate(()=>uploads),[{id:'actual',name:'native.txt',bytes:[...f.bytes]}]);assert.equal(await f.page.evaluate(()=>sendClicks),0);
});
test('shared input relocation after ready is rejected by the final verification',{skip:!enabled},async t=>{
  const f=await fixture(t);await uploadManagedAttachments(f.tab,'outlook',f.root,f.manifest,f.before);
  await f.page.evaluate(()=>document.getElementById('other').append(document.getElementById('actual')));
  await assert.rejects(verifyManagedAttachments(f.tab,'outlook',f.manifest),/email_attachment_control_unavailable/);assert.equal(await f.page.evaluate(()=>sendClicks),0);
});
for(const mode of ['ambiguous-composer','ambiguous-ribbon','ambiguous-menu','image','unrelated-chooser'])test(`shared chooser fails closed: ${mode}`,{skip:!enabled},async t=>{
  const f=await fixture(t,mode);await assert.rejects(uploadManagedAttachments(f.tab,'outlook',f.root,f.manifest,f.before),/email_(attachment_control_unavailable|draft_fields_unverified)/);
  assert.deepEqual(await f.page.evaluate(()=>uploads),[]);assert.equal(await f.page.evaluate(()=>sendClicks),0);
  if(mode==='ambiguous-composer'||mode==='ambiguous-ribbon')assert.equal(f.effects(),0);
});
for(const mode of ['relocated-input','replaced-input','changed-composer'])test(`shared chooser binding remains pinned after native selection: ${mode}`,{skip:!enabled},async t=>{
  const f=await fixture(t),run=f.tab.runReadCode;
  f.tab.runReadCode=async code=>{
    if(code.includes('return page.evaluate(async token'))await f.page.evaluate(mode=>{
      const original=crypto.subtle.digest.bind(crypto.subtle);let changed=false;
      crypto.subtle.digest=(...args)=>{if(!changed){changed=true;const input=document.getElementById('actual');
        if(mode==='relocated-input')document.getElementById('other').append(input);
        else if(mode==='replaced-input')input.replaceWith(input.cloneNode(true));
        else document.getElementById('other').innerHTML='<div contenteditable="true" aria-label="邮件正文">Second composer</div>';
      }return original(...args);};
    },mode);
    return run(code);
  };
  await assert.rejects(uploadManagedAttachments(f.tab,'outlook',f.root,f.manifest,f.before),/email_attachment_control_unavailable/);
  assert.deepEqual(await f.page.evaluate(()=>uploads),[]);assert.equal(await f.page.evaluate(()=>sendClicks),0);
});

test('pre-dispatch diagnostic contains bounded structural facts, no subject/body/filename text',{skip:!enabled},async t=>{
  const f=await fixture(t);
  const value=await f.page.evaluate('('+attachmentDOM.toString()+')('+JSON.stringify('outlook')+',"diagnostic",'+JSON.stringify(f.manifest)+')');
  assert.equal(value.owned,true);assert.equal(value.known_rows,0);
  assert.ok(JSON.stringify(value).length<16000);
  assert.doesNotMatch(JSON.stringify(value),/Synthetic body|native\.txt|workspace/);
  await f.page.evaluate(()=>{window.__sparkclawManagedMail.sendAttempted=true;});
  assert.deepEqual(await f.page.evaluate('('+attachmentDOM.toString()+')("outlook","diagnostic",[])'),{error:'email_draft_fields_unverified'});
});
