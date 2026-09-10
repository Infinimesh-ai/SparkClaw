import fs from 'node:fs/promises';
import path from 'node:path';
import {createRequire} from 'node:module';
const root=process.cwd();
const require=createRequire(path.join(root,'tools/browser-controller/package.json'));
const {chromium}=require('playwright-core');
const {simpleParser}=require('mailparser');
const folder=path.join(root,'data/qualifications/email-dom-20260907');
const observed=JSON.parse(await fs.readFile(path.join(folder,'outlook-observations.json'),'utf8'))[0];
const original=await simpleParser(await fs.readFile(path.join(root,'data/workspaces/email/4c1029697ee358715d3a14a2add817c4b01651440de808371f78165ac90dc581/mb_e557ca3ab561584ea387c535c29b7228/mail_e997f37b2cf500a1bcd749584add5fbe/source/cap_716b49226a5fbea3c95be1784df35d5e/message.eml')));
const normalize=value=>value.normalize('NFC').replace(/\s+/gu,' ').trim();
const browser=await chromium.launch({headless:true,timeout:20000,executablePath:await fs.realpath('/usr/local/bin/chromium')});
try {
 const context=await browser.newContext({javaScriptEnabled:false,offline:true});
 await context.route('**/*',route=>route.abort());
 const page=await context.newPage();
 await page.setContent(original.html,{waitUntil:'domcontentloaded',timeout:10000});
 const reference=await page.evaluate(()=>({text:document.body.innerText,links:Array.from(document.querySelectorAll('a[href]')).map(node=>node.getAttribute('href'))}));
 await page.setContent(observed.body_html,{waitUntil:'domcontentloaded',timeout:10000});
 const saved=await page.evaluate(()=>({text:document.body.innerText,links:Array.from(document.querySelectorAll('a[href]')).map(node=>node.getAttribute('href'))}));
 const cleaned=await page.evaluate(()=>{
  const icons=Array.from(document.querySelectorAll('i.fui-Icon-font[aria-hidden="true"]'));
  for(const icon of icons)icon.remove();
  return {text:document.body.innerText,icons:icons.length};
 });
 const originalLinks=new Set(reference.links);
 const capturedLinks=new Set(saved.links);
 const before=normalize(reference.text),after=normalize(observed.body_text);
 let prefix=0;while(before[prefix]===after[prefix]&&prefix<Math.min(before.length,after.length))prefix++;
 let suffix=0;while(suffix<Math.min(before.length,after.length)-prefix&&before.at(-1-suffix)===after.at(-1-suffix))suffix++;
 const removed=before.slice(prefix,before.length-suffix),added=after.slice(prefix,after.length-suffix);
 const result={offline_render:true,scripts_disabled:true,reference_visible_characters:before.length,web_visible_characters:after.length,captured_html_visible_characters:normalize(saved.text).length,reference_visible_matches_web:before===after,captured_html_visible_matches_reference:normalize(saved.text)===before,reference_links:originalLinks.size,captured_links:capturedLinks.size,all_reference_links_preserved:[...originalLinks].every(url=>capturedLinks.has(url)),removed_characters:removed.length,added_code_points:added.length<=4?Array.from(added).map(c=>c.codePointAt(0)):null,provider_icons_removed_in_comparison:cleaned.icons,visible_text_matches_after_icon_filter:normalize(cleaned.text)===before};
 await fs.writeFile(path.join(folder,'outlook-render-comparison.json'),JSON.stringify(result,null,2),{mode:0o600});
 console.log(JSON.stringify(result));
}finally{await browser.close()}
