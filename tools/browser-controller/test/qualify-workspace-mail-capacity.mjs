// Opt in with SPARKCLAW_MAIL_CAPACITY_TEST=1. Uses actual Controller/HostPage,
// Playwright CLI processes and an isolated Electron adapter. This qualifies
// byte-channel capacity, not a live provider or the production ISCP transport.
import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import fs from 'node:fs/promises';
import http from 'node:http';
import os from 'node:os';
import path from 'node:path';
import {spawn} from 'node:child_process';
import {fileURLToPath, pathToFileURL} from 'node:url';
import {setTimeout as delay} from 'node:timers/promises';

const here = fileURLToPath(import.meta.url);
const repository = path.resolve(path.dirname(here), '../../..');
const hash = bytes => crypto.createHash('sha256').update(bytes).digest('hex');
if (process.versions.electron) void browserHost().catch(error=>{console.error(error);process.exit(1);});
else if (process.env.SPARKCLAW_MAIL_CAPACITY_TEST !== '1') console.log('Skipped: set SPARKCLAW_MAIL_CAPACITY_TEST=1');
else await qualify();

async function browserHost() {
  const {app, BrowserWindow, WebContentsView, session, protocol} = await import('electron');
  const {BrowserPresentation} = await import('../../../apps/desktop/src/main/presentation.mjs');
  const {PageRegistry} = await import('../../../apps/desktop/src/browser/page-registry.mjs');
  const {ElectronAdapterServer} = await import('../../../apps/desktop/src/browser/adapter-server.mjs');
  const directory = process.env.SPARKCLAW_MAIL_CAPACITY_DIRECTORY;
  app.setPath('userData', path.join(directory, 'profile'));
  protocol.registerSchemesAsPrivileged([{scheme:'sparkclaw-internal', privileges:{standard:true, secure:true}}]);
  await app.whenReady();
  const browserSession = session.fromPartition('persist:capacity');
  browserSession.protocol.handle('sparkclaw-internal', () => new Response('<title>Extension connect</title>'));
  browserSession.setPermissionRequestHandler((_contents, _permission, done) => done(false));
  browserSession.webRequest.onBeforeRequest((request, done) => done({cancel:
    !request.url.startsWith(process.env.SPARKCLAW_MAIL_CAPACITY_ORIGIN + '/') &&
    !request.url.startsWith('sparkclaw-internal:') && !request.url.startsWith('about:')}));
  const preferences = {session:browserSession, sandbox:true, contextIsolation:true, nodeIntegration:false};
  const window = new BrowserWindow({width:1500, height:920, show:false, webPreferences:preferences});
  const shieldView = new WebContentsView({webPreferences:preferences});
  const presentation = new BrowserPresentation({window, shieldView});
  const runtimeGeneration = crypto.randomBytes(16).toString('hex');
  const registry = new PageRegistry({runtimeGeneration, browserSession, presentation, qualification:true,
    createView:() => new WebContentsView({webPreferences:preferences})});
  const adapter = await new ElectronAdapterServer({socketPath:path.join(directory,'electron-adapter.sock'),
    secretPath:path.join(directory,'adapter-secret'), downloadRoot:path.join(directory,'downloads'),
    runtimeGeneration, registry, browserSession, qualification:true}).start();
  window.showInactive();
  const stop = async () => {await adapter.close(); window.destroy(); app.exit(0);};
  process.once('SIGTERM', () => {void stop();});
  console.log(JSON.stringify({event:'capacity_browser_ready', electron:process.versions.electron, chromium:process.versions.chrome}));
}

async function qualify() {
  const {ApplicationHostDriver} = await import('../src/host-driver.mjs');
  const {BrowserController} = await import('../src/controller.mjs');
  const runtime = process.env.SPARKCLAW_MAIL_CAPACITY_RUNTIME
    ? pathToFileURL(path.resolve(process.env.SPARKCLAW_MAIL_CAPACITY_RUNTIME) + '/')
    : new URL('../', import.meta.resolve('@infinimesh/app-cli-runtime/release'));
  const source = new URL('applications/mail/lib/workspace-attachments.mjs', runtime);
  const {uploadManagedAttachments, verifyManagedAttachments} = await import(source);
  const binding = JSON.parse(await fs.readFile(new URL('bindings/mail-gmail.json', runtime), 'utf8'));
  // macOS's per-user temporary path can exceed the Unix socket name limit.
  const directory = await fs.realpath(await fs.mkdtemp(path.join(process.platform === 'darwin' ? '/tmp' : os.tmpdir(),'sc-mail-cap-')));
  const evidence = {schema_version:1, passed:false, directory, started_at:new Date().toISOString(), runtime_source_sha256:hash(await fs.readFile(source)),
    actual_controller:true, actual_cli:true, direct_browser_evaluation:false, provider_qualified:false,
    production_iscp_qualified:false, limit_bytes:10<<20, budget_ms:180000, commands:{}, uploaded:[], effects:0};
  const fixture = http.createServer(async (request, response) => {
    try {
      if (request.method === 'POST' && request.url === '/upload') {
        let size = 0; const digest = crypto.createHash('sha256');
        for await (const chunk of request) {size += chunk.length; assert.ok(size <= 10<<20); digest.update(chunk);}
        evidence.uploaded.push({size_bytes:size, sha256:digest.digest('hex')});
        response.setHeader('content-type','application/json'); response.end('{}'); return;
      }
      if (request.method === 'GET' && request.url === '/composer') {
        response.setHeader('content-type','text/html'); response.end(composer()); return;
      }
      response.writeHead(404); response.end();
    } catch {response.writeHead(500); response.end();}
  });
  let electron, driver, controller, handle, timer, heartbeat, pendingHeartbeat=Promise.resolve();
  try {
    await new Promise(resolve => fixture.listen(0,'127.0.0.1',resolve));
    const origin = `http://127.0.0.1:${fixture.address().port}`;
    const executable = process.env.SPARKCLAW_MAIL_CAPACITY_ELECTRON || path.join(repository,'node_modules/electron/dist',
      process.platform === 'darwin' ? 'Electron.app/Contents/MacOS/Electron' : 'electron');
    electron = spawn(executable, [here], {env:{...process.env, SPARKCLAW_MAIL_CAPACITY_DIRECTORY:directory,
      SPARKCLAW_MAIL_CAPACITY_ORIGIN:origin}, stdio:['ignore','pipe','pipe']});
    let browserLog = ''; electron.stderr.on('data', chunk => {browserLog += chunk.toString();});
    evidence.browser = await new Promise((resolve,reject) => {
      const timeout = setTimeout(() => reject(new Error('Electron capacity host did not start: '+browserLog.slice(-2000))),15000);
      let output=''; electron.stdout.on('data', chunk => {
        output += chunk; for (const line of output.split('\n').slice(0,-1)) {
          try {const value=JSON.parse(line); if(value.event==='capacity_browser_ready'){clearTimeout(timeout);resolve(value);}} catch {}
        }
      });
      electron.once('exit', code => {clearTimeout(timeout);reject(new Error(`Electron exited ${code}: ${browserLog.slice(-1000)}`));});
    });
    controller = new BrowserController({clientFactory:{open:async()=>{throw new Error('No alternate browser path');}}});
    driver = new ApplicationHostDriver({controller, runtimeRoot:path.join(directory,'cli-runtime'), releaseRoot:fileURLToPath(runtime),
      entryPoint:path.join(repository,'tools/browser-controller/node_modules/@playwright/cli/playwright-cli.js'),
      executablePath:path.join(repository,'apps/desktop/bin/sparkclaw-electron-browser.mjs'),
      userDataDir:path.join(directory,'profile'), browserChannel:'chromium',
      extraEnv:{TMPDIR:directory},
      electronAdapter:{socketPath:path.join(directory,'electron-adapter.sock'),secretPath:path.join(directory,'adapter-secret')},
      connectTimeoutMS:15000, actionTimeoutMS:10000, navigationTimeoutMS:15000,
      diagnostic:value=>console.error(JSON.stringify(value)),
      spawn:(executable,args,options)=>{
        const command=args.slice(1).find(value=>!value.startsWith('-'));
        evidence.commands[command]=(evidence.commands[command]||0)+1;
        const child=spawn(executable,args,options);
        if(command==='attach'){
          let diagnostic='';for(const stream of [child.stdout,child.stderr])stream.on('data',value=>{diagnostic+=value;});
          child.once('exit',code=>{if(code)console.error(diagnostic.replaceAll(options.env.PLAYWRIGHT_MCP_EXTENSION_TOKEN,'[redacted]').slice(-3000));});
        }
        return child;
      }});
    await driver.prepare();
    const spec=structuredClone(binding.commands.send);
    assert.equal(spec.timeout_ms,180000);
    assert.equal(spec.host.page.timeoutMS,180000);
    assert.equal(spec.host.page.readOnlyCode,false);
    assert.equal(spec.host.page.awaitedRead,true);
    spec.host.page={...spec.host.page, origins:[origin], loginURL:origin+'/composer', deniedURLs:[], beforeNavigationAssets:[]};
    const resource={token:crypto.randomBytes(32).toString('base64url'),workspace_root:directory};
    const started=Date.now();
    const grant={execution_expires_ms:started+180000};
    handle=await driver.create({task:'capacity',epoch:1,generation:1,spec,resource,grant});
    evidence.cli_daemon_pid=JSON.parse(await fs.readFile(path.join(handle.state.directory,'metadata.json'),'utf8')).pid;
    const signal=new AbortController();
    await driver.beginActivity(handle,{id:'upload',kind:'exclusive',spec,resource,grant,signal:signal.signal,task:'capacity'});
    const renew=()=>driver.updateLease(handle,{epoch:1,generation:1,activities:[{id:'upload',kind:'exclusive',
      expires_ms:Math.min(Date.now()+30000,grant.execution_expires_ms)}]});
    await renew(); evidence.heartbeats=0;
    heartbeat=setInterval(()=>{
      pendingHeartbeat=pendingHeartbeat.then(renew).then(()=>{evidence.heartbeats++;}).catch(error=>signal.abort(error));
    },10000);
    const call=(method,...args)=>driver.call(handle,method,args,{signal:signal.signal,activity:'upload',spec});
    await call('navigate',origin+'/composer');
    const bytes=crypto.randomBytes(10<<20), digest=hash(bytes);
    const relative=`.sparkclaw-mail-send-${crypto.randomBytes(16).toString('hex')}/00/capacity.bin`;
    await fs.mkdir(path.dirname(path.join(directory,relative)),{recursive:true,mode:0o700});
    await fs.writeFile(path.join(directory,relative),bytes,{mode:0o600}); bytes.fill(0);
    const attachments=[{path:relative,name:'capacity.bin',size_bytes:10<<20,sha256:'sha256:'+digest}];
    let chunks=0, secrets=[], sample='';
    const tab={attachmentSecretSlots:100,
      inspect:expression=>call('inspect',expression),
      runReadCode:async code=>{
        assert.ok(Buffer.byteLength(code)<=64<<10);
        const value=code.match(/"data":"([A-Za-z0-9+/=]+)"/u)?.[1];
        if(value){assert.ok(secrets.includes(value));chunks++;sample ||= value.slice(0,256);}
        assert.equal(code.includes(relative),false);
        const result=await call('runReadCode',code);
        if(value&&chunks%64===0)console.log(JSON.stringify({event:'capacity_progress',chunks,elapsed_ms:Date.now()-started}));
        return result;
      },
      setAttachmentSecrets:async values=>{
        secrets=values;
        await call('setSecrets',Object.fromEntries([['SC_BASE_BODY','fixture private body'],
          ...values.map((value,index)=>[`SC_ATTACHMENT_${index}`,value])]));
      }};
    console.log(JSON.stringify({event:'capacity_upload_started',directory,bytes:10<<20}));
    timer=setTimeout(()=>signal.abort(new Error('capacity budget exceeded')),Math.max(1,180000-(Date.now()-started)));
    timer.unref();
    try {
      await uploadManagedAttachments(tab,'gmail',directory,attachments,()=>{evidence.effects++;});
      await verifyManagedAttachments(tab,'gmail',attachments);
    } finally {evidence.elapsed_ms=Date.now()-started;evidence.chunks=chunks;clearTimeout(timer);}
    assert.ok(evidence.elapsed_ms<180000);
    assert.equal(evidence.effects,1);
    assert.deepEqual(evidence.uploaded,[{size_bytes:10<<20,sha256:digest}]);
    assert.deepEqual(secrets,[]);
    assert.deepEqual(handle.state.secretValues,['fixture private body']);
    await assertAbsentFromArtifacts(handle.state.directory,sample);
    const privateState=await call('inspect',`()=>({transfer:!!globalThis.__sparkclawManagedMail.attachmentTransfer,sendClicks:globalThis.capacitySendClicks})`);
    assert.deepEqual(privateState.result,{transfer:false,sendClicks:0});
    evidence.input_sha256=digest; evidence.no_email_sent=true; evidence.chunks_redacted=true;
    evidence.buffers_cleared=true; evidence.base_secret_preserved=true; evidence.capacity_passed=true;
    evidence.remaining_ms=180000-evidence.elapsed_ms;
  } catch (error) {
    evidence.error={message:error.message,code:error.code,reason:error.diagnosticReason,command:error.diagnosticCommand};
    process.exitCode=1;
  } finally {
    clearTimeout(timer);
    clearInterval(heartbeat);await pendingHeartbeat;
    try {await driver?.shutdown();await controller?.shutdown();evidence.cleanup=true;}
    catch(error){
      evidence.cleanup=false;evidence.cleanup_error=error.message;
      for(const socket of driver?.connections??[])socket.destroy();
      if(driver?.events?.listening)await new Promise(resolve=>driver.events.close(resolve));
      // Production metadata reaping deliberately requires Linux /proc. This
      // macOS capacity fixture never weakens that fence or kills an unchecked
      // PID: require that the actual CLI close already terminated its daemon.
      if(process.platform!=='linux'&&evidence.cli_daemon_pid&&await processExited(evidence.cli_daemon_pid)) {
        evidence.production_cleanup_qualified=false;
        evidence.cleanup_platform_fence='linux_proc_required';
        evidence.fixture_cleanup=true;
        if(handle?.bootstrap)controller.finishApplication(handle.bootstrap);
        for(const id of handle?.activities.keys()??[])await driver.revokeActivity(handle,id);
      } else process.exitCode=1;
    }
    if(electron&&electron.exitCode===null){
      const exited=new Promise(resolve=>electron.once('exit',resolve));electron.kill('SIGTERM');
      await Promise.race([exited,delay(5000)]);if(electron.exitCode===null)electron.kill('SIGKILL');await exited;
    }
    await new Promise(resolve=>fixture.close(resolve));
    evidence.passed=evidence.capacity_passed===true&&(evidence.cleanup===true||evidence.fixture_cleanup===true);
    // Remove approved bytes and disposable profile; retain only sanitized evidence.
    for(const entry of await fs.readdir(directory))await fs.rm(path.join(directory,entry),{recursive:true,force:true});
    await fs.writeFile(path.join(directory,'evidence.json'),JSON.stringify(evidence,null,2)+'\n',{mode:0o600});
    console.log(JSON.stringify(evidence));
  }
}

async function processExited(pid) {
  for(let attempt=0;attempt<50;attempt++) {
    try {process.kill(pid,0);} catch(error) {if(error.code==='ESRCH')return true;throw error;}
    await delay(100);
  }
  return false;
}

async function assertAbsentFromArtifacts(directory,sample) {
  for(const entry of await fs.readdir(directory,{withFileTypes:true})) {
    const filename=path.join(directory,entry.name);
    if(entry.isDirectory())await assertAbsentFromArtifacts(filename,sample);
    else if(entry.isFile())assert.equal((await fs.readFile(filename)).includes(Buffer.from(sample)),false,'attachment bytes leaked into CLI artifacts');
  }
}

function composer() {
  return `<!doctype html><title>Capacity composer</title><div id="composer"><input type="file" multiple>
    <div contenteditable="true">fixture private body</div><button id="send">Send</button><div class="aQH"></div></div>
    <script>
    const root=document.getElementById('composer'), input=root.querySelector('input');
    globalThis.capacitySendClicks=0;
    globalThis.__sparkclawManagedMail={provider:'gmail',ownershipChecked:true,root,body:root.querySelector('[contenteditable]'),send:root.querySelector('#send')};
    root.querySelector('#send').onclick=()=>globalThis.capacitySendClicks++;
    input.addEventListener('change',async()=>{
      const progress=document.createElement('div');progress.setAttribute('role','progressbar');progress.textContent='Uploading';root.append(progress);
      try {
        for(const file of input.files){
          const response=await fetch('/upload',{method:'POST',body:file});if(!response.ok)throw new Error('upload failed');
          const row=document.createElement('div');row.className='dL';
          const name=document.createElement('span');name.className='vK';name.textContent=file.name;
          const remove=document.createElement('button');remove.textContent='Remove attachment';row.append(name,remove);root.querySelector('.aQH').append(row);
        }
      }catch(error){const alert=document.createElement('div');alert.setAttribute('role','alert');alert.textContent=error.message;root.append(alert);}
      finally{progress.remove();}
    });
    </script>`;
}
