import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import fs from 'node:fs/promises';
import {unlinkSync, symlinkSync} from 'node:fs';
import http from 'node:http';
import os from 'node:os';
import path from 'node:path';
import vm from 'node:vm';
import test from 'node:test';
import {chromium} from 'playwright';
const runtime = new URL('../', import.meta.resolve('@infinimesh/app-cli-runtime/release'));
const {uploadManagedAttachments, verifyManagedAttachments} = await import(new URL('applications/mail/lib/workspace-attachments.mjs', runtime));

const enabled = process.env.SPARKCLAW_MAIL_BROWSER_TEST === '1';
const digest = bytes => 'sha256:' + crypto.createHash('sha256').update(bytes).digest('hex');
async function fixture(t, provider='gmail') {
  const directory = await fs.realpath(await fs.mkdtemp(path.join(os.tmpdir(),'workspace-mail-browser-')));
  t.after(()=>fs.rm(directory,{recursive:true,force:true}));
  const uploads=[];
  const server=http.createServer(async(req,res)=>{
    if(req.method==='POST'&&req.url==='/upload'){
      const chunks=[];for await(const chunk of req)chunks.push(chunk);
      const data=await new Response(Buffer.concat(chunks),{headers:{'content-type':req.headers['content-type']}}).formData();
      for(const file of data.getAll('files'))uploads.push({name:file.name,bytes:Buffer.from(await file.arrayBuffer())});
      res.setHeader('content-type','application/json');res.end('{}');return;
    }
    res.setHeader('content-type','text/html');
    res.end('<!doctype html><div id="composer"><input type="file" multiple><div contenteditable="true">body</div><button id="send">Send</button><div id="attachments" class="aQH"></div></div><div id="other"></div>');
  });
  await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
  t.after(()=>new Promise(resolve=>server.close(resolve)));
  const browser=await chromium.launch({headless:true});t.after(()=>browser.close());
  const page=await browser.newPage();await page.goto(`http://127.0.0.1:${server.address().port}/`);
  await page.evaluate(provider=>{
    const root=document.getElementById('composer'),input=root.querySelector('input');
    globalThis.__sparkclawManagedMail={provider,ownershipChecked:true,root,body:root.querySelector('[contenteditable]'),send:root.querySelector('#send')};
    input.addEventListener('change',async()=>{
      const progress=document.createElement('div');progress.setAttribute('role','progressbar');progress.textContent='Uploading';root.append(progress);
      const form=new FormData();for(const file of input.files)form.append('files',file);
      await fetch('/upload',{method:'POST',body:form});
      for(const file of input.files){
        const row=document.createElement('div');row.className=provider==='gmail'?'dL':'attachment-item';
        if(provider==='outlook')row.setAttribute('data-attachment-id',crypto.randomUUID());
        const name=document.createElement('span');name.className='vK';name.textContent=file.name;
        const remove=document.createElement('button');remove.setAttribute('aria-label','Remove attachment');remove.textContent='Remove';
        row.append(name,remove);root.querySelector('#attachments').append(row);
      }
      progress.remove();
    });
  },provider);
  const bytes=crypto.randomBytes(100003), relative=`.sparkclaw-mail-send-${'a'.repeat(32)}/00/report.bin`;
  const file=path.join(directory,relative);await fs.mkdir(path.dirname(file),{recursive:true,mode:0o700});await fs.writeFile(file,bytes,{mode:0o600});
  const manifest=[{path:relative,name:'report.bin',size_bytes:bytes.length,sha256:digest(bytes)}];
  let secrets=[],effects=0;
  const tab={
    attachmentSecretSlots:100,
    setAttachmentSecrets:async values=>{secrets=values;},
    inspect:async code=>({result:await page.evaluate('('+code+')()')}),
    runReadCode:async code=>{
      assert.ok(Buffer.byteLength(code)<=65536,'each existing Host call stays within its code budget');
      const chunk=code.match(/"data":"([A-Za-z0-9+/=]+)"/u)?.[1];
      if(chunk)assert.ok(secrets.includes(chunk),'binary chunk is registered for diagnostic redaction before execution');
      assert.equal(code.includes(relative),false,'the driver never receives a filesystem pathname');
      return await vm.runInNewContext('('+code+')',{page})(page);
    },
  };
  return {page,tab,directory,file,bytes,manifest,uploads,before:()=>{effects++;},effects:()=>effects,secrets:()=>secrets,provider};
}

for(const provider of ['gmail','outlook','qq_mail'])test(`real browser file input delivers exact frozen bytes: ${provider}`,{skip:!enabled},async t=>{
  const f=await fixture(t,provider);
  await uploadManagedAttachments(f.tab,provider,f.directory,f.manifest,f.before);
  await verifyManagedAttachments(f.tab,provider,f.manifest);
  assert.equal(f.effects(),1);assert.equal(f.uploads.length,1);assert.deepEqual(f.uploads[0],{name:'report.bin',bytes:f.bytes});
  assert.deepEqual(f.secrets(),[]);
  assert.equal(await f.page.evaluate(()=>!!globalThis.__sparkclawManagedMail.attachmentTransfer),false);
});

test('replacing the stage with an outside symlink cannot change uploaded bytes',{skip:!enabled},async t=>{
  const f=await fixture(t),outsideDirectory=await fs.mkdtemp(path.join(os.tmpdir(),'outside-mail-fixture-'));
  t.after(()=>fs.rm(outsideDirectory,{recursive:true,force:true}));
  const outside=path.join(outsideDirectory,'synthetic.bin');
  await fs.writeFile(outside,Buffer.alloc(f.bytes.length,0x7f));
  await uploadManagedAttachments(f.tab,'gmail',f.directory,f.manifest,()=>{
    f.before();unlinkSync(f.file);symlinkSync(outside,f.file);
  });
  assert.equal(f.uploads.length,1);assert.deepEqual(f.uploads[0].bytes,f.bytes);
});

for(const timing of ['before_dispatch','during_hash'])test(`moving the authorized input prevents upload: ${timing}`,{skip:!enabled},async t=>{
  const f=await fixture(t),run=f.tab.runReadCode;
  f.tab.runReadCode=async code=>{
    if(code.includes('return page.evaluate(async token'))await f.page.evaluate(timing=>{
      const move=()=>document.querySelector('#other').append(document.querySelector('input'));
      if(timing==='before_dispatch')move();
      else {const digest=crypto.subtle.digest.bind(crypto.subtle);crypto.subtle.digest=(...args)=>{move();return digest(...args);};}
    },timing);
    return run(code);
  };
  await assert.rejects(uploadManagedAttachments(f.tab,'gmail',f.directory,f.manifest,f.before),/email_attachment_control_unavailable/);
  assert.equal(f.uploads.length,0);
});

test('send recheck rejects missing, extra, pending and error attachment rows',{skip:!enabled},async t=>{
  const f=await fixture(t);await uploadManagedAttachments(f.tab,'gmail',f.directory,f.manifest,f.before);
  for(const mode of ['missing','extra','pending','error']){
    const restore=await f.page.evaluate(mode=>{
      const root=document.getElementById('attachments'),html=root.innerHTML;
      if(mode==='missing')root.replaceChildren();
      if(mode==='extra')root.append(root.firstChild.cloneNode(true));
      if(mode==='pending')root.insertAdjacentHTML('beforeend','<div role="progressbar">Uploading</div>');
      if(mode==='error')root.insertAdjacentHTML('beforeend','<div role="alert">Upload failed</div>');
      return html;
    },mode);
    await assert.rejects(verifyManagedAttachments(f.tab,'gmail',f.manifest),/email_attachment_upload/);
    await f.page.evaluate(html=>{document.getElementById('attachments').innerHTML=html;},restore);
  }
  await f.page.evaluate(()=>{
    const transfer=new DataTransfer();transfer.items.add(new File(['changed'],'report.bin'));
    document.querySelector('input').files=transfer.files;
  });
  await assert.rejects(verifyManagedAttachments(f.tab,'gmail',f.manifest),/email_attachment_changed/);
});
