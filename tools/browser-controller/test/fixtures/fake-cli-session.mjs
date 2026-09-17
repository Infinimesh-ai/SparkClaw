// Real pinned CLI Session protocol fixture; never launches a browser.
import fs from 'node:fs/promises';
import net from 'node:net';
import path from 'node:path';
import {createRequire} from 'node:module';
const require=createRequire(import.meta.url);
const {createClientInfo}=require('../../node_modules/playwright-core/lib/tools/cli-client/registry.js');
const [sessionName,mode,log]=process.argv.slice(2),info=createClientInfo();
const socketPath=path.join(path.dirname(process.cwd()),'fixture.sock');
await fs.mkdir(info.daemonProfilesDir,{recursive:true,mode:0o700});
await fs.writeFile(path.join(info.daemonProfilesDir,`${sessionName}.session`),JSON.stringify({name:sessionName,version:info.version,attached:true,socketPath}),{mode:0o600});
let ran=false,selected=false;
const server=net.createServer(socket=>{
 let buffer='';socket.on('data',async bytes=>{
  buffer+=bytes;const at=buffer.indexOf('\n');if(at<0)return;
  const request=JSON.parse(buffer.slice(0,at)),name=request.params.args._[0];buffer=buffer.slice(at+1);
  await fs.appendFile(log,JSON.stringify({command:name})+'\n');
  let text;
  if(name==='tab-list'){
   const bad=mode==='pre-owner'||mode==='post-owner'&&ran;
   const current=mode!=='select'||selected;
   text=`- 0: ${current?'(current) ':''}[Mail](${mode==='pre-origin'?'https://foreign.test/':'https://mail.google.test/'})\n- 1: ${current?'':'(current) '}[${bad?'Changed':'Owner'}](https://owner.test/)`;
  }else if(name==='tab-select'){selected=true;text='selected';}
  else if(name==='run-code'){
   ran=true;if(mode.startsWith('hang'))return;
   if(mode==='overflow'){socket.write('x'.repeat(34<<20));return;}
   if(mode==='private-error'){socket.end(JSON.stringify({id:request.id,result:{isError:true,text:process.env.PLAYWRIGHT_MCP_EXTENSION_TOKEN}})+'\n');return;}
   text=JSON.stringify({initial_url:'https://mail.google.test/',final_url:mode==='foreign'?'https://foreign.test/':'https://mail.google.test/',result:mode==='private-output'?process.env.PLAYWRIGHT_MCP_EXTENSION_TOKEN:{ok:true}});
  }
  socket.end(JSON.stringify({id:request.id,result:{text,isError:false}})+'\n');
 });socket.on('error',()=>{});
});
await new Promise(resolve=>server.listen(socketPath,resolve));
process.stdout.write('ready\n');
