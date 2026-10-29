import { app, BrowserWindow, protocol, session } from 'electron';
import assert from 'node:assert/strict';
import path from 'node:path';
import fs from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { pdfRangeFixture } from './pdf-range-fixture.mjs';
import { createLocalPreviewProtocolHandler, previewRequestAllowed, guardLocalPreviewNavigation } from '../src/main/local-preview-protocol.mjs';
const temporary=process.env.SPARKCLAW_PREVIEW_TEST_ROOT;
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'../../..');
app.setName('SparkX isolated document qualification');app.setPath('userData',path.join(temporary,'profile'));
protocol.registerSchemesAsPrivileged(['sparkclaw-app','sparkclaw-preview'].map(scheme=>({scheme,privileges:{standard:true,secure:true,supportFetchAPI:true,corsEnabled:true,stream:true}})));
const pause=ms=>new Promise(resolve=>setTimeout(resolve,ms));
const host=`<!doctype html><meta charset="utf-8"><style>html,body{margin:0;height:100%}iframe{width:100%;height:100%;border:0}</style><script>
window.sparkclawLocalFiles={secret:'workbench-only'};localStorage.setItem('workbench-secret','private');
let current,port,sequence=0;window.receipts=[];
window.openFixture=(document,bytes,model)=>{if(port){port.postMessage({type:'revoke'});port.close();}document={...document,id:'document-'+(++sequence),generation:sequence,loginGeneration:'login',language:'en'};current={document,bytes,model};const frame=documentCreate();return document.id;};
function documentCreate(){document.querySelector('iframe')?.remove();const frame=document.createElement('iframe');frame.sandbox='allow-scripts allow-same-origin';frame.src='sparkclaw-preview://viewer/index.html';document.body.append(frame);return frame;}
addEventListener('message',event=>{if(event.origin!=='sparkclaw-preview://viewer'||event.source!==document.querySelector('iframe')?.contentWindow||event.data?.type!=='sparkclaw-preview:ready')return;const channel=new MessageChannel();port=channel.port1;port.onmessage=({data:m})=>{window.receipts.push(m.operation||m.type);if(m.type!=='request')return;const value=m.operation==='read'?{bytes:new Uint8Array(current.bytes.slice(m.offset,m.offset+m.length))}:m.operation==='model'?current.model:undefined;port.postMessage({...m,type:'response',ok:value!==undefined,value,error:{message:'Unavailable fixture'}});};port.start();event.source.postMessage({type:'sparkclaw-preview:init',protocol:1,document:current.document},event.origin,[channel.port2]);});
</script><body></body>`;
app.whenReady().then(async()=>{
 app.dock?.hide();let window;
 try{
  const target=session.defaultSession;target.protocol.handle('sparkclaw-app',()=>new Response(host,{headers:{'content-type':'text/html'}}));target.protocol.handle('sparkclaw-preview',createLocalPreviewProtocolHandler(process.env.SPARKCLAW_PREVIEW_TEST_ASSETS || path.join(root,'apps/webchat/dist-preview')));
  target.webRequest.onBeforeRequest((details,callback)=>callback({cancel:!previewRequestAllowed(details)}));
  target.setPermissionRequestHandler((_wc,_permission,callback)=>callback(false));
  window=new BrowserWindow({show:false,width:1000,height:800,webPreferences:{sandbox:true,contextIsolation:true,nodeIntegration:false,backgroundThrottling:false}});guardLocalPreviewNavigation(window.webContents);window.webContents.setWindowOpenHandler(()=>({action:'deny'}));
  const errors=[];window.webContents.on('console-message',details=>{if(details.level==='error')errors.push(details.message);});
  await window.loadURL('sparkclaw-app://workbench/index.html');const run=expression=>Promise.race([window.webContents.executeJavaScript(expression),pause(5000).then(()=>{throw new Error('host execution timeout');})]);let frame;
  const until=async(fn,label)=>{const deadline=Date.now()+12000;while(Date.now()<deadline){if(await fn())return;await pause(20);}throw new Error(label+' '+JSON.stringify(errors));};
  const load=async(format,name,bytes,model,position)=>{const previousFrame=frame;await run(`openFixture(${JSON.stringify({format,name,size:bytes.length,position})},${JSON.stringify([...bytes])},${JSON.stringify(model||null)})`);await until(()=>{frame=window.webContents.mainFrame.frames.find(f=>f!==previousFrame&&f.url.startsWith('sparkclaw-preview://viewer/'));return Boolean(frame);},'frame did not load');};
  const screenshot=async name=>{if(process.env.SPARKCLAW_PREVIEW_SCREENSHOTS){await fs.mkdir(process.env.SPARKCLAW_PREVIEW_SCREENSHOTS,{recursive:true});await fs.writeFile(path.join(process.env.SPARKCLAW_PREVIEW_SCREENSHOTS,name+'.png'),(await window.capturePage()).toPNG());}};
  const evaluate=expression=>Promise.race([frame.executeJavaScript(expression),pause(5000).then(()=>{throw new Error('frame execution timeout '+expression);})]);
  await load('text','hello.txt',Buffer.from('Hello local preview\n你好世界'));
  await until(()=>evaluate("document.querySelector('pre')?.textContent.includes('你好世界')"),'text missing');
  await screenshot('text');const isolation=await evaluate(`(async()=>{let parentStorage=false,network=false,file=false;try{parent.localStorage.getItem('workbench-secret');}catch{parentStorage=true;}try{await fetch('https://example.com/');}catch{network=true;}try{await fetch('file:///etc/passwd');}catch{file=true;}return {bridge:typeof window.sparkclawLocalFiles,parentStorage,network,file};})()`);
  assert.deepEqual(isolation,{bridge:'undefined',parentStorage:true,network:true,file:true});
  const sheet={kind:'spreadsheet',warnings:[],sheets:[{id:'one',name:'First',rowCount:2,columnCount:2,merges:[],cells:[{row:0,column:0,value:'00123'},{row:0,column:1,value:0,formula:'1-1'},{row:1,column:0,value:'<img src=x onerror=alert(1)>'},{row:1,column:1,value:false,formula:'1=2'}]},{id:'two',name:'Second',rowCount:1,columnCount:1,merges:[],cells:[{row:0,column:0,value:'second sheet'}]}]};
  await load('spreadsheet','workbook.xlsx',Buffer.from('x'),sheet);
  await until(()=>evaluate("Boolean(document.querySelector('.readonlySheet canvas'))"),'FortuneSheet did not render');
  assert.equal(await evaluate("document.querySelector('.literalFormula output').textContent"),'00123');
  await evaluate(`(()=>{const target=document.querySelector('.readonlySheet');for(const key of ['a','Delete','Backspace','F2'])target.dispatchEvent(new KeyboardEvent('keydown',{key,bubbles:true,cancelable:true}));target.dispatchEvent(new Event('paste',{bubbles:true,cancelable:true}));target.dispatchEvent(new Event('drop',{bubbles:true,cancelable:true}));})()`);
  assert.equal(await evaluate("document.querySelector('.literalFormula output').textContent"),'00123');assert.equal(await evaluate("document.querySelectorAll('.readonlySheet img').length"),0);
  await screenshot('spreadsheet');const copy=await evaluate(`(()=>{const event=new ClipboardEvent('copy',{bubbles:true,cancelable:true,clipboardData:new DataTransfer()});document.querySelector('.readonlySheet').dispatchEvent(event);return {value:event.clipboardData.getData('text/plain'),prevented:event.defaultPrevented,html:event.clipboardData.getData('text/html')};})()`);await pause(50);assert.equal(copy.value,'00123');
  await evaluate("document.querySelectorAll('[role=tab]')[1].click()");await until(()=>evaluate("document.querySelector('.literalFormula output').textContent==='second sheet'"),'sheet switching failed');
  await load('markdown','unsafe.md',Buffer.from('# Local heading\n\n<script>window.compromised=true</script>\n\n![remote](https://example.com/image.png)\n\n[link](https://example.com)'));
  await until(()=>evaluate("document.querySelector('h1')?.textContent==='Local heading'"),'markdown missing');assert.equal(await evaluate("Boolean(window.compromised||document.querySelector('article script')||document.querySelector('article a[href]'))"),false);
  await screenshot('markdown');
  // A minimal single-page PDF, with exact cross-reference offsets.
  let pdf='%PDF-1.4\n';const offsets=[0];const objects=['<< /Type /Catalog /Pages 2 0 R >>','<< /Type /Pages /Kids [3 0 R] /Count 1 >>','<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>','<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>','<< /Length 42 >>\nstream\nBT /F1 12 Tf 20 150 Td (Local PDF) Tj ET\nendstream'];for(let i=0;i<objects.length;i++){offsets.push(Buffer.byteLength(pdf));pdf+=`${i+1} 0 obj\n${objects[i]}\nendobj\n`;}const xref=Buffer.byteLength(pdf);pdf+=`xref\n0 6\n0000000000 65535 f \n${offsets.slice(1).map(o=>String(o).padStart(10,'0')+' 00000 n \n').join('')}trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n${xref}\n%%EOF`;
  await load('pdf','fixture.pdf',Buffer.from(pdf));await until(()=>evaluate("Boolean(document.querySelector('.react-pdf__Page canvas'))"),'PDF canvas missing');
  await until(()=>evaluate("document.querySelector('.react-pdf__Page__textContent')?.textContent.includes('Local PDF')"),'PDF selectable text missing');await evaluate("new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)))");const canvas=await evaluate("(()=>{const c=document.querySelector('.react-pdf__Page canvas');return {width:c.width,height:c.height};})()");assert.ok(canvas.width*canvas.height<=8_000_000);
  await screenshot('pdf');const before=await evaluate('location.href');await evaluate("location.href='sparkclaw-app://workbench/index.html'");await pause(150);assert.equal(frame.url,before);
  const ranged=pdfRangeFixture();
  const expectPDFPage=async text=>until(async()=>{
    const problem=await evaluate("document.querySelector('[role=alert]')?.textContent");if(problem)throw new Error('ranged PDF: '+problem);
    return evaluate(`Boolean(document.querySelector('.react-pdf__Page canvas')) && document.querySelector('.react-pdf__Page__textContent')?.textContent.includes(${JSON.stringify(text)})`);
  },'ranged PDF page missing');
  await load('pdf','ranges.pdf',ranged);await expectPDFPage('Range page 1');
  await evaluate("document.querySelector('[aria-label=\"Next page\"]').click()");await expectPDFPage('Range page 2');
  await evaluate("document.querySelector('[aria-label=\"Zoom in\"]').click()");
  await until(()=>evaluate("document.querySelector('.previewToolbar').textContent.includes('125%')"),'PDF zoom missing');await expectPDFPage('Range page 2');
  await load('text','switch.txt',Buffer.from('Switch away while retaining PDF reading state'));
  await until(()=>evaluate("Boolean(document.querySelector('pre'))"),'switch-away text missing');
  await load('pdf','ranges.pdf',ranged,null,{page:3,zoom:1.25});await expectPDFPage('Range page 3');
  assert.equal(await evaluate("document.querySelectorAll('.react-pdf__Page canvas').length"),1);
  assert.ok(await evaluate("(()=>{const c=document.querySelector('.react-pdf__Page canvas');return c.width*c.height<=8000000})()"));
  await screenshot('pdf-ranges');
  if(process.env.SPARKCLAW_PREVIEW_TEST_PDF){
    const bytes=await fs.readFile(process.env.SPARKCLAW_PREVIEW_TEST_PDF);assert.ok(bytes.length<=32*1024*1024);
    await load('pdf','local-regression.pdf',bytes);
    await until(async()=>{const problem=await evaluate("document.querySelector('[role=alert]')?.textContent");if(problem)throw new Error('regression PDF: '+problem);return evaluate("Boolean(document.querySelector('.react-pdf__Page canvas'))");},'regression PDF canvas missing');
    await until(()=>evaluate("Boolean(document.querySelector('.react-pdf__Page__textContent')?.textContent.length)"),'regression PDF text missing');
    await screenshot('pdf-regression');
  }
  await load('image','pixel.png',Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jGv0AAAAASUVORK5CYII=','base64'));await until(()=>evaluate("document.querySelector('.imageViewport img')?.naturalWidth===1"),'image did not decode');
  await evaluate("dispatchEvent(new KeyboardEvent('keydown',{key:'Escape',bubbles:true}))");await until(()=>run("receipts.includes('escape')"),'Escape was not forwarded');
  const result={isolation,formats:['text','spreadsheet','markdown','pdf','image'],sheetCopy:copy,sheetSwitch:true,editingBlocked:true,pdfPixels:canvas.width*canvas.height,pdfRangeRead:true,pdfPageSwitch:true,pdfZoom:true,pdfPositionRestore:true,privatePDFChecked:Boolean(process.env.SPARKCLAW_PREVIEW_TEST_PDF),navigationBlocked:true};console.log(JSON.stringify(result));window.destroy();app.exit(0);
 }catch(error){console.error(error.stack);window?.destroy();app.exit(1);}
});
