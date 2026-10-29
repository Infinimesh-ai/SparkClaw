import { app, BrowserWindow, protocol, session } from 'electron';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { createLocalPreviewProtocolHandler, previewRequestAllowed, guardLocalPreviewNavigation } from '../src/main/local-preview-protocol.mjs';
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'../../..');
const temporary=process.env.SPARKCLAW_FORMAT_NATIVE_TEMP,fixtures=process.env.SPARKCLAW_FORMAT_FIXTURES,converted=process.env.SPARKCLAW_FORMAT_CONVERTED;
if(!temporary||!fixtures||!converted)throw new Error('Use run-local-preview-format-native.mjs');
app.setName('SparkX isolated format qualification');app.setPath('userData',path.join(temporary,'profile'));
protocol.registerSchemesAsPrivileged(['sparkclaw-app','sparkclaw-preview'].map(scheme=>({scheme,privileges:{standard:true,secure:true,supportFetchAPI:true,corsEnabled:true,stream:true}})));
const pause=ms=>new Promise(resolve=>setTimeout(resolve,ms));
const host=`<!doctype html><meta charset="utf-8"><style>html,body{margin:0;height:100%}iframe{width:100%;height:100%;border:0}</style><script>
let current,port,sequence=0,ready;window.receipts=[];
window.openFixture=(metadata,bytes,model)=>{ready=null;if(port){port.postMessage({type:'revoke'});port.close();}current={metadata:{...metadata,id:'format-'+(++sequence),generation:sequence,loginGeneration:'synthetic-login',language:'en'},bytes,model};document.querySelector('iframe')?.remove();const frame=document.createElement('iframe');frame.sandbox='allow-scripts allow-same-origin';frame.src='sparkclaw-preview://viewer/index.html';document.body.append(frame);};
addEventListener('message',event=>{if(event.origin==='sparkclaw-preview://viewer'&&event.source===document.querySelector('iframe')?.contentWindow&&event.data?.type==='sparkclaw-preview:ready')ready=event;});
window.startFixture=()=>{if(!ready)return false;const channel=new MessageChannel();port=channel.port1;port.onmessage=({data:m})=>{receipts.push(m.operation||m.type);if(m.type!=='request')return;const value=m.operation==='read'?{bytes:new Uint8Array(current.bytes.slice(m.offset,m.offset+m.length))}:m.operation==='model'?current.model:undefined;port.postMessage({...m,type:'response',ok:value!==undefined,value,error:{message:'Unavailable fixture'}});};port.start();ready.source.postMessage({type:'sparkclaw-preview:init',protocol:1,document:current.metadata},ready.origin,[channel.port2]);ready=null;return true;};
</script><body></body>`;
app.whenReady().then(async()=>{
 app.dock?.hide();let window;const results=[];
 try{
  const target=session.defaultSession;
  target.protocol.handle('sparkclaw-app',()=>new Response(host,{headers:{'content-type':'text/html'}}));
  target.protocol.handle('sparkclaw-preview',createLocalPreviewProtocolHandler(process.env.SPARKCLAW_PREVIEW_TEST_ASSETS||path.join(root,'apps/webchat/dist-preview')));
  target.webRequest.onBeforeRequest((details,callback)=>callback({cancel:!previewRequestAllowed(details)}));
  target.setPermissionRequestHandler((_wc,_permission,callback)=>callback(false));
  window=new BrowserWindow({show:false,width:1000,height:800,webPreferences:{sandbox:true,contextIsolation:true,nodeIntegration:false,backgroundThrottling:false}});
  guardLocalPreviewNavigation(window.webContents);window.webContents.setWindowOpenHandler(()=>({action:'deny'}));
  const errors=[];window.webContents.on('console-message',details=>{if(details.level==='error')errors.push(details.message);});
  await window.loadURL('sparkclaw-app://workbench/index.html');
  const run=expression=>window.webContents.executeJavaScript(expression);let frame;
  const evaluate=expression=>Promise.race([frame.executeJavaScript(expression),pause(5000).then(()=>{throw new Error('frame evaluation timeout');})]);
  const until=async(fn,label)=>{const deadline=Date.now()+12000;while(Date.now()<deadline){if(await fn())return;await pause(20);}throw new Error(label+' '+JSON.stringify(errors.slice(-5)));};
  const load=async(name,format,directory=fixtures,warnings,model)=>{
    const bytes=await fs.readFile(path.join(directory,name)),previous=frame;
    await run(`openFixture(${JSON.stringify({name,format,size:bytes.length,warnings})},${JSON.stringify([...bytes])},${JSON.stringify(model||null)})`);
    await until(()=>{frame=window.webContents.mainFrame.frames.find(value=>value!==previous&&value.url.startsWith('sparkclaw-preview://viewer/'));return Boolean(frame);},'frame did not load');
    await evaluate('window.passwordPrompts=0;window.prompt=()=>{window.passwordPrompts++;return null}');
    await until(()=>run('startFixture()'),'viewer handshake absent');
  };
  const cases=[
    ['encrypted.pdf','pdf','error'],['corrupt.pdf','pdf','error'],['valid.pdf','pdf','pdf'],
    ...['valid.png','valid.jpg','valid.webp','valid.gif','safe.svg'].map(name=>[name,'image','image']),
    ...['corrupt.png','oversized.png','active.svg','external.svg','animated.gif','unsupported.bmp'].map(name=>[name,'image','error']),
    ['utf8.txt','text','text'],['boundary.txt','text','boundary'],['binary.txt','text','error'],['invalid-utf8.txt','text','error'],
    ['unsafe.md','markdown','markdown'],
  ];
  const matrix=JSON.parse(await fs.readFile(path.join(converted,'matrix.json'),'utf8'));
  for(const row of matrix.rows.filter(value=>value.actual==='pdf'))cases.push([row.output,'pdf','pdf',converted,row.warnings]);
  for(const [name,format,expectation,directory,warnings] of cases){
    const started=Date.now();await load(name,format,directory,warnings);
    if(expectation==='error'){
      await until(()=>evaluate("Boolean(document.querySelector('[role=alert]')?.textContent)"),name+' did not fail visibly');
      const message=await evaluate("document.querySelector('[role=alert]').textContent");
      assert.equal(await evaluate('window.passwordPrompts'),0,name+' opened a native password prompt');
      if(name==='encrypted.pdf')assert.match(message,/Unlock it and save an unencrypted copy/);
      results.push({name,outcome:'rejected',message,elapsedMs:Date.now()-started});continue;
    }
    if(expectation==='pdf')await until(()=>evaluate("Boolean(document.querySelector('[data-pdf-front] canvas'))"),name+' canvas missing');
    if(expectation==='image')await until(()=>evaluate("document.querySelector('img')?.naturalWidth>0"),name+' image missing');
    if(expectation==='text')await until(()=>evaluate("document.querySelector('pre')?.textContent.includes('中文 UTF-8 text')"),name+' text missing');
    if(expectation==='boundary'){
      await until(()=>evaluate("document.querySelector('pre')?.textContent.length===262143"),'UTF-8 window split incorrectly');
      await evaluate("[...document.querySelectorAll('button')].find(button=>button.textContent==='Next').click()");
      await until(()=>evaluate("document.querySelector('pre')?.textContent==='中文 END'"),'UTF-8 continuation corrupted');
    }
    if(expectation==='markdown'){
      await until(()=>evaluate("document.querySelector('h1')?.textContent==='SYNTHETIC MARKDOWN'"),'markdown missing');
      assert.equal(await evaluate("Boolean(window.compromised||document.querySelector('article script,article a[href],article img'))"),false);
    }
    if(name==='missing-font.docx.pdf'){
      await until(()=>evaluate("Boolean(document.querySelector('.previewWarnings'))"),'missing-font warning was dropped');
      assert.match(await evaluate("document.querySelector('.previewWarnings').textContent"),/SparkX Missing Font QA 20261029/);
      if(process.env.SPARKCLAW_FORMAT_SCREENSHOTS){await fs.mkdir(process.env.SPARKCLAW_FORMAT_SCREENSHOTS,{recursive:true});await fs.writeFile(path.join(process.env.SPARKCLAW_FORMAT_SCREENSHOTS,'missing-font-notice.png'),(await window.capturePage()).toPNG());}
      await evaluate("document.querySelector('.previewWarnings button').click()");
      await until(()=>evaluate("!document.querySelector('.previewWarnings')"),'font warning could not be dismissed');
      assert.ok(await evaluate("Boolean(document.querySelector('[data-pdf-front] canvas'))"));
    }
    assert.equal(await evaluate("Boolean(window.compromised||document.querySelector('[role=alert]'))"),false,name+' failed unexpectedly');
    if(process.env.SPARKCLAW_FORMAT_SCREENSHOTS){await fs.mkdir(process.env.SPARKCLAW_FORMAT_SCREENSHOTS,{recursive:true});await fs.writeFile(path.join(process.env.SPARKCLAW_FORMAT_SCREENSHOTS,name+'.png'),(await window.capturePage()).toPNG());}
    results.push({name,outcome:'rendered',elapsedMs:Date.now()-started});
  }
  const model=JSON.parse(await fs.readFile(path.join(converted,'complex.xlsx.json'),'utf8'));
  await load('complex.xlsx','spreadsheet',converted,undefined,model);
  await until(()=>evaluate("document.querySelector('.literalFormula output')?.textContent==='=1-1'"),'spreadsheet formula missing');
  await evaluate("document.querySelectorAll('[role=tab]')[1].click()");
  await until(()=>evaluate("document.querySelector('.literalFormula output')?.textContent==='SECOND SHEET'"),'second worksheet missing');
  results.push({name:'complex.xlsx',outcome:'rendered',cachedFormulaAndSheetSwitch:true});
  console.log(JSON.stringify({passed:true,platform:process.platform,syntheticOnly:true,nativeViewer:true,results}));window.destroy();app.exit(0);
 }catch(error){console.error(JSON.stringify({passed:false,results,error:error.message}));window?.destroy();app.exit(1);}
});
