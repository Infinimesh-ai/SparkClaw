import fs from 'node:fs/promises';
import crypto from 'node:crypto';
import assert from 'node:assert/strict';
import {performance} from 'node:perf_hooks';
import {ISCPTransport} from '../apps/desktop/src/main/iscp-transport.mjs';
import {ISCPObjectClient} from '../apps/desktop/src/main/iscp-object-client.mjs';
import {assertPrivateDirectory,readPrivateJSON,writePrivateJSON} from './lib/private-workbench.mjs';
import {containerState} from './lib/iscp-docker-lab.mjs';
const dir=process.argv[2];
if (!dir?.startsWith('/') || process.argv.length !== 3) throw new Error('Usage: node scripts/iscp-expansion-capacity.mjs <absolute private expansion lab>');
await assertPrivateDirectory(dir);
const metadata=await readPrivateJSON(dir+'/run.json');
if (metadata.schema_version !== 1 || !metadata.source?.capacity_patch || !Array.isArray(metadata.qualified_operations)) throw new Error('An explicitly prepared isolated expansion capacity lab is required');
const installationFile=dir+'/evidence/capacity-installation.json';
let installationID;
try { installationID=(await readPrivateJSON(installationFile)).installation_id; }
catch(error) { if(error.code!=='ENOENT') throw error; installationID=crypto.randomUUID(); await writePrivateJSON(installationFile,{installation_id:installationID},{replace:false}); }
assert.match(installationID,/^[0-9a-f-]{36}$/);
const transport=new ISCPTransport({configPath:dir+'/desktop-helper.json',installationID,origin:'https://iscp.invalid',timeoutMS:30000,onState:state=>{if(['authorization_revoked','disconnected'].includes(state))console.log('state',state);}});
const call=(operation,body,options={})=>transport.invoke(operation,body,{...options,installationID});
const thresholds={upload_8m_seconds:90,download_8m_seconds:90,upload_64m_seconds:600,download_64m_seconds:600,control_p95_ms:2000};
const gateway=containerState(metadata.gateway_container,metadata.lab_id);
assert.ok(gateway?.State.Running && Object.keys(gateway.HostConfig.PortBindings||{}).length===0,'Gateway business ports must not be published');
const report={thresholds,source:metadata.source,gateway_binary_sha256:metadata.gateway_binary_sha256,helper_binary_sha256:metadata.helper_binary_sha256,samples:[],controls:[],gateway_business_ports_published:false,network_observation:'Use the native fixture for process-level bypass observations',model:'not exercised in object benchmark'};
await fs.writeFile(dir+'/evidence/expansion-capacity-thresholds.json',JSON.stringify(thresholds,null,2),{mode:0o600});
try {
 await transport.start();
 console.log('negotiated',transport.capabilities?.profile);
 assert.equal(transport.capabilities?.schema_version,2);
 await call('installation.bind',{schema_version:1,installation_id:installationID});
 const owner=await call('settings.owner.get');
 const id=crypto.randomUUID();
 const changed=await call('settings.owner.patch',{display_name:'ISCP qualification'},{operationID:id,expectedRevision:owner.revision});
 assert.equal(changed.value.display_name,'ISCP qualification');
 const receipt=await call('operations.receipt',undefined,{params:{operation_id:id}});
 assert.equal(receipt.state,'completed');
 console.log('settings CAS and receipt passed');
 const objects=new ISCPObjectClient({root:dir+'/benchmark-objects',scope:{installationID,authorization_revision:transport.capabilities.authorization_revision},call});
 let active=true;
 const controls=(async()=>{while(active){const start=performance.now();try{await call('capabilities.get');report.controls.push(performance.now()-start);}catch(error){report.controls.push({error:error.message});}await new Promise(resolve=>setTimeout(resolve,400));}})();
 try {
  for(const mib of [8,64]) {
   const bytes=crypto.randomBytes(mib*1024*1024);
   let start=performance.now();
   const object=await objects.upload(bytes,{purpose:'file',name:`qualification-${mib}.bin`});
   const upload=(performance.now()-start)/1000;
   console.log('upload',mib,upload);
   start=performance.now();
   const result=await objects.download(object);
   const download=(performance.now()-start)/1000;
   assert.deepEqual(result,bytes);
   report.samples.push({mib,upload_seconds:upload,download_seconds:download,sha256:object.sha256});
   console.log('download',mib,download);
   assert.ok(upload<=thresholds[`upload_${mib}m_seconds`]);
   assert.ok(download<=thresholds[`download_${mib}m_seconds`]);
   await call('object.release',{object_id:object.object_id,version:object.version});
  }
 }finally{active=false;await controls;}
 const times=report.controls.filter(value=>typeof value==='number').sort((a,b)=>a-b);
 report.control_p95_ms=times[Math.ceil(times.length*.95)-1];
 report.passed=report.controls.length===times.length&&report.control_p95_ms<=thresholds.control_p95_ms;
 assert.ok(report.passed,'control delay gate failed');
 console.log('PASS',JSON.stringify(report.samples),report.control_p95_ms);
} catch(error){report.error=error.stack;console.error(error);process.exitCode=1;}
finally{transport.close();await fs.writeFile(dir+'/evidence/expansion-capacity.json',JSON.stringify(report,null,2),{mode:0o600});}
