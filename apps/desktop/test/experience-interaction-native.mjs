import { app,BrowserWindow,session } from "electron";
import fs from "node:fs/promises";
import path from "node:path";
import assert from "node:assert/strict";
const directory=process.env.SPARKCLAW_INTERACTION_ROOT;
app.setName("SparkX isolated interaction regression");app.setPath("userData",path.join(directory,"profile"));
const pause=ms=>new Promise(resolve=>setTimeout(resolve,ms));
app.whenReady().then(async () => {
app.dock?.hide();let window;
try {
 const [js,css]=await Promise.all(["fixture.js","fixture.css"].map(file=>fs.readFile(path.join(directory,"ui",file),"utf8")));
 session.defaultSession.webRequest.onBeforeRequest(({url},callback)=>callback({cancel:!url.startsWith("data:")}));
 window=new BrowserWindow({show:false,width:1440,height:900,webPreferences:{sandbox:true,nodeIntegration:false,contextIsolation:true,backgroundThrottling:false}});
 window.webContents.on("console-message",details=>console.error(details.message));
 window.setContentSize(1440,900);await window.loadURL(`data:text/html;charset=utf-8,${encodeURIComponent(`<html><head><style>${css}</style></head><body><div id="root"></div><script>${js}</script></body></html>`)}`);
 const run=s=>window.webContents.executeJavaScript(s),wait=()=>pause(65);
 await wait();
 const position=()=>run("(() => {const e=document.querySelector('div.messageList');return {top:e.scrollTop,bottom:e.scrollHeight-e.clientHeight-e.scrollTop,selection:getSelection().toString()}})()");
 assert.ok((await position()).bottom<=1,"initial bottom follow");
 await run("document.querySelector('div.messageList').scrollTop=600");await wait();
 const held=(await position()).top;
 await run("window.experienceInteraction.setRevision(1)");await wait();assert.equal((await position()).top,held,"increment preserves user reading");
 await run("window.experienceInteraction.setSession('b')");await wait();assert.ok((await position()).bottom<=1);
 await run("window.experienceInteraction.setSession('a');window.experienceInteraction.setEmpty(true)");await wait();
 await run("window.experienceInteraction.setEmpty(false)");await wait();assert.equal((await position()).top,held,"async conversation restoration");
 await run("(() => {const r=document.createRange();r.selectNodeContents(document.querySelector('.messageContent p'));getSelection().removeAllRanges();getSelection().addRange(r)})()");await wait();
 const selection=(await position()).selection;
 await run("window.experienceInteraction.setRevision(2)");await wait();assert.equal((await position()).top,held);assert.equal((await position()).selection,selection);
 await run("document.querySelector('#latest').click()");await wait();assert.ok((await position()).bottom<=1);
 await run("window.experienceInteraction.setRevision(3)");await wait();assert.ok((await position()).bottom<=1,"explicit latest resumes following");
 const timing=await run(`new Promise(resolve=>{const start=performance.now();const input=document.querySelector('textarea');Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype,'value').set.call(input,'长输入验证\\n'.repeat(30));input.dispatchEvent(new Event('input',{bubbles:true}));requestAnimationFrame(()=>requestAnimationFrame(()=>{const s=getComputedStyle(input);resolve({milliseconds:performance.now()-start,height:input.clientHeight,line:parseFloat(s.lineHeight),overflow:s.overflowY,lines:input.value.split('\\n').length})}))})`);
 assert.ok(timing.height<=timing.line*6+1 && timing.overflow==="auto" && timing.lines===31,"bounded textarea retains thirty lines");
 assert.ok(timing.milliseconds<100,`input feedback target: ${JSON.stringify(timing)}`);
 console.log(JSON.stringify({event:"experience_interaction",passed:true,real_electron_layout:true,scroll_selection:true,async_conversation_restore:true,input:timing,network:false,production_data:false}));
} catch(error){console.error(error);process.exitCode=1;} finally{window?.destroy();app.exit(process.exitCode??0);}

});
