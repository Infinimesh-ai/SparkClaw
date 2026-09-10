import fs from 'node:fs/promises';
import path from 'node:path';
import crypto from 'node:crypto';
import assert from 'node:assert/strict';
import {parseGmailList} from '../../../scripts/email/lib/gmail-list.mjs';

const directory=path.dirname(new URL(import.meta.url).pathname),workspace=path.join(directory,'workspace');
const digest=bytes=>'sha256:'+crypto.createHash('sha256').update(bytes).digest('hex');
const results={};
for(const name of ['gmail','outlook','outlook-known','qq_mail']) {
  const receipt=JSON.parse(await fs.readFile(path.join(directory,name+'-receipt.json'),'utf8'));
  if(receipt.status==='empty'){results[name]={status:'empty'};continue;}
  const manifestPath=path.join(workspace,receipt.capture.manifest_path);
  const bytes=await fs.readFile(manifestPath);assert.equal(digest(bytes),receipt.capture.manifest_sha256);
  const manifest=JSON.parse(bytes);
  for(const file of manifest.files){const actual=await fs.readFile(path.join(workspace,file.path));assert.equal(actual.length,file.bytes);assert.equal(digest(actual),file.sha256);}
  const original=manifest.files.find(file=>file.path.endsWith('/message.eml'));
  const state=JSON.parse(await fs.readFile(path.join(path.dirname(manifestPath),'read-state.json'),'utf8'));
  assert.equal(state.state,'confirmed');assert.equal(state.observed,'read');
  results[name]={status:receipt.status,read_state:receipt.capture.read_state,attachments:receipt.capture.attachments_count,files_verified:manifest.files.length,original_bytes:original.bytes,original_sha256:original.sha256};
  if(name==='gmail'){
    const before=JSON.parse(await fs.readFile(path.join(directory,'gmail-network.json'),'utf8')).captured.flatMap(value=>parseGmailList(JSON.parse(value)));
    assert.equal(before.filter(row=>row.provider_message_id===manifest.provider_message_id&&row.unread&&row.inbox).length,1);
    results[name].initial_single_message_unread_evidence=true;
  }
  if(name==='outlook-known'){
    const old=await fs.readFile(path.join(directory,'../native-download-20260908/outlook.eml'));
    assert.equal(digest(old),original.sha256);results[name].independent_original_matches=true;
    assert.notEqual(manifest.provider_message_id,JSON.parse(await fs.readFile(path.join(directory,'outlook-capture-trace.json'),'utf8')).find(row=>row?.provider_selection_id)?.provider_selection_id);
    results[name].sample='existing_read_singleton_actual_item_id';
  }
}
assert.deepEqual(await fs.readdir(path.join(directory,'cli-runtime')),[]);
const report={date:'2026-09-08',results,scope:'source_script_only',store_ingestion:false,model_analysis:false,
  limits:['Gmail and Outlook accept proven singleton conversations only; ambiguous lists fail before opening.',
    'QQ and Outlook had no unread messages during qualification; fresh unread transitions are not live-qualified for them.',
    'Real samples have no attachments. The previous native synthetic MIME attachment qualification remains applicable.',
    'Opening a provider detail may automatically mark it read before persistence; explicit marking waits for verified complete capture.'],
  cleanup:{runtime_empty:true,temporary_credential_bridge_removed:await fs.stat(path.join(directory,'../../../services/gateway/internal/emailautomation/read_loop_qualification_test.go')).then(()=>false,()=>true)}};
await fs.writeFile(path.join(directory,'report.json'),JSON.stringify(report,null,2)+'\n',{mode:0o600});
console.log(JSON.stringify(report));
