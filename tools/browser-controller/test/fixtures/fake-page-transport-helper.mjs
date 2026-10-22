// Protocol-only stand-in for cli-page-transport.mjs (no daemon, any platform).
// FAKE_TRANSPORT_STARTUP selects startup behavior; request args select replies.
import fs from 'node:fs';
const startup=process.env.FAKE_TRANSPORT_STARTUP??'ok',log=process.env.FAKE_TRANSPORT_LOG;
if(startup==='crash')process.exit(3);
if(startup==='fail'){process.stdout.write('{"error":{"code":"browser_extension_unavailable","reason":"fixture_refused"}}\n');process.exit(1);}
if(startup!=='silent')process.stdout.write('{"ready":true}\n');
let buffer='';
process.stdin.on('data',chunk=>{
 buffer+=chunk;
 for(let at;(at=buffer.indexOf('\n'))>=0;){
  const request=JSON.parse(buffer.slice(0,at));buffer=buffer.slice(at+1);
  const [command,value]=request.args??['batch'];
  if(log)fs.appendFileSync(log,`${process.pid} ${command}\n`);
  if(command==='hang')continue;
  if(command==='exit')process.exit(0);
  if(command==='garbage'){process.stdout.write('not json\n');continue;}
  if(command==='stale-id'){process.stdout.write(`${JSON.stringify({id:request.id+100,code:0,stdout:'',stderr:''})}\n`);continue;}
  if(command==='big'){process.stdout.write('x'.repeat(40<<20));continue;}
  if(command==='batch'){process.stdout.write(`${JSON.stringify({id:request.id,envelope:{output:'{}'}})}\n`);continue;}
  process.stdout.write(`${JSON.stringify({id:request.id,code:0,stdout:`${command}:${value??''}\n`,stderr:''})}\n`);
 }
});
process.stdin.on('end',()=>process.exit(0));
