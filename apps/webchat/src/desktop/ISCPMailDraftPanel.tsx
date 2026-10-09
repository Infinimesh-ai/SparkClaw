import { useEffect, useRef, useState } from "react";
import { api, APIError } from "../api/client";
import type { EmailDraft } from "../api/email";
import type { Language } from "../i18n";
import { clientStore, type LocalFile } from "./clientStore";
import { MailAttachmentManifest, WorkspaceMailAttachments } from "./MailDraftAttachments";

const addresses = (value: string) => value.split(/[,;\n]/).map(part => part.trim()).filter(Boolean);
function blank(mailboxID: string): EmailDraft {
  return { id: crypto.randomUUID(), version: 0, mailbox_id: mailboxID, mode: "compose", to: [], cc: [], subject: "", body: "", state: "draft" };
}

// This editor uses only the registered mail draft operations. The saved version
// is the confirmation boundary; an uncertain send stays locked for reconciliation.
export function ISCPMailDraftPanel({ language, mailboxID, address, attachmentsEnabled = false }: { language: Language; mailboxID: string; address: string; attachmentsEnabled?: boolean }) {
  const zh = language === "zh";
  const [draft, setDraft] = useState<EmailDraft>(() => blank(mailboxID));
  const [to, setTo] = useState("");
  const [cc, setCC] = useState("");
  const [attachmentIDs, setAttachmentIDs] = useState<string[]>([]);
  const [pendingFile, setPendingFile] = useState("");
  const [fileRevision, setFileRevision] = useState(0);
  const [localFiles, setLocalFiles] = useState<LocalFile[]>([]);
  const [items, setItems] = useState<EmailDraft[]>([]);
  const [cursor, setCursor] = useState("");
  const [review, setReview] = useState<EmailDraft | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [reloadRequired, setReloadRequired] = useState(false);
  const action = useRef(false);
  const active = useRef(true);
  const locked = !["draft", "failed"].includes(draft.state);
  useEffect(() => {
    active.current = true;
    const controller = new AbortController();
    void api.emailDrafts({ mailbox_id: mailboxID, limit: 50 }, controller.signal).then(page => {
      if (!controller.signal.aborted) { setItems(page.items); setCursor(page.next_cursor || ""); }
    }).catch(reason => { if (!controller.signal.aborted) setError(String(reason.message || reason)); });
    return () => { active.current = false; controller.abort(); };
  }, [mailboxID]);

  useEffect(() => {
    if (!attachmentsEnabled) return;
    let alive = true;
    const store = clientStore();
    const refresh = () => {
      if (!store?.listFiles) { setError(zh ? "本机工作区文件不可用。" : "Local workspace files are unavailable."); return; }
      void store.listFiles().then(files => { if (alive) setLocalFiles(files); }).catch(reason => { if (alive) setError(String(reason.message || reason)); });
    };
    refresh();
    const unsubscribe = store?.onChange?.(refresh);
    return () => { alive = false; unsubscribe?.(); };
  }, [attachmentsEnabled, mailboxID, zh, fileRevision]);

  function remember(value: EmailDraft) {
    if (!active.current) return;
    if (value.mailbox_id !== mailboxID) throw new Error("Mail draft mailbox differs");
    setDraft(value); setTo(value.to.join(", ")); setCC(value.cc.join(", "));
    setAttachmentIDs((value.attachments || []).map(item => item.local_file_id || `legacy:${item.path || item.name}`)); setPendingFile("");
    setReloadRequired(false);
    setItems(rows => [value, ...rows.filter(row => row.id !== value.id)]);
  }
  async function run(work: () => Promise<void>) {
    if (action.current) return;
    action.current = true; setBusy(true); setError(""); setNotice("");
    try { await work(); }
    catch (reason) {
      if (active.current) {
        setError(reason instanceof Error ? reason.message : String(reason));
        if (reason instanceof APIError && reason.code === "email_conflict") { setReview(null); setReloadRequired(true); }
      }
    }
    finally { action.current = false; if (active.current) setBusy(false); }
  }
  function edit(patch: Partial<EmailDraft>) { setDraft(value => ({ ...value, ...patch })); setReview(null); setNotice(""); }
  function addAttachment() {
    const file = localFiles.find(item => item.id === pendingFile);
    if (!file || attachmentIDs.includes(file.id)) return;
    if (attachmentIDs.length >= 5) { setError(zh ? "最多可添加 5 个附件。" : "Choose at most 5 attachments."); return; }
    setAttachmentIDs(ids => [...ids, file.id]); setPendingFile(""); setReview(null); setNotice(""); setError("");
  }
  const canSave = !busy && !locked && !reloadRequired && !attachmentIDs.some(id => id.startsWith("legacy:")) && !pendingFile.trim() && (attachmentsEnabled || !attachmentIDs.length);
  async function save(forReview: boolean) {
    if (!canSave) return;
    await run(async () => {
      const value = await api.saveEmailDraftSnapshot({ id: draft.id, expected_version: draft.version, mailbox_id: mailboxID, mode: draft.mode, reply_mail_id: draft.reply_mail_id, to: addresses(to), cc: addresses(cc), subject: draft.subject, body: draft.body, attachments: attachmentIDs.map(local_file_id => ({ local_file_id })) });
      remember(value);
      if (active.current) {
        setReview(forReview ? structuredClone(value) : null);
        setNotice(zh ? "草稿已保存。" : "Draft saved.");
      }
    });
  }
  async function send() {
    const snapshot = review;
    if (!snapshot || snapshot.id !== draft.id || snapshot.version !== draft.version || locked || (!attachmentsEnabled && snapshot.attachments?.length)) return;
    await run(async () => {
      setReview(null);
      const unknown = { ...snapshot, state: "unknown" as const };
      remember(unknown);
      try { remember(await api.sendEmailDraft(snapshot.id, snapshot.version, crypto.randomUUID())); }
      catch (reason) {
        if (reason instanceof APIError && ["email_conflict", "email_attachment_changed", "email_attachment_invalid"].includes(reason.code)) {
          // These typed errors are guaranteed to precede the server's send
          // intent. Require a fresh read and another saved-version review.
          remember(snapshot); setReloadRequired(true); throw reason;
        }
        // A missing response cannot authorize a new send, even if a later read
        // still shows a draft. The main-process operation journal owns recovery.
        remember(unknown);
        throw reason;
      }
    });
  }
  async function reconcile() {
    await run(async () => { remember(await api.reconcileEmailDraft(draft.id)); });
  }
  const canReview = canSave && !!addresses(to).length && !!draft.subject.trim() && !!draft.body.trim();
  return <section className="emailComposer" aria-label={zh ? "ISCP 邮件草稿" : "ISCP mail drafts"}>
    <h3>{zh ? "草稿与发送" : "Drafts and sending"}</h3>
    <p>{zh ? `发件邮箱：${address}。发送前会展示已保存的邮件版本供你确认。` : `Sending from ${address}. Review the saved message version before sending.`}</p>
    <div><button type="button" disabled={busy} onClick={() => { setDraft(blank(mailboxID)); setTo(""); setCC(""); setAttachmentIDs([]); setPendingFile(""); setReloadRequired(false); setReview(null); setError(""); setNotice(""); }}>{zh ? "新草稿" : "New draft"}</button>
      <select aria-label={zh ? "选择草稿" : "Select draft"} disabled={busy} value={items.some(item => item.id === draft.id) ? draft.id : ""} onChange={event => { const id = event.target.value; if (id) void run(async () => { remember(await api.emailDraftSnapshot(id)); setReview(null); }); }}>
        <option value="">{zh ? "未保存的草稿" : "Unsaved draft"}</option>
        {items.map(item => <option key={item.id} value={item.id}>{item.subject || (zh ? "无主题" : "No subject")} · {item.state}</option>)}
      </select>
      {cursor && <button type="button" disabled={busy} onClick={() => void run(async () => { const page = await api.emailDrafts({ mailbox_id: mailboxID, cursor, limit: 50 }); if (active.current) { setItems(rows => [...rows, ...page.items.filter(item => !rows.some(row => row.id === item.id))]); setCursor(page.next_cursor || ""); } })}>{zh ? "更多草稿" : "More drafts"}</button>}
    </div>
    <fieldset disabled={busy || locked || !!review}>
      <label>{zh ? "收件人" : "To"}<input aria-label={zh ? "收件人" : "To"} value={to} onChange={event => { setTo(event.target.value); setReview(null); setNotice(""); }}/></label>
      <label>{zh ? "抄送" : "CC"}<input aria-label={zh ? "抄送" : "CC"} value={cc} onChange={event => { setCC(event.target.value); setReview(null); setNotice(""); }}/></label>
      <label>{zh ? "主题" : "Subject"}<input aria-label={zh ? "主题" : "Subject"} value={draft.subject} maxLength={998} onChange={event => edit({ subject: event.target.value })}/></label>
      <label>{zh ? "正文" : "Body"}<textarea aria-label={zh ? "正文" : "Body"} value={draft.body} maxLength={262144} rows={6} onChange={event => edit({ body: event.target.value })}/></label>
      {(attachmentsEnabled || attachmentIDs.length > 0) && <WorkspaceMailAttachments language={language} ids={attachmentIDs} onRefresh={() => setFileRevision(value => value + 1)} files={localFiles} saved={draft.attachments || []} pendingFile={pendingFile} onPendingFile={value => { setPendingFile(value); setReview(null); setNotice(""); }} onAdd={addAttachment} onRemove={path => { setAttachmentIDs(paths => paths.filter(item => item !== path)); setReview(null); setNotice(""); }} enabled={attachmentsEnabled}/>}
    </fieldset>
    {review && <section role="region" aria-label={zh ? "发送确认" : "Send confirmation"}>
      <p>{zh ? `确认发送版本 ${review.version}` : `Confirm sending version ${review.version}`}</p>
      <p>{review.to.join(", ")}{review.cc.length ? ` · CC: ${review.cc.join(", ")}` : ""}</p>
      <strong>{review.subject}</strong><pre className="emailOriginalBody">{review.body}</pre>
      <MailAttachmentManifest language={language} attachments={review.attachments || []}/>
      <button type="button" disabled={busy} onClick={() => setReview(null)}>{zh ? "返回修改" : "Edit message"}</button>
      <button type="button" disabled={busy || (!attachmentsEnabled && !!review.attachments?.length)} onClick={() => void send()}>{zh ? "确认发送此版本" : "Confirm sending this version"}</button>
    </section>}
    {!review && !locked && <footer><button type="button" disabled={!canSave} onClick={() => void save(false)}>{zh ? "保存草稿" : "Save draft"}</button><button type="button" disabled={!canReview} onClick={() => void save(true)}>{zh ? "检查并发送" : "Review and send"}</button></footer>}
    {reloadRequired && <p role="status">{zh ? "草稿或附件已变化，此次未发送。请重新读取草稿，再保存并确认当前文件。重新读取会替换未保存的编辑。" : "The draft or its attachments changed; this attempt was not sent. Reload the draft, then save and review the current files. Reloading replaces unsaved edits."} <button type="button" disabled={busy} onClick={() => void run(async () => { remember(await api.emailDraftSnapshot(draft.id)); setReview(null); })}>{zh ? "重新读取草稿" : "Reload draft"}</button></p>}
    {!review && (draft.attachments?.length || 0) > 0 && <MailAttachmentManifest language={language} attachments={draft.attachments || []}/>}
    {["sending", "unknown"].includes(draft.state) && <p role="status">{zh ? "发送结果不确定。请核对原发送记录；此邮件不会自动重发。" : "Send outcome is unknown. Reconcile the original send record; this message will not be sent again automatically."}</p>}
    {draft.state === "sent" && <p role="status">{draft.confirmation_source === "owner_confirmed_capture" ? (zh ? "已根据核对的邮件原件确认发送。" : "Sending was confirmed against the captured mail.") : (zh ? "服务方已确认发送。" : "The provider confirmed sending.")} {draft.receipt?.provider_message_id && <span>{zh ? "服务方回执" : "Provider receipt"}: {draft.receipt.provider_message_id} · {draft.receipt.provider}</span>} {draft.sent_mail_id ? `${zh ? "邮件记录" : "Mail receipt"}: ${draft.sent_mail_id}` : (zh ? "收件证据仍待关联。" : "Delivery evidence has not been linked yet.")}</p>}
    {draft.state === "failed" && <p role="alert">{zh ? "服务方确认发送失败：" : "The provider reported a failed send: "}{draft.error_code}</p>}
    {locked && <button type="button" disabled={busy} onClick={() => void reconcile()}>{zh ? "核对发送结果" : "Reconcile send outcome"}</button>}
    {notice && <p role="status">{notice}</p>}{error && <p role="alert">{error}</p>}
  </section>;
}
