import fs from 'node:fs/promises';
import path from 'node:path';
import crypto from 'node:crypto';
import {createRequire} from 'node:module';
const root=process.cwd(),folder=path.join(root,'data/qualifications/email-dom-20260907');
const read=async file=>JSON.parse(await fs.readFile(file,'utf8'));
const owner=crypto.createHash('sha256').update('owner').digest('hex');
const journalName=crypto.createHash('sha256').update('outlook\0email_live_read_source_outlook_20260907').digest('hex')+'.json';
const journal=await read(path.join(root,'data/workspaces/email',owner,'invocations',journalName));
const manifestPath=path.join(root,'data/workspaces',journal.receipt.capture.manifest_path);
const manifestBytes=await fs.readFile(manifestPath),manifest=JSON.parse(manifestBytes);
const originalRef=manifest.files.find(file=>file.path.endsWith('/message.eml'));
const originalBytes=await fs.readFile(path.join(root,'data/workspaces',originalRef.path));
const digest=bytes=>'sha256:'+crypto.createHash('sha256').update(bytes).digest('hex');
const referenceIntegrity=digest(manifestBytes)===journal.receipt.capture.manifest_sha256&&originalBytes.length===originalRef.bytes&&digest(originalBytes)===originalRef.sha256;
if(!referenceIntegrity)throw new Error('reference_integrity_failed');
const {simpleParser}=createRequire(path.join(root,'tools/browser-controller/package.json'))('mailparser');
const reference=await simpleParser(originalBytes);
const outlook=await read(path.join(folder,'outlook-result.json'));
const observed=(await read(path.join(folder,'outlook-observations.json')))[0];
const rendering=await read(path.join(folder,'outlook-render-comparison.json'));
const gmail=await read(path.join(folder,'gmail-result.json'));
const gmailObserved=(await read(path.join(folder,'gmail-observations.json')))[0];
const fields=Object.fromEntries(gmailObserved.detail_rows.filter(row=>row.length>=2).map(row=>[row[0].trim().toLowerCase().replace(/:$/,''),row.slice(1).join(' ').trim()]));
const qq=await read(path.join(folder,'qq_mail-result.json'));
const report={date:'2026-09-07',decision:'candidate_not_adopted',scope:'information-only trial on three existing pinned read messages',production_path_changed:false,
 outlook:{reference_integrity_verified:true,independent_reference:'existing_original_eml',account_matches:outlook.account_matches,identity_matches:outlook.identity_matches,subject_matches:outlook.subject_match,from_address_matches:outlook.from_header_present,to_address_available_and_matches:outlook.to_header_present,to_cc_grouping_coverage:'one_recipient_no_cc_only',displayed_date_matches_to_minute:observed.browser_timezone===Intl.DateTimeFormat().resolvedOptions().timeZone&&observed.date_candidates.some(value=>Math.floor(Date.parse(value.text)/60000)===Math.floor(reference.date.getTime()/60000)),body_stable:outlook.body_stable,...rendering,reference_attachments:reference.attachments.length,script_browser_calls:outlook.operations,elapsed_ms:outlook.elapsed_ms},
 gmail:{independent_reference:null,account_matches:gmail.account_matches,identity_matches:gmail.identity_matches,body_stable:gmail.body_stable,body_characters:gmail.body_characters,detail_fields:gmail.detail_fields,subject_detail_matches_header:fields.subject===gmailObserved.subject,details_stable:gmail.details_stable,detail_expansions:1,script_browser_calls:gmail.operations,elapsed_ms:gmail.elapsed_ms},
 qq_mail:{independent_reference:null,account_matches:qq.account_matches,identity_matches:qq.identity_matches,subject_present:qq.subject_present,body_characters:qq.body_characters,body_html_characters:qq.body_html_characters,read_state:qq.read_state,attachment_inventory_complete:qq.attachment_inventory_complete,original_export_invoked:false,script_browser_calls:qq.operations,elapsed_ms:qq.elapsed_ms},
 limitations:['No live attachment download or complete inventory qualification','No new unread selection or read-state mutation trial','Only Outlook has an independent EML reference','No complex quoted thread or recipient grouping coverage','Elapsed times include setup/navigation and capture but exclude cleanup; flows differ from prior EML collection and do not establish speedup']};
await fs.writeFile(path.join(folder,'report.json'),JSON.stringify(report,null,2),{mode:0o600});
console.log(JSON.stringify(report));
