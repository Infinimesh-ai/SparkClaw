// Pinned CLI Session protocol fixture for a whole task page (never launches a
// browser). Answers the commands HostPage issues for a send-shaped page and can
// fail exactly one command, chosen by its 1-based position, to compare where
// per-command processes and the resident transport stop.
import fs from 'node:fs/promises';
import net from 'node:net';
import path from 'node:path';
import {createRequire} from 'node:module';
const require=createRequire(import.meta.url);
const {createClientInfo}=require('../../node_modules/playwright-core/lib/tools/cli-client/registry.js');
const [sessionName,failure,log]=process.argv.slice(2),info=createClientInfo();
const [failAt,kind,...rest]=failure==='none'?[0,'none']:failure.split(':');
const socketPath=path.join(path.dirname(process.cwd()),'fixture.sock');
const url='https://mail.google.test/compose',foreign='https://foreign.test/';
await fs.mkdir(info.daemonProfilesDir,{recursive:true,mode:0o700});
await fs.writeFile(path.join(info.daemonProfilesDir,`${sessionName}.session`),JSON.stringify({name:sessionName,version:info.version,attached:true,socketPath}),{mode:0o600});
let count=0;
const server=net.createServer(socket=>{
 let buffer='';socket.on('data',async bytes=>{
  buffer+=bytes;const at=buffer.indexOf('\n');if(at<0)return;
  const request=JSON.parse(buffer.slice(0,at)),args=request.params.args,name=args._[0];buffer=buffer.slice(at+1);
  const index=++count,fail=index===Number(failAt);
  await fs.appendFile(log,JSON.stringify({command:name,target:name==='eval'||name==='run-code'?undefined:args._[1]})+'\n');
  const reply=(text,isError=false)=>socket.end(JSON.stringify({id:request.id,result:{text,isError}})+'\n');
  if(fail&&kind==='hang')return;
  if(fail&&kind==='close'){socket.destroy();return;}
  if(fail&&kind==='exit'){socket.destroy();server.close();process.exit(0);}
  if(fail&&kind==='error')return reply(rest.join(':'),true);
  if(fail&&kind==='token')return reply(`leak ${process.env.PLAYWRIGHT_MCP_EXTENSION_TOKEN}`);
  const stale=fail&&kind==='stale';
  if(name==='tab-list')return reply(`- 0: (current) [Mail](${url})\n- 1: [${stale?'Changed':'Owner'}](https://owner.test/)`);
  if(name==='eval'){
   const expression=args._[1],location=stale?foreign:url;
   if(expression==='() => location.href')return reply(JSON.stringify(location));
   if(expression.includes('inspectionURL'))return reply(JSON.stringify({initial_url:location,origin:location,result:true}));
   return reply('true');
  }
  if(name==='run-code'){
   const code=args._[1];
   if(code.includes('app-cli:guard')||code.includes('app-cli:secrets'))return reply('true');
   // Read-only code is wrapped by HostPage with its URL guard.
   if(code.includes('initial_url'))return reply(JSON.stringify({initial_url:url,final_url:stale?foreign:url,result:{ok:true}}));
   return reply(JSON.stringify({ok:true,url}));
  }
  reply(`### Ran ${name}`);
 });socket.on('error',()=>{});
});
await new Promise(resolve=>server.listen(socketPath,resolve));
process.stdout.write('ready\n');
