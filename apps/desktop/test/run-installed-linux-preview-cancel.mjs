// Run only against the owned Debian test installation and synthetic capacity fixtures.
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import {execFile} from 'node:child_process';
import {promisify} from 'node:util';
import {LocalPreviewBroker} from '/opt/SparkX/resources/app.asar.unpacked/src/main/local-preview-broker.mjs';
const root=await fs.mkdtemp('/tmp/sparkx-installed-cancel-');
const input=await fs.readFile('/tmp/large-fixtures/capacity-40-pages.docx');
const observations=[];
const broker=new LocalPreviewBroker({directory:root,workerRuntime:'/opt/SparkX/sparkx',getScope:()=> 'synthetic-cancel',stat:async()=>({ref:'doc',version:'v1',locator:'synthetic',name:'capacity.docx',kind:'file',size:input.length}),snapshot:async()=>({bytes:input}),onObservation:v=>observations.push(v)});
const pending=broker.prepare({ref:'doc',version:'v1',kind:'office',readerId:'r'}).then(()=>({unexpectedCompletion:true}),error=>({code:error.code}));
let children=[], peakBeforeCancel=0;
try {
 const deadline=Date.now()+20000;
 while(Date.now()<deadline){
  const output=(await promisify(execFile)('/bin/ps',['-axo','pid=,ppid=,rss=,args='])).stdout;
  const rows=output.trim().split('\n').map(line=>{const m=line.trim().match(/^(\d+)\s+(\d+)\s+(\d+)\s+(.*)$/);return m?{pid:+m[1],ppid:+m[2],rss:+m[3]*1024,args:m[4]}:null}).filter(Boolean);
  const ids=new Set(rows.filter(r=>r.args.includes(root)&&r.args.includes('bwrap')).map(r=>r.pid));
  for(let old=-1;old!==ids.size;){old=ids.size;for(const r of rows)if(ids.has(r.ppid))ids.add(r.pid);}
  children=rows.filter(r=>ids.has(r.pid));peakBeforeCancel=Math.max(peakBeforeCancel,children.reduce((n,r)=>n+r.rss,0));
  if(peakBeforeCancel>=800*1024*1024)break;
  await new Promise(r=>setTimeout(r,25));
 }
 assert.ok(peakBeforeCancel>=800*1024*1024,'Did not observe real WASM resident allocation before cancellation');
 const started=Date.now();await broker.close();const closeMs=Date.now()-started;
 const result=await pending;assert.ok(!result.unexpectedCompletion);
 for(const child of children)assert.throws(()=>process.kill(child.pid,0),{code:'ESRCH'});
 assert.equal(broker.active,null);assert.equal(broker.reservedInput,0);assert.equal(broker.cacheBytes,0);assert.equal((await fs.readdir(root)).length,0);
 console.log(JSON.stringify({platform:process.platform,runtime:'/opt/SparkX/sparkx',realWasmObserved:true,peakBeforeCancel,observedProcesses:children.length,allObservedProcessesGone:true,cacheAndReservationsReleased:true,closeMs,result,observations}));
} finally {await broker.close();await fs.rm(root,{recursive:true,force:true});}
