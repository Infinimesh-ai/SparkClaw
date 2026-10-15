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
const runtime=process.env.APP_CLI_RECOVERY_RUNTIME_ROOT?pathToFileURL(path.resolve(process.env.APP_CLI_RECOVERY_RUNTIME_ROOT)+path.sep):new URL('../',import.meta.resolve('@infinimesh/app-cli-runtime/release'));
const {uploadManagedAttachments,verifyManagedAttachments}=await import(new URL('applications/mail/lib/workspace-attachments.mjs',runtime));
const enabled=process.env.SPARKCLAW_MAIL_BROWSER_TEST==='1';
async function fixture(t){
  const directory=await fs.realpath(await fs.mkdtemp(path.join(os.tmpdir(),'qq-native-card-')));t.after(()=>fs.rm(directory,{recursive:true,force:true}));
  const server=http.createServer((req,res)=>{res.setHeader('content-type','text/html; charset=utf-8');res.end(`<!doctype html><div class="mail-compose-page" id="composer"><div contenteditable="true">Synthetic body</div><button id="send">Send</button><div class="mail-compose-attaches-wrap"><input class="attach-file-input" type="file"><div class="mail-compose-attaches"><div class="attach-cards"></div></div></div><div class="draft-body-attaches-float-bar-wrap" style="height:0;overflow:hidden"><div class="mail-compose-attaches-float-bar compose-float-bar-hide"></div></div></div><div id="foreign"></div>`);});
  await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));t.after(()=>new Promise(resolve=>server.close(resolve)));
  const browser=await chromium.launch({headless:true});t.after(()=>browser.close());const page=await browser.newPage();await page.goto(`http://127.0.0.1:${server.address().port}/`);
  await page.evaluate(()=>{
    const root=document.getElementById('composer'),input=root.querySelector('input');window.uploads=[];window.sendClicks=0;
    window.__sparkclawManagedMail={provider:'qq_mail',ownershipChecked:true,root,body:root.querySelector('[contenteditable]'),send:root.querySelector('#send')};
    root.querySelector('#send').onclick=()=>sendClicks++;
    input.onchange=async()=>{for(const file of input.files){
      uploads.push({name:file.name,bytes:Array.from(new Uint8Array(await file.arrayBuffer()))});
      const card=document.createElement('div');card.className='mail-compose-attach-card attach-card-success';
      card.innerHTML='<div class="attach-card-content"><div class="attach-file-info"><div class="xmail-cmp-attach-icon">Icon</div><div class="file-name-wrap"><div class="attach-name"></div><div class="attach-suffix"></div></div><div class="attach-size">Size</div></div><div class="attach-btns">Controls</div></div>';
      card.querySelector('.attach-name').textContent=file.name.slice(0,-4);card.querySelector('.attach-suffix').textContent=file.name.slice(-4);
      root.querySelector('.attach-cards').append(card);
      root.querySelector('.mail-compose-attaches-float-bar').append(card.cloneNode(true));
    }};
  });
  const bytes=Buffer.from('Approved synthetic bytes\n'),name='synthetic-proof.txt',relative=`.sparkclaw-mail-send-${'c'.repeat(32)}/00/${name}`;
  await fs.mkdir(path.dirname(path.join(directory,relative)),{recursive:true,mode:0o700});await fs.writeFile(path.join(directory,relative),bytes,{mode:0o600});
  const manifest=[{name,path:relative,size_bytes:bytes.length,sha256:'sha256:'+crypto.createHash('sha256').update(bytes).digest('hex')}];
  let effects=0;const tab={attachmentSecretSlots:100,setAttachmentSecrets:async()=>{},inspect:async code=>({result:await page.evaluate('('+code+')()')}),runReadCode:async code=>vm.runInNewContext('('+code+')',{page})(page)};
  await uploadManagedAttachments(tab,'qq_mail',directory,manifest,()=>effects++);
  return {page,tab,manifest,bytes,effects};
}
test('QQ canonical success card accepts exact split filename and verified bytes while excluding hidden mirror',{skip:!enabled},async t=>{
  const f=await fixture(t);await verifyManagedAttachments(f.tab,'qq_mail',f.manifest);assert.equal(f.effects,1);
  assert.deepEqual(await f.page.evaluate(()=>uploads),[{name:'synthetic-proof.txt',bytes:[...f.bytes]}]);
  await f.page.evaluate(()=>{document.querySelector('input').files=new DataTransfer().files;});
  await verifyManagedAttachments(f.tab,'qq_mail',f.manifest);assert.equal(await f.page.evaluate(()=>sendClicks),0);
});
for(const mode of ['visible-mirror','foreign-card','mirror-only','hidden-primary','hidden-ancestor','wrong-name','partial-name','extra-success','extra-pending','extra-failed','not-success','ambiguous-name','foreign-wrap','moved-input','wrong-byte-proof','detached-body','foreign-send'])test(`QQ success-card proof rejects ${mode}`,{skip:!enabled},async t=>{
  const f=await fixture(t);
  await f.page.evaluate(mode=>{
    const root=document.getElementById('composer'),cards=root.querySelector('.attach-cards'),card=cards.firstElementChild,name=card.querySelector('.file-name-wrap'),state=__sparkclawManagedMail;
    if(mode==='visible-mirror')root.querySelector('.draft-body-attaches-float-bar-wrap').style.height='auto';
    if(mode==='foreign-card')root.append(card.cloneNode(true));
    if(mode==='mirror-only')card.remove();
    if(mode==='hidden-primary')card.style.visibility='hidden';
    if(mode==='hidden-ancestor'){cards.style.height='0';cards.style.overflow='hidden';}
    if(mode==='wrong-name')name.querySelector('.attach-name').textContent='different';
    if(mode==='partial-name')name.querySelector('.attach-suffix').remove();
    if(mode.startsWith('extra-')){const extra=card.cloneNode(true);if(mode!=='extra-success')extra.className='mail-compose-attach-card '+(mode==='extra-pending'?'attach-card-uploading':'attach-card-failed');cards.append(extra);}
    if(mode==='not-success')card.classList.remove('attach-card-success');
    if(mode==='ambiguous-name')name.parentElement.append(name.cloneNode(true));
    if(mode==='foreign-wrap'){const wrap=document.createElement('div');wrap.className='mail-compose-attaches-wrap';wrap.innerHTML='<div class="mail-compose-attaches"><div class="attach-cards"></div></div>';root.append(wrap);wrap.querySelector('.attach-cards').append(card);}
    if(mode==='moved-input')document.getElementById('foreign').append(root.querySelector('input'));
    if(mode==='wrong-byte-proof')state.attachmentVerifiedManifest[0].sha256='sha256:'+'0'.repeat(64);
    if(mode==='detached-body')state.body.remove();
    if(mode==='foreign-send')document.getElementById('foreign').append(state.send);
  },mode);
  await assert.rejects(verifyManagedAttachments(f.tab,'qq_mail',f.manifest),/email_(attachment|draft_fields)/);
  assert.equal(await f.page.evaluate(()=>sendClicks),0);
});
