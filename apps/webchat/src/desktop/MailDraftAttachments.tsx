import type { EmailDraftAttachment } from "../api/email";
import type { Language } from "../i18n";

export function MailAttachmentManifest({ language, attachments }: { language: Language; attachments: EmailDraftAttachment[] }) {
  const zh = language === "zh";
  return <section aria-label={zh ? "已保存的附件" : "Saved attachments"}>
    <h4>{zh ? "附件" : "Attachments"}</h4>
    {attachments.length ? <ul>{attachments.map(item => <li key={item.path}>
      <strong>{item.name}</strong> · {item.path} · {new Intl.NumberFormat().format(item.size_bytes)} B
      <details><summary>{zh ? "文件指纹" : "File fingerprint"}</summary><code>{item.sha256}</code></details>
    </li>)}</ul> : <p>{zh ? "无附件" : "No attachments"}</p>}
  </section>;
}

export function WorkspaceMailAttachments({ language, paths, pendingPath, onPendingPath, onAdd, onRemove, enabled }: {
  language: Language; paths: string[]; pendingPath: string; onPendingPath: (value: string) => void;
  onAdd: () => void; onRemove: (path: string) => void; enabled: boolean;
}) {
  const zh = language === "zh";
  return <section aria-label={zh ? "工作区附件" : "Workspace attachments"}>
    <h4>{zh ? "工作区附件" : "Workspace attachments"}</h4>
    {enabled ? <>
      <p>{zh ? "仅支持 Gateway 工作区内的文件。填写相对路径，保存后会核对文件内容；发送前请确认附件清单。" : "Use files in the Gateway workspace. Enter a relative path; saving checks the file contents. Review the attachment list before sending."}</p>
      <label>{zh ? "工作区相对文件路径" : "Workspace relative file path"}<input aria-label={zh ? "工作区相对文件路径" : "Workspace relative file path"} value={pendingPath} onChange={event => onPendingPath(event.target.value)} placeholder="reports/summary.pdf"/></label>
      <button type="button" disabled={!pendingPath.trim()} onClick={onAdd}>{zh ? "添加附件" : "Add attachment"}</button>
      {pendingPath.trim() && <p>{zh ? "请添加此文件或清空路径后保存草稿。" : "Add this file or clear the path before saving the draft."}</p>}
    </> : <p>{zh ? "此连接尚未启用工作区附件发送。可移除附件后发送纯文本邮件。" : "Workspace attachment sending is unavailable on this connection. Remove attachments to send a text message."}</p>}
    {!!paths.length && <ul>{paths.map(path => <li key={path}>{path} <button type="button" aria-label={`${zh ? "移除附件" : "Remove attachment"} ${path}`} onClick={() => onRemove(path)}>{zh ? "移除" : "Remove"}</button></li>)}</ul>}
  </section>;
}
