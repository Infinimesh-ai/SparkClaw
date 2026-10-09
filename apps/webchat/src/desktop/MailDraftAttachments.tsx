import type { EmailDraftAttachment } from "../api/email";
import type { Language } from "../i18n";
import type { LocalFile } from "./clientStore";

export function MailAttachmentManifest({ language, attachments }: { language: Language; attachments: EmailDraftAttachment[] }) {
  const zh = language === "zh";
  return <section aria-label={zh ? "已保存的附件" : "Saved attachments"}>
    <h4>{zh ? "附件" : "Attachments"}</h4>
    {attachments.length ? <ul>{attachments.map((item, index) => <li key={item.local_file_id || index}>
      <strong>{item.name}</strong> · {new Intl.NumberFormat().format(item.size_bytes)} B
      <details><summary>{zh ? "文件指纹" : "File fingerprint"}</summary><code>{item.sha256}</code></details>
      {!item.local_file_id && <p>{zh ? "旧附件来源已失效，请移除后从本机工作区重新选择。" : "This old attachment source is unavailable. Remove it and choose a local workspace file."}</p>}
    </li>)}</ul> : <p>{zh ? "无附件" : "No attachments"}</p>}
  </section>;
}

export function WorkspaceMailAttachments({ language, ids, files, saved, onRefresh, pendingFile, onPendingFile, onAdd, onRemove, enabled }: {
  language: Language; ids: string[]; files: LocalFile[]; saved: EmailDraftAttachment[]; pendingFile: string; onPendingFile: (value: string) => void;
  onRefresh: () => void; onAdd: () => void; onRemove: (id: string) => void; enabled: boolean;
}) {
  const zh = language === "zh";
  return <section aria-label={zh ? "工作区附件" : "Workspace attachments"}>
    <h4>{zh ? "工作区附件" : "Workspace attachments"}</h4>
    {enabled ? <>
      <p>{zh ? "仅支持本机 SparkX 工作区内已保存的文件。最多 5 个，共 10 MiB；发送前请确认已保存的附件清单。" : "Choose files already saved in this SparkX workspace on this computer. Up to 5 files, 10 MiB total. Review the saved attachment list before sending."}</p>
      <label>{zh ? "本机工作区文件" : "Local workspace file"}<select aria-label={zh ? "本机工作区文件" : "Local workspace file"} value={pendingFile} onChange={event => onPendingFile(event.target.value)}>
        <option value="">{zh ? "选择已保存的文件" : "Choose a saved file"}</option>
        {files.filter(file => !ids.includes(file.id)).map(file => <option key={file.id} value={file.id}>{file.name} · {new Intl.NumberFormat().format(file.size)} B</option>)}
      </select></label>
      <button type="button" disabled={!files.some(file => file.id === pendingFile) || ids.length >= 5} onClick={onAdd}>{zh ? "添加附件" : "Add attachment"}</button>
      <button type="button" onClick={onRefresh}>{zh ? "刷新本机文件" : "Refresh local files"}</button>
      {!files.length && <p>{zh ? "当前工作区暂无已保存的文件。" : "This workspace has no saved files."}</p>}
      {pendingFile && <p>{zh ? "请添加此文件或清空选择后保存草稿。" : "Add this file or clear the selection before saving the draft."}</p>}
    </> : <p>{zh ? "此连接尚未启用工作区附件发送。可移除附件后发送纯文本邮件。" : "Workspace attachment sending is unavailable on this connection. Remove attachments to send a text message."}</p>}
    {!!ids.length && <ul>{ids.map(id => <li key={id}>{files.find(file => file.id === id)?.name || saved.find(file => file.local_file_id === id)?.name || (zh ? "附件来源不可用" : "Attachment source unavailable")} <button type="button" aria-label={`${zh ? "移除附件" : "Remove attachment"} ${id}`} onClick={() => onRemove(id)}>{zh ? "移除" : "Remove"}</button></li>)}</ul>}
  </section>;
}
