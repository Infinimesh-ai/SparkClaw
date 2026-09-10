import fs from 'node:fs/promises';
import path from 'node:path';
import crypto from 'node:crypto';
import { fileURLToPath } from 'node:url';

const directory=path.dirname(fileURLToPath(import.meta.url));
const receipts=[];
for(const provider of ['qq_mail','gmail','outlook']) {
  const receipt=JSON.parse(await fs.readFile(path.join(directory,provider+'-receipt.json'),'utf8'));
  const bytes=await fs.readFile(path.join(directory,provider+'.eml'));
  if(bytes.length!==receipt.bytes || crypto.createHash('sha256').update(bytes).digest('hex')!==receipt.sha256 || receipt.reference_exact!==true) throw new Error('receipt_changed');
  receipts.push(receipt);
}
const attachment=JSON.parse(await fs.readFile(path.join(directory,'fixture-mime_attachment-receipt.json'),'utf8'));
const popup=JSON.parse(await fs.readFile(path.join(directory,'fixture-popup-receipt.json'),'utf8'));
const report={date:'2026-09-08',transport:'native_playwright_download_saveAs',bridge_version:'1.0.21',
  originals:receipts,fixtures:[popup,attachment],alternative:'withdrawn',
  verification:{controller_tests:149,bridge_tests:39,artifact_tests:9,mirrored_documents:63},
  scope:{pinned_read_samples:true,new_unread_selection:false,explicit_mark_read:false,real_provider_attachment_inventory:false,gateway_deployment:false},
  limitations:['One export per task; bulk automatic downloads remain subject to Chromium restrictions.','Identical concurrent final URLs are ambiguous and their files are left untouched.']};
await fs.writeFile(path.join(directory,'report.json'),JSON.stringify(report,null,2)+'\n',{mode:0o600});
console.log(JSON.stringify({originals:receipts.map(value=>({provider:value.provider,bytes:value.bytes,reference_exact:value.reference_exact})),attachment_exact:attachment.results[0].attachment_exact,alternative:report.alternative}));
