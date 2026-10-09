import { useEffect, useRef, useState } from "react";
import type { Language } from "../i18n";
import { ISCPMailDraftPanel } from "./ISCPMailDraftPanel";

type Mailbox = { id: string; address: string; provider: string; intake_enabled?: boolean };
type CachedMail = { id: string; subject: string; from: string; summary: string; body_text: string; body_truncated: boolean; attachments: Array<{id:string;name:string;size:number;available:boolean}> };
type Cache = { mailbox_id:string; sequence:number; synced_at:string; messages:CachedMail[] };
type MailCapability = { catalog():Promise<Mailbox[]>; refreshCatalog():Promise<Mailbox[]>; read(mailbox:string):Promise<Cache>; sync(mailbox:string):Promise<Cache>; saveAttachment?(mailbox:string,mail:string,part:string,conversation:string):Promise<unknown> };
declare global {interface Window {sparkclawMailSync?:MailCapability;}}

export function MailCachePanel({language,conversationID="",onFileSaved,attachmentsEnabled=true,sendEnabled=false,sendAttachmentsEnabled=false}: {language:Language;conversationID?:string;onFileSaved?:()=>void|Promise<void>;attachmentsEnabled?:boolean;sendEnabled?:boolean;sendAttachmentsEnabled?:boolean}) {
  const capability=window.sparkclawMailSync;
  const zh=language==="zh";
  const [boxes,setBoxes]=useState<Mailbox[]>([]);
  const [selected,setSelected]=useState("");
  const [cache,setCache]=useState<Cache|null>(null);
  const [busy,setBusy]=useState(false);
  const [error,setError]=useState("");
  const [notice,setNotice]=useState("");
  const [synced,setSynced]=useState(false);
  const generation=useRef(0);
  const actionLock=useRef(false);
  useEffect(()=>{let active=true;if(capability)void capability.catalog().then((rows)=>{if(active){setBoxes(rows);setSelected(rows[0]?.id||"");}}).catch((err:unknown)=>{if(active)setError(err instanceof Error?err.message:"Mail cache unavailable");});return()=>{active=false;};},[capability]);
  useEffect(()=>{const current=++generation.current;setCache(null);setSynced(false);if(capability&&selected)void capability.read(selected).then((result)=>{if(current===generation.current)setCache(result);}).catch((err:unknown)=>{if(current===generation.current)setError(err instanceof Error?err.message:"Mail cache unavailable");});return()=>{++generation.current;};},[capability,selected]);
  if(!capability)return null;
  async function savePart(mailID:string,partID:string){
    if(actionLock.current||!conversationID||!capability?.saveAttachment)return;
    actionLock.current=true;setBusy(true);setError("");setNotice("");
    try{await capability.saveAttachment(selected,mailID,partID,conversationID);await onFileSaved?.();setNotice(zh?"附件已验证并复制到当前本机对话。":"Attachment verified and copied into the current device conversation.");}
    catch(err){setError(err instanceof Error?err.message:zh?"附件保存失败。":"Attachment save failed.");}
    finally{actionLock.current=false;setBusy(false);}
  }
  async function action(refresh:boolean){if(actionLock.current)return;actionLock.current=true;setBusy(true);setError("");const current=generation.current;const mailbox=selected;
    try{if(refresh){const rows=await capability!.refreshCatalog();setBoxes(rows);if(!rows.some((box)=>box.id===mailbox))setSelected(rows[0]?.id||"");}
      else {const result=await capability!.sync(mailbox);if(current===generation.current){setCache(result);setSynced(true);}}}
    catch(err){setSynced(false);setError(err instanceof Error?err.message:zh?"同步失败。上次完整缓存仍可读取。":"Sync failed. The last complete cache remains available.");}
    finally{actionLock.current=false;setBusy(false);}
  }
  return <section className="mailCachePanel" aria-label={zh?"邮箱缓存":"Mailbox cache"}>
    <h2>{zh?"邮箱":"Mail"}</h2>
    <p>{zh?"邮箱由服务端持续收取。本机保留上次完整同步的邮件；同步不会发送邮件或切换收取设置。":"The backend keeps collecting mail. This device retains the last complete synchronization. Sync does not send mail or change collection settings."}</p>
    <div className="mailCacheControls"><button type="button" disabled={busy} onClick={()=>void action(true)}>{zh?"更新邮箱列表":"Refresh mailboxes"}</button>
      {!!boxes.length&&<><select aria-label={zh?"选择邮箱":"Select mailbox"} disabled={busy} value={selected} onChange={(event)=>{setSelected(event.target.value);setError("");}}>{boxes.map((box)=><option key={box.id} value={box.id}>{box.address} ({box.provider})</option>)}</select>
      <button type="button" disabled={busy||!selected} onClick={()=>void action(false)}>{busy?(zh?"正在同步…":"Syncing…"):(zh?"同步邮件":"Sync mail")}</button></>}
    </div>
    {error&&<p role="alert">{error}</p>}
    {notice&&<p role="status">{notice}</p>}
    {cache?.synced_at&&<p role="status">{synced?(zh?"同步完成":"Synchronized"):(zh?"本机缓存":"Device cache")} · {new Date(cache.synced_at).toLocaleString(language==="zh"?"zh-CN":"en-US")}</p>}
    {!boxes.length&&!busy&&<p>{zh?"点击更新邮箱列表以读取已授权的邮箱。":"Refresh mailboxes to load your authorized mailboxes."}</p>}
    {cache&&!cache.messages.length&&<p>{zh?"此邮箱尚无已缓存的邮件。":"No cached mail in this mailbox yet."}</p>}
    {sendEnabled&&selected&&<ISCPMailDraftPanel key={selected} language={language} mailboxID={selected} address={boxes.find(box=>box.id===selected)?.address||""} attachmentsEnabled={sendAttachmentsEnabled}/>}
    {cache?.messages.map((mail)=><details key={mail.id}><summary>{mail.subject||(zh?"无主题":"No subject")} · {mail.from}</summary>
      {mail.summary&&<p>{mail.summary}</p>}{mail.body_text&&<p style={{whiteSpace:"pre-wrap",overflowWrap:"anywhere"}}>{mail.body_text}</p>}
      {mail.body_truncated&&<p>{zh?"正文超出本机缓存限额，可联网读取原件。":"The body exceeds the cache limit. Read the original while online."}</p>}
      {!!mail.attachments.length&&<><ul>{mail.attachments.map((part)=><li key={part.id}>{part.name} · {new Intl.NumberFormat().format(part.size)} B {zh?"（附件原件保留在服务端）":"(original retained on backend)"}
        {part.available&&capability.saveAttachment&&<button type="button" disabled={busy||!attachmentsEnabled||!conversationID||part.size>64*1024*1024} onClick={()=>void savePart(mail.id,part.id)}>{zh?"复制到本机对话":"Copy to device conversation"}</button>}</li>)}</ul>
        {!conversationID&&<p>{zh?"选择一个本机对话以保存附件副本。":"Select a device conversation to save an attachment copy."}</p>}</>}
    </details>)}
  </section>;
}
